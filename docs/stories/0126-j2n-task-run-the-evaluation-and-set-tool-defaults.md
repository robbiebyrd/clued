---
id: 0126-j2n
title: Run the evaluation and set tool defaults
purpose: Turns the measurements into the tools' defaults and a recorded baseline.
type: task
status: in_progress
priority: "3"
effort: S
created: "2026-10-19T18:11:58.507Z"
updated: "2026-10-20T00:46:52.699Z"
started: "2026-10-19T22:27:42.383Z"
tags:
  - plan-0012-cgi
  - wave-9
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 9
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/session/sessionmcp/code_tools.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0125-067", "depends"]
    - ["0124-qap", "depends"]
    - ["0107-nkl", "depends"]
    - ["0108-qz5", "depends"]
    - ["0109-bzh", "depends"]
    - ["0110-djd", "depends"]
    - ["0111-ymw", "depends"]
    - ["0112-5ga", "depends"]
    - ["0113-hpg", "depends"]
    - ["0114-opk", "depends"]
    - ["0115-jq8", "depends"]
    - ["0116-u28", "depends"]
    - ["0117-n53", "depends"]
    - ["0118-lga", "depends"]
    - ["0119-ddc", "depends"]
    - ["0120-fq2", "depends"]
    - ["0121-85d", "depends"]
  plans:
    - ["0012-cgi", "included", ["5.1"]]
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
    status: pending
---

# Run the evaluation and set tool defaults

## Problem Statement

Turns the measurements into the tools' defaults and a recorded baseline.

**Working on this concurrently.** This story is in wave 9 of plan 0012-cgi. Start it when these are complete: `0125-067` Evaluation harness and task set; `0124-qap` Agent tools: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`; `0107-nkl` Resolver for Python; `0108-qz5` Resolver for JavaScript and TypeScript; `0109-bzh` Resolver for Java and Kotlin; `0110-djd` Resolver for C# (csharp); `0111-ymw` Resolver for C, C++ and Objective-C; `0112-5ga` Resolver for Swift; `0113-hpg` Resolver for Rust; `0114-opk` Resolver for Dart; `0115-jq8` Resolver for PHP; `0116-u28` Resolver for Ruby; `0117-n53` Resolver for Perl; `0118-lga` Resolver for Lua; `0119-ddc` Resolver for OCaml; `0120-fq2` Resolver for Lisp dialects; `0121-85d` Resolver for SQL. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 5.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Run

`CLUED_EVAL=1 scripts/eval-codesearch.sh` for every arm.

### 3: Record

Report in plan 0012's and this story's work logs.

### 4: Set defaults

Budgets, depth, listed tools.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/sessionmcp/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [ ] [MANUAL] The report (tokens, turns, success per family and language) is in the work log
- [ ] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./session/sessionmcp/...
- [ ] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/session/sessionmcp/code_tools.go` - default budgets, depth and listed tools from the results

## Work Log

### 2026-10-20T00:04:59.509Z - Default-condition run (subset for the owner's $50 cap: 28 tasks keeping every family and language x grep, graph, context x 1 run = 84 sessions, Opus 5.5, --budget 0.5, isolated local server): success 100% in every arm; 0 code tool calls in all 56 graph/context sessions (the clued tools are deferred and the agent greps; with ENABLE_TOOL_SEARCH=false also 0, at about 1.7x the cost); median tokens grep 112,727, graph 129,556, context 105,782 (noise between identical configurations); turns 4/5/4. Model-free retrieval over 98 tasks: code_context file recall 0.68/0.72/0.73/0.73/0.73 at 2k/4k/6k/8k/12k tokens; callers depth 1/2/3 impact recall 0.86/0.86/0.85 at 314/883/1,708 median tokens. Bug: code_context 503s on 8 larger graphs (UNWIND symbol-by-id query hits FalkorDB's 1 s TIMEOUT). Tables per family and language are in plan 0012-cgi's work log. Spend so far 15.18 USD captured (about 18.2 billed).

### 2026-10-20T00:46:43.130Z - Directed condition (user CLAUDE.md recommending the clued code tools; graph and context arms, same 28 tasks, 56 sessions, 1 run): only 8 of 56 sessions called a code tool, 9 calls in all (code_context 4, code_search 2, code_symbol 2, code_neighbors 1, code_outline 0), each once and then Grep and Read as before; those 8 used more tokens than the task's grep session (median about +37%); 2 of the 4 code_context calls answered unavailable (the FalkorDB timeout); success 100% everywhere; median tokens grep 112,727 / graph 119,743 (+6%) / context 106,119 (-6%), turns 4/5/4, tool calls 4/5/5; by family graph and context vs grep: named +6%/-14%, concept +2%/+1%, impact +10%/+28%, edit +41%/-24% (one task). Tables, the per-language report and the reasoning are in plan 0012-cgi's work log (5.1 Evaluation run and tool defaults).

### 2026-10-20T00:46:46.423Z - Defaults set in session/sessionmcp/code_tools.go: code_context sends 4,000 tokens by default (CodeContextTokens; recall 0.72 at 4k vs 0.73 at 6k-12k for about 700 fewer tokens per answer; the route's DefaultContextTokens stays 6,000 for other callers); code_search, code_symbol, code_neighbors and code_outline send 2,000 (CodeToolTokens = codeapi.DefaultGraphTokens; search was cut at 2k for only 4 of 98 tasks, median 762 tokens); code_neighbors sends depth 1 by default (CodeNeighborsDepth; depth 2 and 3 found no further impact file, recall 0.86/0.86/0.85, at 3-5x the tokens), and its depth description says so. Listed tools unchanged: all five stay listed under the indexed-and-allowed rule. They are deferred, so listing costs a name each, and use was too rare to tell them apart (code_outline was never called); listing them up front cost about 1.7x and got no use. Not changed, outside the Files: PROTOCOL.md 6.1.7 and docs/code-graph.md still say 6,000 for code_context; code_context's 503 on larger graphs is a codegraph/falkor query (UNWIND symbol-by-id) over FalkorDB's 1 s TIMEOUT and needs its own story. Total spend 24.79 USD captured, about 29.75 USD billed (cap 50).
