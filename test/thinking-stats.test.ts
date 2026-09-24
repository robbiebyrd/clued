import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/thinking-stats.js';

test('name and collection', () => {
  assert.equal(name, 'thinking-stats');
  assert.equal(collection, 'transcript_lines');
});

test('matches assistant line type only', () => {
  assert.equal(matches({ line: { type: 'assistant' } }), true);
  assert.equal(matches({ line: { type: 'user' } }), false);
  assert.equal(matches({ line: { type: 'attachment' } }), false);
  assert.equal(matches({}), false);
});

test('returns zeros for non-assistant message role', async () => {
  const r = await enrich({ line: { type: 'assistant', message: { role: 'user', content: [] } } });
  assert.deepEqual(r, { thinking_chars: 0, text_chars: 0 });
});

test('counts thinking_chars from thinking blocks', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'thinking', thinking: 'hmm let me think' }],
      },
    },
  });
  assert.equal(r.thinking_chars, 16);
  assert.equal(r.text_chars, 0);
});

test('counts text_chars from text blocks', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'hello world' }],
      },
    },
  });
  assert.equal(r.text_chars, 11);
  assert.equal(r.thinking_chars, 0);
});

test('accumulates multiple blocks', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [
          { type: 'thinking', thinking: 'abc' },
          { type: 'thinking', thinking: 'de' },
          { type: 'text', text: 'fgh' },
        ],
      },
    },
  });
  assert.equal(r.thinking_chars, 5);
  assert.equal(r.text_chars, 3);
});

test('non-text/thinking blocks do not count', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'tool_use', id: 'x', name: 'Bash', input: {} }],
      },
    },
  });
  assert.deepEqual(r, { thinking_chars: 0, text_chars: 0 });
});

test('returns zeros when content is missing', async () => {
  const r = await enrich({
    line: { type: 'assistant', message: { role: 'assistant' } },
  });
  assert.deepEqual(r, { thinking_chars: 0, text_chars: 0 });
});
