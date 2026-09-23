import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, mkdirSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { readAccountId } from '../src/account';

const TMP = join(tmpdir(), `clued-test-account-${process.pid}`);
mkdirSync(TMP, { recursive: true });

after(() => rmSync(TMP, { recursive: true, force: true }));

test('returns account ID from valid config file', () => {
  const cfgPath = join(TMP, 'config-valid.json');
  writeFileSync(cfgPath, JSON.stringify({ lastKnownAccountUuid: 'abc-123-uuid' }));
  const id = readAccountId(cfgPath);
  assert.equal(id, 'abc-123-uuid');
});

test('returns "unknown" when file is absent and warns', () => {
  const messages: string[] = [];
  const orig = console.warn;
  console.warn = (...args: unknown[]) => { messages.push(args.join(' ')); };
  try {
    const id = readAccountId(join(TMP, 'nonexistent.json'));
    assert.equal(id, 'unknown');
    assert.ok(messages.some(m => m.includes('account ID unavailable')));
  } finally {
    console.warn = orig;
  }
});

test('returns "unknown" when file is unparseable and warns', () => {
  const cfgPath = join(TMP, 'config-bad.json');
  writeFileSync(cfgPath, 'not valid json {{{');
  const messages: string[] = [];
  const orig = console.warn;
  console.warn = (...args: unknown[]) => { messages.push(args.join(' ')); };
  try {
    const id = readAccountId(cfgPath);
    assert.equal(id, 'unknown');
    assert.ok(messages.some(m => m.includes('account ID unavailable')));
  } finally {
    console.warn = orig;
  }
});

test('returns "unknown" when lastKnownAccountUuid key is missing and warns', () => {
  const cfgPath = join(TMP, 'config-no-uuid.json');
  writeFileSync(cfgPath, JSON.stringify({ someOtherKey: 'value' }));
  const messages: string[] = [];
  const orig = console.warn;
  console.warn = (...args: unknown[]) => { messages.push(args.join(' ')); };
  try {
    const id = readAccountId(cfgPath);
    assert.equal(id, 'unknown');
    assert.ok(messages.some(m => m.includes('account ID unavailable')));
  } finally {
    console.warn = orig;
  }
});
