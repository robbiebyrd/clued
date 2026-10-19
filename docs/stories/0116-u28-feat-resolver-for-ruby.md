---
id: 0116-u28
title: Resolver for Ruby
purpose: Resolves references for Ruby so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:03.522Z"
updated: "2026-10-19T21:07:48.867Z"
started: "2026-10-19T21:01:20.597Z"
completed: "2026-10-19T21:07:48.867Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-ruby
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/ruby.go
      - mind-palace/codeindex/resolvers/ruby_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0094-dgl", "depends"]
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

# Resolver for Ruby

## Problem Statement

Resolves references for Ruby so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0094-dgl` Extraction for Ruby. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`require`/`require_relative`, `Module::Class` nesting, `include`/`extend`.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'ruby'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'ruby'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'ruby'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/ruby.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/ruby_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:07:41.993Z - Added codeindex/resolvers/ruby.go (Ruby family registered from init): package key = the file's feature (path without .rb/.rake below the first loadPath dir from the profile option, default lib); require/load open the feature, require_relative the path joined to the requiring file's dir, followed transitively (MaxRubyRequires 1000), only features in the project; autoload imports its names from its feature; gem binds nothing; Visible hides Rake task and namespace symbols; Rules: separators :: # . with name and member fallbacks. Module::Class nesting and include/extend/prepend (extends refs) go through the engine's scope walk and qualified matching. ruby_test.go: 5 tests (rules vs profile, RubyFeature table, Scope over a fake view incl. transitive and cyclic requires, Visible, and an end-to-end index of the codeparse/testdata/ruby goldens plus small files with codeparse.Parse and memgraph, 23 edge assertions). go test -race ./codeindex/... passes; gofmt/vet/build clean. Core limitations (not fixable from a resolver): members of a mixed-in module and nested constants defined in another file or another body of a reopened module (Billing~2) resolve only by name, since the engine's import/open steps take top-level definitions only and the scope walk follows symbol ids; a leading :: qualifier (::Shop::Mailer) does not match qualified names. Only the two listed files changed.
