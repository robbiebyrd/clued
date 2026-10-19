---
id: 0103-cmc
title: FalkorDB graph adapter and CI service
purpose: The production graph store.
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:09:49.112Z"
updated: "2026-10-19T19:29:26.283Z"
started: "2026-10-19T19:17:06.756Z"
completed: "2026-10-19T19:29:22.820Z"
tags:
  - plan-0012-cgi
  - wave-3
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codegraph/falkor/falkor.go
      - mind-palace/codegraph/falkor/falkor_test.go
      - .github/workflows/mind-palace.yml
      - mind-palace/go.mod
      - mind-palace/go.sum
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0102-6fi", "depends"]
  plans:
    - ["0012-cgi", "included", ["2.1"]]
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

# FalkorDB graph adapter and CI service

## Problem Statement

The production graph store.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0102-6fi` In-memory graph and the `codegraphtest` suite. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0047-48v` Run the code graph in clued-cloud.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 2.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Adapter

Never string-built queries; one graph per namespace key.

### 3: Full text

Verify FalkorDB's full-text procedures and use them for `SearchText`.

### 4: CI

Service container pinned by version; tests skip when the URL is unset.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && MIND_PALACE_TEST_FALKORDB_URL=redis://127.0.0.1:6379 go test -race ./codegraph/falkor/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codegraph/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codegraph/falkor/falkor.go` - Redis-protocol adapter, parameterized `GRAPH.QUERY`, schema and indexes, full-text procedures
- `mind-palace/codegraph/falkor/falkor_test.go` - suite with `MIND_PALACE_TEST_FALKORDB_URL`
- `.github/workflows/mind-palace.yml` - `falkordb/falkordb` service container (pinned) and the env var
- `mind-palace/go.mod` - client library if one is used (must pass `go-licenses`)
- `mind-palace/go.sum` - same

## Work Log

### 2026-10-19T19:29:26.283Z - Added mind-palace/codegraph/falkor: a codegraph.Opener over FalkorDB's Redis protocol with a stdlib-only RESP2 client (no new dependency; go.mod/go.sum unchanged): pooled connections, redis:// and rediss:// URLs with AUTH and SELECT, connection failures as ErrUnavailable. Every query is a constant Cypher text (labels and edge types from codegraph's schema constants at init) sent with GRAPH.QUERY/GRAPH.RO_QUERY --compact, values only as CYPHER parameters; one graph per namespace key. PutLayer creates missing range indexes and the full-text index via db.idx.fulltext.createNodeIndex (nostem, weights name 3, signature 2, doc 1); SearchText uses db.idx.fulltext.queryNodes with the text reduced to plain letter/digit/underscore terms (query syntax can never reach RediSearch). Verified against falkordb/falkordb v4.22.1: full-text queries are AND, case-insensitive for ASCII, trailing * prefixes, no stemming with nostem, no implicit result limit; a query on a graph without its index returns nothing. Known difference from memgraph: FalkorDB folds case of ASCII letters only. CI: falkordb/falkordb:v4.22.1 service container with a redis-cli health check and MIND_PALACE_TEST_FALKORDB_URL in the test job. Tests: codegraphtest 22/22 subtests pass on FalkorDB (also -race -count=3), plus 6 adapter tests (parameter round trip of awkward strings, query encoding, text query reduction, URL parsing, ErrUnavailable, indexes after drop and rebuild); without the URL the server tests skip. gofmt/vet/build clean; go test -race ./codegraph/... ok.
