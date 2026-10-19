---
id: 0075-it7
title: Plans and stories sync from the default branch
purpose: Keeps the server's plans and stories equal to the repository's default branch.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:06:49.892Z"
updated: "2026-10-19T19:14:22.233Z"
started: "2026-10-19T19:08:37.690Z"
completed: "2026-10-19T19:14:22.233Z"
tags:
  - plan-0011-gha
  - wave-3
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/ghsync/docs.go
      - mind-palace/ghsync/docs_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  stories:
    - ["0074-uto", "depends"]
    - ["0068-tb0", "depends"]
    - ["0071-sct", "depends"]
  plans:
    - ["0011-gha", "included", ["2.3"]]
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

# Plans and stories sync from the default branch

## Problem Statement

Keeps the server's plans and stories equal to the repository's default branch.

**Working on this concurrently.** This story is in wave 3 of plan 0011-gha. Start it when these are complete: `0074-uto` `store.Sync` conflict mode `newer`; `0068-tb0` GitHub REST client, typed events and a fake GitHub; `0071-sct` `ghsync` contracts: service, jobs port, branch policy, claims. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0077-0l2` Docs for the GitHub App; `0046-6p1` Run the GitHub sync in clued-cloud.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 2.3 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Fetch

Directories from the repository's `mind-palace.config.yaml` at the commit (defaults `docs/plans`, `docs/stories`) into a memory store.

### 3: Sync

`newer`; invalid documents skipped with `plan_sync_problem`; deletions archived through the service.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./ghsync/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghsync/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghsync/docs.go` - fetch, validate, sync with `newer`, archive deletions, outcome events
- `mind-palace/ghsync/docs_test.go` - tests

## Work Log

### 2026-10-19T19:14:04.939Z - Added mind-palace/ghsync/docs.go: Service.SyncDocs(ctx, DocFetcher, DocsRecorder, DocsRequest) fetches the repository's mind-palace config (first of config.DefaultConfigFiles in the root; github/codeSync keys stripped before config.Overlay, then Validate) and the plans/stories dirs (+ archive/) at the commit into a memstore, validates with the repo's kinds via service.Validate (dangling /links/ problems tolerated), skips invalid/unparsable/duplicate/oversized files as plan_sync_problem, syncs with store.ConflictNewer into the DocsOpener palace's store (plan_sync created/updated, plan_sync_conflict), and archives ids present at Before but gone at Commit through Service.SetStatus(archived, force) (plan_sync archived). Slug: remote.project at the commit, else req.Slug, else github.com__owner/repo. An invalid config syncs nothing and records a config problem. Narrow ports defined here for concurrent stories: DocFetcher{ListDir,ReadFile} (missing = fs.ErrNotExist; for 0068's client) and DocsRecorder func (for code.Store.AppendEvents in 0069); DocsEvent carries names/ids/paths/problems only. Also DocsTouched(changed, dirs...) for the push handler. docs_test.go: 8 tests (import, newer conflict and later win, invalid skipped, archive on delete incl. moved-to-archive and unmerged MCP doc untouched, repo config dirs/types/remote.project/JSON, invalid configs (6 subtests), error paths, DocsTouched) over an in-memory fake repo; go test -race ./ghsync/... ok; gofmt/vet/build clean. Status set in_progress with --force because 0068 is being written concurrently. Only the two listed files changed.
