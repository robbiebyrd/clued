---
id: 0001-v1i
title: Plan service
type: dsgn
status: complete
priority: "2"
effort: L
created: "2026-10-09T20:53:01.474Z"
updated: "2026-10-09T20:53:01.695Z"
completed: "2026-10-09T20:53:01.695Z"
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  web:
    pull-request: https://github.com/robbiebyrd/clued/pull/2
  plans:
    - ["0002-2ub", "blocks"]
---

# Plan service

## Summary

**Goal:** Agents and tools save, retrieve and monitor Plans through one service with the same operations on every entrypoint.

**Problem:** Plans live as loose Markdown under docs/superpowers with no identifiers, lifecycle, links or progress tracking, so nothing can monitor or coordinate them.

**Approach:** A Go service (plan/) that validates every write against a JSON Schema, stores plans as Markdown with YAML front matter in docs/plans, fans writes out to pluggable stores, and exposes the same operations over a CLI, HTTP/REST, WebSockets and MCP.

---

# Part 1 — Design

## Current State

`docs/superpowers/plans` and `docs/superpowers/specs` hold hand-written designs and implementation plans with no shared front matter. The repository is a TypeScript Claude Code plugin; the plan service is a separate Go module under `plan/`.

## Decisions

- **Markdown with YAML front matter is the canonical plan representation** — It diffs well in git, agents can read it directly, and every database store persists the same document and parses it on read, so copies never diverge in shape.
- **One operation registry feeds every entrypoint** — The spec requires identical function names, inputs and results across CLI, HTTP, WebSocket and MCP; registering each operation once with a typed parameter struct gives that for free, including MCP tool schemas.
- **Configuration patches the embedded JSON Schema's enumerations** — Types, statuses, priorities and efforts are configurable; rewriting the schema's enums at load time keeps validation and configuration from disagreeing.
- **Writes fan out through a MultiStore; the first store is read from** — Keeps every enabled copy up to date without the service knowing how many stores exist; a secondary failure returns the plan with a warning and `sync` reconciles.
- **Status changes follow a configurable workflow with `force` as the escape hatch** — Matches the default progression in the spec while letting an operator skip a step deliberately.

### Rejected alternatives

- **Extending the existing TypeScript daemon** — The spec asks for a Go service and the plan manager has no dependency on the session mirror.
- **Storing front matter as columns in SQL stores** — Each dialect would need its own mapping for nested links and progress; storing the rendered document keeps one serialisation.

## Design

### Model and markdown

`model` defines Plan, FrontMatter, Link tuples and Progress with numerically ordered quoted keys. `markdown` splits and renders documents, finds numbered headings and keeps the H1 in sync with the title.

### Config and schema

`config` carries types, statuses with synonyms, the workflow, priorities, efforts and storage definitions, loaded from `plan.config.yaml`. `schema` embeds the Plan JSON Schema, patches its enums from the config and validates create input and stored front matter.

### Service

`service` implements every operation: create (id allocation, template rendering, progress seeding), reads, update, delete with link protection, validate, templates, field setters with workflow enforcement, links, progress and sync. Errors are typed (NotFound, ValidationError, InvalidTransition, ImmutableField, UnknownSection, LinkedPlan, Conflict, StorageError).

### Storage

`store` defines the Store interface, plugin registry, MultiStore fan-out and Sync with error/skip/overwrite modes. Plugins: file (default), memory, sqlite/postgres/mysql/clickhouse, mongodb, firestore. `store/storetest` is the conformance suite.

### Entrypoints

`ops` is the registry. `cli` (cobra) prints JSON with per-kind exit codes. `server/httpapi` exposes `/api/v1/ops/{op}` plus resource routes and an SSE stream. `server/ws` handles request frames and subscriptions. `server/mcpserver` serves Streamable HTTP and stdio with a tool per operation, `plan://` resources and a `watch` tool.

### Error Handling

Every failure is a typed service error carried unchanged through each entrypoint: the CLI maps kinds to exit codes, HTTP to status codes, WebSocket and MCP to error payloads. A failed validation never writes.

## Considerations

- **Security:** No new network exposure by default: the CLI is local and `serve` binds to 127.0.0.1. WebSocket connections check the browser origin; MCP uses the SDK's localhost protection. SQL table prefixes are restricted to identifiers.
- **Performance:** Plans are small text files; file storage rescans the directory per operation, which is fine for hundreds of plans. Database stores index status and type.

## Testing

Unit tests per package (model, markdown, config, schema, render, store, service, ops). Entrypoint tests drive the HTTP API through httptest, the WebSocket through gorilla, the MCP server through the official client over Streamable HTTP, and the CLI through its App. A conformance suite runs every store plugin; networked engines run when a DSN is set.

## Scope

**In scope:**

- Plans: create, read, update, delete, validate, templates, fields, links, progress, sync
- File, SQL, MongoDB and Firestore storage plugins
- CLI, HTTP/REST, WebSocket and MCP entrypoints

**Out of scope:**

- Stories — architecturally equal to Plans; tracked as a follow-up plan
- Authentication on the network entrypoints — the service binds to localhost; put it behind a proxy when exposing it
