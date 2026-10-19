---
id: 0085-134
title: Extraction for C# (csharp)
purpose: 'Adds C# (csharp) to the indexer: files .cs.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:15.288Z"
updated: "2026-10-19T20:53:27.504Z"
started: "2026-10-19T20:44:36.345Z"
completed: "2026-10-19T20:53:27.504Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-csharp
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/csharp/
      - mind-palace/codeparse/testdata/csharp/
      - mind-palace/codeparse/grammars/csharp.go
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

# Extraction for C# (csharp)

## Problem Statement

Adds C# (csharp) to the indexer: files .cs. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0110-djd` Resolver for C# (csharp).

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture classes, structs, records, interfaces, enums, methods, properties; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.cs), family, defining forms; scope rules for resolution later: namespaces, `using`, partial classes.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/csharp -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/csharp'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/csharp'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/csharp'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/csharp/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/csharp/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/csharp.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:53:22.556Z - Added C#: grammars/csharp.go registers gotreesitter's c_sharp grammar as "c_sharp"; queries/csharp/profile.yaml (family csharp, .cs, packageBy declaration, exported declared, partialTypes: merge option) and tags.scm (classes, structs, records incl. record struct and positional-record properties, interfaces and their members, enums and members as constants, delegates as types, methods, constructors, destructors, operators, indexers, properties, events, fields, const fields; using / using static / alias / global using imports; block and file-scoped namespace as package; calls incl. ?. and generic calls, type refs at every type:/returns: field, type arguments, constraints, attributes; extends/implements from base lists with the IName heuristic). Type declaration names are matched without their field so declarations recovered around a syntax error keep their names. Five goldens (Shapes, Usings, Orders, Form1.Designer generated/partial/#if/#region/raw strings, Broken); TestGolden/csharp 5/5 pass, go test -race ./codeparse/... ok, gofmt/vet/build clean. Known limits: a nested namespace does not extend the package; gotreesitter drops the broken method and a following enum in Broken.cs. Only the story's three paths changed.
