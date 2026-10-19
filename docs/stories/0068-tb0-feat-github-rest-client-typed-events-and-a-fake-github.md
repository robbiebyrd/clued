---
id: 0068-tb0
title: GitHub REST client, typed events and a fake GitHub
purpose: The sync needs a small, typed GitHub client and a fake to test against (never api.github.com).
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:06:13.697Z"
updated: "2026-10-19T18:06:13.697Z"
tags:
  - plan-0011-gha
  - wave-2
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 2
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/ghapp/client.go
      - mind-palace/ghapp/events.go
      - mind-palace/ghapp/ghapptest/fake.go
      - mind-palace/ghapp/client_test.go
      - mind-palace/ghapp/testdata/
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0067-2xl", "depends"]
  plans:
    - ["0011-gha", "included", ["1.1"]]
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

# GitHub REST client, typed events and a fake GitHub

## Problem Statement

The sync needs a small, typed GitHub client and a fake to test against (never api.github.com).

**Working on this concurrently.** This story is in wave 2 of plan 0011-gha. Start it when these are complete: `0067-2xl` GitHub App auth and webhook verification (`ghapp`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0072-lpi` GitHub code sync: pushes, full imports, branches; `0073-2eu` GitHub activity log; `0075-it7` Plans and stories sync from the default branch.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Client

Pagination, rate-limit headers, `Retry-After`, typed errors.

### 3: Events

push, create, delete, pull_request, check_suite, check_run, status, workflow_run, installation, installation_repositories, repository.

### 4: Fake

Serves repositories, trees (with truncation), compare, blobs, installations, open pulls.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghapp/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./ghapp/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghapp/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghapp/client.go` - `Compare`, `Tree` (recursive, truncation flag), `Blob`, `Contents`, `Installation`, `Repository`, `OpenPulls`
- `mind-palace/ghapp/events.go` - typed payloads for the subscribed events
- `mind-palace/ghapp/ghapptest/fake.go` - `httptest` GitHub from fixtures
- `mind-palace/ghapp/client_test.go` - tests against the fake
- `mind-palace/ghapp/testdata/` - recorded payload fixtures

## Work Log
