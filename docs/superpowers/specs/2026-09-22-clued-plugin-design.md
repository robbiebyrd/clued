# clued Plugin Design

**Date:** 2026-09-22  
**Author:** Robbie Byrd

---

## Overview

`clued` is a Claude Code plugin that mirrors all Claude Code session data (hook events and session transcripts) to a MongoDB database in real time. It installs its own hooks, self-heals its background daemon, backfills historical sessions on startup, and provides a drop-in enricher framework for post-processing stored data.

---

## Goals

1. Make MongoDB connection config user-configurable (env vars + config file).
2. Package as a proper Claude Code plugin with auto-installing hooks.
3. Backfill historical sessions on every plugin startup.
4. Keep the recording daemon alive without OS-level service management.
5. Provide a simple, drop-in enricher/parser framework for hook events and transcript lines.

---

## Repository Structure

```
clued/
  package.json                         # name: "clued", type: "module"
  hooks.json                           # declares SessionStart hook only
  hooks/
    session-start                      # bash: health-check, spawn daemon, trigger backfill
    event-relay                        # bash: curl relay for all other hook types
  src/
    config.mjs                         # config loader: env vars > config file > defaults
    daemon.mjs                         # HTTP server entry point
    mongo.mjs                          # MongoClient wrapper, index setup, insert helpers
    tailer.mjs                         # JSONL file tail logic
    enricher.mjs                       # async enrichment loop + enricher registry
    backfill.mjs                       # historical session crawler
  enrichers/
    bash-binaries.mjs                  # example: extracts binary names from Bash ToolUse
    privacy-redact.mjs                 # example (disabled by default): redacts patterns from transcript_lines
  docker-compose.transcripts.yml       # MongoDB dev container (unchanged)
```

---

## Section 1: Plugin Packaging

The plugin follows the Claude Code marketplace-compatible format:

- `package.json` declares `name: "clued"`, `version`, `type: "module"`.
- `hooks.json` wires only the `SessionStart` hook:

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "${CLAUDE_PLUGIN_ROOT}/hooks/session-start",
            "async": true
          }
        ]
      }
    ]
  }
}
```

- All other hook types remain in the user's `~/.claude/settings.json` pointing at `event-relay` — the plugin does not own these at install time. The `/clued-setup` skill adds them during first-time setup.
- `hooks.json` is intentionally scoped to `SessionStart` only. Future hook types owned by the plugin can be added to `hooks.json`; hook types that the user must opt into (relay hooks) stay in user settings and are managed by `/clued-setup`.
- A `/clued-setup` skill guides first-time configuration: writes `config.json`, appends relay hooks for `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`, `UserPromptSubmit`, `Stop`, `SubagentStop`, `WorktreeCreate`, `WorktreeRemove`, `InstructionsLoaded`, `CwdChanged`, and `FileChanged` to `~/.claude/settings.json` (each pointing at `event-relay`), and confirms the daemon is reachable.
- The plugin is structured for local install (`/install <path>`) and future marketplace publish.

---

## Section 2: Config Management

Config is loaded once at daemon startup by `src/config.mjs` and injected into all modules. No module reads env vars or the config file directly. All `~` paths are expanded via `os.homedir()` — Node.js does not expand `~` natively.

**Resolution order (highest to lowest priority):**
1. Environment variables
2. `~/.claude/plugins/data/clued/config.json`
3. Hardcoded defaults

**Config shape:**
```json
{
  "mongoUrl":         "mongodb://localhost:27018",
  "dbName":           "claude_sessions",
  "port":             8085,
  "projectsDir":      "~/.claude/projects",
  "disabledEnrichers": []
}
```

**Environment variable names:**
```
CLUED_MONGO_URL
CLUED_DB_NAME
CLUED_PORT
CLUED_PROJECTS_DIR
```

The `session-start` bash hook reads only `port` from the config file (via `jq`, falling back to 8085 if the file is absent or `jq` is unavailable), then applies the `CLUED_PORT` env var override if set.

---

## Section 2a: `event-relay` Hook

`hooks/event-relay` is the bash script wired to all non-SessionStart hooks (PreToolUse, PostToolUse, etc.). It reads the port using the same logic as `session-start` (jq + env override) and POSTs the stdin payload to the daemon:

```bash
#!/usr/bin/env bash
PORT="${CLUED_PORT:-$(jq -r '.port // 8085' "${HOME}/.claude/plugins/data/clued/config.json" 2>/dev/null || echo 8085)}"
/usr/bin/curl -sf -X POST "http://127.0.0.1:${PORT}/event" \
  -H "Content-Type: application/json" \
  --data-binary @- 2>/dev/null || true
```

- Always exits 0 (`|| true`) — hook failures must never block Claude Code.
- Reads stdin (the hook event JSON) and forwards it verbatim to the daemon.
- If the daemon is not running the curl fails silently; the `session-start` hook will relaunch the daemon on the next session open.

---

## Section 3: Daemon & Self-Healing

`src/daemon.mjs` is the server entry point. It only runs in server mode — it does not self-spawn. The spawn decision is owned entirely by the `session-start` hook.

**`hooks/session-start` (bash):**
1. Read `port` from config file via `jq`; apply `CLUED_PORT` env override.
2. `GET http://127.0.0.1:${PORT}/health` — if HTTP 200, jump to step 5.
3. Spawn daemon detached: `node "${CLAUDE_PLUGIN_ROOT}/src/daemon.mjs" &`
4. Poll `/health` every 200ms for up to 3 seconds. If still unreachable after 3 seconds, log `clued: daemon failed to start` to stderr and exit 0 (non-blocking).
5. Trigger backfill detached: `node "${CLAUDE_PLUGIN_ROOT}/src/backfill.mjs" &`

Exit code is always 0 — this hook must never block Claude Code from starting.

**Daemon internals (`src/daemon.mjs`):**
- Loads config via `src/config.mjs`.
- Connects to MongoDB; exits with code 1 on connection failure (logged to stderr).
- Starts HTTP server on configured `port`.
- On each `POST /event`: inserts to `hook_events`, calls `trackSession` (starts tailing the transcript file if this is a new session), then notifies the enricher loop.
- `EADDRINUSE` → the initial event was passed on stdin and has not yet been inserted (the server never started). The daemon performs a `POST /event` HTTP call to the already-running server with that event, then exits 0. This is the only IPC between instances — no pipes, no shared state. There is no double-insert risk because the failed server never wrote to MongoDB.
- Graceful shutdown on `SIGTERM`/`SIGINT`: closes HTTP server, flushes pending enrichments, closes MongoDB connection.
- `last_seen` on the session doc is updated on every incoming event (not just the first), so it reflects actual last activity.

**`src/tailer.mjs` exports:**

```js
// Begins tailing filePath from byte offset 0. Calls onLine(rawString) for
// each non-empty line. Polls every 2s and uses fs.watch where available.
// Returns a stop() function that halts the tail.
export function tailFile(filePath, onLine): { stop: () => void }
```

Seq-counter ownership: the caller (`daemon.mjs` / `trackSession`) maintains a per-session `seq` counter as a closure variable and passes it to the MongoDB insert. `tailer.mjs` is stateless with respect to seq — it only delivers raw line strings.

---

## Section 4: Enricher Framework

`src/enricher.mjs` runs a background poll loop inside the daemon. It queries for unenriched documents and processes them asynchronously — entirely after the DB write, never blocking the HTTP path.

**Enricher contract** — every file in `enrichers/` exports:

```js
export const collection = 'hook_events'; // or 'transcript_lines'
export const name = 'bash-binaries';
export const enabled = true;             // set false to ship disabled by default

export function matches(doc) {
  return doc.tool_name === 'Bash' && doc.tool_input?.command;
}

export async function enrich(doc) {
  // return an object — keys are merged into doc.enriched.<name>
  return { binaries: extractBinaries(doc.tool_input.command) };
}
```

**Registry behaviour:**
- At daemon startup, `enricher.mjs` scans `enrichers/` and dynamically imports all `.mjs` files.
- Enrichers where `enabled === false` or whose `name` appears in the config `disabledEnrichers` array are skipped entirely.
- Poll loop runs every 5 seconds, querying for docs where `enriched.<name>` is absent AND `enriched.<name>_failed` is absent. Each query is capped at **100 documents per enricher per cycle** to bound query cost on large backlogs.
- For each match: runs the enricher, then `$set`s `enriched.<name>` on the document.
- **Circuit breaker:** if `enrich()` throws, the error is logged and `enriched.<name>_failed: { message: string, at: ISODate }` is written to the document. That document is excluded from all future poll cycles for that enricher. Errors do not retry indefinitely.
- Adding an enricher = drop a `.mjs` file in `enrichers/` and restart the daemon.
- Disabling an enricher without deleting it: set `export const enabled = false` in the file, or add its `name` to `disabledEnrichers` in `config.json`.

**Bundled enrichers:**
- `bash-binaries.mjs` (`enabled: true`) — targets `hook_events` where `tool_name === 'Bash'`; parses the command string and writes `enriched.bash-binaries.binaries: string[]`.
- `privacy-redact.mjs` (`enabled: false`) — ships disabled by default; targets `transcript_lines`; applies hardcoded regex patterns for common secrets (API keys, email addresses) to `line`; writes sanitized output to `enriched.privacy-redact.redacted_line`. Raw `line` data is never mutated. Users opt in by setting `enabled: true` or removing it from `disabledEnrichers`.

---

## Section 5: Historical Backfill Crawler

`src/backfill.mjs` is a standalone, idempotent script triggered by `session-start` on every session open (fire-and-forget, detached).

**Algorithm:**
1. Load config via `src/config.mjs`; expand `projectsDir` (resolves `~` via `os.homedir()`).
2. Walk all immediate subdirectories of `projectsDir`; collect `*.jsonl` files.
3. For each file:
   - `session_id` = filename without `.jsonl` extension (a UUID).
   - `project_path` = decode the directory name. Claude Code encodes the absolute path by replacing each `/` with `-` (including the leading slash), so `-Users-rob-byrd-Projects-foo` decodes as `/Users/rob-byrd/Projects/foo`. The decode rule is: replace all `-` with `/`. **Known limitation:** path components containing literal hyphens are ambiguous with path separators; such paths are decoded on a best-effort basis (the first character is always a `/` so the leading `-` is unambiguous). Paths that produce a non-existent directory after decoding are skipped with a warning.
4. For each session: upsert the session doc in `sessions`; read all lines from the file sequentially; upsert each line into `transcript_lines` keyed by `{ session_id, seq }` where `seq` is the 0-based line index.
5. Bounded concurrency: 5 sessions processed in parallel.
6. Logs one-line summary on completion: `clued backfill: N sessions, M lines upserted`.

**Idempotency:** upsert by `{ session_id, seq }` means re-runs pick up new lines appended to still-active sessions without duplicating existing ones.

**Scope:** backfill reads current file state only. It does not tail files (daemon's job) and does not start the daemon (hook's job).

---

## Data Model

Three MongoDB collections:

| Collection | Key fields | Notes |
|---|---|---|
| `sessions` | `session_id` (unique index), `transcript_path` (absolute expanded path), `cwd`, `project_path`, `started_at`, `last_seen` | One doc per session; `last_seen` updated on every event; `transcript_path` is always stored as an absolute path with `~` resolved |
| `hook_events` | `session_id` (index), `created_at` (index), `enriched.*` | One doc per hook event; `enriched.<name>` and `enriched.<name>_failed` added async |
| `transcript_lines` | `session_id` + `seq` (compound unique index), `line`, `created_at`, `enriched.*` | One doc per JSONL line; same enriched field pattern |

---

## Error Handling

| Scenario | Behaviour |
|---|---|
| MongoDB unreachable at daemon start | Exit code 1, log to stderr; hook logs warning, exits 0 |
| Hook relay curl failure | `\|\| true` — always silent, always exits 0 |
| Daemon `/health` timeout (3s) | Log `clued: daemon failed to start` to stderr, exit 0 |
| Enricher `enrich()` throws | Log error, set `enriched.<name>_failed: true`, skip doc permanently |
| Backfill file read error | Log per-file warning, continue with remaining sessions |
| `EADDRINUSE` on daemon start | POST initial event to running server via HTTP, exit 0 |

---

## Out of Scope

- Windows support (bash hooks only; polyglot wrapper can be added later).
- Enricher hot-reload without daemon restart.
- A query/read API on top of MongoDB.
- Forwarding events to non-MongoDB destinations.
- Enricher retry with exponential backoff (circuit breaker is sufficient for now).
