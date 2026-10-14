---
id: 0029-h4q
title: Report token usage and estimated cost per session, model and day
type: feat
status: ready
priority: "2"
effort: M
created: "2026-10-14T03:13:00.705Z"
updated: "2026-10-14T03:13:00.705Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
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

- [ ] VERIFY: cd mind-palace && go test -race ./session/...
- [ ] VERIFY: An assistant message split over several lines counts once
- [ ] VERIFY: `usage` groups by session, model and day

## Work Log
