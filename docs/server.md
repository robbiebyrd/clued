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

Each `v*` release publishes `ghcr.io/robbiebyrd/clued-server:<tag>` and
`:latest` (linux/amd64 and linux/arm64, built by `.github/workflows/release.yml`
from `Dockerfile.server`). The image runs `mind-palace server run` as a
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

### The Docker stack: `mind-palace server init`

The way to run the server on a machine of your own is the Compose stack that
`mind-palace server init` generates and `/clued-setup` starts for you (its
"clued server in Docker on this machine" option):

```bash
mind-palace server init --name laptop            # ~/.clued-server, prints the token once
docker compose -f ~/.clued-server/docker-compose.yml up -d
curl --cacert ~/.clued-server/tls/ca.pem https://localhost:8443/health
```

The directory holds everything the stack needs and is the server's home:

| File | What it is |
|---|---|
| `docker-compose.yml` | Three services: `nginx` (publishes `${CLUED_PUBLIC_PORT:-8443}` over HTTPS and forwards to the server), `clued-server` (plain HTTP inside the compose network, `CLUED_SERVER_BEHIND_PROXY=true`, runs as your uid so it can read the token file) and `mongo` (`mongo:8` on the named volume `clued_mongo_data`, not published). The server image is the release of the plugin that ran `init` (`ghcr.io/robbiebyrd/clued-server:v<version>`, `:latest` from a bare binary); `CLUED_SERVER_IMAGE` overrides it without regenerating |
| `nginx/clued.conf` | The front-end: TLS 1.2+, the `Authorization` header and WebSocket upgrades passed through, bodies up to the server's 16 MiB cap, hour-long reads for `/ws` |
| `mind-palace.config.yaml` | The plan and story storage: MongoDB alone, namespaced per project (see [Storage on the server](#storage-on-the-server)) |
| `tls/ca.pem`, `tls/ca-key.pem` | A private CA, valid ten years; clients put `ca.pem` in `serverCaFile` |
| `tls/cert.pem`, `tls/key.pem` | The server certificate signed by that CA, valid two years, covering `localhost`, `127.0.0.1`, `::1`, this host's name and every `--host` |
| `tokens.json` | The token file; `init` mints one token named after the host (`--name`, `--project`, `--account` as for `server token create`) and `mind-palace server token …` finds the file here by default |

`init` is safe to rerun: the compose, nginx and storage files are
regenerated (so a plugin upgrade re-pins the image with another `init`,
followed by `docker compose … up -d`), `tls/` and `tokens.json` are kept, and
a token whose name exists is not minted again (`notes` says so). `--force`
renews the CA and the certificate, which is how a name is added later:

```bash
mind-palace server init --host clued.home.arpa --host 192.168.1.20 --force --no-token
docker compose -f ~/.clued-server/docker-compose.yml up -d --force-recreate nginx
```

**Other machines** connect through the same nginx: their `serverUrl` is
`https://<name>:8443` for a name the certificate covers, their `serverCaFile`
a copy of `tls/ca.pem`, and each gets its own token
(`mind-palace server token create --name desktop --project '*'`, with
`--account` to share one session history). A cloud container does the same
with the `CLUED_SERVER_*` variables.

**Other front-ends.** nginx is a default, not a requirement:

- `--no-tls` keeps the nginx service but generates no CA: put a certificate
  the clients already trust in `tls/cert.pem` and `tls/key.pem` (one from
  [mkcert](https://github.com/FiloSottile/mkcert) for a LAN, or a real CA for
  a public name) and leave `serverCaFile` unset.
- `--proxy none` leaves nginx out and publishes the server itself as plain
  HTTP on `127.0.0.1:${CLUED_PUBLIC_PORT:-8443}`, for a terminator you run on
  the host: Caddy or nginx as in [Behind a reverse proxy](#behind-a-reverse-proxy),
  Traefik, or an ingress when the same three services run under Kubernetes.
  A client on that machine may use `http://127.0.0.1:8443` directly (loopback
  needs no TLS); every other client goes through the terminator.
- Without Docker at all, run the binary as in [From the binary](#from-the-binary)
  behind any of those.

`/clued-uninstall` (and `hooks/uninstall`) stops the stack with
`docker compose down`; the MongoDB volume and `~/.clued-server` stay unless
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
| `serverToken` | `CLUED_SERVER_TOKEN` | The raw token `server token create` printed |
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
Docker on this machine, or a clued server someone else runs. The Docker
option runs `server init` and `docker compose up` and fills the file from
the result; the last asks for the URL and token. Both then write the file,
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
