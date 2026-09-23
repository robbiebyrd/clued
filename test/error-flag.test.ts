import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/error-flag';

test('name and collection', () => {
  assert.equal(name, 'error-flag');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('no error for empty line', async () => {
  const r = await enrich({ line: {} });
  assert.deepEqual(r, { is_error: false, error_type: null });
});

test('detects hook_error', async () => {
  const r = await enrich({ line: { attachment: { type: 'hook_error' } } });
  assert.deepEqual(r, { is_error: true, error_type: 'hook_error' });
});

test('detects tool_failure via is_error on tool_result block', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'user',
        content: [{ type: 'tool_result', tool_use_id: 'x', is_error: true, content: 'failed' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'tool_failure' });
});

test('no tool_failure when is_error is false on tool_result', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'user',
        content: [{ type: 'tool_result', tool_use_id: 'x', is_error: false, content: 'ok' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: false, error_type: null });
});

test('detects error_text: ENOENT', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'ENOENT: no such file or directory' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'error_text' });
});

test('detects error_text: Error:', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'Error: connection refused' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'error_text' });
});

test('no false positive on normal assistant text', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'The function works correctly.' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: false, error_type: null });
});

test('hook_error takes precedence over text pattern', async () => {
  const r = await enrich({
    line: {
      attachment: { type: 'hook_error' },
      message: { role: 'assistant', content: [{ type: 'text', text: 'Error: something' }] },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'hook_error' });
});
