# Usage Enrichment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enrich live hook events with session-window/weekly usage % from Claude's local cache file, and enrich live transcript lines from `assistant` messages with context token counts and %, then surface all five metrics in `get_session_context`.

**Architecture:** Two daemon insertion points — the `tailFile` callback extracts token fields from assistant transcript lines; the `POST /event` handler reads `plan-usage-history.json` (TTL-cached, 60s) and spreads its values into hook event documents. The MCP adds two `findOne` queries to `getSessionContext` and returns a `usage` block. Backfill is untouched; backfilled documents simply lack these fields.

**Tech Stack:** TypeScript, Node.js, MongoDB 6.x, `node:test` + `node:assert/strict` for tests. Run unit tests with `pnpm test`, integration tests with `pnpm test:integration`. Run a single file: `node --import tsx/esm --test test/config.test.ts`.

**Spec:** `docs/superpowers/specs/2026-09-23-usage-enrichment-design.md`

---

## File Map

| File | Change |
|---|---|
| `src/config.ts` | Add `planUsagePath` field, default, env var, `expandHome` call |
| `src/mongo.ts` | Add two indexes to `Promise.allSettled` block |
| `src/daemon.ts` | Add `readFileSync` import, `PlanUsage` + cache vars + `readPlanUsage()` + `modelContextMax()`, enrich tailer upsert, enrich hook event insert |
| `src/mcp.ts` | Add two `findOne` calls in `getSessionContext`, add `usage` block to return value |
| `test/config.test.ts` | Add three tests: default value, env var override, `expandHome` expansion |
| `test/integration/daemon.test.ts` | Modify `before()` to create temp plan-usage file + pass env var; add two enrichment tests |
| `test/integration/mcp.test.ts` | Add `sess-usage-1` seed data in `before()`; add one `get_session_context` usage test |

---

## Task 1: Config — add `planUsagePath`

**Files:**
- Modify: `src/config.ts`
- Test: `test/config.test.ts`

- [ ] **Step 1: Write three failing tests**

Add to the bottom of `test/config.test.ts`:

```ts
test('planUsagePath defaults to Claude plan-usage-history path', () => {
  cleanEnv(() => {
    withEnv({ CLUED_PLAN_USAGE_PATH: undefined }, () => {
      const cfg = loadConfig(join(TMP, 'nonexistent.json'));
      assert.ok(cfg.planUsagePath.includes('plan-usage-history.json'));
      assert.ok(cfg.planUsagePath.startsWith('/'), 'should be an absolute path (~ expanded)');
    });
  });
});

test('CLUED_PLAN_USAGE_PATH env var overrides planUsagePath', () => {
  withEnv({ CLUED_PLAN_USAGE_PATH: '/tmp/my-usage.json',
            CLUED_MONGO_URL: undefined, CLUED_DB_NAME: undefined,
            CLUED_PORT: undefined, CLUED_MCP_PORT: undefined, CLUED_PROJECTS_DIR: undefined }, () => {
    const cfg = loadConfig(join(TMP, 'nonexistent.json'));
    assert.equal(cfg.planUsagePath, '/tmp/my-usage.json');
  });
});

test('planUsagePath expands ~ to home directory', () => {
  const cfgPath = join(TMP, 'config-usage-tilde.json');
  writeFileSync(cfgPath, JSON.stringify({ planUsagePath: '~/some/path.json' }));
  cleanEnv(() => {
    withEnv({ CLUED_PLAN_USAGE_PATH: undefined }, () => {
      const cfg = loadConfig(cfgPath);
      assert.ok(cfg.planUsagePath.startsWith(homedir()));
      assert.ok(!cfg.planUsagePath.includes('~'));
    });
  });
});
```

Also add `CLUED_PLAN_USAGE_PATH` to the `ENV_KEYS` array at line 29 so `cleanEnv` clears it:

```ts
const ENV_KEYS = ['CLUED_MONGO_URL', 'CLUED_DB_NAME', 'CLUED_PORT', 'CLUED_MCP_PORT', 'CLUED_PROJECTS_DIR', 'CLUED_PLAN_USAGE_PATH'];
```

- [ ] **Step 2: Run tests to verify they fail**

```
node --import tsx/esm --test test/config.test.ts
```

Expected: Three new tests fail with `cfg.planUsagePath is undefined` or `TypeError`.

- [ ] **Step 3: Add `planUsagePath` to `src/config.ts`**

In the `Config` interface (after `disabledEnrichers`):
```ts
planUsagePath:     string;
```

In `DEFAULTS` (after `disabledEnrichers`):
```ts
planUsagePath: join(homedir(), 'Library', 'Application Support', 'Claude', 'plan-usage-history.json'),
```

In the env-var block in `loadConfig()` (after the existing `CLUED_PROJECTS_DIR` block):
```ts
if (process.env.CLUED_PLAN_USAGE_PATH) cfg.planUsagePath = process.env.CLUED_PLAN_USAGE_PATH;
```

In the `expandHome` post-processing block (after `cfg.mongoUrl = expandHome(cfg.mongoUrl)`):
```ts
cfg.planUsagePath = expandHome(cfg.planUsagePath);
```

- [ ] **Step 4: Run tests to verify they pass**

```
node --import tsx/esm --test test/config.test.ts
```

Expected: All tests PASS including the three new ones.

- [ ] **Step 5: Commit**

```bash
git add src/config.ts test/config.test.ts
git commit -m "feat: add planUsagePath config field with env override and ~ expansion"
```

---

## Task 2: MongoDB indexes

**Files:**
- Modify: `src/mongo.ts`

No new tests needed here — the existing index creation is tested implicitly by all integration tests that call `createClient`. The two new indexes just need to land in the `Promise.allSettled` block without crashing.

- [ ] **Step 1: Add two new indexes in `src/mongo.ts`**

Inside the `Promise.allSettled([...])` block (after the existing five entries), add:

```ts
db.collection('transcript_lines').createIndex(
  { session_id: 1, 'line.type': 1, seq: -1 }
),
db.collection('hook_events').createIndex(
  { session_id: 1, session_window_pct: 1, created_at: -1 }
),
```

Do **not** modify the existing `{ session_id: 1, seq: 1 }` unique index — the new `transcript_lines` index uses `seq: -1` and is a separate index.

- [ ] **Step 2: Run existing integration tests to confirm no regressions**

```
pnpm test:integration
```

Expected: All existing tests PASS. (The new indexes are created without error; `IndexKeySpecsConflict` warnings are already handled by the `allSettled` loop.)

- [ ] **Step 3: Commit**

```bash
git add src/mongo.ts
git commit -m "feat: add transcript_lines and hook_events indexes for usage queries"
```

---

## Task 3: Daemon — write failing tests

**Files:**
- Modify: `test/integration/daemon.test.ts`

Write the two new tests before implementing the daemon changes so they fail first.

- [ ] **Step 1: Modify `before()` to set up temp plan-usage file**

At the top of `test/integration/daemon.test.ts`, add these imports alongside the existing ones:

```ts
import { writeFileSync, mkdirSync, rmSync } from 'fs';
import { tmpdir } from 'os';
```

Before the `before(async () => { ... })` block, add:

```ts
const TMP_DIR        = join(tmpdir(), `clued-daemon-test-${Date.now()}`);
const TMP_USAGE_PATH = join(TMP_DIR, 'plan-usage-history.json');

mkdirSync(TMP_DIR, { recursive: true });
writeFileSync(TMP_USAGE_PATH, JSON.stringify({
  version: 2,
  samples: [
    { t: 1000, org: 'test', u: { fh: 10, sd: 5  } },
    { t: 2000, org: 'test', u: { fh: 42, sd: 17 } },
  ],
}));
```

In the existing `after(async () => { ... })` block, add cleanup:

```ts
rmSync(TMP_DIR, { recursive: true, force: true });
```

In the `before(async () => { ... })` block, add `CLUED_PLAN_USAGE_PATH: TMP_USAGE_PATH` to the daemon spawn env:

```ts
daemonProc = spawn(process.execPath, ['--import', 'tsx/esm', DAEMON_PATH], {
  env: {
    ...process.env,
    CLUED_PORT:             String(TEST_PORT),
    CLUED_DB_NAME:          TEST_DB,
    CLUED_MONGO_URL:        TEST_URL,
    CLUED_PLAN_USAGE_PATH:  TMP_USAGE_PATH,   // ← new
  },
  stdio: 'pipe',
});
```

- [ ] **Step 2: Add the two new failing tests**

Add to the bottom of `test/integration/daemon.test.ts`:

```ts
test('assistant transcript line gets context_tokens, context_pct, output_tokens', async () => {
  const jsonlPath = join(TMP_DIR, 'usage-session.jsonl');
  writeFileSync(jsonlPath, JSON.stringify({
    type: 'assistant',
    message: {
      model: 'claude-sonnet-4-6',
      usage: {
        input_tokens: 100,
        cache_creation_input_tokens: 5000,
        cache_read_input_tokens: 10000,
        output_tokens: 200,
      },
    },
  }) + '\n');

  const status = await post({
    session_id: 'usage-transcript-test',
    transcript_path: jsonlPath,
    cwd: '/tmp',
  });
  assert.equal(status, 200);

  let doc: Record<string, unknown> | null = null;
  for (let i = 0; i < 40; i++) {
    await new Promise(r => setTimeout(r, 100));
    doc = await mongo.transcriptLines.findOne(
      { session_id: 'usage-transcript-test', context_tokens: { $exists: true } }
    ) as Record<string, unknown> | null;
    if (doc) break;
  }
  assert.ok(doc, 'transcript_lines doc with context_tokens not found within 4s');
  assert.equal(doc.context_tokens, 15100);   // 100 + 5000 + 10000
  assert.equal(doc.context_pct,    7.55);    // 15100 / 200000 * 100
  assert.equal(doc.output_tokens,  200);
});

test('hook event gets session_window_pct and weekly_pct from plan-usage cache', async () => {
  const status = await post({ session_id: 'usage-hook-test', type: 'PreToolUse', tool_name: 'Read' });
  assert.equal(status, 200);

  await new Promise(r => setTimeout(r, 200));
  const doc = await mongo.hookEvents.findOne(
    { session_id: 'usage-hook-test' }
  ) as Record<string, unknown> | null;
  assert.ok(doc, 'hook event not found');
  assert.equal(doc.session_window_pct, 42);   // latest by t: fh=42
  assert.equal(doc.weekly_pct,         17);   // latest by t: sd=17
});
```

- [ ] **Step 3: Run tests to verify they fail**

```
node --import tsx/esm --test test/integration/daemon.test.ts
```

Expected: Both new tests FAIL. The first fails because `context_tokens` is never written; the second fails because `session_window_pct` is undefined.

- [ ] **Step 4: Commit the failing tests**

```bash
git add test/integration/daemon.test.ts
git commit -m "test: add failing tests for usage enrichment in daemon"
```

---

## Task 4: Daemon — implement enrichment

**Files:**
- Modify: `src/daemon.ts`

- [ ] **Step 1: Add `readFileSync` import**

At line 1 of `src/daemon.ts`, the existing import is:
```ts
import http from 'http';
```

Add `readFileSync` to the `fs` imports. Since `daemon.ts` currently has no `fs` import, add:

```ts
import { readFileSync } from 'fs';
```

Place it after the `import http from 'http';` line.

- [ ] **Step 2: Add `PlanUsage` interface, cache variables, `readPlanUsage()`, and `modelContextMax()`**

After the existing imports and before `const __filename = ...`, insert:

```ts
interface PlanUsage { session_window_pct?: number; weekly_pct?: number; }

let planUsageCache: PlanUsage = {};
let planUsageLastRead = 0;
const PLAN_USAGE_TTL  = 60_000;

function readPlanUsage(path: string): PlanUsage {
  if (Date.now() - planUsageLastRead < PLAN_USAGE_TTL) return planUsageCache;
  try {
    const raw  = readFileSync(path, 'utf8');
    const data = JSON.parse(raw) as { samples?: Array<{ t: number; u: { fh?: number; sd?: number } }> };
    const samples = data.samples ?? [];
    if (samples.length > 0) {
      const latest = samples.reduce((best, s) => s.t > best.t ? s : best);
      planUsageCache = { session_window_pct: latest.u.fh, weekly_pct: latest.u.sd };
    }
  } catch { /* file absent or unparseable — keep last known cache */ }
  planUsageLastRead = Date.now();
  return planUsageCache;
}

function modelContextMax(model: unknown): number | null {
  return typeof model === 'string' && /^claude-(sonnet|opus)/i.test(model) ? 200_000 : null;
}
```

- [ ] **Step 3: Warm the cache at daemon startup**

After `const config = loadConfig();` (and after the MongoDB client is set up), add a single warm-up call. Find the line where `const loop = startEnrichmentLoop(mongo, enrichers);` is and add the warm-up just before it:

```ts
readPlanUsage(config.planUsagePath);  // warm cache; failure is silently ignored
```

- [ ] **Step 4: Add transcript line enrichment in the tailer callback**

In `trackSession`, find the `tailFile` callback. The current callback body is:

```ts
tailFile(transcript_path, raw => {
    let line: unknown;
    try { line = JSON.parse(raw); } catch { line = { raw }; }
    const seq = seqRef.value++;
    // Upsert on {session_id, seq} to survive daemon restart + backfill race without duplicates.
    mongo.transcriptLines.updateOne(
      { session_id, seq },
      { $set: { session_id, seq, line }, $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });
```

Replace the body with:

```ts
tailFile(transcript_path, raw => {
    let line: unknown;
    try { line = JSON.parse(raw); } catch { line = { raw }; }
    const seq = seqRef.value++;

    const extraFields: Record<string, unknown> = {};
    if (typeof line === 'object' && line !== null) {
      const l = line as Record<string, unknown>;
      if (l.type === 'assistant') {
        const msg   = l.message as Record<string, unknown> | undefined;
        const usage = msg?.usage as Record<string, unknown> | undefined;
        if (usage) {
          const ctx = ((usage.input_tokens               as number) ?? 0)
                    + ((usage.cache_creation_input_tokens as number) ?? 0)
                    + ((usage.cache_read_input_tokens     as number) ?? 0);
          extraFields.context_tokens = ctx;
          extraFields.output_tokens  = (usage.output_tokens as number) ?? 0;
          const max = modelContextMax(msg?.model);
          if (max !== null) extraFields.context_pct = +(ctx / max * 100).toFixed(2);
        }
      }
    }

    // Upsert on {session_id, seq} to survive daemon restart + backfill race without duplicates.
    mongo.transcriptLines.updateOne(
      { session_id, seq },
      { $set: { session_id, seq, line, ...extraFields }, $setOnInsert: { created_at: new Date() } },
      { upsert: true }
    ).catch(() => {});
  });
```

- [ ] **Step 5: Add hook event enrichment in the POST /event handler**

Find the line:

```ts
await mongo.hookEvents.insertOne({ ...data, created_at: new Date() });
```

Replace with:

```ts
const planUsage = readPlanUsage(config.planUsagePath);
await mongo.hookEvents.insertOne({ ...data, ...planUsage, created_at: new Date() });
```

- [ ] **Step 6: Run the daemon tests**

```
node --import tsx/esm --test test/integration/daemon.test.ts
```

Expected: All tests PASS including the two new enrichment tests.

- [ ] **Step 7: Typecheck**

```
pnpm typecheck
```

Expected: No errors.

- [ ] **Step 8: Commit**

```bash
git add src/daemon.ts
git commit -m "feat: enrich transcript lines and hook events with usage data"
```

---

## Task 5: MCP — write failing test

**Files:**
- Modify: `test/integration/mcp.test.ts`

- [ ] **Step 1: Add `sess-usage-1` seed data in `before()`**

In the `before(async () => { ... })` block of `test/integration/mcp.test.ts`, after the existing `await mongo.transcriptLines.insertMany(...)` call for `sess-3`, add:

```ts
const now2 = new Date();
await mongo.sessions.insertOne({
  session_id: 'sess-usage-1', project_path: '/tmp/usage-test',
  cwd: '/tmp/usage-test', started_at: now2, last_seen: now2,
});
await mongo.transcriptLines.insertOne({
  session_id: 'sess-usage-1', seq: 0,
  line: { type: 'assistant' },
  context_tokens: 50000,
  context_pct:    25.0,
  output_tokens:  300,
  created_at: now2,
});
await mongo.hookEvents.insertOne({
  session_id: 'sess-usage-1',
  session_window_pct: 10,
  weekly_pct:         5,
  created_at: now2,
});
```

- [ ] **Step 2: Add the failing test**

Add to the bottom of `test/integration/mcp.test.ts`:

```ts
test('get_session_context returns usage block with context and plan-usage data', async () => {
  const { endpoint, emitter, close } = await openSSE();
  const pending = collectUntilResult(emitter, 30);
  await callTool(endpoint, 30, 'get_session_context', { session_id: 'sess-usage-1' });
  const [result] = await pending;
  close();
  assert.ok(result.result, `expected result, got error: ${JSON.stringify(result.error)}`);
  const ctx = JSON.parse(
    (result.result as { content: Array<{ text: string }> }).content[0].text
  ) as Record<string, unknown>;
  const usage = ctx.usage as Record<string, unknown>;
  assert.ok(usage, 'expected usage block in response');
  assert.equal(usage.context_tokens,     50000);
  assert.equal(usage.context_pct,        25.0);
  assert.equal(usage.output_tokens,      300);
  assert.equal(usage.session_window_pct, 10);
  assert.equal(usage.weekly_pct,         5);
});
```

- [ ] **Step 3: Run the test to verify it fails**

```
node --import tsx/esm --test test/integration/mcp.test.ts
```

Expected: The new test FAILS with something like `usage is undefined` or `ctx.usage` being absent. All existing tests still pass.

- [ ] **Step 4: Commit the failing test**

```bash
git add test/integration/mcp.test.ts
git commit -m "test: add failing test for get_session_context usage block"
```

---

## Task 6: MCP — implement usage queries

**Files:**
- Modify: `src/mcp.ts`

- [ ] **Step 1: Add the two usage queries inside `getSessionContext`**

Find the `getSessionContext` function in `src/mcp.ts`. It currently ends with a `return { session: {...}, top_commands, first_lines, last_lines }`.

Before that return, add the two new queries. Run them in parallel with `Promise.all` to avoid serial latency:

```ts
const [latestAssistant, latestUsageEvent] = await Promise.all([
  mongo.transcriptLines.findOne(
    { session_id, 'line.type': 'assistant', context_tokens: { $exists: true } },
    { sort: { seq: -1 }, projection: { _id: 0, context_tokens: 1, context_pct: 1, output_tokens: 1 } }
  ),
  mongo.hookEvents.findOne(
    { session_id, session_window_pct: { $exists: true } },
    { sort: { created_at: -1 }, projection: { _id: 0, session_window_pct: 1, weekly_pct: 1 } }
  ),
]);

const usage = {
  context_tokens:     (latestAssistant  as Record<string, unknown> | null)?.context_tokens     ?? null,
  context_pct:        (latestAssistant  as Record<string, unknown> | null)?.context_pct        ?? null,
  output_tokens:      (latestAssistant  as Record<string, unknown> | null)?.output_tokens      ?? null,
  session_window_pct: (latestUsageEvent as Record<string, unknown> | null)?.session_window_pct ?? null,
  weekly_pct:         (latestUsageEvent as Record<string, unknown> | null)?.weekly_pct         ?? null,
};
```

- [ ] **Step 2: Add `usage` to the return object**

Update the return statement at the end of `getSessionContext` to include `usage`:

```ts
return {
  session: {
    session_id:   session.session_id,
    project_path: session.project_path,
    git_origin:   session.git_origin ?? null,
    cwd:          session.cwd,
    started_at:   session.started_at,
    last_seen:    session.last_seen,
  },
  top_commands,
  first_lines,
  last_lines,
  usage,
};
```

- [ ] **Step 3: Run the full MCP test suite**

```
node --import tsx/esm --test test/integration/mcp.test.ts
```

Expected: All tests PASS including the new usage test.

- [ ] **Step 4: Typecheck**

```
pnpm typecheck
```

Expected: No errors.

- [ ] **Step 5: Run all tests to confirm no regressions**

```
pnpm test && pnpm test:integration
```

Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add src/mcp.ts
git commit -m "feat: add usage block to get_session_context"
```

---

## Completion Checklist

- [ ] `pnpm test` — all unit tests pass
- [ ] `pnpm test:integration` — all integration tests pass
- [ ] `pnpm typecheck` — no TypeScript errors
- [ ] `pnpm lint` — no lint errors
- [ ] Five new test cases covering all enrichment paths
- [ ] No changes to `src/backfill.ts`
