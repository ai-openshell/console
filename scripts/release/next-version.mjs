// The version of the next dashboard release. It is computed, never chosen.
//
// The dashboard shares the gateway's minor release line (ADR 0005): dashboard
// 0.1.x is for gateway 0.1.x. So the first two numbers of a release are not
// anybody's to pick. They are the line of the OpenShell release the branch is
// built on, which deploy/ci/gateway-pins.json names. The third number counts
// the dashboard's own releases on that line:
//
//   line     major.minor of the pinned release
//   version  <line>.<n>, where n is one more than the highest patch among the
//            release tags v<line>.* that exist, and 0 when there is none
//
// That is the whole rule. The first release after the pin moves to a new
// gateway minor comes out as X.Y.0 because that line has no tag yet, and
// every other release is the next patch: a dashboard never starts a minor on
// its own (ADR 0005, decision 4). Nothing here reads a commit title, and
// there is no release type to get wrong.
//
// The tags counted are the ones that exist, wherever they point. A patch cut
// from release/0.1 after main moved to 0.2 is counted for 0.1 and for nothing
// else. cut-release.mjs is what reads the repository; this file only does the
// arithmetic, so that it can be tested without one.

// A dashboard release tag: vX.Y.Z and nothing after it. v0.1.4-beta.1 and
// v0.1.4+build are not releases, and neither is anything without the v.
const RELEASE_TAG = /^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

// What a branch can pin (deploy/ci/gateway-pins.json): a stable OpenShell
// release, or, on a bump branch prepared ahead of an upstream release, a
// pre-release.
const STABLE_PIN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const PRE_RELEASE_PIN = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-pre\.(0|[1-9]\d*)$/;

/** "v0.1.3" -> { tag, major, minor, patch, line: "0.1", version: "0.1.3" }; null for any other tag. */
export function parseReleaseTag(tag) {
  const match = RELEASE_TAG.exec(tag);
  if (!match) {
    return null;
  }
  const [major, minor, patch] = match.slice(1).map(Number);
  return { tag, major, minor, patch, line: `${major}.${minor}`, version: `${major}.${minor}.${patch}` };
}

/** Orders two parsed release tags by version, oldest first. */
export function compareReleases(a, b) {
  return a.major - b.major || a.minor - b.minor || a.patch - b.patch;
}

/**
 * The next release for a branch that pins `pinned`, given every tag in the
 * repository.
 *
 * `counted` is what the number was worked out from: the release tags on the
 * line, oldest first. It is returned so that whoever starts a release can see
 * it, because a tag that should not be there is the one way this comes out
 * wrong.
 *
 * @param {object} input
 * @param {string} input.pinned    the pins' `release`, such as "0.1.3"
 * @param {string[]} input.tags    every tag in the repository
 * @returns {{line: string, version: string, tag: string, counted: string[], otherLines: string[], notReleases: string[]}}
 */
export function nextVersion({ pinned, tags }) {
  if (typeof pinned === 'string' && PRE_RELEASE_PIN.test(pinned)) {
    throw new Error(
      `this branch pins OpenShell ${pinned}, a pre-release. Nothing is released from a pre-release pin: ` +
        'a console release is for a gateway release line that exists. A pre-release pin is a bump branch ' +
        'prepared ahead of an upstream release, and it cannot be released. Release once the move to the ' +
        'stable OpenShell release has merged.',
    );
  }
  if (typeof pinned !== 'string' || !STABLE_PIN.test(pinned)) {
    throw new Error(
      `the pinned release is ${JSON.stringify(pinned)}, which is not a stable OpenShell release (X.Y.Z). ` +
        'The version of a console release is worked out from it, so there is nothing to release.',
    );
  }
  const line = pinned.split('.').slice(0, 2).join('.');

  const releases = [];
  const notReleases = [];
  for (const tag of tags) {
    const parsed = parseReleaseTag(tag);
    if (parsed) {
      releases.push(parsed);
    } else {
      notReleases.push(tag);
    }
  }
  releases.sort(compareReleases);

  const onLine = releases.filter((release) => release.line === line);
  const patch = onLine.length === 0 ? 0 : onLine[onLine.length - 1].patch + 1;
  const version = `${line}.${patch}`;
  return {
    line,
    version,
    tag: `v${version}`,
    counted: onLine.map((release) => release.tag),
    otherLines: releases.filter((release) => release.line !== line).map((release) => release.tag),
    notReleases: notReleases.sort(),
  };
}
