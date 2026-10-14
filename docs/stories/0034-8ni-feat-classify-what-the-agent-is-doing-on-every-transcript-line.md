---
id: 0034-8ni
title: Classify what the agent is doing on every transcript line
purpose: Every captured transcript line carries an activity kind (user, response, thinking, bash, edit, read, task, commit) so a session reads as a timeline of what the agent did.
type: feat
status: pending
priority: "2"
effort: S
created: "2026-10-14T04:28:51.337Z"
updated: "2026-10-14T04:28:51.337Z"
tags:
  - sessions
  - enrichment
  - activity
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
  "3":
    status: pending
---

# Classify what the agent is doing on every transcript line

## Problem Statement

The line-classifier enricher only tells user prompts, assistant lines, tool results and hook lines apart. A session cannot be read as what the agent was doing: an assistant line may be a reply, a thought or a tool call, and a tool call may be a shell command, a file edit, a read or a sub-agent task. AgentHUD's activity feed uses exactly that taxonomy (user, response, thinking, tool with a per-tool label, task) and it is what a timeline, a digest or a live view needs.

**Impact:** Timelines and digests have to re-derive the activity from raw content blocks on every read, and each consumer does it differently.

## Steps

### 1: An activity-kind enricher over transcript lines

Add `session/enrichers/activity_kind.go` (`activity-kind`, collection `transcript_lines`, enabled). For each line it stores `{kind, label, detail, tool_name, tool_use_id}`: `user` for a prompt (the text, 300 characters), `response` for an assistant text block, `thinking` for a thinking block, `bash` (the command, ANSI stripped) for a Bash tool_use, `edit` (the file's base name) for Write, Edit, MultiEdit and NotebookEdit, `read` (base name) for Read, Glob and Grep, `task` (the sub-agent's description) for Task and Agent, `tool` (the tool name) for every other tool_use, `result` for a tool_result (with `is_error`), and `other` for everything else (system, meta and hook lines). A line with several blocks takes the kind of its first content block and lists the others under `blocks`.

### 2: Commits as activities

A `bash` activity whose command is a `git commit` gets `kind: commit` with the first line of the message as its detail, so commits stand out in a timeline without a git log.

### 3: Timeline query

Add `session.Store.Activities(ctx, ActivityQuery{AccountID, SessionIDs, Kinds, Since, Limit})` projecting `session_id`, `seq`, `enrichments.activity-kind`, `timestamp` and `created_at`, implemented by the memory, Mongo and remote stores with a `/activities` route and a conformance case, and expose it as the `get_activities` session tool and `session activities`, newest first, with consecutive identical tool activities collapsed into one entry with a `count` the way AgentHUD renders them.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/enrichers/ -run ActivityKind
- [ ] VERIFY: cd mind-palace && go test -race ./session/... ./server/sessionapi/ ./cli/
- [ ] `get_activities` on a captured session lists user, response, thinking, bash, edit, read and task entries in order with the documented labels and details
- [ ] PROTOCOL.md documents the enrichment's fields, the store method, the route and the tool

## Files

- `mind-palace/session/enrichers/activity_kind.go` - the enricher and its result type
- `mind-palace/session/store.go` - `ActivityQuery` and `Store.Activities`
- `mind-palace/session/query/query.go` - `Activities` with the collapsing of repeated tool entries
- `mind-palace/session/sessionmcp/sessionmcp.go` - the `get_activities` tool
- `PROTOCOL.md` - sections 6.1 and 7.3

## Work Log
