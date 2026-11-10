"""The pins file: validation, the stable-release check, and the move."""

import copy
import json
import os
import unittest

import pins
from tests import support

REAL_PINS = os.path.join(support.REPO_ROOT, "deploy", "ci", "gateway-pins.json")
REAL_GO_MOD = os.path.join(support.REPO_ROOT, "backend", "go.mod")


def problems_of(**changes):
    """validate() on the fixture with some keys changed; a value of None removes the key."""
    doc = support.pins_doc()
    for key, value in changes.items():
        if value is None:
            doc.pop(key, None)
        else:
            doc[key] = value
    return pins.validate(doc, schemas=["v1", "v2"])


class CheckedInPinsFile(unittest.TestCase):
    """The file CI actually reads must satisfy every rule below."""

    def setUp(self):
        self.doc = pins.load(REAL_PINS)
        with open(REAL_PINS, encoding="utf-8") as fh:
            self.text = fh.read()

    def test_is_well_formed_and_agrees_with_go_mod(self):
        with open(REAL_GO_MOD, encoding="utf-8") as fh:
            go_mod_version = pins.go_mod_sdk(fh.read())
        self.assertIsNotNone(go_mod_version)
        problems = pins.validate(self.doc, schemas=pins.known_schemas(REAL_PINS), go_mod_version=go_mod_version)
        self.assertEqual(problems, [])

    def test_round_trips_byte_for_byte(self):
        # A pin move rewrites this file; untouched lines must not move.
        self.assertEqual(
            pins.dump(self.doc),
            self.text,
            "gateway-pins.json is not in canonical form; run `python3 deploy/ci/upstream/follow.py format-pins`",
        )
        self.assertTrue(pins.is_canonical(self.text))

    def test_names_one_release_and_nothing_from_the_lanes_it_used_to_hold(self):
        self.assertIsInstance(self.doc["release"], str)
        for key in pins.RETIRED_KEYS:
            self.assertNotIn(key, self.doc)

    def test_the_stack_has_a_template_for_both_config_schemas(self):
        # v1 for gateways up to 0.0.116, v2 from 0.1.0. More may be added.
        self.assertTrue({"v1", "v2"} <= set(pins.known_schemas(REAL_PINS)))

    def test_the_comment_describes_the_file_as_it_is(self):
        comment = " ".join(self.doc["_comment"])
        for word in ("release", "sdk", "gateway_image", "supervisor_image", "sandbox_image", "config_schema", "bump/openshell-"):
            self.assertIn(word, comment)
        for gone in ("lanes", "floor", "ceiling", "sweep", "axis", "`next`"):
            self.assertNotIn(gone, comment)


class Validate(unittest.TestCase):
    def test_the_fixture_is_valid(self):
        self.assertEqual(problems_of(), [])

    def test_a_pre_release_is_well_formed(self):
        # Whether a branch may pin one is require_stable's question, not this one's.
        doc = pins.move(
            support.pins_doc(),
            "0.1.4-pre.2",
            support.image("gateway", "0.1.4-pre.2"),
            support.image("supervisor", "0.1.4-pre.2"),
            "v0.0.0-20261009120000-4c1b16a4a104",
        )
        self.assertEqual(pins.validate(doc, schemas=["v2"]), [])

    def test_every_key_is_required(self):
        for key in pins.REQUIRED_KEYS:
            self.assertTrue(any("missing top-level key %r" % key in p for p in problems_of(**{key: None})), key)

    def test_what_the_file_used_to_hold_is_refused(self):
        for key in ("lanes", "floor", "floor_release", "ceiling", "ceiling_release", "range"):
            found = problems_of(**{key: []})
            self.assertTrue(any("top-level key %r is not allowed" % key in p for p in found), key)

    def test_other_top_level_keys_are_left_alone(self):
        self.assertEqual(problems_of(_note="kept by another tool", owner={"team": "ui"}), [])

    def test_a_release_must_be_a_release(self):
        for release in ("v0.1.3", "0.1", "0.1.3-rc.1", "0.1.3-pre", "0.1.3.1", "", 13, ["0.1.3"]):
            found = problems_of(release=release)
            self.assertTrue(any("release must be an upstream release" in p for p in found), repr(release))

    def test_a_dev_build_is_never_pinned(self):
        for release in ("dev", "0.1.4-dev.84", "latest"):
            found = problems_of(release=release)
            self.assertTrue(any("dev build, which is never pinned" in p for p in found), release)

    def test_images_are_pinned_by_digest(self):
        for field in ("gateway_image", "supervisor_image"):
            found = problems_of(**{field: "ghcr.io/nvidia/openshell/gateway:0.1.3"})
            self.assertTrue(any("%s is not pinned by digest" % field in p for p in found), field)
            found = problems_of(**{field: "ghcr.io/nvidia/openshell/gateway:0.1.3@sha256:abc"})
            self.assertTrue(any("%s is not pinned by digest" % field in p for p in found), field)

    def test_an_image_is_tagged_with_the_release(self):
        found = problems_of(gateway_image=support.image("gateway", "0.1.2"))
        self.assertTrue(any("gateway_image is tagged '0.1.2' but release is '0.1.3'" in p for p in found), found)

    def test_a_moving_tag_is_refused_even_with_a_digest(self):
        for tag in pins.MOVING_TAGS:
            found = problems_of(gateway_image=support.image("gateway", tag))
            self.assertTrue(any("uses the moving tag %r" % tag in p for p in found), tag)

    def test_an_image_without_a_tag_is_malformed(self):
        for ref in ("ghcr.io/nvidia/openshell/gateway", "localhost:5000/gateway", "", 3):
            found = problems_of(supervisor_image=ref)
            self.assertTrue(any("supervisor_image must be repository:tag@sha256:<digest>" in p for p in found), repr(ref))

    def test_the_sandbox_image_is_pinned_by_digest(self):
        found = problems_of(sandbox_image="ghcr.io/nvidia/openshell-community/sandboxes/base:latest")
        self.assertTrue(any("sandbox_image is not pinned by digest" in p for p in found))

    def test_the_sdk_is_an_exact_version_never_a_query(self):
        for sdk in ("latest", "main", "@latest", "v0.1", "6648bd0c290e", "", None):
            found = problems_of(sdk=sdk) if sdk is not None else pins.validate(dict(support.pins_doc(), sdk=None))
            self.assertTrue(any("sdk must be an exact module version" in p for p in found), repr(sdk))
        self.assertEqual(problems_of(sdk="v0.1.3"), [], "a tagged SDK version is exact too")

    def test_the_sdk_must_be_the_one_go_mod_builds_against(self):
        doc = support.pins_doc()
        self.assertEqual(pins.validate(doc, go_mod_version=doc["sdk"]), [])
        found = pins.validate(doc, go_mod_version="v0.0.0-20260101000000-000000000000")
        self.assertTrue(any("one pin recorded twice" in p for p in found), found)

    def test_the_config_schema_needs_a_template(self):
        self.assertTrue(any("config_schema must look like v1 or v2" in p for p in problems_of(config_schema="2")))
        self.assertTrue(any("config_schema 'v9' has no template" in p for p in problems_of(config_schema="v9")))
        # Without a list of templates the shape is all that can be checked.
        self.assertEqual(pins.validate(dict(support.pins_doc(), config_schema="v9")), [])

    def test_every_problem_is_reported_at_once(self):
        found = problems_of(release="dev", sdk="latest", sandbox_image="x:y", config_schema="two", lanes=[])
        self.assertGreaterEqual(len(found), 5, found)

    def test_something_that_is_not_an_object_is_one_problem(self):
        for doc in ([], "pins", None, 3):
            self.assertEqual(pins.validate(doc), ["the pins file must be a JSON object"])


class RequireStable(unittest.TestCase):
    def test_a_stable_release_passes(self):
        self.assertEqual(pins.require_stable(support.pins_doc("0.1.3")), [])

    def test_a_pre_release_is_refused_and_the_message_says_what_it_is_not(self):
        found = pins.require_stable(dict(support.pins_doc(), release="0.1.4-pre.2"))
        self.assertEqual(len(found), 1)
        self.assertIn("pins OpenShell 0.1.4-pre.2, a pre-release", found[0])
        self.assertIn("until upstream releases 0.1.4", found[0])
        # It must not read as a compatibility failure.
        self.assertIn("It says nothing about compatibility", found[0])

    def test_something_that_is_not_a_release_is_refused_too(self):
        for release in ("dev", None, ""):
            self.assertTrue(pins.require_stable(dict(support.pins_doc(), release=release)), repr(release))
        self.assertTrue(pins.require_stable("not a document"))


class CanonicalForm(unittest.TestCase):
    def test_dump_writes_non_ascii_as_itself(self):
        doc = dict(support.pins_doc(), _comment=["a dash — and an arrow →"])
        self.assertIn("a dash — and an arrow →", pins.dump(doc))
        self.assertTrue(pins.is_canonical(pins.dump(doc)))

    def test_what_is_not_canonical(self):
        doc = dict(support.pins_doc(), _comment=["a dash —"])
        canonical = pins.dump(doc)
        for name, text in (
            ("four-space indent", json.dumps(doc, indent=4, ensure_ascii=False) + "\n"),
            ("escaped non-ASCII", json.dumps(doc, indent=2) + "\n"),
            ("no final newline", canonical.rstrip("\n")),
            ("one line", json.dumps(doc, ensure_ascii=False) + "\n"),
            ("not JSON", "{"),
        ):
            self.assertFalse(pins.is_canonical(text), name)


class Move(unittest.TestCase):
    def setUp(self):
        self.doc = support.pins_doc("0.1.3")
        self.to = support.target("0.1.4-pre.2")
        self.sdk = support.sdk_version(self.to["commit"])

    def move(self, **changes):
        args = dict(
            release=self.to["version"],
            gateway_image=self.to["gateway_image"],
            supervisor_image=self.to["supervisor_image"],
            sdk=self.sdk,
        )
        args.update(changes)
        return pins.move(self.doc, **args)

    def test_the_four_things_that_come_from_the_release_move_together(self):
        before = copy.deepcopy(self.doc)
        moved = self.move()
        self.assertEqual(moved["release"], "0.1.4-pre.2")
        self.assertEqual(moved["sdk"], self.sdk)
        self.assertEqual(moved["gateway_image"], self.to["gateway_image"])
        self.assertEqual(moved["supervisor_image"], self.to["supervisor_image"])
        self.assertEqual(self.doc, before, "the document passed in is not modified")

    def test_everything_else_is_carried_over_in_the_same_order(self):
        moved = self.move()
        for key in ("_comment", "sandbox_image", "config_schema"):
            self.assertEqual(moved[key], self.doc[key])
        self.assertEqual(list(moved), list(self.doc))
        self.assertEqual(pins.validate(moved, schemas=["v2"]), [])

    def test_the_diff_is_the_four_lines(self):
        before, after = pins.dump(self.doc).splitlines(), pins.dump(self.move()).splitlines()
        changed = [index for index, pair in enumerate(zip(before, after)) if pair[0] != pair[1]]
        self.assertEqual(len(before), len(after))
        self.assertEqual(len(changed), 4)

    def test_refuses_anything_that_is_not_a_release(self):
        for release in ("dev", "latest", "v0.1.4", "0.1.4-dev.3"):
            with self.assertRaises(pins.PinsError):
                self.move(release=release)

    def test_refuses_an_image_of_another_release_or_without_a_digest(self):
        with self.assertRaises(pins.PinsError):
            self.move(gateway_image=support.image("gateway", "0.1.3"))
        with self.assertRaises(pins.PinsError):
            self.move(supervisor_image="ghcr.io/nvidia/openshell/supervisor:0.1.4-pre.2")

    def test_refuses_an_sdk_that_is_a_query(self):
        for sdk in ("latest", "main", "", None):
            with self.assertRaises(pins.PinsError):
                self.move(sdk=sdk)


class Helpers(unittest.TestCase):
    def test_split_image(self):
        self.assertEqual(pins.split_image("repo/name:1.0@sha256:abc"), ("repo/name", "1.0", "sha256:abc"))
        self.assertEqual(pins.split_image("localhost:5000/gateway"), ("localhost:5000/gateway", "", ""))
        self.assertEqual(pins.split_image("localhost:5000/gateway:0.1.3"), ("localhost:5000/gateway", "0.1.3", ""))
        self.assertEqual(pins.split_image(None), ("", "", ""))

    def test_go_mod_sdk_reads_both_forms_of_require(self):
        block = "require (\n\t%s v0.0.0-20261009050449-e1f3c82caa3e\n)\n" % pins.SDK_MODULE
        single = "require %s v0.1.3\n" % pins.SDK_MODULE
        self.assertEqual(pins.go_mod_sdk(block), "v0.0.0-20261009050449-e1f3c82caa3e")
        self.assertEqual(pins.go_mod_sdk(single), "v0.1.3")
        self.assertIsNone(pins.go_mod_sdk("module example.com/x\n"))

    def test_sdk_commit(self):
        self.assertEqual(pins.sdk_commit("v0.0.0-20261009050449-e1f3c82caa3e"), "e1f3c82caa3e")
        self.assertEqual(pins.sdk_commit("v0.1.4-0.20261009050449-e1f3c82caa3e"), "e1f3c82caa3e")
        self.assertIsNone(pins.sdk_commit("v0.1.3"))
        self.assertIsNone(pins.sdk_commit(None))


if __name__ == "__main__":
    unittest.main()
