import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/skill-detector';

test('name and collection', () => {
  assert.equal(name, 'skill-detector');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns empty skills for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { skills: [] });
});

test('detects a Skill tool invocation', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'x', name: 'Skill', input: { skill: 'brainstorming', args: 'feature X' } },
        ],
      },
    },
  });
  assert.deepEqual(r, { skills: [{ name: 'brainstorming', args: 'feature X' }] });
});

test('ignores non-Skill tool_use blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'a', name: 'Bash', input: { command: 'ls' } },
          { type: 'tool_use', id: 'b', name: 'Skill', input: { skill: 'tdd' } },
        ],
      },
    },
  });
  assert.deepEqual(r, { skills: [{ name: 'tdd' }] });
});

test('handles missing args gracefully', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'x', name: 'Skill', input: { skill: 'no-args-skill' } },
        ],
      },
    },
  });
  assert.deepEqual(r, { skills: [{ name: 'no-args-skill' }] });
});

test('multiple skill invocations in one message', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: '1', name: 'Skill', input: { skill: 'alpha' } },
          { type: 'tool_use', id: '2', name: 'Skill', input: { skill: 'beta', args: 'x' } },
        ],
      },
    },
  });
  assert.equal(r.skills.length, 2);
});
