"""Everything the workflow writes for a person to read.

The bump commit's message, the bump pull request, the issue check A keeps, and
the run summaries. Each says only what this run did or observed.
"""

import textwrap

import bump
import versions
from upstream import UPSTREAM_URL

ADR_PATH = "docs/adrs/0005-gateway-compatibility-and-releases.md"
WORKFLOW_PATH = ".github/workflows/follow-upstream.yml"
PINS_PATH = "deploy/ci/gateway-pins.json"

BASE = "main"

# The bump commit's title, and its pull request's. The release that follows
# the merge lists it as written. Nothing reads it: a release is cut by hand
# (publish.yml), at the version worked out from the pin
# (scripts/release/next-version.mjs).
PR_TITLE = "fix: move to OpenShell %s"

PR_MARKER = "<!-- follow-upstream:bump -->"
ISSUE_MARKER = "<!-- follow-upstream:check-a -->"
# The label of the one issue check A keeps.
ISSUE_LABEL = "compat-migrate"
ISSUE_LABEL_DESCRIPTION = "Follow upstream: main fails the compatibility suite against an upcoming OpenShell release"

PASSED = "passed"
FAILED = "failed"
PULL_FAILED = "pull_failed"
STACK_FAILED = "stack_failed"
BFF_FAILED = "bff_failed"
NO_RESULT = "no_result"

NOT_TESTED_WORDS = {
    PULL_FAILED: "an image could not be pulled, so no gateway was started",
    STACK_FAILED: "the gateway never became healthy",
    BFF_FAILED: "the gateway came up but the BFF did not become healthy in front of it",
    NO_RESULT: "the job did not report a result",
}


def _code(text):
    return "`%s`" % text


def _link(text, repo_url, path):
    """A link that works in an issue or a pull request.

    GitHub rewrites a repository-relative link only inside a rendered file.
    Anywhere else the browser resolves it against the page, which gives a
    404, so those get an absolute URL.
    """
    return "[%s](%s/blob/%s/%s)" % (text, repo_url.rstrip("/"), BASE, path) if repo_url else "%s (`%s`)" % (text, path)


def _kind(version):
    return "stable release" if versions.is_stable(version) else "pre-release"


def _tag_link(tag):
    return "[%s](%s/releases/tag/%s)" % (tag, UPSTREAM_URL, tag)


# --- the pin commit --------------------------------------------------------


def pr_title(release):
    return PR_TITLE % release


def commit_message(target, pinned):
    """The pin commit's message. The trailer is how the commit is found again."""
    version = target["version"]
    paragraphs = [
        "Moves the OpenShell release this branch is built on from %s to %s: the gateway and "
        "supervisor images the compatibility suite runs against, and the Go SDK at the commit "
        "upstream tagged %s (%s)." % (pinned["release"], version, target["tag"], target["commit"][:12])
    ]
    if target["line"] != pinned["line"]:
        paragraphs.append(
            "%s starts gateway release line %s, so the line compiled into the BFF moves from %s "
            "to %s with it." % (version, target["line"], pinned["line"], target["line"])
        )
    if not target["stable"]:
        paragraphs.append(
            "A pre-release, so this is a prep branch and cannot merge: its `pins a stable release` "
            "check fails until upstream releases %s. Fixes the move needs go on this branch." % target["base"]
        )
    body = "\n\n".join(
        textwrap.fill(paragraph, 72, break_long_words=False, break_on_hyphens=False) for paragraph in paragraphs
    )
    return "%s\n\n%s\n\n%s: %s\n" % (pr_title(version), body, bump.TRAILER, version)


# --- the bump pull request -----------------------------------------------


def pr_body(plan, repo_url=""):
    """The description of a bump pull request. Written once, when it is opened."""
    target, pinned = plan["target"], plan["pinned"]
    version = target["version"]
    new_line = not target["same_line"]
    lines = [
        PR_MARKER,
        "Opened by the %s workflow (%s). The bot made this branch and will not touch it again: push "
        "whatever the move needs to it, like any branch."
        % (_link("Follow upstream", repo_url, WORKFLOW_PATH), _link("ADR 0005", repo_url, ADR_PATH)),
        "",
        "**What moved.** One commit: %s, `backend/go.mod` and `backend/go.sum`, %sand the generated "
        "block of `README.md`, from OpenShell %s to %s (%s)."
        % (
            _code(PINS_PATH),
            "the gateway release line compiled into the BFF, " if new_line else "",
            _code(pinned["release"]),
            _code(version),
            _tag_link(target["tag"]),
        ),
        "",
        "| | Pinned here |",
        "|---|---|",
        "| release | %s |" % _code(version),
        "| gateway | %s |" % _code(target["gateway_image"]),
        "| supervisor | %s |" % _code(target["supervisor_image"]),
        "| Go SDK | at %s |" % _code(target["commit"][:12]),
    ]
    if target.get("skipped"):
        lines += [
            "",
            "Upstream also released %s since %s. This goes straight to the newest."
            % (", ".join(_code(skipped) for skipped in target["skipped"]), _code(pinned["release"])),
        ]
    if not target["stable"]:
        lines += [
            "",
            "**A pre-release: this is a prep branch.** Its `pins a stable release` check fails until "
            "upstream releases %s, which is what keeps it from merging. When the release is out, the "
            "workflow opens a bump to it; move the fixes from here onto that branch and close this one."
            % _code(target["base"]),
        ]
    if new_line:
        lines += [
            "",
            "**This starts gateway release line %s.** If the %s line still needs patch releases, make "
            "its branch from `main` before this merges: `git push origin origin/main:refs/heads/release/%s`."
            % (_code(target["line"] + ".x"), _code(pinned["line"] + ".x"), pinned["line"]),
        ]
    lines += [
        "",
        "**After it merges,** cut a release by hand: Actions > Publish > Run workflow on `main`.",
    ]
    return "\n".join(lines) + "\n"


# --- check A ---------------------------------------------------------------


def check_outcome(pull, stack, bff, compat):
    """One result from the four steps of check A, each `success`, `failure` or `skipped`.

    The first step that did not succeed names what happened. Only a failing
    compatibility suite is a compatibility result: an image that cannot be
    pulled, a gateway that never starts and a BFF that never becomes healthy
    sent the gateway nothing, so they say nothing about it.
    """
    for step, outcome in ((pull, PULL_FAILED), (stack, STACK_FAILED), (bff, BFF_FAILED)):
        if step != "success":
            return outcome
    if compat == "success":
        return PASSED
    if compat == "failure":
        return FAILED
    return NO_RESULT


def check_decision(plan, result):
    """What the run concludes from check A.

    Returns {"issue": upsert | close | keep, "incomplete": bool, "why": str}.
    Unknown is never read as a pass or a failure: it leaves the issue as it
    is and turns the run red.
    """
    target = plan.get("target")
    if target is None:
        return {
            "issue": "close",
            "incomplete": False,
            "why": "upstream has no release ahead of `main`, so there is nothing for check A to test",
        }
    if not target["ready"]:
        return {"issue": "keep", "incomplete": False, "why": target["waiting_for"]}
    outcome = (result or {}).get("outcome", NO_RESULT)
    if outcome == PASSED:
        return {
            "issue": "close",
            "incomplete": False,
            "why": "`main` passes the compatibility suite against OpenShell `%s`" % target["version"],
        }
    if outcome == FAILED:
        return {
            "issue": "upsert",
            "incomplete": False,
            "why": "`main` FAILS the compatibility suite against OpenShell `%s`" % target["version"],
        }
    return {
        "issue": "keep",
        "incomplete": True,
        "why": "check A could not be run against OpenShell `%s`: %s"
        % (target["version"], NOT_TESTED_WORDS.get(outcome, NOT_TESTED_WORDS[NO_RESULT])),
    }


def issue_text(plan, run_url="", repo_url=""):
    """Title and body of the one issue check A keeps, for a failing run."""
    target, pinned = plan["target"], plan["pinned"]
    version = target["version"]
    title = "OpenShell %s: main fails the compatibility suite" % version
    run = "[this run](%s)" % run_url if run_url else "the run that wrote this"
    lines = [
        ISSUE_MARKER,
        "Kept by %s (%s, decision 8). Rewritten in place on every run and closed when check A passes "
        "again. Do not edit it by hand: the next run overwrites it."
        % (_link("the Follow upstream workflow", repo_url, WORKFLOW_PATH), _link("ADR 0005", repo_url, ADR_PATH)),
        "",
        "**Check A failed.** The BFF built from `main` (OpenShell %s, SDK %s) does not pass "
        "`backend/test/compat` against the OpenShell %s %s. The failing tests are in %s."
        % (_code(pinned["release"]), _code(pinned["sdk"]), _code(version), _kind(version), run),
        "",
        "| | Tested |",
        "|---|---|",
        "| upstream tag | %s |" % _tag_link(target["tag"]),
        "| gateway | %s |" % _code(target["gateway_image"]),
        "| supervisor | %s |" % _code(target["supervisor_image"]),
        "",
    ]
    if target["same_line"] and target["stable"]:
        lines += [
            "### What this means",
            "",
            "%s is a stable release on %s, the release line `main` is already for. A console that is "
            "installed today and has not been touched fails against it. **Upstream broke a Stable "
            "interface inside a minor release line**, which OpenShell's release policy (RFC 0014) says "
            "it does not do, and which the console's claim to work with every gateway of its line "
            "rests on (ADR 0005, decision 10)."
            % (_code(version), _code(target["line"] + ".x")),
            "",
            "### What to do",
            "",
            "1. **Report it upstream**, at %s/issues, with the failing test and the request it sends. "
            "The remedy belongs there: a fix in the next patch release." % UPSTREAM_URL,
            "2. Until then the console stays on %s, the last release that passed. CI on the bump "
            "pull request shows whether the console built on the new SDK passes."
            % _code(pinned["release"]),
        ]
    elif target["same_line"]:
        lines += [
            "### What this means",
            "",
            "%s is a pre-release of %s, on the release line `main` is already for. If it is released "
            "as it is, a console that is installed today fails against it, and upstream will have "
            "broken a Stable interface inside a minor release line. It is a pre-release, so there is "
            "still time for that not to happen."
            % (_code(version), _code(target["base"])),
            "",
            "### What to do",
            "",
            "1. Read the failing tests. If a Stable interface changed (proto, SDK, CLI, config, "
            "policy), **report it upstream now**, at %s/issues, before %s is released."
            % (UPSTREAM_URL, _code(target["base"])),
            "2. If the change is one the console has to follow, start a prep branch: run the Follow "
            "upstream workflow by hand with `version` %s and the dry-run box cleared. It opens "
            "`%s`, where CI runs the suite on the pre-release's SDK. Or wait for the stable release, "
            "which the workflow bumps to by itself." % (_code(version), target["branch"]),
        ]
    else:
        lines += [
            "### What this means",
            "",
            "%s is on release line %s; `main` is for %s. A new minor release line is where upstream "
            "may change a Stable interface, so the console built for the old line is not expected to "
            "pass, and it tells the user so: the compatibility notice reports the new line as "
            "unsupported. This issue says what the move to the new line has to deal with."
            % (_code(version), _code(target["line"] + ".x"), _code(pinned["line"] + ".x")),
            "",
            "### What to do",
            "",
            "Wait for the stable release, which the workflow bumps to by itself, or start now: run the "
            "Follow upstream workflow by hand with `version` %s and the dry-run box cleared. It opens "
            "`%s`, where CI runs the suite with the console built on the new line's SDK. The console's "
            "minor release waits until that passes (ADR 0005, decision 2)." % (_code(version), target["branch"]),
        ]
    return title, "\n".join(lines) + "\n"


# --- run summaries ---------------------------------------------------------


def plan_summary(plan, dry_run=False):
    pinned, target = plan["pinned"], plan.get("target")
    lines = ["## Follow upstream%s" % (" (dry run: nothing is pushed, opened or edited)" if dry_run else ""), ""]
    lines.append("- This branch pins OpenShell %s (SDK %s)." % (_code(pinned["release"]), _code(pinned["sdk"])))
    if pinned["sdk_on_tag"] is False:
        lines.append(
            "- **The SDK pin is not the commit upstream tagged `v%s`.** The gateway and the SDK are "
            "meant to come from one release; someone moved one without the other." % pinned["release"]
        )
    if target is None:
        lines.append(
            "- Upstream has no release ahead of it (newest stable release: %s). Nothing to do."
            % _code(plan.get("newest_stable") or "none")
        )
        return "\n".join(lines) + "\n"
    lines.append(
        "- Target: OpenShell %s, a %s (%s, commit %s)%s."
        % (
            _code(target["version"]),
            _kind(target["version"]),
            _tag_link(target["tag"]),
            _code(target["commit"][:12]),
            ", which starts release line %s" % _code(target["line"] + ".x") if not target["same_line"] else "",
        )
    )
    if plan.get("requested"):
        lines.append("- Asked for by hand.")
    if target["skipped"]:
        lines.append("- Skipped on the way: %s." % ", ".join(_code(version) for version in target["skipped"]))
    if target["bump"]:
        lines.append("- Bump branch: %s." % _code(target["branch"]))
    else:
        lines.append(
            "- No bump: a scheduled run bumps only to a stable release. Check A still tests it. To make a "
            "prep branch, run this workflow by hand with `version` %s." % _code(target["version"])
        )
    if not target["ready"]:
        lines.append("- **Not yet:** %s. The next run looks again." % target["waiting_for"])
    else:
        lines.append("- gateway %s" % _code(target["gateway_image"]))
        lines.append("- supervisor %s" % _code(target["supervisor_image"]))
    return "\n".join(lines) + "\n"


def bump_summary(outcome, publish, dry_run=False):
    """What the `bump` job did, or in a dry run would do."""
    would = "would be " if dry_run else ""
    lines = ["### Bump", ""]
    if publish.get("skip"):
        lines.append("- Nothing %sdone: %s." % (would, publish["skip"]))
        return "\n".join(lines) + "\n"
    lines.append("- %s %spushed: main at %s plus one commit, %s." % (
        _code(publish["branch"]), would, _code(outcome["main"][:12]), _code(outcome["sha"][:12])))
    if publish.get("pr"):
        lines.append("- A pull request into `main` %sopened: %s." % (would, _code(publish["pr"]["title"])))
    return "\n".join(lines) + "\n"


def check_summary(plan, decision, dry_run=False):
    why = decision["why"][:1].upper() + decision["why"][1:]
    lines = ["### Check A: `main` against the upcoming release", "", "- %s." % why]
    said = {
        "upsert": "The issue is %s." % ("not written in a dry run" if dry_run else "written, or rewritten in place"),
        "close": "An open issue is %s." % ("not closed in a dry run" if dry_run else "closed"),
        "keep": "The issue, if there is one, is left as it is.",
    }[decision["issue"]]
    lines.append("- %s" % said)
    return "\n".join(lines) + "\n"
