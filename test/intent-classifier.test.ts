import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/intent-classifier';

test('name and collection', () => {
  assert.equal(name, 'intent-classifier');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns null intent for non-user-prompt lines', async () => {
  const r = await enrich({ line: { message: { role: 'assistant', content: [] } } });
  assert.deepEqual(r, { intent: null });
});

test('returns null intent when display is absent', async () => {
  const r = await enrich({ line: {} });
  assert.deepEqual(r, { intent: null });
});

test('classifies slash_command', async () => {
  const r = await enrich({ line: { display: '/clear', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'slash_command' });
});

test('classifies question by trailing ?', async () => {
  const r = await enrich({ line: { display: 'Is this correct?', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'question' });
});

test('classifies question by interrogative opener', async () => {
  const r = await enrich({ line: { display: 'How do I fix this?', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'question' });
});

test('classifies instruction', async () => {
  const r = await enrich({ line: { display: 'Fix the bug in auth.ts', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'instruction' });
});

test('classifies approval: go for it', async () => {
  const r = await enrich({ line: { display: 'go for it', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'approval' });
});

test('classifies approval: lgtm', async () => {
  const r = await enrich({ line: { display: 'lgtm', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'approval' });
});

test('classifies correction: starts with no', async () => {
  const r = await enrich({ line: { display: 'no that is wrong', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'correction' });
});

test('no false positive correction for "no" mid-sentence', async () => {
  const r = await enrich({ line: { display: 'Make sure no tests fail', sessionId: 'x' } });
  assert.notEqual(r.intent, 'correction');
});

test('falls through to feedback for display with no strong signal', async () => {
  const r = await enrich({ line: { display: 'Interesting approach', sessionId: 'x' } });
  assert.equal(r.intent, 'feedback');
});
