import { MongoClient } from 'mongodb';

export async function createClient({ mongoUrl, dbName }) {
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
  ]);
  for (const r of results) {
    if (r.status === 'rejected') console.error('clued: index warning:', r.reason.message);
  }

  return {
    db,
    sessions:        db.collection('sessions'),
    hookEvents:      db.collection('hook_events'),
    transcriptLines: db.collection('transcript_lines'),
    close:           () => client.close(),
  };
}
