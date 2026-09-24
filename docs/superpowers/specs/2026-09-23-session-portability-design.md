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

Subagent `.jsonl` files are tailed continuously like the main transcript. Subagent meta and blobs are read once on appearance and re-read if the file changes.

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

Stores all file-based artifacts.

```
{ session_id, blob_type, name, content, encoding, account_id, created_at }
blob_type ∈ { "tool-result" | "file-history" | "subagent-meta" }
encoding  ∈ { "utf8" | "base64" }
unique index: { session_id, blob_type, name }
account index: { account_id, session_id, blob_type }
```

`name` is always the bare filename (no path components). `content` is stored as a UTF-8 string for text artifacts (`encoding: "utf8"`) and as a base64 string for file-history backups (`encoding: "base64"`). The `encoding` field is the authoritative signal — `restore_session` uses it to decide whether to decode before writing, rather than inferring from `blob_type`. All observed artifacts are ≤13 KB, well within MongoDB's 16 MB document limit.

**Update strategy**: `content` and `encoding` are always in `$set` (so a changed file on disk overwrites the stored blob). Only `created_at` is in `$setOnInsert`. This makes the capture idempotent against daemon restarts while ensuring mutated content is not silently stale.

### Config addition

`fileHistoryDir` (string, default `~/.claude/file-history`) — allows overriding the file-history root on non-standard installs. Must be passed through `expandHome` in `loadConfig` (same as `projectsDir`, `mongoUrl`, `claudeAppConfigPath`). Env-var override: `CLUED_FILE_HISTORY_DIR`.

## Capture — Daemon Changes

`trackSession` in `daemon.ts` currently wires up one `tailFile` watcher for the main JSONL. When a session has a `transcript_path`, we compute the session directory as:

```
sessionDir = join(dirname(transcript_path), session_id)
```

(`transcript_path` is `<proj-dir>/<session_id>.jsonl`, so `dirname` gives `<proj-dir>` and appending `<session_id>` gives `<proj-dir>/<session_id>/`.)

We then call `watchArtifactDirs(session_id, sessionDir, fileHistoryDir)` after the main tailer is started.

### `watchArtifactDirs(session_id, sessionDir, fileHistoryDir)`

A new function that sets up three directory watchers. Each watcher uses the same two-layer approach as `tailFile`: `fs.watch` for low-latency notification, plus a `setInterval` poll at 2 seconds as a fallback (required on macOS/kqueue, where `fs.watch` on a directory does not reliably fire for newly created files inside it). The poll is the correctness guarantee; `fs.watch` is the latency optimization.

Each watcher maintains a `Set<string>` of already-processed filenames to avoid re-reading files it has already captured. When a file changes on disk (same name, different content), the `$set` update strategy ensures the new content is stored.

**1. Subagents dir** (`<sessionDir>/subagents/`)

On each check (watch event or poll tick), stat the directory for new files:
- `agent-*.jsonl` — start a `tailFile` watcher (identical to the main transcript pattern). Lines upserted to `subagent_lines` with `subagent_id` = filename stem and an independent `seq` counter per subagent.
- `agent-*.meta.json` — read the file and upsert to `blobs` as `blob_type: "subagent-meta"`, `encoding: "utf8"`, `name` = bare filename.

**2. Tool-results dir** (`<sessionDir>/tool-results/`)

On each check, stat for new or changed files. Read each and upsert to `blobs` as `blob_type: "tool-result"`, `encoding: "utf8"`, `name` = bare filename.

**3. File-history dir** (`<fileHistoryDir>/<session_id>/`)

On each check, stat for new `<hash>@v<n>` files. Read each and upsert to `blobs` as `blob_type: "file-history"`, `encoding: "base64"` (content is base64 of the raw bytes), `name` = bare filename.

**Directory readiness**: each of the three directories may not exist at session start. Each watcher retries on a 500 ms interval until the directory appears, then begins watching.

**Cleanup**: watchers follow the daemon process lifecycle and are not explicitly torn down. Each session produces at most 3 directory-level poll timers plus one `tailFile` watcher per subagent JSONL. Total live timers are bounded by `3 × sessions + total_subagents_across_all_sessions` — acceptable for typical usage.

## Restore — New MCP Tool

### Tool signature

```
restore_session({
  session_id:   string,   // required
  project_path: string?,  // override recorded project path (different username/homedir on target)
  projects_dir: string?,  // override ~/.claude/projects on target
})
```

### Path derivation

The `sessions` collection stores `transcript_path` for all live-captured sessions (set in `daemon.ts` line 71). `project_path` is also stored for backfilled sessions but may be absent for live sessions. The implementation therefore:

1. Prefers `transcript_path` to determine the project directory name — it already contains the encoded form Claude Code uses (e.g. `-Users-robbiebyrd-Projects-indri`), so no re-encoding is needed.
2. Falls back to encoding `project_path` (strip leading `/`, replace remaining `/` with `-`, prepend `-`) only if `transcript_path` is absent.
3. Accepts the caller's `project_path` override to handle cases where the home directory or username differs on the target machine (the caller provides the target machine's path; the tool encodes it to a dir name).

Note: the hyphen-based encoding is lossy — hyphens in directory names are indistinguishable from path separators. Preferring `transcript_path` avoids this ambiguity entirely for live sessions.

### Steps

1. **Verify ownership** — fetch session from `sessions` by `{ session_id, account_id }`. Return a clear error if not found.
2. **Resolve target paths** — determine the project directory name per the derivation above. Combine with `projects_dir` (default `~/.claude/projects`) to form the full target project directory. Session directory is `<projects_dir>/<proj-dir-name>/<session_id>/`.
3. **Write main JSONL** — paginate `transcript_lines` in batches of 500, streaming lines to `<projects_dir>/<proj-dir-name>/<session_id>.jsonl`. Overwrite unconditionally (the database is authoritative; re-running is safe).
4. **Write subagent files** — group `subagent_lines` by `subagent_id`, write each to `<session-dir>/subagents/<subagent_id>.jsonl`. Write `blobs[subagent-meta]` as `<session-dir>/subagents/<name>` alongside.
5. **Write tool-result blobs** — write each `blobs[tool-result]` to `<session-dir>/tool-results/<name>`, decoded per the `encoding` field.
6. **Write file-history blobs** — write each `blobs[file-history]` to `<fileHistoryDir>/<session_id>/<name>`, decoded per the `encoding` field.
7. **Return summary** — `{ files_written, bytes_written, missing: string[] }` where `missing` names artifact types that had no rows in MongoDB (e.g. the session predates blob capture).

### Idempotency

All writes overwrite unconditionally. The database content is the source of truth; re-running `restore_session` on the same machine is safe and self-correcting.

## Files Changed

| File | Change |
|---|---|
| `src/mongo.ts` | Add `subagentLines`, `blobs` collections; add all indexes |
| `src/config.ts` | Add `fileHistoryDir` config option with default |
| `src/daemon.ts` | Add `watchArtifactDirs`; call from `trackSession` after main tailer starts |
| `src/mcp.ts` | Add `restore_session` tool definition and `restoreSession` handler |

No new dependencies. No schema migrations required (new collections, new indexes only).

## Testing

- **Unit — `watchArtifactDirs`**: use a real `tmpdir` (not mocked `fs.watch`) matching the existing `tailer.test.ts` pattern. Create synthetic session directories with each artifact type. Verify correct documents appear in a real local MongoDB. Verify re-appearing files update `content` but not `created_at`.
- **Unit — `restoreSession`**: use a real `tmpdir` as the target. Seed a local MongoDB with transcript lines, subagent lines, and blobs for a synthetic session. Run `restoreSession` and assert file tree matches expectations. Run twice; assert second run produces identical output (idempotency).
- **Integration**: run the full daemon against a real local MongoDB, create a synthetic session directory with all four artifact types arriving over time, verify all documents appear in the correct collections with correct content and encoding.

## Open Questions

- **Subagent meta mutability**: `.meta.json` files have been observed as write-once in practice, but the spec treats them as potentially mutable (re-read on change). If they are confirmed write-once, the per-file tracking set can be simplified to skip-if-seen.
- **Tool-result blob mutability**: similarly, tool-result files appear to be write-once (named by UUID). Confirm before shipping.
