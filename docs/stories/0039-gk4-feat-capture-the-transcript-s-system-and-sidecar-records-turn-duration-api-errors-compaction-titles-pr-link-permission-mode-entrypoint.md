---
id: 0039-gk4
title: 'Capture the transcript''s system and sidecar records: turn duration, API errors, compaction, titles, PR link, permission mode, entrypoint'
purpose: The records Claude Code writes beside the messages (turn timings, API errors, compaction boundaries, the session's title, its pull request, its permission mode and how it was started) become session fields and events instead of unread lines.
type: feat
status: pending
priority: "2"
effort: S
created: "2026-10-14T04:28:51.517Z"
updated: "2026-10-14T04:28:51.517Z"
tags:
  - sessions
  - transcript
  - metadata
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
  "3":
    status: pending
---

# Capture the transcript's system and sidecar records: turn duration, API errors, compaction, titles, PR link, permission mode, entrypoint

## Problem Statement

Besides user and assistant messages the transcript holds `system` records with subtypes `turn_duration`, `api_error`, `compact_boundary`, `stop_hook_summary` and `away_summary`, and sidecar records `ai-title`, `custom-title`, `last-prompt`, `slug`, `permission-mode`, `pr-link`, `worktree-state` and `file-history-snapshot`; the first line's `entrypoint` is `sdk-cli` for a non-interactive session. clued stores the lines (line-classifier calls two of them `session_meta`) and reads none. AgentHUD documents all of them in its schema notes and reads only `cwd`, `entrypoint`, `version` and `model`.

**Impact:** A session has no title, no link to its pull request, no turn timings, no record of API errors or compactions, and interactive and SDK sessions look alike.

## Steps

### 1: Session fields from sidecar records

The tailer (`session/daemon`) and the catch-up (`session/catchup`) recognise the sidecar records and set session fields through `session.Store.SetSessionMeta`: `title` (custom-title over ai-title), `slug`, `permission_mode`, `pr_link`, `worktree` (`worktree-state`), `entrypoint` (`interactive` or `sdk-cli` from the first line) and `claude_version`. `FindSessions` and `get_session_context` project them; `find_sessions` gains a `title` regex filter.

### 2: Events from system records

A `system-record` enricher on transcript lines stores the subtype and its payload: `turn_duration` (`duration_ms`), `api_error` (`status`, `message`, `retry`), `compact_boundary` (`trigger`, `pre_tokens`), `stop_hook_summary` and `away_summary` (their text). The activity timeline shows `compaction` and `api_error` entries, and `find_errors` gains the `api_error` type.

### 3: Turn timings

`get_session_context` and `usage` report `turns` and total and mean `turn_duration_ms` from the records, so a session's pace is visible without reading its transcript.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./session/... ./server/sessionapi/
- [ ] A transcript with an ai-title, a pr-link, a permission-mode change, two turn_duration records and a compact_boundary yields a session with title, pr_link and permission_mode set, two timed turns and a compaction entry in the timeline
- [ ] `find_sessions` returns `title`, `entrypoint` and `pr_link` and filters by title
- [ ] PROTOCOL.md documents the session fields and the enrichment

## Files

- `mind-palace/session/daemon/daemon.go` - sidecar records while tailing
- `mind-palace/session/catchup/catchup.go` - the same in remote mode
- `mind-palace/session/store.go` - `SetSessionMeta` and the projection
- `mind-palace/session/enrichers/system_record.go` - the enricher
- `PROTOCOL.md` - sections 4.3, 6.1 and 7.3

## Work Log
