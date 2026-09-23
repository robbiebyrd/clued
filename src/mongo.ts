import { MongoClient, Collection, Db } from 'mongodb';
import type { Config } from './config';

export interface MongoDb {
  db:              Db;
  sessions:        Collection;
  hookEvents:      Collection;
  transcriptLines: Collection;
  close:           () => Promise<void>;
}

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
  ]);
  for (const r of results) {
    if (r.status === 'rejected') console.error('clued: index warning:', (r.reason as Error).message);
  }

  return {
    db,
    sessions:        db.collection('sessions'),
    hookEvents:      db.collection('hook_events'),
    transcriptLines: db.collection('transcript_lines'),
    close:           () => client.close(),
  };
}
