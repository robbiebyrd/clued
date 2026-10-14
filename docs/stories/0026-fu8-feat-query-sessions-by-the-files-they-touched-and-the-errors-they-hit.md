---
id: 0026-fu8
title: Query sessions by the files they touched and the errors they hit
type: feat
status: complete
priority: "1"
effort: M
created: "2026-10-14T03:13:00.528Z"
updated: "2026-10-14T03:59:05.414Z"
started: "2026-10-14T03:24:34.252Z"
completed: "2026-10-14T03:59:05.414Z"
tags:
  - session
  - mcp
  - enrichers
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  plans:
    - ["0007-w5t", "included"]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
---

# Query sessions by the files they touched and the errors they hit

## Problem Statement

The file-tracker and error-flag enrichers already run on every captured session, but nothing reads their results: an agent cannot ask which sessions edited a file or where a command kept failing, and transcript lines lose their enrichments on the way out.

## Steps

### 1: Store methods

`FileTouches` and `ErrorLines` on `session.Store`, both stores, the session transport and the remote adapter, with sessiontest cases.

### 2: Query and tools

`query.FileHistory` and `query.FindErrors`; MCP tools `file_history` and `find_errors`; transcript lines keep `enrichments`.

### 3: Docs

PROTOCOL.md tool reference and README.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/...
- [x] VERIFY: `file_history` lists the sessions that touched a path, newest first, with their operations
- [x] VERIFY: `find_errors` lists error lines with their type and session, scoped to the account

## Work Log

### 2026-10-14T03:31:19.184Z - Store methods FileEvents (PreToolUse events with enrichments.file-tracker.file_path, and PostToolUse Edit/Write events with edit-diff-stats) and ErrorLines (enrichments.error-flag.is_error), in both stores, POST /api/v1/session/file-events and /error-lines, and the remote adapter, with sessiontest cases; two Mongo indexes. query.FileHistory groups the newest 500 file events per session; query.FindErrors adds a 300-character snippet via the new session.LineText and session.Snippet (also for the search enricher). MCP tools file_history and find_errors, on the stdio server and the server's /mcp. Transcript lines already kept their enrichments in both stores and over the transport; nothing to change there. go test -race ./... passes on the memory stores; Mongo runs in CI.
