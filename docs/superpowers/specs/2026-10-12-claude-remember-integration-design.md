# Design: capture claude-remember summaries in the clued store

Date: 2026-10-12. Status: accepted (plan 0006).

## Problem

[claude-remember](https://github.com/Digital-Process-Tools/claude-remember)
compresses every Claude Code session into Haiku-written memory files on disk
(`now.md` entries, `today-*.md`, `recent.md`, `archive.md`, the `/remember`
handoff and `identity.md`). They live per project under `<project>/.remember/`
or `~/.remember/<slug>/` and are only loaded back into the sessions of that
project on that machine. clued already captures the raw transcripts those
summaries are made from, but has no copy of the summaries, so other sessions
and other machines cannot search them.

## What claude-remember offers

- Every memory file is written as a sibling temp file renamed over the
  target, so a reader never needs a lock.
- `hooks.d/<event>/` listeners run on `after_save` (after each `now.md`
  append) and `after_consolidate` (after `recent.md` / `archive.md` are
  rewritten) with `PROJECT_DIR`, `PIPELINE_DIR` and `REMEMBER_DIR` exported,
  a 120 s detached budget, and a requirement that the script is owned by the
  user and not group- or world-writable. The directory is
  `$PIPELINE_DIR/hooks.d`, inside the plugin install, so a plugin update can
  remove listeners placed there.
- The session id and the entry text are not passed to listeners; entries
  carry `## HH:MM | <branch>` headers only. The handoff has no listener event.
- `recent.md` and `archive.md` are rewritten wholesale; `now.md` and
  `today-*.md` are append-only.

## Design

1. **`memory` package (business rules).** `Scan(dir)` parses a remember
   store into records: one per `## HH:MM | branch` entry of `now.md` and
   `today-*.md` (kind `entry` / `day`), one per version of `recent.md`,
   `archive.md` and their rotated slices (kind `recent` / `archive`), one per
   handoff file (kind `handoff`) and one for `identity.md` (kind `identity`).
   Each record carries the source file, entry time and branch when known,
   the text and a SHA-256 of kind + text. `Locate(cwd, home)` returns the
   candidate stores for a project: `<cwd>/.remember` and
   `~/.remember/<slug>` with the Claude Code slug of the cwd. A per-store
   state file under `~/.claude/plugins/data/clued/remember/` records the
   hashes already posted so a rescan is idempotent.
2. **`MemorySaved` events.** Each new record is posted to the daemon as a
   synthetic hook event (the `ContextCollected` precedent): `hook_event_name`
   `MemorySaved`, `cwd`, optional `session_id` (the newest transcript of the
   project when none is given, as claude-remember itself does), and a
   `memory` object (kind, file, time, branch, slug, remember_dir, text, hash).
   When the daemon is unreachable the event goes to the write-ahead log the
   daemon replays, exactly like relayed hook events.
3. **Store dedup and query.** Session stores skip a `MemorySaved` event
   whose `memory.hash` the account already holds (a unique sparse index in
   MongoDB; a set in memory) and gain `MemoryEvents(MemoryQuery)`: regex over
   the text, optional kinds, session ids and project path, newest first.
   The conformance suite covers both.
4. **Tools.** `search_memory` (MCP) and `mind-palace session search-memory`
   (CLI) search across every session and machine of the account;
   `get_session_context` lists the latest memory records of the session.
   `mind-palace session memory-ingest [--dir D] [--cwd C]` is the ingestion
   entrypoint.
5. **Plugin wiring.** `hooks/install-remember-listener` writes
   `70-clued-ingest.sh` into the `after_save` and `after_consolidate`
   directories of every installed claude-remember plugin, pointing at this
   plugin's `scripts/clued`; `session-start` re-runs it on every start (so a
   plugin update that wiped the listener heals itself) and runs an ingest for
   the cwd in the background; `SessionEnd` runs an ingest too, so handoffs
   and stores without a listener are still captured, one session late at
   most.

## Rejected alternatives

- Changing claude-remember to post to clued: a fork to maintain, and the
  listener contract already exists.
- Re-summarising transcripts inside clued: duplicates Haiku spend and
  diverges from what the user's sessions actually read back.
- Watching the files with inotify from the daemon: not portable, and the
  daemon does not know which projects run claude-remember.

## Out of scope

- A MEMORY section in `prime_context`; summaries are reachable through
  `search_memory` and `get_session_context` first.
- Writing clued data back into claude-remember's store.
