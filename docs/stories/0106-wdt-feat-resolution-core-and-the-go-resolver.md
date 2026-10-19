---
id: 0106-wdt
title: Resolution core and the Go resolver
purpose: Links references to definitions per layer; the engine every language family plugs into.
type: feat
status: pending
priority: "3"
effort: L
created: "2026-10-19T18:10:05.235Z"
updated: "2026-10-19T18:10:05.235Z"
tags:
  - plan-0012-cgi
  - wave-6
  - contract
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 6
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolve.go
      - mind-palace/codeindex/resolvers/registry.go
      - mind-palace/codeindex/resolvers/golang.go
      - mind-palace/codeindex/resolve_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0105-it1", "depends"]
  plans:
    - ["0012-cgi", "included", ["3.2"]]
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

# Resolution core and the Go resolver

## Problem Statement

Links references to definitions per layer; the engine every language family plugs into.

**Working on this concurrently.** This story is in wave 6 of plan 0012-cgi. Start it when these are complete: `0105-it1` Indexer: apply files, tombstones and `hiddenBy`. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0107-nkl` Resolver for Python; `0108-qz5` Resolver for JavaScript and TypeScript; `0109-bzh` Resolver for Java and Kotlin; `0110-djd` Resolver for C# (csharp); `0111-ymw` Resolver for C, C++ and Objective-C; `0112-5ga` Resolver for Swift; `0113-hpg` Resolver for Rust; `0114-opk` Resolver for Dart; `0115-jq8` Resolver for PHP; `0116-u28` Resolver for Ruby; `0117-n53` Resolver for Perl; `0118-lga` Resolver for Lua; `0119-ddc` Resolver for OCaml; `0120-fq2` Resolver for Lisp dialects; `0121-85d` Resolver for SQL.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Engine

Scope order: same scope, file, package/module/namespace, then imports by alias/qualifier; confidence `exact`/`qualified`/`name`.

### 3: Overlays

Re-resolve references into changed definitions for the overlay's view, ≤ 5,000 per batch.

### 4: Go family

Packages by directory, `go.mod` module path, import aliases.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codeindex/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolve.go` - resolution engine, per-layer edges, confidence, dependents re-resolution, batch bound
- `mind-palace/codeindex/resolvers/registry.go` - resolver registry by family; each family registers from its own file
- `mind-palace/codeindex/resolvers/golang.go` - Go family
- `mind-palace/codeindex/resolve_test.go` - engine and Go tests

## Work Log
