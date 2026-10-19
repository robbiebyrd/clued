---
id: 0081-3cl
title: Extraction for Python
purpose: 'Adds Python to the indexer: files .py, .pyi.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:07:21.431Z"
updated: "2026-10-19T20:54:18.460Z"
started: "2026-10-19T20:44:55.258Z"
completed: "2026-10-19T20:54:18.460Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-python
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/python/
      - mind-palace/codeparse/testdata/python/
      - mind-palace/codeparse/grammars/python.go
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

# Extraction for Python

## Problem Statement

Adds Python to the indexer: files .py, .pyi. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0107-nkl` Resolver for Python.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture functions, classes, methods, module variables; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.py, .pyi), family, defining forms; scope rules for resolution later: `import` / `from … import`, modules and packages by path.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/python -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/python'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/python'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/python'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/python/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/python/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/python.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:54:12.332Z - Added grammars/python.go (gotreesitter grammars/python, no go.mod change), queries/python/{profile.yaml,tags.scm} (family python, packageBy file, exported noUnderscore) and 6 goldens under testdata/python (models.py, imports.py, service.py, nested.py, stubs.pyi, broken.py); TestGolden/python: 6/6 pass, go test -race ./codeparse/... ok, gofmt/vet/build clean. Captures: functions (module, nested, conditional, decorated), methods (defs directly in a class body), classes, class attributes as fields, module variables and UPPER_CASE constants (also under module-level if/try, tuple targets), PEP 695 type aliases, docstrings via (block . (string)), every import form (import/as, from/names, relative, aliased names as their own import, wildcard as name *, __future__; kind import|from), call refs (builtins, self, cls left out), decorator calls, extends (bases incl. qualified and subscripted), type refs (annotations, except clauses). Deviations: no @package (a Python file is its own module); a decorated definition's node is the def/class itself so its decorators fall into a window chunk; defs under an if in a class body are functions, not methods; gotreesitter quirk: an alternation over nodes with repeated children yields one match with all captures, so those forms are separate patterns. Only the story's three paths changed.
