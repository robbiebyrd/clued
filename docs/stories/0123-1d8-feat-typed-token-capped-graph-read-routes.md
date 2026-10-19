---
id: 0123-1d8
title: Typed, token-capped graph read routes
purpose: The first read path for collected code, for agents only and only for the caller's own view.
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:11:41.725Z"
updated: "2026-10-19T19:57:11.575Z"
started: "2026-10-19T19:41:23.196Z"
completed: "2026-10-19T19:57:11.575Z"
tags:
  - plan-0012-cgi
  - wave-7
  - hotspot
  - security
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/server/codeapi/graph.go
      - mind-palace/server/codeapi/graph_test.go
      - mind-palace/server/clued.go
      - mind-palace/cli/server_run.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0101-qmn", "depends"]
    - ["0104-v9x", "depends"]
    - ["0070-4j4", "depends"]
    - ["0059-5zs", "depends"]
  plans:
    - ["0012-cgi", "included", ["4.1"]]
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

# Typed, token-capped graph read routes

## Problem Statement

The first read path for collected code, for agents only and only for the caller's own view.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0101-qmn` `codegraph` contract: port, schema, views; `0104-v9x` Indexer: change-log consumer and layer builder; `0070-4j4` GitHub-backed projects in the code API; `0059-5zs` `server run --code-store` and the `server init` stack. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0124-qap` Agent tools: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`; `0127-0sp` FalkorDB in the `server init` stack; `0047-48v` Run the code graph in clued-cloud.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 4.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Routes

As plan 0012's table; POST with typed bodies; no raw Cypher.

### 3: Views

`{session_id}` must be the caller's session; `{ref}`; omitted → base; overlays never shared.

### 4: Budgets

`max_tokens`/`cursor` like `session/sessionmcp/budget.go`; `indexing` and `unavailable` answers.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/... ./cli/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./server/... ./cli/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/... ./cli/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/server/codeapi/graph.go` - `search`, `symbol`, `neighbors`, `outline`, `context`, view resolution, token caps; `codeapi.MountGraph`
- `mind-palace/server/codeapi/graph_test.go` - tests over `memgraph`
- `mind-palace/server/clued.go` - `CluedOptions.Graph` and mounting
- `mind-palace/cli/server_run.go` - `--graph redis://…`; off without it

## Work Log

### 2026-10-19T19:56:48.530Z - Added server/codeapi/graph.go: codeapi.MountGraph with GraphOptions{Graphs, Indexed}, POST code/graph/{search,symbol,neighbors,outline,context} with typed bodies (unknown fields refused, no raw Cypher). Views: omitted=base, {ref}=base+newest branch layer, {session_id[,ref]}=the caller's own overlay (session found via code.Worktree.Sessions on worktrees the caller owns, else 404; other accounts' overlays never included; dropped branch layer falls back to newest of its ref, then base; no overlay -> ref layers with localEdits false). Results carry status ready/indexing/empty with progress; graph unreachable -> 503 unavailable. Budgets: max_tokens/cursor via session/query (Fit, Cursor, Budget), defaults 2000 and 6000 (context budget); symbol bodies come from the stored redacted blobs and page by line. clued.go: CluedOptions.Graph mounts the routes beside Code. server_run.go: --graph (CLUED_SERVER_GRAPH, serverGraphURLEnv) redis://, rediss:// or memory; starts the indexer through graphIndexer on the shared code_index_cursors, plus RunChecks (repair) in the background; commits kick the indexer; read progress peeks the cursors. Outside Files: cli/server_graph.go gained graphEnv.Projects (passed to codeindex Options) at the coordinator's request to reuse graphIndexer in server run. Tests: graph_test.go 10 tests (views and overlay privacy, symbol bodies and paging, search paging and filters, neighbors, outline, context, unavailable/indexing/empty, auth/opt-in, end to end with codeindex Service + FileApplier) on memgraph and FalkorDB; go test -race ./server/... ./cli/... ./codeindex/... pass with MongoDB and FalkorDB; manual smoke of server run --code-store mongo --graph redis://.
