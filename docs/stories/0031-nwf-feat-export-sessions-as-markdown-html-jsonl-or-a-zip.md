---
id: 0031-nwf
title: Export sessions as Markdown, HTML, JSONL or a zip
type: feat
status: pending
priority: "3"
effort: M
created: "2026-10-14T03:13:00.821Z"
updated: "2026-10-14T03:13:00.821Z"
tags:
  - export
  - session
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
    - ["0008-vvc", "included"]
progress:
  "1":
    status: pending
  "2":
    status: pending
---

# Export sessions as Markdown, HTML, JSONL or a zip

## Problem Statement

The public module has no human-readable export; local and self-hosted users cannot share or archive a session outside Claude Code's own layout.

## Steps

### 1: Renderer

session/render for Markdown and HTML.

### 2: Export and import

session export, export_session, session import.

## Acceptance Criteria

- [ ] VERIFY: A session exports to Markdown and HTML and round-trips through JSONL import

## Work Log
