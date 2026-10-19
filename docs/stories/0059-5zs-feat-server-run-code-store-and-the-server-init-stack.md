---
id: 0059-5zs
title: '`server run --code-store` and the `server init` stack'
purpose: Self-hosted servers need a way to turn the code store on.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:05:28.291Z"
updated: "2026-10-19T19:32:37.779Z"
started: "2026-10-19T19:25:08.615Z"
completed: "2026-10-19T19:32:37.779Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./cli/... ./server/stack/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./cli/... ./server/stack/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/cli/server_run.go` - `--code-store mongo|memory|off`, `CLUED_SERVER_CODE_STORE`
- `mind-palace/cli/server_init.go` - turn the code store on in the generated stack
- `mind-palace/server/stack/stack.go` - pass the flag
- `mind-palace/server/stack/templates.go` - compose/apple templates
- `mind-palace/cli/server_run_test.go` - flag tests
- `mind-palace/server/stack/stack_test.go` - generated files

## Work Log

### 2026-10-19T19:32:22.337Z - server run: --code-store mongo|memory|off and CLUED_SERVER_CODE_STORE (flag, then env, then the session store's backend: memory under --store memory, else mongo); unknown values are a bad_request before anything opens. One code store per namespaced project key, opened on first use and cached (codeStores in cli/server_run.go): memcode for memory; mongocode.Open over the session config's MongoDB (mongoUrl, dbName) with mongocode.ProjectPrefix("", key) for mongo, pinged at start like the session store and disconnected on shutdown; off leaves CluedOptions.Code nil (code routes 404). server init: stack.CodeStore = "mongo" passed as CLUED_SERVER_CODE_STORE by the compose file and by up.sh (Apple Containers); no new container. Deviation: the Apple Containers template lives in server/stack/apple.go, not templates.go, so step 3 needed a one-line change there and the matching expected-args update in server/stack/apple_test.go (both outside Files). Tests: 6 new cli tests (flag/env/default, unknown kind, memory per project, off is 404, mongo uses the session Mongo, real MongoDB with MIND_PALACE_TEST_MONGODB_URI) and TestInitTurnsTheCodeStoreOnInEveryRuntime (4 subtests); go test -race ./cli/... ./server/... ./plugin/... pass with MongoDB (135 top-level tests in cli + server/stack).
