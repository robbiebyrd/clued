---
id: 0030-cy1
title: Redact secrets and private projects before capture writes
type: feat
status: pending
priority: "2"
effort: L
created: "2026-10-14T03:13:00.761Z"
updated: "2026-10-14T03:13:00.761Z"
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
    status: pending
  "2":
    status: pending
  "3":
    status: pending
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

- [ ] VERIFY: No stored document contains a seeded secret after capture
- [ ] VERIFY: A session under an excluded path is never stored

## Work Log
