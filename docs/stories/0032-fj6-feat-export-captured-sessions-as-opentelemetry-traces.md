---
id: 0032-fj6
title: Export captured sessions as OpenTelemetry traces
type: feat
status: pending
priority: "3"
effort: L
created: "2026-10-14T03:13:00.892Z"
updated: "2026-10-14T03:13:00.892Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Export captured sessions as OpenTelemetry traces

## Problem Statement

Teams on Grafana, Langfuse or Phoenix cannot see clued's data, and Claude Code's own OTLP telemetry is not joined to it.

## Steps

### 1: Trace mapping

Session, turn, tool and subagent spans with usage attributes.

### 2: Exporter

OTLP HTTP to otelEndpoint from the daemon or server.

### 3: Ingest (later)

OTLP logs and metrics on the clued server.

## Acceptance Criteria

- [ ] VERIFY: An in-process OTLP collector stub receives one trace per session with a span per tool use

## Work Log
