// scripts/quay-login.sh against a stand-in curl, docker and helm: it asks
// GitHub for an OIDC token, trades it at Quay's robot federation endpoint and
// logs both clients in with the robot token, never with the GitHub one.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { chmodSync, mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const script = join(dirname(fileURLToPath(import.meta.url)), 'quay-login.sh');

const stub = (dir, name, body) => {
  const path = join(dir, name);
  writeFileSync(path, `#!/usr/bin/env bash\n${body}\n`);
  chmodSync(path, 0o755);
};

const setup = (t, { exchange = 'ok', helm = true } = {}) => {
  const dir = mkdtempSync(join(tmpdir(), 'quay-login-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const log = join(dir, 'log');
  stub(
    dir,
    'curl',
    `printf 'curl %s\\n' "$*" >> "${log}"
url="\${@: -1}"
case "$url" in
  *audience=*) echo '{"value":"gh.jwt.sig"}' ;;
  */oauth2/federation/robot/token)
    if [[ "${exchange}" == refused ]]; then echo '{"error":"subject not trusted"}'; exit 22; fi
    echo '{"token":"quay.robot.token"}' ;;
  *) exit 7 ;;
esac`,
  );
  stub(dir, 'docker', `printf 'docker %s <%s>\\n' "$*" "$(cat)" >> "${log}"`);
  if (helm) stub(dir, 'helm', `printf 'helm %s <%s>\\n' "$*" "$(cat)" >> "${log}"`);
  const run = (args, env = {}) =>
    spawnSync('bash', [script, ...args], {
      encoding: 'utf8',
      env: {
        PATH: `${dir}:/usr/bin:/bin`,
        ACTIONS_ID_TOKEN_REQUEST_URL: 'https://token.example/req?api-version=2.0',
        ACTIONS_ID_TOKEN_REQUEST_TOKEN: 'runner-secret',
        ...env,
      },
    });
  const lines = () => (existsSync(log) ? readFileSync(log, 'utf8').trim().split('\n') : []);
  return { run, lines };
};

test('trades the GitHub OIDC token for a robot token and logs docker and helm in with it', (t) => {
  const { run, lines } = setup(t);
  const result = run(['ai-openshell+console_release']);
  assert.equal(result.status, 0, result.stderr);
  const log = lines();
  assert.match(log[0], /Authorization: bearer runner-secret .*req\?api-version=2\.0&audience=quay\.io$/);
  assert.match(log[1], /-u ai-openshell\+console_release:gh\.jwt\.sig https:\/\/quay\.io\/oauth2\/federation\/robot\/token$/);
  assert.equal(log[2], 'docker login quay.io -u ai-openshell+console_release --password-stdin <quay.robot.token>');
  assert.equal(log[3], 'helm registry login quay.io -u ai-openshell+console_release --password-stdin <quay.robot.token>');
  // Both tokens are masked in the job log.
  assert.match(result.stdout, /::add-mask::gh\.jwt\.sig/);
  assert.match(result.stdout, /::add-mask::quay\.robot\.token/);
});

test('skips helm when it is not installed', (t) => {
  const { run, lines } = setup(t, { helm: false });
  assert.equal(run(['ai-openshell+console_release']).status, 0);
  assert.equal(lines().filter((l) => l.startsWith('helm')).length, 0);
});

test('fails with Quay’s reason and logs nothing in when the exchange is refused', (t) => {
  const { run, lines } = setup(t, { exchange: 'refused' });
  const result = run(['ai-openshell+console_release']);
  assert.equal(result.status, 1);
  assert.match(result.stderr, /refused the exchange for ai-openshell\+console_release: .*subject not trusted/);
  assert.equal(lines().filter((l) => l.startsWith('docker') || l.startsWith('helm')).length, 0);
});

test('fails before calling anything when the job may not ask for an OIDC token', (t) => {
  const { run, lines } = setup(t);
  const result = run(['ai-openshell+console_release'], { ACTIONS_ID_TOKEN_REQUEST_URL: '' });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /id-token: write/);
  assert.deepEqual(lines(), []);
});

test('needs a robot', (t) => {
  const { run } = setup(t);
  assert.notEqual(run([]).status, 0);
});
