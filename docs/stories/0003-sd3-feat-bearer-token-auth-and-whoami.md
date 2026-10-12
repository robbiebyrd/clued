---
id: 0003-sd3
title: Bearer-token auth with project scopes and the whoami route
purpose: Authenticate every clued-server request with a hashed, revocable token bound to an account and a list of projects.
type: feat
status: in_progress
priority: "2"
effort: M
created: "2026-10-12T20:09:04.909Z"
updated: "2026-10-12T20:35:07.919Z"
started: "2026-10-12T20:35:07.919Z"
tags:
  - server
  - remote
  - security
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 2
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/server/auth/auth.go
      - mind-palace/server/auth/auth_test.go
  plans:
    - ["0006-tsw", "included", ["1.2"]]
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

# Bearer-token auth with project scopes and the whoami route

## Problem Statement

No part of mind-palace handles an Authorization header. Isolation today rests on `lastKnownAccountUuid`, which the client chooses. A server on the public internet needs tokens it can mint, hash, check in constant time, revoke without a restart, and bind to an account id and to the projects the token may touch (decided: account plus project list).

**Impact:** Without it every client could read every account's transcripts and write any repository's plans.

## Steps

### 1: Token file

`auth.TokenFile` reads and writes `tokens.json` (0600) holding `{name, hash, account_id, projects, created, revoked?}`; `hash` is hex SHA-256 of 32 random bytes; `projects` is a list of slugs or `["*"]`. The file is re-read when its mtime changes.

### 2: Middleware and principal

`auth.Middleware(source) func(http.Handler) http.Handler` parses `Authorization: Bearer`, compares with `crypto/subtle`, and stores `auth.Principal{Name, AccountID, Projects}` in the context (`auth.FromContext`). Missing, malformed, unknown or revoked tokens get 401 with `WWW-Authenticate: Bearer` in the `httpapi` envelope.

### 3: AllowProject

`auth.AllowProject(p Principal, slug string) bool` honours `*`; a helper writes the 403 `forbidden` envelope for callers (`storeapi`, service API, `/ws`, `/mcp`).

### 4: CLI and whoami

`mind-palace server token create --name --project... [--account]` prints the raw token once; `token list`, `token revoke`, `token grant --project` edit the file. `auth.WhoamiHandler` serves `GET /api/v1/whoami` with the principal, its projects and the server version.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/auth/
- [ ] The token file never contains a raw token, and `token create` prints it exactly once
- [ ] Revoking a token in the file takes effect on the next request without restarting the server
- [ ] `AllowProject` admits listed slugs and `*`, rejects everything else

## Files

- `mind-palace/server/auth/auth.go` - token file, middleware, principal, AllowProject, whoami
- `mind-palace/server/auth/auth_test.go` - hashing, constant-time lookup, revocation, reload, 401 and 403 shapes

## Work Log
