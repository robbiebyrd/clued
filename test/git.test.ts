import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, rmSync } from 'fs';
import { join, dirname } from 'path';
import { tmpdir } from 'os';
import { fileURLToPath } from 'url';
import { execFileSync } from 'child_process';
import { getGitOrigin, getGitBranch } from '../src/git';

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

test('getGitBranch returns branch name for current repo', async () => {
  // Use a fresh temp repo with a named branch — CI checkouts are detached HEAD.
  const tmp = join(tmpdir(), `clued-git-branch-named-${process.pid}`);
  mkdirSync(tmp, { recursive: true });
  try {
    execFileSync('git', ['init', '-b', 'test-branch'], { cwd: tmp });
    execFileSync('git', ['commit', '--allow-empty', '-m', 'init'], { cwd: tmp });
    const branch = await getGitBranch(tmp);
    assert.ok(typeof branch === 'string' && branch.length > 0, `expected a branch name, got ${JSON.stringify(branch)}`);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test('getGitBranch returns null for a plain directory', async () => {
  const tmp = join(tmpdir(), `clued-git-branch-test-${process.pid}`);
  mkdirSync(tmp, { recursive: true });
  try {
    const branch = await getGitBranch(tmp);
    assert.equal(branch, null);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test('getGitBranch returns null for detached HEAD', async () => {
  const tmp = join(tmpdir(), `clued-git-detached-${process.pid}`);
  mkdirSync(tmp, { recursive: true });
  try {
    execFileSync('git', ['init'], { cwd: tmp });
    execFileSync('git', ['commit', '--allow-empty', '-m', 'init'], { cwd: tmp, env: { ...process.env, GIT_AUTHOR_NAME: 'test', GIT_AUTHOR_EMAIL: 'test@test.com', GIT_COMMITTER_NAME: 'test', GIT_COMMITTER_EMAIL: 'test@test.com' } });
    const sha = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: tmp }).toString().trim();
    execFileSync('git', ['checkout', '--detach', sha], { cwd: tmp });
    const branch = await getGitBranch(tmp);
    assert.equal(branch, null);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test('getGitBranch returns null for a non-existent path', async () => {
  const branch = await getGitBranch('/does/not/exist/at/all');
  assert.equal(branch, null);
});
