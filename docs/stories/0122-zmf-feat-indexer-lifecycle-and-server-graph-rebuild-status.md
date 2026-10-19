---
id: 0122-zmf
title: Indexer lifecycle and `server graph rebuild|status`
purpose: Keeps graphs bounded and recoverable.
type: feat
status: ready
priority: "3"
effort: M
created: "2026-10-19T18:11:36.504Z"
updated: "2026-10-19T18:33:24.011Z"
tags:
  - plan-0012-cgi
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
      - mind-palace/codeindex/lifecycle.go
      - mind-palace/cli/server_graph.go
      - mind-palace/codeindex/lifecycle_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0105-it1", "depends"]
  plans:
    - ["0012-cgi", "included", ["3.2"]]
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

# Indexer lifecycle and `server graph rebuild|status`

## Problem Statement

Keeps graphs bounded and recoverable.

**Working on this concurrently.** This story is in wave 6 of plan 0012-cgi. Start it when these are complete: `0105-it1` Indexer: apply files, tombstones and `hiddenBy`. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0128-dpc` Docs for the code graph; `0047-48v` Run the code graph in clued-cloud.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Lifecycle

As plan 0012.

### 3: Rebuild

Replay current trees; must equal the incremental result.

### 4: Consistency check

Periodic file counts and hashes per layer.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/... ./cli/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codeindex/... ./cli/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/... ./cli/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/lifecycle.go` - idle overlay drop, layer drop on worktree removal and branch deletion, project purge, rebuild
- `mind-palace/cli/server_graph.go` - `mind-palace server graph rebuild <project>` and `status`
- `mind-palace/codeindex/lifecycle_test.go` - rebuild equals incremental

## Work Log
