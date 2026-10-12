---
id: 0017-k62
title: Rebuild the bundled binaries for the clued server
purpose: Keep dist/ in step with the Go source so CI's binaries job and the shim pick up the server and relay commands.
type: chor
status: in_progress
priority: "2"
effort: XS
created: "2026-10-12T20:09:05.317Z"
updated: "2026-10-12T22:01:10.716Z"
started: "2026-10-12T22:00:55.352Z"
tags:
  - server
  - remote
  - release
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 16
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - dist/SHA256SUMS
  plans:
    - ["0006-tsw", "included", ["5.2"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Rebuild the bundled binaries for the clued server

## Problem Statement

The binaries under `dist/` must match the Go source; CI's `binaries` job runs `scripts/build-binaries.sh --check` and fails on a mismatch. Each rebuild adds about 100 MB to the repository, so it happens once per change set, after every other story in the plan has landed.

**Impact:** A stale `dist/` means the plugin's bundled binary has no `hook relay` or `server` command even though the source does.

## Steps

### 1: Full check

From `mind-palace/`: `test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./...`, and once more with `MIND_PALACE_TEST_MONGODB_URI` against a local `mongod` on 27017.

### 2: Rebuild and verify

From the repository root: `scripts/build-binaries.sh && scripts/build-binaries.sh --check`.

### 3: Commit

Commit `dist/` in the same change set as the last Go change of the plan, not in a separate PR.

## Acceptance Criteria

- [x] VERIFY: scripts/build-binaries.sh --check
- [ ] CI's `binaries` job is green on the PR that carries the rebuild
- [x] VERIFY: scripts/clued --locate && scripts/clued hook relay --help && scripts/clued server --help

## Files

- `dist/SHA256SUMS` - rebuilt with the seven binaries

## Work Log

### 2026-10-12T22:01:10.716Z - dist/ rebuilt once for the whole plan 0006-tsw change set; build-binaries.sh --check passes and the shim-resolved bundled binary serves hook relay and server. Criterion 2 waits for CI's binaries job on the PR.
