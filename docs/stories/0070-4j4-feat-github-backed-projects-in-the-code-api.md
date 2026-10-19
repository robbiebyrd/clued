---
id: 0070-4j4
title: GitHub-backed projects in the code API
purpose: When GitHub supplies the pushed code, clients should stop uploading it; the server has to say so and accept entries without content in exactly that case.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:06:24.077Z"
updated: "2026-10-19T18:06:24.077Z"
tags:
  - plan-0011-gha
  - wave-6
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 6
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/server/codeapi/codeapi.go
      - mind-palace/server/clued.go
      - mind-palace/code/remotecode/remotecode.go
      - mind-palace/server/codeapi/codeapi_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0069-3ka", "depends"]
    - ["0058-lmx", "depends"]
  plans:
    - ["0011-gha", "included", ["2.1"]]
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

# GitHub-backed projects in the code API

## Problem Statement

When GitHub supplies the pushed code, clients should stop uploading it; the server has to say so and accept entries without content in exactly that case.

**Working on this concurrently.** This story is in wave 6 of plan 0011-gha. Start it when these are complete: `0069-3ka` Code store additions for GitHub: refs, events, lookup by git blob id; `0058-lmx` Remote code adapter (`code/remotecode`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0076-tdf` Clients skip clean, pushed content on GitHub-backed projects; `0046-6p1` Run the GitHub sync in clued-cloud; `0123-1d8` Typed, token-capped graph read routes.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Capability

`"github": true` from the hook.

### 3: Validation

`sha256: ""` accepted only with `git_oid`, `dirty: false`, `unpushed: false` and a GitHub-backed project; otherwise `invalid`.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/... ./code/remotecode/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/... ./code/remotecode/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/... ./code/remotecode/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/server/codeapi/codeapi.go` - `capabilities.github`; accept content-less entries only when clean, pushed and GitHub-backed
- `mind-palace/server/clued.go` - `CluedOptions.GitHubBacked(ctx, project) bool`
- `mind-palace/code/remotecode/remotecode.go` - read the new capability
- `mind-palace/server/codeapi/codeapi_test.go` - tests

## Work Log
