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
