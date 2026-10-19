---
id: 0098-cjj
title: Extraction for Lua
purpose: 'Adds Lua to the indexer: files .lua.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:09:23.454Z"
updated: "2026-10-19T20:53:52.636Z"
started: "2026-10-19T20:46:48.071Z"
completed: "2026-10-19T20:53:52.634Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-lua
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/lua/
      - mind-palace/codeparse/testdata/lua/
      - mind-palace/codeparse/grammars/lua.go
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

# Extraction for Lua

## Problem Statement

Adds Lua to the indexer: files .lua. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0118-lga` Resolver for Lua.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture functions, local functions, table-field functions (`M.f`, `M:f`); add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.lua), family, defining forms; scope rules for resolution later: `require`, module tables.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/lua -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/lua'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/lua'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/lua'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/lua/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/lua/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/lua.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:53:47.232Z - Added Lua: grammars/lua.go (gotreesitter grammars/lua), queries/lua/profile.yaml (family lua, .lua, separators . and :, packageBy file, exported declared) and tags.scm: global/local functions, M.f and M:m (receiver = table), f = function / local f = function / M.f = function, functions in table constructors, top-level module tables (local M = {} exported when the file returns M) and setmetatable-made class tables, module-table fields; imports require "x" / require("x") / local m = require("x") (alias) / require("x").f (name) / pcall(require, "x") (kind require), dofile/loadfile (kind include); refs: calls (bare, m.f, obj:m, stdlib globals excluded), extends from setmetatable(_, { __index = Base }), type from setmetatable(o, Class). Goldens: module, imports, class, nested (deep paths, closures, long strings, goto, generated handlers) and broken (syntax error) .lua; go test -race ./codeparse/... ok, TestGolden/lua 5 inputs pass; gofmt/vet/build clean. No go.mod change. Quirk: an alternation around the two table-constructor field patterns gave every field the first field's @name under gotreesitter v0.56.0, so they are separate patterns. Only the three listed paths changed.
