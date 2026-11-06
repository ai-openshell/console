"""A bump branch: main plus one commit that moves the pins.

The bump is the move to an upstream release (ADR 0005, decision 7, as amended
on 2026-11-06). This file makes that commit in a scratch worktree, on a
detached HEAD started from main. It never pushes and never touches a remote:
the result is a commit, recorded under a local ref, and a description of what
happened. Publishing is a separate step (publish.py).

The bot makes a bump branch once and never touches it again. It does not
rebase it, move its pin or force-push it: from the moment it exists it is an
ordinary branch that people push fixes to. The commit carries an
`OpenShell-Release:` trailer naming the release it pins.
"""

import os
import shutil
import subprocess
import tempfile

import pinmove

# Where the prepared commit is kept until it is pushed. A commit made on a
# detached HEAD in a worktree that is then removed is otherwise unreachable.
LOCAL_REF = "refs/upstream/bump"

TRAILER = "OpenShell-Release"

BOT_NAME = "github-actions[bot]"
BOT_EMAIL = "41898282+github-actions[bot]@users.noreply.github.com"

# Settings a rebase or a commit can pick up from whoever runs this. They are
# pinned so that a run on a laptop behaves like a run on a runner: no editor,
# no hook, no signing prompt, no conflict resolution remembered from earlier.
GIT_CONFIG = (
    "user.name=" + BOT_NAME,
    "user.email=" + BOT_EMAIL,
    "commit.gpgsign=false",
    "core.hooksPath=/dev/null",
    "core.editor=true",
    "sequence.editor=true",
    "rerere.enabled=false",
    "rebase.autoStash=false",
    "rebase.updateRefs=false",
    "advice.detachedHead=false",
)

class GitError(Exception):
    """git did something this file does not know how to carry on from."""


class Git(object):
    """git in one directory, with the settings above and nothing interactive."""

    def __init__(self, cwd):
        self.cwd = cwd

    def run(self, *args, **kwargs):
        check = kwargs.pop("check", True)
        # A hook or a wrapper may export GIT_DIR and friends, which would point
        # every command at some other repository.
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(GIT_EDITOR="true", GIT_SEQUENCE_EDITOR="true", GIT_TERMINAL_PROMPT="0")
        command = ["git"]
        for setting in GIT_CONFIG:
            command += ["-c", setting]
        command += list(args)
        done = subprocess.run(
            command, cwd=self.cwd, env=env, universal_newlines=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        if check and done.returncode != 0:
            raise GitError("`git %s` failed in %s:\n%s%s" % (" ".join(args), self.cwd, done.stdout, done.stderr))
        return done

    def out(self, *args):
        return self.run(*args).stdout.strip()

    def maybe(self, *args):
        """The command's output, or None when it failed."""
        done = self.run(*args, check=False)
        return done.stdout.strip() if done.returncode == 0 else None


def _stage_pin_move(git):
    """Stage the pin move's files. True when there is something to commit.

    The move may change PIN_PATHS and nothing else. A change anywhere else
    means a tool did something it was not asked to, and it is not committed.
    """
    changed = [text[3:] for text in git.run("status", "--porcelain").stdout.splitlines() if text.strip()]
    foreign = [path for path in changed if path not in pinmove.PIN_PATHS]
    if foreign:
        raise GitError(
            "the pin move changed files it must not: %s. Nothing was committed." % ", ".join(sorted(foreign))
        )
    if not changed:
        return False
    git.run("add", "--", *changed)
    return git.run("diff", "--cached", "--quiet", check=False).returncode != 0


def _commit(git, message):
    """Commit what is staged, signed off, with exactly this message."""
    directory = tempfile.mkdtemp(prefix="upstream-message-")
    try:
        path = os.path.join(directory, "message")
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(message)
        # -s: this repository requires a DCO sign-off on every commit.
        git.run("commit", "-s", "-q", "-F", path)
    finally:
        shutil.rmtree(directory, ignore_errors=True)


def prepare(repo, target, mover, message, main_ref="HEAD"):
    """Make main plus the pin move for `target`, and record it under LOCAL_REF.

    repo: a clone that has `main_ref`.
    target: the plan's target.
    mover(tree, target): the pin move (pinmove.apply with its tools bound).
    message(target, moved): the commit message, trailer included.

    Returns {"action": "create" | "none", "reason", "main", "sha", "moved"}.
    `none` means the move changed nothing: main already pins the target.
    """
    git = Git(repo)
    main = git.out("rev-parse", "--verify", "%s^{commit}" % main_ref)
    outcome = {"action": "none", "reason": "", "main": main, "sha": None, "moved": None}
    scratch = tempfile.mkdtemp(prefix="upstream-bump-")
    tree = os.path.join(scratch, "tree")
    git.run("worktree", "add", "--detach", tree, main)
    work = Git(tree)
    try:
        moved = mover(tree, target)
        outcome["moved"] = moved
        if not _stage_pin_move(work):
            outcome["reason"] = "main already pins OpenShell %s" % target["version"]
            return outcome
        _commit(work, message(target, moved))
        sha = work.out("rev-parse", "HEAD")
        git.run("update-ref", LOCAL_REF, sha)
        outcome.update(action="create", sha=sha, reason="main plus the move to OpenShell %s" % target["version"])
        return outcome
    finally:
        git.run("worktree", "remove", "--force", tree, check=False)
        shutil.rmtree(scratch, ignore_errors=True)
        git.run("worktree", "prune", check=False)
