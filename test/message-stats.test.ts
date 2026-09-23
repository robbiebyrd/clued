import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/message-stats';

test('name and collection', () => {
  assert.equal(name, 'message-stats');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns zeros for line with no text content', async () => {
  const r = await enrich({ line: {} });
  assert.deepEqual(r, { char_count: 0, word_count: 0, block_count: 0, token_estimate: 0 });
});

test('counts chars and words from user_prompt display field', async () => {
  const r = await enrich({ line: { display: 'hello world', sessionId: 'x' } });
  assert.equal(r.char_count, 11);
  assert.equal(r.word_count, 2);
  assert.equal(r.block_count, 0);
  assert.equal(r.token_estimate, Math.ceil(11 / 4));
});

test('counts chars across all text-type content blocks concatenated', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'text', text: 'hello ' },
          { type: 'tool_use', id: 'x', name: 'Bash', input: {} },
          { type: 'text', text: 'world' },
        ],
      },
    },
  });
  assert.equal(r.char_count, 11); // 'hello ' + 'world', not tool_use input
  assert.equal(r.block_count, 3);
});

test('token_estimate is ceil(char_count / 4)', async () => {
  const r = await enrich({ line: { display: 'hi', sessionId: 'x' } });
  assert.equal(r.token_estimate, 1); // ceil(2/4) = 1
});

test('word_count splits on whitespace', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'user',
        content: [{ type: 'text', text: '  one   two  three  ' }],
      },
    },
  });
  assert.equal(r.word_count, 3);
});
