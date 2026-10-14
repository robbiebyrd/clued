---
id: 0028-p8p
title: Rank transcript search over the words of past sessions
type: feat
status: in_progress
priority: "1"
effort: M
created: "2026-10-14T03:13:00.645Z"
updated: "2026-10-14T03:46:47.084Z"
started: "2026-10-14T03:40:53.263Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./session/...
- [x] VERIFY: A phrase from a past session finds it, best match first, and never another account's
- [x] VERIFY: Indexed text holds no secret that the privacy-redact patterns match

## Work Log

### 2026-10-14T03:46:47.084Z - Added the search-text enricher (readable line text, credentials masked, 16 KB cap), SearchTranscripts on the session.Store port (Mongo text index transcript_text with $text and textScore; in-memory term-frequency ranking with phrases and exclusions), POST /transcript-search, the remote adapter, a sessiontest case, the search_transcripts MCP tool and session search. The Mongo ranking runs only in CI.
