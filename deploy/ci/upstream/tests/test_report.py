"""What the workflow writes for a person: the commit, the pull request, the issue."""

import re
import unittest

import nextbranch
import plan as plan_module
import report
from tests import support

REPO_URL = "https://github.com/ai-openshell/console"
RUN_URL = REPO_URL + "/actions/runs/42"


def images_for(version):
    return {"gateway:" + version: support.digest("g" + version), "supervisor:" + version: support.digest("s" + version)}


def plan_with(*tags):
    images = {}
    for tag in tags:
        images.update(images_for(tag[1:]))
    return plan_module.build(
        support.pins_of_2026_10_09(), support.FakeUpstream(support.upstream_data(tags=list(tags), images=images))
    )


TODAY = plan_with()  # target 0.1.4-pre.2
STABLE = plan_with("v0.1.4")
NEW_LINE = plan_with("v0.2.0-pre.1")
NEW_LINE_STABLE = plan_with("v0.2.0")
NOTHING = plan_module.build(
    support.pins_of_2026_10_09(),
    support.FakeUpstream(support.upstream_data(drop=("v0.1.4-pre.1", "v0.1.4-pre.2"))),
)


class CommitMessage(unittest.TestCase):
    def message(self, made):
        return report.commit_message(made["target"], made["pinned"])

    def test_the_title_is_a_fix_that_names_the_release(self):
        self.assertTrue(self.message(TODAY).startswith("fix: move to OpenShell 0.1.4-pre.2\n\n"))
        self.assertEqual(report.pr_title("0.1.4"), "fix: move to OpenShell 0.1.4")

    def test_it_ends_with_the_trailer_the_commit_is_found_by(self):
        text = self.message(TODAY)
        self.assertTrue(text.endswith("\n\nOpenShell-Release: 0.1.4-pre.2\n"))
        self.assertEqual(nextbranch.TRAILER_RE.search(text).group(1), "0.1.4-pre.2")

    def test_it_says_where_from_where_to_and_which_commit(self):
        text = " ".join(self.message(TODAY).split())
        self.assertIn("from 0.1.3 to 0.1.4-pre.2", text)
        self.assertIn("upstream tagged v0.1.4-pre.2 (4c1b16a4a104)", text)

    def test_a_pre_release_says_it_cannot_merge_and_a_stable_release_does_not(self):
        self.assertIn("pre-release", self.message(TODAY))
        self.assertNotIn("pre-release", self.message(STABLE))

    def test_a_new_line_says_the_built_in_line_moves(self):
        self.assertIn("starts gateway release line 0.2", self.message(NEW_LINE_STABLE))
        self.assertNotIn("starts gateway release line", self.message(STABLE))

    def test_the_body_is_wrapped_and_versions_stay_whole(self):
        for made in (TODAY, STABLE, NEW_LINE, NEW_LINE_STABLE):
            for text in self.message(made).splitlines():
                self.assertLessEqual(len(text), 72, text)
        self.assertNotIn("pre.\n2", self.message(TODAY))


class CheckOutcome(unittest.TestCase):
    def test_the_first_step_that_did_not_succeed_names_what_happened(self):
        cases = {
            ("success", "success", "success", "success"): report.PASSED,
            ("success", "success", "success", "failure"): report.FAILED,
            ("failure", "skipped", "skipped", "skipped"): report.PULL_FAILED,
            ("success", "failure", "skipped", "skipped"): report.STACK_FAILED,
            ("success", "success", "failure", "skipped"): report.BFF_FAILED,
            # Cancelled, or a step outcome nobody expected: nothing is known.
            ("success", "success", "success", "cancelled"): report.NO_RESULT,
            ("success", "success", "success", "skipped"): report.NO_RESULT,
            ("", "", "", ""): report.PULL_FAILED,
        }
        for steps, expected in cases.items():
            self.assertEqual(report.check_outcome(*steps), expected, steps)

    def test_only_a_failing_suite_is_a_compatibility_result(self):
        for steps in (("failure", "skipped", "skipped", "skipped"), ("success", "failure", "skipped", "skipped")):
            self.assertNotEqual(report.check_outcome(*steps), report.FAILED)


class CheckDecision(unittest.TestCase):
    def decide(self, made, outcome=None):
        return report.check_decision(made, {"outcome": outcome} if outcome else None)

    def test_a_pass_closes_the_issue(self):
        decision = self.decide(TODAY, report.PASSED)
        self.assertEqual((decision["issue"], decision["incomplete"]), ("close", False))

    def test_a_failure_writes_it_and_is_not_a_red_run(self):
        decision = self.decide(TODAY, report.FAILED)
        self.assertEqual((decision["issue"], decision["incomplete"]), ("upsert", False))
        self.assertIn("FAILS", decision["why"])

    def test_unknown_is_never_a_pass_or_a_failure(self):
        for outcome in (report.PULL_FAILED, report.STACK_FAILED, report.BFF_FAILED, report.NO_RESULT, "something-new", None):
            decision = self.decide(TODAY, outcome)
            self.assertEqual((decision["issue"], decision["incomplete"]), ("keep", True), outcome)
            self.assertIn("could not be run", decision["why"])

    def test_nothing_ahead_closes_an_issue_about_a_release_main_has_moved_to(self):
        decision = self.decide(NOTHING)
        self.assertEqual((decision["issue"], decision["incomplete"]), ("close", False))

    def test_a_target_whose_images_are_not_out_is_neither_and_not_red(self):
        waiting = plan_module.build(
            support.pins_of_2026_10_09(), support.FakeUpstream(support.upstream_data(tags=["v0.1.4"]))
        )
        decision = self.decide(waiting)
        self.assertEqual((decision["issue"], decision["incomplete"]), ("keep", False))
        self.assertIn("not published", decision["why"])


class Issue(unittest.TestCase):
    def text(self, made):
        return report.issue_text(made, RUN_URL, REPO_URL)

    def test_the_title_names_the_release(self):
        self.assertEqual(self.text(TODAY)[0], "OpenShell 0.1.4-pre.2: main fails the compatibility suite")

    def test_the_body_says_what_was_tested_and_links_the_run(self):
        body = self.text(TODAY)[1]
        self.assertTrue(body.startswith(report.ISSUE_MARKER + "\n"))
        self.assertIn("The BFF built from `main` (OpenShell `0.1.3`, SDK `v0.0.0-20261009050449-e1f3c82caa3e`)", body)
        self.assertIn(TODAY["target"]["gateway_image"], body)
        self.assertIn("[this run](%s)" % RUN_URL, body)
        self.assertIn("https://github.com/NVIDIA/OpenShell/releases/tag/v0.1.4-pre.2", body)

    def test_links_are_absolute_because_an_issue_is_not_a_rendered_file(self):
        body = self.text(TODAY)[1]
        for target in re.findall(r"\]\(([^)]+)\)", body):
            self.assertTrue(target.startswith("https://"), target)
        self.assertIn(REPO_URL + "/blob/main/docs/adrs/0005-gateway-compatibility-and-releases.md", body)

    def test_a_stable_release_on_mains_own_line_is_a_broken_stable_interface_to_report_upstream(self):
        body = self.text(STABLE)[1]
        self.assertIn("**Upstream broke a Stable interface inside a minor release line**", body)
        self.assertIn("**Report it upstream**, at https://github.com/NVIDIA/OpenShell/issues", body)
        self.assertIn("the console stays on `0.1.3`", body)

    def test_a_pre_release_on_that_line_can_still_be_fixed_and_says_to_report_it_now(self):
        body = self.text(TODAY)[1]
        self.assertIn("It is a pre-release, so there is still time", body)
        self.assertIn("**report it upstream now**", body)
        self.assertNotIn("Upstream broke a Stable interface", body)

    def test_a_new_line_is_not_called_a_broken_promise(self):
        for made in (NEW_LINE, NEW_LINE_STABLE):
            body = self.text(made)[1]
            self.assertIn("is on release line `0.2.x`; `main` is for `0.1.x`", body)
            self.assertNotIn("Report it upstream", body)
            self.assertNotIn("report it upstream now", body)
            self.assertIn("Do the work on `next`", body)


class Summaries(unittest.TestCase):
    def test_the_plan_says_what_is_pinned_and_what_is_ahead(self):
        text = report.plan_summary(TODAY)
        self.assertIn("pins OpenShell `0.1.3`", text)
        self.assertIn("Target: OpenShell `0.1.4-pre.2`, a pre-release", text)
        self.assertIn(TODAY["target"]["supervisor_image"], text)
        self.assertNotIn("dry run", text)
        self.assertIn("dry run", report.plan_summary(TODAY, dry_run=True))

    def test_nothing_ahead(self):
        self.assertIn("Upstream has no release ahead of it (newest stable release: `0.1.3`)", report.plan_summary(NOTHING))

    def test_a_new_line_and_skipped_releases_are_mentioned(self):
        self.assertIn("starts release line `0.2.x`", report.plan_summary(NEW_LINE))
        skipping = plan_with("v0.1.4", "v0.1.5")
        self.assertIn("Skipped on the way: `0.1.4`.", report.plan_summary(skipping))

    def test_an_sdk_pin_that_is_not_on_the_release_tag_is_called_out(self):
        doc = dict(support.pins_of_2026_10_09(), sdk="v0.0.0-20261001000000-0123456789ab")
        made = plan_module.build(doc, support.FakeUpstream(support.upstream_data()))
        self.assertIn("The SDK pin is not the commit upstream tagged `v0.1.3`", report.plan_summary(made))
        self.assertNotIn("SDK pin is not", report.plan_summary(TODAY))

    def test_a_target_that_is_not_ready_says_what_it_waits_for(self):
        waiting = plan_module.build(
            support.pins_of_2026_10_09(), support.FakeUpstream(support.upstream_data(tags=["v0.1.4"]))
        )
        self.assertIn("**Not yet:** upstream has tagged v0.1.4 but has not published its gateway image yet", report.plan_summary(waiting))

    def test_check_a_says_what_happens_to_the_issue(self):
        failed = report.check_decision(TODAY, {"outcome": report.FAILED})
        self.assertIn("The issue is written, or rewritten in place.", report.check_summary(TODAY, failed))
        self.assertIn("not written in a dry run", report.check_summary(TODAY, failed, dry_run=True))
        passed = report.check_decision(TODAY, {"outcome": report.PASSED})
        self.assertIn("`main` passes the compatibility suite", report.check_summary(TODAY, passed))
        unknown = report.check_decision(TODAY, None)
        self.assertIn("left as it is", report.check_summary(TODAY, unknown))


class NextSummary(unittest.TestCase):
    def outcome(self, action, **more):
        base = {"action": action, "reason": "the pin moves to OpenShell 0.1.4-pre.2", "main": "a" * 40, "next": None, "sha": None, "regenerated": [], "conflict": None}
        base.update(more)
        return base

    def test_each_action_reads_as_what_happened(self):
        empty = {"pr": None}
        self.assertIn("`next` created from `main` at `aaaaaaaaaaaa`", report.next_summary(self.outcome("create", sha="c" * 40), empty))
        self.assertIn("force-pushed (with lease): the pin moves", report.next_summary(self.outcome("update", sha="c" * 40), empty))
        self.assertIn("left as it is", report.next_summary(self.outcome("none"), empty))
        self.assertIn("deleted", report.next_summary(self.outcome("delete"), empty))
        conflict = self.outcome("conflict", reason="next could not be rebased onto main", conflict={"files": ["b.txt", "a.txt"], "stage": "rebase"})
        self.assertIn("In conflict: `a.txt`, `b.txt`. `next` is left exactly as it was.", report.next_summary(conflict, empty))

    def test_a_dry_run_says_would(self):
        publish = {"pr": {"number": None, "title": "fix: move to OpenShell 0.1.4-pre.2", "draft": True}}
        text = report.next_summary(self.outcome("create", sha="c" * 40), publish, dry_run=True)
        self.assertIn("`next` would be created", text)
        self.assertIn("A draft pull request `next` -> `main` would be opened: `fix: move to OpenShell 0.1.4-pre.2`", text)

    def test_regenerated_lock_files_and_the_release_branch_are_listed(self):
        publish = {"pr": None, "release_branch": {"name": "release/0.1", "sha": "a" * 40}}
        text = report.next_summary(self.outcome("update", sha="c" * 40, regenerated=["backend/go.sum", "backend/go.sum"]), publish)
        self.assertIn("Regenerated after a conflict: `backend/go.sum`.", text)
        self.assertIn("`release/0.1` created from `main` at `aaaaaaaaaaaa`", text)


if __name__ == "__main__":
    unittest.main()
