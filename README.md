# clued

A Claude Code plugin that gives Claude a memory of its own work, backed by one
Go binary, `mind-palace`:

- **Session capture.** Every hook event Claude Code emits (all but
  `SessionStart`, which starts the daemon instead of being relayed), every
  transcript line, subagent transcript, tool-result blob and file-history
  backup is mirrored into MongoDB in real time by a local daemon, enriched with
  structured metadata, and searchable from any machine that shares the
  database. A session can be restored to disk elsewhere and resumed. In
  [remote mode](#remote-mode) the same data goes to a clued server over
  HTTPS instead, and the machine needs neither MongoDB nor a daemon.
- **Plans and stories.** Design/implementation plans and issue-tracker
  stories are stored as Markdown with YAML front matter under `docs/plans`
  and `docs/stories`, validated against JSON Schemas, moved through a status
  workflow and linked to each other, with the same operations over a CLI,
  HTTP, WebSockets and MCP.

Both are exposed to Claude as MCP tools the plugin registers itself. The
plugin is bash hooks, a shim (`scripts/clued`) and two slash commands; everything
else is the `mind-palace` binary. See [`mind-palace/README.md`](mind-palace/README.md)
for the plan and story service in depth.

## Prerequisites

- [Claude Code](https://claude.ai/code).
- A MongoDB instance for session capture (local, Docker or remote), or a
  clued server (`mind-palace server run`, see [Remote mode](#remote-mode))
  and a token for it. The setup command walks you through installing a
  MongoDB; plans and stories do not need either.
- `jq` (optional but recommended): the bash hooks use it to read the config
  file and to mirror the hook entries into `settings.json`.
- Nothing else: the `mind-palace` binary is downloaded on first use from the
  GitHub release the plugin pins, pre-built for macOS (Intel and Apple
  Silicon), Linux (amd64, arm64, 32-bit arm) and Windows (amd64, arm64), so
  no Go toolchain is needed (`curl` or `wget` fetches it). On another platform,
  or without access to github.com, install it with
  `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`
  (and put `$(go env GOPATH)/bin` on the PATH Claude Code inherits) or copy
  a release asset into place; `/clued-setup` covers both.

## Install

1. Add the marketplace: `/plugin marketplace add robbiebyrd/clued`
2. Install the plugin: `/plugin install clued@clued` and pick a scope:

   | Scope | Effect |
   |---|---|
   | **User** | Installs for you across all projects (recommended) |
   | **Project** | Installs for all collaborators in this repo (writes to `.claude/settings.json`) |
   | **Local** | Installs for you in this repo only (writes to `.claude/settings.local.json`) |

3. Reload if prompted: `/reload-plugins`
4. Configure: `/clued-setup`. The wizard detects your platform and asks
   where the data goes: a local MongoDB, a clued server in containers on
   this machine, a clued server someone else runs, or Clued Cloud, the
   hosted service at https://clued.cloud. In a Claude Code cloud container
   (Claude Code on the web) it offers only Clued Cloud and a clued server, and
   links the environment instead of the container: `CLUED_SERVER_URL` and
   `CLUED_SERVER_KEY`, an environment key each container trades for a
   token of its own that expires within a day, in the environment's
   settings link every new container without a config file or a browser
   approval
   ([details](docs/server.md#claude-code-cloud-containers)). For a MongoDB it asks how
   to run it (local binary, Docker or remote), installs and starts it,
   installs `mind-palace`, writes `~/.claude/plugins/data/clued/config.json`
   and starts the daemon. For the server in containers it runs
   `mind-palace server init` (the server, its MongoDB and an nginx HTTPS
   front-end under `~/.clued-server`, with a private CA and a first token,
   with Docker or, on a Mac without Docker, Apple Containers), first
   checking that no other stack, clued cloud or daemon holds its ports,
   starts the stack, offers to trust the CA on this machine
   (`mind-palace server trust`: the keychain, the system store, Chrome and
   Firefox) and fills the config from the result, asking before it replaces
   another server's. A developer can run the server from a source checkout
   with the same certificates instead. For a server
   elsewhere it asks for the URL and the token. In both server modes the
   config is written at `0600`, the token is checked with `mind-palace
   session whoami`, the hooks are registered and no daemon starts.
5. Start a new Claude Code session so the MCP servers connect.

Verify (local mode; in remote mode `scripts/clued session whoami` instead of
the health check):

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
mind-palace session find --limit 3
```

Remove it again with `/clued-uninstall`, which detects what is present and
asks before stopping the daemon (local mode only) or the server stack,
removing the config and downloaded binary, the relay's catch-up state and
log, and (optionally) the MongoDB binaries, the containers and volumes of
the server stack, and the data. It does not remove the plugin itself; finish
with `/plugin uninstall clued@clued` (and `/plugin marketplace remove clued`),
then `/reload-plugins`.

## What the plugin installs

| Piece | Role |
|---|---|
| `hooks/hooks.json` | Registers the hooks below for every Claude Code hook event, once each: it is the file Claude Code loads from an installed plugin (a `hooks.json` at the plugin root is never loaded), so no `settings.json` entry is needed (see [Where the hooks are registered](#where-the-hooks-are-registered)) |
| `hooks/check-setup` | SessionStart: prints one system message when the config file (and an environment that names a server) or the `mind-palace` binary is missing, or when a Claude Code cloud container has no server; in remote mode also when the server is unreachable, rejects the token, or the token does not cover this repository's project |
| `hooks/session-start` | SessionStart (async): re-runs `register-hooks --quiet` (the launcher's `plugin-root`, a stale settings mirror), runs `remove-remember-listener` once, rebuilds a dev checkout when `devRepoPath` is set, restarts the daemon when the binary it runs changed, starts `mind-palace session daemon` if its health check fails, then runs `mind-palace session backfill`. In remote mode, and in a Claude Code cloud container, it starts no daemon: in remote mode the SessionStart event goes through `scripts/clued hook relay` and the backfill runs against the server |
| `hooks/remove-remember-listener` | Run once by `session-start` (a stamp in the data directory) and again by `uninstall`: deletes the `70-clued-ingest.sh` listener older clued versions installed into [claude-remember](https://github.com/Digital-Process-Tools/claude-remember), recognised by its marker line, and touches nothing else (see [Native memory](#native-memory)) |
| `hooks/event-relay` | Every other event: POSTs the hook JSON to the daemon; if the daemon is unreachable the event is appended to a write-ahead log the daemon replays later. In remote mode it runs `scripts/clued hook relay` instead. At SessionEnd it also starts `scripts/clued session memory run --session <id>` detached (the session's memory note), at PreCompact the same with `--checkpoint`, unless `memory.engine` is `off` or the session was started with `CLUED_MEMORY=off`; with `CLUED_NO_CAPTURE=1` (a memory engine's own `claude -p`) it exits at once |
| `scripts/clued hook relay` | Remote mode, every event: stores the event on the server, pushes the transcript lines and artifact files written since the previous hook, replays the WAL; never fails the hook, logs failures to `relay.log` (see [Remote mode](#remote-mode)) |
| `scripts/clued hook write-guard` | PreToolUse on `Write`, `Edit`, `MultiEdit`, `NotebookEdit`, `Bash`, `EnterPlanMode` and `ExitPlanMode`: routes every change to a plan or story through mind-palace (see [Plans and stories are written by mind-palace](#plans-and-stories-are-written-by-mind-palace)) |
| `scripts/clued hook stop-guard` | Stop: blocks the assistant from stopping when its last message dodges ownership of a failure, defers work to another session or asks whether to continue (see [Hooks served by the binary](#hooks-served-by-the-binary)) |
| `scripts/clued hook prime` | SessionStart (startup, resume, clear and after compaction): adds the prime report of the session's directory as context, cut to 8,000 characters and collected within 3 seconds; `primeOnStart: false` turns it off |
| `scripts/clued hook status` | The status line, when the config sets `statusLine: true`: prints the line (the previous status line command's output, or `ctx 37% of 1M · 5h 12% · 7d 40%`) and records the context window's fill, the five-hour and seven-day usage limits, the model and the cost as `LimitsSampled` events |
| `scripts/clued hook status-review` | SessionStart, and UserPromptSubmit at most every 30 minutes: tells the assistant to ask you about plans whose stories are all complete and stories whose steps and acceptance criteria are all done but still open |
| `hooks/register-hooks` | Run by `session-start` on every session start and by `/clued-setup` when `jq` is present: writes the hook launcher `~/.claude/plugins/data/clued/bin/clued-hook` (under `$CLAUDE_PLUGIN_DATA` when set) and `plugin-root` next to it, removes the clued entries an older clued mirrored into `~/.claude/settings.json`, and sets or restores the status line; `--settings` mirrors every `hooks/hooks.json` registration into `settings.json` through the launcher instead (for running the hooks without the plugin's hooks loaded), `--plugin` removes that mirror |
| `hooks/uninstall` | Non-interactive cleanup used by `/clued-uninstall` |
| `scripts/clued` | Shim that finds and runs `mind-palace` (resolution order below) |
| `.mcp.json` | Two stdio MCP servers: `clued` (`scripts/clued session mcp`, the session tools) and `mind-palace` (`scripts/clued mcp --no-session-tools --tools core`, the core tier of the plan and story tools) |
| `commands/clued-setup`, `commands/clued-uninstall` | The `/clued-setup` and `/clued-uninstall` wizards |
| `skills/clued-setup`, `skills/plan-storage` | The setup walkthrough as a skill, and the skill that routes superpowers plans through mind-palace |

Events relayed to the daemon: `PreToolUse`, `PostToolUse`,
`PostToolUseFailure`, `PostToolBatch`, `Notification`, `UserPromptSubmit`,
`UserPromptExpansion`, `SessionEnd`, `Stop`, `StopFailure`, `SubagentStart`,
`SubagentStop`, `PreCompact`, `PostCompact`, `PermissionRequest`,
`PermissionDenied`, `Setup`, `TeammateIdle`, `TaskCreated`, `TaskCompleted`,
`Elicitation`, `ElicitationResult`, `ConfigChange`, `WorktreeCreate`,
`WorktreeRemove`, `InstructionsLoaded`, `CwdChanged` and `FileChanged`.

### Where the hooks are registered

Every hook is registered exactly once. The plugin's `hooks/hooks.json` is
the one registration: Claude Code loads it from the installed plugin and
delivers a hook's `additionalContext` (the prime report) from it, which
`scripts/verify-prime.sh` checks on each release (Claude Code 2.1.300: one
prime report with the plugin's hooks, with a resumed session, with the
settings mirror alone, and after an upgrade that removed an older mirror).
Older clued versions also mirrored the core hooks into
`~/.claude/settings.json` because plugin hook output was reported lost
([anthropics/claude-code#16538](https://github.com/anthropics/claude-code/issues/16538),
closed without a fix and not reproducible on 2.1.300); a hook registered in
both places runs twice, so `register-hooks` now removes that mirror on the
next session start. The status line is the only clued entry `settings.json`
keeps (plugins cannot set one).

`register-hooks --settings` writes the mirror back, every registration of
`hooks/hooks.json` with its command run through the launcher
`~/.claude/plugins/data/clued/bin/clued-hook`, which reads the plugin root
from `plugin-root` next to it; every session start rewrites `plugin-root`,
so the entries survive a plugin root that moves (`clued` to `clued~g2`).
Use it only where the plugin's own hooks are not loaded (a checkout used
without installing the plugin, or a Claude Code build where
`verify-prime.sh` shows plugin hook output lost again): with the plugin
installed too, every hook runs twice. `register-hooks --plugin` removes it.

```bash
scripts/verify-prime.sh            # real claude -p sessions; needs Claude credentials
```

### How `scripts/clued` finds the binary

1. When `devRepoPath` is set in the config file, a build of
   `<devRepoPath>/mind-palace`, rebuilt whenever a `.go` file there is newer
   than the binary. This wins over everything below, so plugin developers run
   their own checkout.
2. The release the plugin pins: the `version` in `.claude-plugin/plugin.json`
   names the GitHub release `v<version>`, whose `mind-palace_<os>_<arch>[.exe]`
   asset for the `uname -s` / `uname -m` of this machine is downloaded on
   first use into `~/.claude/plugins/data/clued/bin/releases/v<version>/`
   and verified against the release's `SHA256SUMS`. A plugin update that
   bumps the version fetches the new release and removes the old copy. Needs
   `curl` or `wget` and `sha256sum` or `shasum`; a failed download is retried
   ten minutes later, not on every hook. `CLUED_RELEASE_URL` points the
   download at a mirror (`off` disables it).
3. `mind-palace` on the PATH.
4. `~/.claude/plugins/data/clued/bin/mind-palace`, a binary placed there by
   hand (or by `/clued-setup` when the download is not possible).

`scripts/clued --locate` prints the path that would run; `check-setup` uses it at
session start and warns when nothing resolves. `session-start` records the
path the daemon was started from and restarts the daemon when that path
changes (a plugin update downloads into a new version directory) or a dev
rebuild happened; relaunch Claude Code to reconnect the MCP servers to the new
build.

## Session capture

### Daemon

`mind-palace session daemon` listens on `127.0.0.1:<port>` (default 8085):

- `POST /event` stores a hook event. The first event for a session starts a
  tailer on its transcript (`transcript_path`) and watchers on its subagent
  JSONL files, subagent meta files, tool-result blobs and file-history
  backups, so those land in the database as they are written.
- `GET /health` is what the hooks and the setup wizard probe.

Store writes happen off the request path, so hooks return immediately.
Guards: session ids must be a single path element, transcripts are tailed
only under `projectsDir`, requests with a browser `Origin` are refused, and
bodies are capped at 16 MiB. Events that reach the write-ahead log (daemon
down or MongoDB unreachable) are replayed on start and every 60 seconds; the
log and its directory are private (`0600`/`0700`).

Each session document records the Claude account (`lastKnownAccountUuid`
from the Claude desktop app's config), the host (hostname, user, platform,
primary IP and MAC) and the git origin and branch of the working directory.
Every query and restore is scoped to the current account. When the account id
cannot be read the daemon logs a warning and stamps `unknown`; every machine
in that state shares that id, so on a database shared between people make
sure `claudeAppConfigPath` is readable on each of them (or give them
separate databases).

### Data model

Collections in `dbName` (default `claude_sessions`): `sessions`,
`hook_events`, `transcript_lines`, `subagent_lines`, `blobs`, plus the
`session_full` view that joins them. Transcript lines are keyed by
`{session_id, seq}` so restarts and backfills never duplicate them. Blobs
hold tool results and subagent meta files as UTF-8 and file-history backups
as base64.

With `redactAtCapture` on, every write is masked first (see
[Configuration](#configuration)): the store, the server and every backup
hold `[REDACTED:<rule>]` where the secret was, and the document lists the
rules under `redacted`. Sessions under `excludePaths` are not written at
all; `session forget` takes back what was, and `retentionDays` bounds how
long MongoDB keeps the rest.

### Enrichers

An enrichment loop runs inside the daemon every 5 seconds and stores results
under `enrichments.<name>` on each matching document. Documents an enricher
does not apply to are marked skipped so they leave its queue; transient write
failures are retried; a panicking enricher is recorded, not fatal. Disable
any of them with `disabledEnrichers` in the config; switch on the ones that
ship disabled (`privacy-redact`, which also switches on its sibling
`privacy-redact-memory`) with `enabledEnrichers`. A name in both lists stays
off.

| Enricher | Collection | Records |
|---|---|---|
| `bash-binaries` | `hook_events` | The binary invoked by each stage of a Bash command |
| `bash-outcome` | `hook_events` | Whether a Bash call wrote to stderr and how much it printed |
| `edit-diff-stats` | `hook_events` | Lines added and removed by an Edit or Write call, with the file and the span (`L10-12`) when the hook carried the patch |
| `file-tracker` | `hook_events` | The file each file tool targets, its language and artifact kind (plan, spec, doc, test, config, code) |
| `prompt-features` | `hook_events` | Word count, slash-command name and intent of a user prompt |
| `tool-classifier` | `hook_events` | A category and read-only flag for each tool call |
| `activity-kind` | `transcript_lines` | What the agent was doing: `user`, `response`, `thinking`, `bash`, `commit`, `edit`, `read`, `task`, `tool`, `result` or `other`, with a label (the tool's name) and a detail (the prompt or reply's start, the command, the file's base name, the pattern, the sub-agent's description, the commit message's first line); for `get_activities` |
| `attachment-extractor` | `transcript_lines` | Path, language and size of an attached file |
| `code-language-detector` | `transcript_lines` | Languages of fenced code blocks |
| `error-flag` | `transcript_lines` | Lines that report an error (failed hook, failed tool, error text); a tool result that is the harness's refusal (denied permission, blocked by a hook, invalid input, declined by the user, symlink) is also `rejected` with its `rejection` and `tool_use_id`, and `search_commands`, `file_history` and `get_activities` leave such calls out unless `include_rejected` |
| `file-snapshot-extractor` | `transcript_lines` | Files and languages in a file-history snapshot |
| `hook-linker` | `transcript_lines` | Links a line to the hook events of the tool calls it mentions |
| `intent-classifier` | `transcript_lines` | question, instruction, feedback, correction, approval or slash_command |
| `line-classifier` | `transcript_lines` | user_prompt, assistant, tool_result, hook_success, hook_error, session_meta or unknown |
| `message-stats` | `transcript_lines` | Characters, words and content blocks |
| `model` | `transcript_lines` | The model in use when the line was written: an assistant line's own, else carried from the nearest preceding assistant line (`source`: `line`, `carried` or `unknown`) |
| `hook-model` | `hook_events` | The model in use when the event happened: the event's own (`event`), the newest assistant line stored by then (`carried`), else the session's latest (`session`) |
| `system-record` | `transcript_lines` | The subtype and payload of a `system` record: a turn's duration, an API error's status and message, a compaction's trigger and tokens, a stop-hook summary |
| `skill-detector` | `transcript_lines` | Skill tool invocations of an assistant line |
| `thinking-stats` | `transcript_lines` | Size of the thinking and text of an assistant line |
| `tool-result-detail` | `transcript_lines` | The facts of a tool result's `toolUseResult`: an edit's file, hunks, span (`L10-12`), lines added and removed and unified diff (16 KB); a command's stdout and stderr (4 KB each), exit code and interruption; a read's range; a sub-agent's description, status and result; with a one-line summary |
| `tool-use-summary` | `transcript_lines` | The tools an assistant line calls and whether it thinks |
| `usage` | `transcript_lines` | The token usage of an assistant line (input, cache writes, one-hour cache writes, cache reads, output, input context) with its model, message id and UTC day, for the `usage` tool |
| `search-text` | `transcript_lines` | The readable text of the line (message text, tool calls and results, no thinking), at most 16 KB, with the credential patterns of `privacy-redact` (not e-mail addresses) masked; MongoDB keeps a text index on it for `search_transcripts` |
| `privacy-redact` | `transcript_lines` | A copy of the raw line with four patterns masked (`sk-…` API keys, e-mail addresses, `AKIA…` AWS access key ids and JWTs), stored under `enrichments.privacy-redact.redacted_line`; the raw line itself is kept unchanged. **Off by default**: add it to `enabledEnrichers` to run it |
| `embedding` | `transcript_lines` | Not registered: a server holding an embedder (`enrichers.Embedding`, e.g. over `session/embed/voyage`) adds it. The int8 vector of a prompt or reply of at least 24 characters (no tool calls or results), credentials masked, for `search_semantic` |
| `privacy-redact-memory` | `hook_events` | The same masked copy of the text of each memory record (`MemorySaved` event: native records, already redacted before they are stored, digests, and claude-remember records captured before native memory); switched on and off together with `privacy-redact` |

### MCP tools (`clued` server)

| Tool | Does |
|---|---|
| `find_sessions` | Find previous sessions, newest first; regex filters on project path, cwd and git origin, or one `live_state` (`working`, `waiting`, `ended`) or `hotness` (`hot`: active in the last 30 minutes, `warm`: the last hour, `cool`: earlier today, `cold`: before); or a `title` regex; each session carries its current `model`, `live_state` and `hotness` once known, and the title, slug, permission mode, pull request link, entrypoint and Claude Code version its transcript's records set |
| `get_session_context` | Metadata, the most recent distinct Bash commands, the first and last transcript lines, the session's latest memory records, and its token usage and estimated cost; with the session's model, state, title and pull request link once known, and its turns (count, total, mean and longest duration) |
| `get_full_session` | The record: metadata, transcript, subagent lines, blob metadata and hook events, or only the `sections` asked for |
| `search_commands` | Search the Bash commands run in previous sessions; rejected calls left out unless `include_rejected` |
| `search_memory` | Search clued's memory across every session and machine of the account (session notes, day, recent and archive roll-ups, notes, handoffs, pinned notes, checkpoints, digests, and the claude-remember records captured before native memory): regex over the text, optional `kind`, `project` (a project key), `include_private`, `session_id`, `git_origin` or `project_path` scope (see [Native memory](#native-memory)) |
| `remember` | Write a note, a `handoff` for the next sessions or the project's `pinned` note; `private` keeps it out of every bundle and export; `project` defaults to the server's working directory's |
| `forget_memory` | Delete memory records by `ids` or a `pattern`; a dry run unless `dry_run` is false; not offered by the server's `/mcp` |
| `get_limits` | The context-window and usage-limit samples the status line captured (`LimitsSampled` events): used percentage and window size, the five-hour and seven-day limits with their reset times, model and cost, newest first; scoped by `session_id`, `git_origin`, `project_path` or `since` |
| `get_activities` | The timeline of what the agent did, newest first: each line's activity kind, label and detail, the session's state changes (`state` entries: working, waiting, ended), consecutive repeats of one tool activity collapsed into an entry with a count; scoped by `session_id`, `git_origin`, `project_path`, `kinds` or `since`; built from the `activity-kind` enrichment and the `SessionStateChanged` events |
| `file_history` | Which sessions touched a file (regex over the path): per session the files, how often it read, edited or wrote them, the lines its edits added and removed, the spans they touched, first and last touch; built from the `file-tracker` and `edit-diff-stats` enrichments |
| `find_errors` | Transcript lines that reported an error (failed hook, failed tool result, error text), newest first, optionally of one type and scoped to a session, repository or project; built from the `error-flag` enrichment |
| `search_transcripts` | Search what was said in previous sessions (prompts, replies, tool calls and results) by words, best match first, with a snippet of each line: any word matches, `"quoted phrases"` must appear, `-words` are excluded; optionally scoped to a session, repository or project. Built from the `search-text` enrichment and, in MongoDB, its text index |
| `search_semantic` | Search prompts and replies by meaning rather than by words, best match first, in the shape of `search_transcripts`; offered only by a server built with an embedder (the `embedding` enrichment and `CluedOptions.Semantic`, as Clued Cloud does), not by `server run` or the local daemon |
| `usage` | Token usage and estimated cost (US dollars at list prices) by session, model or day, each message counted once, optionally scoped to a session, repository, project or range of days; built from the `usage` enrichment. The built-in prices are the ones on Anthropic's pricing page (Claude 5.x, 4.x and Haiku 3.5, with each model's cache-read price and Claude Haiku 5.5's price for prompts over 100,000 tokens); other models have no cost until `modelPrices` names them |
| `get_digest` | The stored retro digest of a day or a range (what `session digest` wrote), with its engine, model and cost; null when none |
| `read_transcript` | Transcript lines in order, paginated by offset and limit (default 200, max 500) |
| `get_lines` | Whole transcript lines by their seq numbers: search first (`search_transcripts`, `find_errors`), then fetch only the lines needed |
| `restore_session` | Rebuild the main JSONL, subagent files, tool-result blobs and file-history backups on this machine |
| `export_session` | Write one session to a redacted Markdown, HTML or JSONL file and return its path; not offered by the server's `/mcp` |
| `forget_session` | Delete captured sessions (by id, project path or last seen before a day) with everything stored for them; not offered by the server's `/mcp` |
| `prime_context` | The context collector's `prime` report for a `cwd` (see [Context collector](#context-collector)); `no_store`, `no_docs` and `no_snapshot` mirror the CLI flags |

Results are cut to a token budget (`max_tokens`; about 4,000 tokens for
searches and lists, 8,000 for reads). A list that does not fit comes back
as `{"items", "truncated": true, "next"}`; pass `next` as `cursor` to
continue. An item too large for the budget alone is returned with its
longest strings clipped.

### Session commands

The same operations from the shell, via `mind-palace session` (or
`scripts/clued session`). Every command takes `--config` (default
`~/.claude/plugins/data/clued/config.json`) and, except `daemon` and `mcp`,
prints JSON.

| Command | Does |
|---|---|
| `session find [--project-path re] [--git-origin re] [--query re] [--title re] [--live-state s] [--hotness h] [--limit n]` | Find sessions, newest first |
| `session search-commands <pattern> [--session id] [--git-origin re] [--include-rejected] [--limit n]` | Search the Bash commands run in sessions |
| `session search <words…> [--session id] [--git-origin re] [--project re] [--limit n]` | Ranked search over the text of transcript lines (what `search_transcripts` does) |
| `session export [--session id]... [--project re] [--since day] [--format md\|html\|jsonl\|zip] [--out path] [--force] [--raw]` | Write sessions to Markdown, a self-contained HTML page, Claude Code's JSONL, or a zip of many (which `session import` reads back); redacted unless `--raw` |
| `session otel-export [--session id]... [--project re] [--since day]` | Send sessions to `otelEndpoint` as traces (older sessions, or after an outage; spans sent before are skipped) |
| `session import <file.jsonl\|export.zip>` | Read a JSONL transcript or a zip export back into the store |
| `session forget [--session id]... [--project re] [--before day] [--limit n]` | Delete captured sessions from the store (local MongoDB or the server) with everything stored for them; at least one criterion; `--limit` deletes the oldest n and reports `more` when others match |
| `session usage [--by session\|model\|day] [--session id] [--git-origin re] [--project re] [--since day] [--until day] [--limit n]` | Token usage and estimated cost (what `usage` does) |
| `session limits [--session id] [--git-origin re] [--project-path re] [--since t] [--limit n]` | The status line's context and usage-limit samples (what `get_limits` does) |
| `session activities [--session id] [--git-origin re] [--project-path re] [--kind k]... [--since t] [--include-rejected] [--limit n]` | What the agent did, newest first, repeats collapsed (what `get_activities` does) |
| `session report [--day d \| --from d --to d] [--include kinds] [--format markdown\|json] [--project-path re] [--git-origin re]` | The activity report of a day or a range: one section per session, one `[HH:MM] <icon> <label>: <detail>` row per activity, sub-agents with what they returned, the size in tokens |
| `session digest [--day d \| --from d --to d] [--engine claude\|none] [--model m] [--force] [--show]` | The first-person retro of a day or a range, written by `claude -p` from the report and kept as a memory record of kind `digest` (`get_digest`, `search_memory`, the prime report's MEMORY section); `--engine none` prints the report and the prompt |
| `session search-memory [pattern] [--kind k] [--project key] [--include-private] [--session id] [--git-origin re] [--project-path re] [--limit n]` | Search memory records (what `search_memory` does) |
| `session memory run [--project p \| --cwd d \| --all] [--session id [--checkpoint]] [--engine e] [--model m] [--delay d] [--quiet]` | Bring memory up to date: session notes, then the day, recent and archive roll-ups (what the SessionEnd and PreCompact hooks start) |
| `session memory status [--project p \| --cwd d \| --all]` | Records per layer, engine runs today against the cap, cost this month, the engine |
| `session memory show [--kind k]... [--include-private] [--bundle [--compact]]` | List the project's records, or the bundle the prime report injects |
| `session memory edit` | Edit the project's records as Markdown in `$VISUAL`/`$EDITOR` |
| `session memory remember [--kind note\|handoff\|pinned] [--private] text…` | Write a note |
| `session memory forget [--id id]... [--pattern re] [--yes]` | Delete records (a dry run without `--yes`) |
| `session memory export [--all] [--out file]` / `session memory import file` | JSONL backup and restore of memory (private notes never exported) |
| `session context <session>` | Metadata, recent commands, first and last transcript lines |
| `session full <session>` | The session joined with its transcript, subagents, blobs and hook events |
| `session transcript <session> [--offset n] [--limit n]` | Read transcript lines |
| `session restore <session> [--project-path p] [--projects-dir d]` | Rebuild the session's files for Claude Code; writes only under the projects and file-history directories |
| `session backfill` | Load every transcript under `projectsDir` into the store (idempotent) |
| `session daemon [--addr host:port]` | Run the capture daemon |
| `session mcp [--mcp-tool-prefix p]` | Serve only the session tools over stdio |
| `session whoami` | Remote mode: ask the server who the token is (name, account, projects, server version); exit 2 when no server is configured, 10 when it is unreachable or rejects the token |

### Native memory

clued keeps its own memory of the work, written as clued's own code (see
[docs/memory.md](docs/memory.md) for the whole story):

- **Session notes.** When a session ends, `hooks/event-relay` starts
  `session memory run --session <id>` detached: the session gets a note
  (`session-summary/<session_id>`: its outcome, decisions, open threads and
  files), and before a compaction a checkpoint the note later replaces.
- **Layers.** After each UTC day ends the day's notes become one
  `memory-day` record; `memory-recent` is rebuilt from the last seven days
  and older days fold into one `memory-archive` per month. Runs are
  idempotent (a run with nothing new makes no engine call) and a record you
  edited is never rewritten.
- **Your notes.** `remember` (MCP), `/clued-remember` (a handoff for the next
  session) and `session memory remember` write notes, handoffs and the
  pinned note injected into every session; `session memory edit` edits
  everything as Markdown, `forget_memory` and `session memory forget`
  delete (dry runs by default).
- **The bundle.** The prime report's MEMORY section injects pinned, handoff,
  recent, the latest session notes and the archive head within
  `memory.budgetTokens` (1500); after a compaction only pinned, handoff and
  checkpoint. Private notes are never injected.
- **Engines.** `extract` (default, free, deterministic), `claude` (your own
  `claude -p`, model `haiku`, at most 20 runs a day, then `extract`),
  `hosted` (Clued Cloud Pro's allowance, then your own key, then
  `extract`) or `off`, chosen in `/clued-setup`. Engine runs set
  `CLUED_NO_CAPTURE=1` and are never captured as sessions.
- **Privacy.** `<private>` spans are dropped and the redaction rules always
  apply before anything reaches an engine or the store; `CLUED_MEMORY=off`
  keeps a session out and `memory.exclude` whole projects.
- **Sync.** Memory records live in the session store, so in server or Clued
  Cloud mode every machine shares one memory; `session memory export` and
  `import` back a local store up.
- **claude-remember.** clued no longer reads claude-remember or writes into
  it: `session-start` removes the listener older versions installed, once,
  and `session memory-ingest` is a no-op for one release. The records
  ingested before stay searchable (kinds `entry`, `day`, `recent`,
  `archive`, `handoff`, `identity`) and are injected only while a project
  has no native record.

### Configuration

`~/.claude/plugins/data/clued/config.json` (written by `/clued-setup`):

```json
{
  "mongoUrl": "mongodb://localhost:27018",
  "dbName": "claude_sessions",
  "port": 8085,
  "projectsDir": "~/.claude/projects",
  "fileHistoryDir": "~/.claude/file-history",
  "disabledEnrichers": [],
  "enabledEnrichers": []
}
```

| Field | Default | Environment override | Description |
|---|---|---|---|
| `mongoUrl` | `mongodb://localhost:27018` | `CLUED_MONGO_URL` | MongoDB connection string (`/clued-setup` writes `27017` for a local install, `27018` for Docker) |
| `dbName` | `claude_sessions` | `CLUED_DB_NAME` | Database name |
| `port` | `8085` | `CLUED_PORT` | Daemon HTTP port |
| `projectsDir` | `~/.claude/projects` | `CLUED_PROJECTS_DIR` | Where Claude Code keeps transcripts; the daemon only tails files under it |
| `fileHistoryDir` | `~/.claude/file-history` | `CLUED_FILE_HISTORY_DIR` | Claude Code's file-history backups |
| `disabledEnrichers` | `[]` | | Enricher names to skip |
| `enabledEnrichers` | `[]` | | Enricher names to switch on among those that ship disabled (`["privacy-redact"]`); `disabledEnrichers` wins when a name is in both |
| `claudeAppConfigPath` | the Claude desktop app's `config.json` | `CLUED_CLAUDE_APP_CONFIG_PATH` | Where the account id is read from |
| (env only) | `events.wal` next to the config file | `CLUED_WAL_PATH` | Write-ahead log; not read from the config file |
| `devRepoPath` | | | Source checkout that `scripts/clued` builds and runs instead of an installed binary (plugin developers) |
| `serverUrl` | | `CLUED_SERVER_URL` | Switches to [remote mode](#remote-mode): the clued server everything is sent to; `mongoUrl` is then ignored |
| `serverToken` | | `CLUED_SERVER_TOKEN` | The bearer token the server minted; the file is written `0600` because of it |
| `serverKey` | | `CLUED_SERVER_KEY` | An environment key (`server token create --environment`, or the cloud's Linked clients page): with no `serverToken`, each client trades it for a short-lived token of its own, cached in `server-token.json` next to this file; meant for a Claude Code cloud environment's settings |
| `serverCaFile` | | `CLUED_SERVER_CA_FILE` | PEM bundle trusted in addition to the system roots (a private CA) |
| `serverInsecure` | `false` | `CLUED_SERVER_INSECURE` | Allow a plain `http://` server URL off loopback; never set it outside a network you trust |
| `redactAtCapture` | off; `/clued-setup` writes `true` | `CLUED_REDACT_AT_CAPTURE` | Mask API keys, AWS access key ids, JWTs, GitHub and Slack tokens, PEM private keys and `<private>…</private>` spans in everything captured before it is written or sent to the server |
| `redactPatterns` | `[]` | | More regular expressions to mask with `redactAtCapture` (e-mail addresses, internal host names) |
| `excludePaths` | `[]` | | Globs over the session's directory (`["~/work/client-*"]`): sessions in or below a match are never stored |
| `retentionDays` | `0` | | MongoDB deletes captured documents after this many days |
| `otelEndpoint` | (none) | `CLUED_OTEL_ENDPOINT` | An OTLP/HTTP collector (Grafana Alloy, Langfuse, Phoenix, an OpenTelemetry Collector) that receives each session as a trace when it ends: a span per prompt turn, tool use and subagent, with token usage, under the GenAI conventions |
| `otelHeaders` | `{}` | | Headers for the collector (`Authorization`); never logged |
| `modelPrices` | (none) | | Prices for the `usage` cost estimates by model id prefix, in US dollars per million tokens: `{"<model id prefix>": {"input": 3, "output": 15}}`, optionally with `cacheRead` (default 0.1× `input`) and `longPrompt` (`{"input", "output", "cacheRead"}` for messages whose prompt is over 100,000 tokens). They come before the built-in table, so they also correct it |
| `primeOnStart` | `true` | `CLUED_PRIME_ON_START` | Start every session (and every session after compaction) with the prime report as context; `false` leaves it to `/clued-prime` |
| `memory.engine` | `extract` | `CLUED_MEMORY_ENGINE` | What writes the [memory](#native-memory) notes: `extract` (free), `claude` (your own `claude -p`), `hosted` (Clued Cloud Pro) or `off` |
| `memory.model` | `haiku` | | The model of the `claude` engine |
| `memory.maxRunsPerDay` | `20` | | Engine calls a UTC day before the work falls back to `extract` (`-1`: no cap) |
| `memory.minPrompts` | `2` | | Fewest prompts a session needs for a note |
| `memory.budgetTokens` | `1500` | `CLUED_MEMORY_BUDGET` | Token budget of the prime report's MEMORY bundle |
| `memory.exclude` | `[]` | | Project path globs (the `excludePaths` syntax) whose sessions get no memory; `CLUED_MEMORY=off` in a session's environment keeps that one session out |

`otelEndpoint` sends clued's own traces out. The other direction, Claude
Code's OpenTelemetry events (API requests with cost and tokens, API errors,
tool results), comes in at `/v1/logs` on the clued server or the local
daemon and shows under `telemetry` in `get_session_context`; see
[Claude Code telemetry](docs/server.md#claude-code-telemetry).

A leading `~/` is expanded in every path. Unknown keys are ignored, so a
config file from the earlier Node implementation keeps working; its
`mcpPort` is no longer read (the session tools are served over stdio, not
SSE on port 8086).

## Remote mode

Remote mode replaces the MongoDB and the daemon on each machine with one
clued server that several machines (or one cloud container that can only
make HTTPS requests) share: `mind-palace server run` serves the session
transport, the per-project plan and story transport and `/api/v1/whoami`
over HTTPS, every route behind bearer tokens that the operator mints with
`mind-palace server token create --name <name> --project <slug>` and can
revoke or extend (`token revoke`, `token grant`) without a restart. Sessions
are stored under the token's account in the server's MongoDB; plans and
stories are namespaced per project, a slug derived from the repository's git
origin (`github.com__owner__repo`), and the token lists the projects it may
use (`*` for all). Enrichment runs on the server. The server is hosted on a
machine of your own with the Compose stack `mind-palace server init`
generates (the server, `mongo:8` and an nginx front-end that terminates
TLS with a generated CA, name-constrained to the stack's hosts, that
`mind-palace server trust` installs in this machine's trust stores, or
`--proxy none` for a terminator of your own) or from the binary; running it
either way, from a source checkout, next to a local clued cloud, TLS and reverse-proxy setups, token
management and the HTTPS `/mcp` endpoint are described in
[`docs/server.md`](docs/server.md).

A client is in remote mode as soon as `config.json` has `serverUrl` set (or
`CLUED_SERVER_URL` is in the environment); `serverToken` carries the token,
and `/clued-setup` writes the file with mode `0600` for that reason. The URL
must be `https://`; a plain `http://` URL is accepted only for a loopback
host, or with `serverInsecure`. `mongoUrl`, `dbName` and `port` stay in the
file but are not used.

What runs on the client:

| | Local mode (`serverUrl` empty) | Remote mode (`serverUrl` set) |
|---|---|---|
| Storage | MongoDB at `mongoUrl` | The server, over `remotesession` (sessions) and the `clued-server` store (plans and stories) |
| Resident process | `mind-palace session daemon` on `127.0.0.1:<port>`, started by `session-start` | None |
| Per hook event | `event-relay` POSTs to the daemon, WAL on failure | `event-relay` runs `scripts/clued hook relay`: stores the event, pushes the transcript delta and changed artifact files (catch-up state under `~/.claude/plugins/data/clued/catchup/`), replays the WAL; failures go to `~/.claude/plugins/data/clued/relay.log`; 5 s budget, never fails the hook |
| Session start | Starts or restarts the daemon, then `session backfill` | Relays the SessionStart event, then `session backfill` against the server |
| `check-setup` | Config file and binary present | Also `session whoami`: warns once per session when the server is unreachable, the token is rejected, or the token does not cover this repository's project (with the `server token grant` command to run) |
| Plans and stories | Files in the repository, plus the stores of `mind-palace.config.yaml` | Files stay primary; every write also goes to the server, and `mind-palace sync --from file --to clued-server` reconciles after an outage |
| Enrichers | In the daemon, from `enabledEnrichers`/`disabledEnrichers` | On the server, from the server's config |

The session MCP tools (`find_sessions`, `restore_session`, …) and `mind-palace
session …` work the same in both modes. `mind-palace session whoami` is the
connectivity probe: it prints the token's name, account and projects, exits
2 when no server is configured and 10 when the server is unreachable or
rejects the token.

### Running the daemon by hand

Either let the hook do what every session start does:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

or, when no daemon is running on the port (a second one fails to bind), run
it in the foreground to see its output:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" session daemon
```

The most common failure is MongoDB not being reachable at `mongoUrl`; check
with `nc -z <host> <port>`. The daemon's log is
`~/.claude/plugins/data/clued/daemon.log`.

## Context collector

`scripts/clued context <probe> [args] [--limit n] [--root dir]` answers one question about the working directory with a single plain-text line (or block) so skills can gather facts without parsing JSON. Every probe always exits 0 and prints a sentinel (`yes`/`no`, `none`, `0`, `unknown`, `not a git repo`, `store unavailable`, `[FALLBACK: ...]`) instead of an error.

The composite report that primes a session:

```
scripts/clued context prime [--no-store] [--no-docs]
```

`--no-store` skips the SESSION lookup in MongoDB, `--no-docs` skips the STORIES and PLANS sections. When the daemon is running, the report is also posted to it as a `ContextCollected` hook event, so every snapshot is stored next to its session; `--no-snapshot` skips that.

| Group | Probes |
|---|---|
| Filesystem | `file-exists`, `dir-exists`, `count-files`, `list-files`, `file-head`, `count-lines`, `find-files`, `count-files-with-content`, `pwd` |
| Git | `git` (read-only passthrough), `git-is-clean`, `git-commits-since-tag`, `git-origin`, `git-branch`, `git-default-branch` |
| GitHub | `gh` (read-only passthrough), `gh-pr-for-branch` |
| JSON/config | `json-field`, `json-count`, `json-count-where`, `json-count-with-key`, `mcp-configured` |
| Environment | `command-exists`, `detect-stack`, `detect-coverage`, `detect-frontend`, `detect-css-framework`, `detect-build-tool`, `detect-port` |
| clued | `host`, `account`, `project-path`, `daemon-status`, `session`, `prime` |

The `clued-context` skill (`skills/clued-context/SKILL.md`) documents every probe, its arguments and its sentinels, and `/clued-prime` runs the report and summarises it. The clued MCP server exposes the same report as the `prime_context` tool, which takes an optional `cwd`.

## Plans and stories

`mind-palace` manages two document kinds in the repository you run it in:

- **Plans** (`docs/plans`): design and implementation plans with numbered
  phases and sections, per-section progress, links to specs, stories and
  other plans, and a status workflow.
- **Stories** (`docs/stories`): bugs, features, improvements, chores and
  tasks with numbered steps, acceptance criteria that gate completion, an
  append-only work log, and links to the plan sections they implement.

Every operation is available as `mind-palace plan …` / `mind-palace story …`,
over HTTP, WebSockets and MCP (`plan_*` and `story_*` tools, served by the
plugin's `mind-palace` MCP server), and can mirror documents into SQLite,
PostgreSQL, MySQL, ClickHouse, MongoDB, Firestore or a WebDAV folder
(Nextcloud, ownCloud, a NAS) next to the files.
Configuration lives in `mind-palace.config.yaml` in the repository (the
example file in `mind-palace/` documents every key).

```bash
mind-palace plan template get              # the layout a plan body must follow
mind-palace plan create --input plan.json  # JSON matching `mind-palace plan schema`
mind-palace story create --input story.json
mind-palace story set-status 0002-a3f in_progress
mind-palace story check 0002-a3f 1         # tick acceptance criterion 1
mind-palace serve                          # HTTP + WebSocket + MCP on 127.0.0.1:8087
```

`mind-palace review` lists the plans whose linked stories are all complete
and the stories whose steps and acceptance criteria are all done while their
own status is still open; the `status-review` hook below runs it for you.

The full reference (front matter, workflow, every operation, storage plugins,
templates, error codes) is in [`mind-palace/README.md`](mind-palace/README.md).

### Hooks served by the binary

Three hooks run `scripts/clued hook …` directly; their rules live in the Go
module (`mind-palace/hookguard` and `service.Palace.Review`), not in bash,
and each reads the hook's JSON on stdin and prints exactly what Claude Code
expects. A fourth, `hook relay`, is the remote-mode event path described
under [Remote mode](#remote-mode).

- **`hook stop-guard`** (Stop, blocking). When the assistant's last message
  contains a phrase that dodges ownership of a failure ("pre-existing",
  "unrelated to my changes"), calls something a known limitation or future
  work, quits because the session is long, defers work to a next session, or
  asks whether to continue work it was already asked to do ("shall I
  continue"), it prints `{"decision":"block","reason":"STOP HOOK VIOLATION:
  …"}` and the assistant is sent back to work with that correction. Patterns
  are matched case-insensitively, the first match wins, and the guard lets
  the assistant stop once it has already fired this turn
  (`stop_hook_active`), so it never loops. The rule table is in
  `mind-palace/hookguard/stop.go`.
- **`hook write-guard`** (PreToolUse, blocking). Keeps plans and stories
  under mind-palace's control; the rules are described in the next section
  and live in `mind-palace/hookguard/write.go`.
- **`hook prime`** (SessionStart, synchronous). Adds the prime report (the
  one `/clued-prime` prints) as context when a session starts, resumes, is
  cleared or comes back from compaction, so the branch, open PR, plans,
  stories, last session and build tool are known from the first turn. It is
  cut to 8,000 characters (the longest sections are trimmed first) and
  collected within 3 seconds; a store that is down costs a line, not the
  session start. Set `primeOnStart` to `false` to turn it off.
- **`hook status-review`** (SessionStart; UserPromptSubmit with
  `--every 30m`). Reviews the plans and stories of the hook's `cwd` and, when
  a plan has all of its stories complete or a story has all of its steps
  complete and all of its acceptance criteria done while its status is still
  open, adds context asking the assistant to ask you whether to close them
  (the hook never changes a status itself). A directory without documents
  stays silent. `--every` keeps one timestamp per directory under
  `~/.claude/plugins/data/clued/status-review/`; SessionStart runs without
  it.

### Plans and stories are written by mind-palace

With the plugin installed, any planning tool (Claude Code's own plan mode,
the superpowers `writing-plans` skill, or Claude deciding to "just write the
file") has to go through mind-palace's templates and CRUD interface. The
`write-guard` hook runs before every `Write`, `Edit`, `MultiEdit`,
`NotebookEdit` and `Bash` call, works out which directories hold the plans
and stories of the hook's `cwd` (from `mind-palace.config.yaml`, else
`docs/plans` and `docs/stories`; `docs/superpowers/plans/` is blocked
wherever it appears) and answers with a `permissionDecision` of `deny` whose
reason names the MCP tool and the CLI command to use instead:

| Call | Decision |
|---|---|
| `Write` or `Edit` of a file that does not exist yet in a plans or stories directory | Denied: fetch the layout with `plan_getTemplate` / `plan_getSchema` and create it with `plan_create` (`story_*` for stories) |
| `Write` (or `NotebookEdit`) over an existing document | Denied: `plan_update` replaces the content; front matter goes through the setters |
| `Edit` whose `old_string` touches the front matter | Denied: `plan_setStatus` (with `plan_getTransitions` for the allowed moves), `plan_setTitle`, `plan_setPriority`, `plan_setType`, the link and progress tools |
| `Edit` of the H1, or of a story's `## Acceptance Criteria` or `## Work Log` | Denied: `plan_setTitle`; `story_setCriterion` / `story_addCriterion` / `story_removeCriterion`; `story_appendWorkLog` |
| `Edit` elsewhere in the body | Allowed, with context reminding the assistant that `plan_update` keeps progress keys consistent |
| Anything under `templates/` | Denied: `plan_createTemplate` / `plan_updateTemplate` |
| An existing file in those directories that is not a document (a README) | Not the guard's business |
| `Bash` that redirects into, or runs `rm`, `mv`, `cp`, `tee`, `touch`, `sed -i`, `perl -i`, `git rm`, … against, one of those directories without invoking `mind-palace` | Denied: `plan_update`, `plan_setStatus`, `plan_delete` and friends |
| `Bash` running `git commit` with `closes`, `fixes`, `resolves` or `completes` and the id of a story that is not complete | Denied: finish its steps and criteria, then `story_setStatus complete`, or drop the closing keyword |
| `EnterPlanMode`, `ExitPlanMode` | Allowed, with context: fetch `plan_getTemplate` first and save the finished plan with `plan_create`, then break it into stories |

Read-only tools are never touched, and the guard prints nothing for calls it
does not concern. `skills/plan-storage` tells Claude the same workflow from
the other side: fetch a template with `plan_getTemplate`, save with
`plan_create` or `plan_update`, link a design doc with `plan_addSpec`, and
read plans back with `plan_get`. Design docs under `docs/superpowers/specs/`
stay plain files. The `mind-palace` MCP server starts in the project
directory, so plans go to that project's `docs/plans`; `check-setup` warns at
session start when the binary is missing.

This repository uses it on itself: the plans that drove each piece of work
are under `docs/plans`, the story tracker under `docs/stories`, and the
original design documents under `docs/superpowers/specs`.

## Development

```bash
cd mind-palace
go build ./... && go vet ./... && test -z "$(gofmt -l .)"
go test -race ./...                                          # in-memory stores
MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./...   # plus the MongoDB suites
```

The URI points at whatever MongoDB you have: `27017` for a local `mongod` or
the CI service container, `27018` for the one `docker-compose.transcripts.yml`
starts.

`mind-palace/plugin` holds Go tests that execute the bash hooks and the
`scripts/clued` shim, so plugin changes are covered by `go test` too. CI
(`.github/workflows/mind-palace.yml`) runs all of this against a MongoDB
service container.

### Protocol reference

[PROTOCOL.md](PROTOCOL.md) describes every entrypoint and the contract it
speaks: the hooks, the shim, the daemon, the MCP servers, the clued server's
HTTP API, the command line and the document model.

### Release binaries

No binary is committed. `scripts/build-binaries.sh [DIR]` cross-compiles
`mind-palace` for darwin/{amd64,arm64}, linux/{amd64,arm64,arm} and
windows/{amd64,arm64} (Go has no windows/arm target and Claude Code does not
run on 32-bit x86; linux/arm is built with `GOARM=6` so it runs on ARMv6 and
ARMv7) and writes `SHA256SUMS` next to them, into the git-ignored `dist/` by
default. The builds are reproducible (CGO off, `-trimpath`, no VCS stamp, the
toolchain pinned to `go.mod`). CI's `binaries` job runs it on every pull request and
push to main as one job per target (`--target os/arch`, the jobs run side by side) to prove
each target compiles, and keeps each binary as a workflow artifact for a
week. `Dockerfile.server` compiles nothing: it copies the Linux binary of
its platform from `dist/` (`BINARIES` names another directory), so build
them first:

```bash
scripts/build-binaries.sh --target linux/amd64 --target linux/arm64 dist
scripts/build-binaries.sh --licenses dist
docker build -f Dockerfile.server -t ghcr.io/robbiebyrd/clued-server:dev .
```

`--licenses DIR` saves the licence of every module the binary links under
`DIR/THIRD_PARTY_LICENSES/` (with `go-licenses` v2.0.1) and packs it as
`DIR/THIRD_PARTY_LICENSES.tar.gz`; the image carries the tree under
`/usr/share/licenses/clued-server/` next to clued's `LICENSE` and `NOTICE`,
and CI fails when a dependency's licence is forbidden, restricted or
unknown.

Releasing is CI's job and needs no tag pushed by hand: every push to main
is a release. The `release` job of `.github/workflows/mind-palace.yml` runs
after the tests and builds passed; when the version in
`.claude-plugin/plugin.json` is already released it bumps the patch version
in `plugin.json` and `marketplace.json`, commits `Release v<next> [skip ci]`
to main and releases that commit, and a version with no release yet (a
minor or major bumped by hand before merging) is released as it is
(`scripts/release-plan.sh` makes that decision, once before the builds so
they stamp the right version and again in the release job). The release
collects the binaries the matrix built with the version stamped in
(`/health` and the client's `User-Agent` report it; `--sums` writes their
`SHA256SUMS`), or rebuilds them in the job when a release that finished
in between took the version they carry, assembles and pushes the server image from the Linux ones as
`ghcr.io/robbiebyrd/clued-server:v<version>` and `:latest`, and creates the
release `v<version>` (which creates the tag at that commit) with the
binaries, `SHA256SUMS` and `THIRD_PARTY_LICENSES.tar.gz`. `scripts/clued` on every machine then downloads
the asset of exactly that release, so the binary a plugin install runs
always matches the hooks it shipped with.

To run the plugin against your own checkout, add `"devRepoPath": "/path/to/clued"`
to the config file: `scripts/clued` rebuilds the binary when sources change and
`session-start` restarts the daemon.

`docker-compose.transcripts.yml` starts a MongoDB on port 27018 for the
Docker setup option. It publishes the port on every interface without
authentication, which suits a single-user machine or a deliberately shared
LAN database; on a machine reachable from an untrusted network change the
mapping to `127.0.0.1:27018:27017` or enable authentication.
`docker-compose.webhook.yml` starts a local webhook.site for inspecting hook
payloads while developing.

## License

clued (the plugin, the `mind-palace` binary and Go module, the daemon and
the self-hosted clued server) is licensed under the
[Apache License 2.0](LICENSE); see [NOTICE](NOTICE). Copyright 2026 Space
Hippo, LC. Clued Cloud (clued.cloud) is a separate product under
FSL-1.1-ALv2 and is not part of this repository.

Native memory (`mind-palace/session/memory`, `mind-palace/session/summary`
and the hooks that run them) is clued's own code under the same Apache-2.0
licence; clued ships no code of claude-remember, whose features it was
modelled on, and writes nothing into it.

Contributions are accepted under the Developer Certificate of Origin: sign
off every commit (see [CONTRIBUTING.md](CONTRIBUTING.md#sign-off)).
Licensing questions go to `legal@clued.cloud`; report security issues
privately to `security@clued.cloud` (see [SECURITY.md](SECURITY.md)), never
in a public issue.
