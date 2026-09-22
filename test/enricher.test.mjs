import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { loadEnrichers } from '../src/enricher.mjs';

const TMP = join(tmpdir(), `clued-enricher-test-${process.pid}`);
mkdirSync(TMP, { recursive: true });
after(() => rmSync(TMP, { recursive: true, force: true }));

test('loads enabled enrichers from directory', async () => {
  const dir = join(TMP, 'enrichers-a');
  mkdirSync(dir);
  writeFileSync(join(dir, 'foo.mjs'), `
    export const name = 'foo';
    export const collection = 'hook_events';
    export const enabled = true;
    export function matches(doc) { return true; }
    export async function enrich(doc) { return { ok: true }; }
  `);
  const enrichers = await loadEnrichers(dir, { disabledEnrichers: [] });
  assert.equal(enrichers.length, 1);
  assert.equal(enrichers[0].name, 'foo');
});

test('skips enrichers with enabled = false', async () => {
  const dir = join(TMP, 'enrichers-b');
  mkdirSync(dir);
  writeFileSync(join(dir, 'bar.mjs'), `
    export const name = 'bar';
    export const collection = 'hook_events';
    export const enabled = false;
    export function matches() { return true; }
    export async function enrich() { return {}; }
  `);
  const enrichers = await loadEnrichers(dir, { disabledEnrichers: [] });
  assert.equal(enrichers.length, 0);
});

test('skips enrichers listed in disabledEnrichers config', async () => {
  const dir = join(TMP, 'enrichers-c');
  mkdirSync(dir);
  writeFileSync(join(dir, 'baz.mjs'), `
    export const name = 'baz';
    export const collection = 'hook_events';
    export const enabled = true;
    export function matches() { return true; }
    export async function enrich() { return {}; }
  `);
  const enrichers = await loadEnrichers(dir, { disabledEnrichers: ['baz'] });
  assert.equal(enrichers.length, 0);
});

test('returns empty array for missing enrichers directory', async () => {
  const enrichers = await loadEnrichers(join(TMP, 'nonexistent'), { disabledEnrichers: [] });
  assert.deepEqual(enrichers, []);
});
