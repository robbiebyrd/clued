---
id: 0054-psq
title: Code store conformance suite (`codetest`)
purpose: The memory, Mongo and remote stores must behave identically.
type: feat
status: ready
priority: "3"
effort: M
created: "2026-10-19T18:05:03.789Z"
updated: "2026-10-19T18:26:05.623Z"
tags:
  - plan-0010-awy
  - wave-2
  - contract
  - tests
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 2
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/code/codetest/codetest.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0053-mar", "depends"]
  plans:
    - ["0010-awy", "included", ["1.1"]]
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

# Code store conformance suite (`codetest`)

## Problem Statement

The memory, Mongo and remote stores must behave identically. A shared suite, like `store/storetest` and `session/sessiontest`, is how this repository enforces that.

**Working on this concurrently.** This story is in wave 2 of plan 0010-awy. Start it when these are complete: `0053-mar` Code store contract: model, port and validation. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0055-xeo` In-memory code store (`memcode`); `0056-qbe` MongoDB code store (`mongocode`).

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Write the suite

Cover: blob hash verification, `MissingBlobs`, `Commit` applying upserts and deletes, `Full` replacing a tree, refs isolated per worktree, the change log ordered and replayable with `Changes(afterSeq)`, `RemoveWorktree` tombstones, idempotent retried commits, path validation, `git_oid`/`dirty`/`unpushed` round-trip, `upstream`/`ahead` on changes, server-set `session` per entry, `TouchSession` start and end, `Worktrees` listing owner, default branch and sessions.

### 3: Make it implementation-agnostic

No type assertions on the store; owner and session stamping are exercised through the fields the port accepts.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] [MANUAL] The suite compiles and is runnable from any store's test (`codetest.Run`)
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/codetest/codetest.go` - `Run(t, func(t) code.Store)` with one subtest per behaviour

## Work Log
