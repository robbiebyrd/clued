---
id: 0079-zwm
title: '`codeparse` contract: facts, detection, profiles, cache'
purpose: Fixes the shapes the extractor, the 19 language stories, the cache and the indexer build on.
type: feat
status: pending
priority: "3"
effort: S
created: "2026-10-19T18:07:11.562Z"
updated: "2026-10-19T18:07:11.562Z"
tags:
  - plan-0012-cgi
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
      - mind-palace/codeparse/facts.go
      - mind-palace/codeparse/detect.go
      - mind-palace/codeparse/profiles.go
      - mind-palace/codeparse/cache.go
      - mind-palace/codeparse/contract_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  plans:
    - ["0012-cgi", "included", ["1.2"]]
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

# `codeparse` contract: facts, detection, profiles, cache

## Problem Statement

Fixes the shapes the extractor, the 19 language stories, the cache and the indexer build on.

**Working on this concurrently.** This story is in wave 1 of plan 0012-cgi. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language; `0100-qw0` Mongo parse cache; `0104-v9x` Indexer: change-log consumer and layer builder.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Types

As plan 0012's Parsing component.

### 3: Detection

Extensions from the Languages table; `.h` by content (`@interface`, `class`, `template`, `namespace`) and neighbours.

### 4: Profiles as data

One `profile.yaml` per language directory so language stories never edit Go files.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codeparse/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/facts.go` - `Facts`, `Symbol`, `Import`, `Ref`, `Chunk`, `Pos`, `Version`
- `mind-palace/codeparse/detect.go` - language by extension and content, including `.h` (C/C++/Objective-C) and `.m`
- `mind-palace/codeparse/profiles.go` - loading `queries/<lang>/profile.yaml` (family, extensions, defining forms)
- `mind-palace/codeparse/cache.go` - `Cache` interface and memory cache keyed by `(sha256, version)`
- `mind-palace/codeparse/contract_test.go` - detection and profile loading

## Work Log
