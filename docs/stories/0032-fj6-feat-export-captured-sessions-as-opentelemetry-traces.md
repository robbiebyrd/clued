---
id: 0032-fj6
title: Export captured sessions as OpenTelemetry traces
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-14T03:13:00.892Z"
updated: "2026-10-14T04:54:43.763Z"
started: "2026-10-14T04:52:53.105Z"
completed: "2026-10-14T04:54:43.763Z"
tags:
  - opentelemetry
  - observability
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  plans:
    - ["0008-vvc", "included"]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: archived
---

# Export captured sessions as OpenTelemetry traces

## Problem Statement

Teams on Grafana, Langfuse or Phoenix cannot see clued's data, and Claude Code's own OTLP telemetry is not joined to it.

## Steps

### 1: Trace mapping

Session, turn, tool and subagent spans with usage attributes.

### 2: Exporter

OTLP HTTP to otelEndpoint from the daemon or server.

### 3: Ingest (moved)

OTLP logs and metrics on the clued server: moved to story 0034-chs, so this story closes with the export.

## Acceptance Criteria

- [x] VERIFY: An in-process OTLP collector stub receives one trace per session with a span per tool use

## Work Log

### 2026-10-14T04:52:53.314Z - session/otel maps a session (FullSession) to one trace: a root invoke_agent claude-code span, a turn span per UserPromptSubmit, execute_tool spans from PreToolUse to PostToolUse/PostToolUseFailure (errors as status 2), invoke_agent spans for subagents, gen_ai.usage.* per turn and session from the usage enrichment (each message once). Deviation: spans are sent as OTLP/HTTP JSON by a small exporter instead of the OpenTelemetry SDK (no new dependencies; any OTLP collector accepts it), and a session's trace goes out when it ends (the daemon 5 s after SessionEnd, the relay right after its catch-up, session otel-export by hand) rather than turn by turn; deterministic ids and a per-session state file send each span at most once, so a resumed session adds only its new spans. Config otelEndpoint (CLUED_OTEL_ENDPOINT) and otelHeaders (never logged). Step 3, ingest, moved to 0034-chs. Tests: an in-process collector stub receives one trace per session with a span per tool use, on both stores; the relay and the daemon send at SessionEnd.
