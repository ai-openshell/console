"""Publishing a bump: what is decided without a token, and what is run with one."""

import unittest

import publish
from tests import support

MAIN = "a" * 40
NEW = "c" * 40
REPO_URL = "https://github.com/ai-openshell/console"
BRANCH = "bump/openshell-0.1.4"


def plan_for(target, pinned="0.1.3", bump=True):
    made = {
        "pinned": {"release": pinned, "line": ".".join(pinned.split(".")[:2]), "stable": True, "sdk": "v0.0.0-20261009050449-e1f3c82caa3e", "sdk_on_tag": True},
        "config_schema": "v2",
        "sandbox_image": support.SANDBOX,
        "newest_stable": pinned,
        "requested": None,
        "target": None,
    }
    if target:
        made["target"] = dict(
            target,
            same_line=target["line"] == made["pinned"]["line"],
            skipped=[],
            bump=bump,
            branch="bump/openshell-" + target["version"],
        )
    return made


def created(sha=NEW):
    return {"action": "create", "reason": "", "main": MAIN, "sha": sha, "moved": None}


def pull(number=12, state="OPEN", branch=BRANCH, cross=False):
    return {"number": number, "headRefName": branch, "isCrossRepository": cross, "state": state}


STABLE = support.target("0.1.4")


class Blocked(unittest.TestCase):
    def test_nothing_to_bump_to(self):
        self.assertEqual(publish.blocked(plan_for(None)), "there is nothing to bump to")
        self.assertEqual(publish.blocked(plan_for(STABLE, bump=False)), "there is nothing to bump to")

    def test_images_not_out_yet(self):
        waiting = dict(STABLE, ready=False, waiting_for="upstream has tagged v0.1.4 but has not published its gateway image yet")
        self.assertIn("has not published", publish.blocked(plan_for(waiting)))

    def test_any_pull_request_from_the_branch_in_this_repository(self):
        for state in ("OPEN", "CLOSED", "MERGED"):
            self.assertEqual(
                publish.blocked(plan_for(STABLE), [pull(state=state)]),
                "pull request #12 (%s) already came from `%s`" % (state.lower(), BRANCH),
            )

    def test_the_oldest_is_named(self):
        self.assertIn("#7", publish.blocked(plan_for(STABLE), [pull(12), pull(7, "CLOSED")]))

    def test_a_fork_or_another_branch_does_not_count(self):
        self.assertIsNone(publish.blocked(plan_for(STABLE), [pull(cross=True), pull(branch="bump/openshell-0.1.5")]))
        # An entry that does not say it is not cross-repository is not shown to be ours.
        self.assertIsNone(publish.blocked(plan_for(STABLE), [{"number": 3, "headRefName": BRANCH}]))

    def test_a_branch_that_exists_without_a_pull_request(self):
        self.assertIn("exists already (at %s)" % ("d" * 12), publish.blocked(plan_for(STABLE), [], "d" * 40))

    def test_otherwise_it_goes_ahead(self):
        self.assertIsNone(publish.blocked(plan_for(STABLE), [], None))


class Decide(unittest.TestCase):
    def test_a_new_bump_is_pushed_and_a_pull_request_opened(self):
        decided = publish.build(plan_for(STABLE), created(), [], None, REPO_URL)
        self.assertIsNone(decided["skip"])
        self.assertEqual(decided["branch"], BRANCH)
        self.assertEqual(decided["push"], {"sha": NEW})
        self.assertEqual(decided["pr"]["title"], "fix: move to OpenShell 0.1.4")
        self.assertIn("will not touch it again", decided["pr"]["body"])
        self.assertEqual(decided["compare"], REPO_URL + "/compare/main...%s?expand=1" % BRANCH)

    def test_a_blocked_bump_decides_nothing(self):
        decided = publish.build(plan_for(STABLE), created(), [pull()], None, REPO_URL)
        self.assertTrue(decided["skip"])
        self.assertEqual((decided["push"], decided["pr"]), (None, None))

    def test_a_move_that_changed_nothing_decides_nothing(self):
        outcome = {"action": "none", "reason": "main already pins OpenShell 0.1.4", "main": MAIN, "sha": None, "moved": None}
        decided = publish.build(plan_for(STABLE), outcome, [], None)
        self.assertEqual(decided["skip"], "main already pins OpenShell 0.1.4")
        self.assertIsNone(decided["push"])


class FakeGitHub(object):
    """Stands in for `git push` and `gh`: records every command, and fails the ones it is told to."""

    def __init__(self, fail=None):
        self.commands = []
        self.fail = fail or {}

    def __call__(self, command, env=None):
        self.commands.append(list(command))
        text = " ".join(command)
        for needle, answer in self.fail.items():
            if needle in text:
                return answer
        if command[:3] == ["gh", "pr", "create"]:
            return 0, REPO_URL + "/pull/41\n", ""
        return 0, "", ""

    def shown(self):
        """Each command without the credential plumbing, as one string."""
        out = []
        for command in self.commands:
            words = [word for word in command if word != "-c" and not word.startswith("credential.helper=")]
            out.append(" ".join(words))
        return out


class Execute(unittest.TestCase):
    def run_it(self, decided, fail=None, dry_run=False):
        github = FakeGitHub(fail)
        log = []
        try:
            notes = publish.execute(decided, run=github, echo=log.append, dry_run=dry_run)
            return github, notes, log, None
        except publish.PublishError as err:
            return github, [], log, err

    def decided(self):
        return publish.build(plan_for(STABLE), created(), [], None, REPO_URL)

    def test_push_with_an_empty_lease_then_open_the_pull_request(self):
        github, notes, _, error = self.run_it(self.decided())
        self.assertIsNone(error)
        shown = github.shown()
        self.assertEqual(len(shown), 2)
        self.assertEqual(shown[0], "git push --force-with-lease=refs/heads/%s: origin %s:refs/heads/%s" % (BRANCH, NEW, BRANCH))
        self.assertTrue(shown[1].startswith("gh pr create --head %s --base main --title fix: move to OpenShell 0.1.4 --body-file " % BRANCH))
        self.assertIn("Opened %s/pull/41." % REPO_URL, notes)

    def test_a_skip_runs_nothing(self):
        github, notes, _, error = self.run_it(publish.build(plan_for(STABLE), created(), [pull()], None))
        self.assertIsNone(error)
        self.assertEqual(github.commands, [])
        self.assertTrue(notes[0].startswith("Nothing to publish: pull request #12"))

    def test_nothing_ever_force_pushes_over_a_branch_or_deletes_one(self):
        github, _, _, _ = self.run_it(self.decided())
        for command in github.shown():
            if command.startswith("git push"):
                self.assertIn("--force-with-lease=refs/heads/%s: " % BRANCH, command, "the lease expects no branch")
                self.assertNotIn(" :refs/heads/", command)
                self.assertNotIn("--force ", command)
            self.assertNotIn("pr merge", command)
            self.assertNotIn("pr close", command)

    def test_only_git_push_and_gh_are_ever_run(self):
        github, _, _, _ = self.run_it(self.decided())
        for command in github.commands:
            self.assertTrue(publish._is_allowed(command), command)
            self.assertIn(command[0], ("git", "gh"))
            if command[0] == "git":
                self.assertIn("push", command)
                for word in ("commit", "rebase", "fetch", "config", "clone"):
                    self.assertNotIn(word, command)

    def test_anything_else_is_refused_before_it_runs(self):
        for command in (["go", "mod", "tidy"], ["git", "commit", "-m", "x"], ["git", "-c", "a=b", "fetch"], ["python3", "x.py"], ["sh", "-c", "gh"]):
            self.assertFalse(publish._is_allowed(command), command)
        runner = publish._Runner(FakeGitHub(), lambda text: None, dry_run=False)
        with self.assertRaises(publish.PublishError):
            runner(["node", "script.mjs"])

    def test_the_token_reaches_git_through_the_environment_only(self):
        github, _, _, _ = self.run_it(self.decided())
        push = github.commands[0]
        self.assertEqual(push[:5], ["git", "-c", "credential.helper=", "-c", "credential.helper=" + publish.CREDENTIAL_HELPER])
        self.assertIn('echo "password=${GH_TOKEN}"', publish.CREDENTIAL_HELPER)
        for command in github.commands:
            for word in command:
                self.assertNotIn("x-access-token:", word)
                self.assertNotIn("extraheader", word)

    def test_a_branch_that_appeared_meanwhile_stands_down_without_an_error(self):
        github, notes, _, error = self.run_it(self.decided(), fail={"push --force-with-lease": (1, "", "! [rejected] (stale info)")})
        self.assertIsNone(error)
        self.assertEqual(len(github.commands), 1, "no pull request for a branch this run did not push")
        self.assertIn("appeared while this run was working", notes[0])

    def test_any_other_push_failure_is_an_error_and_nothing_follows(self):
        github, _, _, error = self.run_it(self.decided(), fail={"push --force-with-lease": (1, "", "remote: Permission denied")})
        self.assertIn("could not push %s" % BRANCH, str(error))
        self.assertEqual(len(github.commands), 1)

    def test_a_refused_pull_request_is_an_error_that_gives_the_link(self):
        github, _, _, error = self.run_it(self.decided(), fail={"gh pr create": (1, "", "HTTP 403: Resource not accessible by integration")})
        text = str(error)
        self.assertIn(REPO_URL + "/compare/main...%s?expand=1" % BRANCH, text)
        self.assertIn("The branch is pushed", text)
        self.assertIn("bump-bot App", text)
        # The branch is not taken away again.
        self.assertEqual(len(github.commands), 2)

    def test_a_dry_run_runs_nothing_and_says_what_it_would(self):
        github, notes, log, error = self.run_it(self.decided(), dry_run=True)
        self.assertIsNone(error)
        self.assertEqual(github.commands, [])
        self.assertEqual(notes, [])
        self.assertEqual(len([line for line in log if line.startswith("would run: ")]), 2)


if __name__ == "__main__":
    unittest.main()
