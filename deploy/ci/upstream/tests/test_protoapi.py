"""Reading the gateway's API from .proto text."""

import os
import shutil
import tempfile
import unittest

import protoapi
from tests import protos, support


def surface(text):
    return protoapi.surface(protoapi.load(protos.files(text).get))


class Parse(unittest.TestCase):
    def setUp(self):
        self.file = protoapi.parse(protos.OLD, "openshell.proto")

    def test_package_imports_and_services(self):
        self.assertEqual(self.file["package"], "openshell.v1")
        self.assertEqual(self.file["imports"], ["datamodel.proto", "google/protobuf/timestamp.proto", "options.proto"])
        self.assertEqual(list(self.file["services"]), ["OpenShell"])

    def test_an_rpc_carries_its_shape_its_rule_and_the_first_sentence_above_it(self):
        rpc = self.file["services"]["OpenShell"]["rpcs"]["CreateSandbox"]
        self.assertEqual((rpc["request"], rpc["response"]), ("CreateSandboxRequest", "SandboxResponse"))
        self.assertEqual(rpc["auth"], {"auth_mode": "bearer", "scope": "sandbox:write", "workspace_role": "user"})
        self.assertEqual(rpc["doc"], "Create a new sandbox.")
        self.assertFalse(rpc["server_stream"])

    def test_streaming_and_a_rule_written_on_one_line(self):
        rpc = self.file["services"]["OpenShell"]["rpcs"]["WatchSandbox"]
        self.assertTrue(rpc["server_stream"])
        self.assertFalse(rpc["client_stream"])
        self.assertEqual(rpc["auth"]["scope"], "sandbox:read")

    def test_fields_keep_number_label_and_options(self):
        fields = self.file["messages"]["openshell.v1.SandboxSpec"]["fields"]
        self.assertEqual((fields["image"]["number"], fields["image"]["doc"]), (1, "The image to run."))
        self.assertEqual((fields["environment"]["label"], fields["environment"]["key"], fields["environment"]["type"]), ("map", "string", "string"))
        self.assertEqual(fields["command"]["label"], "repeated")
        self.assertEqual(fields["tty"]["label"], "optional")
        self.assertTrue(fields["token"]["secret"])
        self.assertFalse(fields["image"]["secret"])

    def test_a_comment_after_a_field_documents_that_field_and_not_the_next(self):
        fields = self.file["messages"]["openshell.v1.SandboxSpec"]["fields"]
        self.assertEqual(fields["log_level"]["doc"], "Trailing words about the level.")
        self.assertEqual(fields["replicas"]["doc"], "")

    def test_nested_messages_and_enums_get_full_names(self):
        self.assertIn("openshell.v1.SandboxSpec.Resources", self.file["messages"])
        self.assertEqual(
            list(self.file["enums"]["openshell.v1.SandboxSpec.RestartPolicy"]["values"]),
            ["RESTART_POLICY_UNSPECIFIED", "RESTART_POLICY_NEVER"],
        )

    def test_a_oneof_member_is_a_field_that_names_its_group(self):
        selector = protoapi.parse(protos.DATAMODEL)["messages"]["openshell.datamodel.v1.WorkspaceSelector"]
        self.assertEqual(selector["fields"]["all_workspaces"]["oneof"], "scope")

    def test_reserved_is_not_a_field(self):
        self.assertEqual(
            sorted(self.file["messages"]["openshell.v1.CreateSandboxRequest"]["fields"]), ["name", "spec", "workspace_scope"]
        )

    def test_enum_values_carry_the_comment_above_them(self):
        values = self.file["enums"]["openshell.v1.SandboxPhase"]["values"]
        self.assertEqual(values["SANDBOX_PHASE_READY"], {"number": 1, "doc": "The sandbox answers."})

    def test_syntax_it_does_not_know_stops_it_with_the_place(self):
        with self.assertRaises(protoapi.ProtoError) as raised:
            protoapi.parse('syntax = "proto3";\nmessage A {\n  group B = 1 { }\n}\n', "odd.proto")
        self.assertIn("odd.proto:3", str(raised.exception))

    def test_a_character_it_cannot_read_stops_it(self):
        with self.assertRaises(protoapi.ProtoError):
            protoapi.parse("message A { string a = 1; } @", "odd.proto")


class Load(unittest.TestCase):
    def test_follows_imports_and_leaves_google_alone(self):
        asked = []

        def read(name):
            asked.append(name)
            return protos.files(protos.OLD).get(name)

        self.assertEqual(sorted(protoapi.load(read)), ["datamodel.proto", "openshell.proto", "options.proto"])
        self.assertNotIn("google/protobuf/timestamp.proto", asked)

    def test_a_tag_without_the_root_file_is_an_error_not_an_empty_api(self):
        with self.assertRaises(protoapi.ProtoError):
            protoapi.load({}.get)


class Surface(unittest.TestCase):
    def setUp(self):
        self.surface = surface(protos.OLD)

    def test_user_facing_is_bearer_dual_and_unauthenticated(self):
        self.assertEqual(
            sorted(self.surface["rpcs"]),
            [
                "OpenShell.CreateSandbox",
                "OpenShell.GetSandboxConfig",
                "OpenShell.Health",
                "OpenShell.LegacyThing",
                "OpenShell.StopSandbox",
                "OpenShell.WatchSandbox",
            ],
        )
        self.assertEqual(self.surface["internal"], {"OpenShell.ReportStatus": "sandbox"})

    def test_an_rpc_with_no_rule_is_kept(self):
        text = protos.OLD.replace('    option (openshell.options.v1.authorization) = { auth_mode: "sandbox" };\n', "")
        self.assertIn("OpenShell.ReportStatus", surface(text)["rpcs"])

    def test_type_names_are_resolved_from_where_they_are_written(self):
        fields = self.surface["messages"]["openshell.v1.SandboxSpec"]["fields"]
        self.assertEqual(fields["restart_policy"]["type"], "openshell.v1.SandboxSpec.RestartPolicy")
        self.assertEqual(fields["resources"]["type"], "openshell.v1.SandboxSpec.Resources")
        request = self.surface["messages"]["openshell.v1.CreateSandboxRequest"]["fields"]
        self.assertEqual(request["workspace_scope"]["type"], "openshell.datamodel.v1.WorkspaceSelector")

    def test_messages_know_whether_they_are_sent_returned_or_both(self):
        messages = self.surface["messages"]
        self.assertEqual(messages["openshell.v1.CreateSandboxRequest"]["sides"], ["request"])
        self.assertEqual(messages["openshell.v1.Sandbox"]["sides"], ["response"])
        self.assertEqual(messages["openshell.v1.SandboxSpec"]["sides"], ["request", "response"])

    def test_what_only_a_sandbox_sends_is_left_out(self):
        self.assertNotIn("openshell.v1.ReportStatusRequest", self.surface["messages"])

    def test_enums_come_with_the_fields_that_use_them(self):
        self.assertIn("openshell.v1.SandboxPhase", self.surface["enums"])
        self.assertIn("openshell.v1.SandboxSpec.RestartPolicy", self.surface["enums"])


class GitReader(unittest.TestCase):
    def test_reads_a_tag_of_a_clone_whatever_is_checked_out(self):
        scratch = tempfile.mkdtemp(prefix="upstream-protos-")
        self.addCleanup(shutil.rmtree, scratch, ignore_errors=True)
        repo = support.proto_repo(
            os.path.join(scratch, "upstream"),
            [(protos.OLD_VERSION, protos.files(protos.OLD)), (protos.NEW_VERSION, protos.files(protos.NEW))],
        )
        old = protoapi.surface(protoapi.load(protoapi.git_reader(repo, "v" + protos.OLD_VERSION)))
        self.assertNotIn("OpenShell.SuspendSandbox", old["rpcs"])
        new = protoapi.surface(protoapi.load(protoapi.git_reader(repo, "v" + protos.NEW_VERSION)))
        self.assertIn("OpenShell.SuspendSandbox", new["rpcs"])
        with self.assertRaises(protoapi.ProtoError):
            protoapi.load(protoapi.git_reader(repo, "v9.9.9"))


if __name__ == "__main__":
    unittest.main()
