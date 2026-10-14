---
id: 0039-gk4
title: 'Capture the transcript''s system and sidecar records: turn duration, API errors, compaction, titles, PR link, permission mode, entrypoint'
purpose: The records Claude Code writes beside the messages (turn timings, API errors, compaction boundaries, the session's title, its pull request, its permission mode and how it was started) become session fields and events instead of unread lines.
type: feat
status: complete
priority: "2"
effort: S
created: "2026-10-14T04:28:51.517Z"
updated: "2026-10-14T05:34:10.542Z"
started: "2026-10-14T05:22:27.740Z"
completed: "2026-10-14T05:34:10.542Z"
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
    status: complete
  "2":
    status: complete
  "3":
    status: complete
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

- [x] VERIFY: cd mind-palace && go test -race ./session/... ./server/sessionapi/
- [x] A transcript with an ai-title, a pr-link, a permission-mode change, two turn_duration records and a compact_boundary yields a session with title, pr_link and permission_mode set, two timed turns and a compaction entry in the timeline
- [x] `find_sessions` returns `title`, `entrypoint` and `pr_link` and filters by title
- [x] PROTOCOL.md documents the session fields and the enrichment

## Files

- `mind-palace/session/daemon/daemon.go` - sidecar records while tailing
- `mind-palace/session/catchup/catchup.go` - the same in remote mode
- `mind-palace/session/store.go` - `SetSessionMeta` and the projection
- `mind-palace/session/enrichers/system_record.go` - the enricher
- `PROTOCOL.md` - sections 4.3, 6.1 and 7.3

## Work Log

### 2026-10-14T05:31:27.457Z - Added session/sidecar (Meta reads custom-title, ai-title, slug, permission-mode/mode, pr-link, worktree-state and the entrypoint and version of message lines; Tracker writes each change once and never lets a generated title replace a custom one), Store.SetSessionMeta (memory, Mongo, remote with POST /sessions/{id}/meta limited to the metadata fields, conformance case) with a titleRe filter on FindSessions and the fields projected, tracking in the daemon's tailer, the relay's catch-up (state keeps `meta`) and the backfill; the system-record enricher (turn_duration, api_error, compact_boundary, stop_hook_summary, away_summary); error-flag's api_error type (find_errors enum); activity kinds compaction, api_error and turn; get_session_context `turns` (count, total, mean, longest) from the turn records. `usage` does not report turns: its groups span sessions, models or days and the turn records are per session; get_session_context carries them. Docs in PROTOCOL.md 4.3, 4.7, 6.1, 6.1.1, 6.1.4, 7.3, 9 and README. gofmt, vet and `go test -race ./...` pass.
