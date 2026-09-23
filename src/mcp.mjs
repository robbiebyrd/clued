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

const MAX_LIMIT = 500;

function sseWrite(res, data) {
  res.write(`data: ${JSON.stringify(data)}\n\n`);
}

function pushProgress(res, progressToken, progress, total) {
  sseWrite(res, {
    jsonrpc: '2.0',
    method: 'notifications/progress',
    params: { progressToken, progress, total },
  });
}

async function findSessions({ project_path, git_origin, query, limit = 10 }) {
  limit = Math.min(limit, MAX_LIMIT);
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
  limit = Math.min(limit, MAX_LIMIT);
  let sessionIds;
  if (session_id) {
    sessionIds = [session_id];
  } else if (git_origin) {
    const ss = await mongo.sessions
      .find({ git_origin: { $regex: git_origin, $options: 'i' } }, { projection: { session_id: 1 } })
      .limit(MAX_LIMIT)
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

async function getSessionContext({ session_id }) {
  const session = await mongo.sessions.findOne({ session_id }, { projection: { _id: 0 } });
  if (!session) throw new Error('session not found');

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

async function readTranscript({ session_id, offset = 0, limit = 200 }, progressToken, sseRes) {
  limit = Math.min(limit, MAX_LIMIT);
  const session = await mongo.sessions.findOne({ session_id });
  if (!session) throw new Error('session not found');

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

async function handleToolCall(name, args, meta, sseRes) {
  switch (name) {
    case 'find_sessions':       return findSessions(args);
    case 'get_session_context': return getSessionContext(args);
    case 'search_commands':     return searchCommands(args);
    case 'read_transcript':     return readTranscript(args, meta?.progressToken, sseRes);
    default: throw new Error(`unknown tool: ${name}`);
  }
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
