---
id: 0024-mfj
title: Serve a core tier of the plan and story MCP tools
type: impr
status: complete
priority: "1"
effort: S
created: "2026-10-14T03:13:00.422Z"
updated: "2026-10-14T03:59:05.279Z"
started: "2026-10-14T03:13:19.766Z"
completed: "2026-10-14T03:59:05.279Z"
tags:
  - mcp
  - tokens
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  plans:
    - ["0007-w5t", "included"]
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

# Serve a core tier of the plan and story MCP tools

## Problem Statement

The plugin's mind-palace MCP server registers 99 plan and story tools, and every tool's schema is read by the model on every turn whether or not plans are in use. Most sessions need a dozen of them. A core tier cuts the cost without removing anything from the CLI or from callers that ask for the full set.

## Steps

### 1: Core flag on ops

Add `Core` to `ops.Op` and mark the core set listed in plan 0007-w5t §5.

### 2: Tier option

`mcpserver.Options.Tier`; `mind-palace mcp --tools core|full` and `MIND_PALACE_MCP_TOOLS`; default full.

### 3: Plugin wiring

`.mcp.json` passes `--tools core`; skills and commands only name core tools.

### 4: Tests and docs

Tool lists per tier, the skills check, tools/list size of both tiers in the README and PROTOCOL.md.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test ./server/mcpserver/ ./cli/ ./plugin/
- [x] VERIFY: `mind-palace mcp --tools core` lists only core tools and `--tools full` lists all of them
- [x] VERIFY: Every mind-palace tool named in skills/ and commands/ is in the core tier

## Work Log

### 2026-10-14T03:18:39.721Z - Core tier in ops (coreOps, Op.Core, Op.CompactInputSchema), mcpserver.Options.Tier, mcp --tools core|full and MIND_PALACE_MCP_TOOLS; the plugin serves core. tools/list: full 99 tools / 76,886 bytes, core 49 tools / 26,756 bytes (create's inlined schema is replaced by a pointer to getSchema). Tests: TestCoreTier, TestMCPCoreToolTier, TestPluginServesTheCoreToolTier; ops, mcpserver, cli and plugin pass with -race.
