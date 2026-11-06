// node --test "scripts/**/*.test.mjs"
//
// The release pipeline only runs for real on main and on release branches, and
// its last step talks to a container registry. These tests cover the parts
// that decide things — what the notes say, whether a release was cut, which
// tags move and which never may — with no network, no registry and no docker
// daemon: retag-image.sh is run against a stand-in `docker` that keeps a
// registry in a file, and branch-tip.sh against a throwaway git remote on
// disk. What version a release gets is in next-version.test.mjs, and cutting
// one is in cut-release.test.mjs.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

import { changesNotes, isCiCommit, lineDeclaredBy, releaseNotes, supportedGatewaysNotes } from './release-notes.mjs';
import { releaseAt } from './released-version.mjs';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const SDK = 'v0.0.0-20260928030816-6648bd0c290e';
const declared = { line: '0.1', release: '0.1.2', sdk: SDK };
// What is known about an earlier release: the line it was for.
const previous = { line: '0.1' };

function tempDir(t) {
  const dir = mkdtempSync(join(tmpdir(), 'openshell-dashboard-release-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// --- release notes ---------------------------------------------------------

test('the notes state the line, what it was tested on and the SDK, and link the README of that release', () => {
  const notes = supportedGatewaysNotes({
    declared,
    previous,
    previousTag: 'v0.1.0',
    readmeUrl: 'https://github.com/o/r/blob/v0.1.1/README.md#compatibility',
  });
  assert.match(notes, /^### Supported OpenShell gateways\n/);
  assert.ok(notes.includes(`**0.1.x**, tested on 0.1.2, built against OpenShell Go SDK \`${SDK}\`.`), notes);
  assert.match(notes, /\[Compatibility\]\(https:\/\/github\.com\/o\/r\/blob\/v0\.1\.1\/README\.md#compatibility\)/);
  assert.doesNotMatch(notes, /release line changed/);
});

test('a release is tested on one gateway release, the one its branch pins', () => {
  const notes = supportedGatewaysNotes({ declared, previous: null, readmeUrl: null });
  assert.ok(notes.includes('The release named above is the one this commit is built on'));
  assert.doesNotMatch(notes, / and 0\.1\./);
});

test('a new release line is called out, because a patch number will not say so', () => {
  const notes = supportedGatewaysNotes({
    declared: { line: '0.2', release: '0.2.0', sdk: SDK },
    previous,
    previousTag: 'v0.1.0',
    readmeUrl: null,
  });
  assert.match(
    notes,
    /> \*\*The supported gateway release line changed in this release\.\*\* v0\.1\.0 supported 0\.1\.x\./,
  );
});

test('a newer patch of the same line is not a change of line', () => {
  const notes = supportedGatewaysNotes({
    declared: { ...declared, release: '0.1.3' },
    previous,
    previousTag: 'v0.1.0',
    readmeUrl: null,
  });
  assert.doesNotMatch(notes, /release line changed/);
});

test('nothing is claimed about a previous release whose line is unknown', () => {
  const notes = supportedGatewaysNotes({ declared, previous: null, previousTag: 'v0.1.0', readmeUrl: null });
  assert.doesNotMatch(notes, /release line changed/);
});

// A tag pushed back from an old clone may point at a pins file that lists lanes.
test('the line of the previous release is read from a pins file of either shape', () => {
  assert.equal(lineDeclaredBy({ release: '0.1.3', sdk: SDK }), '0.1');
  const lanes = [
    { version: '0.1.3', required: true },
    { version: '0.1.0', required: true },
  ];
  assert.equal(lineDeclaredBy({ sdk: SDK, lanes }), '0.1');
  assert.equal(lineDeclaredBy({ sdk: SDK, lanes: [{ version: 'dev', required: true }] }), null);
  assert.equal(lineDeclaredBy({}), null);
});

// --- the changes -----------------------------------------------------------

const REPO_URL = 'https://github.com/o/r';
const sha = (letter) => letter.repeat(40);
const commits = [
  { sha: sha('a'), subject: 'feat: add provider profile detail page (#101)' },
  { sha: sha('b'), subject: 'ci: pin the runners (#102)' },
  { sha: sha('c'), subject: 'fix(bff)!: send the workspace scope (#103)' },
  { sha: sha('d'), subject: 'fix(ci): unbreak the build job' },
  { sha: sha('e'), subject: 'UI Helm Charts (#97)' },
];

test('the changes are the commit titles since the previous release, as merged, oldest first', () => {
  const notes = changesNotes({ commits, previousTag: 'v0.1.3', tag: 'v0.1.4', repoUrl: REPO_URL });
  assert.deepEqual(notes.split('\n'), [
    '### Changes since v0.1.3',
    '',
    `- feat: add provider profile detail page (#101) ([aaaaaaa](${REPO_URL}/commit/${sha('a')}))`,
    `- fix(bff)!: send the workspace scope (#103) ([ccccccc](${REPO_URL}/commit/${sha('c')}))`,
    // A title that is not a Conventional Commit is listed as it is.
    `- UI Helm Charts (#97) ([eeeeeee](${REPO_URL}/commit/${sha('e')}))`,
    '',
    `[Everything between v0.1.3 and v0.1.4](${REPO_URL}/compare/v0.1.3...v0.1.4)`,
  ]);
});

test('a commit about CI is left out of the changes, by type or by scope', () => {
  for (const subject of ['ci: x', 'ci!: x', 'fix(ci): x', 'feat(ci)!: x', 'ci(deps): x', 'ci: revert "x"']) {
    assert.equal(isCiCommit(subject), true, subject);
  }
  // git's own title for a revert has no type and no scope, so it is listed.
  for (const subject of ['fix: x', 'feat(bff): x', 'docs: ci notes', 'build(ci-image): x', 'Revert "ci: x"', 'Add foo']) {
    assert.equal(isCiCommit(subject), false, subject);
  }
});

test('a release of nothing but CI commits says so instead of listing nothing', () => {
  const onlyCi = changesNotes({ commits: [commits[1], commits[3]], previousTag: 'v0.1.3', tag: 'v0.1.4', repoUrl: null });
  assert.equal(onlyCi, '### Changes since v0.1.3\n\nNothing is listed: every commit since v0.1.3 is about CI.');
  const none = changesNotes({ commits: [], previousTag: 'v0.1.3', tag: 'v0.1.4', repoUrl: null });
  assert.equal(none, '### Changes since v0.1.3\n\nNo commits since v0.1.3.');
});

test('with no earlier release the changes are a fixed opening, not the whole history', () => {
  const notes = changesNotes({ commits, previousTag: null, tag: 'v0.1.0', repoUrl: REPO_URL });
  assert.equal(
    notes,
    '### Changes\n\nNo earlier release exists to compare this one with, so its changes are not listed.',
  );
});

test('the notes of a release are its changes, then the gateways it supports', () => {
  const notes = releaseNotes({
    tag: 'v0.1.4',
    previousTag: 'v0.1.3',
    commits,
    declared,
    previous,
    repoUrl: REPO_URL,
  });
  const changes = notes.indexOf('### Changes since v0.1.3');
  const gateways = notes.indexOf('### Supported OpenShell gateways');
  assert.equal(changes, 0);
  assert.ok(gateways > changes, notes);
  assert.ok(notes.includes(`**0.1.x**, tested on 0.1.2, built against OpenShell Go SDK \`${SDK}\`.`), notes);
  // The README as of this release, so the link stays right after main moves on.
  assert.ok(notes.includes(`[Compatibility](${REPO_URL}/blob/v0.1.4/README.md#compatibility)`), notes);
  assert.ok(notes.endsWith('\n'));

  const first = releaseNotes({ tag: 'v0.1.0', previousTag: null, commits: [], declared, previous: null, repoUrl: null });
  assert.ok(first.startsWith('### Changes\n\nNo earlier release exists'), first);
  assert.ok(first.includes('### Supported OpenShell gateways'), first);
  assert.ok(first.includes('see Compatibility in the README'), first);
});

test('the first release of a new line says the line changed, next to the changes since the old one', () => {
  const notes = releaseNotes({
    tag: 'v0.2.0',
    previousTag: 'v0.1.7',
    commits: [commits[0]],
    declared: { line: '0.2', release: '0.2.0', sdk: SDK },
    previous,
    repoUrl: null,
  });
  assert.ok(notes.startsWith('### Changes since v0.1.7\n\n- feat: add provider profile detail page (#101) (aaaaaaa)'), notes);
  assert.match(notes, /> \*\*The supported gateway release line changed in this release\.\*\* v0\.1\.7 supported 0\.1\.x\./);
});

// --- nothing goes to npm ---------------------------------------------------

// frontend/package.json is an application's manifest, not a published
// package. `private` is what makes `npm publish` refuse it.
test('frontend/package.json is private, so it cannot be published', () => {
  const pkg = JSON.parse(readFileSync(join(repoRoot, 'frontend', 'package.json'), 'utf8'));
  assert.equal(pkg.private, true);
});

// --- was a release cut? ----------------------------------------------------

test('no release tag at the commit means nothing to tag', () => {
  assert.equal(releaseAt([], ['v0.1.0', 'v0.1.1'], '0.1'), null);
  assert.equal(releaseAt(['some-other-tag', 'v0.1.2-beta.1'], ['v0.1.1'], '0.1'), null);
});

test('a release gets X.Y.Z, and X.Y when it is the newest patch of that line', () => {
  assert.deepEqual(releaseAt(['v0.1.2'], ['v0.1.0', 'v0.1.1', 'v0.1.2'], '0.1'), {
    version: '0.1.2',
    tag: 'v0.1.2',
    imageTags: ['0.1.2', '0.1'],
  });
  assert.deepEqual(releaseAt(['v1.2.10'], ['v1.2.9', 'v1.2.10', 'v1.3.0', 'v2.0.0'], '1.2').imageTags, ['1.2.10', '1.2']);
});

test('re-running for an older release does not pull X.Y back', () => {
  assert.deepEqual(releaseAt(['v0.1.0'], ['v0.1.0', 'v0.1.1'], '0.1').imageTags, ['0.1.0']);
});

// A patch from release/0.1 after main moved to 0.2: the 0.1 tag moves, and
// nothing about 0.2 is touched or consulted.
test('a release moves the tag of its own line and no other', () => {
  const all = ['v0.1.0', 'v0.1.1', 'v0.1.2', 'v0.2.0', 'v0.2.1'];
  assert.deepEqual(releaseAt(['v0.1.2'], all, '0.1').imageTags, ['0.1.2', '0.1']);
  assert.deepEqual(releaseAt(['v0.2.1'], all, '0.2').imageTags, ['0.2.1', '0.2']);
});

// A release tag this pipeline did not cut can sit on a commit whose pins are
// for another line. It is not that commit's release.
test('a release tag of another line on the commit is not its release', () => {
  assert.equal(releaseAt(['v1.2.0'], ['v0.1.3', 'v1.2.0'], '0.1'), null);
  assert.deepEqual(releaseAt(['v1.2.0', 'v0.1.4'], ['v0.1.3', 'v0.1.4', 'v1.2.0'], '0.1'), {
    version: '0.1.4',
    tag: 'v0.1.4',
    imageTags: ['0.1.4', '0.1'],
  });
});

// --- retag by digest -------------------------------------------------------

const DIGEST = `sha256:${'a'.repeat(64)}`;
// What the same commit gets when it is built a second time.
const REBUILT = `sha256:${'b'.repeat(64)}`;
const IMAGE = 'quay.io/example/dashboard';
const SOURCE = 'sha-0a1b2c3';
const have = (tool) => spawnSync(tool, ['--version'], { stdio: 'ignore' }).status === 0;
const canRunRetag = have('bash') && have('jq');
const inspected = (tag) => `buildx imagetools inspect ${IMAGE}:${tag} --format {{json .Manifest}}`;
const pushes = (calls) => calls.filter((call) => call.startsWith('buildx imagetools create'));

/**
 * Runs scripts/retag-image.sh with a stand-in `docker` first on PATH.
 *
 * The stand-in is a registry kept in a JSON file, tag -> digest. `inspect`
 * reads a tag from it and answers a missing one the way buildx does; `create`
 * points tags at the source digest, like the carbon copy the real command
 * makes. So a test says what the registry holds beforehand and looks at what
 * it holds afterwards, instead of only at what was asked of it.
 */
function retag(t, args, { registry = {}, env = {} } = {}) {
  const dir = tempDir(t);
  const log = join(dir, 'docker.log');
  const store = join(dir, 'registry.json');
  const docker = join(dir, 'docker');
  writeFileSync(
    docker,
    `#!/usr/bin/env node
const { appendFileSync, readFileSync, writeFileSync } = require('node:fs');
const args = process.argv.slice(2);
appendFileSync(process.env.STUB_LOG, args.join(' ') + '\\n');
const registry = JSON.parse(readFileSync(process.env.STUB_REGISTRY, 'utf8'));
const tagOf = (ref) => ref.slice(ref.lastIndexOf(':') + 1);
if (args[2] === 'inspect') {
  const tag = tagOf(args[3]);
  if (tag === process.env.STUB_UNREADABLE_TAG) {
    process.stderr.write('ERROR: unexpected status from HEAD request to https://registry.example/v2/dashboard/manifests/' + tag + ': 503 Service Unavailable\\n');
    process.exit(1);
  }
  if (!(tag in registry)) {
    process.stderr.write('ERROR: ' + args[3] + ': not found\\n');
    process.exit(1);
  }
  process.stdout.write(JSON.stringify({ mediaType: 'application/vnd.oci.image.index.v1+json', digest: registry[tag] }));
}
if (args[2] === 'create') {
  const source = args[args.length - 1];
  const digest = process.env.STUB_CREATE_DIGEST || source.slice(source.indexOf('@') + 1);
  for (let i = 3; i < args.length - 1; i += 2) {
    registry[tagOf(args[i + 1])] = digest;
  }
  writeFileSync(process.env.STUB_REGISTRY, JSON.stringify(registry));
}
`,
  );
  chmodSync(docker, 0o755);
  writeFileSync(log, '');
  writeFileSync(store, JSON.stringify(registry));

  const result = spawnSync(join(repoRoot, 'scripts', 'retag-image.sh'), args, {
    encoding: 'utf8',
    env: {
      ...process.env,
      PATH: `${dir}${delimiter}${process.env.PATH}`,
      STUB_LOG: log,
      STUB_REGISTRY: store,
      ...env,
    },
  });
  return {
    ...result,
    calls: readFileSync(log, 'utf8').split('\n').filter(Boolean),
    registry: JSON.parse(readFileSync(store, 'utf8')),
  };
}

test('a release gets its tags in one push, at the digest of the source tag', { skip: !canRunRetag }, (t) => {
  const { status, calls, registry, stderr } = retag(t, [IMAGE, SOURCE, '0.1.0', '0.1'], {
    registry: { [SOURCE]: DIGEST },
  });
  assert.equal(status, 0, stderr);
  assert.deepEqual(calls, [
    inspected(SOURCE),
    // Is the version tag free? It is looked up before anything is pushed.
    inspected('0.1.0'),
    `buildx imagetools create --tag ${IMAGE}:0.1.0 --tag ${IMAGE}:0.1 ${IMAGE}@${DIGEST}`,
    inspected('0.1.0'),
    inspected('0.1'),
  ]);
  assert.deepEqual(registry, { [SOURCE]: DIGEST, '0.1.0': DIGEST, '0.1': DIGEST });
});

test('DRY_RUN=1 resolves the digest and prints the command without pushing', { skip: !canRunRetag }, (t) => {
  const { status, calls, registry, stdout } = retag(t, [IMAGE, SOURCE, 'latest'], {
    registry: { [SOURCE]: DIGEST },
    env: { DRY_RUN: '1' },
  });
  assert.equal(status, 0);
  assert.deepEqual(calls, [inspected(SOURCE)]);
  assert.ok(stdout.includes(`+ docker buildx imagetools create --tag ${IMAGE}:latest ${IMAGE}@${DIGEST}`));
  assert.deepEqual(registry, { [SOURCE]: DIGEST });
});

test('an image CI never built is an error that says so', { skip: !canRunRetag }, (t) => {
  const { status, calls, stderr } = retag(t, [IMAGE, SOURCE, '0.1.0']);
  assert.equal(status, 1);
  assert.deepEqual(calls, [inspected(SOURCE)], 'nothing else is asked of the registry, and nothing is pushed');
  assert.match(stderr, /sha-0a1b2c3 is not in the registry/);
});

test('a new tag that does not carry the source digest fails the step', { skip: !canRunRetag }, (t) => {
  const { status, stderr } = retag(t, [IMAGE, SOURCE, '0.1.0'], {
    registry: { [SOURCE]: DIGEST },
    env: { STUB_CREATE_DIGEST: REBUILT },
  });
  assert.equal(status, 1);
  assert.match(stderr, /0\.1\.0 is sha256:b+, expected sha256:a+/);
});

test('retag refuses to run without a source and at least one new tag', { skip: !canRunRetag }, (t) => {
  const { status, calls } = retag(t, [IMAGE, SOURCE]);
  assert.equal(status, 2);
  assert.equal(calls.length, 0);
});

// A full re-run of a released commit's CI run builds the commit again and
// re-points sha-<commit> at the new build. The release must not follow it.
test('a version tag is write-once: a rebuilt commit cannot move it, and nothing else moves', { skip: !canRunRetag }, (t) => {
  const before = { [SOURCE]: REBUILT, '0.1.0': DIGEST, '0.1': DIGEST, latest: DIGEST };
  const { status, calls, registry, stderr } = retag(t, [IMAGE, SOURCE, '0.1.0', '0.1'], { registry: before });
  assert.equal(status, 1);
  assert.match(stderr, /0\.1\.0 already names sha256:a+, and a version tag is never moved/);
  assert.match(stderr, /sha-0a1b2c3 is now sha256:b+/);
  assert.deepEqual(pushes(calls), [], 'not even the movable 0.1 is pushed');
  assert.deepEqual(registry, before);
});

test('the refusal does not depend on the order the tags are given in', { skip: !canRunRetag }, (t) => {
  const before = { [SOURCE]: REBUILT, '0.1.0': DIGEST, '0.1': DIGEST };
  const { status, calls, registry } = retag(t, [IMAGE, SOURCE, '0.1', '0.1.0'], { registry: before });
  assert.equal(status, 1);
  assert.deepEqual(pushes(calls), []);
  assert.deepEqual(registry, before);
});

test('tagging a release again with the image it already has is not an error', { skip: !canRunRetag }, (t) => {
  const before = { [SOURCE]: DIGEST, '0.1.0': DIGEST, '0.1': DIGEST };
  const again = retag(t, [IMAGE, SOURCE, '0.1.0', '0.1'], { registry: before });
  assert.equal(again.status, 0, again.stderr);
  // The version tag is left alone; only the movable one is pushed.
  assert.deepEqual(pushes(again.calls), [`buildx imagetools create --tag ${IMAGE}:0.1 ${IMAGE}@${DIGEST}`]);
  assert.match(again.stdout, /0\.1\.0 already names sha256:a+; leaving it/);
  assert.deepEqual(again.registry, before);

  const alone = retag(t, [IMAGE, SOURCE, '0.1.0'], { registry: before });
  assert.equal(alone.status, 0, alone.stderr);
  assert.deepEqual(pushes(alone.calls), []);
  assert.match(alone.stdout, /nothing to push/);
});

test('a version tag is not pushed while the registry cannot say whether it exists', { skip: !canRunRetag }, (t) => {
  const before = { [SOURCE]: DIGEST };
  const { status, calls, registry, stderr } = retag(t, [IMAGE, SOURCE, '0.1.0', '0.1'], {
    registry: before,
    env: { STUB_UNREADABLE_TAG: '0.1.0' },
  });
  assert.equal(status, 1);
  assert.match(stderr, /503 Service Unavailable/, 'the registry answer is shown');
  assert.match(stderr, /could not tell whether quay\.io\/example\/dashboard:0\.1\.0 already exists/);
  assert.deepEqual(pushes(calls), []);
  assert.deepEqual(registry, before);
});

test('latest and X.Y are meant to move, and still do', { skip: !canRunRetag }, (t) => {
  const latest = retag(t, [IMAGE, SOURCE, 'latest'], { registry: { [SOURCE]: REBUILT, latest: DIGEST } });
  assert.equal(latest.status, 0, latest.stderr);
  assert.equal(latest.registry.latest, REBUILT);
  assert.deepEqual(latest.calls.slice(0, 2), [
    inspected(SOURCE),
    `buildx imagetools create --tag ${IMAGE}:latest ${IMAGE}@${REBUILT}`,
  ]);

  // The next patch release: 0.1 moves to it, 0.1.0 stays where it was.
  const patch = retag(t, [IMAGE, SOURCE, '0.1.1', '0.1'], {
    registry: { [SOURCE]: REBUILT, '0.1.0': DIGEST, '0.1': DIGEST },
  });
  assert.equal(patch.status, 0, patch.stderr);
  assert.deepEqual(patch.registry, { [SOURCE]: REBUILT, '0.1.0': DIGEST, '0.1.1': REBUILT, '0.1': REBUILT });
});

// --- latest only moves forward ---------------------------------------------

// Under a git hook GIT_DIR and friends point at the real repository; without
// this the throwaway commits and tags below would land there.
const cleanGitEnv = () =>
  Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.startsWith('GIT_')));
const gitIdentity = ['-c', 'user.name=t', '-c', 'user.email=t@example.com', '-c', 'commit.gpgsign=false', '-c', 'tag.gpgsign=false'];

test('branch-tip.sh tells the tip of the remote branch from a commit it has moved past', { skip: !have('bash') }, (t) => {
  const dir = tempDir(t);
  const origin = join(dir, 'origin.git');
  const work = join(dir, 'work');
  const env = cleanGitEnv();
  const git = (cwd, ...args) => execFileSync('git', [...gitIdentity, ...args], { cwd, env, encoding: 'utf8' }).trim();
  const branchTip = (...args) => {
    const result = spawnSync(join(repoRoot, 'scripts', 'branch-tip.sh'), args, { cwd: work, env, encoding: 'utf8' });
    return { ...result, outputs: Object.fromEntries(result.stdout.split('\n').filter(Boolean).map((line) => line.split('='))) };
  };

  git(dir, 'init', '--quiet', '--bare', origin);
  git(dir, 'init', '--quiet', '--initial-branch=main', work);
  git(work, 'remote', 'add', 'origin', origin);
  git(work, 'commit', '--quiet', '--allow-empty', '-m', 'feat: older');
  const older = git(work, 'rev-parse', 'HEAD');
  git(work, 'push', '--quiet', 'origin', 'main');

  // The only commit on main is its tip.
  let result = branchTip('origin', 'main', older);
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.outputs, { is_tip: 'true', tip: older });

  git(work, 'commit', '--quiet', '--allow-empty', '-m', 'feat: newer');
  const newer = git(work, 'rev-parse', 'HEAD');
  git(work, 'push', '--quiet', 'origin', 'main');

  // A re-run of the older commit's workflow run: main has moved on.
  result = branchTip('origin', 'main', older);
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(result.outputs, { is_tip: 'false', tip: newer });
  assert.match(result.stderr, /Standing down/);

  result = branchTip('origin', 'main', newer);
  assert.deepEqual(result.outputs, { is_tip: 'true', tip: newer });

  // The answer comes from the remote. A local checkout of the older commit,
  // which is what a re-run has, must not make it look like the tip.
  git(work, 'checkout', '--quiet', '--detach', older);
  assert.equal(branchTip('origin', 'main', older).outputs.is_tip, 'false');

  // Not knowing is an error, not "false" and certainly not "true".
  for (const args of [
    ['origin', 'no-such-branch', newer],
    [join(dir, 'no-such-remote.git'), 'main', newer],
  ]) {
    result = branchTip(...args);
    assert.equal(result.status, 1, args.join(' '));
    assert.equal(result.stdout, '', 'no is_tip output at all');
    assert.match(result.stderr, /could not read refs\/heads\//);
  }
  assert.equal(branchTip('origin', 'main').status, 2);
});

test('promote-latest touches the registry only after its commit is confirmed as the tip of main', () => {
  const workflow = readFileSync(join(repoRoot, '.github', 'workflows', 'ci.yml'), 'utf8');
  const start = workflow.indexOf('\n  promote-latest:\n');
  assert.notEqual(start, -1, 'ci.yml has a promote-latest job');
  const rest = workflow.slice(start + 1);
  const nextJob = rest.slice(1).search(/\n {2}[a-z][a-z0-9-]*:\n/);
  const job = nextJob === -1 ? rest : rest.slice(0, nextJob + 1);
  const steps = job.split('\n      - ').slice(1);

  const tip = steps.findIndex((step) => step.includes('id: tip'));
  assert.notEqual(tip, -1, 'the job has the tip check');
  assert.ok(steps[tip].includes('scripts/branch-tip.sh origin main "$GITHUB_SHA" | tee -a "$GITHUB_OUTPUT"'));
  // Without pipefail a failing check would hide behind tee and read as "not the tip".
  assert.ok(steps[tip].includes('set -euo pipefail'));

  // Everything before the check is the checkout that provides the script.
  const before = steps.slice(0, tip).map((step) => step.split('\n')[0]);
  assert.equal(before.length, 1);
  assert.match(before[0], /^uses: actions\/checkout@[0-9a-f]{40} # v\d/);
  // Everything after it stands down with it: no login, no retag.
  const after = steps.slice(tip + 1);
  assert.ok(after.some((step) => step.includes('docker/login-action')));
  assert.ok(after.some((step) => step.includes('scripts/retag-image.sh') && step.includes(' latest')));
  for (const step of after) {
    assert.ok(step.includes("if: steps.tip.outputs.is_tip == 'true'"), `unconditional step:\n${step}`);
  }
});

// --- the detection script against a real repository ------------------------

test('released-version.mjs reports the release at HEAD, and nothing when there is none', (t) => {
  const repo = tempDir(t);
  const env = cleanGitEnv();
  const git = (...args) => execFileSync('git', [...gitIdentity, ...args], { cwd: repo, env, encoding: 'utf8' }).trim();
  const detect = () =>
    Object.fromEntries(
      execFileSync(process.execPath, [join(repoRoot, 'scripts', 'release', 'released-version.mjs')], {
        cwd: repo,
        env,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'ignore'],
      })
        .split('\n')
        .filter(Boolean)
        .map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]),
    );

  git('init', '--quiet', '--initial-branch=main');
  // The line comes from the pins of the commit that is checked out.
  mkdirSync(join(repo, 'deploy', 'ci'), { recursive: true });
  writeFileSync(join(repo, 'deploy', 'ci', 'gateway-pins.json'), JSON.stringify({ release: '0.1.3', sdk: SDK }));
  git('add', '.');
  git('commit', '--quiet', '-m', 'feat: one');
  git('tag', 'v0.1.0');
  git('commit', '--quiet', '--allow-empty', '-m', 'ci: two');
  const head = git('rev-parse', 'HEAD');

  assert.deepEqual(detect(), {
    version: '',
    git_tag: '',
    image_tags: '',
    source_tag: `sha-${head.slice(0, 7)}`,
    sha: head,
  });

  // A tag of another line on this commit changes nothing.
  git('tag', 'v1.2.0');
  assert.equal(detect().version, '');

  git('tag', 'v0.1.1');
  assert.deepEqual(detect(), {
    version: '0.1.1',
    git_tag: 'v0.1.1',
    image_tags: '0.1.1 0.1',
    source_tag: `sha-${head.slice(0, 7)}`,
    sha: head,
  });
});
