"""The real Upstream object, with the subprocess replaced.

Everything else in this directory uses a fake Upstream. These tests cover the
one thing a fake cannot: that the real one tells "does not exist" (a normal
answer) from "could not ask" (an error that must stop the run).
"""

import json
import subprocess
import unittest
from unittest import mock

import upstream

DIGEST = "sha256:" + "ab" * 32
REFERENCE = "ghcr.io/nvidia/openshell/gateway:0.1.4"


def manifest(digest=DIGEST):
    return json.dumps({"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "digest": digest})


class ReadInspect(unittest.TestCase):
    def test_returns_the_digest_of_the_index(self):
        self.assertEqual(upstream.read_inspect(REFERENCE, 0, manifest(), ""), DIGEST)

    def test_a_missing_tag_is_none(self):
        # buildx words the registry's 404 exactly like this.
        self.assertIsNone(upstream.read_inspect(REFERENCE, 1, "", "ERROR: %s: not found\n" % REFERENCE))

    def test_any_other_failure_is_an_error_not_a_missing_tag(self):
        for stderr in (
            "ERROR: unexpected status from HEAD request to https://ghcr.io/v2/x/manifests/y: 503 Service Unavailable",
            "ERROR: failed to authorize: 401 Unauthorized",
            "dial tcp: lookup ghcr.io: no such host",
            "docker: 'buildx' is not a docker command.",
            # "not found" in the middle of a line is some other message.
            "ERROR: not found: the docker daemon socket",
            "",
        ):
            with self.assertRaises(upstream.UpstreamError, msg=stderr):
                upstream.read_inspect(REFERENCE, 1, "", stderr)

    def test_output_that_is_not_a_manifest_is_an_error(self):
        for stdout in ("not json", "{}", json.dumps({"digest": None}), manifest("sha256:short"), manifest("md5:" + "ab" * 32)):
            with self.assertRaises(upstream.UpstreamError, msg=stdout):
                upstream.read_inspect(REFERENCE, 0, stdout, "")


class ImageDigest(unittest.TestCase):
    def lookup(self, returncode=0, stdout="", stderr=""):
        done = subprocess.CompletedProcess(args=[], returncode=returncode, stdout=stdout, stderr=stderr)
        with mock.patch("subprocess.run", return_value=done) as run:
            digest = upstream.Upstream().image_digest(upstream.GATEWAY_REPOSITORY, "0.1.4")
        return digest, run

    def test_asks_the_registry_through_buildx_and_pulls_nothing(self):
        digest, run = self.lookup(stdout=manifest())
        self.assertEqual(digest, DIGEST)
        command = run.call_args[0][0]
        self.assertEqual(
            command, ["docker", "buildx", "imagetools", "inspect", REFERENCE, "--format", "{{json .Manifest}}"]
        )

    def test_a_tag_that_is_not_published_is_none(self):
        digest, _ = self.lookup(returncode=1, stderr="ERROR: %s: not found" % REFERENCE)
        self.assertIsNone(digest)

    def test_a_registry_that_is_down_is_an_error(self):
        with self.assertRaises(upstream.UpstreamError):
            self.lookup(returncode=1, stderr="ERROR: 503 Service Unavailable")

    def test_a_missing_docker_binary_is_an_error(self):
        with mock.patch("subprocess.run", side_effect=OSError("docker: not found")):
            with self.assertRaises(upstream.UpstreamError):
                upstream.Upstream().image_digest(upstream.GATEWAY_REPOSITORY, "0.1.4")

    def test_a_lookup_that_hangs_is_an_error(self):
        with mock.patch("subprocess.run", side_effect=subprocess.TimeoutExpired(["docker"], 1)):
            with self.assertRaises(upstream.UpstreamError):
                upstream.Upstream().image_digest(upstream.SUPERVISOR_REPOSITORY, "0.1.4")


class ReleaseTags(unittest.TestCase):
    def test_lists_tags_with_their_peeled_commits(self):
        with mock.patch("subprocess.check_output", return_value="abc\trefs/tags/v0.1.2\n") as run:
            text = upstream.Upstream().release_tags()
        self.assertEqual(text, "abc\trefs/tags/v0.1.2\n")
        command = run.call_args[0][0]
        self.assertEqual(command, ["git", "ls-remote", "--tags", upstream.UPSTREAM_REPO])
        # --refs would hide the ^{} lines, which are the only ones naming a commit.
        self.assertNotIn("--refs", command)

    def test_a_failing_git_is_an_error_not_an_empty_tag_list(self):
        failure = subprocess.CalledProcessError(128, ["git"], "fatal: unable to access")
        with mock.patch("subprocess.check_output", side_effect=failure):
            with self.assertRaises(upstream.UpstreamError):
                upstream.Upstream().release_tags()

    def test_the_repository_followed_is_openshell(self):
        self.assertEqual(upstream.UPSTREAM_REPO, "https://github.com/NVIDIA/OpenShell.git")
        self.assertEqual(upstream.SDK_MODULE, "github.com/ai-openshell/sdk-go")
        self.assertEqual(upstream.SDK_REPO, "https://github.com/ai-openshell/sdk-go.git")
        self.assertEqual(upstream.LEGACY_SDK_MODULE, "github.com/NVIDIA/OpenShell/sdk/go")

    def test_sdk_tags_are_read_from_sdk_go(self):
        with mock.patch("subprocess.check_output", return_value="") as check_output:
            upstream.Upstream().sdk_tags()
        self.assertEqual(check_output.call_args[0][0], ["git", "ls-remote", "--tags", upstream.SDK_REPO])
        self.assertEqual(upstream.GATEWAY_REPOSITORY, "nvidia/openshell/gateway")
        self.assertEqual(upstream.SUPERVISOR_REPOSITORY, "nvidia/openshell/supervisor")


if __name__ == "__main__":
    unittest.main()
