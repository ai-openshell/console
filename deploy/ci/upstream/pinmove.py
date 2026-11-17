"""The pin move: put a working tree on another upstream release.

Moving to a release is one change (ADR 0005, decision 7). Everything that
comes from the release moves in it, and everything that restates the pins is
brought back in step:

    deploy/ci/gateway-pins.json                  release, images, sdk
    backend/go.mod, backend/go.sum               the SDK at its tag for the release
    backend/pkg/models/gateway_release_line.go   only when the minor changes
    README.md                                    the generated block

The SDK is its own module, github.com/ai-openshell/sdk-go, and its tag vX.Y.Z
is the SDK for core vX.Y.Z. It is pinned at that tag with `go get
<module>@vX.Y.Z` and `go mod tidy`, never `@latest`, and `go list -m -json`
confirms the tag is the commit the plan read from sdk-go. A tree still on the
SDK's old path (github.com/NVIDIA/OpenShell/sdk/go, a directory of the core
repository until 0.1.8) has its imports rewritten in the same move.

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
from upstream import LEGACY_SDK_MODULE, SDK_MODULE

PINS = "deploy/ci/gateway-pins.json"
GO_MOD = "backend/go.mod"
GO_SUM = "backend/go.sum"
LINE_SOURCE = "backend/pkg/models/gateway_release_line.go"
README = "README.md"
README_SCRIPT = "scripts/readme-gateway-range.mjs"

# Everything a pin move may change. The commit is made from these paths and
# no others, plus the Go files rewrite_imports() changed in the one move that
# takes a tree off the SDK's old path.
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


def sdk_commands(version):
    """`go get` of the SDK at its tag. `go mod tidy` runs after it, once imports are right."""
    return [["go", "get", "%s@%s" % (SDK_MODULE, version)]]


TIDY = ["go", "mod", "tidy"]


def rewrite_imports(tree):
    """Move the BFF's imports from the SDK's old path to its own module.

    The SDK left the core repository with OpenShell 0.1.7: the same packages
    under github.com/ai-openshell/sdk-go instead of
    github.com/NVIDIA/OpenShell/sdk/go. A tree that still imports the old path
    is rewritten once, in the move that takes it onto the new module, so the
    bump branch builds. Returns the paths it changed, relative to the tree.
    """
    changed = []
    backend = os.path.join(tree, "backend")
    for directory, subdirs, files in os.walk(backend):
        subdirs[:] = sorted(name for name in subdirs if not name.startswith(".") and name != "vendor")
        for name in sorted(files):
            if not name.endswith(".go"):
                continue
            path = os.path.join(directory, name)
            text = _read(path)
            if LEGACY_SDK_MODULE not in text:
                continue
            _write(path, text.replace(LEGACY_SDK_MODULE, SDK_MODULE))
            changed.append(os.path.relpath(path, tree).replace(os.sep, "/"))
    return changed


def _sdk_origin(run, backend, version, echo):
    """The commit the SDK's tag points at, as the module metadata records it, or None.

    `go list -m -json` reads the module's metadata, which records the commit
    the tag was cut from; it builds and runs nothing.
    """
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
    return commit if isinstance(commit, str) and len(commit) >= 12 else None


def at_target(doc, tree, target):
    """Is this tree already on the target: its release, its images and its SDK tag?

    doc: the tree's pins. The image digests count, not only the version: a
    pre-release tag that upstream pushed again is a different gateway under
    the same name.
    """
    module, version = pins.go_mod_sdk_module(_read(os.path.join(tree, GO_MOD)))
    sdk = target.get("sdk") or {}
    return (
        doc.get("release") == target["version"]
        and doc.get("gateway_image") == target["gateway_image"]
        and doc.get("supervisor_image") == target["supervisor_image"]
        and module == SDK_MODULE
        and bool(sdk.get("version"))
        and version == sdk["version"]
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
    result = {
        "moved": False,
        "from": before,
        "to": target["version"],
        "sdk": doc.get("sdk"),
        "line": None,
        "migrated": False,
        "rewritten": [],
    }
    if at_target(doc, tree, target):
        return result

    sdk_target = target.get("sdk") or {}
    if not sdk_target.get("version") or not sdk_target.get("commit"):
        raise PinMoveError(
            "the plan names no SDK for OpenShell %s: %s has not tagged %s yet. Nothing is moved "
            "without it." % (target["version"], SDK_MODULE, target["tag"])
        )
    backend = os.path.join(tree, "backend")
    go_mod_path = os.path.join(tree, GO_MOD)
    module_before, _ = pins.go_mod_sdk_module(_read(go_mod_path))
    if module_before is None:
        raise PinMoveError("%s requires neither %s nor %s" % (GO_MOD, SDK_MODULE, LEGACY_SDK_MODULE))
    for command in sdk_commands(sdk_target["version"]):
        must(run, command, backend, echo)
    rewritten = rewrite_imports(tree) if module_before == LEGACY_SDK_MODULE else []
    if echo and rewritten:
        echo("rewrote the SDK's imports from %s to %s in %d files" % (LEGACY_SDK_MODULE, SDK_MODULE, len(rewritten)))
    must(run, TIDY, backend, echo)

    go_mod = _read(go_mod_path)
    module, sdk = pins.go_mod_sdk_module(go_mod)
    if module != SDK_MODULE or sdk is None:
        raise PinMoveError("%s does not require %s after the move" % (GO_MOD, SDK_MODULE))
    if re.search(r"^\s*(?:require\s+)?%s\s" % re.escape(LEGACY_SDK_MODULE), go_mod, re.M):
        raise PinMoveError(
            "%s still requires %s after the move: something still imports the SDK's old path" % (GO_MOD, LEGACY_SDK_MODULE)
        )
    commit = _sdk_origin(run, backend, sdk, echo) if sdk == sdk_target["version"] else None
    if not commit or not sdk_target["commit"].startswith(commit[:12]):
        raise PinMoveError(
            "%s requires SDK %s after `go get`, which is not %s at %s (%s). Nothing is pinned to an SDK "
            "other than the one tagged for the release." % (GO_MOD, sdk, SDK_MODULE, sdk_target["version"], sdk_target["commit"][:12])
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
    result.update(moved=True, sdk=sdk, migrated=module_before == LEGACY_SDK_MODULE, rewritten=rewritten)
    return result
