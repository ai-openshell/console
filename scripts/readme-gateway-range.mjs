#!/usr/bin/env node
// Keeps the README's statement of the gateway release line this branch is for
// in step with deploy/ci/gateway-pins.json.
//
// The README is where people look first, and a statement written there by hand
// goes stale the day the pins move — at which point it contradicts the release
// notes and the image, which are both derived. So the few lines that restate
// the line, the release it is tested on and the SDK sit between two marker
// comments and are generated, and CI fails when they are out of date.
//
//   node scripts/readme-gateway-range.mjs --write   regenerate the block
//   node scripts/readme-gateway-range.mjs --check   exit 1 when it is stale (CI)
//
// Whatever moves the pins runs --write in the same change. The Follow upstream
// workflow does, in the bump branch it makes for a new upstream release: the block
// restates the release and the SDK, and a branch that left it stale could
// never pass --check. It runs this copy of the script, from the commit the
// workflow runs at, against the tree it is changing, which is what --readme
// and --pins are for (deploy/ci/upstream/pinmove.py). --write changes nothing
// outside the markers and is a no-op when the block is already right, so it
// is safe to run every time.
//
// Only the part between the markers is generated. The prose around it is never
// touched. The markers still say "gateway-range", from when a build declared a
// range of gateway versions.
import { readFileSync, realpathSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

import { formatLine, readGatewayLine } from './gateway-range.mjs';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');

export const README_PATH = join(repoRoot, 'README.md');
export const BEGIN =
  '<!-- gateway-range:begin (generated from deploy/ci/gateway-pins.json by scripts/readme-gateway-range.mjs; do not edit) -->';
export const END = '<!-- gateway-range:end -->';

export function renderBlock(declared) {
  return [
    BEGIN,
    '| | |',
    '|---|---|',
    `| Supported gateways | \`${formatLine(declared.line)}\` |`,
    `| Tested on | \`${declared.release}\` |`,
    `| OpenShell Go SDK | \`${declared.sdk}\` |`,
    END,
  ].join('\n');
}

/** Returns the document with the generated block replaced. */
export function replaceBlock(readme, block) {
  const begin = readme.indexOf(BEGIN);
  const end = readme.indexOf(END);
  if (begin === -1 || end === -1 || end < begin) {
    throw new Error(`the markers are missing or out of order; expected\n  ${BEGIN}\n  …\n  ${END}`);
  }
  if (readme.indexOf(BEGIN, begin + 1) !== -1 || readme.indexOf(END, end + 1) !== -1) {
    throw new Error('the markers appear more than once');
  }
  return readme.slice(0, begin) + block + readme.slice(end + END.length);
}

function main() {
  const { values } = parseArgs({
    options: {
      write: { type: 'boolean', default: false },
      check: { type: 'boolean', default: false },
      readme: { type: 'string', default: README_PATH },
      pins: { type: 'string' },
    },
  });
  if (values.write === values.check) {
    throw new Error('pass exactly one of --write or --check');
  }

  const block = renderBlock(readGatewayLine(values.pins));
  const current = readFileSync(values.readme, 'utf8');
  const wanted = replaceBlock(current, block);

  if (current === wanted) {
    process.stdout.write(`${values.readme}: the supported gateway release line is up to date\n`);
    return;
  }
  if (values.write) {
    writeFileSync(values.readme, wanted);
    process.stdout.write(`${values.readme}: regenerated the supported gateway release line\n`);
    return;
  }
  throw new Error(
    `${values.readme} is stale: it does not state what deploy/ci/gateway-pins.json ` +
      `now gives. It should read:\n\n${block}\n\n` +
      'Run `node scripts/readme-gateway-range.mjs --write` and commit the result.',
  );
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`readme-gateway-range: ${error.message}\n`);
    process.exit(1);
  }
}
