---
id: 0067-2xl
title: GitHub App auth and webhook verification (`ghapp`)
purpose: Everything the GitHub App does starts with an app JWT, an installation token and a verified webhook.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:06:09.413Z"
updated: "2026-10-19T19:03:53.887Z"
started: "2026-10-19T18:57:46.172Z"
completed: "2026-10-19T19:03:53.887Z"
tags:
  - plan-0011-gha
  - wave-1
  - contract
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/ghapp/jwt.go
      - mind-palace/ghapp/tokens.go
      - mind-palace/ghapp/webhook.go
      - mind-palace/ghapp/auth_test.go
  web:
    plan-0011-gha: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md
  plans:
    - ["0011-gha", "included", ["1.1"]]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
---

# GitHub App auth and webhook verification (`ghapp`)

## Problem Statement

Everything the GitHub App does starts with an app JWT, an installation token and a verified webhook. Standard library only.

**Working on this concurrently.** This story is in wave 1 of plan 0011-gha. It can start now: it depends on nothing open. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0068-tb0` GitHub REST client, typed events and a fake GitHub; `0044-tko` GitHub webhook endpoint and Mongo job queue.

**Impact:** Blocks the later stories of plan 0011-gha that depend on it.

## Steps

### 1: Read the plan section

Read plan 0011-gha sections 1.1 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0011-gha-dsgn-github-app-for-code-plan-and-story-sync.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: JWT

`iat` 60 s in the past, `exp` ≤ 10 min, `crypto/rsa`.

### 3: Tokens

Per-installation cache with refresh; the token never logged.

### 4: Verify

Reject missing header, wrong secret, altered body; constant-time compare.

### 5: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghapp/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./ghapp/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./ghapp/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/ghapp/jwt.go` - RS256 app JWT
- `mind-palace/ghapp/tokens.go` - installation tokens cached until 5 minutes before expiry
- `mind-palace/ghapp/webhook.go` - `Verify(secret, body, header)` with HMAC-SHA256 and `hmac.Equal`
- `mind-palace/ghapp/auth_test.go` - tests

## Work Log

### 2026-10-19T19:03:49.272Z - New package mind-palace/ghapp (standard library only). jwt.go: App{ID, Key, BaseURL, HTTPClient, Now}, ParsePrivateKey (PKCS#1/PKCS#8 PEM), App.JWT and SignJWT (RS256, iat now-60s, exp now+9min, so exp-iat = 10 min). tokens.go: App.InstallationToken(ctx, id) buying POST /app/installations/{id}/access_tokens with the app JWT, cached per installation (per-entry mutex: concurrent callers share one request) until TokenRefreshMargin (5 min) before expiry; App.Invalidate; Token whose String/GoString/LogValue redact the secret (Value() for the header); TokenError (404 unwraps to ErrInstallationNotFound); SetHeaders (Bearer, vnd.github+json, X-GitHub-Api-Version 2022-11-28) for 0068's client. webhook.go: Verify(secret, body []byte, header string) with HMAC-SHA256 and hmac.Equal (ErrNoSecret, ErrMissingSignature, ErrMalformedSig, ErrBadSignature), Sign for fakes, header-name constants and MaxWebhookBody (25 MiB). auth_test.go: 8 tests (JWT claims and signature, key parsing, cache/refresh timing with a fake clock, concurrency, error mapping, token redaction in fmt and slog, GitHub's documented HMAC test vector plus missing header, wrong secret, altered body and malformed headers) against an httptest fake, never api.github.com. gofmt, go vet ./..., go build ./..., go test -race ./ghapp/... pass. App.InstallationClient is left to 0068 (client.go). Only the four listed files changed.
