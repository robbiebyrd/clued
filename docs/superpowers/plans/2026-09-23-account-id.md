# Account ID Enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stamp every MongoDB document with an `account_id` (the user's Anthropic account UUID) and scope all MCP queries to the current account, plus add `git_branch` to session documents.

**Architecture:** A new shared utility `src/account.ts` reads `lastKnownAccountUuid` from the Claude desktop app's local config once at startup; the daemon writes it to every collection; the MCP server reads the same value and prepends it to every query filter. `git_branch` follows the existing `git_origin` pattern in `src/git.ts`.

**Tech Stack:** Node.js, TypeScript, MongoDB driver, `node:test` (built-in test runner)

---

## File Map

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `src/account.ts` | `readAccountId(path)` — read UUID from Claude config, warn on fallback |
| Modify | `src/git.ts` | Add `getGitBranch(cwd)` alongside existing `getGitOrigin` |
| Modify | `src/config.ts` | Add `claudeAppConfigPath` field + env var + expandHome |
| Modify | `src/mongo.ts` | Add 5 new compound indexes |
| Modify | `src/daemon.ts` | Read account_id at startup; stamp all writes; update trackSession retry |
| Modify | `src/backfill.ts` | Add account_id + git_branch to processSession; update signatures |
| Modify | `src/mcp.ts` | Read account_id at startup; prepend to all query filters |
| Create | `test/account.test.ts` | Unit tests for readAccountId |
| Modify | `test/git.test.ts` | Add getGitBranch tests |
| Modify | `test/config.test.ts` | Add claudeAppConfigPath tests |
| Modify | `test/integration/daemon.test.ts` | account_id + git_branch integration tests |
| Modify | `test/integration/backfill.test.ts` | account_id integration test |
| Modify | `test/integration/mcp.test.ts` | Cross-account isolation tests; add account_id to existing seeds |

---

## Task 1: `src/account.ts` — readAccountId utility

**Files:**
- Create: `test/account.test.ts`
- Create: `src/account.ts`

- [ ] **Step 1: Write the failing tests**

Create `test/account.test.ts`:

```ts
import { test, mock } from 'node:test';
import assert from 'node:assert/strict';
import { writeFileSync, mkdirSync, rmSync } from 'fs';
import { join } from 'path';
import { tmpdir } from 'os';
import { readAccountId } from '../src/account';

const TMP = join(tmpdir(), `clued-test-account-${process.pid}`);
mkdirSync(TMP, { recursive: true });

import { after } from 'node:test';
after(() => rmSync(TMP, { recursive: true, force: true }));

test('returns lastKnownAccountUuid when present', () => {
  const p = join(TMP, 'config-ok.json');
  writeFileSync(p, JSON.stringify({ lastKnownAccountUuid: 'test-uuid-1234' }));
  assert.equal(readAccountId(p), 'test-uuid-1234');
});

test('returns "unknown" and warns when file is absent', () => {
  const warn = mock.method(console, 'warn', () => {});
  try {
    const id = readAccountId(join(TMP, 'nonexistent.json'));
    assert.equal(id, 'unknown');
    assert.ok(
      warn.mock.calls.some(c => String(c.arguments[0]).includes('isolation is degraded')),
      'expected degraded-isolation warning'
    );
  } finally { warn.mock.restore(); }
});

test('returns "unknown" and warns when key is missing', () => {
  const p = join(TMP, 'config-nokey.json');
  writeFileSync(p, JSON.stringify({ other: 'stuff' }));
  const warn = mock.method(console, 'warn', () => {});
  try {
    const id = readAccountId(p);
    assert.equal(id, 'unknown');
    assert.ok(
      warn.mock.calls.some(c => String(c.arguments[0]).includes('isolation is degraded')),
      'expected degraded-isolation warning'
    );
  } finally { warn.mock.restore(); }
});

test('returns "unknown" when file is invalid JSON', () => {
  const p = join(TMP, 'config-bad.json');
  writeFileSync(p, 'NOT_JSON{{{');
  assert.equal(readAccountId(p), 'unknown');
});
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
node --import tsx/esm --test 'test/account.test.ts'
```

Expected: FAIL — `readAccountId` not found

- [ ] **Step 3: Create `src/account.ts`**

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

- [ ] **Step 4: Run tests — expect PASS**

```bash
node --import tsx/esm --test 'test/account.test.ts'
```

Expected: 4 passing

- [ ] **Step 5: Commit**

```bash
git add src/account.ts test/account.test.ts
git commit -m "feat: add readAccountId utility"
```

---

## Task 2: `src/git.ts` — getGitBranch

**Files:**
- Modify: `src/git.ts`
- Modify: `test/git.test.ts`

- [ ] **Step 1: Add failing tests to `test/git.test.ts`**

Append to the existing file (after the last test):

```ts
import { execFileSync } from 'child_process';
import { getGitBranch } from '../src/git';

test('getGitBranch returns current branch for a git repo', async () => {
  const branch = await getGitBranch(ROOT);
  assert.ok(branch, 'expected a branch name');
  assert.equal(typeof branch, 'string');
});

test('getGitBranch returns null for a non-git directory', async () => {
  const tmp = join(tmpdir(), `clued-branch-nogit-${process.pid}`);
  mkdirSync(tmp, { recursive: true });
  try {
    assert.equal(await getGitBranch(tmp), null);
  } finally { rmSync(tmp, { recursive: true, force: true }); }
});

test('getGitBranch returns null for a non-existent path', async () => {
  assert.equal(await getGitBranch('/does/not/exist/9999'), null);
});

test('getGitBranch returns null in detached HEAD state', async () => {
  const tmp = join(tmpdir(), `clued-branch-detached-${process.pid}`);
  mkdirSync(tmp, { recursive: true });
  try {
    execFileSync('git', ['init', '--template=', tmp], { stdio: 'ignore' });
    execFileSync('git', ['-C', tmp, 'config', 'user.email', 'test@test.com'], { stdio: 'ignore' });
    execFileSync('git', ['-C', tmp, 'config', 'user.name',  'Test'],          { stdio: 'ignore' });
    execFileSync('git', ['-C', tmp, 'commit', '--allow-empty', '-m', 'init'], { stdio: 'ignore' });
    const hash = execFileSync('git', ['-C', tmp, 'rev-parse', 'HEAD'])
      .toString().trim();
    execFileSync('git', ['-C', tmp, 'checkout', '--detach', hash], { stdio: 'ignore' });
    assert.equal(await getGitBranch(tmp), null);
  } finally { rmSync(tmp, { recursive: true, force: true }); }
});
```

Note: `execFileSync` and `getGitBranch` need to be added to the import at the top:

```ts
import { execFileSync } from 'child_process';   // add
import { getGitBranch } from '../src/git';       // add alongside getGitOrigin
```

- [ ] **Step 2: Run tests to confirm new ones fail**

```bash
node --import tsx/esm --test 'test/git.test.ts'
```

Expected: existing tests pass, 4 new tests FAIL — `getGitBranch` not exported

- [ ] **Step 3: Add `getGitBranch` to `src/git.ts`**

Append to the existing file (after the closing brace of `getGitOrigin`):

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

- [ ] **Step 4: Run tests — expect PASS**

```bash
node --import tsx/esm --test 'test/git.test.ts'
```

Expected: all 7 passing

- [ ] **Step 5: Commit**

```bash
git add src/git.ts test/git.test.ts
git commit -m "feat: add getGitBranch utility"
```

---

## Task 3: `src/config.ts` — claudeAppConfigPath

**Files:**
- Modify: `src/config.ts`
- Modify: `test/config.test.ts`

- [ ] **Step 1: Add failing tests to `test/config.test.ts`**

At line 29, add `'CLUED_CLAUDE_APP_CONFIG_PATH'` to `ENV_KEYS`:

```ts
const ENV_KEYS = ['CLUED_MONGO_URL', 'CLUED_DB_NAME', 'CLUED_PORT', 'CLUED_MCP_PORT', 'CLUED_PROJECTS_DIR', 'CLUED_CLAUDE_APP_CONFIG_PATH'];
```

Append the following tests to the file:

```ts
test('claudeAppConfigPath default ends with Library/Application Support/Claude/config.json', () => {
  cleanEnv(() => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.ok(
      cfg.claudeAppConfigPath.endsWith(join('Library', 'Application Support', 'Claude', 'config.json')),
      `unexpected default: ${cfg.claudeAppConfigPath}`
    );
    assert.ok(!cfg.claudeAppConfigPath.includes('~'));
  });
});

test('CLUED_CLAUDE_APP_CONFIG_PATH env var overrides claudeAppConfigPath', () => {
  withEnv({ CLUED_CLAUDE_APP_CONFIG_PATH: '/custom/path/config.json',
            CLUED_MONGO_URL: undefined, CLUED_DB_NAME: undefined,
            CLUED_PORT: undefined, CLUED_MCP_PORT: undefined, CLUED_PROJECTS_DIR: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.claudeAppConfigPath, '/custom/path/config.json');
  });
});

test('claudeAppConfigPath expands ~ to home directory', () => {
  withEnv({ CLUED_CLAUDE_APP_CONFIG_PATH: '~/.claude/app-config.json',
            CLUED_MONGO_URL: undefined, CLUED_DB_NAME: undefined,
            CLUED_PORT: undefined, CLUED_MCP_PORT: undefined, CLUED_PROJECTS_DIR: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.ok(cfg.claudeAppConfigPath.startsWith(homedir()), `expected ${cfg.claudeAppConfigPath} to start with homedir`);
    assert.ok(!cfg.claudeAppConfigPath.includes('~'));
  });
});
```

- [ ] **Step 2: Run tests to confirm new ones fail**

```bash
node --import tsx/esm --test 'test/config.test.ts'
```

Expected: existing 7 tests pass, 3 new tests FAIL — `claudeAppConfigPath` undefined

- [ ] **Step 3: Update `src/config.ts`**

In the `Config` interface, add:
```ts
claudeAppConfigPath: string;
```

In `DEFAULTS`, add (using the existing `homedir()` import):
```ts
claudeAppConfigPath: join(homedir(), 'Library', 'Application Support', 'Claude', 'config.json'),
```

In `loadConfig()`, after the existing env-var block, add:
```ts
if (process.env.CLUED_CLAUDE_APP_CONFIG_PATH) cfg.claudeAppConfigPath = process.env.CLUED_CLAUDE_APP_CONFIG_PATH;
```

In the post-processing block (after the `cfg.projectsDir = expandHome(...)` line), add:
```ts
cfg.claudeAppConfigPath = expandHome(cfg.claudeAppConfigPath);
```

- [ ] **Step 4: Run tests — expect PASS**

```bash
node --import tsx/esm --test 'test/config.test.ts'
```

Expected: all 10 passing

- [ ] **Step 5: Commit**

```bash
git add src/config.ts test/config.test.ts
git commit -m "feat: add claudeAppConfigPath config field"
```

---

## Task 4: `src/mongo.ts` — compound indexes

**Files:**
- Modify: `src/mongo.ts`

No failing test to write — index creation is additive and the integration tests cover correctness implicitly. This task is mechanical.

- [ ] **Step 1: Add indexes to the `Promise.allSettled` block in `src/mongo.ts`**

After the last existing `createIndex` call (line 24, `{ session_id: 1, seq: 1 }`), append five new entries:

```ts
db.collection('sessions').createIndex({ account_id: 1, last_seen: -1 }),
db.collection('sessions').createIndex({ account_id: 1, git_origin: 1 }),
db.collection('sessions').createIndex({ account_id: 1, git_origin: 1, git_branch: 1 }),
db.collection('hook_events').createIndex({ account_id: 1, session_id: 1, created_at: -1 }),
db.collection('transcript_lines').createIndex({ account_id: 1, session_id: 1, seq: 1 }),
```

- [ ] **Step 2: Confirm no TypeScript errors**

```bash
pnpm run typecheck
```

Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add src/mongo.ts
git commit -m "feat: add compound account_id indexes"
```

---

## Task 5: Daemon — write failing integration tests

**Files:**
- Modify: `test/integration/daemon.test.ts`

- [ ] **Step 1: Add TMP_DIR setup to `before()` and write a temp Claude config**

Add a `TMP_DIR` constant and a `writeFileSync` import if not present. Current imports include `mkdirSync` and `rmSync` from `'fs'` — add `writeFileSync` to that import.

Add `TMP_DIR` constant near the top of the file (after `const ROOT = ...`):

```ts
import { writeFileSync, mkdirSync, rmSync } from 'fs'; // add writeFileSync

const TMP_DIR = join(tmpdir(), `clued-daemon-test-${process.pid}`);
const CLAUDE_CONFIG = join(TMP_DIR, 'claude-config.json');
```

In `before()`, before the `spawn` call, add:

```ts
mkdirSync(TMP_DIR, { recursive: true });
writeFileSync(CLAUDE_CONFIG, JSON.stringify({ lastKnownAccountUuid: 'test-account-uuid' }));
```

Add `CLUED_CLAUDE_APP_CONFIG_PATH: CLAUDE_CONFIG` to the daemon's env:

```ts
daemonProc = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
  env: {
    ...process.env,
    CLUED_PORT: String(TEST_PORT),
    CLUED_DB_NAME: TEST_DB,
    CLUED_MONGO_URL: TEST_URL,
    CLUED_CLAUDE_APP_CONFIG_PATH: CLAUDE_CONFIG,
  },
  stdio: 'pipe',
});
```

In `after()`, add cleanup:

```ts
rmSync(TMP_DIR, { recursive: true, force: true });
```

- [ ] **Step 2: Add 3 failing tests at the end of the file**

```ts
test('POST /event stamps account_id on hook event document', async () => {
  const status = await post({ session_id: 'daemon-acct-hook', type: 'PreToolUse', tool_name: 'Read' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 300));
  const doc = await mongo.hookEvents.findOne({ session_id: 'daemon-acct-hook' });
  assert.ok(doc, 'hook event not found');
  assert.equal((doc as Record<string, unknown>).account_id, 'test-account-uuid');
});

test('POST /event stamps account_id on session document', async () => {
  const status = await post({ session_id: 'daemon-acct-sess', transcript_path: '/tmp/acct-fake.jsonl', cwd: '/tmp' });
  assert.equal(status, 200);
  await new Promise(r => setTimeout(r, 300));
  const doc = await mongo.sessions.findOne({ session_id: 'daemon-acct-sess' });
  assert.ok(doc, 'session not found');
  assert.equal((doc as Record<string, unknown>).account_id, 'test-account-uuid');
});

test('transcript line gets account_id stamped by tailer', async () => {
  const jsonlPath = join(TMP_DIR, 'acct-tailer.jsonl');
  writeFileSync(jsonlPath, '');
  await post({ session_id: 'daemon-acct-line', transcript_path: jsonlPath, cwd: '/tmp' });
  await new Promise(r => setTimeout(r, 300));
  writeFileSync(jsonlPath, JSON.stringify({ type: 'human', text: 'hi' }) + '\n');
  let doc;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.transcriptLines.findOne({ session_id: 'daemon-acct-line' });
    if (doc) break;
  }
  assert.ok(doc, 'transcript line not found');
  assert.equal((doc as Record<string, unknown>).account_id, 'test-account-uuid');
});

test('session gets git_branch when cwd is a git repo', async () => {
  const status = await post({
    session_id: 'daemon-branch-test',
    transcript_path: '/tmp/branch-fake.jsonl',
    cwd: ROOT,
  });
  assert.equal(status, 200);
  let doc;
  for (let i = 0; i < 20; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.sessions.findOne({ session_id: 'daemon-branch-test', git_branch: { $exists: true } });
    if (doc) break;
  }
  assert.ok(doc, 'session with git_branch not found');
  assert.equal(typeof (doc as Record<string, unknown>).git_branch, 'string');
});
```

Also add a fifth test that verifies the `"unknown"` fallback. This test spawns its own short-lived daemon pointing at a nonexistent config file:

```ts
test('account_id falls back to "unknown" when claude config is absent', async () => {
  const MISSING_CONFIG = join(TMP_DIR, 'nonexistent-claude.json');
  const FALLBACK_PORT  = TEST_PORT + 1;
  const fallbackProc   = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
    env: {
      ...process.env,
      CLUED_PORT: String(FALLBACK_PORT),
      CLUED_DB_NAME: TEST_DB,
      CLUED_MONGO_URL: TEST_URL,
      CLUED_CLAUDE_APP_CONFIG_PATH: MISSING_CONFIG,
    },
    stdio: 'pipe',
  });
  try {
    for (let i = 0; i < 20; i++) {
      await new Promise(r => setTimeout(r, 100));
      const ok = await new Promise<boolean>(resolve =>
        http.get(`http://127.0.0.1:${FALLBACK_PORT}/health`, res => resolve(res.statusCode === 200))
          .on('error', () => resolve(false))
      );
      if (ok) break;
    }
    await new Promise((resolve, reject) => {
      const body = JSON.stringify({ session_id: 'daemon-unknown-acct', type: 'PreToolUse' });
      const req  = http.request({
        hostname: '127.0.0.1', port: FALLBACK_PORT, path: '/event', method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(body) },
      }, res => { res.resume(); res.on('end', resolve); });
      req.on('error', reject);
      req.end(body);
    });
    await new Promise(r => setTimeout(r, 300));
    const doc = await mongo.hookEvents.findOne({ session_id: 'daemon-unknown-acct' });
    assert.ok(doc, 'hook event not found');
    assert.equal((doc as Record<string, unknown>).account_id, 'unknown');
  } finally {
    fallbackProc.kill('SIGTERM');
  }
});
```

- [ ] **Step 3: Run tests to confirm new ones fail**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/daemon.test.ts'
```

Expected: existing tests pass, 5 new tests FAIL — documents have no `account_id` and no `git_branch`

- [ ] **Step 4: Commit failing tests**

```bash
git add test/integration/daemon.test.ts
git commit -m "test: add failing daemon account_id and git_branch tests"
```

---

## Task 6: Daemon — implementation

**Files:**
- Modify: `src/daemon.ts`

- [ ] **Step 1: Add imports at the top of `src/daemon.ts`**

Add after existing imports:

```ts
import { readAccountId } from './account';
import { getGitBranch } from './git';
```

Do **not** add `import { readFileSync } from 'fs'` — `readAccountId` handles file reading internally and `daemon.ts` has no other need for it.

- [ ] **Step 2: Read account_id at module level (after `const config = loadConfig()`)**

```ts
const account_id = readAccountId(config.claudeAppConfigPath);
```

- [ ] **Step 3: Update `trackSession` — initial registration path**

Replace the existing `Promise.resolve(cwd ? getGitOrigin(cwd) : null).then(...)` block (lines 53–62 in the current file) in its entirety with:

```ts
Promise.all([
  cwd ? getGitOrigin(cwd) : Promise.resolve(null),
  cwd ? getGitBranch(cwd) : Promise.resolve(null),
]).then(([git_origin, git_branch]) => {
  if (git_origin) state.gitOriginFound = true;
  const $set: Record<string, unknown> = { session_id, transcript_path, cwd, last_seen: now, account_id };
  if (git_origin) $set.git_origin = git_origin;
  if (git_branch) $set.git_branch = git_branch;
  mongo.sessions.updateOne(
    { session_id },
    { $set, $setOnInsert: { started_at: now } },
    { upsert: true }
  ).catch(() => {});
});
```

The `if (!transcript_path) return;` guard and `tailFile(...)` call that follow remain in place, outside the `.then()`.

- [ ] **Step 4: Update `trackSession` — retry path**

Replace the current retry block (lines 33–46 in the current file, the `if (tracked.has(session_id))` block) with:

```ts
if (tracked.has(session_id)) {
  const state = tracked.get(session_id)!;
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
  return;
}
```

- [ ] **Step 5: Update hook event insertion (inside `req.on('end', ...)` handler)**

Change:

```ts
await mongo.hookEvents.insertOne({ ...data, created_at: new Date() });
```

to:

```ts
await mongo.hookEvents.insertOne({ ...data, account_id, created_at: new Date() });
```

- [ ] **Step 6: Update last_seen heartbeat updateOne**

Change:

```ts
mongo.sessions.updateOne({ session_id: data.session_id }, { $set: { last_seen: new Date() } }).catch(() => {});
```

to:

```ts
mongo.sessions.updateOne({ session_id: data.session_id, account_id }, { $set: { last_seen: new Date() } }).catch(() => {});
```

- [ ] **Step 7: Update transcript line upsert in tailer callback**

Change:

```ts
{ $set: { session_id, seq, line }, $setOnInsert: { created_at: new Date() } },
```

to:

```ts
{ $set: { session_id, seq, line, account_id }, $setOnInsert: { created_at: new Date() } },
```

**Note:** The spec's Section 3d snippet shows `...extraFields` in this `$set`. That variable does not exist in the current `daemon.ts` and must NOT be included here. The plan snippet above is correct. `extraFields` is only relevant if the usage-enrichment feature is implemented first; it is absent from the current codebase.

- [ ] **Step 8: Run daemon integration tests — expect PASS**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/daemon.test.ts'
```

Expected: all tests passing (including the 4 new ones)

- [ ] **Step 9: Commit**

```bash
git add src/daemon.ts
git commit -m "feat: stamp account_id and git_branch on all daemon writes"
```

---

## Task 7: Backfill — failing test + implementation

**Files:**
- Modify: `test/integration/backfill.test.ts`
- Modify: `src/backfill.ts`

- [ ] **Step 1: Add a failing test to `test/integration/backfill.test.ts`**

Add `writeFileSync` to the existing `fs` import. Add a `TMP_CLAUDE` path after the existing `TMP` constant:

```ts
import { mkdirSync, writeFileSync, rmSync } from 'fs'; // add writeFileSync

const TMP_CLAUDE = join(tmpdir(), `clued-bf-claude-${process.pid}`);
```

Add cleanup in `after()`:

```ts
rmSync(TMP_CLAUDE, { recursive: true, force: true });
```

Add the new test at the end of the file. Note: this test needs its own mongo client and project directory to be isolated from the other tests:

```ts
test('backfill stamps account_id on sessions and transcript lines', async () => {
  mkdirSync(TMP_CLAUDE, { recursive: true });
  const claudeConfig = join(TMP_CLAUDE, 'config.json');
  writeFileSync(claudeConfig, JSON.stringify({ lastKnownAccountUuid: 'backfill-account-uuid' }));

  const projDir   = join(TMP_CLAUDE, '-Users-test-acctproject');
  const sessionId = 'ccccdddd-eeee-ffff-0000-111111111111';
  mkdirSync(projDir, { recursive: true });
  writeFileSync(join(projDir, `${sessionId}.jsonl`),
    JSON.stringify({ type: 'human', text: 'test' }) + '\n');

  const cfg = {
    mongoUrl:           process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018',
    dbName:             `clued_bf_acct_test_${Date.now()}`,
    projectsDir:        TMP_CLAUDE,
    claudeAppConfigPath: claudeConfig,
  };
  const { readAccountId } = await import('../src/account');
  const acctId = readAccountId(cfg.claudeAppConfigPath);
  const m = await createClient(cfg);
  try {
    await backfill(cfg, m, acctId);
    const sess = await m.sessions.findOne({ session_id: sessionId });
    assert.ok(sess, 'session not found');
    assert.equal((sess as Record<string, unknown>).account_id, 'backfill-account-uuid');
    const line = await m.transcriptLines.findOne({ session_id: sessionId });
    assert.ok(line, 'transcript line not found');
    assert.equal((line as Record<string, unknown>).account_id, 'backfill-account-uuid');
  } finally {
    await m.db.dropDatabase();
    await m.close();
  }
});
```

- [ ] **Step 2: Run tests to confirm the new test fails**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/backfill.test.ts'
```

Expected: existing tests pass, new test FAILS — `backfill` takes 2 arguments, not 3; no `account_id` on docs

- [ ] **Step 3: Update `src/backfill.ts` — imports**

Add to the top-level imports:

```ts
import { getGitBranch } from './git';
import { readAccountId } from './account';
```

- [ ] **Step 4: Update `processSession` signature and body**

Change the signature from:

```ts
async function processSession(
  mongo: MongoDb,
  projectPath: string,
  sessionId: string,
  filePath: string,
): Promise<number>
```

to:

```ts
async function processSession(
  mongo: MongoDb,
  projectPath: string,
  sessionId: string,
  filePath: string,
  account_id: string,
): Promise<number>
```

Replace the existing `const git_origin = await getGitOrigin(projectPath);` line with:

```ts
const [git_origin, git_branch] = await Promise.all([
  getGitOrigin(projectPath),
  getGitBranch(projectPath),
]);
```

Replace the `$set` initialization block:

```ts
const $set: Record<string, unknown> = { session_id: sessionId, project_path: projectPath, transcript_path: filePath, last_seen: now };
if (git_origin) $set.git_origin = git_origin;
```

with:

```ts
const $set: Record<string, unknown> = {
  session_id: sessionId, project_path: projectPath, transcript_path: filePath,
  last_seen: now, account_id,
};
if (git_origin) $set.git_origin = git_origin;
if (git_branch) $set.git_branch = git_branch;
```

In the `ops` map, update the `$set` in each bulk write op:

```ts
update: { $set: { session_id: sessionId, seq, line, account_id }, $setOnInsert: { created_at: now } },
```

- [ ] **Step 5: Update `backfill()` signature and call sites**

Change:

```ts
export async function backfill(config: Pick<Config, 'projectsDir'>, mongo: MongoDb): Promise<void>
```

to:

```ts
export async function backfill(config: Pick<Config, 'projectsDir'>, mongo: MongoDb, account_id: string): Promise<void>
```

In the inner loop where `processSession` is called, add `account_id` as the 5th argument:

```ts
batch.map(s => processSession(mongo, s.projectPath, s.sessionId, s.filePath, account_id))
```

- [ ] **Step 6: Update the standalone entry point (bottom of `src/backfill.ts`)**

Replace:

```ts
await backfill(config, mongo);
```

with:

```ts
const account_id = readAccountId(config.claudeAppConfigPath);
await backfill(config, mongo, account_id);
```

The `backfill()` function signature keeps `Pick<Config, 'projectsDir'>` unchanged — `claudeAppConfigPath` is consumed only in the standalone entry before `backfill()` is called, so the `Pick` type does not need to change.

- [ ] **Step 7: Update existing backfill test call sites**

There are **four** existing call sites in `test/integration/backfill.test.ts`. Add `''` as the third argument to each:

- Line 43: `await backfill(TEST_CONFIG, mongo, '');`
- Line 56: `await backfill(TEST_CONFIG, mongo, '');`
- Line 64: `await assert.doesNotReject(() => backfill(TEST_CONFIG, mongo, ''));`
- Line 86: `await backfill(gitConfig, gitMongo, '');`  ← note: different config and mongo variables

Since these tests don't check `account_id`, passing `''` keeps them passing without any other changes.

- [ ] **Step 8: Run tests — expect PASS**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/backfill.test.ts'
```

Expected: all tests passing

- [ ] **Step 9: Commit**

```bash
git add src/backfill.ts test/integration/backfill.test.ts
git commit -m "feat: add account_id and git_branch to backfill"
```

---

## Task 8: MCP — failing integration tests

**Files:**
- Modify: `test/integration/mcp.test.ts`

The MCP server will filter by `account_id` after Task 9. That means the existing seeded sessions (which have no `account_id`) will become invisible, breaking existing tests. This task updates the seed data AND adds the new cross-account isolation tests — all while pointing at a temp Claude config with a known UUID.

- [ ] **Step 1: Add TMP_DIR and claude config setup to `before()`**

Add imports at the top:

```ts
import { writeFileSync, mkdirSync, rmSync } from 'fs';  // add these
import { tmpdir } from 'os';                              // add
```

Add constants after existing constants:

```ts
const TMP_DIR       = join(tmpdir(), `clued-mcp-test-${process.pid}`);
const CLAUDE_CONFIG = join(TMP_DIR, 'claude-config.json');
const TEST_ACCOUNT  = 'test-mcp-account';
```

At the start of `before()`, before the `spawn` call:

```ts
mkdirSync(TMP_DIR, { recursive: true });
writeFileSync(CLAUDE_CONFIG, JSON.stringify({ lastKnownAccountUuid: TEST_ACCOUNT }));
```

Add `CLUED_CLAUDE_APP_CONFIG_PATH: CLAUDE_CONFIG` to the MCP spawn env:

```ts
mcpProc = spawn(process.execPath, ['--import', 'tsx/esm', MCP_PATH], {
  env: {
    ...process.env,
    CLUED_MCP_PORT: String(TEST_PORT),
    CLUED_DB_NAME: TEST_DB,
    CLUED_MONGO_URL: TEST_URL,
    CLUED_CLAUDE_APP_CONFIG_PATH: CLAUDE_CONFIG,
  },
  stdio: 'pipe',
});
```

- [ ] **Step 2: Add `account_id` to all existing session seeds in `before()`**

In the `insertMany` call for sessions, add `account_id: TEST_ACCOUNT` to each document:

```ts
await mongo.sessions.insertMany([
  { session_id: 'sess-1', account_id: TEST_ACCOUNT, project_path: '/home/user/myrepo',
    git_origin: 'https://github.com/user/myrepo.git',
    cwd: '/home/user/myrepo', started_at: now, last_seen: now },
  { session_id: 'sess-2', account_id: TEST_ACCOUNT, project_path: '/home/user/other',
    git_origin: 'https://github.com/user/other.git',
    cwd: '/home/user/other',  started_at: now, last_seen: now },
  { session_id: 'sess-3', account_id: TEST_ACCOUNT, project_path: '/home/user/myrepo',
    git_origin: 'https://github.com/user/myrepo.git',
    cwd: '/home/user/myrepo', started_at: now, last_seen: now },
]);
```

- [ ] **Step 3: Add cross-account seed data in `before()`** (after the existing insertMany calls)

```ts
// Cross-account isolation test data
await mongo.sessions.insertMany([
  { session_id: 'sess-acct-1', account_id: TEST_ACCOUNT,    project_path: '/home/user/acct', started_at: now, last_seen: now },
  { session_id: 'sess-acct-2', account_id: 'other-account', project_path: '/home/other/acct', started_at: now, last_seen: now },
]);
await mongo.hookEvents.insertOne({
  session_id: 'sess-acct-2', account_id: 'other-account',
  tool_name: 'Bash', tool_input: { command: 'echo hello' }, created_at: now,
});
await mongo.transcriptLines.insertMany([
  { session_id: 'sess-acct-2', account_id: 'other-account', seq: 0, line: { type: 'human' }, created_at: now },
]);
```

- [ ] **Step 4: Add cross-account isolation tests at end of file**

Use IDs starting at 100 to avoid collisions with existing tests:

```ts
test('find_sessions does not return sessions from other accounts', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 100);
  await callTool(endpoint, 100, 'find_sessions', {});
  const [result] = await pending;
  close();
  const sessions = JSON.parse(
    (result.result as { content: Array<{ text: string }> }).content[0].text
  ) as Array<Record<string, unknown>>;
  assert.ok(!sessions.some(s => s.session_id === 'sess-acct-2'), 'sess-acct-2 (other account) must not appear');
  assert.ok(sessions.some(s => s.session_id === 'sess-acct-1'), 'sess-acct-1 (own account) must appear');
});

test('get_session_context returns "session not found" for foreign account session', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 101);
  await callTool(endpoint, 101, 'get_session_context', { session_id: 'sess-acct-2' });
  const [result] = await pending;
  close();
  assert.ok(result.error, 'expected error for foreign session');
  assert.equal((result.error as { message: string }).message, 'session not found');
});

test('search_commands does not return commands from other account', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 102);
  await callTool(endpoint, 102, 'search_commands', { pattern: 'echo' });
  const [result] = await pending;
  close();
  const matches = JSON.parse(
    (result.result as { content: Array<{ text: string }> }).content[0].text
  ) as Array<Record<string, unknown>>;
  assert.deepEqual(matches, [], 'echo command belongs to other-account and must not appear');
});

test('search_commands with foreign session_id returns empty (ownership check)', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 103);
  await callTool(endpoint, 103, 'search_commands', { session_id: 'sess-acct-2', pattern: 'echo' });
  const [result] = await pending;
  close();
  const matches = JSON.parse(
    (result.result as { content: Array<{ text: string }> }).content[0].text
  ) as Array<Record<string, unknown>>;
  assert.deepEqual(matches, []);
});

test('read_transcript returns "session not found" for foreign account session', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 104);
  await callTool(endpoint, 104, 'read_transcript', { session_id: 'sess-acct-2' });
  const [result] = await pending;
  close();
  assert.ok(result.error);
  assert.equal((result.error as { message: string }).message, 'session not found');
});
```

- [ ] **Step 5: Add `after()` cleanup for TMP_DIR**

In the existing `after()`:

```ts
rmSync(TMP_DIR, { recursive: true, force: true });
```

- [ ] **Step 6: Run tests — confirm existing pass, new ones fail**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/mcp.test.ts'
```

Expected: existing tests continue to pass (seeds now have account_id), 5 new tests FAIL — MCP doesn't filter yet

- [ ] **Step 7: Commit failing tests**

```bash
git add test/integration/mcp.test.ts
git commit -m "test: add failing MCP cross-account isolation tests"
```

---

## Task 9: MCP — implementation

**Files:**
- Modify: `src/mcp.ts`

- [ ] **Step 1: Add imports at the top of `src/mcp.ts`**

```ts
import { readAccountId } from './account';
```

- [ ] **Step 2: Read account_id at module level** (after `const config = loadConfig()`)

```ts
const account_id = readAccountId(config.claudeAppConfigPath);
```

- [ ] **Step 3: Update `findSessions`**

Change:

```ts
const filter: Record<string, unknown> = {};
```

to:

```ts
const filter: Record<string, unknown> = { account_id };
```

- [ ] **Step 4: Update `getSessionContext`**

Change the session lookup:

```ts
const session = await mongo.sessions.findOne({ session_id }, { projection: { _id: 0 } });
```

to:

```ts
const session = await mongo.sessions.findOne({ session_id, account_id }, { projection: { _id: 0 } });
```

In the `bashEvents` query, add `account_id`:

```ts
.find({ session_id, account_id, tool_name: 'Bash', 'tool_input.command': { $type: 'string' } })
```

In the `first_lines` and `last_lines` queries, add `account_id`:

```ts
mongo.transcriptLines.find({ session_id, account_id }, { projection: { _id: 0 } })
```

(both occurrences)

Also update `countDocuments`:

```ts
const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
```

- [ ] **Step 5: Update `searchCommands`**

Add the ownership check at the top of the `session_id` fast-path:

```ts
if (session_id) {
  const owned = await mongo.sessions.findOne(
    { session_id, account_id },
    { projection: { session_id: 1 } }
  );
  if (!owned) return [];
  sessionIds = [session_id];
} else if (git_origin) {
```

Update the `git_origin` sessions lookup:

```ts
const ss = await mongo.sessions
  .find({ account_id, git_origin: { $regex: git_origin, $options: 'i' } }, { projection: { session_id: 1 } })
```

After the `const filter: Record<string, unknown> = { ... }` line, add:

```ts
filter.account_id = account_id;
```

- [ ] **Step 6: Update `readTranscript`**

Change the session lookup:

```ts
const session = await mongo.sessions.findOne({ session_id });
```

to:

```ts
const session = await mongo.sessions.findOne({ session_id, account_id });
```

In the `transcriptLines` query inside the loop:

```ts
mongo.transcriptLines.find({ session_id, account_id }, { projection: { _id: 0 } })
```

Also update `countDocuments`:

```ts
const total = await mongo.transcriptLines.countDocuments({ session_id, account_id });
```

- [ ] **Step 7: Run MCP tests — expect all pass**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/mcp.test.ts'
```

Expected: all tests passing

- [ ] **Step 8: Run all tests**

```bash
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/*.test.ts'
CLUED_MONGO_URL=mongodb://localhost:27017 node --import tsx/esm --test 'test/integration/*.test.ts'
```

Expected: all passing

- [ ] **Step 9: Commit**

```bash
git add src/mcp.ts
git commit -m "feat: scope all MCP queries to account_id"
```

---

## Final check

- [ ] **Run full type check**

```bash
pnpm run typecheck
```

Expected: no errors

- [ ] **Run linter**

```bash
pnpm run lint
```

Expected: no errors
