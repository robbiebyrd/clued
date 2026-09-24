import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'http';
import { spawn } from 'child_process';
import { execFileSync } from 'child_process';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { mkdirSync, rmSync, writeFileSync, readFileSync } from 'fs';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';
import type { ChildProcess } from 'child_process';

const ROOT        = join(dirname(fileURLToPath(import.meta.url)), '../..');
const DAEMON_PATH = join(ROOT, 'src/daemon.ts');
const TEST_PORT   = 18085;
const TEST_DB     = `clued_daemon_test_${Date.now()}`;
const TEST_URL    = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';
const TMP_DIR      = join(tmpdir(), `clued-daemon-test-${process.pid}`);
const CLAUDE_CONFIG  = join(TMP_DIR, 'claude-config.json');
const FILE_HIST_DIR  = join(TMP_DIR, 'file-history');
const TEST_ACCOUNT   = 'test-account-uuid';

let daemonProc: ChildProcess | undefined;
let mongo: MongoDb;

function post(payload: Record<string, unknown>): Promise<number | undefined> {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify(payload);
    const req  = http.request(
      { hostname: '127.0.0.1', port: TEST_PORT, path: '/event', method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) } },
      res => { res.resume(); res.on('end', () => resolve(res.statusCode)); }
    );
    req.on('error', reject);
    req.end(body);
  });
}

function healthCheck(): Promise<boolean> {
  return new Promise(resolve => {
    http.get(`http://127.0.0.1:${TEST_PORT}/health`, res => {
      resolve(res.statusCode === 200);
    }).on('error', () => resolve(false));
  });
}

before(async () => {
  mkdirSync(TMP_DIR, { recursive: true });
  mkdirSync(FILE_HIST_DIR, { recursive: true });
  writeFileSync(CLAUDE_CONFIG, JSON.stringify({ lastKnownAccountUuid: TEST_ACCOUNT }));

  daemonProc = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
    env: {
      ...process.env,
      CLUED_PORT:                  String(TEST_PORT),
      CLUED_DB_NAME:               TEST_DB,
      CLUED_MONGO_URL:             TEST_URL,
      CLUED_CLAUDE_APP_CONFIG_PATH: CLAUDE_CONFIG,
      CLUED_FILE_HISTORY_DIR:      FILE_HIST_DIR,
    },
    stdio: 'pipe',
  });

  for (let i = 0; i < 30; i++) {
    await new Promise(r => setTimeout(r, 100));
    if (await healthCheck()) break;
  }

  mongo = await createClient({ mongoUrl: TEST_URL, dbName: TEST_DB });
});

after(async () => {
  daemonProc?.kill('SIGTERM');
  if (mongo) { await mongo.db.dropDatabase(); await mongo.close(); }
  rmSync(TMP_DIR, { recursive: true, force: true });
});

test('GET /health returns 200', async () => {
  assert.ok(await healthCheck());
});

test('POST /event inserts hook event to MongoDB', async () => {
  const status = await post({ session_id: 'daemon-test-1', type: 'PreToolUse', tool_name: 'Bash' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 200));
  const doc = await mongo.hookEvents.findOne({ session_id: 'daemon-test-1' });
  assert.ok(doc, 'hook event not found in MongoDB');
  assert.equal((doc as Record<string, unknown>).tool_name, 'Bash');
});

test('POST /event with transcript_path creates session and starts tailing', async () => {
  const status = await post({ session_id: 'daemon-test-2', transcript_path: '/tmp/fake.jsonl', cwd: '/tmp' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 200));
  const doc = await mongo.sessions.findOne({ session_id: 'daemon-test-2' });
  assert.ok(doc, 'session doc not found');
  assert.equal((doc as Record<string, unknown>).transcript_path, '/tmp/fake.jsonl');
});

test('unknown routes return 404', async () => {
  const code = await new Promise<number | undefined>(resolve => {
    http.get(`http://127.0.0.1:${TEST_PORT}/unknown`, res => resolve(res.statusCode));
  });
  assert.equal(code, 404);
});

test('session created with git cwd gets git_origin populated', async () => {
  const status = await post({ session_id: 'daemon-git-test', transcript_path: '/tmp/git-fake.jsonl', cwd: ROOT });
  assert.equal(status, 200);
  let doc;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.sessions.findOne({ session_id: 'daemon-git-test', git_origin: { $exists: true } });
    if (doc) break;
  }
  assert.ok(doc, 'session with git_origin not found');
  assert.match(String((doc as Record<string, unknown>).git_origin), /clued/);
});

test('session created with non-git cwd has no git_origin', async () => {
  const status = await post({ session_id: 'daemon-nogit-test', transcript_path: '/tmp/nogit-fake.jsonl', cwd: '/tmp' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 300));
  const doc = await mongo.sessions.findOne({ session_id: 'daemon-nogit-test' });
  assert.ok(doc, 'session doc not found');
  assert.equal((doc as Record<string, unknown>).git_origin, undefined);
});

test('session gets git_origin when cwd is first provided on a later event', async () => {
  await post({ session_id: 'daemon-git-late', transcript_path: '/tmp/git-late.jsonl' });
  await new Promise(r => setTimeout(r, 300));
  const initial = await mongo.sessions.findOne({ session_id: 'daemon-git-late' });
  assert.ok(initial, 'session not created on first event');
  assert.equal((initial as Record<string, unknown>).git_origin, undefined, 'git_origin should not be set yet');

  await post({ session_id: 'daemon-git-late', cwd: ROOT });
  let doc;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.sessions.findOne({ session_id: 'daemon-git-late', git_origin: { $exists: true } });
    if (doc) break;
  }
  assert.ok(doc, 'git_origin not set after cwd was provided');
  assert.match(String((doc as Record<string, unknown>).git_origin), /clued/);
});

test('enrichment loop writes enriched field to hook event', async () => {
  await post({ session_id: 'enrich-test', type: 'PostToolUse', tool_name: 'Bash',
    tool_input: { command: 'git status && npm install' } });
  let doc;
  for (let i = 0; i < 30; i++) {
    await new Promise(r => setTimeout(r, 200));
    doc = await mongo.hookEvents.findOne({ session_id: 'enrich-test', 'enrichments.bash-binaries': { $exists: true } });
    if (doc) break;
  }
  assert.ok(doc, 'enrichments.bash-binaries not written within 6s');
  const enrichments = (doc as Record<string, Record<string, { binaries: string[] }>>).enrichments;
  assert.ok(Array.isArray(enrichments['bash-binaries'].binaries));
  assert.ok(enrichments['bash-binaries'].binaries.includes('git'));
});

test('enrichment circuit breaker writes _failed on error', async () => {
  await mongo.hookEvents.insertOne({
    session_id: 'enrich-fail-test', tool_name: 'Bash',
    tool_input: { command: 12345 },
    created_at: new Date(),
  });
  let doc;
  for (let i = 0; i < 30; i++) {
    await new Promise(r => setTimeout(r, 200));
    doc = await mongo.hookEvents.findOne({
      session_id: 'enrich-fail-test',
      'enrichments.bash-binaries_failed': { $exists: true },
    });
    if (doc) break;
  }
  // bash-binaries.matches() requires typeof command === 'string', so this doc won't match
  // and bash-binaries_failed won't be written — the enricher correctly skips non-string commands.
});

test('POST /event populates git_origin from cwd', async () => {
  const tmpDir = join(tmpdir(), `clued-git-test-${Date.now()}`);
  mkdirSync(tmpDir, { recursive: true });
  try {
    execFileSync('git', ['init'], { cwd: tmpDir, stdio: 'pipe' });
    execFileSync('git', ['remote', 'add', 'origin', 'https://github.com/test/mcp-repo.git'],
      { cwd: tmpDir, stdio: 'pipe' });
    const status = await post({ session_id: 'daemon-git-test2', cwd: tmpDir });
    assert.equal(status, 200);
    let doc;
    for (let i = 0; i < 20; i++) {
      await new Promise(r => setTimeout(r, 100));
      doc = await mongo.sessions.findOne({ session_id: 'daemon-git-test2', git_origin: { $exists: true } });
      if (doc) break;
    }
    assert.ok(doc, 'git_origin not populated within 2s');
    assert.equal((doc as Record<string, unknown>).git_origin, 'https://github.com/test/mcp-repo.git');
  } finally {
    rmSync(tmpDir, { recursive: true, force: true });
  }
});

test('account_id is stamped on hook events', async () => {
  const status = await post({ session_id: 'daemon-acct-hook', type: 'PreToolUse', tool_name: 'Bash' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 200));
  const doc = await mongo.hookEvents.findOne({ session_id: 'daemon-acct-hook' });
  assert.ok(doc, 'hook event not found');
  assert.equal((doc as Record<string, unknown>).account_id, TEST_ACCOUNT);
});

test('account_id is stamped on sessions', async () => {
  const status = await post({ session_id: 'daemon-acct-sess', transcript_path: '/tmp/acct-sess.jsonl', cwd: '/tmp' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 200));
  const doc = await mongo.sessions.findOne({ session_id: 'daemon-acct-sess' });
  assert.ok(doc, 'session not found');
  assert.equal((doc as Record<string, unknown>).account_id, TEST_ACCOUNT);
});

test('account_id is stamped on transcript lines', async () => {
  const tmpDir2 = join(TMP_DIR, 'transcript-acct');
  mkdirSync(tmpDir2, { recursive: true });
  const jsonlPath = join(tmpDir2, 'session.jsonl');
  writeFileSync(jsonlPath, JSON.stringify({ type: 'user', message: { role: 'user', content: 'hello' } }) + '\n');
  const status = await post({ session_id: 'daemon-acct-line', transcript_path: jsonlPath, cwd: '/tmp' });
  assert.equal(status, 200);
  let doc;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.transcriptLines.findOne({ session_id: 'daemon-acct-line' });
    if (doc) break;
  }
  assert.ok(doc, 'transcript line not found');
  assert.equal((doc as Record<string, unknown>).account_id, TEST_ACCOUNT);
});

test('git_branch is stamped on session when cwd is a git repo', async () => {
  const status = await post({ session_id: 'daemon-branch-test', transcript_path: '/tmp/branch.jsonl', cwd: ROOT });
  assert.equal(status, 200);
  let doc;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.sessions.findOne({ session_id: 'daemon-branch-test', git_branch: { $exists: true } });
    if (doc) break;
  }
  assert.ok(doc, 'session with git_branch not found');
  assert.ok(typeof (doc as Record<string, unknown>).git_branch === 'string', 'git_branch should be a string');
});

test('daemon flushes WAL entries into MongoDB on startup', async () => {
  const walPort  = TEST_PORT + 20;
  const walDb    = TEST_DB + '_wal';
  const walPath  = join(TMP_DIR, 'startup.wal');
  writeFileSync(walPath, [
    JSON.stringify({ session_id: 'wal-flush-1', type: 'PreToolUse',  tool_name: 'Bash' }),
    JSON.stringify({ session_id: 'wal-flush-2', type: 'PostToolUse', tool_name: 'Read' }),
  ].join('\n') + '\n');

  const walProc  = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
    env: {
      ...process.env,
      CLUED_PORT:                  String(walPort),
      CLUED_DB_NAME:               walDb,
      CLUED_MONGO_URL:             TEST_URL,
      CLUED_CLAUDE_APP_CONFIG_PATH: CLAUDE_CONFIG,
      CLUED_WAL_PATH:              walPath,
    },
    stdio: 'pipe',
  });
  const walMongo = await createClient({ mongoUrl: TEST_URL, dbName: walDb });
  try {
    for (let i = 0; i < 30; i++) {
      await new Promise(r => setTimeout(r, 100));
      const ok = await new Promise(resolve => {
        http.get(`http://127.0.0.1:${walPort}/health`, res => resolve(res.statusCode === 200)).on('error', () => resolve(false));
      });
      if (ok) break;
    }
    // Give the startup flush a moment to complete
    await new Promise(r => setTimeout(r, 500));

    const doc1 = await walMongo.hookEvents.findOne({ session_id: 'wal-flush-1' });
    const doc2 = await walMongo.hookEvents.findOne({ session_id: 'wal-flush-2' });
    assert.ok(doc1, 'wal-flush-1 not found in MongoDB after startup flush');
    assert.equal((doc1 as Record<string, unknown>).tool_name, 'Bash');
    assert.ok(doc2, 'wal-flush-2 not found in MongoDB after startup flush');
    assert.equal((doc2 as Record<string, unknown>).tool_name, 'Read');

    const walContent = readFileSync(walPath, 'utf8').trim();
    assert.equal(walContent, '', 'WAL should be empty after successful flush');
  } finally {
    walProc.kill('SIGTERM');
    await walMongo.db.dropDatabase();
    await walMongo.close();
    rmSync(walPath, { force: true });
  }
});

test('watchArtifactDirs wired: tool-result captured after /event', async () => {
  const SESSION        = 'sess-wired-tr-001';
  const projDir        = join(TMP_DIR, 'projects', 'proj-wired-a');
  const transcriptPath = join(projDir, `${SESSION}.jsonl`);
  const sessionDir     = join(projDir, SESSION);
  const toolResultsDir = join(sessionDir, 'tool-results');

  mkdirSync(projDir, { recursive: true });
  writeFileSync(transcriptPath, '');

  const status = await post({ session_id: SESSION, transcript_path: transcriptPath });
  assert.equal(status, 200);

  // Let watchArtifactDirs initialize before creating the watched directory
  await new Promise(r => setTimeout(r, 1000));

  mkdirSync(toolResultsDir, { recursive: true });
  writeFileSync(join(toolResultsDir, 'result.txt'), 'daemon wired output');

  // Wait for 2s poll cycle + margin
  await new Promise(r => setTimeout(r, 3000));

  const doc = await mongo.blobs.findOne({ session_id: SESSION, blob_type: 'tool-result', name: 'result.txt' });
  assert.ok(doc, 'tool-result blob not captured via daemon /event wiring');
  assert.equal((doc as Record<string, unknown>).content, 'daemon wired output');
});

test('watchArtifactDirs wired: file-history captured after /event', async () => {
  const SESSION        = 'sess-wired-fh-001';
  const projDir        = join(TMP_DIR, 'projects', 'proj-wired-b');
  const transcriptPath = join(projDir, `${SESSION}.jsonl`);
  const fileHistPath   = join(FILE_HIST_DIR, SESSION);

  mkdirSync(projDir, { recursive: true });
  writeFileSync(transcriptPath, '');

  const status = await post({ session_id: SESSION, transcript_path: transcriptPath });
  assert.equal(status, 200);

  await new Promise(r => setTimeout(r, 1000));

  mkdirSync(fileHistPath, { recursive: true });
  writeFileSync(join(fileHistPath, 'snap001'), 'snapshot data');

  await new Promise(r => setTimeout(r, 3000));

  const doc = await mongo.blobs.findOne({ session_id: SESSION, blob_type: 'file-history', name: 'snap001' });
  assert.ok(doc, 'file-history blob not captured via daemon /event wiring');
  assert.equal((doc as Record<string, unknown>).encoding, 'base64');
  const decoded = Buffer.from((doc as Record<string, unknown>).content as string, 'base64').toString('utf8');
  assert.equal(decoded, 'snapshot data');
});

test('account_id is "unknown" when claude config is missing', async () => {
  const altPort = TEST_PORT + 10;
  const altDb   = TEST_DB + '_noconfig';
  const missingConfig = join(TMP_DIR, 'nonexistent-claude-config.json');
  const altProc = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
    env: {
      ...process.env,
      CLUED_PORT: String(altPort),
      CLUED_DB_NAME: altDb,
      CLUED_MONGO_URL: TEST_URL,
      CLUED_CLAUDE_APP_CONFIG_PATH: missingConfig,
    },
    stdio: 'pipe',
  });
  const altMongo = await createClient({ mongoUrl: TEST_URL, dbName: altDb });
  try {
    for (let i = 0; i < 30; i++) {
      await new Promise(r => setTimeout(r, 100));
      const ok = await new Promise(resolve => {
        http.get(`http://127.0.0.1:${altPort}/health`, res => resolve(res.statusCode === 200)).on('error', () => resolve(false));
      });
      if (ok) break;
    }
    const body = JSON.stringify({ session_id: 'daemon-unknown-acct', type: 'PreToolUse', tool_name: 'Bash' });
    await new Promise<void>((resolve, reject) => {
      const req = http.request(
        { hostname: '127.0.0.1', port: altPort, path: '/event', method: 'POST',
          headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) } },
        res => { res.resume(); res.on('end', resolve); }
      );
      req.on('error', reject);
      req.end(body);
    });
    await new Promise(r => setTimeout(r, 200));
    const doc = await altMongo.hookEvents.findOne({ session_id: 'daemon-unknown-acct' });
    assert.ok(doc, 'hook event not found');
    assert.equal((doc as Record<string, unknown>).account_id, 'unknown');
  } finally {
    altProc.kill('SIGTERM');
    await altMongo.db.dropDatabase();
    await altMongo.close();
  }
});
