"""Publishing: what is decided without a token, and what is run with one."""

import unittest

import publish
import report
from tests import support

MAIN = "a" * 40
OLD = "b" * 40
NEW = "c" * 40
REPO_URL = "https://github.com/ai-openshell/console"


def plan_for(target, pinned="0.1.3"):
    made = {
        "pinned": {"release": pinned, "line": ".".join(pinned.split(".")[:2]), "stable": True, "sdk": "v0.0.0-20261009050449-e1f3c82caa3e", "sdk_on_tag": True},
        "config_schema": "v2",
        "sandbox_image": support.SANDBOX,
        "newest_stable": pinned,
        "target": None,
    }
    if target:
        new_line = target["line"] != made["pinned"]["line"]
        made["target"] = dict(
            target,
            same_line=not new_line,
            skipped=[],
            release_branch="release/%s" % made["pinned"]["line"] if new_line and target["stable"] else None,
        )
    return made


def pins_on(target):
    return {
        "release": target["version"],
        "sdk": support.sdk_version(target["commit"]),
        "gateway_image": target["gateway_image"],
        "supervisor_image": target["supervisor_image"],
    }


def outcome_for(action, target=None, existing=None, **more):
    outcome = {
        "action": action,
        "reason": "because",
        "main": MAIN,
        "next": existing,
        "sha": NEW if action in ("create", "update") else (existing if action == "none" else None),
        "rebased": False,
        "pin_moved": action in ("create", "update"),
        "regenerated": [],
        "conflict": None,
        "people": [],
        "pins": pins_on(target) if target else None,
    }
    outcome.update(more)
    return outcome


def pull(number=7, **more):
    entry = {
        "number": number,
        "title": "fix: move to OpenShell 0.1.4-pre.1",
        "body": "",
        "isDraft": True,
        "headRefName": "next",
        "headRefOid": OLD,
        "baseRefName": "main",
        "isCrossRepository": False,
        "comments": [],
    }
    entry.update(more)
    return entry


PRE1, PRE2, STABLE, MINOR = (support.target(v) for v in ("0.1.4-pre.1", "0.1.4-pre.2", "0.1.4", "0.2.0"))
CONFLICT = {"stage": "rebase", "files": ["app.txt"], "commit": {"sha": "d" * 40, "subject": "feat: change line two"}}


class WhichPullRequestIsOurs(unittest.TestCase):
    def test_the_one_from_next_in_this_repository_into_main(self):
        self.assertEqual(publish.own_pull([pull(7)])["number"], 7)

    def test_a_fork_that_named_its_branch_next_is_not(self):
        self.assertIsNone(publish.own_pull([pull(isCrossRepository=True)]))
        # Without the field nothing shows it is ours, so it is left alone.
        entry = pull()
        del entry["isCrossRepository"]
        self.assertIsNone(publish.own_pull([entry]))

    def test_another_branch_or_another_base_is_not(self):
        self.assertIsNone(publish.own_pull([pull(headRefName="next-steps")]))
        self.assertIsNone(publish.own_pull([pull(baseRefName="release/0.1")]))
        self.assertIsNone(publish.own_pull([]))
        self.assertIsNone(publish.own_pull(None))

    def test_the_oldest_wins_and_a_fork_beside_it_does_not_count(self):
        pulls = [pull(12), pull(3, isCrossRepository=True), pull(9)]
        self.assertEqual(publish.own_pull(pulls)["number"], 9)

    def test_spent_means_merged_from_next_at_exactly_this_commit(self):
        merged = [pull(5, headRefOid=OLD)]
        self.assertTrue(publish.is_spent(merged, OLD))
        self.assertFalse(publish.is_spent(merged, NEW), "next has moved on since that merge")
        self.assertFalse(publish.is_spent([pull(5, headRefOid=OLD, isCrossRepository=True)], OLD))
        self.assertFalse(publish.is_spent(merged, None))
        self.assertFalse(publish.is_spent(None, OLD))


class Decide(unittest.TestCase):
    def build(self, action, target, pulls=None, on_branch=None, plan_target="same", **flags):
        outcome = outcome_for(action, on_branch or target, **{key: flags.pop(key) for key in ("existing", "conflict") if key in flags})
        made = plan_for(target if plan_target == "same" else plan_target)
        return publish.build(made, outcome, open_pulls=pulls, repo_url=REPO_URL, **flags)

    def test_a_new_branch_is_pushed_and_a_draft_opened_for_a_pre_release(self):
        decided = self.build("create", PRE1)
        self.assertEqual(decided["push"], {"sha": NEW, "expected": "", "created": True})
        self.assertEqual(decided["pr"]["number"], None)
        self.assertEqual(decided["pr"]["title"], "fix: move to OpenShell 0.1.4-pre.1")
        self.assertTrue(decided["pr"]["draft"])
        self.assertIsNone(decided["comment"])
        self.assertIsNone(decided["delete"])
        self.assertIsNone(decided["release_branch"])

    def test_an_update_is_leased_on_what_next_was(self):
        decided = self.build("update", PRE2, [pull()], existing=OLD)
        self.assertEqual(decided["push"], {"sha": NEW, "expected": OLD, "created": False})

    def test_nothing_is_pushed_when_nothing_changed_but_the_pull_request_is_still_kept(self):
        decided = self.build("none", PRE1, [], existing=OLD)
        self.assertIsNone(decided["push"])
        self.assertEqual(decided["pr"]["number"], None, "someone closed it: it is opened again")

    def test_a_pull_request_that_is_already_right_is_not_rewritten(self):
        first = self.build("none", PRE1, [], existing=OLD)["pr"]
        existing = pull(title=first["title"], body=first["body"].replace("\n", "\r\n"), isDraft=True)
        again = self.build("none", PRE1, [existing], existing=OLD)["pr"]
        self.assertEqual((again["number"], again["set_title"], again["set_body"], again["set_draft"]), (7, False, False, None))

    def test_the_body_is_the_same_on_every_run_that_finds_nothing_new(self):
        bodies = {self.build("none", PRE1, [], existing=OLD)["pr"]["body"] for _ in range(3)}
        self.assertEqual(len(bodies), 1)
        self.assertNotIn("/actions/runs/", bodies.pop())

    def test_a_new_target_retitles_and_rewrites(self):
        decided = self.build("update", PRE2, [pull()], existing=OLD)["pr"]
        self.assertEqual(decided["title"], "fix: move to OpenShell 0.1.4-pre.2")
        self.assertTrue(decided["set_title"] and decided["set_body"])
        self.assertIsNone(decided["set_draft"], "still a pre-release, still a draft")

    def test_the_stable_release_makes_it_ready_for_review(self):
        decided = self.build("update", STABLE, [pull()], existing=OLD)["pr"]
        self.assertEqual(decided["title"], "fix: move to OpenShell 0.1.4")
        self.assertFalse(decided["draft"])
        self.assertEqual(decided["set_draft"], "ready")

    def test_a_pull_request_someone_marked_ready_goes_back_to_draft_on_a_pre_release(self):
        decided = self.build("none", PRE1, [pull(isDraft=False)], existing=OLD)["pr"]
        self.assertEqual(decided["set_draft"], "draft")

    def test_a_fork_pull_request_from_a_branch_called_next_is_never_touched(self):
        decided = self.build("update", PRE2, [pull(isCrossRepository=True)], existing=OLD)
        self.assertIsNone(decided["pr"]["number"], "ours does not exist yet, so it is opened; theirs is left alone")

    def test_auto_merge_only_when_asked_and_only_for_a_ready_stable_move(self):
        self.assertFalse(self.build("update", STABLE, [pull()], existing=OLD)["automerge"], "off unless the variable says so")
        self.assertTrue(self.build("update", STABLE, [pull()], existing=OLD, automerge=True)["automerge"])
        self.assertTrue(self.build("none", STABLE, [pull()], existing=OLD, automerge=True)["automerge"])
        self.assertFalse(self.build("update", PRE2, [pull()], existing=OLD, automerge=True)["automerge"], "never a pre-release")
        self.assertFalse(
            self.build("conflict", STABLE, [pull()], on_branch=PRE2, existing=OLD, conflict=CONFLICT, automerge=True)["automerge"]
        )

    def test_the_body_says_how_ci_starts_and_how_it_merges(self):
        default = self.build("create", PRE1)["pr"]["body"]
        self.assertIn("CI does not start by itself", default)
        self.assertIn("close this pull request and reopen it", default)
        self.assertIn("UPSTREAM_BOT_TOKEN", default)
        self.assertIn("Nothing merges this automatically", default)
        with_token = self.build("create", PRE1, has_bot_token=True)["pr"]["body"]
        self.assertNotIn("does not start by itself", with_token)
        self.assertIn("UPSTREAM_AUTOMERGE", self.build("create", STABLE, automerge=True)["pr"]["body"])


class Conflicts(unittest.TestCase):
    def decide(self, pulls, conflict=CONFLICT):
        outcome = outcome_for("conflict", PRE1, existing=OLD, conflict=conflict)
        return publish.build(plan_for(PRE2), outcome, open_pulls=pulls), outcome

    def test_a_comment_names_the_files_and_nothing_is_pushed(self):
        decided, _ = self.decide([pull()])
        self.assertIsNone(decided["push"])
        self.assertIn("`app.txt`", decided["comment"]["body"])
        self.assertIn("feat: change line two", decided["comment"]["body"])
        self.assertIn("left exactly as it was", decided["comment"]["body"])

    def test_the_pull_request_keeps_describing_what_is_actually_on_next(self):
        decided, _ = self.decide([pull()])
        self.assertEqual(decided["pr"]["title"], "fix: move to OpenShell 0.1.4-pre.1", "not the target it could not reach")
        self.assertFalse(decided["pr"]["set_title"])

    def test_the_same_conflict_is_not_commented_on_twice(self):
        first, outcome = self.decide([pull()])
        commented = pull(comments=[{"body": "unrelated"}, {"body": first["comment"]["body"]}])
        again, _ = self.decide([commented])
        self.assertIsNone(again["comment"])

    def test_main_moving_again_is_still_the_same_conflict(self):
        first, outcome = self.decide([pull()])
        moved = dict(outcome, main="e" * 40)
        self.assertEqual(report.conflict_key(moved), report.conflict_key(outcome))

    def test_a_different_file_or_a_changed_next_is_a_new_one(self):
        first, _ = self.decide([pull()])
        commented = pull(comments=[{"body": first["comment"]["body"]}])
        other_file, _ = self.decide([commented], dict(CONFLICT, files=["app.txt", "other.txt"]))
        self.assertIsNotNone(other_file["comment"])
        outcome = outcome_for("conflict", PRE1, existing=NEW, conflict=CONFLICT)
        self.assertIsNotNone(publish.build(plan_for(PRE2), outcome, open_pulls=[commented])["comment"])

    def test_without_a_pull_request_one_is_opened_and_then_commented_on(self):
        decided, _ = self.decide([])
        self.assertIsNone(decided["pr"]["number"])
        self.assertIsNotNone(decided["comment"])


class NothingToKeep(unittest.TestCase):
    def test_delete_closes_the_pull_request_and_is_leased_on_what_next_is(self):
        outcome = outcome_for("delete", existing=OLD)
        decided = publish.build(plan_for(None), outcome, open_pulls=[pull(4)])
        self.assertEqual(decided["delete"]["expected"], OLD)
        self.assertEqual(decided["delete"]["pr"], 4)
        self.assertIn("because", decided["delete"]["comment"])
        self.assertIsNone(decided["push"])
        self.assertIsNone(decided["pr"])

    def test_leave_and_nothing_ahead_do_nothing(self):
        for action in ("leave", "none"):
            decided = publish.build(plan_for(None), outcome_for(action, existing=OLD), open_pulls=[pull()])
            self.assertEqual(
                [decided[key] for key in ("push", "pr", "comment", "delete", "release_branch")], [None] * 5, action
            )

    def test_a_target_whose_images_are_not_out_yet_does_nothing(self):
        waiting = dict(STABLE, ready=False, waiting_for="not published yet", gateway_image=None, supervisor_image=None)
        decided = publish.build(plan_for(waiting), outcome_for("none", existing=OLD), open_pulls=[pull()])
        self.assertIsNone(decided["pr"])
        self.assertIsNone(decided["push"])


class ANewMinor(unittest.TestCase):
    def test_the_branch_for_the_old_line_is_created_from_main_before_anything_else(self):
        decided = publish.build(plan_for(MINOR), outcome_for("update", MINOR, existing=OLD), open_pulls=[pull()])
        self.assertEqual(decided["release_branch"], {"name": "release/0.1", "sha": MAIN})
        body = decided["pr"]["body"]
        self.assertIn("This starts gateway release line `0.2.x`", body)
        self.assertIn("`release/0.1` was created by the run that wrote this, from `main` at `%s`" % MAIN[:12], body)

    def test_one_that_exists_is_never_moved_and_the_body_still_names_it(self):
        there = "f" * 40
        decided = publish.build(
            plan_for(MINOR), outcome_for("none", MINOR, existing=OLD), open_pulls=[pull()], release_branch_sha=there
        )
        self.assertIsNone(decided["release_branch"])
        self.assertIn("`release/0.1` exists; it was created from `main` at `%s`" % there[:12], decided["pr"]["body"])

    def test_a_pre_release_of_the_new_line_creates_none_yet(self):
        pre = support.target("0.2.0-pre.1")
        decided = publish.build(plan_for(pre), outcome_for("create", pre))
        self.assertIsNone(decided["release_branch"])
        self.assertIn("This starts gateway release line `0.2.x`", decided["pr"]["body"])


class ABranchThatCannotBeRead(unittest.TestCase):
    def test_a_next_without_readable_pins_needs_a_person(self):
        outcome = outcome_for("none", PRE1, existing=OLD, pins=None)
        with self.assertRaises(publish.PublishError):
            publish.build(plan_for(PRE1), outcome, open_pulls=[])


# --- execute ---------------------------------------------------------------


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

    def decided(self, action, target, pulls=None, **more):
        flags = {key: more.pop(key) for key in ("automerge", "release_branch_sha") if key in more}
        return publish.build(plan_for(target), outcome_for(action, target, **more), open_pulls=pulls, **flags)

    def test_create_pushes_with_an_empty_lease_then_opens_a_draft(self):
        github, notes, _, error = self.run_it(self.decided("create", PRE1))
        self.assertIsNone(error)
        shown = github.shown()
        self.assertEqual(shown[0], "git push --force-with-lease=refs/heads/next: origin %s:refs/heads/next" % NEW)
        self.assertTrue(shown[1].startswith("gh pr create --head next --base main --title fix: move to OpenShell 0.1.4-pre.1 --body-file "))
        self.assertTrue(shown[1].endswith(" --draft"))
        self.assertEqual(len(shown), 2)
        self.assertEqual(notes, ["Opened %s/pull/41." % REPO_URL])

    def test_update_is_a_force_push_with_a_lease_on_the_commit_that_was_there(self):
        github, _, _, _ = self.run_it(self.decided("update", PRE2, [pull()], existing=OLD))
        self.assertEqual(github.shown()[0], "git push --force-with-lease=refs/heads/next:%s origin %s:refs/heads/next" % (OLD, NEW))
        self.assertNotIn("--force ", github.shown()[0])

    def test_the_title_and_body_are_patched_and_the_draft_state_set(self):
        github, _, _, _ = self.run_it(self.decided("update", STABLE, [pull()], existing=OLD))
        shown = github.shown()
        self.assertTrue(shown[1].startswith("gh api --method PATCH repos/{owner}/{repo}/pulls/7 --silent -f title=fix: move to OpenShell 0.1.4 -F body=@"))
        self.assertEqual(shown[2], "gh pr ready 7")
        back = self.run_it(self.decided("none", PRE1, [pull(isDraft=False)], existing=OLD))[0].shown()
        self.assertIn("gh pr ready 7 --undo", back)

    def test_nothing_is_run_for_a_pull_request_that_is_already_right(self):
        first = self.decided("none", PRE1, [], existing=OLD)["pr"]
        decided = self.decided("none", PRE1, [pull(title=first["title"], body=first["body"])], existing=OLD)
        github, notes, _, error = self.run_it(decided)
        self.assertEqual((github.commands, notes, error), ([], [], None))

    def test_only_git_push_and_gh_are_ever_run(self):
        for decided in (
            self.decided("create", PRE1),
            self.decided("update", STABLE, [pull()], existing=OLD, automerge=True),
            self.decided("update", MINOR, [pull()], existing=OLD),
            publish.build(plan_for(PRE2), outcome_for("conflict", PRE1, existing=OLD, conflict=CONFLICT), open_pulls=[]),
            publish.build(plan_for(None), outcome_for("delete", existing=OLD), open_pulls=[pull()]),
        ):
            github, _, _, error = self.run_it(decided)
            self.assertIsNone(error)
            self.assertTrue(github.commands)
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
        github, _, _, _ = self.run_it(self.decided("create", PRE1))
        push = github.commands[0]
        self.assertEqual(push[:5], ["git", "-c", "credential.helper=", "-c", "credential.helper=" + publish.CREDENTIAL_HELPER])
        self.assertIn('echo "password=${GH_TOKEN}"', publish.CREDENTIAL_HELPER)
        for command in github.commands:
            for word in command:
                self.assertNotIn("x-access-token:", word)
                self.assertNotIn("extraheader", word)

    def test_a_refused_pull_request_takes_the_new_branch_away_again_and_names_both_remedies(self):
        refusal = (1, "", "pull request create failed: GraphQL: GitHub Actions is not permitted to create or approve pull requests")
        github, _, _, error = self.run_it(self.decided("create", PRE1), fail={"gh pr create": refusal})
        self.assertIsNotNone(error)
        self.assertEqual(github.shown()[-1], "git push --force-with-lease=refs/heads/next:%s origin :refs/heads/next" % NEW)
        message = str(error)
        self.assertIn("Allow GitHub Actions to create and approve pull requests", message)
        self.assertIn("UPSTREAM_BOT_TOKEN", message)
        self.assertIn("The branch was deleted again", message)
        self.assertIn("not permitted to create", message, "GitHub's own answer is shown")

    def test_a_refused_pull_request_leaves_a_branch_that_was_already_there(self):
        github, _, _, error = self.run_it(
            self.decided("update", PRE2, [], existing=OLD), fail={"gh pr create": (1, "", "refused")}
        )
        self.assertIsNotNone(error)
        self.assertEqual(len([text for text in github.shown() if text.startswith("git push")]), 1)
        self.assertNotIn("deleted again", str(error))

    def test_a_lease_that_no_longer_holds_stands_down_without_an_error(self):
        stale = (1, "", " ! [rejected]        %s -> next (stale info)\nerror: failed to push some refs" % NEW)
        github, notes, _, error = self.run_it(self.decided("update", PRE2, [pull()], existing=OLD), fail={"git": stale})
        self.assertIsNone(error)
        self.assertEqual(len(github.commands), 1, "the pull request is not touched either")
        self.assertIn("moved while this run was working", notes[0])

    def test_any_other_push_failure_is_an_error(self):
        github, _, _, error = self.run_it(
            self.decided("update", PRE2, [pull()], existing=OLD), fail={"git": (1, "", "remote: Permission denied")}
        )
        self.assertIn("could not push next", str(error))
        self.assertEqual(len(github.commands), 1)

    def test_a_push_refused_over_a_workflow_file_says_what_the_default_token_cannot_do(self):
        refusal = (
            1,
            "",
            "! [remote rejected] next (refusing to allow a GitHub App to create or update workflow "
            "`.github/workflows/ci.yml` without `workflows` permission)",
        )
        _, _, _, error = self.run_it(self.decided("update", PRE2, [pull()], existing=OLD), fail={"git": refusal})
        self.assertIn("UPSTREAM_BOT_TOKEN", str(error))
        self.assertIn("Workflows read/write", str(error))

    def test_a_conflict_comment_goes_on_the_pull_request_that_was_just_opened(self):
        decided = publish.build(plan_for(PRE2), outcome_for("conflict", PRE1, existing=OLD, conflict=CONFLICT), open_pulls=[])
        shown = self.run_it(decided)[0].shown()
        self.assertTrue(shown[0].startswith("gh pr create "))
        self.assertTrue(shown[1].startswith("gh pr comment 41 --body-file "))
        self.assertEqual(len(shown), 2, "and nothing was pushed")

    def test_auto_merge_is_turned_on_by_rebase_and_a_refusal_is_a_note_not_a_failure(self):
        decided = self.decided("update", STABLE, [pull()], existing=OLD, automerge=True)
        github, notes, _, error = self.run_it(decided)
        self.assertEqual(github.shown()[-2], "gh pr view 7 --json id --jq .id")
        self.assertTrue(github.shown()[-1].startswith("gh api graphql -f query=mutation($id: ID!) { enablePullRequestAutoMerge("))
        self.assertIn("mergeMethod: REBASE", github.shown()[-1])
        self.assertEqual((notes, error), ([], None))
        refusal = (1, "", "GraphQL: Pull request is in clean status (enablePullRequestAutoMerge)")
        _, notes, _, error = self.run_it(decided, fail={"enablePullRequestAutoMerge": refusal})
        self.assertIsNone(error)
        self.assertIn("Auto-merge could not be turned on", notes[0])
        self.assertIn("Pull request is in clean status", notes[0])
        self.assertIn("Allow auto-merge", notes[0])

    def test_nothing_ever_merges_a_pull_request_itself(self):
        # `gh pr merge --auto` merges on the spot when nothing is required of
        # the pull request. Only the mutation that turns auto-merge on is used.
        for decided in (
            self.decided("update", STABLE, [pull()], existing=OLD, automerge=True),
            self.decided("update", STABLE, [pull()], existing=OLD),
            self.decided("create", PRE1, automerge=True),
        ):
            for text in self.run_it(decided)[0].shown():
                self.assertNotIn("pr merge", text)
                self.assertNotIn("mergePullRequest", text)

    def test_nothing_about_merging_is_run_without_the_variable(self):
        for decided in (self.decided("update", STABLE, [pull()], existing=OLD), self.decided("create", PRE1)):
            for text in self.run_it(decided)[0].shown():
                self.assertNotIn("AutoMerge", text)

    def test_auto_merge_that_is_already_on_is_not_asked_for_again(self):
        first = self.decided("none", STABLE, [], existing=OLD)["pr"]
        on = pull(title=first["title"], body=first["body"], isDraft=False, autoMergeRequest={"mergeMethod": "REBASE"})
        decided = self.decided("none", STABLE, [on], existing=OLD, automerge=True)
        self.assertFalse(decided["automerge"])
        # The body mentions auto-merge once the variable is on, so only that is rewritten.
        self.assertFalse([text for text in self.run_it(decided)[0].shown() if "graphql" in text])

    def test_the_release_branch_is_created_first_and_only_if_it_does_not_exist(self):
        shown = self.run_it(self.decided("update", MINOR, [pull()], existing=OLD))[0].shown()
        self.assertEqual(shown[0], "git push --force-with-lease=refs/heads/release/0.1: origin %s:refs/heads/release/0.1" % MAIN)
        self.assertIn("refs/heads/next", shown[1])
        _, _, _, error = self.run_it(
            self.decided("update", MINOR, [pull()], existing=OLD), fail={"release/0.1": (1, "", "stale info")}
        )
        self.assertIn("could not create release/0.1", str(error))

    def test_delete_closes_the_pull_request_then_removes_the_branch_with_a_lease(self):
        decided = publish.build(plan_for(None), outcome_for("delete", existing=OLD), open_pulls=[pull(4)])
        shown = self.run_it(decided)[0].shown()
        self.assertTrue(shown[0].startswith("gh pr close 4 --comment Closing: because."))
        self.assertEqual(shown[1], "git push --force-with-lease=refs/heads/next:%s origin :refs/heads/next" % OLD)

    def test_a_dry_run_runs_nothing_and_says_what_it_would(self):
        github, notes, log, error = self.run_it(self.decided("create", PRE1), dry_run=True)
        self.assertEqual(github.commands, [])
        self.assertIsNone(error)
        self.assertTrue(log[0].startswith("would run: git push --force-with-lease=refs/heads/next: origin "))
        self.assertTrue(log[1].startswith("would run: gh pr create --head next --base main "))
        self.assertNotIn("credential.helper", "\n".join(log))


if __name__ == "__main__":
    unittest.main()
