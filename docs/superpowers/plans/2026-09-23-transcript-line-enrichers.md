# Transcript Line Enrichers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add nine enrichers that annotate `transcript_lines` MongoDB documents with structured metadata extracted from the raw Claude JSONL content, plus one disabled LLM-summary enricher, enabling efficient querying and dashboard analytics.

**Architecture:** Each enricher is a standalone `.ts` module in `enrichers/` exporting `name`, `collection`, `enabled`, `matches()`, and `enrich()`. The existing enrichment loop in `src/enricher.ts` runs all enrichers every 5 seconds. Two interface additions are required first: an optional `batchLimit` field (to cap docs per tick) and passing `mongo` as a second argument to `enrich()` (needed by `hook-linker` to query `hook_events`).

**Tech Stack:** TypeScript (strict ESM), Node.js built-in test runner (`node:test`), MongoDB driver (`mongodb`), no new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-23-transcript-line-enrichers-design.md`

---

## File Structure

**Modified:**
- `src/enricher.ts` — add `batchLimit?: number` to `Enricher` interface; pass `mongo` as second arg to `enrich()`

**Created:**
- `enrichers/line-classifier.ts`
- `enrichers/tool-use-summary.ts`
- `enrichers/error-flag.ts`
- `enrichers/message-stats.ts`
- `enrichers/intent-classifier.ts`
- `enrichers/code-language-detector.ts`
- `enrichers/skill-detector.ts`
- `enrichers/hook-linker.ts`
- `enrichers/line-summary.ts`

**Tests created:**
- `test/line-classifier.test.ts`
- `test/tool-use-summary.test.ts`
- `test/error-flag.test.ts`
- `test/message-stats.test.ts`
- `test/intent-classifier.test.ts`
- `test/code-language-detector.test.ts`
- `test/skill-detector.test.ts`
- `test/line-summary.test.ts`
- `test/integration/hook-linker.test.ts`

---

## Task 1: Extend the Enricher interface

**Files:**
- Modify: `src/enricher.ts`
- Modify: `test/enricher.test.ts`

The `enrich()` method needs a `mongo` parameter (so `hook-linker` can query `hook_events` without module-level state). `batchLimit` caps docs per tick for expensive enrichers. Existing enrichers (`bash-binaries`, `privacy-redact`) use `enrich(doc)` with one argument — TypeScript allows fewer parameters than the declared type, so they don't need changes.

- [ ] **Step 1: Update the interface and loop in `src/enricher.ts`**

Replace the file contents with:

```ts
import { readdir } from 'fs/promises';
import { join, extname } from 'path';
import { pathToFileURL } from 'url';
import type { MongoDb } from './mongo';
import type { Config } from './config';

export interface Enricher {
  name:        string;
  collection:  string;
  enabled?:    boolean;
  batchLimit?: number;
  matches(doc: Record<string, unknown>): boolean;
  enrich(doc: Record<string, unknown>, mongo: MongoDb): Promise<unknown>;
}

export async function loadEnrichers(
  enrichersDir: string,
  config: Pick<Config, 'disabledEnrichers'>,
): Promise<Enricher[]> {
  let files: string[];
  try {
    files = await readdir(enrichersDir);
  } catch {
    return [];
  }

  const EXTS = new Set(['.ts', '.mjs']);
  const enrichers: Enricher[] = [];
  for (const file of files.filter(f => EXTS.has(extname(f)))) {
    const mod = await import(pathToFileURL(join(enrichersDir, file)).href) as Enricher;
    if (mod.enabled === false) continue;
    if ((config.disabledEnrichers ?? []).includes(mod.name)) continue;
    enrichers.push(mod);
  }
  return enrichers;
}

export function startEnrichmentLoop(
  mongo: MongoDb,
  enrichers: Enricher[],
): { stop: () => void } {
  const timer = setInterval(async () => {
    for (const enricher of enrichers) {
      const coll      = mongo.db.collection(enricher.collection);
      const failedKey = `enriched.${enricher.name}_failed`;
      const doneKey   = `enriched.${enricher.name}`;
      const limit     = enricher.batchLimit ?? 100;
      const docs = await coll
        .find({ [doneKey]: { $exists: false }, [failedKey]: { $exists: false } })
        .limit(limit)
        .toArray()
        .catch((err: Error) => { console.error('clued enricher query failed:', err.message); return []; });

      for (const doc of docs) {
        const d = doc as Record<string, unknown>;
        if (!enricher.matches(d)) continue;
        try {
          const result = await enricher.enrich(d, mongo);
          await coll.updateOne({ _id: doc._id }, { $set: { [doneKey]: result } });
        } catch (err) {
          const e = err as Error;
          console.error(`clued enricher "${enricher.name}" failed on ${doc._id}:`, e.message);
          await coll.updateOne({ _id: doc._id }, {
            $set: { [failedKey]: { message: e.message, at: new Date() } },
          });
        }
      }
    }
  }, 5000);

  return { stop: () => clearInterval(timer) };
}
```

- [ ] **Step 2: Update `test/enricher.test.ts` — add batchLimit test**

The existing mock `enrich: async () => ...` lambdas do NOT need to be updated. The `Enricher.enrich` is declared using method shorthand syntax in the interface (`enrich(doc, mongo): Promise<unknown>`), which TypeScript checks bivariantly — a function with fewer parameters is assignable. `pnpm typecheck` will pass without modifying the existing mocks.

Add this test at the end of `test/enricher.test.ts`:

```ts
test('startEnrichmentLoop respects batchLimit', async (t) => {
  t.mock.timers.enable({ apis: ['setInterval'] });

  const docs = Array.from({ length: 150 }, (_, i) => ({ _id: `id${i}` }));
  const queried: number[] = [];
  const mockCollection = {
    find: () => ({ limit: (n: number) => { queried.push(n); return { toArray: async () => [] }; } }),
    updateOne: async () => {},
  };
  const mockMongo = { db: { collection: () => mockCollection } } as unknown as MongoDb;
  const enricher = {
    name: 'limited',
    collection: 'hook_events',
    batchLimit: 25,
    matches: () => true,
    enrich: async () => ({}),
  };

  const loop = startEnrichmentLoop(mockMongo, [enricher]);
  t.mock.timers.tick(5000);
  await new Promise(resolve => setImmediate(resolve));
  loop.stop();

  assert.ok(queried.length >= 1, 'at least one query made');
  assert.ok(queried.every(n => n === 25), `expected limit 25, got: ${queried}`);
});
```

- [ ] **Step 3: Run the enricher tests**

```
node --import tsx/esm --test 'test/enricher.test.ts'
```

Expected: all tests pass including the new batchLimit test.

- [ ] **Step 4: Run typecheck**

```
pnpm typecheck
```

Expected: no errors.

- [ ] **Step 5: Commit**

```bash
git add src/enricher.ts test/enricher.test.ts
git commit -m "feat: add batchLimit and mongo param to Enricher interface"
```

---

## Task 2: `line-classifier`

**Files:**
- Create: `enrichers/line-classifier.ts`
- Create: `test/line-classifier.test.ts`

- [ ] **Step 1: Write the failing test — `test/line-classifier.test.ts`**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/line-classifier';

test('name and collection', () => {
  assert.equal(name, 'line-classifier');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
  assert.equal(matches({ anything: true }), true);
});

test('classifies session_meta: last-prompt', async () => {
  const r = await enrich({ line: { type: 'last-prompt', sessionId: 'abc' } });
  assert.deepEqual(r, { type: 'session_meta' });
});

test('classifies session_meta: permission-mode', async () => {
  const r = await enrich({ line: { type: 'permission-mode' } });
  assert.deepEqual(r, { type: 'session_meta' });
});

test('classifies hook_success', async () => {
  const r = await enrich({ line: { attachment: { type: 'hook_success' } } });
  assert.deepEqual(r, { type: 'hook_success' });
});

test('classifies hook_error', async () => {
  const r = await enrich({ line: { attachment: { type: 'hook_error' } } });
  assert.deepEqual(r, { type: 'hook_error' });
});

test('classifies user_prompt', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'abc', timestamp: 123 } });
  assert.deepEqual(r, { type: 'user_prompt' });
});

test('classifies tool_result', async () => {
  const r = await enrich({
    line: { message: { role: 'user', content: [{ type: 'tool_result', tool_use_id: 'x', content: [] }] } },
  });
  assert.deepEqual(r, { type: 'tool_result' });
});

test('classifies assistant', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [{ type: 'text', text: 'hello' }] } },
  });
  assert.deepEqual(r, { type: 'assistant' });
});

test('classifies assistant with tool_use blocks', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [{ type: 'tool_use', id: 'x', name: 'Bash', input: {} }] } },
  });
  assert.deepEqual(r, { type: 'assistant' });
});

test('classifies unknown for unrecognized shapes', async () => {
  const r = await enrich({ line: { foo: 'bar' } });
  assert.deepEqual(r, { type: 'unknown' });
});

test('classifies unknown when line is null', async () => {
  const r = await enrich({ line: null });
  assert.deepEqual(r, { type: 'unknown' });
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/line-classifier.test.ts'
```

Expected: MODULE_NOT_FOUND or import error.

- [ ] **Step 3: Implement `enrichers/line-classifier.ts`**

```ts
export const name       = 'line-classifier';
export const collection = 'transcript_lines';
export const enabled    = true;

type LineType = 'user_prompt' | 'assistant' | 'tool_result' |
               'hook_success' | 'hook_error' | 'session_meta' | 'unknown';

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ type: LineType }> {
  const line = doc.line as Record<string, unknown> | null | undefined;
  if (!line || typeof line !== 'object') return { type: 'unknown' };

  if (line.type === 'last-prompt' || line.type === 'permission-mode') return { type: 'session_meta' };

  const att = line.attachment as Record<string, unknown> | undefined;
  if (att?.type === 'hook_success') return { type: 'hook_success' };
  if (att?.type === 'hook_error')   return { type: 'hook_error' };

  if (line.display != null && line.sessionId != null) return { type: 'user_prompt' };

  const msg = line.message as Record<string, unknown> | undefined;
  const content = (msg?.content ?? []) as Array<Record<string, unknown>>;

  if (msg?.role === 'user' && content.some(b => b.type === 'tool_result')) return { type: 'tool_result' };
  if (msg?.role === 'assistant') return { type: 'assistant' };

  return { type: 'unknown' };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/line-classifier.test.ts'
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add enrichers/line-classifier.ts test/line-classifier.test.ts
git commit -m "feat: add line-classifier enricher"
```

---

## Task 3: `tool-use-summary`

**Files:**
- Create: `enrichers/tool-use-summary.ts`
- Create: `test/tool-use-summary.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/tool-use-summary';

test('name and collection', () => {
  assert.equal(name, 'tool-use-summary');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns empty tools and no thinking for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { tools: [], has_thinking: false });
});

test('extracts tool_use blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'tu1', name: 'Bash', input: { command: 'ls', cwd: '/tmp' } },
          { type: 'text', text: 'done' },
        ],
      },
    },
  });
  assert.deepEqual(r, {
    tools: [{ id: 'tu1', name: 'Bash', inputKeys: ['command', 'cwd'] }],
    has_thinking: false,
  });
});

test('detects thinking block', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'thinking', thinking: 'hmm' },
          { type: 'tool_use', id: 'tu2', name: 'Read', input: { file_path: '/x' } },
        ],
      },
    },
  });
  assert.equal(r.has_thinking, true);
  assert.equal(r.tools.length, 1);
  assert.equal(r.tools[0].name, 'Read');
});

test('multiple tool_use blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'a', name: 'Bash', input: { command: 'ls' } },
          { type: 'tool_use', id: 'b', name: 'Read', input: { file_path: '/x' } },
        ],
      },
    },
  });
  assert.equal(r.tools.length, 2);
});

test('returns empty for assistant with no tool_use or thinking', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [{ type: 'text', text: 'hi' }] } },
  });
  assert.deepEqual(r, { tools: [], has_thinking: false });
});

test('handles missing or null content gracefully', async () => {
  const r = await enrich({ line: { message: { role: 'assistant' } } });
  assert.deepEqual(r, { tools: [], has_thinking: false });
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/tool-use-summary.test.ts'
```

- [ ] **Step 3: Implement `enrichers/tool-use-summary.ts`**

```ts
export const name       = 'tool-use-summary';
export const collection = 'transcript_lines';
export const enabled    = true;

interface ToolSummary { id: string; name: string; inputKeys: string[]; }
interface Result { tools: ToolSummary[]; has_thinking: boolean; }

const EMPTY: Result = { tools: [], has_thinking: false };

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return EMPTY;

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const tools: ToolSummary[] = [];
  let has_thinking = false;

  for (const block of content) {
    if (block.type === 'thinking') {
      has_thinking = true;
    } else if (block.type === 'tool_use') {
      tools.push({
        id:        block.id as string,
        name:      block.name as string,
        inputKeys: Object.keys((block.input as Record<string, unknown>) ?? {}),
      });
    }
  }

  return { tools, has_thinking };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/tool-use-summary.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/tool-use-summary.ts test/tool-use-summary.test.ts
git commit -m "feat: add tool-use-summary enricher"
```

---

## Task 4: `error-flag`

**Files:**
- Create: `enrichers/error-flag.ts`
- Create: `test/error-flag.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/error-flag';

test('name and collection', () => {
  assert.equal(name, 'error-flag');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('no error for empty line', async () => {
  const r = await enrich({ line: {} });
  assert.deepEqual(r, { is_error: false, error_type: null });
});

test('detects hook_error', async () => {
  const r = await enrich({ line: { attachment: { type: 'hook_error' } } });
  assert.deepEqual(r, { is_error: true, error_type: 'hook_error' });
});

test('detects tool_failure via is_error on tool_result block', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'user',
        content: [{ type: 'tool_result', tool_use_id: 'x', is_error: true, content: 'failed' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'tool_failure' });
});

test('no tool_failure when is_error is false on tool_result', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'user',
        content: [{ type: 'tool_result', tool_use_id: 'x', is_error: false, content: 'ok' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: false, error_type: null });
});

test('detects error_text: ENOENT', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'ENOENT: no such file or directory' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'error_text' });
});

test('detects error_text: Error:', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'Error: connection refused' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'error_text' });
});

test('no false positive on normal assistant text', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'The function works correctly.' }],
      },
    },
  });
  assert.deepEqual(r, { is_error: false, error_type: null });
});

test('hook_error takes precedence over text pattern', async () => {
  const r = await enrich({
    line: {
      attachment: { type: 'hook_error' },
      message: { role: 'assistant', content: [{ type: 'text', text: 'Error: something' }] },
    },
  });
  assert.deepEqual(r, { is_error: true, error_type: 'hook_error' });
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/error-flag.test.ts'
```

- [ ] **Step 3: Implement `enrichers/error-flag.ts`**

```ts
export const name       = 'error-flag';
export const collection = 'transcript_lines';
export const enabled    = true;

type ErrorType = 'hook_error' | 'tool_failure' | 'error_text' | null;
interface Result { is_error: boolean; error_type: ErrorType; }

const ERROR_TEXT_RE = /\b(Error:|ENOENT|EACCES|exception|stack trace|exit code [^0])/;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown> | undefined;
  if (!line) return { is_error: false, error_type: null };

  const att = line.attachment as Record<string, unknown> | undefined;
  if (att?.type === 'hook_error') return { is_error: true, error_type: 'hook_error' };

  const msg = line.message as Record<string, unknown> | undefined;
  const content = (msg?.content ?? []) as Array<Record<string, unknown>>;

  if (msg?.role === 'user' && content.some(b => b.type === 'tool_result' && b.is_error === true)) {
    return { is_error: true, error_type: 'tool_failure' };
  }

  for (const block of content) {
    if (block.type === 'text' && typeof block.text === 'string' && ERROR_TEXT_RE.test(block.text)) {
      return { is_error: true, error_type: 'error_text' };
    }
  }

  return { is_error: false, error_type: null };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/error-flag.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/error-flag.ts test/error-flag.test.ts
git commit -m "feat: add error-flag enricher"
```

---

## Task 5: `message-stats`

**Files:**
- Create: `enrichers/message-stats.ts`
- Create: `test/message-stats.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/message-stats';

test('name and collection', () => {
  assert.equal(name, 'message-stats');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns zeros for line with no text content', async () => {
  const r = await enrich({ line: {} });
  assert.deepEqual(r, { char_count: 0, word_count: 0, block_count: 0, token_estimate: 0 });
});

test('counts chars and words from user_prompt display field', async () => {
  const r = await enrich({ line: { display: 'hello world', sessionId: 'x' } });
  assert.equal(r.char_count, 11);
  assert.equal(r.word_count, 2);
  assert.equal(r.block_count, 0);
  assert.equal(r.token_estimate, Math.ceil(11 / 4));
});

test('counts chars across all text-type content blocks concatenated', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'text', text: 'hello ' },
          { type: 'tool_use', id: 'x', name: 'Bash', input: {} },
          { type: 'text', text: 'world' },
        ],
      },
    },
  });
  assert.equal(r.char_count, 11); // 'hello ' + 'world', not tool_use input
  assert.equal(r.block_count, 3);
});

test('token_estimate is ceil(char_count / 4)', async () => {
  const r = await enrich({ line: { display: 'hi', sessionId: 'x' } });
  assert.equal(r.token_estimate, 1); // ceil(2/4) = 1
});

test('word_count splits on whitespace', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'user',
        content: [{ type: 'text', text: '  one   two  three  ' }],
      },
    },
  });
  assert.equal(r.word_count, 3);
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/message-stats.test.ts'
```

- [ ] **Step 3: Implement `enrichers/message-stats.ts`**

```ts
export const name       = 'message-stats';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { char_count: number; word_count: number; block_count: number; token_estimate: number; }

const ZERO: Result = { char_count: 0, word_count: 0, block_count: 0, token_estimate: 0 };

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown> | undefined;
  if (!line) return ZERO;

  let text = '';
  let block_count = 0;

  if (typeof line.display === 'string' && line.sessionId != null) {
    text = line.display;
  } else {
    const msg = line.message as Record<string, unknown> | undefined;
    const content = (msg?.content ?? []) as Array<Record<string, unknown>>;
    block_count = content.length;
    for (const block of content) {
      if (block.type === 'text' && typeof block.text === 'string') text += block.text;
      if (block.type === 'thinking' && typeof block.thinking === 'string') text += block.thinking;
    }
  }

  const char_count   = text.length;
  const word_count   = text.trim() ? text.trim().split(/\s+/).length : 0;
  const token_estimate = Math.ceil(char_count / 4);

  return { char_count, word_count, block_count, token_estimate };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/message-stats.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/message-stats.ts test/message-stats.test.ts
git commit -m "feat: add message-stats enricher"
```

---

## Task 6: `intent-classifier`

**Files:**
- Create: `enrichers/intent-classifier.ts`
- Create: `test/intent-classifier.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/intent-classifier';

test('name and collection', () => {
  assert.equal(name, 'intent-classifier');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns null intent for non-user-prompt lines', async () => {
  const r = await enrich({ line: { message: { role: 'assistant', content: [] } } });
  assert.deepEqual(r, { intent: null });
});

test('returns null intent when display is absent', async () => {
  const r = await enrich({ line: {} });
  assert.deepEqual(r, { intent: null });
});

test('classifies slash_command', async () => {
  const r = await enrich({ line: { display: '/clear', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'slash_command' });
});

test('classifies question by trailing ?', async () => {
  const r = await enrich({ line: { display: 'Is this correct?', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'question' });
});

test('classifies question by interrogative opener', async () => {
  const r = await enrich({ line: { display: 'How do I fix this?', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'question' });
});

test('classifies instruction', async () => {
  const r = await enrich({ line: { display: 'Fix the bug in auth.ts', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'instruction' });
});

test('classifies approval: go for it', async () => {
  const r = await enrich({ line: { display: 'go for it', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'approval' });
});

test('classifies approval: lgtm', async () => {
  const r = await enrich({ line: { display: 'lgtm', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'approval' });
});

test('classifies correction: starts with no', async () => {
  const r = await enrich({ line: { display: 'no that is wrong', sessionId: 'x' } });
  assert.deepEqual(r, { intent: 'correction' });
});

test('no false positive correction for "no" mid-sentence', async () => {
  const r = await enrich({ line: { display: 'Make sure no tests fail', sessionId: 'x' } });
  // does not start with "no", so must not classify as correction
  assert.notEqual(r.intent, 'correction');
});

test('falls through to feedback for display with no strong signal', async () => {
  const r = await enrich({ line: { display: 'Interesting approach', sessionId: 'x' } });
  assert.equal(r.intent, 'feedback');
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/intent-classifier.test.ts'
```

- [ ] **Step 3: Implement `enrichers/intent-classifier.ts`**

```ts
export const name       = 'intent-classifier';
export const collection = 'transcript_lines';
export const enabled    = true;

type Intent = 'question' | 'instruction' | 'feedback' | 'correction' | 'approval' | 'slash_command' | null;

const QUESTION_OPENER = /^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b/i;
const INSTRUCTION     = /\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b/i;
const APPROVAL        = /\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b/i;
const CORRECTION      = /^(no|wrong|incorrect|that's not|don't do|shouldn't)\b/i;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ intent: Intent }> {
  const line    = doc.line as Record<string, unknown> | undefined;
  const display = line?.display;
  if (typeof display !== 'string') return { intent: null };

  const d = display.trim();
  if (!d) return { intent: null };

  if (d.startsWith('/')) return { intent: 'slash_command' };
  if (d.endsWith('?') || QUESTION_OPENER.test(d)) return { intent: 'question' };
  if (INSTRUCTION.test(d)) return { intent: 'instruction' };

  const lower = d.toLowerCase();
  if (APPROVAL.test(lower) || lower === 'yes' || lower === 'ok' || lower === 'sure') {
    return { intent: 'approval' };
  }
  if (CORRECTION.test(d)) return { intent: 'correction' };

  return { intent: 'feedback' };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/intent-classifier.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/intent-classifier.ts test/intent-classifier.test.ts
git commit -m "feat: add intent-classifier enricher"
```

---

## Task 7: `code-language-detector`

**Files:**
- Create: `enrichers/code-language-detector.ts`
- Create: `test/code-language-detector.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/code-language-detector';

test('name and collection', () => {
  assert.equal(name, 'code-language-detector');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns empty languages for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { languages: [] });
});

test('extracts single language from fenced code block', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'Here:\n```typescript\nconst x = 1;\n```' }],
      },
    },
  });
  assert.deepEqual(r, { languages: ['typescript'] });
});

test('extracts multiple languages, deduplicated, lowercased', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: '```Bash\nls\n```\nand\n```BASH\necho hi\n```\nand\n```json\n{}\n```' }],
      },
    },
  });
  assert.deepEqual(r.languages.sort(), ['bash', 'json']);
});

test('returns empty for assistant text with no code blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'No code here.' }],
      },
    },
  });
  assert.deepEqual(r, { languages: [] });
});

test('handles empty content array', async () => {
  const r = await enrich({
    line: { message: { role: 'assistant', content: [] } },
  });
  assert.deepEqual(r, { languages: [] });
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/code-language-detector.test.ts'
```

- [ ] **Step 3: Implement `enrichers/code-language-detector.ts`**

```ts
export const name       = 'code-language-detector';
export const collection = 'transcript_lines';
export const enabled    = true;

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ languages: string[] }> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { languages: [] };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const seen = new Set<string>();

  for (const block of content) {
    if (block.type !== 'text' || typeof block.text !== 'string') continue;
    // Regex created per-call to avoid module-scope gm flag lastIndex footgun
    const fenceRe = /^```(\w+)\s*$/gm;
    for (const m of block.text.matchAll(fenceRe)) {
      seen.add(m[1].toLowerCase());
    }
  }

  return { languages: [...seen] };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/code-language-detector.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/code-language-detector.ts test/code-language-detector.test.ts
git commit -m "feat: add code-language-detector enricher"
```

---

## Task 8: `skill-detector`

**Files:**
- Create: `enrichers/skill-detector.ts`
- Create: `test/skill-detector.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/skill-detector';

test('name and collection', () => {
  assert.equal(name, 'skill-detector');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
});

test('returns empty skills for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { skills: [] });
});

test('detects a Skill tool invocation', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'x', name: 'Skill', input: { skill: 'brainstorming', args: 'feature X' } },
        ],
      },
    },
  });
  assert.deepEqual(r, { skills: [{ name: 'brainstorming', args: 'feature X' }] });
});

test('ignores non-Skill tool_use blocks', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'a', name: 'Bash', input: { command: 'ls' } },
          { type: 'tool_use', id: 'b', name: 'Skill', input: { skill: 'tdd' } },
        ],
      },
    },
  });
  assert.deepEqual(r, { skills: [{ name: 'tdd' }] });
});

test('handles missing args gracefully', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: 'x', name: 'Skill', input: { skill: 'no-args-skill' } },
        ],
      },
    },
  });
  assert.deepEqual(r, { skills: [{ name: 'no-args-skill' }] });
});

test('multiple skill invocations in one message', async () => {
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: '1', name: 'Skill', input: { skill: 'alpha' } },
          { type: 'tool_use', id: '2', name: 'Skill', input: { skill: 'beta', args: 'x' } },
        ],
      },
    },
  });
  assert.equal(r.skills.length, 2);
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/skill-detector.test.ts'
```

- [ ] **Step 3: Implement `enrichers/skill-detector.ts`**

```ts
export const name       = 'skill-detector';
export const collection = 'transcript_lines';
export const enabled    = true;

interface SkillRef { name: string; args?: string; }

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ skills: SkillRef[] }> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { skills: [] };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const skills: SkillRef[] = [];

  for (const block of content) {
    if (block.type !== 'tool_use' || block.name !== 'Skill') continue;
    const input = block.input as Record<string, unknown> | undefined;
    const skillName = input?.skill;
    if (typeof skillName !== 'string') continue;
    const ref: SkillRef = { name: skillName };
    if (typeof input?.args === 'string') ref.args = input.args;
    skills.push(ref);
  }

  return { skills };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/skill-detector.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/skill-detector.ts test/skill-detector.test.ts
git commit -m "feat: add skill-detector enricher"
```

---

## Task 9: `hook-linker`

**Files:**
- Create: `enrichers/hook-linker.ts`
- Create: `test/integration/hook-linker.test.ts`

This enricher queries `hook_events` to find the matching hook event for a tool_use ID. It receives `mongo` as the second argument to `enrich()` (added in Task 1). Before implementing, you **must verify the actual field name used in `hook_events` documents** to store the tool-use correlation ID — run this query against your local MongoDB instance:

```bash
# Connect to your local MongoDB (port 27017 for dev, 27018 for test)
# and inspect a hook_events document that relates to a tool call:
mongosh --eval 'db = db.getSiblingDB("clued"); db.hook_events.findOne({ tool_name: { $exists: true } })' mongodb://localhost:27017
```

Look for a field that looks like a UUID (e.g., `tool_use_id`, `toolUseId`). Update the constant `TOOL_USE_ID_FIELD` in the implementation below with the verified name.

- [ ] **Step 0: Verify the real `hook_events` field name before writing any code**

Run this against your local dev MongoDB (port 27017, database `clued`) to find the field that stores the tool-use correlation ID:

```bash
mongosh --eval 'db = db.getSiblingDB("clued"); printjson(db.hook_events.findOne({ tool_name: { $exists: true } }))' mongodb://localhost:27017
```

Look for a UUID-shaped field (e.g., `tool_use_id`, `toolUseId`). Set `TOOL_USE_ID_FIELD` in `hook-linker.ts` to whatever you find. **The integration tests insert their own documents using that same field name — they are self-consistent but will not catch a wrong field name against production data.** The manual check here is the only way to verify correctness against real hook events.

- [ ] **Step 1: Write the failing integration test — `test/integration/hook-linker.test.ts`**

```ts
import { test, after } from 'node:test';
import assert from 'node:assert/strict';
import { ObjectId } from 'mongodb';
import { createClient } from '../../src/mongo';
import { matches, enrich, name, collection } from '../../enrichers/hook-linker';
import type { MongoDb } from '../../src/mongo';

const TEST_CONFIG = {
  mongoUrl: process.env.CLUED_MONGO_URL || 'mongodb://localhost:27018',
  dbName:   `clued_test_${Date.now()}`,
};

let mongo: MongoDb;
after(async () => {
  if (mongo) {
    await mongo.db.dropDatabase();
    await mongo.close();
  }
});

test('setup', async () => {
  mongo = await createClient(TEST_CONFIG);
});

test('name and collection', () => {
  assert.equal(name, 'hook-linker');
  assert.equal(collection, 'transcript_lines');
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
  assert.equal(matches({ anything: 1 }), true);
});

test('returns empty arrays for line with no tool_use content', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } }, mongo);
  assert.deepEqual(r, { tool_use_ids: [], hook_event_ids: [] });
});

test('collects tool_use_id from attachment.toolUseID and finds matching hook event', async () => {
  const toolUseId = 'test-tuid-1';
  const session_id = 'sess-hook-test';

  // Insert a hook event with the known tool_use ID (using the verified field name)
  const hookDoc = { session_id, tool_name: 'Bash', created_at: new Date() } as Record<string, unknown>;
  // Replace 'tool_use_id' with the actual verified field name if different:
  hookDoc['tool_use_id'] = toolUseId;
  const inserted = await mongo.hookEvents.insertOne(hookDoc);

  const doc = {
    session_id,
    line: { attachment: { type: 'hook_success', toolUseID: toolUseId, hookEvent: 'PostToolUse' } },
  };

  const r = await enrich(doc, mongo);
  assert.deepEqual(r.tool_use_ids, [toolUseId]);
  assert.equal(r.hook_event_ids.length, 1);
  assert.deepEqual(r.hook_event_ids[0], inserted.insertedId);
});

test('collects tool_use ids from assistant message content blocks', async () => {
  const toolUseId = 'test-tuid-2';
  const session_id = 'sess-hook-test-2';

  const hookDoc = { session_id, tool_name: 'Read', created_at: new Date() } as Record<string, unknown>;
  hookDoc['tool_use_id'] = toolUseId;
  const inserted = await mongo.hookEvents.insertOne(hookDoc);

  const doc = {
    session_id,
    line: {
      message: {
        role: 'assistant',
        content: [
          { type: 'tool_use', id: toolUseId, name: 'Read', input: { file_path: '/x' } },
        ],
      },
    },
  };

  const r = await enrich(doc, mongo);
  assert.deepEqual(r.tool_use_ids, [toolUseId]);
  assert.equal(r.hook_event_ids.length, 1);
  assert.deepEqual(r.hook_event_ids[0], inserted.insertedId);
});

test('returns empty hook_event_ids when no matching hook event exists', async () => {
  const doc = {
    session_id: 'no-session',
    line: { attachment: { type: 'hook_success', toolUseID: 'nonexistent-id' } },
  };
  const r = await enrich(doc, mongo);
  assert.deepEqual(r.tool_use_ids, ['nonexistent-id']);
  assert.deepEqual(r.hook_event_ids, []);
});

test('deduplicates tool_use_ids from both sources', async () => {
  const toolUseId = 'duplicate-id';
  const session_id = 'sess-dedup';
  const doc = {
    session_id,
    line: {
      attachment: { type: 'hook_success', toolUseID: toolUseId },
      message: {
        role: 'assistant',
        content: [{ type: 'tool_use', id: toolUseId, name: 'Bash', input: {} }],
      },
    },
  };
  const r = await enrich(doc, mongo);
  assert.equal(r.tool_use_ids.length, 1);
  assert.deepEqual(r.tool_use_ids, [toolUseId]);
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/integration/hook-linker.test.ts'
```

Expected: import error (module not found).

- [ ] **Step 3: Implement `enrichers/hook-linker.ts`**

**Before writing this file, verify `TOOL_USE_ID_FIELD` by inspecting a real `hook_events` doc as described above.**

```ts
import type { MongoDb } from '../src/mongo';

export const name        = 'hook-linker';
export const collection  = 'transcript_lines';
export const enabled     = true;
export const batchLimit  = 20;

// Verified against real hook_events docs — update if the field name differs.
const TOOL_USE_ID_FIELD = 'tool_use_id';

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(
  doc: Record<string, unknown>,
  mongo: MongoDb,
): Promise<{ tool_use_ids: string[]; hook_event_ids: unknown[] }> {
  const line       = doc.line as Record<string, unknown> | undefined;
  const session_id = doc.session_id as string | undefined;
  const ids        = new Set<string>();

  const attId = (line?.attachment as Record<string, unknown> | undefined)?.toolUseID;
  if (typeof attId === 'string') ids.add(attId);

  const content = ((line?.message as Record<string, unknown> | undefined)?.content ?? []) as Array<Record<string, unknown>>;
  for (const block of content) {
    if (block.type === 'tool_use' && typeof block.id === 'string') ids.add(block.id);
  }

  const tool_use_ids = [...ids];
  if (tool_use_ids.length === 0) return { tool_use_ids: [], hook_event_ids: [] };

  const filter: Record<string, unknown> = { [TOOL_USE_ID_FIELD]: { $in: tool_use_ids } };
  if (session_id) filter.session_id = session_id;

  const events = await mongo.hookEvents
    .find(filter, { projection: { _id: 1 } })
    .toArray();

  return { tool_use_ids, hook_event_ids: events.map(e => e._id) };
}
```

- [ ] **Step 4: Run the integration tests** (requires MongoDB running on port 27018)

```
pnpm test:integration
```

Expected: all integration tests pass including hook-linker tests.

- [ ] **Step 5: Run typecheck**

```
pnpm typecheck
```

- [ ] **Step 6: Commit**

```bash
git add enrichers/hook-linker.ts test/integration/hook-linker.test.ts
git commit -m "feat: add hook-linker enricher"
```

---

## Task 10: `line-summary` (disabled)

**Files:**
- Create: `enrichers/line-summary.ts`
- Create: `test/line-summary.test.ts`

This enricher is disabled by default (`enabled = false`). Tests verify the disabled state and that `enrich()` returns null for non-qualifying lines — no real API calls are made.

- [ ] **Step 1: Write the failing test**

```ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, enabled, batchLimit, matches, enrich } from '../enrichers/line-summary';

test('name, collection, batchLimit', () => {
  assert.equal(name, 'line-summary');
  assert.equal(collection, 'transcript_lines');
  assert.equal(batchLimit, 10);
});

test('is disabled by default', () => {
  assert.equal(enabled, false);
});

test('matches all docs', () => {
  assert.equal(matches({}), true);
  assert.equal(matches({ line: null }), true);
});

test('returns null summary for non-assistant line', async () => {
  const r = await enrich({ line: { display: 'hello', sessionId: 'x' } });
  assert.deepEqual(r, { summary: null, model: null });
});

test('returns null summary for assistant line below token threshold', async () => {
  // Short text — well under 200 tokens
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'short reply' }],
      },
    },
  });
  assert.deepEqual(r, { summary: null, model: null });
});

test('returns null summary for assistant line above token threshold (API stub, no network call)', async () => {
  // >800 chars → >200 token estimate — hits the stub return, no API called
  const longText = 'word '.repeat(200); // 1000 chars
  const r = await enrich({
    line: {
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: longText }],
      },
    },
  });
  // Confirms stub path is exercised and no exception is thrown (no real API import needed)
  assert.deepEqual(r, { summary: null, model: null });
});
```

- [ ] **Step 2: Run to confirm it fails**

```
node --import tsx/esm --test 'test/line-summary.test.ts'
```

- [ ] **Step 3: Implement `enrichers/line-summary.ts`**

The API call path is stubbed — it returns null until enabled and wired to a real Anthropic SDK import.

```ts
export const name        = 'line-summary';
export const collection  = 'transcript_lines';
export const enabled     = false;
export const batchLimit  = 10;

const TOKEN_THRESHOLD  = 200;
const MODEL            = 'claude-haiku-4-5-20251001';

export function matches(_doc: Record<string, unknown>): boolean {
  return true;
}

export async function enrich(doc: Record<string, unknown>): Promise<{ summary: string | null; model: string | null }> {
  const msg = (doc.line as Record<string, unknown> | undefined)?.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { summary: null, model: null };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  const text    = content
    .filter(b => b.type === 'text' && typeof b.text === 'string')
    .map(b => b.text as string)
    .join('');

  if (Math.ceil(text.length / 4) <= TOKEN_THRESHOLD) return { summary: null, model: null };

  // Requires @anthropic-ai/sdk — add to package.json dependencies when enabling.
  // import Anthropic from '@anthropic-ai/sdk';
  // const client = new Anthropic();
  // const response = await client.messages.create({
  //   model: MODEL,
  //   max_tokens: 60,
  //   messages: [{ role: 'user', content: `Summarize what Claude is doing in this message in one sentence (max 20 words):\n\n${text}` }],
  // });
  // const summary = (response.content[0] as { type: 'text'; text: string }).text.trim();
  // return { summary, model: MODEL };

  return { summary: null, model: null };
}
```

- [ ] **Step 4: Run tests**

```
node --import tsx/esm --test 'test/line-summary.test.ts'
```

- [ ] **Step 5: Commit**

```bash
git add enrichers/line-summary.ts test/line-summary.test.ts
git commit -m "feat: add line-summary enricher stub (disabled)"
```

---

## Task 11: Full test suite pass

- [ ] **Step 1: Run all unit tests**

```
pnpm test
```

Expected: all pass, zero failures.

- [ ] **Step 2: Run integration tests**

```
pnpm test:integration
```

Expected: all pass.

- [ ] **Step 3: Run typecheck and lint**

```
pnpm typecheck && pnpm lint
```

Expected: no errors.

- [ ] **Step 4: Final commit if any fixes were needed**

```bash
git add -p
git commit -m "fix: address typecheck/lint issues in enrichers"
```
