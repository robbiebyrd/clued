import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/prompt-features.js';

test('name and collection', () => {
  assert.equal(name, 'prompt-features');
  assert.equal(collection, 'hook_events');
});

test('matches UserPromptSubmit only', () => {
  assert.equal(matches({ hook_event_name: 'UserPromptSubmit' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse' }), false);
  assert.equal(matches({}), false);
});

test('empty prompt → zeroed fields', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: '' });
  assert.equal(r.word_count, 0);
  assert.equal(r.is_slash_command, false);
  assert.equal(r.command_name, null);
  assert.equal(r.intent, null);
  assert.equal(r.looks_like_task_start, false);
});

test('slash command detected', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: '/clear' });
  assert.equal(r.is_slash_command, true);
  assert.equal(r.command_name, 'clear');
  assert.equal(r.intent, null);
});

test('slash command with args extracts name only', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: '/reload-plugins foo' });
  assert.equal(r.command_name, 'reload-plugins');
});

test('question intent by trailing ?', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'Is this correct?' });
  assert.equal(r.intent, 'question');
});

test('question intent by opener word', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'How do I fix this' });
  assert.equal(r.intent, 'question');
});

test('instruction intent', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'Fix the failing tests in auth.ts' });
  assert.equal(r.intent, 'instruction');
});

test('approval intent: ok', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'ok' });
  assert.equal(r.intent, 'approval');
});

test('approval intent: lgtm', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'lgtm' });
  assert.equal(r.intent, 'approval');
});

test('correction intent', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: "No, that's wrong" });
  assert.equal(r.intent, 'correction');
});

test('looks_like_task_start: instruction + 6+ words, no continuation', async () => {
  const r = await enrich({
    hook_event_name: 'UserPromptSubmit',
    prompt: 'Fix the authentication bug in login handler',
  });
  assert.equal(r.looks_like_task_start, true);
});

test('looks_like_task_start: false when fewer than 6 words', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'Fix this bug' });
  assert.equal(r.looks_like_task_start, false);
});

test('looks_like_task_start: false for continuation prefix', async () => {
  const r = await enrich({
    hook_event_name: 'UserPromptSubmit',
    prompt: 'Also fix the broken test in auth suite',
  });
  assert.equal(r.looks_like_task_start, false);
});

test('word_count is correct', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'one two three' });
  assert.equal(r.word_count, 3);
});
