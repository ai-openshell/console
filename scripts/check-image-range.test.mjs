// node --test "scripts/**/*.test.mjs"
//
// The release line and the SDK reach the image's labels through three places
// that have to agree by name: the build-args in ci.yml, the ARG / LABEL lines
// in deploy/Dockerfile, and what check-image-range.mjs looks for in the pushed
// image. Nothing here builds or runs an image; the checker is run against a
// stand-in `docker`.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { delimiter, dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';

import { LABEL_PREFIX, declarationProblems, platformConfigs } from './check-image-range.mjs';
import { readGatewayLine } from './gateway-range.mjs';

const repoRoot = join(dirname(fileURLToPath(import.meta.url)), '..');
const SDK = 'v0.0.0-20260928030816-6648bd0c290e';
const declared = { line: '0.1', tested: ['0.1.0', '0.1.2'], sdk: SDK };

// One entry of what `docker buildx imagetools inspect --format '{{json .Image}}'` prints.
const imageConfig = (labels) => ({
  architecture: 'amd64',
  os: 'linux',
  config: {
    Env: ['PATH=/usr/bin', 'PORT=8080'],
    Labels: { 'org.opencontainers.image.title': 'x', ...labels },
  },
});
const goodLabels = {
  [`${LABEL_PREFIX}.gateway.line`]: '0.1',
  [`${LABEL_PREFIX}.sdk`]: SDK,
};

test('an image that declares the line and the SDK on every platform has no problems', () => {
  const multiPlatform = {
    'linux/amd64': imageConfig(goodLabels),
    'linux/arm64': imageConfig(goodLabels),
  };
  assert.deepEqual(
    platformConfigs(multiPlatform).map(([platform]) => platform),
    ['linux/amd64', 'linux/arm64'],
  );
  assert.deepEqual(declarationProblems(multiPlatform, declared), []);
  // A single-platform image is one config rather than a map of them.
  assert.deepEqual(declarationProblems(imageConfig(goodLabels), declared), []);
});

test('an image built without the args, or with the wrong ones, is reported per platform', () => {
  // What a misspelt build arg produces: the Dockerfile default, the empty string.
  const empty = imageConfig({
    [`${LABEL_PREFIX}.gateway.line`]: '',
    [`${LABEL_PREFIX}.sdk`]: '',
  });
  const problems = declarationProblems({ 'linux/amd64': imageConfig(goodLabels), 'linux/arm64': empty }, declared);
  assert.equal(problems.length, 2);
  assert.ok(problems.every((problem) => problem.startsWith('linux/arm64: ')));
  assert.ok(problems.includes(`linux/arm64: label ${LABEL_PREFIX}.gateway.line is "", expected "0.1"`));

  // An image without the labels has neither. Labels under other names
  // (gateway.min, gateway.max) do not stand in for the line.
  assert.equal(declarationProblems(imageConfig({}), declared).length, 2);
  const ranged = imageConfig({
    [`${LABEL_PREFIX}.gateway.min`]: '0.1.0',
    [`${LABEL_PREFIX}.gateway.max`]: '0.1.2',
    [`${LABEL_PREFIX}.sdk`]: SDK,
  });
  assert.deepEqual(declarationProblems(ranged, declared), [
    `linux/amd64: label ${LABEL_PREFIX}.gateway.line is undefined, expected "0.1"`,
  ]);

  // A stale line is as wrong as a missing one.
  const stale = imageConfig({ ...goodLabels, [`${LABEL_PREFIX}.gateway.line`]: '0.0' });
  assert.deepEqual(declarationProblems(stale, declared), [
    `linux/amd64: label ${LABEL_PREFIX}.gateway.line is "0.0", expected "0.1"`,
  ]);
});

test('nothing to check is a failure, not a pass', () => {
  assert.deepEqual(declarationProblems({}, declared), ['the registry returned no image config to check']);
  assert.deepEqual(declarationProblems(null, declared), ['the registry returned no image config to check']);
});

test('the label keys the checker expects are the ones deploy/Dockerfile sets', () => {
  const dockerfile = readFileSync(join(repoRoot, 'deploy', 'Dockerfile'), 'utf8');
  for (const label of ['gateway.line="${GATEWAY_RELEASE_LINE}"', 'sdk="${OPENSHELL_SDK_VERSION}"']) {
    assert.ok(dockerfile.includes(`${LABEL_PREFIX}.${label}`), `${LABEL_PREFIX}.${label}`);
  }
  for (const name of ['GATEWAY_RELEASE_LINE', 'OPENSHELL_SDK_VERSION']) {
    assert.ok(dockerfile.includes(`ARG ${name}=""`), `ARG ${name}`);
  }
});

// The line a running BFF uses is compiled into the binary, so that an image
// built by another Dockerfile, or with no build args, still knows it. An ENV
// here would put a second copy in the image. The BFF reads no range of
// gateway versions either, so no GATEWAY_SUPPORTED_ variable belongs here.
test('the Dockerfile hands the BFF no line and no range through the environment', () => {
  const instructions = readFileSync(join(repoRoot, 'deploy', 'Dockerfile'), 'utf8')
    .split('\n')
    .filter((line) => !line.trimStart().startsWith('#'));
  assert.deepEqual(
    instructions.filter((line) => line.includes('GATEWAY_SUPPORTED_')),
    [],
  );
  // The build arg and the label it fills, and nothing else: no ENV.
  assert.deepEqual(
    instructions.filter((line) => line.includes('GATEWAY_RELEASE_LINE')),
    ['ARG GATEWAY_RELEASE_LINE=""', `LABEL ${LABEL_PREFIX}.gateway.line="\${GATEWAY_RELEASE_LINE}" \\`],
  );
});

test('the build job passes exactly the args the Dockerfile reads, from the derived line', () => {
  const workflow = readFileSync(join(repoRoot, '.github', 'workflows', 'ci.yml'), 'utf8');
  for (const line of [
    'GATEWAY_RELEASE_LINE=${{ steps.gateway.outputs.line }}',
    'OPENSHELL_SDK_VERSION=${{ steps.gateway.outputs.sdk }}',
  ]) {
    assert.ok(workflow.includes(line), line);
  }
  assert.doesNotMatch(workflow, /GATEWAY_SUPPORTED_(MIN|MAX)/);
});

test('check-image-range.mjs passes a correct image and fails one that claims nothing', (t) => {
  const dir = mkdtempSync(join(tmpdir(), 'console-image-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  // The stand-in docker answers `buildx imagetools inspect` with whatever the
  // test hands it. No registry, no daemon.
  writeFileSync(join(dir, 'docker'), '#!/usr/bin/env node\nprocess.stdout.write(process.env.STUB_IMAGE);\n');
  chmodSync(join(dir, 'docker'), 0o755);

  const committed = readGatewayLine();
  const check = (image) =>
    spawnSync(
      process.execPath,
      [join(repoRoot, 'scripts', 'check-image-range.mjs'), 'quay.io/example/dashboard:sha-0a1b2c3'],
      {
        encoding: 'utf8',
        env: { ...process.env, PATH: `${dir}${delimiter}${process.env.PATH}`, STUB_IMAGE: JSON.stringify(image) },
      },
    );

  const good = imageConfig({
    [`${LABEL_PREFIX}.gateway.line`]: committed.line,
    [`${LABEL_PREFIX}.sdk`]: committed.sdk,
  });
  const passed = check({ 'linux/amd64': good, 'linux/arm64': good });
  assert.equal(passed.status, 0, passed.stderr);
  assert.match(passed.stdout, /declares gateway release line .* on linux\/amd64, linux\/arm64/);

  const failed = check({ 'linux/amd64': good, 'linux/arm64': imageConfig({}) });
  assert.equal(failed.status, 1);
  assert.match(failed.stderr, /does not declare its gateway release line/);
  assert.ok(failed.stderr.includes(`linux/arm64: label ${LABEL_PREFIX}.gateway.line is undefined`));
});
