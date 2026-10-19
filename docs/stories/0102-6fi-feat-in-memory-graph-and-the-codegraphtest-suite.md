---
id: 0102-6fi
title: In-memory graph and the `codegraphtest` suite
purpose: Gives every later story a graph to test against without FalkorDB, and pins the behaviour FalkorDB must match.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:09:43.796Z"
updated: "2026-10-19T19:16:05.697Z"
started: "2026-10-19T19:05:50.687Z"
completed: "2026-10-19T19:16:05.697Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./codegraph/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codegraph/memgraph/memgraph.go` - adapter
- `mind-palace/codegraph/codegraphtest/codegraphtest.go` - suite
- `mind-palace/codegraph/memgraph/memgraph_test.go` - runs the suite

## Work Log

### 2026-10-19T19:16:00.148Z - Added codegraph/memgraph (Opener over per-key maps, Graph implementing the whole port) and codegraph/codegraphtest (Run(t, Factory) with Factory func(*testing.T) codegraph.Opener; the suite opens random u<hex>__cgt-<test> keys, drops each graph and closes the opener). 22 subtests: layers, ApplyFile/Files (tombstones, id ownership per layer), replace semantics, hiddenBy kept on nil, resolved edges kept for surviving (layer,id) and dropped otherwise, RemoveFile, hiddenBy across base/branch/overlay/other, tombstones, per-layer edges (overlay rename), PutEdges replace/validation/atomicity, DropLayer, Drop and rebuild, FindSymbols, Symbol, Neighbors (relations, depth, cycles, imports/importers), Outline, SearchText, view validation, concurrent writes, Opener isolation. Behaviour the contract left open and the suite now pins: Files of an unknown layer is ErrNotFound; RemoveFile in an unknown layer is a no-op; a symbol or chunk id held by another path of the layer is ErrInvalid; packages keep the kind they were created with (empty = package); one id visible in several layers answers from the highest (FindSymbols and SearchText too); Neighbors rows ordered by depth, From then To (path, start, id, name, layer), type, alias; importers of a package absent from the view is ErrNotFound; Outline with no visible match is ErrNotFound; SearchText ANDs case-insensitive terms, supports trailing-* prefixes, treats query syntax as text. go test -race ./codegraph/... passes; gofmt/vet/build clean. Only the three listed files changed.
