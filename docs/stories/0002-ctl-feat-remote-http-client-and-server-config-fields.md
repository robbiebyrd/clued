---
id: 0002-ctl
title: Remote HTTP client and server config fields
purpose: Give every remote adapter one authenticated, TLS-aware HTTP client and the config.json keys that switch the plugin into remote mode.
type: feat
status: complete
priority: "2"
effort: S
created: "2026-10-12T20:09:04.868Z"
updated: "2026-10-12T20:50:01.443Z"
started: "2026-10-12T20:35:07.894Z"
completed: "2026-10-12T20:50:01.443Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 1
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/remote/client.go
      - mind-palace/remote/client_test.go
      - mind-palace/session/config.go
      - mind-palace/session/config_test.go
  plans:
    - ["0006-tsw", "included", ["1.1"]]
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

# Remote HTTP client and server config fields

## Problem Statement

Nothing in mind-palace can talk to a clued server: there is no HTTP client with bearer auth, no TLS trust configuration, no mapping from the `{ok, error}` envelope to the port sentinels, and `session.Config` has no server fields. Every later story (the session adapter, the store driver, the hook relay, `check-setup`) needs the same client and the same mode switch.

**Impact:** Without a shared client each adapter would reimplement auth, TLS and error mapping, and the plain-HTTP refusal would be enforced inconsistently.

## Steps

### 1: Config fields

Add `serverUrl`, `serverToken`, `serverCaFile` and `serverInsecure` to `session.Config` with `CLUED_SERVER_URL`, `CLUED_SERVER_TOKEN`, `CLUED_SERVER_CA_FILE` and `CLUED_SERVER_INSECURE` overrides, `~/` expansion for the CA file, and `Config.Remote() bool`. Every existing key stays.

### 2: Scheme rule and TLS

`remote.New(cfg)` accepts `https://` always, `http://` only for loopback hosts or when `serverInsecure` is set, else returns `ErrInsecureURL`; loads `serverCaFile` into the TLS root pool when set.

### 3: Do and error mapping

`Client.Do(ctx, method, path, in, out) error` sends JSON with `Authorization: Bearer`, `User-Agent: clued/<version>` and a per-call timeout, decodes the `httpapi` envelope, and maps `not_found` → the caller's sentinel via `remote.IsNotFound`, 401 → `ErrUnauthorized`, 403 → `ErrForbidden`, 413 → `ErrTooLarge`.

### 4: FromConfig

`remote.FromConfig(session.Config) (*Client, bool)` returns the client only when `serverUrl` is set; tests cover precedence of env over file and each scheme case.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./remote/ ./session/
- [x] An `http://` server URL on a non-loopback host fails at config load with `ErrInsecureURL` unless `serverInsecure` is set
- [x] A test server with a private CA is reachable through `serverCaFile` and unreachable without it
- [x] `config.json` with only the pre-existing keys still loads identically and `Remote()` is false

## Files

- `mind-palace/remote/client.go` - new package: Client, FromConfig, sentinels
- `mind-palace/remote/client_test.go` - scheme, TLS, envelope and error-mapping tests
- `mind-palace/session/config.go` - server fields, env overrides, Remote()
- `mind-palace/session/config_test.go` - new field and override cases

## Work Log
