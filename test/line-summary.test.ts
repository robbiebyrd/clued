import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, enabled, batchLimit, matches, enrich } from '../enrichers/line-summary';

test('name, collection, batchLimit', () => {
  assert.equal(name, 'line-summary');
  assert.equal(collection, 'transcript_lines');
  assert.equal(batchLimit, 10);
});

test('is disabled by default', () => {
  assert.equal(enabled, false);
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
  assert.equal(matches({ line: null }), true);
});

test('returns null summary for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { summary: null, model: null });
});

test('returns null summary for assistant line below token threshold', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'short reply' }],
      },
    },
  });
  assert.deepEqual(r, { summary: null, model: null });
});

test('returns null summary for assistant line above token threshold (API stub, no network call)', async () => {
  const longText = 'word '.repeat(200); // 1000 chars = 250 token estimate
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: longText }],
      },
    },
  });
  assert.deepEqual(r, { summary: null, model: null });
});
