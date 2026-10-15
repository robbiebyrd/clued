---
id: 0046-u27
title: 'P3: storeapi BeforePut hook for raw client writes'
purpose: 'Lets clued-cloud workspaces have workflow and claim checks on raw client writes to `w` keys instead of: members'' clients are trusted.'
type: feat
status: complete
priority: "2"
created: "2026-10-15T19:09:10.105Z"
updated: "2026-10-15T19:09:18.863Z"
started: "2026-10-15T19:09:18.791Z"
completed: "2026-10-15T19:09:18.863Z"
tags:
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
      - mind-palace/server/storeapi/storeapi.go
      - mind-palace/server/clued.go
      - mind-palace/server/storeapi/storeapi_test.go
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

# P3: storeapi BeforePut hook for raw client writes

## Problem Statement

Public-module prerequisite P3 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** `storeapi.Options.BeforePut(ctx, prev, next)`.

**Needed by:** workflow and claim checks on raw client writes to `w` keys.

**Without it:** members' clients are trusted.

`storeapi.Options.BeforePut` and `CluedOptions.BeforePut` `func(ctx, prev, next *model.Document) error` (prev nil for a new document) run before a document PUT; an error refuses it with 403 and its message, or a `*httpapi.StatusError`'s own status, and the write that follows is conditional on prev's `updated`. The hook reads the caller with `auth.FromContext`, the project slug with `storeapi.Project(ctx)` and `storeapi.Forced(ctx)` (header `X-Clued-Force: 1`).

**Impact:** Without it: members' clients are trusted.

## Steps

### 1: Implement and test

`storeapi.Options.BeforePut` and `CluedOptions.BeforePut` `func(ctx, prev, next *model.Document) error` (prev nil for a new document) run before a document PUT; an error refuses it with 403 and its message, or a `*httpapi.StatusError`'s own status, and the write that follows is conditional on prev's `updated`. The hook reads the caller with `auth.FromContext`, the project slug with `storeapi.Project(ctx)` and `storeapi.Forced(ctx)` (header `X-Clued-Force: 1`).

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] A `BeforePut` error refuses the PUT with 403 (or the StatusError's status) and nothing is written
- [x] The hook receives the stored document (nil for a new one), the project slug and the force flag
- [x] VERIFY: cd mind-palace && go test -race -run 'TestBeforePut' ./server/storeapi/

## Files

- `mind-palace/server/storeapi/storeapi.go`
- `mind-palace/server/clued.go`
- `mind-palace/server/storeapi/storeapi_test.go`

## Work Log

### 2026-10-15T19:09:11.757Z - Implemented and tested on claude/m07-phase-6 (PR #49) in 3ea0935; documented in 5b96627.
