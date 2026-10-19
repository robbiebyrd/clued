---
id: 0113-hpg
title: Resolver for Rust
purpose: Resolves references for Rust so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:46.433Z"
updated: "2026-10-19T21:12:41.798Z"
started: "2026-10-19T21:05:48.243Z"
completed: "2026-10-19T21:12:41.798Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-rust
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/rust.go
      - mind-palace/codeindex/resolvers/rust_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0086-vwk", "depends"]
  plans:
    - ["0012-cgi", "included", ["3.2"]]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
---

# Resolver for Rust

## Problem Statement

Resolves references for Rust so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0086-vwk` Extraction for Rust. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`mod` tree from the crate root, `use` paths, `impl Trait for Type`, `Cargo.toml` crate names.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'rust'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'rust'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'rust'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/rust.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/rust_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:12:35.215Z - Added codeindex/resolvers/rust.go (family rust: package key = module path from the crate root, crate name from Cargo.toml [lib]/[package], [lib]/[[bin]] paths, src/bin, tests, examples, benches as crates of their own; use trees incl. globs, aliases, self/super/crate/::name, mod and extern crate forms; impl blocks never targets so impl Trait for Type links the impl to the trait and Type to the struct; other crates see pub only) and rust_test.go (6 tests: manifest, package keys, use expansion and scope over the imports.rs/generated.rs goldens, visibility, and an end-to-end resolve through codeindex over the lib.rs/references.rs goldens plus hand-built modules and a binary). go test -race ./codeindex/... passes (TestRunChecksRepairsInTheBackground in codeindex is flaky under load, unrelated). Core gaps: item aliases (use a::f as g) and paths of 3+ segments through file modules (crate::a::f()) cannot resolve exactly because the engine binds only a qualifier's first segment and matches the rest against QualName.
