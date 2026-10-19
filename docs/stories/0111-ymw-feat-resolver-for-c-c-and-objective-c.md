---
id: 0111-ymw
title: Resolver for C, C++ and Objective-C
purpose: Resolves references for C, C++ and Objective-C so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:33.998Z"
updated: "2026-10-19T21:16:07.619Z"
started: "2026-10-19T20:57:38.649Z"
completed: "2026-10-19T21:14:26.624Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-cfamily
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/cfamily.go
      - mind-palace/codeindex/resolvers/cfamily_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0087-rzn", "depends"]
    - ["0088-pe1", "depends"]
    - ["0089-99h", "depends"]
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

# Resolver for C, C++ and Objective-C

## Problem Statement

Resolves references for C, C++ and Objective-C so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0087-rzn` Extraction for C; `0088-pe1` Extraction for C++; `0089-99h` Extraction for Objective-C. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`#include`/`#import` by include directories from `CMakeLists.txt`/`compile_commands.json` or relative path; C++ namespaces and `using`; out-of-line methods; Objective-C selectors.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'cfamily'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'cfamily'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'cfamily'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/cfamily.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/cfamily_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:14:19.249Z - Added mind-palace/codeindex/resolvers/cfamily.go (the cfamily resolver, registered from init) and cfamily_test.go (8 tests, all pass under -race: C goldens list.h/registry.c end to end through codeindex.NewResolver with CMake include dirs, decoy headers, pairing, statics and prototypes; hand-built C++ facts shaped like story 0088's geometry goldens: namespaces, using, std:: and a std namespace alias, out-of-line A::f; hand-built Objective-C facts shaped like 0089's: #import, @import, selector calls, class-extension declarations; include resolution via compile_commands.json and CMakeLists.txt; pairing; CMake parsing; effective qualified names). Package key is the base name without extension so Scope can look files up by name through View.Package; Visible scopes by the include closure and pair it keeps per *File (weak-keyed, dropped when the File is collected). Core limitations, not changed: StepImport/StepOpen accept only top-level definitions, so C enumerators and C++ namespace members reached through an include resolve only by the name fallback (confidence name); namespace aliases of project namespaces are not expanded (Visible does not see the reference). Only the two Files changed (merge of claude/sharp-gates-45zpak brought 0087's C extraction as instructed). Verify: gofmt, go vet ./..., go build ./..., go test -race ./codeindex/... pass.

### 2026-10-19T21:16:07.619Z - Coordinator request: prefixed every top-level test helper with cfamily, merged claude/sharp-gates-45zpak again (other families' resolvers), gofmt/vet ./.../build ./... and go test -race ./codeindex/... pass with no clash.
