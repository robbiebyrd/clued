---
id: 0055-xeo
title: In-memory code store (`memcode`)
purpose: Tests everywhere (transport, client, indexer) and `server run --store memory` need a code store with no database.
type: feat
status: ready
priority: "3"
effort: S
created: "2026-10-19T18:05:08.895Z"
updated: "2026-10-19T18:26:11.962Z"
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
      - mind-palace/code/memcode/memcode.go
      - mind-palace/code/memcode/memcode_test.go
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
---

# In-memory code store (`memcode`)

## Problem Statement

Tests everywhere (transport, client, indexer) and `server run --store memory` need a code store with no database.

**Working on this concurrently.** This story is in wave 3 of plan 0010-awy. Start it when these are complete: `0054-psq` Code store conformance suite (`codetest`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0057-ykp` Write-only code API (`server/codeapi`) and `CluedOptions.Code`; `0069-3ka` Code store additions for GitHub: refs, events, lookup by git blob id; `0104-v9x` Indexer: change-log consumer and layer builder.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Implement every `code.Store` method

Guard state with a mutex; copy slices on the way in and out.

### 3: Run the conformance suite

`go test -race ./code/memcode/...` passes `codetest`.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/memcode/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./code/memcode/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/memcode/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/memcode/memcode.go` - the store
- `mind-palace/code/memcode/memcode_test.go` - runs `codetest.Run`

## Work Log
