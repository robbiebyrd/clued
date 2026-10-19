---
id: 0092-y8g
title: Extraction for Dart (and Flutter)
purpose: 'Adds Dart (and Flutter) to the indexer: files .dart.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:52.472Z"
updated: "2026-10-19T21:15:09.729Z"
started: "2026-10-19T20:46:54.950Z"
completed: "2026-10-19T21:15:09.729Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-dart
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/dart/
      - mind-palace/codeparse/testdata/dart/
      - mind-palace/codeparse/grammars/dart.go
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

# Extraction for Dart (and Flutter)

## Problem Statement

Adds Dart (and Flutter) to the indexer: files .dart. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0114-opk` Resolver for Dart.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture classes, mixins, extensions, enums, functions, methods; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.dart), family, defining forms; scope rules for resolution later: `import`/`export`/`part`, `package:` via `pubspec.yaml`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/dart -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/dart'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/dart'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/dart'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/dart/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/dart/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/dart.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T21:15:04.187Z - Added codeparse/grammars/dart.go (gotreesitter grammars/dart), queries/dart/tags.scm and profile.yaml (family dart, packageBy file, exported noUnderscore, options for package: URIs via pubspec.yaml), and 7 goldens under testdata/dart (shapes: every definition kind; directives: import/export/part with prefixes, show, deferred, conditional URIs; counter_app: Flutter widgets; user.dart + user.g.dart: json_serializable part/part of and generated code; main: definitions at the start of a file; broken: a syntax error with definitions kept on both sides). TestGolden/dart: 7/7 pass under -race; gofmt, go vet ./..., go build ./... clean; go test -race ./codeparse/... passes. Design: a function/method/constructor with a body is captured on its function_body (the grammar has no node around signature and body), so ranges, nested local functions, enclosing refs and chunks hold the body; @name/@signature come from the signature sibling. Deviations and limits: docs are the extractor default only, so members with annotations (@override) and functions whose signature spans several lines get no doc; capturing doc+annotation siblings needs 3+-sibling quantified patterns, which gotreesitter v0.56.0 matches in quadratic time (it collects every later candidate before checking an anchor; measured 18 s on a 180 KB file) and drops past its 1M work budget. Every definition pattern therefore starts at a named parent and spans at most two siblings (query is ~0.2 s on a 90 KB file). Members of an unnamed extension have no parent. Top-level final and const are both constants. Only files under the story's three paths changed.
