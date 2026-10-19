---
id: 0101-qmn
title: '`codegraph` contract: port, schema, views'
purpose: Fixes the graph shapes so the memory adapter, the FalkorDB adapter, the indexer and the read API can be built in parallel.
type: feat
status: ready
priority: "3"
effort: S
created: "2026-10-19T18:09:38.850Z"
updated: "2026-10-19T18:31:07.716Z"
tags:
  - plan-0012-cgi
  - wave-1
  - contract
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codegraph/graph.go
      - mind-palace/codegraph/view.go
      - mind-palace/codegraph/schema.go
      - mind-palace/codegraph/view_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
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

# `codegraph` contract: port, schema, views

## Problem Statement

Fixes the graph shapes so the memory adapter, the FalkorDB adapter, the indexer and the read API can be built in parallel.

**Working on this concurrently.** This story is in wave 1 of plan 0012-cgi. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0102-6fi` In-memory graph and the `codegraphtest` suite; `0123-1d8` Typed, token-capped graph read routes.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Port

`ApplyFile`, `RemoveFile`, `SetHidden`, `PutEdges`, `DropLayer`, `Drop`, `FindSymbols`, `Symbol`, `Neighbors`, `Outline`, `SearchText`.

### 3: View rule

A node is visible when its layer is in the view and no layer in the view hides its file; edges need their layer and both endpoints visible.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codegraph/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codegraph/graph.go` - `Graph` port: writes and typed reads, result types
- `mind-palace/codegraph/view.go` - `View` and the visibility rule
- `mind-palace/codegraph/schema.go` - labels, properties, edge types, index definitions as constants
- `mind-palace/codegraph/view_test.go` - visibility truth table

## Work Log
