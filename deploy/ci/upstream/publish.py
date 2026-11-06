"""Putting a bump on GitHub: the branch and its pull request.

Split in two on purpose.

build() decides. It reads what the `bump` job prepared and what GitHub says
about pull requests from the bump branch, and writes down every step to take.
It runs without a token.

execute() acts. It is handed the token, so it does as little as possible: it
runs `git push` and `gh` with exactly the arguments build() chose, in a fixed
order. It runs nothing else, and a test holds it to that.

The bot makes a bump branch once. When the branch exists already, or any pull
request has ever come from it in this repository (open, closed or merged), it
does nothing: an open one is people's to finish, a closed one means "not this
one", and a merged one is done.
"""

import os
import shutil
import subprocess
import tempfile

import report

BASE = report.BASE
REMOTE = "origin"

# Hands git the token for one command, from the environment: it is never
# written to the git config and never appears on a command line.
CREDENTIAL_HELPER = '!f() { echo "username=x-access-token"; echo "password=${GH_TOKEN}"; }; f'


class PublishError(Exception):
    """A step failed in a way a person has to look at."""


def own_pulls(pulls, branch):
    """The pull requests from `branch` IN THIS REPOSITORY, in any state.

    pulls: what `gh pr list --head <branch> --state all --json
    number,headRefName,isCrossRepository,state,url` printed. `--head` matches
    the branch NAME in this repository and in every fork, so a pull request is
    ours only when GitHub says outright that it is not cross-repository.
    """
    return [
        pull
        for pull in pulls or []
        if pull.get("isCrossRepository") is False
        and pull.get("headRefName") == branch
        and isinstance(pull.get("number"), int)
    ]


def blocked(plan, pulls=None, branch_sha=None):
    """Why there is nothing to bump, or None when the bump branch should be made.

    branch_sha: where the bump branch is on the remote, or None when it does
    not exist. Checked before any pin is moved.
    """
    target = plan.get("target") or {}
    branch = target.get("branch")
    if not branch or not target.get("bump"):
        return "there is nothing to bump to"
    if not target.get("ready"):
        return target.get("waiting_for") or "the release's images are not published yet"
    own = own_pulls(pulls, branch)
    if own:
        first = min(own, key=lambda pull: pull["number"])
        return "pull request #%d (%s) already came from `%s`" % (
            first["number"],
            str(first.get("state") or "?").lower(),
            branch,
        )
    if branch_sha:
        return "`%s` exists already (at %s) with no pull request from it; open one, or delete the branch" % (
            branch,
            branch_sha[:12],
        )
    return None


def build(plan, outcome, pulls=None, branch_sha=None, repo_url=""):
    """Every step to take on GitHub for what the `bump` job prepared, as plain data."""
    target = plan.get("target") or {}
    branch = target.get("branch")
    publish = {"branch": branch, "skip": None, "push": None, "pr": None, "compare": None}
    publish["skip"] = blocked(plan, pulls, branch_sha)
    if publish["skip"]:
        return publish
    if outcome.get("action") != "create":
        publish["skip"] = outcome.get("reason") or "the move changed nothing"
        return publish

    publish["push"] = {"sha": outcome["sha"]}
    publish["pr"] = {"title": report.pr_title(target["version"]), "body": report.pr_body(plan, repo_url)}
    if repo_url:
        publish["compare"] = "%s/compare/%s...%s?expand=1" % (repo_url.rstrip("/"), BASE, branch)
    return publish


def run_command(command, env=None):
    """Run one command; (exit code, stdout, stderr). Tests replace this."""
    try:
        done = subprocess.run(command, env=env, universal_newlines=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    except OSError as err:
        return 127, "", "%s: %s" % (command[0], err)
    return done.returncode, done.stdout, done.stderr


def _push_command(*args):
    return ["git", "-c", "credential.helper=", "-c", "credential.helper=" + CREDENTIAL_HELPER, "push"] + list(args)


def _is_allowed(command):
    """git push, or gh. Nothing else is run beside the token."""
    if command[:1] == ["gh"]:
        return True
    if command[:1] != ["git"]:
        return False
    rest = command[1:]
    while rest[:1] == ["-c"]:
        rest = rest[2:]
    return rest[:1] == ["push"]


class _Runner(object):
    """Runs a command if it is allowed, and logs it without the credential helper."""

    def __init__(self, run, echo, dry_run):
        self.run, self.echo, self.dry_run = run, echo, dry_run
        self.directory = tempfile.mkdtemp(prefix="upstream-publish-")
        self.files = 0

    def file(self, text):
        self.files += 1
        path = os.path.join(self.directory, "text-%d.md" % self.files)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(text)
        return path

    def __call__(self, command):
        if not _is_allowed(command):
            raise PublishError("refusing to run %r beside the token: only `git push` and `gh` are run here" % (command,))
        words = [word for word in command if not word.startswith("credential.helper=") and word != "-c"]
        self.echo("%s %s" % ("would run:" if self.dry_run else "$", " ".join(words)))
        if self.dry_run:
            return 0, "", ""
        code, out, err = self.run(command)
        for text in (out, err):
            if text.strip():
                self.echo(text.rstrip())
        return code, out, err


def execute(publish, run=None, echo=print, dry_run=False):
    """Carry out what build() decided. Returns notes for the run summary.

    Raises PublishError when a step fails and a person has to look.
    """
    if publish.get("skip"):
        return ["Nothing to publish: %s." % publish["skip"]]
    call = _Runner(run or run_command, echo, dry_run)
    try:
        return _steps(publish, call, dry_run)
    finally:
        # The pull request body was written to a file for gh.
        shutil.rmtree(call.directory, ignore_errors=True)


def _steps(publish, call, dry_run):
    notes = []
    branch = publish["branch"]

    # An empty lease: the branch must not exist. The bot never moves one.
    push = publish["push"]
    code, _, err = call(_push_command("--force-with-lease=refs/heads/%s:" % branch, REMOTE, "%s:refs/heads/%s" % (push["sha"], branch)))
    if code != 0:
        if "stale info" in err:
            notes.append("`%s` appeared while this run was working, so nothing was pushed." % branch)
            return notes
        raise PublishError("could not push %s:\n%s" % (branch, err.strip()))
    if not dry_run:
        notes.append("Pushed `%s` at %s." % (branch, push["sha"][:12]))

    # Opened with the bump-bot App's token, the pull request starts CI like
    # any other.
    pull = publish["pr"]
    code, out, err = call(
        ["gh", "pr", "create", "--head", branch, "--base", BASE, "--title", pull["title"], "--body-file", call.file(pull["body"])]
    )
    if code != 0:
        raise PublishError(
            "GitHub refused to open the pull request %s -> %s:\n%s\n"
            "The branch is pushed; open the pull request by hand%s. If this keeps happening, check that "
            "the bump-bot App is installed on this repository with Pull requests: write."
            % (branch, BASE, err.strip(), ": " + publish["compare"] if publish.get("compare") else "")
        )
    if not dry_run:
        notes.append("Opened %s." % (out.strip() or "the pull request"))
    return notes
