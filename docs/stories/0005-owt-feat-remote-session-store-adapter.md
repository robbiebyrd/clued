---
id: 0005-owt
title: Remote session store adapter and conformance
purpose: Let every session consumer run unchanged against a clued server by implementing session.Store over HTTP.
type: feat
status: complete
priority: "2"
effort: M
created: "2026-10-12T20:09:04.968Z"
updated: "2026-10-12T21:00:14.716Z"
started: "2026-10-12T20:49:47.617Z"
completed: "2026-10-12T21:00:14.716Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 4
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/session/remotesession/remotesession.go
      - mind-palace/session/remotesession/remotesession_test.go
      - mind-palace/session/store.go
      - mind-palace/cli/session.go
      - mind-palace/cli/session_test.go
  plans:
    - ["0006-tsw", "included", ["1.4"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Remote session store adapter and conformance

## Problem Statement

`openSessionStore` in `cli/session.go` always builds `mongosession`. The query service, restore, backfill and the `clued` MCP tools need a `session.Store` that calls the server's `sessionapi` routes instead, selected automatically when `config.json` names a server.

**Impact:** This is the piece that removes MongoDB from the client; without it remote mode has no reads.

## Steps

### 1: Adapter

`remotesession.New(*remote.Client) session.Store` implements each port method with the matching route; `Unenriched` and `SetEnrichment*` return the new `session.ErrUnsupported`; `Close` is a no-op; `ErrNotFound` is mapped from 404.

### 2: Conformance

Run `sessiontest.Run` against `httptest.NewServer(auth.Middleware(sessionapi.Handler(memsession.New())))` with a fixed token, and again against `httptest.NewTLSServer` with the test certificate written to a temp `serverCaFile`.

### 3: CLI selection

`openSessionStore` returns the remote adapter when `cfg.Remote()`; a CLI test runs `session find` with `CLUED_SERVER_URL` pointing at the test server and never touches MongoDB.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/... ./cli/
- [x] `sessiontest.Run` passes over plain HTTP and over TLS with a private CA, with no environment variables set
- [x] `session find`, `session full`, `session transcript`, `session restore` and `session backfill` work with only `serverUrl` and `serverToken` configured

## Files

- `mind-palace/session/remotesession/remotesession.go` - adapter
- `mind-palace/session/remotesession/remotesession_test.go` - conformance over HTTP and TLS
- `mind-palace/session/store.go` - ErrUnsupported sentinel
- `mind-palace/cli/session.go` - remote selection in openSessionStore
- `mind-palace/cli/session_test.go` - remote selection test

## Work Log
