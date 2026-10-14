---
id: 0021-hr9
title: 'Link a machine to a clued cloud: session login, logout and config-pull'
type: feat
status: complete
priority: "1"
effort: M
created: "2026-10-13T18:13:17.698Z"
updated: "2026-10-14T19:12:20.223Z"
started: "2026-10-13T18:13:17.698Z"
completed: "2026-10-14T19:12:20.223Z"
tags:
  - cloud
  - session
  - setup
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    files:
      - mind-palace/cli/session_login.go
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
  "4":
    status: pending
---

# Link a machine to a clued cloud: session login, logout and config-pull

## Problem Statement

The clued cloud (robbiebyrd/clued-cloud) hands out tokens through a device flow, but the plugin could only take a token copied by hand into config.json.

**Impact:** Without a client side of the flow a user cannot click a link in a Claude Code session, log in, and have the token land in the client.

## Steps

### 1: Config file merge

`session.UpdateConfigFile` merges keys into config.json atomically with mode 0600, keeping every other key.

### 2: session login

Starts the device flow, prints the link and code on stderr, polls until approval, writes serverUrl and serverToken, checks with whoami.

### 3: session logout and config-pull

`logout` revokes the client's token and removes the server keys; `config-pull` applies the account's client settings, run by session-start in remote mode.

### 4: Setup and docs

`/clued-setup` option 4 (clued cloud), docs/server.md, plugin version 1.2.0.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race -run 'TestSessionLogin|TestSessionConfigPull|TestUpdateConfigFile' ./cli/ ./session/
- [x] [MANUAL] On a machine with the plugin, `/clued-setup` option 4 against a running clued cloud links the machine after approving in a browser and `session whoami` names the account

## Work Log

### 2026-10-13T18:13:17.847Z - Implemented in robbiebyrd/clued#24; the cloud side is robbiebyrd/clued-cloud#1.
