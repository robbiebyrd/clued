---
id: 0044-can
title: 'P1: Clued.Palace returns the transport''s cached palace for a principal''s project'
purpose: 'Lets clued-cloud workspaces have board writes sharing locks and events with the transport instead of: board runs behind `--board` on a separate palace (shared store, no shared locks or events).'
type: feat
status: complete
priority: "2"
created: "2026-10-15T19:09:02.128Z"
updated: "2026-10-15T19:09:18.319Z"
started: "2026-10-15T19:09:18.243Z"
completed: "2026-10-15T19:09:18.319Z"
tags:
  - server
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
      - mind-palace/server/clued.go
      - mind-palace/server/clued_test.go
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

# P1: Clued.Palace returns the transport's cached palace for a principal's project

## Problem Statement

Public-module prerequisite P1 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** `(*server.Clued).Palace(ctx, p, project) (*service.Palace, error)`.

**Needed by:** board writes sharing locks and events with the transport.

**Without it:** board runs behind `--board` on a separate palace (shared store, no shared locks or events).

`(*Clued).Palace(ctx, p auth.Principal, project string)` returns the same cached `*service.Palace` the MCP and WebSocket transport use for that principal's project, after `projects.ValidSlug`, `AllowProject` and the namespace, so a web board writes through the palace whose claim mutex and event bus the other clients share. Errors: `projects.ErrInvalidProject`, `ErrProjectForbidden`, the `NamespaceCtx` error, `ErrPalacesClosed`.

**Impact:** Without it: board runs behind `--board` on a separate palace (shared store, no shared locks or events).

## Steps

### 1: Implement and test

`(*Clued).Palace(ctx, p auth.Principal, project string)` returns the same cached `*service.Palace` the MCP and WebSocket transport use for that principal's project, after `projects.ValidSlug`, `AllowProject` and the namespace, so a web board writes through the palace whose claim mutex and event bus the other clients share. Errors: `projects.ErrInvalidProject`, `ErrProjectForbidden`, the `NamespaceCtx` error, `ErrPalacesClosed`.

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] `Clued.Palace` returns the identical palace the transport serves for the principal's project, so its claims and events are shared with MCP and WebSocket clients
- [x] An invalid slug, a forbidden project, a namespace error and a closed server are returned as errors and open no palace
- [x] VERIFY: cd mind-palace && go test -race -run 'TestCluedPalaceSharesTransportPalace' ./server/

## Files

- `mind-palace/server/clued.go`
- `mind-palace/server/clued_test.go`

## Work Log

### 2026-10-15T19:09:10.883Z - Implemented and tested on claude/m07-phase-6 (PR #49) in a3a80cf; documented in 5b96627.
