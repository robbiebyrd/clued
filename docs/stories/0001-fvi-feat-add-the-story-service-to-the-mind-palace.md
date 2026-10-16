---
id: 0001-fvi
title: Add the Story service to the mind-palace
purpose: Track stories (bugs, features, chores) with the same tooling, rules and storage as plans.
type: feat
status: complete
priority: "2"
effort: L
created: "2026-10-10T02:27:00.171Z"
updated: "2026-10-16T17:00:49.254Z"
started: "2026-10-10T02:27:00.171Z"
completed: "2026-10-16T17:00:49.254Z"
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
  plans:
    - ["0002-2ub", "included"]
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

# Add the Story service to the mind-palace

## Problem Statement

The plan service only knows about Plans. Stories (the issue tracker) need the same CRUD, workflow, linking, progress and storage behaviour without duplicating the code.

**Impact:** Without it, stories referenced from plans live nowhere and cannot be validated, linked or tracked.

## Steps

### 1: Re-modularise as mind-palace

Move `plan/` to `mind-palace/`, introduce the `kind` package and make the model, config, schema, store, service, ops and entrypoints kind-aware.

### 2: Story rules

Add the story schema and template, `purpose`/`started`, plan-link sections, the acceptance-criteria gate, auto-complete, strict progress on update and the work log.

### 3: Entrypoints

Expose stories through the CLI (`story` group and binary), `/api/v1/stories`, the WebSocket `kind` field and `story_*` MCP tools and `story://` resources.

### 4: Tests and docs

Cover every kind in the conformance suite, service, ops and entrypoint tests; update the README, example config and CI workflow.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test ./...
- [x] A story can be created, linked to a plan section, progressed, checked and completed from the CLI, HTTP, WebSocket and MCP entrypoints
- [x] Every store plugin round-trips both plans and stories

## Files

- `mind-palace/kind/kind.go` - new: the Plan and Story kinds
- `mind-palace/service/` - Palace + per-kind Service
- `mind-palace/schema/story.schema.json` - new: the Story creation schema
- `mind-palace/render/story.md.tmpl` - new: the default Story template

## Proof

- [x] [completeness] Completeness (every spec operation registered for both kinds (ops tests count them))
- [x] [input-validation] Input Validation (per-kind JSON Schemas plus service invariants)
- [x] [thread-safety] Thread Safety (id allocation serialised across kinds; event bus and stores lock)

## Work Log

### 2026-10-10T02:27:00.374Z - Refactor, story rules, entrypoints and tests landed; waiting on the entrypoint walkthrough before closing

### 2026-10-16T17:00:45.149Z - Entrypoint walkthrough (2026-10-16, mind-palace built from main b58298e, file store, fresh git repo per entrypoint): through each of the CLI (story subcommands), HTTP (mind-palace serve: POST /api/v1/stories, ops/addPlanLink, PUT …/status, ops/setProgress, ops/setCriterion, GET), WebSocket (/ws, kind story) and MCP (mind-palace mcp over stdio, story_* tools, 55 of them) a story was created from testdata/create-story.json, linked to plan section 1.1 (included), moved to in_progress, steps 1, 1.1 and 2 completed, both criteria checked, then completed and read back: status complete, completed set, link [plan, included, [1.1]] kept. All four PASS. Script: scratchpad walk/walk.py, log walk/walk.log.
