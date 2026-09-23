# Usage Enrichment Design

**Date:** 2026-09-23
**Author:** Robbie Byrd

---

## Overview

`clued` gains live usage enrichment: three metrics are captured as documents flow through the daemon and exposed via the existing `get_session_context` MCP tool.

| Metric | Source | Storage |
|---|---|---|
| Context window % | `assistant` transcript lines → `message.usage` token counts | `transcript_lines.context_pct` |
| Context token count | Same | `transcript_lines.context_tokens` |
| 5-hour session window % | `~/Library/Application Support/Claude/plan-usage-history.json` → `u.fh` | `hook_events.session_window_pct` |
| 7-day weekly usage % | Same file → `u.sd` | `hook_events.weekly_pct` |

All fields are written only for live events (daemon tailer / `POST /event`). The backfill path is unchanged — historical documents simply lack these fields.

---

## Section 1: Data Sources

### Context percentage — transcript lines

Every `assistant`-type transcript line contains `message.usage`:

```json
{
  "input_tokens": 3,
  "cache_creation_input_tokens": 31759,
  "cache_read_input_tokens": 0,
  "output_tokens": 191
}
```

Context window fill = `(input_tokens + cache_creation_input_tokens + cache_read_input_tokens) / MODEL_MAX_TOKENS`.

`MODEL_MAX_TOKENS` is 200,000 for all current Claude Sonnet and Opus models (`claude-sonnet-*`, `claude-opus-*`). If the model name doesn't match a known constant, `context_pct` is omitted and only `context_tokens` is written.

### Session window and weekly usage — plan-usage-history.json

Claude Code maintains `~/Library/Application Support/Claude/plan-usage-history.json`, a local cache refreshed every few minutes. Each sample:

```json
{
  "t": 1790190736395,
  "org": "3a4477ad-7c63-4ddd-8ce5-a9152cb5efc8",
  "u": { "fh": 7, "sd": 3 }
}
```

- `fh` — five-hour rolling session window, 0–100
- `sd` — seven-day rolling weekly usage, 0–100

The daemon reads this file at startup and re-reads it at most every 60 seconds (guarded by `lastReadAt` timestamp). The latest sample is used.

---

## Section 2: Config Changes

**`src/config.ts`** — one new field:

```ts
planUsagePath: string
// default: ~/Library/Application Support/Claude/plan-usage-history.json
// env override: CLUED_PLAN_USAGE_PATH
```

Expanded via the existing `expandHome()` helper.

---

## Section 3: Daemon Changes (`src/daemon.ts`)

### 3a — Plan-usage cache (module-level)

```ts
interface PlanUsage { session_window_pct?: number; weekly_pct?: number; }

let planUsageCache: PlanUsage = {};
let planUsageLastRead = 0;
const PLAN_USAGE_TTL = 60_000;

function readPlanUsage(path: string): PlanUsage {
  if (Date.now() - planUsageLastRead < PLAN_USAGE_TTL) return planUsageCache;
  try {
    const raw = readFileSync(path, 'utf8');
    const data = JSON.parse(raw);
    const samples: Array<{ u: { fh?: number; sd?: number } }> = data.samples ?? [];
    if (samples.length === 0) return planUsageCache;
    const last = samples[samples.length - 1];
    planUsageCache = {
      session_window_pct: last.u.fh,
      weekly_pct:         last.u.sd,
    };
  } catch {
    // file absent or unparseable — return last known cache
  }
  planUsageLastRead = Date.now();
  return planUsageCache;
}
```

Called once at startup (warm the cache) and on each `POST /event`.

### 3b — Transcript line enrichment (inside `tailFile` callback)

After parsing the raw line:

```ts
const extraFields: Record<string, unknown> = {};
if (
  typeof line === 'object' && line !== null &&
  (line as Record<string,unknown>).type === 'assistant'
) {
  const usage = (line as Record<string,unknown>).message?.usage;
  if (usage) {
    const ctx = (usage.input_tokens ?? 0)
              + (usage.cache_creation_input_tokens ?? 0)
              + (usage.cache_read_input_tokens ?? 0);
    extraFields.context_tokens = ctx;
    extraFields.output_tokens  = usage.output_tokens ?? 0;
    const max = modelContextMax((line as Record<string,unknown>).message?.model);
    if (max) extraFields.context_pct = +(ctx / max * 100).toFixed(2);
  }
}

mongo.transcriptLines.updateOne(
  { session_id, seq },
  { $set: { session_id, seq, line, ...extraFields }, $setOnInsert: { created_at: new Date() } },
  { upsert: true }
).catch(() => {});
```

`modelContextMax(model: unknown): number | null` returns `200_000` for strings matching `/^claude-(sonnet|opus)/i`, `null` otherwise.

### 3c — Hook event enrichment (`POST /event` handler)

Before `mongo.hookEvents.insertOne(...)`:

```ts
const planUsage = readPlanUsage(config.planUsagePath);
await mongo.hookEvents.insertOne({ ...data, ...planUsage, created_at: new Date() });
```

`planUsage` is `{}` when the file is absent, so no extra fields land on the document.

---

## Section 4: MongoDB Index

**`src/mongo.ts`** — one new index in `createClient()`, added to the `Promise.allSettled` block:

```ts
db.collection('transcript_lines').createIndex(
  { session_id: 1, 'line.type': 1, seq: -1 }
),
```

Supports the MCP query: "latest assistant line for a session."

---

## Section 5: MCP Changes (`src/mcp.ts`)

**`get_session_context`** — the return value gains a `usage` field:

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

All five fields are `number | null` — `null` when data is absent.

Implementation — two additional queries inside `getSessionContext`:

```ts
// Latest live assistant line for context%
const latestAssistant = await mongo.transcriptLines.findOne(
  { session_id, 'line.type': 'assistant', context_tokens: { $exists: true } },
  { sort: { seq: -1 }, projection: { _id: 0, context_tokens: 1, context_pct: 1, output_tokens: 1 } }
);

// Latest hook event with plan-usage data
const latestUsageEvent = await mongo.hookEvents.findOne(
  { session_id, session_window_pct: { $exists: true } },
  { sort: { created_at: -1 }, projection: { _id: 0, session_window_pct: 1, weekly_pct: 1 } }
);

const usage = {
  context_tokens:     latestAssistant?.context_tokens     ?? null,
  context_pct:        latestAssistant?.context_pct        ?? null,
  output_tokens:      latestAssistant?.output_tokens      ?? null,
  session_window_pct: latestUsageEvent?.session_window_pct ?? null,
  weekly_pct:         latestUsageEvent?.weekly_pct         ?? null,
};
```

---

## Section 6: Error Handling

| Scenario | Behaviour |
|---|---|
| `plan-usage-history.json` absent | Cache returns `{}`; no fields written to hook events; no crash |
| File unparseable | Same — last known cache (initially `{}`) is used |
| No samples in file | Same |
| Transcript line missing `usage` | No extra fields written; existing upsert proceeds normally |
| Unknown model name | `context_pct` omitted; `context_tokens` still written |
| `get_session_context` — no live lines yet | All five usage fields are `null` |

---

## Section 7: Testing

### `test/integration/daemon.test.ts` — two new tests

**Transcript line usage extraction:** Write a real `.jsonl` file containing an `assistant` line with `message.usage`, start the daemon with a temp `transcript_path`, wait for the tailer to process it, then assert that the inserted `transcript_lines` doc has `context_tokens`, `context_pct`, and `output_tokens`.

**Hook event plan-usage enrichment:** Write a temp `plan-usage-history.json` with a known sample (`fh: 42, sd: 17`), set `CLUED_PLAN_USAGE_PATH` to that path, POST an event, then assert the `hook_events` doc has `session_window_pct: 42` and `weekly_pct: 17`.

### `test/integration/mcp.test.ts` — one new test

**`get_session_context` usage block:** Seed a session with a `transcript_lines` doc that has `context_tokens: 50000`, `context_pct: 25.0`, `output_tokens: 300`, `line.type: 'assistant'`; and a `hook_events` doc with `session_window_pct: 10`, `weekly_pct: 5`. Call `get_session_context`. Assert `usage` contains all five fields with expected values.

---

## Out of Scope

- Backfilling usage fields onto historical documents.
- Calling the Anthropic API directly for usage data (local file is sufficient).
- Tracking per-model context window sizes beyond the current 200k constant.
- Exposing a dedicated MCP tool for usage-only queries.
