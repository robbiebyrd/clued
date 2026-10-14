---
id: 0037-de8
title: 'Summarise tool results: diff hunks, line spans, output and interruption from toolUseResult'
purpose: Each tool call's structured result is reduced to the facts a timeline shows (files and line ranges edited with lines added and removed, command output, whether it was interrupted) without re-reading the transcript.
type: feat
status: complete
priority: "2"
effort: S
created: "2026-10-14T04:28:51.447Z"
updated: "2026-10-14T05:16:45.733Z"
started: "2026-10-14T05:07:58.483Z"
completed: "2026-10-14T05:16:45.733Z"
tags:
  - sessions
  - enrichment
  - tools
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
---

# Summarise tool results: diff hunks, line spans, output and interruption from toolUseResult

## Problem Statement

Claude Code writes a `toolUseResult` object next to each tool result line: for Edit and Write a `structuredPatch` with hunks, for Bash `stdout`, `stderr` and `interrupted`, for Read the `file.startLine` and `numLines`, for Task the sub-agent's returned text. clued stores the lines but reads none of it; `edit-diff-stats` counts lines on the hook event, not the patch. AgentHUD turns these into `name L10-12 +3 -1` labels with the unified diff as the detail, stdout and stderr bodies, and `[interrupted]` markers.

**Impact:** A timeline or digest cannot show what an edit changed or what a command printed without parsing raw tool results again.

## Steps

### 1: A tool-result-detail enricher

Add `session/enrichers/tool_result_detail.go` (`tool-result-detail`, collection `transcript_lines`, matching lines with a `toolUseResult`). It stores, by tool: Edit, Write, MultiEdit and NotebookEdit: `file_path`, `hunks` (`old_start`, `old_lines`, `new_start`, `new_lines`), `lines_added`, `lines_removed`, `span` (`L10-12`) and `diff` (the unified diff, capped at 16 KB); Bash: `command`, `stdout` and `stderr` (each capped at 4 KB, ANSI stripped), `exit_code` when present, `interrupted`; Read: `file_path`, `start_line`, `num_lines`; Task and Agent: `description` and `result` (capped at 4 KB); any other tool: the result's top-level keys.

### 2: Link to the call

The result carries the `tool_use_id` and, through the hook-linker, the id of the PreToolUse hook event of the call, so `file_history` can add the span and the diff of each edit.

### 3: Expose it

`get_full_session` returns the enrichment with each line as it does the others; `file_history` adds `spans` and `lines_added`/`lines_removed` per edit from it when present; the activity timeline (`get_activities`) uses it for the `edit` and `bash` details.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/enrichers/ -run ToolResultDetail
- [x] An Edit's line reports its file, `L10-12`, `+3 -1` and the diff; a Bash line reports stdout, stderr and `interrupted: true` when it was; a Read reports its range
- [x] PROTOCOL.md documents the enrichment's fields per tool and the caps

## Files

- `mind-palace/session/enrichers/tool_result_detail.go` - the enricher
- `mind-palace/session/query/files.go` - spans and diffs in `file_history`
- `PROTOCOL.md` - the enrichments list

## Work Log

### 2026-10-14T05:13:58.218Z - Added session/structpatch (hunks, line counts, span, unified diff from a structuredPatch), the tool-result-detail enricher (edit/write, bash, read, task, text, other by the result's shape, with caps of 16 KB on the diff and 4 KB on texts, a one-line summary and the tool_use_id), file_path and span on edit-diff-stats from the hook's tool_response.structuredPatch (file_history reports them as `spans`), and the detail on get_activities result entries (summary as the detail, the enrichment without its texts). The command itself is not in a Bash result, so it is not stored there (the tool_use line holds it); the hook-linker's link is untouched. Docs in PROTOCOL.md 6.1.3 and the tool tables, README. gofmt, vet and `go test -race ./...` pass.
