---
id: 0125-067
title: Evaluation harness and task set
purpose: Measures tokens, turns and success with and without the tools; built in parallel with everything else.
type: feat
status: ready
priority: "3"
effort: M
created: "2026-10-19T18:11:53.687Z"
updated: "2026-10-19T18:33:43.800Z"
tags:
  - plan-0012-cgi
  - wave-1
  - tests
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/eval/codesearch/tasks.yaml
      - mind-palace/eval/codesearch/corpus.yaml
      - mind-palace/eval/codesearch/harness.go
      - mind-palace/eval/codesearch/score.go
      - mind-palace/eval/codesearch/harness_test.go
      - scripts/eval-codesearch.sh
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  plans:
    - ["0012-cgi", "included", ["5.1"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
---

# Evaluation harness and task set

## Problem Statement

Measures tokens, turns and success with and without the tools; built in parallel with everything else.

**Working on this concurrently.** This story is in wave 1 of plan 0012-cgi. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 5.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Tasks

About 40 on clued and clued-cloud plus about 3 per language, across named, concept, impact and edit families.

### 3: Runner

Headless `claude -p` sessions per arm; tokens and turns from clued's capture (`usage`, `get_activities`); three runs per task.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./eval/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./eval/...
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./eval/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/eval/codesearch/tasks.yaml` - tasks with ground truth
- `mind-palace/eval/codesearch/corpus.yaml` - pinned public repositories per language
- `mind-palace/eval/codesearch/harness.go` - runner for the arms
- `mind-palace/eval/codesearch/score.go` - scoring
- `mind-palace/eval/codesearch/harness_test.go` - loader and scorer tests
- `scripts/eval-codesearch.sh` - manual run, `CLUED_EVAL=1`

## Work Log
