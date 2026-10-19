---
id: 0125-067
title: Evaluation harness and task set
purpose: Measures tokens, turns and success with and without the tools; built in parallel with everything else.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:53.687Z"
updated: "2026-10-19T19:20:21.665Z"
started: "2026-10-19T18:57:30.629Z"
completed: "2026-10-19T19:20:12.649Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./eval/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./eval/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/eval/codesearch/tasks.yaml` - tasks with ground truth
- `mind-palace/eval/codesearch/corpus.yaml` - pinned public repositories per language
- `mind-palace/eval/codesearch/harness.go` - runner for the arms
- `mind-palace/eval/codesearch/score.go` - scoring
- `mind-palace/eval/codesearch/harness_test.go` - loader and scorer tests
- `scripts/eval-codesearch.sh` - manual run, `CLUED_EVAL=1`

## Work Log

### 2026-10-19T19:20:07.499Z - Built mind-palace/eval/codesearch: tasks.yaml (101 tasks: 42 on clued and clued-cloud — 29 clued, 12 clued-cloud... across named/concept/impact/edit, 5 edits with check commands; 59 on the corpus, 3 per corpus language, 5 for Lisp across Clojure, Common Lisp and Emacs Lisp), corpus.yaml (21 public repositories pinned to full SHAs resolved with git ls-remote on 2026-10-19), harness.go (loader with strict YAML, Validate, arms grep/graph/context with --allowedTools/--disallowedTools, ClaudeCLI over claude -p --output-format json, CluedCLI reading session usage and session activities, WaitMeasure, Sources.Checkout as shared clones at HEAD or the pinned commit with origin kept, Execute: runs interleaved by arm, JSONL per run), score.go (file and symbol recall with min_recall, edit scoring by changed files plus check, Summarize: per-task medians then medians per family, language and overall, Markdown with token change vs grep). harness_test.go: 17 tests (loader, validator, scorer, report, parsers, CLI args, Execute against a temp git repo with fake agent and capture, ground truth of clued on every run, of clued-cloud with CLUED_EVAL_CLOUD_DIR (passes against /home/user/clued-cloud), of the corpus with CLUED_EVAL_VERIFY=1 (passes: every pinned commit fetched, every truth file and symbol found), TestEvalLive gated by CLUED_EVAL=1). scripts/eval-codesearch.sh runs the evaluation (exit 3 without credentials, checked) or --verify-only (checked). No live claude -p run here: no Claude credentials in this container; the run itself is story 0126-j2n. Only the six listed files changed; no new dependency.

### 2026-10-19T19:20:21.665Z - Correction to the entry above: the 42 own tasks are 29 on clued and 13 on clued-cloud.
