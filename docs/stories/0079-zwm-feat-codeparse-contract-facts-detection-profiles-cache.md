---
id: 0079-zwm
title: '`codeparse` contract: facts, detection, profiles, cache'
purpose: Fixes the shapes the extractor, the 19 language stories, the cache and the indexer build on.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:07:11.562Z"
updated: "2026-10-19T19:06:44.847Z"
started: "2026-10-19T18:57:57.812Z"
completed: "2026-10-19T19:06:44.847Z"
tags:
  - plan-0012-cgi
  - wave-1
  - contract
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/facts.go
      - mind-palace/codeparse/detect.go
      - mind-palace/codeparse/profiles.go
      - mind-palace/codeparse/cache.go
      - mind-palace/codeparse/contract_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
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
---

# `codeparse` contract: facts, detection, profiles, cache

## Problem Statement

Fixes the shapes the extractor, the 19 language stories, the cache and the indexer build on.

**Working on this concurrently.** This story is in wave 1 of plan 0012-cgi. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language; `0100-qw0` Mongo parse cache; `0104-v9x` Indexer: change-log consumer and layer builder.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Types

As plan 0012's Parsing component.

### 3: Detection

Extensions from the Languages table; `.h` by content (`@interface`, `class`, `template`, `namespace`) and neighbours.

### 4: Profiles as data

One `profile.yaml` per language directory so language stories never edit Go files.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/facts.go` - `Facts`, `Symbol`, `Import`, `Ref`, `Chunk`, `Pos`, `Version`
- `mind-palace/codeparse/detect.go` - language by extension and content, including `.h` (C/C++/Objective-C) and `.m`
- `mind-palace/codeparse/profiles.go` - loading `queries/<lang>/profile.yaml` (family, extensions, defining forms)
- `mind-palace/codeparse/cache.go` - `Cache` interface and memory cache keyed by `(sha256, version)`
- `mind-palace/codeparse/contract_test.go` - detection and profile loading

## Work Log

### 2026-10-19T19:06:39.764Z - Added mind-palace/codeparse: facts.go (Facts, Symbol, Import, Ref, Chunk, Pos, Version="1", kind constants, SymbolID/ChunkID/HashText, Facts.Validate, Clone, ParseError causes), detect.go (Languages table of the 20 language IDs = queries/ directory names with family, extensions, filenames and per-extension dialects: tsx, ocaml interface, Lisp commonlisp/elisp/clojure/scheme/racket; Detect(path, content, neighbours) by filename, longest extension (.d.ts), Markdown; .h by content outside comments/strings (ObjC forms first, then C++ forms) then neighbours (.m/.mm > C++ sources > .c) then C; .m always ObjC), profiles.go (LoadProfiles(fs.FS) over <lang>/profile.yaml, strict camelCase YAML checked against the table, grammar/query/dialect defaults, kinds over DefaultKinds, definingForms, scope separators/packageBy/exported/caseInsensitive/options, per-language digest -> Profile.FactsVersion(dialect) so a language's data change re-parses only its files; TextFactsVersion for text/Markdown), cache.go (Key{SHA256, Version}, ContentKey, Cache port, Cached parse-on-miss helper, LRU MemoryCache that copies in and out). No embed of queries/ here (no files there yet): the extractor (0080) embeds it and passes fs.Sub. Runtime-agnostic: nothing depends on the parser runtime. go test -race ./codeparse/...: 20 tests (about 60 subtests) pass; gofmt, go vet ./..., go build ./... clean. Only the five listed files changed; no go.mod change (yaml.v3 already required).
