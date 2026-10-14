---
id: 0029-h4q
title: Report token usage and estimated cost per session, model and day
type: feat
status: in_progress
priority: "2"
effort: M
created: "2026-10-14T03:13:00.705Z"
updated: "2026-10-14T03:58:19.758Z"
started: "2026-10-14T03:58:19.396Z"
tags:
  - session
  - usage
  - mcp
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
    - ["0007-w5t", "included"]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
---

# Report token usage and estimated cost per session, model and day

## Problem Statement

clued stores every assistant line with its `message.usage`, yet no tool reports tokens or cost; the 2026-09-23 usage design was never ported to Go.

## Steps

### 1: usage enricher

Tokens by type, model, message id and an estimated cost per assistant line from a price table.

### 2: UsageTotals

Store aggregation by session, model or day, deduplicated by message id, in both stores, the transport and the remote adapter.

### 3: Tool and CLI

MCP `usage`, `mind-palace session usage`, totals in `get_session_context`.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/...
- [x] VERIFY: An assistant message split over several lines counts once
- [x] VERIFY: `usage` groups by session, model and day

## Work Log

### 2026-10-14T03:58:19.758Z - Added the usage enricher (token counters, model, message id, UTC day per assistant line; synthetic lines skipped), UsageTotals on the session.Store port (Mongo two-stage $group, in-memory equivalent, POST /usage-totals, remote adapter, sessiontest case), the usage MCP tool, session usage and a usage block in get_session_context. Deviation: the cost is computed when queried, not stored by the enricher, so the config's new modelPrices key (and later price changes) apply to past sessions; models outside the built-in Claude 3/4 table report tokens without cost and are listed in unpriced_models. The enrichment loop covers backfilled lines too. Also fixed: the in-memory store kept enricher result structs as Go values, so file_history and find_errors found nothing under --store memory; it now keeps their JSON document, as MongoDB keeps their BSON. JSONTimes now promotes embedded struct fields as encoding/json does.
