import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo.mjs';
import { backfill, decodeProjectPath } from '../../src/backfill.mjs';

const TMP = join(tmpdir(), `clued-backfill-test-${process.pid}`);
const TEST_CONFIG = {
  mongoUrl:    process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018',
  dbName:      `clued_bf_test_${Date.now()}`,
  projectsDir: TMP,
};

let mongo;
after(async () => {
  if (mongo) { await mongo.db.dropDatabase(); await mongo.close(); }
  rmSync(TMP, { recursive: true, force: true });
});

test('decodeProjectPath converts encoded dir name to absolute path', () => {
  assert.equal(decodeProjectPath('-Users-alice-Projects-myapp'), '/Users/alice/Projects/myapp');
});

test('decodeProjectPath best-effort on paths with hyphens in components', () => {
  const decoded = decodeProjectPath('-Users-rob-byrd-Projects-foo');
  assert.ok(decoded.startsWith('/'), 'decoded path must be absolute');
});

test('upserts sessions and transcript lines from project directories', async () => {
  const projDir   = join(TMP, '-Users-test-myproject');
  const sessionId = 'aaaabbbb-cccc-dddd-eeee-ffffffffffff';
  mkdirSync(projDir, { recursive: true });
  writeFileSync(join(projDir, `${sessionId}.jsonl`), [
    JSON.stringify({ type: 'human', text: 'hello' }),
    JSON.stringify({ type: 'assistant', text: 'world' }),
  ].join('\n') + '\n');

  mongo = await createClient(TEST_CONFIG);
  await backfill(TEST_CONFIG, mongo);

  const session = await mongo.sessions.findOne({ session_id: sessionId });
  assert.ok(session, 'session doc not found');
  assert.equal(session.project_path, '/Users/test/myproject');

  const lines = await mongo.transcriptLines.find({ session_id: sessionId }).sort({ seq: 1 }).toArray();
  assert.equal(lines.length, 2);
  assert.equal(lines[0].line.type, 'human');
  assert.equal(lines[1].line.type, 'assistant');
});

test('is idempotent — re-running does not duplicate lines', async () => {
  await backfill(TEST_CONFIG, mongo);
  const count = await mongo.transcriptLines.countDocuments({ session_id: 'aaaabbbb-cccc-dddd-eeee-ffffffffffff' });
  assert.equal(count, 2);
});

test('handles empty project directory gracefully', async () => {
  const emptyDir = join(TMP, '-Users-test-empty');
  mkdirSync(emptyDir, { recursive: true });
  await assert.doesNotReject(() => backfill(TEST_CONFIG, mongo));
});
