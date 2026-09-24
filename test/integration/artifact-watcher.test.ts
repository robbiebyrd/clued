import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';
import { watchArtifactDirs } from '../../src/artifact-watcher';

function delay(ms: number): Promise<void> { return new Promise(r => setTimeout(r, ms)); }

const TEST_URL = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';
const TEST_DB  = `clued_watcher_test_${Date.now()}`;
const TMP      = join(tmpdir(), `clued-watcher-${process.pid}`);
const ACCOUNT  = 'acc-watcher';
const HOST     = { hostname: 'test' };

let mongo: MongoDb;

after(async () => {
  await mongo?.db.dropDatabase();
  await mongo?.close();
  rmSync(TMP, { recursive: true, force: true });
});

test('setup', async () => {
  mkdirSync(TMP, { recursive: true });
  mongo = await createClient({ mongoUrl: TEST_URL, dbName: TEST_DB });
});

test('captures tool-result blob when file appears in tool-results/', async () => {
  const SESSION       = 'sess-tr-001';
  const sessionDir    = join(TMP, 'tr-session');
  const fileHistPath  = join(TMP, 'tr-fh', SESSION);
  mkdirSync(sessionDir, { recursive: true });

  watchArtifactDirs(SESSION, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  const toolResultsDir = join(sessionDir, 'tool-results');
  mkdirSync(toolResultsDir);
  writeFileSync(join(toolResultsDir, 'blob1.txt'), 'tool output here');

  await delay(3000);

  const doc = await mongo.blobs.findOne({ session_id: SESSION, blob_type: 'tool-result', name: 'blob1.txt' });
  assert.ok(doc, 'blob document not found');
  assert.equal((doc as Record<string, unknown>).content, 'tool output here');
  assert.equal((doc as Record<string, unknown>).encoding, 'utf8');
});

test('captures file-history blob as base64 when file appears', async () => {
  const SESSION2     = 'sess-fh-001';
  const sessionDir   = join(TMP, 'fh-session');
  const fileHistPath = join(TMP, 'fh-dir', SESSION2);
  mkdirSync(sessionDir, { recursive: true });

  watchArtifactDirs(SESSION2, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  mkdirSync(fileHistPath, { recursive: true });
  const rawContent = 'binary file content';
  writeFileSync(join(fileHistPath, 'abc123@v1'), rawContent);

  await delay(3000);

  const doc = await mongo.blobs.findOne({ session_id: SESSION2, blob_type: 'file-history', name: 'abc123@v1' });
  assert.ok(doc, 'file-history blob not found');
  assert.equal((doc as Record<string, unknown>).encoding, 'base64');
  const decoded = Buffer.from((doc as Record<string, unknown>).content as string, 'base64').toString('utf8');
  assert.equal(decoded, rawContent);
});

test('tails subagent JSONL and captures subagent-meta', async () => {
  const SESSION3     = 'sess-sub-001';
  const sessionDir   = join(TMP, 'sub-session');
  const fileHistPath = join(TMP, 'sub-fh', SESSION3);
  const subagentsDir = join(sessionDir, 'subagents');
  mkdirSync(subagentsDir, { recursive: true });

  watchArtifactDirs(SESSION3, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  const agentId = 'agent-aabbccdd';
  writeFileSync(join(subagentsDir, `${agentId}.jsonl`),
    '{"type":"user","uuid":"u1"}\n{"type":"assistant","uuid":"u2"}\n');
  writeFileSync(join(subagentsDir, `${agentId}.meta.json`),
    JSON.stringify({ agentId, created: new Date().toISOString() }));

  await delay(3000);

  const lineCount = await mongo.subagentLines.countDocuments({ session_id: SESSION3, subagent_id: agentId });
  assert.equal(lineCount, 2);

  const meta = await mongo.blobs.findOne({ session_id: SESSION3, blob_type: 'subagent-meta', name: `${agentId}.meta.json` });
  assert.ok(meta, 'subagent meta not found');
  assert.equal((meta as Record<string, unknown>).encoding, 'utf8');
});

test('updates content when tool-result file is overwritten', async () => {
  const SESSION4       = 'sess-mut-001';
  const sessionDir     = join(TMP, 'mut-session');
  const fileHistPath   = join(TMP, 'mut-fh', SESSION4);
  const toolResultsDir = join(sessionDir, 'tool-results');
  mkdirSync(toolResultsDir, { recursive: true });

  writeFileSync(join(toolResultsDir, 'mutable.txt'), 'version 1');
  watchArtifactDirs(SESSION4, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  await delay(3000);

  writeFileSync(join(toolResultsDir, 'mutable.txt'), 'version 2');
  await delay(3000);

  const doc = await mongo.blobs.findOne({ session_id: SESSION4, blob_type: 'tool-result', name: 'mutable.txt' });
  assert.equal((doc as Record<string, unknown>).content, 'version 2');
  const count = await mongo.blobs.countDocuments({ session_id: SESSION4, blob_type: 'tool-result', name: 'mutable.txt' });
  assert.equal(count, 1, 'must not duplicate on overwrite');
});

test('does not start duplicate tailer when subagent JSONL mtime changes', async () => {
  const SESSION5     = 'sess-nodup-001';
  const sessionDir   = join(TMP, 'nodup-session');
  const fileHistPath = join(TMP, 'nodup-fh', SESSION5);
  const subagentsDir = join(sessionDir, 'subagents');
  mkdirSync(subagentsDir, { recursive: true });

  const agentId = 'agent-noduptest';
  writeFileSync(join(subagentsDir, `${agentId}.jsonl`), '{"type":"user"}\n');
  watchArtifactDirs(SESSION5, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  await delay(3000);

  // Append a second line — should be tailed once, seq=1
  const { appendFileSync } = await import('fs');
  appendFileSync(join(subagentsDir, `${agentId}.jsonl`), '{"type":"assistant"}\n');
  await delay(3000);

  const lines = await mongo.subagentLines
    .find({ session_id: SESSION5, subagent_id: agentId })
    .sort({ seq: 1 })
    .toArray();
  assert.equal(lines.length, 2, `expected 2 lines, got ${lines.length}`);
  assert.equal((lines[0] as Record<string, unknown>).seq, 0);
  assert.equal((lines[1] as Record<string, unknown>).seq, 1);
});
