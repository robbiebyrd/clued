---
id: 0071-sct
title: '`ghsync` contracts: service, jobs port, branch policy, claims'
purpose: Fixes the shapes the code sync, docs sync and activity log stories build on, so those three can run in parallel.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:06:29.520Z"
updated: "2026-10-19T18:06:29.520Z"
tags:
  - plan-0011-gha
  - wave-1
  - contract
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/ghsync/ghsync.go
      - mind-palace/ghsync/jobs.go
      - mind-palace/ghsync/memjobs.go
      - mind-palace/ghsync/policy.go
      - mind-palace/ghsync/contracts_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
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
---

# `ghsync` contracts: service, jobs port, branch policy, claims

## Problem Statement

Fixes the shapes the code sync, docs sync and activity log stories build on, so those three can run in parallel.

**Working on this concurrently.** This story is in wave 1 of plan 0011-gha. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0072-lpi` GitHub code sync: pushes, full imports, branches; `0073-2eu` GitHub activity log; `0075-it7` Plans and stories sync from the default branch; `0044-tko` GitHub webhook endpoint and Mongo job queue.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Service and interfaces

No behaviour beyond construction.

### 3: Jobs

Port plus `memjobs`.

### 4: Policy

Pure functions with tests; defaults `active`, 14 days, 200.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./ghsync/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghsync/ghsync.go` - `Service` struct, `Delivery`, `Target`, interfaces it consumes (`Claims`, openers)
- `mind-palace/ghsync/jobs.go` - `Jobs` port: `Enqueue`, `Claim(lease)`, `Done`, `Retry(after)`, per-repository ordering
- `mind-palace/ghsync/memjobs.go` - in-memory jobs
- `mind-palace/ghsync/policy.go` - branch policy: `active` (open PR or push within `activeDays`), `default`, `all` with `maxBranches`; parsing `github.*` from `mind-palace.config.yaml`
- `mind-palace/ghsync/contracts_test.go` - memjobs ordering and leases; policy decisions

## Work Log
