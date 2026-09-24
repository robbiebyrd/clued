# Session Portability: Cross-Machine Resume

**Date:** 2026-09-23  
**Status:** Approved for implementation

## Problem

Claude Code sessions live entirely on the machine that ran them. Transcripts, subagent conversations, tool-result blobs, and file-history backups are all local. When a developer switches machines — or wants to hand a session to a colleague — none of that context travels with them.

clued already mirrors transcript lines and hook events to MongoDB. With a shared cloud MongoDB instance both machines can reach, the missing piece is (a) capturing the remaining on-disk artifacts in real time and (b) a `restore_session` MCP tool that reconstructs the full session directory tree on demand.

## Scope

Full fidelity in one build. All four artifact categories:

| Artifact | Source path | Currently captured |
|---|---|---|
| Main transcript | `~/.claude/projects/<proj>/<id>.jsonl` | ✅ `transcript_lines` |
| Subagent transcripts | `…/<id>/subagents/agent-*.jsonl` | ❌ |
| Subagent meta | `…/<id>/subagents/agent-*.meta.json` | ❌ |
| Tool-result blobs | `…/<id>/tool-results/*` | ❌ |
| File-history backups | `~/.claude/file-history/<id>/<hash>@v<n>` | ❌ |

Out of scope: subagent `.jsonl` files are tailed like the main transcript; subagent meta and blobs are read-once on appearance.

## Data Model

Two new MongoDB collections.

### `subagent_lines`

Mirrors `transcript_lines`; adds `subagent_id` (the agent filename stem, e.g. `agent-a48871976744a4a4b`).

```
{ session_id, subagent_id, seq, line, account_id, host, created_at }
unique index: { session_id, subagent_id, seq }
account index: { account_id, session_id, subagent_id, seq }
```

### `blobs`

Stores all file-based binary/text artifacts.

```
{ session_id, blob_type, name, content, account_id, created_at }
blob_type ∈ { "tool-result" | "file-history" | "subagent-meta" }
unique index: { session_id, blob_type, name }
account index: { account_id, session_id, blob_type }
```

`content` is a UTF-8 string for text artifacts and base64 for file-history backups. All observed artifacts are ≤13 KB — well within MongoDB's 16 MB document limit.

### Config addition

`fileHistoryDir` (string, default `~/.claude/file-history`) — allows overriding the file-history root on non-standard installs.

## Capture — Daemon Changes

`trackSession` in `daemon.ts` currently wires up one `tailFile` watcher for the main JSONL. When a session has a `transcript_path`, we derive the session directory from it (`dirname(transcript_path)/<session_id>/`) and add three artifact watchers.

### `watchArtifactDirs(session_id, sessionDir, fileHistoryDir)`

A new function called from `trackSession` after the main tailer is started. Sets up three directory watchers:

**1. Subagents dir** (`<sessionDir>/subagents/`)

Uses `fs.watch` on the directory. On each change event:
- For `agent-*.jsonl`: start a `tailFile` watcher (identical to the main transcript pattern). Lines upserted to `subagent_lines` with `subagent_id` = filename stem and an independent `seq` counter per subagent.
- For `agent-*.meta.json`: read the file once and upsert to `blobs` as `subagent-meta` with `name` = filename.

**2. Tool-results dir** (`<sessionDir>/tool-results/`)

Uses `fs.watch`. On each change event, stat the directory for new files, read each unseen file once, upsert to `blobs` as `tool-result`.

**3. File-history dir** (`<fileHistoryDir>/<session_id>/`)

Uses `fs.watch`. On each change event, stat the directory for new `<hash>@v<n>` files, read each unseen file once, upsert to `blobs` as `file-history`. Content is base64-encoded since these are opaque binary snapshots.

**Directory readiness**: all three directories may not exist at session start. Each watcher retries on a 500 ms interval until the directory appears, then starts watching — matching the pattern the existing tailer uses for the JSONL file.

**Deduplication**: all upserts use the unique index as the conflict key (`updateOne` with `$setOnInsert` for `created_at`), so daemon restarts and backfill races are safe.

**Cleanup**: watchers are not explicitly torn down — they follow the same lifecycle as the daemon process. Sessions end; the daemon keeps running.

## Restore — New MCP Tool

### Tool signature

```
restore_session({
  session_id:   string,   // required
  project_path: string?,  // override recorded path (different username/homedir on target)
  projects_dir: string?,  // override ~/.claude/projects on target
})
```

### Steps

1. **Verify ownership** — fetch session from `sessions` by `{ session_id, account_id }`. Fail with a clear error if not found.
2. **Resolve target paths** — derive the project directory name from `project_path` (or the recorded `project_path` if not overridden) using Claude Code's convention: replace `/` with `-`, strip the leading `-`. Combine with `projects_dir` to get the full target directory.
3. **Write main JSONL** — paginate `transcript_lines` in batches of 500, streaming lines to `<projects_dir>/<proj-dir>/<session_id>.jsonl`. Skip if file already exists and line count matches.
4. **Write subagent files** — group `subagent_lines` by `subagent_id`, write each to `<session-dir>/subagents/<subagent_id>.jsonl`. Write `blobs[subagent-meta]` as `<subagent_id>.meta.json` alongside.
5. **Write tool-result blobs** — write each `blobs[tool-result]` to `<session-dir>/tool-results/<name>`.
6. **Write file-history blobs** — write each `blobs[file-history]` to `<fileHistoryDir>/<session_id>/<name>`, base64-decoded.
7. **Return summary** — `{ files_written, bytes_written, missing: string[] }` where `missing` lists artifact types that had no data in MongoDB (e.g. the session predates blob capture).

### Idempotency

All writes check for existing files before writing. Re-running `restore_session` on the same machine is safe.

## Files Changed

| File | Change |
|---|---|
| `src/mongo.ts` | Add `subagentLines`, `blobs` collections; add all indexes |
| `src/config.ts` | Add `fileHistoryDir` config option with default |
| `src/daemon.ts` | Add `watchArtifactDirs`; call from `trackSession` |
| `src/mcp.ts` | Add `restore_session` tool definition and `restoreSession` handler |

No new dependencies. No schema migrations required (new collections, new indexes only).

## Testing

- **Unit**: `watchArtifactDirs` — mock `fs.watch`, verify upserts for each artifact type; retry logic for missing directories.
- **Unit**: `restoreSession` — mock MongoDB, verify correct file paths written for each artifact category; idempotency (existing files not overwritten); `missing` array populated when collection empty.
- **Integration**: run daemon against a real local MongoDB, create a synthetic session directory with all four artifact types, verify all documents appear in the correct collections with correct content.

## Open Questions

None — all design decisions are resolved.
