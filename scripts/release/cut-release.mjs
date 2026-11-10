#!/usr/bin/env node
// Cuts a dashboard release from the checked-out commit, or says what it would
// cut.
//
// publish.yml runs this when a person starts the workflow on main or on
// release/<major>.<minor>. Nothing else starts a release. This works out which
// release it is:
//
//   the version   <line>.<n>, from the OpenShell release this branch pins and
//                 the release tags that exist (next-version.mjs). Nobody
//                 chooses it and no commit title goes into it.
//   the notes     the commits since the previous release, then the gateways
//                 the release supports (release-notes.mjs).
//
// Then it creates the GitHub release, and GitHub creates the tag vX.Y.Z at
// this commit with it, in one request. The image tags and the chart follow
// from that tag in later jobs (released-version.mjs).
//
// With --dry-run it prints all of that and creates nothing. A dry run needs no
// token and no network; everything it says comes from the checkout. What it
// prints names every release tag the version was worked out from, because a
// tag that should not be there is the one way the number comes out wrong.
//
// Four things stop it, each with a message that says what to do:
//
//   - The checkout is shallow. The tags and the history are what everything
//     here is worked out from.
//   - The branch pins a pre-release. Only a bump branch that prepares for an
//     upcoming OpenShell release does, and nothing is released from one.
//   - The branch is release/X.Y and pins another line than X.Y.
//   - The newest release of the line is not in this commit's history. Numbers
//     on a line are given out in order, so a release from here would be
//     numbered above one it does not contain, and the X.Y image tag would
//     move back to older code. This is what a second run for an old commit
//     looks like, and a release branch that main has released past.
//
// A real run, and only a real run, stops for one more: a counted release tag
// whose commit was not built for the line. That is a tag this script did not
// cut: one pushed by hand, or pushed back from an old clone. A dry run shows
// the number it leads to and warns.
//
// A commit that already carries a release tag of its line is released: the
// script says so and succeeds. That makes a workflow run safe to re-run after
// a later job failed: the re-run is for the same commit, finds the tag, and
// goes on to the image tags and the chart.
//
//   node scripts/release/cut-release.mjs --branch main --dry-run
//   GH_TOKEN=... GH_REPO=owner/name node scripts/release/cut-release.mjs --branch release/0.1
import { execFileSync, spawnSync } from 'node:child_process';
import { appendFileSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

import { PINS_FILE, formatLine, readGatewayLine } from '../gateway-range.mjs';
import { compareReleases, nextVersion, parseReleaseTag } from './next-version.mjs';
import { declaredAtTag, isCiCommit, releaseNotes, repositoryUrl } from './release-notes.mjs';

// The line before main's is maintained from release/<major>.<minor>
// (ADR 0005, decision 5). No other branch is released.
const MAIN = 'main';
const RELEASE_BRANCH = /^release\/((?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$/;

function git(cwd, ...args) {
  return execFileSync('git', args, { cwd, encoding: 'utf8' }).trim();
}

function lines(text) {
  return text.split('\n').map((line) => line.trim()).filter(Boolean);
}

/** The line a branch is named for: "0.1" for release/0.1, null for main. Any other branch is refused. */
export function lineOfBranch(branch) {
  if (branch === MAIN) {
    return null;
  }
  const match = RELEASE_BRANCH.exec(branch ?? '');
  if (!match) {
    throw new Error(
      `releases are cut from ${MAIN} or from release/<major>.<minor>, not from ${JSON.stringify(branch ?? '')}.`,
    );
  }
  return match[1];
}

function isAncestor(cwd, ancestor, descendant) {
  const { status, stderr } = spawnSync('git', ['merge-base', '--is-ancestor', ancestor, descendant], {
    cwd,
    encoding: 'utf8',
  });
  if (status !== 0 && status !== 1) {
    throw new Error(`could not tell whether ${ancestor} is in the history of ${descendant}: ${stderr.trim()}`);
  }
  return status === 0;
}

/**
 * The nearest release in the history of HEAD: what the notes list the changes
 * since. It is the nearest one, not the one with the highest number, because
 * that is the release a reader of this commit's history last saw. On a branch
 * that only ever carried releases numbered by this script the two are the
 * same.
 */
function previousRelease(cwd, line) {
  const decorated = git(
    cwd,
    'log',
    '--topo-order',
    '--simplify-by-decoration',
    '--decorate=full',
    '--decorate-refs=refs/tags/*',
    '--format=%D',
    'HEAD',
  );
  for (const row of lines(decorated)) {
    const here = row
      .split(', ')
      .filter((name) => name.startsWith('tag: refs/tags/'))
      .map((name) => parseReleaseTag(name.slice('tag: refs/tags/'.length)))
      .filter(Boolean)
      .sort(compareReleases);
    if (here.length > 0) {
      // Several release tags on one commit: the one on this line, else the highest.
      return (here.findLast((release) => release.line === line) ?? here[here.length - 1]).tag;
    }
  }
  return null;
}

/**
 * Everything about the release this commit would be, read from the checkout.
 * Changes nothing.
 *
 * @param {object} input
 * @param {string} input.cwd      anywhere inside the checkout
 * @param {string} input.branch   the branch the release is asked for on
 */
export function planRelease({ cwd, branch }) {
  const branchLine = lineOfBranch(branch);
  const root = git(cwd, 'rev-parse', '--show-toplevel');
  // The version is worked out from every tag and the notes from the history.
  // A shallow checkout has neither, and would number the release <line>.0
  // without a word.
  if (git(root, 'rev-parse', '--is-shallow-repository') === 'true') {
    throw new Error(
      'this checkout is shallow, so the release tags and the history a version and its notes are worked out ' +
        'from are not all here. Fetch everything first (actions/checkout with fetch-depth: 0, or ' +
        '`git fetch --unshallow --tags`).',
    );
  }
  const sha = git(root, 'rev-parse', 'HEAD');
  const declared = readGatewayLine(join(root, PINS_FILE));
  const allTags = lines(git(root, 'tag', '--list'));
  // Throws for a pre-release pin, before anything else is looked at.
  const next = nextVersion({ pinned: declared.release, tags: allTags });

  if (branchLine !== null && branchLine !== next.line) {
    throw new Error(
      `${branch} pins OpenShell ${declared.release}, which is release line ${next.line}. A release branch ` +
        `releases its own line and nothing else: release ${formatLine(next.line)} from the branch that is for it, ` +
        'or move the pin back.',
    );
  }

  const base = { sha, branch, declared, line: next.line };

  const here = lines(git(root, 'tag', '--points-at', 'HEAD'))
    .map(parseReleaseTag)
    .filter((release) => release && release.line === next.line)
    .sort(compareReleases);
  if (here.length > 0) {
    return { ...base, already: here[here.length - 1].tag };
  }

  const newestOnLine = next.counted[next.counted.length - 1];
  if (newestOnLine && !isAncestor(root, newestOnLine, 'HEAD')) {
    throw new Error(
      `${newestOnLine}, the newest release of the ${formatLine(next.line)} line, is not in the history of ` +
        `${sha.slice(0, 7)}. A release from here would be numbered ${next.tag} without containing ${newestOnLine}, ` +
        `and the ${next.line} image tag would move back to older code. Release from a commit that contains ` +
        `${newestOnLine}: on a release branch, bring the branch up to it first (git merge ${newestOnLine}).`,
    );
  }

  // A counted tag whose own commit was not built for this line. Every release
  // this script cuts is tagged at a commit that pins the line it is numbered
  // for, so a tag that fails this was not cut by it, and it is the one thing
  // that makes the number come out too high.
  const foreign = next.counted
    .map((tag) => ({ tag, line: declaredAtTag(tag, root)?.line ?? null }))
    .filter((counted) => counted.line !== next.line);

  const previousTag = previousRelease(root, next.line);
  const commits = previousTag
    ? lines(git(root, 'log', '--no-merges', '--reverse', '--format=%H%x1f%s', `${previousTag}..HEAD`)).map((row) => {
        const [commit, subject = ''] = row.split('\x1f');
        return { sha: commit, subject };
      })
    : [];

  // GitHub shows one release as "Latest". A patch of an older line, cut after
  // a newer line has a release, is not it.
  const newestOverall = allTags.map(parseReleaseTag).filter(Boolean).sort(compareReleases).pop();
  const latest = !newestOverall || compareReleases(parseReleaseTag(next.tag), newestOverall) > 0;

  const notes = releaseNotes({
    tag: next.tag,
    previousTag,
    commits,
    declared,
    previous: declaredAtTag(previousTag, root),
    repoUrl: repositoryUrl(join(root, 'frontend', 'package.json')),
  });

  return {
    ...base,
    already: null,
    version: next.version,
    tag: next.tag,
    counted: next.counted,
    otherLines: next.otherLines,
    notReleases: next.notReleases,
    foreign,
    previousTag,
    commits,
    latest,
    newestOverall: newestOverall?.tag ?? null,
    // The new release is the newest patch of its line by construction, so the
    // line's moving tag follows it. Nothing else moves: not `latest`, and not
    // another line's tag.
    imageTags: [next.version, next.line],
    notes,
  };
}

function plural(count, one, many) {
  return `${count} ${count === 1 ? one : many}`;
}

/**
 * What is wrong with each counted tag that was not built for the line, one
 * paragraph per tag. A dry run prints these and carries on, so that the
 * number and the reason for it can be seen. A real run stops on the first.
 */
export function warnings(plan) {
  return (plan.foreign ?? []).map(({ tag, line }) => {
    const built = line
      ? `it pins gateway ${formatLine(line)}`
      : `its ${PINS_FILE} names no OpenShell release`;
    return (
      `${tag} was counted, but the commit it points at was not built for gateway ${formatLine(plan.line)} (${built}). ` +
      'A tag from before releases were numbered by gateway release line makes the version come out too high. ' +
      'A real run refuses to release while such a tag is counted: delete the tag, then run this again.'
    );
  });
}

/** The plan in words, for the log and the run summary. */
export function describe(plan, { dryRun }) {
  const where = `${plan.sha.slice(0, 7)} on ${plan.branch}`;
  if (plan.already) {
    return (
      `${plan.already} already points at ${where}: this commit is released, and there is nothing to cut.\n` +
      (dryRun
        ? 'This is a dry run, so nothing else is done for it either.\n'
        : 'The jobs that follow give it its image tags and its chart if it does not have them yet, which is what re-running a run is for.\n')
    );
  }

  const highest = plan.counted.length > 0 ? parseReleaseTag(plan.counted[plan.counted.length - 1]).patch : null;
  const listed = plan.commits.filter((commit) => !isCiCommit(commit.subject)).length;
  const rows = [
    [
      'Gateway release line',
      `${formatLine(plan.line)}   (${PINS_FILE} pins OpenShell ${plan.declared.release})`,
    ],
    [
      'Release tags counted',
      plan.counted.length > 0
        ? `${plan.counted.join(' ')}   (${plural(plan.counted.length, 'tag', 'tags')} on this line; the highest patch is ${highest})`
        : `none   (no release tag on this line yet, so it starts at ${plan.line}.0)`,
    ],
    ['Version', `${plan.version}   (tag ${plan.tag})`],
    [
      'Release tags ignored',
      plan.otherLines.length > 0
        ? `${plan.otherLines.join(' ')}   (on other lines)`
        : 'none on other lines',
    ],
    [
      'Other tags ignored',
      plan.notReleases.length > 0 ? `${plan.notReleases.join(' ')}   (not release tags)` : 'none',
    ],
    [
      'Previous release',
      plan.previousTag
        ? `${plan.previousTag}   (the nearest release in this commit's history; ` +
          `${plural(plan.commits.length, 'commit', 'commits')} since, ${listed} listed in the notes)`
        : "none   (no release in this commit's history, so the notes list no changes)",
    ],
    [
      'GitHub "Latest"',
      plan.latest ? 'yes' : `no   (${plan.newestOverall} is a higher version)`,
    ],
    ['Image tags', `${plan.imageTags[0]} (written once) and ${plan.imageTags[1]} (moves to this release)`],
    ['Helm chart', plan.version],
  ];
  const width = Math.max(...rows.map(([label]) => label.length));
  return [
    // Said before anything is created, so a real run does not claim more
    // than a plan either: "Released ..." is printed once it is done.
    dryRun
      ? `Dry run for ${where}: this is what a release would be. Nothing is created.`
      : `The release of ${where}:`,
    '',
    ...rows.map(([label, value]) => `  ${label.padEnd(width)}   ${value}`),
    '',
  ].join('\n');
}

const RULE = '-'.repeat(72);

function report(plan, { dryRun }) {
  const text = describe(plan, { dryRun });
  const warned = warnings(plan);
  process.stdout.write(`${text}\n`);
  for (const warning of warned) {
    process.stdout.write(`WARNING: ${warning}\n\n`);
    if (process.env.GITHUB_ACTIONS === 'true') {
      // On the run's summary page, not only in the log.
      process.stdout.write(`::warning title=A counted release tag was not built for this line::${warning}\n`);
    }
  }
  if (!plan.already) {
    process.stdout.write(`Release notes for ${plan.tag}:\n${RULE}\n${plan.notes}${RULE}\n`);
  }

  if (process.env.GITHUB_STEP_SUMMARY) {
    const summary = plan.already
      ? `${text}\n`
      : [
          `### ${dryRun ? 'Dry run: ' : ''}${plan.tag}`,
          '',
          '```',
          text.trimEnd(),
          '```',
          '',
          ...warned.map((warning) => `> **Warning.** ${warning}\n`),
          `#### Release notes for ${plan.tag}`,
          '',
          plan.notes,
        ].join('\n');
    appendFileSync(process.env.GITHUB_STEP_SUMMARY, summary);
  }
}

/**
 * Creates the GitHub release. GitHub creates the tag at `sha` in the same
 * request, so there is no state in which the tag exists without its release.
 */
function createRelease(cwd, plan) {
  const dir = mkdtempSync(join(tmpdir(), 'openshell-dashboard-release-'));
  try {
    const notesFile = join(dir, 'notes.md');
    writeFileSync(notesFile, plan.notes);
    const args = [
      'release',
      'create',
      plan.tag,
      '--target',
      plan.sha,
      '--title',
      plan.tag,
      '--notes-file',
      notesFile,
      // Said either way, so that it does not depend on what gh or GitHub
      // would otherwise work out from dates.
      `--latest=${plan.latest}`,
    ];
    const { status, error } = spawnSync('gh', args, { cwd, stdio: 'inherit' });
    if (error) {
      throw new Error(`could not run gh to create the release: ${error.message}`);
    }
    if (status !== 0) {
      throw new Error(
        `gh release create ${plan.tag} failed (its own message is above). Nothing else was done; ` +
          'if no release and no tag were created, run this again.',
      );
    }
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
  // The tag now exists on GitHub and not in this checkout. The next step asks
  // the checkout which release is at this commit, so it is given the same tag.
  git(cwd, 'tag', plan.tag, plan.sha);
}

function main() {
  const { values } = parseArgs({
    options: {
      branch: { type: 'string' },
      'dry-run': { type: 'boolean', default: false },
    },
  });
  if (!values.branch) {
    throw new Error('--branch <main | release/X.Y> is required: the branch the release is cut on');
  }
  const dryRun = values['dry-run'];
  const plan = planRelease({ cwd: process.cwd(), branch: values.branch });
  report(plan, { dryRun });
  if (plan.already || dryRun) {
    return;
  }
  // A release cannot be renumbered once it is out: its image tag is written
  // once and its chart version is never pushed again, and a real run need not
  // follow a dry run, so this is where a number that is known to be too high
  // is stopped.
  if (plan.foreign.length > 0) {
    const tags = plan.foreign.map(({ tag }) => tag).join(', ');
    throw new Error(
      `not releasing ${plan.tag}: ${tags} ${plan.foreign.length === 1 ? 'was' : 'were'} counted on the ` +
        `${formatLine(plan.line)} line and ${plan.foreign.length === 1 ? 'does' : 'do'} not point at a commit built ` +
        'for it (the warning above says what each one is). The version would be too high, and a release ' +
        'cannot be renumbered once it is out. Delete the tag, then run this again.',
    );
  }
  createRelease(process.cwd(), plan);
  process.stdout.write(`Released ${plan.tag} at ${plan.sha}.\n`);
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`cut-release: ${error.message}\n`);
    if (process.env.GITHUB_ACTIONS === 'true') {
      process.stdout.write(`::error title=No release was cut::${error.message}\n`);
    }
    process.exit(1);
  }
}
