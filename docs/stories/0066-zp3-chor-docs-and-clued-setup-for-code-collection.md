---
id: 0066-zp3
title: Docs and `/clued-setup` for code collection
purpose: Every entrypoint change goes into PROTOCOL.md, and users must be told plainly what leaves their machine.
type: chor
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:06:04.203Z"
updated: "2026-10-19T20:11:29.693Z"
started: "2026-10-19T20:00:59.814Z"
completed: "2026-10-19T20:11:26.311Z"
tags:
  - plan-0010-awy
  - wave-8
  - docs
  - hotspot
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 8
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - PROTOCOL.md
      - docs/server.md
      - README.md
      - commands/clued-setup.md
      - mind-palace/README.md
      - .claude/CLAUDE.md
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0065-2l0", "depends"]
    - ["0059-5zs", "depends"]
  plans:
    - ["0010-awy", "included", ["4.1"]]
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

# Docs and `/clued-setup` for code collection

## Problem Statement

Every entrypoint change goes into PROTOCOL.md, and users must be told plainly what leaves their machine.

**Working on this concurrently.** This story is in wave 8 of plan 0010-awy. Start it when these are complete: `0065-2l0` Relay triggers and `session code-sync` CLI; `0059-5zs` `server run --code-store` and the `server init` stack. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Owns PROTOCOL.md and .claude/CLAUDE.md for 0010; the 0011 and 0012 docs stories follow it. Unblocks: `0077-0l2` Docs for the GitHub App.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 4.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: PROTOCOL.md

New §7.x for the code routes, the codesync state next to §4.4, the keys in §11.1.

### 3: Setup

`/clued-setup` asks in remote mode and states that the source of every repository Claude works in will be uploaded.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./plugin/`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test ./plugin/
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./plugin/
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `PROTOCOL.md` - code routes, state format, config keys
- `docs/server.md` - enabling the code store
- `README.md` - what is uploaded, the deny list, opting out
- `commands/clued-setup.md` - ask about code sync in remote mode
- `mind-palace/README.md` - package map
- `.claude/CLAUDE.md` - layout table: `code/`, `codefilter/`, `server/codeapi/`, `session/codesync/`

## Work Log

### 2026-10-19T20:11:29.693Z - Documented code collection (plan 0010 4.1) from the code: PROTOCOL.md 4.4.1 (code sync triggers per hook_relay.go incl. WorktreeCreate carrying only name, run steps, filters, budget, failures/back-offs, state file JSON), 7.12 code transport (7 write routes, check order, validation, owner/session stamping, BeforeCodeWrite 403 forbidden / 413 quota, 409 conflict, capabilities incl. github, Mongo collections; graph routes left to 0128), 1.2 conflict/quota kinds, 2.6 relay, 7.9 config-pull keys, 9 session code-sync [status] JSON and exit codes plus server run --code-store, 11.1 codeSync/codeSyncMaxFileBytes/codeSyncExclude (+excludePaths/redaction notes), 11.2 codeSync: false. docs/server.md: Collected code section, --code-store flag, client behaviour. README: Code sync section (what is uploaded, deny list, opting out) and config rows. /clued-setup asks in remote mode (and cloud containers via CLUED_CODE_SYNC) with a plain statement that the source of every repository Claude works in is uploaded. mind-palace/README package map and .claude/CLAUDE.md layout rows for code/, codefilter/, server/codeapi/, session/codesync/. go test ./plugin/ ok (87.9s), gofmt/vet/build + go test -race ./plugin/ ok (104.1s). Follow-up outside Files: hooks/uninstall and /clued-uninstall do not remove <config dir>/codesync/ when the config is kept.
