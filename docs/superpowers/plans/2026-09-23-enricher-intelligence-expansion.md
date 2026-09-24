# Enricher Intelligence Expansion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add 8 new deterministic enrichers (5 for `hook_events`, 3 for `transcript_lines`) that extract structured intelligence from raw session data so the MCP server can search and filter with meaningful fields.

**Architecture:** Each enricher is a standalone TypeScript module exporting `name`, `collection`, `enabled`, `matches()`, and `enrich()`. A shared `enrichers/lang.ts` utility provides the extension-to-language map used by three enrichers. All logic is pure and synchronous except for the `enrich()` async signature required by the enricher loader.

**Tech Stack:** TypeScript (ESM), `node:test`, `node:assert/strict`, no external deps beyond what already exists.

---

## File Structure

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `enrichers/lang.ts` | Shared extension→language map and `extToLang()` helper |
| Create | `enrichers/tool-classifier.ts` | Categorises every PreToolUse event by operation type |
| Create | `enrichers/file-tracker.ts` | Tracks file paths, languages, operations, and artifact types |
| Create | `enrichers/bash-outcome.ts` | Records success/failure and output metrics for Bash calls |
| Create | `enrichers/edit-diff-stats.ts` | Counts lines added/removed for Edit and Write calls |
| Create | `enrichers/prompt-features.ts` | Extracts intent, word count, and task-start signal from prompts |
| Create | `enrichers/attachment-extractor.ts` | Extracts file metadata from attachment transcript lines |
| Create | `enrichers/thinking-stats.ts` | Counts thinking and text characters in assistant lines |
| Create | `enrichers/file-snapshot-extractor.ts` | Lists tracked files from file-history-snapshot lines |
| Create | `test/lang.test.ts` | Tests for extToLang |
| Create | `test/tool-classifier.test.ts` | Tests for tool-classifier |
| Create | `test/file-tracker.test.ts` | Tests for file-tracker |
| Create | `test/bash-outcome.test.ts` | Tests for bash-outcome |
| Create | `test/edit-diff-stats.test.ts` | Tests for edit-diff-stats |
| Create | `test/prompt-features.test.ts` | Tests for prompt-features |
| Create | `test/attachment-extractor.test.ts` | Tests for attachment-extractor |
| Create | `test/thinking-stats.test.ts` | Tests for thinking-stats |
| Create | `test/file-snapshot-extractor.test.ts` | Tests for file-snapshot-extractor |

---

### Task 1: Shared language utility (`enrichers/lang.ts`)

**Files:**
- Create: `enrichers/lang.ts`
- Create: `test/lang.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/lang.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { extToLang } from '../enrichers/lang.js';

test('returns null for null input', () => {
  assert.equal(extToLang(null), null);
});

test('returns null for no-extension path', () => {
  assert.equal(extToLang('Makefile'), null);
});

test('maps .ts to typescript', () => {
  assert.equal(extToLang('src/foo.ts'), 'typescript');
});

test('maps .tsx to typescript', () => {
  assert.equal(extToLang('components/Button.tsx'), 'typescript');
});

test('maps .py to python', () => {
  assert.equal(extToLang('script.py'), 'python');
});

test('maps .json to json', () => {
  assert.equal(extToLang('package.json'), 'json');
});

test('maps .yaml to yaml', () => {
  assert.equal(extToLang('config.yaml'), 'yaml');
});

test('maps .yml to yaml', () => {
  assert.equal(extToLang('.github/ci.yml'), 'yaml');
});

test('returns null for unknown extension', () => {
  assert.equal(extToLang('archive.zip'), null);
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/lang.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/lang.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/lang.ts
export const EXT_LANG: Record<string, string> = {
  '.ts': 'typescript', '.tsx': 'typescript',
  '.js': 'javascript', '.jsx': 'javascript', '.mjs': 'javascript', '.cjs': 'javascript',
  '.py': 'python', '.rb': 'ruby', '.go': 'go', '.rs': 'rust',
  '.java': 'java', '.c': 'c', '.cpp': 'cpp', '.cs': 'csharp',
  '.sh': 'bash', '.bash': 'bash',
  '.md': 'markdown', '.mdx': 'markdown',
  '.json': 'json', '.yaml': 'yaml', '.yml': 'yaml', '.toml': 'toml',
  '.env': 'dotenv',
  '.html': 'html', '.css': 'css', '.scss': 'css', '.sass': 'css',
  '.vue': 'vue', '.svelte': 'svelte',
  '.sql': 'sql', '.graphql': 'graphql',
};

export function extToLang(filePath: string | null): string | null {
  if (!filePath) return null;
  const dot = filePath.lastIndexOf('.');
  if (dot < 0) return null;
  return EXT_LANG[filePath.slice(dot)] ?? null;
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/lang.test.ts'
```

Expected: all 9 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/lang.ts test/lang.test.ts
git commit -m "feat: add shared extension-to-language utility"
```

---

### Task 2: `tool-classifier` enricher

**Files:**
- Create: `enrichers/tool-classifier.ts`
- Create: `test/tool-classifier.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/tool-classifier.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/tool-classifier.js';

test('name and collection', () => {
  assert.equal(name, 'tool-classifier');
  assert.equal(collection, 'hook_events');
});

test('matches PreToolUse only', () => {
  assert.equal(matches({ hook_event_name: 'PreToolUse' }), true);
  assert.equal(matches({ hook_event_name: 'PostToolUse' }), false);
  assert.equal(matches({ hook_event_name: 'UserPromptSubmit' }), false);
  assert.equal(matches({}), false);
});

test('Read → file_read, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Read' });
  assert.deepEqual(r, { category: 'file_read', is_read_only: true });
});

test('Write → file_write, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Write' });
  assert.deepEqual(r, { category: 'file_write', is_read_only: false });
});

test('Edit → file_edit, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Edit' });
  assert.deepEqual(r, { category: 'file_edit', is_read_only: false });
});

test('Bash → shell_exec, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Bash' });
  assert.deepEqual(r, { category: 'shell_exec', is_read_only: false });
});

test('WebFetch → web_fetch, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'WebFetch' });
  assert.deepEqual(r, { category: 'web_fetch', is_read_only: true });
});

test('WebSearch → web_fetch, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'WebSearch' });
  assert.deepEqual(r, { category: 'web_fetch', is_read_only: true });
});

test('Grep → code_search, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Grep' });
  assert.deepEqual(r, { category: 'code_search', is_read_only: true });
});

test('Glob → code_search, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Glob' });
  assert.deepEqual(r, { category: 'code_search', is_read_only: true });
});

test('LS → code_search, is_read_only: true', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'LS' });
  assert.deepEqual(r, { category: 'code_search', is_read_only: true });
});

test('Agent → agent_dispatch, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'Agent' });
  assert.deepEqual(r, { category: 'agent_dispatch', is_read_only: false });
});

test('unknown tool → other, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse', tool_name: 'SomeFutureTool' });
  assert.deepEqual(r, { category: 'other', is_read_only: false });
});

test('missing tool_name → other, is_read_only: false', async () => {
  const r = await enrich({ hook_event_name: 'PreToolUse' });
  assert.deepEqual(r, { category: 'other', is_read_only: false });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/tool-classifier.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/tool-classifier.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/tool-classifier.ts
export const name       = 'tool-classifier';
export const collection = 'hook_events';
export const enabled    = true;

type Category = 'file_read' | 'file_write' | 'file_edit' | 'shell_exec' | 'web_fetch' | 'code_search' | 'agent_dispatch' | 'other';
interface Result { category: Category; is_read_only: boolean; }

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PreToolUse';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const tool = doc.tool_name as string | undefined;
  if (tool === 'Read')                          return { category: 'file_read',      is_read_only: true  };
  if (tool === 'Write')                         return { category: 'file_write',     is_read_only: false };
  if (tool === 'Edit')                          return { category: 'file_edit',      is_read_only: false };
  if (tool === 'Bash')                          return { category: 'shell_exec',     is_read_only: false };
  if (tool === 'WebFetch' || tool === 'WebSearch') return { category: 'web_fetch',  is_read_only: true  };
  if (tool === 'Grep' || tool === 'Glob' || tool === 'LS') return { category: 'code_search', is_read_only: true };
  if (tool === 'Agent' || tool === 'Task')      return { category: 'agent_dispatch', is_read_only: false };
  return { category: 'other', is_read_only: false };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/tool-classifier.test.ts'
```

Expected: all 13 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/tool-classifier.ts test/tool-classifier.test.ts
git commit -m "feat: add tool-classifier enricher for hook_events"
```

---

### Task 3: `file-tracker` enricher

**Files:**
- Create: `enrichers/file-tracker.ts`
- Create: `test/file-tracker.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/file-tracker.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/file-tracker.js';

test('name and collection', () => {
  assert.equal(name, 'file-tracker');
  assert.equal(collection, 'hook_events');
});

test('matches PreToolUse + file tool', () => {
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Read' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Edit' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Write' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Glob' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'LS' }), true);
});

test('does not match Bash or PostToolUse', () => {
  assert.equal(matches({ hook_event_name: 'PreToolUse', tool_name: 'Bash' }), false);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Read' }), false);
});

test('Read extracts file_path, language, operation=read', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: '/src/app.ts' },
  });
  assert.equal(r.file_path, '/src/app.ts');
  assert.equal(r.language, 'typescript');
  assert.equal(r.operation, 'read');
});

test('Write extracts file_path and operation=write', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Write',
    tool_input: { file_path: '/src/util.py', content: 'x' },
  });
  assert.equal(r.operation, 'write');
  assert.equal(r.language, 'python');
});

test('Edit extracts operation=edit', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Edit',
    tool_input: { file_path: '/src/main.go', old_string: 'a', new_string: 'b' },
  });
  assert.equal(r.operation, 'edit');
  assert.equal(r.language, 'go');
});

test('Glob → operation=glob, no language', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Glob',
    tool_input: { pattern: '**/*.ts' },
  });
  assert.equal(r.operation, 'glob');
  assert.equal(r.file_path, null);
  assert.equal(r.language, null);
});

test('artifact_type: test wins by path pattern', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'test/foo.test.ts' },
  });
  assert.equal(r.artifact_type, 'test');
});

test('artifact_type: spec wins by path segment', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'docs/specs/my-design.md' },
  });
  assert.equal(r.artifact_type, 'spec');
});

test('artifact_type: plan wins by filename keyword', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'docs/plans/2026-01-01-roadmap.md' },
  });
  assert.equal(r.artifact_type, 'plan');
});

test('artifact_type: doc for plain .md not matching higher priority', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'README.md' },
  });
  assert.equal(r.artifact_type, 'doc');
});

test('artifact_type: config for .json', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'tsconfig.json' },
  });
  assert.equal(r.artifact_type, 'config');
});

test('artifact_type: code for .ts outside test/spec/plan', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Edit',
    tool_input: { file_path: 'src/daemon.ts', old_string: '', new_string: '' },
  });
  assert.equal(r.artifact_type, 'code');
});

test('artifact_type: null for unknown extension', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'Read',
    tool_input: { file_path: 'data/export.parquet' },
  });
  assert.equal(r.artifact_type, null);
});

test('null file_path when tool_input has no path fields', async () => {
  const r = await enrich({
    hook_event_name: 'PreToolUse',
    tool_name: 'LS',
    tool_input: {},
  });
  assert.equal(r.file_path, null);
  assert.equal(r.language, null);
  assert.equal(r.artifact_type, null);
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/file-tracker.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/file-tracker.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/file-tracker.ts
import { extToLang } from './lang.js';

export const name       = 'file-tracker';
export const collection = 'hook_events';
export const enabled    = true;

type ArtifactType = 'plan' | 'spec' | 'doc' | 'test' | 'config' | 'code' | null;
type Operation    = 'read' | 'write' | 'edit' | 'glob' | 'ls';
interface Result { file_path: string | null; language: string | null; operation: Operation; artifact_type: ArtifactType; }

const FILE_OPS: Record<string, Operation> = {
  Read: 'read', Write: 'write', Edit: 'edit', Glob: 'glob', LS: 'ls',
};

const CONFIG_EXTS = new Set(['.json', '.yaml', '.yml', '.toml', '.env']);
const SOURCE_EXTS = new Set([
  '.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs',
  '.py', '.rb', '.go', '.rs', '.java', '.c', '.cpp', '.cs',
  '.sh', '.bash', '.html', '.css', '.scss', '.vue', '.svelte', '.sql', '.graphql',
]);

function detectArtifact(fp: string | null): ArtifactType {
  if (!fp) return null;
  const lower = fp.toLowerCase();
  const base  = lower.split('/').pop() ?? '';
  const dot   = base.lastIndexOf('.');
  const ext   = dot >= 0 ? base.slice(dot) : '';

  // Path-segment checks first — directory context beats filename keywords.
  if (/\/tests?\//.test(lower)) return 'test';
  if (/\/specs?\//.test(lower)) return 'spec';
  if (/\/plans?\//.test(lower)) return 'plan';
  // Filename pattern checks (lower confidence than directory context).
  if (/\.(test|spec)\.[^.]+$/.test(lower)) return 'test';
  if (/\b(spec|design|architecture)\b/.test(base)) return 'spec';
  if (/\b(plan|roadmap|todo)\b/.test(base)) return 'plan';
  if (ext === '.md' || ext === '.mdx') return 'doc';
  if (CONFIG_EXTS.has(ext)) return 'config';
  if (SOURCE_EXTS.has(ext)) return 'code';
  return null;
}

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PreToolUse' && typeof FILE_OPS[doc.tool_name as string] === 'string';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const operation = FILE_OPS[doc.tool_name as string];
  const ti        = doc.tool_input as Record<string, unknown> | undefined;
  const file_path = ((ti?.file_path ?? ti?.path) as string | undefined) ?? null;
  return {
    file_path,
    language:      extToLang(file_path),
    operation,
    artifact_type: detectArtifact(file_path),
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/file-tracker.test.ts'
```

Expected: all 15 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/file-tracker.ts test/file-tracker.test.ts
git commit -m "feat: add file-tracker enricher for hook_events"
```

---

### Task 4: `bash-outcome` enricher

**Files:**
- Create: `enrichers/bash-outcome.ts`
- Create: `test/bash-outcome.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/bash-outcome.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/bash-outcome.js';

test('name and collection', () => {
  assert.equal(name, 'bash-outcome');
  assert.equal(collection, 'hook_events');
});

test('matches PostToolUse + Bash only', () => {
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Bash' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse',  tool_name: 'Bash' }), false);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Read' }), false);
  assert.equal(matches({}), false);
});

test('success when stderr is empty string', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'hello\nworld', stderr: '' },
  });
  assert.equal(r.success, true);
  assert.equal(r.has_error, false);
  assert.equal(r.output_lines, 2);
  assert.equal(r.truncated, false);
});

test('success when stderr is absent', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'ok' },
  });
  assert.equal(r.success, true);
});

test('failure when stderr is non-empty', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: '', stderr: 'command not found' },
  });
  assert.equal(r.success, false);
  assert.equal(r.has_error, true);
});

test('truncated=true when stdout contains [truncated]', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'line1\n[truncated]', stderr: '' },
  });
  assert.equal(r.truncated, true);
});

test('output_lines=0 when stdout is absent', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: {},
  });
  assert.equal(r.output_lines, 0);
});

test('counts lines correctly for multi-line output', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'a\nb\nc', stderr: '' },
  });
  assert.equal(r.output_lines, 3);
});

test('success when stderr is null', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Bash',
    tool_response: { stdout: 'ok', stderr: null },
  });
  assert.equal(r.success, true);
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/bash-outcome.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/bash-outcome.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/bash-outcome.ts
export const name       = 'bash-outcome';
export const collection = 'hook_events';
export const enabled    = true;

interface Result { success: boolean; has_error: boolean; output_lines: number; truncated: boolean; }

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PostToolUse' && doc.tool_name === 'Bash';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const tr     = doc.tool_response as Record<string, unknown> | undefined;
  const stderr = tr?.stderr;
  const stdout = (tr?.stdout ?? '') as string;
  const success = !stderr || stderr === '';
  return {
    success,
    has_error:    !success,
    output_lines: stdout ? stdout.split('\n').length : 0,
    truncated:    stdout.includes('[truncated]'),
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/bash-outcome.test.ts'
```

Expected: all 9 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/bash-outcome.ts test/bash-outcome.test.ts
git commit -m "feat: add bash-outcome enricher for hook_events"
```

---

### Task 5: `edit-diff-stats` enricher

**Files:**
- Create: `enrichers/edit-diff-stats.ts`
- Create: `test/edit-diff-stats.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/edit-diff-stats.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/edit-diff-stats.js';

test('name and collection', () => {
  assert.equal(name, 'edit-diff-stats');
  assert.equal(collection, 'hook_events');
});

test('matches PostToolUse + Edit or Write', () => {
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Edit' }),  true);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Write' }), true);
  assert.equal(matches({ hook_event_name: 'PostToolUse', tool_name: 'Read' }),  false);
  assert.equal(matches({ hook_event_name: 'PreToolUse',  tool_name: 'Edit' }),  false);
  assert.equal(matches({}), false);
});

test('Edit: counts lines added and removed', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: {
      old_string: 'line1\nline2\nline3',
      new_string: 'line1\nline2',
    },
  });
  assert.equal(r.lines_added, 2);
  assert.equal(r.lines_removed, 3);
  assert.equal(r.net_change, -1);
});

test('Edit: net_change positive when adding lines', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: { old_string: 'a', new_string: 'a\nb\nc' },
  });
  assert.equal(r.lines_added, 3);
  assert.equal(r.lines_removed, 1);
  assert.equal(r.net_change, 2);
});

test('Edit: empty strings give 0 lines', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Edit',
    tool_input: { old_string: '', new_string: '' },
  });
  assert.equal(r.lines_added, 0);
  assert.equal(r.lines_removed, 0);
  assert.equal(r.net_change, 0);
});

test('Write: lines_removed is always 0', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: { file_path: '/x.ts', content: 'a\nb\nc' },
  });
  assert.equal(r.lines_added, 3);
  assert.equal(r.lines_removed, 0);
  assert.equal(r.net_change, 3);
});

test('Write: empty content → 0 added', async () => {
  const r = await enrich({
    hook_event_name: 'PostToolUse',
    tool_name: 'Write',
    tool_input: { file_path: '/x.ts', content: '' },
  });
  assert.equal(r.lines_added, 0);
  assert.equal(r.net_change, 0);
});

test('missing tool_input strings treated as empty', async () => {
  const r = await enrich({ hook_event_name: 'PostToolUse', tool_name: 'Edit', tool_input: {} });
  assert.deepEqual(r, { lines_added: 0, lines_removed: 0, net_change: 0 });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/edit-diff-stats.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/edit-diff-stats.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/edit-diff-stats.ts
export const name       = 'edit-diff-stats';
export const collection = 'hook_events';
export const enabled    = true;

interface Result { lines_added: number; lines_removed: number; net_change: number; }

function countLines(s: string): number {
  return s ? s.split('\n').length : 0;
}

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'PostToolUse' && (doc.tool_name === 'Edit' || doc.tool_name === 'Write');
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  // tool_input is forwarded on PostToolUse events by Claude Code.
  const ti = doc.tool_input as Record<string, unknown> | undefined;
  if (doc.tool_name === 'Write') {
    const lines_added = countLines((ti?.content ?? '') as string);
    return { lines_added, lines_removed: 0, net_change: lines_added };
  }
  const lines_added   = countLines((ti?.new_string ?? '') as string);
  const lines_removed = countLines((ti?.old_string ?? '') as string);
  return { lines_added, lines_removed, net_change: lines_added - lines_removed };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/edit-diff-stats.test.ts'
```

Expected: all 8 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/edit-diff-stats.ts test/edit-diff-stats.test.ts
git commit -m "feat: add edit-diff-stats enricher for hook_events"
```

---

### Task 6: `prompt-features` enricher

**Files:**
- Create: `enrichers/prompt-features.ts`
- Create: `test/prompt-features.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/prompt-features.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/prompt-features.js';

test('name and collection', () => {
  assert.equal(name, 'prompt-features');
  assert.equal(collection, 'hook_events');
});

test('matches UserPromptSubmit only', () => {
  assert.equal(matches({ hook_event_name: 'UserPromptSubmit' }), true);
  assert.equal(matches({ hook_event_name: 'PreToolUse' }), false);
  assert.equal(matches({}), false);
});

test('empty prompt → zeroed fields', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: '' });
  assert.equal(r.word_count, 0);
  assert.equal(r.is_slash_command, false);
  assert.equal(r.command_name, null);
  assert.equal(r.intent, null);
  assert.equal(r.looks_like_task_start, false);
});

test('slash command detected', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: '/clear' });
  assert.equal(r.is_slash_command, true);
  assert.equal(r.command_name, 'clear');
  assert.equal(r.intent, null);
});

test('slash command with args extracts name only', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: '/reload-plugins foo' });
  assert.equal(r.command_name, 'reload-plugins');
});

test('question intent by trailing ?', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'Is this correct?' });
  assert.equal(r.intent, 'question');
});

test('question intent by opener word', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'How do I fix this' });
  assert.equal(r.intent, 'question');
});

test('instruction intent', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'Fix the failing tests in auth.ts' });
  assert.equal(r.intent, 'instruction');
});

test('approval intent: ok', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'ok' });
  assert.equal(r.intent, 'approval');
});

test('approval intent: lgtm', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'lgtm' });
  assert.equal(r.intent, 'approval');
});

test('correction intent', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: "No, that's wrong" });
  assert.equal(r.intent, 'correction');
});

test('looks_like_task_start: instruction + 6+ words, no continuation', async () => {
  const r = await enrich({
    hook_event_name: 'UserPromptSubmit',
    prompt: 'Fix the authentication bug in login handler',
  });
  assert.equal(r.looks_like_task_start, true);
});

test('looks_like_task_start: false when fewer than 6 words', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'Fix this bug' });
  assert.equal(r.looks_like_task_start, false);
});

test('looks_like_task_start: false for continuation prefix', async () => {
  const r = await enrich({
    hook_event_name: 'UserPromptSubmit',
    prompt: 'Also fix the broken test in auth suite',
  });
  assert.equal(r.looks_like_task_start, false);
});

test('word_count is correct', async () => {
  const r = await enrich({ hook_event_name: 'UserPromptSubmit', prompt: 'one two three' });
  assert.equal(r.word_count, 3);
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/prompt-features.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/prompt-features.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/prompt-features.ts
export const name       = 'prompt-features';
export const collection = 'hook_events';
export const enabled    = true;

type Intent = 'instruction' | 'question' | 'correction' | 'approval' | null;
interface Result {
  word_count:          number;
  is_slash_command:    boolean;
  command_name:        string | null;
  intent:              Intent;
  looks_like_task_start: boolean;
}

const QUESTION_OPENER = /^\b(what|how|why|when|where|is|are|can|does|should|could|would)\b/i;
const INSTRUCTION     = /\b(fix|add|remove|change|update|make|create|write|implement|refactor|delete|rename)\b/i;
const APPROVAL        = /\b(looks good|lgtm|go for it|proceed|approved|sounds good)\b/i;
const CORRECTION      = /^(no|wrong|incorrect|that's not|don't do|shouldn't)\b/i;
const CONTINUATION    = /^(also|and also|now also|one more)\b/i;

function classifyIntent(prompt: string): Intent {
  if (prompt.endsWith('?') || QUESTION_OPENER.test(prompt)) return 'question';
  if (INSTRUCTION.test(prompt)) return 'instruction';
  const lower = prompt.toLowerCase();
  if (APPROVAL.test(lower) || lower === 'yes' || lower === 'ok' || lower === 'sure') return 'approval';
  if (CORRECTION.test(prompt)) return 'correction';
  return null;
}

export function matches(doc: Record<string, unknown>): boolean {
  return doc.hook_event_name === 'UserPromptSubmit';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const prompt  = ((doc.prompt ?? '') as string).trim();
  const word_count       = prompt ? prompt.split(/\s+/).length : 0;
  const is_slash_command = prompt.startsWith('/');
  const command_name     = is_slash_command ? (prompt.split(/\s+/)[0].slice(1) || null) : null;
  const intent           = is_slash_command ? null : classifyIntent(prompt);
  const looks_like_task_start = intent === 'instruction' && word_count >= 6 && !CONTINUATION.test(prompt);
  return { word_count, is_slash_command, command_name, intent, looks_like_task_start };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/prompt-features.test.ts'
```

Expected: all 15 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/prompt-features.ts test/prompt-features.test.ts
git commit -m "feat: add prompt-features enricher for hook_events"
```

---

### Task 7: `attachment-extractor` enricher

**Files:**
- Create: `enrichers/attachment-extractor.ts`
- Create: `test/attachment-extractor.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/attachment-extractor.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/attachment-extractor.js';

test('name and collection', () => {
  assert.equal(name, 'attachment-extractor');
  assert.equal(collection, 'transcript_lines');
});

test('matches attachment line type only', () => {
  assert.equal(matches({ line: { type: 'attachment' } }), true);
  assert.equal(matches({ line: { type: 'assistant' } }), false);
  assert.equal(matches({ line: { type: 'user' } }), false);
  assert.equal(matches({}), false);
});

test('extracts file_path and language from attachment', async () => {
  const r = await enrich({
    line: {
      type: 'attachment',
      attachment: { file_path: 'src/util.ts', content: 'export const x = 1;' },
    },
  });
  assert.equal(r.file_path, 'src/util.ts');
  assert.equal(r.language, 'typescript');
  assert.equal(r.size_chars, 20);
});

test('language is null for unknown extension', async () => {
  const r = await enrich({
    line: {
      type: 'attachment',
      attachment: { file_path: 'data.parquet', content: 'binary' },
    },
  });
  assert.equal(r.language, null);
});

test('file_path is null when absent', async () => {
  const r = await enrich({
    line: { type: 'attachment', attachment: { content: 'some text' } },
  });
  assert.equal(r.file_path, null);
  assert.equal(r.size_chars, 9);
});

test('size_chars is null when content is absent', async () => {
  const r = await enrich({
    line: { type: 'attachment', attachment: { file_path: 'foo.ts' } },
  });
  assert.equal(r.size_chars, null);
});

test('all nulls for empty attachment', async () => {
  const r = await enrich({ line: { type: 'attachment', attachment: {} } });
  assert.deepEqual(r, { file_path: null, language: null, size_chars: null });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/attachment-extractor.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/attachment-extractor.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/attachment-extractor.ts
import { extToLang } from './lang.js';

export const name       = 'attachment-extractor';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { file_path: string | null; language: string | null; size_chars: number | null; }

export function matches(doc: Record<string, unknown>): boolean {
  return (doc.line as Record<string, unknown> | undefined)?.type === 'attachment';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown>;
  const att  = line.attachment as Record<string, unknown> | undefined ?? {};
  const file_path = (att.file_path as string | undefined) ?? null;
  const content   = (att.content as string | undefined) ?? null;
  return {
    file_path,
    language:   extToLang(file_path),
    size_chars: typeof content === 'string' ? content.length : null,
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/attachment-extractor.test.ts'
```

Expected: all 7 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/attachment-extractor.ts test/attachment-extractor.test.ts
git commit -m "feat: add attachment-extractor enricher for transcript_lines"
```

---

### Task 8: `thinking-stats` enricher

**Files:**
- Create: `enrichers/thinking-stats.ts`
- Create: `test/thinking-stats.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/thinking-stats.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/thinking-stats.js';

test('name and collection', () => {
  assert.equal(name, 'thinking-stats');
  assert.equal(collection, 'transcript_lines');
});

test('matches assistant line type only', () => {
  assert.equal(matches({ line: { type: 'assistant' } }), true);
  assert.equal(matches({ line: { type: 'user' } }), false);
  assert.equal(matches({ line: { type: 'attachment' } }), false);
  assert.equal(matches({}), false);
});

test('returns zeros for non-assistant message role', async () => {
  const r = await enrich({ line: { type: 'assistant', message: { role: 'user', content: [] } } });
  assert.deepEqual(r, { thinking_chars: 0, text_chars: 0 });
});

test('counts thinking_chars from thinking blocks', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'thinking', thinking: 'hmm let me think' }],
      },
    },
  });
  assert.equal(r.thinking_chars, 17);
  assert.equal(r.text_chars, 0);
});

test('counts text_chars from text blocks', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'text', text: 'hello world' }],
      },
    },
  });
  assert.equal(r.text_chars, 11);
  assert.equal(r.thinking_chars, 0);
});

test('accumulates multiple blocks', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [
          { type: 'thinking', thinking: 'abc' },
          { type: 'thinking', thinking: 'de' },
          { type: 'text', text: 'fgh' },
        ],
      },
    },
  });
  assert.equal(r.thinking_chars, 5);
  assert.equal(r.text_chars, 3);
});

test('non-text/thinking blocks do not count', async () => {
  const r = await enrich({
    line: {
      type: 'assistant',
      message: {
        role: 'assistant',
        content: [{ type: 'tool_use', id: 'x', name: 'Bash', input: {} }],
      },
    },
  });
  assert.deepEqual(r, { thinking_chars: 0, text_chars: 0 });
});

test('returns zeros when content is missing', async () => {
  const r = await enrich({
    line: { type: 'assistant', message: { role: 'assistant' } },
  });
  assert.deepEqual(r, { thinking_chars: 0, text_chars: 0 });
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/thinking-stats.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/thinking-stats.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/thinking-stats.ts
export const name       = 'thinking-stats';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { thinking_chars: number; text_chars: number; }

export function matches(doc: Record<string, unknown>): boolean {
  return (doc.line as Record<string, unknown> | undefined)?.type === 'assistant';
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line = doc.line as Record<string, unknown>;
  const msg  = line.message as Record<string, unknown> | undefined;
  if (msg?.role !== 'assistant') return { thinking_chars: 0, text_chars: 0 };

  const content = (msg.content ?? []) as Array<Record<string, unknown>>;
  let thinking_chars = 0;
  let text_chars     = 0;

  for (const block of content) {
    if (block.type === 'thinking' && typeof block.thinking === 'string') {
      thinking_chars += block.thinking.length;
    } else if (block.type === 'text' && typeof block.text === 'string') {
      text_chars += block.text.length;
    }
  }

  return { thinking_chars, text_chars };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/thinking-stats.test.ts'
```

Expected: all 8 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/thinking-stats.ts test/thinking-stats.test.ts
git commit -m "feat: add thinking-stats enricher for transcript_lines"
```

---

### Task 9: `file-snapshot-extractor` enricher

**Files:**
- Create: `enrichers/file-snapshot-extractor.ts`
- Create: `test/file-snapshot-extractor.test.ts`

- [ ] **Step 1: Write the failing test**

```typescript
// test/file-snapshot-extractor.test.ts
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { name, collection, matches, enrich } from '../enrichers/file-snapshot-extractor.js';

test('name and collection', () => {
  assert.equal(name, 'file-snapshot-extractor');
  assert.equal(collection, 'transcript_lines');
});

test('matches file-history-snapshot with non-empty trackedFileBackups', () => {
  assert.equal(matches({
    line: {
      type: 'file-history-snapshot',
      snapshot: { trackedFileBackups: { 'src/a.ts': {} } },
    },
  }), true);
});

test('does not match when trackedFileBackups is empty', () => {
  assert.equal(matches({
    line: {
      type: 'file-history-snapshot',
      snapshot: { trackedFileBackups: {} },
    },
  }), false);
});

test('does not match wrong line type', () => {
  assert.equal(matches({ line: { type: 'assistant' } }), false);
  assert.equal(matches({ line: { type: 'user' } }), false);
  assert.equal(matches({}), false);
});

test('does not match when snapshot is missing', () => {
  assert.equal(matches({ line: { type: 'file-history-snapshot' } }), false);
});

test('extracts files from trackedFileBackups keys', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: {
          'src/app.ts': { content: '...' },
          'src/util.ts': { content: '...' },
        },
      },
    },
  });
  assert.equal(r.file_count, 2);
  assert.ok(r.files.includes('src/app.ts'));
  assert.ok(r.files.includes('src/util.ts'));
});

test('detects languages from file extensions', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: {
          'src/app.ts': {},
          'src/util.ts': {},
          'README.md': {},
        },
      },
    },
  });
  assert.ok(r.languages.includes('typescript'));
  assert.ok(r.languages.includes('markdown'));
  assert.equal(r.languages.length, 2);
});

test('deduplicates languages', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: {
          'a.ts': {},
          'b.ts': {},
          'c.ts': {},
        },
      },
    },
  });
  assert.deepEqual(r.languages, ['typescript']);
});

test('unknown extension produces no language entry', async () => {
  const r = await enrich({
    line: {
      type: 'file-history-snapshot',
      snapshot: {
        trackedFileBackups: { 'data.parquet': {} },
      },
    },
  });
  assert.equal(r.file_count, 1);
  assert.deepEqual(r.languages, []);
});
```

- [ ] **Step 2: Run test to verify it fails**

```bash
node --import tsx/esm --test 'test/file-snapshot-extractor.test.ts'
```

Expected: fails with `Cannot find module '../enrichers/file-snapshot-extractor.js'`

- [ ] **Step 3: Write minimal implementation**

```typescript
// enrichers/file-snapshot-extractor.ts
import { extToLang } from './lang.js';

export const name       = 'file-snapshot-extractor';
export const collection = 'transcript_lines';
export const enabled    = true;

interface Result { files: string[]; languages: string[]; file_count: number; }

export function matches(doc: Record<string, unknown>): boolean {
  const line     = doc.line as Record<string, unknown> | undefined;
  if (line?.type !== 'file-history-snapshot') return false;
  const snapshot = line.snapshot as Record<string, unknown> | undefined;
  const backups  = snapshot?.trackedFileBackups as Record<string, unknown> | undefined;
  return !!backups && Object.keys(backups).length > 0;
}

export async function enrich(doc: Record<string, unknown>): Promise<Result> {
  const line    = doc.line as Record<string, unknown>;
  const snapshot = line.snapshot as Record<string, unknown>;
  const backups  = snapshot.trackedFileBackups as Record<string, unknown>;
  const files    = Object.keys(backups);
  const languages = [...new Set(
    files.map(f => extToLang(f)).filter((l): l is string => l !== null),
  )];
  return { files, languages, file_count: files.length };
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
node --import tsx/esm --test 'test/file-snapshot-extractor.test.ts'
```

Expected: all 9 tests PASS

- [ ] **Step 5: Commit**

```bash
git add enrichers/file-snapshot-extractor.ts test/file-snapshot-extractor.test.ts
git commit -m "feat: add file-snapshot-extractor enricher for transcript_lines"
```

---

### Task 10: Full test suite pass

- [ ] **Step 1: Run all tests**

```bash
pnpm test
```

Expected: all existing tests plus all 9 new test files pass, output pristine.

- [ ] **Step 2: Typecheck**

```bash
pnpm run typecheck
```

Expected: no errors.

- [ ] **Step 3: Lint**

```bash
pnpm run lint
```

Expected: no errors.

- [ ] **Step 4: Commit if any fixes were needed**

```bash
git add -p
git commit -m "fix: address typecheck/lint issues in enricher batch"
```

Only commit if there were actual fixes. Skip if all checks passed cleanly.
