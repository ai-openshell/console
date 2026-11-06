"""Shared test plumbing: a fake upstream, stand-in tools and a throwaway repository.

Nothing here opens a socket, starts a container or needs Go or Node:

  * FakeUpstream answers the two network questions from a fixture;
  * FakeTools stands in for `go get`, `go mod tidy`, `npm install` and the
    README script. Each does to a file what the real tool does to it in
    kind: a lock file is derived from its manifest, the README block from the
    pins. That is enough for a conflict in one to be a real git conflict and
    for regenerating it to be a real resolution;
  * Repo is a real git repository on disk with a bare `origin`, because what
    making a bump branch does is git, and only git can show it works.
"""

import atexit
import copy
import hashlib
import json
import os
import re
import shutil
import subprocess
import tempfile

import pinmove
import pins
import upstream

HERE = os.path.dirname(os.path.abspath(__file__))
FIXTURES = os.path.join(HERE, "fixtures")
REPO_ROOT = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))

SANDBOX = "ghcr.io/nvidia/openshell-community/sandboxes/base:latest@sha256:" + "5a" * 32
README_BEGIN = "<!-- gateway-range:begin (generated) -->"
README_END = "<!-- gateway-range:end -->"


def fixture(name):
    with open(os.path.join(FIXTURES, name), encoding="utf-8") as fh:
        return json.load(fh)


def digest(text):
    return "sha256:" + hashlib.sha256(text.encode()).hexdigest()


def commit_of(version):
    """A made-up but stable 40-character commit for a made-up release."""
    return hashlib.sha1(("commit of " + version).encode()).hexdigest()


def image(name, version, salt=""):
    return "ghcr.io/nvidia/openshell/%s:%s@%s" % (name, version, digest(name + version + salt))


def pins_doc(release="0.1.3", sdk=None):
    return {
        "_comment": ["A fixture."],
        "release": release,
        "sdk": sdk or sdk_version(commit_of(release)),
        "gateway_image": image("gateway", release),
        "supervisor_image": image("supervisor", release),
        "sandbox_image": SANDBOX,
        "config_schema": "v2",
    }


def sdk_version(commit):
    """The pseudo-version `go get <module>@<commit>` would write."""
    return "v0.0.0-20261001000000-%s" % commit[:12]


def pins_of_2026_10_09():
    """What `main` pinned on 2026-10-09: OpenShell 0.1.3, with its real digests.

    A copy, on purpose. A test that read the checked-in pins file for its
    values would fail on a bump branch the moment the workflow moved the pin there.
    """
    return {
        "_comment": ["A fixture: main on 2026-10-09."],
        "release": "0.1.3",
        "sdk": "v0.0.0-20261009050449-e1f3c82caa3e",
        "gateway_image": "ghcr.io/nvidia/openshell/gateway:0.1.3@sha256:ea14aa4db0980fab4769fb6b5509ac42ae3dc8495e645b1ba1fe1ada49892fa6",
        "supervisor_image": "ghcr.io/nvidia/openshell/supervisor:0.1.3@sha256:4ab2b970cdb02c7518ba4ccbbadb4861ae9a8d0b8770e5b160697f0453b0a8e4",
        "sandbox_image": SANDBOX,
        "config_schema": "v2",
    }


def target(version, salt="", stable=None):
    """A plan's target for a made-up release, shaped like plan.build() makes it."""
    is_stable = "-pre." not in version if stable is None else stable
    return {
        "version": version,
        "tag": "v" + version,
        "commit": commit_of(version),
        "stable": is_stable,
        "base": version.split("-")[0],
        "line": ".".join(version.split(".")[:2]),
        "gateway_image": image("gateway", version, salt),
        "supervisor_image": image("supervisor", version, salt),
        "ready": True,
        "waiting_for": None,
    }


class FakeUpstream(object):
    """Answers the network questions from a fixture and records them."""

    def __init__(self, data):
        self.data = data
        self.digest_lookups = []

    def release_tags(self):
        return "\n".join(self.data["tags"]) + "\n"

    def image_digest(self, repository, tag):
        key = "%s:%s" % (repository.rsplit("/", 1)[-1], tag)
        self.digest_lookups.append(key)
        if key in self.data.get("outage", []):
            raise upstream.UpstreamError("ghcr.io/%s:%s: registry answered HTTP 503" % (repository, tag))
        return self.data["images"].get(key)


def upstream_data(tags=None, images=None, outage=None, drop=()):
    """The real upstream of 2026-10-09, with a test's additions on top."""
    data = copy.deepcopy(fixture("upstream.json"))
    data["tags"] = [text for text in data["tags"] if text.split("refs/tags/")[1] not in drop]
    data["tags"] += ["%s\trefs/tags/%s" % (commit_of(name), name) for name in (tags or [])]
    data["images"].update(images or {})
    data["outage"] = list(outage or [])
    return data


# --- stand-in tools --------------------------------------------------------


def _lock_from(manifest_lines):
    """One line per requirement: what a lock file is, reduced to its essence."""
    return "".join("%s h1:%s\n" % (text, hashlib.sha1(text.encode()).hexdigest()[:16]) for text in manifest_lines)


def go_sum_for(go_mod_text):
    return _lock_from(sorted(text.strip() for text in go_mod_text.splitlines() if text.startswith("\t")))


def package_lock_for(package_json_text):
    # One line, so two changes to it always collide, as they do in the real
    # file's nested integrity entries.
    return json.dumps({"locked": sorted(json.loads(package_json_text)["dependencies"].items())}) + "\n"


def readme_block(doc):
    return "\n".join(
        [README_BEGIN, "| Tested on | `%s` |" % doc["release"], "| OpenShell Go SDK | `%s` |" % doc["sdk"], README_END]
    )


class FakeTools(object):
    """Runs in place of pinmove.run_command, and records what was asked of it."""

    def __init__(self, fail=None, stray=None, tagged=None):
        self.calls = []
        # {release commit: tag}: `go get` of that commit resolves to the SDK's
        # own tag, as it does upstream from 0.1.7 on.
        self.tagged = tagged or {}
        self.tag_commits = {tag: commit for commit, tag in self.tagged.items()}
        self.fail = fail  # a command prefix that fails, e.g. ("go", "get")
        self.stray = stray  # a path `go mod tidy` also writes, which it must not

    def __call__(self, command, cwd):
        self.calls.append(list(command))
        if self.fail and tuple(command[: len(self.fail)]) == tuple(self.fail):
            return 1, "%s: simulated failure" % " ".join(command)
        if command[:2] == ["go", "get"]:
            module, _, commit = command[2].partition("@")
            path = os.path.join(cwd, "go.mod")
            text = _read(path)
            version = self.tagged.get(commit) or sdk_version(commit)
            text = re.sub(r"(?m)^\t%s \S+$" % re.escape(module), "\t%s %s" % (module, version), text)
            _write(path, text)
            return 0, ""
        if command[:3] == ["go", "list", "-m"]:
            # A tagged SDK: the module metadata names the commit the tag points at.
            module, _, version = command[-1].partition("@")
            return 0, json.dumps({"Path": module, "Version": version, "Origin": {"Hash": self.tag_commits.get(version, "")}})
        if command[:3] == ["go", "mod", "tidy"]:
            _write(os.path.join(cwd, "go.sum"), go_sum_for(_read(os.path.join(cwd, "go.mod"))))
            if self.stray:
                _write(os.path.join(cwd, self.stray), "left behind\n")
            return 0, ""
        if command[:2] == ["npm", "install"]:
            _write(os.path.join(cwd, "package-lock.json"), package_lock_for(_read(os.path.join(cwd, "package.json"))))
            return 0, ""
        if command[0] == "node" and command[1].endswith(pinmove.README_SCRIPT):
            readme = command[command.index("--readme") + 1]
            doc = pins.load(command[command.index("--pins") + 1])
            text = _read(readme)
            begin, end = text.index(README_BEGIN), text.index(README_END) + len(README_END)
            _write(readme, text[:begin] + readme_block(doc) + text[end:])
            return 0, ""
        return 127, "FakeTools does not know %r" % (command,)


def _read(path):
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def _write(path, text):
    directory = os.path.dirname(path)
    if directory:
        os.makedirs(directory, exist_ok=True)
    with open(path, "w", encoding="utf-8") as fh:
        fh.write(text)


# --- a throwaway repository ------------------------------------------------

GO_MOD = """module example.com/dashboard/backend

go 1.26.7

require (
\texample.com/aaa v1.0.0
\texample.com/bbb v1.0.0
\texample.com/ccc v1.0.0
\texample.com/ddd v1.0.0
\texample.com/eee v1.0.0
\texample.com/fff v1.0.0
\texample.com/ggg v1.0.0
\t%s %s
\texample.com/ttt v1.0.0
\texample.com/uuu v1.0.0
\texample.com/vvv v1.0.0
\texample.com/www v1.0.0
\texample.com/xxx v1.0.0
\texample.com/yyy v1.0.0
\texample.com/zzz v1.0.0
)
"""

LINE_SOURCE = """package models

// The gateway minor release line this build is for.
const BuiltInGatewayReleaseLine = "%s"
"""


class Repo(object):
    """A clone with a bare `origin`, holding the files a pin move touches."""

    # One repository per release is built with git and then copied for every
    # test that asks: a dozen git commands cost more than copying their result.
    _templates = {}

    def __init__(self, release="0.1.3"):
        self.root = tempfile.mkdtemp(prefix="upstream-test-")
        self.origin = os.path.join(self.root, "origin.git")
        self.path = os.path.join(self.root, "work")
        if release not in Repo._templates:
            self._build(release)
            template = tempfile.mkdtemp(prefix="upstream-template-")
            atexit.register(shutil.rmtree, template, ignore_errors=True)
            for name in ("origin.git", "work"):
                shutil.copytree(os.path.join(self.root, name), os.path.join(template, name), symlinks=True)
            Repo._templates[release] = template
            return
        for name in ("origin.git", "work"):
            shutil.copytree(os.path.join(Repo._templates[release], name), os.path.join(self.root, name), symlinks=True)

    def _build(self, release):
        self._git(self.root, "init", "-q", "--bare", self.origin)
        self._git(self.root, "init", "-q", "-b", "main", self.path)
        # Relative, so a copy of the pair still points at its own origin.
        self.git("remote", "add", "origin", "../origin.git")
        doc = pins_doc(release)
        go_mod = GO_MOD % (pins.SDK_MODULE, doc["sdk"])
        self.write(pinmove.PINS, pins.dump(doc))
        self.write(pinmove.GO_MOD, go_mod)
        self.write(pinmove.GO_SUM, go_sum_for(go_mod))
        self.write(pinmove.LINE_SOURCE, LINE_SOURCE % ".".join(release.split(".")[:2]))
        self.write(pinmove.README, "# Dashboard\n\n%s\n\nMore prose.\n" % readme_block(doc))
        package = json.dumps({"dependencies": {"react": "18.0.0"}}, indent=2) + "\n"
        self.write("frontend/package.json", package)
        self.write("frontend/package-lock.json", package_lock_for(package))
        self.write("app.txt", "one\ntwo\nthree\n")
        # A template for the config schema the pins name, as deploy/ci has.
        self.write("deploy/ci/gateway.e2e.v2.toml.tmpl", "version = 2\n")
        self.commit("start")
        self.git("push", "-q", "origin", "main")

    def close(self):
        shutil.rmtree(self.root, ignore_errors=True)

    def _git(self, cwd, *args):
        env = {key: value for key, value in os.environ.items() if not key.startswith("GIT_")}
        env.update(
            GIT_AUTHOR_NAME="A Person",
            GIT_AUTHOR_EMAIL="person@example.com",
            GIT_COMMITTER_NAME="A Person",
            GIT_COMMITTER_EMAIL="person@example.com",
            GIT_CONFIG_GLOBAL=os.devnull,
            GIT_CONFIG_NOSYSTEM="1",
        )
        return subprocess.run(
            ("git",) + args, cwd=cwd, env=env, universal_newlines=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True
        ).stdout.strip()

    def git(self, *args):
        return self._git(self.path, *args)

    def read(self, path, ref=None):
        if ref:
            return self._git(self.path, "show", "%s:%s" % (ref, path)) + "\n"
        return _read(os.path.join(self.path, path))

    def write(self, path, text):
        _write(os.path.join(self.path, path), text)

    def commit(self, message):
        self.git("add", "-A")
        self.git("commit", "-q", "-m", message)
        return self.git("rev-parse", "HEAD")

    def rev(self, ref):
        return self.git("rev-parse", ref)

    def log(self, base, tip):
        """Subjects of base..tip, oldest first."""
        out = self.git("log", "--reverse", "--format=%s", "%s..%s" % (base, tip))
        return out.splitlines() if out else []

    def on_main(self, message, files):
        """Commit to main and push it, the way a merged pull request arrives."""
        self.git("checkout", "-q", "main")
        for path, text in files.items():
            self.write(path, text)
        sha = self.commit(message)
        self.git("push", "-q", "origin", "main")
        self.git("fetch", "-q", "origin")
        return sha

    def push_branch(self, sha, name):
        """Put a commit on origin as branch `name`, as a person or the publish step would."""
        self.git("push", "-q", "--force", "origin", "%s:refs/heads/%s" % (sha, name))
        self.git("fetch", "-q", "origin")

    def origin_branch(self, name):
        self.git("fetch", "-q", "--prune", "origin")
        try:
            return self.git("rev-parse", "--verify", "-q", "refs/remotes/origin/%s" % name)
        except subprocess.CalledProcessError:
            return None
