"""The coverage ledger: its shape, what it leaves unclassified, and the document made from it."""

import copy
import os
import unittest

import apicoverage
import protoapi
from tests import protos, support

LEDGER = os.path.join(support.REPO_ROOT, "deploy", "ci", "upstream", "api-coverage.json")
DOCUMENT = os.path.join(support.REPO_ROOT, "docs", "api-coverage.md")


def surface(text):
    return protoapi.surface(protoapi.load(protos.files(text).get))


def entry(ui, **more):
    made = {"sdk": "yes", "bff": "yes", "ui": ui, "where": "frontend/src/pages/X.tsx:1", "note": ""}
    made.update(more)
    return made


def ledger_for(made):
    """A ledger that classifies everything in a surface as offered in a form."""
    wanted = apicoverage.request_fields(made)
    return {
        "release": protos.OLD_VERSION,
        "groups": [{"id": "all", "title": "Everything", "rpcs": sorted(made["rpcs"])}],
        "messages": dict((key.rsplit(".", 1)[0], "all") for key in wanted),
        "rpcs": dict((key, entry("action", sdk="Sandboxes().X", bff="POST /x")) for key in made["rpcs"]),
        "fields": dict((key, entry("plumbing" if key.endswith("workspace_scope") else "form")) for key in wanted),
    }


class Shape(unittest.TestCase):
    def setUp(self):
        self.ledger = ledger_for(surface(protos.OLD))

    def test_a_complete_ledger_has_no_problems(self):
        self.assertEqual(apicoverage.problems(self.ledger), [])

    def test_a_missing_section_is_the_only_thing_reported(self):
        del self.ledger["fields"]
        self.assertEqual(apicoverage.problems(self.ledger), ["the ledger has no 'fields'"])

    def test_an_unknown_ui_value_is_refused(self):
        self.ledger["rpcs"]["OpenShell.Health"]["ui"] = "sort of"
        self.assertIn("OpenShell.Health: ui is 'sort of'", apicoverage.problems(self.ledger)[0])

    def test_an_rpc_outside_every_group_is_refused(self):
        self.ledger["groups"][0]["rpcs"].remove("OpenShell.Health")
        self.assertEqual(apicoverage.problems(self.ledger), ["OpenShell.Health is in no group"])

    def test_a_field_whose_message_is_in_no_group_is_refused(self):
        del self.ledger["messages"]["openshell.v1.SandboxSpec"]
        self.assertTrue(all("its message is in no group" in problem for problem in apicoverage.problems(self.ledger)))

    def test_a_field_says_yes_or_no_for_the_sdk_and_the_bff(self):
        self.ledger["fields"]["openshell.v1.SandboxSpec.image"]["sdk"] = "Sandboxes().Create"
        self.assertIn("sdk is 'Sandboxes().Create'", apicoverage.problems(self.ledger)[0])


class Against(unittest.TestCase):
    def test_a_ledger_made_for_a_release_covers_it(self):
        made = surface(protos.OLD)
        self.assertEqual(
            apicoverage.against(ledger_for(made), made),
            {"unclassified_rpcs": [], "unclassified_fields": [], "stale_rpcs": [], "stale_fields": []},
        )

    def test_the_next_release_leaves_what_it_added_unclassified_and_what_it_removed_stale(self):
        found = apicoverage.against(ledger_for(surface(protos.OLD)), surface(protos.NEW))
        self.assertEqual(found["unclassified_rpcs"], ["OpenShell.ResumeSandbox", "OpenShell.SuspendSandbox"])
        self.assertIn("openshell.v1.SandboxSpec.idle_timeout", found["unclassified_fields"])
        self.assertIn("openshell.v1.ResumeSandboxRequest.restart_if_restore_fails", found["unclassified_fields"])
        # Only what a client can send is the ledger's business.
        self.assertNotIn("openshell.v1.Sandbox.reason", found["unclassified_fields"])
        self.assertEqual(found["stale_rpcs"], ["OpenShell.LegacyThing"])
        self.assertIn("openshell.v1.SandboxSpec.command", found["stale_fields"])
        report = "\n".join(apicoverage.unclassified_report(found, protos.NEW_VERSION))
        self.assertIn("2 RPCs the ledger does not list:\n  OpenShell.ResumeSandbox", report)


class Matrix(unittest.TestCase):
    def setUp(self):
        self.ledger = ledger_for(surface(protos.OLD))
        self.ledger["rpcs"]["OpenShell.LegacyThing"] = entry("none", sdk="none", bff="none", where="", note="Nobody asked.")
        self.ledger["fields"]["openshell.v1.SandboxSpec.token"] = entry("document", note="YAML | JSON only.")
        self.text = apicoverage.matrix(self.ledger)

    def test_counts_leave_plumbing_out_of_the_share(self):
        made = apicoverage.counts(self.ledger)
        self.assertEqual(made["rpcs"]["none"], 1)
        self.assertEqual(made["fields"]["plumbing"], 3)
        text = apicoverage.summary(self.ledger)
        self.assertIn("has nothing for **1**", text)
        self.assertIn("3 are plumbing", text)

    def test_an_rpc_row_says_where_and_why(self):
        self.assertIn("| `LegacyThing` | none | none | **not offered** |  | Nobody asked. |", self.text)

    def test_a_message_with_something_to_author_gets_a_table(self):
        self.assertIn("### `SandboxSpec`", self.text)
        self.assertIn("| `token` | yes | yes | document only | `frontend/src/pages/X.tsx:1` | YAML \\| JSON only. |", self.text)

    def test_requests_that_are_all_plumbing_are_one_line(self):
        self.ledger["fields"]["openshell.v1.StopSandboxRequest.name"]["ui"] = "plumbing"
        text = apicoverage.matrix(self.ledger)
        self.assertNotIn("### `StopSandboxRequest`", text)
        self.assertIn("Requests with nothing for a person to author: `StopSandboxRequest` (`name`, `workspace_scope`)", text)

    def test_splice_replaces_what_is_between_the_markers_and_nothing_else(self):
        first, last = apicoverage.begin("matrix"), apicoverage.end("matrix")
        document = "# Title\n\nwritten by hand\n\n%s\n\nold\n\n%s\n\nafter\n" % (first, last)
        made = apicoverage.splice(document, "matrix", "new\n")
        self.assertEqual(made, "# Title\n\nwritten by hand\n\n%s\n\nnew\n\n%s\n\nafter\n" % (first, last))
        self.assertEqual(apicoverage.splice(made, "matrix", "new\n"), made)
        with self.assertRaises(ValueError):
            apicoverage.splice(made, "summary", "new")

    def test_the_document_has_the_counts_above_and_the_tables_below(self):
        document = "\n".join(
            [apicoverage.begin("summary"), apicoverage.end("summary"), "by hand", apicoverage.begin("matrix"), apicoverage.end("matrix"), ""]
        )
        made = apicoverage.write_into(document, self.ledger)
        self.assertLess(made.index("has nothing for **1**"), made.index("by hand"))
        self.assertLess(made.index("by hand"), made.index("## Everything"))


class CheckedIn(unittest.TestCase):
    """The ledger and the document in this repository."""

    def setUp(self):
        self.ledger = apicoverage.load(LEDGER)

    def test_the_ledger_is_well_formed(self):
        self.assertEqual(apicoverage.problems(self.ledger), [])

    def test_every_place_it_points_at_exists(self):
        missing = []
        for kind in ("rpcs", "fields"):
            for key, made in self.ledger[kind].items():
                path = made["where"].split(":")[0]
                if path and not os.path.exists(os.path.join(support.REPO_ROOT, path)):
                    missing.append("%s: %s" % (key, made["where"]))
        self.assertEqual(missing, [])

    def test_the_document_is_what_the_ledger_says(self):
        with open(DOCUMENT, encoding="utf-8") as fh:
            document = fh.read()
        self.assertEqual(
            apicoverage.write_into(document, self.ledger),
            document,
            "docs/api-coverage.md is stale: run python3 deploy/ci/upstream/follow.py api-coverage --write",
        )


if __name__ == "__main__":
    unittest.main()
