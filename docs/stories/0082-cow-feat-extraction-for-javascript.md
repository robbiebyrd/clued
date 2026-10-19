---
id: 0082-cow
title: Extraction for JavaScript
purpose: 'Adds JavaScript to the indexer: files .js, .mjs, .cjs, .jsx.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:07:26.246Z"
updated: "2026-10-19T20:53:11.443Z"
started: "2026-10-19T20:44:55.233Z"
completed: "2026-10-19T20:53:11.434Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-javascript
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/javascript/
      - mind-palace/codeparse/testdata/javascript/
      - mind-palace/codeparse/grammars/javascript.go
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

# Extraction for JavaScript

## Problem Statement

Adds JavaScript to the indexer: files .js, .mjs, .cjs, .jsx. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0108-qz5` Resolver for JavaScript and TypeScript.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture functions, classes, methods, exported bindings; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.js, .mjs, .cjs, .jsx), family, defining forms; scope rules for resolution later: ES `import`/`export`, CommonJS `require`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/javascript -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/javascript'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/javascript'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/javascript'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/javascript/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/javascript/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/javascript.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:53:05.743Z - JavaScript extraction: grammars/javascript.go registers gotreesitter's javascript grammar (JSX included; no go.mod change). queries/javascript/tags.scm captures function/generator declarations, top-level arrow/function-expression bindings, classes (declarations and class expressions bound to a name), methods (incl. get/set, static, #private, computed, constructor, function-valued fields), fields, top-level const/let/var (require bindings excluded), Foo.prototype.m methods (@receiver), CommonJS exports.x / module.exports.x / module.exports = {...} functions and constants; @exported for ES export and CommonJS exports (profile scope.exported: declared, packageBy: file, family jsts, options moduleSystems/resolveExtensions/indexFile). Imports: ES import (default and namespace as alias, named as names), export...from (kind export), require (kind require, binding as alias, destructured keys as names), import() (kind dynamic, string specs only). References: calls (bare and member with qualifier; require and builtin conversions excluded), new C / new ns.C (type), instanceof (type), extends (identifier and member), capitalized JSX elements and member JSX elements (call). Goldens: shapes.mjs, store.cjs, App.jsx, reexports.js, widget.umd.js (generated UMD/minified), broken.js (syntax error) - reviewed by hand. go test -race ./codeparse/... ok; TestGolden/javascript 6/6 inputs pass; gofmt/vet/build clean. Known limits (documented in tags.scm): class members carry no exported flag; names exported later by export { a } or module.exports = { a } are not marked. Only the three listed paths changed.
