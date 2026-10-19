---
id: 0060-6ip
title: Code sync config keys
purpose: The client needs an opt-in switch (off by default) and limits before it uploads anything.
type: feat
status: in_progress
priority: "3"
effort: S
created: "2026-10-19T18:05:33.909Z"
updated: "2026-10-19T19:08:30.577Z"
started: "2026-10-19T18:57:30.462Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
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

### 2026-10-19T19:08:30.576Z - Added codeSync (*bool), codeSyncMaxFileBytes (default 1 MiB, DefaultCodeSyncMaxFileBytes) and codeSyncExclude to session.Config with CLUED_CODE_SYNC / CLUED_CODE_SYNC_MAX_FILE_BYTES overrides and Config.CodeSyncEnabled() (set and true, remote mode, captureContent not false). session.ValidPulledValue type-checks pulled values (captureContent/codeSync bool, positive whole cap, string list) so config-pull never writes a value that would make config.json unparsable; cli/session_login.go adds the three keys to pulledConfigKeys and uses it (code sync keys are not removed when the server sends none, unlike captureContent). config.Config.CodeSync + CodeSyncAllowed(): codeSync: false in mind-palace.config.yaml opts the repository out. Tests: 6 new session tests, 1 new config test, existing compat test updated for the default cap; go test -race ./session/ ./cli/ ./config/... ok (195 PASS lines with -v), go test ./... whole module ok, gofmt/vet/build clean. No files outside the list; this repo's own mind-palace.config.yaml was not changed (not in Files).
