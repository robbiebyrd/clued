---
id: 0064-7z3
title: Code sync run (`codesync.Run`)
purpose: 'The core of collection: turn a hook event into uploaded blobs and a committed change, within a budget.'
type: feat
status: ready
priority: "3"
effort: L
created: "2026-10-19T18:05:52.551Z"
updated: "2026-10-19T18:27:10.692Z"
tags:
  - plan-0010-awy
  - wave-6
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 6
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/session/codesync/codesync.go
      - mind-palace/session/codesync/enumerate.go
      - mind-palace/session/codesync/codesync_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0058-lmx", "depends"]
    - ["0060-6ip", "depends"]
    - ["0061-v23", "depends"]
    - ["0062-1v2", "depends"]
    - ["0063-891", "depends"]
  plans:
    - ["0010-awy", "included", ["3.2"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
  "5":
    status: pending
  "6":
    status: pending
  "7":
    status: pending
---

# Code sync run (`codesync.Run`)

## Problem Statement

The core of collection: turn a hook event into uploaded blobs and a committed change, within a budget.

**Working on this concurrently.** This story is in wave 6 of plan 0010-awy. Start it when these are complete: `0058-lmx` Remote code adapter (`code/remotecode`); `0060-6ip` Code sync config keys; `0061-v23` Git probes for code sync; `0062-1v2` Shared code filter: deny list, binary detection, size cap, per-file excludes; `0063-891` Code sync state and lock files. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0065-2l0` Relay triggers and `session code-sync` CLI.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Resolve identity

Root, ref, head, upstream, ahead, default branch, project slug (skip bare directory names), worktree id.

### 3: Decide the work

Full reconcile when the ref changed, `Full`, or 6 h passed; else the event's paths plus the `git status` delta; re-hash only changed stamps.

### 4: Fill the git facts

`git_oid`, `dirty`, `unpushed`, re-sent after commits and pushes.

### 5: Upload and commit

Filter and redact, hash, manifest, missing blobs, commit; budget 3 s / 8 MiB, the rest stays pending; back-off on 404/403/413 quota.

### 6: Test

`.gitignore`, deny list, binary, size cap, redaction before hashing, ref switch, rename, delete, commit and push flag refresh, budget, lock.

### 7: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/codesync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/codesync/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/codesync/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/codesync/codesync.go` - `Run(ctx, Request)`
- `mind-palace/session/codesync/enumerate.go` - enumeration and deltas
- `mind-palace/session/codesync/codesync_test.go` - tests on temporary git repositories against `memcode` over `httptest`

## Work Log
