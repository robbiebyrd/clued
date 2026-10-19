---
id: 0104-v9x
title: 'Indexer: change-log consumer and layer builder'
purpose: Turns 0010's change log into the right layers.
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:09:54.069Z"
updated: "2026-10-19T19:29:29.182Z"
started: "2026-10-19T19:19:26.388Z"
completed: "2026-10-19T19:29:29.182Z"
tags:
  - plan-0012-cgi
  - wave-4
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 4
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/service.go
      - mind-palace/codeindex/cursor.go
      - mind-palace/codeindex/layers.go
      - mind-palace/codeindex/layers_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0102-6fi", "depends"]
    - ["0079-zwm", "depends"]
    - ["0055-xeo", "depends"]
  plans:
    - ["0012-cgi", "included", ["3.1"]]
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

# Indexer: change-log consumer and layer builder

## Problem Statement

Turns 0010's change log into the right layers.

**Working on this concurrently.** This story is in wave 4 of plan 0012-cgi. Start it when these are complete: `0102-6fi` In-memory graph and the `codegraphtest` suite; `0079-zwm` `codeparse` contract: facts, detection, profiles, cache; `0055-xeo` In-memory code store (`memcode`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0105-it1` Indexer: apply files, tombstones and `hiddenBy`; `0123-1d8` Typed, token-capped graph read routes.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Consume

`Changes(afterSeq)` in batches with a cursor and a lease.

### 3: Select

Base from the GitHub-backed worktree when present, else clean pushed default-branch entries; branch layers per (ref, upstream); overlays from dirty or unpushed entries.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/service.go` - `Service`, workers, per-project lease
- `mind-palace/codeindex/cursor.go` - `code_index_cursors`
- `mind-palace/codeindex/layers.go` - base, branch and overlay source selection
- `mind-palace/codeindex/layers_test.go` - selection tests from recorded changes

## Work Log

### 2026-10-19T19:29:13.451Z - Added mind-palace/codeindex: service.go (Service, Options with injected ParseFunc/ParseVersion/Cache/Applier, Sync and Run with workers, per-project lease turns, back-off, Kick; Facts loads blob -> parse cache -> parser; PlanFromGraph), cursor.go (Cursors port for code_index_cursors with MemCursors and MongoCursors: compare-and-set leases, cursor Seq plus per-tree head/upstream/upstreamSeq state from the log), layers.go (Select: base from GitHub-backed worktree else newest-upstream clean pushed default-branch entries with blob chosen by git_oid then majority sha; branch layers per (ref, upstream) holding only git_oid diffs vs base plus tombstones, GitHub tree preferred; overlays per client (worktree, ref) with dirty/unpushed entries, session tags, owner and tombstones vs the view below; Diff into per-layer put/remove plus dropped layers; Plan.View). layers_test.go: 19 tests replaying recorded change logs through memcode + memgraph (all pass with -race; TestMongoCursors runs with MIND_PALACE_TEST_MONGODB_URI, passes incl. concurrent acquire). gofmt/vet/build clean. Deviations: file writes (Facts->FileFacts mapping, ApplyFile, tombstones, hiddenBy) are left to the Applier that 0105 provides in apply.go; the Service puts and drops layer nodes itself. Only the four listed files changed (git diff vs the branch base).
