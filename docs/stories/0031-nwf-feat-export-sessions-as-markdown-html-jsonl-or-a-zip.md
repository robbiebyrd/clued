---
id: 0031-nwf
title: Export sessions as Markdown, HTML, JSONL or a zip
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-14T03:13:00.821Z"
updated: "2026-10-14T04:54:43.692Z"
started: "2026-10-14T04:47:02.110Z"
completed: "2026-10-14T04:54:43.692Z"
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
    status: complete
  "2":
    status: complete
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

- [x] VERIFY: A session exports to Markdown and HTML and round-trips through JSONL import

## Work Log

### 2026-10-14T04:47:02.293Z - session/render builds one model (header with project, repository, branch, times and usage; turns with tool calls summarised, tool results folded, errors marked; thinking, snapshots and meta lines left out) and renders it as Markdown or a self-contained HTML page (inline CSS with a dark scheme, no scripts, html/template escaping); golden files under session/render/testdata. session/export: Bundle (format clued-session/1), Run (md, html, jsonl, zip; redacted with the capture rules unless --raw; mode 0600; never replaces a file unless --force), Import (JSONL or zip; upserts, hook events only for a new session; through the capture wrapper). CLI session export/import and the export_session tool (always redacted, local only). Tests: redaction by default, zip and JSONL round trips through import on both stores, and refusals. clued-cloud's own renderer is not replaced yet (cloud follow-up).
