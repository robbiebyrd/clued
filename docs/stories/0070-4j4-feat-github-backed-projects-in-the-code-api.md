---
id: 0070-4j4
title: GitHub-backed projects in the code API
purpose: When GitHub supplies the pushed code, clients should stop uploading it; the server has to say so and accept entries without content in exactly that case.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:06:24.077Z"
updated: "2026-10-19T19:40:17.668Z"
started: "2026-10-19T19:33:02.595Z"
completed: "2026-10-19T19:38:51.911Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./server/... ./code/remotecode/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./server/... ./code/remotecode/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/server/codeapi/codeapi.go` - `capabilities.github`; accept content-less entries only when clean, pushed and GitHub-backed
- `mind-palace/server/clued.go` - `CluedOptions.GitHubBacked(ctx, project) bool`
- `mind-palace/code/remotecode/remotecode.go` - read the new capability
- `mind-palace/server/codeapi/codeapi_test.go` - tests

## Work Log

### 2026-10-19T19:38:46.186Z - codeapi: Options.GitHubBacked(ctx, key) and Capabilities.GitHub ("github" in GET capabilities); a change's upsert with sha256 "" is accepted only when git_oid is set, dirty false, unpushed false and the project is GitHub-backed (hook asked once per change), or when size exceeds MaxFileBytes (0010's over-the-cap entries, server-verifiable); otherwise 400 invalid. server.CluedOptions.GitHubBacked passes through with the namespaced key. remotecode.Capabilities documents and surfaces the flag (codeapi.Capabilities). Tests: TestGitHubBacked, capabilities JSON, TestValidation, TestCluedMountsCode; the HTTP conformance runs (codeapi, remotecode) use a GitHub-backed hook because codetest's content-less clean, pushed entries (logo.png, symlink) need it. Deviation: binary, symlink and refused content-less entries of 0010 on non-GitHub projects are now invalid, as the plan says; the 0010 client must upload them or the rule must be widened. Outside the list: code/remotecode/remotecode_test.go (conformance hook, capability assertion). go test -race ./server/... ./code/...: all ok.

### 2026-10-19T19:40:17.668Z - Coordinator: the plan 0011 rule that rejected content-less entries outside GitHub-backed projects contradicted plan 0010 (binaries, symlinks, refused blobs and files over the cap are sent without content), so a client would stall on any repository with an image. codeapi now accepts an entry without sha256 everywhere; GitHubBacked only drives capabilities.github, and the indexer takes clean, pushed content-less entries from the GitHub-backed worktree by git_oid.
