// node --test "scripts/**/*.test.mjs"
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

import {
  LINE_CONSTANT,
  PINS_PATH,
  deriveGatewayLine,
  formatLine,
  lineInGoSource,
  lineOf,
  lineOfLanes,
  readGatewayLine,
  sdkInGoMod,
} from './gateway-range.mjs';
import { BEGIN, END, README_PATH, renderBlock, replaceBlock } from './readme-gateway-range.mjs';

const scripts = dirname(fileURLToPath(import.meta.url));
const SDK = 'v0.0.0-20260928030816-6648bd0c290e';
const pinned = (release) => ({ release, sdk: SDK });

/** Runs one of the scripts the way CI and the Follow upstream workflow do. */
const run = (script, ...args) => spawnSync(process.execPath, [join(scripts, script), ...args], { encoding: 'utf8' });

function tempDir(t) {
  const dir = mkdtempSync(join(tmpdir(), 'openshell-dashboard-range-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

/**
 * The committed pins after one of the moves they can make. Each is the path
 * of a scratch copy.
 *
 * Everything is worked out from whatever release is committed, never from a
 * release written here: on a bump branch prepared ahead of an upstream
 * release the committed release is a pre-release, and these tests run there
 * too.
 */
function movedPins(t) {
  const dir = tempDir(t);
  const pins = JSON.parse(readFileSync(PINS_PATH, 'utf8'));
  const [major, minor, patch] = pins.release.split('-')[0].split('.').map(Number);
  const nextPatch = `${major}.${minor}.${patch + 1}`;
  const nextMinor = `${major}.${minor + 1}.0`;
  const nextSdk = 'v0.0.0-20991231000000-0123456789ab';
  const write = (name, changed) => {
    const path = join(dir, name);
    writeFileSync(path, `${JSON.stringify(changed, null, 2)}\n`);
    return path;
  };
  return {
    release: pins.release,
    nextPatch,
    nextMinor,
    nextSdk,
    // A newer patch release of the same line.
    patch: write('patch.json', { ...pins, release: nextPatch }),
    // A pre-release of it, which is what a prep bump branch pins.
    preRelease: write('pre-release.json', { ...pins, release: `${nextPatch}-pre.2` }),
    // The first release of the next minor line.
    nextLine: write('next-line.json', { ...pins, release: nextMinor }),
    // The sdk field alone, without go.mod: a move that was only half made.
    sdkOnly: write('sdk-only.json', { ...pins, sdk: nextSdk }),
    // A whole move, as far as the pins file goes: the release and its SDK.
    whole: write('whole.json', { ...pins, release: nextPatch, sdk: nextSdk }),
  };
}

test('the line is the major.minor of the pinned release', () => {
  assert.deepEqual(deriveGatewayLine(pinned('0.1.2')), { line: '0.1', release: '0.1.2', sdk: SDK });
  assert.equal(deriveGatewayLine(pinned('1.20.300')).line, '1.20');
  assert.equal(formatLine('0.1'), '0.1.x');
  assert.equal(lineOf('0.1.2'), '0.1');
  assert.equal(lineOf('1.20.300'), '1.20');
});

test('a pre-release is on the line of the release it leads up to', () => {
  assert.deepEqual(deriveGatewayLine(pinned('0.1.4-pre.2')), { line: '0.1', release: '0.1.4-pre.2', sdk: SDK });
  assert.equal(deriveGatewayLine(pinned('0.2.0-pre.11')).line, '0.2');
});

test('a release that is not a release is refused, not read as a line', () => {
  for (const release of ['dev', '0.1.3-dev.84', 'latest', 'v0.1.2', '0.1', '0.1.3-rc.1', '0.1.3-pre', '', undefined, 13]) {
    assert.throws(() => deriveGatewayLine(pinned(release)), /not an OpenShell release/, `release ${release}`);
  }
});

test('there is no declaration without the SDK, or from a pins file that lists lanes', () => {
  assert.throws(() => deriveGatewayLine({ release: '0.1.2' }), /"sdk" is missing/);
  assert.throws(() => deriveGatewayLine({ release: '0.1.2', sdk: ' ' }), /"sdk" is missing/);
  const lanes = { sdk: SDK, lanes: [{ version: '0.1.2', required: true }] };
  assert.throws(() => deriveGatewayLine(lanes), /not an OpenShell release/);
  assert.throws(() => deriveGatewayLine(null), /not an OpenShell release/);
});

// A tag pushed back from an old clone can point at a commit whose pins file
// lists lanes.
test('the line of a pins file that lists lanes is that of its newest required lane', () => {
  const lane = (version, required = true) => ({ version, required });
  assert.equal(lineOfLanes({ lanes: [lane('0.1.2'), lane('0.1.0')] }), '0.1');
  assert.equal(lineOfLanes({ lanes: [lane('0.9.0'), lane('0.10.0'), lane('0.2.11')] }), '0.10');
  assert.equal(lineOfLanes({ lanes: [lane('0.0.116', false), lane('0.1.0'), lane('0.2.0', false)] }), '0.1');
  assert.equal(lineOfLanes({ lanes: [lane('0.1.2', false)] }), null);
  assert.equal(lineOfLanes({ lanes: [lane('dev')] }), null);
  assert.equal(lineOfLanes(pinned('0.1.2')), null);
  assert.equal(lineOfLanes(null), null);
});

test('the committed pins give a line, and name the SDK that backend/go.mod builds against', () => {
  const declared = readGatewayLine();
  assert.match(declared.line, /^\d+\.\d+$/);
  assert.match(declared.release, /^\d+\.\d+\.\d+(-pre\.\d+)?$/);
  assert.ok(declared.release.startsWith(`${declared.line}.`));
  assert.equal(declared.sdk, sdkInGoMod());
});

// The BFF has the line compiled in, so that an image knows it whatever built
// it. That constant restates the pins, and this is what holds it to them.
test('the line compiled into the BFF is the line the committed pins give', () => {
  assert.equal(lineInGoSource(), readGatewayLine().line);
});

test('the built-in line is read from its Go constant, and a source without one is an error', (t) => {
  const dir = tempDir(t);
  const source = join(dir, 'gateway_release_line.go');

  writeFileSync(source, `package models\n\n// A comment.\nconst ${LINE_CONSTANT} = "0.2"\n`);
  assert.equal(lineInGoSource(source), '0.2');

  // Renamed, or moved into a const block: the check must stop, not pass.
  writeFileSync(source, 'package models\n\nconst SomethingElse = "0.2"\n');
  assert.throws(() => lineInGoSource(source), /does not declare the built-in gateway release line/);
  writeFileSync(source, `package models\n\nconst (\n\t${LINE_CONSTANT} = "0.2"\n)\n`);
  assert.throws(() => lineInGoSource(source), /does not declare the built-in gateway release line/);
});

test('the README block is replaced in place and everything around it is kept', () => {
  const block = renderBlock(deriveGatewayLine(pinned('0.1.2')));
  const readme = `# Title\n\nbefore\n\n${BEGIN}\nstale\n${END}\n\nafter\n`;

  const updated = replaceBlock(readme, block);
  assert.equal(updated, `# Title\n\nbefore\n\n${block}\n\nafter\n`);
  assert.match(updated, /\| Supported gateways \| `0\.1\.x` \|/);
  assert.match(updated, /\| Tested on \| `0\.1\.2` \|/);
  assert.ok(updated.includes(`| OpenShell Go SDK | \`${SDK}\` |`));
  // Regenerating an up-to-date README changes nothing, which is what --check relies on.
  assert.equal(replaceBlock(updated, block), updated);
});

test('a README without exactly one pair of markers is an error, not a silent no-op', () => {
  const block = renderBlock(deriveGatewayLine(pinned('0.1.2')));
  assert.throws(() => replaceBlock('# Title\n', block), /markers are missing/);
  assert.throws(() => replaceBlock(`${END}\n${BEGIN}\n`, block), /markers are missing or out of order/);
  assert.throws(() => replaceBlock(`${BEGIN}\n${END}\n${BEGIN}\n${END}\n`, block), /more than once/);
});

// --- the two scripts as CI and the Follow upstream workflow run them --------

test('--check holds the sdk field to backend/go.mod', (t) => {
  const moved = movedPins(t);

  // Only the sdk field differs from go.mod.
  const stale = run('gateway-range.mjs', '--check', '--pins', moved.sdkOnly);
  assert.equal(stale.status, 1);
  assert.match(stale.stderr, /The sdk field must equal the SDK version in backend\/go\.mod/);
  assert.ok(stale.stderr.includes(`builds against ${sdkInGoMod()}`));
  assert.ok(stale.stderr.includes(`upstream tagged ${moved.release} with`));

  // Deriving the line does not need the two files to agree; only --check does.
  assert.equal(run('gateway-range.mjs', '--pins', moved.sdkOnly).status, 0);
});

test('--check holds the line compiled into the BFF to the pinned release', (t) => {
  const moved = movedPins(t);
  const built = lineInGoSource();

  // A newer patch of the same line, or a pre-release of one, changes nothing
  // the BFF has compiled in, so it passes on its own.
  for (const [pins, release] of [
    [moved.patch, moved.nextPatch],
    [moved.preRelease, `${moved.nextPatch}-pre.2`],
  ]) {
    const same = run('gateway-range.mjs', '--check', '--format', 'json', '--pins', pins);
    assert.equal(same.status, 0, same.stderr);
    assert.equal(JSON.parse(same.stdout).release, release);
    assert.equal(JSON.parse(same.stdout).line, built);
  }

  // The next minor is a new line, and the constant has to move in the same
  // change. The message says what to do, both ways round.
  const minor = run('gateway-range.mjs', '--check', '--pins', moved.nextLine);
  assert.equal(minor.status, 1);
  assert.ok(minor.stderr.includes(`${LINE_CONSTANT} = "${built}"`), minor.stderr);
  assert.ok(minor.stderr.includes(`pins OpenShell ${moved.nextMinor}, which is release line ${lineOf(moved.nextMinor)}`));
  assert.ok(minor.stderr.includes(`set ${LINE_CONSTANT} = "${lineOf(moved.nextMinor)}"`));
  assert.match(minor.stderr, /backend\/pkg\/models\/gateway_release_line\.go/);
  assert.match(minor.stderr, /If the pin moved by mistake, move it back/);

  // Without --check the script only derives, so the same pins print their line.
  const derived = run('gateway-range.mjs', '--format', 'github', '--pins', moved.nextLine);
  assert.equal(derived.status, 0, derived.stderr);
  assert.ok(derived.stdout.startsWith(`line=${lineOf(moved.nextMinor)}\n`));
});

test('--format github prints the outputs the build job reads', () => {
  const declared = readGatewayLine();
  const printed = run('gateway-range.mjs', '--format', 'github');
  assert.equal(printed.status, 0, printed.stderr);
  assert.equal(printed.stdout, `line=${declared.line}\nrelease=${declared.release}\nsdk=${declared.sdk}\n`);
});

// The workflow that moves the pin regenerates the block with --readme and
// --pins pointing at the tree it is changing, or that tree could never pass
// the stale check. This is the whole of what it needs to run.
test('--write brings the README back in step after the pin moves, and touches only its block', (t) => {
  const moved = movedPins(t);
  const original = readFileSync(README_PATH, 'utf8');
  const outside = (readme) => [readme.slice(0, readme.indexOf(BEGIN)), readme.slice(readme.indexOf(END))];

  for (const [pins, expected] of [
    [moved.patch, [`| Tested on | \`${moved.nextPatch}\` |`]],
    [moved.preRelease, [`| Tested on | \`${moved.nextPatch}-pre.2\` |`]],
    [moved.whole, [`| Tested on | \`${moved.nextPatch}\` |`, `| OpenShell Go SDK | \`${moved.nextSdk}\` |`]],
    [moved.nextLine, [`| Supported gateways | \`${lineOf(moved.nextMinor)}.x\` |`]],
  ]) {
    const readme = join(tempDir(t), 'README.md');
    writeFileSync(readme, original);
    const readmeRange = (mode) => run('readme-gateway-range.mjs', mode, '--readme', readme, '--pins', pins);

    const before = readmeRange('--check');
    assert.equal(before.status, 1, 'the committed README is stale once the pins move');
    assert.match(before.stderr, /is stale/);
    for (const line of expected) {
      assert.ok(before.stderr.includes(line), 'and the check says what it should read');
    }
    assert.equal(readFileSync(readme, 'utf8'), original, '--check never writes');

    assert.equal(readmeRange('--write').status, 0);
    assert.equal(readmeRange('--check').status, 0);

    const regenerated = readFileSync(readme, 'utf8');
    for (const line of expected) {
      assert.ok(regenerated.includes(line));
    }
    assert.deepEqual(outside(regenerated), outside(original), 'nothing outside the markers changed');
    // A second --write is a no-op, so running it when nothing moved is harmless.
    assert.equal(readmeRange('--write').status, 0);
    assert.equal(readFileSync(readme, 'utf8'), regenerated);
  }
});
