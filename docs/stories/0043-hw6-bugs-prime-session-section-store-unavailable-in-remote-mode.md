---
id: 0043-hw6
title: Prime's SESSION section reports "store unavailable" in remote mode when the store is healthy
type: bugs
status: complete
priority: "2"
created: "2026-10-15T15:31:15.869Z"
updated: "2026-10-15T16:17:20.382Z"
started: "2026-10-15T16:17:00.064Z"
completed: "2026-10-15T16:17:20.382Z"
tags:
  - prime
  - remote
  - session
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links: {}
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
---

# Prime's SESSION section reports "store unavailable" in remote mode when the store is healthy

## Problem Statement

In remote mode (a clued server or Clued Cloud), `context prime` and `context session` print `SESSION: store unavailable` even though the server is healthy and `session find` returns the session. The SESSION section has one 2-second budget (`prime.StoreTimeout`) for the whole lookup: `sessionsource.Current` opens the store, runs `FindSessions`, then `query.Service.SessionContext`, which makes about seven sequential store calls (`GetSession`, `TranscriptLines` twice, `CountTranscriptLines`, `MemoryEvents`, `Activities`, ...). Over `session/remotesession` each call is its own HTTPS request, so the total exceeds 2 s and the deadline error is replaced by the bare sentinel.

Measured on 2026-10-15 in a Claude Code cloud container linked to https://clued.cloud (server v0.1.12), for a session with 2,819 events: `session find` 1.0 s, `session context` 4.4-4.7 s, `context session` returned `store unavailable` after 2.06 s. The MEMORY section shares the same timeout and swallow pattern.

**Issue:** `mind-palace/collect/prime/prime.go:27` (`StoreTimeout = 2 * time.Second`), `:410` and `:468` (errors replaced by `collect.StoreUnavailable`), `mind-palace/cli/context.go:231` (same in the `context session` probe), `mind-palace/collect/sources/sessionsource/sessionsource.go:65` (`SessionContext` for the snapshot), `mind-palace/session/query/query.go:494` (the sequential store calls).

**Impact:** Every session started in remote mode loses the SESSION part of the prime report (last session and its top commands), the main thing prime adds over plain git state, and the report blames the store, which sends users debugging a link that works.

## Steps

### 1: Reproduce in a test

`TestCurrentSkipsTheFullSessionContext` in `collect/sources/sessionsource` wraps the store so that `GetSession`, `TranscriptLines`, `MemoryEvents` and `Activities` fail, and asserts `Current` still returns the snapshot (id, last seen, hotness, top commands). It fails while `Current` goes through `SessionContext`.

### 2: One-call snapshot

`sessionsource.Current` builds the snapshot from the session `FindSessions` already returned (model, live state, hotness, started and last seen) plus the new `query.Service.TopCommands` (one `BashEvents` call), instead of `SessionContext`. A lookup is now three store calls (find, its line count, Bash events) instead of about ten, with no new server route.

### 3: Budget per mode

`prime.RemoteStoreTimeout` (6 s) bounds SESSION and MEMORY when a clued server is configured (`context prime`, `context session`); a local store keeps `prime.StoreTimeout` (2 s). The SessionStart hook keeps a third of its budget per lookup locally and gets up to half of it (at most 6 s) in remote mode.

### 4: Say why

`prime.StoreFailure(err)` prints `store unavailable (timeout)`, `(unauthorized)` or `(forbidden)` when the error is a deadline or a remote 401/403, and the bare sentinel otherwise; used by the SESSION and MEMORY sections and the `context session` probe. `TestStoreFailureNamesTheCause` covers it; `TestSlowStoreHitsTheStoreTimeoutNotTheBudget` now expects the `(timeout)` suffix; `/clued-prime` and the `clued-context` skill document the suffix.

### 5: Verify against a real server

In a cloud container linked to Clued Cloud, the built binary's `context session` returned the session (3,000+ events) in 0.8-1.7 s, where v1.2.14 returned `store unavailable` after 2.06 s (2026-10-15).

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./collect/... ./session/... ./cli/... ./server/... ./plugin/...
- [x] VERIFY: cd mind-palace && go test -race -run TestCurrentSkipsTheFullSessionContext ./collect/sources/sessionsource/
- [x] VERIFY: cd mind-palace && go test -race -run 'TestStoreFailureNamesTheCause|TestSlowStoreHitsTheStoreTimeoutNotTheBudget' ./collect/prime/
- [x] [MANUAL] In a Claude Code cloud container linked to Clued Cloud, `context prime` shows the last session and its top commands

## Files

- `mind-palace/collect/sources/sessionsource/sessionsource.go` - Build the snapshot from the found session plus its top commands instead of `SessionContext`.
- `mind-palace/session/query/query.go` - Exported `TopCommands`.
- `mind-palace/collect/prime/prime.go` - `RemoteStoreTimeout` and `StoreFailure`; SESSION and MEMORY report the cause.
- `mind-palace/cli/context.go` - Remote budget for `context prime` and the `context session` probe; cause in the probe.
- `mind-palace/cli/hook_prime.go` - Remote store budget in the SessionStart hook.
- `commands/clued-prime.md`, `skills/clued-context/SKILL.md` - Document the cause suffix and the budgets.

## Work Log

### 2026-10-15T16:17:18.961Z - Fixed in robbiebyrd/clued#45 (merged as d152c32, released in v1.2.15). Verified: the full race suite (collect, session, server, cli, plugin) passed locally and in CI; TestCurrentSkipsTheFullSessionContext, TestStoreFailureNamesTheCause and TestSlowStoreHitsTheStoreTimeoutNotTheBudget pass on main; the v1.2.15 release binary, in a Claude Code cloud container linked to Clued Cloud, shows the SESSION section (session, model, state, top commands) where v1.2.14 printed store unavailable.
