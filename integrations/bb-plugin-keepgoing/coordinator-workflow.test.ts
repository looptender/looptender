import { test } from 'node:test';
import { strict as assert } from 'node:assert';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { mkdtempSync, mkdirSync, realpathSync, rmSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';

function fixtureEnvironment(root: string, inherited: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const location = (name: string) => {
    const path = join(root, name); mkdirSync(path, { recursive: true, mode: 0o700 }); return path;
  };
  const env: NodeJS.ProcessEnv = {
    PATH: '/usr/bin:/bin', HOME: location('home'), USERPROFILE: location('home'),
    XDG_CONFIG_HOME: location('config'), XDG_DATA_HOME: location('data'), XDG_CACHE_HOME: location('cache'),
    XDG_STATE_HOME: location('state'), XDG_RUNTIME_DIR: location('run'),
    TMPDIR: location('tmp'), TMP: location('tmp'), TEMP: location('tmp'),
    LANG: 'C', LC_ALL: 'C', TZ: 'UTC', PYTHONDONTWRITEBYTECODE: '1', PYTHONUTF8: '1',
  };
  for (const prefix of ['LOOPTENDER_', 'KEEPGOING_']) {
    env[prefix + 'REGISTRY_DIR'] = location('registry');
    env[prefix + 'SESSION_DIR'] = location('sessions');
    env[prefix + 'ROUTES_FILE'] = join(root, 'routes.yaml');
  }
  // The one explicit caller-supplied test input; never forward caller context.
  if (inherited.KEEPGOING_TEST_BINARY) {
    assert.ok(isAbsolute(inherited.KEEPGOING_TEST_BINARY));
    env.KEEPGOING_TEST_BINARY = realpathSync(inherited.KEEPGOING_TEST_BINARY);
  }
  return env;
}

test('coordinator harness excludes poisoned inherited environment', { skip: process.platform === 'win32' }, () => {
  const root = mkdtempSync(join(realpathSync('/tmp'), 'coordinator-environment-'));
  try {
    const poison: NodeJS.ProcessEnv = {
      HOME: '/no-such-fictional-home', PATH: '/no-such-fictional-bin', TMPDIR: '/no-such-fictional-tmp',
      KEEPGOING_ROUTES_FILE: '/no-such-fictional-catalog', LOOPTENDER_REGISTRY_DIR: '/no-such-fictional-registry',
      OPENAI_API_KEY: 'fictional-credential-sentinel', ANTHROPIC_API_KEY: 'fictional-credential-sentinel',
      BB_CLI: '/no-such-fictional-bb', BB_THREAD_ID: 'thr_example_wrong_owner', PYTHONPATH: '/no-such-fictional-import',
    };
    const result = spawnSync('/usr/bin/python3', ['-B', '-c', 'import os,json;print(json.dumps(dict(os.environ)))'], {
      env: fixtureEnvironment(root, poison), encoding: 'utf8', timeout: 5_000, maxBuffer: 32_768,
    });
    assert.ifError(result.error); assert.equal(result.status, 0);
    const env = JSON.parse(result.stdout) as Record<string, string>;
    assert.ok(Object.values(env).every(value => !value.includes('sentinel') && !value.includes('no-such-fictional')));
    for (const key of ['OPENAI_API_KEY', 'ANTHROPIC_API_KEY', 'BB_CLI', 'BB_THREAD_ID', 'PYTHONPATH']) assert.equal(env[key], undefined);
    assert.equal(env.KEEPGOING_ROUTES_FILE, env.LOOPTENDER_ROUTES_FILE);
  } finally { rmSync(root, { recursive: true, force: true }); }
});

// Execute the packaged consumer through explicit fictional executables. The
// Python interpreter and optional built CLI are absolute; no inherited PATH,
// home, provider credentials, aliases or caller context reaches the fixture.
test('coordinator workflow preserves obligations and bounds dispatch', { skip: process.platform === 'win32', timeout: 180_000 }, () => {
  const root = mkdtempSync(join(realpathSync('/tmp'), 'coordinator-wrapper-'));
  try {
    const suite = fileURLToPath(new URL('../../tools/coordinator-workflow', import.meta.url));
    const result = spawnSync('/usr/bin/python3', ['-B', '-m', 'unittest', 'discover', '-s', suite, '-p', 'test_*.py', '-v'], {
      env: fixtureEnvironment(root, process.env), encoding: 'utf8', timeout: 170_000, maxBuffer: 512 * 1024,
    });
    assert.ifError(result.error);
    process.stdout.write(result.stdout + result.stderr);
    assert.equal(result.status, 0, result.stdout + result.stderr);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
