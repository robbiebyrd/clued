---
id: 0115-jq8
title: Resolver for PHP
purpose: Resolves references for PHP so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:57.853Z"
updated: "2026-10-19T21:11:33.110Z"
started: "2026-10-19T21:05:42.901Z"
completed: "2026-10-19T21:11:33.110Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-php
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/php.go
      - mind-palace/codeindex/resolvers/php_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0093-yvw", "depends"]
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

# Resolver for PHP

## Problem Statement

Resolves references for PHP so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0093-yvw` Extraction for PHP. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

namespaces, `use`, `require`/`include`, Composer PSR-4 autoload when present.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'php'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'php'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'php'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/php.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/php_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:11:26.591Z - Added codeindex/resolvers/php.go (php family: package = namespace declaration, a lone braced namespace, or the PSR-4 root of composer.json autoload/autoload-dev for a class file without a declaration, lower-cased keys; use/use as/group use bind names and qualifiers (parent namespace and the name itself), use function/const bind names, namespace\ names the own namespace; bare names never reach class members, the global namespace is a fallback for functions/constants only; require/include(_once) of literal, __DIR__ and dirname() paths mark the required file's namespace as imported for member preference; CaseInsensitive, MemberFallback, no NameFallback; exported helpers PHPNamespaceKey, PHPPSR4Namespace, PHPRequirePaths, PHPComposerFile) and php_test.go (6 tests: package keys, PSR-4, require paths, scope over imports.php, visibility, end-to-end resolution through codeindex with codeparse.Parse over the five PHP golden inputs plus supporting files). go test -race ./codeindex/... passes; gofmt/vet/build clean. Only the two listed files changed. Engine limits noted for the coordinator: braced-namespace contents are not top-level so bare/imported names cannot reach them; a qualifier of only '\' (\X) cannot resolve to a global top-level; aliased bare names (use A\B as C; new C) have no candidate; Qualifiers/Names lookups are case-sensitive despite CaseInsensitive.
