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
4. Stream large transcript reads progressively using MCP progress notifications.
5. Keep the existing daemon unchanged — recording and search are separate concerns.

---

## Repository Changes

| Path | Status | Responsibility |
|---|---|---|
| `src/mcp.mjs` | Create | MCP HTTP+SSE server — tool registry, request routing, SSE lifecycle |
| `src/config.mjs` | Modify | Add `mcpPort: 8086`, `CLUED_MCP_PORT` env override |
| `src/mongo.mjs` | Modify | Add index on `sessions.git_origin` |
| `hooks/session-start` | Modify | Health-check + self-heal MCP server alongside daemon |
| `skills/clued-setup.md` | Modify | Add step to register `mcpServers.clued` in `~/.claude/settings.json` |
| `test/integration/mcp.test.mjs` | Create | Integration tests — real MongoDB, real HTTP |

---

## Section 1: Architecture

`src/mcp.mjs` is a standalone Node.js ESM process. It shares `loadConfig()` and `createClient()` with the daemon but runs independently — separate PID, separate MongoDB connection, separate port.

**Transport:** MCP HTTP+SSE — the spec's standard persistent transport. The protocol uses two channels:

- `GET /sse` — client holds this open. On connection, the server sends an SSE `endpoint` event carrying the POST URL for this session (e.g., `http://127.0.0.1:8086/message?sessionId=<uuid>`). All subsequent server→client messages (tool results, progress notifications, errors) are pushed down this channel.
- `POST /message?sessionId=<id>` — client sends JSON-RPC 2.0 messages here (tool calls, initialize, ping). Server responds with `202 Accepted` immediately, then pushes the actual result as an SSE `data:` event on the client's open GET channel, routed by `sessionId`.
- `GET /health` — returns 200 `ok` for liveness checks.

Each `GET /sse` connection gets a unique `sessionId` (UUID). The server maps `sessionId → SSE response` to route POST results back to the right client. When a client disconnects, its session is removed.

**Lifecycle:** `session-start` health-checks and self-heals both the daemon and the MCP server independently before triggering backfill — see Section 5 for the updated hook flow.

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

**Output:** Array of session objects (empty array if no matches):
```json
{
  "session_id":    "string",
  "project_path":  "string",
  "git_origin":    "string | null",
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
  "session":      "{ session_id, project_path, git_origin, cwd, started_at, last_seen }",
  "top_commands": "string[] (up to 10 distinct Bash commands, most recent first)",
  "first_lines":  "object[] (first 20 transcript lines)",
  "last_lines":   "object[] (last 20 transcript lines)"
}
```

Returns MCP error `"session not found"` if `session_id` is unknown.

---

### `search_commands`

Searches Bash tool invocations across sessions. Useful for recalling a specific command or flag used in the past.

**Input:**
```json
{
  "pattern":    "string (regex, applied to tool_input.command)",
  "session_id": "string (optional, scope to one session)",
  "git_origin": "string (optional, regex — scope to all sessions for a repo)",
  "limit":      "number (optional, default 20)"
}
```

**Output:** Array of matches (empty array if no matches):
```json
{
  "session_id":   "string",
  "project_path": "string",
  "git_origin":   "string | null",
  "command":      "string",
  "created_at":   "ISO date"
}
```

`project_path` is included so results are human-readable when `git_origin` is null (i.e., sessions not inside a git repository).

---

### `read_transcript`

Returns paginated transcript lines for a session. For large transcripts, the server sends MCP progress notifications (`notifications/progress`) as each page is fetched, so the client sees lines arriving before the full result is ready. The caller includes `_meta.progressToken` in the tool call to opt in to progress events; without it, the server waits for all pages before sending the result.

**Input:**
```json
{
  "session_id": "string",
  "offset":     "number (optional, default 0)",
  "limit":      "number (optional, default 200)",
  "_meta": {
    "progressToken": "string | number (optional — include to receive progress notifications)"
  }
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

**Progress notifications** (sent when `progressToken` is present): each page of fetched lines is sent as a `notifications/progress` event with `{ progressToken, progress, total, data: lines[] }` before the final tool result. The final result contains all lines.

---

## Section 3: Streaming

The MCP HTTP+SSE transport protocol:

1. Client opens `GET /sse`. Server sends an SSE `endpoint` event: `data: http://127.0.0.1:<mcpPort>/message?sessionId=<uuid>`.
2. Client sends JSON-RPC messages via `POST /message?sessionId=<uuid>`. Server responds `202 Accepted` immediately.
3. Server pushes the actual JSON-RPC response as an SSE `data:` event on the client's open GET channel, matched by `sessionId`.
4. Progress notifications (`notifications/progress`) follow the same channel — pushed as SSE `data:` events before the final result.

For `find_sessions`, `get_session_context`, and `search_commands`: single result event, no progress notifications.

For `read_transcript` when `_meta.progressToken` is present: the server fetches lines in batches of 50 from MongoDB, sends a `notifications/progress` event per batch, then sends the final tool result containing all lines. If the client disconnects mid-stream, the MongoDB cursor is aborted and the session is cleaned up.

---

## Section 4: Error Handling

| Scenario | Behaviour |
|---|---|
| MongoDB unreachable at startup | Exit code 1, log to stderr; `session-start` logs warning, exits 0 |
| MongoDB drops during a tool call | Return MCP error with message; server stays alive |
| Tool returns no results | Return empty array — not an error |
| `get_session_context` / `read_transcript` with unknown `session_id` | Return MCP error: `"session not found"` |
| `read_transcript` client disconnects mid-stream | Abort MongoDB cursor, remove session mapping — no crash |
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

**`src/mongo.mjs`** gains one new index in `createClient()`:

```js
db.collection('sessions').createIndex({ git_origin: 1 }),
```

Added to the `Promise.allSettled` block alongside the existing indexes. Supports efficient `git_origin`-filtered queries in `find_sessions` and `search_commands`.

**`hooks/session-start`** updated flow — preserves the existing fast-exit pattern for both services:

1. Read `port` and `mcpPort` from config (via `jq` + env override, same pattern as current hook).
2. If daemon is NOT healthy: spawn daemon detached, poll up to 3s.
3. If MCP server is NOT healthy: spawn MCP server detached, poll up to 3s.
4. Trigger backfill detached.
5. Exit 0.

If both services are already healthy (the common case after first startup), steps 2 and 3 are each a single fast health-check that succeeds immediately, preserving the fast-exit behaviour. Each service is checked and healed independently — a failed MCP server does not prevent backfill from running.

**`skills/clued-setup.md`** gains a new step (after writing `config.json`, before verifying daemon):

> Register the MCP server in `~/.claude/settings.json` under `mcpServers`:
> ```json
> "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
> ```
> Do not duplicate if it already exists.

---

## Section 6: Testing

`test/integration/mcp.test.mjs` — integration tests using a test port (`18086`) and a unique test DB (`clued_mcp_test_<timestamp>`). The test `before` hook spawns the MCP server process (same pattern as `daemon.test.mjs`), waits for `/health`, and creates a direct MongoDB client for seeding data and assertions.

**Tests:**
- `GET /health` returns 200
- `GET /sse` establishes SSE connection and receives `endpoint` event
- `find_sessions` returns sessions matching `git_origin` filter
- `find_sessions` returns sessions matching `project_path` filter
- `find_sessions` returns empty array when no sessions match
- `get_session_context` returns metadata + top commands + first/last lines
- `get_session_context` returns error for unknown `session_id`
- `search_commands` returns matching commands across sessions
- `search_commands` scoped by `git_origin` returns only matching sessions
- `search_commands` returns empty array when pattern matches nothing
- `read_transcript` returns paginated lines
- `read_transcript` with `progressToken` sends progress notifications before final result
- `read_transcript` returns error for unknown `session_id`

---

## Out of Scope

- Full-text search across transcript content (MongoDB text indexes, semantic search) — regex on commands is sufficient for MVP.
- Authentication or access control on the MCP endpoint.
- Exposing the MCP server to non-localhost clients.
- Tool results cached between calls.
