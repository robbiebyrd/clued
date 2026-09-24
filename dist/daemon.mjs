import { createRequire } from 'module'; const require = createRequire(import.meta.url);

// src/daemon.ts
import http from "http";
import { join as join4, dirname as dirname3 } from "path";
import { fileURLToPath } from "url";

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

// src/tailer.ts
import fs from "fs";
import readline from "readline";
function tailFile(filePath, onLine) {
  let pos = 0;
  let stopped = false;
  let watcher = null;
  let pollTimer = null;
  let inFlight = false;
  const read = () => {
    if (stopped || inFlight) return;
    inFlight = true;
    try {
      const size = fs.statSync(filePath).size;
      if (size <= pos) {
        inFlight = false;
        return;
      }
      const rl = readline.createInterface({
        input: fs.createReadStream(filePath, { start: pos, end: size - 1 }),
        crlfDelay: Infinity
      });
      const batch = [];
      rl.on("line", (l) => {
        if (l.trim()) batch.push(l);
      });
      rl.on("error", () => {
        rl.close();
        inFlight = false;
      });
      rl.on("close", () => {
        pos = size;
        inFlight = false;
        batch.forEach(onLine);
      });
    } catch {
      inFlight = false;
    }
  };
  const start = () => {
    if (stopped) return;
    if (!fs.existsSync(filePath)) {
      setTimeout(start, 500);
      return;
    }
    read();
    try {
      watcher = fs.watch(filePath, read);
    } catch {
    }
    pollTimer = setInterval(read, 2e3);
  };
  start();
  return {
    stop() {
      stopped = true;
      if (watcher) {
        watcher.close();
        watcher = null;
      }
      if (pollTimer) {
        clearInterval(pollTimer);
        pollTimer = null;
      }
    }
  };
}

// src/enricher.ts
import { readdir } from "fs/promises";
import { join as join2, extname } from "path";
import { pathToFileURL } from "url";
async function loadEnrichers(enrichersDir, config2) {
  let files;
  try {
    files = await readdir(enrichersDir);
  } catch {
    return [];
  }
  const EXTS = /* @__PURE__ */ new Set([".ts", ".mjs"]);
  const enrichers2 = [];
  for (const file of files.filter((f) => EXTS.has(extname(f)))) {
    const mod = await import(pathToFileURL(join2(enrichersDir, file)).href);
    if (mod.enabled === false) continue;
    if ((config2.disabledEnrichers ?? []).includes(mod.name)) continue;
    enrichers2.push(mod);
  }
  return enrichers2;
}
function startEnrichmentLoop(mongo2, enrichers2) {
  const timer = setInterval(async () => {
    for (const enricher of enrichers2) {
      const coll = mongo2.db.collection(enricher.collection);
      const failedKey = `enrichments.${enricher.name}_failed`;
      const doneKey = `enrichments.${enricher.name}`;
      const limit = enricher.batchLimit ?? 100;
      const docs = await coll.find({ [doneKey]: { $exists: false }, [failedKey]: { $exists: false } }).limit(limit).toArray().catch((err) => {
        console.error("clued enricher query failed:", err.message);
        return [];
      });
      for (const doc of docs) {
        const d = doc;
        if (!enricher.matches(d)) continue;
        try {
          const result = await enricher.enrich(d, mongo2);
          await coll.updateOne({ _id: doc._id }, { $set: { [doneKey]: result } });
        } catch (err) {
          const e = err;
          console.error(`clued enricher "${enricher.name}" failed on ${doc._id}:`, e.message);
          await coll.updateOne({ _id: doc._id }, {
            $set: { [failedKey]: { message: e.message, at: /* @__PURE__ */ new Date() } }
          });
        }
      }
    }
  }, 5e3);
  return { stop: () => clearInterval(timer) };
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

// src/host.ts
import { hostname as osHostname, networkInterfaces } from "os";
function readHostInfo() {
  const hostname = osHostname();
  const ifaces = networkInterfaces();
  for (const iface of Object.values(ifaces)) {
    if (!iface) continue;
    const entry = iface.find((a) => a.family === "IPv4" && !a.internal);
    if (entry) return { hostname, ip: entry.address, mac: entry.mac };
  }
  return { hostname, ip: null, mac: null };
}

// src/wal.ts
import { appendFileSync, existsSync, mkdirSync, readFileSync as readFileSync3, writeFileSync } from "fs";
import { dirname as dirname2 } from "path";
function appendToWal(walPath, event) {
  mkdirSync(dirname2(walPath), { recursive: true });
  appendFileSync(walPath, JSON.stringify(event) + "\n");
}
async function flushWal(walPath, insert) {
  if (!existsSync(walPath)) return;
  const lines = readFileSync3(walPath, "utf8").split("\n").filter(Boolean);
  if (lines.length === 0) return;
  const failed = [];
  for (const line of lines) {
    let doc;
    try {
      doc = JSON.parse(line);
    } catch {
      continue;
    }
    try {
      await insert(doc);
    } catch {
      failed.push(line);
    }
  }
  writeFileSync(walPath, failed.length > 0 ? failed.join("\n") + "\n" : "");
}

// src/artifact-watcher.ts
import fs2 from "fs";
import { join as join3 } from "path";
function watchDir(dirPath, onChange) {
  const mtimes = /* @__PURE__ */ new Map();
  const check = () => {
    let entries;
    try {
      entries = fs2.readdirSync(dirPath, { withFileTypes: true });
    } catch {
      return;
    }
    for (const entry of entries) {
      if (!entry.isFile()) continue;
      let mtime;
      try {
        mtime = fs2.statSync(join3(dirPath, entry.name)).mtimeMs;
      } catch {
        continue;
      }
      if (mtimes.get(entry.name) !== mtime) {
        mtimes.set(entry.name, mtime);
        onChange(entry.name, join3(dirPath, entry.name));
      }
    }
  };
  let watching = false;
  const tryWatch = () => {
    if (watching || !fs2.existsSync(dirPath)) return;
    watching = true;
    try {
      const w = fs2.watch(dirPath, () => check());
      w.on("error", () => {
      });
    } catch {
    }
    check();
  };
  const readyInterval = setInterval(() => {
    if (!fs2.existsSync(dirPath)) return;
    clearInterval(readyInterval);
    tryWatch();
  }, 500);
  tryWatch();
  setInterval(check, 2e3);
}
function watchArtifactDirs(session_id, sessionDir, fileHistoryPath, mongo2, account_id2, host2) {
  const subagentsDir = join3(sessionDir, "subagents");
  const toolResultsDir = join3(sessionDir, "tool-results");
  const subagentSeqs = /* @__PURE__ */ new Map();
  watchDir(subagentsDir, (filename, fullPath) => {
    if (filename.endsWith(".jsonl")) {
      const subagent_id = filename.replace(/\.jsonl$/, "");
      if (subagentSeqs.has(subagent_id)) return;
      const seqRef = { value: 0 };
      subagentSeqs.set(subagent_id, seqRef);
      tailFile(fullPath, (raw) => {
        let line;
        try {
          line = JSON.parse(raw);
        } catch {
          line = { raw };
        }
        const seq = seqRef.value++;
        mongo2.subagentLines.updateOne(
          { session_id, subagent_id, seq },
          {
            $set: { session_id, subagent_id, seq, line, account_id: account_id2, host: host2 },
            $setOnInsert: { created_at: /* @__PURE__ */ new Date() }
          },
          { upsert: true }
        ).catch(() => {
        });
      });
    } else if (filename.endsWith(".meta.json")) {
      let content;
      try {
        content = fs2.readFileSync(fullPath, "utf8");
      } catch {
        return;
      }
      mongo2.blobs.updateOne(
        { session_id, blob_type: "subagent-meta", name: filename },
        {
          $set: { content, encoding: "utf8", account_id: account_id2 },
          $setOnInsert: { created_at: /* @__PURE__ */ new Date() }
        },
        { upsert: true }
      ).catch(() => {
      });
    }
  });
  watchDir(toolResultsDir, (filename, fullPath) => {
    let content;
    try {
      content = fs2.readFileSync(fullPath, "utf8");
    } catch {
      return;
    }
    mongo2.blobs.updateOne(
      { session_id, blob_type: "tool-result", name: filename },
      {
        $set: { content, encoding: "utf8", account_id: account_id2 },
        $setOnInsert: { created_at: /* @__PURE__ */ new Date() }
      },
      { upsert: true }
    ).catch(() => {
    });
  });
  watchDir(fileHistoryPath, (filename, fullPath) => {
    let buf;
    try {
      buf = fs2.readFileSync(fullPath);
    } catch {
      return;
    }
    mongo2.blobs.updateOne(
      { session_id, blob_type: "file-history", name: filename },
      {
        $set: { content: buf.toString("base64"), encoding: "base64", account_id: account_id2 },
        $setOnInsert: { created_at: /* @__PURE__ */ new Date() }
      },
      { upsert: true }
    ).catch(() => {
    });
  });
}

// src/daemon.ts
var __filename = fileURLToPath(import.meta.url);
var __dir = dirname3(__filename);
var ENRICHERS_DIR = __filename.endsWith(".ts") ? join4(__dir, "..", "enrichers") : join4(__dir, "enrichers");
var config = loadConfig();
var account_id = readAccountId(config.claudeAppConfigPath);
var host = readHostInfo();
var mongo = await createClient(config).catch((err) => {
  console.error("clued daemon: MongoDB connection failed:", err.message);
  process.exit(1);
});
var enrichers = await loadEnrichers(ENRICHERS_DIR, config);
var loop = startEnrichmentLoop(mongo, enrichers);
var walInsert = (doc) => mongo.hookEvents.insertOne({ ...doc, created_at: doc.created_at ?? /* @__PURE__ */ new Date() }).then(() => void 0);
await flushWal(config.walPath, walInsert).catch(() => {
});
var walFlushInterval = setInterval(() => {
  flushWal(config.walPath, walInsert).catch(() => {
  });
}, 6e4);
var tracked = /* @__PURE__ */ new Map();
async function trackSession({ session_id, transcript_path, cwd } = {}) {
  if (!session_id) return;
  if (tracked.has(session_id)) {
    const state2 = tracked.get(session_id);
    if (!state2.gitOriginFound && cwd) {
      const [gitOrigin, gitBranch] = await Promise.all([getGitOrigin(cwd), getGitBranch(cwd)]);
      if (gitOrigin) {
        state2.gitOriginFound = true;
        const $set = { git_origin: gitOrigin };
        if (gitBranch) $set.git_branch = gitBranch;
        mongo.sessions.updateOne(
          { session_id, git_origin: { $exists: false } },
          { $set }
        ).catch(() => {
        });
      }
    }
    return;
  }
  const seqRef = { value: 0 };
  const state = { gitOriginFound: false };
  tracked.set(session_id, state);
  const now = /* @__PURE__ */ new Date();
  Promise.all([
    cwd ? getGitOrigin(cwd) : Promise.resolve(null),
    cwd ? getGitBranch(cwd) : Promise.resolve(null)
  ]).then(([git_origin, git_branch]) => {
    if (git_origin) state.gitOriginFound = true;
    const $set = { session_id, transcript_path, cwd, last_seen: now, account_id, host };
    if (git_origin) $set.git_origin = git_origin;
    if (git_branch) $set.git_branch = git_branch;
    mongo.sessions.updateOne(
      { session_id },
      { $set, $setOnInsert: { started_at: now } },
      { upsert: true }
    ).catch(() => {
    });
  });
  if (!transcript_path) return;
  tailFile(transcript_path, (raw) => {
    let line;
    try {
      line = JSON.parse(raw);
    } catch {
      line = { raw };
    }
    const seq = seqRef.value++;
    mongo.transcriptLines.updateOne(
      { session_id, seq },
      { $set: { session_id, seq, line, account_id, host }, $setOnInsert: { created_at: /* @__PURE__ */ new Date() } },
      { upsert: true }
    ).catch(() => {
    });
  });
  const sessionDir = join4(dirname3(transcript_path), session_id);
  const fileHistoryPath = join4(config.fileHistoryDir, session_id);
  watchArtifactDirs(session_id, sessionDir, fileHistoryPath, mongo, account_id, host);
}
var server = http.createServer((req, res) => {
  if (req.method === "GET" && req.url === "/health") {
    res.writeHead(200);
    res.end("ok");
    return;
  }
  if (req.method !== "POST" || req.url !== "/event") {
    res.writeHead(404);
    res.end();
    return;
  }
  let body = "";
  req.on("data", (c) => {
    body += c;
  });
  req.on("end", async () => {
    try {
      const data = JSON.parse(body);
      trackSession(data);
      if (data.session_id) {
        mongo.sessions.updateOne({ session_id: data.session_id, account_id }, { $set: { last_seen: /* @__PURE__ */ new Date() } }).catch(() => {
        });
      }
      try {
        await mongo.hookEvents.insertOne({ ...data, account_id, host, created_at: /* @__PURE__ */ new Date() });
      } catch {
        appendToWal(config.walPath, { ...data, account_id, host, created_at: (/* @__PURE__ */ new Date()).toISOString() });
      }
      res.writeHead(200);
      res.end("ok");
    } catch (e) {
      res.writeHead(400);
      res.end(e.message);
    }
  });
});
server.on("error", async (e) => {
  if (e.code === "EADDRINUSE") {
    await mongo.close();
    process.exit(0);
  }
  console.error("clued daemon error:", e.message);
  await mongo.close();
  process.exit(1);
});
server.listen(config.port, "127.0.0.1");
var shutdown = async () => {
  clearInterval(walFlushInterval);
  server.close();
  loop.stop();
  await mongo.close();
  process.exit(0);
};
process.on("SIGTERM", shutdown);
process.on("SIGINT", shutdown);
