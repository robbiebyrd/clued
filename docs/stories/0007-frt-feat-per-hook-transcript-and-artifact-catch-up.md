---
id: 0007-frt
title: Per-hook transcript and artifact catch-up
purpose: Push the transcript lines, subagent lines and blobs written since the previous hook without a resident tailer.
type: feat
status: complete
priority: "2"
effort: L
created: "2026-10-12T20:09:05.034Z"
updated: "2026-10-12T20:50:01.603Z"
started: "2026-10-12T20:35:07.987Z"
completed: "2026-10-12T20:50:01.603Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 6
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/session/catchup/catchup.go
      - mind-palace/session/catchup/state.go
      - mind-palace/session/catchup/catchup_test.go
      - mind-palace/session/tail/artifacts.go
  plans:
    - ["0006-tsw", "included", ["2.2"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Per-hook transcript and artifact catch-up

## Problem Statement

Transcript lines, subagent JSONL, tool results and file-history blobs reach the store only through the daemon's long-running tailers (`tail.TailFile`, `tail.WatchArtifacts`). In remote mode there is no daemon, so the same data must be pushed incrementally by the short-lived hook relay, with state that survives between hooks and protection against two hooks running at once.

**Impact:** Without it the server would hold hook events but no transcripts, which is most of what `get_full_session` and `read_transcript` return.

## Steps

### 1: One-shot mirroring in tail

Lift the mirroring out of `WatchArtifacts` into `tail.MirrorArtifacts(ctx, store, sid, dirs, seen) (seen, error)` and reuse the transcript line parser and `seq` numbering; `WatchArtifacts` calls the same function in its loop.

### 2: State and lock

`<datadir>/catchup/<sid>.json` holds `offset`, `seq` and per-artifact `{size, mtime}`, written atomically; `<sid>.lock` is created with `O_EXCL`, stale after 60 s by mtime; a held lock means skip and return `Skipped`.

### 3: Run

`catchup.Run(ctx, store, Paths{Transcript, SessionDir, FileHistoryDir}, StateDir) (Report, error)`: validate the transcript is under `projectsDir`, reset to offset 0 if the file shrank, read the delta, upsert lines in batches of 500, mirror changed artifacts, save state. Blobs refused with 413 are recorded in state so they are not retried every hook.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/catchup/ ./session/tail/
- [x] Running catch-up twice without new data makes no store calls the second time
- [x] A truncated transcript restarts from offset 0 and the re-upsert is idempotent by `{session_id, seq}`
- [x] A second concurrent run for the same session skips without error, and a stale lock is reclaimed

## Files

- `mind-palace/session/catchup/catchup.go` - Run and Report
- `mind-palace/session/catchup/state.go` - state file and lock
- `mind-palace/session/catchup/catchup_test.go` - offset, truncation, batching, artifacts, lock
- `mind-palace/session/tail/artifacts.go` - expose MirrorArtifacts

## Work Log
