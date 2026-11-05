// node --test "scripts/**/*.test.mjs"
//
// publish.yml runs for real only on main and on release branches, after a
// merge. Two of its steps are shell written in the workflow file: the one that
// decides WHETHER a run releases, and the one that hands the answer to
// cut-release.mjs. These tests take those scripts out of the file and run
// them, with a stand-in `gh` that answers the two questions the first one
// asks GitHub (did CI pass for this commit, and which pull request merged it).
import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const workflow = readFileSync(join(repoRoot, '.github', 'workflows', 'publish.yml'), 'utf8');
const REPO = 'ai-openshell/console';
const haveBash = spawnSync('bash', ['--version'], { stdio: 'ignore' }).status === 0;

/** The shell of the step with this name, as the runner would run it. */
function stepScript(name) {
  const lines = workflow.split('\n');
  const start = lines.findIndex((line) => line.trim() === `- name: ${name}`);
  assert.notEqual(start, -1, `publish.yml has a step named "${name}"`);
  const stepIndent = lines[start].indexOf('- ');
  let run = -1;
  for (let i = start + 1; i < lines.length; i += 1) {
    if (lines[i].trim() !== '' && lines[i].search(/\S/) <= stepIndent) {
      break;
    }
    if (/^\s+run: \|$/.test(lines[i])) {
      run = i;
      break;
    }
  }
  assert.notEqual(run, -1, `the step "${name}" has a run: | script`);
  const keyIndent = lines[run].indexOf('run:');
  const body = [];
  for (let i = run + 1; i < lines.length; i += 1) {
    if (lines[i].trim() !== '' && lines[i].search(/\S/) <= keyIndent) {
      break;
    }
    body.push(lines[i].slice(keyIndent + 2));
  }
  return body.join('\n');
}

function tempDir(t) {
  const dir = mkdtempSync(join(tmpdir(), 'openshell-dashboard-publish-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return dir;
}

// Neither the real repository (GIT_*) nor the real run (GITHUB_*) may reach a
// script under test.
const cleanEnv = () =>
  Object.fromEntries(
    Object.entries(process.env).filter(([name]) => !name.startsWith('GIT_') && !name.startsWith('GITHUB_')),
  );

const fromNext = [
  { merged_at: '2026-10-14T06:30:00Z', base: { ref: 'main' }, head: { ref: 'next', repo: { full_name: REPO } } },
];
const ordinary = [
  { merged_at: '2026-10-14T06:30:00Z', base: { ref: 'main' }, head: { ref: 'fix/something', repo: { full_name: REPO } } },
];

/**
 * Runs the step that decides whether a run releases, in a checkout of one
 * commit, and returns what it wrote for the steps after it.
 */
function decide(t, { event, branch = 'main', dryRun = '', greenRuns = 1, pulls = ordinary }) {
  const dir = tempDir(t);
  const work = join(dir, 'work');
  const bin = join(dir, 'bin');
  const output = join(dir, 'output');
  const summary = join(dir, 'summary');
  const ghLog = join(dir, 'gh.log');
  mkdirSync(work);
  mkdirSync(bin);
  for (const file of [output, summary, ghLog]) {
    writeFileSync(file, '');
  }
  writeFileSync(
    join(bin, 'gh'),
    `#!/usr/bin/env node
const { appendFileSync } = require('node:fs');
const args = process.argv.slice(2);
appendFileSync(process.env.STUB_LOG, args.join(' ') + '\\n');
if (args[0] === 'api' && args[1].includes('/actions/workflows/ci.yml/runs?')) {
  process.stdout.write(process.env.STUB_GREEN_RUNS + '\\n');
} else if (args[0] === 'api' && /\\/commits\\/[0-9a-f]{40}\\/pulls$/.test(args[1])) {
  process.stdout.write(process.env.STUB_PULLS);
} else {
  process.stderr.write('unexpected gh call: ' + args.join(' ') + '\\n');
  process.exit(1);
}
`,
  );
  chmodSync(join(bin, 'gh'), 0o755);

  const env = cleanEnv();
  const git = (...args) =>
    execFileSync('git', ['-c', 'user.name=t', '-c', 'user.email=t@example.com', '-c', 'commit.gpgsign=false', ...args], {
      cwd: work,
      env,
      encoding: 'utf8',
    }).trim();
  git('init', '--quiet', '--initial-branch=main');
  git('commit', '--quiet', '--allow-empty', '-m', 'fix: move to OpenShell 0.1.4');
  const sha = git('rev-parse', 'HEAD');
  // The step runs this repository's script by its path from the checkout.
  symlinkSync(join(repoRoot, 'scripts'), join(work, 'scripts'));

  const result = spawnSync('bash', ['-c', stepScript('Does this run release?')], {
    cwd: work,
    encoding: 'utf8',
    env: {
      ...env,
      PATH: `${bin}${delimiter}${process.env.PATH}`,
      EVENT: event,
      BRANCH: branch,
      DRY_RUN: dryRun,
      REPO,
      GH_TOKEN: 'not-a-token',
      GITHUB_OUTPUT: output,
      GITHUB_STEP_SUMMARY: summary,
      STUB_LOG: ghLog,
      STUB_GREEN_RUNS: String(greenRuns),
      STUB_PULLS: JSON.stringify(pulls),
    },
  });
  return {
    ...result,
    sha,
    outputs: Object.fromEntries(
      readFileSync(output, 'utf8')
        .split('\n')
        .filter(Boolean)
        .map((line) => [line.slice(0, line.indexOf('=')), line.slice(line.indexOf('=') + 1)]),
    ),
    summary: readFileSync(summary, 'utf8'),
    gh: readFileSync(ghLog, 'utf8').split('\n').filter(Boolean),
  };
}

// --- nobody chooses the version ----------------------------------------------

test('the workflow asks for a dry run or not, and for nothing else', () => {
  const inputs = workflow.slice(workflow.indexOf('  workflow_dispatch:'), workflow.indexOf('  workflow_run:'));
  assert.deepEqual(
    inputs.split('\n').filter((line) => /^ {6}[a-z_]+:$/.test(line)).map((line) => line.trim()),
    ['dry_run:'],
  );
  assert.match(inputs, /^ {8}type: boolean\n {8}default: true$/m, 'and a dry run is the default');
  for (const gone of ['release_type', 'RELEASE_TYPE', 'semantic-release', '(choose one)']) {
    assert.ok(!workflow.includes(gone), `publish.yml still mentions ${gone}`);
  }
  // Nor does CI still test a release configuration that no longer exists.
  const ci = readFileSync(join(repoRoot, '.github', 'workflows', 'ci.yml'), 'utf8');
  for (const gone of ['semantic-release', 'release.config', 'check-release-config']) {
    assert.ok(!ci.includes(gone), `ci.yml still mentions ${gone}`);
  }
});

test('a person can start it on main or on a release branch, and nowhere else', () => {
  assert.ok(
    workflow.includes(
      "(github.event_name == 'workflow_dispatch' &&\n" +
        "       (github.ref == 'refs/heads/main' || startsWith(github.ref, 'refs/heads/release/'))) ||",
    ),
  );
  // The automatic path is still for a push to main in this repository only.
  assert.ok(workflow.includes("github.event.workflow_run.head_branch == 'main' &&"));
  assert.ok(workflow.includes('github.event.workflow_run.head_repository.full_name == github.repository)'));
});

// --- does this run release? --------------------------------------------------

test('asked for by a person: a dry run by default, on the branch it was started on', { skip: !haveBash }, (t) => {
  const run = decide(t, { event: 'workflow_dispatch', dryRun: 'true' });
  assert.equal(run.status, 0, run.stderr);
  assert.deepEqual(run.outputs, { release: 'true', dry_run: 'true', branch: 'main' });
  assert.equal(run.summary, 'Asked for by a person, on main (dry run).\n');
  // It asked whether CI passed for a push of exactly this commit.
  assert.deepEqual(run.gh, [
    `api repos/${REPO}/actions/workflows/ci.yml/runs?head_sha=${run.sha}&event=push&status=success&per_page=1 --jq .total_count`,
  ]);

  const real = decide(t, { event: 'workflow_dispatch', dryRun: 'false', branch: 'release/0.1' });
  assert.equal(real.status, 0, real.stderr);
  assert.deepEqual(real.outputs, { release: 'true', dry_run: 'false', branch: 'release/0.1' });
  assert.equal(real.summary, 'Asked for by a person, on release/0.1.\n');
});

test('a release needs a commit whose CI passed; a dry run only warns', { skip: !haveBash }, (t) => {
  const refused = decide(t, { event: 'workflow_dispatch', dryRun: 'false', greenRuns: 0 });
  assert.equal(refused.status, 1);
  assert.match(refused.stdout, /^::error::CI has not passed for [0-9a-f]{40}, so there is no tested image to release\./m);
  assert.deepEqual(refused.outputs, {}, 'nothing is handed to the steps after it');

  const dry = decide(t, { event: 'workflow_dispatch', dryRun: 'true', greenRuns: 0 });
  assert.equal(dry.status, 0, dry.stderr);
  assert.match(dry.stdout, /^::warning::CI has not passed for [0-9a-f]{40}\. This dry run goes ahead/m);
  assert.deepEqual(dry.outputs, { release: 'true', dry_run: 'true', branch: 'main' });
});

test('CI finished on main: the merge of next is released, for real, and nothing else is', { skip: !haveBash }, (t) => {
  const merged = decide(t, { event: 'workflow_run', pulls: fromNext });
  assert.equal(merged.status, 0, merged.stderr);
  assert.deepEqual(merged.outputs, { release: 'true', dry_run: 'false', branch: 'main' });
  assert.equal(merged.summary, 'Merged from next, the move to a new OpenShell release: releasing it.\n');
  assert.deepEqual(merged.gh, [`api repos/${REPO}/commits/${merged.sha}/pulls`]);

  const other = decide(t, { event: 'workflow_run', pulls: ordinary });
  assert.equal(other.status, 0, other.stderr);
  assert.deepEqual(other.outputs, { release: 'false', dry_run: 'false', branch: 'main' });
  assert.match(other.summary, /was not merged from next\. Nothing is released automatically/);

  // A commit pushed straight to main, and a fork's branch that is named next.
  assert.equal(decide(t, { event: 'workflow_run', pulls: [] }).outputs.release, 'false');
  const fork = [{ ...fromNext[0], head: { ref: 'next', repo: { full_name: 'someone/openshell-dashboard' } } }];
  assert.equal(decide(t, { event: 'workflow_run', pulls: fork }).outputs.release, 'false');
});

// A commit that is already released has its tag, and the step that asks the
// repository "was a release cut here?" would find it. In a dry run that
// answer must not reach the jobs that push image tags and the chart.
test('a dry run publishes nothing, even for a commit that is already released', () => {
  const steps = workflow.split('\n      - ');
  const released = steps.find((step) => step.startsWith('name: Was a release cut at this commit?\n'));
  assert.ok(released.includes("\n        if: steps.plan.outputs.dry_run != 'true'\n"), released);
  // The jobs after it run only on what that step reports.
  for (const job of ['tag-image', 'publish-chart']) {
    const body = workflow.slice(workflow.indexOf(`\n  ${job}:\n`));
    assert.ok(body.slice(0, 200).includes("if: needs.publish.outputs.version != ''"), job);
  }
  assert.ok(workflow.includes('version: ${{ steps.released.outputs.version }}'));
});

// --- handing it to cut-release.mjs -------------------------------------------

test('the release step runs cut-release.mjs for the branch, as a dry run only when told', { skip: !haveBash }, (t) => {
  const steps = workflow.split('\n      - ');
  const cut = steps.find((step) => step.startsWith('name: Cut the release\n'));
  assert.ok(cut.includes("if: steps.plan.outputs.release == 'true'"), 'it runs only when the step before said so');

  const dir = tempDir(t);
  const log = join(dir, 'node.log');
  // A stand-in `node` that records what it was asked to run.
  writeFileSync(join(dir, 'node'), '#!/usr/bin/env bash\nprintf \'%s\\n\' "$@" > "$STUB_LOG"\n');
  chmodSync(join(dir, 'node'), 0o755);
  const asked = (env) => {
    const result = spawnSync('bash', ['-c', stepScript('Cut the release')], {
      encoding: 'utf8',
      env: { ...cleanEnv(), PATH: `${dir}${delimiter}${process.env.PATH}`, STUB_LOG: log, ...env },
    });
    assert.equal(result.status, 0, result.stderr);
    return readFileSync(log, 'utf8').split('\n').filter(Boolean);
  };
  assert.deepEqual(asked({ BRANCH: 'main', DRY_RUN: 'true' }), ['scripts/release/cut-release.mjs', '--branch', 'main', '--dry-run']);
  assert.deepEqual(asked({ BRANCH: 'release/0.1', DRY_RUN: 'false' }), ['scripts/release/cut-release.mjs', '--branch', 'release/0.1']);
});
