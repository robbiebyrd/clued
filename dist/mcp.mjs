#!/usr/bin/env node
import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// src/mcp.ts
import http from "http";
import { randomUUID } from "crypto";
import { createInterface } from "readline";

// src/config.ts
import { readFileSync } from "fs";
import { homedir } from "os";
import { dirname, join } from "path";
var DEFAULT_CONFIG_PATH = join(homedir(), ".claude", "plugins", "data", "clued", "config.json");
var DEFAULTS = {
  mongoUrl: "mongodb://localhost:27018",
  dbName: "claude_sessions",
  port: 8085,
  mcpPort: 8086,
  projectsDir: join(homedir(), ".claude", "projects"),
  fileHistoryDir: join(homedir(), ".claude", "file-history"),
  disabledEnrichers: [],
  claudeAppConfigPath: join(homedir(), "Library", "Application Support", "Claude", "config.json"),
  walPath: join(homedir(), ".claude", "plugins", "data", "clued", "events.wal")
};
function expandHome(val) {
  return val.startsWith("~/") ? join(homedir(), val.slice(2)) : val;
}
function loadConfig(configPath = DEFAULT_CONFIG_PATH) {
  let fileConfig = {};
  try {
    fileConfig = JSON.parse(readFileSync(configPath, "utf8"));
  } catch {
  }
  const cfg = { ...DEFAULTS, ...fileConfig };
  if (process.env.CLUED_MONGO_URL) cfg.mongoUrl = process.env.CLUED_MONGO_URL;
  if (process.env.CLUED_DB_NAME) cfg.dbName = process.env.CLUED_DB_NAME;
  if (process.env.CLUED_PORT) cfg.port = parseInt(process.env.CLUED_PORT, 10);
  if (process.env.CLUED_MCP_PORT) cfg.mcpPort = parseInt(process.env.CLUED_MCP_PORT, 10);
  if (process.env.CLUED_PROJECTS_DIR) cfg.projectsDir = process.env.CLUED_PROJECTS_DIR;
  if (process.env.CLUED_FILE_HISTORY_DIR) cfg.fileHistoryDir = process.env.CLUED_FILE_HISTORY_DIR;
  if (process.env.CLUED_CLAUDE_APP_CONFIG_PATH) cfg.claudeAppConfigPath = process.env.CLUED_CLAUDE_APP_CONFIG_PATH;
  cfg.projectsDir = expandHome(cfg.projectsDir);
  cfg.fileHistoryDir = expandHome(cfg.fileHistoryDir);
  cfg.mongoUrl = expandHome(cfg.mongoUrl);
  cfg.claudeAppConfigPath = expandHome(cfg.claudeAppConfigPath);
  cfg.walPath = process.env.CLUED_WAL_PATH ?? join(dirname(configPath), "events.wal");
  return cfg;
}

// src/mcp-protocol.ts
var SUPPORTED_PROTOCOL_VERSIONS = ["2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"];
var DEFAULT_PROTOCOL_VERSION = "2025-06-18";
var SERVER_INFO = { name: "clued", version: "1.0.0" };
var TOOLS = [
  {
    name: "find_sessions",
    description: "Find previous Claude Code sessions, newest first. Filters match (case-insensitive regex) against project path, cwd, and git origin.",
    inputSchema: {
      type: "object",
      properties: {
        project_path: { type: "string", description: "Regex matched against the session project path" },
        git_origin: { type: "string", description: "Regex matched against the git remote origin URL" },
        query: { type: "string", description: "Regex matched against project path or cwd" },
        limit: { type: "number", description: "Max sessions to return (default 10, max 500)" }
      }
    }
  },
  {
    name: "get_session_context",
    description: "Summarise one session: metadata, its most recent distinct Bash commands, and the first/last transcript lines.",
    inputSchema: {
      type: "object",
      properties: { session_id: { type: "string" } },
      required: ["session_id"]
    }
  },
  {
    name: "search_commands",
    description: "Search Bash commands run in previous sessions, newest first.",
    inputSchema: {
      type: "object",
      properties: {
        pattern: { type: "string", description: "Case-insensitive regex matched against the command" },
        session_id: { type: "string", description: "Restrict to one session" },
        git_origin: { type: "string", description: "Restrict to sessions whose git origin matches this regex" },
        limit: { type: "number", description: "Max commands to return (default 20, max 500)" }
      },
      required: ["pattern"]
    }
  },
  {
    name: "read_transcript",
    description: "Read transcript lines of a session in order, paginated by offset/limit.",
    inputSchema: {
      type: "object",
      properties: {
        session_id: { type: "string" },
        offset: { type: "number", description: "Line offset (default 0)" },
        limit: { type: "number", description: "Lines to return (default 200, max 500)" }
      },
      required: ["session_id"]
    }
  },
  {
    name: "restore_session",
    description: "Restore a session from MongoDB to the local filesystem. Reconstructs the main JSONL, subagent files, tool-result blobs, and file-history backups.",
    inputSchema: {
      type: "object",
      properties: {
        session_id: { type: "string", description: "Session to restore" },
        project_path: { type: "string", description: "Override the recorded project path (use when username/homedir differs on this machine)" },
        projects_dir: { type: "string", description: "Override the target ~/.claude/projects directory" }
      },
      required: ["session_id"]
    }
  }
];
async function dispatch(rpc, callTool) {
  const { id, method, params = {} } = rpc;
  if (id === void 0 || id === null) return void 0;
  const ok = (result) => ({ jsonrpc: "2.0", id, result });
  switch (method) {
    case "initialize": {
      const requested = params.protocolVersion;
      const protocolVersion = requested && SUPPORTED_PROTOCOL_VERSIONS.includes(requested) ? requested : DEFAULT_PROTOCOL_VERSION;
      return ok({ protocolVersion, capabilities: { tools: {} }, serverInfo: SERVER_INFO });
    }
    case "ping":
      return ok({});
    case "tools/list":
      return ok({ tools: TOOLS });
    case "tools/call":
      try {
        const result = await callTool(
          params.name,
          params.arguments || {},
          params._meta || {}
        );
        return ok({ content: [{ type: "text", text: JSON.stringify(result) }] });
      } catch (e) {
        return { jsonrpc: "2.0", id, error: { code: -32e3, message: e.message } };
      }
    default:
      return { jsonrpc: "2.0", id, error: { code: -32601, message: `method not found: ${method}` } };
  }
}

// src/mongo.ts
import { MongoClient } from "mongodb";
async function createClient({ mongoUrl, dbName }) {
  const client = new MongoClient(mongoUrl);
  await client.connect();
  const db = client.db(dbName);
  const results = await Promise.allSettled([
    db.collection("sessions").createIndex({ session_id: 1 }, { unique: true }),
    db.collection("sessions").createIndex({ git_origin: 1 }),
    db.collection("hook_events").createIndex({ session_id: 1 }),
    db.collection("hook_events").createIndex({ created_at: -1 }),
    db.collection("transcript_lines").createIndex({ session_id: 1, seq: 1 }, { unique: true }),
    db.collection("sessions").createIndex({ account_id: 1, last_seen: -1 }),
    db.collection("sessions").createIndex({ account_id: 1, git_origin: 1 }),
    db.collection("sessions").createIndex({ account_id: 1, git_origin: 1, git_branch: 1 }),
    db.collection("hook_events").createIndex({ account_id: 1, session_id: 1, created_at: -1 }),
    db.collection("transcript_lines").createIndex({ account_id: 1, session_id: 1, seq: 1 }),
    db.collection("subagent_lines").createIndex({ session_id: 1, subagent_id: 1, seq: 1 }, { unique: true }),
    db.collection("subagent_lines").createIndex({ account_id: 1, session_id: 1, subagent_id: 1, seq: 1 }),
    db.collection("blobs").createIndex({ session_id: 1, blob_type: 1, name: 1 }, { unique: true }),
    db.collection("blobs").createIndex({ account_id: 1, session_id: 1, blob_type: 1 })
  ]);
  for (const r of results) {
    if (r.status === "rejected") console.error("clued: index warning:", r.reason.message);
  }
  return {
    db,
    sessions: db.collection("sessions"),
    hookEvents: db.collection("hook_events"),
    transcriptLines: db.collection("transcript_lines"),
    subagentLines: db.collection("subagent_lines"),
    blobs: db.collection("blobs"),
    close: () => client.close()
  };
}

// src/account.ts
import { readFileSync as readFileSync2 } from "fs";
function readAccountId(path) {
  try {
    const raw = readFileSync2(path, "utf8");
    const data = JSON.parse(raw);
    const id = data.lastKnownAccountUuid ?? "unknown";
    if (id === "unknown") console.warn("clued: account ID unavailable \u2014 isolation is degraded");
    return id;
  } catch {
    console.warn("clued: account ID unavailable \u2014 isolation is degraded");
    return "unknown";
  }
}

// src/restore.ts
import { mkdirSync, createWriteStream, writeFileSync } from "fs";
import { join as join2, basename, dirname as dirname2 } from "path";
async function restoreSession({ session_id, project_path, projects_dir }, account_id2, mongo, fileHistoryDir) {
  const session = await mongo.sessions.findOne({ session_id, account_id: account_id2 });
  if (!session) throw new Error(`session not found: ${session_id}`);
  let projDirName;
  if (project_path) {
    projDirName = "-" + project_path.replace(/^\//, "").replaceAll("/", "-");
  } else if (session.transcript_path) {
    projDirName = basename(dirname2(session.transcript_path));
  } else if (session.project_path) {
    projDirName = "-" + session.project_path.replace(/^\//, "").replaceAll("/", "-");
  } else {
    throw new Error(`session ${session_id} has no path information to derive target directory`);
  }
  const targetProjDir = join2(projects_dir ?? "", projDirName);
  const sessionDir = join2(targetProjDir, session_id);
  const subagentsDir = join2(sessionDir, "subagents");
  const toolResultsDir = join2(sessionDir, "tool-results");
  const sessionFhDir = join2(fileHistoryDir, session_id);
  mkdirSync(targetProjDir, { recursive: true });
  mkdirSync(subagentsDir, { recursive: true });
  mkdirSync(toolResultsDir, { recursive: true });
  mkdirSync(sessionFhDir, { recursive: true });
  let files_written = 0;
  let bytes_written = 0;
  const missing = [];
  const total = await mongo.transcriptLines.countDocuments({ session_id, account_id: account_id2 });
  if (total === 0) {
    missing.push("transcript_lines");
  } else {
    const jsonlPath = join2(targetProjDir, `${session_id}.jsonl`);
    const ws = createWriteStream(jsonlPath, { flags: "w" });
    const BATCH = 500;
    for (let skip = 0; skip < total; skip += BATCH) {
      const lines = await mongo.transcriptLines.find({ session_id, account_id: account_id2 }, { projection: { _id: 0, line: 1 } }).sort({ seq: 1 }).skip(skip).limit(BATCH).toArray();
      for (const doc of lines) {
        const row = JSON.stringify(doc.line) + "\n";
        ws.write(row);
        bytes_written += Buffer.byteLength(row);
      }
    }
    await new Promise((resolve, reject) => {
      ws.end((err) => err ? reject(err) : resolve());
    });
    files_written++;
  }
  const subagentIds = await mongo.subagentLines.distinct("subagent_id", { session_id, account_id: account_id2 });
  for (const subagent_id of subagentIds) {
    const lines = await mongo.subagentLines.find({ session_id, subagent_id, account_id: account_id2 }, { projection: { _id: 0, line: 1 } }).sort({ seq: 1 }).toArray();
    const content = lines.map((d) => JSON.stringify(d.line)).join("\n") + "\n";
    writeFileSync(join2(subagentsDir, `${subagent_id}.jsonl`), content);
    bytes_written += Buffer.byteLength(content);
    files_written++;
  }
  const blobs = await mongo.blobs.find({ session_id, account_id: account_id2 }).toArray();
  const seenBlobTypes = /* @__PURE__ */ new Set();
  for (const blobDoc of blobs) {
    const b = blobDoc;
    const blobType = b.blob_type;
    const name = b.name;
    const content = b.content;
    const encoding = b.encoding;
    seenBlobTypes.add(blobType);
    let outPath;
    if (blobType === "subagent-meta") outPath = join2(subagentsDir, name);
    else if (blobType === "tool-result") outPath = join2(toolResultsDir, name);
    else if (blobType === "file-history") outPath = join2(sessionFhDir, name);
    else continue;
    const buf = encoding === "base64" ? Buffer.from(content, "base64") : Buffer.from(content, "utf8");
    writeFileSync(outPath, buf);
    bytes_written += buf.length;
    files_written++;
  }
  for (const expected of ["subagent-meta", "tool-result", "file-history"]) {
    if (!seenBlobTypes.has(expected)) missing.push(expected);
  }
  return { files_written, bytes_written, missing };
}

// src/mcp.ts
var config = loadConfig();
var account_id = readAccountId(config.claudeAppConfigPath);
var _mongo;
async function getMongo() {
  if (!_mongo) {
    _mongo = await createClient(config).catch((err) => {
      throw new Error(`MongoDB connection failed: ${err.message}`);
    });
  }
  return _mongo;
}
var sessions = /* @__PURE__ */ new Map();
var MAX_LIMIT = 500;
function sseWrite(res, data) {
  res.write(`data: ${JSON.stringify(data)}

`);
}
function pushProgress(notify, progressToken, progress, total) {
  notify({
    jsonrpc: "2.0",
    method: "notifications/progress",
    params: { progressToken, progress, total }
  });
}
async function findSessions({ project_path, git_origin, query, limit = 10 }) {
  limit = Math.min(limit, MAX_LIMIT);
  const filter = { account_id };
  if (project_path) filter.project_path = { $regex: project_path, $options: "i" };
  if (git_origin) filter.git_origin = { $regex: git_origin, $options: "i" };
  if (query) filter.$or = [
    { project_path: { $regex: query, $options: "i" } },
    { cwd: { $regex: query, $options: "i" } }
  ];
  const mongo = await getMongo();
  const docs = await mongo.sessions.find(filter, { projection: { _id: 0, session_id: 1, project_path: 1, git_origin: 1, cwd: 1, started_at: 1, last_seen: 1 } }).sort({ last_seen: -1 }).limit(limit).toArray();
  const counts = await Promise.all(
    docs.map((s) => mongo.transcriptLines.countDocuments({ session_id: s.session_id }))
  );
  return docs.map((s, i) => ({ ...s, event_count: counts[i] }));
}
async function searchCommands({ pattern, session_id, git_origin, limit = 20 }) {
  limit = Math.min(limit, MAX_LIMIT);
  const mongo = await getMongo();
  let sessionIds;
  if (session_id) {
    const owned = await mongo.sessions.findOne({ session_id, account_id }, { projection: { session_id: 1 } });
    if (!owned) return [];
    sessionIds = [session_id];
  } else if (git_origin) {
    const ss = await mongo.sessions.find({ account_id, git_origin: { $regex: git_origin, $options: "i" } }, { projection: { session_id: 1 } }).limit(MAX_LIMIT).toArray();
    sessionIds = ss.map((s) => s.session_id);
    if (sessionIds.length === 0) return [];
  }
  const filter = { tool_name: "Bash", "tool_input.command": { $regex: pattern, $options: "i" } };
  if (sessionIds) filter.session_id = { $in: sessionIds };
  filter.account_id = account_id;
  const events = await mongo.hookEvents.find(filter, { projection: { _id: 0, session_id: 1, tool_input: 1, created_at: 1 } }).sort({ created_at: -1 }).limit(limit).toArray();
  const uniqueIds = [...new Set(events.map((e) => e.session_id))];
  const sessionMap = /* @__PURE__ */ new Map();
  if (uniqueIds.length > 0) {
    const ss = await mongo.sessions.find({ session_id: { $in: uniqueIds } }, { projection: { session_id: 1, project_path: 1, git_origin: 1 } }).toArray();
    for (const s of ss) sessionMap.set(s.session_id, s);
  }
  return events.map((ev) => ({
    session_id: ev.session_id,
    project_path: sessionMap.get(ev.session_id)?.project_path ?? null,
    git_origin: sessionMap.get(ev.session_id)?.git_origin ?? null,
    command: ev.tool_input.command,
    created_at: ev.created_at
  }));
}
async function getSessionContext({ session_id }) {
  const mongo = await getMongo();
  const session = await mongo.sessions.findOne({ session_id, account_id }, { projection: { _id: 0 } });
  if (!session) throw new Error("session not found");
  const bashEvents = await mongo.hookEvents.find({ session_id, account_id, tool_name: "Bash", "tool_input.command": { $type: "string" } }).sort({ created_at: -1 }).limit(100).toArray();
  const seen = /* @__PURE__ */ new Set();
  const top_commands = [];
  for (const ev of bashEvents) {
    const cmd = ev.tool_input.command;
    if (!seen.has(cmd)) {
      seen.add(cmd);
      top_commands.push(cmd);
      if (top_commands.length >= 10) break;
    }
  }
  const first_lines = await mongo.transcriptLines.find({ session_id, account_id }, { projection: { _id: 0 } }).sort({ seq: 1 }).limit(20).toArray();
  const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
  const last_lines = total > 20 ? (await mongo.transcriptLines.find({ session_id, account_id }, { projection: { _id: 0 } }).sort({ seq: -1 }).limit(20).toArray()).reverse() : [];
  return {
    session: {
      session_id: session.session_id,
      project_path: session.project_path,
      git_origin: session.git_origin ?? null,
      cwd: session.cwd,
      started_at: session.started_at,
      last_seen: session.last_seen
    },
    top_commands,
    first_lines,
    last_lines
  };
}
async function readTranscript({ session_id, offset = 0, limit = 200 }, progressToken, notify) {
  limit = Math.min(limit, MAX_LIMIT);
  const mongo = await getMongo();
  const session = await mongo.sessions.findOne({ session_id, account_id });
  if (!session) throw new Error("session not found");
  const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
  const BATCH = 50;
  const allLines = [];
  for (let batchStart = offset; batchStart < offset + limit; batchStart += BATCH) {
    const batchLimit = Math.min(BATCH, offset + limit - batchStart);
    const lines = await mongo.transcriptLines.find({ session_id, account_id }, { projection: { _id: 0 } }).sort({ seq: 1 }).skip(batchStart).limit(batchLimit).toArray();
    allLines.push(...lines);
    if (progressToken !== void 0 && notify && lines.length > 0) {
      pushProgress(notify, progressToken, allLines.length, total);
    }
    if (lines.length < batchLimit) break;
  }
  return allLines;
}
async function handleToolCall(name, args, meta, notify) {
  switch (name) {
    case "find_sessions":
      return findSessions(args);
    case "get_session_context":
      return getSessionContext(args);
    case "search_commands":
      return searchCommands(args);
    case "read_transcript":
      return readTranscript(args, meta?.progressToken, notify);
    case "restore_session":
      return restoreSession(args, account_id, await getMongo(), config.fileHistoryDir);
    default:
      throw new Error(`unknown tool: ${name}`);
  }
}
var server = http.createServer(async (req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200);
    res.end("ok");
    return;
  }
  if (req.method === "GET" && req.url === "/sse") {
    const sessionId = randomUUID();
    res.writeHead(200, {
      "Content-Type": "text/event-stream",
      "Cache-Control": "no-cache",
      "Connection": "keep-alive"
    });
    res.write(`event: endpoint
data: http://127.0.0.1:${config.mcpPort}/message?sessionId=${sessionId}

`);
    sessions.set(sessionId, res);
    req.on("close", () => sessions.delete(sessionId));
    return;
  }
  if (req.method === "POST" && req.url?.startsWith("/message")) {
    const sessionId = new URL(req.url, "http://x").searchParams.get("sessionId");
    const sseRes = sessionId ? sessions.get(sessionId) : void 0;
    if (!sseRes) {
      res.writeHead(400);
      res.end("unknown session");
      return;
    }
    let body = "";
    req.on("data", (c) => {
      body += c;
    });
    req.on("end", async () => {
      let rpc;
      try {
        rpc = JSON.parse(body);
      } catch {
        res.writeHead(400);
        res.end("invalid json");
        return;
      }
      res.writeHead(202);
      res.end();
      const notify = (msg) => sseWrite(sseRes, msg);
      const reply = await dispatch(rpc, (name, args, meta) => handleToolCall(name, args, meta, notify));
      if (reply) notify(reply);
    });
    return;
  }
  res.writeHead(404);
  res.end();
});
if (process.argv.includes("--stdio")) {
  const notify = (msg) => {
    process.stdout.write(JSON.stringify(msg) + "\n");
  };
  const rl = createInterface({ input: process.stdin });
  rl.on("line", async (line) => {
    if (!line.trim()) return;
    let rpc;
    try {
      rpc = JSON.parse(line);
    } catch {
      notify({ jsonrpc: "2.0", id: null, error: { code: -32700, message: "parse error" } });
      return;
    }
    const reply = await dispatch(rpc, (name, args, meta) => handleToolCall(name, args, meta, notify));
    if (reply) notify(reply);
  });
  rl.on("close", async () => {
    await _mongo?.close();
    process.exit(0);
  });
} else {
  startHttpServer();
}
function startHttpServer() {
  server.on("error", async (e) => {
    if (e.code === "EADDRINUSE") {
      await _mongo?.close();
      process.exit(0);
    }
    console.error("clued mcp error:", e.message);
    await _mongo?.close();
    process.exit(1);
  });
  server.listen(config.mcpPort, "127.0.0.1");
}
var shutdown = async () => {
  server.close();
  await _mongo?.close();
  process.exit(0);
};
process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);
