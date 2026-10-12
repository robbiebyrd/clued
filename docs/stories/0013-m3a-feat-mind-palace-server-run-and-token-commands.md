---
id: 0013-m3a
title: mind-palace server run and server token commands
purpose: Compose the clued server as a mode of the mind-palace binary with TLS, auth, graceful shutdown and server-side enrichment.
type: feat
status: in_progress
priority: "2"
effort: L
created: "2026-10-12T20:09:05.204Z"
updated: "2026-10-12T20:49:47.651Z"
started: "2026-10-12T20:49:47.651Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 12
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/cli/server.go
      - mind-palace/cli/server_test.go
      - mind-palace/server/server.go
      - mind-palace/cli/cli.go
  plans:
    - ["0006-tsw", "included", ["4.1"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# mind-palace server run and server token commands

## Problem Statement

`mind-palace serve` listens on plain HTTP on loopback with no auth and no session routes. The clued server needs a command that loads the server's plan and story config and its session config, mounts `/health`, `/api/v1/whoami`, `sessionapi` and `storeapi` behind `auth.Middleware`, terminates TLS or runs behind a proxy, runs `enrich.Loop`, logs requests and shuts down cleanly.

**Impact:** This is the deployable unit; without it the transports exist only in tests.

## Steps

### 1: Command group

`cli/server.go` adds `server run` with `--addr`, `--tls-cert`, `--tls-key`, `--behind-proxy`, `--tokens`, `--config`, `--session-config`, each mirrored by `CLUED_SERVER_ADDR`, `CLUED_SERVER_TLS_CERT`, `CLUED_SERVER_TLS_KEY`, `CLUED_SERVER_BEHIND_PROXY`, `CLUED_SERVER_TOKENS`; `server token create|list|revoke|grant` from the auth story; a `--store memory` test hook that uses `memsession` and `memstore`.

### 2: Composition

One `http.ServeMux`: `/health` unauthenticated with `version` and `auth: "bearer"`; everything else behind `auth.Middleware`; `enrich.Loop` with the server's enabler list; `ReadHeaderTimeout`, `MaxHeaderBytes`, 16 MiB body limit; one `slog` line per request with principal, method, path, status and duration.

### 3: Listening and shutdown

`ListenAndServeTLS` when cert and key are given; plain HTTP only when the address is loopback or `--behind-proxy` is set, else refuse to start with a clear error; SIGTERM drains in-flight requests then closes the stores.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./cli/ ./server/...
- [ ] `server run --addr 0.0.0.0:8443` without certificates or `--behind-proxy` exits non-zero with an error naming both options
- [ ] The TLS listener answers `/health` without a token and 401 on `/api/v1/whoami` without one
- [ ] Env values are overridden by flags, and SIGTERM lets an in-flight request finish

## Files

- `mind-palace/cli/server.go` - command group
- `mind-palace/cli/server_test.go` - flags, env, TLS, refusal, shutdown
- `mind-palace/server/server.go` - auth wrapping and composition hooks
- `mind-palace/cli/cli.go` - register the group

## Work Log
