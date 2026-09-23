import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import { createClient } from '../../src/mongo';
import { matches, enrich, name, collection } from '../../enrichers/hook-linker';
import type { MongoDb } from '../../src/mongo';

const TEST_CONFIG = {
  mongoUrl: process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018',
  dbName:   `clued_test_${Date.now()}`,
};

let mongo: MongoDb;
before(async () => { mongo = await createClient(TEST_CONFIG); });
after(async () => { await mongo.db.dropDatabase(); await mongo.close(); });

test('name and collection', () => {
  assert.equal(name, 'hook-linker');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
  assert.equal(matches({ anything: 1 }), true);
});

test('returns empty arrays for line with no tool_use content', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } }, mongo);
  assert.deepEqual(r, { tool_use_ids: [], hook_event_ids: [] });
});

test('collects tool_use_id from attachment.toolUseID and finds matching hook event', async () => {
  const toolUseId  = 'test-tuid-1';
  const session_id = 'sess-hook-test';
  const inserted   = await mongo.hookEvents.insertOne({ session_id, tool_name: 'Bash', tool_use_id: toolUseId, created_at: new Date() });

  const doc = {
    session_id,
    line: { attachment: { type: 'hook_success', toolUseID: toolUseId, hookEvent: 'PostToolUse' } },
  };

  const r = await enrich(doc, mongo);
  assert.deepEqual(r.tool_use_ids, [toolUseId]);
  assert.equal(r.hook_event_ids.length, 1);
  assert.deepEqual(r.hook_event_ids[0], inserted.insertedId);
});

test('collects tool_use ids from assistant message content blocks', async () => {
  const toolUseId  = 'test-tuid-2';
  const session_id = 'sess-hook-test-2';
  const inserted   = await mongo.hookEvents.insertOne({ session_id, tool_name: 'Read', tool_use_id: toolUseId, created_at: new Date() });

  const doc = {
    session_id,
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: toolUseId, name: 'Read', input: { file_path: '/x' } },
        ],
      },
    },
  };

  const r = await enrich(doc, mongo);
  assert.deepEqual(r.tool_use_ids, [toolUseId]);
  assert.equal(r.hook_event_ids.length, 1);
  assert.deepEqual(r.hook_event_ids[0], inserted.insertedId);
});

test('returns empty hook_event_ids when no matching hook event exists', async () => {
  const doc = {
    session_id: 'no-session',
    line: { attachment: { type: 'hook_success', toolUseID: 'nonexistent-id' } },
  };
  const r = await enrich(doc, mongo);
  assert.deepEqual(r.tool_use_ids, ['nonexistent-id']);
  assert.deepEqual(r.hook_event_ids, []);
});

test('deduplicates tool_use_ids from both sources', async () => {
  const toolUseId  = 'duplicate-id';
  const session_id = 'sess-dedup';
  const doc = {
    session_id,
    line: {
      attachment: { type: 'hook_success', toolUseID: toolUseId },
      message: {
        role: 'assistant',
        content: [{ type: 'tool_use', id: toolUseId, name: 'Bash', input: {} }],
      },
    },
  };
  const r = await enrich(doc, mongo);
  assert.equal(r.tool_use_ids.length, 1);
  assert.deepEqual(r.tool_use_ids, [toolUseId]);
});
