---
id: 0087-rzn
title: Extraction for C
purpose: 'Adds C to the indexer: files .c, .h.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:26.017Z"
updated: "2026-10-19T20:53:46.464Z"
started: "2026-10-19T20:44:56.208Z"
completed: "2026-10-19T20:53:46.464Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-c
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/c/
      - mind-palace/codeparse/testdata/c/
      - mind-palace/codeparse/grammars/c.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0080-6sk", "depends"]
  plans:
    - ["0012-cgi", "included", ["1.2"]]
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
  "6":
    status: complete
---

# Extraction for C

## Problem Statement

Adds C to the indexer: files .c, .h. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0111-ymw` Resolver for C, C++ and Objective-C.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture functions, structs, unions, enums, typedefs, macros, globals; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.c, .h), family, defining forms; scope rules for resolution later: `#include`, header/source pairing.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/c -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/c'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/c'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/c'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/c/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/c/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/c.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:53:40.914Z - Added C: grammars/c.go (gotreesitter grammars/c), queries/c/profile.yaml (family cfamily, .c/.h, packageBy none, exported: declared, separators [.], options for include kinds and header/source pairing by basename) and queries/c/tags.scm: functions (definitions incl. pointer-returning and function-pointer-returning ones), prototypes and extern globals outside function bodies (a prototype and its definition give f and f~2), globals (plain, pointer, array, function-pointer, initialised), structs/unions/enums with a body, enumerators (const), fields (incl. bit-fields, function pointers, anonymous nested unions), typedefs (a typedef of a tagged struct/union/enum with a body is its declarator only, so the tag stays top level; anonymous struct members nest under the typedef), object- and function-like macros; static functions/globals are not exported (regex on the specifiers, skipping __attribute__((...))). Imports: #include "x" (include), <x> (include_system), #include MACRO (include_macro). Refs: calls (bare and through struct members with qualifier), type_identifier types, and upper-case identifiers as value refs (macro constants/enumerators, incl. #ifdef/defined()). No inheritance in C. Goldens: list.h, registry.c, generated.c (X-macros, generated getters, #if/#elif/#else branches, K&R, attributes, nested local types), broken.c (error node, symbols after it kept). go test -race ./codeparse/ -run TestGolden/c: 4/4 pass; gofmt/vet/build clean; go test -race ./codeparse/... passes (TestGolden/go/large.go hits the 2 s parse cut-off once under load average ~47, passes on rerun; unrelated to C). Only files under the story's three paths changed.
