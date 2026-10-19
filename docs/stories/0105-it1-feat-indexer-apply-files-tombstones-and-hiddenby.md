---
id: 0105-it1
title: 'Indexer: apply files, tombstones and `hiddenBy`'
purpose: Writes each changed file into its layer and keeps views correct.
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:09:59.821Z"
updated: "2026-10-19T18:09:59.821Z"
tags:
  - plan-0012-cgi
  - wave-5
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/apply.go
      - mind-palace/codeindex/apply_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0104-v9x", "depends"]
    - ["0080-6sk", "depends"]
  plans:
    - ["0012-cgi", "included", ["3.1"]]
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

# Indexer: apply files, tombstones and `hiddenBy`

## Problem Statement

Writes each changed file into its layer and keeps views correct.

**Working on this concurrently.** This story is in wave 5 of plan 0012-cgi. Start it when these are complete: `0104-v9x` Indexer: change-log consumer and layer builder; `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0106-wdt` Resolution core and the Go resolver; `0122-zmf` Indexer lifecycle and `server graph rebuild|status`.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Apply

Facts from the cache (parse on miss).

### 3: Visibility

Maintain `hiddenBy` on the same path in lower layers; deletions as tombstones.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codeindex/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/apply.go` - parse via cache, `ApplyFile`, tombstones, hide lower files
- `mind-palace/codeindex/apply_test.go` - three-layer tests

## Work Log
