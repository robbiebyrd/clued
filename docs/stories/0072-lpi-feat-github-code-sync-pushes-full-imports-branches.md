---
id: 0072-lpi
title: 'GitHub code sync: pushes, full imports, branches'
purpose: Keeps the GitHub-backed worktree (base and branch layers) current from pushes.
type: feat
status: pending
priority: "3"
effort: L
created: "2026-10-19T18:06:33.866Z"
updated: "2026-10-19T18:06:33.866Z"
tags:
  - plan-0011-gha
  - wave-5
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/ghsync/code.go
      - mind-palace/ghsync/code_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0068-tb0", "depends"]
    - ["0069-3ka", "depends"]
    - ["0071-sct", "depends"]
    - ["0062-1v2", "depends"]
  plans:
    - ["0011-gha", "included", ["2.2"]]
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
  "6":
    status: pending
---

# GitHub code sync: pushes, full imports, branches

## Problem Statement

Keeps the GitHub-backed worktree (base and branch layers) current from pushes.

**Working on this concurrently.** This story is in wave 5 of plan 0011-gha. Start it when these are complete: `0068-tb0` GitHub REST client, typed events and a fake GitHub; `0069-3ka` Code store additions for GitHub: refs, events, lookup by git blob id; `0071-sct` `ghsync` contracts: service, jobs port, branch policy, claims; `0062-1v2` Shared code filter: deny list, binary detection, size cap, per-file excludes. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0077-0l2` Docs for the GitHub App; `0046-6p1` Run the GitHub sync in clued-cloud.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Push

Before-tip rule, renames, force push or truncated compare → full import; entries clean and pushed with GitHub's blob ids.

### 3: Full import

Recursive tree, subtree walk when truncated, `Full: true` change.

### 4: Branches

Apply `policy.go`; inactive branches keep their last tree; deleted branches `RemoveRef`.

### 5: Filters

`codefilter`, account redaction patterns, repository `codeSync: false`.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./ghsync/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghsync/code.go` - push → compare → blobs → commit; full import; branch create/delete; inactivity sweep; nightly reconcile
- `mind-palace/ghsync/code_test.go` - against `ghapptest.Fake` and `memcode`

## Work Log
