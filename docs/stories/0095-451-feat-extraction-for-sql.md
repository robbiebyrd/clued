---
id: 0095-451
title: Extraction for SQL
purpose: 'Adds SQL to the indexer: files .sql.'
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:09:07.704Z"
updated: "2026-10-19T21:06:48.234Z"
started: "2026-10-19T20:46:38.235Z"
completed: "2026-10-19T21:06:48.234Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-sql
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/sql/
      - mind-palace/codeparse/testdata/sql/
      - mind-palace/codeparse/grammars/sql.go
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

# Extraction for SQL

## Problem Statement

Adds SQL to the indexer: files .sql. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0121-85d` Resolver for SQL.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture tables, views, columns, functions, procedures, triggers, indexes; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.sql), family, defining forms; scope rules for resolution later: schema-qualified names; `FROM`/`JOIN`/`INSERT INTO`/`CALL` references; unparsable statements as text.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/sql -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/sql'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/sql'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/sql'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/sql/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/sql/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/sql.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T21:06:40.795Z - Added SQL: grammars/sql.go registers gotreesitter's sql grammar (m-novikov/tree-sitter-sql; the only SQL grammar gotreesitter v0.56.0 ships, so DerekStride via ts2go was not added: it would mean generating a grammar outside this story's files). queries/sql/tags.scm captures schema, table, column (incl. ALTER TABLE ADD COLUMN), view (incl. materialized), function, procedure (told apart by keyword with #match?), trigger (owned by its table via @receiver), index, enum, type, domain, sequence; schema-qualified names via @receiver (s.t); quotes stripped with #strip!. References: table refs (reference.table -> type) from FROM, JOIN, INSERT INTO, UPDATE (UPDATE s.t recovered from its error node), DELETE, ALTER TABLE, CREATE INDEX ON, trigger ON, REFERENCES; calls from function_call (built-ins filtered), trigger EXECUTE FUNCTION and CALL (recovered from the error node's text, since the grammar cannot parse CALL); user types. Imports: CREATE EXTENSION (kind extension), psql \i/\ir (kind include). @package: USE <db> (MySQL/T-SQL), recovered from its error node. profile.yaml: family sql, .sql, separators ., packageBy declaration, exported all, caseInsensitive. Goldens (5): schema.sql (every kind), queries.sql (references, includes, CALL), migration.sql (Prisma-generated, quoted identifiers), mysql.sql (dialect quirks), broken.sql (syntax error). Limits documented in tags.scm: routine bodies are strings to this grammar; consecutive unparsable statements merge into one error node so only the first CALL is seen; CTE names show up as table refs. Verify: gofmt/vet/build clean; go test -race ./codeparse/ -run TestGolden/sql 5/5 pass. go test -race ./codeparse/... fails only TestGolden/go/large.go (the Go reference) on this host (load average ~60 on 4 CPUs: the 2 s parse cut-off fires under -race); it passes without -race and is untouched by this story. No file outside the story's three paths changed.
