---
id: 0073-2eu
title: GitHub activity log
purpose: Records pushes, branches, pull requests and CI for later use, without storing whole payloads.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:06:39.671Z"
updated: "2026-10-19T19:29:08.334Z"
started: "2026-10-19T19:23:46.386Z"
completed: "2026-10-19T19:29:08.334Z"
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
      - mind-palace/ghsync/events.go
      - mind-palace/ghsync/events_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0068-tb0", "depends"]
    - ["0069-3ka", "depends"]
    - ["0071-sct", "depends"]
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
---

# GitHub activity log

## Problem Statement

Records pushes, branches, pull requests and CI for later use, without storing whole payloads.

**Working on this concurrently.** This story is in wave 5 of plan 0011-gha. Start it when these are complete: `0068-tb0` GitHub REST client, typed events and a fake GitHub; `0069-3ka` Code store additions for GitHub: refs, events, lookup by git blob id; `0071-sct` `ghsync` contracts: service, jobs port, branch policy, claims. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0077-0l2` Docs for the GitHub App; `0046-6p1` Run the GitHub sync in clued-cloud.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Map events

As plan 0011's Activity log table; logins kept, e-mail addresses never.

### 3: Track open pull requests

Feed the branch policy's `active` rule.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./ghsync/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghsync/events.go` - allow-listed `code.Event` per subscribed event
- `mind-palace/ghsync/events_test.go` - fixture → event tests

## Work Log

### 2026-10-19T19:29:01.451Z - Added mind-palace/ghsync/events.go: ActivityEvents(Delivery, ghapp.Event, now) []code.Event maps every subscribed event/action of plan 0011's activity-log table (push to a branch, branch create/delete, pull_request opened/synchronize/closed/reopened/ready_for_review, check_suite/check_run/workflow_run completed, status, repository renamed/transferred/archived/deleted) to one allow-listed event; other actions, tags, installation and ping record nothing. Fields that would fail code.Event.Validate are dropped, URLs kept only as plain https page links, names clipped; no e-mail or payload text is kept. AppendActivity writes them via code.Store; DocsActivityEvents + ActivityDocsRecorder map the docs sync outcomes (plan_sync*). OpenPRs (Apply, OpenPRsFromList, Mark, Branches, Has) and ReplayOpenPRs track open pull requests for the branch policy's active rule. events_test.go: 11 tests over the ghapp fixtures (36 passing tests in ./ghsync with existing ones). gofmt/vet/build clean, go test -race ./ghsync/... ok. Only the two listed files changed.
