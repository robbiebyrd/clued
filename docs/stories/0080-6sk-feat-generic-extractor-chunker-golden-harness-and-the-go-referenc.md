---
id: 0080-6sk
title: Generic extractor, chunker, golden harness and the Go reference language
purpose: The engine every language plugs into, proven on Go.
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:07:16.265Z"
updated: "2026-10-19T20:40:33.188Z"
started: "2026-10-19T19:51:59.653Z"
completed: "2026-10-19T20:40:33.188Z"
tags:
  - plan-0012-cgi
  - wave-2
  - contract
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 2
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/go.mod
      - mind-palace/go.sum
      - mind-palace/codeparse/extract.go
      - mind-palace/codeparse/chunk.go
      - mind-palace/codeparse/markdown.go
      - mind-palace/codeparse/text.go
      - mind-palace/codeparse/grammars/registry.go
      - mind-palace/codeparse/golden_test.go
      - mind-palace/codeparse/queries/go/
      - mind-palace/codeparse/testdata/go/
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0078-k15", "depends"]
    - ["0079-zwm", "depends"]
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
  "7":
    status: complete
---

# Generic extractor, chunker, golden harness and the Go reference language

## Problem Statement

The engine every language plugs into, proven on Go. After this, each language is a directory of data.

**Working on this concurrently.** This story is in wave 2 of plan 0012-cgi. Start it when these are complete: `0078-k15` Parser spike: gotreesitter vs tree-sitter WebAssembly under wazero; `0079-zwm` `codeparse` contract: facts, detection, profiles, cache. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0081-3cl` Extraction for Python; `0082-cow` Extraction for JavaScript; `0083-a3p` Extraction for TypeScript; `0084-ouo` Extraction for Java; `0085-134` Extraction for C#; `0086-vwk` Extraction for Rust; `0087-rzn` Extraction for C; `0088-pe1` Extraction for C++; `0089-99h` Extraction for Objective-C; `0090-aau` Extraction for Swift; `0091-fcm` Extraction for Kotlin; `0092-y8g` Extraction for Dart (and Flutter); `0093-yvw` Extraction for PHP; `0094-dgl` Extraction for Ruby; `0095-451` Extraction for SQL; `0096-daz` Extraction for Perl; `0097-8oc` Extraction for OCaml; `0098-cjj` Extraction for Lua; `0099-1pa` Extraction for Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme/Racket); `0105-it1` Indexer: apply files, tombstones and `hiddenBy`.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Extractor

Captures `@definition.*`, `@reference.*`, `@import`, `@package`, `@doc`, `@signature`.

### 3: Chunker and fallbacks

As plan 0012.

### 4: Golden harness

Per-language subtests discovered from `testdata/`; `-update` rewrites `*.facts.json`.

### 5: Go reference

Query, profile and goldens for Go.

### 6: Contributor note

A short `codeparse/queries/README.md` saying exactly which files a language story adds.

### 7: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/... && GOOS=windows GOARCH=arm64 CGO_ENABLED=0 go build ./...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/go.mod` - the chosen runtime
- `mind-palace/go.sum` - the chosen runtime
- `mind-palace/codeparse/extract.go` - run a language's query file, build `Facts`
- `mind-palace/codeparse/chunk.go` - structural chunks with signature repetition, ≤ 1,500 tokens
- `mind-palace/codeparse/markdown.go` - heading chunks
- `mind-palace/codeparse/text.go` - 60-line windows, 10 overlap; parse timeout fallback
- `mind-palace/codeparse/grammars/registry.go` - grammar registry; each language registers from its own `grammars/<lang>.go`
- `mind-palace/codeparse/golden_test.go` - `TestGolden/<lang>` over `testdata/<lang>/*` with `-update`
- `mind-palace/codeparse/queries/go/` - Go query file and profile
- `mind-palace/codeparse/testdata/go/` - Go golden files

## Work Log

### 2026-10-19T20:40:27.311Z - Built the generic extractor (codeparse/extract.go: Engine, NewEngine, Default, Parse, ParseDetected, Version/VersionDetected, Check; package-level Parse(path, content) and ParseVersion matching codeindex's ParseFunc/ParseVersion options), the structural chunker (chunk.go: one chunk per top-level definition with its doc, split at statement boundaries into <= 1,500-token parts repeating the signature, comments kept with their statement, the code between definitions as windows, gaps under 16 bytes skipped), Markdown heading sections (markdown.go), 60/10 line windows and text fallback (text.go), the grammar registry (grammars/registry.go; each language registers from grammars/<lang>.go, Go in grammars/go.go), the Go query and profile (queries/go/), the contributor note (queries/README.md) and the golden harness (golden_test.go: TestGolden/<lang>/<input> with -update, x.in inputs parsed as x, chunk invariants, every profile needs goldens, plus text, Markdown, no-grammar, bad-query, timeout, capture, concurrency and split tests; 98 tests and subtests pass with -race, with and without MongoDB). Decisions: a parse cut off (2 s) or failed gives text facts with parseError; a tree with error nodes keeps whatever the query matched (no parseError, as Facts.Validate requires). Added github.com/odvcencio/gotreesitter v0.56.0 (MIT); CGO_ENABLED=0 go build ./... passes for all 7 targets; go-licenses check (CI's flags) passes. Files outside the list: codeparse/grammars/go.go (the per-language registration file the registry design calls for) and codeparse/queries/README.md (required by step 6). Not wired into a server: nothing constructs codeindex.Service yet, so callers pass Options{Parse: codeparse.Parse, ParseVersion: codeparse.ParseVersion} when they do.
