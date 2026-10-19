---
id: 0127-0sp
title: FalkorDB in the `server init` stack
purpose: Self-hosted servers get the graph with no manual setup.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:12:09.305Z"
updated: "2026-10-19T20:04:34.501Z"
started: "2026-10-19T19:58:13.843Z"
completed: "2026-10-19T20:04:34.501Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./server/stack/... ./cli/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/stack/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/server/stack/stack.go` - FalkorDB service
- `mind-palace/server/stack/templates.go` - compose
- `mind-palace/server/stack/apple.go` - Apple Containers
- `mind-palace/cli/server_init.go` - pass `--graph`
- `mind-palace/server/stack/stack_test.go` - generated files

## Work Log

### 2026-10-19T20:04:28.452Z - FalkorDB in the server init stack. server/stack: DefaultFalkorDBImage falkordb/falkordb:v4.22.1 (Options.FalkorDBImage, server init --falkordb-image); FalkorDB password generated once (32 random bytes hex) into falkordb.password 0600 and kept on reruns (Result.Graph generated|kept; a hand-made one must be 16+ letters/digits, errors never carry it); falkordb.env (REDIS_ARGS --requirepass, --save with no value, --appendonly no; REDISCLI_AUTH for the health check) and graph.env (CLUED_SERVER_GRAPH=redis://default:<pw>@falkordb:6379), both 0600, narrowed before every write. Compose: falkordb service, no ports, no volume, BROWSER=0, env_file, redis-cli health check; clued-server env_file graph.env and depends_on falkordb healthy. Apple: up.sh starts clued-falkordb with --env-file (no publish), renders run/graph.env with its address under umask 077, server gets --env-file; down.sh removes it. The password is on no command line and in no compose/script/stack.json/result. Tests: TestInitRunsFalkorDBForTheCodeGraph (4 runtime/proxy cases), TestInitKeepsTheGraphPassword; go test -race ./server/stack/... ./cli/... ok; gofmt/vet/build clean; compose service smoke-tested with docker (falkordb:latest since Docker Hub rate-limited the v4.22.1 pull): healthy, NOAUTH without password, save empty, no published port. Outside the Files list: server/stack/apple_test.go (existing exact-string expectations of the container list and the server's run line updated for the new container).
