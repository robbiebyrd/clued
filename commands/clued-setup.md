---
name: clued-setup
description: Configure the clued plugin — set up MongoDB, write config, and start the daemon
---

You are helping the user configure the `clued` Claude Code plugin for the first time.

## Step 1 — Detect platform

!`uname -s 2>/dev/null || echo "Windows"`
!`uname -m`

- `Darwin` → macOS
- `Linux`  → Linux
- anything else / `Windows` → Windows

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

**Try Homebrew first.** Homebrew has a hard dependency on the current Xcode version and
will refuse to install even pre-built bottles if Xcode is outdated. Attempt the install
and check for errors before proceeding:

```bash
brew tap mongodb/brew 2>&1
brew install mongodb/brew/mongodb-community 2>&1 | grep -E "Error:|successfully installed|already installed"
```

**If Homebrew succeeds:** `brew services start mongodb/brew/mongodb-community`
Homebrew's `brew services` registers a launchd agent automatically — mongod restarts at login without any additional steps.

**If Homebrew fails** (common error: "Your Xcode (X.Y) is too outdated"), fall back to the
**direct binary tarball** — a pre-compiled binary that requires no compiler or Xcode:

```bash
ARCH=$(uname -m)   # arm64 or x86_64
curl -fsSL "https://fastdl.mongodb.org/osx/mongodb-macos-${ARCH}-8.0.10.tgz" -o /tmp/mongodb.tgz
mkdir -p ~/mongodb && tar -xzf /tmp/mongodb.tgz -C ~/mongodb --strip-components=1
mkdir -p ~/data/db
~/mongodb/bin/mongod --dbpath ~/data/db --port 27017 --fork --logpath ~/data/mongod.log
```

**Create a launchd plist** for auto-start at login. Use absolute paths — launchd does not expand `~`:

```bash
HOME_DIR="$HOME"
cat > ~/Library/LaunchAgents/org.mongodb.mongod.plist << EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>org.mongodb.mongod</string>
  <key>ProgramArguments</key>
  <array>
    <string>${HOME_DIR}/mongodb/bin/mongod</string>
    <string>--dbpath</string><string>${HOME_DIR}/data/db</string>
    <string>--port</string><string>27017</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>${HOME_DIR}/data/mongod.log</string>
  <key>StandardErrorPath</key><string>${HOME_DIR}/data/mongod.log</string>
</dict>
</plist>
EOF
launchctl load ~/Library/LaunchAgents/org.mongodb.mongod.plist
```

**Verify** — `mongosh` is not in the server tarball, use netcat instead:

```bash
nc -z 127.0.0.1 27017 && echo "port 27017 open"
```

If the port is closed: `tail -20 ~/data/mongod.log`

---

#### Linux

```bash
grep ^ID= /etc/os-release 2>/dev/null
```

- `ubuntu`/`debian`: add the official MongoDB apt repo and `sudo apt-get install -y mongodb-org`
- `rhel`/`centos`/`fedora`/`amzn`: `sudo yum install -y mongodb-org`
- Other: download the Linux tarball:
  ```bash
  ARCH=$(uname -m)
  curl -fsSL "https://fastdl.mongodb.org/linux/mongodb-linux-${ARCH}-ubuntu2204-8.0.10.tgz" -o /tmp/mongodb.tgz
  mkdir -p ~/mongodb && tar -xzf /tmp/mongodb.tgz -C ~/mongodb --strip-components=1
  mkdir -p ~/data/db
  ~/mongodb/bin/mongod --dbpath ~/data/db --port 27017 --fork --logpath ~/data/mongod.log
  ```

After package install: `sudo systemctl start mongod && sudo systemctl enable mongod`

---

#### Windows

Try in order: `winget install MongoDB.Server` → `choco install mongodb` → MSI from
`https://www.mongodb.com/try/download/community` (select "Run as a Network Service").

After install: `net start MongoDB`

---

**Local binary default port: `27017`**

---

### Option B: Docker

```bash
docker info 2>&1 | head -1
```

If Docker is not running, ask the user to start Docker Desktop first.

```bash
docker compose -f "${CLAUDE_PLUGIN_ROOT}/docker-compose.transcripts.yml" up -d mongodb
```

**Docker default port: `27018`**

---

### Option C: Remote

Ask the user for their full connection string (e.g. `mongodb+srv://...` for Atlas,
`mongodb://user:pass@host:27017/` for self-hosted). Skip straight to Step 4.

---

## Step 4 — Gather clued config

| Setting | Binary default | Docker default | Remote |
|---------|---------------|----------------|--------|
| MongoDB URL | `mongodb://localhost:27017` | `mongodb://localhost:27018` | *(user-provided)* |
| Database name | `claude_sessions` | `claude_sessions` | `claude_sessions` |

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

Read `~/.claude/settings.json`. Merge this into `mcpServers` (do not duplicate if already present):

```json
"mcpServers": {
  "clued": { "type": "sse", "url": "http://127.0.0.1:8086/sse" }
}
```

## Step 7 — Start the daemon

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

Wait 2 seconds, then verify:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
curl -sf http://127.0.0.1:8086/health && echo "mcp server running"
```

If a health check fails: `timeout 3 node "${CLAUDE_PLUGIN_ROOT}/dist/daemon.mjs" 2>&1 || true`

Most common failure: MongoDB not reachable. Confirm with `nc -z <host> <port>`.

## Step 8 — Confirm to user

- MongoDB option and URL configured
- Config written to `~/.claude/plugins/data/clued/config.json`
- MCP server registered under `mcpServers.clued` (`http://127.0.0.1:8086/sse`)
- Daemon on port 8085 / MCP server on port 8086 — running or failed with error
- macOS tarball: launchd plist at `~/Library/LaunchAgents/org.mongodb.mongod.plist` auto-restarts mongod at login
- Hooks registered automatically via `hooks.json` — no manual settings edit needed
- Customise: drop a `.mjs` enricher into `<plugin-root>/enrichers/` and restart the daemon
