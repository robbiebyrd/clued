---
id: 0012-gbx
title: Client fan-out of plans and stories to the server
purpose: Make every Palace the client opens write to the file store first and the clued server second when remote mode is on.
type: feat
status: in_progress
priority: "2"
effort: M
created: "2026-10-12T20:09:05.174Z"
updated: "2026-10-12T20:51:37.749Z"
started: "2026-10-12T20:51:37.749Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 11
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/cli/cli.go
      - mind-palace/cli/palace.go
      - mind-palace/cli/cli_test.go
      - mind-palace/collect/sources/docsource/docsource.go
      - mind-palace/collect/sources/docsource/docsource_test.go
      - mind-palace/config/config.go
      - mind-palace/service/review.go
      - mind-palace.config.example.yaml
  plans:
    - ["0006-tsw", "included", ["3.3"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Client fan-out of plans and stories to the server

## Problem Statement

A `Palace` is opened in several places (the plan and story CLI commands, `mcp`, `docsource.Open` for `prime_context`, the status review behind the hook guards), each from the repository's `mind-palace.config.yaml` alone. Remote mode must append the server as a secondary store in all of them, with the project derived from the git origin or an override, and the remote store must show up in `getConfig`, `stores` and `sync`.

**Impact:** A missed call site would mean some writes never reach the server.

## Steps

### 1: Shared opener

One `openPalace(ctx, cwd)` helper used by every call site; when `cfg.Remote()` it appends `StorageDef{Name: "clued-server", Kind: "clued", Options: {url, token, project}}` after the repository's own definitions so the file store stays primary.

### 2: Project

Project from `gitinfo` through `projects.Slug`; a `remote.project` key in `mind-palace.config.yaml` overrides it; a repository with no origin uses the directory name and the status review says so. Document the key in `mind-palace.config.example.yaml`.

### 3: Visibility and sync

`getConfig` and `mind-palace stores` list `clued-server`; a `FanoutError` from it surfaces as the existing warning naming `mind-palace sync --from file --to clued-server`.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./...
- [ ] With `CLUED_SERVER_URL` set, `plan_create` writes the file under `docs/plans` and the document on the test server under the repository's project
- [ ] With the server down, `plan_create` still writes the file and returns the sync warning naming `clued-server`
- [ ] `mind-palace sync --from file --to clued-server` brings the server up to date after an outage

## Files

- `mind-palace/cli/palace.go` - openPalace with remote definition injection
- `mind-palace/cli/cli.go` - use openPalace
- `mind-palace/cli/cli_test.go` - two-store palace tests
- `mind-palace/collect/sources/docsource/docsource.go` - use openPalace
- `mind-palace/collect/sources/docsource/docsource_test.go` - remote case
- `mind-palace/config/config.go` - remote.project key
- `mind-palace/service/review.go` - note missing origin
- `mind-palace.config.example.yaml` - document remote.project

## Work Log
