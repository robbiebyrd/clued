---
id: 0063-891
title: Code sync state and lock files
purpose: Each worktree's sync needs a stamp cache, a pending queue, refused files and a back-off, kept safely across concurrent hook runs.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:05:47.734Z"
updated: "2026-10-19T19:03:03.403Z"
started: "2026-10-19T18:58:15.009Z"
completed: "2026-10-19T19:03:03.403Z"
tags:
  - plan-0010-awy
  - wave-1
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/session/codesync/state.go
      - mind-palace/session/codesync/state_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  plans:
    - ["0010-awy", "included", ["3.2"]]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
---

# Code sync state and lock files

## Problem Statement

Each worktree's sync needs a stamp cache, a pending queue, refused files and a back-off, kept safely across concurrent hook runs.

**Working on this concurrently.** This story is in wave 1 of plan 0010-awy. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0064-7z3` Code sync run (`codesync.Run`).

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: State file

`<config dir>/codesync/<worktree id>.json`, mode 0600, temp file and rename; fields as plan 0010's Client collector section.

### 3: Lock

`<id>.lock` with `O_EXCL`, stale after 60 s, modelled on `session/catchup/state.go` (copy, don't edit catch-up).

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/codesync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/codesync/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/codesync/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/codesync/state.go` - state file and lock
- `mind-palace/session/codesync/state_test.go` - atomic write, lock takeover

## Work Log

### 2026-10-19T19:02:57.630Z - Added mind-palace/session/codesync/state.go: exported State (worktree, root, project, defaultBranch, ref, head, upstream, ahead, files stamp cache of FileState{size,mtime,sha256,gitOid,dirty,unpushed,synced}, pending{upserts,deletes}, lastFull, refused path->size+mtime stamp, unsupportedUntil), LoadState/SaveState (temp file + rename, file 0600, dir 0700)/RemoveState under StateDir(configDir)=<config dir>/codesync, AcquireLock (<id>.lock, O_EXCL, StaleAfter 60s, copied from catch-up, which is untouched), WorktreeID/ValidWorktreeID (wt_ + 16 hex of sha256(host NUL root); ids are validated before naming a file), and helpers QueueUpsert/QueueDelete/Dequeue, Refuse/IsRefused, BackOff/BackedOff (24h), FullDue (6h). state_test.go: 16 tests (round trip, JSON field names, atomic write under concurrent readers, failed save keeps old file, corrupt file, bad ids, lock held/release/stale takeover/concurrent single winner). gofmt, go vet, go build ./... clean; go test -race ./session/codesync/... ok. Only the two listed files changed.
