---
id: 0002-fve
title: WebDAV storage plugin for plans and stories
purpose: Let a user keep plans and stories in a WebDAV folder (Nextcloud, ownCloud, Apache mod_dav, Synology) as a secondary or primary mind-palace store.
type: feat
status: complete
priority: "2"
effort: M
created: "2026-10-12T19:49:11.161Z"
updated: "2026-10-13T16:33:15.888Z"
started: "2026-10-12T19:49:11.161Z"
completed: "2026-10-13T16:33:15.888Z"
tags: []
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/store/webdavstore/webdavstore.go
      - mind-palace/store/webdavstore/webdavstore_test.go
      - mind-palace/cmd/mind-palace/main.go
      - mind-palace/README.md
      - mind-palace/mind-palace.config.example.yaml
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

# WebDAV storage plugin for plans and stories

## Problem Statement

mind-palace stores plans and stories as Markdown files and can mirror them into SQL, MongoDB and Firestore, but not into a WebDAV folder. Users who keep their notes on Nextcloud, ownCloud, a Synology NAS or any mod_dav server have no way to store or share plans and stories there.

**Impact:** Plans and stories stay confined to the repository and database copies; a shared WebDAV folder, which is the simplest cross-machine storage many people already run, cannot hold them.

## Steps

### 1: WebDAV client

A minimal client on net/http in store/webdavstore: PROPFIND (Depth 1) with multistatus parsing, GET, PUT, DELETE, MKCOL, with basic auth or a bearer token and a configurable timeout. No third-party dependency.

### 2: Store plugin

store.Store implementation registered as kind `webdav` with the same layout as the file store under one collection per kind (<base>/<plansPath>, <base>/<storiesPath>, each with archive/ and templates/). Put moves the document when its Path changes; Delete and template operations return store.ErrNotFound as the suite expects.

### 3: Tests

Run store/storetest against golang.org/x/net/webdav served by httptest (a real WebDAV server in-process), plus unit tests for the multistatus parsing, auth headers and option defaults. An optional MIND_PALACE_TEST_WEBDAV_URL runs the suite against a real server.

### 4: Wiring and docs

Import the plugin from cmd/mind-palace, cmd/plan and cmd/story; document the kind and its options in mind-palace/README.md, the config example and CLAUDE.md; rebuild dist/.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./store/webdavstore/ ./store/
- [x] A `storage` entry with kind `webdav` appears in `mind-palace stores` and plans, stories and templates round-trip through a WebDAV server, including rename to archive/ and delete
- [x] VERIFY: cd .. && scripts/build-binaries.sh --check

## Files

- `mind-palace/store/webdavstore/webdavstore.go` - the client and the store plugin
- `mind-palace/store/webdavstore/webdavstore_test.go` - conformance suite against an in-process WebDAV server and unit tests
- `mind-palace/cmd/mind-palace/main.go` - register the plugin (and in cmd/plan, cmd/story)
- `mind-palace/README.md` - storage table row and options
- `mind-palace/mind-palace.config.example.yaml` - commented webdav example

## Proof

- [ ] [completeness] Completeness (the conformance suite covers every Store method for both kinds)
- [ ] [input-validation] Input Validation (template ids and paths are checked before they reach a URL)
- [ ] [security] Security (credentials come from options or the environment and never appear in error messages)
- [ ] [resilience] Resilience (a missing collection is created on Init; HTTP errors carry the status and method)

## Work Log

### 2026-10-12T19:56:57.487Z - webdavstore plugin, tests (conformance against x/net/webdav in-process, auth, multistatus, options), wiring, docs and dist rebuilt; CLI round trip verified against a local WebDAV server (create, archive, delete, sync). Steps and criteria done; closing is the user's call.
