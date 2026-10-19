---
id: 0117-n53
title: Resolver for Perl
purpose: Resolves references for Perl so calls, references and implementations link to the right definitions.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:11:09.249Z"
updated: "2026-10-19T21:11:26.006Z"
started: "2026-10-19T21:04:44.237Z"
completed: "2026-10-19T21:11:26.006Z"
tags:
  - plan-0012-cgi
  - wave-7
  - language
  - family-perl
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 7
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/resolvers/perl.go
      - mind-palace/codeindex/resolvers/perl_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0106-wdt", "depends"]
    - ["0096-daz", "depends"]
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

# Resolver for Perl

## Problem Statement

Resolves references for Perl so calls, references and implementations link to the right definitions.

**Working on this concurrently.** This story is in wave 7 of plan 0012-cgi. Start it when these are complete: `0106-wdt` Resolution core and the Go resolver; `0096-daz` Extraction for Perl. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0126-j2n` Run the evaluation and set tool defaults.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Rules

`package`, `use`/`require`, `Package::sub`.

### 3: Confidence

`exact` only when the rule identifies one definition; ambiguous matches keep the best with `name`.

### 4: Tests

Use the `codeparse/testdata/` inputs of the family's languages; read them, don't edit them.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'perl'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/resolvers/ -run 'perl'
- [x] [MANUAL] Only this family's two files changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/resolvers/ -run 'perl'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/resolvers/perl.go` - the family's resolver, registered in its own file
- `mind-palace/codeindex/resolvers/perl_test.go` - resolution tests over the language goldens

## Work Log

### 2026-10-19T21:11:14.923Z - Added codeindex/resolvers/perl.go (perl family resolver: package = declared package, else the script's path as its own package main; use LIST binds names (sigils dropped, :tag opens), use without list opens, require binds nothing; single-segment modules in the project bound as qualifiers unless the file also qualifies by Module::X; require of a .pl opens that script, of a .pm is its module; $self/$class/shift/__PACKAGE__ bound to the own package unless the file's subs span several packages (sub Other::f, recognised by its signature, does not count); Visible: package/import/open steps only reach definitions that belong to the keyed package (QualName), underscore subs hidden from opens and name fallback across packages; exported PerlInOwnPackage, PerlModuleOfPath) and perl_test.go (external test package: end-to-end Service+memgraph over the Circle.pm, report.pl, Modern.pm goldens plus hand-built facts for multi-package files and absolute sub names, and unit tests of Package/Scope/Visible; 4 tests pass with -race). Verified: gofmt, go vet ./..., go build ./..., go test -race ./codeindex/... ok. Merged claude/sharp-gates-45zpak (Perl extraction, 0096) first as instructed; only perl.go and perl_test.go changed by this story. Core limits (not changed): File has one package key, so bare refs inside a secondary package statement cannot be scoped to it; the engine binds a qualifier by its first segment, so multi-segment Perl modules resolve Package::sub by qualified name (confidence qualified) instead of exact; statement-package subs are top-level, so $obj->method on a non-self invocant only reaches block-package/class members.
