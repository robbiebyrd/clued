import http from 'http';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { loadConfig }                         from './config';
import { createClient }                       from './mongo';
import { tailFile }                           from './tailer';
import { loadEnrichers, startEnrichmentLoop } from './enricher';
import { getGitOrigin, getGitBranch }          from './git';
import { readAccountId }                       from './account';

const __filename = fileURLToPath(import.meta.url);
const __dir      = dirname(__filename);
// In dev (.ts), enrichers/ is a sibling of src/. In prod (.mjs), compiled enrichers are in dist/enrichers/.
const ENRICHERS_DIR = __filename.endsWith('.ts')
  ? join(__dir, '..', 'enrichers')
  : join(__dir, 'enrichers');

const config     = loadConfig();
const account_id = readAccountId(config.claudeAppConfigPath);
const mongo     = await createClient(config).catch(err => {
  console.error('clued daemon: MongoDB connection failed:', (err as Error).message);
  process.exit(1);
});
const enrichers = await loadEnrichers(ENRICHERS_DIR, config);
const loop      = startEnrichmentLoop(mongo, enrichers);

interface SessionState { gitOriginFound: boolean; }
const tracked = new Map<string, SessionState>();

async function trackSession({ session_id, transcript_path, cwd }: {
  session_id?: string; transcript_path?: string; cwd?: string;
} = {}) {
  if (!session_id) return;

  if (tracked.has(session_id)) {
    const state = tracked.get(session_id)!;
    if (!state.gitOriginFound && cwd) {
      const [gitOrigin, gitBranch] = await Promise.all([getGitOrigin(cwd), getGitBranch(cwd)]);
      if (gitOrigin) {
        state.gitOriginFound = true;
        const $set: Record<string, unknown> = { git_origin: gitOrigin };
        if (gitBranch) $set.git_branch = gitBranch;
        mongo.sessions.updateOne(
          { session_id, git_origin: { $exists: false } },
          { $set }
        ).catch(() => {});
      }
    }
    return;
  }

  const seqRef = { value: 0 };
  const state: SessionState = { gitOriginFound: false };
  tracked.set(session_id, state);

  const now = new Date();
  Promise.all([
    cwd ? getGitOrigin(cwd) : Promise.resolve(null),
    cwd ? getGitBranch(cwd) : Promise.resolve(null),
  ]).then(([git_origin, git_branch]) => {
    if (git_origin) state.gitOriginFound = true;
    const $set: Record<string, unknown> = { session_id, transcript_path, cwd, last_seen: now, account_id };
    if (git_origin) $set.git_origin = git_origin;
    if (git_branch) $set.git_branch = git_branch;
    mongo.sessions.updateOne(
      { session_id },
      { $set, $setOnInsert: { started_at: now } },
      { upsert: true }
    ).catch(() => {});
  });

  if (!transcript_path) return;

  tailFile(transcript_path, raw => {
    let line: unknown;
    try { line = JSON.parse(raw); } catch { line = { raw }; }
    const seq = seqRef.value++;
    // Upsert on {session_id, seq} to survive daemon restart + backfill race without duplicates.
    mongo.transcriptLines.updateOne(
      { session_id, seq },
      { $set: { session_id, seq, line, account_id }, $setOnInsert: { created_at: new Date() } },
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
  req.on('data', (c: Buffer) => { body += c; });
  req.on('end', async () => {
    try {
      const data = JSON.parse(body) as { session_id?: string; transcript_path?: string; cwd?: string };
      trackSession(data);
      if (data.session_id) {
        mongo.sessions.updateOne({ session_id: data.session_id, account_id }, { $set: { last_seen: new Date() } }).catch(() => {});
      }
      await mongo.hookEvents.insertOne({ ...data, account_id, created_at: new Date() });
      res.writeHead(200); res.end('ok');
    } catch (e) {
      res.writeHead(400); res.end((e as Error).message);
    }
  });
});

server.on('error', async (e: NodeJS.ErrnoException) => {
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
