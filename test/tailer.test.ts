import { test } from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, appendFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { tailFile } from '../src/tailer';

function delay(ms: number): Promise<void> { return new Promise(r => setTimeout(r, ms)); }

test('delivers lines present at start', async () => {
  const f = join(tmpdir(), `tail-${Date.now()}-1.jsonl`);
  writeFileSync(f, '{"a":1}\n{"b":2}\n');
  const lines: string[] = [];
  const { stop } = tailFile(f, l => lines.push(l));
  await delay(200);
  stop();
  rmSync(f);
  assert.deepEqual(lines, ['{"a":1}', '{"b":2}']);
});

test('delivers lines appended after start', async () => {
  const f = join(tmpdir(), `tail-${Date.now()}-2.jsonl`);
  writeFileSync(f, '{"a":1}\n');
  const lines: string[] = [];
  const { stop } = tailFile(f, l => lines.push(l));
  await delay(100);
  appendFileSync(f, '{"b":2}\n');
  await delay(2500); // wait for 2s poll
  stop();
  rmSync(f);
  assert.ok(lines.includes('{"b":2}'), `expected {"b":2} in ${JSON.stringify(lines)}`);
});

test('waits for file to appear then delivers lines', async () => {
  const f = join(tmpdir(), `tail-${Date.now()}-3.jsonl`);
  const lines: string[] = [];
  const { stop } = tailFile(f, l => lines.push(l));
  await delay(300);
  writeFileSync(f, '{"x":1}\n');
  await delay(800);
  stop();
  rmSync(f, { force: true });
  assert.ok(lines.includes('{"x":1}'), `expected {"x":1} in ${JSON.stringify(lines)}`);
});

test('stop() halts delivery of new lines', async () => {
  const f = join(tmpdir(), `tail-${Date.now()}-4.jsonl`);
  writeFileSync(f, '{"a":1}\n');
  const lines: string[] = [];
  const { stop } = tailFile(f, l => lines.push(l));
  await delay(100);
  stop();
  appendFileSync(f, '{"b":2}\n');
  await delay(2500);
  rmSync(f);
  assert.ok(!lines.includes('{"b":2}'), 'should not receive lines after stop()');
});
