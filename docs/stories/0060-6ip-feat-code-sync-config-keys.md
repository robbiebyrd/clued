---
id: 0060-6ip
title: Code sync config keys
purpose: The client needs an opt-in switch (off by default) and limits before it uploads anything.
type: feat
status: ready
priority: "3"
effort: S
created: "2026-10-19T18:05:33.909Z"
updated: "2026-10-19T18:26:44.539Z"
tags:
  - plan-0010-awy
  - wave-1
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/session/config.go
      - mind-palace/session/config_test.go
      - mind-palace/cli/session_login.go
      - mind-palace/config/config.go
      - mind-palace/config/config_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  plans:
    - ["0010-awy", "included", ["3.1"]]
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

# Code sync config keys

## Problem Statement

The client needs an opt-in switch (off by default) and limits before it uploads anything.

**Working on this concurrently.** This story is in wave 1 of plan 0010-awy. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0064-7z3` Code sync run (`codesync.Run`).

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 3.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Machine config

`CLUED_CODE_SYNC`, `CLUED_CODE_SYNC_MAX_FILE_BYTES`; `CodeSyncEnabled()` is false when nil, or when `captureContent` is false.

### 3: Pulled keys

Server/cloud can manage the three keys through `config-pull`.

### 4: Repository opt-out

`codeSync: false` in `mind-palace.config.yaml` wins over the machine config.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/ ./cli/ ./config/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/ ./cli/ ./config/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/ ./cli/ ./config/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/config.go` - `codeSync` (*bool), `codeSyncMaxFileBytes`, `codeSyncExclude`, env overrides, `CodeSyncEnabled()`
- `mind-palace/session/config_test.go` - defaults, env, captureContent=false turns it off
- `mind-palace/cli/session_login.go` - add the keys to `pulledConfigKeys`
- `mind-palace/config/config.go` - per-repository `codeSync: false` in `mind-palace.config.yaml`
- `mind-palace/config/config_test.go` - parsing

## Work Log
