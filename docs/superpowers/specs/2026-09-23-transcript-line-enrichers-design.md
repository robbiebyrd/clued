# Transcript Line Enrichers Design

Date: 2026-09-23

## Overview

Add a set of enrichers targeting the `transcript_lines` MongoDB collection to give consumers (MCP tools and dashboards) structured insight into each line without parsing raw JSONL on every query. All enrichers follow the existing `Enricher` interface in `src/enricher.ts` and live as individual `.ts` files in `enrichers/`.

## Background

`transcript_lines` stores `{ session_id, seq, line, created_at }` where `line` is a parsed JSON object from Claude's `.jsonl` transcript files. Lines vary widely in shape: user prompts, assistant messages (text, tool_use, thinking blocks), hook result attachments, and session metadata. Currently only `privacy-redact` (disabled) targets this collection.

The enrichment loop runs every 5 seconds, processing up to 100 unenriched docs per enricher per tick. Results are stored as `enriched.<name>` on the document. Failed enrichments are stored as `enriched.<name>_failed`. Docs without `enriched.<name>` are re-queried on every tick, so **enrichers that return `false` from `matches()` for a large portion of docs create ongoing query load** — all enrichers below match every doc to avoid this, returning a typed "not applicable" result for non-matching lines.

## Interface Extension: `batchLimit`

Add an optional `batchLimit` field to the `Enricher` interface to allow an enricher to cap how many docs it processes per tick, independent of the loop's default 100:

```ts
export interface Enricher {
  name:        string;
  collection:  string;
  enabled?:    boolean;
  batchLimit?: number;  // NEW: cap docs processed per tick (default: loop's limit)
  matches(doc: Record<string, unknown>): boolean;
  enrich(doc: Record<string, unknown>): Promise<unknown>;
}
```

`startEnrichmentLoop` uses `enricher.batchLimit ?? 100` in the `.limit()` call. This is needed for `line-summary` to avoid burst API costs.

## Enrichers

### 1. `line-classifier` (foundational)

**Collection:** `transcript_lines`

**Purpose:** Classify each line into a known type. Other enrichers and queries key off `enriched.line-classifier.type`.

**matches:** All docs (returns `{ type: 'unknown' }` for unrecognized shapes).

**Output shape:**
```ts
{
  type: 'user_prompt' | 'assistant' | 'tool_result' |
        'hook_success' | 'hook_error' | 'session_meta' | 'unknown';
}
```

**Classification rules (evaluated in order, first match wins):**
1. `line.type === 'last-prompt'` or `line.type === 'permission-mode'` → `session_meta`
2. `line.attachment?.type === 'hook_success'` → `hook_success`
3. `line.attachment?.type === 'hook_error'` → `hook_error`
4. `line.display != null && line.sessionId != null` → `user_prompt`
5. `line.message?.role === 'user'` and any content block has `type === 'tool_result'` → `tool_result`
6. `line.message?.role === 'assistant'` → `assistant` (covers text, tool_use, and thinking blocks — use `tool-use-summary` to distinguish sub-types)
7. Falls through → `unknown`

*Note: assistant messages may contain text, tool_use, and/or thinking blocks in the same message. Rather than attempting to sub-classify at this level, `line-classifier` uses a single `assistant` type and delegates sub-type details to `tool-use-summary`.*

---

### 2. `hook-linker`

**Collection:** `transcript_lines`

**Purpose:** Store the `tool_use_id` join key so queries can `$lookup` against `hook_events`. This enables joining transcript lines to the corresponding hook event (PreToolUse, PostToolUse, etc.) without duplicating hook payload data.

**matches:** All docs (returns `{ tool_use_ids: [], hook_event_ids: [] }` for lines with no tool-use content).

**Output shape:**
```ts
{
  tool_use_ids:    string[];    // all tool_use IDs found in this line
  hook_event_ids:  ObjectId[];  // _id values from matching hook_events docs
}
```

**Logic:**
1. Collect `tool_use_id` values from two sources:
   - `line.attachment?.toolUseID` (string) — present on hook result attachment lines
   - `line.message?.content[]` where `content[].type === 'tool_use'` → `content[].id`
2. Issue a **single** `hook_events` query per doc using `$in` across all collected IDs — not one query per ID. The exact field name in `hook_events` that stores the tool-use correlation ID **must be verified against real `hook_events` documents before implementation** — check candidate fields (`tool_use_id`, `toolUseId`, `id`) on actual stored docs and use whichever is present.
   ```ts
   hookEvents.find({ session_id, <verified_field>: { $in: toolUseIds } })
   ```
3. Store resulting `_id`s in `hook_event_ids`. Empty array is valid — not all tool_use blocks have a corresponding hook event.

**Note:** Uses a single batched `$in` query per doc. It processes at most 20 docs per tick (`batchLimit: 20`) to bound DB load.

---

### 3. `tool-use-summary`

**Collection:** `transcript_lines`

**Purpose:** Flatten nested tool_use and thinking content blocks into a top-level summary so queries don't need to dig through `line.message.content[]`.

**matches:** All docs (returns `{ tools: [], has_thinking: false }` for non-assistant lines).

**Output shape:**
```ts
{
  tools: Array<{
    id:        string;
    name:      string;
    inputKeys: string[];  // Object.keys(input) only — values stay in line.message to avoid duplication
  }>;
  has_thinking: boolean;
}
```

---

### 4. `error-flag`

**Collection:** `transcript_lines`

**Purpose:** Surface lines indicating failure so dashboards can filter on `enriched.error-flag.is_error`.

**matches:** All docs.

**Output shape:**
```ts
{
  is_error:   boolean;
  error_type: 'hook_error' | 'tool_failure' | 'error_text' | null;
}
```

**Detection rules (first match wins):**
1. `line.attachment?.type === 'hook_error'` → `hook_error`
2. `line.message?.role === 'user'` and any `tool_result` content block has `is_error: true` → `tool_failure`
3. Any text block in `line.message?.content` matches `/\b(Error:|ENOENT|EACCES|exception|stack trace|exit code [^0])/` → `error_text`
4. Otherwise `{ is_error: false, error_type: null }`

---

### 5. `message-stats`

**Collection:** `transcript_lines`

**Purpose:** Lightweight per-line metrics for analytics (prompt sizes, verbosity, cost estimation).

**matches:** All docs (returns zeros for lines with no text content).

**Output shape:**
```ts
{
  char_count:     number;  // total characters across all text-type content blocks concatenated; for user_prompt lines uses line.display
  word_count:     number;  // char_count split on /\s+/ and filtered
  block_count:    number;  // number of content blocks in line.message.content; 0 for non-message lines
  token_estimate: number;  // Math.ceil(char_count / 4)
}
```

---

### 6. `intent-classifier`

**Collection:** `transcript_lines`

**Purpose:** Classify user prompt lines by intent for analytics and filtering.

**matches:** All docs (returns `{ intent: null }` for non-user-prompt lines).

**Output shape:**
```ts
{
  intent: 'question' | 'instruction' | 'feedback' | 'correction' |
          'approval' | 'slash_command' | null;
}
```

**Classification rules (evaluated in order against `line.display`, word-boundary matching):**
1. `line.display` is null/undefined → `intent: null`
2. Starts with `/` → `slash_command`
3. Ends with `?` or starts with `\b(what|how|why|when|where|is|are|can|does|should|could|would)\b` → `question`
4. Contains `\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b` → `instruction`
5. Matches `\b(looks good|lgtm|go for it|proceed|yes|approved|sounds good)\b` (case-insensitive) OR is a single word that is `yes`/`ok`/`sure` → `approval`
6. Matches `^(no|wrong|incorrect|that's not|don't do|shouldn't)\b` (start-of-string anchor + word boundary) → `correction`
7. Otherwise → `feedback`

---

### 7. `code-language-detector`

**Collection:** `transcript_lines`

**Purpose:** Extract programming language tags from fenced code blocks in assistant text.

**matches:** All docs (returns `{ languages: [] }` for non-assistant lines or lines with no code blocks).

**Output shape:**
```ts
{
  languages: string[];  // deduplicated, lowercased, e.g. ['typescript', 'bash', 'json']
}
```

**Logic:** Regex scan of all text-type content blocks for ` ```<lang>\n ` patterns: `/^```(\w+)\s*$/gm`. Deduplicate and lowercase.

---

### 8. `skill-detector`

**Collection:** `transcript_lines`

**Purpose:** Detect Skill tool invocations to enable analytics on skill usage frequency.

**matches:** All docs (returns `{ skills: [] }` for non-assistant lines or lines with no Skill tool calls).

**Output shape:**
```ts
{
  skills: Array<{ name: string; args?: string }>;
}
```

**Logic:**
1. Look for tool_use blocks with `name === 'Skill'` in `line.message?.content[]` directly — do not depend on `enriched.tool-use-summary` being present, to avoid ordering issues. The fallback is reading the same source data independently.
2. Extract `input.skill` as `name` and `input.args` as `args` (both optional strings).

---

### 9. `line-summary` (optional, disabled by default)

**Collection:** `transcript_lines`

**Purpose:** LLM-generated one-sentence summary of complex assistant messages for dashboard previews.

**matches:** All docs (returns `{ summary: null, model: null }` for non-assistant lines or short messages). Internally skips enrich logic when `line.message?.role !== 'assistant'` or estimated token count ≤ 200.

**batchLimit:** 10 per tick to prevent API bursts.

**Output shape:**
```ts
{
  summary: string | null;
  model:   string | null;
}
```

**Notes:**
- Calls Claude API. Model: `claude-haiku-4-5-20251001` (verify against Anthropic docs before implementing — use the cheapest available Haiku model ID at implementation time).
- Gated by `enabled = false` by default; opt-in via removing from `disabledEnrichers` config or setting `enabled = true` in the file.
- Prompt: `"Summarize what Claude is doing in this message in one sentence (max 20 words):"` followed by the concatenated text content.

---

## Execution Order

No hard runtime dependencies. All enrichers have independent `matches()` logic that reads from the raw `line` field. `skill-detector` deliberately re-derives tool_use blocks from `line.message.content[]` rather than depending on `enriched.tool-use-summary` to avoid ordering assumptions.

## File Layout

```
enrichers/
  bash-binaries.ts          (existing, hook_events)
  privacy-redact.ts         (existing, transcript_lines, disabled)
  line-classifier.ts        (new)
  hook-linker.ts            (new)
  tool-use-summary.ts       (new)
  error-flag.ts             (new)
  message-stats.ts          (new)
  intent-classifier.ts      (new)
  code-language-detector.ts (new)
  skill-detector.ts         (new)
  line-summary.ts           (new, disabled by default)
```

## Interface Change

`src/enricher.ts` gains the optional `batchLimit?: number` field on `Enricher`, and `startEnrichmentLoop` uses `enricher.batchLimit ?? 100` in the `.limit()` call. This is a backwards-compatible additive change.

## Testing

Each enricher gets a unit test file in `test/` (e.g. `test/line-classifier.test.ts`) following the pattern of `test/privacy-redact.test.ts`:
- Import `matches`, `enrich`, `name`, `enabled` directly from the enricher file
- Test `matches()` with representative and edge-case inputs
- Test `enrich()` output shape for each distinct line type
- Test edge cases: empty content arrays, null/undefined fields, unexpected shapes

`hook-linker` requires MongoDB access for the join query. Its tests go in `test/integration/` and use a real MongoDB instance following the pattern in `test/integration/mongo.test.ts` (connect to `mongodb://localhost:27018`, use a timestamped test DB, drop it in `after()`). No mocks.

`line-summary` tests do not call the real Claude API — use `enabled = false` coverage only (verify it is disabled by default, verify `enrich()` returns `{ summary: null, model: null }` for non-assistant lines and for assistant lines below the token threshold).
