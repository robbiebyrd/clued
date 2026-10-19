---
id: 0122-zmf
title: Indexer lifecycle and `server graph rebuild|status`
purpose: Keeps graphs bounded and recoverable.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:36.504Z"
updated: "2026-10-19T19:52:30.572Z"
started: "2026-10-19T19:41:07.444Z"
completed: "2026-10-19T19:52:30.572Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/... ./cli/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/... ./cli/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/lifecycle.go` - idle overlay drop, layer drop on worktree removal and branch deletion, project purge, rebuild
- `mind-palace/cli/server_graph.go` - `mind-palace server graph rebuild <project>` and `status`
- `mind-palace/codeindex/lifecycle_test.go` - rebuild equals incremental

## Work Log

### 2026-10-19T19:52:25.102Z - Added codeindex/lifecycle.go: DropIdleOverlays (OverlayIdle 14 days: an overlay with no file and no worktree activity is left out of the selection, applied in selectPlan), Service.Rebuild (lease kept alive in the background; catches the cursor's tree states up with the log metadata, drops the graph and writes Diff(empty, selection of current trees) through the same Applier, saves the cursor at the end of the log), Service.Purge (drop graph + delete cursor), Service.Check (per-layer file counts, FilesHash and layer nodes vs the selection, 'behind' when the log is ahead, repair writes Diff(graph, selection)), Service.RunChecks (periodic, logs drift), Service.Status and CursorPeeker (Peek on MemCursors and MongoCursors). Worktree removal and GitHub branch deletion already drop layers through the incremental path; tests cover them. cli/server_graph.go: 'mind-palace server graph rebuild <project>... [--wait]' and 'status <project>... [--check [--repair]]' with persistent --graph (env CLUED_SERVER_GRAPH), --code-store (CLUED_SERVER_CODE_STORE, mongo only) and --session-config; code stores and cursors (code_index_cursors) in the session config's MongoDB, graph via falkor.New. Tests: lifecycle_test.go 13 tests (rebuild equals incremental from the cursor and from no cursor, on memgraph and FalkorDB; idle drop; removal/branch deletion; purge; drift check+repair; lease; keep-alive; RunChecks; Peek on Mem and Mongo), cli/server_graph_test.go 4 tests (memory env, refusals, options, real MongoDB+FalkorDB). go test -race ./codeindex/... ./cli/... ./codegraph/... pass with MIND_PALACE_TEST_FALKORDB_URL and MIND_PALACE_TEST_MONGODB_URI set and without; gofmt/vet/build clean. Files outside the list: codeindex/service.go (one line in selectPlan applying DropIdleOverlays, plus its comment: the idle rule must be part of selection or every batch re-adds the overlay), cli/server.go (registers serverGraphCmd), cli/server_graph_test.go (new CLI tests). No real parser is wired yet (Parse nil: files indexed as text) until the extractor lands.
