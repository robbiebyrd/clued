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
  hooks.json                           # declares SessionStart hook
  hooks/
    session-start                      # bash: health-check, spawn daemon, trigger backfill
    event-relay                        # bash: curl relay (used by other hook types)
  src/
    config.mjs                         # config loader: env vars > config file > defaults
    daemon.mjs                         # HTTP server entry point
    mongo.mjs                          # MongoClient wrapper, index setup, insert helpers
    tailer.mjs                         # JSONL file tail logic
    enricher.mjs                       # async enrichment loop + enricher registry
    backfill.mjs                       # historical session crawler
  enrichers/
    bash-binaries.mjs                  # example: extracts binary names from Bash ToolUse
    privacy-redact.mjs                 # example: redacts patterns from transcript_lines
  docker-compose.transcripts.yml       # MongoDB dev container (unchanged)
```

---

## Section 1: Plugin Packaging

The plugin follows the Claude Code marketplace-compatible format:

- `package.json` declares `name: "clued"`, `version`, `type: "module"`.
- `hooks.json` wires only the `SessionStart` hook to `hooks/session-start`. All other hook types (PreToolUse, PostToolUse, etc.) remain in the user's `~/.claude/settings.json` pointing at the `event-relay` curl command — the plugin does not own these.
- A `/clued-setup` skill guides first-time configuration and confirms the daemon is reachable.
- The plugin is structured for local install (`/install <path>`) and future marketplace publish.

---

## Section 2: Config Management

Config is loaded once at daemon startup by `src/config.mjs` and injected into all modules. No module reads env vars or files directly.

**Resolution order (highest to lowest priority):**
1. Environment variables
2. `~/.claude/plugins/data/clued/config.json`
3. Hardcoded defaults

**Config shape:**
```json
{
  "mongoUrl":    "mongodb://localhost:27018",
  "dbName":      "claude_sessions",
  "port":        8085,
  "projectsDir": "~/.claude/projects"
}
```

**Environment variable names:**
```
CLUED_MONGO_URL
CLUED_DB_NAME
CLUED_PORT
CLUED_PROJECTS_DIR
```

The `session-start` bash hook reads only `port` from the config file (via `jq`, with fallback to default 8085), then checks env var override. This keeps the hook minimal.

---

## Section 3: Daemon & Self-Healing

`src/daemon.mjs` is the server entry point. It only runs in server mode — it no longer self-spawns. The spawn decision is owned entirely by the `session-start` hook.

**`hooks/session-start` (bash):**
1. Read `port` from config file, apply `CLUED_PORT` env override.
2. `GET /health` — if HTTP 200, done.
3. If no response: spawn daemon detached: `node <plugin-root>/src/daemon.mjs &`
4. Poll `/health` for up to 3 seconds to confirm startup.
5. Trigger backfill detached: `node <plugin-root>/src/backfill.mjs &`

This runs on every `SessionStart` event. No launchd, no cron. The daemon heals naturally each time Claude Code opens a session.

**Daemon internals (`src/daemon.mjs`):**
- Loads config via `src/config.mjs`.
- Connects to MongoDB; exits non-zero on connection failure.
- Starts HTTP server on configured port.
- On each `POST /event`: inserts to `hook_events`, calls `trackSession` (starts tailing transcript if new session), then queues doc for async enrichment.
- `EADDRINUSE` → hands off initial event to the already-running server and exits cleanly.
- Graceful shutdown on `SIGTERM`/`SIGINT`.

---

## Section 4: Enricher Framework

`src/enricher.mjs` runs a background poll loop inside the daemon. It queries for unenriched documents and runs matching enrichers against them asynchronously — entirely after the DB write, never blocking the HTTP path.

**Enricher contract** — every file in `enrichers/` exports:

```js
export const collection = 'hook_events'; // or 'transcript_lines'
export const name = 'bash-binaries';

export function matches(doc) {
  return doc.tool_name === 'Bash' && doc.tool_input?.command;
}

export async function enrich(doc) {
  // return object — keys merged into doc.enriched.<name>
  return { binaries: extractBinaries(doc.tool_input.command) };
}
```

**Registry behaviour:**
- At daemon startup, `enricher.mjs` scans `enrichers/` and dynamically imports all `.mjs` files.
- Poll loop runs every 5 seconds, querying for docs missing `enriched.<enricher-name>`.
- For each match: runs the enricher, then `$set`s `enriched.<name>` on the document.
- Adding an enricher = drop a `.mjs` file in `enrichers/` and restart the daemon.

**Bundled enrichers:**
- `bash-binaries.mjs` — targets `hook_events` where `tool_name === 'Bash'`; parses the command string and writes `enriched.bash-binaries.binaries: string[]`.
- `privacy-redact.mjs` — targets `transcript_lines`; applies configurable regex patterns to `line`, writes sanitized output to `enriched.privacy-redact.redacted_line`. Raw data is never mutated.

---

## Section 5: Historical Backfill Crawler

`src/backfill.mjs` is a standalone, idempotent script triggered by `session-start` on every session open (fire-and-forget, detached).

**Algorithm:**
1. Read `projectsDir` from config (default `~/.claude/projects`).
2. Walk all subdirectories; collect `*.jsonl` files.
3. Derive `session_id` from filename (UUID before `.jsonl`); derive `project_path` by reversing the encoded directory name (`-Users-robbiebyrd-Projects-clued` → `/Users/robbiebyrd/Projects/clued`).
4. For each session: upsert the session doc in `sessions`; read the full file; upsert each line into `transcript_lines` keyed by `{ session_id, seq }`.
5. Bounded concurrency: 5 sessions processed in parallel.
6. Logs one-line summary on completion: `backfill: N sessions, M lines`.

**Idempotency:** upsert by `{ session_id, seq }` means re-runs pick up new lines appended to still-active sessions without duplicating existing ones.

**Scope:** backfill reads current file state only. It does not tail files (daemon's job) and does not start the daemon (hook's job).

---

## Data Model

Three MongoDB collections, unchanged from current implementation:

| Collection | Key fields | Notes |
|---|---|---|
| `sessions` | `session_id` (unique), `transcript_path`, `cwd`, `started_at`, `last_seen` | One doc per session |
| `hook_events` | `session_id`, `created_at`, `enriched.*` | One doc per hook event; enriched fields added async |
| `transcript_lines` | `session_id`, `seq`, `line`, `created_at`, `enriched.*` | One doc per JSONL line; enriched fields added async |

---

## Error Handling

- **Daemon startup failure** (MongoDB unreachable): non-zero exit so the hook can log it; recording silently skipped until next session start.
- **Enricher failure**: logged, `enriched.<name>` left absent so the poll loop retries on next cycle.
- **Backfill failure**: logged per-file; other sessions continue.
- **Hook relay failure** (curl to daemon): already `|| true` — hook never blocks Claude Code.

---

## Out of Scope

- Windows support (bash hooks only; polyglot wrapper can be added later).
- Enricher hot-reload without daemon restart.
- A query/read API on top of MongoDB.
- Forwarding events to non-MongoDB destinations.
