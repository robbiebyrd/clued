---
id: 0075-it7
title: Plans and stories sync from the default branch
purpose: Keeps the server's plans and stories equal to the repository's default branch.
type: feat
status: ready
priority: "3"
effort: M
created: "2026-10-19T18:06:49.892Z"
updated: "2026-10-19T18:28:20.865Z"
tags:
  - plan-0011-gha
  - wave-3
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/ghsync/docs.go
      - mind-palace/ghsync/docs_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0074-uto", "depends"]
    - ["0068-tb0", "depends"]
    - ["0071-sct", "depends"]
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

# Plans and stories sync from the default branch

## Problem Statement

Keeps the server's plans and stories equal to the repository's default branch.

**Working on this concurrently.** This story is in wave 3 of plan 0011-gha. Start it when these are complete: `0074-uto` `store.Sync` conflict mode `newer`; `0068-tb0` GitHub REST client, typed events and a fake GitHub; `0071-sct` `ghsync` contracts: service, jobs port, branch policy, claims. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0077-0l2` Docs for the GitHub App; `0046-6p1` Run the GitHub sync in clued-cloud.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.3 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Fetch

Directories from the repository's `mind-palace.config.yaml` at the commit (defaults `docs/plans`, `docs/stories`) into a memory store.

### 3: Sync

`newer`; invalid documents skipped with `plan_sync_problem`; deletions archived through the service.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./ghsync/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghsync/docs.go` - fetch, validate, sync with `newer`, archive deletions, outcome events
- `mind-palace/ghsync/docs_test.go` - tests

## Work Log
