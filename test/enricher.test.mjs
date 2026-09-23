import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { loadEnrichers, startEnrichmentLoop } from '../src/enricher.mjs';

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

test('startEnrichmentLoop enriches matching docs on success', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });

  const updated = [];
  const mockCollection = {
    find: () => ({ limit: () => ({ toArray: async () => [{ _id: 'id1', tool_name: 'Bash', tool_input: { command: 'ls' } }] }) }),
    updateOne: async (filter, update) => { updated.push({ filter, update }); },
  };
  const mockMongo = { db: { collection: () => mockCollection } };
  const enricher = {
    name: 'test-enricher',
    collection: 'hook_events',
    matches: doc => doc.tool_name === 'Bash',
    enrich: async () => ({ result: 'ok' }),
  };

  const loop = startEnrichmentLoop(mockMongo, [enricher]);
  t.mock.timers.tick(5000);
  await new Promise(resolve => setImmediate(resolve));
  loop.stop();

  assert.equal(updated.length, 1);
  assert.deepEqual(updated[0].update, { $set: { 'enriched.test-enricher': { result: 'ok' } } });
});

test('startEnrichmentLoop writes failure field when enrich throws', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });

  const updated = [];
  const mockCollection = {
    find: () => ({ limit: () => ({ toArray: async () => [{ _id: 'id2', tool_name: 'Bash', tool_input: { command: 'ls' } }] }) }),
    updateOne: async (_filter, update) => { updated.push(update); },
  };
  const mockMongo = { db: { collection: () => mockCollection } };
  const enricher = {
    name: 'failing-enricher',
    collection: 'hook_events',
    matches: () => true,
    enrich: async () => { throw new Error('boom'); },
  };

  const loop = startEnrichmentLoop(mockMongo, [enricher]);
  t.mock.timers.tick(5000);
  await new Promise(resolve => setImmediate(resolve));
  loop.stop();

  assert.equal(updated.length, 1);
  assert.equal(updated[0].$set['enriched.failing-enricher_failed'].message, 'boom');
  assert.ok(updated[0].$set['enriched.failing-enricher_failed'].at instanceof Date);
});

test('startEnrichmentLoop skips docs where matches returns false', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });

  const updated = [];
  const mockCollection = {
    find: () => ({ limit: () => ({ toArray: async () => [{ _id: 'id3', tool_name: 'NotBash' }] }) }),
    updateOne: async (_filter, update) => { updated.push(update); },
  };
  const mockMongo = { db: { collection: () => mockCollection } };
  const enricher = {
    name: 'selective-enricher',
    collection: 'hook_events',
    matches: doc => doc.tool_name === 'Bash',
    enrich: async () => ({ result: 'ok' }),
  };

  const loop = startEnrichmentLoop(mockMongo, [enricher]);
  t.mock.timers.tick(5000);
  await new Promise(resolve => setImmediate(resolve));
  loop.stop();

  assert.equal(updated.length, 0);
});
