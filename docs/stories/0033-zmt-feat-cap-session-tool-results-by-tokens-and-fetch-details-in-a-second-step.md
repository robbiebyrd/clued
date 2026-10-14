---
id: 0033-zmt
title: Cap session tool results by tokens and fetch details in a second step
type: feat
status: ready
priority: "2"
effort: M
created: "2026-10-14T03:13:00.945Z"
updated: "2026-10-14T04:30:07.178Z"
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
    status: pending
  "2":
    status: pending
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

- [ ] VERIFY: Every session tool honours max_tokens and returns a cursor when it truncates

## Work Log
