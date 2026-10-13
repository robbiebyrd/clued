---
id: 0006-6lm
title: Capture claude-remember summaries in the clued store
type: impl
status: in_progress
priority: "2"
effort: L
created: "2026-10-12T22:07:26.861Z"
updated: "2026-10-12T22:21:27.457Z"
tags:
  - feature
  - sessions
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  specs:
    - ./docs/superpowers/specs/2026-10-12-claude-remember-integration-design.md
  web:
    claude-remember: https://github.com/Digital-Process-Tools/claude-remember
progress:
  "1":
    status: complete
  "1.1":
    status: complete
  "1.2":
    status: complete
  "2":
    status: complete
  "2.1":
    status: complete
  "2.2":
    status: complete
  "3":
    status: complete
  "3.1":
    status: complete
  "3.2":
    status: complete
---

# Capture claude-remember summaries in the clued store

## Summary

**Goal:** Summaries, handoffs and identity written by claude-remember are stored in the clued session store and searchable from every session and machine through the clued tools.

**Problem:** claude-remember's Haiku-written memory lives only in per-project files on one machine; clued captures the raw transcripts but not the summaries, so other sessions cannot search them.

**Approach:** A memory package scans a claude-remember store into hashed records and posts them to the daemon as MemorySaved events; stores dedup by hash and answer a search_memory query; a hooks.d listener plus clued's own session hooks trigger ingestion.

---

# Part 2 — Implementation

**Architecture:** memory/ (scan, locate, state, post) feeds the daemon's /event endpoint; session stores gain MemoryEvents and hash dedup; query/sessionmcp/cli expose search_memory; hooks install the listener and run ingests.

**Tech Stack:** Go 1.26, net/http, MongoDB unique sparse index, bash hooks

## Constraints

- Read-only against claude-remember's files; nothing written into its store
- Idempotent ingestion: re-running never duplicates an event
- Credentials and private text never appear in errors or logs

## Files

| File | Change | Responsibility |
|---|---|---|
| `mind-palace/memory/memory.go` | create | Scan a remember store into records, locate stores for a cwd, state file, event payload |
| `mind-palace/memory/post.go` | create | Post MemorySaved events to the daemon with WAL fallback |
| `mind-palace/session/store.go` | modify | MemoryQuery and MemoryEvents on the Store interface |
| `mind-palace/session/mongosession/mongosession.go` | modify | Unique sparse index on memory.hash, MemoryEvents query, duplicate-key tolerance |
| `mind-palace/session/memsession/memsession.go` | modify | Same semantics in memory |
| `mind-palace/session/sessiontest/sessiontest.go` | modify | Conformance cases for dedup and MemoryEvents |
| `mind-palace/session/query/query.go` | modify | SearchMemory and recent memory in SessionContext |
| `mind-palace/session/sessionmcp/sessionmcp.go` | modify | search_memory tool |
| `mind-palace/cli/session.go` | modify | session memory-ingest and session search-memory commands |
| `hooks/install-remember-listener` | create | Install the hooks.d listener into every claude-remember plugin |
| `hooks/session-start` | modify | Re-install the listener and run a background ingest |
| `hooks.json` | modify | SessionEnd ingest |

## Phase 1: Memory package

### 1.1: Scan, locate and state

**Files:**
- Create: `mind-palace/memory/memory.go`
- Test: `mind-palace/memory/memory_test.go`

**Depends on:** none

- [ ] **Step 1: Write failing tests over a fixture store** — now.md with two entries, today-2026-10-11.md, recent.md, archive-2026-09-30.md, remember.md, remember.<sid>.md, identity.md; expect one record per entry and file with stable hashes
- [ ] **Step 2: Implement Scan, Locate and the state file**
- [ ] **Step 3: Run the tests** — `go test ./memory/`

**Validation:** `go test ./memory/`

### 1.2: Event payload and posting

**Files:**
- Create: `mind-palace/memory/post.go`
- Test: `mind-palace/memory/post_test.go`

**Depends on:** none

- [ ] **Step 1: Test the JSON shape against an httptest daemon and the WAL fallback**
- [ ] **Step 2: Implement Post with the ContextCollected payload conventions**

**Validation:** `go test ./memory/`

## Phase 2: Store and query

### 2.1: MemoryEvents and hash dedup in both stores

**Files:**
- Modify: `mind-palace/session/store.go`
- Modify: `mind-palace/session/mongosession/mongosession.go`
- Modify: `mind-palace/session/memsession/memsession.go`
- Test: `mind-palace/session/sessiontest/sessiontest.go`

**Depends on:** none

- [ ] **Step 1: Add conformance cases: duplicate memory hash is dropped; MemoryEvents filters by regex, kind, session and account, newest first**
- [ ] **Step 2: Implement in memsession and mongosession (unique sparse index, duplicate key tolerated)**
- [ ] **Step 3: Run the suites** — `go test -race ./session/...`

**Validation:** `go test -race ./session/...`

### 2.2: search_memory tool, CLI and session context

**Files:**
- Modify: `mind-palace/session/query/query.go`
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`
- Modify: `mind-palace/cli/session.go`

**Depends on:** none

- [ ] **Step 1: SearchMemory in query with project and git-origin scoping; recent memory in SessionContext**
- [ ] **Step 2: Register search_memory in sessionmcp; add session search-memory and session memory-ingest commands**
- [ ] **Step 3: Tests through the in-memory store** — `go test -race ./session/query/ ./session/sessionmcp/ ./cli/`

**Validation:** `go test -race ./session/... ./cli/`

## Phase 3: Plugin wiring and docs

### 3.1: Listener installation and hooks

**Files:**
- Create: `hooks/install-remember-listener`
- Modify: `hooks/session-start`
- Modify: `hooks.json`
- Modify: `hooks/register-hooks`
- Test: `mind-palace/plugin/remember_listener_test.go`

**Depends on:** none

- [ ] **Step 1: Plugin tests: listener written into a fake claude-remember install with 0755 and the right command; idempotent; session-start runs it**
- [ ] **Step 2: Implement install-remember-listener; session-start calls it and ingests in the background; SessionEnd ingests**

**Validation:** `go test ./plugin/`

### 3.2: Docs and archives

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `mind-palace/README.md`
- Modify: `commands/clued-setup.md`
- Modify: `skills/clued-setup.md`

**Depends on:** none

- [ ] **Step 1: Document the integration, the new tools and the listener**
- [ ] **Step 2: Rebuild dist and verify** — `scripts/build-binaries.sh --check`

**Validation:** `scripts/build-binaries.sh --check`

## Acceptance Criteria

- [ ] A session running claude-remember has its new now.md entries, handoff and consolidated files appear as MemorySaved events in the clued store within one save or one session boundary
- [ ] search_memory returns matching summaries from other sessions and machines of the account, newest first, with project path and git origin
- [ ] Re-running the ingest never duplicates an event
- [ ] go test -race ./... and scripts/build-binaries.sh --check pass

## Next Steps

1. A MEMORY section in prime_context
2. Apply privacy-redact to MemorySaved text
