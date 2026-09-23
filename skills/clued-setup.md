---
name: clued-setup
description: First-time setup for the clued plugin. Run this after installing clued to configure MongoDB and start the daemon.
---

# clued Setup

You are helping the user configure the `clued` Claude Code plugin for the first time.

## Step 1 — Detect platform

Run the following to determine the OS:

```bash
uname -s 2>/dev/null || echo "Windows"
```

- `Darwin` → macOS
- `Linux`  → Linux
- anything else / `Windows` → Windows

## Step 2 — Ask how to run MongoDB

Present the user with two options. **Local binary is the default.**

> "How would you like to run MongoDB for clued?
>
> 1. **Local binary** (recommended) — install MongoDB directly on this machine; no Docker required
> 2. **Docker** — run MongoDB in a container via docker-compose
> 3. **Remote** — connect to an existing MongoDB instance (Atlas, VPS, self-hosted, etc.)"

Wait for their answer before continuing.

## Step 3 — Install and start MongoDB

### Option A: Local binary

#### macOS

Check for Homebrew first:

```bash
command -v brew
```

If Homebrew is available:

```bash
brew tap mongodb/brew
brew install mongodb-community
brew services start mongodb-community
```

If Homebrew is not available, direct the user to download the macOS tarball from
`https://www.mongodb.com/try/download/community` (version 7.x, platform macOS,
package tgz), unpack it, and add the `bin/` directory to their PATH. Then run:

```bash
mkdir -p ~/data/db
mongod --dbpath ~/data/db --port 27017 --fork --logpath ~/data/mongod.log
```

#### Linux

Detect the distro:

```bash
cat /etc/os-release 2>/dev/null | grep ^ID=
```

- `ubuntu` or `debian`: `sudo apt-get install -y mongodb` (or follow the official
  MongoDB apt repo instructions for a newer version)
- `rhel`, `centos`, `fedora`, `amzn`: `sudo yum install -y mongodb-org`
- Other: direct the user to the tarball download at
  `https://www.mongodb.com/try/download/community` (Linux x64, tgz), unpack it,
  and run:

  ```bash
  mkdir -p ~/data/db
  mongod --dbpath ~/data/db --port 27017 --fork --logpath ~/data/mongod.log
  ```

After package install, start the service:

```bash
sudo systemctl start mongod
sudo systemctl enable mongod
```

#### Windows

Check for winget:

```bash
winget --version 2>nul
```

If available:

```bash
winget install MongoDB.Server
```

Otherwise check for Chocolatey:

```bash
choco --version 2>nul
```

If available:

```bash
choco install mongodb
```

Otherwise direct the user to download the Windows MSI installer from
`https://www.mongodb.com/try/download/community` and run it (select
"Run as a Network Service" during install, which auto-starts mongod).

After install on Windows, ensure the service is running:

```bash
net start MongoDB
```

**Local binary default port: `27017`**

Verify MongoDB is accepting connections:

```bash
mongosh --eval "db.runCommand({ ping: 1 })" --quiet 2>/dev/null \
  || mongo  --eval "db.runCommand({ ping: 1 })" --quiet 2>/dev/null \
  || echo "ping-failed"
```

If the ping fails, tell the user and ask them to check the MongoDB install before continuing.

---

### Option B: Docker

Check Docker is running:

```bash
docker info 2>&1 | head -1
```

If Docker is not running, ask the user to start Docker Desktop and press Enter to continue.

The plugin ships a `docker-compose.transcripts.yml` in its root. Locate it:

```bash
echo "${CLAUDE_PLUGIN_ROOT}"
```

Start MongoDB:

```bash
docker compose -f "${CLAUDE_PLUGIN_ROOT}/docker-compose.transcripts.yml" up -d mongodb
```

**Docker default port: `27018`** (mapped from container 27017 to avoid clashing with a local mongod).

Verify:

```bash
docker compose -f "${CLAUDE_PLUGIN_ROOT}/docker-compose.transcripts.yml" ps
```

---

### Option C: Remote

Ask the user for their full MongoDB connection string. Examples:

- MongoDB Atlas: `mongodb+srv://user:password@cluster.mongodb.net/`
- Self-hosted with auth: `mongodb://user:password@host:27017/`
- Plain remote: `mongodb://host:27017/`

No installation or service-start steps needed. Skip straight to Step 4.

---

## Step 4 — Gather clued config

Ask the user for:

| Setting | Default (binary) | Default (Docker) | Remote |
|---------|-----------------|-----------------|--------|
| MongoDB URL | `mongodb://localhost:27017` | `mongodb://localhost:27018` | *(user-provided)* |
| Database name | `claude_sessions` | `claude_sessions` | `claude_sessions` |

Accept the defaults if the user does not specify otherwise. For remote, the URL is required — do not proceed without it.

## Step 5 — Write config file

Write to `~/.claude/plugins/data/clued/config.json` (create parent directories if needed):

```json
{
  "mongoUrl": "<chosen URL>",
  "dbName": "<chosen DB name>",
  "port": 8085,
  "mcpPort": 8086,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": []
}
```

## Step 6 — Register MCP server in Claude settings

Read `~/.claude/settings.json`. Add the MCP server entry under `mcpServers`:

```json
"mcpServers": {
  "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
}
```

If `mcpServers` already exists, merge the `"clued"` key in. Do not duplicate if `"clued"` is already present.

## Step 7 — Add relay hooks to user settings

Read `~/.claude/settings.json`. Add a hook entry for each of these event types, pointing at `${CLAUDE_PLUGIN_ROOT}/hooks/event-relay` with `async: true`:

`PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch`, `Notification`,
`UserPromptSubmit`, `Stop`, `SubagentStop`, `WorktreeCreate`, `WorktreeRemove`,
`InstructionsLoaded`, `CwdChanged`, `FileChanged`

Each entry uses this shape:
```json
{
  "hooks": [
    {
      "type": "command",
      "command": "${CLAUDE_PLUGIN_ROOT}/hooks/event-relay",
      "async": true
    }
  ]
}
```

Do not duplicate entries if they already exist.

## Step 8 — Start the daemon

Run the session-start hook manually to confirm the daemon and MCP server start:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

Wait 2 seconds, then check health:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon is running"
curl -sf http://127.0.0.1:8086/health && echo "mcp server is running"
```

If a health check fails, check for an error by running the process directly in the foreground for 3 seconds:

```bash
timeout 3 node "${CLAUDE_PLUGIN_ROOT}/dist/daemon.mjs" 2>&1 || true
```

Report any error output to the user.

## Step 9 — Confirm to user

Tell the user:

- Which MongoDB option they chose and what URL is configured
- Config written to `~/.claude/plugins/data/clued/config.json`
- Relay hooks added to `~/.claude/settings.json`
- MCP server registered in `~/.claude/settings.json` under `mcpServers.clued` (URL: `http://127.0.0.1:8086/sse`)
- Daemon status (running on port 8085 / failed with error)
- How to add custom enrichers: drop a `.mjs` file in `<plugin-root>/enrichers/` and restart the daemon
- How to enable the privacy-redact enricher: remove `privacy-redact` from `disabledEnrichers` in `config.json` (or set `enabled: true` in the file)
