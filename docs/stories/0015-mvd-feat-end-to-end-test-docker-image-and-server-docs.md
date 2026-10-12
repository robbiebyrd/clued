---
id: 0015-mvd
title: End-to-end test, Docker image published to GHCR, and server docs
purpose: Prove the whole remote loop in one test and ship the server as a published image with documentation.
type: feat
status: in_progress
priority: "2"
effort: L
created: "2026-10-12T20:09:05.261Z"
updated: "2026-10-12T21:36:17.235Z"
started: "2026-10-12T21:36:17.235Z"
tags:
  - server
  - remote
  - docs
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 14
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/cli/remote_e2e_test.go
      - Dockerfile.server
      - docker-compose.server.yml
      - docs/server.md
      - .github/workflows/ci.yml
      - .github/workflows/release.yml
  plans:
    - ["0006-tsw", "included", ["4.3"]]
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

# End-to-end test, Docker image published to GHCR, and server docs

## Problem Statement

Each transport and adapter is tested on its own, but nothing exercises the whole loop: server up, relay pushes an event and a transcript, the MCP tools read them back, a plan fans out, the server restarts, `sync` reconciles. Operators also need a way to run the server (decided: a GHCR image published on `v*` tags) and a document that walks through setup, tokens, TLS and `claude mcp add --transport http`.

**Impact:** Without the end-to-end test a protocol regression could pass every unit suite; without the image and docs nobody can deploy it.

## Steps

### 1: End-to-end test

`cli/remote_e2e_test.go`: in-process `server run --store memory` with a token file; `hook relay` with a synthetic `PostToolUse` event and a temp transcript; assert through `sessionmcp` over `remotesession` and `plan_create` through the two-store palace; stop and restart the server; run `sync --from file --to clued-server`.

### 2: Docker image

`Dockerfile.server`: multi-stage build from `mind-palace/`, non-root user, `tokens.json` and certificates as read-only mounts, `CLUED_SERVER_*` env. `docker-compose.server.yml` with a `mongo:8` service pulls `ghcr.io/robbiebyrd/clued-server:latest` by default with a build override.

### 3: Workflows

`ci.yml` builds the image without pushing; `release.yml` pushes `ghcr.io/robbiebyrd/clued-server:<tag>` and `:latest` on `v*` tags with `GITHUB_TOKEN` and `packages: write`.

### 4: docs/server.md

Binary and Docker setups, token management (`create`, `grant`, `revoke`), TLS and reverse-proxy, client `config.json`, `claude mcp add --transport http clued-remote https://<host>/mcp --header "Authorization: Bearer <token>" --header "X-Clued-Project: <project>"`, `sync` after outages, and the cloud-container walkthrough.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race -run Remote ./cli/
- [ ] VERIFY: docker build -f Dockerfile.server -t clued-server .
- [ ] The end-to-end test passes with no environment variables and covers event, transcript, plan fan-out and sync after restart
- [ ] [MANUAL] A `v*` tag publishes `ghcr.io/robbiebyrd/clued-server:<tag>` and the compose file pulls it

## Files

- `mind-palace/cli/remote_e2e_test.go` - end-to-end test
- `Dockerfile.server` - image
- `docker-compose.server.yml` - server plus mongo
- `docs/server.md` - operator and client guide
- `.github/workflows/ci.yml` - image build
- `.github/workflows/release.yml` - GHCR publish

## Work Log
