---
id: 0048-0f7
title: 'P5: CluedOptions.NamespaceCtx routes with the request context and an error'
purpose: 'Lets clued-cloud workspaces have routing with the request context and an error; exact revocation instead of: background lookup, a sentinel key, and up to 30 s of client writes after removal.'
type: feat
status: complete
priority: "1"
created: "2026-10-15T19:09:10.247Z"
updated: "2026-10-15T19:09:19.441Z"
started: "2026-10-15T19:09:19.356Z"
completed: "2026-10-15T19:09:19.441Z"
tags:
  - server
  - workspaces
  - auth
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
      - mind-palace/server/clued.go
      - mind-palace/server/server.go
      - mind-palace/server/storeapi/storeapi.go
      - mind-palace/server/auth/auth.go
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

# P5: CluedOptions.NamespaceCtx routes with the request context and an error

## Problem Statement

Public-module prerequisite P5 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** `CluedOptions.NamespaceCtx(ctx, p, project) (string, error)`.

**Needed by:** routing with the request context and an error; exact revocation.

**Without it:** background lookup, a sentinel key, and up to 30 s of client writes after removal.

`CluedOptions.NamespaceCtx func(ctx, p auth.Principal, project string) (string, error)` replaces `Namespace` when set. It runs on every service API and storeapi request (once per WebSocket connection or MCP session); a `*httpapi.StatusError` answers with its status and kind, `auth.ErrProjectForbidden` (= `server.ErrProjectForbidden`) with 403, anything else with 502, so a removed member is refused on the next request.

**Impact:** Without it: background lookup, a sentinel key, and up to 30 s of client writes after removal.

## Steps

### 1: Implement and test

`CluedOptions.NamespaceCtx func(ctx, p auth.Principal, project string) (string, error)` replaces `Namespace` when set. It runs on every service API and storeapi request (once per WebSocket connection or MCP session); a `*httpapi.StatusError` answers with its status and kind, `auth.ErrProjectForbidden` (= `server.ErrProjectForbidden`) with 403, anything else with 502, so a removed member is refused on the next request.

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] `NamespaceCtx` receives the request context and replaces `Namespace` when set
- [x] Its error fails the request: StatusError as given, `ErrProjectForbidden` 403, anything else 502
- [x] VERIFY: cd mind-palace && go test -race -run 'TestNamespaceCtxReceivesRequestContextAndFailsRequests' ./server/

## Files

- `mind-palace/server/clued.go`
- `mind-palace/server/server.go`
- `mind-palace/server/storeapi/storeapi.go`
- `mind-palace/server/auth/auth.go`

## Work Log

### 2026-10-15T19:09:12.670Z - Implemented and tested on claude/m07-phase-6 (PR #49) in a3a80cf; documented in 5b96627.
