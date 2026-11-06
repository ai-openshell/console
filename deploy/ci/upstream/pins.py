"""deploy/ci/gateway-pins.json: load it, validate it, move it to another release.

The pins file names the ONE upstream release a branch is built on (ADR 0005):
the gateway and supervisor images the compatibility suite runs against, and
the Go SDK the BFF is compiled with. They are taken from the same upstream
tag and move together.

Three rules are enforced by validate() rather than left to review:

  * images are pinned by digest (tag@sha256 - the tag is for readers, the
    digest is what gets pulled), and the tag is the release;
  * a release is `X.Y.Z` or a pre-release `X.Y.Z-pre.N`. Which branches may
    pin a pre-release is not decided here: require_stable() is the check CI
    runs for pull requests into `main` and `release/**`;
  * dev / HEAD builds are never pinned. A dev gateway resolves
    ghcr.io/nvidia/openshell/sandbox:dev at runtime, so even a digest-pinned
    dev build drifts with upstream HEAD. That is what broke main on
    2026-10-02.
"""

import copy
import json
import os
import re

import versions

REQUIRED_KEYS = ("release", "sdk", "gateway_image", "supervisor_image", "sandbox_image", "config_schema")
# Keys the file held when it listed several gateways. A branch pins one
# release, so these are refused: left in place, they would be a second
# statement that nothing reads.
RETIRED_KEYS = ("lanes", "floor", "floor_release", "ceiling", "ceiling_release", "range")

DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
SCHEMA_RE = re.compile(r"^v\d+$")
# A Go pseudo-version (what `go get module@<commit>` writes) or a tagged
# release. Never a query such as "latest": a query is not a pin.
SDK_PSEUDO_RE = re.compile(r"^v\d+\.\d+\.\d+-(?:[0-9A-Za-z.]+\.)?\d{14}-[0-9a-f]{12}$")
SDK_TAGGED_RE = re.compile(r"^v\d+\.\d+\.\d+$")
PSEUDO_COMMIT_RE = re.compile(r"[-.]\d{14}-([0-9a-f]{12})$")
SDK_MODULE = "github.com/NVIDIA/OpenShell/sdk/go"

# Tags that move. An image reference carrying one of these is not a release
# even when a digest is attached, because the gateway inside still resolves
# moving images of its own at runtime.
MOVING_TAGS = ("dev", "latest", "main", "nightly", "edge")


class PinsError(Exception):
    """The pins file is malformed. str() lists every problem, one per line."""

    def __init__(self, problems):
        self.problems = list(problems)
        Exception.__init__(self, "\n".join(self.problems))


def split_image(ref):
    """'repo:tag@sha256:...' -> (repo, tag, digest). Missing parts are ''."""
    if not isinstance(ref, str):
        return "", "", ""
    name, _, digest = ref.partition("@")
    repo, tag = name, ""
    # The last colon is a tag separator only when no '/' follows it;
    # otherwise it belongs to a registry port (localhost:5000/gateway).
    head, sep, tail = name.rpartition(":")
    if sep and "/" not in tail:
        repo, tag = head, tail
    return repo, tag, digest


def image_ref(repository, tag, digest):
    return "%s:%s@%s" % (repository, tag, digest)


def known_schemas(pins_path):
    """Config schemas the CI stack can render: one template per schema."""
    directory = os.path.dirname(os.path.abspath(pins_path))
    found = []
    for name in sorted(os.listdir(directory)):
        m = re.match(r"^gateway\.e2e\.(v\d+)\.toml\.tmpl$", name)
        if m:
            found.append(m.group(1))
    return found


def go_mod_sdk(go_mod_text):
    """The SDK version required by backend/go.mod, or None."""
    m = re.search(r"^\s*(?:require\s+)?%s\s+(\S+)" % re.escape(SDK_MODULE), go_mod_text, re.M)
    return m.group(1) if m else None


def sdk_commit(version):
    """The 12-character commit prefix inside a pseudo-version, or None."""
    m = PSEUDO_COMMIT_RE.search(version or "")
    return m.group(1) if m else None


def _is_sdk_version(value):
    return isinstance(value, str) and bool(SDK_PSEUDO_RE.match(value) or SDK_TAGGED_RE.match(value))


def _check_image(field, ref, release, problems):
    repo, tag, digest = split_image(ref)
    if not repo or not tag:
        problems.append("%s must be repository:tag@sha256:<digest>, got %r" % (field, ref))
        return
    if not DIGEST_RE.match(digest):
        problems.append(
            "%s is not pinned by digest (%r). Append @sha256:<digest>: a tag alone can be "
            "re-pushed, and then two CI runs test different gateways." % (field, ref)
        )
    if tag in MOVING_TAGS:
        problems.append(
            "%s uses the moving tag %r. dev/HEAD builds are never pinned, with or without "
            "a digest - pin a release." % (field, tag)
        )
    elif release is not None and tag != release:
        problems.append(
            "%s is tagged %r but release is %r. The gateway, the supervisor and the SDK "
            "come from one upstream release, so the tag must name it." % (field, tag, release)
        )


def validate(doc, schemas=None, go_mod_version=None):
    """Return every problem with a pins document as a list of sentences.

    An empty list means the file is well formed. Nothing is raised, so the
    caller sees all the problems at once instead of fixing them one CI run at
    a time.

    schemas: config schemas the stack has a template for (None skips the check).
    go_mod_version: the SDK version in backend/go.mod (None skips the check).
    """
    problems = []
    if not isinstance(doc, dict):
        return ["the pins file must be a JSON object"]

    # Other top-level keys are left alone, so another tool may keep its own
    # data here. Only the keys in RETIRED_KEYS are refused.
    for key in RETIRED_KEYS:
        if key in doc:
            problems.append(
                "top-level key %r is not allowed: a branch pins one upstream release "
                "(`release`, with its images and `sdk`), not a set of lanes or a range." % key
            )
    for key in REQUIRED_KEYS:
        if key not in doc:
            problems.append("missing top-level key %r" % key)

    release = doc.get("release")
    known_release = None
    if "release" in doc:
        if versions.parse(release) is not None:
            known_release = release
        elif isinstance(release, str) and ("dev" in release or release in MOVING_TAGS):
            problems.append(
                "release %r is a dev build, which is never pinned. A dev gateway resolves "
                "ghcr.io/nvidia/openshell/sandbox:dev at runtime, so even pinned by digest it "
                "drifts with upstream HEAD." % (release,)
            )
        else:
            problems.append(
                "release must be an upstream release X.Y.Z, or a pre-release X.Y.Z-pre.N on "
                "a prep branch, without the leading v. Got %r." % (release,)
            )

    sdk = doc.get("sdk")
    if "sdk" in doc:
        if not _is_sdk_version(sdk):
            problems.append(
                "sdk must be an exact module version such as "
                "v0.0.0-20260928030816-6648bd0c290e, got %r. '@latest' and branch "
                "names are queries, not pins." % (sdk,)
            )
        elif go_mod_version is not None and sdk != go_mod_version:
            problems.append(
                "sdk is %s but backend/go.mod requires %s. They are one pin "
                "recorded twice and must move in the same commit." % (sdk, go_mod_version)
            )

    for field in ("gateway_image", "supervisor_image"):
        if field in doc:
            _check_image(field, doc[field], known_release, problems)

    if "sandbox_image" in doc:
        _, _, digest = split_image(doc.get("sandbox_image"))
        if not DIGEST_RE.match(digest):
            problems.append(
                "sandbox_image is not pinned by digest (%r). The workload image is held "
                "constant while the gateway moves, which a moving tag cannot do." % (doc.get("sandbox_image"),)
            )

    if "config_schema" in doc:
        schema = doc["config_schema"]
        if not isinstance(schema, str) or not SCHEMA_RE.match(schema):
            problems.append("config_schema must look like v1 or v2, got %r" % (schema,))
        elif schemas is not None and schema not in schemas:
            problems.append(
                "config_schema %r has no template (known: %s)" % (schema, ", ".join(schemas) or "none")
            )
    return problems


def require_stable(doc):
    """Problems, if this pins document may not merge into `main` or `release/**`.

    Only a stable release is pinned there. A prep branch, a bump to a
    pre-release made by hand, is the only branch that pins one (ADR 0005,
    amendment of 2026-11-06).
    """
    release = doc.get("release") if isinstance(doc, dict) else None
    if versions.parse(release) is None:
        return ["release %r is not an upstream release, so it cannot be told whether it is stable" % (release,)]
    if versions.is_stable(release):
        return []
    return [
        "this branch pins OpenShell %s, a pre-release. `main` and `release/**` pin stable "
        "releases only. This is a prep branch: it cannot merge until upstream releases %s, when "
        "the upstream workflow opens a bump to the stable release. It says nothing about "
        "compatibility; the `compat` job does."
        % (release, versions.base(release))
    ]


def load(path):
    with open(path, encoding="utf-8") as fh:
        return json.load(fh)


def dump(doc):
    """Serialise exactly as the checked-in file is written: the canonical form.

    Round-tripping an untouched document must reproduce it byte for byte, so
    a pin move shows only the lines it meant to change. Non-ASCII text is
    written as itself, so a comment with a dash in it does not turn into an
    escape sequence the first time the file is rewritten.
    """
    return json.dumps(doc, indent=2, ensure_ascii=False) + "\n"


def is_canonical(text):
    """Is this file text exactly what dump() would write for its content?

    A pin move rewrites the whole file. Only a file that is already in
    canonical form comes back with nothing but the intended lines changed, so
    CI requires it on every PR rather than discovering the difference inside
    a move (`follow.py format-pins` rewrites a file into this form).
    """
    try:
        return dump(json.loads(text)) == text
    except ValueError:
        return False


def move(doc, release, gateway_image, supervisor_image, sdk):
    """Return a copy pinned to another upstream release.

    The four things that come from the release move together; the workload
    image and the config schema are carried over. A release that needs a new
    config schema fails the compatibility suite, and a person adds the
    template: that is not something to guess at here.
    """
    if versions.parse(release) is None:
        raise PinsError(["refusing to pin %r: only releases X.Y.Z and pre-releases X.Y.Z-pre.N are pinned" % (release,)])
    for field, ref in (("gateway_image", gateway_image), ("supervisor_image", supervisor_image)):
        repo, tag, digest = split_image(ref)
        if not repo or tag != release or not DIGEST_RE.match(digest):
            raise PinsError(["refusing to pin %s=%r: expected <repository>:%s@sha256:<digest>" % (field, ref, release)])
    if not _is_sdk_version(sdk):
        raise PinsError(["refusing to record sdk=%r: not an exact module version" % (sdk,)])
    out = copy.deepcopy(doc)
    out["release"] = release
    out["sdk"] = sdk
    out["gateway_image"] = gateway_image
    out["supervisor_image"] = supervisor_image
    return out
