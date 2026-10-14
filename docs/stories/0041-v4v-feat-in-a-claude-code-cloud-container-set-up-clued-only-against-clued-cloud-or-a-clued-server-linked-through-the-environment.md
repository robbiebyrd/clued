---
id: 0041-v4v
title: In a Claude Code cloud container, set up clued only against Clued Cloud or a clued server, linked through the environment
type: feat
status: in_progress
priority: "2"
effort: S
created: "2026-10-14T15:13:44.214Z"
updated: "2026-10-14T15:13:52.392Z"
started: "2026-10-14T15:13:44.214Z"
tags:
  - setup
  - cloud
  - hooks
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
  "4":
    status: complete
---

# In a Claude Code cloud container, set up clued only against Clued Cloud or a clued server, linked through the environment

## Problem Statement

In a Claude Code cloud container `/clued-setup` offered a local MongoDB and a container stack, which cannot survive the container, and the Clued Cloud device login wrote its token into a config file that is lost when the container is reclaimed, so every new container had to be approved in a browser again. `check-setup` also asked for /clued-setup whenever config.json was missing, even when CLUED_SERVER_URL and CLUED_SERVER_TOKEN came from the environment, and `session-start` started a daemon there.

**Impact:** Cloud sessions either captured nothing or needed a new login per container.

## Steps

### 1: Detect the cloud container in setup

Step 1 of `/clued-setup` (command and skill) reports `CLAUDE_CODE_REMOTE=true` and whether the environment already names a server (never the token). A new section replaces Step 2 there: only Clued Cloud (https://clued.cloud by default) or a clued server, a reachability probe with the network-access fix, a token from the cloud's Linked clients page or `server token create`, and CLUED_SERVER_URL and CLUED_SERVER_TOKEN in the environment's settings; an environment that already provides them is only verified.

### 2: Hooks

`check-setup` asks a cloud container without a server for Clued Cloud or a clued server and no longer asks for a config file when the environment names a server; `session-start` starts no daemon and `event-relay` buffers no WAL in a cloud container.

### 3: Clued Cloud default

Outside cloud containers the Clued Cloud option names https://clued.cloud and `session login --server https://clued.cloud`.

### 4: Docs

README, docs/server.md (Claude Code cloud containers) and PROTOCOL.md 2.2 to 2.4.

## Acceptance Criteria

- [x] VERIFY: `go test ./plugin/` passes, including the cloud-container cases: the check-setup message, no setup nag for a server from the environment, no daemon from session-start, no WAL from event-relay
- [ ] [MANUAL] In a Claude Code cloud container with CLUED_SERVER_URL=https://clued.cloud and CLUED_SERVER_TOKEN set in the environment's settings, a new session is linked without /clued-setup and `session whoami` names the token

## Work Log

### 2026-10-14T15:13:52.392Z - Setup command and skill detect CLAUDE_CODE_REMOTE=true and offer only Clued Cloud (https://clued.cloud) or a clued server, linked through CLUED_SERVER_URL and CLUED_SERVER_TOKEN in the environment's settings; check-setup, session-start and event-relay follow; plugin tests added. The manual criterion (a fresh container linked by the environment alone) needs a real token and is left for the user.
