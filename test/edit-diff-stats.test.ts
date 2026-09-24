import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/edit-diff-stats.js';

test('name and collection', () => {
  assert.equal(name, 'edit-diff-stats');
  assert.equal(collection, 'hook_events');
});

test('matches PostToolUse + Edit or Write', () => {
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Edit' }),  true);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Write' }), true);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Read' }),  false);
  assert.equal(matches({ hook_event_name: 'PreToolUse',  tool_name: 'Edit' }),  false);
  assert.equal(matches({}), false);
});

test('Edit: counts lines added and removed', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: {
      old_string: 'line1\nline2\nline3',
      new_string: 'line1\nline2',
    },
  });
  assert.equal(r.lines_added, 2);
  assert.equal(r.lines_removed, 3);
  assert.equal(r.net_change, -1);
});

test('Edit: net_change positive when adding lines', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: { old_string: 'a', new_string: 'a\nb\nc' },
  });
  assert.equal(r.lines_added, 3);
  assert.equal(r.lines_removed, 1);
  assert.equal(r.net_change, 2);
});

test('Edit: empty strings give 0 lines', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: { old_string: '', new_string: '' },
  });
  assert.equal(r.lines_added, 0);
  assert.equal(r.lines_removed, 0);
  assert.equal(r.net_change, 0);
});

test('Write: lines_removed is always 0', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: { file_path: '/x.ts', content: 'a\nb\nc' },
  });
  assert.equal(r.lines_added, 3);
  assert.equal(r.lines_removed, 0);
  assert.equal(r.net_change, 3);
});

test('Write: empty content → 0 added', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: { file_path: '/x.ts', content: '' },
  });
  assert.equal(r.lines_added, 0);
  assert.equal(r.net_change, 0);
});

test('missing tool_input strings treated as empty', async () => {
  const r = await enrich({ hook_event_name: 'PostToolUse', tool_name: 'Edit', tool_input: {} });
  assert.deepEqual(r, { lines_added: 0, lines_removed: 0, net_change: 0 });
});
