---
id: 0035-ngb
title: Record the model in use on every transcript line and on the session
purpose: Every captured line names the model that was serving the session when it was recorded, so usage, activity and limit data can be read per model.
type: feat
status: pending
priority: "2"
effort: S
created: "2026-10-14T04:28:51.382Z"
updated: "2026-10-14T04:28:51.382Z"
tags:
  - sessions
  - enrichment
  - model
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Record the model in use on every transcript line and on the session

## Problem Statement

Only assistant lines carry `message.model`, and only the `usage` enricher reads it. User prompts, tool results, hook events and the session document itself do not say which model was in use, and a session that switched models mid-way (`/model`, a fallback) cannot be read per model. The status line sample (`LimitsSampled`) carries the model too, but only while the status line is on.

**Impact:** Queries that group by model are limited to assistant lines, and a session's current model is not available to find_sessions, the prime report or a live view.

## Steps

### 1: A model enricher that carries the model forward

Add `session/enrichers/model.go` (`model`, collection `transcript_lines`, enabled). An assistant line stores its own `message.model`; any other line stores the model of the nearest preceding assistant line of the session (looked up through `session.Lookup`, which gains `LastModelBefore(ctx, sessionID, seq)`), with `source: line | carried | unknown` so a consumer can tell a stated model from an inferred one. Hook events get the same treatment on `hook_events` (`hook-model`), from the latest assistant line of the session at the time of the event.

### 2: The session's current model

The daemon and `hook relay` set `model` and `model_changed_at` on the session document whenever an assistant line names a model that differs from the stored one (`session.Store.SetModel`), and a `LimitsSampled` or `PreModelSwitch`/`PostModelSwitch` hook event updates it the same way. `FindSessions` projects `model` and `get_session_context` reports it; `prime_context`'s SESSION section names it.

### 3: Model history

Each change appends a `ModelChanged` hook event (`session_id`, `from`, `to`, `seq`, `at`) so a session's model switches are a queryable sequence.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/... ./server/sessionapi/
- [ ] A transcript where the model changes mid-session yields lines whose `enrichments.model` follows the change, user lines carrying the model of the preceding assistant line, and a `ModelChanged` event at the switch
- [ ] `find_sessions` and `get_session_context` show the session's current model
- [ ] PROTOCOL.md documents the enrichment, the session fields and the event

## Files

- `mind-palace/session/enrichers/model.go` - the enricher
- `mind-palace/session/store.go` - `SetModel`, `LastModelBefore` on Lookup, the projection
- `mind-palace/session/ingest/ingest.go` - model tracking on ingest
- `mind-palace/session/daemon/daemon.go` - model tracking while tailing
- `PROTOCOL.md` - sections 4.2, 6.1 and 7.3

## Work Log
