---
id: 0053-mar
title: 'Code store contract: model, port and validation'
purpose: Every other 0010, 0011 and 0012 story builds against `code.Store` and its model.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:04:50.160Z"
updated: "2026-10-19T18:04:50.160Z"
tags:
  - plan-0010-awy
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
      - mind-palace/code/model.go
      - mind-palace/code/store.go
      - mind-palace/code/validate.go
      - mind-palace/code/validate_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  plans:
    - ["0010-awy", "included", ["1.1"]]
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

# Code store contract: model, port and validation

## Problem Statement

Every other 0010, 0011 and 0012 story builds against `code.Store` and its model. Landing the contract first, with no implementation, lets the stores, the transport, the client and the indexer be written in parallel against the same types.

**Working on this concurrently.** This story is in wave 1 of plan 0010-awy. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0054-psq` Code store conformance suite (`codetest`).

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Write the model and port

Types and interface as in plan 0010, Design → Server model and the `code.Store` port (include 0011's `Worktree.Source` field now, empty meaning `client`, so later stories don't reopen these files for it).

### 3: Write the validators

Paths: slash-separated, `filepath.IsLocal` on the cleaned path, no NUL, ≤ 1,024 bytes. Hashes: lowercase hex. Worktree ids: `wt_` + 16 hex or `gh_` + digits.

### 4: Document every exported name

Doc comments say who sets each field (client, server stamping, indexer) so implementers don't have to read the plan for it.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] [MANUAL] `code.Store` and the model compile and are documented; nothing implements them yet
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/model.go` - `Blob`, `Entry` (with `GitOID`, `Dirty`, `Unpushed`, `Session`), `Worktree` (with `Owner`, `DefaultBranch`, `Sessions`, `Source`), `SessionSeen`, `Change` (with `Owner`, `Upstream`, `Ahead`)
- `mind-palace/code/store.go` - the `code.Store` interface exactly as plan 0010's Design lists it
- `mind-palace/code/validate.go` - `ValidPath`, `ValidSHA256`, `ValidGitOID` (40 or 64 hex), `ValidWorktreeID` (`wt_`/`gh_` + id), `ValidRef`
- `mind-palace/code/validate_test.go` - table tests for every validator

## Work Log
