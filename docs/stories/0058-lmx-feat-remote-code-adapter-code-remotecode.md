---
id: 0058-lmx
title: Remote code adapter (`code/remotecode`)
purpose: The client side of the transport, used by `codesync`.
type: feat
status: ready
priority: "3"
effort: S
created: "2026-10-19T18:05:23.491Z"
updated: "2026-10-19T18:26:31.534Z"
tags:
  - plan-0010-awy
  - wave-5
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/code/remotecode/remotecode.go
      - mind-palace/code/remotecode/remotecode_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0057-ykp", "depends"]
  plans:
    - ["0010-awy", "included", ["2.2"]]
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

# Remote code adapter (`code/remotecode`)

## Problem Statement

The client side of the transport, used by `codesync`.

**Working on this concurrently.** This story is in wave 5 of plan 0010-awy. Start it when these are complete: `0057-ykp` Write-only code API (`server/codeapi`) and `CluedOptions.Code`. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0064-7z3` Code sync run (`codesync.Run`); `0070-4j4` GitHub-backed projects in the code API.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 2.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Implement the writes and `capabilities`

Map 404, 403, 409 and 413 to typed errors (`ErrUnsupported`, `ErrForbidden`, `ErrConflict`, `ErrTooLarge`, plus `ErrQuota` for kind `quota`).

### 3: Pass the suite over HTTP

Reads in the suite use the server-side store handle since the API has none.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/remotecode/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./code/remotecode/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/remotecode/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/remotecode/remotecode.go` - client for the write routes and `capabilities` over `remote.Client`
- `mind-palace/code/remotecode/remotecode_test.go` - `codetest` over `httptest` against `codeapi`, reads through the server-side store

## Work Log
