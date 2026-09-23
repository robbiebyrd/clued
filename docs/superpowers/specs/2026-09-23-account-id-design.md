# Account ID Enrichment Design

**Date:** 2026-09-23
**Author:** Robbie Byrd

---

## Overview

`clued` gains per-account isolation: an `account_id` field (the user's Anthropic account UUID) is stamped on every document written to MongoDB, and all MCP queries are scoped to the current account. A `git_branch` field is also added to `sessions` alongside the existing `git_origin`, captured the same way.

**Threat model:** Two scenarios are protected:
1. One machine running multiple Anthropic accounts — their sessions must not intermingle.
2. Multiple users sharing one MongoDB instance — each user's data must be invisible to others.

Both are solved by the same mechanism: `account_id` on every document + `account_id` filter on every MCP query.

---

## Section 1: Data Source

### Account ID — `lastKnownAccountUuid`

The Claude desktop app persists the authenticated account UUID in:

```
~/Library/Application Support/Claude/config.json   (macOS)
```

This is a flat JSON object. The relevant key:

```json
{ "lastKnownAccountUuid": "ffbd5aa3-056e-4d61-b634-efebf6f92082" }
```

**Linux support is out of scope.** The path `~/.config/Claude/config.json` exists on Linux but is not handled in this feature.

Both the daemon and MCP server read this value **once at startup** using a synchronous `readFileSync`. No TTL cache is needed — the account UUID does not change during a running session.

If the file is absent, unreadable, or the key is missing, `account_id` defaults to `"unknown"`. The daemon and MCP server continue to operate normally.

**Known limitation of the `"unknown"` fallback:** When two processes (or two users on a shared MongoDB instance) both produce `account_id: "unknown"` due to a missing config file, their documents intermingle — the same cross-pollution the feature is designed to prevent. This is an acceptable degraded-mode behaviour. A warning is logged at startup when `"unknown"` is used so operators are aware isolation is degraded.

The path is configurable via `CLUED_CLAUDE_APP_CONFIG_PATH` env var, following the existing pattern for `CLUED_PLAN_USAGE_PATH`.

```ts
function readAccountId(path: string): string {
  try {
    const raw  = readFileSync(path, 'utf8');
    const data = JSON.parse(raw) as { lastKnownAccountUuid?: string };
    const id   = data.lastKnownAccountUuid ?? 'unknown';
    if (id === 'unknown') console.warn('clued: account ID unavailable — isolation is degraded');
    return id;
  } catch {
    console.warn('clued: account ID unavailable — isolation is degraded');
    return 'unknown';
  }
}
```

### Git branch — `git branch --show-current`

A new `getGitBranch(cwd: string): Promise<string | null>` function is added to `src/git.ts`, mirroring `getGitOrigin`:

```ts
export async function getGitBranch(cwd: string): Promise<string | null> {
  try {
    const { stdout } = await execFileAsync(
      'git', ['-C', cwd, 'branch', '--show-current'],
      { timeout: 2000 }
    );
    return stdout.trim() || null;
  } catch {
    return null;
  }
}
```

Returns `null` for detached HEAD (empty stdout), non-git directories, and git command failures.

---

## Section 2: Config Changes

**`src/config.ts`** — one new field:

```ts
claudeAppConfigPath: string
// DEFAULTS value: join(homedir(), 'Library', 'Application Support', 'Claude', 'config.json')
// env override: CLUED_CLAUDE_APP_CONFIG_PATH
```

The default is built with `join(homedir(), ...)` — the same pattern used for `projectsDir` in the existing `DEFAULTS`. The `expandHome` call in the post-processing block is applied for env-var overrides that use `~/` syntax.

In `loadConfig()`, add alongside the existing env-var block:

```ts
if (process.env.CLUED_CLAUDE_APP_CONFIG_PATH) cfg.claudeAppConfigPath = process.env.CLUED_CLAUDE_APP_CONFIG_PATH;
```

In the post-processing block where `expandHome` is applied:

```ts
cfg.claudeAppConfigPath = expandHome(cfg.claudeAppConfigPath);
```

---

## Section 3: Daemon Changes (`src/daemon.ts`)

### 3a — Account ID constant

At module level, read once at startup:

```ts
import { readFileSync } from 'fs';   // already imported after usage-enrichment feature

const account_id = readAccountId(config.claudeAppConfigPath);
```

### 3b — Session tracking (`trackSession`)

`git_origin` and `git_branch` are fetched in parallel. Both are written to the session document.

**Initial registration path** (fires once, when `session_id` is first seen):

The existing `Promise.resolve(cwd ? getGitOrigin(cwd) : null).then(...)` call is replaced in its entirety with the following `Promise.all` pattern:

```ts
Promise.all([
  cwd ? getGitOrigin(cwd) : Promise.resolve(null),
  cwd ? getGitBranch(cwd) : Promise.resolve(null),
]).then(([git_origin, git_branch]) => {
  if (git_origin) state.gitOriginFound = true;
  const $set: Record<string, unknown> = { session_id, transcript_path, cwd, last_seen: now, account_id };
  if (git_origin)  $set.git_origin  = git_origin;
  if (git_branch)  $set.git_branch  = git_branch;
  mongo.sessions.updateOne(
    { session_id },
    { $set, $setOnInsert: { started_at: now } },
    { upsert: true }
  ).catch(() => {});
});
// The tailFile call is placed AFTER the Promise.all(...).then(...) chain,
// guarded by `if (!transcript_path) return;`, unchanged from the current implementation.
```

**Retry path** (fires on subsequent events when `!state.gitOriginFound && cwd`):

The retry path is updated to fetch both `git_origin` and `git_branch` in parallel. Previously it only fetched `git_origin` and wrote `$set: { git_origin }`. Now:

```ts
if (!state.gitOriginFound && cwd) {
  const [gitOrigin, gitBranch] = await Promise.all([getGitOrigin(cwd), getGitBranch(cwd)]);
  if (gitOrigin) {
    state.gitOriginFound = true;
    const $set: Record<string, unknown> = { git_origin: gitOrigin };
    if (gitBranch) $set.git_branch = gitBranch;
    mongo.sessions.updateOne(
      { session_id, git_origin: { $exists: false } },
      { $set }
    ).catch(() => {});
  }
}
```

`git_branch` is only captured once — either in the initial registration or the first successful retry — and not re-fetched afterwards. Branch changes within a session are not tracked.

### 3c — Hook event enrichment

`account_id` is spread into `insertOne`:

```ts
await mongo.hookEvents.insertOne({ ...data, account_id, created_at: new Date() });
```

The `last_seen` heartbeat `updateOne` that fires on every event (daemon.ts, HTTP handler) also gains `account_id` in its filter for defence-in-depth:

```ts
mongo.sessions.updateOne(
  { session_id: data.session_id, account_id },
  { $set: { last_seen: new Date() } }
).catch(() => {});
```

### 3d — Transcript line enrichment

`account_id` is added to the `$set` in the tailer `updateOne`:

```ts
mongo.transcriptLines.updateOne(
  { session_id, seq },
  { $set: { session_id, seq, line, account_id, ...extraFields }, $setOnInsert: { created_at: new Date() } },
  { upsert: true }
).catch(() => {});
```

---

## Section 4: Backfill Changes (`src/backfill.ts`)

`account_id` is added to the session `$set` in `processSession`:

```ts
const $set: Record<string, unknown> = {
  session_id: sessionId,
  project_path: projectPath,
  transcript_path: filePath,
  last_seen: now,
  account_id,
};
if (git_origin) $set.git_origin = git_origin;
if (git_branch) $set.git_branch = git_branch;
```

`git_branch` is also fetched in `processSession` alongside `git_origin`:

```ts
const [git_origin, git_branch] = await Promise.all([
  getGitOrigin(projectPath),
  getGitBranch(projectPath),
]);
```

Transcript line ops gain `account_id` in `$set`:

```ts
update: { $set: { session_id: sessionId, seq, line, account_id }, $setOnInsert: { created_at: now } },
```

`account_id` is computed once in the standalone entry point and passed through the call chain:

```ts
// Standalone entry point
const account_id = readAccountId(config.claudeAppConfigPath);
await backfill(config, mongo, account_id);
```

`backfill()` gains a third parameter and forwards it to `processSession`:

```ts
export async function backfill(
  config: Pick<Config, 'projectsDir'>,
  mongo: MongoDb,
  account_id: string,
): Promise<void>
```

`processSession` gains a fifth parameter:

```ts
async function processSession(
  mongo: MongoDb,
  projectPath: string,
  sessionId: string,
  filePath: string,
  account_id: string,
): Promise<number>
```

**Important:** Existing documents written before this feature lack `account_id`. Running backfill after deploying stamps `account_id` onto all historical session and transcript line documents. Hook events cannot be retroactively stamped (no backfill path for hook_events exists). The setup documentation should note that a backfill run is recommended after upgrading.

---

## Section 5: MongoDB Indexes (`src/mongo.ts`)

All new indexes are added to the existing `Promise.allSettled` block.

### `sessions` collection

```ts
// List all sessions for an account, sorted by recency
db.collection('sessions').createIndex({ account_id: 1, last_seen: -1 }),

// Find an account's sessions by repo
db.collection('sessions').createIndex({ account_id: 1, git_origin: 1 }),

// Find an account's sessions by repo + branch
db.collection('sessions').createIndex({ account_id: 1, git_origin: 1, git_branch: 1 }),
```

The existing `{ git_origin: 1 }` single-field index is retained — it may serve legacy queries against pre-feature documents that lack `account_id`.

### `hook_events` collection

```ts
// Account-scoped hook event queries, sorted by recency
db.collection('hook_events').createIndex({ account_id: 1, session_id: 1, created_at: -1 }),
```

### `transcript_lines` collection

```ts
// Account-scoped transcript queries
db.collection('transcript_lines').createIndex({ account_id: 1, session_id: 1, seq: 1 }),
```

The existing `{ session_id: 1, seq: 1 }` unique index is **not modified** — `session_id` UUIDs are globally unique, so it already enforces cross-account uniqueness.

---

## Section 6: MCP Changes (`src/mcp.ts`)

At module level, read `account_id` once at startup, same as the daemon:

```ts
const account_id = readAccountId(config.claudeAppConfigPath);
```

`readAccountId` is extracted to a shared utility (`src/account.ts`) imported by both `daemon.ts` and `mcp.ts`.

Every query gains an `account_id` filter:

### `findSessions`

```ts
const filter: Record<string, unknown> = { account_id };
```

`account_id` is set unconditionally as the first key. The subsequent `project_path`, `git_origin`, and `query`/`$or` conditions are all ANDed with it — MongoDB evaluates `{ account_id, $or: [...] }` as an AND of the account filter with the `$or` clause, so all branches of `findSessions` are covered by this single assignment.

### `getSessionContext`

```ts
const session = await mongo.sessions.findOne({ session_id, account_id }, { projection: { _id: 0 } });
```

Hook events and transcript lines queries are already scoped to `session_id`. Since the session ownership check fails first for foreign sessions, downstream queries are never reached. No change needed to hook_events or transcript_lines filters in this function — but `account_id` is added for defence-in-depth:

```ts
mongo.hookEvents.find({ session_id, account_id, ... })
mongo.transcriptLines.find({ session_id, account_id })
```

### `searchCommands`

When `session_id` is passed directly (the fast-path that skips the sessions lookup), an ownership check is added first:

```ts
if (session_id) {
  const owned = await mongo.sessions.findOne({ session_id, account_id }, { projection: { session_id: 1 } });
  if (!owned) return [];
  sessionIds = [session_id];
}
```

When filtering by `git_origin`, `account_id` is prepended to the sessions query:

```ts
mongo.sessions.find({ account_id, git_origin: { $regex: git_origin, $options: 'i' } }, ...)
```

`account_id` is added to the hook_events filter in all paths:

```ts
filter.account_id = account_id;
```

### `readTranscript`

```ts
const session = await mongo.sessions.findOne({ session_id, account_id });
```

A foreign `session_id` returns `"session not found"` — identical to a nonexistent session. No information leakage.

---

## Section 7: New Shared Utility (`src/account.ts`)

`readAccountId` is extracted to avoid duplication between daemon and MCP server. The implementation matches Section 1 exactly — including the startup warning:

```ts
import { readFileSync } from 'fs';

export function readAccountId(path: string): string {
  try {
    const raw  = readFileSync(path, 'utf8');
    const data = JSON.parse(raw) as { lastKnownAccountUuid?: string };
    const id   = data.lastKnownAccountUuid ?? 'unknown';
    if (id === 'unknown') console.warn('clued: account ID unavailable — isolation is degraded');
    return id;
  } catch {
    console.warn('clued: account ID unavailable — isolation is degraded');
    return 'unknown';
  }
}
```

---

## Section 8: Error Handling

| Scenario | Behaviour |
|---|---|
| `config.json` absent or `lastKnownAccountUuid` missing | `account_id` = `"unknown"` — startup warning logged; isolation is degraded but daemon and MCP operate normally |
| `git branch --show-current` fails | `git_branch` omitted from session doc |
| Detached HEAD | `git branch --show-current` returns empty stdout → `git_branch` = `null` → omitted |
| Session queried by wrong account | Returns `"session not found"` — same as nonexistent session |
| Pre-feature documents lacking `account_id` | Invisible to MCP queries until backfill re-runs and stamps `account_id` |

---

## Section 9: Testing

### `test/config.test.ts`

- Default `claudeAppConfigPath` expands to `~/Library/Application Support/Claude/config.json`
- `CLUED_CLAUDE_APP_CONFIG_PATH` env var overrides the path
- `expandHome` is applied

### `test/account.test.ts` (new)

- Returns `lastKnownAccountUuid` value when file exists and key is present
- Returns `"unknown"` when file is absent; asserts `console.warn` was called with `'clued: account ID unavailable — isolation is degraded'`
- Returns `"unknown"` when key is missing; asserts `console.warn` was called with the same message
- Returns `"unknown"` when file is invalid JSON

### `test/git.test.ts` (new)

- `getGitBranch` returns the branch name for the current repo
- Returns `null` for a non-git directory
- Returns `null` for detached HEAD (simulate by writing an empty string stdout)

### `test/integration/daemon.test.ts`

- Write a temp `config.json` with `lastKnownAccountUuid: "test-account-uuid"`. Set `CLUED_CLAUDE_APP_CONFIG_PATH`. Assert that session, hook_event, and transcript_line documents all have `account_id: "test-account-uuid"`.
- Assert `git_branch` appears on session doc when daemon receives an event with a valid git `cwd`.
- When `CLUED_CLAUDE_APP_CONFIG_PATH` points to a missing file, assert `account_id: "unknown"` on documents.

### `test/integration/backfill.test.ts`

- Run backfill with `CLUED_CLAUDE_APP_CONFIG_PATH` pointing to a temp file containing `{ "lastKnownAccountUuid": "backfill-account-uuid" }`. Assert that all resulting `sessions` and `transcript_lines` documents have `account_id: "backfill-account-uuid"`.

### `test/integration/mcp.test.ts`

- Seed two sessions: `sess-acct-1` with `account_id: "acct-a"` and `sess-acct-2` with `account_id: "acct-b"`. Also seed a `hook_events` doc for `sess-acct-2` with `tool_name: "Bash"` and `tool_input.command: "echo hello"`. Set module-level `account_id = "acct-a"`.
  - Assert `find_sessions` returns only `sess-acct-1`.
  - Assert `get_session_context({ session_id: "sess-acct-2" })` throws `"session not found"`.
  - Assert `search_commands({ pattern: "echo" })` returns empty (no match for `acct-a`).
  - Assert `search_commands({ session_id: "sess-acct-2", pattern: "echo" })` returns empty (ownership check blocks the foreign session_id fast-path).
  - Assert `read_transcript({ session_id: "sess-acct-2" })` throws `"session not found"`.

---

## Out of Scope

- Linux `claudeAppConfigPath` default (`~/.config/Claude/config.json`).
- Tracking branch changes within an active session.
- Migrating existing `hook_events` documents to add `account_id` (no backfill path exists for hook_events).
- Removing the existing single-field `{ git_origin: 1 }` index.
- Per-account database name partitioning.
