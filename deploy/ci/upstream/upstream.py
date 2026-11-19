"""Everything the workflow learns from the network, behind one small object.

The decision logic never talks to git or a registry directly: it is handed an
Upstream. Tests hand it a fake built from fixtures, which is what lets every
decision be exercised without a network and without a container.

A lookup that fails for a reason other than "that does not exist" raises. An
outage must stop the run, not be mistaken for "nothing newer was released".
"""

import json
import re
import subprocess

UPSTREAM_REPO = "https://github.com/NVIDIA/OpenShell.git"
UPSTREAM_URL = "https://github.com/NVIDIA/OpenShell"
# The Go SDK is its own module, in its own repository, since OpenShell 0.1.7
# (ai-openshell/sdk-go). Its tag vX.Y.Z is the SDK for core vX.Y.Z, and it is
# cut after core releases, so for a while a core release has no SDK tag yet.
SDK_MODULE = "github.com/ai-openshell/sdk-go"
SDK_REPO = "https://github.com/ai-openshell/sdk-go.git"
SDK_URL = "https://github.com/ai-openshell/sdk-go"
# Where the SDK lived until then: a directory of the core repository. A
# go.mod that still requires it is moved off it by the next pin move.
LEGACY_SDK_MODULE = "github.com/NVIDIA/OpenShell/sdk/go"
REGISTRY = "ghcr.io"
GATEWAY_REPOSITORY = "nvidia/openshell/gateway"
SUPERVISOR_REPOSITORY = "nvidia/openshell/supervisor"

DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
# How buildx words the registry's 404, the same test scripts/retag-image.sh
# makes: "ERROR: <reference>: not found". Only a line ending that way means
# the tag is not published. Anything else it says is some other failure.
NOT_FOUND_RE = re.compile(r": not found\s*$", re.M)


class UpstreamError(Exception):
    """A lookup failed for a reason other than 'that does not exist'."""


def read_inspect(reference, returncode, stdout, stderr):
    """What `docker buildx imagetools inspect --format '{{json .Manifest}}'` said.

    Returns the digest, or None when the registry has no such tag. Raises for
    every other answer: a registry that is down is not "not released yet".
    """
    if returncode != 0:
        if NOT_FOUND_RE.search(stderr or ""):
            return None
        raise UpstreamError("%s: %s" % (reference, (stderr or "").strip() or "imagetools inspect failed"))
    try:
        digest = json.loads(stdout)["digest"]
    except (ValueError, KeyError, TypeError) as err:
        raise UpstreamError("%s: unexpected imagetools output (%s)" % (reference, err))
    if not isinstance(digest, str) or not DIGEST_RE.match(digest):
        raise UpstreamError("%s: imagetools returned no usable digest (%r)" % (reference, digest))
    return digest


class Upstream(object):
    """The real network. Each method is one question with one answer."""

    def __init__(self, timeout=120):
        self.timeout = timeout

    def _ls_remote_tags(self, repo):
        try:
            return subprocess.check_output(
                ["git", "ls-remote", "--tags", repo], universal_newlines=True, timeout=self.timeout
            )
        except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError) as err:
            raise UpstreamError("git ls-remote %s failed: %s" % (repo, err))

    def release_tags(self):
        """Raw `git ls-remote --tags` output for the upstream repository.

        Deliberately without --refs: an annotated tag is listed twice, and only
        the peeled `^{}` line names the commit.
        """
        return self._ls_remote_tags(UPSTREAM_REPO)

    def sdk_tags(self):
        """Raw `git ls-remote --tags` output for the SDK's repository, ai-openshell/sdk-go."""
        return self._ls_remote_tags(SDK_REPO)

    def image_digest(self, repository, tag):
        """'sha256:...' for repository:tag on ghcr.io, or None when the tag is not published.

        Asked of the registry through buildx, which answers with the digest of
        the multi-arch index: the one `docker pull repo@sha256:...` resolves on
        any runner. It talks to the registry only; nothing is pulled or run.
        """
        reference = "%s/%s:%s" % (REGISTRY, repository, tag)
        try:
            done = subprocess.run(
                ["docker", "buildx", "imagetools", "inspect", reference, "--format", "{{json .Manifest}}"],
                universal_newlines=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                timeout=self.timeout,
            )
        except (subprocess.TimeoutExpired, OSError) as err:
            raise UpstreamError("%s: %s" % (reference, err))
        return read_inspect(reference, done.returncode, done.stdout, done.stderr)
