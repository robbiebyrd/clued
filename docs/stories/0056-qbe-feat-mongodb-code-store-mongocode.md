---
id: 0056-qbe
title: MongoDB code store (`mongocode`)
purpose: The server's real backend for collected code.
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:05:13.803Z"
updated: "2026-10-19T18:05:13.803Z"
tags:
  - plan-0010-awy
  - wave-3
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/code/mongocode/mongocode.go
      - mind-palace/code/mongocode/mongocode_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0054-psq", "depends"]
  plans:
    - ["0010-awy", "included", ["1.2"]]
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
---

# MongoDB code store (`mongocode`)

## Problem Statement

The server's real backend for collected code.

**Working on this concurrently.** This story is in wave 3 of plan 0010-awy. Start it when these are complete: `0054-psq` Code store conformance suite (`codetest`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0059-5zs` `server run --code-store` and the `server init` stack; `0041-rw5` Wire the code store into clued-cloud; `0069-3ka` Code store additions for GitHub: refs, events, lookup by git blob id.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Collections and indexes

As plan 0010's Design: `code_trees {worktree, ref, path}` unique and `{git_oid}`; `code_changes {seq}`; follow `store/mongostore`'s naming convention for per-project collections.

### 3: Atomic commit

Use a transaction when the deployment supports one; otherwise order writes so a retried commit is idempotent.

### 4: Run the suite against MongoDB

With `MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017`.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/mongocode/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./code/mongocode/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/mongocode/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/mongocode/mongocode.go` - collections `code_blobs`, `code_trees`, `code_worktrees`, `code_changes`, the per-project sequence counter, indexes
- `mind-palace/code/mongocode/mongocode_test.go` - runs `codetest.Run` when `MIND_PALACE_TEST_MONGODB_URI` is set

## Work Log
