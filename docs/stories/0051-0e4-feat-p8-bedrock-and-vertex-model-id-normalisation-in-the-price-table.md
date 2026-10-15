---
id: 0051-0e4
title: 'P8: Bedrock and Vertex model-id normalisation in the price table'
purpose: 'Lets clued-cloud workspaces have estimated cost for those users instead of: listed as unpriced.'
type: feat
status: complete
priority: "4"
created: "2026-10-15T19:09:10.469Z"
updated: "2026-10-15T19:09:20.327Z"
started: "2026-10-15T19:09:20.250Z"
completed: "2026-10-15T19:09:20.327Z"
tags:
  - session
  - usage
  - pricing
  - prerequisite
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: https://github.com/robbiebyrd/clued.git
    pull_request: https://github.com/robbiebyrd/clued/pull/49
    files:
      - mind-palace/session/usage.go
      - mind-palace/session/usage_test.go
  specs:
    - https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md
  web:
    plan-0003-m07: https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md
    spec: https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md
progress:
  "1":
    status: complete
  "2":
    status: complete
---

# P8: Bedrock and Vertex model-id normalisation in the price table

## Problem Statement

Public-module prerequisite P8 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** Bedrock and Vertex model-id normalisation in the price table.

**Needed by:** estimated cost for those users.

**Without it:** listed as unpriced.

`session.NormalizeModelID` strips Bedrock ARNs, regional and global inference-profile prefixes, `anthropic.` and Vertex publisher paths, and trailing snapshot, version and `[1m]` context tags; `PriceFor` looks the normalised id up (overrides match either form).

**Impact:** Without it: listed as unpriced.

## Steps

### 1: Implement and test

`session.NormalizeModelID` strips Bedrock ARNs, regional and global inference-profile prefixes, `anthropic.` and Vertex publisher paths, and trailing snapshot, version and `[1m]` context tags; `PriceFor` looks the normalised id up (overrides match either form).

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] Bedrock, Vertex and `[1m]` model ids normalise to the first-party id and get a price
- [x] VERIFY: cd mind-palace && go test -race -run 'TestNormalizeModelID' ./session/

## Files

- `mind-palace/session/usage.go`
- `mind-palace/session/usage_test.go`

## Work Log

### 2026-10-15T19:09:13.850Z - Implemented and tested on claude/m07-phase-6 (PR #49) in a2c8cb7; documented in 5b96627.
