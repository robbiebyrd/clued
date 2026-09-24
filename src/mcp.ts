import http, { ServerResponse } from 'http';
import { randomUUID } from 'crypto';
import { loadConfig }    from './config';
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

function pushProgress(res: ServerResponse, progressToken: unknown, progress: number, total: number): void {
  sseWrite(res, {
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
  sseRes: ServerResponse | undefined,
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
    if (progressToken !== undefined && sseRes && lines.length > 0) {
      pushProgress(sseRes, progressToken, allLines.length, total);
    }
    if (lines.length < batchLimit) break;
  }

  return allLines;
}

const TOOLS = [
  {
    name: 'find_sessions',
    description: 'Find Claude Code sessions by project path, git origin, or keyword.',
    inputSchema: {
      type: 'object',
      properties: {
        project_path: { type: 'string', description: 'Filter by project path (regex, case-insensitive)' },
        git_origin:   { type: 'string', description: 'Filter by git remote origin (regex, case-insensitive)' },
        query:        { type: 'string', description: 'Keyword search across project_path and cwd' },
        limit:        { type: 'number', description: 'Max results (default 10)' },
      },
    },
  },
  {
    name: 'get_session_context',
    description: 'Get session metadata, top bash commands, and first/last transcript lines for a session.',
    inputSchema: {
      type: 'object',
      required: ['session_id'],
      properties: {
        session_id: { type: 'string', description: 'Session ID to look up' },
      },
    },
  },
  {
    name: 'search_commands',
    description: 'Search bash commands across sessions by regex pattern.',
    inputSchema: {
      type: 'object',
      required: ['pattern'],
      properties: {
        pattern:    { type: 'string', description: 'Regex pattern matched against command strings' },
        session_id: { type: 'string', description: 'Limit to a specific session' },
        git_origin: { type: 'string', description: 'Limit to sessions matching this git origin (regex)' },
        limit:      { type: 'number', description: 'Max results (default 20)' },
      },
    },
  },
  {
    name: 'read_transcript',
    description: 'Read transcript lines from a session with pagination and optional progress streaming.',
    inputSchema: {
      type: 'object',
      required: ['session_id'],
      properties: {
        session_id: { type: 'string', description: 'Session ID' },
        offset:     { type: 'number', description: 'Starting line index (default 0)' },
        limit:      { type: 'number', description: 'Lines to return (default 200, max 500)' },
      },
    },
  },
];

async function handleRpc(
  method: string,
  params: Record<string, unknown>,
  id: unknown,
  sseRes: ServerResponse,
): Promise<void> {
  if (method === 'initialize') {
    sseWrite(sseRes, {
      jsonrpc: '2.0', id,
      result: {
        protocolVersion: '2024-11-05',
        capabilities: { tools: {} },
        serverInfo: { name: 'clued', version: '1.0.0' },
      },
    });
    return;
  }
  if (method === 'tools/list') {
    sseWrite(sseRes, { jsonrpc: '2.0', id, result: { tools: TOOLS } });
    return;
  }
  if (method === 'notifications/initialized') {
    return;
  }
  if (method !== 'tools/call') {
    sseWrite(sseRes, { jsonrpc: '2.0', id, result: {} });
    return;
  }
  const meta = ((params._meta as Record<string, unknown>) || {});
  try {
    const result = await handleToolCall(
      params.name as string,
      (params.arguments as Record<string, unknown>) || {},
      meta,
      sseRes,
    );
    sseWrite(sseRes, {
      jsonrpc: '2.0', id,
      result: { content: [{ type: 'text', text: JSON.stringify(result) }] },
    });
  } catch (e) {
    sseWrite(sseRes, { jsonrpc: '2.0', id, error: { code: -32000, message: (e as Error).message } });
  }
}

async function handleToolCall(
  name: string,
  args: Record<string, unknown>,
  meta: Record<string, unknown>,
  sseRes: ServerResponse,
) {
  switch (name) {
    case 'find_sessions':       return findSessions(args as FindSessionsArgs);
    case 'get_session_context': return getSessionContext(args as { session_id: string });
    case 'search_commands':     return searchCommands(args as unknown as SearchCommandsArgs);
    case 'read_transcript':     return readTranscript(args as unknown as ReadTranscriptArgs, meta?.progressToken, sseRes);
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
      let rpc: { id: unknown; method: string; params?: Record<string, unknown> };
      try { rpc = JSON.parse(body); } catch {
        res.writeHead(400); res.end('invalid json'); return;
      }
      res.writeHead(202); res.end();

      const { id, method, params = {} } = rpc;
      handleRpc(method, params, id, sseRes).catch(() => {});
    });
    return;
  }

  res.writeHead(404); res.end();
});

server.on('error', async (e: NodeJS.ErrnoException) => {
  if (e.code === 'EADDRINUSE') { await mongo.close(); process.exit(0); }
  console.error('clued mcp error:', e.message);
  await mongo.close(); process.exit(1);
});

server.listen(config.mcpPort, '127.0.0.1');

const shutdown = async () => { server.close(); await mongo.close(); process.exit(0); };
process.on('SIGTERM', shutdown);
process.on('SIGINT',  shutdown);
