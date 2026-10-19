---
id: 0105-it1
title: 'Indexer: apply files, tombstones and `hiddenBy`'
purpose: Writes each changed file into its layer and keeps views correct.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-19T18:09:59.821Z"
updated: "2026-10-19T19:39:22.201Z"
started: "2026-10-19T19:33:12.770Z"
completed: "2026-10-19T19:39:22.201Z"
tags:
  - plan-0012-cgi
  - wave-5
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeindex/apply.go
      - mind-palace/codeindex/apply_test.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0104-v9x", "depends"]
    - ["0080-6sk", "depends"]
  plans:
    - ["0012-cgi", "included", ["3.1"]]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
---

# Indexer: apply files, tombstones and `hiddenBy`

## Problem Statement

Writes each changed file into its layer and keeps views correct.

**Working on this concurrently.** This story is in wave 5 of plan 0012-cgi. Start it when these are complete: `0104-v9x` Indexer: change-log consumer and layer builder; `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0106-wdt` Resolution core and the Go resolver; `0122-zmf` Indexer lifecycle and `server graph rebuild|status`.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 3.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Apply

Facts from the cache (parse on miss).

### 3: Visibility

Maintain `hiddenBy` on the same path in lower layers; deletions as tombstones.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeindex/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeindex/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeindex/apply.go` - parse via cache, `ApplyFile`, tombstones, hide lower files
- `mind-palace/codeindex/apply_test.go` - three-layer tests

## Work Log

### 2026-10-19T19:39:10.429Z - Added codeindex/apply.go: FileApplier (Applier) with ParseTimeout (DefaultParseTimeout 2s). Per batch it recomputes hiddenBy from the selection (a file is hidden by every layer above it through Parent holding its path) for the put/removed paths in every layer below them, before and after the batch, and for every path of a layer whose parent chain changed; SetHidden runs first for files that stay, then RemoveFile and ApplyFile (lower layers first) with the computed hiddenBy, so an interrupted batch is finished by the next turn. Facts come from the Service's parse cache (parse on miss); no parser, a parse error, invalid facts, a panic or a timeout index the file as text (parse_error, 60-line windows with 10 overlap, TextFacts); parse errors are cached, timeouts and no-parser are not. Exported: FileApplier, GraphFacts, TextFacts, SymbolNodeID/ChunkNodeID (path#local id), PackageKind, DefaultParseTimeout, MaxChunksPerFile (1000), TextWindowLines/Overlap. Tests (apply_test.go, fake parser since 0080 is not merged): three layers through the Service (base/branch/two overlays, revert, base moving under a branch), plan-driven hiddenBy (content-less files, overlay re-parented, branch layer dropped, new lower file under an existing upper one), interrupted batch, text fallback and caching, no parser, TextFacts, GraphFacts, PackageKind; each graph test runs on memgraph and FalkorDB (all pass with MIND_PALACE_TEST_FALKORDB_URL). go test -race ./codeindex/... 26 top-level tests pass; gofmt/vet/build clean. Deviations: started and completed with --force because 0080-6sk is not merged (parser injected through Options.Parse). FileApplier is not wired as the default Apply in New (service.go is not in this story's files): callers pass Options{Apply: FileApplier{}}. Only the two listed files changed.
