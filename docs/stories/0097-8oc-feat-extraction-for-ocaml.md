---
id: 0097-8oc
title: Extraction for OCaml
purpose: 'Adds OCaml to the indexer: files .ml, .mli.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:09:17.942Z"
updated: "2026-10-19T20:53:50.559Z"
started: "2026-10-19T20:46:55.574Z"
completed: "2026-10-19T20:53:50.559Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-ocaml
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/ocaml/
      - mind-palace/codeparse/testdata/ocaml/
      - mind-palace/codeparse/grammars/ocaml.go
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

# Extraction for OCaml

## Problem Statement

Adds OCaml to the indexer: files .ml, .mli. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0119-ddc` Resolver for OCaml.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture modules, functors, types, values, functions, classes; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.ml, .mli), family, defining forms; scope rules for resolution later: `open`, `Module.name`, `.mli`/`.ml` pairing, dune libraries.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/ocaml -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/ocaml'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/ocaml'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/ocaml'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/ocaml/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/ocaml/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/ocaml.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:53:43.879Z - Added OCaml extraction: grammars/ocaml.go registers gotreesitter's ocaml grammar (also used by the .mli 'interface' dialect, since no ocaml_interface grammar ships); queries/ocaml/tags.scm captures modules, functors, module types (interface), types (enum for variants, struct for records, type), variant constructors, record fields, exceptions, module-level let functions/values (incl. operators), val specs, externals, classes, class types, methods, instance variables; imports for open/include/let open/M.(e) (kinds open, include, local_open); refs: calls (qualified, pipelines |> and @@, method invocations, functor applications), types, module bindings, implements (module type constraints), extends (inherit), new, constructors, record field reads. profile.yaml: family ocaml, packageBy file, interface dialect on the ocaml grammar, options for .mli/.ml pairing and dune. Goldens: shapes.ml, shapes.mli, store.ml, widgets.ml, broken.ml (5 inputs). gofmt/vet/build clean; go test -race ./codeparse/... passes (TestGolden/ocaml 5/5). Known engine behaviour: fields of a one-line record inherit the comment above the type as their doc. Only the story's three paths changed.
