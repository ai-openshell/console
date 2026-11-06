"""The pin move: put a working tree on another upstream release.

Moving to a release is one change (ADR 0005, decision 7). Everything that
comes from the release moves in it, and everything that restates the pins is
brought back in step:

    deploy/ci/gateway-pins.json                  release, images, sdk
    backend/go.mod, backend/go.sum               the SDK at the tag's commit
    backend/pkg/models/gateway_release_line.go   only when the minor changes
    README.md                                    the generated block

The SDK has no tags of its own: it is a directory of the upstream repository.
So it is pinned to the commit the release tag points at, with `go get
<module>@<commit>` and `go mod tidy`. Never `@latest`.

What runs here is `go get`, `go mod tidy`, `go list -m` and this
repository's own README script. None of them builds or executes anything from upstream, which is why
the job that holds a write token is allowed to call this.
"""

import json
import os
import re
import subprocess

import pins
import versions
from upstream import SDK_MODULE

PINS = "deploy/ci/gateway-pins.json"
GO_MOD = "backend/go.mod"
GO_SUM = "backend/go.sum"
LINE_SOURCE = "backend/pkg/models/gateway_release_line.go"
README = "README.md"
README_SCRIPT = "scripts/readme-gateway-range.mjs"

# Everything a pin move may change. The commit is made from these paths and
# no others.
PIN_PATHS = (PINS, GO_MOD, GO_SUM, LINE_SOURCE, README)

LINE_CONSTANT = "BuiltInGatewayReleaseLine"
LINE_RE = re.compile(r'^const %s = "([^"]*)"$' % LINE_CONSTANT, re.M)


class PinMoveError(Exception):
    """The move could not be made. Nothing about the target is concluded from it."""


def run_command(command, cwd):
    """Run one command; (exit code, combined output). Tests replace this."""
    try:
        done = subprocess.run(command, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    except OSError as err:
        return 127, "%s: %s" % (command[0], err)
    return done.returncode, done.stdout.decode("utf-8", errors="replace")


def _read(path):
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def _write(path, text):
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)


def must(run, command, cwd, echo):
    """Run one command and raise, with its output, when it fails."""
    if echo:
        echo("$ " + " ".join(command))
    code, output = run(command, cwd)
    if echo and output.strip():
        echo(output.rstrip())
    if code != 0:
        raise PinMoveError("`%s` failed in %s:\n%s" % (" ".join(command), cwd, output.rstrip()))


def sdk_commands(commit):
    """The two commands that move the SDK, in the order they run."""
    return [["go", "get", "%s@%s" % (SDK_MODULE, commit)], ["go", "mod", "tidy"]]


def _tagged_commit(run, backend, version, echo):
    """The commit a tagged SDK version was cut from, or None.

    From OpenShell 0.1.7 upstream tags the SDK module itself (sdk/go/vX.Y.Z),
    so `go get <module>@<commit>` resolves to that tag rather than to a
    pseudo-version, and the version no longer names the commit. `go list -m
    -json` reads the module's metadata, which records the commit the tag
    points at; it builds and runs nothing.
    """
    if pins.sdk_commit(version) or not version:
        return None
    command = ["go", "list", "-m", "-json", "%s@%s" % (SDK_MODULE, version)]
    if echo:
        echo("$ " + " ".join(command))
    code, output = run(command, backend)
    if code != 0:
        raise PinMoveError("`%s` failed in %s:\n%s" % (" ".join(command), backend, output.rstrip()))
    try:
        found = json.loads(output[output.index("{") :])
    except ValueError:
        raise PinMoveError("`%s` printed no module metadata:\n%s" % (" ".join(command), output.rstrip()))
    origin = found.get("Origin") or {}
    commit = origin.get("Hash")
    return commit[:12] if isinstance(commit, str) and len(commit) >= 12 else None


def at_target(doc, tree, target):
    """Is this tree already on the target: its release, its images and its SDK commit?

    doc: the tree's pins. The image digests count, not only the version: a
    pre-release tag that upstream pushed again is a different gateway under
    the same name.
    """
    commit = pins.sdk_commit(pins.go_mod_sdk(_read(os.path.join(tree, GO_MOD))) or "")
    return (
        doc.get("release") == target["version"]
        and doc.get("gateway_image") == target["gateway_image"]
        and doc.get("supervisor_image") == target["supervisor_image"]
        and bool(commit)
        and target["commit"].startswith(commit)
    )


def apply(tree, target, tools, run=None, echo=None):
    """Move the working tree at `tree` to `target`.

    target: the plan's target (version, commit, gateway_image, supervisor_image).
    tools: the checkout whose scripts are run. It is the commit the workflow
    itself runs from, not the tree being changed, so the job that can write
    executes nothing a branch other than `main` brought along.

    Returns {"moved": bool, "from": release, "to": release, "sdk": version,
    "line": the built-in line when it changed, else None}. Leaves the changes
    in the working tree; committing is the caller's business.
    """
    run = run or run_command
    pins_path = os.path.join(tree, PINS)
    try:
        doc = pins.load(pins_path)
    except (OSError, ValueError) as err:
        raise PinMoveError("%s cannot be read in the tree being moved: %s" % (PINS, err))
    if not isinstance(doc, dict):
        raise PinMoveError("%s in the tree being moved is not a JSON object" % PINS)
    before = doc.get("release")
    result = {"moved": False, "from": before, "to": target["version"], "sdk": doc.get("sdk"), "line": None}
    if at_target(doc, tree, target):
        return result

    backend = os.path.join(tree, "backend")
    for command in sdk_commands(target["commit"]):
        must(run, command, backend, echo)
    sdk = pins.go_mod_sdk(_read(os.path.join(tree, GO_MOD)))
    if sdk is None:
        raise PinMoveError("%s no longer requires %s after the move" % (GO_MOD, SDK_MODULE))
    commit = pins.sdk_commit(sdk) or _tagged_commit(run, backend, sdk, echo)
    if not commit or not target["commit"].startswith(commit):
        raise PinMoveError(
            "%s requires SDK %s after `go get`, which is not commit %s of %s. Nothing is pinned "
            "to an SDK other than the one at the release tag." % (GO_MOD, sdk, target["commit"][:12], target["tag"])
        )

    moved = pins.move(doc, target["version"], target["gateway_image"], target["supervisor_image"], sdk)
    problems = pins.validate(moved, schemas=pins.known_schemas(pins_path), go_mod_version=sdk)
    if problems:
        raise PinMoveError("the moved pins file would be malformed:\n" + "\n".join(problems))
    _write(pins_path, pins.dump(moved))

    # The line the BFF compares a gateway with is compiled in, and it is the
    # line of the pinned release. It changes only when the minor does.
    line = versions.line(target["version"])
    source_path = os.path.join(tree, LINE_SOURCE)
    source = _read(source_path)
    current = LINE_RE.search(source)
    if not current:
        raise PinMoveError(
            '%s does not declare the built-in line as  const %s = "X.Y"  on a line of its own, '
            "so it cannot be kept in step with the pins" % (LINE_SOURCE, LINE_CONSTANT)
        )
    if current.group(1) != line:
        _write(source_path, LINE_RE.sub('const %s = "%s"' % (LINE_CONSTANT, line), source, count=1))
        result["line"] = line

    must(
        run,
        [
            "node",
            os.path.join(tools, README_SCRIPT),
            "--write",
            "--readme",
            os.path.join(tree, README),
            "--pins",
            pins_path,
        ],
        tree,
        echo,
    )
    result.update(moved=True, sdk=sdk)
    return result
