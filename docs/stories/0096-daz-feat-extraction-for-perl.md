---
id: 0096-daz
title: Extraction for Perl
purpose: 'Adds Perl to the indexer: files .pl, .pm, .t.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:09:12.665Z"
updated: "2026-10-19T20:58:33.112Z"
started: "2026-10-19T20:46:43.521Z"
completed: "2026-10-19T20:58:33.112Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-perl
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/perl/
      - mind-palace/codeparse/testdata/perl/
      - mind-palace/codeparse/grammars/perl.go
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

# Extraction for Perl

## Problem Statement

Adds Perl to the indexer: files .pl, .pm, .t. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0117-n53` Resolver for Perl.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture packages, subs; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.pl, .pm, .t), family, defining forms; scope rules for resolution later: `package`, `use`/`require`, `Package::sub`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/perl -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/perl'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/perl'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/perl'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/perl/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/perl/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/perl.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:58:25.953Z - Added Perl: grammars/perl.go (gotreesitter grammars/perl), queries/perl/profile.yaml (family perl, .pl/.pm/.t, separators ::, packageBy declaration, exported noUnderscore, definingForms has->field) and tags.scm: @package = first 'package NAME;' statement; symbols for block packages (package), 5.38 class (class), subs (function, receiver for sub Pkg::f), methods, fields, use constant (const), top-level our variables (var), Moose has (field); imports for use (pragma for lowercase modules, use otherwise, qw/quoted names) and require; refs: calls (builtins excluded, Package::sub and &Package::sub split into name and qualifier with #strip!), method calls with the invocant as qualifier, extends from use parent/base, @ISA, extends, with (mixin) and :isa. POD right before a definition is its doc. Goldens: Circle.pm, report.pl, Modern.pm, generated.t (multi-package, generated subs, heredoc, forward decl), broken.pl; TestGolden/perl 6 passes under -race. Known limits (engine, not query): subs after a second statement package are qualified by the first; sub Other::f keeps the file package in front of its receiver; the grammar parses a forward declaration 'sub f;' as a sub whose body runs on. Alternations capturing @name are written as separate patterns (gotreesitter v0.56.0 bug). Full go test -race ./codeparse/... failed only on TestGolden/go/large.go hitting the 2 s parse cut-off at load average ~42; it passes without -race.
