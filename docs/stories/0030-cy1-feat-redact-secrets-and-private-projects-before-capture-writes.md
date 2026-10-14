---
id: 0030-cy1
title: Redact secrets and private projects before capture writes
type: feat
status: complete
priority: "2"
effort: L
created: "2026-10-14T03:13:00.761Z"
updated: "2026-10-14T04:54:43.515Z"
started: "2026-10-14T04:30:07.249Z"
completed: "2026-10-14T04:54:43.515Z"
tags:
  - privacy
  - session
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
    status: complete
---

# Redact secrets and private projects before capture writes

## Problem Statement

Raw transcripts, secrets included, are written to the store and synced to the server; the opt-in redactor keeps the raw line beside the masked one, and there is no exclusion, forget or retention.

## Steps

### 1: Design review

Settle plan 0008-vvc's open questions.

### 2: Redaction in ingest

session/redact applied before every write; config keys.

### 3: Exclude, forget, retention

excludePaths, session forget and forget_session, retentionDays TTL.

## Acceptance Criteria

- [x] VERIFY: No stored document contains a seeded secret after capture
- [x] VERIFY: A session under an excluded path is never stored

## Work Log

### 2026-10-14T04:30:18.148Z - Open questions settled with the plan's defaults (recorded in the plan): an absent redactAtCapture means off, /clued-setup writes true; spans use GenAI conventions. session/redact is a session.Store decorator cli wraps around every store it opens (daemon, tailer, backfill, relay, server run), so every write path redacts: rules private-key, api-key, aws-key, jwt, github-token, slack-token, <private> spans and redactPatterns, recorded under redacted on hook events, lines and blobs. Deviation: e-mail addresses are not masked at capture (not secrets; they would break search and restore); redactPatterns covers them. excludePaths drops a session's record, events, lines and blobs, learned from the cwd of the record, events and lines. Store.DeleteSessions with POST /sessions/delete backs session forget and forget_session (destructive, not offered by the server /mcp). retentionDays creates TTL indexes in MongoDB. Tests: capture through ingest and catchup holds no seeded secret; an excluded session stores nothing; the relay sends neither the secret nor an excluded session's data.
