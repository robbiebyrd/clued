---
id: 0026-fu8
title: Query sessions by the files they touched and the errors they hit
type: feat
status: ready
priority: "1"
effort: M
created: "2026-10-14T03:13:00.528Z"
updated: "2026-10-14T03:13:00.528Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
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

- [ ] VERIFY: cd mind-palace && go test -race ./session/...
- [ ] VERIFY: `file_history` lists the sessions that touched a path, newest first, with their operations
- [ ] VERIFY: `find_errors` lists error lines with their type and session, scoped to the account

## Work Log
