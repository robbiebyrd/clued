import { test } from 'node:test';
import assert from 'node:assert/strict';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { spawnSync } from 'child_process';

const ROOT  = join(dirname(fileURLToPath(import.meta.url)), '..');
const GUARD = join(ROOT, 'hooks/guard-superpowers-plans');

function run(input: unknown) {
  return spawnSync('/bin/bash', [GUARD], { input: JSON.stringify(input), encoding: 'utf8' });
}
const call = (tool_name: string, file_path: string) => ({ tool_name, tool_input: { file_path } });

test('guard blocks Write under docs/superpowers/plans and names the MCP tools', () => {
  const r = run(call('Write', '/repo/docs/superpowers/plans/2026-10-09-feature.md'));
  assert.equal(r.status, 2);
  assert.match(r.stderr, /plan_create/);
  assert.match(r.stderr, /plan_update/);
  assert.equal(r.stdout, '');
});

test('guard blocks Edit under docs/superpowers/plans', () => {
  const r = run(call('Edit', '/repo/docs/superpowers/plans/2026-10-09-feature.md'));
  assert.equal(r.status, 2);
});

test('guard allows Write under docs/superpowers/specs', () => {
  const r = run(call('Write', '/repo/docs/superpowers/specs/2026-10-09-feature-design.md'));
  assert.equal(r.status, 0);
  assert.equal(r.stdout, '');
  assert.equal(r.stderr, '');
});

test('guard allows an unrelated path', () => {
  const r = run(call('Write', '/repo/src/index.ts'));
  assert.equal(r.status, 0);
});

test('guard allows input without a file_path', () => {
  const r = run({ tool_name: 'Write', tool_input: {} });
  assert.equal(r.status, 0);
  assert.equal(r.stderr, '');
});

test('guard reports itself inactive instead of silently allowing when jq is missing', () => {
  const r = spawnSync('/bin/bash', [GUARD], {
    input: JSON.stringify(call('Write', '/repo/docs/superpowers/plans/x.md')),
    encoding: 'utf8',
    env: { ...process.env, PATH: '/var/empty' },
  });
  assert.equal(r.status, 1, 'non-blocking error so the tool still runs');
  assert.match(r.stderr, /jq not found/);
  assert.match(r.stderr, /plan guard inactive/);
});
