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
