"""The plan: what is upstream ahead of this branch, and what moving to it would pin."""

import unittest

import pins
import plan
import upstream
from tests import support



def images_for(*versions):
    out = {}
    for version in versions:
        for name in ("gateway", "supervisor"):
            out["%s:%s" % (name, version)] = support.digest(name + version)
    return out


def build(doc=None, requested=None, **data):
    fake = support.FakeUpstream(support.upstream_data(**data))
    return plan.build(doc or support.pins_of_2026_10_09(), fake, requested=requested), fake


class UpstreamToday(unittest.TestCase):
    """What main pinned on 2026-10-09 against upstream as it was that day."""

    def setUp(self):
        self.plan, self.fake = build()

    def test_main_is_on_0_1_3_and_its_sdk_is_the_tags_commit(self):
        pinned = self.plan["pinned"]
        self.assertEqual((pinned["release"], pinned["line"], pinned["stable"]), ("0.1.3", "0.1", True))
        self.assertIs(pinned["sdk_on_tag"], True)
        self.assertEqual(self.plan["newest_stable"], "0.1.3")

    def test_the_target_is_the_newest_pre_release_of_0_1_4(self):
        target = self.plan["target"]
        self.assertEqual(target["version"], "0.1.4-pre.2")
        self.assertEqual(target["commit"], "4c1b16a4a104581fb0afe8675feff34f00cc2ca8")
        self.assertFalse(target["stable"])
        self.assertEqual((target["base"], target["line"], target["same_line"]), ("0.1.4", "0.1", True))
        self.assertEqual(target["skipped"], [])
        # A schedule does not bump to a pre-release; check A still tests it.
        self.assertFalse(target["bump"])
        self.assertEqual(target["branch"], "bump/openshell-0.1.4-pre.2")

    def test_its_images_are_resolved_to_digests_once(self):
        target = self.plan["target"]
        self.assertTrue(target["ready"])
        self.assertEqual(
            target["gateway_image"],
            "ghcr.io/nvidia/openshell/gateway:0.1.4-pre.2@sha256:7eaaa328795e84aa4ad2e61d4eede8354d0a6073cd833d68c1d9568739e3093c",
        )
        self.assertTrue(target["supervisor_image"].startswith("ghcr.io/nvidia/openshell/supervisor:0.1.4-pre.2@sha256:"))
        # Only the target is looked up. Nothing else is tested, so nothing else is asked.
        self.assertEqual(self.fake.digest_lookups, ["gateway:0.1.4-pre.2", "supervisor:0.1.4-pre.2"])

    def test_the_workload_image_and_the_config_schema_are_carried(self):
        doc = support.pins_of_2026_10_09()
        self.assertEqual(self.plan["sandbox_image"], doc["sandbox_image"])
        self.assertEqual(self.plan["config_schema"], doc["config_schema"])


class Requested(unittest.TestCase):
    """A version a person asked for, from workflow_dispatch."""

    def test_a_pre_release_asked_for_is_bumped_to_as_a_prep_branch(self):
        made, _ = build(requested="0.1.4-pre.1", images=images_for("0.1.4-pre.1"))
        target = made["target"]
        self.assertEqual(made["requested"], "0.1.4-pre.1")
        self.assertEqual((target["version"], target["stable"], target["bump"]), ("0.1.4-pre.1", False, True))
        self.assertEqual(target["branch"], "bump/openshell-0.1.4-pre.1")

    def test_it_wins_over_the_newest_release(self):
        made, _ = build(requested="0.1.4", tags=["v0.1.4", "v0.1.5"], images=images_for("0.1.4"))
        self.assertEqual(made["target"]["version"], "0.1.4")

    def test_a_version_upstream_has_not_tagged_is_refused(self):
        with self.assertRaisesRegex(plan.PlanError, "upstream has no tag v0.9.0"):
            build(requested="0.9.0")

    def test_a_version_that_is_not_above_the_pin_is_refused(self):
        with self.assertRaisesRegex(plan.PlanError, "A bump only moves forward"):
            build(requested="0.1.3")

    def test_a_malformed_version_is_refused(self):
        for text in ("v0.1.4", "latest", "0.1"):
            with self.assertRaisesRegex(plan.PlanError, "without the leading v"):
                build(requested=text)


class Targets(unittest.TestCase):
    def test_nothing_ahead_is_no_target_and_no_lookup(self):
        made, fake = build(drop=("v0.1.4-pre.1", "v0.1.4-pre.2"))
        self.assertIsNone(made["target"])
        self.assertEqual(fake.digest_lookups, [])

    def test_a_stable_release_on_the_same_line(self):
        made, _ = build(tags=["v0.1.4"], images=images_for("0.1.4"))
        target = made["target"]
        self.assertEqual((target["version"], target["stable"], target["same_line"]), ("0.1.4", True, True))
        self.assertTrue(target["bump"])
        self.assertEqual(target["branch"], "bump/openshell-0.1.4")

    def test_stable_releases_in_between_are_skipped_and_said_so(self):
        made, _ = build(tags=["v0.1.4", "v0.1.5", "v0.1.6"], images=images_for("0.1.6"))
        self.assertEqual(made["target"]["version"], "0.1.6")
        self.assertEqual(made["target"]["skipped"], ["0.1.4", "0.1.5"])

    def test_a_stable_release_that_starts_a_new_line_is_bumped_to(self):
        made, _ = build(tags=["v0.2.0"], images=images_for("0.2.0"))
        target = made["target"]
        self.assertEqual((target["version"], target["line"], target["same_line"]), ("0.2.0", "0.2", False))
        self.assertTrue(target["bump"])
        self.assertNotIn("release_branch", target, "release branches are made by hand")

    def test_a_pre_release_of_a_new_line_is_tested_and_not_bumped_on_a_schedule(self):
        made, _ = build(tags=["v0.2.0-pre.1"], images=images_for("0.2.0-pre.1"))
        target = made["target"]
        self.assertEqual((target["version"], target["same_line"]), ("0.2.0-pre.1", False))
        self.assertFalse(target["bump"])

    def test_a_tag_whose_images_are_not_published_yet_is_not_ready(self):
        made, _ = build(tags=["v0.1.4"])
        target = made["target"]
        self.assertEqual(target["version"], "0.1.4")
        self.assertFalse(target["ready"])
        self.assertIn("has not published its gateway image yet", target["waiting_for"])
        self.assertIsNone(target["gateway_image"])
        # It does not fall back to an older release that does have images.

    def test_a_missing_supervisor_image_is_not_ready_either(self):
        made, _ = build(tags=["v0.1.4"], images={"gateway:0.1.4": support.digest("g")})
        self.assertFalse(made["target"]["ready"])
        self.assertIn("supervisor image", made["target"]["waiting_for"])


class WhatMustStopTheRun(unittest.TestCase):
    def test_a_registry_outage_is_an_error_not_a_missing_image(self):
        with self.assertRaises(upstream.UpstreamError):
            build(outage=["gateway:0.1.4-pre.2"])

    def test_no_release_tags_at_all_is_an_error_not_nothing_new(self):
        fake = support.FakeUpstream({"tags": ["%s\trefs/tags/dev" % ("a" * 40)], "images": {}})
        with self.assertRaises(plan.PlanError):
            plan.build(support.pins_of_2026_10_09(), fake)

    def test_a_malformed_pins_file_is_an_error(self):
        doc = support.pins_of_2026_10_09()
        doc["release"] = "dev"
        with self.assertRaises(pins.PinsError):
            build(doc)


class SdkPinOnTheReleaseTag(unittest.TestCase):
    def test_a_pin_that_is_somewhere_else_is_reported(self):
        doc = support.pins_of_2026_10_09()
        doc["sdk"] = "v0.0.0-20261001000000-0123456789ab"
        made, _ = build(doc)
        self.assertIs(made["pinned"]["sdk_on_tag"], False)

    def test_it_cannot_be_told_for_a_tag_upstream_no_longer_has(self):
        made, _ = build(drop=("v0.1.3",))
        self.assertIsNone(made["pinned"]["sdk_on_tag"])


if __name__ == "__main__":
    unittest.main()
