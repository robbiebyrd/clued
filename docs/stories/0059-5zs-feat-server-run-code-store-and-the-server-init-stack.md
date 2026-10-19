---
id: 0059-5zs
title: '`server run --code-store` and the `server init` stack'
purpose: Self-hosted servers need a way to turn the code store on.
type: feat
status: ready
priority: "3"
effort: S
created: "2026-10-19T18:05:28.291Z"
updated: "2026-10-19T18:26:38.063Z"
tags:
  - plan-0010-awy
  - wave-5
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/cli/server_run.go
      - mind-palace/cli/server_init.go
      - mind-palace/server/stack/stack.go
      - mind-palace/server/stack/templates.go
      - mind-palace/cli/server_run_test.go
      - mind-palace/server/stack/stack_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0057-ykp", "depends"]
    - ["0056-qbe", "depends"]
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
---

# `server run --code-store` and the `server init` stack

## Problem Statement

Self-hosted servers need a way to turn the code store on.

**Working on this concurrently.** This story is in wave 5 of plan 0010-awy. Start it when these are complete: `0057-ykp` Write-only code API (`server/codeapi`) and `CluedOptions.Code`; `0056-qbe` MongoDB code store (`mongocode`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Owns `cli/server_run.go`, `cli/server_init.go` and `server/stack/*` for 0010; 0012/I13 and I16 edit them after this one. Unblocks: `0066-zp3` Docs and `/clued-setup` for code collection; `0123-1d8` Typed, token-capped graph read routes; `0127-0sp` FalkorDB in the `server init` stack.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Flag and env

Default: the session store's backend.

### 3: Stack

Compose and Apple Containers both pass it; no new container.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./cli/... ./server/stack/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./cli/... ./server/stack/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./cli/... ./server/stack/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/cli/server_run.go` - `--code-store mongo|memory|off`, `CLUED_SERVER_CODE_STORE`
- `mind-palace/cli/server_init.go` - turn the code store on in the generated stack
- `mind-palace/server/stack/stack.go` - pass the flag
- `mind-palace/server/stack/templates.go` - compose/apple templates
- `mind-palace/cli/server_run_test.go` - flag tests
- `mind-palace/server/stack/stack_test.go` - generated files

## Work Log
