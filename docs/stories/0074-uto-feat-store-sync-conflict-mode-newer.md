---
id: 0074-uto
title: '`store.Sync` conflict mode `newer`'
purpose: The docs sync must not overwrite an MCP edit that is newer than the repository copy.
type: feat
status: ready
priority: "3"
effort: XS
created: "2026-10-19T18:06:45.600Z"
updated: "2026-10-19T18:28:14.454Z"
tags:
  - plan-0011-gha
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
      - mind-palace/store/sync.go
      - mind-palace/store/sync_test.go
      - mind-palace/service/service.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  plans:
    - ["0011-gha", "included", ["2.3"]]
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

# `store.Sync` conflict mode `newer`

## Problem Statement

The docs sync must not overwrite an MCP edit that is newer than the repository copy.

**Working on this concurrently.** This story is in wave 1 of plan 0011-gha. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0075-it7` Plans and stories sync from the default branch.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.3 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Add the mode

Parse `newer` in `ParseConflictMode`.

### 3: Test

Same, older and newer timestamps; both kinds and templates.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./store/... ./service/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./store/... ./service/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./store/... ./service/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/store/sync.go` - `ConflictNewer`: replace when the source's `updated` is the same or later, else report
- `mind-palace/store/sync_test.go` - tests
- `mind-palace/service/service.go` - accept the mode name

## Work Log
