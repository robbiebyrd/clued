---
id: 0061-v23
title: Git probes for code sync
purpose: Code sync needs the worktree root, HEAD, the upstream commit, which paths are unpushed or dirty, and the default branch; today only origin and branch are probed.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:05:38.362Z"
updated: "2026-10-19T18:05:38.362Z"
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
      - mind-palace/session/gitinfo/gitinfo.go
      - mind-palace/session/gitinfo/gitinfo_test.go
      - mind-palace/collect/gitprobe/gitprobe.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  plans:
    - ["0010-awy", "included", ["3.2"]]
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

# Git probes for code sync

## Problem Statement

Code sync needs the worktree root, HEAD, the upstream commit, which paths are unpushed or dirty, and the default branch; today only origin and branch are probed.

**Working on this concurrently.** This story is in wave 1 of plan 0010-awy. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0064-7z3` Code sync run (`codesync.Run`).

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Add the probes

All with the existing 2 s timeout, `-z` output parsed safely: `rev-parse --show-toplevel`, `rev-parse HEAD`, `status --porcelain -z`, `ls-files -z -co --exclude-standard`, `ls-tree -r -z HEAD`, `rev-parse @{upstream}` + `rev-list --count @{upstream}..HEAD`, `diff --name-only -z @{upstream} HEAD`.

### 3: Share `DefaultBranch`

One implementation (origin/HEAD, then main/master) used by `gitprobe` and `gitinfo`.

### 4: Test the edge cases

No upstream, detached HEAD, SHA-256 repositories, renamed and untracked files.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/gitinfo/... ./collect/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/gitinfo/... ./collect/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/gitinfo/... ./collect/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/gitinfo/gitinfo.go` - `Root`, `Head`, `StatusPaths`, `ListFiles`, `TreeOIDs`, `Upstream` (commit and ahead count), `UnpushedPaths`, `DefaultBranch`
- `mind-palace/session/gitinfo/gitinfo_test.go` - tests on temporary repositories
- `mind-palace/collect/gitprobe/gitprobe.go` - use the shared `DefaultBranch` instead of its own

## Work Log
