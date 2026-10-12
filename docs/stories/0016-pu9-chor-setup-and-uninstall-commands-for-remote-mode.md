---
id: 0016-pu9
title: Setup and uninstall commands and repository docs for remote mode
purpose: Let a user choose the clued server in /clued-setup and keep the repository's own docs accurate.
type: chor
status: complete
priority: "2"
effort: S
created: "2026-10-12T20:09:05.289Z"
updated: "2026-10-12T21:41:06.190Z"
started: "2026-10-12T21:31:05.747Z"
completed: "2026-10-12T21:41:06.190Z"
tags:
  - server
  - remote
  - docs
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 15
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - commands/clued-setup.md
      - commands/clued-uninstall.md
      - hooks/uninstall
      - README.md
      - CLAUDE.md
      - mind-palace/README.md
  plans:
    - ["0006-tsw", "included", ["5.1"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Setup and uninstall commands and repository docs for remote mode

## Problem Statement

`/clued-setup` only knows the local MongoDB path and writes `config.json` without a mode; `/clued-uninstall` and `hooks/uninstall` do not know about the catch-up state or the relay log; the README, the CLAUDE.md layout table and the mind-palace package map do not mention the server, the new packages or the new commands.

**Impact:** Remote mode would exist but nobody could set it up from inside Claude Code.

## Steps

### 1: /clued-setup

First question: local MongoDB or clued server. The server path asks for the URL and token, writes `config.json` at 0600 with `serverUrl` and `serverToken`, runs `scripts/clued session whoami` and reports the principal and projects, runs `register-hooks`, and starts nothing.

### 2: /clued-uninstall and hooks/uninstall

Remove `<datadir>/catchup/` and `relay.log` as well as the config; skip the launchd and daemon steps when no daemon was ever configured.

### 3: Docs

README: remote mode section; CLAUDE.md: layout rows for `remote/`, `server/auth`, `server/sessionapi`, `server/storeapi`, `server/projects`, `session/ingest`, `session/catchup`, `session/remotesession`, `store/remotestore`, `Dockerfile.server`, `docs/server.md`; mind-palace README package map likewise.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./plugin/
- [x] Following `/clued-setup` on a machine with no MongoDB ends with a successful `whoami` and registered hooks
- [x] CLAUDE.md's layout table lists every new package and file

## Files

- `commands/clued-setup.md` - server path
- `commands/clued-uninstall.md` - remote cleanup
- `hooks/uninstall` - remote cleanup
- `README.md` - remote mode
- `CLAUDE.md` - layout rows
- `mind-palace/README.md` - package map

## Work Log
