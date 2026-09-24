import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/bash-outcome.js';

test('name and collection', () => {
  assert.equal(name, 'bash-outcome');
  assert.equal(collection, 'hook_events');
});

test('matches PostToolUse + Bash only', () => {
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Bash' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse',  tool_name: 'Bash' }), false);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Read' }), false);
  assert.equal(matches({}), false);
});

test('success when stderr is empty string', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'hello\nworld', stderr: '' },
  });
  assert.equal(r.success, true);
  assert.equal(r.has_error, false);
  assert.equal(r.output_lines, 2);
  assert.equal(r.truncated, false);
});

test('success when stderr is absent', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'ok' },
  });
  assert.equal(r.success, true);
});

test('failure when stderr is non-empty', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: '', stderr: 'command not found' },
  });
  assert.equal(r.success, false);
  assert.equal(r.has_error, true);
});

test('truncated=true when stdout contains [truncated]', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'line1\n[truncated]', stderr: '' },
  });
  assert.equal(r.truncated, true);
});

test('output_lines=0 when stdout is absent', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: {},
  });
  assert.equal(r.output_lines, 0);
});

test('counts lines correctly for multi-line output', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'a\nb\nc', stderr: '' },
  });
  assert.equal(r.output_lines, 3);
});

test('success when stderr is null', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'ok', stderr: null },
  });
  assert.equal(r.success, true);
});
