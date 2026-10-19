---
id: 0078-k15
title: 'Parser spike: gotreesitter vs tree-sitter WebAssembly under wazero'
purpose: Decides the pure-Go tree-sitter runtime every language story uses.
type: task
status: pending
priority: "3"
effort: M
created: "2026-10-19T18:07:07.216Z"
updated: "2026-10-19T18:07:07.216Z"
tags:
  - plan-0012-cgi
  - wave-1
  - spike
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/spike_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  plans:
    - ["0012-cgi", "included", ["1.1"]]
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

# Parser spike: gotreesitter vs tree-sitter WebAssembly under wazero

## Problem Statement

Decides the pure-Go tree-sitter runtime every language story uses.

**Working on this concurrently.** This story is in wave 1 of plan 0012-cgi. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Build both

`CGO_ENABLED=0` for the 7 targets; `go-licenses check`.

### 3: Measure

Grammar availability and query support for all 21 languages, correctness on samples, files/s, memory, binary size, on clued, clued-cloud and one public repository per language.

### 4: Decide and record

Append the numbers and the choice to plan 0012's work log and this story's; list grammars that must be added to the runtime.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] [MANUAL] The choice and its numbers are recorded in the work log
- [ ] [MANUAL] `spike_test.go` is deleted
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/spike_test.go` - temporary; deleted at the end

## Work Log
