---
id: 0057-ykp
title: Write-only code API (`server/codeapi`) and `CluedOptions.Code`
purpose: The transport that receives collected code.
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:05:18.723Z"
updated: "2026-10-19T18:05:18.723Z"
tags:
  - plan-0010-awy
  - wave-4
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 4
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/server/codeapi/codeapi.go
      - mind-palace/server/codeapi/codeapi_test.go
      - mind-palace/server/clued.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0055-xeo", "depends"]
  plans:
    - ["0010-awy", "included", ["2.1"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
  "5":
    status: pending
---

# Write-only code API (`server/codeapi`) and `CluedOptions.Code`

## Problem Statement

The transport that receives collected code. It is write-only: no route returns code, paths or trees.

**Working on this concurrently.** This story is in wave 4 of plan 0010-awy. Start it when these are complete: `0055-xeo` In-memory code store (`memcode`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Owns `server/clued.go` for 0010. Stories 0011/G4 and 0012/I13 edit it after this one. Unblocks: `0058-lmx` Remote code adapter (`code/remotecode`); `0059-5zs` `server run --code-store` and the `server init` stack; `0041-rw5` Wire the code store into clued-cloud.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Routes

`POST manifest`, `PUT blobs/{sha256}`, `PUT worktrees/{id}`, `POST worktrees/{id}/changes`, `PUT worktrees/{id}/sessions/{session_id}`, `DELETE worktrees/{id}`, `GET capabilities`, with the envelope, limits (5,000 per batch, `maxFileBytes`), hash verification and inline path validation.

### 3: Stamping and ownership

Owner from the principal on worktrees and changes; `session` per entry from the change's `session_id`; a body `owner` or entry `session` is `invalid`; another owner's worktree is 404.

### 4: Wire into `NewClued`

Mount only when `Code` is set (404 otherwise), namespaced like storeapi, fail closed. Expose `codeapi.Mount(mux, opts)` so 0011 and 0012 add their parts without editing the route table here.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/codeapi/... ./server/...
- [ ] [MANUAL] No GET route other than `capabilities` exists, and `capabilities` returns limits only
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/server/codeapi/codeapi.go` - the seven routes
- `mind-palace/server/codeapi/codeapi_test.go` - route tests over `memcode`
- `mind-palace/server/clued.go` - `CluedOptions.Code`, `CluedOptions.BeforeCodeWrite`, mounting under `/api/v1/projects/{project}/code/` through `NamespaceCtx`

## Work Log
