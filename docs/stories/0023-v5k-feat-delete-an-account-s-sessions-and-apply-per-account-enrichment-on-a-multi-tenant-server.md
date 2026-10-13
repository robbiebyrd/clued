---
id: 0023-v5k
title: Delete an account's sessions and apply per-account enrichment on a multi-tenant server
type: feat
status: in_progress
priority: "1"
effort: S
created: "2026-10-13T18:49:06.353Z"
updated: "2026-10-13T18:49:06.353Z"
started: "2026-10-13T18:49:06.353Z"
tags:
  - sessions
  - enrichment
  - cloud
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
---

# Delete an account's sessions and apply per-account enrichment on a multi-tenant server

## Problem Statement

A multi-tenant server built on this module (the clued cloud) cannot delete one account's captured sessions, and its enrichment loop runs the server's enricher set for every account alike.

**Impact:** Account deletion would leave the account's transcripts behind, and an account's enricher settings apply only to its clients' local behaviour.

## Steps

### 1: DeleteAccount

`session.Store.DeleteAccount(ctx, accountID)` removes every document stamped with the account; memsession and mongosession implement it, remotesession answers ErrUnsupported; the conformance suite covers it.

### 2: Enrichment policy

`enrich.Policy` decides per document's account whether an enricher runs (refused documents are marked skipped); `LoopWithPolicy`, `PassWithPolicy`, `session.Allows`, `session.Registered` and `CluedOptions.EnrichPolicy`.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/... ./server/
- [ ] VERIFY: MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./session/mongosession/ (CI)

## Work Log
