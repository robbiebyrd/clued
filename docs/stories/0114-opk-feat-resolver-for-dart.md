---
id: 0114-opk
title: Resolver for Dart
purpose: Resolves references for Dart so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:10:52.301Z"
updated: "2026-10-19T21:22:33.823Z"
started: "2026-10-19T21:04:29.828Z"
completed: "2026-10-19T21:22:33.823Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-dart
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/dart.go
      - mind-palace/codeindex/resolvers/dart_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0092-y8g", "depends"]
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

# Resolver for Dart

## Problem Statement

Resolves references for Dart so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0092-y8g` Extraction for Dart (and Flutter). Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`import`/`export`/`part`, `package:` URIs via `pubspec.yaml`.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'dart'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'dart'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'dart'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/dart.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/dart_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:22:28.240Z - Added codeindex/resolvers/dart.go (family dart: library key = file path, parts with part of 'uri' share it; import/export/part/conditional URIs resolved via pubspec.yaml name, path dependencies/overrides, workspace members and root package dirs, following the profile options manifest/packageScheme/packageDir/sdkScheme; prefixes bind the library plus its transitive exports; unprefixed imports bind public top-level names (so names imported twice are ambiguous -> name); show/hide combinators read from the directive text at Import.Start, through export chains; underscore privacy with library-name matching for part of a.b; Rules: separator '.', NameFallback, MemberFallback, ReceiverMembers). dart_test.go: 6 tests (end-to-end through codeindex.Resolver on hand-built facts and on the extractor goldens directives.dart, shapes.dart, user.dart, user.g.dart; URI/pubspec table; profile options; visibility). gofmt/vet/build clean; go test -race ./codeindex/... passes. Limitation: a part that names its library by name (part of a.b;) has no link to the library's imports (the View cannot find the library by name), so its own refs fall back to name confidence. Only dart.go and dart_test.go changed.
