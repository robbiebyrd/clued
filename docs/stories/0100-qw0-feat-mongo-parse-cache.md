---
id: 0100-qw0
title: Mongo parse cache
purpose: Parse each file version once across branches, layers and members, and survive restarts.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:09:33.601Z"
updated: "2026-10-19T19:10:35.332Z"
started: "2026-10-19T19:07:42.451Z"
completed: "2026-10-19T19:10:35.332Z"
tags:
  - plan-0012-cgi
  - wave-2
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 2
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/mongocache/mongocache.go
      - mind-palace/codeparse/mongocache/mongocache_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0079-zwm", "depends"]
  plans:
    - ["0012-cgi", "included", ["1.2"]]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
---

# Mongo parse cache

## Problem Statement

Parse each file version once across branches, layers and members, and survive restarts.

**Working on this concurrently.** This story is in wave 2 of plan 0012-cgi. Start it when these are complete: `0079-zwm` `codeparse` contract: facts, detection, profiles, cache. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0047-48v` Run the code graph in clued-cloud.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Implement `codeparse.Cache` on Mongo

Serialized facts; lazy invalidation by version.

### 3: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/mongocache/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./codeparse/mongocache/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/mongocache/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/mongocache/mongocache.go` - `code_parses` keyed by `(sha256, version)`
- `mind-palace/codeparse/mongocache/mongocache_test.go` - tests with `MIND_PALACE_TEST_MONGODB_URI`

## Work Log

### 2026-10-19T19:10:29.967Z - Added codeparse/mongocache: Cache over the code_parses collection (_id "<sha256>@<version>", facts stored as their JSON, lang/put_at/used_at kept), New(db, Options{Collection, TTL, Logger, Now}), EnsureIndexes (version index; optional TTL index on used_at, collMod when it moves, dropped when off), Get (undecodable entry = miss, best-effort used_at touch past TTL/8), Put (upsert, ErrTooLarge over 15 MiB), DeleteVersion, Len. 9 tests, all run against MongoDB (MIND_PALACE_TEST_MONGODB_URI, per-test database) and skip without it; gofmt/vet/build clean, go test -race ./codeparse/... passes. Only the two listed files changed.
