import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/code-language-detector';

test('name and collection', () => {
  assert.equal(name, 'code-language-detector');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns empty languages for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { languages: [] });
});

test('extracts single language from fenced code block', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'Here:\n```typescript\nconst x = 1;\n```' }],
      },
    },
  });
  assert.deepEqual(r, { languages: ['typescript'] });
});

test('extracts multiple languages, deduplicated, lowercased', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: '```Bash\nls\n```\nand\n```BASH\necho hi\n```\nand\n```json\n{}\n```' }],
      },
    },
  });
  assert.deepEqual(r.languages.sort(), ['bash', 'json']);
});

test('returns empty for assistant text with no code blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'No code here.' }],
      },
    },
  });
  assert.deepEqual(r, { languages: [] });
});

test('handles empty content array', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [] } },
  });
  assert.deepEqual(r, { languages: [] });
});
