---
id: 0050-9tc
title: 'P7: project_slug on session records and usage of selected session ids'
purpose: 'Lets clued-cloud workspaces have efficient rollup and archive instead of: post-filtering up to 500 sessions per member.'
type: feat
status: complete
priority: "3"
created: "2026-10-15T19:09:10.392Z"
updated: "2026-10-15T19:09:20.017Z"
started: "2026-10-15T19:09:19.923Z"
completed: "2026-10-15T19:09:20.017Z"
tags:
  - session
  - usage
  - workspaces
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
      - mind-palace/session/model.go
      - mind-palace/session/memsession/memsession.go
      - mind-palace/session/mongosession/mongosession.go
      - mind-palace/session/query/usage.go
      - mind-palace/session/sessiontest/sessiontest.go
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

# P7: project_slug on session records and usage of selected session ids

## Problem Statement

Public-module prerequisite P7 of the workspaces design ([spec](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/superpowers/specs/2026-10-15-workspaces-design.md), table "Public-module prerequisites"), filed from clued-cloud plan [0003-m07](https://github.com/robbiebyrd/clued-cloud/blob/main/docs/plans/0003-m07-impl-monetization-free-core-paid-cloud.md) section 6.1 step 1.

**Change:** `project_slug` on session records (indexed); `UsageArgs.SessionIDs`.

**Needed by:** efficient rollup and archive.

**Without it:** post-filtering up to 500 sessions per member.

`session.Session.ProjectSlug` (`project_slug`) is written by both session stores, derived from `git_origin` with `session.ProjectSlugOf` when not given, and indexed in MongoDB on (account_id, project_slug, last_seen). `SessionQuery.ProjectSlugs` filters `FindSessions`; `query.UsageArgs.SessionIDs` totals selected sessions in one query.

**Impact:** Without it: post-filtering up to 500 sessions per member.

## Steps

### 1: Implement and test

`session.Session.ProjectSlug` (`project_slug`) is written by both session stores, derived from `git_origin` with `session.ProjectSlugOf` when not given, and indexed in MongoDB on (account_id, project_slug, last_seen). `SessionQuery.ProjectSlugs` filters `FindSessions`; `query.UsageArgs.SessionIDs` totals selected sessions in one query.

### 2: Document

PROTOCOL.md and the docs describe the change (commit 5b96627).

## Acceptance Criteria

- [x] Both session stores store and return `project_slug`, derived from `git_origin`, and filter `FindSessions` by `ProjectSlugs` (sessiontest conformance case)
- [x] `UsageArgs.SessionIDs` restricts usage totals to the given sessions
- [x] VERIFY: cd mind-palace && go test -race -run 'TestUsageOfSelectedSessionIDs' ./session/query/
- [x] VERIFY: cd mind-palace && go test -race ./session/memsession/ ./session/mongosession/

## Files

- `mind-palace/session/model.go`
- `mind-palace/session/memsession/memsession.go`
- `mind-palace/session/mongosession/mongosession.go`
- `mind-palace/session/query/usage.go`
- `mind-palace/session/sessiontest/sessiontest.go`

## Work Log

### 2026-10-15T19:09:13.516Z - Implemented and tested on claude/m07-phase-6 (PR #49) in f9604cf; documented in 5b96627.
