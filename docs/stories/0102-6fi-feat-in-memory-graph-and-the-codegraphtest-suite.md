---
id: 0102-6fi
title: In-memory graph and the `codegraphtest` suite
purpose: Gives every later story a graph to test against without FalkorDB, and pins the behaviour FalkorDB must match.
type: feat
status: ready
priority: "3"
effort: M
created: "2026-10-19T18:09:43.796Z"
updated: "2026-10-19T18:31:14.456Z"
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
      - mind-palace/codegraph/memgraph/memgraph.go
      - mind-palace/codegraph/codegraphtest/codegraphtest.go
      - mind-palace/codegraph/memgraph/memgraph_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0101-qmn", "depends"]
  plans:
    - ["0012-cgi", "included", ["2.1"]]
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

# In-memory graph and the `codegraphtest` suite

## Problem Statement

Gives every later story a graph to test against without FalkorDB, and pins the behaviour FalkorDB must match.

**Working on this concurrently.** This story is in wave 2 of plan 0012-cgi. Start it when these are complete: `0101-qmn` `codegraph` contract: port, schema, views. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0103-cmc` FalkorDB graph adapter and CI service; `0104-v9x` Indexer: change-log consumer and layer builder.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Suite

Apply/remove files, `hiddenBy` across three layers, per-layer edges, tombstones, drop layer and graph, every typed read with views.

### 3: Adapter

Passes the suite.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codegraph/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codegraph/memgraph/memgraph.go` - adapter
- `mind-palace/codegraph/codegraphtest/codegraphtest.go` - suite
- `mind-palace/codegraph/memgraph/memgraph_test.go` - runs the suite

## Work Log
