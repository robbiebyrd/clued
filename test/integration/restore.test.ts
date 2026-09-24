import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, rmSync, writeFileSync, readFileSync, existsSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';
import { restoreSession } from '../../src/restore';

const TEST_URL = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';
const TEST_DB  = `clued_restore_test_${Date.now()}`;
const TMP      = join(tmpdir(), `clued-restore-${process.pid}`);
const ACCOUNT  = 'acc-restore';
const SESSION  = 'sess-restore-001';
const PROJ_DIR = '-Users-testuser-Projects-myproject';

let mongo: MongoDb;

after(async () => {
  await mongo?.db.dropDatabase();
  await mongo?.close();
  rmSync(TMP, { recursive: true, force: true });
});

test('setup: seed MongoDB with a full session', async () => {
  mkdirSync(TMP, { recursive: true });
  mongo = await createClient({ mongoUrl: TEST_URL, dbName: TEST_DB });

  const projectsDir    = join(TMP, 'source-projects');
  const transcriptPath = join(projectsDir, PROJ_DIR, `${SESSION}.jsonl`);

  await mongo.sessions.insertOne({
    session_id: SESSION, account_id: ACCOUNT,
    project_path: '/Users/testuser/Projects/myproject',
    transcript_path: transcriptPath,
    cwd: '/Users/testuser/Projects/myproject',
    started_at: new Date(), last_seen: new Date(),
  });

  for (let i = 0; i < 5; i++) {
    await mongo.transcriptLines.insertOne({
      session_id: SESSION, seq: i, account_id: ACCOUNT,
      line: { type: 'user', content: `line ${i}` }, created_at: new Date(),
    });
  }

  await mongo.subagentLines.insertOne({
    session_id: SESSION, subagent_id: 'agent-aabb', seq: 0, account_id: ACCOUNT,
    line: { type: 'user', content: 'subagent line' }, created_at: new Date(),
  });

  await mongo.blobs.insertOne({
    session_id: SESSION, blob_type: 'subagent-meta', name: 'agent-aabb.meta.json',
    content: '{"agentId":"agent-aabb"}', encoding: 'utf8', account_id: ACCOUNT, created_at: new Date(),
  });
  await mongo.blobs.insertOne({
    session_id: SESSION, blob_type: 'tool-result', name: 'result1.txt',
    content: 'tool output', encoding: 'utf8', account_id: ACCOUNT, created_at: new Date(),
  });
  const fhContent = Buffer.from('raw binary').toString('base64');
  await mongo.blobs.insertOne({
    session_id: SESSION, blob_type: 'file-history', name: 'abc@v1',
    content: fhContent, encoding: 'base64', account_id: ACCOUNT, created_at: new Date(),
  });
});

test('restores main JSONL with correct line count', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const fileHistoryDir    = join(TMP, 'target-file-history');

  const result = await restoreSession(
    { session_id: SESSION, projects_dir: targetProjectsDir },
    ACCOUNT, mongo, fileHistoryDir
  );

  const jsonlPath = join(targetProjectsDir, PROJ_DIR, `${SESSION}.jsonl`);
  assert.ok(existsSync(jsonlPath), `JSONL not found at ${jsonlPath}`);
  const lines = readFileSync(jsonlPath, 'utf8').trim().split('\n');
  assert.equal(lines.length, 5);
  assert.ok(result.files_written >= 1);
});

test('restores subagent JSONL and meta', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const subagentJsonl = join(targetProjectsDir, PROJ_DIR, SESSION, 'subagents', 'agent-aabb.jsonl');
  const subagentMeta  = join(targetProjectsDir, PROJ_DIR, SESSION, 'subagents', 'agent-aabb.meta.json');

  assert.ok(existsSync(subagentJsonl), `subagent JSONL not found at ${subagentJsonl}`);
  assert.ok(existsSync(subagentMeta),  `subagent meta not found at ${subagentMeta}`);
  assert.equal(readFileSync(subagentMeta, 'utf8'), '{"agentId":"agent-aabb"}');
});

test('restores tool-result blob', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const blobPath = join(targetProjectsDir, PROJ_DIR, SESSION, 'tool-results', 'result1.txt');
  assert.ok(existsSync(blobPath), `tool-result blob not found at ${blobPath}`);
  assert.equal(readFileSync(blobPath, 'utf8'), 'tool output');
});

test('restores file-history blob decoded from base64', async () => {
  const fileHistoryDir = join(TMP, 'target-file-history');
  const fhPath = join(fileHistoryDir, SESSION, 'abc@v1');
  assert.ok(existsSync(fhPath), `file-history blob not found at ${fhPath}`);
  assert.equal(readFileSync(fhPath, 'utf8'), 'raw binary');
});

test('restoreSession is idempotent — second run produces identical output', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const fileHistoryDir    = join(TMP, 'target-file-history');
  const jsonlPath = join(targetProjectsDir, PROJ_DIR, `${SESSION}.jsonl`);

  const before = readFileSync(jsonlPath, 'utf8');
  await restoreSession(
    { session_id: SESSION, projects_dir: targetProjectsDir },
    ACCOUNT, mongo, fileHistoryDir
  );
  const after = readFileSync(jsonlPath, 'utf8');
  assert.equal(before, after);
});

test('restoreSession with project_path override uses encoded override path', async () => {
  const targetProjectsDir = join(TMP, 'override-projects');
  const fileHistoryDir    = join(TMP, 'override-file-history');

  await restoreSession(
    { session_id: SESSION, project_path: '/Users/otheruser/Projects/myproject',
      projects_dir: targetProjectsDir },
    ACCOUNT, mongo, fileHistoryDir
  );

  const expectedDir = '-Users-otheruser-Projects-myproject';
  const jsonlPath = join(targetProjectsDir, expectedDir, `${SESSION}.jsonl`);
  assert.ok(existsSync(jsonlPath), `JSONL not found at override path ${jsonlPath}`);
});

test('throws on unknown session_id', async () => {
  await assert.rejects(
    () => restoreSession({ session_id: 'no-such-session' }, ACCOUNT, mongo, TMP),
    /session not found/
  );
});
