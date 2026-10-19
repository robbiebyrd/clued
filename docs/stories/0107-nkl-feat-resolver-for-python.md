---
id: 0107-nkl
title: Resolver for Python
purpose: Resolves references for Python so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:10.732Z"
updated: "2026-10-19T21:01:15.311Z"
started: "2026-10-19T20:51:51.441Z"
completed: "2026-10-19T21:01:15.311Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-python
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/python.go
      - mind-palace/codeindex/resolvers/python_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0081-3cl", "depends"]
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

# Resolver for Python

## Problem Statement

Resolves references for Python so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0081-3cl` Extraction for Python. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

modules and packages by path, relative imports, `__init__.py`.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'python'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'python'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'python'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/python.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/python_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:01:09.943Z - Added codeindex/resolvers/python.go (python resolver registered for FamilyPython; exported PythonModuleKey) and python_test.go (5 tests: module keys, scope incl. relative/absolute/src roots/re-exports/stars/aliases, visibility, engine resolution over hand-built facts, engine resolution over codeparse/testdata/python goldens parsed with codeparse.Parse). Keys are module paths (a/b.py -> a/b, a/__init__.py -> a); imports located via View.Has under the importer's package root, project root and src/. Merged claude/sharp-gates-45zpak (0081 extraction) and aligned to real facts. Core notes: resolve.go drops files with neither symbols nor refs from the view, so import-only __init__.py modules are parsed from View.Content for their imports (bounded cache); dotted qualifiers through import a.b.c (a.b.c.f()) stay unresolved because the engine binds only the qualifier's first part and Python QualNames carry no module prefix. Verify: gofmt/vet/build clean, go test -race ./codeindex/... ok.
