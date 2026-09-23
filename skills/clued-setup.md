---
name: clued-setup
description: First-time setup for the clued plugin. Run this after installing clued to configure MongoDB and start the daemon.
---

# clued Setup

You are helping the user configure the `clued` Claude Code plugin for the first time.

## Step 1 — Detect platform

```bash
uname -s 2>/dev/null || echo "Windows"
```

- `Darwin` → macOS
- `Linux`  → Linux
- anything else / `Windows` → Windows

Also detect CPU architecture on macOS/Linux — needed for tarball download URLs:

```bash
uname -m   # arm64 = Apple Silicon; x86_64 = Intel
```

## Step 2 — Ask how to run MongoDB

Present the user with three options. **Local binary is the default.**

> "How would you like to run MongoDB for clued?
>
> 1. **Local binary** (recommended) — install MongoDB directly on this machine; no Docker required
> 2. **Docker** — run MongoDB in a container via docker-compose
> 3. **Remote** — connect to an existing instance (Atlas, VPS, self-hosted, etc.)"

Wait for their answer before continuing.

## Step 3 — Install and start MongoDB

### Option A: Local binary

#### macOS

**Try Homebrew first.** Important: Homebrew has a hard dependency on the current Xcode
version and will refuse to install even pre-built bottles if Xcode is outdated.
Attempt the install and check for errors before proceeding:

```bash
brew tap mongodb/brew 2>&1
brew install mongodb/brew/mongodb-community 2>&1 | grep -E "Error:|successfully installed|already installed"
```

**If Homebrew succeeds:**

```bash
brew services start mongodb/brew/mongodb-community
```

Homebrew's `brew services` registers a launchd agent automatically — mongod will
restart at login without any additional steps.

**If Homebrew fails** (common error: "Your Xcode (X.Y) is too outdated. Please update
to Xcode Y.Z"), fall back to the **direct binary tarball**. This is a pre-compiled
binary that requires no compiler or Xcode at all:

```bash
ARCH=$(uname -m)   # arm64 or x86_64
curl -fsSL "https://fastdl.mongodb.org/osx/mongodb-macos-${ARCH}-8.0.10.tgz" -o /tmp/mongodb.tgz
mkdir -p ~/mongodb && tar -xzf /tmp/mongodb.tgz -C ~/mongodb --strip-components=1
```

Create the data directory and do an initial start:

```bash
mkdir -p ~/data/db
~/mongodb/bin/mongod --dbpath ~/data/db --port 27017 --fork --logpath ~/data/mongod.log
```

**Create a launchd plist** so mongod starts automatically at login and restarts if it
crashes. Use absolute paths — launchd does not expand `~` or `$HOME`:

```bash
HOME_DIR="$HOME"
cat > ~/Library/LaunchAgents/org.mongodb.mongod.plist << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>org.mongodb.mongod</string>
  <key>ProgramArguments</key>
  <array>
    <string>${HOME_DIR}/mongodb/bin/mongod</string>
    <string>--dbpath</string>
    <string>${HOME_DIR}/data/db</string>
    <string>--port</string>
    <string>27017</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>StandardOutPath</key>
  <string>${HOME_DIR}/data/mongod.log</string>
  <key>StandardErrorPath</key>
  <string>${HOME_DIR}/data/mongod.log</string>
</dict>
</plist>
EOF
launchctl load ~/Library/LaunchAgents/org.mongodb.mongod.plist
```

**Verify mongod is up.** `mongosh` is not included in the server tarball, so use
netcat or the HTTP probe instead:

```bash
nc -z 127.0.0.1 27017 && echo "port 27017 open"
# Alternative: curl returns a human-readable error when hitting the native driver port
curl -sf http://127.0.0.1:27017 2>&1 | grep -q "native driver port" && echo "up"
```

If the port is closed, show the last 20 lines of the log: `tail -20 ~/data/mongod.log`

---

#### Linux

Detect the distro:

```bash
grep ^ID= /etc/os-release 2>/dev/null
```

- `ubuntu` or `debian`: add the official MongoDB apt repo and `sudo apt-get install -y mongodb-org`
- `rhel`, `centos`, `fedora`, `amzn`: `sudo yum install -y mongodb-org`
- Other: download the Linux tarball:

  ```bash
  ARCH=$(uname -m)  # x86_64 or aarch64
  curl -fsSL "https://fastdl.mongodb.org/linux/mongodb-linux-${ARCH}-ubuntu2204-8.0.10.tgz" -o /tmp/mongodb.tgz
  mkdir -p ~/mongodb && tar -xzf /tmp/mongodb.tgz -C ~/mongodb --strip-components=1
  mkdir -p ~/data/db
  ~/mongodb/bin/mongod --dbpath ~/data/db --port 27017 --fork --logpath ~/data/mongod.log
  ```

After package install, start and enable the service:

```bash
sudo systemctl start mongod && sudo systemctl enable mongod
```

---

#### Windows

Check for winget: `winget --version 2>nul`

If available: `winget install MongoDB.Server`

Otherwise check for Chocolatey: `choco install mongodb`

Otherwise direct the user to the MSI installer from
`https://www.mongodb.com/try/download/community` — select "Run as a Network
Service" during install, which registers mongod as a Windows service that
starts automatically.

After install: `net start MongoDB`

---

**Local binary default port: `27017`**

---

### Option B: Docker

Check Docker is running:

```bash
docker info 2>&1 | head -1
```

If Docker is not running, ask the user to start Docker Desktop and confirm before
continuing.

Start MongoDB using the included compose file:

```bash
docker compose -f "${CLAUDE_PLUGIN_ROOT}/docker-compose.transcripts.yml" up -d mongodb
```

**Docker default port: `27018`** (avoids clashing with a local mongod on 27017).

Verify: `docker compose -f "${CLAUDE_PLUGIN_ROOT}/docker-compose.transcripts.yml" ps`

---

### Option C: Remote

Ask the user for their full MongoDB connection string. Examples:

- MongoDB Atlas: `mongodb+srv://user:password@cluster.mongodb.net/`
- Self-hosted with auth: `mongodb://user:password@host:27017/`
- Plain remote: `mongodb://host:27017/`

No installation or service steps needed — skip straight to Step 4.

---

## Step 4 — Gather clued config

| Setting | Default (binary) | Default (Docker) | Remote |
|---------|-----------------|-----------------|--------|
| MongoDB URL | `mongodb://localhost:27017` | `mongodb://localhost:27018` | *(user-provided)* |
| Database name | `claude_sessions` | `claude_sessions` | `claude_sessions` |

Accept defaults unless the user overrides. For remote, the URL is required.

## Step 5 — Write config file

```bash
mkdir -p ~/.claude/plugins/data/clued
cat > ~/.claude/plugins/data/clued/config.json << 'EOF'
{
  "mongoUrl": "<chosen URL>",
  "dbName": "<chosen DB name>",
  "port": 8085,
  "mcpPort": 8086,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": []
}
EOF
```

## Step 6 — Register MCP server in Claude settings

Read `~/.claude/settings.json` and merge in the `clued` MCP server entry. Use `jq`
if available (safest), otherwise edit the file directly:

```bash
# With jq (preferred — handles existing mcpServers keys cleanly)
jq '.mcpServers.clued = {"type":"sse","url":"http://127.0.0.1:8086/sse"}' \
  ~/.claude/settings.json > /tmp/settings.tmp && mv /tmp/settings.tmp ~/.claude/settings.json
```

If `jq` is not available, read the file and add or merge the key manually:

```json
{
  "mcpServers": {
    "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
  }
}
```

Do not duplicate the `"clued"` key if it already exists with the correct URL.

## Step 7 — Start the daemon

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

Wait 2 seconds, then verify both services:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
curl -sf http://127.0.0.1:8086/health && echo "mcp server running"
```

If a health check fails, run the failing process briefly in the foreground to see
the error:

```bash
# Daemon
timeout 3 node "${CLAUDE_PLUGIN_ROOT}/dist/daemon.mjs" 2>&1 || true

# MCP server
timeout 3 node "${CLAUDE_PLUGIN_ROOT}/dist/mcp.mjs" 2>&1 || true
```

Most common failure cause: MongoDB isn't reachable at the configured URL. Confirm
the port is open with `nc -z <host> <port>` before retrying.

## Step 8 — Confirm to user

Tell the user:

- MongoDB option chosen and URL configured
- Config written to `~/.claude/plugins/data/clued/config.json`
- MCP server registered in `~/.claude/settings.json` under `mcpServers.clued` (URL: `http://127.0.0.1:8086/sse`)
- Daemon status: running on port 8085 / failed (show error output)
- MCP server status: running on port 8086 / failed
- On macOS tarball install: launchd plist written to `~/Library/LaunchAgents/org.mongodb.mongod.plist` — mongod restarts automatically at login with `KeepAlive: true`
- Hooks are registered automatically via `hooks.json` — no manual settings edit needed
- To add a custom enricher: create a `.ts` file in `<plugin-root>/enrichers/`, then rebuild and restart:
  ```bash
  # Install mise if not present: https://mise.jdx.dev
  cd <plugin-root>
  mise install          # sets up Node.js + pnpm per mise.toml
  pnpm install --frozen-lockfile
  pnpm run lint && pnpm run build
  bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
  ```
- To enable the privacy-redact enricher: remove `privacy-redact` from `disabledEnrichers` in `config.json`
