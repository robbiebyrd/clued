---
id: 0088-pe1
title: Extraction for C++
purpose: 'Adds C++ to the indexer: files .cc, .cpp, .cxx, .hpp, .hh, .h.'
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:08:31.497Z"
updated: "2026-10-19T20:56:36.385Z"
started: "2026-10-19T20:45:27.039Z"
completed: "2026-10-19T20:56:36.385Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-cpp
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/cpp/
      - mind-palace/codeparse/testdata/cpp/
      - mind-palace/codeparse/grammars/cpp.go
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

# Extraction for C++

## Problem Statement

Adds C++ to the indexer: files .cc, .cpp, .cxx, .hpp, .hh, .h. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0111-ymw` Resolver for C, C++ and Objective-C.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture classes, structs, functions, methods, namespaces, templates, enums; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.cc, .cpp, .cxx, .hpp, .hh, .h), family, defining forms; scope rules for resolution later: `#include`, namespaces, `using`, out-of-line `A::f`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/cpp -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/cpp'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/cpp'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/cpp'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/cpp/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/cpp/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/cpp.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:56:30.182Z - Added codeparse/grammars/cpp.go (gotreesitter grammars/cpp), queries/cpp/{tags.scm,profile.yaml} (family cfamily, separators ::, packageBy none: namespaces are definitions that qualify by position; includes as include/include_system, using directives, using declarations, namespace aliases) and four goldens in testdata/cpp (geometry.hpp: every definition kind incl. namespaces, classes, structs, unions, enums and enumerators, typedef/using aliases, templates, methods, fields, constants, variables, macros; geometry.cpp: imports, out-of-line A::f, calls, type and extends refs; quirks.h: #ifdef branches, extern C, nested/inline namespaces, specializations, member templates, deep A::B::~B; broken.cc: syntax error). TestGolden/cpp: 4 inputs pass; go test -race ./codeparse/... ok; gofmt/vet/build clean. Engine gap, not worked around (extract.go is shared): a method defined out of line inside a namespace block (namespace geometry { double Circle::area() ... }) gets the namespace as parent and QualName geometry::area, its @receiver ignored, because resolveDefs applies a receiver only to a definition with no container; fix: qualify as qual(container)+sep+receiver+sep+name when the receiver's type is not found. Only the innermost scope is the receiver (outer::inner::Packet::Header::~Header gives Header::~Header). gotreesitter quirk kept in quirks.h: Grid<outer::inner::Packet> (qualified template argument) parses as a comparison and yields no type refs. Template parameters (T) appear as type refs.
