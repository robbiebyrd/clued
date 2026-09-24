import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/file-tracker.js';

test('name and collection', () => {
  assert.equal(name, 'file-tracker');
  assert.equal(collection, 'hook_events');
});

test('matches PreToolUse + file tool', () => {
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Read' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Edit' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Write' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Glob' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'LS' }), true);
});

test('does not match Bash or PostToolUse', () => {
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Bash' }), false);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Read' }), false);
});

test('Read extracts file_path, language, operation=read', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: '/src/app.ts' },
  });
  assert.equal(r.file_path, '/src/app.ts');
  assert.equal(r.language, 'typescript');
  assert.equal(r.operation, 'read');
});

test('Write extracts file_path and operation=write', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Write',
    tool_input: { file_path: '/src/util.py', content: 'x' },
  });
  assert.equal(r.operation, 'write');
  assert.equal(r.language, 'python');
});

test('Edit extracts operation=edit', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Edit',
    tool_input: { file_path: '/src/main.go', old_string: 'a', new_string: 'b' },
  });
  assert.equal(r.operation, 'edit');
  assert.equal(r.language, 'go');
});

test('Glob → operation=glob, no language', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Glob',
    tool_input: { pattern: '**/*.ts' },
  });
  assert.equal(r.operation, 'glob');
  assert.equal(r.file_path, null);
  assert.equal(r.language, null);
});

test('artifact_type: test wins by path pattern', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'test/foo.test.ts' },
  });
  assert.equal(r.artifact_type, 'test');
});

test('artifact_type: spec wins by path segment', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'docs/specs/my-design.md' },
  });
  assert.equal(r.artifact_type, 'spec');
});

test('artifact_type: plan wins by path segment even when filename has design keyword', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'docs/plans/api-design.md' },
  });
  assert.equal(r.artifact_type, 'plan');
});

test('artifact_type: plan wins by filename keyword', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'docs/plans/2026-01-01-roadmap.md' },
  });
  assert.equal(r.artifact_type, 'plan');
});

test('artifact_type: doc for plain .md not matching higher priority', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'README.md' },
  });
  assert.equal(r.artifact_type, 'doc');
});

test('artifact_type: config for .json', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'tsconfig.json' },
  });
  assert.equal(r.artifact_type, 'config');
});

test('artifact_type: code for .ts outside test/spec/plan', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Edit',
    tool_input: { file_path: 'src/daemon.ts', old_string: '', new_string: '' },
  });
  assert.equal(r.artifact_type, 'code');
});

test('artifact_type: null for unknown extension', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'data/export.parquet' },
  });
  assert.equal(r.artifact_type, null);
});

test('null file_path when tool_input has no path fields', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'LS',
    tool_input: {},
  });
  assert.equal(r.file_path, null);
  assert.equal(r.language, null);
  assert.equal(r.artifact_type, null);
});
