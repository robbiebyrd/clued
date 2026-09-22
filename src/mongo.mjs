import { MongoClient } from 'mongodb';

export async function createClient({ mongoUrl, dbName }) {
  const client = new MongoClient(mongoUrl);
  await client.connect();
  const db = client.db(dbName);

  await Promise.all([
    db.collection('sessions').createIndex({ session_id: 1 }, { unique: true }),
    db.collection('hook_events').createIndex({ session_id: 1 }),
    db.collection('hook_events').createIndex({ created_at: -1 }),
    db.collection('transcript_lines').createIndex({ session_id: 1, seq: 1 }, { unique: true }),
  ]);

  return {
    db,
    sessions:        db.collection('sessions'),
    hookEvents:      db.collection('hook_events'),
    transcriptLines: db.collection('transcript_lines'),
    close:           () => client.close(),
  };
}
