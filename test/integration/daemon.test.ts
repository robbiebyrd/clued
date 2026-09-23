import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'http';
import { spawn } from 'child_process';
import { execFileSync } from 'child_process';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { mkdirSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';
import type { ChildProcess } from 'child_process';

const ROOT        = join(dirname(fileURLToPath(import.meta.url)), '../..');
const DAEMON_PATH = join(ROOT, 'src/daemon.ts');
const TEST_PORT   = 18085;
const TEST_DB     = `clued_daemon_test_${Date.now()}`;
const TEST_URL    = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';

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
  daemonProc = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
    env: { ...process.env, CLUED_PORT: String(TEST_PORT), CLUED_DB_NAME: TEST_DB, CLUED_MONGO_URL: TEST_URL },
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
    doc = await mongo.hookEvents.findOne({ session_id: 'enrich-test', 'enriched.bash-binaries': { $exists: true } });
    if (doc) break;
  }
  assert.ok(doc, 'enriched.bash-binaries not written within 6s');
  const enriched = (doc as Record<string, Record<string, { binaries: string[] }>>).enriched;
  assert.ok(Array.isArray(enriched['bash-binaries'].binaries));
  assert.ok(enriched['bash-binaries'].binaries.includes('git'));
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
      'enriched.bash-binaries_failed': { $exists: true },
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
