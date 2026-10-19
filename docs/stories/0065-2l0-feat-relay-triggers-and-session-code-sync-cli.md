---
id: 0065-2l0
title: Relay triggers and `session code-sync` CLI
purpose: Collection has to happen on its own, on the hook events where Claude changes files.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:05:59.414Z"
updated: "2026-10-19T19:59:47.357Z"
started: "2026-10-19T19:49:18.504Z"
completed: "2026-10-19T19:59:47.357Z"
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

- [x] VERIFY: cd mind-palace && go test -race ./cli/... && go test ./plugin/
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./cli/... ./plugin/
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/cli/hook_relay.go` - map hook events to `codesync.Request`, run last within the budget
- `mind-palace/cli/session_codesync.go` - `session code-sync [--full] [--dry-run] [--path DIR]` and `status`
- `mind-palace/cli/session.go` - register the subcommand
- `mind-palace/plugin/codesync_test.go` - real hooks against a test server

## Work Log

### 2026-10-19T19:59:41.968Z - Relay triggers: cli/hook_relay.go maps hook events to codesync.Request (codeSyncRequest) and runs relayCodeSync last, with what is left of the 5 s relay budget capped at codesync.DefaultBudget (3 s); skipped when codeSync is off or CLUED_NO_CAPTURE=1; errors, warnings and the quota line go to relay.log, never the hook. SessionStart and CwdChanged (new_cwd) touch the session (TouchStart; Run decides full vs pending), SessionEnd TouchEnd, PostToolUse Write/Edit/MultiEdit/NotebookEdit send tool_input.file_path / notebook_path (Run keeps it only inside the worktree root), PostToolUse Bash, Stop and SubagentStop the git status delta (Status), FileChanged its file_path, WorktreeCreate a full reconcile of worktree_path when the input names one (otherwise the new worktree's first event does it, having no state), WorktreeRemove codesync.Remove(worktree_path, absolute only); PostToolUseFailure and every other event do nothing. CLI: cli/session_codesync.go adds 'session code-sync [--full] [--dry-run] [--path DIR]' (remote mode required; no time or byte budget; without --full it sends the git status delta, dirty files and the pending queue) and 'session code-sync status [--path DIR]' (local state files only: ref, head, upstream, file/synced/dirty/unpushed counts, pending and refused paths, last full, back-off), both in the ok JSON envelope; registered in cli/session.go. Plugin test plugin/codesync_test.go runs the real event-relay hook and the built binary against an in-test clued server (sessionapi + codeapi over memcode behind auth): SessionStart full tree and session, Write, Edit (relative path), a write outside the worktree (nothing sent), Bash (git rm + git mv), FileChanged, PostToolUseFailure (nothing sent), Stop (out-of-tool edit and generated file), change log attribution, CLI status / dry run / run, SessionEnd (session ended), WorktreeRemove (worktree removed, state dropped); and with codeSync off no code request, no code store, no state dir. Deviation: mind-palace/cli/session_test.go (not under Files) gained 'code-sync' in TestRootGainsOnlySessionGroup's list of session subcommands, the one-word change the new subcommand requires. PROTOCOL.md is left for 0066-zp3 (docs). Tests: gofmt/vet/build clean; go test -race ./cli/... ./plugin/ ok (210 passed, 2 skipped: the Mongo code store test without its URI, the live verify-prime test).
