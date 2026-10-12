---
id: 0014-5fn
title: Project-scoped service API, WebSocket and MCP over HTTPS
purpose: Serve the plan and story service, the WebSocket and the MCP endpoint on the clued server for a project chosen per request.
type: feat
status: in_progress
priority: "2"
effort: L
created: "2026-10-12T20:09:05.232Z"
updated: "2026-10-12T21:14:55.227Z"
started: "2026-10-12T21:14:55.227Z"
tags:
  - server
  - remote
  - mcp
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 13
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
    files:
      - mind-palace/server/server.go
      - mind-palace/server/server_test.go
      - mind-palace/server/httpapi/httpapi.go
      - mind-palace/server/ws/ws.go
      - mind-palace/server/mcpserver/mcpserver.go
      - mind-palace/server/mcpserver/mcpserver_test.go
      - mind-palace/session/sessionmcp/sessionmcp.go
  plans:
    - ["0006-tsw", "included", ["4.2"]]
progress:
  "1":
    status: pending
  "2":
    status: pending
  "3":
    status: pending
---

# Project-scoped service API, WebSocket and MCP over HTTPS

## Problem Statement

`server.New` binds the REST API, `/ws` and `/mcp` to one fixed `Palace`. On the clued server the palace depends on the project, chosen by the `X-Clued-Project` header or `?project=` (decided: the header is the documented pattern, set with `claude mcp add --header`). The MCP endpoint must also offer only the session tools that need no local disk.

**Impact:** Without this the server stores documents but cannot serve the tools, so zero-process clients (cloud sessions) have no MCP.

## Steps

### 1: Resolver

`server.Options.Resolve func(*http.Request) (*service.Palace, error)`; the local `serve` passes a constant resolver; the clued server resolves the project, checks `auth.AllowProject` (403) and returns the cached per-project palace; missing project → 400.

### 2: Thread it through

`httpapi`, `ws` and `mcpserver` take the palace per request or per connection instead of at construction; events and subscriptions stay per project.

### 3: Session tool subset

`sessionmcp.Tools.Subset(remote bool)` excludes `restore_session` and `prime_context` on the HTTPS endpoint; the stdio servers keep the full set.

## Acceptance Criteria

- [ ] VERIFY: cd mind-palace && go test -race ./server/... ./session/sessionmcp/
- [ ] Two projects see different plans through `/api/v1/plans`, `/ws` and `/mcp` on one server
- [ ] A project outside the token's list gets 403 on all three; a missing project gets 400
- [ ] `tools/list` on the HTTPS `/mcp` endpoint contains no `restore_session` or `prime_context`; `mind-palace serve` still works locally

## Files

- `mind-palace/server/server.go` - Resolve option
- `mind-palace/server/server_test.go` - two-project tests
- `mind-palace/server/httpapi/httpapi.go` - per-request palace
- `mind-palace/server/ws/ws.go` - per-connection palace
- `mind-palace/server/mcpserver/mcpserver.go` - per-request palace, header
- `mind-palace/server/mcpserver/mcpserver_test.go` - subset and header tests
- `mind-palace/session/sessionmcp/sessionmcp.go` - Subset

## Work Log
