# MCP Search Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose a persistent MCP HTTP+SSE server (`src/mcp.mjs`) that lets Claude Code autonomously search past sessions by project, git origin, and command history across machines.

**Architecture:** A standalone Node.js ESM process sharing `loadConfig()` and `createClient()` with the daemon, listening on port 8086, implementing MCP HTTP+SSE transport (`GET /sse` → `endpoint` event → `POST /message?sessionId`). Four tools: `find_sessions`, `get_session_context`, `search_commands`, `read_transcript`. The `session-start` hook is updated to independently health-check and self-heal both the daemon and the MCP server.

**Tech Stack:** Node.js built-in `http`, `crypto.randomUUID`, `node:test`, MongoDB 6.x driver (already a dep).

---

## File Map

| Path | Action | Responsibility |
|---|---|---|
| `src/config.mjs` | Modify | Add `mcpPort: 8086` default and `CLUED_MCP_PORT` env override |
| `src/mongo.mjs` | Modify | Add `{ git_origin: 1 }` index on `sessions` |
| `src/daemon.mjs` | Modify | Extract `git_origin` via `git remote get-url origin` when tracking a session |
| `src/backfill.mjs` | Modify | Same `git_origin` extraction when processing sessions |
| `src/mcp.mjs` | Create | MCP HTTP+SSE server — routing, SSE lifecycle, all four tools |
| `hooks/session-start` | Modify | Add independent health-check + self-heal for MCP server |
| `skills/clued-setup.md` | Modify | Add MCP server registration step |
| `test/config.test.mjs` | Modify | Add `mcpPort` default and `CLUED_MCP_PORT` override tests |
| `test/integration/daemon.test.mjs` | Modify | Add `git_origin` population test |
| `test/integration/mcp.test.mjs` | Create | Full integration test suite for all MCP tools |

---

## Task 1: Add `mcpPort` to config

**Files:**
- Modify: `src/config.mjs`
- Modify: `test/config.test.mjs`

- [ ] **Step 1: Write two failing tests in `test/config.test.mjs`**

Update `ENV_KEYS` array to include `CLUED_MCP_PORT`, then append two new `test()` calls at the end of the file:

```js
// Update this line:
const ENV_KEYS = ['CLUED_MONGO_URL', 'CLUED_DB_NAME', 'CLUED_PORT', 'CLUED_MCP_PORT', 'CLUED_PROJECTS_DIR'];

// Append at end of file:
test('mcpPort defaults to 8086', () => {
  cleanEnv(() => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.mcpPort, 8086);
  });
});

test('CLUED_MCP_PORT env var overrides mcpPort', () => {
  withEnv({ CLUED_MCP_PORT: '9086', CLUED_MONGO_URL: undefined, CLUED_DB_NAME: undefined,
            CLUED_PORT: undefined, CLUED_PROJECTS_DIR: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.mcpPort, 9086);
  });
});
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
node --test test/config.test.mjs
```

Expected: last two tests fail with `cfg.mcpPort is undefined` or assertion error.

- [ ] **Step 3: Update `src/config.mjs`**

Add `mcpPort: 8086` to `DEFAULTS` and the `CLUED_MCP_PORT` env override after `CLUED_PORT`:

```js
const DEFAULTS = {
  mongoUrl:          'mongodb://localhost:27018',
  dbName:            'claude_sessions',
  port:              8085,
  mcpPort:           8086,
  projectsDir:       join(homedir(), '.claude', 'projects'),
  disabledEnrichers: [],
};

// Inside loadConfig(), after the CLUED_PORT line:
if (process.env.CLUED_MCP_PORT) cfg.mcpPort = parseInt(process.env.CLUED_MCP_PORT, 10);
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
node --test test/config.test.mjs
```

Expected: all 7 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/config.mjs test/config.test.mjs
git commit -m "feat: add mcpPort config field with CLUED_MCP_PORT env override"
```

---

## Task 2: Add `git_origin` index and daemon population

`git_origin` is the primary cross-machine session identifier. It must be stored on session documents and indexed for efficient `find_sessions` and `search_commands` queries.

**Files:**
- Modify: `src/mongo.mjs`
- Modify: `src/daemon.mjs`
- Modify: `test/integration/daemon.test.mjs`

- [ ] **Step 1: Write failing test in `test/integration/daemon.test.mjs`**

Add these imports at the top of the file (alongside existing imports):

```js
import { mkdirSync, rmSync } from 'fs';
import { tmpdir } from 'os';
import { execSync } from 'child_process';
```

Append this test at the end of the file:

```js
test('POST /event populates git_origin from cwd', async () => {
  const tmpDir = join(tmpdir(), `clued-git-test-${Date.now()}`);
  mkdirSync(tmpDir, { recursive: true });
  try {
    execSync('git init && git remote add origin https://github.com/test/mcp-repo.git',
      { cwd: tmpDir, stdio: 'pipe' });
    const status = await post({ session_id: 'daemon-git-test', cwd: tmpDir });
    assert.equal(status, 200);
    let doc;
    for (let i = 0; i < 20; i++) {
      await new Promise(r => setTimeout(r, 100));
      doc = await mongo.sessions.findOne({ session_id: 'daemon-git-test', git_origin: { $exists: true } });
      if (doc) break;
    }
    assert.ok(doc, 'git_origin not populated within 2s');
    assert.equal(doc.git_origin, 'https://github.com/test/mcp-repo.git');
  } finally {
    rmSync(tmpDir, { recursive: true, force: true });
  }
});
```

- [ ] **Step 2: Run integration tests to verify the new test fails**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/daemon.test.mjs
```

Expected: last test fails (no `git_origin` on session doc).

- [ ] **Step 3: Add `git_origin` index to `src/mongo.mjs`**

In `createClient()`, add one line to the `Promise.allSettled` block after the first sessions index:

```js
const results = await Promise.allSettled([
  db.collection('sessions').createIndex({ session_id: 1 }, { unique: true }),
  db.collection('sessions').createIndex({ git_origin: 1 }),          // NEW
  db.collection('hook_events').createIndex({ session_id: 1 }),
  db.collection('hook_events').createIndex({ created_at: -1 }),
  db.collection('transcript_lines').createIndex({ session_id: 1, seq: 1 }, { unique: true }),
]);
```

- [ ] **Step 4: Update `src/daemon.mjs` to extract and store `git_origin`**

Add these two imports after the existing imports at the top of `daemon.mjs`:

```js
import { execFile } from 'child_process';
import { promisify } from 'util';
```

Add this function before `trackSession`:

```js
const execFileAsync = promisify(execFile);

async function getGitOrigin(cwd) {
  try {
    const { stdout } = await execFileAsync(
      'git', ['-C', cwd, 'remote', 'get-url', 'origin'],
      { timeout: 2000 }
    );
    return stdout.trim() || null;
  } catch {
    return null;
  }
}
```

Replace the body of `trackSession` with:

```js
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
    mongo.transcriptLines.updateOne(
      { session_id, seq },
      { $set: { session_id, seq, line }, $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });
}
```

- [ ] **Step 5: Run integration tests to verify all pass**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/daemon.test.mjs
```

Expected: all 7 tests pass (including the new `git_origin` test).

- [ ] **Step 6: Commit**

```bash
git add src/mongo.mjs src/daemon.mjs test/integration/daemon.test.mjs
git commit -m "feat: add git_origin index and daemon extraction from session cwd"
```

---

## Task 3: Add `git_origin` population to backfill

**Files:**
- Modify: `src/backfill.mjs`

No new tests needed — the `git_origin` behavior is covered by the MCP integration tests (which seed sessions with `git_origin` directly). Backfill's correctness is verified by existing backfill tests.

- [ ] **Step 1: Add imports to `src/backfill.mjs`**

Add these two imports after the existing imports:

```js
import { execFile } from 'child_process';
import { promisify } from 'util';

const execFileAsync = promisify(execFile);
```

- [ ] **Step 2: Update `processSession` in `src/backfill.mjs`**

Replace the current `processSession` signature + first `updateOne` call with:

```js
async function processSession(mongo, projectPath, sessionId, filePath) {
  const now = new Date();

  let git_origin = null;
  try {
    const { stdout } = await execFileAsync(
      'git', ['-C', projectPath, 'remote', 'get-url', 'origin'],
      { timeout: 2000 }
    );
    git_origin = stdout.trim() || null;
  } catch { /* not a git repo or git unavailable */ }

  const $set = { session_id: sessionId, project_path: projectPath, transcript_path: filePath, last_seen: now };
  if (git_origin) $set.git_origin = git_origin;

  await mongo.sessions.updateOne(
    { session_id: sessionId },
    { $set, $setOnInsert: { started_at: now } },
    { upsert: true }
  );

  // rest of function is unchanged — content/rawLines/ops/bulkWrite
```

Keep the rest of `processSession` (content read, rawLines, bulkWrite) exactly as-is.

- [ ] **Step 3: Run unit tests to confirm nothing broken**

```bash
node --test 'test/*.test.mjs'
```

Expected: all unit tests pass.

- [ ] **Step 4: Commit**

```bash
git add src/backfill.mjs
git commit -m "feat: extract git_origin during backfill via git remote get-url origin"
```

---

## Task 4: MCP server scaffold — HTTP, SSE, health

Create the server with health endpoint, SSE lifecycle, and request routing. No tool implementations yet.

**Files:**
- Create: `src/mcp.mjs`
- Create: `test/integration/mcp.test.mjs`

- [ ] **Step 1: Create `test/integration/mcp.test.mjs` with scaffold and health + SSE tests**

```js
import { test, before, after } from 'node:test';
import assert from 'node:assert/strict';
import http from 'http';
import { EventEmitter } from 'events';
import { spawn } from 'child_process';
import { join, dirname } from 'path';
import { fileURLToPath } from 'url';
import { createClient } from '../../src/mongo.mjs';

const ROOT      = join(dirname(fileURLToPath(import.meta.url)), '../..');
const MCP_PATH  = join(ROOT, 'src/mcp.mjs');
const TEST_PORT = 18086;
const TEST_DB   = `clued_mcp_test_${Date.now()}`;
const TEST_URL  = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';

let mcpProc, mongo;

// --- helpers ---

function healthCheck() {
  return new Promise(resolve => {
    http.get(`http://127.0.0.1:${TEST_PORT}/health`, res => resolve(res.statusCode === 200))
      .on('error', () => resolve(false));
  });
}

function openSSE() {
  return new Promise((resolve, reject) => {
    const emitter = new EventEmitter();
    let buf = '';
    const req = http.request(
      { hostname: '127.0.0.1', port: TEST_PORT, path: '/sse' },
      res => {
        res.setEncoding('utf8');
        res.on('data', chunk => {
          buf += chunk;
          const blocks = buf.split('\n\n');
          buf = blocks.pop();
          for (const block of blocks) {
            if (!block.trim()) continue;
            const msg = {};
            for (const line of block.split('\n')) {
              const i = line.indexOf(':');
              if (i < 0) continue;
              msg[line.slice(0, i).trim()] = line.slice(i + 1).trim();
            }
            emitter.emit('message', msg);
          }
        });
      }
    );
    req.on('error', reject);
    req.end();
    emitter.once('message', msg => {
      if (msg.event === 'endpoint') resolve({ endpoint: msg.data, emitter, close: () => req.destroy() });
      else reject(new Error(`expected endpoint event, got: ${JSON.stringify(msg)}`));
    });
  });
}

function callTool(endpoint, id, name, args, meta) {
  return new Promise((resolve, reject) => {
    const body = JSON.stringify({
      jsonrpc: '2.0', id, method: 'tools/call',
      params: { name, arguments: args, ...(meta ? { _meta: meta } : {}) },
    });
    const url = new URL(endpoint);
    const req = http.request({
      hostname: url.hostname, port: Number(url.port),
      path: `${url.pathname}${url.search}`, method: 'POST',
      headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) },
    }, res => { res.resume(); resolve(res.statusCode); });
    req.on('error', reject);
    req.end(body);
  });
}

function collectUntilResult(emitter, id, timeoutMs = 5000) {
  return new Promise((resolve, reject) => {
    const events = [];
    const timer = setTimeout(() => reject(new Error('timeout waiting for result')), timeoutMs);
    function handler(msg) {
      if (!msg.data) return;
      let parsed;
      try { parsed = JSON.parse(msg.data); } catch { return; }
      events.push(parsed);
      if (parsed.id === id) {
        clearTimeout(timer);
        emitter.off('message', handler);
        resolve(events);
      }
    }
    emitter.on('message', handler);
  });
}

// --- lifecycle ---

before(async () => {
  mcpProc = spawn(process.execPath, [MCP_PATH], {
    env: { ...process.env, CLUED_MCP_PORT: String(TEST_PORT), CLUED_DB_NAME: TEST_DB, CLUED_MONGO_URL: TEST_URL },
    stdio: 'pipe',
  });
  for (let i = 0; i < 30; i++) {
    await new Promise(r => setTimeout(r, 100));
    if (await healthCheck()) break;
  }
  mongo = await createClient({ mongoUrl: TEST_URL, dbName: TEST_DB });
});

after(async () => {
  mcpProc?.kill('SIGTERM');
  if (mongo) { await mongo.db.dropDatabase(); await mongo.close(); }
});

// --- tests ---

test('GET /health returns 200', async () => {
  assert.ok(await healthCheck());
});

test('GET /sse establishes connection and receives endpoint event', async () => {
  const { endpoint, close } = await openSSE();
  assert.match(endpoint, /^http:\/\/127\.0\.0\.1:\d+\/message\?sessionId=[\w-]+$/);
  close();
});
```

- [ ] **Step 2: Run to verify tests fail**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: process errors (cannot find module `src/mcp.mjs`).

- [ ] **Step 3: Create `src/mcp.mjs` with scaffold only**

```js
import http from 'http';
import { randomUUID } from 'crypto';
import { loadConfig } from './config.mjs';
import { createClient } from './mongo.mjs';

const config = loadConfig();
const mongo  = await createClient(config).catch(err => {
  console.error('clued mcp: MongoDB connection failed:', err.message);
  process.exit(1);
});

const sessions = new Map();

function sseWrite(res, data) {
  res.write(`data: ${JSON.stringify(data)}\n\n`);
}

async function handleToolCall(name, args, meta, sseRes) {
  throw new Error(`unknown tool: ${name}`);
}

const server = http.createServer(async (req, res) => {
  if (req.method === 'GET' && req.url === '/health') {
    res.writeHead(200); res.end('ok'); return;
  }

  if (req.method === 'GET' && req.url === '/sse') {
    const sessionId = randomUUID();
    res.writeHead(200, {
      'Content-Type':  'text/event-stream',
      'Cache-Control': 'no-cache',
      'Connection':    'keep-alive',
    });
    res.write(`event: endpoint\ndata: http://127.0.0.1:${config.mcpPort}/message?sessionId=${sessionId}\n\n`);
    sessions.set(sessionId, res);
    req.on('close', () => sessions.delete(sessionId));
    return;
  }

  if (req.method === 'POST' && req.url?.startsWith('/message')) {
    const sessionId = new URL(req.url, 'http://x').searchParams.get('sessionId');
    const sseRes = sessions.get(sessionId);
    if (!sseRes) { res.writeHead(400); res.end('unknown session'); return; }

    let body = '';
    req.on('data', c => { body += c; });
    req.on('end', async () => {
      let rpc;
      try { rpc = JSON.parse(body); } catch {
        res.writeHead(400); res.end('invalid json'); return;
      }
      res.writeHead(202); res.end();

      const { id, method, params = {} } = rpc;
      try {
        if (method !== 'tools/call') {
          sseWrite(sseRes, { jsonrpc: '2.0', id, result: {} }); return;
        }
        const result = await handleToolCall(params.name, params.arguments || {}, params._meta || {}, sseRes);
        sseWrite(sseRes, {
          jsonrpc: '2.0', id,
          result: { content: [{ type: 'text', text: JSON.stringify(result) }] },
        });
      } catch (e) {
        sseWrite(sseRes, { jsonrpc: '2.0', id, error: { code: -32000, message: e.message } });
      }
    });
    return;
  }

  res.writeHead(404); res.end();
});

server.on('error', async e => {
  if (e.code === 'EADDRINUSE') { await mongo.close(); process.exit(0); }
  console.error('clued mcp error:', e.message);
  await mongo.close(); process.exit(1);
});

server.listen(config.mcpPort, '127.0.0.1');

const shutdown = async () => { server.close(); await mongo.close(); process.exit(0); };
process.on('SIGTERM', shutdown);
process.on('SIGINT', shutdown);
```

- [ ] **Step 4: Run tests to verify health and SSE tests pass**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: both tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/mcp.mjs test/integration/mcp.test.mjs
git commit -m "feat: add MCP server scaffold with health and SSE transport"
```

---

## Task 5: `find_sessions` and `search_commands` tools

**Files:**
- Modify: `src/mcp.mjs`
- Modify: `test/integration/mcp.test.mjs`

- [ ] **Step 1: Seed test data and add `find_sessions` and `search_commands` tests**

In `test/integration/mcp.test.mjs`, add data seeding at the end of the `before` hook (after `mongo = await createClient(...)`:

```js
  // Seed test data
  const now = new Date();
  await mongo.sessions.insertMany([
    { session_id: 'sess-1', project_path: '/home/user/myrepo',
      git_origin: 'https://github.com/user/myrepo.git',
      cwd: '/home/user/myrepo', started_at: now, last_seen: now },
    { session_id: 'sess-2', project_path: '/home/user/other',
      git_origin: 'https://github.com/user/other.git',
      cwd: '/home/user/other',  started_at: now, last_seen: now },
    { session_id: 'sess-3', project_path: '/home/user/myrepo',
      git_origin: 'https://github.com/user/myrepo.git',
      cwd: '/home/user/myrepo', started_at: now, last_seen: now },
  ]);
  await mongo.hookEvents.insertMany([
    { session_id: 'sess-1', tool_name: 'Bash',
      tool_input: { command: 'git status' },   created_at: new Date(now - 3000) },
    { session_id: 'sess-1', tool_name: 'Bash',
      tool_input: { command: 'npm install' },  created_at: new Date(now - 2000) },
    { session_id: 'sess-1', tool_name: 'Bash',
      tool_input: { command: 'git status' },   created_at: new Date(now - 1000) },
    { session_id: 'sess-2', tool_name: 'Bash',
      tool_input: { command: 'cargo build' },  created_at: new Date(now) },
  ]);
  await mongo.transcriptLines.insertMany(
    Array.from({ length: 25 }, (_, i) => ({
      session_id: 'sess-1', seq: i, line: { type: 'msg', index: i }, created_at: now,
    }))
  );
  await mongo.transcriptLines.insertMany(
    Array.from({ length: 120 }, (_, i) => ({
      session_id: 'sess-3', seq: i, line: { type: 'msg', index: i }, created_at: now,
    }))
  );
```

Append these tests at the end of the file:

```js
test('find_sessions returns sessions matching git_origin', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 1);
  await callTool(endpoint, 1, 'find_sessions', { git_origin: 'user/myrepo' });
  const [result] = await pending;
  close();
  assert.ok(result.result, `expected result, got: ${JSON.stringify(result)}`);
  const sessions = JSON.parse(result.result.content[0].text);
  assert.equal(sessions.length, 2);
  assert.ok(sessions.every(s => s.git_origin.includes('myrepo')));
});

test('find_sessions returns sessions matching project_path', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 2);
  await callTool(endpoint, 2, 'find_sessions', { project_path: '/home/user/other' });
  const [result] = await pending;
  close();
  const sessions = JSON.parse(result.result.content[0].text);
  assert.equal(sessions.length, 1);
  assert.equal(sessions[0].session_id, 'sess-2');
});

test('find_sessions returns empty array when no sessions match', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 3);
  await callTool(endpoint, 3, 'find_sessions', { git_origin: 'nonexistent/repo' });
  const [result] = await pending;
  close();
  const sessions = JSON.parse(result.result.content[0].text);
  assert.deepEqual(sessions, []);
});

test('find_sessions result includes event_count', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 4);
  await callTool(endpoint, 4, 'find_sessions', { project_path: '/home/user/myrepo', limit: 5 });
  const [result] = await pending;
  close();
  const sessions = JSON.parse(result.result.content[0].text);
  const sess1 = sessions.find(s => s.session_id === 'sess-1');
  assert.ok(sess1, 'sess-1 not in results');
  assert.equal(typeof sess1.event_count, 'number');
  assert.equal(sess1.event_count, 25);
});

test('search_commands returns matching commands across sessions', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 5);
  await callTool(endpoint, 5, 'search_commands', { pattern: 'git' });
  const [result] = await pending;
  close();
  const matches = JSON.parse(result.result.content[0].text);
  assert.ok(matches.length >= 2);
  assert.ok(matches.every(m => m.command.includes('git')));
  assert.ok(matches[0].session_id !== undefined);
  assert.ok(matches[0].project_path !== undefined);
});

test('search_commands scoped by git_origin returns only matching sessions', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 6);
  await callTool(endpoint, 6, 'search_commands', { pattern: 'build', git_origin: 'user/other' });
  const [result] = await pending;
  close();
  const matches = JSON.parse(result.result.content[0].text);
  assert.equal(matches.length, 1);
  assert.equal(matches[0].command, 'cargo build');
  assert.equal(matches[0].session_id, 'sess-2');
});

test('search_commands returns empty array when pattern matches nothing', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 7);
  await callTool(endpoint, 7, 'search_commands', { pattern: 'xyzzy_no_match_ever' });
  const [result] = await pending;
  close();
  const matches = JSON.parse(result.result.content[0].text);
  assert.deepEqual(matches, []);
});
```

- [ ] **Step 2: Run tests to verify new tests fail**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: health + SSE tests pass; find_sessions + search_commands tests fail with `"unknown tool"` error.

- [ ] **Step 3: Implement `findSessions` and `searchCommands` in `src/mcp.mjs`**

Add these two functions before `handleToolCall`:

```js
async function findSessions({ project_path, git_origin, query, limit = 10 }) {
  const filter = {};
  if (project_path) filter.project_path = { $regex: project_path, $options: 'i' };
  if (git_origin)   filter.git_origin   = { $regex: git_origin,   $options: 'i' };
  if (query) filter.$or = [
    { project_path: { $regex: query, $options: 'i' } },
    { cwd:          { $regex: query, $options: 'i' } },
  ];
  const docs = await mongo.sessions
    .find(filter, { projection: { _id: 0, session_id: 1, project_path: 1, git_origin: 1, cwd: 1, started_at: 1, last_seen: 1 } })
    .sort({ last_seen: -1 })
    .limit(limit)
    .toArray();
  const counts = await Promise.all(
    docs.map(s => mongo.transcriptLines.countDocuments({ session_id: s.session_id }))
  );
  return docs.map((s, i) => ({ ...s, event_count: counts[i] }));
}

async function searchCommands({ pattern, session_id, git_origin, limit = 20 }) {
  let sessionIds;
  if (session_id) {
    sessionIds = [session_id];
  } else if (git_origin) {
    const ss = await mongo.sessions
      .find({ git_origin: { $regex: git_origin, $options: 'i' } }, { projection: { session_id: 1 } })
      .toArray();
    sessionIds = ss.map(s => s.session_id);
    if (sessionIds.length === 0) return [];
  }

  const filter = { tool_name: 'Bash', 'tool_input.command': { $regex: pattern, $options: 'i' } };
  if (sessionIds) filter.session_id = { $in: sessionIds };

  const events = await mongo.hookEvents
    .find(filter, { projection: { _id: 0, session_id: 1, tool_input: 1, created_at: 1 } })
    .sort({ created_at: -1 })
    .limit(limit)
    .toArray();

  const uniqueIds = [...new Set(events.map(e => e.session_id))];
  const sessionMap = new Map();
  if (uniqueIds.length > 0) {
    const ss = await mongo.sessions
      .find({ session_id: { $in: uniqueIds } }, { projection: { session_id: 1, project_path: 1, git_origin: 1 } })
      .toArray();
    for (const s of ss) sessionMap.set(s.session_id, s);
  }

  return events.map(ev => ({
    session_id:   ev.session_id,
    project_path: sessionMap.get(ev.session_id)?.project_path ?? null,
    git_origin:   sessionMap.get(ev.session_id)?.git_origin   ?? null,
    command:      ev.tool_input.command,
    created_at:   ev.created_at,
  }));
}
```

Update `handleToolCall` to dispatch to the new tools:

```js
async function handleToolCall(name, args, meta, sseRes) {
  switch (name) {
    case 'find_sessions':   return findSessions(args);
    case 'search_commands': return searchCommands(args);
    default: throw new Error(`unknown tool: ${name}`);
  }
}
```

- [ ] **Step 4: Run tests to verify all pass**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: all 9 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/mcp.mjs test/integration/mcp.test.mjs
git commit -m "feat: implement find_sessions and search_commands MCP tools"
```

---

## Task 6: `get_session_context` tool

**Files:**
- Modify: `src/mcp.mjs`
- Modify: `test/integration/mcp.test.mjs`

- [ ] **Step 1: Add `get_session_context` tests to `test/integration/mcp.test.mjs`**

Append these tests at the end of the file:

```js
test('get_session_context returns metadata, top_commands, first/last lines', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 10);
  await callTool(endpoint, 10, 'get_session_context', { session_id: 'sess-1' });
  const [result] = await pending;
  close();
  assert.ok(result.result, `expected result, got error: ${JSON.stringify(result.error)}`);
  const ctx = JSON.parse(result.result.content[0].text);
  assert.equal(ctx.session.session_id, 'sess-1');
  assert.equal(ctx.session.git_origin, 'https://github.com/user/myrepo.git');
  // top_commands: 2 distinct (git status, npm install), most recent first
  assert.deepEqual(ctx.top_commands, ['git status', 'npm install']);
  // sess-1 has 25 lines: first 20 present, last 20 present (overlap is fine)
  assert.equal(ctx.first_lines.length, 20);
  assert.equal(ctx.first_lines[0].seq, 0);
  assert.equal(ctx.last_lines.length, 20);
  assert.equal(ctx.last_lines[19].seq, 24);
});

test('get_session_context returns error for unknown session_id', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 11);
  await callTool(endpoint, 11, 'get_session_context', { session_id: 'does-not-exist' });
  const [result] = await pending;
  close();
  assert.ok(result.error, 'expected error response');
  assert.equal(result.error.message, 'session not found');
});
```

- [ ] **Step 2: Run tests to verify new tests fail**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: the two new tests fail with `"unknown tool"`.

- [ ] **Step 3: Implement `getSessionContext` in `src/mcp.mjs`**

Add this function before `handleToolCall`:

```js
async function getSessionContext({ session_id }) {
  const session = await mongo.sessions.findOne({ session_id }, { projection: { _id: 0 } });
  if (!session) throw Object.assign(new Error('session not found'), { isMcpError: true });

  const bashEvents = await mongo.hookEvents
    .find({ session_id, tool_name: 'Bash', 'tool_input.command': { $type: 'string' } })
    .sort({ created_at: -1 })
    .limit(100)
    .toArray();
  const seen = new Set();
  const top_commands = [];
  for (const ev of bashEvents) {
    const cmd = ev.tool_input.command;
    if (!seen.has(cmd)) {
      seen.add(cmd);
      top_commands.push(cmd);
      if (top_commands.length >= 10) break;
    }
  }

  const first_lines = await mongo.transcriptLines
    .find({ session_id }, { projection: { _id: 0 } })
    .sort({ seq: 1 })
    .limit(20)
    .toArray();

  const total = await mongo.transcriptLines.countDocuments({ session_id });
  const last_lines = total > 20
    ? (await mongo.transcriptLines
        .find({ session_id }, { projection: { _id: 0 } })
        .sort({ seq: -1 })
        .limit(20)
        .toArray()).reverse()
    : [];

  return {
    session: {
      session_id:   session.session_id,
      project_path: session.project_path,
      git_origin:   session.git_origin ?? null,
      cwd:          session.cwd,
      started_at:   session.started_at,
      last_seen:    session.last_seen,
    },
    top_commands,
    first_lines,
    last_lines,
  };
}
```

Update `handleToolCall` to add the new case:

```js
async function handleToolCall(name, args, meta, sseRes) {
  switch (name) {
    case 'find_sessions':       return findSessions(args);
    case 'get_session_context': return getSessionContext(args);
    case 'search_commands':     return searchCommands(args);
    default: throw new Error(`unknown tool: ${name}`);
  }
}
```

- [ ] **Step 4: Run tests to verify all pass**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: all 11 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/mcp.mjs test/integration/mcp.test.mjs
git commit -m "feat: implement get_session_context MCP tool"
```

---

## Task 7: `read_transcript` tool with progress notifications

**Files:**
- Modify: `src/mcp.mjs`
- Modify: `test/integration/mcp.test.mjs`

- [ ] **Step 1: Add `read_transcript` tests to `test/integration/mcp.test.mjs`**

Append these tests at the end of the file:

```js
test('read_transcript returns paginated lines', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 20);
  await callTool(endpoint, 20, 'read_transcript', { session_id: 'sess-1', offset: 0, limit: 10 });
  const [result] = await pending;
  close();
  assert.ok(result.result);
  const lines = JSON.parse(result.result.content[0].text);
  assert.equal(lines.length, 10);
  assert.equal(lines[0].seq, 0);
  assert.equal(lines[9].seq, 9);
});

test('read_transcript with progressToken sends progress notifications before final result', async () => {
  const { endpoint, emitter, close } = await openSSE();
  // sess-3 has 120 lines — 3 batches of 50, 50, 20 → at least 2 progress notifications
  const pending = collectUntilResult(emitter, 21);
  await callTool(endpoint, 21, 'read_transcript', { session_id: 'sess-3', offset: 0, limit: 200 },
    { progressToken: 'tok-21' });
  const events = await pending;
  close();
  const notifications = events.filter(e => e.method === 'notifications/progress');
  const finalResult   = events.at(-1);
  assert.ok(notifications.length >= 1, 'expected at least one progress notification');
  // notifications carry only numeric fields, not line data
  for (const n of notifications) {
    assert.equal(typeof n.params.progress, 'number');
    assert.equal(typeof n.params.total,    'number');
    assert.ok(n.params.data === undefined, 'progress notification must not carry line data');
    assert.equal(n.params.progressToken, 'tok-21');
  }
  // final result comes after all notifications
  assert.ok(finalResult.result, 'last event must be the tool result');
  const lines = JSON.parse(finalResult.result.content[0].text);
  assert.equal(lines.length, 120);
});

test('read_transcript returns error for unknown session_id', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 22);
  await callTool(endpoint, 22, 'read_transcript', { session_id: 'no-such-session' });
  const [result] = await pending;
  close();
  assert.ok(result.error);
  assert.equal(result.error.message, 'session not found');
});
```

- [ ] **Step 2: Run tests to verify new tests fail**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: the three new tests fail with `"unknown tool"`.

- [ ] **Step 3: Implement `readTranscript` in `src/mcp.mjs`**

Add `pushProgress` helper after `sseWrite`:

```js
function pushProgress(res, progressToken, progress, total) {
  sseWrite(res, {
    jsonrpc: '2.0',
    method: 'notifications/progress',
    params: { progressToken, progress, total },
  });
}
```

Add `readTranscript` before `handleToolCall`:

```js
async function readTranscript({ session_id, offset = 0, limit = 200 }, progressToken, sseRes) {
  const session = await mongo.sessions.findOne({ session_id });
  if (!session) throw Object.assign(new Error('session not found'), { isMcpError: true });

  const total = await mongo.transcriptLines.countDocuments({ session_id });
  const BATCH = 50;
  const allLines = [];

  for (let batchStart = offset; batchStart < offset + limit; batchStart += BATCH) {
    const batchLimit = Math.min(BATCH, offset + limit - batchStart);
    const lines = await mongo.transcriptLines
      .find({ session_id }, { projection: { _id: 0 } })
      .sort({ seq: 1 })
      .skip(batchStart)
      .limit(batchLimit)
      .toArray();
    allLines.push(...lines);
    if (progressToken !== undefined && sseRes && lines.length > 0) {
      pushProgress(sseRes, progressToken, allLines.length, total);
    }
    if (lines.length < batchLimit) break;
  }

  return allLines;
}
```

Update `handleToolCall` to add the final case, passing `meta.progressToken`:

```js
async function handleToolCall(name, args, meta, sseRes) {
  switch (name) {
    case 'find_sessions':       return findSessions(args);
    case 'get_session_context': return getSessionContext(args);
    case 'search_commands':     return searchCommands(args);
    case 'read_transcript':     return readTranscript(args, meta?.progressToken, sseRes);
    default: throw new Error(`unknown tool: ${name}`);
  }
}
```

- [ ] **Step 4: Run the full integration test suite**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test test/integration/mcp.test.mjs
```

Expected: all 14 tests pass.

- [ ] **Step 5: Commit**

```bash
git add src/mcp.mjs test/integration/mcp.test.mjs
git commit -m "feat: implement read_transcript MCP tool with progress notifications"
```

---

## Task 8: Update `session-start` hook

The hook currently fast-exits if the daemon is healthy. The updated version independently checks and heals both the daemon and the MCP server, then always triggers backfill.

**Files:**
- Modify: `hooks/session-start`

There are no automated tests for this hook. Correctness is verified manually at the end of this task.

- [ ] **Step 1: Replace `hooks/session-start` with the updated version**

```bash
#!/usr/bin/env bash
PLUGIN_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_FILE="${HOME}/.claude/plugins/data/clued/config.json"
DEFAULT_PORT=8085
DEFAULT_MCP_PORT=8086

if command -v jq &>/dev/null && [[ -f "$CONFIG_FILE" ]]; then
  PORT=$(jq -r ".port // ${DEFAULT_PORT}" "$CONFIG_FILE" 2>/dev/null || echo "$DEFAULT_PORT")
  MCP_PORT=$(jq -r ".mcpPort // ${DEFAULT_MCP_PORT}" "$CONFIG_FILE" 2>/dev/null || echo "$DEFAULT_MCP_PORT")
else
  PORT=$DEFAULT_PORT
  MCP_PORT=$DEFAULT_MCP_PORT
fi
PORT="${CLUED_PORT:-$PORT}"
MCP_PORT="${CLUED_MCP_PORT:-$MCP_PORT}"

daemon_ok() {
  curl -sf --max-time 1 "http://127.0.0.1:${PORT}/health" &>/dev/null
}

mcp_ok() {
  curl -sf --max-time 1 "http://127.0.0.1:${MCP_PORT}/health" &>/dev/null
}

if ! daemon_ok; then
  node "${PLUGIN_ROOT}/src/daemon.mjs" &>/dev/null &
  DAEMON_PID=$!
  disown
  for _ in $(seq 1 15); do
    sleep 0.2
    if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
      echo "clued: daemon exited on startup — check MongoDB connection (${CLUED_MONGO_URL:-see config.json})" >&2
      break
    fi
    if daemon_ok; then break; fi
  done
fi

if ! mcp_ok; then
  node "${PLUGIN_ROOT}/src/mcp.mjs" &>/dev/null &
  MCP_PID=$!
  disown
  for _ in $(seq 1 15); do
    sleep 0.2
    if ! kill -0 "$MCP_PID" 2>/dev/null; then
      echo "clued: mcp server exited on startup — check MongoDB connection (${CLUED_MONGO_URL:-see config.json})" >&2
      break
    fi
    if mcp_ok; then break; fi
  done
fi

node "${PLUGIN_ROOT}/src/backfill.mjs" &>/dev/null &
disown
exit 0
```

- [ ] **Step 2: Make executable**

```bash
chmod +x hooks/session-start
```

- [ ] **Step 3: Manual smoke test**

Kill any running daemon and MCP server, then run the hook:

```bash
pkill -f 'src/daemon.mjs' || true
pkill -f 'src/mcp.mjs'    || true
sleep 0.5
CLUED_MONGO_URL=mongodb://localhost:27018 bash hooks/session-start
sleep 1
curl -sf http://127.0.0.1:8085/health && echo "daemon ok"
curl -sf http://127.0.0.1:8086/health && echo "mcp ok"
```

Expected: both `daemon ok` and `mcp ok` printed.

- [ ] **Step 4: Verify fast-exit when both already healthy**

```bash
time CLUED_MONGO_URL=mongodb://localhost:27018 bash hooks/session-start
```

Expected: completes in well under 1 second (health checks succeed immediately, no spawning).

- [ ] **Step 5: Stop background processes and commit**

```bash
pkill -f 'src/daemon.mjs' || true
pkill -f 'src/mcp.mjs'    || true
git add hooks/session-start
git commit -m "feat: update session-start hook to independently heal daemon and MCP server"
```

---

## Task 9: Update `skills/clued-setup.md`

Add the MCP server registration step to the setup guide.

**Files:**
- Modify: `skills/clued-setup.md`

- [ ] **Step 1: Add Step 3 (MCP server registration) between "Write config" and "Add relay hooks"**

After the `### 2. Write config file` section and before `### 3. Add relay hooks to user settings`, insert:

```markdown
### 3. Register MCP server in Claude settings

Read `~/.claude/settings.json`. Add the MCP server entry under `mcpServers`:

```json
"mcpServers": {
  "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
}
```

If `mcpServers` already exists, merge the `"clued"` key in. Do not duplicate if `"clued"` is already present.
```

Renumber the existing steps 3, 4, 5 to 4, 5, 6.

Also add the MCP server status to the confirmation step (now Step 6):

```markdown
- MCP server URL: `http://127.0.0.1:8086/sse`
- Registered in `~/.claude/settings.json` under `mcpServers.clued`
```

- [ ] **Step 2: Commit**

```bash
git add skills/clued-setup.md
git commit -m "docs: add MCP server registration step to clued-setup skill"
```

---

## Final verification

- [ ] **Run all unit tests**

```bash
node --test 'test/*.test.mjs'
```

Expected: all unit tests pass.

- [ ] **Run all integration tests**

```bash
CLUED_MONGO_URL=mongodb://localhost:27018 node --test 'test/integration/*.test.mjs'
```

Expected: all integration tests pass (daemon + MCP).
