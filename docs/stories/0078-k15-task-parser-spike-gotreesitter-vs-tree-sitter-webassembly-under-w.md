---
id: 0078-k15
title: 'Parser spike: gotreesitter vs tree-sitter WebAssembly under wazero'
purpose: Decides the pure-Go tree-sitter runtime every language story uses.
type: task
status: in_progress
priority: "3"
effort: M
created: "2026-10-19T18:07:07.216Z"
updated: "2026-10-19T19:50:14.735Z"
started: "2026-10-19T18:58:05.291Z"
tags:
  - plan-0012-cgi
  - wave-1
  - spike
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/spike_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  plans:
    - ["0012-cgi", "included", ["1.1"]]
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

# Parser spike: gotreesitter vs tree-sitter WebAssembly under wazero

## Problem Statement

Decides the pure-Go tree-sitter runtime every language story uses.

**Working on this concurrently.** This story is in wave 1 of plan 0012-cgi. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Build both

`CGO_ENABLED=0` for the 7 targets; `go-licenses check`.

### 3: Measure

Grammar availability and query support for all 21 languages, correctness on samples, files/s, memory, binary size, on clued, clued-cloud and one public repository per language.

### 4: Decide and record

Append the numbers and the choice to plan 0012's work log and this story's; list grammars that must be added to the runtime.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] [MANUAL] The choice and its numbers are recorded in the work log
- [x] [MANUAL] `spike_test.go` is deleted
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/spike_test.go` - temporary; deleted at the end

## Work Log

### 2026-10-19T19:49:32.246Z - **Decision: gotreesitter v0.56.0 (MIT), imported one grammar at a time (`github.com/odvcencio/gotreesitter/grammars/<lang>`), never the aggregate `grammars` package, which embeds GPL-3.0 and MPL-2.0 grammars.** The full numbers, the C-parity table, the wazero evidence and the verified API snippet for 0080-6sk are in plan 0012's new "Work Log" section (2026-10-19 entry).

### 2026-10-19T19:49:35.315Z - **Files:** spike_test.go was added in 4baad28 to reproduce the numbers and deleted in the next commit. go.mod and go.sum are restored, so 0080-6sk adds the dependency. The plan's work log was written through `plan update` because there is no `plan log` command.

### 2026-10-19T19:49:38.299Z - **Tests:** with the spike in place, VERIFY passed (gofmt, vet and build clean; `go test -race ./codeparse/...` ok with 25+1 subtests, plus a corpus run under -race with a shared Query and no races). After the deletion, `./codeparse/...` matches no package in this branch until 0079-zwm's codeparse files merge. gofmt, vet and build of ./... stay clean.

### 2026-10-19T19:49:54.521Z - Numbers: **Builds:** both candidates build with CGO_ENABLED=0 for all 7 targets. CI's `go-licenses check` passes with either one linked. mind-palace grows by +12.1 to +13.0 MiB with gotreesitter and 25 grammars, and by +3.2 MiB with wazero and 1 grammar (about +40 MiB for all 25 as .wasm). • **Grammars and queries:** 25/25 grammars ship with upstream node types, and a tags-style query passes on samples for every one. The Lisp dialects are commonlisp, elisp, clojure, scheme and racket; .mli files parse with the ocaml grammar. Nothing needs adding to the runtime. • **Agree with the C runtime file for file:** python, java, javascript, rust, php, ruby, lua, elisp, clojure, racket, c, objc, ocaml, commonlisp, scheme, and go on clued plus clued-cloud. • **Need work (extra errors only gotreesitter reports):** ◦ dart: 146 of 1303 files (Flutter trailing-comma calls). ◦ kotlin: 63 of 327 (one-line class bodies; `TestSpikeKnownDivergences` kept a reproducer). ◦ swift: 32 of 101 (whole-file errors, 5-30 s parses). ◦ c_sharp: 28 of 951 (#pragma). ◦ perl: about 9. ◦ cpp: 2. ◦ sql: 23 of 56 files fail on PostgreSQL DDL; the grammar is m-novikov's, and DerekStride is suggested for 0095. • **Speed:** clued is 0.58 MiB/s (73 files/s) single-threaded, clued-cloud 0.50 MiB/s. That is about 9x slower than C and 2.2x slower than wazero running the C runtime. Peak heap is 222 MiB with 1 worker and 1.1 GiB with 4 workers on the 38 MiB corpus. The 2 s cut-off works. • **wazero:** prebuilt tree-sitter .wasm files are Emscripten side modules (dylink, GOT and shared-table imports) that wazero cannot link, and 6 of our grammars are missing from them. A WASI reactor built with wasi-sdk works (530 KB for Go) but needs a C toolchain, a shim and 25 committed artifacts, and it is interpreter-only on linux/arm, where it is 50x slower.
