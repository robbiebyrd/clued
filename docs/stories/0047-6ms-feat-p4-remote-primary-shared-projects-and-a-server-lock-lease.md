---
id: 0047-6ms
title: 'P4: Remote-primary shared projects and a server lock lease'
purpose: 'Lets clued-cloud workspaces have correct client-side claims across members instead of: claims are reliable only through the board or `/mcp`.'
type: feat
status: complete
priority: "2"
created: "2026-10-15T19:09:10.178Z"
updated: "2026-10-15T19:09:19.152Z"
started: "2026-10-15T19:09:19.070Z"
completed: "2026-10-15T19:09:19.152Z"
tags:
  - clientcfg
  - store
  - storeapi
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
      - mind-palace/store/store.go
      - mind-palace/store/mongostore/mongostore.go
      - mind-palace/store/remotestore/remotestore.go
      - mind-palace/server/storeapi/storeapi.go
      - mind-palace/clientcfg/clientcfg.go
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

# P4: Remote-primary shared projects and a server lock lease

## Problem Statement

Public-module prerequisite P4 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** remote-primary reads for shared projects, a lock lease endpoint and a mongostore lease `Locker`.

**Needed by:** correct client-side claims across members.

**Without it:** claims are reliable only through the board or `/mcp`.

`store.Leaser{AcquireLease, ReleaseLease}` with `ErrLeased`, `ErrNoLease`, `LeaseLock`, `MemLeases` and `DefaultLeaseTTL` (30 s); mongostore keeps leases in `<prefix>leases`. storeapi serves `POST /api/v1/projects/{project}/store/{kind}/{id}/lock` (`{lease, expires_at}`, 409 `locked` while held) and `.../unlock`; remotestore is a `store.Locker` over them. `clientcfg.Open` probes `GET /api/v1/me/workspaces`; a project a workspace binds opens with the server as the primary store (`Remote.Shared`, `Remote.Workspace`, `Remote.SharedErr`).

**Impact:** Without it: claims are reliable only through the board or `/mcp`.

## Steps

### 1: Implement and test

`store.Leaser{AcquireLease, ReleaseLease}` with `ErrLeased`, `ErrNoLease`, `LeaseLock`, `MemLeases` and `DefaultLeaseTTL` (30 s); mongostore keeps leases in `<prefix>leases`. storeapi serves `POST /api/v1/projects/{project}/store/{kind}/{id}/lock` (`{lease, expires_at}`, 409 `locked` while held) and `.../unlock`; remotestore is a `store.Locker` over them. `clientcfg.Open` probes `GET /api/v1/me/workspaces`; a project a workspace binds opens with the server as the primary store (`Remote.Shared`, `Remote.Workspace`, `Remote.SharedErr`).

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] The lock route grants one lease at a time (409 `locked` while held) and unlock releases only the matching lease
- [x] mongostore and the memory store implement `store.Leaser` and pass the `storetest` leases case; mongostore releases only a well-formed lease it holds
- [x] A project listed by `GET /api/v1/me/workspaces` opens with the server as primary; an unlisted one keeps the local primary
- [x] VERIFY: cd mind-palace && go test -race -run 'TestLockRoutes' ./server/storeapi/
- [x] VERIFY: cd mind-palace && go test -race -run 'TestSharedProjectIsRemotePrimary|TestUnsharedProjectStaysLocalPrimary' ./clientcfg/

## Files

- `mind-palace/store/store.go`
- `mind-palace/store/mongostore/mongostore.go`
- `mind-palace/store/remotestore/remotestore.go`
- `mind-palace/server/storeapi/storeapi.go`
- `mind-palace/clientcfg/clientcfg.go`

## Work Log

### 2026-10-15T19:09:12.268Z - Implemented and tested on claude/m07-phase-6 (PR #49) in da05154, 1dcc024, ea14e54; documented in 5b96627.
