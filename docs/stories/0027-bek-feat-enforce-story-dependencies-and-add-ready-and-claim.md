---
id: 0027-bek
title: Enforce story dependencies and add ready and claim
type: feat
status: ready
priority: "1"
effort: M
created: "2026-10-14T03:13:00.587Z"
updated: "2026-10-14T03:13:00.587Z"
tags:
  - plans-and-stories
  - service
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
  "4":
    status: pending
---

# Enforce story dependencies and add ready and claim

## Problem Statement

Plans and stories can link with `depends` and `blocks`, but the service never checks the links: a story can start while what it depends on is open, nothing lists the work that can start now, and two agents can take the same story.

## Steps

### 1: Dependency gate

Open dependencies of a document (depends targets, documents whose blocks links name it, either kind); refuse in_progress and complete while any is open; force overrides; new error kind.

### 2: ready op

Documents in pending, validated or ready with no open dependency, by priority, order and id; tag, milestone and unassigned filters.

### 3: claim op

Set the assignee and move to in_progress in one save; refuse another assignee's document; file-store lock and re-read check.

### 4: Tests and docs

Service tests, a concurrent claim test, CLI and MCP exposure, PROTOCOL.md.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./service/ ./ops/ ./store/...
- [ ] VERIFY: Starting a story whose dependency is open fails with the dependency listed, and succeeds with force
- [ ] VERIFY: Two concurrent claims of one story produce exactly one winner

## Work Log
