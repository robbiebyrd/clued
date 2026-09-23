# Transcript Line Enrichers Design

Date: 2026-09-23

## Overview

Add a set of enrichers targeting the `transcript_lines` MongoDB collection to give consumers (MCP tools and dashboards) structured insight into each line without parsing raw JSONL on every query. All enrichers follow the existing `Enricher` interface in `src/enricher.ts` and live as individual `.ts` files in `enrichers/`.

## Background

`transcript_lines` stores `{ session_id, seq, line, created_at }` where `line` is a parsed JSON object from Claude's `.jsonl` transcript files. Lines vary widely in shape: user prompts, assistant messages (text, tool_use, thinking blocks), hook result attachments, and session metadata. Currently only `privacy-redact` (disabled) targets this collection.

The enrichment loop runs every 5 seconds, processing up to 100 unenriched docs per enricher per tick. Results are stored as `enriched.<name>` on the document.

## Enrichers

### 1. `line-classifier` (foundational)

**Collection:** `transcript_lines`

**Purpose:** Classify each line into a known type so all other enrichers and queries can key off `enriched.line-classifier.type` instead of re-deriving it.

**matches:** All docs (every line needs classification).

**Output shape:**
```ts
{
  type: 'user_prompt' | 'assistant_text' | 'tool_use' | 'tool_result' |
        'hook_success' | 'hook_error' | 'session_meta' | 'thinking' | 'unknown';
}
```

**Classification rules:**
- `line.display` present + `line.sessionId` present → `user_prompt`
- `line.type === 'last-prompt'` or `line.type === 'permission-mode'` → `session_meta`
- `line.attachment.type === 'hook_success'` → `hook_success`
- `line.attachment.type === 'hook_error'` → `hook_error`
- `line.message.role === 'assistant'` + content has `tool_use` blocks → `tool_use`
- `line.message.role === 'assistant'` + content has `thinking` blocks → `thinking`
- `line.message.role === 'assistant'` → `assistant_text`
- `line.message.role === 'user'` + content has `tool_result` blocks → `tool_result`
- Falls through → `unknown`

---

### 2. `hook-linker`

**Collection:** `transcript_lines`

**Purpose:** For lines that correspond to a hook event, store the `tool_use_id` join key so queries can `$lookup` against `hook_events`.

**matches:** Lines where `line.attachment.toolUseID` is a string, OR lines where the assistant message contains at least one `tool_use` content block.

**Output shape:**
```ts
{
  tool_use_ids: string[];         // all tool_use IDs found in this line
  hook_event_ids: ObjectId[];     // _id values from matching hook_events docs
}
```

**Logic:**
1. Collect `tool_use_id` values: from `line.attachment.toolUseID` and/or from `line.message.content[].id` where `content[].type === 'tool_use'`.
2. For each ID, query `hook_events` where `tool_use_id` matches and `session_id` matches — store resulting `_id`s.
3. If no matching hook events found, store empty arrays (not an error — not all tool uses have hook events).

---

### 3. `tool-use-summary`

**Collection:** `transcript_lines`

**Purpose:** Flatten nested tool_use content blocks into a top-level summary array for efficient querying.

**matches:** Lines where `line.message.role === 'assistant'` and content contains at least one `tool_use` block.

**Output shape:**
```ts
{
  tools: Array<{
    id:        string;
    name:      string;
    inputKeys: string[];  // Object.keys(input) — not values, to avoid re-storing large inputs
  }>;
}
```

---

### 4. `error-flag`

**Collection:** `transcript_lines`

**Purpose:** Surface lines that indicate failure so dashboards and queries can filter on `enriched.error-flag.is_error` without parsing line content.

**matches:** All docs.

**Output shape:**
```ts
{
  is_error:   boolean;
  error_type: 'hook_error' | 'tool_failure' | 'error_text' | null;
}
```

**Detection rules:**
- `line.attachment.type === 'hook_error'` → `hook_error`
- `line.message.role === 'user'` and any `tool_result` content has `is_error: true` → `tool_failure`
- Assistant text contains common error patterns (`Error:`, `failed`, `exception`, `ENOENT`, etc.) → `error_text`
- Otherwise `is_error: false`, `error_type: null`

---

### 5. `message-stats`

**Collection:** `transcript_lines`

**Purpose:** Lightweight metrics per line for analytics (session length, prompt sizes, verbosity).

**matches:** Lines with a `line.message` or `line.display` field.

**Output shape:**
```ts
{
  char_count:    number;
  word_count:    number;
  block_count:   number;  // number of content blocks in message, 0 for simple display lines
  token_estimate: number; // Math.ceil(char_count / 4)
}
```

---

### 6. `intent-classifier`

**Collection:** `transcript_lines`

**Purpose:** Classify user prompt lines by intent for analytics and filtering.

**matches:** Lines where `line.display` is a non-empty string.

**Output shape:**
```ts
{
  intent: 'question' | 'instruction' | 'feedback' | 'correction' |
          'approval' | 'slash_command' | 'unknown';
}
```

**Classification rules (keyword/pattern, no LLM):**
- Starts with `/` → `slash_command`
- Ends with `?` or starts with interrogative (what, how, why, when, where, is, can, does, should) → `question`
- Contains "fix", "add", "remove", "change", "update", "make", "create", "write", "implement", "refactor" → `instruction`
- Contains "good", "great", "looks good", "lgtm", "yes", "go for it", "proceed", "approve" → `approval`
- Contains "no", "wrong", "incorrect", "that's not", "don't", "shouldn't" → `correction`
- Contains "but", "however", "also", "and", "feedback" without strong instruction signals → `feedback`
- Falls through → `unknown`

---

### 7. `code-language-detector`

**Collection:** `transcript_lines`

**Purpose:** Extract programming languages from fenced code blocks in assistant text responses.

**matches:** Lines where `line.message.role === 'assistant'` and content has text blocks.

**Output shape:**
```ts
{
  languages: string[];  // e.g. ['typescript', 'bash', 'json']
}
```

**Logic:** Regex scan of all text content for ` ```<lang> ` patterns, deduplicated, lowercased.

---

### 8. `skill-detector`

**Collection:** `transcript_lines`

**Purpose:** Detect Skill tool invocations in assistant messages to enable analytics on skill usage frequency and patterns.

**matches:** Lines where `line.message.role === 'assistant'` and `enriched.tool-use-summary.tools` includes `name === 'Skill'` (or fallback: content has `tool_use` block with `name === 'Skill'`).

**Output shape:**
```ts
{
  skills: Array<{ name: string; args?: string }>;
}
```

---

### 9. `line-summary` (optional, external)

**Collection:** `transcript_lines`

**Purpose:** LLM-generated one-sentence summary of complex assistant messages for dashboard previews.

**matches:** Lines where `line.message.role === 'assistant'` and estimated token count > 200 (keyed off `enriched.message-stats.token_estimate` if present, otherwise derived inline).

**Output shape:**
```ts
{
  summary: string;  // one sentence
  model:   string;  // model used to generate
}
```

**Notes:**
- Calls Claude API (`claude-haiku-4-5-20251001` for cost efficiency).
- Should be gated behind a config flag (`disabledEnrichers` or a new `enabledEnrichers` allowlist) since it has ongoing cost.
- Rate-limit: process at most 10 per enrichment tick to avoid bursts.

---

## Execution Order

No hard runtime dependencies between enrichers (the loop runs all of them independently). However, `hook-linker` and `skill-detector` perform best after `line-classifier` has run — but since they have their own `matches()` logic, they degrade gracefully if classification hasn't happened yet.

## File Layout

```
enrichers/
  bash-binaries.ts        (existing, hook_events)
  privacy-redact.ts       (existing, transcript_lines, disabled)
  line-classifier.ts      (new)
  hook-linker.ts          (new)
  tool-use-summary.ts     (new)
  error-flag.ts           (new)
  message-stats.ts        (new)
  intent-classifier.ts    (new)
  code-language-detector.ts (new)
  skill-detector.ts       (new)
  line-summary.ts         (new, optional/disabled by default)
```

## Testing

Each enricher gets a unit test in `test/enrichers/` exercising:
- `matches()` returns true/false for correct shapes
- `enrich()` produces the correct output shape for representative inputs
- Edge cases: empty content arrays, missing fields, unknown shapes

The `hook-linker` requires a test double for the MongoDB call — inject a mock `hookEvents` collection or test with an in-memory fixture.
