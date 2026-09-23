import { test } from 'node:test';
import assert from 'node:assert/strict';

const { matches, enrich, enabled, name } = await import('../enrichers/privacy-redact.mjs');

test('is disabled by default', () => {
  assert.equal(enabled, false);
});

test('name is privacy-redact', () => {
  assert.equal(name, 'privacy-redact');
});

test('matches any doc with a line field', () => {
  assert.equal(matches({ line: 'hello' }), true);
  assert.equal(matches({ line: null }), false);
  assert.equal(matches({}), false);
});

test('redacts OpenAI-style API key from string line', async () => {
  const result = await enrich({ line: 'token=sk-abc123DEF456ghi789JKL012' });
  assert.match(result.redacted_line, /\[REDACTED:api-key\]/);
  assert.ok(!result.redacted_line.includes('sk-abc123'), 'raw key must not appear in output');
});

test('redacts email address', async () => {
  const result = await enrich({ line: 'contact me at user@example.com please' });
  assert.match(result.redacted_line, /\[REDACTED:email\]/);
});

test('redacts JWT token', async () => {
  const jwt = 'eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c';
  const result = await enrich({ line: `auth: ${jwt}` });
  assert.match(result.redacted_line, /\[REDACTED:jwt\]/);
});

test('does not mutate line field — raw data preserved in original doc', async () => {
  const doc = { line: 'email: user@example.com' };
  await enrich(doc);
  assert.equal(doc.line, 'email: user@example.com', 'original line must be unchanged');
});

test('handles object line by JSON-stringifying before redacting', async () => {
  const result = await enrich({ line: { text: 'sk-abc123DEF456ghi789JKL012' } });
  assert.match(result.redacted_line, /\[REDACTED:api-key\]/);
});
