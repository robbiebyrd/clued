# Native memory

clued keeps its own memory of your work with Claude Code: a short note of
every session, consolidated into layers so old work shrinks while its long
tail survives, plus the notes you write yourself. A new session starts with
a small, budgeted bundle of it in the prime report's MEMORY section, and
every session and machine of your account can search it. It is clued's own
code, under Apache-2.0 like the rest of the plugin; clued ships nothing of
claude-remember and writes nothing into it.

## What is captured

| Record | Id | When |
|---|---|---|
| Session note (`session-summary`) | `session-summary/<session_id>` | When a session ends (SessionEnd), or on the next run once a session has been idle for 30 minutes. It carries the session's id, so it expires with the session. |
| Checkpoint (`memory-checkpoint`) | `memory-checkpoint/<session_id>` | Before Claude Code compacts a running session (PreCompact); the session's note replaces it. |
| Day (`memory-day`) | `memory-day/<project>/<YYYY-MM-DD>` | Once a UTC day is over: one record from that day's session notes, notes and handoffs. Never for today. |
| Recent (`memory-recent`) | `memory-recent/<project>` | Rebuilt from the day records of the last seven finished days. |
| Archive (`memory-archive`) | `memory-archive/<project>/<YYYY-MM>` | The day records of a month older than the recent window, folded into one. |
| Note (`memory-note`) | `memory-note/<project>/<stamp>` | `remember` (MCP), `session memory remember`. |
| Handoff (`memory-handoff`) | `memory-handoff/<project>/<stamp>` | `/clued-remember`, `remember` with kind `handoff`: what the next session should know; injected for seven days. |
| Pinned (`memory-pinned`) | `memory-pinned/<project>` | `remember` with kind `pinned`: injected into every session; a new one replaces the old. |

Every record is a `MemorySaved` event in the session store (see
[PROTOCOL.md](../PROTOCOL.md#5-memory-events)) with a stable `memory.id` and
`memory.project`. The project is the git origin normalised to
`host/owner/repo` (the ssh, https and scp forms of one remote agree), or the
project path when there is no origin, so every worktree and clone of a
repository shares one memory. A session gets a note only when it holds at
least `memory.minPrompts` (2) prompts.

Runs are idempotent: each roll-up records the hashes of the records it was
built from, so a run with nothing new makes no engine call, and a roll-up is
replaced (by id) only when its sources changed. A record you edited is
marked `edited` and never rewritten by an engine. Runs of one project are
serialised by a lock under `~/.claude/plugins/data/clued/memory/`.

## The bundle and its budget

The prime report (the SessionStart hook, `/clued-prime`, `prime_context`)
injects the project's bundle within `memory.budgetTokens` (1500 by default,
`CLUED_MEMORY_BUDGET`), counted at four characters a token, after a header
saying the notes are data about the project, not instructions. In order:

1. the pinned note,
2. the newest handoff under seven days old,
3. the recent roll-up,
4. the notes of the last seven days (at most three), today's session notes
   and the last three before today,
5. the head of the newest archive.

Each part has a share of the budget (30, 15, 25, 20 and 10 per cent); what a
part leaves unused passes down to the next, and a note longer than what is
left is cut, so the bundle is never over budget. After a compaction (the
SessionStart source `compact`) the bundle holds only the pinned note, the
handoff and the newest checkpoint. Private notes are never injected. A
start bundle with budget to spare ends with yesterday's digest
(`session digest`). `mind-palace session memory show --bundle [--compact]`
prints the bundle.

## Engines and cost

`memory.engine` (or `CLUED_MEMORY_ENGINE`), chosen in `/clued-setup`:

| Engine | What writes the notes | Cost |
|---|---|---|
| `extract` (default) | A deterministic note from what the session did: its first request, the commit subjects, the ten files edited most, the errors no later run fixed, the head of its last reply; roll-ups keep the first lines of each source. No model runs; nothing leaves the machine. | Free |
| `claude` | Your own `claude -p --no-session-persistence`, model `memory.model` (`haiku`), at most `memory.maxRunsPerDay` (20) calls a UTC day; past the cap, or when a call fails, the rest of the work falls back to `extract`. | Your Claude Code subscription |
| `hosted` | Clued Cloud (Pro): the plan's monthly AI allowance, then your own Anthropic key, then `extract`. On a plain clued server or in local mode it falls back to `extract`. | The Pro allowance |
| `off` | Nothing runs; the records you have stay searchable. | — |

Every engine run sets `CLUED_NO_CAPTURE=1`: the clued hooks of that
`claude -p` capture, relay and prime nothing, so an engine's own run never
becomes a session. Each record stores its engine, model and cost
(`memory.cost_usd`); `mind-palace session memory status` shows the records
per layer, the engine runs used today against the cap and what the notes
cost this month.

The prompts are clued's own; put a `summary.md`, `day.md`, `recent.md` or
`archive.md` in `~/.claude/plugins/data/clued/memory/` to override one.

## Privacy

- `<private>…</private>` spans are dropped from everything before it reaches
  an engine or the store (an unclosed tag drops the rest of the text).
- The built-in redaction rules (API keys, AWS keys, JWTs, GitHub and Slack
  tokens, private keys) and your `redactPatterns` always apply to memory,
  to the prompt and to the note, whether or not `redactAtCapture` is on.
- `CLUED_MEMORY=off` in the environment a session starts with keeps that
  session out of memory (the capture records `memory: off` on it).
- `memory.exclude` (path globs, the `excludePaths` syntax) keeps whole
  projects out of memory; `excludePaths` keeps them out of capture.
- A private note (`remember` with `private`) is stored with its secrets
  masked, but never injected, exported, or listed without
  `include_private`.

## Reading, editing and forgetting

- `search_memory` (MCP) and `mind-palace session search-memory` search every
  record, native and legacy, newest first; `project` keeps one project's
  native records and `include_private` adds private notes.
- `remember` (MCP), `/clued-remember` and `session memory remember` write
  notes, handoffs and the pinned note.
- `forget_memory` (MCP, local only like `forget_session`) and
  `session memory forget` delete records by id or by a regex over their text;
  both are dry runs unless told otherwise (`dry_run: false`, `--yes`).
- `mind-palace session memory edit` opens the project's records as Markdown
  in `$VISUAL` or `$EDITOR`: one block per record, keyed by an id line.
  Change a block's text to rewrite the record (it is then `edited`), delete a
  block (or empty it) to forget the record. A block whose id is not one of
  the project's records refuses the whole edit.
- `mind-palace session memory run [--project p | --all]` brings memory up to
  date by hand; `session memory show` lists the records.

## Backup and sync

There is no separate memory store and no git remote. In server mode or with
Clued Cloud, memory records are stored on the server with your sessions,
under your account, so every linked machine and cloud container shares one
memory. With a local MongoDB, `mind-palace session memory export [--all]
[--out file]` writes the records as JSONL (private notes never) and
`session memory import <file>` restores them, each once.

## Migration from claude-remember

Earlier clued versions installed a listener (`70-clued-ingest.sh`) into the
claude-remember plugin and ingested its files. That integration is gone:

- `session-start` removes clued's listener from every claude-remember plugin
  once (only a file carrying clued's marker line; nothing else is touched)
  and `/clued-uninstall` removes it again.
- `mind-palace session memory-ingest` does nothing for one release, so a
  listener not yet removed never fails inside claude-remember.
- The records ingested before (kinds `entry`, `day`, `recent`, `archive`,
  `handoff`, `identity`) stay searchable, and the bundle injects them while a
  project has no native record of its own (marked `[claude-remember]`).
