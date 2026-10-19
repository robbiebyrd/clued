---
id: 0127-0sp
title: FalkorDB in the `server init` stack
purpose: Self-hosted servers get the graph with no manual setup.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:12:09.305Z"
updated: "2026-10-19T18:12:09.305Z"
tags:
  - plan-0012-cgi
  - wave-8
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 8
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/server/stack/stack.go
      - mind-palace/server/stack/templates.go
      - mind-palace/server/stack/apple.go
      - mind-palace/cli/server_init.go
      - mind-palace/server/stack/stack_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0059-5zs", "depends"]
    - ["0123-1d8", "depends"]
  plans:
    - ["0012-cgi", "included", ["6.1"]]
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

# FalkorDB in the `server init` stack

## Problem Statement

Self-hosted servers get the graph with no manual setup.

**Working on this concurrently.** This story is in wave 8 of plan 0012-cgi. Start it when these are complete: `0059-5zs` `server run --code-store` and the `server init` stack; `0123-1d8` Typed, token-capped graph read routes. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0128-dpc` Docs for the code graph.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 6.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Container

`falkordb/falkordb` pinned, stack network only, generated password, no persistence.

### 3: Wire

`--graph redis://…` to `server run`.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/stack/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/stack/... ./cli/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/stack/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/server/stack/stack.go` - FalkorDB service
- `mind-palace/server/stack/templates.go` - compose
- `mind-palace/server/stack/apple.go` - Apple Containers
- `mind-palace/cli/server_init.go` - pass `--graph`
- `mind-palace/server/stack/stack_test.go` - generated files

## Work Log
