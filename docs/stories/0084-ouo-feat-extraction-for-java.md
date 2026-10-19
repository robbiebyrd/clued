---
id: 0084-ouo
title: Extraction for Java
purpose: 'Adds Java to the indexer: files .java.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:07:36.735Z"
updated: "2026-10-19T20:53:51.543Z"
started: "2026-10-19T20:44:52.930Z"
completed: "2026-10-19T20:53:48.342Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-java
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/java/
      - mind-palace/codeparse/testdata/java/
      - mind-palace/codeparse/grammars/java.go
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

# Extraction for Java

## Problem Statement

Adds Java to the indexer: files .java. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0109-bzh` Resolver for Java and Kotlin.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture classes, interfaces, enums, records, methods, fields; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.java), family, defining forms; scope rules for resolution later: `package`, `import`, `extends`/`implements`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/java -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/java'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/java'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/java'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/java/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/java/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/java.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:53:51.543Z - Added Java: grammars/java.go (gotreesitter grammars/java), queries/java/profile.yaml (family jvm, packageBy declaration, exported declared, options implicitImports java.lang and the import kinds) and queries/java/tags.scm (classes, interfaces, enums, records, annotation types (kind annotation), module-info modules; methods, constructors (kind constructor, compact ones too), fields, enum and interface constants, record components; imports single/on_demand/static/static_on_demand with the simple name, module requires; refs: calls, object creation, method references, types, extends/implements, annotations, field accesses). Lower-case type names and package-path field accesses are left out (Java naming convention) so java.io.File gives no 'java'/'io' refs. Goldens: Inventory.java, OrderService.java, ShapeProto.java (generated, nested/local types, sealed), module-info.java, Broken.java.in; TestGolden/java 5/5 pass with -race; gofmt, vet, build clean. No shared file touched. Note: TestGolden/go/large.go fails under -race only while the host is overloaded (load ~50 on 4 CPUs; the 2 s parse cut-off), unrelated to Java; passes without -race.
