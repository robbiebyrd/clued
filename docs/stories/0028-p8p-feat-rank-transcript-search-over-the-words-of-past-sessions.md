---
id: 0028-p8p
title: Rank transcript search over the words of past sessions
type: feat
status: ready
priority: "1"
effort: M
created: "2026-10-14T03:13:00.645Z"
updated: "2026-10-14T03:13:00.645Z"
tags:
  - session
  - search
  - mcp
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

# Rank transcript search over the words of past sessions

## Problem Statement

Search covers Bash commands, memory text and project paths by unranked regex; the words of a conversation cannot be searched at all, so finding the session that discussed a topic means paging transcripts.

## Steps

### 1: search-text enricher

Plain text of each transcript line, secrets masked, capped at 16 KB, under `enrichments.search-text`.

### 2: SearchTranscripts

Store method with a Mongo text index and textScore ranking, a term-frequency memory implementation, transport and remote adapter, sessiontest case.

### 3: Query, tool and CLI

`query.SearchTranscripts` with scope and snippets; MCP `search_transcripts` (also on the server's /mcp); `mind-palace session search`.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/...
- [ ] VERIFY: A phrase from a past session finds it, best match first, and never another account's
- [ ] VERIFY: Indexed text holds no secret that the privacy-redact patterns match

## Work Log
