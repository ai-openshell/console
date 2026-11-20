"""The API comparison as the workflow carries it: in the plan, the pull request, the issue and the command line."""

import contextlib
import io
import json
import os
import shutil
import tempfile
import unittest

import apicoverage
import follow
import plan
import protoapi
import report
from tests import protos, support
from tests.test_apicoverage import ledger_for


def images_for(version):
    return dict(("%s:%s" % (name, version), support.digest(name + version)) for name in ("gateway", "supervisor"))


def plan_for(protos_by_version=None, outage=None, ledger=None):
    """Main on 0.9.0 with 0.9.1 released upstream."""
    data = support.upstream_data(tags=["v0.9.0", "v0.9.1"], images=images_for("0.9.1"), outage=outage)
    if protos_by_version is not None:
        data["protos"] = protos_by_version
    return plan.build(support.pins_doc(release="0.9.0"), support.FakeUpstream(data), ledger=ledger)


class InThePlan(unittest.TestCase):
    def test_the_plan_carries_what_is_new_between_the_pin_and_the_target(self):
        api = plan_for(protos.by_version())["api"]
        self.assertEqual((api["from"], api["to"], api["error"]), ("0.9.0", "0.9.1", None))
        self.assertEqual(
            [item["rpc"] for item in api["changes"]["rpcs_added"]], ["OpenShell.ResumeSandbox", "OpenShell.SuspendSandbox"]
        )
        # It is handed from job to job as JSON.
        self.assertEqual(json.loads(json.dumps(api)), api)

    def test_no_target_no_comparison(self):
        data = support.upstream_data(tags=["v0.9.0", "v0.9.1"])
        data["protos"] = protos.by_version()
        made = plan.build(support.pins_doc(release="0.9.1"), support.FakeUpstream(data))
        self.assertIsNone(made["target"])
        self.assertNotIn("api", made)

    def test_protos_that_cannot_be_fetched_do_not_stop_the_plan(self):
        made = plan_for(protos.by_version(), outage=["protos"])
        self.assertTrue(made["target"]["bump"])
        self.assertIn("reading the protos of v0.9.0 failed", made["api"]["error"])
        self.assertIsNone(made["api"]["changes"])

    def test_a_proto_it_cannot_parse_does_not_stop_the_plan(self):
        broken = protos.by_version()
        broken["0.9.1"]["openshell.proto"] += "\nmessage Odd { group G = 1 { } }\n"
        made = plan_for(broken)
        self.assertTrue(made["target"]["ready"])
        self.assertIn("openshell.proto:", made["api"]["error"])

    def test_the_ledger_marks_what_the_dashboard_already_has(self):
        ledger = ledger_for(protoapi.surface(protoapi.load(protos.files(protos.NEW).get)))
        api = plan_for(protos.by_version(), ledger=ledger)["api"]
        self.assertTrue(all(item["covered"] for item in api["changes"]["rpcs_added"]))


class InTheReports(unittest.TestCase):
    def setUp(self):
        self.plan = plan_for(protos.by_version())

    def test_the_pull_request_names_suspend_and_resume(self):
        body = report.pr_body(self.plan)
        self.assertIn("## What is new in the gateway's API", body)
        self.assertIn("| `SuspendSandbox` | workspace user (`sandbox:write`) |", body)
        self.assertIn("`SANDBOX_PHASE_SUSPENDED`", body)
        self.assertIn("Nothing above has to be done for this move to merge.", body)
        self.assertLess(body.index("**After it merges,**"), body.index("## What is new"))

    def test_the_run_summary_and_the_issue_carry_it_too(self):
        self.assertIn("`ResumeSandbox`", report.plan_summary(self.plan))
        _, text = report.issue_text(self.plan)
        self.assertIn("### What is new in the gateway's API", text)
        self.assertIn("`ResumeSandbox`", text)

    def test_a_comparison_that_failed_says_so_and_how_to_run_it(self):
        failed = plan_for(protos.by_version(), outage=["protos"])
        body = report.pr_body(failed)
        self.assertIn("**Not compared.**", body)
        self.assertIn("follow.py api-diff --from 0.9.0 --to 0.9.1", body)

    def test_a_plan_from_before_the_comparison_reads_as_before(self):
        del self.plan["api"]
        self.assertNotIn("What is new", report.pr_body(self.plan))
        self.assertNotIn("What is new", report.plan_summary(self.plan))

    def test_the_pull_request_table_is_cut_and_the_summary_is_not(self):
        rows = self.plan["api"]["changes"]["enum_values_added"]
        rows.extend(dict(rows[0], value="SANDBOX_PHASE_EXTRA_%d" % number) for number in range(60))
        self.assertIn("…and 22 more.", report.pr_body(self.plan))
        self.assertIn("SANDBOX_PHASE_EXTRA_59", report.plan_summary(self.plan))


class CommandLine(unittest.TestCase):
    def setUp(self):
        self.dir = tempfile.mkdtemp(prefix="upstream-api-")
        self.addCleanup(shutil.rmtree, self.dir, ignore_errors=True)
        self.repo = support.proto_repo(
            os.path.join(self.dir, "upstream"),
            [(protos.OLD_VERSION, protos.files(protos.OLD)), (protos.NEW_VERSION, protos.files(protos.NEW))],
        )
        self.ledger = ledger_for(protoapi.surface(protoapi.load(protos.files(protos.OLD).get)))
        self.ledger_path = self.write("api-coverage.json", json.dumps(self.ledger))
        markers = [mark(block) for block in apicoverage.BLOCKS for mark in (apicoverage.begin, apicoverage.end)]
        self.doc_path = self.write("api-coverage.md", "# Coverage\n\n%s\n\nBy hand.\n" % "\n".join(markers))
        self.pins_path = self.write("pins.json", json.dumps(support.pins_doc(release=protos.OLD_VERSION)))
        os.makedirs(os.path.join(self.dir, "root", "frontend", "src"))

    def write(self, name, text):
        path = os.path.join(self.dir, name)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)
        return path

    def run_cli(self, *argv):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = follow.main(list(argv))
        return code, out.getvalue(), err.getvalue()

    def coverage(self, *more):
        return self.run_cli("api-coverage", "--ledger", self.ledger_path, "--doc", self.doc_path, "--pins", self.pins_path, *more)

    def test_api_diff_reads_two_tags_of_a_clone(self):
        out_path = os.path.join(self.dir, "report.md")
        code, out, _ = self.run_cli(
            "api-diff", "--from", "0.9.0", "--to", "0.9.1", "--repo", self.repo,
            "--ledger", self.ledger_path, "--root", os.path.join(self.dir, "root"), "--out", out_path,
        )  # fmt: skip
        self.assertEqual(code, 0)
        self.assertIn("| `SuspendSandbox` |", out)
        with open(out_path, encoding="utf-8") as fh:
            self.assertEqual(fh.read() + "\n", out)

    def test_api_diff_of_a_tag_that_does_not_exist_fails(self):
        code, _, err = self.run_cli("api-diff", "--from", "0.9.0", "--to", "9.9.9", "--repo", self.repo, "--root", self.dir)
        self.assertEqual(code, 1)
        self.assertIn("openshell.proto: not found", err)

    def test_api_coverage_fails_until_the_document_is_written_from_the_ledger(self):
        code, _, err = self.coverage()
        self.assertEqual(code, 1)
        self.assertIn("api-coverage --write", err)
        self.assertEqual(self.coverage("--write")[0], 0)
        self.assertEqual(self.coverage("--doc", self.pins_path)[0], 1)  # a file without the markers
        code, out, _ = self.coverage()
        self.assertEqual(code, 0)
        self.assertIn("OpenShell 0.9.0, 6 RPCs (0 not offered)", out)
        with open(self.doc_path, encoding="utf-8") as fh:
            document = fh.read()
        self.assertIn("## Everything", document)
        self.assertTrue(document.endswith("\n\nBy hand.\n"))

    def test_api_coverage_against_the_pinned_release(self):
        self.coverage("--write")
        code, out, _ = self.coverage("--repo", self.repo)
        self.assertEqual(code, 0)
        self.assertIn("covers every user-facing RPC and request field of OpenShell 0.9.0", out)

    def test_after_a_pin_move_it_names_what_nobody_has_classified(self):
        self.coverage("--write")
        self.write("pins.json", json.dumps(support.pins_doc(release=protos.NEW_VERSION)))
        code, _, err = self.coverage("--repo", self.repo)
        self.assertEqual(code, 1)
        self.assertIn("the ledger is for OpenShell 0.9.0 and this branch pins 0.9.1", err)
        self.assertIn("OpenShell.SuspendSandbox", err)
        self.assertIn("openshell.v1.SandboxSpec.idle_timeout", err)

    def test_a_malformed_ledger_is_refused_before_anything_is_written(self):
        self.ledger["rpcs"]["OpenShell.Health"]["ui"] = "maybe"
        self.write("api-coverage.json", json.dumps(self.ledger))
        code, _, err = self.coverage("--write")
        self.assertEqual(code, 1)
        self.assertIn("OpenShell.Health: ui is 'maybe'", err)


if __name__ == "__main__":
    unittest.main()
