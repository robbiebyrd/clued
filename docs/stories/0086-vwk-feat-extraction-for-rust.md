---
id: 0086-vwk
title: Extraction for Rust
purpose: 'Adds Rust to the indexer: files .rs.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:20.739Z"
updated: "2026-10-19T21:01:59.238Z"
started: "2026-10-19T20:44:37.040Z"
completed: "2026-10-19T21:01:59.238Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-rust
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/rust/
      - mind-palace/codeparse/testdata/rust/
      - mind-palace/codeparse/grammars/rust.go
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

# Extraction for Rust

## Problem Statement

Adds Rust to the indexer: files .rs. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0113-hpg` Resolver for Rust.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture functions, structs, enums, traits, impls, modules, macros; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.rs), family, defining forms; scope rules for resolution later: `mod` tree, `use`, `impl Trait for Type`.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/rust -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/rust'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/rust'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/rust'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/rust/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/rust/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/rust.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T21:01:53.990Z - Rust extraction: grammars/rust.go registers gotreesitter grammars/rust; queries/rust/profile.yaml (family rust, .rs, separators ::, packageBy file, exported declared, options manifest/crateRoots/modFile/pathRoots for the mod-tree resolver) and tags.scm: structs, enums (+ variants, kind variant), unions, traits (+ associated types/consts), type aliases, consts, statics, modules, macro_rules, impls (named after their type, so methods are Type::method; trait impls give @reference.implements), functions (file, mod, block, extern, ERROR) and methods (inherent, trait impl, trait), fields; imports for use (spec = path, names as written, alias, kind glob), extern crate (kind extern_crate) and mod name; (kind mod); refs for calls (plain, path, method, turbofish), macros (std macros excluded), types (scoped with qualifier, Self excluded), supertraits (extends). Goldens: lib.rs, imports.rs, references.rs, generated.rs (nested mods/fns, macro-generated code, raw idents, cfg(test)), broken.rs; TestGolden/rust 5/5 pass, go test -race ./codeparse/... and ./codeindex/... pass. Deviations/limits: (1) a /// doc above #[attributes] is not attached (attributes are siblings; sibling-sequence patterns were exhaustive but made a 130 KB file take 10 s in gotreesitter vs 0.29 s now, and a quantified sibling run is not retried at later children) - an engine option to skip attribute siblings when collecting the doc above would fix it for Rust and other decorator languages; (2) the grammar's doc_comment swallows one following blank line, so a //! crate doc followed by one blank line becomes the first item's doc (lib.rs, broken.rs goldens); (3) #[macro_export] is not detected, macros are never exported; (4) generic type parameters appear as type refs. Alternations split per the known gotreesitter @name bug. No file outside the story's paths changed; go.mod unchanged.
