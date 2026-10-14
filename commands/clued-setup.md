---
name: clued-setup
description: Configure the clued plugin — choose a local MongoDB or a clued server, write config, register the hooks, and start the daemon (local mode only)
---

You are helping the user configure the `clued` Claude Code plugin for the first time.

## Step 1 — Detect platform

!`uname -s 2>/dev/null || echo "Windows"`
!`uname -m`

- `Darwin` → macOS
- `Linux`  → Linux
- anything else / `Windows` → Windows

## Step 2 — Ask where clued stores its data

First question. Present the user with three options. **Local MongoDB is the default.**

> "Where should clued keep your sessions, plans and stories?
>
> 1. **Local MongoDB** (recommended for one machine) — a MongoDB this machine can reach and a capture daemon that runs on this machine
> 2. **clued server in Docker on this machine** — the clued server, its MongoDB and an nginx HTTPS front-end run as containers here (Docker required); other machines and cloud containers can connect to it with a token. Plans and stories stay as Markdown in each repository and are copied to the server
> 3. **clued server someone else runs** — a `mind-palace server run` operated for you over HTTPS; nothing runs on this machine between hooks, and no MongoDB is installed here. You need its URL and a bearer token: one minted with `mind-palace server token create`, or one the server mints when `scripts/clued session login --server <url>` is approved from a machine already linked to it with `scripts/clued server device approve <code>` (see `docs/server.md` in the plugin repository)
> 4. **clued cloud** — a hosted, multi-tenant clued (the `clued-cloud` server) with an account of your own: you log in once in a browser and this machine is linked to the account; no token to copy, and the account's client settings are applied here at every session start. You need the cloud's URL"

Wait for their answer before continuing.

- **clued server someone else runs**: skip Steps 3 and 4 entirely; continue with Step 5. Nothing in Step 3 is installed, started or touched.
- **clued cloud**: skip Steps 3 and 4 entirely; continue with Step 5, then the "clued cloud" part of Step 6, which links the machine with a browser log-in instead of a token.
- **clued server in Docker on this machine**: skip Step 4; do Step 5 first (the binary generates the stack), then the "clued server in Docker" part of Step 3, then Step 6.
- **Local MongoDB**: ask the second question below, then continue with Step 3.

The server options and the cloud are "server mode" below: the config file names a server, the hooks relay to it, and the daemon is never started.

### Local MongoDB — ask how to run it

Present three options. **Local binary is the default.**

> "How would you like to run MongoDB for clued?
>
> 1. **Local binary** (recommended) — install MongoDB directly on this machine; no Docker required
> 2. **Docker** — run MongoDB in a container via docker-compose
> 3. **Remote** — connect to an existing instance (Atlas, VPS, self-hosted, etc.)"

Wait for their answer before continuing.

## Step 3 — Install and start MongoDB (local MongoDB), or the server stack (clued server in Docker)

### clued server in Docker on this machine — generate and start the stack

Do this after Step 5 (it needs the binary).

**1. Check for conflicts first.** One machine can host this stack next to other
clued pieces, but not on the same port, and an earlier stack must be recognised
rather than overwritten:

```bash
docker info 2>&1 | head -1 && docker compose version
test -f ~/.clued-server/docker-compose.yml && echo "stack: already generated at ~/.clued-server" || echo "stack: none yet"
docker ps --format '{{.Names}}\t{{.Ports}}' 2>/dev/null | grep -E 'clued|8443|8080' || echo "no clued containers running"
for port in 8443 8080 8085; do (echo >/dev/tcp/127.0.0.1/$port) 2>/dev/null && echo "port $port: in use" || echo "port $port: free"; done
```

- Docker not running: ask the user to start Docker Desktop (or the daemon) and confirm before continuing.
- A stack already generated: this machine was set up before. Rerunning `server init` keeps its CA, certificate and tokens (below), so it is safe; do not delete `~/.clued-server`.
- **Port 8443 in use** by something that is not this stack's `clued-nginx` container: pick another port and pass `--port <n>` to `server init` (for example 9443); every URL below then uses it.
- **Port 8080** belongs to a local **clued cloud** (`clued-cloud serve` or the clued-cloud repository's `docker compose up`). It does not conflict: the stack uses 8443 and its own Compose project (`clued`), containers (`clued-nginx`, `clued-server`, `clued-mongo`) and volume (`clued_clued_mongo_data`), while the cloud uses port 8080, the `clued-cloud` project and its own MongoDB volume. Neither publishes its MongoDB. The one thing they share is this machine's `config.json`, which names **one** server: see "One machine, two servers" in Step 6 before switching it.
- **Port 8085** is the local capture daemon (local MongoDB mode). The stack never uses it; leave it alone.

**2. Generate the stack directory**, `~/.clued-server`:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" server init --name "$(hostname -s 2>/dev/null || hostname)"
```

It writes `docker-compose.yml` (services `nginx`, `clued-server`, `mongo`), `nginx/clued.conf`, `mind-palace.config.yaml`, a private CA with a certificate for `localhost`, `127.0.0.1`, `::1` and this host's name under `tls/`, and mints the first token into `tokens.json`. The CA is name-constrained to those names: trusted on this machine (step 4), it vouches for this server and nothing else. The JSON result carries what the next steps need:

- `server_url` (`https://localhost:8443`), `ca_file` (`~/.clued-server/tls/ca.pem`), `ca_fingerprint`, `token.token` (printed once; never echo it, never put it on a command line), `up` and `health`.
- If `token` is absent and `notes` says the name already exists, this machine was set up before: reuse the token already in `config.json`, or mint another with `"${CLAUDE_PLUGIN_ROOT}/scripts/clued" server token create --tokens ~/.clued-server/tokens.json --name <another name> --project '*'` and use its `token`.
- A rerun keeps `tls/` and `tokens.json`. `--force` reissues the server certificate under the **same** CA (kept while it covers every host), so a machine that already trusts the CA keeps trusting the server; only `--new-ca`, or a `--host` the CA does not cover, makes a new CA, and the result's `notes` then say "the CA changed": do step 4 again on this machine and give other clients the new `ca.pem`.

Options to offer only when the user asks: `--port <n>` for another public port; `--host <name>` (repeatable) for every other name or address other machines will use in the URL (the certificate must cover it; rerun with `--force` to add one later); `--proxy none` to leave nginx out and publish the server as plain HTTP on `127.0.0.1:8443` for a terminator the user runs (Caddy, Traefik, their own nginx; the local client then uses `http://127.0.0.1:8443` and other machines go through their terminator); `--no-tls` to use certificates they supply as `~/.clued-server/tls/cert.pem` and `key.pem` (mkcert, a real CA; then no `serverCaFile` is written and step 4 is skipped).

**3. Start it** and wait for the health check (the first start pulls the images):

```bash
docker compose -f ~/.clued-server/docker-compose.yml up -d
for i in $(seq 1 60); do curl -sf --cacert ~/.clued-server/tls/ca.pem https://localhost:8443/health && break; sleep 2; done
```

(`--proxy none`: `curl -sf http://127.0.0.1:8443/health`; `--no-tls`: without `--cacert`.) If it never answers, show `docker compose -f ~/.clued-server/docker-compose.yml logs clued-server nginx` to the user; the usual causes are a port already in use (rerun `server init --port <other>`) and the image tag not yet published (the result's compose file pins the plugin's release; `CLUED_SERVER_IMAGE=ghcr.io/robbiebyrd/clued-server:latest docker compose ... up -d` works around it).

**4. Trust the CA on this machine.** The plugin's own client trusts the server through `serverCaFile` (Step 6) and needs nothing more; this step is for browsers, `curl` without `--cacert`, other MCP clients and tools. Ask once:

> "Should this machine trust the clued server's certificate, so browsers, curl and other tools accept https://localhost:8443? (The CA only vouches for this server's names.) (yes/no)"

On yes:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" server trust
```

It installs `~/.clued-server/tls/ca.pem` where this platform keeps trusted roots: the macOS login keychain (macOS may show a password dialog: tell the user to approve it), the Linux system store (`update-ca-certificates`, `update-ca-trust` or `trust`) plus the NSS databases Chrome and Firefox use on Linux, or the Windows user Root store (Windows asks to confirm). Read the result:

- `pending` empty and `verified: true`: done; `curl https://localhost:8443/health` now works without `--cacert`.
- `pending` lists commands (the Linux system store needs root, or the keychain could not show its dialog): show them to the user and ask them to run them in their own terminal, or in this prompt prefixed with `!` so the password prompt reaches them, then run `"${CLAUDE_PLUGIN_ROOT}/scripts/clued" server trust` again to check (it reports `verified`).
- `notes` names what it could not reach (certutil missing for Chrome and Firefox on Linux: `libnss3-tools` on Debian and Ubuntu, `nss-tools` on Fedora). Firefox on macOS and Windows reads the system roots.
- A browser that was open before keeps its old view until it restarts.

`server trust --dry-run` lists the commands without running them; `server trust --remove` takes the CA out of the same stores (`/clued-uninstall` does it).

Then continue with Step 6, "clued server", using `server_url`, the token and `ca_file`.

### clued server from a source checkout (developers)

For someone working on the server itself: the same HTTPS endpoint, served by the binary built from their checkout instead of the published image, with no Docker. Do this only when the user asks for it, or when `devRepoPath` is set in the config (the shim then builds and runs the checkout).

1. Do the conflict check of step 1 above. Port 8443 must be free: stop the Docker stack first if it runs (`docker compose -f ~/.clued-server/docker-compose.yml stop`), or use another port everywhere below.
2. Generate the certificates and the token the same way, without starting anything: `"${CLAUDE_PLUGIN_ROOT}/scripts/clued" server init --name "$(hostname -s 2>/dev/null || hostname)"` (the compose file it writes is simply not used).
3. Trust the CA as in step 4 above (optional).
4. Run the server from the checkout in its own terminal (it serves until Ctrl-C), with sessions, plans and stories in memory:

   ```bash
   cd <checkout>/mind-palace
   go run ./cmd/mind-palace server run --addr 127.0.0.1:8443 \
     --tls-cert ~/.clued-server/tls/cert.pem --tls-key ~/.clued-server/tls/key.pem \
     --tokens ~/.clued-server/tokens.json --store memory
   ```

   To keep data, drop `--store memory` and give it a MongoDB: sessions go where `CLUED_MONGO_URL` and `CLUED_DB_NAME` say, plans and stories to the storage of the config passed with `--config`. Copy `~/.clued-server/mind-palace.config.yaml` to `~/.clued-server/dev.config.yaml`, change its `uri` to `mongodb://127.0.0.1:27017` and its `database` to `clued_server_dev_docs`, and run `CLUED_MONGO_URL=mongodb://127.0.0.1:27017 CLUED_DB_NAME=clued_server_dev go run ./cmd/mind-palace --config ~/.clued-server/dev.config.yaml server run …` with the same TLS and token flags. Keep these databases apart from the `claude_sessions` a local-mode daemon writes and the `clued_cloud` a local clued cloud writes. The server ignores the `serverUrl` and `serverToken` of the config file it reads, so it never relays to itself.
5. Check it: `curl -sf --cacert ~/.clued-server/tls/ca.pem https://localhost:8443/health`.

Then continue with Step 6, "clued server", exactly as for the Docker stack.

---

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
docker compose -f "${CLAUDE_PLUGIN_ROOT}/docker-compose.transcripts.yml" up -d
```

**Docker default port: `27018`**

The compose file publishes that port on every interface with no authentication. If this machine is reachable from an untrusted network, tell the user to change the mapping in `docker-compose.transcripts.yml` to `127.0.0.1:27018:27017` (or enable authentication) before continuing.

---

### Option C: Remote

Ask the user for their full connection string (e.g. `mongodb+srv://...` for Atlas,
`mongodb://user:pass@host:27017/` for self-hosted). Skip straight to Step 4.

---

## Step 4 — Gather clued config (local MongoDB only)

| Setting | Binary default | Docker default | Remote |
|---------|---------------|----------------|--------|
| MongoDB URL | `mongodb://localhost:27017` | `mongodb://localhost:27018` | *(user-provided)* |
| Database name | `claude_sessions` | `claude_sessions` | `claude_sessions` |

## Step 5 — Check the mind-palace binary

clued runs a single Go binary, `mind-palace`, for the daemon, backfill and MCP servers (in server mode: for the hook relay, backfill and MCP servers). Every release publishes it pre-built for macOS (Intel and Apple Silicon), Linux (amd64, arm64 and 32-bit arm) and Windows (amd64 and arm64), so no Go toolchain is needed. The `scripts/clued` shim downloads the asset for this machine from the release the plugin pins (the `version` in `.claude-plugin/plugin.json`) on first use into `~/.claude/plugins/data/clued/bin/releases/v<version>/` and checks it against the release's `SHA256SUMS`; it needs `curl` or `wget` and `sha256sum` or `shasum`.

Confirm the shim can find a binary (`${CLAUDE_PLUGIN_ROOT}` is the plugin root; see Step 8 if it is not set):

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" --locate
```

It prints the path of the binary that will run and exits 0 (the first run downloads, so give it a moment). Nothing more to do when it succeeds.

Only when it exits 1 (no network access to github.com, an unsupported platform, or `curl`/`wget`/`sha256sum`/`shasum` missing; its stderr says which) install the binary another way. If `go` is present (`command -v go`):

```bash
go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest
```

Make sure `$(go env GOPATH)/bin` is on the PATH that Claude Code inherits.

Otherwise copy the release asset for this machine (from another machine, or a browser) into the plugin data directory. The release is `v<version>` for the `version` in `${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json`; `uname -s` gives `darwin` or `linux` once lowercased; `x86_64` maps to `amd64`, `aarch64` to `arm64`, `armv7l` to `arm`:

```bash
VERSION=$(jq -r .version "${CLAUDE_PLUGIN_ROOT}/.claude-plugin/plugin.json")
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in x86_64) ARCH=amd64 ;; aarch64|arm64) ARCH=arm64 ;; armv7l) ARCH=arm ;; esac
mkdir -p ~/.claude/plugins/data/clued/bin
curl -fsSL "https://github.com/robbiebyrd/clued/releases/download/v${VERSION}/mind-palace_${OS}_${ARCH}" \
  -o ~/.claude/plugins/data/clued/bin/mind-palace
chmod +x ~/.claude/plugins/data/clued/bin/mind-palace
```

`scripts/clued` resolves the binary in this order: a dev build when `devRepoPath` is set, the pinned release, `mind-palace` on PATH, then `~/.claude/plugins/data/clued/bin/mind-palace`.

## Step 6 — Write config file

### Local MongoDB

```bash
mkdir -p ~/.claude/plugins/data/clued
cat > ~/.claude/plugins/data/clued/config.json << 'EOF'
{
  "mongoUrl": "<chosen URL>",
  "dbName": "<chosen DB name>",
  "port": 8085,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": [],
  "enabledEnrichers": [],
  "redactAtCapture": true
}
EOF
```

### clued server

For the Docker stack of Step 3 nothing is asked: the URL is the `server_url` `server init` printed (`https://localhost:8443`), the token is `token.token`, and the CA file is `ca_file` (omit it with `--no-tls` or `--proxy none`). Otherwise ask the user for, in this order:

1. **The server URL**, e.g. `https://clued.example.com` (no trailing path). It must be `https://`: the binary refuses a plain `http://` URL unless the host is `localhost`, `127.0.0.1` or `::1`. If the user gives `http://` for another host, stop and explain that the token and the transcripts would cross the network in clear; only on a private network they trust may they add `"serverInsecure": true` to the file by hand.
2. **The token** the server operator minted (`mind-palace server token create --name <name> --project <slug>`; it is printed once). Never echo it back, never put it on a command line, never log it.
3. **A CA file** (optional): only when the server uses a certificate from a private CA, the path of its PEM bundle; it is trusted in addition to the system roots. Skip the key otherwise.

**Before writing, look at the config this machine already has.** The heredoc replaces the whole file:

```bash
test -f ~/.claude/plugins/data/clued/config.json && jq '{serverUrl, linked: (.serverToken != null), redactAtCapture, statusLine}' ~/.claude/plugins/data/clued/config.json || echo "no config yet"
```

If it already names a `serverUrl` other than the one about to be written (a clued cloud such as `http://localhost:8080`, another server), say so and ask before replacing it: the plugin talks to **one** server at a time, and replacing the file drops that link (its token stays valid on that server until revoked). On yes, keep a copy first so the user can switch back, then write the file:

```bash
cp -p ~/.claude/plugins/data/clued/config.json ~/.claude/plugins/data/clued/config.json.bak-$(date +%Y%m%d%H%M%S)
```

Keep any other keys the old file set that the heredoc below does not (`statusLine`, `redactPatterns`, `excludePaths`, `modelPrices`, `otelEndpoint`, `otelHeaders`, `retentionDays`): add them back to the new file.

**One machine, two servers.** A developer can run the clued server stack (8443) and a clued cloud (8080) side by side; they do not share ports, Compose projects, containers or MongoDB volumes. Only the client config is shared. To point one shell, or one test, at the other server without touching the file, set `CLUED_SERVER_URL` and `CLUED_SERVER_TOKEN` (and `CLUED_SERVER_CA_FILE`) in that shell; they override the file. To switch the plugin for good, restore the backup or rerun this step.

Write the file with the heredoc below so the token never appears in a command line, and keep it private: it is the only credential on this machine. `mongoUrl` stays at its default and is ignored while `serverUrl` is set (`dbName` and `port` likewise; they are kept so the file can go back to local mode by removing the two server keys).

```bash
mkdir -p ~/.claude/plugins/data/clued
umask 077
cat > ~/.claude/plugins/data/clued/config.json << 'EOF'
{
  "mongoUrl": "mongodb://localhost:27018",
  "dbName": "claude_sessions",
  "port": 8085,
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": [],
  "enabledEnrichers": [],
  "redactAtCapture": true,
  "serverUrl": "<server URL>",
  "serverToken": "<token>"
}
EOF
chmod 600 ~/.claude/plugins/data/clued/config.json
```

When a CA file was given, add `"serverCaFile": "<path>"` next to `serverToken` (a leading `~/` is expanded).

### clued cloud

Ask for the cloud's URL, e.g. `https://cloud.example.com` (`https://` only, as above). Then link the machine with the device flow; the command writes `serverUrl` and `serverToken` into the config file itself (mode 0600, every other key kept) once the user approves in a browser:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" session login --server <cloud URL> --name "$(hostname -s 2>/dev/null || hostname)"
```

The command prints on stderr a line like `clued: open https://cloud.example.com/link?code=BCDF-GHJK in a browser and approve the code BCDF-GHJK`, then waits (up to 15 minutes) for the approval. **Show the user that link and the code immediately** (as a clickable link; in the Claude Code app it renders as one) and tell them to open it in any browser, sign up or log in there, check that the page shows this code and this machine's name, and approve. The command then finishes with a JSON result (`server`, `name`, `account_id`, `token_id`, `config`) and has already checked the token with `whoami`. A denied or expired request fails it: run it again.

If the cloud uses a certificate from a private CA, add `--ca-file <PEM bundle>` (written as `serverCaFile`). The config file is created when missing, so no heredoc is needed; the other keys keep their defaults.

Later, `session logout` revokes this machine's token on the cloud and takes the server keys out of the file (back to local mode), and the account's **Settings** page in the cloud manages `enabledEnrichers`, `disabledEnrichers`, `projectsDir` and `fileHistoryDir` for every linked machine; `session-start` applies them with `session config-pull`.

## Step 7 — MCP servers (no action needed)

The plugin ships its own `.mcp.json`, so Claude Code launches both MCP servers
itself over stdio: `clued` (`${CLAUDE_PLUGIN_ROOT}/scripts/clued session mcp`, the
session tools) and `mind-palace` (`${CLAUDE_PLUGIN_ROOT}/scripts/clued mcp
--no-session-tools`, the plan and story tools) — no settings edit is required.
Their tools appear in the next fresh session. In server mode both talk to the
server: the session tools read from it directly and the plan and story tools
keep the files in the repository and mirror every write to it.

Claude Code does not read `mcpServers` from `~/.claude/settings.json`. If an older
setup left a `mcpServers.clued` entry there, remove it:

```bash
jq 'del(.mcpServers.clued) | if .mcpServers == {} then del(.mcpServers) else . end' \
  ~/.claude/settings.json > ~/.claude/settings.json.tmp && mv ~/.claude/settings.json.tmp ~/.claude/settings.json
```

## Step 8 — Start the daemon (local MongoDB) or check the server (clued server)

### Local MongoDB — start the daemon

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/session-start"
```

Wait 2 seconds, then verify:

```bash
curl -sf http://127.0.0.1:8085/health && echo "daemon running"
```

If the health check fails: `timeout 3 "${CLAUDE_PLUGIN_ROOT}/scripts/clued" session daemon 2>&1 || true`

Most common failure: MongoDB not reachable. Confirm with `nc -z <host> <port>`.

### clued server — check the server and register the hooks

Nothing is started: no daemon, no MongoDB. Every hook runs `scripts/clued hook relay`,
which sends its event and the transcript delta to the server and exits. Ask the
server who the token is:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" session whoami
```

On success it prints `{"ok": true, "result": {"name", "account_id", "projects", "version", "server"}}`.
Report the token's `name`, its `projects` (`*` means every project) and the server `version` to the user.

If it fails, the message in the `error` envelope on stderr says why; do not retry blindly:

- exit 2 (`BadRequest`, "no server configured"): the config file was not written where the binary reads it, go back to Step 6
- exit 10 (`StorageError`) with `unauthorized`: the server rejected the token; ask for it again and rewrite the file
- exit 10 with `connection refused`, `no such host`, `deadline exceeded`, `certificate` or `TLS`: the server is unreachable or its certificate is not trusted; check the URL, the network and (private CA) `serverCaFile`
- exit 10 with `plain http://`: see Step 6, the URL must be `https://`

Then check that the token covers the repository you are in. Plans and stories live
under a project on the server, whose slug the server derives from the git origin:
host and path lower-cased, `/` written as `__`, a trailing `.git` dropped
(`git@github.com:Owner/Repo.git` → `github.com__owner__repo`; a repository without an
origin uses its directory name). `check-setup` does this comparison and prints the
exact warning, with the command that fixes it, or nothing when all is well:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/check-setup"
```

When it reports that the token does not cover this project, tell the user: sessions
are captured regardless, but plans and stories stay local here until the server
operator runs `mind-palace server token grant <token name> --project <slug>` (both
names are in the message).

Finally register the hooks now rather than at the next session start:

```bash
bash "${CLAUDE_PLUGIN_ROOT}/hooks/register-hooks"
```

Then offer the status line, once:

> "Should clued also run Claude Code's status line? It prints your current status line unchanged (or `ctx 37% of 1M · 5h 12% · 7d 40%` when there is none) and records the context window's fill and, on a Pro or Max plan, the five-hour and seven-day usage limits as `LimitsSampled` events you can query with `get_limits`. (yes/no)"

On yes, set `statusLine` in the config file and register again, which keeps the
status line command configured today to run after each sample:

```bash
jq '.statusLine = true' "${HOME}/.claude/plugins/data/clued/config.json" > "${HOME}/.claude/plugins/data/clued/config.json.tmp" && mv "${HOME}/.claude/plugins/data/clued/config.json.tmp" "${HOME}/.claude/plugins/data/clued/config.json" && chmod 600 "${HOME}/.claude/plugins/data/clued/config.json"
bash "${CLAUDE_PLUGIN_ROOT}/hooks/register-hooks"
```

The status line takes effect on the next Claude Code start. Setting `statusLine`
back to `false` and registering again puts the previous command back.

## Step 9 — Confirm to user

- Where the data goes: the MongoDB option and URL, the clued server URL and the token name and projects `session whoami` reported, or the clued cloud URL and the account `session login` linked (its web app manages linked machines and client settings)
- Config written to `~/.claude/plugins/data/clued/config.json` (mode `0600` in server mode, it holds the token)
- mind-palace binary location as printed by `scripts/clued --locate` (the downloaded release under `~/.claude/plugins/data/clued/bin/releases/`, on PATH, or `~/.claude/plugins/data/clued/bin/mind-palace`)
- MCP servers (`clued` session tools, `mind-palace` plan and story tools) provided by the plugin's `.mcp.json` over stdio — start a new session to load them
- Local MongoDB: daemon on port 8085 — running or failed with error
- clued server: nothing runs on this machine between hooks; each hook relays to the server, failures are logged to `~/.claude/plugins/data/clued/relay.log` and buffered in the WAL until the server answers; if the project was not covered, the `server token grant` command the operator must run
- clued server in Docker: the stack lives in `~/.clued-server` (compose file, `tokens.json`, `tls/`); it restarts with Docker and stops with `docker compose -f ~/.clued-server/docker-compose.yml down` (or `/clued-uninstall`). Another machine connects with `https://<this host's name>:8443` (add the name with `server init --host <name> --force` if it was not covered), a copy of `~/.clued-server/tls/ca.pem` as its `serverCaFile`, and its own token from `scripts/clued server token create --tokens ~/.clued-server/tokens.json --name <machine> --project '*'`
- In both server modes plans and stories keep their Markdown files under `docs/plans` and `docs/stories` in each repository (the primary store) and every write is also sent to the server; `mind-palace sync --from file --to clued-server` catches the server up after an outage
- macOS tarball: launchd plist at `~/Library/LaunchAgents/org.mongodb.mongod.plist` auto-restarts mongod at login
- Hooks registered automatically via `hooks.json` (and mirrored into `~/.claude/settings.json` by `register-hooks` on every session start) — no manual settings edit needed. Three of them run the binary directly: `scripts/clued hook write-guard` on PreToolUse (plans and stories change only through mind-palace), `scripts/clued hook stop-guard` on Stop and `scripts/clued hook status-review` on SessionStart and (every 30 minutes) UserPromptSubmit; in server mode `hooks/event-relay` runs `scripts/clued hook relay` for every other event
- If the claude-remember plugin is installed, `session-start` installs clued's listener into it so its summaries are captured too (`search_memory` finds them)
- Capture-time redaction is on (`"redactAtCapture": true`): API keys, AWS access key ids, JWTs, GitHub and Slack tokens, PEM private keys and `<private>…</private>` spans are masked before anything is written to MongoDB or sent to the server. Add regular expressions to `redactPatterns` to mask more (e-mail addresses, internal host names), and globs to `excludePaths` (`["~/work/client-*"]`) to keep whole directories out; `mind-palace session forget --session <id>` (or `--project <re>`, `--before <day>`) deletes what was captured, and `retentionDays` lets MongoDB expire it.
- To enable the privacy-redact enricher (off by default): add `"privacy-redact"` to `enabledEnrichers` in `config.json` and restart the daemon (local MongoDB; in server mode enrichers run on the server and are configured there). It stores a masked copy of each transcript line (`sk-` API keys, e-mail addresses, AWS access key ids, JWTs) under `enrichments.privacy-redact`, and of each captured claude-remember record under `enrichments.privacy-redact-memory`; the raw text is still stored. `disabledEnrichers` only switches enrichers off.
