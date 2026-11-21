import { test } from 'node:test';
import { strict as assert } from 'node:assert';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// Execute the packaged Python consumer, including its real streaming adapter,
// against isolated fake BB commands. No installed BB/provider is invoked.
test('coordinator workflow preserves obligations and bounds dispatch', { skip: process.platform === 'win32', timeout: 180_000 }, () => {
  const suite = fileURLToPath(new URL('../../tools/coordinator-workflow', import.meta.url));
  const result = spawnSync('python3', ['-B', '-m', 'unittest', 'discover', '-s', suite, '-p', 'test_*.py', '-v'], {
    env: { ...process.env, PYTHONDONTWRITEBYTECODE: '1' }, encoding: 'utf8', timeout: 170_000, maxBuffer: 512 * 1024,
  });
  assert.ifError(result.error);
  assert.equal(result.status, 0, result.stdout + result.stderr);
});
