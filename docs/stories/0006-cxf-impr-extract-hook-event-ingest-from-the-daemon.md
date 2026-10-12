---
id: 0006-cxf
title: Extract hook-event ingest from the session daemon
purpose: Share the daemon's event bookkeeping with the daemon-less relay so both modes stamp, track and buffer events identically.
type: impr
status: complete
priority: "2"
effort: M
created: "2026-10-12T20:09:05.003Z"
updated: "2026-10-12T20:50:01.543Z"
started: "2026-10-12T20:35:07.967Z"
completed: "2026-10-12T20:50:01.543Z"
tags:
  - server
  - remote
  - refactor
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/session/ingest/ingest.go
      - mind-palace/session/ingest/ingest_test.go
      - mind-palace/session/daemon/daemon.go
  plans:
    - ["0006-tsw", "included", ["2.1"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Extract hook-event ingest from the session daemon

## Problem Statement

`daemon.handleEvent` mixes three things: session bookkeeping (first-sight upsert with git, host and account, git fill on later events, touch, stamping `account_id`, `host`, `created_at`, `InsertHookEvent` with WAL fallback), transcript tailing, and the HTTP endpoint. The relay needs the first without the other two.

**Impact:** Duplicating the bookkeeping would let local and remote mode drift; moving it keeps one implementation and one test suite.

## Steps

### 1: Port the tests

Copy the bookkeeping cases from `daemon_test.go` (first-sight upsert, git fill, touch, stamping, WAL fallback and flush order) into `ingest_test.go` against `memsession` and a temp WAL path; confirm they fail.

### 2: Move the code

`ingest.Service{Store, AccountID, Host, Git, Logger, WALPath}` with `Handle(ctx, event map[string]any) (Tracked, error)` returning whether the session was first seen (so the daemon can start its tailers) and `FlushWAL(ctx) error`.

### 3: Delegate from the daemon

The daemon keeps `/event`, `/health`, tailing and its loops and calls the service; `daemon_test.go` passes unchanged.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/ingest/ ./session/daemon/
- [x] `daemon_test.go` is unchanged and green
- [x] A failed insert lands in the WAL and `FlushWAL` replays it in order

## Files

- `mind-palace/session/ingest/ingest.go` - new package
- `mind-palace/session/ingest/ingest_test.go` - ported bookkeeping tests
- `mind-palace/session/daemon/daemon.go` - delegate to ingest.Service

## Work Log
