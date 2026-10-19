---
id: 0054-psq
title: Code store conformance suite (`codetest`)
purpose: The memory, Mongo and remote stores must behave identically.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:05:03.789Z"
updated: "2026-10-19T19:09:58.651Z"
started: "2026-10-19T19:04:36.963Z"
completed: "2026-10-19T19:09:58.651Z"
tags:
  - plan-0010-awy
  - wave-2
  - contract
  - tests
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 2
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/code/codetest/codetest.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0053-mar", "depends"]
  plans:
    - ["0010-awy", "included", ["1.1"]]
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

# Code store conformance suite (`codetest`)

## Problem Statement

The memory, Mongo and remote stores must behave identically. A shared suite, like `store/storetest` and `session/sessiontest`, is how this repository enforces that.

**Working on this concurrently.** This story is in wave 2 of plan 0010-awy. Start it when these are complete: `0053-mar` Code store contract: model, port and validation. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0055-xeo` In-memory code store (`memcode`); `0056-qbe` MongoDB code store (`mongocode`).

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Write the suite

Cover: blob hash verification, `MissingBlobs`, `Commit` applying upserts and deletes, `Full` replacing a tree, refs isolated per worktree, the change log ordered and replayable with `Changes(afterSeq)`, `RemoveWorktree` tombstones, idempotent retried commits, path validation, `git_oid`/`dirty`/`unpushed` round-trip, `upstream`/`ahead` on changes, server-set `session` per entry, `TouchSession` start and end, `Worktrees` listing owner, default branch and sessions.

### 3: Make it implementation-agnostic

No type assertions on the store; owner and session stamping are exercised through the fields the port accepts.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] [MANUAL] The suite compiles and is runnable from any store's test (`codetest.Run`)
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/codetest/codetest.go` - `Run(t, func(t) code.Store)` with one subtest per behaviour

## Work Log

### 2026-10-19T19:09:53.510Z - Added mind-palace/code/codetest/codetest.go: Run(t, Factory) with 26 subtests (empty store; blob put/idempotence/usage; hash, uppercase/short/empty hash and size verification -> ErrInvalid; unknown blob -> ErrNotFound; MissingBlobs order; PutWorktree create, update (metadata, ref merge, Removed cleared, sessions kept) and malformed id -> ErrInvalid; unknown worktree -> ErrNotFound for Commit/TouchSession/RemoveWorktree; upserts/deletes and seqs from 1; Tree sorted by path; content-less upsert; ErrMissingBlob changes nothing; Full replaces only its ref; refs and worktrees isolated (incl. detached@); entry git_oid/dirty/unpushed/mode/lang/size round-trip; per-entry session from the writing change; change owner/head/upstream/ahead/session round-trip; log ordered, limited, paged with Changes(afterSeq) and replayed into the trees; retried commit idempotent with at most one duplicate; path/ref/sha/git_oid/head/upstream validation -> ErrInvalid with nothing changed; tombstone shape; TouchSession start/end/commit; Worktrees listing; concurrent commits; copies in and out). Exports Owner (the account the suite stamps, for remotecode's token), Factory, and Split{Writes, Reads} so remotecode can run the suite with the HTTP client for writes and the server-side store for reads. Requests stay within codeapi's rules (clean entries carry git_oid, entries of a never-pushed change are unpushed, one ref per PutWorktree, Source unset); times compared by order with ms truncation tolerated; RemoveWorktree's effect on Tree is deliberately not asserted (contract is silent). Verified against a throwaway reference store (52/52 subtests pass with -race, directly and via Split; not committed). VERIFY: gofmt/vet/build clean, go test -race ./code/... ok. Only the listed file changed.
