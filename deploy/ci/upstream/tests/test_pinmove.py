"""The pin move: one working tree, taken from one upstream release to another."""

import os
import unittest

import pinmove
import pins
from tests import support

TOOLS = "/the/workflows/own/checkout"


class PinMove(unittest.TestCase):
    def setUp(self):
        self.repo = support.Repo("0.1.3")
        self.addCleanup(self.repo.close)
        self.tools = support.FakeTools()

    def apply(self, target, tools=None):
        return pinmove.apply(self.repo.path, target, tools=TOOLS, run=tools or self.tools)

    def changed(self):
        # "XY path" per line. No path here has a space in it.
        out = self.repo.git("status", "--porcelain")
        return sorted(text.split()[-1] for text in out.splitlines())

    def pins(self):
        return pins.load(os.path.join(self.repo.path, pinmove.PINS))


class MovesEverythingTogether(PinMove):
    def setUp(self):
        PinMove.setUp(self)
        self.to = support.target("0.1.4-pre.2")
        self.result = self.apply(self.to)

    def test_says_what_moved(self):
        self.assertEqual(
            self.result,
            {
                "moved": True,
                "from": "0.1.3",
                "to": "0.1.4-pre.2",
                "sdk": "v0.1.4-pre.2",
                "line": None,
                "migrated": False,
                "rewritten": [],
            },
        )

    def test_the_pins_name_the_release_its_images_and_its_sdk(self):
        doc = self.pins()
        self.assertEqual(doc["release"], "0.1.4-pre.2")
        self.assertEqual(doc["gateway_image"], self.to["gateway_image"])
        self.assertEqual(doc["supervisor_image"], self.to["supervisor_image"])
        self.assertEqual(doc["sdk"], "v0.1.4-pre.2")
        self.assertTrue(pins.is_canonical(self.repo.read(pinmove.PINS)))

    def test_go_mod_is_on_the_sdk_go_module_at_the_releases_tag(self):
        go_mod = self.repo.read(pinmove.GO_MOD)
        self.assertEqual(pins.go_mod_sdk_module(go_mod), (pins.SDK_MODULE, self.pins()["sdk"]))
        self.assertIn("%s v0.1.4-pre.2" % pins.SDK_MODULE, self.repo.read(pinmove.GO_SUM))

    def test_the_sdk_is_fetched_at_its_tag_tidied_and_its_tag_checked_never_at_latest(self):
        self.assertEqual(
            self.tools.calls[:3],
            [
                ["go", "get", "%s@v0.1.4-pre.2" % pins.SDK_MODULE],
                ["go", "mod", "tidy"],
                ["go", "list", "-m", "-json", "%s@v0.1.4-pre.2" % pins.SDK_MODULE],
            ],
        )
        for command in self.tools.calls:
            self.assertNotIn("@latest", " ".join(command))

    def test_the_readme_block_is_regenerated_by_the_workflows_own_copy_of_the_script(self):
        readme = [command for command in self.tools.calls if command[0] == "node"]
        self.assertEqual(len(readme), 1)
        # From the checkout the workflow runs at, pointed at the tree being changed.
        self.assertEqual(readme[0][1], os.path.join(TOOLS, "scripts/readme-gateway-range.mjs"))
        self.assertEqual(readme[0][2:], [
            "--write",
            "--readme", os.path.join(self.repo.path, "README.md"),
            "--pins", os.path.join(self.repo.path, pinmove.PINS),
        ])
        self.assertIn("| Tested on | `0.1.4-pre.2` |", self.repo.read("README.md"))

    def test_a_patch_or_pre_release_of_the_same_line_leaves_the_built_in_line_alone(self):
        self.assertIn('const BuiltInGatewayReleaseLine = "0.1"', self.repo.read(pinmove.LINE_SOURCE))

    def test_nothing_else_in_the_tree_changes(self):
        self.assertEqual(self.changed(), sorted([pinmove.PINS, pinmove.GO_MOD, pinmove.GO_SUM, pinmove.README]))
        self.assertTrue(set(self.changed()) <= set(pinmove.PIN_PATHS))

    def test_only_go_get_go_mod_tidy_and_the_readme_script_are_run(self):
        # Nothing that builds or executes what was downloaded.
        for command in self.tools.calls:
            self.assertIn(tuple(command[:2]), (("go", "get"), ("go", "mod"), ("go", "list"), ("node", command[1])), command)
            self.assertNotIn(command[1], ("build", "test", "vet", "run", "install", "generate"))


class ANewMinor(PinMove):
    def test_the_line_compiled_into_the_bff_moves_with_the_pin(self):
        result = self.apply(support.target("0.2.0"))
        self.assertEqual(result["line"], "0.2")
        source = self.repo.read(pinmove.LINE_SOURCE)
        self.assertIn('const BuiltInGatewayReleaseLine = "0.2"', source)
        self.assertIn("// The gateway minor release line this build is for.", source, "the rest of the file is kept")
        self.assertIn(pinmove.LINE_SOURCE, self.changed())

    def test_a_pre_release_of_a_new_minor_moves_it_too(self):
        self.assertEqual(self.apply(support.target("0.2.0-pre.1"))["line"], "0.2")

    def test_a_source_file_without_the_constant_is_an_error(self):
        self.repo.write(pinmove.LINE_SOURCE, "package models\n\nconst (\n\tBuiltInGatewayReleaseLine = \"0.1\"\n)\n")
        with self.assertRaises(pinmove.PinMoveError) as caught:
            self.apply(support.target("0.2.0"))
        self.assertIn("does not declare the built-in line", str(caught.exception))


class AlreadyThere(PinMove):
    def test_a_tree_on_the_target_is_left_alone_and_nothing_is_run(self):
        to = support.target("0.1.4-pre.2")
        self.apply(to)
        self.repo.commit("on the target")
        again = support.FakeTools()
        result = self.apply(to, again)
        self.assertFalse(result["moved"])
        self.assertEqual(again.calls, [])
        self.assertEqual(self.changed(), [])

    def test_a_tag_upstream_pushed_again_is_a_different_gateway(self):
        self.apply(support.target("0.1.4-pre.2"))
        self.repo.commit("on the target")
        pushed_again = support.target("0.1.4-pre.2", salt="rebuilt")
        result = self.apply(pushed_again)
        self.assertTrue(result["moved"])
        self.assertEqual(self.pins()["gateway_image"], pushed_again["gateway_image"])
        self.assertEqual(self.changed(), [pinmove.PINS], "the SDK commit is the same, so only the digests move")

    def test_at_target_needs_the_release_the_digests_and_the_sdk_commit(self):
        to = support.target("0.1.4-pre.2")
        at = lambda target: pinmove.at_target(self.pins(), self.repo.path, target)
        self.assertFalse(at(to))
        self.apply(to)
        self.assertTrue(at(to))
        self.assertFalse(at(support.target("0.1.4-pre.2", salt="rebuilt")))
        self.assertFalse(at(dict(to, sdk=support.sdk_of("0.1.4"))))
        self.assertFalse(at(dict(to, sdk=None)))


class WhatStopsIt(PinMove):
    def test_a_failing_go_get_is_an_error_and_the_pins_are_not_touched(self):
        before = self.repo.read(pinmove.PINS)
        with self.assertRaises(pinmove.PinMoveError) as caught:
            self.apply(support.target("0.1.4"), support.FakeTools(fail=("go", "get")))
        self.assertIn("go get", str(caught.exception))
        self.assertIn("simulated failure", str(caught.exception))
        self.assertEqual(self.repo.read(pinmove.PINS), before)

    def test_a_failing_tidy_or_readme_script_is_an_error(self):
        for prefix in (("go", "mod", "tidy"), ("node",)):
            with self.assertRaises(pinmove.PinMoveError):
                self.apply(support.target("0.1.4"), support.FakeTools(fail=prefix))

    def test_an_sdk_that_did_not_land_on_the_tag_is_refused(self):
        # go.mod ends up on some other version than the one that was asked for.
        def elsewhere(command, cwd):
            if command[:2] == ["go", "get"]:
                command = ["go", "get", "%s@v0.1.3" % pins.SDK_MODULE]
            return support.FakeTools()(command, cwd)

        with self.assertRaises(pinmove.PinMoveError) as caught:
            self.apply(support.target("0.1.4"), elsewhere)
        self.assertIn("Nothing is pinned to an SDK other than the one tagged for the release", str(caught.exception))

    def test_a_tag_that_is_not_the_commit_the_plan_read_is_refused(self):
        # sdk-go's tag was moved between the plan and the bump.
        tools = support.FakeTools(origin={"v0.1.4": "e" * 40})
        with self.assertRaises(pinmove.PinMoveError) as caught:
            self.apply(support.target("0.1.4"), tools)
        self.assertIn("Nothing is pinned to an SDK other than the one tagged for the release", str(caught.exception))

    def test_a_target_with_no_sdk_tag_yet_is_refused_and_nothing_is_run(self):
        before = self.repo.read(pinmove.PINS)
        with self.assertRaises(pinmove.PinMoveError) as caught:
            self.apply(dict(support.target("0.1.4"), sdk=None))
        self.assertIn("has not tagged v0.1.4 yet", str(caught.exception))
        self.assertEqual(self.tools.calls, [])
        self.assertEqual(self.repo.read(pinmove.PINS), before)

    def test_a_target_whose_image_is_of_another_release_is_refused(self):
        to = dict(support.target("0.1.4"), gateway_image=support.image("gateway", "0.1.3"))
        with self.assertRaises(pins.PinsError):
            self.apply(to)

    def test_a_pins_file_that_cannot_be_read_is_an_error_not_a_traceback(self):
        for text in ("{ not json", "[]", ""):
            self.repo.write(pinmove.PINS, text)
            with self.assertRaises(pinmove.PinMoveError):
                self.apply(support.target("0.1.4"))
        os.remove(os.path.join(self.repo.path, pinmove.PINS))
        with self.assertRaises(pinmove.PinMoveError):
            self.apply(support.target("0.1.4"))

    def test_the_lanes_the_file_used_to_hold_are_not_moved_blindly(self):
        # A branch cut before the pins named one release: the move would leave
        # a file that is half one shape and half the other.
        doc = dict(support.pins_doc(), lanes=[{"version": "0.1.0", "required": True}])
        self.repo.write(pinmove.PINS, pins.dump(doc))
        with self.assertRaises(pinmove.PinMoveError) as caught:
            self.apply(support.target("0.1.4"))
        self.assertIn("top-level key 'lanes' is not allowed", str(caught.exception))

    def test_a_missing_tool_is_an_error_that_names_it(self):
        code, output = pinmove.run_command(["no-such-tool-anywhere"], self.repo.path)
        self.assertEqual(code, 127)
        self.assertIn("no-such-tool-anywhere", output)


class OffTheOldPath(unittest.TestCase):
    """A tree that still imports github.com/NVIDIA/OpenShell/sdk/go, as main did up to 0.1.8."""

    def setUp(self):
        self.repo = support.Repo("0.1.3", legacy=True)
        self.addCleanup(self.repo.close)
        self.tools = support.FakeTools()
        self.to = support.target("0.1.9")
        self.result = pinmove.apply(self.repo.path, self.to, tools=TOOLS, run=self.tools)

    def test_the_move_lands_on_the_new_module_at_the_tag(self):
        go_mod = self.repo.read(pinmove.GO_MOD)
        self.assertEqual(pins.go_mod_sdk_module(go_mod), (pins.SDK_MODULE, "v0.1.9"))
        self.assertNotIn(pins.LEGACY_SDK_MODULE, go_mod)
        self.assertEqual(pins.load(os.path.join(self.repo.path, pinmove.PINS))["sdk"], "v0.1.9")

    def test_the_imports_are_rewritten_and_reported(self):
        source = self.repo.read(support.SDK_USER)
        self.assertIn('"%s/openshell/v1"' % pins.SDK_MODULE, source)
        self.assertNotIn(pins.LEGACY_SDK_MODULE, source)
        self.assertEqual((self.result["migrated"], self.result["rewritten"]), (True, [support.SDK_USER]))

    def test_imports_are_rewritten_before_tidy_so_tidy_drops_the_old_path(self):
        self.assertEqual(
            [command[:3] for command in self.tools.calls[:2]],
            [["go", "get", "%s@v0.1.9" % pins.SDK_MODULE], ["go", "mod", "tidy"]],
        )

    def test_only_the_pin_paths_and_the_rewritten_go_files_change(self):
        out = self.repo.git("status", "--porcelain")
        changed = sorted(text.split()[-1] for text in out.splitlines())
        self.assertEqual(changed, sorted([pinmove.PINS, pinmove.GO_MOD, pinmove.GO_SUM, pinmove.README, support.SDK_USER]))

    def test_an_import_left_on_the_old_path_is_refused(self):
        repo = support.Repo("0.1.3", legacy=True)
        self.addCleanup(repo.close)

        def keeps_the_old_import(command, cwd):
            code, output = support.FakeTools()(command, cwd)
            if command[:3] == ["go", "mod", "tidy"]:
                # Something outside backend/**.go still imports the old path.
                path = os.path.join(cwd, "go.mod")
                text = support._read(path).replace("require (\n", "require (\n\t%s v0.0.1\n" % pins.LEGACY_SDK_MODULE, 1)
                support._write(path, text)
            return code, output

        with self.assertRaises(pinmove.PinMoveError) as caught:
            pinmove.apply(repo.path, self.to, tools=TOOLS, run=keeps_the_old_import)
        self.assertIn("still requires %s" % pins.LEGACY_SDK_MODULE, str(caught.exception))


if __name__ == "__main__":
    unittest.main()
