---
id: 0045-jiw
title: 'P2: If-Match preconditions on storeapi PUT, PutIf in the stores'
purpose: 'Lets clued-cloud workspaces have lost-update protection for raw client writes instead of: last writer wins.'
type: feat
status: complete
priority: "2"
created: "2026-10-15T19:09:10.033Z"
updated: "2026-10-15T19:09:18.577Z"
started: "2026-10-15T19:09:18.497Z"
completed: "2026-10-15T19:09:18.577Z"
tags:
  - storeapi
  - store
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
      - mind-palace/store/memstore/memstore.go
      - mind-palace/store/remotestore/remotestore.go
      - mind-palace/server/storeapi/storeapi.go
      - mind-palace/store/storetest/storetest.go
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

# P2: If-Match preconditions on storeapi PUT, PutIf in the stores

## Problem Statement

Public-module prerequisite P2 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** storeapi `PUT` honours `If-Match` on `front_matter.updated` (412); remotestore sends it; mongostore `PutIf`.

**Needed by:** lost-update protection for raw client writes.

**Without it:** last writer wins.

`store.ConditionalPutter{PutIf(ctx, d, prevUpdated)}` with `store.ErrPrecondition` and the helper `store.PutIf` (read-compare-write fallback); mongostore (conditional `ReplaceOne`), memstore, remotestore and MultiStore implement it. storeapi GET answers `ETag: "<updated>"`; a PUT with `If-Match` is 412 `precondition` unless the stored copy still has that `updated`. remotestore sends the `updated` it last read as `If-Match`.

**Impact:** Without it: last writer wins.

## Steps

### 1: Implement and test

`store.ConditionalPutter{PutIf(ctx, d, prevUpdated)}` with `store.ErrPrecondition` and the helper `store.PutIf` (read-compare-write fallback); mongostore (conditional `ReplaceOne`), memstore, remotestore and MultiStore implement it. storeapi GET answers `ETag: "<updated>"`; a PUT with `If-Match` is 412 `precondition` unless the stored copy still has that `updated`. remotestore sends the `updated` it last read as `If-Match`.

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] storeapi GET returns `ETag: "<updated>"` and a PUT whose `If-Match` no longer matches is refused with 412 kind `precondition`, atomically against a concurrent writer
- [x] mongostore, memstore, remotestore and MultiStore implement `PutIf`, and the `storetest` conformance suite has a conditional-put case
- [x] remotestore sends the `updated` it last read (Get, List) as `If-Match`
- [x] VERIFY: cd mind-palace && go test -race -run 'TestPutIfMatch' ./server/storeapi/
- [x] VERIFY: cd mind-palace && go test -race -run 'TestPutSendsTheUpdatedLastRead' ./store/remotestore/
- [x] VERIFY: cd mind-palace && go test -race -run 'TestMultiStorePutIf' ./store/

## Files

- `mind-palace/store/store.go`
- `mind-palace/store/mongostore/mongostore.go`
- `mind-palace/store/memstore/memstore.go`
- `mind-palace/store/remotestore/remotestore.go`
- `mind-palace/server/storeapi/storeapi.go`
- `mind-palace/store/storetest/storetest.go`

## Work Log

### 2026-10-15T19:09:11.399Z - Implemented and tested on claude/m07-phase-6 (PR #49) in 3ea0935; documented in 5b96627.
