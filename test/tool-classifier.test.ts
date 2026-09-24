import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/tool-classifier.js';

test('name and collection', () => {
  assert.equal(name, 'tool-classifier');
  assert.equal(collection, 'hook_events');
});

test('matches PreToolUse only', () => {
  assert.equal(matches({ hook_event_name: 'PreToolUse' }), true);
  assert.equal(matches({ hook_event_name: 'PostToolUse' }), false);
  assert.equal(matches({ hook_event_name: 'UserPromptSubmit' }), false);
  assert.equal(matches({}), false);
});

test('Read → file_read, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Read' });
  assert.deepEqual(r, { category: 'file_read', is_read_only: true });
});

test('Write → file_write, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Write' });
  assert.deepEqual(r, { category: 'file_write', is_read_only: false });
});

test('Edit → file_edit, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Edit' });
  assert.deepEqual(r, { category: 'file_edit', is_read_only: false });
});

test('Bash → shell_exec, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Bash' });
  assert.deepEqual(r, { category: 'shell_exec', is_read_only: false });
});

test('WebFetch → web_fetch, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'WebFetch' });
  assert.deepEqual(r, { category: 'web_fetch', is_read_only: true });
});

test('WebSearch → web_fetch, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'WebSearch' });
  assert.deepEqual(r, { category: 'web_fetch', is_read_only: true });
});

test('Grep → code_search, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Grep' });
  assert.deepEqual(r, { category: 'code_search', is_read_only: true });
});

test('Glob → code_search, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Glob' });
  assert.deepEqual(r, { category: 'code_search', is_read_only: true });
});

test('LS → code_search, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'LS' });
  assert.deepEqual(r, { category: 'code_search', is_read_only: true });
});

test('Agent → agent_dispatch, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Agent' });
  assert.deepEqual(r, { category: 'agent_dispatch', is_read_only: false });
});

test('unknown tool → other, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'SomeFutureTool' });
  assert.deepEqual(r, { category: 'other', is_read_only: false });
});

test('missing tool_name → other, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse' });
  assert.deepEqual(r, { category: 'other', is_read_only: false });
});
