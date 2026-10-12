# clued

A Claude Code plugin that gives Claude a memory of its own work, backed by one
Go binary, `mind-palace`:

- **Session capture.** Every hook event Claude Code emits (all but
  `SessionStart`, which starts the daemon instead of being relayed), every
  transcript line, subagent transcript, tool-result blob and file-history
  backup is mirrored into MongoDB in real time by a local daemon, enriched with
  structured metadata, and searchable from any machine that shares the
  database. A session can be restored to disk elsewhere and resumed.
- **Plans and stories.** Design/implementation plans and issue-tracker
  stories are stored as Markdown with YAML front matter under `docs/plans`
  and `docs/stories`, validated against JSON Schemas, moved through a status
  workflow and linked to each other, with the same operations over a CLI,
  HTTP, WebSockets and MCP.

Both are exposed to Claude as MCP tools the plugin registers itself. The
plugin is bash hooks, a shim (`bin/clued`) and two slash commands; everything
else is the `mind-palace` binary. See [`mind-palace/README.md`](mind-palace/README.md)
for the plan and story service in depth.

## Prerequisites

- [Claude Code](https://claude.ai/code).
- A MongoDB instance for session capture (local, Docker or remote). The setup
  command walks you through installing one; plans and stories do not need it.
- `jq` (optional but recommended): the hooks use it to read the config file
  and the plan guard needs it to run.
- The `mind-palace` binary. `/clued-setup` downloads a release build for your
  OS and architecture; to install it yourself:
  `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`
  and make sure `$(go env GOPATH)/bin` is on the PATH Claude Code inherits.

## Install

1. Add the marketplace: `/plugin marketplace add robbiebyrd/clued`
2. Install the plugin: `/plugin install clued@clued` and pick a scope:

   | Scope | Effect |
   |---|---|
   | **User** | Installs for you across all projects (recommended) |
   | **Project** | Installs for all collaborators in this repo (writes to `.claude/settings.json`) |
   | **Local** | Installs for you in this repo only (writes to `.claude/settings.local.json`) |

3. Reload if prompted: `/reload-plugins`
4. Configure: `/clued-setup`. The wizard detects your platform, asks how to
   run MongoDB (local binary, Docker or remote), installs and starts it,
   installs `mind-palace`, writes `~/.claude/plugins/data/clued/config.json`
   and starts the daemon.
5. Start a new Claude Code session so the MCP servers connect.

Verify:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
mind-palace session find --limit 3
```

Remove it again with `/clued-uninstall`, which detects what is present and
asks before stopping the daemon, removing the config and downloaded binary,
and (optionally) the MongoDB binaries, the Docker container and volume, and
the data.

## What the plugin installs

| Piece | Role |
|---|---|
| `hooks.json` | Registers the hooks below for every Claude Code hook event; no manual edit of `settings.json` is needed |
| `hooks/check-setup` | SessionStart: prints one system message when the config file or the `mind-palace` binary is missing |
| `hooks/session-start` | SessionStart (async): re-runs `register-hooks`, rebuilds a dev checkout when `devRepoPath` is set, starts `mind-palace session daemon` if its health check fails, then runs `mind-palace session backfill` |
| `hooks/event-relay` | Every other event: POSTs the hook JSON to the daemon; if the daemon is unreachable the event is appended to a write-ahead log the daemon replays later |
| `hooks/guard-superpowers-plans` | PreToolUse on `Write`/`Edit`: blocks writes under `docs/superpowers/plans/` and points at the plan MCP tools (see [Plan storage](#plan-storage-for-superpowers)) |
| `hooks/register-hooks` | Run by `session-start` on every session start when `jq` is present: mirrors the core entries (`SessionStart`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `SessionEnd`, `Notification`, `SubagentStop`) into `~/.claude/settings.json` as a fallback for setups that do not load `hooks.json`, replacing stale clued entries and leaving everything else alone. The remaining events are relayed only through `hooks.json` |
| `hooks/uninstall` | Non-interactive cleanup used by `/clued-uninstall` |
| `bin/clued` | Shim that finds and runs `mind-palace` (resolution order below) |
| `.mcp.json` | Two stdio MCP servers: `clued` (`bin/clued session mcp`, the session tools) and `mind-palace` (`bin/clued mcp --no-session-tools`, the plan and story tools) |
| `commands/clued-setup`, `commands/clued-uninstall` | The `/clued-setup` and `/clued-uninstall` wizards |
| `skills/clued-setup`, `skills/plan-storage` | The setup walkthrough as a skill, and the skill that routes superpowers plans through mind-palace |

Events relayed to the daemon: `PreToolUse`, `PostToolUse`,
`PostToolUseFailure`, `PostToolBatch`, `Notification`, `UserPromptSubmit`,
`UserPromptExpansion`, `SessionEnd`, `Stop`, `StopFailure`, `SubagentStart`,
`SubagentStop`, `PreCompact`, `PostCompact`, `PermissionRequest`,
`PermissionDenied`, `Setup`, `TeammateIdle`, `TaskCreated`, `TaskCompleted`,
`Elicitation`, `ElicitationResult`, `ConfigChange`, `WorktreeCreate`,
`WorktreeRemove`, `InstructionsLoaded`, `CwdChanged` and `FileChanged`.

### How `bin/clued` finds the binary

1. When `devRepoPath` is set in the config file, a build of
   `<devRepoPath>/mind-palace`, rebuilt whenever a `.go` file there is newer
   than the binary. This wins over everything below, so plugin developers run
   their own checkout.
2. `mind-palace` on the PATH.
3. `~/.claude/plugins/data/clued/bin/mind-palace`, the release asset
   `mind-palace_<os>_<arch>` downloaded by `/clued-setup`.

When a rebuild happens, `session-start` restarts the daemon; relaunch Claude
Code to reconnect the MCP servers to the new build.

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

### Enrichers

An enrichment loop runs inside the daemon every 5 seconds and stores results
under `enrichments.<name>` on each matching document. Documents an enricher
does not apply to are marked skipped so they leave its queue; transient write
failures are retried; a panicking enricher is recorded, not fatal. Disable
any of them with `disabledEnrichers` in the config.

| Enricher | Collection | Records |
|---|---|---|
| `bash-binaries` | `hook_events` | The binary invoked by each stage of a Bash command |
| `bash-outcome` | `hook_events` | Whether a Bash call wrote to stderr and how much it printed |
| `edit-diff-stats` | `hook_events` | Lines added and removed by an Edit or Write call |
| `file-tracker` | `hook_events` | The file each file tool targets, its language and artifact kind (plan, spec, doc, test, config, code) |
| `prompt-features` | `hook_events` | Word count, slash-command name and intent of a user prompt |
| `tool-classifier` | `hook_events` | A category and read-only flag for each tool call |
| `attachment-extractor` | `transcript_lines` | Path, language and size of an attached file |
| `code-language-detector` | `transcript_lines` | Languages of fenced code blocks |
| `error-flag` | `transcript_lines` | Lines that report an error (failed hook, failed tool, error text) |
| `file-snapshot-extractor` | `transcript_lines` | Files and languages in a file-history snapshot |
| `hook-linker` | `transcript_lines` | Links a line to the hook events of the tool calls it mentions |
| `intent-classifier` | `transcript_lines` | question, instruction, feedback, correction, approval or slash_command |
| `line-classifier` | `transcript_lines` | user_prompt, assistant, tool_result, hook_success, hook_error, session_meta or unknown |
| `message-stats` | `transcript_lines` | Characters, words and content blocks |
| `skill-detector` | `transcript_lines` | Skill tool invocations of an assistant line |
| `thinking-stats` | `transcript_lines` | Size of the thinking and text of an assistant line |
| `tool-use-summary` | `transcript_lines` | The tools an assistant line calls and whether it thinks |
| `privacy-redact` | `transcript_lines` | A copy of the raw line with secrets and e-mail addresses masked, stored as an enrichment; the stored line itself is not changed. **Disabled by default** |

### MCP tools (`clued` server)

| Tool | Does |
|---|---|
| `find_sessions` | Find previous sessions, newest first; regex filters on project path, cwd and git origin |
| `get_session_context` | Metadata, the most recent distinct Bash commands, and the first and last transcript lines |
| `get_full_session` | The complete record: metadata, transcript, subagent lines, blob metadata and hook events |
| `search_commands` | Search the Bash commands run in previous sessions |
| `read_transcript` | Transcript lines in order, paginated by offset and limit (default 200, max 500) |
| `restore_session` | Rebuild the main JSONL, subagent files, tool-result blobs and file-history backups on this machine |

### Session commands

The same operations from the shell, via `mind-palace session` (or
`bin/clued session`). Every command takes `--config` (default
`~/.claude/plugins/data/clued/config.json`) and, except `daemon` and `mcp`,
prints JSON.

| Command | Does |
|---|---|
| `session find [--project-path re] [--git-origin re] [--query re] [--limit n]` | Find sessions, newest first |
| `session search-commands <pattern> [--session id] [--git-origin re] [--limit n]` | Search the Bash commands run in sessions |
| `session context <session>` | Metadata, recent commands, first and last transcript lines |
| `session full <session>` | The session joined with its transcript, subagents, blobs and hook events |
| `session transcript <session> [--offset n] [--limit n]` | Read transcript lines |
| `session restore <session> [--project-path p] [--projects-dir d]` | Rebuild the session's files for Claude Code; writes only under the projects and file-history directories |
| `session backfill` | Load every transcript under `projectsDir` into the store (idempotent) |
| `session daemon [--addr host:port]` | Run the capture daemon |
| `session mcp [--mcp-tool-prefix p]` | Serve only the session tools over stdio |

### Configuration

`~/.claude/plugins/data/clued/config.json` (written by `/clued-setup`):

```json
{
  "mongoUrl": "mongodb://localhost:27018",
  "dbName": "claude_sessions",
  "port": 8085,
  "projectsDir": "~/.claude/projects",
  "fileHistoryDir": "~/.claude/file-history",
  "disabledEnrichers": []
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
| `claudeAppConfigPath` | the Claude desktop app's `config.json` | `CLUED_CLAUDE_APP_CONFIG_PATH` | Where the account id is read from |
| (env only) | `events.wal` next to the config file | `CLUED_WAL_PATH` | Write-ahead log; not read from the config file |
| `devRepoPath` | | | Source checkout that `bin/clued` builds and runs instead of an installed binary (plugin developers) |

A leading `~/` is expanded in every path. Unknown keys are ignored, so a
config file from the earlier Node implementation keeps working; its
`mcpPort` is no longer read (the session tools are served over stdio, not
SSE on port 8086).

### Running the daemon by hand

Either let the hook do what every session start does:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

or, when no daemon is running on the port (a second one fails to bind), run
it in the foreground to see its output:

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/clued" session daemon
```

The most common failure is MongoDB not being reachable at `mongoUrl`; check
with `nc -z <host> <port>`. The daemon's log is
`~/.claude/plugins/data/clued/daemon.log`.

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
PostgreSQL, MySQL, ClickHouse, MongoDB or Firestore next to the files.
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

The full reference (front matter, workflow, every operation, storage plugins,
templates, error codes) is in [`mind-palace/README.md`](mind-palace/README.md).

### Plan storage for superpowers

With the plugin installed, implementation plans written by the superpowers
`writing-plans` skill are stored by mind-palace rather than under
`docs/superpowers/plans/`:

- `hooks/guard-superpowers-plans` blocks `Write` and `Edit` under
  `docs/superpowers/plans/` and names the `plan_create` / `plan_update` MCP
  tools to use instead. Design docs under `docs/superpowers/specs/` are not
  affected.
- `skills/plan-storage` tells Claude to fetch a template with
  `plan_getTemplate`, save with `plan_create` or `plan_update`, link a design
  doc with `plan_addSpec`, and read plans back with `plan_get`.
- The `mind-palace` MCP server starts in the project directory, so plans go
  to that project's `docs/plans`. `check-setup` warns at session start when
  the binary is missing.

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
`bin/clued` shim, so plugin changes are covered by `go test` too. CI
(`.github/workflows/mind-palace.yml`) runs all of this against a MongoDB
service container. Pushing a `v*` tag runs `.github/workflows/release.yml`,
which builds `mind-palace_{darwin,linux}_{amd64,arm64}` and attaches them to
the GitHub release that `/clued-setup` downloads from.

To run the plugin against your own checkout, add `"devRepoPath": "/path/to/clued"`
to the config file: `bin/clued` rebuilds the binary when sources change and
`session-start` restarts the daemon.

`docker-compose.transcripts.yml` starts a MongoDB on port 27018 for the
Docker setup option. It publishes the port on every interface without
authentication, which suits a single-user machine or a deliberately shared
LAN database; on a machine reachable from an untrusted network change the
mapping to `127.0.0.1:27018:27017` or enable authentication.
`docker-compose.webhook.yml` starts a local webhook.site for inspecting hook
payloads while developing.
