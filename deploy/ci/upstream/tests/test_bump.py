"""bump.prepare: main plus one commit that moves the pins, made in a scratch worktree."""

import unittest

import bump
import pinmove
import report
from tests import support


class Prepare(unittest.TestCase):
    def setUp(self):
        self.repo = support.Repo("0.1.3")
        self.addCleanup(self.repo.close)

    def prepare(self, target, tools=None):
        tools = tools or support.FakeTools()
        pinned = {"release": "0.1.3", "line": "0.1"}
        return bump.prepare(
            self.repo.path,
            target,
            mover=lambda tree, to: pinmove.apply(tree, to, tools="/tools", run=tools),
            message=lambda to, moved: report.commit_message(to, pinned),
            main_ref="refs/remotes/origin/main",
        )

    def test_the_commit_sits_directly_on_main_and_is_kept_under_a_local_ref(self):
        outcome = self.prepare(support.target("0.1.4"))
        main = self.repo.rev("origin/main")
        self.assertEqual((outcome["action"], outcome["main"]), ("create", main))
        self.assertEqual(self.repo.rev(outcome["sha"] + "^"), main)
        self.assertEqual(self.repo.rev(bump.LOCAL_REF), outcome["sha"])

    def test_it_changes_the_pin_paths_and_nothing_else(self):
        outcome = self.prepare(support.target("0.1.4"))
        changed = set(self.repo.git("diff", "--name-only", "origin/main", outcome["sha"]).splitlines())
        self.assertTrue(changed)
        self.assertLessEqual(changed, set(pinmove.PIN_PATHS))

    def test_it_is_signed_off_by_the_bot_and_carries_the_trailer(self):
        sha = self.prepare(support.target("0.1.4"))["sha"]
        self.assertEqual(self.repo.git("log", "-1", "--format=%an", sha), bump.BOT_NAME)
        message = self.repo.git("log", "-1", "--format=%B", sha)
        self.assertIn("%s: 0.1.4" % bump.TRAILER, message)
        self.assertIn("Signed-off-by: %s <%s>" % (bump.BOT_NAME, bump.BOT_EMAIL), message)

    def test_a_tool_that_writes_outside_the_pin_paths_is_refused(self):
        with self.assertRaisesRegex(bump.GitError, "changed files it must not"):
            self.prepare(support.target("0.1.4"), tools=support.FakeTools(stray="stray.txt"))

    def test_the_working_copy_is_left_as_it_was(self):
        before = self.repo.git("status", "--porcelain")
        self.prepare(support.target("0.1.4"))
        self.assertEqual(self.repo.git("status", "--porcelain"), before)
        self.assertEqual(self.repo.git("worktree", "list").count("\n"), 0, "the scratch worktree is gone")


if __name__ == "__main__":
    unittest.main()
