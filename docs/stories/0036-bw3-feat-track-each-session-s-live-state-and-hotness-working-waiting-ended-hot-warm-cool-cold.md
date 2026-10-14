---
id: 0036-bw3
title: 'Track each session''s live state and hotness: working, waiting, ended, hot, warm, cool, cold'
purpose: A session's state (whether the agent is mid-turn or waiting on the user) and its transitions are captured as they happen, and its hotness is reported from when it was last active.
type: feat
status: complete
priority: "2"
effort: M
created: "2026-10-14T04:28:51.415Z"
updated: "2026-10-14T05:07:23.105Z"
started: "2026-10-14T04:56:44.738Z"
completed: "2026-10-14T05:07:23.105Z"
tags:
  - sessions
  - hooks
  - live-state
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  stories:
    - ["0034-8ni", "depends"]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
---

# Track each session's live state and hotness: working, waiting, ended, hot, warm, cool, cold

## Problem Statement

clued records every hook event but keeps no notion of what state a session is in. AgentHUD derives two kinds of badges by scanning transcript tails: a live state, `[working]` (the agent is mid-turn, a tool running or a reply coming) or `[waiting]` (the agent yielded, including an AskUserQuestion, and the next move is the user's), and a hotness, `[hot]` (updated in the last 30 minutes), `[warm]` (the last hour), `[cool]` (earlier today) or `[cold]` (yesterday or earlier). Its heuristic lags a poll and guesses; the hooks tell clued exactly when the state changes.

**Impact:** A live view or a report cannot say which sessions are running, which are blocked on the user and which went quiet, and the history of those transitions is lost.

## Steps

### 1: Live state from the hooks

In `session/ingest`, derive the state from each event: `working` on `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure` and `SubagentStart`; `waiting` on `Stop`, `StopFailure`, a `PreToolUse` of `AskUserQuestion`, a `PermissionRequest`, and a `Notification` whose type is `permission_prompt`, `idle_prompt` or `elicitation`; `ended` on `SessionEnd`. Store `live_state`, `live_state_at` and `live_state_reason` (the event that set it) on the session document through `session.Store.SetLiveState`, and append a `SessionStateChanged` hook event (`session_id`, `from`, `to`, `reason`, `at`) whenever the state differs from the stored one. Local mode does this in the daemon, remote mode in `hook relay`.

### 2: Hotness at query time

Hotness is a function of `last_seen`, not stored: `hot` within 30 minutes, `warm` within an hour, `cool` earlier the same UTC day, `cold` before that. `FindSessions` and `get_session_context` add `live_state`, `live_state_at` and `hotness` to their results, and `find_sessions` gains `live_state` and `hotness` filters.

### 3: Prime and status line

The prime report's SESSION section names the state and hotness of the newest session; the status line sample (`hook status`) adds `live_state` when the session document carries one, so `get_limits` samples say whether the session was working or waiting.

### 4: State transitions as a query

`get_activities` (the activity timeline) includes `SessionStateChanged` events as `state` entries, so a timeline shows when the agent started working and when it yielded.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/... ./server/sessionapi/ ./cli/
- [x] Replaying a hook sequence of UserPromptSubmit, PreToolUse, PostToolUse, Stop, UserPromptSubmit, PreToolUse(AskUserQuestion), SessionEnd produces the states working, waiting, working, waiting, ended with one SessionStateChanged event per change
- [x] `find_sessions` returns `live_state` and `hotness` and filters on them; a session last seen 10 minutes ago is hot, 50 minutes ago warm, earlier today cool, yesterday cold
- [x] PROTOCOL.md documents the states, the transitions table, the session fields and the event

## Files

- `mind-palace/session/ingest/ingest.go` - state derivation and the SessionStateChanged event
- `mind-palace/session/store.go` - `SetLiveState`, the projection and the filters
- `mind-palace/session/query/query.go` - hotness and the new filters
- `mind-palace/session/limits/limits.go` - `live_state` on the sample
- `PROTOCOL.md` - sections 4.2, 6.1 and 7.3

## Work Log

### 2026-10-14T05:06:14.378Z - Added session/livestate (StateOf transitions, Observe, Hotness, Window), Store.SetLiveState and StateChanges (memory, Mongo, remote with POST /sessions/{id}/state and /state-changes, conformance cases), SessionQuery live-state and last-seen filters, SessionStateChanged events recorded by the ingest service (daemon and relay) with LimitsSampled samples stamped with live_state, hotness computed at query time in find_sessions (live_state and hotness filters, `session find --live-state --hotness`) and get_session_context, state entries in get_activities (kind state), the prime SESSION section's state and hotness lines, and docs (PROTOCOL.md 4.2, 4.6, 5.1, 6.1, 7.3, 9; README). Tests counting hook events now expect the state-change event. gofmt, vet and `go test -race ./...` pass.
