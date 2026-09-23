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
