---
id: 0008-gep
title: hook relay command and the remote snapshot path
purpose: Replace the daemon on the client with a short-lived subcommand that ingests the event, catches up the transcript and flushes the WAL.
type: feat
status: in_progress
priority: "2"
effort: M
created: "2026-10-12T20:09:05.060Z"
updated: "2026-10-12T21:00:06.367Z"
started: "2026-10-12T21:00:06.367Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/cli/hook.go
      - mind-palace/cli/hook_test.go
      - mind-palace/cli/session.go
      - mind-palace/collect/snapshot/snapshot.go
      - mind-palace/collect/prime/prime.go
  plans:
    - ["0006-tsw", "included", ["2.3"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# hook relay command and the remote snapshot path

## Problem Statement

Hook events reach storage only through `curl` to the daemon on loopback, and `prime_context` posts its `ContextCollected` snapshot the same way. Remote mode needs an entrypoint that does the daemon's job for one event and exits, never blocking Claude, and a snapshot path that writes through the store.

**Impact:** This is the entrypoint that makes remote mode work without a daemon; its time budget decides whether hooks feel slow.

## Steps

### 1: hook relay

`mind-palace hook relay` reads the hook JSON from stdin, loads the config, builds `remotesession`, runs `ingest.Service.Handle`, then `catchup.Run` for the event's session (always for `Stop` and `SessionEnd`), then `FlushWAL` with the remainder of a 5 s budget; always exits 0; failures go to `<datadir>/relay.log` and the WAL.

### 2: session whoami

`mind-palace session whoami` calls `/api/v1/whoami` and prints the principal, projects and server version; non-zero exit on 401 or network failure for `check-setup` and `/clued-setup`.

### 3: Remote snapshot

`snapshot.Post` and `prime.DaemonHealthy` take a store in remote mode: the `ContextCollected` event is inserted through `ingest.Service` instead of posted to loopback; `no_snapshot` keeps its meaning.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./cli/ ./collect/...
- [ ] With the test server up, a synthetic `PostToolUse` event and a temp transcript are stored under the token's account with their lines
- [ ] With the server down, the relay writes the WAL, logs once and exits 0 within the budget
- [ ] `prime_context` without `no_snapshot` inserts a `ContextCollected` event through the remote store

## Files

- `mind-palace/cli/hook.go` - hook relay
- `mind-palace/cli/hook_test.go` - relay tests over httptest
- `mind-palace/cli/session.go` - session whoami
- `mind-palace/collect/snapshot/snapshot.go` - store-backed post
- `mind-palace/collect/prime/prime.go` - remote health path

## Work Log
