import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// src/backfill.ts
import { readdir, readFile } from "fs/promises";
import { join as join2, basename } from "path";
import { fileURLToPath } from "url";

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

// src/git.ts
import { execFile } from "child_process";
import { promisify } from "util";
var execFileAsync = promisify(execFile);
async function getGitOrigin(cwd) {
  try {
    const { stdout } = await execFileAsync(
      "git",
      ["-C", cwd, "remote", "get-url", "origin"],
      { timeout: 2e3 }
    );
    return stdout.trim() || null;
  } catch {
    return null;
  }
}
async function getGitBranch(cwd) {
  try {
    const { stdout } = await execFileAsync(
      "git",
      ["-C", cwd, "branch", "--show-current"],
      { timeout: 2e3 }
    );
    return stdout.trim() || null;
  } catch {
    return null;
  }
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

// src/backfill.ts
function decodeProjectPath(dirName) {
  return "/" + dirName.slice(1).replaceAll("-", "/");
}
async function processSession(mongo, projectPath, sessionId, filePath, account_id) {
  const now = /* @__PURE__ */ new Date();
  const [git_origin, git_branch] = await Promise.all([
    getGitOrigin(projectPath),
    getGitBranch(projectPath)
  ]);
  const $set = {
    session_id: sessionId,
    project_path: projectPath,
    transcript_path: filePath,
    last_seen: now,
    account_id
  };
  if (git_origin) $set.git_origin = git_origin;
  if (git_branch) $set.git_branch = git_branch;
  await mongo.sessions.updateOne(
    { session_id: sessionId },
    { $set, $setOnInsert: { started_at: now } },
    { upsert: true }
  );
  const content = await readFile(filePath, "utf8");
  const rawLines = content.split("\n").filter((l) => l.trim());
  if (rawLines.length === 0) return 0;
  const ops = rawLines.map((raw, seq) => {
    let line;
    try {
      line = JSON.parse(raw);
    } catch {
      line = { raw };
    }
    return {
      updateOne: {
        filter: { session_id: sessionId, seq },
        update: { $set: { session_id: sessionId, seq, line, account_id }, $setOnInsert: { created_at: now } },
        upsert: true
      }
    };
  });
  await mongo.transcriptLines.bulkWrite(ops, { ordered: false });
  return rawLines.length;
}
async function backfill(config, mongo, account_id) {
  const dirs = await readdir(config.projectsDir, { withFileTypes: true }).catch(() => []);
  const sessions = [];
  for (const dir of dirs.filter((d) => d.isDirectory())) {
    const projectPath = decodeProjectPath(dir.name);
    const dirPath = join2(config.projectsDir, dir.name);
    const files = await readdir(dirPath).catch(() => []);
    for (const file of files.filter((f) => f.endsWith(".jsonl"))) {
      sessions.push({
        projectPath,
        sessionId: basename(file, ".jsonl"),
        filePath: join2(dirPath, file)
      });
    }
  }
  let totalLines = 0;
  for (let i = 0; i < sessions.length; i += 5) {
    const batch = sessions.slice(i, i + 5);
    const results = await Promise.allSettled(
      batch.map((s) => processSession(mongo, s.projectPath, s.sessionId, s.filePath, account_id))
    );
    for (let j = 0; j < results.length; j++) {
      if (results[j].status === "fulfilled") totalLines += results[j].value;
      else console.error(`clued backfill error (${batch[j].filePath}):`, results[j].reason?.message);
    }
  }
  console.log(`clued backfill: ${sessions.length} sessions, ${totalLines} lines upserted`);
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const config = loadConfig();
  const account_id = readAccountId(config.claudeAppConfigPath);
  const mongo = await createClient(config);
  try {
    await backfill(config, mongo, account_id);
  } finally {
    await mongo.close();
  }
}
export {
  backfill,
  decodeProjectPath
};
