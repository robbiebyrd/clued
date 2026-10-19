---
id: 0071-sct
title: '`ghsync` contracts: service, jobs port, branch policy, claims'
purpose: Fixes the shapes the code sync, docs sync and activity log stories build on, so those three can run in parallel.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:06:29.520Z"
updated: "2026-10-19T19:04:19.943Z"
started: "2026-10-19T18:57:44.949Z"
completed: "2026-10-19T19:04:19.943Z"
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

- [x] VERIFY: cd mind-palace && go test -race ./ghsync/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghsync/ghsync.go` - `Service` struct, `Delivery`, `Target`, interfaces it consumes (`Claims`, openers)
- `mind-palace/ghsync/jobs.go` - `Jobs` port: `Enqueue`, `Claim(lease)`, `Done`, `Retry(after)`, per-repository ordering
- `mind-palace/ghsync/memjobs.go` - in-memory jobs
- `mind-palace/ghsync/policy.go` - branch policy: `active` (open PR or push within `activeDays`), `default`, `all` with `maxBranches`; parsing `github.*` from `mind-palace.config.yaml`
- `mind-palace/ghsync/contracts_test.go` - memjobs ordering and leases; policy decisions

## Work Log

### 2026-10-19T19:04:14.919Z - Added mind-palace/ghsync contracts. ghsync.go: Service/New(Options) (App, Jobs, Claims, Code, Docs openers, clock, logger; no behaviour beyond construction), Target{Kind user|workspace, ID, Owner}+Validate, Delivery (minimal queued delivery), Claims port returning Claim{Installation, Target, Policy (page setting), CodeSync} or ErrNotClaimed, CodeOpener/DocsOpener (Docs returns *service.Palace). GitHubApp and CodeStore are aliases of any because ghapp (0067/0068) and code (0053) are not merged yet: the first story that uses them narrows the alias to *ghapp.App / code.Store (a one-line change in ghsync.go). jobs.go: Jobs port Enqueue/Claim(lease)/Done/Retry(after, cause)/Pause(until, rate limits: holds the installation without counting an attempt), per-OrderKey (repository, else installation) FIFO with one in flight, lease expiry counts an attempt, dead letter after MaxAttempts=10 (ErrDeadLettered), Backoff 30s doubling to 1h; JobKinds delivery/import/reconcile/sweep. memjobs.go: MemJobs (+Len, DeadLetters). policy.go: BranchPolicy{Mode active|default|all, ActiveDays 14, MaxBranches 200}, Normalize/Validate/Over, Wants, Select (default first; all caps by most recent push incl. default), EffectivePolicy(page, file) with file keys winning, ParseRepoConfig reading github.*, codeSync, remote.project leniently (YAML or JSON). 17 tests, 96% coverage; gofmt/vet/build clean, go test -race ./ghsync/... ok. Note: config.Overlay decodes with KnownFields(true), so a github: or codeSync: key in mind-palace.config.yaml is still refused by the client loader; a later story must add them to config's fileConfig (outside this story's files).
