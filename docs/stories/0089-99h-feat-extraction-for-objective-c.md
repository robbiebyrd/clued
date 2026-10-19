---
id: 0089-99h
title: Extraction for Objective-C
purpose: 'Adds Objective-C to the indexer: files .m, .mm, .h.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:36.538Z"
updated: "2026-10-19T20:58:14.221Z"
started: "2026-10-19T20:46:50.663Z"
completed: "2026-10-19T20:58:14.221Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-objc
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/objc/
      - mind-palace/codeparse/testdata/objc/
      - mind-palace/codeparse/grammars/objc.go
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

# Extraction for Objective-C

## Problem Statement

Adds Objective-C to the indexer: files .m, .mm, .h. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0111-ymw` Resolver for C, C++ and Objective-C.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture `@interface`, `@implementation`, `@protocol`, selectors, categories, C functions; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.m, .mm, .h), family, defining forms; scope rules for resolution later: `#import`/`#include`, `@import`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/objc -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/objc'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/objc'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/objc'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/objc/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/objc/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/objc.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:58:04.115Z - Objective-C extraction: grammars/objc.go registers gotreesitter's objc grammar; queries/objc/profile.yaml (cfamily, .m/.mm/.h, packageBy none, separator '.', kind category, options includeKinds/moduleKinds for the resolver) and tags.scm: classes (@interface with superclass or root), class extensions (kind extension), categories (kind category, member of their class via @receiver), @implementation blocks (kind impl; category implementations under their class), protocols, methods (declarations and definitions, named by the selector's first keyword; the full selector is in the signature, so tableView:numberOfRowsInSection: and tableView:cellForRowAtIndexPath: are tableView and tableView~2), properties, instance variables, C functions and prototypes, structs, unions, enums (C and the three shapes the grammar gives NS_ENUM/NS_OPTIONS), typedefs, fields, enumerators, macros, file-level constants and variables; imports #import/#include (kinds import, import_system, include, include_system) and @import (kind module, submodules as names); references: message sends (first keyword, receiver as qualifier), C calls, superclass and protocol-list extends/implements, @class/@protocol forward declarations, @protocol(), method and C types, property accesses, @synthesize/@dynamic. Each declarator shape is its own pattern (the alternation @name bug). Goldens: Shapes.h, Shape.m, AppDelegate.m, Bridge.mm (Objective-C++, C++ parts parse with errors), broken.m. gofmt/vet/build clean; go test -race ./codeparse/... ok (TestGolden/objc: 5 inputs pass). Only the three listed paths changed. Engine note: a symbol name is one node, so a multi-keyword selector cannot be its name; joining repeated @name captures (with ':') would give full selectors.
