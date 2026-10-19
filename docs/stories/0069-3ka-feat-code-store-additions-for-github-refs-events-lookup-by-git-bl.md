---
id: 0069-3ka
title: 'Code store additions for GitHub: refs, events, lookup by git blob id'
purpose: The GitHub sync needs to drop deleted branches, record activity events and avoid re-fetching blobs it already holds.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:06:18.886Z"
updated: "2026-10-19T19:22:44.758Z"
started: "2026-10-19T19:22:03.373Z"
completed: "2026-10-19T19:22:44.758Z"
tags:
  - plan-0011-gha
  - wave-4
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 4
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/code/store.go
      - mind-palace/code/model.go
      - mind-palace/code/memcode/memcode.go
      - mind-palace/code/mongocode/mongocode.go
      - mind-palace/code/codetest/codetest.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0055-xeo", "depends"]
    - ["0056-qbe", "depends"]
  plans:
    - ["0011-gha", "included", ["2.1"]]
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

# Code store additions for GitHub: refs, events, lookup by git blob id

## Problem Statement

The GitHub sync needs to drop deleted branches, record activity events and avoid re-fetching blobs it already holds.

**Working on this concurrently.** This story is in wave 4 of plan 0011-gha. Start it when these are complete: `0055-xeo` In-memory code store (`memcode`); `0056-qbe` MongoDB code store (`mongocode`). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0070-4j4` GitHub-backed projects in the code API; `0072-lpi` GitHub code sync: pushes, full imports, branches; `0073-2eu` GitHub activity log.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Port additions

As plan 0011, 2.1.

### 3: Both stores and the suite

Memory and Mongo pass the extended `codetest`.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./code/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/store.go` - `RemoveRef`, `AppendEvents`, `SHA256ForGitOID`
- `mind-palace/code/model.go` - `Event`
- `mind-palace/code/memcode/memcode.go` - implement
- `mind-palace/code/mongocode/mongocode.go` - implement (`code_events` collection)
- `mind-palace/code/codetest/codetest.go` - cases for the additions

## Work Log

### 2026-10-19T19:22:39.040Z - code.Store gains RemoveRef(ctx, worktree, ref) (drops the tree and the ref, appends a ref tombstone: Ref set, Full, no upserts), AppendEvents(ctx, []Event) (validated batch, store-assigned Seq on its own sequence, At filled when zero; at least once), SHA256ForGitOID(ctx, []string) map[string]string (clean entries with content, any tree; mongo uses the code_trees git_oid index), and the read Events(ctx, afterSeq, limit). code.Event carries the plan's fields plus Worktree, Commits, Forced, BaseRef/BaseCommit, Merged/MergeCommit, Name, and the docs-sync fields of ghsync.DocsEvent (DocKind, DocID, Path, Action, Problems); Event.Validate and Event* kind constants in model.go. memcode and mongocode (code_events collection, events counter in code_counters, transactional and WithoutTransactions paths) implement them; codetest.Split forwards them. Existing method signatures unchanged. codetest: 7 new cases (33 total), passing on memcode, mongocode with transactions and WithoutTransactions (replica set); gofmt/vet/build clean, go test -race ./code/... ./ghsync/... ok. No files outside the list.
