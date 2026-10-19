---
id: 0062-1v2
title: 'Shared code filter: deny list, binary detection, size cap, per-file excludes'
purpose: The client (0010) and the GitHub sync (0011) must apply the same rules, so they live in one package both import.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:05:43.018Z"
updated: "2026-10-19T18:05:43.018Z"
tags:
  - plan-0010-awy
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
      - mind-palace/codefilter/codefilter.go
      - mind-palace/codefilter/codefilter_test.go
      - mind-palace/session/redact/store.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  plans:
    - ["0010-awy", "included", ["3.2"]]
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

# Shared code filter: deny list, binary detection, size cap, per-file excludes

## Problem Statement

The client (0010) and the GitHub sync (0011) must apply the same rules, so they live in one package both import.

**Working on this concurrently.** This story is in wave 1 of plan 0010-awy. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0064-7z3` Code sync run (`codesync.Run`); `0072-lpi` GitHub code sync: pushes, full imports, branches.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Deny list

Exactly plan 0010's list (`.env`, `.env.*`, `*.pem`, `*.key`, `*.p12`, `*.pfx`, `id_rsa*`, `id_ed25519*`, `*.kdbx`, `.npmrc`, `.pypirc`, `.netrc`, `credentials*.json`, `*secret*` under `config/`); not configurable.

### 3: Binary and size

NUL in the first 8 KiB or invalid UTF-8 → metadata only; over the cap → metadata only.

### 4: Excludes

`excludePaths` (absolute globs) and `codeSyncExclude` (root-relative globs) per file.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codefilter/... ./session/redact/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codefilter/... ./session/redact/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codefilter/... ./session/redact/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codefilter/codefilter.go` - `Decide(path, size, head []byte, opts) (Content|MetadataOnly|Skip, reason)`
- `mind-palace/codefilter/codefilter_test.go` - table tests
- `mind-palace/session/redact/store.go` - export the per-path `excludePaths` matcher so it can be applied per file

## Work Log
