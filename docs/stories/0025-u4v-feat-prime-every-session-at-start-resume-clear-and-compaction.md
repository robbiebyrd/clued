---
id: 0025-u4v
title: Prime every session at start, resume, clear and compaction
type: feat
status: ready
priority: "1"
effort: S
created: "2026-10-14T03:13:00.479Z"
updated: "2026-10-14T03:13:00.479Z"
tags:
  - prime
  - hooks
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
---

# Prime every session at start, resume, clear and compaction

## Problem Statement

The prime report (branch, PR, plans, stories, last session, build tool) is produced only when the user or agent asks for it, so most sessions start without it and lose it again after compaction. Claude Code accepts `additionalContext` from a SessionStart hook and re-runs SessionStart with source `compact`, so the plugin can inject a capped report every time.

## Steps

### 1: Hook output mode

`mind-palace context prime --hook` prints SessionStart hook JSON with the report capped at 8,000 characters and 3 seconds; prints nothing on failure.

### 2: SessionStart hook

`hooks/session-start` runs it in local and remote mode for every source unless `primeOnStart` is false or `CLUED_PRIME_ON_START=0`, with every other command silent on stdout.

### 3: Tests and docs

Plugin tests for the JSON, the cap and the switch; PROTOCOL.md and README.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test ./cli/ ./plugin/
- [ ] VERIFY: A SessionStart payload with source compact gets the report as additionalContext
- [ ] VERIFY: primeOnStart false makes the hook print nothing

## Work Log
