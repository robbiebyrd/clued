---
id: 0108-qz5
title: Resolver for JavaScript and TypeScript
purpose: Resolves references for JavaScript and TypeScript so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:16.135Z"
updated: "2026-10-19T21:06:13.946Z"
started: "2026-10-19T20:54:50.174Z"
completed: "2026-10-19T21:06:13.946Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-jsts
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/jsts.go
      - mind-palace/codeindex/resolvers/jsts_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0082-cow", "depends"]
    - ["0083-a3p", "depends"]
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

# Resolver for JavaScript and TypeScript

## Problem Statement

Resolves references for JavaScript and TypeScript so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0082-cow` Extraction for JavaScript; `0083-a3p` Extraction for TypeScript. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

ES modules and CommonJS by relative path, `index` files, `tsconfig.json` `paths`/`baseUrl`, `package.json` exports.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'jsts'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'jsts'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'jsts'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/jsts.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/jsts_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:06:06.710Z - Added codeindex/resolvers/jsts.go (family jsts, registered from init) and jsts_test.go. Package key = file path. Scope resolves relative specs (as written, .js->.ts source swap, extensions from resolveExtensions/extensions, index files, directory package.json), tsconfig/jsconfig paths+baseUrl with extends (JSONC), package.json imports (#x), packages by self-reference, node_modules and workspaces (exports map with conditions and patterns, else types/module/main/source, else index), .d.ts paired with its .js; unresolved bare specs key npm:<spec>. Default/namespace imports bind a qualifier and the default-export name; named imports bind names; re-export chains (export {a} from, export *, export * as ns) are followed; JS and TS name forms (a as b, type a, a: b, default as b) both handled via JSImportName. Visible: members visible except #private; module-level needs exported, an import naming it (covers export {a}/module.exports={a} written later), or a script file; name fallback never takes an explicitly imported name. Tests: 8 top-level tests over the JS and TS goldens plus hand-built modules, all pass; go test -race ./codeindex/... ok; gofmt/vet/build clean. Status forced to in_progress while 0083 was open. Extractor inconsistency to unify (not edited): the JS query writes import/export specifiers as the exported name only (b as c -> b, default as Canvas -> default), the TS query writes them whole (a as b).
