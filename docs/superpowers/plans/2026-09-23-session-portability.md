# Session Portability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Capture all four Claude Code session artifact types into MongoDB and expose a `restore_session` MCP tool that reconstructs the full session directory tree on a target machine.

**Architecture:** The daemon gains a `watchArtifactDirs` function that wires up three directory watchers (subagents, tool-results, file-history) alongside the existing main-JSONL tailer. A new `restore_session` MCP tool reads all artifact collections and writes the full directory tree to the target machine, using `transcript_path` from the session record for reliable path derivation.

**Tech Stack:** Node.js 22+, TypeScript, MongoDB 6, `node:test`, `node:assert/strict`, `fs.watch` + `setInterval` (same pattern as existing `tailFile`).

**Spec:** `docs/superpowers/specs/2026-09-23-session-portability-design.md`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `src/config.ts` | Modify | Add `fileHistoryDir` field |
| `src/mongo.ts` | Modify | Add `subagentLines`, `blobs` collections + indexes |
| `src/daemon.ts` | Modify | Add `watchArtifactDirs`; call from `trackSession` |
| `src/mcp-protocol.ts` | Modify | Add `restore_session` tool schema |
| `src/mcp.ts` | Modify | Add `restoreSession` handler |
| `test/config.test.ts` | Modify | Add `fileHistoryDir` tests |
| `test/integration/mongo.test.ts` | Modify | Add `subagentLines`, `blobs` collection tests |
| `test/integration/artifact-watcher.test.ts` | Create | Full artifact watcher tests |
| `test/integration/restore.test.ts` | Create | `restoreSession` unit + idempotency tests |

---

## Task 1: Add `fileHistoryDir` to config

**Files:**
- Modify: `src/config.ts`
- Modify: `test/config.test.ts`

- [ ] **Step 1.1: Write the failing tests**

Append to `test/config.test.ts`:

```typescript
test('fileHistoryDir has a default value containing file-history', () => {
  cleanEnv(() => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.ok(cfg.fileHistoryDir.includes('file-history'), `got ${cfg.fileHistoryDir}`);
    assert.ok(!cfg.fileHistoryDir.includes('~'));
  });
});

test('fileHistoryDir expands ~ to home directory', () => {
  const cfgPath = join(TMP, 'config-fh.json');
  writeFileSync(cfgPath, JSON.stringify({ fileHistoryDir: '~/.claude/file-history' }));
  cleanEnv(() => {
    const cfg = loadConfig(cfgPath);
    assert.ok(cfg.fileHistoryDir.startsWith(homedir()), `got ${cfg.fileHistoryDir}`);
    assert.ok(!cfg.fileHistoryDir.includes('~'));
  });
});

test('CLUED_FILE_HISTORY_DIR env var overrides fileHistoryDir', () => {
  withEnv({ CLUED_FILE_HISTORY_DIR: '/custom/file-history', CLUED_MONGO_URL: undefined,
            CLUED_DB_NAME: undefined, CLUED_PORT: undefined, CLUED_PROJECTS_DIR: undefined,
            CLUED_MCP_PORT: undefined, CLUED_CLAUDE_APP_CONFIG_PATH: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.fileHistoryDir, '/custom/file-history');
  });
});
```

Also update `ENV_KEYS` constant in `test/config.test.ts` to include `'CLUED_FILE_HISTORY_DIR'`:
```typescript
const ENV_KEYS = ['CLUED_MONGO_URL', 'CLUED_DB_NAME', 'CLUED_PORT', 'CLUED_MCP_PORT',
                  'CLUED_PROJECTS_DIR', 'CLUED_CLAUDE_APP_CONFIG_PATH', 'CLUED_FILE_HISTORY_DIR'];
```

- [ ] **Step 1.2: Run tests to confirm they fail**

```bash
node --import tsx/esm --test test/config.test.ts
```
Expected: 3 new tests fail (`cfg.fileHistoryDir` is not a property).

- [ ] **Step 1.3: Implement the config changes**

In `src/config.ts`:

Add `fileHistoryDir: string` to the `Config` interface:
```typescript
export interface Config {
  mongoUrl:             string;
  dbName:               string;
  port:                 number;
  mcpPort:              number;
  projectsDir:          string;
  fileHistoryDir:       string;   // ← add this
  disabledEnrichers:    string[];
  claudeAppConfigPath:  string;
  walPath:              string;
}
```

Add the default in `DEFAULTS`:
```typescript
fileHistoryDir: join(homedir(), '.claude', 'file-history'),
```

Add the env-var override after the existing overrides (around line 44):
```typescript
if (process.env.CLUED_FILE_HISTORY_DIR) cfg.fileHistoryDir = process.env.CLUED_FILE_HISTORY_DIR;
```

Add the `expandHome` call alongside the others (around line 48):
```typescript
cfg.fileHistoryDir = expandHome(cfg.fileHistoryDir);
```

- [ ] **Step 1.4: Run tests to confirm they pass**

```bash
node --import tsx/esm --test test/config.test.ts
```
Expected: all tests pass.

- [ ] **Step 1.5: Typecheck**

```bash
pnpm typecheck
```
Expected: no errors.

- [ ] **Step 1.6: Commit**

```bash
git add src/config.ts test/config.test.ts
git commit -m "feat(config): add fileHistoryDir with expandHome and env-var override"
```

---

## Task 2: Add `subagentLines` and `blobs` MongoDB collections

**Files:**
- Modify: `src/mongo.ts`
- Modify: `test/integration/mongo.test.ts`

- [ ] **Step 2.1: Write the failing tests**

Append to `test/integration/mongo.test.ts`:

```typescript
test('creates subagentLines and blobs collections', async () => {
  assert.ok(mongo.subagentLines, 'subagentLines collection missing');
  assert.ok(mongo.blobs,         'blobs collection missing');
});

test('upserts subagent lines by session_id + subagent_id + seq; created_at not overwritten', async () => {
  const firstSeen = new Date(Date.now() - 5000);
  await mongo.subagentLines.updateOne(
    { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 },
    { $set: { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0,
              line: { type: 'user' }, account_id: 'acc' },
      $setOnInsert: { created_at: firstSeen } },
    { upsert: true }
  );
  // Second upsert — must not create a second document, must not update created_at
  await mongo.subagentLines.updateOne(
    { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 },
    { $set: { session_id: 'test-123', subagent_id: 'agent-abc', seq: 0,
              line: { type: 'user' }, account_id: 'acc' },
      $setOnInsert: { created_at: new Date() } },
    { upsert: true }
  );
  const count = await mongo.subagentLines.countDocuments({ session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 });
  assert.equal(count, 1);
  const doc = await mongo.subagentLines.findOne({ session_id: 'test-123', subagent_id: 'agent-abc', seq: 0 });
  assert.equal((doc as Record<string, unknown>)?.created_at?.valueOf(), firstSeen.valueOf(),
    'created_at must not be overwritten on subsequent upserts');
});

test('upserts blobs by session_id + blob_type + name; content updates on second upsert', async () => {
  const now = new Date();
  await mongo.blobs.updateOne(
    { session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' },
    { $set: { content: 'first', encoding: 'utf8', account_id: 'acc' },
      $setOnInsert: { created_at: now } },
    { upsert: true }
  );
  await mongo.blobs.updateOne(
    { session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' },
    { $set: { content: 'second', encoding: 'utf8', account_id: 'acc' },
      $setOnInsert: { created_at: now } },
    { upsert: true }
  );
  const doc = await mongo.blobs.findOne({ session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' });
  assert.equal((doc as Record<string, unknown>)?.content, 'second');
  const count = await mongo.blobs.countDocuments({ session_id: 'test-123', blob_type: 'tool-result', name: 'abc.txt' });
  assert.equal(count, 1);
});
```

- [ ] **Step 2.2: Run tests to confirm they fail**

```bash
node --import tsx/esm --test test/integration/mongo.test.ts
```
Expected: 3 new tests fail (`mongo.subagentLines` is undefined).

- [ ] **Step 2.3: Implement the collection changes**

In `src/mongo.ts`, update the `MongoDb` interface:
```typescript
export interface MongoDb {
  db:              Db;
  sessions:        Collection;
  hookEvents:      Collection;
  transcriptLines: Collection;
  subagentLines:   Collection;   // ← add
  blobs:           Collection;   // ← add
  close:           () => Promise<void>;
}
```

In `createClient`, add indexes to the `Promise.allSettled` array:
```typescript
db.collection('subagent_lines').createIndex({ session_id: 1, subagent_id: 1, seq: 1 }, { unique: true }),
db.collection('subagent_lines').createIndex({ account_id: 1, session_id: 1, subagent_id: 1, seq: 1 }),
db.collection('blobs').createIndex({ session_id: 1, blob_type: 1, name: 1 }, { unique: true }),
db.collection('blobs').createIndex({ account_id: 1, session_id: 1, blob_type: 1 }),
```

Return the new collections in the return object:
```typescript
return {
  db,
  sessions:        db.collection('sessions'),
  hookEvents:      db.collection('hook_events'),
  transcriptLines: db.collection('transcript_lines'),
  subagentLines:   db.collection('subagent_lines'),   // ← add
  blobs:           db.collection('blobs'),             // ← add
  close:           () => client.close(),
};
```

- [ ] **Step 2.4: Run tests to confirm they pass**

```bash
node --import tsx/esm --test test/integration/mongo.test.ts
```
Expected: all tests pass.

- [ ] **Step 2.5: Typecheck**

```bash
pnpm typecheck
```
Expected: no errors.

- [ ] **Step 2.6: Commit**

```bash
git add src/mongo.ts test/integration/mongo.test.ts
git commit -m "feat(mongo): add subagentLines and blobs collections with indexes"
```

---

## Task 3: Implement `watchArtifactDirs`

**Files:**
- Modify: `src/daemon.ts`
- Create: `test/integration/artifact-watcher.test.ts`

The function signature (all deps explicit for testability):

```typescript
function watchArtifactDirs(
  session_id: string,
  sessionDir: string,
  fileHistoryPath: string,   // full path: join(fileHistoryDir, session_id)
  mongo: MongoDb,
  account_id: string,
  host: unknown,
): void
```

Internal helper:

```typescript
function watchDir(
  dirPath: string,
  onChange: (filename: string, fullPath: string) => void,
): void
```

`watchDir` calls `onChange` for every file in the directory whenever a new file appears or an existing file's mtime changes. It retries every 500 ms until the directory exists, then switches to `fs.watch` + 2-second `setInterval`.

- [ ] **Step 3.1: Create the test file**

Create `test/integration/artifact-watcher.test.ts`:

```typescript
import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, writeFileSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';

function delay(ms: number): Promise<void> { return new Promise(r => setTimeout(r, ms)); }

const TEST_URL = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';
const TEST_DB  = `clued_watcher_test_${Date.now()}`;
const TMP      = join(tmpdir(), `clued-watcher-${process.pid}`);
const SESSION  = 'sess-watcher-001';
const ACCOUNT  = 'acc-watcher';
const HOST     = { hostname: 'test' };

let mongo: MongoDb;

// Import watchArtifactDirs — exported for testing
import { watchArtifactDirs } from '../../src/daemon';

after(async () => {
  await mongo?.db.dropDatabase();
  await mongo?.close();
  rmSync(TMP, { recursive: true, force: true });
});

test('setup', async () => {
  mkdirSync(TMP, { recursive: true });
  mongo = await createClient({ mongoUrl: TEST_URL, dbName: TEST_DB });
});

test('captures tool-result blob when file appears in tool-results/', async () => {
  const sessionDir    = join(TMP, 'tr-session');
  const fileHistPath  = join(TMP, 'tr-fh', SESSION);
  mkdirSync(sessionDir, { recursive: true });

  watchArtifactDirs(SESSION, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  const toolResultsDir = join(sessionDir, 'tool-results');
  mkdirSync(toolResultsDir);
  writeFileSync(join(toolResultsDir, 'blob1.txt'), 'tool output here');

  await delay(3000); // wait for poll cycle

  const doc = await mongo.blobs.findOne({ session_id: SESSION, blob_type: 'tool-result', name: 'blob1.txt' });
  assert.ok(doc, 'blob document not found');
  assert.equal((doc as Record<string, unknown>).content, 'tool output here');
  assert.equal((doc as Record<string, unknown>).encoding, 'utf8');
});

test('captures file-history blob as base64 when file appears', async () => {
  const sessionDir   = join(TMP, 'fh-session');
  const fileHistPath = join(TMP, 'fh-dir', 'sess-fh-001');
  const SESSION2     = 'sess-fh-001';
  mkdirSync(sessionDir, { recursive: true });

  watchArtifactDirs(SESSION2, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  mkdirSync(fileHistPath, { recursive: true });
  const rawContent = 'binary file content';
  writeFileSync(join(fileHistPath, 'abc123@v1'), rawContent);

  await delay(3000);

  const doc = await mongo.blobs.findOne({ session_id: SESSION2, blob_type: 'file-history', name: 'abc123@v1' });
  assert.ok(doc, 'file-history blob not found');
  assert.equal((doc as Record<string, unknown>).encoding, 'base64');
  const decoded = Buffer.from((doc as Record<string, unknown>).content as string, 'base64').toString('utf8');
  assert.equal(decoded, rawContent);
});

test('tails subagent JSONL and captures subagent-meta', async () => {
  const SESSION3      = 'sess-sub-001';
  const sessionDir    = join(TMP, 'sub-session');
  const fileHistPath  = join(TMP, 'sub-fh', SESSION3);
  const subagentsDir  = join(sessionDir, 'subagents');
  mkdirSync(subagentsDir, { recursive: true });

  watchArtifactDirs(SESSION3, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  const agentId = 'agent-aabbccdd';
  writeFileSync(join(subagentsDir, `${agentId}.jsonl`),
    '{"type":"user","uuid":"u1"}\n{"type":"assistant","uuid":"u2"}\n');
  writeFileSync(join(subagentsDir, `${agentId}.meta.json`),
    JSON.stringify({ agentId, created: new Date().toISOString() }));

  await delay(3000);

  const lineCount = await mongo.subagentLines.countDocuments({ session_id: SESSION3, subagent_id: agentId });
  assert.equal(lineCount, 2);

  const meta = await mongo.blobs.findOne({ session_id: SESSION3, blob_type: 'subagent-meta', name: `${agentId}.meta.json` });
  assert.ok(meta, 'subagent meta not found');
  assert.equal((meta as Record<string, unknown>).encoding, 'utf8');
});

test('updates content when tool-result file is overwritten', async () => {
  const SESSION4     = 'sess-mut-001';
  const sessionDir   = join(TMP, 'mut-session');
  const fileHistPath = join(TMP, 'mut-fh', SESSION4);
  const toolResultsDir = join(sessionDir, 'tool-results');
  mkdirSync(toolResultsDir, { recursive: true });

  writeFileSync(join(toolResultsDir, 'mutable.txt'), 'version 1');
  watchArtifactDirs(SESSION4, sessionDir, fileHistPath, mongo, ACCOUNT, HOST);

  await delay(3000);

  writeFileSync(join(toolResultsDir, 'mutable.txt'), 'version 2');
  await delay(3000);

  const doc = await mongo.blobs.findOne({ session_id: SESSION4, blob_type: 'tool-result', name: 'mutable.txt' });
  assert.equal((doc as Record<string, unknown>).content, 'version 2');
  const count = await mongo.blobs.countDocuments({ session_id: SESSION4, blob_type: 'tool-result', name: 'mutable.txt' });
  assert.equal(count, 1, 'must not duplicate on overwrite');
});
```

- [ ] **Step 3.2: Run tests to confirm they fail**

```bash
node --import tsx/esm --test test/integration/artifact-watcher.test.ts
```
Expected: tests fail because `watchArtifactDirs` is not exported from `daemon.ts`.

- [ ] **Step 3.3: Implement `watchDir` and `watchArtifactDirs` in `src/daemon.ts`**

Add these functions near the top of `daemon.ts`, after the imports:

```typescript
function watchDir(
  dirPath: string,
  onChange: (filename: string, fullPath: string) => void,
): void {
  // mtimes tracks filename → last mtime to detect new/changed files
  const mtimes = new Map<string, number>();
  let watching = false;

  const check = () => {
    let entries: import('fs').Dirent[];
    try { entries = fs.readdirSync(dirPath, { withFileTypes: true }); }
    catch { return; } // directory doesn't exist yet or transient error
    for (const entry of entries) {
      if (!entry.isFile()) continue;
      let mtime: number;
      try { mtime = fs.statSync(join(dirPath, entry.name)).mtimeMs; }
      catch { continue; }
      if (mtimes.get(entry.name) !== mtime) {
        mtimes.set(entry.name, mtime);
        onChange(entry.name, join(dirPath, entry.name));
      }
    }
  };

  const tryWatch = () => {
    if (watching) return;
    if (!fs.existsSync(dirPath)) return;
    watching = true;
    try { fs.watch(dirPath, () => check()); } catch { /* poll-only fallback */ }
    check(); // immediate scan once directory is ready
  };

  // Retry until the directory appears, then switch to watch+poll
  const readyInterval = setInterval(() => { tryWatch(); }, 500);
  setTimeout(() => { clearInterval(readyInterval); tryWatch(); }, 0);

  setInterval(check, 2000); // poll fallback — correctness guarantee on macOS
}

export function watchArtifactDirs(
  session_id: string,
  sessionDir: string,
  fileHistoryPath: string,
  mongo: MongoDb,
  account_id: string,
  host: unknown,
): void {
  const subagentsDir   = join(sessionDir, 'subagents');
  const toolResultsDir = join(sessionDir, 'tool-results');

  // --- Subagents dir ---
  const subagentSeqs = new Map<string, { value: number }>();
  watchDir(subagentsDir, (filename, fullPath) => {
    if (filename.endsWith('.jsonl')) {
      const subagent_id = filename.replace(/\.jsonl$/, '');
      // Guard: only start a new tailer the first time this subagent is seen.
      // watchDir fires onChange again when mtime changes (poll + watch can both fire);
      // without this guard, a second call would reset seqRef to 0 and tail from the
      // beginning again, producing seq collisions that violate the unique index.
      if (subagentSeqs.has(subagent_id)) return;
      const seqRef = { value: 0 };
      subagentSeqs.set(subagent_id, seqRef);
      tailFile(fullPath, raw => {
        let line: unknown;
        try { line = JSON.parse(raw); } catch { line = { raw }; }
        const seq = seqRef.value++;
        mongo.subagentLines.updateOne(
          { session_id, subagent_id, seq },
          { $set: { session_id, subagent_id, seq, line, account_id, host },
            $setOnInsert: { created_at: new Date() } },
          { upsert: true }
        ).catch(() => {});
      });
    } else if (filename.endsWith('.meta.json')) {
      let content: string;
      try { content = fs.readFileSync(fullPath, 'utf8'); } catch { return; }
      mongo.blobs.updateOne(
        { session_id, blob_type: 'subagent-meta', name: filename },
        { $set: { content, encoding: 'utf8', account_id },
          $setOnInsert: { created_at: new Date() } },
        { upsert: true }
      ).catch(() => {});
    }
  });

  // --- Tool-results dir ---
  watchDir(toolResultsDir, (filename, fullPath) => {
    let content: string;
    try { content = fs.readFileSync(fullPath, 'utf8'); } catch { return; }
    mongo.blobs.updateOne(
      { session_id, blob_type: 'tool-result', name: filename },
      { $set: { content, encoding: 'utf8', account_id },
        $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });

  // --- File-history dir ---
  watchDir(fileHistoryPath, (filename, fullPath) => {
    let buf: Buffer;
    try { buf = fs.readFileSync(fullPath); } catch { return; }
    const content = buf.toString('base64');
    mongo.blobs.updateOne(
      { session_id, blob_type: 'file-history', name: filename },
      { $set: { content, encoding: 'base64', account_id },
        $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });
}
```

Note: `watchDir`'s `readyInterval` clears itself after one kick via `setTimeout(..., 0)` but the `setInterval(500)` keeps running until the dir appears. Fix: clear `readyInterval` once `watching` is true. Adjust the implementation so the 500ms interval self-clears:

```typescript
const readyInterval = setInterval(() => {
  if (!fs.existsSync(dirPath)) return;
  clearInterval(readyInterval);
  tryWatch();
}, 500);
tryWatch(); // immediate attempt
```

Also add the necessary imports at the top of `daemon.ts` if not already present:
```typescript
import { join, dirname } from 'path';
```
(`dirname` is already imported; `join` may already be there — check and add only what's missing.)

- [ ] **Step 3.4: Run the watcher tests**

```bash
node --import tsx/esm --test test/integration/artifact-watcher.test.ts
```
Expected: all 5 tests pass (allow up to 45 seconds total; each test uses 3-second delays).

- [ ] **Step 3.5: Typecheck**

```bash
pnpm typecheck
```
Expected: no errors.

- [ ] **Step 3.6: Run full test suite to check for regressions**

```bash
pnpm test && node --import tsx/esm --test test/integration/*.test.ts
```
Expected: all pass.

- [ ] **Step 3.7: Commit**

```bash
git add src/daemon.ts test/integration/artifact-watcher.test.ts
git commit -m "feat(daemon): add watchArtifactDirs with subagents, tool-results, file-history watchers"
```

---

## Task 4: Wire `watchArtifactDirs` into `trackSession`

**Files:**
- Modify: `src/daemon.ts`
- Modify: `test/integration/daemon.test.ts`

- [ ] **Step 4.1: Write the failing test**

Read `test/integration/daemon.test.ts` to find the test structure. Append a test that sends a session event with a transcript_path pointing to a synthetic directory, creates a tool-result file in the session's `tool-results/` subdirectory, and verifies a blob document appears in MongoDB.

Add after the last test in `test/integration/daemon.test.ts`:

```typescript
test('daemon captures tool-result blob via trackSession', async () => {
  const sessId  = `wired-${Date.now()}`;
  const projDir = join(TMP_DIR, '-wired-proj');
  mkdirSync(projDir, { recursive: true });
  const transcriptPath = join(projDir, `${sessId}.jsonl`);
  writeFileSync(transcriptPath, '');

  await post({ session_id: sessId, transcript_path: transcriptPath, cwd: projDir });
  await delay(500);

  const toolDir = join(projDir, sessId, 'tool-results');
  mkdirSync(toolDir, { recursive: true });
  writeFileSync(join(toolDir, 'result.txt'), 'hello from tool');

  await delay(3500); // one poll cycle

  const doc = await mongo.blobs.findOne({ session_id: sessId, blob_type: 'tool-result', name: 'result.txt' });
  assert.ok(doc, 'blob not captured by daemon');
  assert.equal((doc as Record<string, unknown>).content, 'hello from tool');
});
```

Add `delay` helper if not already in the file:
```typescript
function delay(ms: number): Promise<void> { return new Promise(r => setTimeout(r, ms)); }
```

- [ ] **Step 4.2: Run to confirm it fails**

```bash
node --import tsx/esm --test test/integration/daemon.test.ts 2>&1 | tail -20
```
Expected: the new test fails because `watchArtifactDirs` is not yet called from `trackSession`.

- [ ] **Step 4.3: Wire `watchArtifactDirs` into `trackSession`**

In `src/daemon.ts`, inside `trackSession`, after the `tailFile(transcript_path, ...)` call, add:

```typescript
const sessionDir     = join(dirname(transcript_path), session_id);
const fileHistoryPath = join(config.fileHistoryDir, session_id);
watchArtifactDirs(session_id, sessionDir, fileHistoryPath, mongo, account_id, host);
```

The full updated block (after `if (!transcript_path) return;`) should look like:

```typescript
if (!transcript_path) return;

tailFile(transcript_path, raw => {
  let line: unknown;
  try { line = JSON.parse(raw); } catch { line = { raw }; }
  const seq = seqRef.value++;
  mongo.transcriptLines.updateOne(
    { session_id, seq },
    { $set: { session_id, seq, line, account_id, host }, $setOnInsert: { created_at: new Date() } },
    { upsert: true }
  ).catch(() => {});
});

const sessionDir      = join(dirname(transcript_path), session_id);
const fileHistoryPath = join(config.fileHistoryDir, session_id);
watchArtifactDirs(session_id, sessionDir, fileHistoryPath, mongo, account_id, host);
```

- [ ] **Step 4.4: Run the daemon integration test**

```bash
node --import tsx/esm --test test/integration/daemon.test.ts
```
Expected: all tests pass including the new one.

- [ ] **Step 4.5: Typecheck**

```bash
pnpm typecheck
```

- [ ] **Step 4.6: Commit**

```bash
git add src/daemon.ts test/integration/daemon.test.ts
git commit -m "feat(daemon): wire watchArtifactDirs into trackSession"
```

---

## Task 5: Add `restore_session` MCP tool

**Files:**
- Modify: `src/mcp-protocol.ts`
- Modify: `src/mcp.ts`
- Create: `test/integration/restore.test.ts`

### 5a — Tool schema

- [ ] **Step 5a.1: Add the tool schema to `mcp-protocol.ts`**

Append to the `TOOLS` array in `src/mcp-protocol.ts`:

```typescript
{
  name: 'restore_session',
  description: 'Restore a session from MongoDB to the local filesystem. Reconstructs the main JSONL, subagent files, tool-result blobs, and file-history backups.',
  inputSchema: {
    type: 'object',
    properties: {
      session_id:   { type: 'string', description: 'Session to restore' },
      project_path: { type: 'string', description: 'Override the recorded project path (use when username/homedir differs on this machine)' },
      projects_dir: { type: 'string', description: 'Override the target ~/.claude/projects directory' },
    },
    required: ['session_id'],
  },
},
```

- [ ] **Step 5a.2: Verify `mcp-protocol.test.ts` still passes**

```bash
node --import tsx/esm --test test/mcp-protocol.test.ts
```
Expected: all pass.

### 5b — Handler implementation and tests

- [ ] **Step 5b.1: Write the failing tests**

Create `test/integration/restore.test.ts`:

```typescript
import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { mkdirSync, rmSync, writeFileSync, readFileSync, existsSync } from 'fs';
import { join, basename, dirname } from 'path';
import { tmpdir } from 'os';
import { createClient } from '../../src/mongo';
import type { MongoDb } from '../../src/mongo';
import { restoreSession } from '../../src/mcp';

function delay(ms: number): Promise<void> { return new Promise(r => setTimeout(r, ms)); }

const TEST_URL  = process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018';
const TEST_DB   = `clued_restore_test_${Date.now()}`;
const TMP       = join(tmpdir(), `clued-restore-${process.pid}`);
const ACCOUNT   = 'acc-restore';
const SESSION   = 'sess-restore-001';
const PROJ_DIR  = '-Users-testuser-Projects-myproject';

let mongo: MongoDb;

after(async () => {
  await mongo?.db.dropDatabase();
  await mongo?.close();
  rmSync(TMP, { recursive: true, force: true });
});

test('setup: seed MongoDB with a full session', async () => {
  mkdirSync(TMP, { recursive: true });
  mongo = await createClient({ mongoUrl: TEST_URL, dbName: TEST_DB });

  const projectsDir    = join(TMP, 'source-projects');
  const transcriptPath = join(projectsDir, PROJ_DIR, `${SESSION}.jsonl`);

  // Session record
  await mongo.sessions.insertOne({
    session_id: SESSION, account_id: ACCOUNT,
    project_path: '/Users/testuser/Projects/myproject',
    transcript_path: transcriptPath,
    cwd: '/Users/testuser/Projects/myproject',
    started_at: new Date(), last_seen: new Date(),
  });

  // Transcript lines
  for (let i = 0; i < 5; i++) {
    await mongo.transcriptLines.insertOne({
      session_id: SESSION, seq: i, account_id: ACCOUNT,
      line: { type: 'user', content: `line ${i}` }, created_at: new Date(),
    });
  }

  // Subagent lines
  await mongo.subagentLines.insertOne({
    session_id: SESSION, subagent_id: 'agent-aabb', seq: 0, account_id: ACCOUNT,
    line: { type: 'user', content: 'subagent line' }, created_at: new Date(),
  });

  // Blobs
  await mongo.blobs.insertOne({
    session_id: SESSION, blob_type: 'subagent-meta', name: 'agent-aabb.meta.json',
    content: '{"agentId":"agent-aabb"}', encoding: 'utf8', account_id: ACCOUNT, created_at: new Date(),
  });
  await mongo.blobs.insertOne({
    session_id: SESSION, blob_type: 'tool-result', name: 'result1.txt',
    content: 'tool output', encoding: 'utf8', account_id: ACCOUNT, created_at: new Date(),
  });
  const fhContent = Buffer.from('raw binary').toString('base64');
  await mongo.blobs.insertOne({
    session_id: SESSION, blob_type: 'file-history', name: 'abc@v1',
    content: fhContent, encoding: 'base64', account_id: ACCOUNT, created_at: new Date(),
  });
});

test('restores main JSONL with correct line count', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const fileHistoryDir    = join(TMP, 'target-file-history');

  const result = await restoreSession(
    { session_id: SESSION, projects_dir: targetProjectsDir },
    ACCOUNT, mongo, fileHistoryDir
  );

  const jsonlPath = join(targetProjectsDir, PROJ_DIR, `${SESSION}.jsonl`);
  assert.ok(existsSync(jsonlPath), `JSONL not found at ${jsonlPath}`);
  const lines = readFileSync(jsonlPath, 'utf8').trim().split('\n');
  assert.equal(lines.length, 5);
  assert.ok(result.files_written >= 1);
});

test('restores subagent JSONL and meta', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const subagentJsonl = join(targetProjectsDir, PROJ_DIR, SESSION, 'subagents', 'agent-aabb.jsonl');
  const subagentMeta  = join(targetProjectsDir, PROJ_DIR, SESSION, 'subagents', 'agent-aabb.meta.json');

  assert.ok(existsSync(subagentJsonl), `subagent JSONL not found at ${subagentJsonl}`);
  assert.ok(existsSync(subagentMeta),  `subagent meta not found at ${subagentMeta}`);
  assert.equal(readFileSync(subagentMeta, 'utf8'), '{"agentId":"agent-aabb"}');
});

test('restores tool-result blob', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const blobPath = join(targetProjectsDir, PROJ_DIR, SESSION, 'tool-results', 'result1.txt');
  assert.ok(existsSync(blobPath), `tool-result blob not found at ${blobPath}`);
  assert.equal(readFileSync(blobPath, 'utf8'), 'tool output');
});

test('restores file-history blob decoded from base64', async () => {
  const fileHistoryDir = join(TMP, 'target-file-history');
  const fhPath = join(fileHistoryDir, SESSION, 'abc@v1');
  assert.ok(existsSync(fhPath), `file-history blob not found at ${fhPath}`);
  assert.equal(readFileSync(fhPath, 'utf8'), 'raw binary');
});

test('restoreSession is idempotent — second run produces identical output', async () => {
  const targetProjectsDir = join(TMP, 'target-projects');
  const fileHistoryDir    = join(TMP, 'target-file-history');
  const jsonlPath = join(targetProjectsDir, PROJ_DIR, `${SESSION}.jsonl`);

  const before = readFileSync(jsonlPath, 'utf8');
  await restoreSession(
    { session_id: SESSION, projects_dir: targetProjectsDir },
    ACCOUNT, mongo, fileHistoryDir
  );
  const after = readFileSync(jsonlPath, 'utf8');
  assert.equal(before, after);
});

test('restoreSession with project_path override uses encoded override path', async () => {
  const targetProjectsDir = join(TMP, 'override-projects');
  const fileHistoryDir    = join(TMP, 'override-file-history');

  // Override project_path simulates a different machine username
  await restoreSession(
    { session_id: SESSION, project_path: '/Users/otheruser/Projects/myproject',
      projects_dir: targetProjectsDir },
    ACCOUNT, mongo, fileHistoryDir
  );

  const expectedDir = '-Users-otheruser-Projects-myproject';
  const jsonlPath = join(targetProjectsDir, expectedDir, `${SESSION}.jsonl`);
  assert.ok(existsSync(jsonlPath), `JSONL not found at override path ${jsonlPath}`);
});

test('throws on unknown session_id', async () => {
  await assert.rejects(
    () => restoreSession({ session_id: 'no-such-session' }, ACCOUNT, mongo, TMP),
    /session not found/
  );
});
```

- [ ] **Step 5b.2: Run to confirm tests fail**

```bash
node --import tsx/esm --test test/integration/restore.test.ts
```
Expected: fail because `restoreSession` is not exported from `src/mcp.ts`.

- [ ] **Step 5b.3: Implement `restoreSession` in `src/mcp.ts`**

Add these imports at the top of `src/mcp.ts` (after existing imports):
```typescript
import { mkdirSync, createWriteStream, writeFileSync } from 'fs';
import { join, basename, dirname } from 'path';
```

Add the `restoreSession` function and export it. Place it before `handleToolCall`:

```typescript
interface RestoreSessionArgs {
  session_id:   string;
  project_path?: string;
  projects_dir?: string;
}

interface RestoreResult {
  files_written: number;
  bytes_written: number;
  missing: string[];
}

export async function restoreSession(
  { session_id, project_path, projects_dir }: RestoreSessionArgs,
  account_id: string,
  mongoClient: Awaited<ReturnType<typeof createClient>>,
  fileHistoryDir: string,
): Promise<RestoreResult> {
  const session = await mongoClient.sessions.findOne({ session_id, account_id });
  if (!session) throw new Error(`session not found: ${session_id}`);

  // Derive project directory name — prefer transcript_path, fall back to project_path
  let projDirName: string;
  if (project_path) {
    // Caller override: encode the target machine's path
    projDirName = '-' + project_path.replace(/^\//, '').replaceAll('/', '-');
  } else if (session.transcript_path) {
    // transcript_path = /<projects>/<projDir>/<id>.jsonl — take the parent dir basename
    projDirName = basename(dirname(session.transcript_path as string));
  } else if (session.project_path) {
    projDirName = '-' + (session.project_path as string).replace(/^\//, '').replaceAll('/', '-');
  } else {
    throw new Error(`session ${session_id} has no transcript_path or project_path to derive target directory`);
  }

  const targetProjectsDir = projects_dir ?? config.projectsDir;
  const targetProjDir     = join(targetProjectsDir, projDirName);
  const sessionDir        = join(targetProjDir, session_id);
  const subagentsDir      = join(sessionDir, 'subagents');
  const toolResultsDir    = join(sessionDir, 'tool-results');
  const sessionFhDir      = join(fileHistoryDir, session_id);

  mkdirSync(targetProjDir,  { recursive: true });
  mkdirSync(subagentsDir,   { recursive: true });
  mkdirSync(toolResultsDir, { recursive: true });
  mkdirSync(sessionFhDir,   { recursive: true });

  let files_written = 0;
  let bytes_written = 0;
  const missing: string[] = [];

  // 1. Write main JSONL
  const jsonlPath = join(targetProjDir, `${session_id}.jsonl`);
  const total = await mongoClient.transcriptLines.countDocuments({ session_id, account_id });
  if (total === 0) {
    missing.push('transcript_lines');
  } else {
    const ws = createWriteStream(jsonlPath, { flags: 'w' });
    const BATCH = 500;
    for (let skip = 0; skip < total; skip += BATCH) {
      const lines = await mongoClient.transcriptLines
        .find({ session_id, account_id }, { projection: { _id: 0, line: 1 } })
        .sort({ seq: 1 }).skip(skip).limit(BATCH).toArray();
      for (const doc of lines) {
        const row = JSON.stringify(doc.line) + '\n';
        ws.write(row);
        bytes_written += Buffer.byteLength(row);
      }
    }
    await new Promise<void>((resolve, reject) => { ws.end(err => err ? reject(err) : resolve()); });
    files_written++;
  }

  // 2. Write subagent JSONLs and meta
  const subagentIds = await mongoClient.subagentLines.distinct('subagent_id', { session_id, account_id });
  for (const subagent_id of subagentIds as string[]) {
    const lines = await mongoClient.subagentLines
      .find({ session_id, subagent_id, account_id }, { projection: { _id: 0, line: 1 } })
      .sort({ seq: 1 }).toArray();
    const content = lines.map(d => JSON.stringify(d.line)).join('\n') + '\n';
    const outPath = join(subagentsDir, `${subagent_id}.jsonl`);
    writeFileSync(outPath, content);
    bytes_written += Buffer.byteLength(content);
    files_written++;
  }

  // 3. Write blobs
  const blobs = await mongoClient.blobs.find({ session_id, account_id }).toArray();
  const blobTypes = new Set(blobs.map(b => (b as Record<string, unknown>).blob_type as string));

  for (const blobDoc of blobs) {
    const b = blobDoc as Record<string, unknown>;
    const content  = b.content  as string;
    const encoding = b.encoding as string;
    const name     = b.name     as string;
    const blobType = b.blob_type as string;

    let outPath: string;
    if (blobType === 'subagent-meta')  outPath = join(subagentsDir,   name);
    else if (blobType === 'tool-result') outPath = join(toolResultsDir, name);
    else if (blobType === 'file-history') outPath = join(sessionFhDir,   name);
    else continue; // unknown type — skip

    const buf = encoding === 'base64' ? Buffer.from(content, 'base64') : Buffer.from(content, 'utf8');
    writeFileSync(outPath, buf);
    bytes_written += buf.length;
    files_written++;
  }

  // Report which blob types had no data
  for (const expected of ['subagent-meta', 'tool-result', 'file-history'] as const) {
    if (!blobTypes.has(expected)) missing.push(expected);
  }

  return { files_written, bytes_written, missing };
}
```

Wire it up in `handleToolCall`:
```typescript
case 'restore_session': return restoreSession(
  args as RestoreSessionArgs,
  account_id,
  await getMongo(),
  config.fileHistoryDir,
);
```

- [ ] **Step 5b.4: Run restore tests**

```bash
node --import tsx/esm --test test/integration/restore.test.ts
```
Expected: all 8 tests pass.

- [ ] **Step 5b.5: Typecheck**

```bash
pnpm typecheck
```
Expected: no errors.

- [ ] **Step 5b.6: Run full test suite**

```bash
pnpm test && node --import tsx/esm --test test/integration/*.test.ts
```
Expected: all pass.

- [ ] **Step 5b.7: Commit**

```bash
git add src/mcp-protocol.ts src/mcp.ts test/integration/restore.test.ts
git commit -m "feat(mcp): add restore_session tool — reconstructs full session directory from MongoDB"
```

---

## Task 6: Final verification

- [ ] **Step 6.1: Run the complete test suite**

```bash
pnpm test && node --import tsx/esm --test test/integration/*.test.ts
```
Expected: all tests pass, no errors, no unexpected output.

- [ ] **Step 6.2: Build the production bundle**

```bash
pnpm build
```
Expected: `dist/` updated with no errors.

- [ ] **Step 6.3: Lint**

```bash
pnpm lint
```
Expected: no lint errors.

- [ ] **Step 6.4: Final commit**

```bash
git add -A
git status  # verify only expected files staged
git commit -m "chore: final build artifacts for session portability feature"
```
