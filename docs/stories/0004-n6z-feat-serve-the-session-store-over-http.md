---
id: 0004-n6z
title: Serve the session store over HTTP (sessionapi)
purpose: Expose every session.Store method as an authenticated JSON route so a client needs no database connection.
type: feat
status: pending
priority: "2"
effort: M
created: "2026-10-12T20:09:04.939Z"
updated: "2026-10-12T20:09:04.939Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/server/sessionapi/sessionapi.go
      - mind-palace/server/sessionapi/sessionapi_test.go
  plans:
    - ["0006-tsw", "included", ["1.3"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Serve the session store over HTTP (sessionapi)

## Problem Statement

Session reads and writes go straight to MongoDB from the daemon, the CLI and the MCP tools. The server needs an HTTP transport over the `session.Store` port, scoped by the authenticated account, so that the same callers can run with a remote adapter and the conformance suite can prove fidelity.

**Impact:** Without a method-for-method transport the remote adapter could not pass `sessiontest.Run`, and callers would need special cases.

## Steps

### 1: Route table

`sessionapi.Handler(store session.Store, limits Limits) http.Handler` mounts under `/api/v1/session/`: `PUT /sessions`, `POST /sessions/{id}/touch`, `POST /sessions/{id}/git`, `GET /sessions/{id}`, `POST /sessions/find`, `POST /hook-events` (array), `POST /hook-events/by-tool-use`, `PUT /transcript-lines` (≤500), `GET /transcript-lines/count`, `POST /transcript-lines/query`, `PUT /subagent-lines`, `GET /subagents`, `POST /subagent-lines/query`, `PUT /blobs`, `POST /blobs/query`, `POST /bash-events`, `GET /sessions/{id}/full`. Enrichment methods are not exposed.

### 2: Account scoping

Every write overwrites `account_id` with the principal's account; every read passes it as the query's `AccountID`. Session ids and paths from requests are validated inline before use.

### 3: Envelope and limits

Share the `httpapi` envelope writer (export it or move it to a small shared file); 16 MiB body limit → 413, malformed JSON → 400, `ErrNotFound` → 404, store failure → 502 `store`.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/sessionapi/
- [ ] A write with a foreign `account_id` is stored under the token's account, and a read for another account returns nothing
- [ ] A transcript batch above 500 lines and a body above 16 MiB are rejected with the documented codes

## Files

- `mind-palace/server/sessionapi/sessionapi.go` - handler and routes
- `mind-palace/server/sessionapi/sessionapi_test.go` - route tests against memsession
- `mind-palace/server/httpapi/httpapi.go` - export the envelope helpers if needed

## Work Log
