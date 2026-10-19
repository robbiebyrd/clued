---
id: 0111-ymw
title: Resolver for C, C++ and Objective-C
purpose: Resolves references for C, C++ and Objective-C so calls, references and implementations link to the right definitions.
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:10:33.998Z"
updated: "2026-10-19T18:10:33.998Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
  "5":
    status: pending
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

- [ ] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'cfamily'
- [ ] [MANUAL] Only this family's two files changed
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'cfamily'
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/cfamily.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/cfamily_test.go` - resolution tests over the language goldens

## Work Log
