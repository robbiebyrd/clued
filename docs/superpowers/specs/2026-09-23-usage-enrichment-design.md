# Usage Enrichment Design

**Date:** 2026-09-23
**Author:** Robbie Byrd

---

## Overview

`clued` gains live usage enrichment: four metrics are captured as documents flow through the daemon and exposed via the existing `get_session_context` MCP tool.

| Metric | Source | Storage |
|---|---|---|
| Input context tokens | `assistant` transcript lines → `message.usage` | `transcript_lines.context_tokens` |
| Input context % | Same, divided by model max | `transcript_lines.context_pct` |
| Output tokens | Same → `output_tokens` | `transcript_lines.output_tokens` |
| 5-hour session window % | `plan-usage-history.json` → `u.fh` | `hook_events.session_window_pct` |
| 7-day weekly usage % | Same file → `u.sd` | `hook_events.weekly_pct` |

All fields are written only for **live events** (daemon tailer / `POST /event`). The backfill path (`src/backfill.ts`) is unchanged — backfilled documents simply lack these fields, and `get_session_context` returns `null` for all usage fields on backfilled sessions. This is expected and documented behaviour.

---

## Section 1: Data Sources

### Context tokens and % — transcript lines

Every `assistant`-type transcript line in the JSONL files carries a `message.usage` object:

```json
{
  "input_tokens": 3,
  "cache_creation_input_tokens": 31759,
  "cache_read_input_tokens": 0,
  "output_tokens": 191
}
```

The model name is stored at `line.message.model` (e.g., `"claude-sonnet-4-6"`).

**`context_tokens`** = `input_tokens + cache_creation_input_tokens + cache_read_input_tokens`

This is the *input-side* context fill — the number of prompt tokens consumed by this request. Output tokens (`output_tokens`) are stored separately because they are not part of the input context measurement; they are retained for informational completeness but intentionally excluded from `context_pct`.

**`context_pct`** = `context_tokens / MODEL_MAX_TOKENS × 100` (rounded to 2 decimal places)

`MODEL_MAX_TOKENS` = 200,000 for models matching `/^claude-(sonnet|opus)/i` (read from `line.message.model`). If the model field is absent, is not a string, or does not match that pattern, `context_pct` is **omitted** from the stored document. `context_tokens` and `output_tokens` are still written.

### Session window and weekly usage — plan-usage-history.json

Claude Code maintains `~/Library/Application Support/Claude/plan-usage-history.json` — a local cache of usage percentages fetched from the Anthropic API, refreshed every few minutes by the Claude Code process. Schema:

```json
{
  "version": 2,
  "samples": [
    { "t": 1790190736395, "org": "...", "u": { "fh": 7, "sd": 3 } }
  ]
}
```

Each sample:
- `t` — Unix timestamp in milliseconds; the array may arrive out of order, so "latest" means the sample with the **maximum `t` value**
- `u.fh` — five-hour rolling session window usage, 0–100
- `u.sd` — seven-day rolling weekly usage, 0–100

**Important:** `session_window_pct` and `weekly_pct` stored on a hook event reflect the file's state **at the moment that hook event arrived**. They are not a live reading — a session that has been idle since the last hook event will have stale values. Consumers of `get_session_context` should treat these as "usage at last activity."

---

## Section 2: Config Changes

**`src/config.ts`** — one new field added to the `Config` interface and `DEFAULTS`:

```ts
planUsagePath: string
// DEFAULTS value: ~/Library/Application Support/Claude/plan-usage-history.json
// env override: CLUED_PLAN_USAGE_PATH
```

In `loadConfig()`, add alongside the existing env-var block:

```ts
if (process.env.CLUED_PLAN_USAGE_PATH) cfg.planUsagePath = process.env.CLUED_PLAN_USAGE_PATH;
```

In the post-processing block where `expandHome` is applied, add:

```ts
cfg.planUsagePath = expandHome(cfg.planUsagePath);
```

---

## Section 3: Daemon Changes (`src/daemon.ts`)

### 3a — Plan-usage cache (module-level)

A synchronous `readFileSync`-based on-demand cache with a 60-second staleness check. The cache holds two module-level variables:

- `planUsageCache: PlanUsage` — the last successfully parsed `{ session_window_pct?, weekly_pct? }` (initially `{}`)
- `planUsageLastRead: number` — the `Date.now()` timestamp of the last read attempt (initially `0`)

On each call to `readPlanUsage(path)`: if `Date.now() - planUsageLastRead < 60_000`, return the existing `planUsageCache` without touching the file. Otherwise, attempt the read, update `planUsageLastRead` regardless of success, and update `planUsageCache` only on success. On any error, `planUsageCache` retains its last value.

Concurrent calls within the same event-loop tick may both pass the staleness check and both issue a read — this is acceptable because the file is tiny, reads are synchronous, and the result is idempotent.

```ts
interface PlanUsage { session_window_pct?: number; weekly_pct?: number; }

let planUsageCache: PlanUsage = {};
let planUsageLastRead = 0;
const PLAN_USAGE_TTL = 60_000;

function readPlanUsage(path: string): PlanUsage {
  if (Date.now() - planUsageLastRead < PLAN_USAGE_TTL) return planUsageCache;
  try {
    const raw  = readFileSync(path, 'utf8');
    const data = JSON.parse(raw) as { samples?: Array<{ t: number; u: { fh?: number; sd?: number } }> };
    const samples = data.samples ?? [];
    if (samples.length > 0) {
      // "Latest" = sample with maximum t value (array may not be sorted)
      const latest = samples.reduce((best, s) => s.t > best.t ? s : best);
      planUsageCache = { session_window_pct: latest.u.fh, weekly_pct: latest.u.sd };
    }
  } catch {
    // file absent or unparseable — keep last known cache
  }
  planUsageLastRead = Date.now();
  return planUsageCache;
}
```

Called once at daemon startup (to warm the cache; failure is silently ignored) and on each `POST /event`.

### 3b — Transcript line enrichment (inside `tailFile` callback)

After `let line: unknown; try { line = JSON.parse(raw); } catch { line = { raw }; }`, and **before** the `mongo.transcriptLines.updateOne` call:

```ts
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

mongo.transcriptLines.updateOne(
  { session_id, seq },
  { $set: { session_id, seq, line, ...extraFields }, $setOnInsert: { created_at: new Date() } },
  { upsert: true }
).catch(() => {});
```

`context_tokens`, `output_tokens`, and `context_pct` are stored at the **document root** alongside `line`, `seq`, `session_id` — **not** nested inside the `line` subdocument. They are a denormalised extraction of `line.message.usage` for query performance. The raw `line` document is stored unchanged and continues to contain `line.message.usage.*`.

The Section 5 MCP query filters `context_tokens: { $exists: true }` at the document root. The Section 4 index uses `'line.type'` (a dotted-path into the `line` subdocument) and `seq` at root level — both levels are valid and intentional.

```ts
function modelContextMax(model: unknown): number | null {
  return typeof model === 'string' && /^claude-(sonnet|opus)/i.test(model) ? 200_000 : null;
}
```

### 3c — Hook event enrichment (`POST /event` handler)

In the `req.on('end')` handler, before `mongo.hookEvents.insertOne`:

```ts
const planUsage = readPlanUsage(config.planUsagePath);
await mongo.hookEvents.insertOne({ ...data, ...planUsage, created_at: new Date() });
```

When `planUsage` is `{}` (file absent or unparseable), no extra fields are added to the document.

---

## Section 4: MongoDB Indexes

Both new indexes are added to the existing `Promise.allSettled` block in `src/mongo.ts`:

```ts
// Supports MCP query: latest live assistant line for a session
db.collection('transcript_lines').createIndex(
  { session_id: 1, 'line.type': 1, seq: -1 }
),

// Supports MCP query: latest hook event with plan-usage data for a session
db.collection('hook_events').createIndex(
  { session_id: 1, session_window_pct: 1, created_at: -1 }
),
```

`'line.type'` is a dotted-path index on the `line` subdocument field — valid MongoDB syntax.

The existing unique index `{ session_id: 1, seq: 1 }` on `transcript_lines` (ascending `seq`) is unaffected. The new index uses `seq: -1` (descending) — MongoDB treats these as distinct indexes; do **not** modify the existing unique index.

---

## Section 5: MCP Changes (`src/mcp.ts`)

**`get_session_context`** — the return value gains a `usage` field. All five sub-fields are `number | null`.

```json
{
  "usage": {
    "context_tokens":     31762,
    "context_pct":        15.88,
    "output_tokens":      191,
    "session_window_pct": 7,
    "weekly_pct":         3
  }
}
```

`context_pct` measures the **input-side** fraction of the context window. `output_tokens` is stored separately for informational use and is not included in `context_pct`.

Two additional queries inside `getSessionContext` (run in parallel with existing queries where possible):

```ts
const latestAssistant = await mongo.transcriptLines.findOne(
  { session_id, 'line.type': 'assistant', context_tokens: { $exists: true } },
  { sort: { seq: -1 }, projection: { _id: 0, context_tokens: 1, context_pct: 1, output_tokens: 1 } }
);

const latestUsageEvent = await mongo.hookEvents.findOne(
  { session_id, session_window_pct: { $exists: true } },
  { sort: { created_at: -1 }, projection: { _id: 0, session_window_pct: 1, weekly_pct: 1 } }
);

const usage = {
  context_tokens:     latestAssistant?.context_tokens      ?? null,
  context_pct:        latestAssistant?.context_pct         ?? null,
  output_tokens:      latestAssistant?.output_tokens       ?? null,
  session_window_pct: latestUsageEvent?.session_window_pct ?? null,
  weekly_pct:         latestUsageEvent?.weekly_pct          ?? null,
};
```

---

## Section 6: Error Handling

| Scenario | Behaviour |
|---|---|
| `plan-usage-history.json` absent | Cache returns `{}`; no `session_window_pct`/`weekly_pct` written; no crash |
| File unparseable | Same — last known cache (initially `{}`) is used |
| No samples in file | Same |
| Transcript line missing `message.usage` | No extra fields written; upsert proceeds normally |
| `line.message.model` absent or unknown | `context_pct` omitted; `context_tokens` and `output_tokens` still written |
| `get_session_context` — session has no live assistant lines | `context_tokens`, `context_pct`, `output_tokens` are `null` |
| `get_session_context` — session has no live hook events | `session_window_pct`, `weekly_pct` are `null` |
| Backfilled session | All five usage fields are `null` — expected and correct |

---

## Section 7: Testing

### `test/integration/daemon.test.ts` — two new tests

**Transcript line usage extraction:** Write a temp `.jsonl` file containing one `assistant` line with `message.model: "claude-sonnet-4-6"` and `message.usage: { input_tokens: 100, cache_creation_input_tokens: 5000, cache_read_input_tokens: 10000, output_tokens: 200 }`. POST a `SessionStart`-style event pointing `transcript_path` at the file. Wait for the tailer to process it. Assert: `transcript_lines` doc has `context_tokens: 15100`, `context_pct: 7.55`, `output_tokens: 200`.

**Hook event plan-usage enrichment:** Write a temp `plan-usage-history.json` with samples `[{ t: 1000, u: { fh: 10, sd: 5 } }, { t: 2000, u: { fh: 42, sd: 17 } }]` (two samples; latest by `t` is the second). Set `CLUED_PLAN_USAGE_PATH` to this file. POST any event. Assert: `hook_events` doc has `session_window_pct: 42` and `weekly_pct: 17`.

### `test/integration/mcp.test.ts` — one new test

**`get_session_context` usage block:** Use a dedicated session ID `sess-usage-1` (do not reuse existing seeded sessions `sess-1` / `sess-2` / `sess-3` to avoid coupling with existing test assertions). Directly insert: a `sessions` doc for `sess-usage-1`; a `transcript_lines` doc with `session_id: 'sess-usage-1'`, `seq: 0`, `context_tokens: 50000`, `context_pct: 25.0`, `output_tokens: 300`, `line: { type: 'assistant' }`; and a `hook_events` doc with `session_id: 'sess-usage-1'`, `session_window_pct: 10`, `weekly_pct: 5`. Call `get_session_context({ session_id: 'sess-usage-1' })`. Assert `result.usage` equals `{ context_tokens: 50000, context_pct: 25.0, output_tokens: 300, session_window_pct: 10, weekly_pct: 5 }`.

---

## Out of Scope

- Backfilling usage fields onto historical documents.
- Calling the Anthropic API directly for usage data (local file is sufficient).
- Supporting per-model context window sizes beyond the 200k constant.
- Exposing a dedicated MCP tool for usage-only queries.
- Including `output_tokens` in the `context_pct` numerator (stored for information only).
