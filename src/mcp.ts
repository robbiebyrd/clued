import http, { ServerResponse } from 'http';
import { randomUUID } from 'crypto';
import { createInterface } from 'readline';
import { loadConfig }    from './config';
import { dispatch, JsonRpcRequest } from './mcp-protocol';
import { createClient }  from './mongo';
import { readAccountId } from './account';

const config     = loadConfig();
const account_id = readAccountId(config.claudeAppConfigPath);
const mongo  = await createClient(config).catch(err => {
  console.error('clued mcp: MongoDB connection failed:', (err as Error).message);
  process.exit(1);
});

const sessions = new Map<string, ServerResponse>();
const MAX_LIMIT = 500;

function sseWrite(res: ServerResponse, data: unknown): void {
  res.write(`data: ${JSON.stringify(data)}\n\n`);
}

type Notify = (msg: unknown) => void;

function pushProgress(notify: Notify, progressToken: unknown, progress: number, total: number): void {
  notify({
    jsonrpc: '2.0',
    method: 'notifications/progress',
    params: { progressToken, progress, total },
  });
}

interface FindSessionsArgs {
  project_path?: string;
  git_origin?:   string;
  query?:        string;
  limit?:        number;
}

async function findSessions({ project_path, git_origin, query, limit = 10 }: FindSessionsArgs) {
  limit = Math.min(limit, MAX_LIMIT);
  const filter: Record<string, unknown> = { account_id };
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

interface SearchCommandsArgs {
  pattern:      string;
  session_id?:  string;
  git_origin?:  string;
  limit?:       number;
}

async function searchCommands({ pattern, session_id, git_origin, limit = 20 }: SearchCommandsArgs) {
  limit = Math.min(limit, MAX_LIMIT);
  let sessionIds: string[] | undefined;
  if (session_id) {
    const owned = await mongo.sessions.findOne({ session_id, account_id }, { projection: { session_id: 1 } });
    if (!owned) return [];
    sessionIds = [session_id];
  } else if (git_origin) {
    const ss = await mongo.sessions
      .find({ account_id, git_origin: { $regex: git_origin, $options: 'i' } }, { projection: { session_id: 1 } })
      .limit(MAX_LIMIT)
      .toArray();
    sessionIds = ss.map(s => s.session_id as string);
    if (sessionIds.length === 0) return [];
  }

  const filter: Record<string, unknown> = { tool_name: 'Bash', 'tool_input.command': { $regex: pattern, $options: 'i' } };
  if (sessionIds) filter.session_id = { $in: sessionIds };
  filter.account_id = account_id;

  const events = await mongo.hookEvents
    .find(filter, { projection: { _id: 0, session_id: 1, tool_input: 1, created_at: 1 } })
    .sort({ created_at: -1 })
    .limit(limit)
    .toArray();

  const uniqueIds = [...new Set(events.map(e => e.session_id as string))];
  const sessionMap = new Map<string, Record<string, unknown>>();
  if (uniqueIds.length > 0) {
    const ss = await mongo.sessions
      .find({ session_id: { $in: uniqueIds } }, { projection: { session_id: 1, project_path: 1, git_origin: 1 } })
      .toArray();
    for (const s of ss) sessionMap.set(s.session_id as string, s as Record<string, unknown>);
  }

  return events.map(ev => ({
    session_id:   ev.session_id,
    project_path: sessionMap.get(ev.session_id as string)?.project_path ?? null,
    git_origin:   sessionMap.get(ev.session_id as string)?.git_origin   ?? null,
    command:      (ev.tool_input as Record<string, unknown>).command,
    created_at:   ev.created_at,
  }));
}

async function getSessionContext({ session_id }: { session_id: string }) {
  const session = await mongo.sessions.findOne({ session_id, account_id }, { projection: { _id: 0 } });
  if (!session) throw new Error('session not found');

  const bashEvents = await mongo.hookEvents
    .find({ session_id, account_id, tool_name: 'Bash', 'tool_input.command': { $type: 'string' } })
    .sort({ created_at: -1 })
    .limit(100)
    .toArray();
  const seen = new Set<string>();
  const top_commands: string[] = [];
  for (const ev of bashEvents) {
    const cmd = (ev.tool_input as Record<string, unknown>).command as string;
    if (!seen.has(cmd)) {
      seen.add(cmd);
      top_commands.push(cmd);
      if (top_commands.length >= 10) break;
    }
  }

  const first_lines = await mongo.transcriptLines
    .find({ session_id, account_id }, { projection: { _id: 0 } })
    .sort({ seq: 1 })
    .limit(20)
    .toArray();

  const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
  const last_lines = total > 20
    ? (await mongo.transcriptLines
        .find({ session_id, account_id }, { projection: { _id: 0 } })
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

interface ReadTranscriptArgs { session_id: string; offset?: number; limit?: number; }

async function readTranscript(
  { session_id, offset = 0, limit = 200 }: ReadTranscriptArgs,
  progressToken: unknown,
  notify: Notify | undefined,
) {
  limit = Math.min(limit, MAX_LIMIT);
  const session = await mongo.sessions.findOne({ session_id, account_id });
  if (!session) throw new Error('session not found');

  const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
  const BATCH = 50;
  const allLines: unknown[] = [];

  for (let batchStart = offset; batchStart < offset + limit; batchStart += BATCH) {
    const batchLimit = Math.min(BATCH, offset + limit - batchStart);
    const lines = await mongo.transcriptLines
      .find({ session_id, account_id }, { projection: { _id: 0 } })
      .sort({ seq: 1 })
      .skip(batchStart)
      .limit(batchLimit)
      .toArray();
    allLines.push(...lines);
    if (progressToken !== undefined && notify && lines.length > 0) {
      pushProgress(notify, progressToken, allLines.length, total);
    }
    if (lines.length < batchLimit) break;
  }

  return allLines;
}

async function handleToolCall(
  name: string,
  args: Record<string, unknown>,
  meta: Record<string, unknown>,
  notify: Notify,
) {
  switch (name) {
    case 'find_sessions':       return findSessions(args as FindSessionsArgs);
    case 'get_session_context': return getSessionContext(args as { session_id: string });
    case 'search_commands':     return searchCommands(args as unknown as SearchCommandsArgs);
    case 'read_transcript':     return readTranscript(args as unknown as ReadTranscriptArgs, meta?.progressToken, notify);
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
    const sseRes = sessionId ? sessions.get(sessionId) : undefined;
    if (!sseRes) { res.writeHead(400); res.end('unknown session'); return; }

    let body = '';
    req.on('data', (c: Buffer) => { body += c; });
    req.on('end', async () => {
      let rpc: JsonRpcRequest;
      try { rpc = JSON.parse(body); } catch {
        res.writeHead(400); res.end('invalid json'); return;
      }
      res.writeHead(202); res.end();

      const notify: Notify = msg => sseWrite(sseRes, msg);
      const reply = await dispatch(rpc, (name, args, meta) => handleToolCall(name, args, meta, notify));
      if (reply) notify(reply);
    });
    return;
  }

  res.writeHead(404); res.end();
});

// stdio transport: Claude Code spawns `mcp.mjs --stdio` per session (see .mcp.json).
if (process.argv.includes('--stdio')) {
  const notify: Notify = msg => { process.stdout.write(JSON.stringify(msg) + '\n'); };
  const rl = createInterface({ input: process.stdin });
  rl.on('line', async line => {
    if (!line.trim()) return;
    let rpc: JsonRpcRequest;
    try { rpc = JSON.parse(line); } catch {
      notify({ jsonrpc: '2.0', id: null, error: { code: -32700, message: 'parse error' } }); return;
    }
    const reply = await dispatch(rpc, (name, args, meta) => handleToolCall(name, args, meta, notify));
    if (reply) notify(reply);
  });
  rl.on('close', async () => { await mongo.close(); process.exit(0); });
} else {
  startHttpServer();
}

function startHttpServer(): void {
  server.on('error', async (e: NodeJS.ErrnoException) => {
    if (e.code === 'EADDRINUSE') { await mongo.close(); process.exit(0); }
    console.error('clued mcp error:', e.message);
    await mongo.close(); process.exit(1);
  });

  server.listen(config.mcpPort, '127.0.0.1');
}

const shutdown = async () => { server.close(); await mongo.close(); process.exit(0); };
process.on('SIGTERM', shutdown);
process.on('SIGINT',  shutdown);
