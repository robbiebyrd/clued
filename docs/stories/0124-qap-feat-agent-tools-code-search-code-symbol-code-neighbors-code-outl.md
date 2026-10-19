---
id: 0124-qap
title: 'Agent tools: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`'
purpose: Gives agents compact answers with `path:line` instead of grep and whole-file reads.
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:11:48.239Z"
updated: "2026-10-19T18:11:48.239Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
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

- [ ] VERIFY: cd mind-palace && go test -race ./session/sessionmcp/... ./code/remotecode/... && go test ./plugin/
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/sessionmcp/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/sessionmcp/code_tools.go` - the five tools
- `mind-palace/session/sessionmcp/sessionmcp.go` - register them when indexed and allowed
- `mind-palace/code/remotecode/graph.go` - client for the read routes
- `mind-palace/session/sessionmcp/code_tools_test.go` - tests
- `mind-palace/plugin/code_tools_test.go` - plugin test against a server with `memgraph`

## Work Log
