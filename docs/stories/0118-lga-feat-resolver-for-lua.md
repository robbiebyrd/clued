---
id: 0118-lga
title: Resolver for Lua
purpose: Resolves references for Lua so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:14.667Z"
updated: "2026-10-19T21:02:28.964Z"
started: "2026-10-19T20:57:58.087Z"
completed: "2026-10-19T21:02:28.964Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-lua
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/lua.go
      - mind-palace/codeindex/resolvers/lua_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0098-cjj", "depends"]
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

# Resolver for Lua

## Problem Statement

Resolves references for Lua so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0098-cjj` Extraction for Lua. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`require` by module path, module tables.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'lua'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'lua'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'lua'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/lua.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/lua_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:02:19.432Z - Added codeindex/resolvers/lua.go (registered for family lua; exported LuaModuleKey, LuaRequire, LuaExt): module key = path without .lua; require("a.b") finds a/b.lua or a/b/init.lua at the root, then under src/lua/lib, then from the requiring file's directory upward (first held wins; otherwise the spec's path, which stays unresolved); local m = require binds m as qualifier, require("m").f binds the name f, a require whose value is dropped and dofile/loadfile open the module. Visible: same file always; through a require any top-level definition or exported member; bare names (open, name fallback) only globals (exported, not declared local); member fallback only exported members of an exported table. Rules: separators . and :, name and member fallbacks. lua_test.go (package resolvers_test): 5 tests (registration/profile, module keys and require search incl. refused specs, scope, visibility over parsed goldens, and an end-to-end run of codeindex with codeparse.Parse over all five testdata/lua inputs plus hand-written modules), all pass; go test -race ./codeindex/... passes; gofmt/vet/build clean. Engine limit: a member of a module's returned table reached as m.f resolves qualified when the module's table is named like the alias, else by name (StepImport only accepts top-level definitions). Only the two listed files changed (relative to the merged extraction branch).
