import http from 'http';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { loadConfig }                         from './config.mjs';
import { createClient }                       from './mongo.mjs';
import { tailFile }                           from './tailer.mjs';
import { loadEnrichers, startEnrichmentLoop } from './enricher.mjs';
import { getGitOrigin }                       from './git.mjs';

const ENRICHERS_DIR = join(dirname(fileURLToPath(import.meta.url)), '..', 'enrichers');

const config    = loadConfig();
const mongo     = await createClient(config).catch(err => {
  console.error('clued daemon: MongoDB connection failed:', err.message);
  process.exit(1);
});
const enrichers = await loadEnrichers(ENRICHERS_DIR, config);
const loop      = startEnrichmentLoop(mongo, enrichers);

const tracked = new Map();

function trackSession({ session_id, transcript_path, cwd } = {}) {
  if (!session_id || tracked.has(session_id)) return;
  const seqRef = { value: 0 };
  tracked.set(session_id, seqRef);

  const now = new Date();
  Promise.resolve(cwd ? getGitOrigin(cwd) : null).then(git_origin => {
    const $set = { session_id, transcript_path, cwd, last_seen: now };
    if (git_origin) $set.git_origin = git_origin;
    mongo.sessions.updateOne(
      { session_id },
      { $set, $setOnInsert: { started_at: now } },
      { upsert: true }
    ).catch(() => {});
  });

  if (!transcript_path) return;

  tailFile(transcript_path, raw => {
    let line;
    try { line = JSON.parse(raw); } catch { line = { raw }; }
    const seq = seqRef.value++;
    // Upsert on {session_id, seq} to survive daemon restart + backfill race without duplicates.
    mongo.transcriptLines.updateOne(
      { session_id, seq },
      { $set: { session_id, seq, line }, $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });
}

const server = http.createServer((req, res) => {
  if (req.method === 'GET' && req.url === '/health') {
    res.writeHead(200); res.end('ok'); return;
  }
  if (req.method !== 'POST' || req.url !== '/event') {
    res.writeHead(404); res.end(); return;
  }
  let body = '';
  req.on('data', c => { body += c; });
  req.on('end', async () => {
    try {
      const data = JSON.parse(body);
      trackSession(data);
      if (data.session_id) {
        mongo.sessions.updateOne({ session_id: data.session_id }, { $set: { last_seen: new Date() } }).catch(() => {});
      }
      await mongo.hookEvents.insertOne({ ...data, created_at: new Date() });
      res.writeHead(200); res.end('ok');
    } catch (e) {
      res.writeHead(400); res.end(e.message);
    }
  });
});

server.on('error', async e => {
  if (e.code === 'EADDRINUSE') {
    await mongo.close();
    process.exit(0);
  }
  console.error('clued daemon error:', e.message);
  await mongo.close();
  process.exit(1);
});

server.listen(config.port, '127.0.0.1');

const shutdown = async () => {
  server.close();
  loop.stop();
  await mongo.close();
  process.exit(0);
};

process.on('SIGTERM', shutdown);
process.on('SIGINT',  shutdown);
