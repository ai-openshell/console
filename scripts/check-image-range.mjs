#!/usr/bin/env node
// Checks that an image in the registry really declares the gateway release
// line it is for and the SDK it was built against, on every platform it was
// built for.
//
//   node scripts/check-image-range.mjs ghcr.io/ai-openshell/console:sha-0a1b2c3
//
// Both reach the image as build args: ci.yml passes them, and deploy/Dockerfile
// turns them into two labels. A build arg that is misspelt on either side is
// not an error — the Dockerfile's default is the empty string, on purpose, so
// that a plain `docker build` works — which means the image would be published
// with empty labels and no step would fail. This reads the pushed image's
// config back and compares it with what gateway-range.mjs derives. It only
// talks to the registry; it does not pull or run the image.
//
// The labels are for someone holding an image reference. The BFF inside does
// not read them: its line is compiled in, and `gateway-range.mjs --check`
// holds that constant to the same pins.
import { execFileSync } from 'node:child_process';
import { realpathSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

import { readGatewayLine } from './gateway-range.mjs';

// Must match the LABEL keys in deploy/Dockerfile.
export const LABEL_PREFIX = 'io.github.ai-openshell.console';

/** The labels deploy/Dockerfile should have baked in for this build. */
export function expectedLabels(declared) {
  return {
    [`${LABEL_PREFIX}.gateway.line`]: declared.line,
    [`${LABEL_PREFIX}.sdk`]: declared.sdk,
  };
}

/**
 * `imagetools inspect --format '{{json .Image}}'` prints one image config for a
 * single-platform image and a map of platform -> config for a multi-platform
 * one. Returns [platform, config] pairs either way.
 */
export function platformConfigs(inspected) {
  if (inspected && typeof inspected === 'object' && 'config' in inspected) {
    return [[`${inspected.os ?? '?'}/${inspected.architecture ?? '?'}`, inspected.config ?? {}]];
  }
  return Object.entries(inspected ?? {})
    .filter(([, image]) => image && typeof image === 'object' && 'config' in image)
    .map(([platform, image]) => [platform, image.config ?? {}]);
}

/** Every way the image fails to say what it should. Empty means it is right. */
export function declarationProblems(inspected, declared) {
  const configs = platformConfigs(inspected);
  if (configs.length === 0) {
    return ['the registry returned no image config to check'];
  }
  const want = expectedLabels(declared);
  const problems = [];
  for (const [platform, config] of configs) {
    for (const [name, value] of Object.entries(want)) {
      const actual = config.Labels?.[name];
      if (actual !== value) {
        problems.push(`${platform}: label ${name} is ${JSON.stringify(actual)}, expected ${JSON.stringify(value)}`);
      }
    }
  }
  return problems;
}

function main() {
  const ref = process.argv[2];
  if (!ref) {
    throw new Error('usage: check-image-range.mjs IMAGE_REFERENCE');
  }
  const declared = readGatewayLine();
  const inspected = JSON.parse(
    execFileSync('docker', ['buildx', 'imagetools', 'inspect', ref, '--format', '{{json .Image}}'], {
      encoding: 'utf8',
    }),
  );
  const problems = declarationProblems(inspected, declared);
  if (problems.length > 0) {
    throw new Error(
      `${ref} does not declare its gateway release line (${declared.line}) and SDK (${declared.sdk}):\n  ` +
        `${problems.join('\n  ')}\n` +
        'Check the build-args in the build job of ci.yml against the ARG names in deploy/Dockerfile.',
    );
  }
  const platforms = platformConfigs(inspected).map(([platform]) => platform);
  process.stdout.write(
    `${ref} declares gateway release line ${declared.line} and SDK ${declared.sdk} on ${platforms.join(', ')}\n`,
  );
}

if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    main();
  } catch (error) {
    process.stderr.write(`check-image-range: ${error.message}\n`);
    process.exit(1);
  }
}
