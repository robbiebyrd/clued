---
id: 0040-jka
title: Generate a daily or range retro digest of captured sessions
purpose: A day's or a range's sessions are rendered as an activity report and summarised by a model into a short first-person engineering retro, cached per day.
type: feat
status: complete
priority: "3"
effort: M
created: "2026-10-14T04:28:51.552Z"
updated: "2026-10-14T05:39:19.556Z"
started: "2026-10-14T05:34:27.472Z"
completed: "2026-10-14T05:39:19.556Z"
tags:
  - sessions
  - digest
  - reporting
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  stories:
    - ["0034-8ni", "depends"]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
---

# Generate a daily or range retro digest of captured sessions

## Problem Statement

clued can search and prime from captured sessions but cannot tell the story of a day. AgentHUD's most used output is a digest: it renders each session's activities as `[HH:MM] <icon> <label>: <detail>` rows (user, response, bash, edit, thinking, task by default; read, glob and commits optional; sub-agents nested with their returned text), estimates the report's size, and feeds it to `claude -p` with a prompt asking for a 300 to 500 word first-person retro under `Context`, `Key Accomplishments`, `Technical Insights`, `Major Code Changes` and `Open Questions / Next Steps`, omitting empty sections. Range digests wrap each cached daily digest in `<day date="…">` and ask for `Range Overview`, `Major Outcomes`, `Workstreams`, `Recurring Patterns` and `Carried-Over / Unfinished Work`. Past days are cached; today is regenerated.

**Impact:** Reviewing what happened across sessions means reading transcripts, and nothing carries a day's work into the next session's memory.

## Steps

### 1: The activity report

`session report [--day YYYY-MM-DD | --from … --to …] [--include user,response,bash,edit,thinking,task,read,commit] [--format markdown|json] [--project re] [--git-origin re]` renders the account's sessions of the period from the activity timeline (the activity-kind enrichment, with tool-result details for edits and commands), one section per session with its project, model, start and end times, nested sub-agents and their results, details cut at 120 characters, and an estimated token count with a warning above 300,000.

### 2: The digest

`session digest [--day | --from --to] [--model m] [--engine claude|codex|none] [--force]` pipes the report to `claude -p --no-session-persistence --output-format stream-json` with the daily prompt (a template under `mind-palace/session/digest/templates/`, overridable from the config directory), reads the result's text and cost, and stores the digest as a `DigestGenerated` hook event (`day` or `range`, `text`, `engine`, `model`, `cost_usd`, `report_tokens`) so every machine of the account shares it. A range digest is built from the cached dailies, generating the missing ones first. `--engine none` prints the report and the prompt for another tool.

### 3: Reuse

`get_digest` session tool and `session digest --show` return a cached digest; `prime_context`'s MEMORY section includes yesterday's digest when there is one, and `search_memory` indexes digests as kind `digest`.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./session/digest/ ./cli/
- [x] `session report --day` on seeded sessions renders the documented rows, nests sub-agents, honours `--include` and reports the token estimate
- [x] `session digest --engine none` prints the report and the prompt; with a stubbed `claude` on PATH the digest is stored once and returned by `get_digest`, and a range digest wraps the cached dailies
- [x] PROTOCOL.md documents the report format, the event and the tools

## Files

- `mind-palace/session/digest/report.go` - the report renderer
- `mind-palace/session/digest/digest.go` - the engine runners and the cache as events
- `mind-palace/session/digest/templates/daily.md` - the daily prompt
- `mind-palace/session/digest/templates/range.md` - the range prompt
- `mind-palace/cli/session.go` - `session report` and `session digest`
- `PROTOCOL.md` - sections 6.1 and 9

## Work Log

### 2026-10-14T05:39:11.712Z - Added session/digest: Period (day or range), Build renders the activity report from the timeline (one section per session with title, project, repository, model and span; `[HH:MM] <icon> <label>: <detail>` rows, details cut at 120 characters, repeats marked ×N, a sub-agent's returned text under its task; default kinds user, response, bash, edit, thinking, task; token estimate with a warning past 300,000), the embedded daily.md and range.md prompts (overridable from <config dir>/digest/), the claude engine (`claude -p --no-session-persistence --output-format stream-json --verbose`, result text and cost) and Generate with the cache. The digest is stored as a MemorySaved event of kind `digest` rather than a DigestGenerated event: that one choice makes search_memory, get_digest and the prime MEMORY section (which now leads with yesterday's digest) work on the same record, with the scope, from, to, engine, model, cost and report size inside `memory`. `session report`, `session digest` (--engine claude|none, --model, --force, --show, --template-dir; the codex engine was left out, nothing drives it yet) and the get_digest tool (15 tools, 13 on the server). Docs in PROTOCOL.md 5.2, 6.1 and 9, README. gofmt, vet and `go test -race ./...` pass.
