---
id: 0034-chs
title: Ingest Claude Code's OTLP telemetry on the clued server
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-14T04:52:38.573Z"
updated: "2026-10-14T04:52:38.573Z"
tags:
  - otel
  - server
  - session
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  stories:
    - ["0032-fj6", "depends"]
  plans:
    - ["0008-vvc", "included"]
progress:
  "1":
    status: pending
  "2":
    status: pending
---

# Ingest Claude Code's OTLP telemetry on the clued server

## Problem Statement

Claude Code exports its own metrics and events (api_request, tool_result) over OTLP when CLAUDE_CODE_ENABLE_TELEMETRY=1, but clued cannot receive them, so get_session_context cannot report them beside the hook data. Split from 0032-fj6, whose export landed first, as plan 0008-vvc planned.

## Steps

### 1: OTLP receiver

The clued server accepts OTLP/HTTP logs and metrics at /v1/logs and /v1/metrics with the bearer token, scoped to the token's account.

### 2: Join

Keep api_request and tool_result events in an otel_events collection keyed by session.id and prompt.id; get_session_context reports them beside the hook data.

## Acceptance Criteria

- [ ] VERIFY: An OTLP log export from a client stub is stored under the token's account and shows in get_session_context

## Work Log
