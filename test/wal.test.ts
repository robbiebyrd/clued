import { test } from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, readFileSync, rmSync, existsSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { appendToWal, flushWal } from '../src/wal';

function tmpWal(): string {
  return join(tmpdir(), `wal-${Date.now()}-${Math.random().toString(36).slice(2)}.jsonl`);
}

test('appendToWal creates file and writes a JSON line', () => {
  const f = tmpWal();
  try {
    appendToWal(f, { type: 'test', session_id: 'abc' });
    const lines = readFileSync(f, 'utf8').trim().split('\n');
    assert.equal(lines.length, 1);
    const parsed = JSON.parse(lines[0]) as Record<string, unknown>;
    assert.equal(parsed.type, 'test');
    assert.equal(parsed.session_id, 'abc');
  } finally {
    rmSync(f, { force: true });
  }
});

test('appendToWal appends successive events as separate lines', () => {
  const f = tmpWal();
  try {
    appendToWal(f, { type: 'a' });
    appendToWal(f, { type: 'b' });
    const lines = readFileSync(f, 'utf8').trim().split('\n');
    assert.equal(lines.length, 2);
    assert.equal((JSON.parse(lines[0]) as Record<string, unknown>).type, 'a');
    assert.equal((JSON.parse(lines[1]) as Record<string, unknown>).type, 'b');
  } finally {
    rmSync(f, { force: true });
  }
});

test('flushWal calls insert for every entry and clears the file', async () => {
  const f = tmpWal();
  try {
    writeFileSync(f, '{"type":"a"}\n{"type":"b"}\n');
    const inserted: unknown[] = [];
    await flushWal(f, async doc => { inserted.push(doc); });
    assert.equal(inserted.length, 2);
    const remaining = readFileSync(f, 'utf8').trim();
    assert.equal(remaining, '');
  } finally {
    rmSync(f, { force: true });
  }
});

test('flushWal retains only entries whose insert throws', async () => {
  const f = tmpWal();
  try {
    writeFileSync(f, '{"type":"a"}\n{"type":"b"}\n{"type":"c"}\n');
    await flushWal(f, async doc => {
      if ((doc as Record<string, unknown>).type === 'b') throw new Error('mongo down');
    });
    const lines = readFileSync(f, 'utf8').trim().split('\n').filter(Boolean);
    assert.equal(lines.length, 1);
    assert.equal((JSON.parse(lines[0]) as Record<string, unknown>).type, 'b');
  } finally {
    rmSync(f, { force: true });
  }
});

test('flushWal does nothing when WAL file does not exist', async () => {
  const f = tmpWal();
  // must not throw, and insert must never be called
  await flushWal(f, async () => { throw new Error('should not be called'); });
  assert.ok(!existsSync(f));
});

test('flushWal skips malformed lines without throwing', async () => {
  const f = tmpWal();
  try {
    writeFileSync(f, '{"type":"a"}\nnot-json\n{"type":"c"}\n');
    const inserted: unknown[] = [];
    await flushWal(f, async doc => { inserted.push(doc); });
    assert.equal(inserted.length, 2);
  } finally {
    rmSync(f, { force: true });
  }
});
