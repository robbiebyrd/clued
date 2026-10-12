---
id: 0010-54u
title: Per-project plan and story stores over HTTP (storeapi, projects)
purpose: Serve store.Store per project so plans and stories from many repositories live on one server without colliding ids.
type: feat
status: pending
priority: "2"
effort: L
created: "2026-10-12T20:09:05.114Z"
updated: "2026-10-12T20:09:05.114Z"
tags:
  - server
  - remote
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 9
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/server/storeapi/storeapi.go
      - mind-palace/server/storeapi/storeapi_test.go
      - mind-palace/server/projects/projects.go
      - mind-palace/server/projects/projects_test.go
      - mind-palace/config/config.go
      - mind-palace/config/config_test.go
  plans:
    - ["0006-tsw", "included", ["3.1"]]
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

# Per-project plan and story stores over HTTP (storeapi, projects)

## Problem Statement

Plan and story ids are per repository (`0001-v1i` exists in every repository), the server has one `Palace` over one `MultiStore`, and `config.Validate` insists on an enabled file store. A shared server needs a raw `store.Store` transport namespaced by project, per-project stores opened from one storage template, and a config mode that allows a database-only server.

**Impact:** Without namespacing two repositories would overwrite each other's documents on the server.

## Steps

### 1: Project slugs

`projects.Slug(origin)` normalizes a git origin to `github.com__owner__repo`; slugs match `[a-z0-9._-]+` with `/` encoded as `__` and are validated inline (`filepath.IsLocal`, no `..`) wherever a request supplies one.

### 2: Opener

`projects.Opener` copies each enabled server `StorageDef`, stamps the project into the plugin prefix (`collection: <project>_` mongo, `tablePrefix: <project>_` sql, `dir: <root>/<project>` file), opens them with `store.Open`, wraps in `store.NewMulti`, caches per project and closes on shutdown.

### 3: storeapi

`storeapi.Handler(open func(project) (store.Store, error))` under `/api/v1/projects/{project}/store/`: `GET /{kind}`, `GET|PUT|DELETE /{kind}/{id}`, `GET|PUT|DELETE /{kind}/templates[/{id}]`; checks `auth.AllowProject` first and answers 403 otherwise; `ErrNotFound` → 404.

### 4: Config

`config.Validate(cfg, config.AllowNoFileStore)` lets the server run on a database alone; the client-side rule is unchanged.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/storeapi/ ./server/projects/ ./config/
- [ ] Two projects with the same plan id are stored and listed independently
- [ ] A request for a project outside the token's list gets 403 before any store is opened
- [ ] `..`, `/` and empty project slugs are rejected with 400

## Files

- `mind-palace/server/storeapi/storeapi.go` - handler
- `mind-palace/server/storeapi/storeapi_test.go` - routes, 403, 404, templates
- `mind-palace/server/projects/projects.go` - Slug, Opener, cache
- `mind-palace/server/projects/projects_test.go` - slug and prefix stamping
- `mind-palace/config/config.go` - AllowNoFileStore
- `mind-palace/config/config_test.go` - validation option

## Work Log
