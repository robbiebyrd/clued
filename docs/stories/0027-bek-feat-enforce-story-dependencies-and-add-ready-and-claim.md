---
id: 0027-bek
title: Enforce story dependencies and add ready and claim
type: feat
status: complete
priority: "1"
effort: M
created: "2026-10-14T03:13:00.587Z"
updated: "2026-10-14T03:59:05.480Z"
started: "2026-10-14T03:31:28.439Z"
completed: "2026-10-14T03:59:05.480Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./service/ ./ops/ ./store/...
- [x] VERIFY: Starting a story whose dependency is open fails with the dependency listed, and succeeds with force
- [x] VERIFY: Two concurrent claims of one story produce exactly one winner

## Work Log

### 2026-10-14T03:40:05.282Z - Dependency gate in applyStatus, following the relations' documented meaning: moving to in_progress needs every depends target (plan or story) finished (complete, rejected or archived); moving to complete also needs every document whose blocks link names this one finished. New error kind BlockedByDependency (409, exit 15) lists them; force overrides; auto-completion from progress waits while one is open. New ops ready (both kinds; statuses that may move to in_progress, no open depends, by priority/order/id; tag, milestone, unassigned, limit), dependencies (both kinds) and claim (stories): assignee + in_progress in one write, Conflict for another holder, idempotent for the holder; serialised by a palace mutex and, on the file store, store.Locker (an O_EXCL lock file under the system temp dir, stale after 30 s), then re-read. CLI ready, dependencies, claim; ready and claim are in the core tier (now 52 tools / 29,461 bytes; full 104 / 80,553). Tests: TestDependenciesGateStartAndCompletion, TestDependsOnAPlan, TestReadyListsStartableWorkInPriorityOrder, TestClaimHasOneWinner (8 concurrent claims, one winner), filestore lock tests. Tried on this repo: story dependencies 0033-zmt lists 0028-p8p.
