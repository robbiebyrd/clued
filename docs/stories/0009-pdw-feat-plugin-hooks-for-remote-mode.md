---
id: 0009-pdw
title: Plugin hooks for remote mode
purpose: Route the bash hooks to the server when config.json names one, and start no daemon.
type: feat
status: in_progress
priority: "2"
effort: M
created: "2026-10-12T20:09:05.085Z"
updated: "2026-10-12T21:17:29.616Z"
started: "2026-10-12T21:17:29.616Z"
tags:
  - server
  - remote
  - hooks
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 8
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - hooks/event-relay
      - hooks/session-start
      - hooks/check-setup
      - mind-palace/plugin/event_relay_test.go
      - mind-palace/plugin/session_start_test.go
      - mind-palace/plugin/check_setup_test.go
  plans:
    - ["0006-tsw", "included", ["2.4"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
---

# Plugin hooks for remote mode

## Problem Statement

`hooks/event-relay` curls loopback, `hooks/session-start` starts and restarts the daemon, and `hooks/check-setup` only checks that `config.json` and the binary exist. None of them knows about a server.

**Impact:** Without these changes a remote-mode machine would still try to start a daemon and would silently drop every event.

## Steps

### 1: event-relay

When `jq -r .serverUrl` (or `CLUED_SERVER_URL`) is non-empty, pipe stdin to `${CLAUDE_PLUGIN_ROOT}/scripts/clued hook relay`; otherwise keep the curl path and WAL fallback unchanged. Stays registered `async`.

### 2: session-start

In remote mode skip the daemon health check, rebuild-restart and daemon launch; run `scripts/clued session backfill` and `scripts/clued hook relay` for the starting session detached.

### 3: check-setup

In remote mode run `scripts/clued session whoami`; print one `systemMessage` naming the server when it is unreachable, when the token is rejected, or when the current repository's project is not in the token's list.

### 4: Plugin tests

Go tests in `plugin/` start an `httptest` server over `sessionapi`, set `CLUED_SERVER_URL` and a token, run the real bash hooks and assert the routing; skipped without `jq` as today.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./plugin/
- [ ] With `serverUrl` set, `event-relay` never contacts loopback and `session-start` starts no daemon
- [ ] Without `serverUrl`, all three hooks behave exactly as before

## Files

- `hooks/event-relay` - branch on serverUrl
- `hooks/session-start` - no daemon in remote mode
- `hooks/check-setup` - whoami probe
- `mind-palace/plugin/event_relay_test.go` - remote routing
- `mind-palace/plugin/session_start_test.go` - no daemon
- `mind-palace/plugin/check_setup_test.go` - 401 and unreachable messages

## Work Log
