#!/usr/bin/env node
// The OpenShell gateway release line this build is for, derived from
// deploy/ci/gateway-pins.json.
//
// A dashboard build shares the gateway's minor release line (ADR 0005): a
// build for 0.1 works with every gateway 0.1.x and with no other line. Nobody
// gets to type the line an artifact declares. It is the major.minor of the
// release the pins file names: the one upstream release this branch is built
// on, and the gateway every change is tested against.
//
// This file is the only place that turns the pins into a line. The container
// image's label (ci.yml), the version and the notes of a release
// (scripts/release/) and the README's Compatibility section
// (readme-gateway-range.mjs) all call it, so a published artifact cannot claim
// something CI did not test and two artifacts cannot disagree with each other.
//
// One thing restates the line and cannot call this file: the BFF, which has
// the line compiled in so that every image knows it whatever built it
// (BuiltInGatewayReleaseLine in backend/pkg/models/gateway_release_line.go).
// --check holds that constant to the pins.
//
//   node scripts/gateway-range.mjs                  summary for a person
//   node scripts/gateway-range.mjs --format json    for scripts
//   node scripts/gateway-range.mjs --format github  key=value lines for $GITHUB_OUTPUT
//   node scripts/gateway-range.mjs --check          also fail when the line compiled
//                                                   into the BFF is not the pins' line,
//                                                   or the pins' sdk is not the one in
//                                                   backend/go.mod
import { readFileSync, realpathSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

// Repo-relative, with forward slashes, so it can also be used in `git show <tag>:<path>`.
export const PINS_FILE = 'deploy/ci/gateway-pins.json';
export const PINS_PATH = join(repoRoot, PINS_FILE);
export const GO_MOD_PATH = join(repoRoot, 'backend', 'go.mod');
export const SDK_MODULE = 'github.com/NVIDIA/OpenShell/sdk/go';
// Where the BFF's built-in line is written, and the name it is written under.
export const LINE_SOURCE_FILE = 'backend/pkg/models/gateway_release_line.go';
export const LINE_SOURCE_PATH = join(repoRoot, LINE_SOURCE_FILE);
export const LINE_CONSTANT = 'BuiltInGatewayReleaseLine';

// What a branch may pin: a stable release X.Y.Z, or a pre-release X.Y.Z-pre.N.
// Only a bump branch prepared ahead of an upstream release pins a
// pre-release, and CI refuses one in every pull request
// (deploy/ci/upstream/follow.py require-stable), so it never merges. Upstream
// also publishes dev builds (0.1.3-dev.84); a dev gateway pulls a moving
// sandbox image at runtime, so nothing is ever declared to be tested on one.
const RELEASE = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-pre\.(0|[1-9]\d*))?$/;

/** "0.1.3" -> "0.1": the minor release line a release belongs to. */
export function lineOf(release) {
  return release.split('.').slice(0, 2).join('.');
}

/**
 * Turn the parsed pins file into what this build may declare: the release
 * line, the release it is built on and tested against, and the SDK.
 */
export function deriveGatewayLine(pins) {
  const release = pins?.release;
  if (typeof release !== 'string' || !RELEASE.test(release)) {
    throw new Error(
      `"release" is ${JSON.stringify(release)}, which is not an OpenShell release ` +
        '(X.Y.Z, or X.Y.Z-pre.N on a bump branch). A release line can only be declared from the ' +
        'release this branch is built on; a dev build is never pinned.',
    );
  }
  if (typeof pins.sdk !== 'string' || pins.sdk.trim() === '') {
    throw new Error('"sdk" is missing: it must record the SDK pin from backend/go.mod');
  }
  return { line: lineOf(release), release, sdk: pins.sdk };
}

/**
 * The line a pins file that lists several gateways as `lanes` declares: that
 * of the newest required one. Nothing writes that shape, and no release tag
 * vX.Y.Z points at a commit that has it. It is read only for a release tag
 * this pipeline did not cut (one pushed by hand, or pushed back from an old
 * clone): the release notes and cut-release.mjs ask which line such a tag's
 * commit was built for. Returns null when the file is not of that shape
 * either.
 */
export function lineOfLanes(pins) {
  const versions = (Array.isArray(pins?.lanes) ? pins.lanes : [])
    .filter((lane) => lane?.required === true && typeof lane.version === 'string' && RELEASE.test(lane.version))
    .map((lane) => lane.version.split('-')[0].split('.').map(Number));
  if (versions.length === 0) {
    return null;
  }
  versions.sort((a, b) => a[0] - b[0] || a[1] - b[1] || a[2] - b[2]);
  return versions[versions.length - 1].slice(0, 2).join('.');
}

export function readGatewayLine(pinsPath = PINS_PATH) {
  let pins;
  try {
    pins = JSON.parse(readFileSync(pinsPath, 'utf8'));
  } catch (error) {
    throw new Error(`cannot read ${pinsPath}: ${error.message}`);
  }
  try {
    return deriveGatewayLine(pins);
  } catch (error) {
    throw new Error(`${pinsPath}: ${error.message}`);
  }
}

/** The SDK version backend/go.mod actually builds against. */
export function sdkInGoMod(goModPath = GO_MOD_PATH) {
  const goMod = readFileSync(goModPath, 'utf8');
  const escaped = SDK_MODULE.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = goMod.match(new RegExp(`^\\s*(?:require\\s+)?${escaped}\\s+(\\S+)`, 'm'));
  if (!match) {
    throw new Error(`${goModPath} does not require ${SDK_MODULE}`);
  }
  return match[1];
}

/** The release line compiled into the BFF, as its Go source states it. */
export function lineInGoSource(sourcePath = LINE_SOURCE_PATH) {
  const source = readFileSync(sourcePath, 'utf8');
  const match = source.match(new RegExp(`^const ${LINE_CONSTANT} = "([^"]*)"$`, 'm'));
  if (!match) {
    throw new Error(
      `${sourcePath} does not declare the built-in gateway release line. This check reads it ` +
        `from a line of the form  const ${LINE_CONSTANT} = "0.1"  and found none.`,
    );
  }
  return match[1];
}

/** "0.1.x": the line the way people write it. */
export function formatLine(line) {
  return `${line}.x`;
}

/**
 * Hold what the build contains to what the pins say. Two things restate the
 * pins and each has to agree with them; nothing else is tied together.
 */
function check(declared, pinsPath) {
  // The line. The pins say which gateway release the build is built on; the
  // Go constant is what a running BFF compares a gateway with. If they
  // differ, the image's label and the notice in the UI would name different
  // lines.
  const built = lineInGoSource();
  if (built !== declared.line) {
    throw new Error(
      `${LINE_SOURCE_FILE} says ${LINE_CONSTANT} = ${JSON.stringify(built)}, but ${pinsPath} pins ` +
        `OpenShell ${declared.release}, which is release line ${declared.line}. ` +
        'The line compiled into the BFF must be the line of the pinned release.\n' +
        `  If the gateway started a new minor release line and this branch is moving to it, set ` +
        `${LINE_CONSTANT} = ${JSON.stringify(declared.line)} in the same change that moves the pin, ` +
        'and run `node scripts/readme-gateway-range.mjs --write`. The Follow upstream workflow does ' +
        'both in the bump it opens. Moving to a new line is a minor release of the dashboard (ADR 0005).\n' +
        '  If the pin moved by mistake, move it back: a patch release of the gateway never changes the line.',
    );
  }

  // The SDK. The `sdk` field of the pins file equals the SDK version in
  // backend/go.mod. The field is a record; go.mod is what the BFF is compiled
  // against. If they differ, every artifact would declare an SDK the build
  // does not contain. Both come from the pinned release and move with it, in
  // one commit (ADR 0005, decision 7).
  const sdk = sdkInGoMod();
  if (sdk !== declared.sdk) {
    throw new Error(
      `${pinsPath} says "sdk": ${JSON.stringify(declared.sdk)}, but backend/go.mod builds against ${sdk}. ` +
        'The sdk field must equal the SDK version in backend/go.mod: the SDK at the commit upstream ' +
        `tagged ${declared.release} with. Bring the stale one of the two in line.`,
    );
  }
}

function main() {
  const { values } = parseArgs({
    options: {
      format: { type: 'string', default: 'text' },
      pins: { type: 'string' },
      check: { type: 'boolean', default: false },
    },
  });

  const pinsPath = values.pins ?? PINS_PATH;
  const declared = readGatewayLine(pinsPath);

  if (values.check) {
    check(declared, pinsPath);
  }

  switch (values.format) {
    case 'json':
      process.stdout.write(`${JSON.stringify(declared, null, 2)}\n`);
      break;
    case 'github':
      process.stdout.write(`line=${declared.line}\nrelease=${declared.release}\nsdk=${declared.sdk}\n`);
      break;
    case 'text':
      process.stdout.write(
        `Supported OpenShell gateways: ${formatLine(declared.line)}  (release line ${declared.line})\n` +
          `Built on and tested against:  ${declared.release}\n` +
          `OpenShell Go SDK:             ${declared.sdk}\n`,
      );
      break;
    default:
      throw new Error(`unknown --format ${JSON.stringify(values.format)} (text, json or github)`);
  }
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`gateway-range: ${error.message}\n`);
    process.exit(1);
  }
}
