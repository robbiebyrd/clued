---
id: 0112-5ga
title: Resolver for Swift
purpose: Resolves references for Swift so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:40.665Z"
updated: "2026-10-19T21:09:42.469Z"
started: "2026-10-19T20:59:25.416Z"
completed: "2026-10-19T21:09:42.469Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-swift
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/swift.go
      - mind-palace/codeindex/resolvers/swift_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0090-aau", "depends"]
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

# Resolver for Swift

## Problem Statement

Resolves references for Swift so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0090-aau` Extraction for Swift. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

modules, extensions merged into their type, protocol conformance.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'swift'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'swift'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'swift'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/swift.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/swift_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:09:36.061Z - Added codeindex/resolvers/swift.go (family swift, registered from init): modules are build targets (the directory below a targetRoots entry, Sources/Tests by default, of the nearest visible Package.swift; else the first target root on the path; else the top directory, as an Xcode project's target folder; '.' at the root; profile option module other than target falls back to Default.Package). Scope: import M opens M and binds M as a qualifier (submodule specs bind their top module), import struct M.T imports the name T, the file's own module qualifies its definitions, @_exported import re-exports are followed transitively through the project's modules. Visible: an extension is never a target (references to Point reach the type, extensions' members merge through their qualified names), same module sees all, other modules need public/open or a @testable import, the name fallback stays within the own and imported modules. Rules: '.', name and member fallbacks. Tests (swift_test.go, 4 tests): module rule, scope over the Imports.swift golden, visibility over Shapes.swift, and an end-to-end index of the Shapes/Builders/ShapeStore goldens plus hand-written files through codeindex with the real Swift extraction (exact/qualified/name confidences, extensions, conformance, re-exports, testable, Xcode layout). go test -race ./codeindex/... passes. Core gaps, not fixed here (resolve.go is outside this story): (1) the engine takes the edge type from the ref kind, so the extractor's extends for a class whose first inherited name is a protocol cannot be corrected to implements by the resolver; (2) the engine drops facts with no symbols and no refs, so a file of @_exported imports alone is invisible to View.Package and its re-exports are not followed.
