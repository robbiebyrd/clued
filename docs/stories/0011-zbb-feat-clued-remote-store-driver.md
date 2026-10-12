---
id: 0011-zbb
title: The clued remote store driver and conformance
purpose: Register a store.Store driver that writes plans and stories to the server through storeapi.
type: feat
status: pending
priority: "2"
effort: S
created: "2026-10-12T20:09:05.148Z"
updated: "2026-10-12T20:09:05.148Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 10
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/store/remotestore/remotestore.go
      - mind-palace/store/remotestore/remotestore_test.go
      - mind-palace/cmd/mind-palace/main.go
      - mind-palace/cmd/plan/main.go
      - mind-palace/cmd/story/main.go
  plans:
    - ["0006-tsw", "included", ["3.2"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# The clued remote store driver and conformance

## Problem Statement

The client's `MultiStore` can only fan out to drivers registered with `store.Register`, all of which open a database connection. A `clued` driver over `remote.Client` lets the existing fan-out, `FanoutError` and `mind-palace sync` carry plans and stories to the server with no new rules.

**Impact:** Without it plans and stories stay local-only in remote mode.

## Steps

### 1: Driver

`store/remotestore` registers driver `clued` in `init()`; options `url`, `token`, `project` (filled by the client, not hand-written); implements `List`, `Get`, `Put`, `Delete` and the template methods against `storeapi`; `Name()` is the definition name, `Driver()` is `clued`; 404 → `store.ErrNotFound`.

### 2: Conformance

`storetest.Run` against `httptest.NewServer(auth.Middleware(storeapi.Handler(...memstore per project...)))` with a token allowing the test project.

### 3: Registration

Blank-import the driver in the three `cmd/*` mains next to the existing store plugins.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./store/...
- [ ] `mind-palace stores` lists `clued` among the drivers
- [ ] `storetest.Run` passes with no environment variables set

## Files

- `mind-palace/store/remotestore/remotestore.go` - driver
- `mind-palace/store/remotestore/remotestore_test.go` - conformance
- `mind-palace/cmd/mind-palace/main.go` - blank import
- `mind-palace/cmd/plan/main.go` - blank import
- `mind-palace/cmd/story/main.go` - blank import

## Work Log
