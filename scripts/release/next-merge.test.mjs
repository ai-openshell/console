// Run with: node --test "scripts/**/*.test.mjs"
//
// A release is started by a person. The one automatic release is the merge of
// `next`, the move to a new OpenShell release. These tests cover what decides
// that: next-merge.mjs, which says whether a commit may be released without
// anyone asking. What version it gets is in next-version.test.mjs.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import test from 'node:test';
import { fileURLToPath } from 'node:url';

import { mergedFromNext } from './next-merge.mjs';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..', '..');

// ---- which commit may release without being asked ---------------------------

const REPO = 'ai-openshell/console';
const pull = (overrides = {}) => ({
  merged_at: '2026-10-14T06:30:00Z',
  base: { ref: 'main' },
  head: { ref: 'next', repo: { full_name: REPO } },
  title: 'fix: move to OpenShell 0.1.4',
  ...overrides,
});

test('a commit merged from the next branch of this repository is the move to a new release', () => {
  assert.equal(mergedFromNext([pull()], REPO), true);
  // The title plays no part: it is what a person could type.
  assert.equal(mergedFromNext([pull({ title: 'anything at all' })], REPO), true);
});

test('anything else is not', () => {
  // An ordinary pull request, whatever it is titled.
  const ordinary = { head: { ref: 'fix/something', repo: { full_name: REPO } } };
  assert.equal(mergedFromNext([pull(ordinary)], REPO), false);
  assert.equal(mergedFromNext([pull({ ...ordinary, title: 'fix: move to OpenShell 0.1.4' })], REPO), false);
  // A branch whose name only starts with it, or only contains it.
  for (const ref of ['next-steps', 'feature/next', 'nextish', 'bot/next']) {
    assert.equal(mergedFromNext([pull({ head: { ref, repo: { full_name: REPO } } })], REPO), false, ref);
  }
  // A fork that named its branch next.
  assert.equal(
    mergedFromNext([pull({ head: { ref: 'next', repo: { full_name: 'someone/openshell-dashboard' } } })], REPO),
    false,
  );
  // A fork that has since been deleted.
  assert.equal(mergedFromNext([pull({ head: { ref: 'next', repo: null } })], REPO), false);
  // Not merged, or merged somewhere other than main.
  assert.equal(mergedFromNext([pull({ merged_at: null })], REPO), false);
  assert.equal(mergedFromNext([pull({ base: { ref: 'release/0.1' } })], REPO), false);
  assert.equal(mergedFromNext([pull({ base: { ref: '0.2.x' } })], REPO), false);
  // A commit pushed straight to main has no pull request.
  assert.equal(mergedFromNext([], REPO), false);
  // Two merged pull requests for one commit is not something to guess about.
  assert.equal(mergedFromNext([pull(), pull()], REPO), false);
});

test('a pull request into next is not released when it merges; only next into main is', () => {
  const intoNext = pull({ base: { ref: 'next' }, head: { ref: 'feat/needs-0.1.4', repo: { full_name: REPO } } });
  assert.equal(mergedFromNext([intoNext], REPO), false);
  // Should GitHub list both for a commit that reached main through next, the
  // one into next does not count against the one into main.
  assert.equal(mergedFromNext([intoNext, pull()], REPO), true);
});

test('the command prints where the commit came from in GITHUB_OUTPUT form, and exits 0 either way', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'next-merge-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const pulls = join(dir, 'pulls.json');
  const run = (list) => {
    writeFileSync(pulls, JSON.stringify(list));
    return spawnSync(
      process.execPath,
      [join(repoRoot, 'scripts/release/next-merge.mjs'), '--sha', 'a'.repeat(40), '--repo', REPO, '--pulls', pulls],
      { encoding: 'utf8' },
    );
  };
  const ordinary = run([pull({ head: { ref: 'feat/something', repo: { full_name: REPO } } })]);
  assert.equal(ordinary.status, 0);
  assert.equal(ordinary.stdout, 'from=\n');
  assert.match(ordinary.stderr, /was not merged from the next branch/);

  const fromNext = run([pull()]);
  assert.equal(fromNext.status, 0);
  assert.equal(fromNext.stdout, 'from=next\n');

  // publish.yml reads the answer with `sed -n 's/^from=//p'`.
  const read = (stdout) => stdout.replace(/^from=/m, '').trim();
  assert.equal(read(ordinary.stdout), '');
  assert.equal(read(fromNext.stdout), 'next');
});

test('the command needs a commit and a repository', () => {
  const result = spawnSync(process.execPath, [join(repoRoot, 'scripts/release/next-merge.mjs')], { encoding: 'utf8' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /--sha <commit> and --repo <owner\/name> are required/);
});
