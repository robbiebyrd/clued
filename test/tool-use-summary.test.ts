import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/tool-use-summary';

test('name and collection', () => {
  assert.equal(name, 'tool-use-summary');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns empty tools and no thinking for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { tools: [], has_thinking: false });
});

test('extracts tool_use blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'ls', cwd: '/tmp' } },
          { type: 'text', text: 'done' },
        ],
      },
    },
  });
  assert.deepEqual(r, {
    tools: [{ id: 'tu1', name: 'Bash', inputKeys: ['command', 'cwd'] }],
    has_thinking: false,
  });
});

test('detects thinking block', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'thinking', thinking: 'hmm' },
          { type: 'tool_use', id: 'tu2', name: 'Read', input: { file_path: '/x' } },
        ],
      },
    },
  });
  assert.equal(r.has_thinking, true);
  assert.equal(r.tools.length, 1);
  assert.equal(r.tools[0].name, 'Read');
});

test('multiple tool_use blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'a', name: 'Bash', input: { command: 'ls' } },
          { type: 'tool_use', id: 'b', name: 'Read', input: { file_path: '/x' } },
        ],
      },
    },
  });
  assert.equal(r.tools.length, 2);
});

test('returns empty for assistant with no tool_use or thinking', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [{ type: 'text', text: 'hi' }] } },
  });
  assert.deepEqual(r, { tools: [], has_thinking: false });
});

test('handles missing or null content gracefully', async () => {
  const r = await enrich({ line: { message: { role: 'assistant' } } });
  assert.deepEqual(r, { tools: [], has_thinking: false });
});
