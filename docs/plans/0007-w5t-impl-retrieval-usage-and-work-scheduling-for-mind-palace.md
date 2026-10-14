---
id: 0007-w5t
title: Retrieval, usage and work scheduling for mind-palace
type: impl
status: in_progress
priority: "1"
effort: XL
created: "2026-10-14T03:11:32.943Z"
updated: "2026-10-14T03:13:19.714Z"
tags:
  - session
  - mcp
  - search
  - usage
  - plans-and-stories
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  specs:
    - docs/superpowers/specs/2026-09-23-usage-enrichment-design.md
    - docs/superpowers/specs/2026-09-23-clued-mcp-search-design.md
progress:
  "1":
    status: pending
    stories:
      - 0024-mfj
  "1.1":
    status: pending
  "2":
    status: pending
    stories:
      - 0025-u4v
  "2.1":
    status: pending
  "3":
    status: pending
    stories:
      - 0026-fu8
  "3.1":
    status: pending
  "3.2":
    status: pending
  "4":
    status: pending
    stories:
      - 0027-bek
  "4.1":
    status: pending
  "4.2":
    status: pending
  "5":
    status: pending
    stories:
      - 0028-p8p
  "5.1":
    status: pending
  "5.2":
    status: pending
  "6":
    status: pending
    stories:
      - 0029-h4q
  "6.1":
    status: pending
  "6.2":
    status: pending
---

# Retrieval, usage and work scheduling for mind-palace

## Summary

**Goal:** An agent can find any past conversation by its words, see which sessions touched a file or failed, read token use and cost, pick the next ready story and claim it, load a small tool set, and start every session primed with the repository's state.

**Problem:** mind-palace captures everything but gives little of it back: search is unranked regex over Bash commands, memory and project paths (conversation text cannot be searched), the 19 enrichers' results have no query, tokens and cost are never computed, story dependencies are stored but not enforced and nothing lists ready work, the plugin registers 99 plan and story tools whatever the task, and the prime report is only produced on request. The feature gap report (reports/Mind palace feature gap analysis.md) ranks these six as the highest value for their effort.

**Approach:** Read the data already captured: two new enrichers (search text, usage) with store queries and MCP tools over them, store queries over the existing file-tracker and error-flag results, dependency rules and ready/claim operations in the service, a core tool tier, and a SessionStart hook output that injects a capped prime report on startup, resume, clear and compaction.

---

# Part 1 — Design

## Current State

- `session.Store` queries are regex only (`SessionQuery`, `CommandQuery`, `MemoryQuery`); `find_sessions.query` matches project path or cwd. Mongo has no text index ([session/store.go](mind-palace/session/store.go), [mongosession](mind-palace/session/mongosession/mongosession.go)).
- Enrichers store results under `enrichments.<name>` ([session/enricher.go](mind-palace/session/enricher.go)); `file-tracker` (hook_events: file_path, operation, language) and `error-flag` (transcript_lines: is_error, error_type) already run by default, but no store method or tool reads them.
- `docs/superpowers/specs/2026-09-23-usage-enrichment-design.md` describes usage extraction from `message.usage`, written for the retired TypeScript daemon; nothing implements it in Go.
- Story and plan links accept `depends` and `blocks` ([kind/kind.go](mind-palace/kind/kind.go)); `applyStatus` checks only the transition table and the criteria gate ([service/fields.go](mind-palace/service/fields.go)). `setAssignee` is a plain write.
- `server/mcpserver` registers every op of both registries as a tool, 99 in all; `.mcp.json` starts `mcp --no-session-tools`.
- `hooks/session-start` starts the daemon (or relays in remote mode) and prints nothing; `prime_context` and `/clued-prime` produce the report on request ([collect/prime](mind-palace/collect/prime/prime.go)).

## Decisions

- **Index text through an enricher (`search-text`), not at write time.** — The enrichment loop already walks every stored line, so old transcripts are indexed by the same code as new ones, with no migration; a few seconds of lag after capture is acceptable for search.
- **Mask secrets in the indexed text unconditionally, with the privacy-redact patterns.** — The search index is a second copy of the conversation; it should never be the place a secret is easiest to find, whether or not the opt-in redactor runs.
- **Rank with MongoDB `$text` and `textScore`; the memory store scores by term frequency.** — Mongo is the only session store; a native text index needs no new dependency. The memory store only has to order consistently for the conformance suite.
- **Usage is computed per assistant message (deduplicated by `message.id`) with an estimated USD cost from a built-in price table.** — Claude Code writes one transcript line per content block, each repeating the message's usage; summing lines would multiply the counts. Costs are labelled estimates; the counts are exact.
- **Dependencies are enforced in the service: a document cannot start (in_progress) or complete while one it depends on is not finished; `force` overrides, as for transitions.** — Rules live in service/, and the links already exist with the right meaning; enforcing them on both kinds needs no kind flag because both kinds have the relations.
- **`claim` sets the assignee and moves to in_progress in one call, refusing a document someone else holds; the file store serialises claims with a lock file, other stores re-read before and after the write.** — Parallel agents need one atomic step; the store port has no compare-and-swap, and adding one to nine plugins is out of proportion. An in-process mutex covers one server, the lock file covers agents on one checkout.
- **Tool tiers: ops carry a `Core` flag; `mcp --tools core|full` (env `MIND_PALACE_MCP_TOOLS`), default `full`; the plugin's `.mcp.json` asks for `core`.** — Library users keep every tool; plugin users get the token saving. Every op stays reachable through the CLI and through `full`.
- **Priming is a SessionStart hook output (`additionalContext`) from `mind-palace context prime --hook`, capped at 8,000 characters and 3 seconds, on by default, off with `primeOnStart: false` / `CLUED_PRIME_ON_START=0`.** — Claude Code re-runs SessionStart with source `compact` after compaction, so one path covers start, resume, clear and compaction; a cap keeps the cost predictable.

### Rejected alternatives

- **Embeddings or a vector store for search.** — Adds a model dependency and a second store; ranked full-text covers the common queries. Revisit after token-capped results.
- **A compare-and-swap method on store.Store for claiming.** — Nine storage plugins would each need it and pass storetest; the lock file plus re-read is enough for agents sharing a checkout.
- **Making `core` the default tier for every `mcp` caller.** — Would silently remove tools from existing library and server users.

## Design

### 1. Ranked transcript search

- Enricher `search-text` (transcript_lines, on by default): plain text of the line (user and assistant text blocks, tool name and truncated input, tool result text), secrets masked, capped at 16 KB, stored at `enrichments.search-text`.
- `session.Store.SearchTranscripts(ctx, TextQuery{AccountID, Query, SessionIDs, Limit}) ([]Doc, error)`: `session_id`, `seq`, `score`, `text` of each hit, best first. Mongo: text index on `enrichments.search-text` (`default_language: none`), `$text` + `$meta: textScore`. Memory store: term-frequency score. Remote: `POST /api/v1/session/search-transcripts` in `server/sessionapi` and `session/remotesession`. A `sessiontest` case covers ranking, scoping and account isolation.
- `query.SearchTranscripts` scopes by session, git origin or project path, adds the session's project and repository, and cuts a snippet around the first match.
- MCP tool `search_transcripts` (also on the server's HTTPS `/mcp`), CLI `mind-palace session search`.

### 2. Queries over enrichment data

- `session.Store.FileTouches(ctx, FileQuery{AccountID, PathRe, SessionIDs, Limit})` over hook_events with `enrichments.file-tracker`, newest first; `session.Store.ErrorLines(ctx, ErrorQuery{AccountID, Types, SessionIDs, Limit})` over transcript_lines with `enrichments.error-flag.is_error`.
- `query.FileHistory` groups touches per session (first and last touch, operations, edit stats when `edit-diff-stats` ran); `query.FindErrors` returns error lines with their type and session.
- MCP tools `file_history` ("which sessions touched this file") and `find_errors`; transcript lines returned by `read_transcript` keep their `enrichments`.

### 3. Usage and cost

- Enricher `usage` (transcript_lines, assistant lines with `message.usage`): model, input, output, cache creation and cache read tokens, `message_id`, estimated `cost_usd` from a price table per model family (unknown model: no cost).
- `session.Store.UsageTotals(ctx, UsageQuery{AccountID, SessionIDs, Since, Until, GroupBy})` with GroupBy `session`, `model` or `day`, deduplicating by message id (Mongo aggregation; memory store in Go).
- `query.Usage`, MCP tool `usage`, CLI `mind-palace session usage`; `get_session_context` gains the session's totals.

### 4. Ready queue, dependency rules and claim

- A document's open dependencies: the targets of its `depends` links and the documents whose `blocks` links name it, of either kind, that are not complete, rejected or archived.
- `applyStatus` refuses `in_progress` and `complete` while dependencies are open (new error kind `blocked_by_dependency`, listing them); `force` overrides.
- Op `ready` (both kinds): documents in pending, validated or ready with no open dependency, by priority, order and id; filters for tag, milestone and unassigned.
- Op `claim` (stories, and plans when the kind has an assignee): sets the assignee and moves to in_progress in one save; refuses a document held by another assignee (`conflict`) or with open dependencies; the file store takes a lock file under its directory for the duration.

### 5. MCP tool tiers

- `ops.Op.Core`; core set: create, get, getContent, list, update, validate, setStatus, getTransitions, getCriteria, setCriterion, addCriterion, appendWorkLog, getProgress, setProgress, addPlanLink, addStoryLink, addSpec, setAssignee, ready, claim, getTemplate, getSchema, plus sync, getConfig and watch.
- `mind-palace mcp --tools core|full`, env `MIND_PALACE_MCP_TOOLS`; `.mcp.json` passes `--tools core`; skills and commands that name a tool use only core tools, which a test checks.
- A test measures the serialized `tools/list` size of both tiers; the README states both numbers.

### 6. Automatic priming

- `mind-palace context prime --hook` prints `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"…"}}` with the report cut to 8,000 characters (later sections trimmed first, with a marker) within a 3-second budget; it prints nothing and exits 0 on any failure.
- `hooks/session-start` runs it in local and remote mode for every source (startup, resume, clear, compact), unless `primeOnStart` is false or `CLUED_PRIME_ON_START=0`; everything else the script runs keeps stdout silent.

### Error Handling

Search, usage and file queries return an empty list, never an error, for an account with nothing indexed yet. A store that cannot run a query (remote adapter for a method the server lacks) answers ErrUnsupported and the tool reports it. Priming never fails a session start: any error prints nothing.

### Edge Cases

Lines captured before `search-text` existed are indexed by the next enrichment passes, oldest first, in batches. Assistant messages split over several lines count once. A dependency on a deleted document is reported as open with a note, so a typo cannot silently unblock work. A claim by the current assignee is idempotent.

## Considerations

- **Security:** The search index masks secrets; queries stay scoped by account like every other session query; `claim` cannot take a document from another assignee without force.
- **Performance:** One text index on transcript_lines; usage aggregation uses the session and created_at indexes; the prime report is capped in size and time; the core tier cuts the tool list the model reads every turn.

## Testing

Unit tests per enricher; `sessiontest` cases for SearchTranscripts, FileTouches, ErrorLines and UsageTotals (memory store always, Mongo in CI); remotesession conformance over httptest; service tests for dependency gates, ready ordering and concurrent claims; an MCP test for each tier's tool list; plugin tests run `hooks/session-start` and check the hook JSON.

## Scope

**In scope:**

- The six items above in the public module and plugin
- PROTOCOL.md and README updates for every new tool, op, flag and config key

**Out of scope:**

- Capture-time redaction, token-capped results, export, OpenTelemetry — Designed in their own plan
- clued-cloud web pages for the new queries — Cloud follow-up once the module is released
- Semantic (vector) search — Rejected for now, see above

---

# Part 2 — Implementation

**Tech Stack:** Go 1.26, MongoDB driver v2, the official MCP Go SDK, bash hooks.

## Constraints

- Every new session store method passes `session/sessiontest`; the remote adapter runs it over httptest
- Business rules in `service/` and `session/`; entrypoints only translate
- gofmt clean, go vet silent, `go test -race ./...` green with and without MongoDB

## Phase 1: MCP tool tiers

### 1.1: Core flag and --tools

**Files:**
- Modify: `mind-palace/ops/ops.go`
- Modify: `mind-palace/ops/documents.go`
- Modify: `mind-palace/server/mcpserver/mcpserver.go`
- Modify: `mind-palace/cli/cli.go`
- Modify: `.mcp.json`

**Depends on:** none

- [ ] **Step 1: Mark core ops and filter registration by tier** — Op.Core; mcpserver.Options.Tier; the tool loop skips non-core ops in the core tier.
- [ ] **Step 2: Flag, env and plugin wiring** — `mcp --tools core|full`, `MIND_PALACE_MCP_TOOLS`; `.mcp.json` passes `--tools core`.
- [ ] **Step 3: Tests** — Tool list per tier; every tool named in skills/ and commands/ is core; tools/list size of both tiers logged.

**Validation:** `cd mind-palace && go test ./server/mcpserver/ ./cli/ ./plugin/`

## Phase 2: Automatic priming

### 2.1: prime --hook and the SessionStart hook

**Files:**
- Modify: `mind-palace/cli/context.go`
- Modify: `hooks/session-start`
- Modify: `mind-palace/session/config.go`
- Modify: `PROTOCOL.md`
- Test: `mind-palace/plugin/`

**Depends on:** none

- [ ] **Step 1: Hook output mode with size and time caps**
- [ ] **Step 2: Call it from hooks/session-start in both modes, honouring primeOnStart**
- [ ] **Step 3: Plugin test: the hook prints valid SessionStart JSON, and nothing when disabled**

**Validation:** `cd mind-palace && go test ./cli/ ./plugin/`

## Phase 3: Queries over enrichment data

### 3.1: FileTouches and ErrorLines in the stores

**Files:**
- Modify: `mind-palace/session/store.go`
- Modify: `mind-palace/session/mongosession/mongosession.go`
- Modify: `mind-palace/session/memsession/memsession.go`
- Modify: `mind-palace/session/remotesession/remotesession.go`
- Modify: `mind-palace/server/sessionapi/sessionapi.go`
- Modify: `mind-palace/session/sessiontest/sessiontest.go`

**Depends on:** none

- [ ] **Step 1: Port methods, both stores, transport, conformance cases**

### 3.2: file_history and find_errors tools

**Files:**
- Modify: `mind-palace/session/query/query.go`
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`

**Depends on:** 3.1

- [ ] **Step 1: Query use cases, tools, keep enrichments on transcript lines**

## Phase 4: Ready queue, dependency rules and claim

### 4.1: Dependency gate

**Files:**
- Modify: `mind-palace/service/fields.go`
- Modify: `mind-palace/service/links.go`
- Modify: `mind-palace/service/errors.go`

**Depends on:** none

- [ ] **Step 1: Open dependencies of a document; refuse in_progress and complete; force overrides**

### 4.2: ready and claim ops

**Files:**
- Modify: `mind-palace/service/schedule.go`
- Modify: `mind-palace/ops/documents.go`
- Modify: `mind-palace/store/filestore/`

**Depends on:** 4.1

- [ ] **Step 1: ready ordering and filters**
- [ ] **Step 2: claim with the file-store lock and re-read check**
- [ ] **Step 3: Concurrency test: two claims, one winner**

## Phase 5: Ranked transcript search

### 5.1: search-text enricher and text index

**Files:**
- Create: `mind-palace/session/enrichers/search_text.go`
- Modify: `mind-palace/session/mongosession/mongosession.go`

**Depends on:** none

- [ ] **Step 1: Extract and mask text; text index**

### 5.2: SearchTranscripts across stores and the tool

**Files:**
- Modify: `mind-palace/session/store.go`
- Modify: `mind-palace/session/memsession/memsession.go`
- Modify: `mind-palace/session/remotesession/remotesession.go`
- Modify: `mind-palace/server/sessionapi/sessionapi.go`
- Modify: `mind-palace/session/query/query.go`
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`
- Modify: `mind-palace/cli/session.go`

**Depends on:** 5.1

- [ ] **Step 1: Port method, conformance, query, MCP tool and CLI**

## Phase 6: Usage and cost

### 6.1: usage enricher and price table

**Files:**
- Create: `mind-palace/session/enrichers/usage.go`

**Depends on:** none

- [ ] **Step 1: Extract usage per assistant line; estimate cost**

### 6.2: UsageTotals and the usage tool

**Files:**
- Modify: `mind-palace/session/store.go`
- Modify: `mind-palace/session/mongosession/mongosession.go`
- Modify: `mind-palace/session/memsession/memsession.go`
- Modify: `mind-palace/session/remotesession/remotesession.go`
- Modify: `mind-palace/server/sessionapi/sessionapi.go`
- Modify: `mind-palace/session/query/query.go`
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`

**Depends on:** 6.1

- [ ] **Step 1: Aggregation deduplicated by message id; usage tool; session context totals**

## Acceptance Criteria

- [ ] `search_transcripts` finds a session by a phrase spoken in it, best match first, scoped to the account
- [ ] `file_history` lists the sessions that touched a file; `find_errors` lists failing lines
- [ ] `usage` reports tokens and estimated cost per session, model or day, counting each assistant message once
- [ ] A story with an open dependency cannot start or complete without force; `story_ready` lists startable stories; two concurrent claims have one winner
- [ ] The plugin's mind-palace MCP server lists only core tools; `--tools full` lists all
- [ ] A new, resumed, cleared or compacted session starts with the prime report as context, under 8,000 characters; `primeOnStart: false` turns it off
- [ ] go test -race ./... passes with and without MIND_PALACE_TEST_MONGODB_URI
