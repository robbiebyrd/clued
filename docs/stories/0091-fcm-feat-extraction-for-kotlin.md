---
id: 0091-fcm
title: Extraction for Kotlin
purpose: 'Adds Kotlin to the indexer: files .kt, .kts.'
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:08:47.069Z"
updated: "2026-10-19T20:59:38.872Z"
started: "2026-10-19T20:46:58.543Z"
completed: "2026-10-19T20:59:38.872Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-kotlin
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/kotlin/
      - mind-palace/codeparse/testdata/kotlin/
      - mind-palace/codeparse/grammars/kotlin.go
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

# Extraction for Kotlin

## Problem Statement

Adds Kotlin to the indexer: files .kt, .kts. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0109-bzh` Resolver for Java and Kotlin.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture classes, objects, interfaces, functions, properties, extension functions; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.kt, .kts), family, defining forms; scope rules for resolution later: `package`, `import`, extension receivers.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/kotlin -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/kotlin'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/kotlin'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/kotlin'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/kotlin/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/kotlin/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/kotlin.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T20:59:33.365Z - Kotlin extraction added: grammars/kotlin.go registers gotreesitter's grammars/kotlin; queries/kotlin/profile.yaml (family jvm, .kt/.kts, packageBy declaration, exported declared, options.defaultImports = Kotlin's implicit imports for the JVM resolver) and tags.scm: classes (data/sealed/abstract/value/annotation/inner), interfaces (fun interface too), enums and entries (const), objects and named companions (kind object), typealiases (type), top-level functions, methods (class/enum bodies), extension functions and properties (@receiver = last identifier of the receiver, nullable or generic), properties, const val (const), val/var constructor parameters (property); locals excluded with #not-has-ancestor? statements/function_body; visibility: every definition is two patterns with complementary regexes on its modifier prefix, only private is unexported; imports (plain, alias, wildcard with import.kind wildcard), package header; refs: calls (bare, minus common stdlib helpers; qualified via navigation), types (minus built-ins, qualifier = previous identifier of a qualified type), extends (constructor-invoked supertype, interface supertypes), implements (bare and by-delegated supertypes of classes/objects/object literals), annotations as type refs. Recovers the top-level misparse '@Ann(x) private fun f() {}' (infix_expression). One pattern per name shape (alternation bug). Goldens: Model.kt, Service.kt, Nested.kt, build.gradle.kts, Broken.kt; go test -race ./codeparse/ -run TestGolden/kotlin: 5/5 subtests pass; go test -race ./codeparse/...: ok. Engine limits, not changed (shared files): signatures are the first line (grammar has no body field), so a definition with annotations on their own line has the annotation as its signature; a val/var constructor parameter on the class's first line takes the class's KDoc as its doc. Only the three listed paths changed.
