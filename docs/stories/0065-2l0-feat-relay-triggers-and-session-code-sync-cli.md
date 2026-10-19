---
id: 0065-2l0
title: Relay triggers and `session code-sync` CLI
purpose: Collection has to happen on its own, on the hook events where Claude changes files.
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:05:59.414Z"
updated: "2026-10-19T18:05:59.414Z"
tags:
  - plan-0010-awy
  - wave-7
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/cli/hook_relay.go
      - mind-palace/cli/session_codesync.go
      - mind-palace/cli/session.go
      - mind-palace/plugin/codesync_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0064-7z3", "depends"]
  plans:
    - ["0010-awy", "included", ["3.3"]]
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

# Relay triggers and `session code-sync` CLI

## Problem Statement

Collection has to happen on its own, on the hook events where Claude changes files.

**Working on this concurrently.** This story is in wave 7 of plan 0010-awy. Start it when these are complete: `0064-7z3` Code sync run (`codesync.Run`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0066-zp3` Docs and `/clued-setup` for code collection; `0076-tdf` Clients skip clean, pushed content on GitHub-backed projects.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 3.3 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Triggers

SessionStart, SessionEnd, PostToolUse (Write/Edit/MultiEdit/NotebookEdit and Bash), FileChanged, Stop, SubagentStop, CwdChanged, WorktreeCreate, WorktreeRemove, as plan 0010's table.

### 3: CLI

First import without budget; `status` shows local state only.

### 4: Plugin test

Write/Edit/Bash/Stop produce the expected trees and log; with `codeSync` off nothing is sent.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./cli/... ./plugin/`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./cli/... && go test ./plugin/
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./cli/... ./plugin/
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/cli/hook_relay.go` - map hook events to `codesync.Request`, run last within the budget
- `mind-palace/cli/session_codesync.go` - `session code-sync [--full] [--dry-run] [--path DIR]` and `status`
- `mind-palace/cli/session.go` - register the subcommand
- `mind-palace/plugin/codesync_test.go` - real hooks against a test server

## Work Log
