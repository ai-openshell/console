// node --test "scripts/**/*.test.mjs"
//
// cut-release.mjs against real repositories: a throwaway clone with a bare
// repository on disk as its `origin`, and a stand-in `gh` that does to that
// origin what GitHub does with the request (creates the tag at the target).
// Nothing here reaches the network, and nothing reads this repository's own
// tags: CI checks it out without them.
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

import { lineOfBranch } from './cut-release.mjs';

const scripts = dirname(fileURLToPath(import.meta.url));
const SDK = 'v0.0.0-20261009050449-e1f3c82caa3e';
const REPO_URL = 'https://github.com/example/dashboard';

// Under a git hook GIT_DIR and friends point at the real repository, and on a
// runner GITHUB_STEP_SUMMARY is the real run's summary. Neither may reach the
// scripts under test.
const cleanEnv = () =>
  Object.fromEntries(
    Object.entries(process.env).filter(([name]) => !name.startsWith('GIT_') && !name.startsWith('GITHUB_')),
  );
const gitIdentity = ['-c', 'user.name=t', '-c', 'user.email=t@example.com', '-c', 'commit.gpgsign=false', '-c', 'tag.gpgsign=false'];

/**
 * A clone to release from, its bare origin, and a stand-in `gh` on PATH.
 *
 * `commit(subject, { pinned })` adds a commit; with `pinned` it also moves the
 * pins to that OpenShell release, which is what decides the line.
 */
function repository(t, { pinned = '0.1.3' } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'openshell-dashboard-cut-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const origin = join(dir, 'origin.git');
  const work = join(dir, 'work');
  const bin = join(dir, 'bin');
  const ghLog = join(dir, 'gh.log');
  const published = join(dir, 'published-notes.md');
  const env = { ...cleanEnv(), PATH: `${bin}${delimiter}${process.env.PATH}`, STUB_LOG: ghLog, STUB_ORIGIN: origin, STUB_NOTES: published };

  const git = (cwd, ...args) => execFileSync('git', [...gitIdentity, ...args], { cwd, env, encoding: 'utf8' }).trim();

  mkdirSync(bin);
  writeFileSync(ghLog, '');
  writeFileSync(
    join(bin, 'gh'),
    `#!/usr/bin/env node
const { appendFileSync, readFileSync, writeFileSync } = require('node:fs');
const { execFileSync } = require('node:child_process');
const args = process.argv.slice(2);
appendFileSync(process.env.STUB_LOG, JSON.stringify(args) + '\\n');
if (process.env.STUB_GH_FAILS) {
  process.stderr.write('HTTP 422: Validation Failed (https://api.github.com/repos/example/dashboard/releases)\\n');
  process.exit(1);
}
if (args[0] === 'release' && args[1] === 'create') {
  const value = (flag) => args[args.indexOf(flag) + 1];
  // GitHub creates the tag at the target when it creates the release.
  execFileSync('git', ['--git-dir', process.env.STUB_ORIGIN, 'tag', args[2], value('--target')]);
  writeFileSync(process.env.STUB_NOTES, readFileSync(value('--notes-file'), 'utf8'));
}
`,
  );
  chmodSync(join(bin, 'gh'), 0o755);

  git(dir, 'init', '--quiet', '--bare', '--initial-branch=main', origin);
  git(dir, 'init', '--quiet', '--initial-branch=main', work);
  git(work, 'remote', 'add', 'origin', origin);
  mkdirSync(join(work, 'deploy', 'ci'), { recursive: true });
  mkdirSync(join(work, 'frontend'));
  writeFileSync(join(work, 'frontend', 'package.json'), JSON.stringify({ repository: { url: REPO_URL } }));

  const pin = (release) =>
    writeFileSync(join(work, 'deploy', 'ci', 'gateway-pins.json'), `${JSON.stringify({ release, sdk: SDK })}\n`);
  const commit = (subject, { pinned: release } = {}) => {
    if (release) {
      pin(release);
    }
    git(work, 'add', '--all');
    git(work, 'commit', '--quiet', '--allow-empty', '-m', subject);
    return git(work, 'rev-parse', 'HEAD');
  };
  const run = (script, args, extraEnv = {}) =>
    spawnSync(process.execPath, [join(scripts, script), ...args], {
      cwd: work,
      env: { ...env, ...extraEnv },
      encoding: 'utf8',
    });

  if (pinned) {
    commit(`fix: move to OpenShell ${pinned}`, { pinned });
  }
  return {
    work,
    origin,
    commit,
    git: (...args) => git(work, ...args),
    push: (...refs) => git(work, 'push', '--quiet', 'origin', ...refs),
    cut: (args, extraEnv) => run('cut-release.mjs', args, extraEnv),
    released: () =>
      Object.fromEntries(
        run('released-version.mjs', [])
          .stdout.split('\n')
          .filter(Boolean)
          .map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]),
      ),
    ghCalls: () => readFileSync(ghLog, 'utf8').split('\n').filter(Boolean).map((line) => JSON.parse(line)),
    publishedNotes: () => readFileSync(published, 'utf8'),
    tags: () => git(work, 'tag', '--list').split('\n').filter(Boolean),
    originTags: () => git(dir, '--git-dir', origin, 'tag', '--list').split('\n').filter(Boolean),
  };
}

/** The value printed beside a label in the plan. */
const row = (stdout, label) => {
  const line = stdout.split('\n').find((candidate) => candidate.trimStart().startsWith(label));
  return line === undefined ? undefined : line.trimStart().slice(label.length).trim();
};

// --- a dry run ---------------------------------------------------------------

test('a dry run says the version, every tag it was worked out from and the notes, and creates nothing', (t) => {
  const repo = repository(t);
  repo.git('tag', 'v0.1.0');
  repo.commit('feat: add provider profile detail page (#101)');
  repo.git('tag', 'v0.1.1');
  repo.commit('fix: handle an empty workspace list (#102)');
  repo.commit('ci: pin the runners (#103)');
  const head = repo.commit('docs: say how releases are numbered');
  repo.push('main', '--tags');

  const { status, stdout, stderr } = repo.cut(['--branch', 'main', '--dry-run']);
  assert.equal(status, 0, stderr);
  assert.ok(stdout.startsWith(`Dry run for ${head.slice(0, 7)} on main: this is what a release would be. Nothing is created.`), stdout);
  assert.equal(row(stdout, 'Gateway release line'), '0.1.x   (deploy/ci/gateway-pins.json pins OpenShell 0.1.3)');
  assert.equal(row(stdout, 'Release tags counted'), 'v0.1.0 v0.1.1   (2 tags on this line; the highest patch is 1)');
  assert.equal(row(stdout, 'Version'), '0.1.2   (tag v0.1.2)');
  assert.equal(row(stdout, 'Release tags ignored'), 'none on other lines');
  assert.equal(
    row(stdout, 'Previous release'),
    "v0.1.1   (the nearest release in this commit's history; 3 commits since, 2 listed in the notes)",
  );
  assert.equal(row(stdout, 'GitHub "Latest"'), 'yes');
  assert.equal(row(stdout, 'Image tags'), '0.1.2 (written once) and 0.1 (moves to this release)');
  assert.equal(row(stdout, 'Helm chart'), '0.1.2');
  assert.doesNotMatch(stdout, /WARNING/);

  // The notes it would publish, in full.
  const notes = stdout.slice(stdout.indexOf('Release notes for v0.1.2:'));
  assert.ok(notes.includes('### Changes since v0.1.1'), notes);
  assert.ok(notes.includes('- fix: handle an empty workspace list (#102) ('), notes);
  assert.ok(notes.includes(`- docs: say how releases are numbered ([${head.slice(0, 7)}](${REPO_URL}/commit/${head}))`), notes);
  assert.ok(!notes.includes('pin the runners'), 'a commit about CI is not in the notes');
  assert.ok(notes.includes(`[Everything between v0.1.1 and v0.1.2](${REPO_URL}/compare/v0.1.1...v0.1.2)`), notes);
  assert.ok(notes.includes('**0.1.x**, tested on 0.1.3, built against OpenShell Go SDK'), notes);

  assert.deepEqual(repo.ghCalls(), [], 'gh is not run at all');
  assert.deepEqual(repo.tags(), ['v0.1.0', 'v0.1.1']);
  assert.deepEqual(repo.originTags(), ['v0.1.0', 'v0.1.1']);
  assert.equal(repo.released().version, '', 'and the next step finds no release to tag an image for');
});

test('the run summary gets the plan and the notes', (t) => {
  const repo = repository(t);
  const summary = join(repo.work, '..', 'summary.md');
  writeFileSync(summary, '');
  const { status, stdout } = repo.cut(['--branch', 'main', '--dry-run'], { GITHUB_STEP_SUMMARY: summary, GITHUB_ACTIONS: 'true' });
  assert.equal(status, 0);
  const written = readFileSync(summary, 'utf8');
  assert.ok(written.startsWith('### Dry run: v0.1.0\n'), written);
  assert.ok(written.includes('Release tags counted   none   (no release tag on this line yet, so it starts at 0.1.0)'), written);
  assert.ok(written.includes('#### Release notes for v0.1.0\n\n### Changes\n\nNo earlier release exists'), written);
  assert.doesNotMatch(stdout, /::warning/);
});

// --- release tags this script did not cut ------------------------------------

// A repository with release tags this script did not cut, as an old clone
// could push them back: v0.1.3, v0.2.0 and v1.0.0 on commits that pin nothing,
// and v1.2.0 on a commit whose pins file lists lanes.
function withOldTags(t) {
  const repo = repository(t, { pinned: null });
  repo.commit('feat: the first dashboard');
  repo.git('tag', 'v0.1.3');
  repo.commit('feat: workspaces');
  repo.git('tag', 'v0.2.0');
  repo.commit('feat!: resync the SDK');
  repo.git('tag', 'v1.0.0');
  // v1.2.0 goes on a commit whose pins file lists lanes.
  mkdirSync(join(repo.work, 'deploy', 'ci'), { recursive: true });
  writeFileSync(
    join(repo.work, 'deploy', 'ci', 'gateway-pins.json'),
    JSON.stringify({ sdk: SDK, lanes: [{ version: '0.1.2', required: true }, { version: '0.1.0', required: true }] }),
  );
  repo.commit('feat: a runtime compatibility notice (#81)');
  repo.git('tag', 'v1.2.0');
  repo.commit('feat: bring the dashboard to parity with OpenShell 0.1.2 (#95)');
  repo.commit('fix: move to OpenShell 0.1.3 (#106)', { pinned: '0.1.3' });
  return repo;
}

test('a tag that was not cut for the line raises the version to 0.1.4, and the run says which tag made it so', (t) => {
  const repo = withOldTags(t);
  const { status, stdout, stderr } = repo.cut(['--branch', 'main', '--dry-run'], { GITHUB_ACTIONS: 'true' });
  assert.equal(status, 0, stderr);
  assert.equal(row(stdout, 'Release tags counted'), 'v0.1.3   (1 tag on this line; the highest patch is 3)');
  assert.equal(row(stdout, 'Version'), '0.1.4   (tag v0.1.4)');
  assert.equal(row(stdout, 'Release tags ignored'), 'v0.2.0 v1.0.0 v1.2.0   (on other lines)');
  // The notes list what came after the last release in the history, which is
  // v1.2.0, whatever it is numbered.
  assert.match(row(stdout, 'Previous release'), /^v1\.2\.0 {3}\(the nearest release in this commit's history; 2 commits since/);
  assert.equal(row(stdout, 'GitHub "Latest"'), 'no   (v1.2.0 is a higher version)');
  assert.ok(
    stdout.includes(
      'WARNING: v0.1.3 was counted, but the commit it points at was not built for gateway 0.1.x ' +
        '(its deploy/ci/gateway-pins.json names no OpenShell release).',
    ),
    stdout,
  );
  assert.match(stdout, /^::warning title=A counted release tag was not built for this line::v0\.1\.3 was counted/m);
  assert.ok(stdout.includes('### Changes since v1.2.0'), stdout);
  // v1.2.0 was for the same gateway line, so nothing is said about a change of line.
  assert.doesNotMatch(stdout, /release line changed/);
});

// A real run need not follow a dry run, and a release cannot be renumbered
// afterwards.
test('a real run does not publish a number that such a tag made too high', (t) => {
  const repo = withOldTags(t);
  repo.push('main', '--tags');
  const before = repo.originTags();

  const { status, stdout, stderr } = repo.cut(['--branch', 'main']);
  assert.equal(status, 1);
  // It still shows what it worked out, and why.
  assert.equal(row(stdout, 'Version'), '0.1.4   (tag v0.1.4)');
  assert.match(stdout, /WARNING: v0\.1\.3 was counted/);
  assert.match(
    stderr,
    /^cut-release: not releasing v0\.1\.4: v0\.1\.3 was counted on the 0\.1\.x line and does not point at a commit built for it/,
  );
  assert.deepEqual(repo.ghCalls(), []);
  assert.deepEqual(repo.originTags(), before);

  // With that tag gone the same commit is released, as the first of its line.
  // The other tags are on other lines and do not stop it.
  repo.git('tag', '--delete', 'v0.1.3');
  const released = repo.cut(['--branch', 'main']);
  assert.equal(released.status, 0, released.stderr);
  assert.equal(row(released.stdout, 'Version'), '0.1.0   (tag v0.1.0)');
  assert.equal(repo.released().image_tags, '0.1.0 0.1');
  // Not the highest version while v1.2.0 exists, so not marked Latest.
  assert.ok(repo.ghCalls()[0].includes('--latest=false'));
});

test('without those tags the first release is 0.1.0, and its notes open with a fixed sentence', (t) => {
  const repo = withOldTags(t);
  repo.git('tag', '--delete', 'v0.1.3', 'v0.2.0', 'v1.0.0', 'v1.2.0');
  const { status, stdout, stderr } = repo.cut(['--branch', 'main', '--dry-run'], { GITHUB_ACTIONS: 'true' });
  assert.equal(status, 0, stderr);
  assert.equal(row(stdout, 'Release tags counted'), 'none   (no release tag on this line yet, so it starts at 0.1.0)');
  assert.equal(row(stdout, 'Version'), '0.1.0   (tag v0.1.0)');
  assert.equal(row(stdout, 'Previous release'), "none   (no release in this commit's history, so the notes list no changes)");
  assert.equal(row(stdout, 'GitHub "Latest"'), 'yes');
  assert.doesNotMatch(stdout, /WARNING|::warning/);
  assert.ok(
    stdout.includes('### Changes\n\nNo earlier release exists to compare this one with, so its changes are not listed.\n\n### Supported OpenShell gateways'),
    stdout,
  );
  assert.doesNotMatch(stdout, /the first dashboard|parity with OpenShell/, 'the whole history is not listed');
});

// --- a release branch --------------------------------------------------------

// main released 0.1.0 and 0.1.1, release/0.1 was cut from it, and main moved
// to gateway 0.2.
function withReleaseBranch(t) {
  const repo = repository(t);
  repo.git('tag', 'v0.1.0');
  repo.commit('feat: sandbox templates (#110)');
  repo.git('tag', 'v0.1.1');
  repo.git('branch', 'release/0.1');
  repo.commit('fix: move to OpenShell 0.2.0 (#120)', { pinned: '0.2.0' });
  repo.commit('feat: something only 0.2 has (#121)');
  repo.git('switch', '--quiet', 'release/0.1');
  repo.commit('fix: a security fix for the 0.1 line (#122)');
  return repo;
}

test('on release/0.1 the same computation gives that line its next patch', (t) => {
  const repo = withReleaseBranch(t);
  const { status, stdout, stderr } = repo.cut(['--branch', 'release/0.1', '--dry-run']);
  assert.equal(status, 0, stderr);
  assert.equal(row(stdout, 'Gateway release line'), '0.1.x   (deploy/ci/gateway-pins.json pins OpenShell 0.1.3)');
  assert.equal(row(stdout, 'Release tags counted'), 'v0.1.0 v0.1.1   (2 tags on this line; the highest patch is 1)');
  assert.equal(row(stdout, 'Version'), '0.1.2   (tag v0.1.2)');
  assert.equal(row(stdout, 'Image tags'), '0.1.2 (written once) and 0.1 (moves to this release)');
  assert.ok(stdout.includes('### Changes since v0.1.1\n\n- fix: a security fix for the 0.1 line (#122) ('), stdout);
  assert.doesNotMatch(stdout, /only 0\.2 has/, 'what is on main only is not in a release of release/0.1');
});

test('main, on the next line, starts it at .0 and says the line changed', (t) => {
  const repo = withReleaseBranch(t);
  repo.git('switch', '--quiet', 'main');
  const { status, stdout, stderr } = repo.cut(['--branch', 'main', '--dry-run']);
  assert.equal(status, 0, stderr);
  assert.equal(row(stdout, 'Release tags counted'), 'none   (no release tag on this line yet, so it starts at 0.2.0)');
  assert.equal(row(stdout, 'Version'), '0.2.0   (tag v0.2.0)');
  assert.equal(row(stdout, 'Release tags ignored'), 'v0.1.0 v0.1.1   (on other lines)');
  assert.ok(stdout.includes('### Changes since v0.1.1'), stdout);
  assert.ok(stdout.includes('> **The supported gateway release line changed in this release.** v0.1.1 supported 0.1.x.'), stdout);
});

test('a patch of the older line, cut after the newer line has a release, moves only its own line', (t) => {
  const repo = withReleaseBranch(t);
  repo.push('main', 'release/0.1', '--tags');
  repo.git('switch', '--quiet', 'main');
  assert.equal(repo.cut(['--branch', 'main']).status, 0);
  assert.equal(repo.released().image_tags, '0.2.0 0.2');

  repo.git('switch', '--quiet', 'release/0.1');
  const { status, stdout, stderr } = repo.cut(['--branch', 'release/0.1']);
  assert.equal(status, 0, stderr);
  assert.equal(row(stdout, 'Version'), '0.1.2   (tag v0.1.2)');
  assert.equal(row(stdout, 'GitHub "Latest"'), 'no   (v0.2.0 is a higher version)');
  assert.ok(repo.ghCalls()[1].includes('--latest=false'));
  assert.equal(repo.released().image_tags, '0.1.2 0.1');
  // And main's next release is still the next patch of its own line.
  repo.git('switch', '--quiet', 'main');
  repo.commit('fix: another (#130)');
  assert.equal(row(repo.cut(['--branch', 'main', '--dry-run']).stdout, 'Version'), '0.2.1   (tag v0.2.1)');
});

test('a release branch that pins another line than its own is refused', (t) => {
  const repo = withReleaseBranch(t);
  repo.commit('fix: move to OpenShell 0.2.0 by mistake', { pinned: '0.2.0' });
  const { status, stderr } = repo.cut(['--branch', 'release/0.1', '--dry-run']);
  assert.equal(status, 1);
  assert.match(stderr, /release\/0\.1 pins OpenShell 0\.2\.0, which is release line 0\.2\. A release branch releases its own line/);
});

test('only main and release/<major>.<minor> are released', () => {
  assert.equal(lineOfBranch('main'), null);
  assert.equal(lineOfBranch('release/0.1'), '0.1');
  assert.equal(lineOfBranch('release/12.30'), '12.30');
  for (const branch of ['next', '0.2.x', 'release/0.1.x', 'release/0.1.2', 'release/01.2', 'release/', 'feature/release/0.1', '', undefined]) {
    assert.throws(() => lineOfBranch(branch), /releases are cut from main or from release\/<major>\.<minor>/, String(branch));
  }
});

// --- what stops a release ----------------------------------------------------

test('a pre-release pin never releases', (t) => {
  const repo = repository(t);
  repo.git('tag', 'v0.1.0');
  repo.commit('fix: move to OpenShell 0.1.4-pre.2', { pinned: '0.1.4-pre.2' });
  for (const args of [['--branch', 'main', '--dry-run'], ['--branch', 'main']]) {
    const { status, stdout, stderr } = repo.cut(args, { GITHUB_ACTIONS: 'true' });
    assert.equal(status, 1);
    assert.match(stderr, /^cut-release: this branch pins OpenShell 0\.1\.4-pre\.2, a pre-release\. Nothing is released from a pre-release pin/);
    assert.match(stdout, /^::error title=No release was cut::this branch pins OpenShell 0\.1\.4-pre\.2/);
  }
  assert.deepEqual(repo.ghCalls(), []);
  assert.deepEqual(repo.tags(), ['v0.1.0']);
});

// release/0.1 is created from main before the move to 0.2 merges, so for a
// while both are on the 0.1 line. A release from main in that window is not on the
// release branch.
test('a commit that does not contain the newest release of its line is refused', (t) => {
  const repo = repository(t);
  repo.git('tag', 'v0.1.0');
  repo.git('branch', 'release/0.1');
  repo.commit('feat: reaches main after the branch was created (#140)');
  repo.git('tag', 'v0.1.1');
  repo.git('switch', '--quiet', 'release/0.1');
  const head = repo.commit('fix: a fix for the 0.1 line (#141)');

  const { status, stderr } = repo.cut(['--branch', 'release/0.1', '--dry-run']);
  assert.equal(status, 1);
  assert.ok(
    stderr.includes(
      `v0.1.1, the newest release of the 0.1.x line, is not in the history of ${head.slice(0, 7)}. ` +
        'A release from here would be numbered v0.1.2 without containing v0.1.1',
    ),
    stderr,
  );
  // Brought up to it, the branch releases.
  repo.git('merge', '--quiet', '--no-edit', 'v0.1.1');
  assert.equal(row(repo.cut(['--branch', 'release/0.1', '--dry-run']).stdout, 'Version'), '0.1.2   (tag v0.1.2)');
});

// The release-tooling job in ci.yml is such a checkout, and publish.yml would
// be one if its fetch-depth were ever dropped: v0.1.0 exists and is not there.
test('a shallow checkout is refused instead of numbering the release .0', (t) => {
  const repo = repository(t);
  repo.git('tag', 'v0.1.0');
  repo.commit('feat: one more (#101)');
  repo.push('main', '--tags');
  const shallow = join(repo.work, '..', 'shallow');
  repo.git('clone', '--quiet', '--depth', '1', '--no-tags', `file://${repo.origin}`, shallow);

  const { status, stderr } = spawnSync(process.execPath, [join(scripts, 'cut-release.mjs'), '--branch', 'main', '--dry-run'], {
    cwd: shallow,
    env: cleanEnv(),
    encoding: 'utf8',
  });
  assert.equal(status, 1);
  assert.match(stderr, /^cut-release: this checkout is shallow/);
});

test('a branch has to be named', (t) => {
  const repo = repository(t);
  const { status, stderr } = repo.cut(['--dry-run']);
  assert.equal(status, 1);
  assert.match(stderr, /--branch <main \| release\/X\.Y> is required/);
});

// --- a real run --------------------------------------------------------------

test('a real run creates the release at this commit, and the tag with it', (t) => {
  const repo = repository(t);
  repo.git('tag', 'v0.1.0');
  const head = repo.commit('feat: add provider profile detail page (#101)');
  repo.push('main', '--tags');

  const { status, stdout, stderr } = repo.cut(['--branch', 'main']);
  assert.equal(status, 0, stderr);
  assert.ok(stdout.startsWith(`The release of ${head.slice(0, 7)} on main:`), stdout);
  assert.ok(stdout.endsWith(`Released v0.1.1 at ${head}.\n`), stdout);

  const calls = repo.ghCalls();
  assert.equal(calls.length, 1);
  const [call] = calls;
  assert.deepEqual(call.slice(0, 7), ['release', 'create', 'v0.1.1', '--target', head, '--title', 'v0.1.1']);
  assert.equal(call[7], '--notes-file');
  assert.equal(call[9], '--latest=true');
  assert.equal(call.length, 10);

  // What was published is what the dry run showed.
  const notes = repo.publishedNotes();
  assert.ok(notes.startsWith('### Changes since v0.1.0\n\n- feat: add provider profile detail page (#101) ('), notes);
  assert.ok(stdout.includes(notes));

  // The tag is on the remote, where gh put it, and in the checkout, for the
  // step that asks which release is at this commit.
  assert.deepEqual(repo.originTags(), ['v0.1.0', 'v0.1.1']);
  assert.equal(repo.git('rev-parse', 'v0.1.1'), head);
  assert.deepEqual(repo.released(), {
    version: '0.1.1',
    git_tag: 'v0.1.1',
    image_tags: '0.1.1 0.1',
    source_tag: `sha-${head.slice(0, 7)}`,
    sha: head,
  });
});

test('running it again for a released commit cuts nothing, and the image step still finds the release', (t) => {
  const repo = repository(t);
  const head = repo.commit('feat: one (#101)');
  repo.push('main');
  assert.equal(repo.cut(['--branch', 'main']).status, 0);
  assert.equal(repo.ghCalls().length, 1);

  for (const args of [['--branch', 'main'], ['--branch', 'main', '--dry-run']]) {
    const again = repo.cut(args);
    assert.equal(again.status, 0, again.stderr);
    assert.ok(
      again.stdout.startsWith(`v0.1.0 already points at ${head.slice(0, 7)} on main: this commit is released, and there is nothing to cut.`),
      again.stdout,
    );
  }
  assert.equal(repo.ghCalls().length, 1, 'no second release');
  assert.deepEqual(repo.tags(), ['v0.1.0']);
  assert.equal(repo.released().image_tags, '0.1.0 0.1');
});

test('when GitHub refuses the release nothing is left behind', (t) => {
  const repo = repository(t);
  repo.push('main');
  const { status, stderr } = repo.cut(['--branch', 'main'], { STUB_GH_FAILS: '1' });
  assert.equal(status, 1);
  assert.match(stderr, /HTTP 422/, "gh's own message is shown");
  assert.match(stderr, /cut-release: gh release create v0\.1\.0 failed/);
  assert.deepEqual(repo.tags(), []);
  assert.deepEqual(repo.originTags(), []);
  assert.equal(repo.released().version, '');
});
