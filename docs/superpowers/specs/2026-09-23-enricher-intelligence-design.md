# Enricher Intelligence Expansion

**Date:** 2026-09-23  
**Author:** Robbie Byrd  
**Status:** Approved

## Goal

Extract structured intelligence from the raw `hook_events` and `transcript_lines` streams so the MCP server can search and query session data with meaningful filters and aggregations.

## Approach

Approach B — extractors plus lightweight heuristics. All enrichers are deterministic (no LLM calls). Signals that require semantic understanding (e.g. `line-summary`) remain opt-in and disabled by default.

## New hook_events Enrichers (5)

### `tool-classifier`
- **Matches:** `hook_event_name === 'PreToolUse'` — canonical phase; input fields are sufficient and avoids double-enriching per tool call
- **Output:** `{ category: 'file_read' | 'file_write' | 'file_edit' | 'shell_exec' | 'web_fetch' | 'code_search' | 'agent_dispatch' | 'other', is_read_only: boolean }`
- **Purpose:** Enables aggregation of what operation types dominate a session.

### `file-tracker`
- **Matches:** `hook_event_name === 'PreToolUse'` AND `tool_name` is Read, Edit, Write, Glob, or LS — PreToolUse is canonical; `tool_input.file_path` is available there and using only one phase avoids duplicate enriched documents per tool call
- **Output:** `{ file_path: string | null, language: string | null, operation: 'read' | 'write' | 'edit' | 'glob' | 'ls', artifact_type: 'plan' | 'spec' | 'doc' | 'test' | 'config' | 'code' | null }`
- **Purpose:** Tracks file activity and classifies artifacts in a single pass. Combining these avoids extracting `file_path` twice.
- **Artifact heuristics (in priority order):**
  - `test`: path contains `/test/` or filename matches `*.test.*`, `*.spec.*`
  - `spec`: path contains `/specs/` or filename contains "spec", "design", "architecture"
  - `plan`: path contains `/plans/` or filename contains "plan", "roadmap", "todo"
  - `doc`: extension is `.md` or `.mdx` and not matched above
  - `config`: extension is `.json`, `.yaml`, `.toml`, `.env`
  - `code`: all other recognised source extensions
  - `null`: unrecognised extension or no file path

### `bash-outcome`
- **Matches:** `hook_event_name === 'PostToolUse'` AND `tool_name === 'Bash'`
- **Output:** `{ success: boolean, has_error: boolean, output_lines: number, truncated: boolean }`
- **Purpose:** Enables queries like "sessions where a bash command failed."
- **Heuristic:** `success = (tool_response.stderr === '' || !tool_response.stderr)`. `has_error = !success`. `truncated = true` when stdout contains Claude Code's `[truncated]` marker. Stderr presence is the sole success signal — no stdout pattern matching.
- **Note:** Both guards (`PostToolUse` AND `Bash`) are required in `matches()` — `PreToolUse` events have no `tool_response`.

### `edit-diff-stats`
- **Matches:** `hook_event_name === 'PostToolUse'` AND `tool_name` is Edit or Write
- **Output:** `{ lines_added: number, lines_removed: number, net_change: number }`
- **Purpose:** Enables "how much code changed" queries per session or across time.
- **Method:** For Edit: count newlines in `new_string` minus `old_string`. For Write: `lines_added = newline count of content`, `lines_removed = 0` — Write has no old content available so removal is intentionally not tracked.

### `prompt-features`
- **Matches:** `hook_event_name === 'UserPromptSubmit'`
- **Output:** `{ word_count: number, is_slash_command: boolean, command_name: string | null, intent: 'instruction' | 'question' | 'correction' | 'approval' | null, looks_like_task_start: boolean }`
- **Purpose:** Enables task-start detection and intent-based filtering directly on `hook_events`, without joining to `transcript_lines`.
- **Note:** `intent-classifier` produces a similar field on `transcript_lines`. These are intentionally denormalized — `prompt-features` allows hook_event queries without a cross-collection join, and uses the same regex patterns to keep results consistent.
- **Heuristics:**
  - `is_slash_command`: prompt starts with `/`
  - `intent`: same regex patterns as `intent-classifier` (question openers, instruction verbs, correction markers, approval phrases)
  - `looks_like_task_start`: `intent === 'instruction'` AND `word_count >= 6` AND prompt does not begin with a continuation word ("also", "and also", "now also", "one more")

## New transcript_lines Enrichers (3)

### `attachment-extractor`
- **Matches:** `line.type === 'attachment'`
- **Output:** `{ file_path: string | null, language: string | null, size_chars: number | null }`
- **Purpose:** Tracks which files were attached to prompts.

### `thinking-stats`
- **Matches:** `line.type === 'assistant'`
- **Output:** `{ thinking_chars: number, text_chars: number }`
- **Purpose:** Enables filtering on sessions where Claude used extended thinking. `thinking_chars > 0` implies thinking was present. Does not duplicate `tool-use-summary.has_thinking` — this enricher adds character-level sizing that `tool-use-summary` does not produce.

### `file-snapshot-extractor`
- **Matches:** `line.type === 'file-history-snapshot'` AND `line.snapshot.trackedFileBackups` is non-empty
- **Output:** `{ files: string[], languages: string[], file_count: number }`
- **Source:** `files = Object.keys(line.snapshot.trackedFileBackups)` — confirmed structure from production data.
- **Purpose:** Captures what files were tracked in the editor at each snapshot point.

## MCP Query Coverage

| Query | Enricher(s) |
|---|---|
| Sessions where TypeScript files were edited | `file-tracker.language` |
| Sessions where a bash command failed | `bash-outcome.success` |
| Where a plan or spec was created | `file-tracker.artifact_type` |
| What operation types dominated a session | `tool-classifier.category` |
| Where a task started | `prompt-features.looks_like_task_start` |
| Sessions with heavy thinking | `thinking-stats.thinking_chars` |
| What files were tracked when a snapshot was taken | `file-snapshot-extractor.files` |
| How much code changed in a session | `edit-diff-stats.net_change` |
| Where a skill was invoked | `skill-detector.skills[].name` (existing) |
| Intent behind a user prompt | `prompt-features.intent` |

## Testing

Each enricher gets a unit test file at `test/<name>.test.ts` following the existing pattern:
- Real fixture documents (no mocks)
- At least one positive match, one negative match, and one edge case per enricher
- `matches()` and `enrich()` tested independently

## Out of Scope

- LLM-powered enrichers (`line-summary` remains disabled)
- Privacy redaction (`privacy-redact` remains disabled)
- New MCP tool signatures (existing search tools gain richer fields to filter against)
- Cross-collection join enrichers (`hook-linker` already handles this)
