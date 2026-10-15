---
id: 0052-jf1
title: 'P9: config-pull pulls the redaction keys and enforces managed keys'
purpose: 'Lets clued-cloud workspaces have client-side enforcement instead of: server-side enforcement only, which still holds.'
type: feat
status: complete
priority: "3"
created: "2026-10-15T19:09:10.538Z"
updated: "2026-10-15T19:09:20.620Z"
started: "2026-10-15T19:09:20.540Z"
completed: "2026-10-15T19:09:20.620Z"
tags:
  - cli
  - config
  - redaction
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
      - mind-palace/cli/session_login.go
      - mind-palace/cli/session_login_test.go
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

# P9: config-pull pulls the redaction keys and enforces managed keys

## Problem Statement

Public-module prerequisite P9 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** `config-pull` pulls the redaction keys and enforces `managed`.

**Needed by:** client-side enforcement.

**Without it:** server-side enforcement only, which still holds.

`session config-pull` also writes `redactAtCapture`, `redactPatterns` and `excludePaths`. Keys `GET /api/v1/me/config` lists as managed (`managed` / `managedBy`, inside `client_config` or beside it) overwrite a differing local value with a stderr note naming the manager; the result reports the managed and overridden keys.

**Impact:** Without it: server-side enforcement only, which still holds.

## Steps

### 1: Implement and test

`session config-pull` also writes `redactAtCapture`, `redactPatterns` and `excludePaths`. Keys `GET /api/v1/me/config` lists as managed (`managed` / `managedBy`, inside `client_config` or beside it) overwrite a differing local value with a stderr note naming the manager; the result reports the managed and overridden keys.

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] config-pull writes `redactAtCapture`, `redactPatterns` and `excludePaths` from the server
- [x] A managed key overwrites a differing local value, notes the manager on stderr and is reported as overridden
- [x] VERIFY: cd mind-palace && go test -race -run 'TestSessionConfigPullWritesRedactionKeysAndEnforcesManaged' ./cli/

## Files

- `mind-palace/cli/session_login.go`
- `mind-palace/cli/session_login_test.go`

## Work Log

### 2026-10-15T19:09:14.223Z - Implemented and tested on claude/m07-phase-6 (PR #49) in 38af8f3; documented in 5b96627.
