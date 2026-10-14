---
id: 0008-vvc
title: Capture-time privacy, token-capped results, export and OpenTelemetry
type: dsgn
status: complete
priority: "2"
effort: XL
created: "2026-10-14T03:12:18.334Z"
updated: "2026-10-14T14:46:28.948Z"
completed: "2026-10-14T14:46:28.948Z"
tags:
  - session
  - privacy
  - mcp
  - export
  - opentelemetry
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
    - docs/superpowers/specs/2026-09-23-session-portability-design.md
  plans:
    - ["0007-w5t", "depends"]
progress:
  "7":
    status: complete
    stories:
      - 0030-cy1
  "8":
    status: complete
    stories:
      - 0033-zmt
  "9":
    status: complete
    stories:
      - 0031-nwf
  "10":
    status: complete
    stories:
      - 0032-fj6
      - 0034-chs
---

# Capture-time privacy, token-capped results, export and OpenTelemetry

## Summary

**Goal:** Secrets and private projects never reach the store, session tools return what fits a token budget, any session or project exports to Markdown, HTML or JSONL, and captured sessions flow to and from OpenTelemetry tooling.

**Problem:** Raw transcripts, secrets included, are written to MongoDB or the server, and the only redaction is an opt-in enricher that keeps the raw line beside the masked copy; there is no exclusion, forget or retention. get_full_session and read_transcript return everything at once, which an agent cannot afford on a long session. The public module has no export (clued-cloud has a zip export, local and self-hosted users have nothing). Teams that standardise on OpenTelemetry (Grafana, Langfuse, Phoenix) cannot see clued's data, and Claude Code's native OTLP telemetry is not joined to it.

**Approach:** Four designs that share the session store and the query service: redaction and exclusion in the ingest path before any write, a budget and a two-step search-then-fetch shape for the session tools, an exporter over the query service with one Markdown/HTML renderer, and an OTLP exporter (spans and logs from hook events and usage) plus an optional OTLP ingest on the server.

---

# Part 1 — Design

## Current State

- Ingest writes hook events, transcript lines, subagent lines and blobs as received ([session/ingest](mind-palace/session/ingest/), [session/catchup](mind-palace/session/catchup/)). `privacy-redact` and `privacy-redact-memory` are enrichers, off by default, that write a masked copy under `enrichments` and leave the raw document ([session/enrichers/privacy_redact.go](mind-palace/session/enrichers/privacy_redact.go)).
- Session tools: `get_full_session` returns the full join; `read_transcript` pages by offset and limit (max 500 lines) with no size bound ([session/query](mind-palace/session/query/query.go)).
- Restore writes Claude Code's own JSONL layout ([session/restore](mind-palace/session/restore/restore.go)); there is no human-readable export. clued-cloud renders transcript lines for its web pages in its own code.
- Nothing in the module speaks OTLP. Claude Code exports metrics, events and beta traces over OTLP when `CLAUDE_CODE_ENABLE_TELEMETRY=1`.
- Plan 0007-w5t adds a `search-text` enricher, ranked search and usage, which components 2 and 4 build on.

## Decisions

- **Redact in ingest, before the write, on by default for new configurations (`redactAtCapture`), with the privacy-redact patterns plus `<private>…</private>` spans.** — A masked copy next to the raw line protects nothing; redacting before the write means the store, the server and every backup never hold the secret. Existing installs keep their behaviour until they opt in.
- **`excludePaths` (globs over cwd and project path) drop a session's events at the hook, and `session forget` deletes by session, project or age; `retentionDays` sets a TTL index.** — Users need to keep whole repositories out, take a mistake back and bound how long transcripts live; Mongo TTL indexes make retention free at query time.
- **Every session tool takes `max_tokens` (estimated as characters / 4) and returns `truncated` and a `next` cursor; search tools return ids and snippets, and a new `get_lines(session_id, seqs)` fetches the lines.** — The two-step shape (index, then detail) is what lets claude-mem-style retrieval stay cheap; a cursor keeps long reads resumable without the agent guessing offsets.
- **One renderer in the public module (`session/render`) turns lines into Markdown and HTML; the exporter writes Markdown, HTML, JSONL (Claude Code's layout, as restore does) or a zip of many sessions; the export is redacted by default.** — clued-cloud can then reuse the renderer instead of its own copy, and a shared transcript never leaks what capture-time redaction would have removed.
- **OpenTelemetry export first: one trace per session, a span per prompt turn and a child span per tool use, GenAI semantic-convention attributes, usage as attributes, sent with the OTLP HTTP exporter to `otelEndpoint`; ingest of Claude Code's own OTLP on the clued server second.** — Export lets Grafana, Langfuse and Phoenix users see clued data with no new storage; ingest needs a receiver and a join on session and prompt ids, so it follows once export proves the mapping.

### Rejected alternatives

- **Keep redaction as an enricher and only flip its default** — The raw line would still be stored and synced to the server.
- **Token counting with a real tokenizer** — A model-specific tokenizer is a heavy dependency for a budget that only needs to be approximately right.
- **Embedding the OpenTelemetry Collector** — Users who want a collector already run one; the SDK exporter is enough to reach it.

## Design

### 7. Redaction and exclusion at capture

- `session/redact`: the privacy-redact patterns as a library, plus `<private>` spans, applied by `session/ingest` and `session/catchup` to hook event inputs and outputs, transcript and subagent lines, blobs and memory before the write. A redacted document carries `redacted: true` and the rule names that fired.
- Config: `redactAtCapture` (default true for configs written by `/clued-setup` from this release on), `redactPatterns` (extra regexes), `excludePaths`.
- `mind-palace session forget --session ID | --project RE | --before DATE`, and MCP `forget_session` (local only), use a new `Store.DeleteSessions(ctx, accountID, q)`; `retentionDays` adds a TTL index on `created_at` in every collection.
- The existing enrichers stay for data captured before the switch.

### 8. Token-capped results

- A budget helper in `session/query` stops adding items when the estimated tokens of the JSON exceed `max_tokens` (default 4,000 for search tools, 8,000 for reads), returning `truncated: true` and `next` (an opaque cursor: session, seq, offset).
- `search_transcripts`, `search_commands`, `search_memory`, `find_errors` return compact hits (ids, snippet, time); `get_lines` fetches full lines by seq; `get_full_session` gains `sections` (`transcript`, `subagents`, `hook_events`, `blobs`) and honours the budget per section.
- Tool descriptions state the default budget so the model can raise it deliberately.

### 9. Export

- `session/render`: Markdown and self-contained HTML (no scripts, inline CSS) of a session: header (project, repository, branch, times, usage), turns with roles, tool calls collapsed to name and summary, errors marked.
- `mind-palace session export --session ID | --project RE | --since DATE --format md|html|jsonl|zip --out PATH [--raw]`; MCP `export_session` (local only, writes a file and returns its path).
- `session import PATH` reads a JSONL or zip export back into the store, so an export is also a backup.

### 10. OpenTelemetry

- Export: `session/otel` maps a session's hook events to a trace (UserPromptSubmit opens a turn span, PreToolUse/PostToolUse a tool span with name, duration and outcome, SubagentStop a subagent span) and usage to `gen_ai.usage.*` attributes; the daemon (local) or server (remote) sends finished turns to `otelEndpoint` with `otelHeaders`, batched, at most once per turn.
- Ingest (second step): the clued server accepts OTLP/HTTP logs and metrics at `/v1/logs` and `/v1/metrics` with the bearer token, keeps Claude Code's `api_request` and `tool_result` events in a `otel_events` collection keyed by `session.id` and `prompt.id`, and `get_session_context` reports them beside the hook data.

### Error Handling

Redaction never drops an event: a pattern that fails to compile is reported at config load and skipped. An OTLP endpoint that is down queues spans in memory up to a bound, then drops the oldest with a counter in the daemon log; capture never waits on it. An export of a missing session is a not-found error; a partial zip lists the sessions that failed.

### Edge Cases

Secrets split across two transcript lines are not caught by per-line patterns; documented. A `<private>` span left unclosed redacts to the end of the line. Forgetting a session that a restore wrote back is a store delete only; local JSONL files are the user's. Budgets smaller than one item still return that item, truncated, so a call always makes progress.

## Considerations

- **Security:** Redaction before the write is the main change: the store and the server stop being the copy of last resort for secrets. Export redacts by default and needs `--raw` to include originals. OTLP headers are secrets and are never logged; ingest requires the same bearer token as the session API.
- **Performance:** Redaction adds regex passes in the hook path: patterns are compiled once and lines over 256 KB are masked in chunks; the 5-second relay budget is the bound to test against. OTLP export batches per turn.

## Testing

Golden-file tests for the renderer; ingest tests that no stored document contains a seeded secret; budget tests per tool; an OTLP test against an in-process collector stub; sessiontest cases for DeleteSessions and TTL; plugin tests for excludePaths in both modes.

## Scope

**In scope:**

- Designs for items 7–10; a story each, implemented after plan 0007-w5t

**Out of scope:**

- Encryption at rest of the session store — A store and deployment concern (Mongo encryption, disk encryption), not capture
- clued-cloud UI for export and OTLP settings — Cloud follow-up

## Open Questions

- [x] Should `redactAtCapture` also default to true for existing configs on upgrade, with a one-time notice? — robbiebyrd. Decided for now (2026-10-14): no. An absent key means off, so existing configs keep their behaviour; `/clued-setup` writes `true`. No notice; revisit if you want upgrades switched on.
- [x] Which OpenTelemetry semantic conventions should spans follow: GenAI (`gen_ai.*`) or OpenInference, which Phoenix expects? — robbiebyrd. Decided for now (2026-10-14): GenAI, the OpenTelemetry standard that Grafana and Langfuse read; OpenInference attributes can be added beside them later.
