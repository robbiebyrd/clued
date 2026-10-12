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

Determine the plugin root (same logic as Step 8 — run this now if doing Docker setup):

```bash
CLUED_PLUGIN_ROOT=$(jq -r '
  .extraKnownMarketplaces | to_entries[]
  | select(.value.source.source == "directory")
  | select(.value.source.path | test("clued"))
  | .value.source.path
' ~/.claude/settings.json 2>/dev/null | head -1)
CLUED_PLUGIN_ROOT="${CLUED_PLUGIN_ROOT:-$(ls -dt ~/.claude/plugins/cache/*/clued/*/ 2>/dev/null | head -1)}"
```

Start MongoDB using the included compose file:

```bash
docker compose -f "${CLUED_PLUGIN_ROOT}/docker-compose.transcripts.yml" up -d
```

**Docker default port: `27018`** (avoids clashing with a local mongod on 27017).

The compose file publishes that port on every interface with no authentication. If this machine is reachable from an untrusted network, tell the user to change the mapping in `docker-compose.transcripts.yml` to `127.0.0.1:27018:27017` (or enable authentication) before continuing.

Verify: `docker compose -f "${CLUED_PLUGIN_ROOT}/docker-compose.transcripts.yml" ps`

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

## Step 5 — Check the mind-palace binary

clued runs a single Go binary, `mind-palace`, for the daemon, backfill and MCP servers. The plugin ships it pre-built for macOS (Intel and Apple Silicon), Linux (amd64, arm64 and 32-bit arm) and Windows (amd64 and arm64) under `dist/`, so no Go toolchain is needed. The `scripts/clued` shim unpacks the archive for this machine on first use into `~/.claude/plugins/data/clued/bin/bundled/<sha256>/` and checks it against `dist/SHA256SUMS`.

Confirm the shim can find a binary (`${CLAUDE_PLUGIN_ROOT}` is the plugin root; see Step 8 if it is not set):

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" --locate
```

It prints the path of the binary that will run and exits 0. Nothing more to do when it succeeds.

Only when it exits 1 (an unsupported platform, or `gzip`/`sha256sum`/`shasum` missing) install the binary another way. If `go` is present (`command -v go`):

```bash
go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest
```

Make sure `$(go env GOPATH)/bin` is on the PATH that Claude Code inherits.

Otherwise download the release asset for this machine into the plugin data directory (`uname -s` gives `darwin` or `linux` once lowercased; `x86_64` maps to `amd64`, `aarch64` to `arm64`, `armv7l` to `arm`):

```bash
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; armv7l) ARCH=arm ;; esac
mkdir -p ~/.claude/plugins/data/clued/bin
curl -fsSL "https://github.com/robbiebyrd/clued/releases/latest/download/mind-palace_${OS}_${ARCH}" \
  -o ~/.claude/plugins/data/clued/bin/mind-palace
chmod +x ~/.claude/plugins/data/clued/bin/mind-palace
```

`scripts/clued` resolves the binary in this order: a dev build when `devRepoPath` is set, the bundled binary, `mind-palace` on PATH, then `~/.claude/plugins/data/clued/bin/mind-palace`.

## Step 6 — Write config file

```bash
mkdir -p ~/.claude/plugins/data/clued
cat > ~/.claude/plugins/data/clued/config.json << 'EOF'
{
  "mongoUrl": "<chosen URL>",
  "dbName": "<chosen DB name>",
  "port": 8085,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": [],
  "enabledEnrichers": []
}
EOF
```

**For plugin developers only** — if this machine is the clued development repo, also add `devRepoPath` so `scripts/clued` builds `mind-palace` from that checkout and session-start restarts the daemon when the build changed:

```bash
jq '.devRepoPath = "/path/to/clued"' \
  ~/.claude/plugins/data/clued/config.json > /tmp/clued.tmp && \
  mv /tmp/clued.tmp ~/.claude/plugins/data/clued/config.json
```

Replace `/path/to/clued` with the actual repo path. Skip this for normal installations.

## Step 7 — MCP servers (no action needed)

The plugin ships a `.mcp.json` that tells Claude Code to run two stdio servers:

```
${CLAUDE_PLUGIN_ROOT}/scripts/clued session mcp              # clued: the session tools
${CLAUDE_PLUGIN_ROOT}/scripts/clued mcp --no-session-tools   # mind-palace: the plan and story tools
```

Claude Code reads this at startup and spawns both as stdio child processes —
no settings edit is required. **MCP tools appear in the next fresh
session** (a new Claude Code window or `exit` + reopen); `/reload-plugins` alone
is not sufficient.

If an older setup left a `mcpServers.clued` SSE entry in `~/.claude/settings.json`,
remove it to avoid a conflicting stale connection:

```bash
jq 'del(.mcpServers.clued) | if .mcpServers == {} then del(.mcpServers) else . end' \
  ~/.claude/settings.json > ~/.claude/settings.json.tmp && mv ~/.claude/settings.json.tmp ~/.claude/settings.json
```

## Step 8 — Determine plugin root

For a directory-sourced installation, read the plugin root from `~/.claude/settings.json`:

```bash
CLUED_PLUGIN_ROOT=$(jq -r '
  .extraKnownMarketplaces
  | to_entries[]
  | select(.value.source.source == "directory")
  | select(.value.source.path | test("clued"))
  | .value.source.path
' ~/.claude/settings.json 2>/dev/null | head -1)
```

If that returns empty (e.g. installed from a marketplace, not a directory), find the
most recently modified cache entry instead:

```bash
CLUED_PLUGIN_ROOT=$(ls -dt ~/.claude/plugins/cache/*/clued/*/ 2>/dev/null | head -1)
```

Confirm the path looks right before continuing.

## Step 9 — Register hooks and start the daemon

Register clued's hooks into `~/.claude/settings.json`:

```bash
bash "${CLUED_PLUGIN_ROOT}/hooks/register-hooks"
```

Then start the daemon:

```bash
bash "${CLUED_PLUGIN_ROOT}/hooks/session-start"
```

Wait 2 seconds, then verify:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
```

If the health check fails, run the daemon briefly in the foreground to see the error:

```bash
timeout 3 "${CLUED_PLUGIN_ROOT}/scripts/clued" session daemon 2>&1 || true
```

Most common failure cause: MongoDB isn't reachable at the configured URL. Confirm
the port is open with `nc -z <host> <port>` before retrying.

## Step 10 — Confirm to user

Tell the user:

- MongoDB option chosen and URL configured
- Config written to `~/.claude/plugins/data/clued/config.json`
- MCP servers: `clued` (session tools) and `mind-palace` (plan and story tools), provided by the plugin's `.mcp.json` (stdio) — Claude Code spawns them automatically; tools appear in the **next fresh session** (not just `/reload-plugins`)
- Hooks registered in `~/.claude/settings.json` via `register-hooks` — self-healing: re-runs on every `SessionStart`. Two of them run the binary directly: `scripts/clued hook stop-guard` on Stop and `scripts/clued hook status-review` on SessionStart and (every 30 minutes) UserPromptSubmit
- mind-palace binary location as printed by `scripts/clued --locate` (bundled with the plugin, on PATH, or in `~/.claude/plugins/data/clued/bin/`)
- Daemon status: running on port 8085 / failed (show error output)
- On macOS tarball install: launchd plist written to `~/Library/LaunchAgents/org.mongodb.mongod.plist` — mongod restarts automatically at login with `KeepAlive: true`
- Ask the user to run `/reload-plugins` then **start a new session** for all changes (hooks + MCP tools) to take effect
- If the claude-remember plugin is installed, `session-start` installs clued's listener into it so its summaries are captured too (`search_memory` finds them)
- To enable the privacy-redact enricher (off by default): add `"privacy-redact"` to `enabledEnrichers` in `config.json` and restart the daemon. It stores a masked copy of each transcript line (`sk-` API keys, e-mail addresses, AWS access key ids, JWTs) under `enrichments.privacy-redact`; the raw line is still stored. `disabledEnrichers` only switches enrichers off.
