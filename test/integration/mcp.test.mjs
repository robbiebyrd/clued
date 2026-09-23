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
