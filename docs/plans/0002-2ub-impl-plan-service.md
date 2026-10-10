---
id: 0002-2ub
title: Implement the plan service
type: impl
status: complete
priority: "2"
effort: L
created: "2026-10-09T20:53:01.506Z"
updated: "2026-10-09T20:53:01.959Z"
completed: "2026-10-09T20:53:01.717Z"
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  specs:
    - ./docs/plans/0001-v1i-dsgn-plan-service.md
  web:
    pull-request: https://github.com/robbiebyrd/clued/pull/2
  plans:
    - ["0001-v1i", "depends"]
progress:
  "1":
    status: complete
  "1.1":
    status: complete
  "1.2":
    status: complete
  "1.3":
    status: complete
  "2":
    status: complete
  "2.1":
    status: complete
  "2.2":
    status: complete
  "3":
    status: complete
  "3.1":
    status: complete
  "4":
    status: complete
---

# Implement the plan service

## Summary

**Goal:** The plan service in plan/ builds, passes its tests and serves every operation over the CLI, HTTP, WebSockets and MCP.

**Problem:** The design in 0001-v1i needs to be delivered as a Go module with pluggable storage and four entrypoints.

**Approach:** Build bottom-up: model and parsing, config and schema, storage, service, registry, entrypoints, database plugins, then docs.

---

# Part 2 — Implementation

**Architecture:** A service layer enforces every rule over a fan-out store. All entrypoints dispatch through one operation registry so names, parameters and results stay identical.

**Tech Stack:** Go 1.26, cobra, yaml.v3, santhosh-tekuri/jsonschema, gorilla/websocket, modelcontextprotocol/go-sdk, modernc sqlite, pgx, go-sql-driver/mysql, clickhouse-go, mongo-driver v2, cloud.google.com/go/firestore

## Constraints

- Every write is validated before it is saved
- id and created never change after create
- The content H1 always matches the title
- Each section leaves go test ./... green

## Files

| File | Change | Responsibility |
|---|---|---|
| `plan/model/model.go` | create | Plan types and YAML/JSON tuple forms |
| `plan/config/config.go` | create | Defaults, loading, normalisation, workflow |
| `plan/schema/plan.schema.json` | create | Embedded Plan JSON Schema |
| `plan/service/service.go` | create | Every operation and rule |
| `plan/store/store.go` | create | Store interface, registry, fan-out, sync |
| `plan/ops/plans.go` | create | Operation registry |
| `plan/cli/cli.go` | create | Command line entrypoint |
| `plan/server` | create | HTTP, WebSocket and MCP entrypoints |

## Phase 1: Core

### 1.1: Model, config, schema, render, markdown

**Files:**
- Create: `plan/model/model.go`
- Create: `plan/config/config.go`
- Create: `plan/schema/schema.go`
- Create: `plan/render/render.go`
- Create: `plan/markdown/markdown.go`

**Depends on:** none

- [ ] **Step 1: Write the failing tests** — YAML round trip of link tuples and progress keys, synonym normalisation, schema validation of the sample inputs, template rendering
- [ ] **Step 2: Implement** — types with custom marshalling, config defaults and overlay, schema patching, default template
- [ ] **Step 3: Run the tests** — `go test ./model ./config ./schema ./render ./markdown`
- [ ] **Step 4: Commit** — `git commit -m 'feat(plan): scaffold Go plan service core'`

**Validation:** `go test ./model ./config ./schema ./render ./markdown`

### 1.2: Storage layer

**Files:**
- Create: `plan/store/store.go`
- Create: `plan/store/sync.go`
- Create: `plan/store/filestore/filestore.go`
- Create: `plan/store/memstore/memstore.go`
- Create: `plan/events/events.go`

**Depends on:** 1.1

- [ ] **Step 1: Write the failing tests** — file round trip with rename and archive moves, fan-out failure reporting, sync conflict modes
- [ ] **Step 2: Implement** — Store interface, registry, MultiStore, Sync, file and memory plugins, event bus
- [ ] **Step 3: Run the tests** — `go test ./store/... ./events`
- [ ] **Step 4: Commit** — `git commit -m 'feat(plan): storage layer'`

**Validation:** `go test ./store/... ./events`

### 1.3: Service

**Files:**
- Create: `plan/service/service.go`
- Create: `plan/service/errors.go`

**Depends on:** 1.2

- [ ] **Step 1: Write the failing tests** — create with synonyms and id allocation, workflow, archive moves, links, progress, templates, patch rules, delete protection, sync
- [ ] **Step 2: Implement** — every operation of the service interface with typed errors
- [ ] **Step 3: Run the tests** — `go test ./service`
- [ ] **Step 4: Commit** — `git commit -m 'feat(plan): service layer'`

**Validation:** `go test ./service`

## Phase 2: Entrypoints

### 2.1: Operation registry and CLI

**Files:**
- Create: `plan/ops/ops.go`
- Create: `plan/ops/plans.go`
- Create: `plan/cli/cli.go`
- Create: `plan/cmd/plan/main.go`

**Depends on:** 1.3

- [ ] **Step 1: Implement** — typed registry with schema inference, cobra commands with exit codes
- [ ] **Step 2: Run the tests** — `go test ./ops ./cli`
- [ ] **Step 3: Commit** — `git commit -m 'feat(plan): operation registry and CLI'`

**Validation:** `go test ./ops ./cli`

### 2.2: HTTP, WebSocket and MCP servers

**Files:**
- Create: `plan/server/httpapi/httpapi.go`
- Create: `plan/server/ws/ws.go`
- Create: `plan/server/mcpserver/mcpserver.go`
- Create: `plan/server/server.go`

**Depends on:** 2.1

- [ ] **Step 1: Implement** — generic op route plus resource routes and SSE; socket frames and subscriptions; MCP tools, resources, subscriptions and watch
- [ ] **Step 2: Run the tests** — `go test ./server/...`
- [ ] **Step 3: Commit** — `git commit -m 'feat(plan): HTTP, WebSocket and MCP entrypoints'`

**Validation:** `go test ./server/...`

## Phase 3: Database plugins

### 3.1: SQL, MongoDB and Firestore stores

**Files:**
- Create: `plan/store/sqlstore/sqlstore.go`
- Create: `plan/store/mongostore/mongostore.go`
- Create: `plan/store/firestorestore/firestorestore.go`
- Create: `plan/store/storetest/storetest.go`

**Depends on:** 1.2

- [ ] **Step 1: Write the conformance suite** — one suite every plugin must pass
- [ ] **Step 2: Implement** — dialect table for sqlite, postgres, mysql and clickhouse; document-per-plan stores for MongoDB and Firestore
- [ ] **Step 3: Run the tests** — `go test ./store/...`
- [ ] **Step 4: Commit** — `git commit -m 'feat(plan): database storage plugins'`

**Validation:** `go test ./store/...`

## Phase 4: Documentation and CI

- [ ] plan/README.md documents usage, operations, entrypoints, configuration and storage
- [ ] plan.config.example.yaml lists every option
- [ ] GitHub Actions runs gofmt, vet, build and test for plan/**

**Validation:** `go test ./...`

## Acceptance Criteria

- [ ] go test ./... passes in plan/
- [ ] plan create, get, list, set-status, set-progress and validate work from the CLI with JSON output and typed exit codes
- [ ] The same operations are reachable over HTTP, WebSocket and MCP with identical parameters
- [ ] Plans mirror into a second store and plan sync reconciles differences

## Self-Review

| Requirement | Delivered by |
|---|---|
| Every write is validated | 1.3, service.save |
| Status workflow with synonyms | 1.1 config, 1.3 SetStatus |
| Fan-out storage with sync | 1.2 |
| CLI, HTTP, WebSocket, MCP | 2.1, 2.2 |

**Placeholder scan:** none — every step names real files and commands
**Residual risk:** PostgreSQL, MySQL, ClickHouse, MongoDB and Firestore plugins pass the conformance suite only when run against a live engine.

## Next Steps

1. Add Stories as an architectural equal of Plans
2. Optional authentication for the network entrypoints
