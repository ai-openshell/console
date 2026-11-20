"""What changed in the gateway's API between two releases, and how it is reported."""

import os
import shutil
import tempfile
import unittest

import apidiff
import protoapi
from tests import protos


def surface(text):
    return protoapi.surface(protoapi.load(protos.files(text).get))


def changes():
    return apidiff.diff(surface(protos.OLD), surface(protos.NEW))


def names(items, key):
    return [item[key] for item in items]


class Diff(unittest.TestCase):
    def setUp(self):
        self.changes = changes()

    def test_the_same_release_twice_is_no_change(self):
        same = apidiff.diff(surface(protos.OLD), surface(protos.OLD))
        self.assertTrue(apidiff.is_empty(same))
        self.assertIn("is the same", apidiff.headline(same, "0.9.0", "0.9.0"))

    def test_new_rpcs_come_with_who_may_call_them_and_what_they_do(self):
        added = dict((item["rpc"], item) for item in self.changes["rpcs_added"])
        self.assertEqual(sorted(added), ["OpenShell.ResumeSandbox", "OpenShell.SuspendSandbox"])
        suspend = added["OpenShell.SuspendSandbox"]
        self.assertEqual(suspend["who"], "workspace user (`sandbox:write`)")
        self.assertEqual(suspend["doc"], "Freeze a ready sandbox and retain its memory state for a later resume.")

    def test_an_rpc_only_a_peer_calls_is_listed_apart(self):
        self.assertEqual(self.changes["internal_added"], ["OpenShell.PeerNotify"])
        self.assertNotIn("OpenShell.PeerNotify", names(self.changes["rpcs_added"], "rpc"))

    def test_a_removed_rpc_and_a_changed_rule_are_named(self):
        self.assertEqual(names(self.changes["rpcs_removed"], "rpc"), ["OpenShell.LegacyThing"])
        changed = dict((item["rpc"], item["changes"]) for item in self.changes["rpcs_changed"])
        self.assertEqual(list(changed), ["OpenShell.StopSandbox"])
        self.assertIn("workspace admin", changed["OpenShell.StopSandbox"][0])

    def test_a_new_field_says_whether_a_client_can_send_it(self):
        added = dict((item["field"], item) for item in self.changes["fields_added"])
        self.assertEqual(sorted(added), ["openshell.v1.Sandbox.reason", "openshell.v1.SandboxSpec.idle_timeout"])
        self.assertIn("request", added["openshell.v1.SandboxSpec.idle_timeout"]["sides"])
        self.assertEqual(added["openshell.v1.Sandbox.reason"]["sides"], ["response"])
        self.assertIn("CreateSandbox", added["openshell.v1.SandboxSpec.idle_timeout"]["used_by"])

    def test_a_field_on_a_message_only_a_sandbox_sends_is_not_reported(self):
        self.assertNotIn("more_internal_detail", " ".join(names(self.changes["fields_added"], "field")))

    def test_the_fields_of_a_new_message_are_listed_with_it_not_one_by_one(self):
        added = dict((item["message"], item) for item in self.changes["messages_added"])
        self.assertEqual(
            added["openshell.v1.ResumeSandboxRequest"]["fields"], ["name", "restart_if_restore_fails", "workspace_scope"]
        )
        self.assertNotIn("openshell.v1.ResumeSandboxRequest.name", names(self.changes["fields_added"], "field"))

    def test_a_rename_a_type_change_and_a_removal_are_told_apart(self):
        changed = dict((item["field"], item["changes"]) for item in self.changes["fields_changed"])
        self.assertIn("renamed from `log_level`", changed["openshell.v1.SandboxSpec.verbosity"][0])
        self.assertEqual(changed["openshell.v1.SandboxSpec.replicas"], ["type `int32` is now `int64`"])
        self.assertEqual(names(self.changes["fields_removed"], "field"), ["openshell.v1.SandboxSpec.command"])

    def test_new_enum_values_say_where_the_enum_is_used(self):
        added = self.changes["enum_values_added"]
        self.assertEqual(names(added, "value"), ["SANDBOX_PHASE_SUSPENDING", "SANDBOX_PHASE_SUSPENDED"])
        self.assertEqual(added[0]["used_in"], ["Sandbox.phase"])
        self.assertEqual(names(self.changes["enum_values_removed"], "value"), ["SANDBOX_PHASE_GONE"])


class Source(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp(prefix="dashboard-src-")
        self.addCleanup(shutil.rmtree, self.root, ignore_errors=True)
        self.write("frontend/src/types/index.ts", "export type Phase = 'Ready' | 'Suspended';\n")
        self.write("frontend/src/pages/__tests__/Sandbox.spec.tsx", "idleTimeout suspendSandbox\n")
        self.write("frontend/src/pages/Sandbox.spec.tsx", "idleTimeout\n")
        self.write("backend/pkg/handlers/sandbox_handler.go", "h.svc.Suspend(ctx)\n// SuspendSandbox\n")
        self.write("backend/pkg/handlers/sandbox_handler_test.go", "IdleTimeout\n")
        self.source = apidiff.Source(self.root)

    def write(self, path, text):
        full = os.path.join(self.root, path)
        os.makedirs(os.path.dirname(full), exist_ok=True)
        with open(full, "w", encoding="utf-8") as fh:
            fh.write(text)

    def test_tests_do_not_count(self):
        self.assertEqual(self.source.mentions("idleTimeout", "IdleTimeout"), [])

    def test_whole_words_only(self):
        self.assertEqual(self.source.mentions("Suspend"), ["backend/pkg/handlers/sandbox_handler.go"])
        self.assertEqual(self.source.mentions("Suspen"), [])

    def test_a_generated_getter_is_not_a_call_of_the_rpc_of_that_name(self):
        self.write("backend/pkg/models/secret.go", "value := item.GetSecret()\n")
        source = apidiff.Source(self.root)
        self.assertEqual(source.mentions("GetSecret", calls=True), [])
        self.assertEqual(source.mentions("GetSecret"), ["backend/pkg/models/secret.go"])
        self.write("backend/pkg/clients/rawsecret.go", "return r.client.GetSecret(ctx, req)\n")
        self.assertEqual(apidiff.Source(self.root).mentions("GetSecret", calls=True), ["backend/pkg/clients/rawsecret.go"])

    def test_an_enum_value_is_looked_for_in_every_spelling(self):
        made = apidiff.annotate(changes(), {}, self.source)
        by_value = dict((item["value"], item["dashboard"]) for item in made["enum_values_added"])
        self.assertIn("named in the UI", by_value["SANDBOX_PHASE_SUSPENDED"])
        self.assertIn("not named anywhere", by_value["SANDBOX_PHASE_SUSPENDING"])


class Annotate(unittest.TestCase):
    def test_without_a_ledger_a_new_rpc_is_not_covered(self):
        made = apidiff.annotate(changes())
        suspend = [item for item in made["rpcs_added"] if item["rpc"] == "OpenShell.SuspendSandbox"][0]
        self.assertFalse(suspend["covered"])
        self.assertIn("**not covered**", suspend["dashboard"])

    def test_the_ledger_is_believed_over_the_search(self):
        ledger = {
            "rpcs": {
                "OpenShell.SuspendSandbox": {"ui": "action", "where": "frontend/src/pages/SandboxDetailPage.tsx:90"},
                "OpenShell.ResumeSandbox": {"ui": "none", "where": ""},
            },
            "fields": {"openshell.v1.SandboxSpec.idle_timeout": {"ui": "document", "where": "frontend/src/x.ts:1"}},
        }
        made = apidiff.annotate(changes(), ledger)
        rpcs = dict((item["rpc"], item) for item in made["rpcs_added"])
        self.assertTrue(rpcs["OpenShell.SuspendSandbox"]["covered"])
        self.assertIn("SandboxDetailPage.tsx:90", rpcs["OpenShell.SuspendSandbox"]["dashboard"])
        self.assertFalse(rpcs["OpenShell.ResumeSandbox"]["covered"])
        self.assertEqual(rpcs["OpenShell.ResumeSandbox"]["dashboard"], "**not offered**")
        fields = dict((item["field"], item) for item in made["fields_added"])
        self.assertIn("document only", fields["openshell.v1.SandboxSpec.idle_timeout"]["dashboard"])
        self.assertIsNone(fields["openshell.v1.Sandbox.reason"]["covered"])


class Render(unittest.TestCase):
    def setUp(self):
        self.text = apidiff.render(apidiff.annotate(changes()), "0.9.0", "0.9.1")

    def test_the_headline_counts_what_is_new_and_what_may_break(self):
        self.assertIn(
            "OpenShell 0.9.0 → 0.9.1: 2 new RPCs, 1 new field a client can send, 1 new field in what it returns, "
            "2 new enum values, 2 new messages. 6 things removed or changed.",
            self.text,
        )

    def test_suspend_and_resume_are_in_the_table_of_new_rpcs(self):
        section = self.text.split("### New RPCs")[1].split("###")[0]
        self.assertIn("| `SuspendSandbox` | workspace user (`sandbox:write`) | Freeze a ready sandbox", section)
        self.assertIn("| `ResumeSandbox` |", section)
        self.assertEqual(section.count("**not covered**"), 2)

    def test_the_new_phases_are_in_the_table_of_enum_values(self):
        section = self.text.split("### New enum values")[1].split("###")[0]
        self.assertIn("| `SandboxPhase` | `SANDBOX_PHASE_SUSPENDED` |", section)
        self.assertIn("`Sandbox.phase`", section)

    def test_what_can_break_has_a_section_of_its_own(self):
        section = self.text.split("### Removed or changed")[1].split("###")[0]
        for expected in ("RPC `LegacyThing` | removed", "field `SandboxSpec.command` | removed", "enum value `SANDBOX_PHASE_GONE`"):
            self.assertIn(expected, section)

    def test_internal_rpcs_are_named_and_not_tabled(self):
        self.assertIn("which the dashboard never calls: `PeerNotify`.", self.text)

    def test_a_limit_cuts_a_table_and_says_how_much_is_left(self):
        cut = apidiff.render(apidiff.annotate(changes()), "0.9.0", "0.9.1", limit=1)
        self.assertIn("…and 1 more. The run summary lists every row.", cut)
        self.assertLess(len(cut), len(self.text))

    def test_a_pipe_in_a_comment_does_not_break_a_table(self):
        made = apidiff.annotate(changes())
        made["rpcs_added"][0]["doc"] = "a | b"
        self.assertIn("a \\| b", apidiff.render(made, "0.9.0", "0.9.1"))


if __name__ == "__main__":
    unittest.main()
