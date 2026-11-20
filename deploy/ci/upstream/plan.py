"""What is upstream ahead of this branch, and what would moving to it pin?

One run of the workflow answers this once, in a job that can only read, and
every later job works from the answer: the `bump` job pins exactly the image
digests check A tested, even if upstream re-pushes a tag in between.

Two answers come out of it:

  target   the release check A tests main against. On a schedule, the newest
           stable release above the pin, else the newest pre-release leading
           up to a release above it. When a person asks for a version, that.
  bump     whether to open a bump branch for the target. A scheduled run
           bumps only to a stable release; a version a person asked for is
           bumped to whatever it is, so a pre-release gets a prep branch.

A third thing rides along and decides nothing: `api`, what a signed-in person
can reach in the target's gateway that they cannot in the pinned one, read
from the two releases' protos (apidiff.py). It is there so that the bump pull
request, the run summary and check A's issue say what is new.

Nothing here opens a socket. The network is the Upstream object passed in.
"""

import apidiff
import pins
import protoapi
import versions
from upstream import GATEWAY_REPOSITORY, REGISTRY, SDK_MODULE, SDK_URL, SUPERVISOR_REPOSITORY, UpstreamError


class PlanError(Exception):
    """The plan cannot be made from what was found."""


def _images(upstream, version):
    """(gateway ref, supervisor ref, what is missing)."""
    refs = []
    for name, repository in (("gateway", GATEWAY_REPOSITORY), ("supervisor", SUPERVISOR_REPOSITORY)):
        digest = upstream.image_digest(repository, version)
        if digest is None:
            return None, None, "upstream has tagged v%s but has not published its %s image yet" % (version, name)
        refs.append(pins.image_ref("%s/%s" % (REGISTRY, repository), version, digest))
    return refs[0], refs[1], None


def _sdk(upstream, version):
    """(the SDK to pin for this release, why there is none yet).

    The SDK for core vX.Y.Z is ai-openshell/sdk-go's tag vX.Y.Z. sdk-go cuts it
    after core releases, so a release can have no SDK tag for a while. Then
    there is nothing to bump to yet: never another SDK version, never a commit.
    """
    tag = "v" + version
    commit = versions.parse_tags(upstream.sdk_tags()).get(tag)
    if not commit:
        return None, (
            "%s has not tagged %s yet. It releases the SDK after core, and the bump waits for it"
            % (SDK_URL.split("github.com/")[1], tag)
        )
    return {"module": SDK_MODULE, "version": tag, "commit": commit}, None


def _pin_on_tag(doc, tags):
    """Is the pinned SDK the commit the pinned release's tag points at?

    None when it cannot be told: upstream deletes a train's pre-release tags
    once it goes stable, and a tagged (non-pseudo) SDK version names no commit.
    """
    tagged = tags.get("v" + doc["release"])
    commit = pins.sdk_commit(doc["sdk"])
    if not tagged or not commit:
        return None
    return tagged.startswith(commit)


BRANCH_PREFIX = "bump/openshell-"


def branch_name(version):
    """The branch a bump to this release is made on."""
    return BRANCH_PREFIX + version


def _requested(found, pinned, version):
    """The release a person asked for, checked against what upstream has."""
    if versions.parse(version) is None:
        raise PlanError(
            "%r is not an OpenShell release. Write it as X.Y.Z or X.Y.Z-pre.N, without the leading v." % version
        )
    match = [release for release in found if release["version"] == version]
    if not match:
        raise PlanError("upstream has no tag v%s. Nothing was bumped." % version)
    if versions.order(version) <= versions.order(pinned):
        raise PlanError(
            "main already pins OpenShell %s, which is not below %s. A bump only moves forward." % (pinned, version)
        )
    return match[0]


def api_changes(upstream, old_version, new_version, ledger=None, source=None):
    """What is new in the gateway's API between two releases, for the reports.

    Never raises for what it cannot read: this informs and decides nothing,
    so a tag without protos, or a proto this parser does not know, must not
    stop a bump or check A. The reports then say the comparison is missing,
    and why, which is what keeps "nothing new" from being read into silence.
    """
    out = {"from": old_version, "to": new_version, "error": None, "changes": None}
    try:
        surfaces = []
        for version in (old_version, new_version):
            files = upstream.proto_files(version)
            surfaces.append(protoapi.surface(protoapi.load(files.get)))
        out["changes"] = apidiff.annotate(apidiff.diff(*surfaces), ledger, source)
    except (UpstreamError, protoapi.ProtoError) as err:
        out["error"] = str(err)
    return out


def build(doc, upstream, requested=None, ledger=None, source=None):
    """Everything one run needs to know, as plain data.

    doc: the pins of the branch the workflow runs on, which is `main` for
    every run that acts.
    requested: a release a person asked to bump to (no leading v), or None.
    ledger, source: the coverage ledger and the dashboard's source, which say
    of each new piece of the API whether the dashboard has it. Without them
    the report lists what is new and says nothing of the dashboard.
    """
    problems = pins.validate(doc)
    if problems:
        raise pins.PinsError(problems)
    tags = versions.parse_tags(upstream.release_tags())
    found = versions.releases(tags)
    if not found:
        raise PlanError(
            "upstream lists no release tags (vX.Y.Z or vX.Y.Z-pre.N). That is an outage or a "
            "changed tag scheme, not 'nothing new'; refusing to conclude anything from it."
        )

    pinned = doc["release"]
    plan = {
        "pinned": {
            "release": pinned,
            "line": versions.line(pinned),
            "stable": versions.is_stable(pinned),
            "sdk": doc["sdk"],
            "sdk_on_tag": _pin_on_tag(doc, tags),
        },
        "config_schema": doc["config_schema"],
        "sandbox_image": doc["sandbox_image"],
        "newest_stable": next((release["version"] for release in found if release["stable"]), None),
        "requested": requested or None,
        "target": None,
    }

    if requested:
        target = _requested(found, pinned, requested)
    else:
        target = versions.select_target(pinned, found)
    if target is None:
        return plan

    gateway, supervisor, waiting_for = _images(upstream, target["version"])
    sdk, sdk_waiting_for = _sdk(upstream, target["version"])
    new_line = versions.line(target["version"]) != versions.line(pinned)
    plan["target"] = {
        "version": target["version"],
        "tag": target["tag"],
        "commit": target["commit"],
        "stable": target["stable"],
        "base": versions.base(target["version"]),
        "line": versions.line(target["version"]),
        "same_line": not new_line,
        # Stable releases between the pin and the target. They are skipped:
        # the branch goes straight to the newest.
        "skipped": sorted(
            (
                release["version"]
                for release in found
                if release["stable"]
                and target["stable"]
                and versions.order(pinned) < versions.order(release["version"]) < versions.order(target["version"])
            ),
            key=versions.order,
        ),
        # A schedule bumps only to a stable release. A version a person asked
        # for is bumped to as it is: a pre-release gets a prep branch.
        "bump": bool(requested) or target["stable"],
        "branch": branch_name(target["version"]),
        "gateway_image": gateway,
        "supervisor_image": supervisor,
        # Images to test and pin. Check A needs these and nothing else.
        "ready": waiting_for is None,
        "waiting_for": waiting_for,
        # The SDK to pin. A bump needs it as well.
        "sdk": sdk,
        "sdk_waiting_for": sdk_waiting_for,
    }
    plan["api"] = api_changes(upstream, pinned, target["version"], ledger, source)
    return plan
