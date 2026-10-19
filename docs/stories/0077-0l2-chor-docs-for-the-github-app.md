---
id: 0077-0l2
title: Docs for the GitHub App
purpose: Users and operators need to know what the app reads, stores and keeps.
type: chor
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:07:00.716Z"
updated: "2026-10-19T18:07:00.716Z"
tags:
  - plan-0011-gha
  - wave-9
  - docs
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 9
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - docs/github-app.md
      - PROTOCOL.md
      - README.md
      - .claude/CLAUDE.md
      - mind-palace/README.md
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0072-lpi", "depends"]
    - ["0073-2eu", "depends"]
    - ["0075-it7", "depends"]
    - ["0076-tdf", "depends"]
    - ["0066-zp3", "depends"]
  plans:
    - ["0011-gha", "included", ["4.1"]]
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

# Docs for the GitHub App

## Problem Statement

Users and operators need to know what the app reads, stores and keeps.

**Working on this concurrently.** This story is in wave 9 of plan 0011-gha. Start it when these are complete: `0072-lpi` GitHub code sync: pushes, full imports, branches; `0073-2eu` GitHub activity log; `0075-it7` Plans and stories sync from the default branch; `0076-tdf` Clients skip clean, pushed content on GitHub-backed projects; `0066-zp3` Docs and `/clued-setup` for code collection. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0128-dpc` Docs for the code graph.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 4.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Write `docs/github-app.md`

Including that information flows GitHub → clued only and data stays until wiped.

### 3: Update PROTOCOL.md and layout tables

Update PROTOCOL.md and layout tables.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./plugin/`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test ./plugin/
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./plugin/
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `docs/github-app.md` - permissions, events, what is stored, sync and conflicts, branch policy, uninstall
- `PROTOCOL.md` - `capabilities.github`, content-less entries
- `README.md` - pointer
- `.claude/CLAUDE.md` - layout: `ghapp/`, `ghsync/`
- `mind-palace/README.md` - package map

## Work Log
