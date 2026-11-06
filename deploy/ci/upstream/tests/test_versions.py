"""Which of upstream's tags are releases, how they order, and which one to follow."""

import unittest

import versions
from tests import support


def found(*names):
    """releases() for a made-up tag list."""
    return versions.releases({name: support.commit_of(name) for name in names})


def target(pinned, *names):
    chosen = versions.select_target(pinned, found(*names))
    return chosen["version"] if chosen else None


class Parse(unittest.TestCase):
    def test_a_stable_release_and_a_pre_release(self):
        self.assertEqual(versions.parse("0.1.4"), (0, 1, 4, None))
        self.assertEqual(versions.parse("0.1.4-pre.2"), (0, 1, 4, 2))
        self.assertEqual(versions.parse("10.20.300-pre.11"), (10, 20, 300, 11))

    def test_everything_else_is_not_a_release(self):
        for text in (
            "v0.1.4",  # a version is written without the v
            "0.1",
            "0.1.4.5",
            "0.1.4-dev.84",
            "0.1.4-rc.1",
            "0.1.4-pre",
            "0.1.4-pre.x",
            "0.1.4-pre.2.1",
            "0.1.4+build",
            "01.1.4",
            "dev",
            "latest",
            "",
            None,
            14,
        ):
            self.assertIsNone(versions.parse(text), text)

    def test_what_a_version_belongs_to(self):
        self.assertEqual(versions.base("0.1.4-pre.2"), "0.1.4")
        self.assertEqual(versions.base("0.1.4"), "0.1.4")
        self.assertEqual(versions.line("0.1.4-pre.2"), "0.1")
        self.assertEqual(versions.line("1.20.3"), "1.20")
        self.assertTrue(versions.is_stable("0.1.4"))
        self.assertFalse(versions.is_stable("0.1.4-pre.2"))

    def test_asking_about_something_that_is_not_a_release_is_an_error(self):
        for ask in (versions.order, versions.base, versions.line, versions.is_stable):
            with self.assertRaises(versions.VersionError):
                ask("dev")


class Order(unittest.TestCase):
    def test_numbers_compare_as_numbers(self):
        ordered = sorted(["0.10.0", "0.9.0", "0.2.11", "0.2.9"], key=versions.order)
        self.assertEqual(ordered, ["0.2.9", "0.2.11", "0.9.0", "0.10.0"])

    def test_a_pre_release_comes_before_its_release_and_after_the_one_before(self):
        ordered = sorted(["0.1.4", "0.1.4-pre.10", "0.1.3", "0.1.4-pre.2", "0.1.5-pre.1"], key=versions.order)
        self.assertEqual(ordered, ["0.1.3", "0.1.4-pre.2", "0.1.4-pre.10", "0.1.4", "0.1.5-pre.1"])


class Tags(unittest.TestCase):
    def setUp(self):
        self.tags = versions.parse_tags("\n".join(support.fixture("upstream.json")["tags"]))

    def test_the_peeled_commit_of_an_annotated_tag_wins(self):
        # v0.1.0-pre.1 is annotated upstream: the first line is the tag object.
        self.assertEqual(self.tags["v0.1.0-pre.1"], "f54a7a617760295cc101d6ec7f31df1dba50fc23")
        self.assertEqual(self.tags["v0.1.3"], "e1f3c82caa3ed3b65de22889ae7ef32a774878ef")

    def test_only_release_shaped_tags_are_releases(self):
        names = [release["tag"] for release in versions.releases(self.tags)]
        self.assertNotIn("dev", names)
        self.assertNotIn("vm-runtime", names)
        self.assertEqual(names[0], "v0.1.4-pre.2", "newest first")
        self.assertEqual(names[-1], "v0.0.116")

    def test_a_release_says_whether_it_is_stable_and_where_it_points(self):
        by_version = {release["version"]: release for release in versions.releases(self.tags)}
        self.assertTrue(by_version["0.1.3"]["stable"])
        self.assertFalse(by_version["0.1.4-pre.2"]["stable"])
        self.assertEqual(by_version["0.1.4-pre.2"]["commit"], "4c1b16a4a104581fb0afe8675feff34f00cc2ca8")

    def test_junk_tags_are_ignored_not_guessed_at(self):
        junk = (
            "dev",
            "vm-runtime",
            "latest",
            "0.1.9",  # no v
            "v0.1.9-rc.1",
            "v0.1.9-dev.3",
            "v0.1.9-pre",
            "v0.1.9.1",
            "release-0.1.9",
            "sdk/go/v0.1.9",
            "v0.1.9-pre.1-hotfix",
        )
        self.assertEqual(found(*junk), [])
        self.assertIsNone(target("0.1.3", *junk))
        # And they do not hide a real one standing next to them.
        self.assertEqual(target("0.1.3", "v0.1.4", *junk), "0.1.4")

    def test_text_that_is_not_a_tag_line_is_skipped(self):
        text = "warning: redirecting\n\n" + "a" * 40 + "\trefs/tags/v1.0.0\nnot a line\n" + "b" * 40 + "\trefs/heads/main\n"
        self.assertEqual(versions.parse_tags(text), {"v1.0.0": "a" * 40})


class SelectTarget(unittest.TestCase):
    def test_upstream_today(self):
        tags = versions.parse_tags("\n".join(support.fixture("upstream.json")["tags"]))
        chosen = versions.select_target("0.1.3", versions.releases(tags))
        self.assertEqual(chosen["version"], "0.1.4-pre.2")
        self.assertEqual(chosen["tag"], "v0.1.4-pre.2")
        self.assertFalse(chosen["stable"])

    def test_a_stable_release_above_the_pin(self):
        self.assertEqual(target("0.1.3", "v0.1.3", "v0.1.4"), "0.1.4")

    def test_several_stable_releases_waiting_goes_straight_to_the_newest(self):
        self.assertEqual(target("0.1.3", "v0.1.4", "v0.1.5", "v0.1.6", "v0.1.2"), "0.1.6")
        self.assertEqual(target("0.1.3", "v0.1.4", "v0.2.0", "v0.1.9"), "0.2.0")

    def test_a_stable_release_wins_over_any_pre_release(self):
        # Even a pre-release of something newer still: stable first, then the
        # next run looks at what leads up to the release after it.
        self.assertEqual(target("0.1.3", "v0.1.4", "v0.1.4-pre.9", "v0.1.5-pre.1", "v0.2.0-pre.3"), "0.1.4")

    def test_only_pre_releases_takes_the_newest(self):
        self.assertEqual(target("0.1.3", "v0.1.4-pre.1", "v0.1.4-pre.2", "v0.1.4-pre.10"), "0.1.4-pre.10")
        self.assertEqual(target("0.1.3", "v0.1.4-pre.7", "v0.2.0-pre.1"), "0.2.0-pre.1")

    def test_a_pre_release_whose_base_is_not_above_the_pin_is_ignored(self):
        # Pre-releases of the release main is already on, and of older ones.
        self.assertIsNone(target("0.1.3", "v0.1.3-pre.8", "v0.1.3-pre.1", "v0.1.2-pre.4", "v0.1.0-pre.12"))
        # ...and they do not get in the way of one that counts.
        self.assertEqual(target("0.1.3", "v0.1.3-pre.8", "v0.1.4-pre.1"), "0.1.4-pre.1")

    def test_nothing_newer_is_no_target(self):
        self.assertIsNone(target("0.1.3", "v0.1.3", "v0.1.2", "v0.0.116"))
        self.assertIsNone(target("0.1.3"))

    def test_the_pin_itself_is_never_the_target(self):
        self.assertIsNone(target("0.1.3", "v0.1.3"))

    def test_a_branch_on_a_pre_release_follows_the_same_rules(self):
        # A prep branch is not what the target is chosen for, but the rule holds there.
        self.assertEqual(target("0.1.4-pre.2", "v0.1.4-pre.2", "v0.1.4-pre.3"), "0.1.4-pre.3")
        self.assertEqual(target("0.1.4-pre.2", "v0.1.4-pre.3", "v0.1.4"), "0.1.4")
        self.assertIsNone(target("0.1.4-pre.2", "v0.1.4-pre.1", "v0.1.4-pre.2"))

    def test_a_pin_that_is_not_a_release_is_an_error(self):
        with self.assertRaises(versions.VersionError):
            versions.select_target("dev", found("v0.1.4"))


if __name__ == "__main__":
    unittest.main()
