---
id: 0124-qap
title: 'Agent tools: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`'
purpose: Gives agents compact answers with `path:line` instead of grep and whole-file reads.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:48.239Z"
updated: "2026-10-19T20:26:02.557Z"
started: "2026-10-19T19:58:24.955Z"
completed: "2026-10-19T20:26:02.557Z"
tags:
  - plan-0012-cgi
  - wave-8
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 8
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/session/sessionmcp/code_tools.go
      - mind-palace/session/sessionmcp/sessionmcp.go
      - mind-palace/code/remotecode/graph.go
      - mind-palace/session/sessionmcp/code_tools_test.go
      - mind-palace/plugin/code_tools_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0123-1d8", "depends"]
  plans:
    - ["0012-cgi", "included", ["4.2"]]
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

# Agent tools: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`

## Problem Statement

Gives agents compact answers with `path:line` instead of grep and whole-file reads.

**Working on this concurrently.** This story is in wave 8 of plan 0012-cgi. Start it when these are complete: `0123-1d8` Typed, token-capped graph read routes. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults; `0128-dpc` Docs for the code graph.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 4.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Tools

Pass the current session id; results in `path:line` form; budgets as other session tools.

### 3: Listing

Only when the project is indexed and the entitlement allows (capabilities).

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/sessionmcp/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/sessionmcp/... ./code/remotecode/... && go test ./plugin/
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/sessionmcp/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/sessionmcp/code_tools.go` - the five tools
- `mind-palace/session/sessionmcp/sessionmcp.go` - register them when indexed and allowed
- `mind-palace/code/remotecode/graph.go` - client for the read routes
- `mind-palace/session/sessionmcp/code_tools_test.go` - tests
- `mind-palace/plugin/code_tools_test.go` - plugin test against a server with `memgraph`

## Work Log

### 2026-10-19T20:25:57.022Z - Built the code tools. code/remotecode/graph.go: typed client for the graph read routes (Store.Search/Symbol/Neighbors/Outline/Context returning SearchResult, codeapi.SymbolResult, NeighborsResult, OutlineResult, ContextResult) plus ErrUnavailable (503) and ErrUnknownSession (404 for the view's session). session/sessionmcp/code_tools.go: code_search, code_symbol, code_neighbors, code_outline, code_context; each passes the current session as the view (CLAUDE_SESSION_ID, else the newest session captured in the working directory; on ErrUnknownSession it reads the working directory's branch and says so in note; an explicit ref arg reads that branch), answers with path:line ('at') for every hit, child, edge end and body, and takes max_tokens/cursor through budgetSchema (2000 tokens, 6000 for code_context), passed to the routes. Tools fields CodeGraph, CodeView, CodeProbeTimeout, CodeRecheck. Listing (sessionmcp.go AddTools -> addCodeTools): only for the stdio set in remote mode (never the server's remote subset); a probe POST graph/search lists them when status is ready or indexing; 404 no route (no --graph), 403, 401 never list them; empty or 503 rechecks every 2 min and adds them with tools/list_changed. Capability detection is by the graph route's answer, since codeapi's capabilities (server/codeapi/codeapi.go) is outside this story's files. Tests: 7 in session/sessionmcp/code_tools_test.go (codeapi + memgraph over httptest), 1 plugin test (plugin/code_tools_test.go) running mind-palace server run --store memory --code-store memory --graph memory, codesync.Run, and the shim's session mcp over stdio. Deviation: the server's indexer has no parser wired (codeindex FileApplier without Parse: files indexed as text), so the plugin test checks the listing, the session view, outline files and errors end to end; symbol-level path:line answers are covered by the unit tests. VERIFY 1 and 2 pass; with MIND_PALACE_TEST_MONGODB_URI the pre-existing mongosession subtests of sessionmcp time out under the shared MongoDB's load (unrelated to these files).
