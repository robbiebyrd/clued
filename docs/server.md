# The clued server

`mind-palace server run` is the remote mode of clued: one HTTPS service that
stores captured sessions, plans and stories for every machine that holds a
token, so a laptop, a desktop and a cloud container share one memory without
each running MongoDB. This page is for the operator who runs the server and
for the client setup that points a plugin installation at it.

How it fits together:

- **Sessions** are stored only on the server. The client runs no daemon: each
  hook invokes `mind-palace hook relay`, which stores the event, pushes the
  transcript lines written since the previous hook and replays the local
  write-ahead log. Enrichment runs on the server.
- **Plans and stories** keep the repository's files under `docs/plans` and
  `docs/stories` as the primary store; the server is appended as a secondary
  store (`clued-server`) and receives every write through the fan-out. After
  an outage `mind-palace sync --from file --to clued-server` reconciles.
- **MCP** stays available through the plugin's stdio servers, which in remote
  mode read and write through the server, and additionally over HTTPS at
  `/mcp` for clients that want no local process at all.

Every route but `GET /health` requires a bearer token. Tokens are minted on
the server, bound to an account and a list of projects, and stored hashed.

## Running the server

### From the binary

Any `mind-palace` binary (a release asset, `go install
github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest`, or the one
the plugin downloaded) serves:

```bash
mind-palace server token create --name laptop --project '*'          # prints the token once
mind-palace server run --addr 0.0.0.0:8443 \
  --tls-cert /etc/letsencrypt/live/clued.example.com/fullchain.pem \
  --tls-key  /etc/letsencrypt/live/clued.example.com/privkey.pem \
  --config   /etc/clued/mind-palace.config.yaml
```

`run` prints `clued server listening on https://0.0.0.0:8443` and serves until
SIGINT or SIGTERM, when it drains in-flight requests for up to 10 s. Each flag
falls back to an environment variable, so a service unit or a container can
be configured without arguments:

| Flag | Variable | Default | What it is |
|---|---|---|---|
| `--addr` | `CLUED_SERVER_ADDR` | `127.0.0.1:8443` | Listen address |
| `--tls-cert`, `--tls-key` | `CLUED_SERVER_TLS_CERT`, `CLUED_SERVER_TLS_KEY` | unset | PEM certificate and key; the server terminates TLS when both are set |
| `--behind-proxy` | `CLUED_SERVER_BEHIND_PROXY` | `false` | Serve plain HTTP off loopback because a reverse proxy terminates TLS |
| `--tokens` | `CLUED_SERVER_TOKENS` | `~/.clued-server/tokens.json` | The token file (shared with `server token`) |
| `--config` | `MIND_PALACE_CONFIG` | `mind-palace.config.yaml` in the working directory | Plan and story storage, see below |
| `--session-config` | | `~/.claude/plugins/data/clued/config.json` | Session storage and enrichers; `CLUED_MONGO_URL` and `CLUED_DB_NAME` override its `mongoUrl` and `dbName` |
| `--version` | | the build's version | Reported by `/health` and `/api/v1/whoami` |

Sessions always live in MongoDB: set `CLUED_MONGO_URL` (for example
`mongodb://127.0.0.1:27017`) and, optionally, `CLUED_DB_NAME` (default
`claude_sessions`). `--store memory` keeps everything in memory and exists for
tests.

`GET /health` answers without a token (`version` is `dev` unless `--version`
says otherwise):

```json
{"ok": true, "service": "clued-server", "version": "dev", "auth": "bearer"}
```

### From the image

Each release publishes `ghcr.io/robbiebyrd/clued-server:v<version>` and
`:latest` (linux/amd64 and linux/arm64, assembled by `Dockerfile.server` from
the release's own `mind-palace_linux_<arch>` binaries in the `release` job of
`.github/workflows/mind-palace.yml`, which cuts a release on every push to
main, bumping the patch version in `.claude-plugin/plugin.json` itself unless
the push carries a new one; the binary in the image is byte-for-byte the
release asset). The image runs `mind-palace server run` as a
non-root user on a distroless base, listens on `0.0.0.0:8443` and reads the
token file from `/etc/clued/tokens.json`; everything else comes from the
`CLUED_SERVER_*`, `MIND_PALACE_CONFIG`, `CLUED_MONGO_URL` and `CLUED_DB_NAME`
variables above and from read-only mounts:

| Mount | Holds |
|---|---|
| `/etc/clued/tokens.json` | The token file; the server re-reads it whenever it changes, so revoking or granting needs no restart |
| `/etc/clued/mind-palace.config.yaml` | The plan and story storage (`MIND_PALACE_CONFIG` must point at it) |
| `/etc/clued/tls/` | `cert.pem` and `key.pem` when the server terminates TLS itself |

The distroless image has no shell or HTTP client, so it carries no
`HEALTHCHECK`; probe `/health` from the host or the proxy. Without a host
binary, the token commands run from the image (`server run` is the
entrypoint, so it is replaced):

```bash
docker run --rm -v "$HOME/.clued-server:/etc/clued" --user "$(id -u):$(id -g)" \
  --entrypoint mind-palace ghcr.io/robbiebyrd/clued-server:latest \
  server token create --tokens /etc/clued/tokens.json --name laptop --project '*'
```

### The container stack: `mind-palace server init`

The way to run the server on a machine of your own is the stack that
`mind-palace server init` generates and `/clued-setup` starts for you (its
"clued server in containers on this machine" option). It runs with Docker
Compose, or with Apple Containers on a Mac (macOS 26 or later) that has no
Docker:

```bash
mind-palace server init --name laptop                         # ~/.clued-server, prints the token once
docker compose -f ~/.clued-server/docker-compose.yml up -d    # runtime compose
bash ~/.clued-server/up.sh                                    # runtime apple
curl --cacert ~/.clued-server/tls/ca.pem https://localhost:8443/health
```

`--runtime auto`, the default, picks `compose` whenever `docker` is on the
PATH and `apple` only when it is not and the `container` CLI is (`brew
install --cask container`, then `container system start`); `--runtime
compose|apple` forces one, and switching removes the other runtime's files.
The result's `up`, `down`, `logs` and `health` are the commands for the
runtime chosen.

The directory holds everything the stack needs and is the server's home:

| File | What it is |
|---|---|
| `stack.json` | Which runtime runs the stack, the proxy, the port, the server URL and the `up`, `down` and `logs` commands |
| `docker-compose.yml` | Runtime `compose`. Three services: `nginx` (publishes `--port`, 8443 by default, over HTTPS and forwards to the server), `clued-server` (plain HTTP inside the compose network, `CLUED_SERVER_BEHIND_PROXY=true`, runs as your uid so it can read the token file) and `mongo` (`mongo:8` on the named volume `clued_mongo_data`, not published). The server image is the release of the plugin that ran `init` (`ghcr.io/robbiebyrd/clued-server:v<version>`, `:latest` from a bare binary; `--image` overrides it). Every value is literal, with no `${VAR:-default}` interpolation (Apple's compose plugin, among others, does not support it): change one by rerunning `init` with the matching flag |
| `up.sh`, `down.sh` | Runtime `apple`. `up.sh` starts `container system` when needed, creates the `clued_mongo_data` volume, starts `clued-mongo`, waits for it with `mongosh`, starts `clued-server` with the MongoDB address from `container inspect` (container names do not resolve inside Apple containers without a DNS domain set up with sudo) and, with nginx, renders `run/nginx/clued.conf` with the server's address and starts `clued-nginx` publishing `--port` on every interface. It is a no-op while the containers run (`up.sh --recreate` replaces them, which a rerun of `init` needs) and installs the launchd agent. The server container runs as root because bind-mounted files appear root-owned inside Apple containers. `down.sh` removes the containers and the agent; `down.sh --volumes` deletes the data too |
| `launchd/com.robbiebyrd.clued-server.plist` | Runtime `apple`: the agent `up.sh` copies into `~/Library/LaunchAgents` so the stack starts at login (Apple Containers have no restart policy); it logs to `launchd/up.log` |
| `run/` | Runtime `apple`: the storage and nginx configs rendered with the addresses of the current start |
| `nginx/clued.conf` | The front-end: TLS 1.2+, the `Authorization` header and WebSocket upgrades passed through, bodies up to the server's 16 MiB cap, hour-long reads for `/ws` |
| `mind-palace.config.yaml` | The plan and story storage: MongoDB alone, namespaced per project (see [Storage on the server](#storage-on-the-server)) |
| `tls/ca.pem`, `tls/ca-key.pem` | A private CA, valid ten years and name-constrained to the hosts below (it can sign for this server and nothing else); clients put `ca.pem` in `serverCaFile`, or this machine trusts it with [`server trust`](#trusting-the-ca-on-this-machine) |
| `tls/cert.pem`, `tls/key.pem` | The server certificate signed by that CA, valid two years, covering `localhost`, `127.0.0.1`, `::1`, this host's name and every `--host` |
| `tokens.json` | The token file; `init` mints one token named after the host (`--name`, `--project`, `--account` as for `server token create`) and `mind-palace server token …` finds the file here by default |

`init` is safe to rerun: the runtime's files and the nginx and storage files
are regenerated (so a plugin upgrade re-pins the image with another `init`,
followed by the `up` command again: `docker compose … up -d`, or `up.sh
--recreate`), `tls/` and `tokens.json` are kept, and a token whose name
exists is not minted again (`notes` says so). `--force` reissues the server
certificate, which is how a name is added later. The CA is kept while it
covers every host and has more than 30 days left, so the machines that
already trust it keep working; a host outside its name constraints needs a
new CA, and `--new-ca` asks for one outright. Either way `notes` says when
the CA changed, and every machine that trusted the old one must trust the
new one (`server trust` again here, a new `ca.pem` in `serverCaFile`
elsewhere). The result names the CA file and its SHA-256 fingerprint (`ca`,
`ca_fingerprint`):

```bash
mind-palace server init --host clued.home.arpa --host 192.168.1.20 --force --no-token
docker compose -f ~/.clued-server/docker-compose.yml up -d --force-recreate nginx   # or: bash ~/.clued-server/up.sh --recreate
```

The Apple runtime follows the `container` command reference and is
exercised in the tests against a stub of the CLI, not on a Mac: that the
port published as `0.0.0.0:<port>` is reachable from other machines, and
that bind-mounted files are root-owned inside the container (why the server
runs as root there), are what the reference documents. If a start fails,
`container logs clued-server` and `launchd/up.log` say why, and `--runtime
compose` with Docker is the fallback.

#### Trusting the CA on this machine

Clients of the plugin read the CA from `serverCaFile`, but a browser (the
HTTPS `/mcp` endpoint, `/health`), `curl` without `--cacert` or any other
tool reads only the machine's trust stores. `mind-palace server trust`
(`scripts/clued server trust` from the plugin; `/clued-setup` offers it)
puts `tls/ca.pem` there:

| Platform | Stores | Root |
|---|---|---|
| macOS | The login keychain, as a trusted root (`security add-trusted-cert`); Safari, Chrome, Firefox, curl and Go read it | No; macOS may ask for the account password |
| Linux | The system store: `update-ca-certificates` (Debian, Ubuntu, Alpine, openSUSE), `update-ca-trust` (Fedora, RHEL) or p11-kit's `trust anchor` (Arch); the file is `clued-server-ca-<fingerprint>.crt` | Yes |
| Linux | The NSS databases Chrome and Chromium (`~/.pki/nssdb`) and each Firefox profile (including the snap one) read, with `certutil` (`libnss3-tools`, `nss-tools`), when they exist | No |
| Windows | The current user's Root store (`certutil -user -addstore Root`) | No; Windows asks to confirm |

```bash
mind-palace server trust --dry-run   # lists the steps and changes nothing
mind-palace server trust             # installs; prints pending root steps
mind-palace server trust --remove    # takes the CA out of the same stores
```

A step that needs root runs when the command runs as root or `sudo -n`
works; otherwise it is left in `pending` as the exact shell line to run. When
nothing is pending the command checks `--url` (by default the stack's
`https://localhost:<port>`) with the system roots alone and reports
`verified`. `--dir` names another stack directory and `--ca` any other CA
file. The name constraints are what make this safe: even with its key, the
CA cannot vouch for any host but the stack's. A CA from an older `init` has
none; `init --new-ca` replaces it with one that does.

When `/clued-uninstall` deletes the stack's data (or `hooks/uninstall
--purge-server` does), it runs `server trust --remove` before it deletes the
directory; root steps it could not run are printed, with the CA copied to
`~/clued-server-ca.pem` for them.

**Other machines** connect through the same nginx: their `serverUrl` is
`https://<name>:8443` for a name the certificate covers and their
`serverCaFile` a copy of `tls/ca.pem`. Each gets its own token, either
through the [device login](#device-login) (`scripts/clued session login
--server https://<name>:8443 --ca-file <ca.pem>` on the new machine,
`scripts/clued server device approve <code>` on this one) or minted by hand
(`mind-palace server token create --name desktop --project '*'`, with
`--account` to share one session history). A cloud container does the same
with the `CLUED_SERVER_*` variables.

**Other front-ends.** nginx is a default, not a requirement:

- `--no-tls` keeps the nginx service but generates no CA: put a certificate
  the clients already trust in `tls/cert.pem` and `tls/key.pem` (one from
  [mkcert](https://github.com/FiloSottile/mkcert) for a LAN, or a real CA for
  a public name) and leave `serverCaFile` unset.
- `--proxy none` leaves nginx out and publishes the server itself as plain
  HTTP on `127.0.0.1:<port>` (8443 by default), for a terminator you run on
  the host: Caddy or nginx as in [Behind a reverse proxy](#behind-a-reverse-proxy),
  Traefik, or an ingress when the same three services run under Kubernetes.
  A client on that machine may use `http://127.0.0.1:8443` directly (loopback
  needs no TLS); every other client goes through the terminator.
- Without containers at all, run the binary as in [From the binary](#from-the-binary)
  behind any of those.

#### From a source checkout

Someone working on the server runs it from their checkout with the stack's
certificates and token file, and no Docker (stop the stack first, or pick
another port):

```bash
mind-palace server init --name dev     # certificates and a token; nothing starts
mind-palace server trust               # optional
cd <checkout>/mind-palace
go run ./cmd/mind-palace server run --addr 127.0.0.1:8443 \
  --tls-cert ~/.clued-server/tls/cert.pem --tls-key ~/.clued-server/tls/key.pem \
  --tokens ~/.clued-server/tokens.json --store memory
```

Drop `--store memory` to keep data: sessions go to `CLUED_MONGO_URL` and
`CLUED_DB_NAME`, plans and stories to the storage of `--config` (a copy of
`~/.clued-server/mind-palace.config.yaml` pointed at a MongoDB on the host).
Give it databases of its own, apart from a local-mode daemon's
`claude_sessions` and a local clued cloud's. `server run` ignores
`serverUrl`, `serverToken` and `serverCaFile` in the machine's `config.json`,
so a machine whose client points at this server does not make the server
relay to itself. Clients then use it exactly as they use the stack.

#### Next to a local clued cloud

A clued cloud run on the same machine (`clued-cloud serve`, or its
`docker compose up`) does not collide with the stack:

| | Server stack | clued cloud |
|---|---|---|
| Port | `8443` (`server init --port`) | `8080` (`--addr`) |
| Compose project | `clued` (containers `clued-nginx`, `clued-server`, `clued-mongo`) | `clued-cloud` |
| MongoDB | the volume `clued_clued_mongo_data`, not published | its own volume, not published |

The local daemon of local mode listens on `8085`. A client's `config.json`
names one server at a time: `/clued-setup` asks before it replaces a
`serverUrl` and keeps a copy of the file, and `CLUED_SERVER_URL`,
`CLUED_SERVER_TOKEN` and `CLUED_SERVER_CA_FILE` point one shell at the other
server without touching the file.

`/clued-uninstall` (and `hooks/uninstall`) stops the stack with
`docker compose down`, or with `down.sh` on Apple Containers (which also
removes the launchd agent); the MongoDB volume and `~/.clued-server` stay unless
`--purge-server` is given, so a reinstall finds the data, the tokens and the
CA where they were.

## Tokens

The token file (`--tokens`, `CLUED_SERVER_TOKENS`, default
`~/.clued-server/tokens.json`, mode 0600) holds one entry per token with its
name, the hex SHA-256 of the raw token, the account it is bound to, its
project list, when it was created and, once revoked, when. The raw token is
printed exactly once by `create` and never stored. Lookups compare every
stored hash in constant time; the file is re-read when it changes.

```bash
mind-palace server token create --name laptop --project '*'
mind-palace server token create --name ci --project github.com__acme__widgets --project github.com__acme__api
mind-palace server token create --name desktop --project '*' --account 2f1b...   # share laptop's account
mind-palace server token list
mind-palace server token grant ci --project github.com__acme__site
mind-palace server token revoke ci
```

### Device login

A machine can get its token without anyone copying it: it asks the server
for a code, and a machine already linked to the server approves the code.

```bash
# on the new machine: prints a code such as BCDF-GHJK and waits
scripts/clued session login --server https://clued.home.arpa:8443 --ca-file ~/ca.pem
# on a machine already linked to the server (its config.json holds the server and a token)
scripts/clued server device list
scripts/clued server device approve BCDF-GHJK            # or: --name desk --project github.com__acme__widgets
scripts/clued server device deny BCDF-GHJK
```

The approved token is minted into the server's token file under the
approver's account, with the approver's projects unless `--project` narrows
them, named after the new machine (`-2`, `-3`, … when the name is taken).
`session login` then writes `serverUrl`, `serverToken` and `serverCaFile`
into the new machine's config file and checks them with `whoami`. A request
expires after 15 minutes. `session logout` revokes a machine's own token on
the server and takes the keys out of its config file; `session config-pull`
(run by the session-start hook) applies the enricher settings of the
server's session config to the machine. The routes behind this are in
[PROTOCOL.md](../PROTOCOL.md), section 7.9.

- **Account.** `create` binds the token to a new random account id unless
  `--account` names one. The server stamps that account on every session
  write and scopes every session read by it, ignoring whatever account id the
  client sends; two tokens with the same `--account` share one session
  history (a laptop and a desktop of the same person), two with different
  accounts never see each other's sessions.
- **Projects.** The list names the project slugs (see [Project slugs](#project-slugs))
  the token may read and write plans and stories for; `'*'` grants every
  project. The session routes carry no project and are scoped by account
  alone, so a token with an empty project list still captures sessions. A
  request for a project outside the list is `403 forbidden`; `grant` adds
  projects to an existing token without minting a new one.
- **Revocation.** `revoke` marks the entry; the next request with that token
  is `401`. On the client, every relay logs the 401 to `relay.log` and buffers
  its event in the WAL, and the next session start reports the rejected token
  through `check-setup`; nothing is lost once a new token is written.
- `GET /api/v1/whoami` returns the principal of the presented token with its
  projects and the server version. It is the connectivity probe of
  `/clued-setup` and `check-setup`, and `mind-palace session whoami` prints it.

## TLS

Transcripts and tool results leave the machine, so the transport is HTTPS:
the server refuses to serve plain HTTP on a non-loopback address unless told
a proxy terminates TLS, and the client refuses a plain `http://` URL unless
the host is loopback or `serverInsecure` is set. Two setups cover the usual
deployments.

### Certificate files

Pass `--tls-cert` and `--tls-key` (or `CLUED_SERVER_TLS_CERT` and
`CLUED_SERVER_TLS_KEY`); TLS 1.2 is the minimum. Any PEM pair works: a
Let's Encrypt certificate from certbot or acme.sh, or one from a private CA.
The server reads the files at startup; restart it after a renewal. With a
private CA the clients put the CA bundle in `serverCaFile` (next section),
which is trusted in addition to the system roots.

### Behind a reverse proxy

With `--behind-proxy` (or `CLUED_SERVER_BEHIND_PROXY=true`) the server serves
plain HTTP on any address and leaves TLS to the proxy in front of it. Bind it
to loopback or to the container network, never to a public interface. The
proxy must pass the `Authorization` header through unchanged and allow long
responses and WebSocket upgrades (`/ws`).

Caddy (automatic certificates):

```
clued.example.com {
    reverse_proxy 127.0.0.1:8443
}
```

nginx:

```nginx
server {
    listen 443 ssl;
    server_name clued.example.com;
    ssl_certificate     /etc/letsencrypt/live/clued.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/clued.example.com/privkey.pem;
    client_max_body_size 16m;

    location / {
        proxy_pass http://127.0.0.1:8443;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header Authorization $http_authorization;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;
    }
}
```

Request bodies are capped at 16 MiB on the server, headers at 1 MiB, and the
header read at 10 s, independently of the proxy's own limits.

## Storage on the server

### Plans and stories: `mind-palace.config.yaml`

The server reads the same configuration file as a repository checkout
(`--config`, `MIND_PALACE_CONFIG`), but only its `storage` list matters and,
unlike the client, it does not need a file store: `server run` validates
with `AllowNoFileStore`, so a database alone is accepted. The
`mind-palace.config.yaml` that `server init` writes is the example:

```yaml
storage:
  - name: mongo
    kind: mongodb
    options:
      uri: mongodb://mongo:27017
      database: mind_palace
      collection: mp_
```

Every enabled entry is namespaced per project: the server copies the
definition for each project and stamps its prefix option with the slug
(`collection: mp_<project>_` for MongoDB and Firestore, `tablePrefix:
<project>_` for the SQL stores, `dir: <root>/<project>` for a file store), so
one database holds every project apart. Any store plugin of mind-palace works
(SQLite, Postgres, MySQL, ClickHouse, MongoDB, Firestore, file); see
`mind-palace/README.md` for their options. At least one entry must be
enabled.

### Sessions: `CLUED_MONGO_URL`

Sessions, transcript lines, subagent lines, blobs and hook events go to the
MongoDB named by `CLUED_MONGO_URL` and `CLUED_DB_NAME` (or by `mongoUrl` and
`dbName` in the `--session-config` file). It can be the same instance as the
document store, in another database. The enrichers run on the server with
the `enabledEnrichers` and `disabledEnrichers` of the session config file;
with the generated compose file, mount a `config.json` holding them into
`clued-server` and add `--session-config /etc/clued/config.json` through
`command:`.

## Client setup

### `config.json`

The plugin's per-machine configuration, `~/.claude/plugins/data/clued/config.json`,
switches into remote mode when `serverUrl` is set. The file holds a token,
so write it with mode 0600.

| Key | Variable | Meaning |
|---|---|---|
| `serverUrl` | `CLUED_SERVER_URL` | The server, for example `https://clued.example.com`. Remote mode is on exactly when this is set |
| `serverToken` | `CLUED_SERVER_TOKEN` | The raw token `server token create` printed, or the one `session login` received |
| `serverCaFile` | `CLUED_SERVER_CA_FILE` | Optional PEM bundle trusted in addition to the system roots (a private CA) |
| `serverInsecure` | `CLUED_SERVER_INSECURE` | `true` allows a plain `http://` URL off loopback; never for a real deployment |

```json
{
  "serverUrl": "https://clued.example.com",
  "serverToken": "…",
  "projectsDir": "~/.claude/projects",
  "disabledEnrichers": [],
  "enabledEnrichers": []
}
```

The existing keys keep their meaning; `mongoUrl` and `dbName` are not used
while `serverUrl` is set (no MongoDB is opened on the client). The environment variables
override the file, which is how a cloud container is configured without a
file at all.

`/clued-setup` asks first whether to use a local MongoDB, a clued server in
containers on this machine, a clued server someone else runs, or a clued
cloud (next section). The container option runs `server init` (Docker, or
Apple Containers on a Mac without Docker) and the result's `up` command and
fills the file from the result; the last asks for the URL and token. Both then write the file,
check the connection and register the hooks, and start no daemon. The
check is:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" session whoami
```

which prints the token's name, account, projects and the server version, or
fails with exit 10 and an error naming the cause (unreachable, TLS,
`unauthorized`). `check-setup` runs the same probe on every session start
and reports an unreachable server, a rejected token, or a token whose project
list does not cover the current repository as one system message.

### clued cloud: linking without a token

A clued cloud (the hosted, multi-tenant server built on this module in
`robbiebyrd/clued-cloud`) accepts the same client, but the token is obtained
by logging in rather than copied. `/clued-setup`'s fourth option runs:

```bash
"${CLAUDE_PLUGIN_ROOT}/scripts/clued" session login --server https://cloud.example.com
```

which starts the device flow (`POST /api/v1/device/code`), prints a link
such as `https://cloud.example.com/link?code=BCDF-GHJK` and the code on
stderr, and polls (`POST /api/v1/device/token`) until the user approves in a
browser (terminal, desktop app or cloud container alike: any browser does).
It then writes `serverUrl` and `serverToken` (and `serverCaFile` with
`--ca-file`) into `config.json` with mode 0600, keeping the other keys, and
checks the token with `whoami`. `--name` sets how the machine is listed in
the cloud (the host name by default); `--wait` bounds the wait (15 minutes).

- `session logout` revokes the machine's token on the cloud
  (`DELETE /api/v1/me/tokens/current`) and removes the server keys from the
  file, which puts the plugin back in local mode (`--keep-config` revokes
  only).
- `session config-pull` fetches the account's client settings
  (`GET /api/v1/me/config`: `enabledEnrichers`, `disabledEnrichers`,
  `projectsDir`, `fileHistoryDir`) into `config.json`; `session-start` runs
  it with `--quiet` in remote mode, so settings changed in the cloud's web
  app reach every linked machine at its next session. Against a plain clued
  server the route does not exist and the command is a no-op.

### What runs on the client

Nothing stays resident. In remote mode:

- `hooks/event-relay` pipes each hook's JSON to `scripts/clued hook relay`
  instead of posting to the loopback daemon, and `hooks/session-start` relays
  the `SessionStart` event and runs `session backfill` detached rather than
  starting a daemon. The hooks stay `async`, so Claude never waits.
- `hook relay` stores the event under the token's account, upserts the
  session with its cwd, git origin, branch and host on first sight, pushes
  the transcript lines (and subagent files, tool results and file-history
  blobs) written since the previous hook, and replays the WAL, all within a
  5 s budget. `Stop` and `SessionEnd` always run the catch-up so the end of a
  turn is never left behind. It prints nothing and exits 0 whatever happened.
- Catch-up state lives under `~/.claude/plugins/data/clued/catchup/<session>.json`
  (byte offset, line sequence, per-artifact size and mtime), with a
  `<session>.lock` that serializes concurrent hooks of one session; a relay
  that finds the lock held leaves the work to the next hook.
- A failed relay appends one timestamped line to
  `~/.claude/plugins/data/clued/relay.log` (mode 0600). A failed event insert
  is buffered in the write-ahead log, `events.wal` next to `config.json`, and
  replayed in order by the first relay that reaches the server; transcript
  lines are not buffered, they are re-read from the file by offset. A failed
  catch-up leaves the offset unchanged, so the next hook retries from the
  same place.
- Sessions that ran before remote mode was configured are loaded by `session
  backfill` on the next session start, which walks `projectsDir` and upserts
  them through the server, exactly as it does for MongoDB.
- `session find|context|full|search-commands|transcript|restore`, the `clued`
  MCP server's tools and `prime_context` work unchanged, reading through the
  server. `/clued-uninstall` removes the config directory with the catch-up
  state, `relay.log` and the WAL.

## Plans and stories from a repository

With `serverUrl` set, everything that opens the plan and story service on
the client (the `plan` and `story` commands, the `mind-palace` MCP server,
`mind-palace serve`, the hook guards' status review and `prime_context`)
appends a store named `clued-server` of kind `clued` after the repository's
own stores. `mind-palace stores` lists it and `mind-palace config` (and the
`getConfig` tool) show its definition without the token:

```
$ mind-palace stores
{"ok": true, "result": {"stores": ["file (file)", "clued-server (clued)"]}}
```

The file store stays primary: reads come from the files, every write goes
to the files first and then to the server. When the server does not answer,
the write still succeeds locally and the command reports it:

```
mind-palace: clued-server is not answering (…); plans and stories are kept locally, run `mind-palace sync --from file --to clued-server` once it is back
```

and the result carries a `warning` naming the store. Once the server is
back, reconcile from the repository:

```bash
mind-palace sync --from file --to clued-server                      # copies what the server lacks
mind-palace sync --from file --to clued-server --on-conflict overwrite   # the files win on every document
```

`sync` copies plans, stories and templates between two configured stores
and reports, per kind, the ids it copied, left unchanged, overwrote and
the conflicts it refused (`--on-conflict error`, the default, fails on the
first one; `skip` leaves them); the `sync` MCP tool does the same.

### Project slugs

The server files a repository's documents under a project slug and checks
it against the token's project list. The slug is derived from the git
origin: host and path lower-cased, user info, port, a leading `/` and a
trailing `.git` removed, `/` encoded as `__`, every other character outside
`[a-z0-9._-]` replaced by `-`, at most 200 characters. The three common forms
of one origin give the same slug:

```
git@github.com:acme/widgets.git      → github.com__acme__widgets
https://github.com/acme/widgets.git  → github.com__acme__widgets
ssh://git@github.com/acme/widgets    → github.com__acme__widgets
```

A repository without an origin uses its directory name (`Scratch Pad` →
`scratch-pad`) and the command says so on stderr, because two unrelated
folders of the same name on two machines would share that namespace. To
choose the slug by hand, or to point several repositories at one project,
set `remote.project` in the repository's `mind-palace.config.yaml`; the value
is normalized the same way:

```yaml
remote:
  project: acme__widgets
```

`check-setup` warns at session start when the token does not cover the
repository's slug and prints the `server token grant` command that fixes it.

## Claude Code telemetry

Claude Code can export its own OpenTelemetry events: one per API request
(model, cost, tokens, duration), API error and tool result. The clued server
receives them at `/v1/logs` (and accepts `/v1/metrics` without storing it)
and `get_session_context` sums them under `telemetry`. Turn it on where
Claude Code runs, for example in `~/.claude/settings.json`:

```json
{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_METRICS_EXPORTER": "none",
    "OTEL_EXPORTER_OTLP_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_ENDPOINT": "https://clued.example.com"
  },
  "otelHeadersHelper": "jq -c '{Authorization: (\"Bearer \" + .serverToken)}' ~/.claude/plugins/data/clued/config.json"
}
```

The headers helper reads the token from the plugin's `config.json`, so it
is not copied into `settings.json`; `OTEL_EXPORTER_OTLP_HEADERS="Authorization=Bearer <token>"`
works too. `http/protobuf` is accepted as well, gzip or not. Against the
Docker stack's private CA, Claude Code (a Node program) also needs
`NODE_EXTRA_CA_CERTS=~/.clued-server/tls/ca.pem`: `server trust` puts the
CA in the system store, which Node does not read by default. In local mode
the daemon serves the same routes without a token:
`OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:8085` and no headers.

Only events of sessions the server (or daemon) already captured are kept,
with an allow-list of attributes: prompt text, tool input, error output and
the e-mail and account attributes Claude Code adds are dropped whatever
its `OTEL_LOG_*` settings let it send. Section 7.10 of
[`PROTOCOL.md`](../PROTOCOL.md) lists what is stored.

## The HTTPS MCP endpoint

The server serves MCP over Streamable HTTP at `/mcp` behind the same tokens.
It needs to know the project, which the `X-Clued-Project` header carries
(`?project=<slug>` is the fallback). Register it once per server and project
with Claude Code:

```bash
claude mcp add --transport http clued-remote https://clued.example.com/mcp \
  --header "Authorization: Bearer <token>" \
  --header "X-Clued-Project: github.com__acme__widgets"
```

The endpoint serves every `plan_*` and `story_*` tool plus `sync` and
`getConfig` for that project, and the session tools that need no local disk:
`find_sessions`, `get_session_context`, `get_full_session`, `search_commands`
and `read_transcript`, scoped to the token's account. `restore_session`
(writes the local filesystem) and `prime_context` (probes the local
repository) are not there; they stay on the plugin's stdio `clued` server,
which in remote mode reads through the same server. The plan and story
resources (`plan://<id>`, `story://<id>`) and the `watch` tool work over the
endpoint as well, so a client gets change notifications for its project.

The same header selects the project for the service API under `/api/v1`
(`GET /api/v1/plans`, `/api/v1/stories`, …) and the WebSocket at `/ws`; a
request without it is `400 invalid`, one for a project outside the token's
list is `403 forbidden`.

## Claude Code cloud containers

A Claude Code cloud container (Claude Code on the web, `CLAUDE_CODE_REMOTE=true`)
is reclaimed when its session ends, so the plugin never runs local mode there:
`session-start` starts no daemon, `event-relay` buffers nothing, and
`check-setup` asks for Clued Cloud (`https://clued.cloud`) or a clued server
until one is configured. `/clued-setup` offers only those two and links the
environment rather than the container: the user creates a token (on the
cloud's **Linked clients** page, `/links`, or with `server token create`) and
adds `CLUED_SERVER_URL` and `CLUED_SERVER_TOKEN` (and `CLUED_SERVER_CA_FILE`
for a private CA kept in the repository) as environment variables in the
environment's settings. Every new container of that environment starts
linked, with no config file and no browser approval; the environment's
network access must allow the server's host. Revoking that token unlinks all
of them; `session logout` in one container would do the same, so it is not
the way to unlink there.

## Cloud container walkthrough

The scenario that motivated the server: a Claude Code session in a cloud
container whose only way out is HTTPS, with no MongoDB and no way to run a
daemon that outlives the session. The steps below are the manual check of a
deployment; the Go test `TestRemoteEndToEnd` in `mind-palace/cli` runs the
same loop in-process.

1. **Server.** On a host reachable as `https://clued.example.com`, run the
   server (the binary, or the stack from `server init`) and mint a token:

   ```bash
   mind-palace server token create --name cloud --project '*'
   ```

   Confirm from anywhere that `curl -s https://clued.example.com/health`
   answers and that the token is accepted:

   ```bash
   curl -s -H "Authorization: Bearer <token>" https://clued.example.com/api/v1/whoami
   ```

2. **Container.** Install the plugin, then configure it through the
   environment (no file to write, nothing to leave behind) or with
   `/clued-setup`'s server option:

   ```bash
   export CLUED_SERVER_URL=https://clued.example.com
   export CLUED_SERVER_TOKEN=<token>
   "${CLAUDE_PLUGIN_ROOT}/scripts/clued" session whoami
   ```

   `whoami` must print the principal named `cloud`; a TLS failure here means
   the proxy's certificate is not trusted by the container (use
   `CLUED_SERVER_CA_FILE` for a private CA).

3. **Capture.** Start a Claude Code session in a repository with an origin
   and run a couple of commands. No daemon appears in `ps`; each hook runs
   `hook relay` and exits. `~/.claude/plugins/data/clued/catchup/<session>.json`
   advances with the transcript and `relay.log` stays absent.

4. **Read back.** From any machine with a token of the same account, or in
   the same session through the `clued` tools:

   ```bash
   mind-palace session find --limit 5
   mind-palace session transcript <session-id> --limit 3
   ```

   Both answer from the server with the container's session.

5. **Plans and stories.** In the container's repository:

   ```bash
   mind-palace plan create --input '{"frontMatter":{"title":"From the cloud","type":"drft","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}'
   curl -s -H "Authorization: Bearer <token>" \
     https://clued.example.com/api/v1/projects/github.com__acme__widgets/store/plan | jq '.result[].frontMatter.id'
   ```

   The plan exists under `docs/plans` in the container and on the server
   under the repository's slug. Stop the server, create another plan (the
   command warns and keeps the file), start the server again and run
   `mind-palace sync --from file --to clued-server`; the second plan appears
   on the server.

6. **MCP over HTTPS.** Register the endpoint with `claude mcp add
   --transport http` as above and check `claude mcp list` shows
   `clued-remote` connected; its `plan_list` returns both plans and its
   `find_sessions` the container's session.
