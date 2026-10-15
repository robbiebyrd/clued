---
id: 0049-due
title: 'P6: whoami suggests the assignee'
purpose: 'Lets clued-cloud workspaces have consistent assignees across clients and the board instead of: agents pick their own.'
type: feat
status: complete
priority: "3"
created: "2026-10-15T19:09:10.318Z"
updated: "2026-10-15T19:09:19.709Z"
started: "2026-10-15T19:09:19.623Z"
completed: "2026-10-15T19:09:19.709Z"
tags:
  - server
  - auth
  - workspaces
  - prerequisite
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: https://github.com/robbiebyrd/clued.git
    pull_request: https://github.com/robbiebyrd/clued/pull/49
    files:
      - mind-palace/server/auth/auth.go
      - mind-palace/server/clued.go
      - mind-palace/cli/session.go
      - mind-palace/collect/prime/server.go
  specs:
    - https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md
  web:
    plan-0003-m07: https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md
    spec: https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md
progress:
  "1":
    status: complete
  "2":
    status: complete
---

# P6: whoami suggests the assignee

## Problem Statement

Public-module prerequisite P6 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** `whoami` returns a suggested assignee.

**Needed by:** consistent assignees across clients and the board.

**Without it:** agents pick their own.

`CluedOptions.Assignee func(ctx, p auth.Principal) string` fills the new `assignee` field of `GET /api/v1/whoami`, so agents claim as the same `provider:handle` the server's board uses; `prime.Principal.Assignee` and `session whoami` print it.

**Impact:** Without it: agents pick their own.

## Steps

### 1: Implement and test

`CluedOptions.Assignee func(ctx, p auth.Principal) string` fills the new `assignee` field of `GET /api/v1/whoami`, so agents claim as the same `provider:handle` the server's board uses; `prime.Principal.Assignee` and `session whoami` print it.

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] `GET /api/v1/whoami` includes `assignee` when `CluedOptions.Assignee` returns one and omits it otherwise
- [x] `session whoami` and the prime report show the suggested assignee
- [x] VERIFY: cd mind-palace && go test -race -run 'TestWhoamiSuggestsTheAssignee' ./server/

## Files

- `mind-palace/server/auth/auth.go`
- `mind-palace/server/clued.go`
- `mind-palace/cli/session.go`
- `mind-palace/collect/prime/server.go`

## Work Log

### 2026-10-15T19:09:13.055Z - Implemented and tested on claude/m07-phase-6 (PR #49) in 66e08a8; documented in 5b96627.
