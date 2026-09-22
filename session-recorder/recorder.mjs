// Persistent HTTP server (port 8085) that records Claude Code session data to MongoDB.
//
// Invoked by every Claude Code hook with the event JSON on stdin.
//   Normal mode: tries to POST the event to a running server; exits immediately.
//   If no server running: spawns itself as a detached daemon (--server flag), then exits.
//   Daemon mode (--server): starts the HTTP server, tails transcript JSONL files, and
//   inserts all hook events + transcript lines into MongoDB on localhost:27018.

import http from 'http';
import fs from 'fs';
import readline from 'readline';
import { spawn } from 'child_process';
import { MongoClient } from 'mongodb';

const PORT      = 8085;
const MONGO_URL = 'mongodb://localhost:27018';
const DB_NAME   = 'claude_sessions';

async function readStdin() {
  const chunks = [];
  for await (const chunk of process.stdin) chunks.push(chunk);
  return Buffer.concat(chunks).toString().trim();
}

function postToServer(data) {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify(data);
    const req = http.request(
      {
        hostname: '127.0.0.1', port: PORT, path: '/event', method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) },
      },
      res => { res.resume(); res.on('end', resolve); }
    );
    req.setTimeout(2000, () => { req.destroy(); reject(new Error('timeout')); });
    req.on('error', reject);
    req.end(body);
  });
}

// Tails filePath from position 0, calling onLine for each non-empty line.
// Waits for the file to appear, then polls + watches for new content.
function tailFile(filePath, onLine) {
  let pos = 0;

  const read = () => {
    try {
      const size = fs.statSync(filePath).size;
      if (size <= pos) return;
      const rl = readline.createInterface({
        input: fs.createReadStream(filePath, { start: pos, end: size - 1 }),
        crlfDelay: Infinity,
      });
      const batch = [];
      rl.on('line', l => { if (l.trim()) batch.push(l); });
      rl.on('close', () => { pos = size; batch.forEach(onLine); });
    } catch { /* file temporarily unavailable */ }
  };

  const start = () => {
    if (!fs.existsSync(filePath)) { setTimeout(start, 500); return; }
    read();
    try { fs.watch(filePath, read); } catch { /* fall through to poll-only */ }
    setInterval(read, 2000);
  };

  start();
}

async function runServer(initialData) {
  const client = new MongoClient(MONGO_URL);
  await client.connect();
  const db = client.db(DB_NAME);

  const sessions       = db.collection('sessions');
  const hookEvents     = db.collection('hook_events');
  const transcriptLines = db.collection('transcript_lines');

  await Promise.all([
    sessions.createIndex({ session_id: 1 }, { unique: true }),
    hookEvents.createIndex({ session_id: 1 }),
    hookEvents.createIndex({ created_at: -1 }),
    transcriptLines.createIndex({ session_id: 1, seq: 1 }),
  ]);

  const tracked = new Set();

  function trackSession({ session_id, transcript_path, cwd } = {}) {
    if (!session_id || tracked.has(session_id)) return;
    tracked.add(session_id);

    sessions.updateOne(
      { session_id },
      {
        $set:         { session_id, transcript_path, cwd, last_seen: new Date() },
        $setOnInsert: { started_at: new Date() },
      },
      { upsert: true }
    ).catch(() => {});

    if (!transcript_path) return;

    let seq = 0;
    tailFile(transcript_path, raw => {
      let line;
      try { line = JSON.parse(raw); } catch { line = { raw }; }
      transcriptLines.insertOne({ session_id, seq: seq++, line, created_at: new Date() }).catch(() => {});
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
        await hookEvents.insertOne({ ...data, created_at: new Date() });
        res.writeHead(200); res.end('ok');
      } catch (e) {
        res.writeHead(400); res.end(e.message);
      }
    });
  });

  // EADDRINUSE means another daemon won the race — hand off initial data and exit.
  server.on('error', async e => {
    if (e.code === 'EADDRINUSE') {
      if (initialData) await postToServer(initialData).catch(() => {});
      await client.close();
      process.exit(0);
    }
    process.exit(1);
  });

  server.listen(PORT, '127.0.0.1', () => {
    if (initialData) trackSession(initialData);
  });

  const shutdown = () => { server.close(); client.close(); process.exit(0); };
  process.on('SIGTERM', shutdown);
  process.on('SIGINT', shutdown);
}

// ---- Entry point ----

const raw  = await readStdin();
const data = raw ? JSON.parse(raw) : null;

if (process.argv.includes('--server')) {
  await runServer(data);
} else if (data) {
  try {
    await postToServer(data);
    process.exit(0);
  } catch {
    // No server running — spawn detached daemon with our event data on its stdin.
    const child = spawn(process.execPath, [process.argv[1], '--server'], {
      detached: true,
      stdio: ['pipe', 'ignore', 'ignore'],
      env: process.env,
    });
    child.stdin.write(raw);
    child.stdin.end();
    child.unref();
    process.exit(0);
  }
} else {
  process.exit(0);
}
