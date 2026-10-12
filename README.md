# clued

MongoDB session mirror for Claude Code. Captures every hook event and stores it in MongoDB with an enricher pipeline.

## Prerequisites

- [Claude Code](https://claude.ai/code) installed
- A running or accessible MongoDB instance (local, Docker, or remote — setup guides you through this)
- The `mind-palace` binary, which runs session capture and plan storage. `/clued-setup` downloads it for you; to install it yourself: `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`, then make sure `$(go env GOPATH)/bin` is on the PATH that Claude Code inherits

---

## 1. Add the Marketplace

Register the clued marketplace with Claude Code so it can find the plugin:

```
/plugin marketplace add robbiebyrd/clued
```

---

## 2. Install the Plugin

```
/plugin install clued@clued
```

Claude Code will ask you to choose an installation scope:

| Scope | Effect |
|-------|--------|
| **User** | Installs for you across all projects (recommended) |
| **Project** | Installs for all collaborators in this repo (writes to `.claude/settings.json`) |
| **Local** | Installs for you in this repo only (writes to `.claude/settings.local.json`) |

---

## 3. Reload Plugins

If Claude Code prompts you to reload, run:

```
/reload-plugins
```

---

## 4. Configure

Run the setup command and follow the prompts:

```
/clued-setup
```

The setup wizard will:

1. Detect your platform
2. Ask how you want to run MongoDB (local binary, Docker, or remote)
3. Install and start MongoDB if needed
4. Install the `mind-palace` binary (a release download for your OS and architecture)
5. Write a config file to `~/.claude/plugins/data/clued/config.json`
6. Start the daemon and verify it is running

The plugin runs the Go binary through `bin/clued`, a shim that picks the first of:

1. a build from the source checkout named by `devRepoPath` in the config file (rebuilt when its `.go` files change); when `devRepoPath` is set it wins over everything below
2. `mind-palace` on your PATH (from `go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`)
3. `~/.claude/plugins/data/clued/bin/mind-palace`, downloaded from the GitHub release assets `mind-palace_<os>_<arch>`

On every session start `hooks/session-start` starts `mind-palace session daemon` if its health check fails, then runs `mind-palace session backfill`. The session MCP tools (`find_sessions`, `get_session_context`, `get_full_session`, `search_commands`, `read_transcript`, `restore_session`) are provided by the plugin's `.mcp.json`, which runs `${CLAUDE_PLUGIN_ROOT}/bin/clued session mcp` over stdio — no settings edit needed.

---

## 5. Verify

After setup the daemon should be healthy and the CLI should reach MongoDB:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
mind-palace session find --limit 3
```

---

## Manual Configuration Reference

For environments where `/clued-setup` isn't available or you prefer to configure by hand.

### Config file

`~/.claude/plugins/data/clued/config.json`:

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
|-------|---------|----------------------|-------------|
| `mongoUrl` | `mongodb://localhost:27018` | `CLUED_MONGO_URL` | MongoDB connection string |
| `dbName` | `claude_sessions` | `CLUED_DB_NAME` | Database name |
| `port` | `8085` | `CLUED_PORT` | Daemon HTTP port |
| `projectsDir` | `~/.claude/projects` | `CLUED_PROJECTS_DIR` | Where Claude Code stores project data; the daemon only tails transcripts under it |
| `fileHistoryDir` | `~/.claude/file-history` | `CLUED_FILE_HISTORY_DIR` | Claude Code's file-history backups |
| `disabledEnrichers` | `[]` | | Enricher names to skip (e.g. `["privacy-redact"]`) |
| `claudeAppConfigPath` | the Claude desktop app's `config.json` | `CLUED_CLAUDE_APP_CONFIG_PATH` | Claude desktop app config, used to identify the account |
| (env only) | `events.wal` next to the config file | `CLUED_WAL_PATH` | Write-ahead log for events received while MongoDB is unreachable; not read from the config file |
| `devRepoPath` | | | Source checkout `bin/clued` builds and runs, taking precedence over an installed binary |

**Docker MongoDB** uses port `27018` by default to avoid clashing with a local mongod on `27017`.

### MCP server registration

The plugin's `.mcp.json` registers both servers; nothing goes in `~/.claude/settings.json`. The session tools run over stdio through the shim, and `mind-palace` serves the plan-storage tools (`--no-session-tools` keeps the session tools from being registered twice) the plan hook and skill expect (without it a blocked plan write has no `plan_create` tool to fall back on):

```json
{
  "clued": { "command": "${CLAUDE_PLUGIN_ROOT}/bin/clued", "args": ["session", "mcp"] },
  "mind-palace": { "command": "${CLAUDE_PLUGIN_ROOT}/bin/clued", "args": ["mcp", "--no-session-tools"] }
}
```

`mind-palace mcp` and `mind-palace serve` (Streamable HTTP) also expose the session tools next to the plan and story tools.

### Starting the daemon manually

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/register-hooks"
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

or run it directly, in the foreground:

```bash
"${CLAUDE_PLUGIN_ROOT}/bin/clued" session daemon
```

The daemon listens on `127.0.0.1:8085` (`POST /event` for hook events, `GET /health`). It rejects browser-originated requests, caps event bodies, and only tails transcripts under `projectsDir`.

---

## Session commands

Captured sessions live in MongoDB (collections and the `session_full` view are unchanged). Query them with `mind-palace session` (or `bin/clued session`, which resolves the binary):

| Command | Does |
|---|---|
| `session find [--project-path re] [--git-origin re] [--query re] [--limit n]` | Find sessions, newest first |
| `session search-commands <pattern> [--session id] [--git-origin re] [--limit n]` | Search the Bash commands run in sessions |
| `session context <session>` | Metadata, recent commands, first and last transcript lines |
| `session full <session>` | The session joined with its transcript, subagents, blobs and hook events |
| `session transcript <session> [--offset n] [--limit n]` | Read transcript lines |
| `session restore <session> [--project-path p] [--projects-dir d]` | Rebuild the transcript, subagent and blob files for Claude Code |
| `session backfill` | Load the transcripts under `projectsDir` into the store |
| `session daemon [--addr host:port]` | Run the capture daemon |
| `session mcp [--mcp-tool-prefix p]` | Serve only the session tools over stdio |

Every command takes `--config` (default `~/.claude/plugins/data/clued/config.json`) and, except `daemon` and `mcp`, prints JSON.

---

## Context collector

`bin/clued context <probe> [args] [--limit n] [--root dir]` answers one question about the working directory with a single plain-text line (or block) so skills can gather facts without parsing JSON. Every probe always exits 0 and prints a sentinel (`yes`/`no`, `none`, `0`, `unknown`, `not a git repo`, `store unavailable`, `[FALLBACK: ...]`) instead of an error.

The composite report that primes a session:

```
bin/clued context prime [--no-store] [--no-docs]
```

`--no-store` skips the SESSION lookup in MongoDB, `--no-docs` skips the STORIES and PLANS sections.

| Group | Probes |
|---|---|
| Filesystem | `file-exists`, `dir-exists`, `count-files`, `list-files`, `file-head`, `count-lines`, `find-files`, `count-files-with-content`, `pwd` |
| Git | `git` (read-only passthrough), `git-is-clean`, `git-commits-since-tag`, `git-origin`, `git-branch`, `git-default-branch` |
| GitHub | `gh` (read-only passthrough), `gh-pr-for-branch` |
| JSON/config | `json-field`, `json-count`, `json-count-where`, `json-count-with-key`, `mcp-configured` |
| Environment | `command-exists`, `detect-stack`, `detect-coverage`, `detect-frontend`, `detect-css-framework`, `detect-build-tool`, `detect-port` |
| clued | `host`, `account`, `project-path`, `daemon-status`, `session`, `prime` |

The `clued-context` skill (`skills/clued-context/SKILL.md`) documents every probe, its arguments and its sentinels, and `/clued-prime` runs the report and summarises it. The clued MCP server exposes the same report as the `prime_context` tool, which takes an optional `cwd`.

---

## Uninstall

```
/clued-uninstall
```

The uninstall wizard detects what's present on your machine and asks before removing anything. It stops the daemon and removes the downloaded `mind-palace` binary along with the config.

---

## Mind palace (`mind-palace/`)

The repository also ships `mind-palace`, a Go backend for saving, retrieving
and monitoring Plans (design and implementation plans, stored under
`docs/plans`) and Stories (bugs, features, improvements, chores and tasks,
stored under `docs/stories`) as Markdown with YAML front matter. The same
operations are exposed for both kinds over a CLI, HTTP/REST, WebSockets and
MCP, and every store plugin (files, SQL, MongoDB, Firestore) holds both. See
[`mind-palace/README.md`](mind-palace/README.md).

```bash
cd mind-palace && go build -o mind-palace ./cmd/mind-palace   # plus ./cmd/plan and ./cmd/story
./mind-palace plan template get
./mind-palace plan create --input plan.json
./mind-palace story create --input story.json
./mind-palace serve
```

### Plan storage for superpowers

With the plugin installed, implementation plans written by the superpowers
`writing-plans` skill are stored in mind-palace rather than under
`docs/superpowers/plans/`:

- `hooks/guard-superpowers-plans` is a PreToolUse hook that blocks `Write` and
  `Edit` under `docs/superpowers/plans/` and names the `plan_create` /
  `plan_update` MCP tools to use instead. Design docs under
  `docs/superpowers/specs/` are not affected.
- `skills/plan-storage` tells Claude to fetch a template with
  `plan_getTemplate`, save with `plan_create` or `plan_update`, link a design
  doc with `plan_addSpec`, and read plans back with `plan_get`.
- `.mcp.json` starts `mind-palace mcp` in the project directory, so it needs
  the `mind-palace` binary on PATH (see Prerequisites). `check-setup` warns at
  session start when it is missing.
