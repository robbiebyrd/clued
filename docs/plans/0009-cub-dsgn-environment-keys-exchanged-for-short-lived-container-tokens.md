---
id: 0009-cub
title: Environment keys exchanged for short-lived container tokens
type: dsgn
status: in_progress
priority: "2"
effort: L
created: "2026-10-14T15:31:39.678Z"
updated: "2026-10-14T15:39:27.627Z"
tags:
  - auth
  - cloud
  - server
  - setup
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
progress:
  "1":
    status: complete
  "1.1":
    status: pending
  "1.2":
    status: pending
  "1.3":
    status: pending
  "1.4":
    status: pending
  "2":
    status: pending
  "2.1":
    status: pending
  "2.2":
    status: pending
---

# Environment keys exchanged for short-lived container tokens

## Summary

**Goal:** A Claude Code cloud environment holds one environment key; every container trades it at its first request for its own token that expires within a day, on Clued Cloud and on a clued server alike.

**Problem:** Cloud containers are linked today with a client token in the environment's settings: it covers the whole account, never expires, and is shared by every container, so a leak lasts until someone notices and the Linked clients page cannot tell containers apart.

**Approach:** A new kind of credential, the environment key, authenticates exactly one route, POST /api/v1/token/exchange, which mints a short-lived token for the caller; the client exchanges CLUED_SERVER_KEY on demand, caches the token in its config directory and renews it before it expires.

---

# Part 1 — Design

## Current State

Clued Cloud client tokens (account.ClientToken) and clued server tokens (auth.Token in the token file) have no expiry and no kind; Service.Lookup and TokenFile.Lookup accept any unrevoked token for every route. Every remote client is built by remote.New(session.Config) and sends cfg.ServerToken as the bearer.

## Decisions

- **Container tokens live 24 hours by default; a key can set 1 to 168 hours.** — Long enough for a working day in one container, short enough that a leaked container token goes stale on its own; the client renews it in place.
- **The same route and envelope on the cloud and the clued server.** — The client stays server-agnostic, as it is for the session and store transports.
- **A key cannot read or write any data itself.** — It is the long-lived secret in the environment's settings; what it can do is mint tokens the Linked clients page shows and the user can revoke one by one or all at once.

## Design

### Protocol

`POST /api/v1/token/exchange`, `Authorization: Bearer <environment key>`, body `{"client_name", "client_info"}`, answer `{"ok": true, "result": {"token", "token_id", "name", "account_id", "expires_at"}}`. A key that is unknown, revoked or not an environment key is 401 `unauthorized`; too many exchanges for one key in an hour is 429 `rate_limited`. A server without exchange answers 404, which the client reports as such. An environment key presented on any other route is 401, like an unknown token.

### Client (mind-palace)

`session.Config.ServerKey` (`serverKey`, env `CLUED_SERVER_KEY`). When `ServerToken` is empty and `ServerKey` is set, `remote.Client.Do` gets its bearer from the exchange: a cached token in `<config dir>/server-token.json` (mode 0600, holding the server URL, a hash of the key, the token, its id and `expires_at`) while it has more than ten minutes left, else a new exchange under a lock file, so the hooks starting together exchange once. A 401 on a request made with an exchanged token drops the cache and exchanges once more. `ServerToken` still wins when both are set.

### clued server (mind-palace)

`auth.Token` gains `kind` (`environment` for a key), `expires` and `parent`. `TokenFile.Lookup` skips keys and expired tokens; `TokenFile.Exchange` checks a key, mints a child named `<key>@<random>` with the key's account and projects and `expires` now + TTL, prunes children expired more than a day, and counts exchanges per key per hour. `Revoke` of a key revokes its children. `server token create --environment [--ttl 24h]`; `list` shows kind, expiry and parent. `CluedOptions.Exchange` serves the route; nil leaves it unserved (404).

### Clued Cloud (clued-cloud)

`account.ClientToken` gains `Kind`, `ExpiresAt` and `ParentID`; `Service.Lookup` and the `/api/v1/me` checks reject keys and expired tokens. `Service.CreateEnvironmentKey` (Linked clients page: name and lifetime of the tokens it issues) and `Service.ExchangeEnvironmentKey` (rate limited per key in the store, audited as `token_exchanged`). Revoking a key revokes its children. The Linked clients page lists keys with their active containers and hides expired tokens; MongoDB expires them a week after `expires_at`.

### Setup

In a cloud container `/clued-setup` recommends an environment key: `CLUED_SERVER_URL` and `CLUED_SERVER_KEY` in the environment's settings; `CLUED_SERVER_TOKEN` stays supported. `check-setup` names both.

### Edge Cases

Hooks of one session start together: the lock makes one of them exchange and the others read its cache. A container that runs longer than the token's lifetime renews it transparently. A changed CLUED_SERVER_KEY or server URL invalidates the cache through the stored key hash and URL.

## Considerations

- **Security:** The key and the tokens are stored hashed on the servers and never logged; the client's cache file is 0600 in the plugin's data directory, which a cloud container throws away. Exchanges are rate limited per key. Revoking a key cuts off every container at its next request.

## Testing

auth token file: keys rejected by Lookup, exchange mints a child with the key's projects and expiry, expired tokens rejected, cascade revoke, rate limit. remote: exchange on first request, cache reuse across clients, renewal before expiry, retry after a 401, one exchange under concurrent clients. Server: the route over httptest. Cloud: accounttest cases for kind, expiry, children and cascade; the route; the Linked clients page.

## Scope

**In scope:**

- Environment keys and the exchange on Clued Cloud and the clued server
- The client's exchange, cache and renewal
- Setup recommending CLUED_SERVER_KEY in cloud containers

**Out of scope:**

- Expiring or project-scoped ordinary client tokens (option 1) — A separate change; the exchanged tokens already expire
- Credentials the platform signs for a container — No such credential is known to exist for Claude Code cloud sessions

---

# Part 2 — Implementation

## Phase 1: mind-palace: client, clued server, CLI and protocol

### 1.1: Token file kinds, expiry and exchange

**Depends on:** none

- [ ] **Step 1: auth.Token kind, expires, parent; Lookup rules; Exchange; cascade revoke; tests**

### 1.2: Exchange route and CLI

**Depends on:** none

- [ ] **Step 1: POST /api/v1/token/exchange in NewClued; server token create --environment --ttl; list columns; tests**

### 1.3: Client exchange and cache

**Depends on:** none

- [ ] **Step 1: Config.ServerKey; remote.Client exchange, cache file, lock, renewal, 401 retry; tests**

### 1.4: Setup and docs

**Depends on:** none

- [ ] **Step 1: Cloud container section and check-setup name CLUED_SERVER_KEY; PROTOCOL.md, docs/server.md**

## Phase 2: clued-cloud: environment keys

### 2.1: Account model and service

**Depends on:** none

- [ ] **Step 1: ClientToken kind, expiry, parent; both stores and accounttest; Lookup rules; create, exchange, cascade revoke; audit**

### 2.2: Route and Linked clients page

**Depends on:** none

- [ ] **Step 1: POST /api/v1/token/exchange; the page creates keys and lists their containers; pin bump**

## Acceptance Criteria

- [ ] An environment key is refused by every route but the exchange, on both servers
- [ ] A client with only CLUED_SERVER_URL and CLUED_SERVER_KEY works against both servers, exchanging once per container and renewing before expiry
- [ ] Revoking a key makes its container tokens fail at their next request
