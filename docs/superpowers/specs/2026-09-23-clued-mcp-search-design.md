# clued MCP Search Server Design

**Date:** 2026-09-23
**Author:** Robbie Byrd

---

## Overview

`clued` gains a persistent MCP server (`src/mcp.mjs`) that exposes session history search tools to Claude Code. Claude Code discovers and autonomously calls these tools when it needs cross-machine context — for example, when resuming work on a repository that was last touched on a different computer.

The server uses the MCP HTTP+SSE transport, runs alongside the existing daemon, and is self-healed by the `session-start` hook.

---

## Goals

1. Let Claude Code autonomously search past sessions by project, git origin, and command history.
2. Support the cross-machine continuity use case: same git remote → find relevant past sessions regardless of which machine ran them.
3. Start with excerpts and summaries; allow drill-down to full transcript on demand.
4. Stream large transcript reads progressively over SSE rather than blocking on a full query.
5. Keep the existing daemon unchanged — recording and search are separate concerns.

---

## Repository Changes

| Path | Status | Responsibility |
|---|---|---|
| `src/mcp.mjs` | Create | MCP HTTP+SSE server — tool registry, request routing, SSE lifecycle |
| `src/config.mjs` | Modify | Add `mcpPort: 8086`, `CLUED_MCP_PORT` env override |
| `hooks/session-start` | Modify | Health-check + self-heal MCP server alongside daemon |
| `skills/clued-setup.md` | Modify | Add step to register `mcpServers.clued` in `~/.claude/settings.json` |
| `test/integration/mcp.test.mjs` | Create | Integration tests — real MongoDB, real HTTP |

---

## Section 1: Architecture

`src/mcp.mjs` is a standalone Node.js ESM process. It shares `loadConfig()` and `createClient()` with the daemon but runs independently — separate PID, separate MongoDB connection, separate port.

**Transport:** MCP HTTP+SSE (the spec's standard persistent transport).
- `GET /sse` — client holds this open; server pushes tool results and notifications down it.
- `POST /mcp` — client sends tool call requests here; server responds over the SSE stream.
- `GET /health` — returns 200 `ok` for liveness checks.

**Lifecycle:** `session-start` spawns and health-checks the MCP server immediately after the daemon is confirmed healthy, before triggering backfill. Same self-healing pattern: `GET /health` → if down, spawn detached → poll up to 3s → log warning and exit 0 if unreachable.

**Registration:** Users add the server to `~/.claude/settings.json` via `clued-setup`:
```json
"mcpServers": {
  "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
}
```

**Config:** `mcpPort` defaults to `8086`. Overridable via `CLUED_MCP_PORT` env var or `mcpPort` in `config.json`.

---

## Section 2: MCP Tools

### `find_sessions`

Finds sessions matching a project or repository. `git_origin` is the preferred cross-machine identifier — the git remote URL is stable across machines, whereas `project_path` is machine-local.

**Input:**
```json
{
  "project_path": "string (optional, regex, case-insensitive)",
  "git_origin":   "string (optional, regex, case-insensitive)",
  "query":        "string (optional, searches project_path + cwd)",
  "limit":        "number (optional, default 10)"
}
```

**Output:** Array of session objects:
```json
{
  "session_id":    "string",
  "project_path":  "string",
  "git_origin":    "string",
  "cwd":           "string",
  "started_at":    "ISO date",
  "last_seen":     "ISO date",
  "event_count":   "number"
}
```

---

### `get_session_context`

Returns a summary of a session — enough to understand what was done without loading the full transcript. This is the primary "excerpt first" tool.

**Input:**
```json
{ "session_id": "string" }
```

**Output:**
```json
{
  "session":       "{ session_id, project_path, git_origin, cwd, started_at, last_seen }",
  "top_commands":  "string[] (up to 10 distinct Bash commands, most recent first)",
  "first_lines":   "object[] (first 20 transcript lines)",
  "last_lines":    "object[] (last 20 transcript lines)"
}
```

Returns MCP error `"session not found"` if `session_id` is unknown.

---

### `search_commands`

Searches Bash tool invocations across sessions. Useful for recalling a specific command or flag used in the past.

**Input:**
```json
{
  "pattern":     "string (regex, applied to tool_input.command)",
  "session_id":  "string (optional, scope to one session)",
  "git_origin":  "string (optional, regex — scope to all sessions for a repo)",
  "limit":       "number (optional, default 20)"
}
```

**Output:** Array of matches:
```json
{
  "session_id":  "string",
  "git_origin":  "string",
  "command":     "string",
  "created_at":  "ISO date"
}
```

---

### `read_transcript`

Returns paginated transcript lines for a session. Over SSE, pages are streamed progressively as they are fetched from MongoDB — Claude Code receives the first page before the full query completes.

**Input:**
```json
{
  "session_id": "string",
  "offset":     "number (optional, default 0)",
  "limit":      "number (optional, default 200)"
}
```

**Output:** Array of transcript line objects:
```json
{
  "seq":        "number",
  "line":       "object (parsed JSONL content)",
  "created_at": "ISO date"
}
```

Returns MCP error `"session not found"` if `session_id` is unknown.

---

## Section 3: Streaming

The MCP HTTP+SSE transport uses two channels:

- The `GET /sse` channel is held open by the client. The server pushes all tool results, errors, and notifications down this channel as SSE `data:` events.
- The `POST /mcp` channel accepts tool call requests from the client.

For `find_sessions`, `get_session_context`, and `search_commands`, results are a single JSON payload sent as one SSE event.

For `read_transcript`, the server fetches lines in pages from MongoDB and emits each page as a partial SSE event before the full result completes. This allows Claude Code to begin reading immediately for large transcripts.

The server maintains one SSE connection per connected client. No state is held between connections — a reconnecting client gets a fresh session.

---

## Section 4: Error Handling

| Scenario | Behaviour |
|---|---|
| MongoDB unreachable at startup | Exit code 1, log to stderr; `session-start` logs warning, exits 0 |
| MongoDB drops during a tool call | Return MCP error with message; server stays alive |
| Tool returns no results | Return empty array — not an error |
| `get_session_context` / `read_transcript` with unknown `session_id` | Return MCP error: `"session not found"` |
| `read_transcript` client disconnects mid-stream | Abort MongoDB cursor, clean up — no crash |
| `EADDRINUSE` on startup | Exit 0 — another instance won the race |
| Malformed tool call input | Return MCP error with validation message |

---

## Section 5: Configuration Changes

**`src/config.mjs`** gains one new field:

```json
{
  "mcpPort": 8086
}
```

Environment variable override: `CLUED_MCP_PORT`.

**`hooks/session-start`** updated flow:
1. Read `port` and `mcpPort` from config.
2. Health-check daemon → spawn if down → poll.
3. Health-check MCP server → spawn if down → poll.
4. Trigger backfill detached.
5. Exit 0.

**`skills/clued-setup.md`** gains a new step (after writing `config.json`, before verifying daemon):

> Register the MCP server in `~/.claude/settings.json` under `mcpServers`:
> ```json
> "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
> ```
> Do not duplicate if it already exists.

---

## Section 6: Testing

`test/integration/mcp.test.mjs` — integration tests using a test port (`18086`) and a unique test DB (`clued_mcp_test_<timestamp>`). The test `before` hook spawns the MCP server process (same pattern as `daemon.test.mjs`), waits for `/health`, and creates a direct MongoDB client for assertions.

**Tests:**
- `GET /health` returns 200
- `GET /sse` establishes SSE connection (receives initial protocol handshake)
- `find_sessions` returns sessions matching `git_origin` filter
- `find_sessions` returns sessions matching `project_path` filter
- `get_session_context` returns metadata + commands + first/last lines
- `get_session_context` returns error for unknown session_id
- `search_commands` returns matching commands across sessions
- `search_commands` scoped by `git_origin` returns only matching sessions
- `read_transcript` returns paginated lines
- `read_transcript` returns error for unknown session_id

---

## Out of Scope

- Full-text search across transcript content (MongoDB text indexes, semantic search) — regex on commands is sufficient for MVP.
- Authentication or access control on the MCP endpoint.
- Exposing the MCP server to non-localhost clients.
- Tool results cached between calls.
