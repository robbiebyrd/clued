---
id: 0090-aau
title: Extraction for Swift
purpose: 'Adds Swift to the indexer: files .swift.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:41.549Z"
updated: "2026-10-19T20:59:13.618Z"
started: "2026-10-19T20:46:58.865Z"
completed: "2026-10-19T20:59:13.618Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-swift
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/swift/
      - mind-palace/codeparse/testdata/swift/
      - mind-palace/codeparse/grammars/swift.go
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

# Extraction for Swift

## Problem Statement

Adds Swift to the indexer: files .swift. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0112-5ga` Resolver for Swift.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture classes, structs, enums, protocols, extensions, functions, methods; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.swift), family, defining forms; scope rules for resolution later: `import`, extensions merged, conformance.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/swift -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/swift'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/swift'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/swift'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/swift/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/swift/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/swift.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:59:08.686Z - Swift extraction: grammars/swift.go registers gotreesitter's swift grammar; queries/swift/profile.yaml (family swift, .swift, kinds actor->class, case->const, associatedtype->type; scope separators '.', packageBy directory, exported declared, options module=target/targetRoots Sources,Tests/extensions merge/conformance implements) and tags.scm (imports incl. 'import struct M.T' names, @testable and @_exported kinds; class/struct/enum/actor/protocol/extension (named by the extended type, so members merge into it); top-level functions and operator functions; methods, init/deinit/subscript, properties and typealiases of classes, structs, actors, enums, extensions and protocols, with public/open as exported and public extension/protocol members exported; enum cases; top-level let/var/typealias; macros; inheritance as extends (class first entry, protocol parents) and implements (other entries, struct/enum/actor/extension conformances), stdlib types left out; calls f(x)/f{} not subscripts, qualified calls via name/member/self/super, freestanding macros; type refs minus stdlib types and built-in attributes). No [..] alternation of @name patterns (LANG.md bug). Goldens: Shapes, Imports, ShapeStore, Builders, Broken (5 inputs, 5 subtests pass); gofmt/vet/build clean, go test -race ./codeparse/... ok. Found a gotreesitter v0.56.0 swift bug: a top-level /** */ doc comment can silently drop earlier top-level declarations (protocol and struct before it vanish, no error node, HasError false); the goldens use /// at top level, /** */ only inside a type body where it parses. Limitation: a private member of a public extension is marked exported; a macro declaration whose attribute sits on its own line has that line as signature.
