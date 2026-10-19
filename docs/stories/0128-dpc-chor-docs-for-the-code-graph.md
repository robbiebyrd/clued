---
id: 0128-dpc
title: Docs for the code graph
purpose: Documents the first read path for collected code.
type: chor
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:12:15.055Z"
updated: "2026-10-19T18:12:15.055Z"
tags:
  - plan-0012-cgi
  - wave-10
  - docs
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 10
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - docs/code-graph.md
      - PROTOCOL.md
      - docs/server.md
      - README.md
      - .claude/CLAUDE.md
      - mind-palace/README.md
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0124-qap", "depends"]
    - ["0127-0sp", "depends"]
    - ["0122-zmf", "depends"]
    - ["0077-0l2", "depends"]
  plans:
    - ["0012-cgi", "included", ["6.3"]]
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

# Docs for the code graph

## Problem Statement

Documents the first read path for collected code.

**Working on this concurrently.** This story is in wave 10 of plan 0012-cgi. Start it when these are complete: `0124-qap` Agent tools: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`; `0127-0sp` FalkorDB in the `server init` stack; `0122-zmf` Indexer lifecycle and `server graph rebuild|status`; `0077-0l2` Docs for the GitHub App. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 6.3 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Write `docs/code-graph.md`

Write `docs/code-graph.md`.

### 3: Update PROTOCOL.md and layout tables

Update PROTOCOL.md and layout tables.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./plugin/`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test ./plugin/
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./plugin/
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `docs/code-graph.md` - layers and views, tools, privacy, languages, rebuild
- `PROTOCOL.md` - graph routes and MCP tools
- `docs/server.md` - `--graph`, the container
- `README.md` - pointer
- `.claude/CLAUDE.md` - layout: `codeparse/`, `codegraph/`, `codeindex/`, `eval/`
- `mind-palace/README.md` - package map

## Work Log
