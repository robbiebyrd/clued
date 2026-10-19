---
id: 0119-ddc
title: Resolver for OCaml
purpose: Resolves references for OCaml so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:20.133Z"
updated: "2026-10-19T21:07:01.891Z"
started: "2026-10-19T20:58:48.336Z"
completed: "2026-10-19T21:07:01.891Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-ocaml
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/ocaml.go
      - mind-palace/codeindex/resolvers/ocaml_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0097-8oc", "depends"]
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

# Resolver for OCaml

## Problem Statement

Resolves references for OCaml so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0097-8oc` Extraction for OCaml. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`open`, `Module.name`, `.mli`/`.ml` pairing, dune library names.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'ocaml'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'ocaml'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'ocaml'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/ocaml.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/ocaml_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:06:55.521Z - Added codeindex/resolvers/ocaml.go (ocaml family: module per file keyed Lib.Module for a wrapped dune library, Module unwrapped, dir/Module otherwise, include_subdirs and (modules ...) honoured; .mli/.ml pairing with the interface keyed <key>#mli and visible only from itself when its .ml is visible; Module.name bound to own library, same directory, opened library, a library's main module, then unwrapped/root modules; open/include/local_open as Open; module aliases bound like their target; functor parameters and local modules left to the engine; modules outside the project bound to an empty package) and ocaml_test.go (6 tests: package keys, profile options, scope over the store.ml golden, visibility, end-to-end through codeindex with codeparse.Parse over shapes.ml/.mli, store.ml, widgets.ml goldens plus a small dune project). go test -race ./codeindex/resolvers/ -run ocaml: 6 pass; ./codeindex/... pass (TestRebuildKeepsItsLeaseAlive flaked once under load with a short lease, passes on rerun; unrelated). Only the two listed files changed. Limitation: a path through a wrapped library's name (Geo.Shapes.x without open Geo) stays unresolved: the engine binds only a qualifier's first part and matches the rest against QualName, which carries no file-module name.
