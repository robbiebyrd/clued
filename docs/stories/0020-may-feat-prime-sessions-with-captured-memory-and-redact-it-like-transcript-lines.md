---
id: 0020-may
title: Prime sessions with captured memory and redact it like transcript lines
purpose: 'The two follow-ups of plan 0006-6lm: prime_context shows the newest claude-remember notes of the project, and privacy-redact also masks the captured memory text.'
type: feat
status: in_progress
priority: "2"
effort: S
created: "2026-10-13T16:37:58.815Z"
updated: "2026-10-13T16:50:45.694Z"
started: "2026-10-13T16:37:58.815Z"
tags:
  - memory
  - prime
  - enrichers
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
---

# Prime sessions with captured memory and redact it like transcript lines

## Problem Statement

Plan 0006-6lm captured claude-remember's summaries into the store and made them searchable, but a new session still has to ask for them: the prime report has no memory section. The privacy-redact enricher masks transcript lines only, so a secret pasted into a session summary is stored unmasked once captured.

**Impact:** Sessions start without the conclusions of the previous ones, and the one enricher meant to keep secrets out of the store skips the documents most likely to be read by people.

## Steps

### 1: MEMORY section

`prime.Collector` gains a `MemorySource` port (`Recent(cwd, limit)`), implemented by `sessionsource` over `query.SearchMemory` scoped to the project path; the MEMORY section (between SESSION and BUILD) shows the five newest notes as `<kind> <day> [<time>] (<branch>)` plus their text cut to eight lines, with the SESSION sentinels; the CLI and the MCP tools wire it.

### 2: privacy-redact-memory

`session.Enricher` gains a `Group`; `Active` matches a listed name against the name or the group. `privacy-redact-memory` (group `privacy-redact`, collection `hook_events`) masks `memory.text` of MemorySaved events into `enrichments.privacy-redact-memory.redacted_text` with the same patterns, so enabling `privacy-redact` switches both on.

### 3: Tests and docs

Prime, sessionsource, enricher registry and enricher tests; the context skill, prime command, README and setup docs describe the section and the sibling enricher.

## Acceptance Criteria

- [x] VERIFY: `cd mind-palace && go test -race ./collect/... ./session/... ./cli/` passes with the MEMORY section in the report and both redactors active when `privacy-redact` is enabled

## Work Log
