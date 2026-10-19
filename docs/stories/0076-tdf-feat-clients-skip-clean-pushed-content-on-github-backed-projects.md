---
id: 0076-tdf
title: Clients skip clean, pushed content on GitHub-backed projects
purpose: Avoids every machine re-uploading what GitHub already supplies.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:06:55.354Z"
updated: "2026-10-19T18:06:55.354Z"
tags:
  - plan-0011-gha
  - wave-8
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 8
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/session/codesync/codesync.go
      - mind-palace/session/codesync/codesync_test.go
      - mind-palace/plugin/codesync_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0070-4j4", "depends"]
    - ["0065-2l0", "depends"]
  plans:
    - ["0011-gha", "included", ["2.4"]]
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

# Clients skip clean, pushed content on GitHub-backed projects

## Problem Statement

Avoids every machine re-uploading what GitHub already supplies.

**Working on this concurrently.** This story is in wave 8 of plan 0011-gha. Start it when these are complete: `0070-4j4` GitHub-backed projects in the code API; `0065-2l0` Relay triggers and `session code-sync` CLI. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0077-0l2` Docs for the GitHub App.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.4 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Skip

No manifest or upload for those entries; overlays as before.

### 3: Resume

When the capability turns false, the next full reconcile uploads clean files again.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/codesync/... ./plugin/`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/codesync/... && go test ./plugin/
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/codesync/... ./plugin/
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/codesync/codesync.go` - send clean, pushed entries without content when `capabilities.github`
- `mind-palace/session/codesync/codesync_test.go` - tests
- `mind-palace/plugin/codesync_test.go` - GitHub-backed case

## Work Log
