import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';

const TEST_CONFIG = {
  mongoUrl: process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018',
  dbName:   `clued_test_${Date.now()}`,
};

let mongo: MongoDb;
after(async () => {
  if (mongo) {
    await mongo.db.dropDatabase();
    await mongo.close();
  }
});

test('connects and creates indexes', async () => {
  mongo = await createClient(TEST_CONFIG);
  assert.ok(mongo.sessions,        'sessions collection missing');
  assert.ok(mongo.hookEvents,      'hookEvents collection missing');
  assert.ok(mongo.transcriptLines, 'transcriptLines collection missing');
});

test('upserts a session doc', async () => {
  const doc = { session_id: 'test-123', cwd: '/tmp', started_at: new Date() };
  await mongo.sessions.updateOne(
    { session_id: doc.session_id },
    { $set: { session_id: doc.session_id, cwd: doc.cwd }, $setOnInsert: { started_at: doc.started_at } },
    { upsert: true }
  );
  const found = await mongo.sessions.findOne({ session_id: 'test-123' });
  assert.equal((found as Record<string, unknown>)?.cwd, '/tmp');
});

test('inserts a hook event', async () => {
  await mongo.hookEvents.insertOne({ session_id: 'test-123', type: 'PreToolUse', created_at: new Date() });
  const found = await mongo.hookEvents.findOne({ session_id: 'test-123' });
  assert.equal((found as Record<string, unknown>)?.type, 'PreToolUse');
});

test('creates subagentLines and blobs collections', async () => {
  assert.ok(mongo.subagentLines, 'subagentLines collection missing');
  assert.ok(mongo.blobs,         'blobs collection missing');
});

test('upserts subagent lines by session_id + subagent_id + seq; created_at not overwritten', async () => {
  const firstSeen = new Date(Date.now() - 5000);
  await mongo.subagentLines.updateOne(
    { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 },
    { $set: { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0,
              line: { type: 'user' }, account_id: 'acc' },
      $setOnInsert: { created_at: firstSeen } },
    { upsert: true }
  );
  await mongo.subagentLines.updateOne(
    { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 },
    { $set: { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0,
              line: { type: 'user' }, account_id: 'acc' },
      $setOnInsert: { created_at: new Date() } },
    { upsert: true }
  );
  const count = await mongo.subagentLines.countDocuments({ session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 });
  assert.equal(count, 1);
  const doc = await mongo.subagentLines.findOne({ session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 });
  assert.equal((doc as Record<string, unknown>)?.created_at?.valueOf(), firstSeen.valueOf(),
    'created_at must not be overwritten on subsequent upserts');
});

test('upserts blobs by session_id + blob_type + name; content updates on second upsert', async () => {
  const now = new Date();
  await mongo.blobs.updateOne(
    { session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' },
    { $set: { content: 'first', encoding: 'utf8', account_id: 'acc' },
      $setOnInsert: { created_at: now } },
    { upsert: true }
  );
  await mongo.blobs.updateOne(
    { session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' },
    { $set: { content: 'second', encoding: 'utf8', account_id: 'acc' },
      $setOnInsert: { created_at: now } },
    { upsert: true }
  );
  const doc = await mongo.blobs.findOne({ session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' });
  assert.equal((doc as Record<string, unknown>)?.content, 'second');
  const count = await mongo.blobs.countDocuments({ session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' });
  assert.equal(count, 1);
});

test('upserts transcript lines by session_id + seq', async () => {
  await mongo.transcriptLines.updateOne(
    { session_id: 'test-123', seq: 0 },
    { $set: { session_id: 'test-123', seq: 0, line: { type: 'human' }, created_at: new Date() } },
    { upsert: true }
  );
  // Second upsert of same seq — should not duplicate
  await mongo.transcriptLines.updateOne(
    { session_id: 'test-123', seq: 0 },
    { $set: { session_id: 'test-123', seq: 0, line: { type: 'human' }, created_at: new Date() } },
    { upsert: true }
  );
  const count = await mongo.transcriptLines.countDocuments({ session_id: 'test-123', seq: 0 });
  assert.equal(count, 1);
});
