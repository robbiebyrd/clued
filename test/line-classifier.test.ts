import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/line-classifier';

test('name and collection', () => {
  assert.equal(name, 'line-classifier');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
  assert.equal(matches({ anything: true }), true);
});

test('classifies session_meta: last-prompt', async () => {
  const r = await enrich({ line: { type: 'last-prompt', sessionId: 'abc' } });
  assert.deepEqual(r, { type: 'session_meta' });
});

test('classifies session_meta: permission-mode', async () => {
  const r = await enrich({ line: { type: 'permission-mode' } });
  assert.deepEqual(r, { type: 'session_meta' });
});

test('classifies hook_success', async () => {
  const r = await enrich({ line: { attachment: { type: 'hook_success' } } });
  assert.deepEqual(r, { type: 'hook_success' });
});

test('classifies hook_error', async () => {
  const r = await enrich({ line: { attachment: { type: 'hook_error' } } });
  assert.deepEqual(r, { type: 'hook_error' });
});

test('classifies user_prompt', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'abc', timestamp: 123 } });
  assert.deepEqual(r, { type: 'user_prompt' });
});

test('classifies tool_result', async () => {
  const r = await enrich({
    line: { message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'x', content: [] }] } },
  });
  assert.deepEqual(r, { type: 'tool_result' });
});

test('classifies assistant', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [{ type: 'text', text: 'hello' }] } },
  });
  assert.deepEqual(r, { type: 'assistant' });
});

test('classifies assistant with tool_use blocks', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [{ type: 'tool_use', id: 'x', name: 'Bash', input: {} }] } },
  });
  assert.deepEqual(r, { type: 'assistant' });
});

test('classifies unknown for unrecognized shapes', async () => {
  const r = await enrich({ line: { foo: 'bar' } });
  assert.deepEqual(r, { type: 'unknown' });
});

test('classifies unknown when line is null', async () => {
  const r = await enrich({ line: null });
  assert.deepEqual(r, { type: 'unknown' });
});
