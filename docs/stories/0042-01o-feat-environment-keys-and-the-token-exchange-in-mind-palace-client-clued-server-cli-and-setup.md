---
id: 0042-01o
title: 'Environment keys and the token exchange in mind-palace: client, clued server, CLI and setup'
type: feat
status: in_progress
priority: "2"
effort: M
created: "2026-10-14T15:39:27.224Z"
updated: "2026-10-14T15:39:27.574Z"
started: "2026-10-14T15:39:27.224Z"
tags:
  - auth
  - server
  - cloud
  - setup
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  plans:
    - ["0009-cub", "included"]
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

# Environment keys and the token exchange in mind-palace: client, clued server, CLI and setup

## Problem Statement

A Claude Code cloud environment was linked with a plain token in its settings: it covers the whole account, never expires and is shared by every container.

**Impact:** A leaked token lasts until someone notices, and the server cannot tell containers apart.

## Steps

### 1: Token file

auth.Token kind, token_ttl, parent and expires; Lookup skips environment keys and expired tokens; CreateEnvironmentKey; Exchange mints <key>@<client>-<hex> with the key's account and projects, 60 per key per hour, prunes children expired a day; Revoke of a key revokes its children.

### 2: Route and CLI

POST /api/v1/token/exchange (CluedOptions.Exchange, served by server run from the token file); server token create --environment [--ttl].

### 3: Client

serverKey / CLUED_SERVER_KEY; remote.Client exchanges on its first request, caches the token in server-token.json (0600) under a lock file, renews it ten minutes before it ends and retries once after a 401; clientcfg and the clued store driver carry the key.

### 4: Setup and docs

/clued-setup recommends an environment key in cloud containers; check-setup names CLUED_SERVER_KEY; PROTOCOL.md 7.11, docs/server.md, README.

## Acceptance Criteria

- [ ] VERIFY: `go test ./server/auth/ ./remote/ ./cli/` passes: keys refused by Lookup, exchange, expiry, prune, cascade revoke, rate limit; one exchange per cache across concurrent clients, renewal, retry, revocation
- [x] [MANUAL] `server run` with an environment key from `server token create --environment`: `session whoami` with only CLUED_SERVER_URL and CLUED_SERVER_KEY works twice with one exchange, the key gets 401 on /api/v1/whoami, and revoking the key cuts the client off

## Work Log

### 2026-10-14T15:39:27.574Z - Verified end to end with the real binary: server run --store memory, server token create --environment --ttl 2h, two session whoami runs with only CLUED_SERVER_URL and CLUED_SERVER_KEY (one exchange, cache 0600), the key refused on /api/v1/whoami (401), and the client cut off after server token revoke.
