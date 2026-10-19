---
id: 0058-lmx
title: Remote code adapter (`code/remotecode`)
purpose: The client side of the transport, used by `codesync`.
type: feat
status: complete
priority: "3"
effort: S
created: "2026-10-19T18:05:23.491Z"
updated: "2026-10-19T19:31:36.051Z"
started: "2026-10-19T19:25:03.406Z"
completed: "2026-10-19T19:31:36.051Z"
tags:
  - plan-0010-awy
  - wave-5
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 5
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/code/remotecode/remotecode.go
      - mind-palace/code/remotecode/remotecode_test.go
  web:
    plan-0010-awy: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md
  stories:
    - ["0057-ykp", "depends"]
  plans:
    - ["0010-awy", "included", ["2.2"]]
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

# Remote code adapter (`code/remotecode`)

## Problem Statement

The client side of the transport, used by `codesync`.

**Working on this concurrently.** This story is in wave 5 of plan 0010-awy. Start it when these are complete: `0057-ykp` Write-only code API (`server/codeapi`) and `CluedOptions.Code`. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0064-7z3` Code sync run (`codesync.Run`); `0070-4j4` GitHub-backed projects in the code API.

**Impact:** Blocks the later stories of plan 0010-awy that depend on it.

## Steps

### 1: Read the plan section

Read plan 0010-awy sections 2.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0010-awy-dsgn-continuous-code-collection-for-the-code-graph.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Implement the writes and `capabilities`

Map 404, 403, 409 and 413 to typed errors (`ErrUnsupported`, `ErrForbidden`, `ErrConflict`, `ErrTooLarge`, plus `ErrQuota` for kind `quota`).

### 3: Pass the suite over HTTP

Reads in the suite use the server-side store handle since the API has none.

### 4: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/remotecode/...`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./code/remotecode/...
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./code/remotecode/...
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/code/remotecode/remotecode.go` - client for the write routes and `capabilities` over `remote.Client`
- `mind-palace/code/remotecode/remotecode_test.go` - `codetest` over `httptest` against `codeapi`, reads through the server-side store

## Work Log

### 2026-10-19T19:31:30.250Z - Added code/remotecode: Store (code.Store over server/codeapi through remote.Client) with New(client, project), Project(), Capabilities(ctx) (codeapi.Capabilities; any 404 is ErrUnsupported), MissingBlobs (POST manifest), PutBlob (raw PUT blobs/{sha256}; Size != len(Content) and malformed sha refused locally as code.ErrInvalid), PutWorktree (one PUT per ref, owner/source not sent), Commit (invalid-UTF-8 upsert/delete paths refused locally as code.ErrInvalid; owner and entry session not sent), RemoveWorktree, TouchSession; RemoveRef, AppendEvents, SHA256ForGitOID and the reads return ErrUnsupported without a request. Typed errors: ErrUnsupported (404 without a code error: codeapi.NotFound's 'no route' or a non-envelope 404, and 405), ErrForbidden (403, also remote.ErrForbidden), ErrConflict (409, also code.ErrMissingBlob), ErrTooLarge (413 too_large, also remote.ErrTooLarge), ErrQuota (413 quota); 400 invalid wraps code.ErrInvalid, 404 not_found code.ErrNotFound, 401 remote.ErrUnauthorized; *remotecode.Error carries Status()/Kind() and unwraps to the server detail as *remote.Error. Tests: codetest.Run over httptest+auth.Middleware+codeapi (33 subtests; RemoveRef/AppendEvents routed to the server-side store, reads via Split) plus capabilities, unsupported (envelope catch-all and bare mux), not found vs unsupported, forbidden (Allow and BeforeWrite refusal), unauthorized, conflict then retry, too_large vs quota, local checks with no request sent, no-route methods, stamping of writes, New: 49 passing (sub)tests with -race. gofmt/vet/build clean; go test -race ./code/... ./remote/... ./server/... ./store/remotestore/... ./session/remotesession/... ./cli/... ok. DEVIATION: one file outside the list, mind-palace/remote/client.go: added remote.Response and Client.Send (raw body with a content type, response returned unmapped, same headers and key-exchange retry), with send refactored onto the shared roundTrip and its behaviour unchanged (remote tests pass). Needed because Do only sends JSON (PUT blobs takes raw bytes) and collapses 413 to ErrTooLarge without the envelope kind, so quota could not be told apart from too_large.
