---
id: 0038-ebt
title: Flag tool calls that were rejected and never ran
purpose: A tool call that Claude Code refused (a denied permission, a blocked path, a validation error) is marked as never executed, so counts and timelines do not report it as work done.
type: feat
status: pending
priority: "3"
effort: XS
created: "2026-10-14T04:28:51.481Z"
updated: "2026-10-14T04:28:51.481Z"
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
    status: pending
  "2":
    status: pending
---

# Flag tool calls that were rejected and never ran

## Problem Statement

A tool call the harness rejected still appears as a tool_use with a tool_result, and the result's text is the refusal. `error-flag` marks it as a tool failure, which is right, but nothing says the call never ran. AgentHUD drops such calls from its activity feed by matching seven refusal texts in the result: `InputValidationError:`, `Permission for this action was denied`, `Blocked:`, `Refusing to write through symlink`, `The user doesn't want to proceed`, `Tool use was rejected` and `This operation was blocked by a hook`.

**Impact:** Tool counts, file histories and edit statistics count calls that changed nothing, and a timeline shows a rejected edit as if it happened.

## Steps

### 1: Rejection detection

Extend `error-flag` on transcript lines with `rejected: true` and `rejection: permission_denied | blocked_by_hook | input_validation | user_declined | symlink | other` when a tool_result's text starts with or contains one of the refusal markers (kept as a list in `session/enrichers/rejections.go` so new markers are one line). The PreToolUse hook event of the call gets the same flag through the hook-linker (`tool-classifier` gains `rejected`).

### 2: Keep rejected calls out of the counts

`file_history`, `edit-diff-stats` totals, `search_commands` and the activity timeline skip rejected calls by default and take an `include_rejected` parameter to show them.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/enrichers/ ./session/query/
- [ ] A denied Edit is flagged `rejected: true, rejection: permission_denied` on its result line and its hook event, and `file_history` does not count it unless asked
- [ ] PROTOCOL.md lists the rejection markers and the `include_rejected` parameter

## Files

- `mind-palace/session/enrichers/rejections.go` - the marker list
- `mind-palace/session/enrichers/error_flag.go` - `rejected` and `rejection`
- `mind-palace/session/query/files.go` - `include_rejected`
- `PROTOCOL.md` - the enrichments list and the tool parameters

## Work Log
