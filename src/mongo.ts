import { MongoClient, Collection, Db } from 'mongodb';
import type { Config } from './config';

export interface MongoDb {
  db:              Db;
  sessions:        Collection;
  hookEvents:      Collection;
  transcriptLines: Collection;
  subagentLines:   Collection;
  blobs:           Collection;
  sessionFull:     Collection;
  close:           () => Promise<void>;
}

// Aggregation view that joins all sub-collections for a session into one document.
// blob content is excluded to avoid multi-MB payloads; redundant session_id / account_id /
// host fields are stripped from sub-arrays since they're already on the parent.
const SESSION_FULL_PIPELINE = [
  {
    $lookup: {
      from: 'transcript_lines',
      let:  { sid: '$session_id' },
      pipeline: [
        { $match:  { $expr: { $eq: ['$session_id', '$$sid'] } } },
        { $sort:   { seq: 1 } },
        { $unset:  ['_id', 'session_id', 'account_id', 'host'] },
      ],
      as: 'transcript_lines',
    },
  },
  {
    $lookup: {
      from: 'subagent_lines',
      let:  { sid: '$session_id' },
      pipeline: [
        { $match:  { $expr: { $eq: ['$session_id', '$$sid'] } } },
        { $sort:   { subagent_id: 1, seq: 1 } },
        { $unset:  ['_id', 'session_id', 'account_id', 'host'] },
      ],
      as: 'subagent_lines',
    },
  },
  {
    $lookup: {
      from: 'blobs',
      let:  { sid: '$session_id' },
      pipeline: [
        { $match:  { $expr: { $eq: ['$session_id', '$$sid'] } } },
        { $sort:   { blob_type: 1, name: 1 } },
        { $unset:  ['_id', 'session_id', 'account_id', 'content'] },
      ],
      as: 'blobs',
    },
  },
  {
    $lookup: {
      from: 'hook_events',
      let:  { sid: '$session_id' },
      pipeline: [
        { $match:  { $expr: { $eq: ['$session_id', '$$sid'] } } },
        { $sort:   { created_at: 1 } },
        { $unset:  ['_id', 'session_id', 'account_id'] },
      ],
      as: 'hook_events',
    },
  },
];

export async function createClient({ mongoUrl, dbName }: Pick<Config, 'mongoUrl' | 'dbName'>): Promise<MongoDb> {
  const client = new MongoClient(mongoUrl);
  await client.connect();
  const db = client.db(dbName);

  // IndexKeySpecsConflict (code 86) happens when upgrading from older data with a non-unique index.
  // Warn and continue rather than crash — the index still exists and queries still work.
  const results = await Promise.allSettled([
    db.collection('sessions').createIndex({ session_id: 1 }, { unique: true }),
    db.collection('sessions').createIndex({ git_origin: 1 }),
    db.collection('hook_events').createIndex({ session_id: 1 }),
    db.collection('hook_events').createIndex({ created_at: -1 }),
    db.collection('transcript_lines').createIndex({ session_id: 1, seq: 1 }, { unique: true }),
    db.collection('sessions').createIndex({ account_id: 1, last_seen: -1 }),
    db.collection('sessions').createIndex({ account_id: 1, git_origin: 1 }),
    db.collection('sessions').createIndex({ account_id: 1, git_origin: 1, git_branch: 1 }),
    db.collection('hook_events').createIndex({ account_id: 1, session_id: 1, created_at: -1 }),
    db.collection('transcript_lines').createIndex({ account_id: 1, session_id: 1, seq: 1 }),
    db.collection('subagent_lines').createIndex({ session_id: 1, subagent_id: 1, seq: 1 }, { unique: true }),
    db.collection('subagent_lines').createIndex({ account_id: 1, session_id: 1, subagent_id: 1, seq: 1 }),
    db.collection('blobs').createIndex({ session_id: 1, blob_type: 1, name: 1 }, { unique: true }),
    db.collection('blobs').createIndex({ account_id: 1, session_id: 1, blob_type: 1 }),
  ]);
  for (const r of results) {
    if (r.status === 'rejected') console.error('clued: index warning:', (r.reason as Error).message);
  }

  try {
    await db.createCollection('session_full', { viewOn: 'sessions', pipeline: SESSION_FULL_PIPELINE });
  } catch (e: any) {
    if (e.code === 48) {
      // View exists — update pipeline to pick up any changes
      await db.command({ collMod: 'session_full', viewOn: 'sessions', pipeline: SESSION_FULL_PIPELINE });
    } else {
      throw e;
    }
  }

  return {
    db,
    sessions:        db.collection('sessions'),
    hookEvents:      db.collection('hook_events'),
    transcriptLines: db.collection('transcript_lines'),
    subagentLines:   db.collection('subagent_lines'),
    blobs:           db.collection('blobs'),
    sessionFull:     db.collection('session_full'),
    close:           () => client.close(),
  };
}
