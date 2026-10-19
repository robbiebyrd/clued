---
id: 0109-bzh
title: Resolver for Java and Kotlin
purpose: Resolves references for Java and Kotlin so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:22.516Z"
updated: "2026-10-19T21:06:52.951Z"
started: "2026-10-19T20:59:18.190Z"
completed: "2026-10-19T21:06:52.951Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-jvm
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/jvm.go
      - mind-palace/codeindex/resolvers/jvm_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0084-ouo", "depends"]
    - ["0091-fcm", "depends"]
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

# Resolver for Java and Kotlin

## Problem Statement

Resolves references for Java and Kotlin so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0084-ouo` Extraction for Java; `0091-fcm` Extraction for Kotlin. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

packages, single and on-demand imports, same-package visibility, extension receivers.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'jvm'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'jvm'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'jvm'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/jvm.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/jvm_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:06:46.389Z - Added codeindex/resolvers/jvm.go (jvm family: Java and Kotlin share declared packages; single imports bind the local name as qualifier of the owning package (longest prefix that is a view package) and as a bare name when unaliased and top-level; on-demand/Kotlin wildcard and implicit imports (profile implicitImports / defaultImports, else JVMImplicitImports) open packages; static imports and module-info directives bind no package; Visible: Kotlin private file-local, explicit imports shadow package/open/name steps, import step only reaches explicitly imported definitions, Kotlin extensions from another package need an import) and jvm_test.go (4 tests: end-to-end through codeindex with the real extractor over the java/ and kotlin/ goldens and small rule sources, Scope and Visible units). go test -race ./codeindex/... passes. Known gap in the core, not changed: the engine's member steps exclude top-level definitions, so a Kotlin extension called through a value receiver (s.shout()) stays unresolved; bare calls and type-qualified calls resolve. Only jvm.go and jvm_test.go changed (plus merges of claude/sharp-gates-45zpak for 0084 and 0091).
