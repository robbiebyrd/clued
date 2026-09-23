import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, rmSync } from 'fs';
import { join, dirname } from 'path';
import { tmpdir } from 'os';
import { fileURLToPath } from 'url';
import { getGitOrigin } from '../src/git';

const ROOT = join(dirname(fileURLToPath(import.meta.url)), '..');

test('returns origin URL for a git repo with a remote', async () => {
  const origin = await getGitOrigin(ROOT);
  assert.ok(origin, 'expected a non-null git origin URL');
  assert.match(origin, /clued/);
});

test('returns null for a plain directory (no git)', async () => {
  const tmp = join(tmpdir(), `clued-git-test-${process.pid}`);
  mkdirSync(tmp, { recursive: true });
  try {
    const origin = await getGitOrigin(tmp);
    assert.equal(origin, null);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test('returns null for a non-existent path', async () => {
  const origin = await getGitOrigin('/does/not/exist/at/all');
  assert.equal(origin, null);
});
