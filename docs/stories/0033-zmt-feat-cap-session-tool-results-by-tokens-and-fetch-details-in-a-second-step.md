---
id: 0033-zmt
title: Cap session tool results by tokens and fetch details in a second step
type: feat
status: complete
priority: "2"
effort: M
created: "2026-10-14T03:13:00.945Z"
updated: "2026-10-14T04:54:43.596Z"
started: "2026-10-14T04:39:48.492Z"
completed: "2026-10-14T04:54:43.596Z"
tags:
  - mcp
  - tokens
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  stories:
    - ["0028-p8p", "depends"]
  plans:
    - ["0008-vvc", "included"]
progress:
  "1":
    status: complete
  "2":
    status: complete
---

# Cap session tool results by tokens and fetch details in a second step

## Problem Statement

get_full_session and read_transcript return everything at once; an agent cannot afford that on a long session and has no cursor to continue.

## Steps

### 1: Budget helper

max_tokens, truncated and next on every session tool.

### 2: Two-step search

Compact hits plus get_lines; sectioned get_full_session.

## Acceptance Criteria

- [x] VERIFY: Every session tool honours max_tokens and returns a cursor when it truncates

## Work Log

### 2026-10-14T04:39:48.656Z - query/budget.go: token estimate (JSON length / 4), Fit (longest prefix that fits, a lone oversized item clipped at its longest strings), Page, opaque tool-bound cursors. sessionmcp applies it: max_tokens and cursor on every tool but restore and forget (defaults 4000 for lists and searches, 8000 for reads); a list that fits keeps its shape, one that does not becomes {items, truncated, next}; usage cuts groups, get_session_context trims lines and hands a read_transcript cursor, get_full_session takes sections and continues each cut one, prime_context caps the report. New get_lines(session_id, seqs) fetches whole lines after a search. Deviation: the search hits were already compact (ids, snippet, time) and stay as they are; the budget does the rest. The query service signatures are unchanged, since clued-cloud's web app calls them. Tests page every tool under a budget to the end and check no item is skipped or repeated, against both stores.
