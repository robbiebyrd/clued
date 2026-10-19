---
id: 0110-djd
title: Resolver for C# (csharp)
purpose: Resolves references for C# (csharp) so calls, references and implementations link to the right definitions.
type: feat
status: ready
priority: "3"
effort: M
created: "2026-10-19T18:10:28.294Z"
updated: "2026-10-19T18:32:06.841Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-csharp
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/csharp.go
      - mind-palace/codeindex/resolvers/csharp_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0085-134", "depends"]
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

# Resolver for C# (csharp)

## Problem Statement

Resolves references for C# (csharp) so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0085-134` Extraction for C# (csharp). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

namespaces, `using` and aliases, partial classes.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'csharp'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'csharp'
- [ ] [MANUAL] Only this family's two files changed
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'csharp'
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/csharp.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/csharp_test.go` - resolution tests over the language goldens

## Work Log
