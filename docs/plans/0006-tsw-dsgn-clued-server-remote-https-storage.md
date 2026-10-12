---
id: 0006-tsw
title: 'Clued server: remote HTTPS storage for sessions, plans and stories'
type: dsgn
status: ready
priority: "2"
effort: XL
created: "2026-10-12T19:57:12.904Z"
updated: "2026-10-12T21:36:27.219Z"
tags:
  - server
  - remote
  - feature
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  plans:
    - ["0003-7lv", "depends"]
    - ["0001-v1i", "depends"]
progress:
  "1":
    status: complete
  "1.1":
    status: complete
    stories:
      - 0002-ctl
  "1.2":
    status: complete
    stories:
      - 0003-sd3
  "1.3":
    status: complete
    stories:
      - 0004-n6z
  "1.4":
    status: complete
    stories:
      - 0005-owt
  "2":
    status: complete
  "2.1":
    status: complete
    stories:
      - 0006-cxf
  "2.2":
    status: complete
    stories:
      - 0007-frt
  "2.3":
    status: complete
    stories:
      - 0008-gep
  "2.4":
    status: complete
    stories:
      - 0009-pdw
  "3":
    status: complete
  "3.1":
    status: complete
    stories:
      - 0010-54u
  "3.2":
    status: complete
    stories:
      - 0011-zbb
  "3.3":
    status: complete
    stories:
      - 0012-gbx
  "4":
    status: pending
  "4.1":
    status: complete
    stories:
      - 0013-m3a
  "4.2":
    status: complete
    stories:
      - 0014-5fn
  "4.3":
    status: in_progress
    stories:
      - 0015-mvd
  "5":
    status: pending
  "5.1":
    status: in_progress
    stories:
      - 0016-pu9
  "5.2":
    status: pending
    stories:
      - 0017-k62
---

# Clued server: remote HTTPS storage for sessions, plans and stories

## Summary

**Goal:** A machine whose `config.json` names a clued server URL and a token runs Claude Code with the clued plugin and nothing else: no MongoDB, no `session daemon`, no listening port. Every hook event, transcript line, subagent line and blob, and every plan and story, is persisted on the server over HTTPS, and the session and mind-palace tools answer from it.

**Problem:** clued persists sessions only through a MongoDB the client reaches directly (`mongoUrl`), with a long-running local daemon on port 8085 that tails transcripts and ingests hook events. Plans and stories live in the repository's `docs/` folders with optional fan-out to databases the client must also reach directly. That shape fails wherever the client cannot open a database connection: cloud containers whose egress proxy carries only HTTPS (this session's container is one: port 27017 times out), locked-down laptops, and any machine where running `mongod` or a daemon is unwelcome. It also puts the database password in clear text on every client and ties account isolation to a string the client chooses (`lastKnownAccountUuid`).

**Approach:** Make the two storage ports the wire protocol. A new `mind-palace server` mode serves `session.Store` and `store.Store` over HTTPS behind bearer-token auth, with its own databases behind it; the client gains a `remotesession` adapter and a `clued` store driver that implement those ports over HTTP, validated by the existing conformance suites. Hook events reach the server from a short-lived `hook relay` subcommand that also pushes the transcript delta, so the daemon is not needed; plans and stories keep the file store as primary and fan out to the server as a secondary store through the existing `MultiStore`.

---

# Part 1 — Design

## Current State

**Two local servers today.** `mind-palace serve` (`server/server.go`, default `127.0.0.1:8087`) composes the plan and story REST API (`server/httpapi`, `/api/v1/...`), the WebSocket (`server/ws`, gorilla) and MCP Streamable HTTP (`server/mcpserver`, `/mcp`) over one `service.Palace`. `mind-palace session daemon` (`session/daemon/daemon.go`, default `127.0.0.1:8085`) serves only `GET /health` and `POST /event`; `handleEvent` upserts the session with git and host info, tails the transcript under `projectsDir` (`session/tail.TailFile`), mirrors subagent files and file-history blobs (`tail.WatchArtifacts`), stamps `account_id`, `host` and `created_at` on the event and calls `Store.InsertHookEvent`, falling back to the JSONL write-ahead log (`session/wal`) when the store fails. `Run` flushes the WAL every 60 s and runs `enrich.Loop` every 5 s.

**Reads never go through the daemon.** The `clued` MCP server (`scripts/clued session mcp`, `session/sessionmcp`) and the `session find|context|full|transcript|restore|backfill` commands connect to MongoDB directly through the package variable `openSessionStore` in `cli/session.go` (`mongosession.New(cfg).Connect`). `prime_context` runs local probes and opens a local `Palace` for the STORIES and PLANS sections (`collect/sources/docsource`). `restore_session` writes files to the local disk.

**Ports already exist.** `session.Store` (`session/store.go`) has two adapters, `mongosession` and `memsession`, and the conformance suite `session/sessiontest.Run`. `store.Store` (`store/store.go`) has `filestore`, `sqlstore` (sqlite, postgres, mysql, clickhouse), `mongostore`, `firestorestore` and `memstore`, registered through `store.Register` in `init()`, opened by `store.OpenAll` into a `MultiStore` whose first store is the read primary and whose secondary failures become a `FanoutError` that points at `mind-palace sync`. `config.Validate` requires one enabled `kind: file` store. The conformance suite is `store/storetest.Run`.

**No auth, no TLS, loopback everywhere.** Nothing handles Authorization headers or `ListenAndServeTLS`. The only protection is the daemon's rejection of requests carrying an `Origin` header and the WebSocket same-host check. Every client endpoint is hard-coded to `http://127.0.0.1:<port>` in `hooks/event-relay`, `hooks/session-start`, `collect/snapshot.Post` and `prime.DaemonHealthy`. The account id is whatever `session.ReadAccountID` finds in the Claude desktop config, `"unknown"` otherwise.

**Client config.** `~/.claude/plugins/data/clued/config.json` (`session/config.go`) holds `mongoUrl`, `dbName`, `port`, `projectsDir`, `fileHistoryDir`, `disabledEnrichers`, `enabledEnrichers`, `claudeAppConfigPath`, with `CLUED_*` overrides; `devRepoPath` is read only by the shim and `session-start`. The hooks are registered by `hooks/register-hooks`; `event-relay` is async on every event and appends to the WAL under `umask 077` when the POST fails.

## Decisions

- **The storage ports are the wire protocol: the server exposes `session.Store` and `store.Store` method-for-method over HTTPS, and the client adapters implement those same interfaces.** — Every consumer (`query.Service`, `restore`, `backfill`, `enrich`, `sessionmcp`, `service.Palace`, the hooks the binary serves) already talks to the ports, so none of them changes. The conformance suites `sessiontest.Run` and `storetest.Run` then validate the remote adapters end to end against an in-process `httptest` server, which is the strongest test the repository has.
- **The client runs no daemon in remote mode. Hook events are relayed by a new short-lived `mind-palace hook relay` subcommand that also pushes the transcript and artifact delta since the previous hook, keeping its offset in a small state file.** — Hooks fire on every tool use, prompt and stop, so a per-hook catch-up delivers transcript lines within one hook of being written, which is enough for a persistence store. It removes the listening port, the restart-on-rebuild dance in `session-start` and the `ErrAddrInUse` race. The daemon stays as the local-MongoDB mode; the ingest bookkeeping is extracted into one package both modes share.
- **Plans and stories keep the file store as the primary and add the server as a secondary store through the existing `MultiStore` fan-out; sessions have no local copy in remote mode, only the WAL as an outage buffer.** — The request keeps local plans and stories, and `MultiStore` already provides fan-out, `FanoutError` and `mind-palace sync` for reconciliation. Sessions have no local store today other than MongoDB, and the WAL already covers unreachable stores.
- **The local stdio MCP servers in `.mcp.json` stay; in remote mode they are backed by the remote adapters and spawn no other process. The server additionally serves `/mcp` over HTTPS for clients that want zero local processes.** — `restore_session` writes files to the local disk and `prime_context` runs local fs, git, gh and env probes; neither can run on the server. A stdio MCP process is spawned and owned by Claude Code for the life of one session, not a daemon the user manages. The plugin manifest cannot be switched per machine, so the tool set must be identical in both modes. The remote `/mcp` endpoint carries the subset that needs no local disk (`find_sessions`, `get_session_context`, `get_full_session`, `search_commands`, `read_transcript`, all `plan_*` and `story_*`) and is registered per user with `claude mcp add --transport http`.
- **Authentication is a bearer token minted on the server (`mind-palace server token create`), stored hashed on the server and bound to an account id and a list of allowed projects; the server stamps that account id on every write, scopes every read by it, ignores any account id the client sends, and answers 403 for a project outside the token's list.** — Today isolation rests on `lastKnownAccountUuid`, a value the client chooses. Binding the account to the token moves isolation to the server, and the project list means a token leaked from one machine cannot read or write another repository's plans and stories. SHA-256 of a 32-byte random token, compared in constant time, needs no password hashing library and no database table; a 0600 JSON file the CLI edits is enough for a single-operator server. (Decided by Robbie, 2026-10-12: account plus project list.)
- **TLS is terminated by the server itself from a certificate and key file (`--tls-cert`, `--tls-key`); plain HTTP is accepted only on a loopback address or with `--behind-proxy`. The client refuses a plain `http://` server URL unless it is loopback or `serverInsecure` is set, and accepts a private CA through `serverCaFile`.** — Certificate and key files cover both Let's Encrypt (certbot, acme.sh) and private CAs without adding an ACME dependency or an HTTP-01 listener. `--behind-proxy` covers Caddy, nginx and cloud load balancers that terminate TLS. Refusing plain HTTP on the client stops a mistyped scheme from sending transcripts in clear.
- **Plans and stories on the server are namespaced by project, derived from the normalized git origin (`github.com/robbiebyrd/clued`) with a per-repository override in `mind-palace.config.yaml`; the server opens one store per project by stamping the project into the plugin-specific prefix option (`collection` for Mongo, `tablePrefix` for SQL, `dir` for file).** — Plan ids are per repository (`0001-v1i` exists in every repository), so a shared server needs a namespace. Deriving it from the origin needs no new config on the common path, and the existing prefix options give per-project isolation without changing `store.Store` or the plugins.
- **Enrichment runs on the server (`enrich.Loop` inside `mind-palace server`), not on the client.** — Enrichers are store-side passes over `Unenriched` documents and need a long-lived process, which only the server has in remote mode. The client keeps no enricher state.
- **The server is a mode of the same `mind-palace` binary (`mind-palace server run`), reusing `server/httpapi`, `server/ws` and `server/mcpserver`, and is released as a Docker image alongside the existing binaries.** — One module, one build, one `dist/` rebuild; the server's plan and story storage is the existing store plugins and its session storage is `mongosession` behind it, so nothing is written twice.

### Rejected alternatives

- **Point `mongoUrl` at a remote MongoDB and keep the daemon.** — That is today's setup and it is what fails: raw-TCP databases do not pass HTTPS-only egress, the password sits on every client, and the client still needs the daemon.
- **Tunnel MongoDB over WebSocket or HTTP CONNECT.** — The proxy in cloud containers rejects WebSocket upgrades and non-443 ports, and it would still carry database credentials to the client.
- **Keep the daemon and make it the remote client (daemon posts to the server).** — Keeps the listening port, the health polling and the restart logic the request wants gone; the per-hook relay delivers the same data without a resident process.
- **Replace the plugin's stdio MCP entries with `type: http` entries pointing at the server.** — `restore_session` and `prime_context` need the local disk, and a plugin manifest cannot differ per machine; the HTTPS `/mcp` endpoint is offered in addition, not instead.
- **Mutual TLS instead of bearer tokens.** — Client certificates are awkward to provision through `/clued-setup` and to rotate; a token in a 0600 file is one string to paste. mTLS can be layered later at the proxy.
- **ACME (autocert) built into the server.** — Adds a dependency and an HTTP-01 listener on port 80 for a single-operator service; certificate files cover the same deployments.
- **A gRPC protocol.** — gRPC does not pass the same HTTPS proxies that motivate this plan, and the REST envelope `{ok, result}` / `{ok, error}` already exists in `server/httpapi`.

## Design

### `remote` package (client transport)

`remote.Client` wraps `net/http` for one server: base URL, bearer token, TLS config from `serverCaFile`, a per-call timeout and the scheme rule (`https://` always; `http://` only for loopback hosts or when `serverInsecure` is set, else `ErrInsecureURL`). `Do(ctx, method, path, in, out)` sends JSON, decodes the `{ok, result}` envelope of `server/httpapi` and maps `{ok: false, error}` to typed errors: `kind: not_found` → `session.ErrNotFound` / `store.ErrNotFound`, 401 → `remote.ErrUnauthorized`, 403 → `remote.ErrForbidden`, 413 → `remote.ErrTooLarge`, network failures → wrapped `*url.Error`. `remote.FromConfig(session.Config) (*Client, bool)` returns the client when `serverUrl` is set; `cfg.Remote()` is the mode switch used by the CLI. Requests carry `User-Agent: clued/<version>` and the hook relay carries `X-Clued-Host` for logging only.

### `server/auth` (tokens and principals)

Token file `tokens.json` (0600) with `{"tokens": [{"name", "hash", "account_id", "projects", "created", "revoked"?}]}`; `hash` is hex SHA-256 of the raw token and `projects` is the list of project slugs the token may use (`["*"]` for every project). `auth.Middleware(source)` reads `Authorization: Bearer <token>`, compares in constant time, and puts `auth.Principal{Name, AccountID, Projects}` on the request context; failure is `401` with `WWW-Authenticate: Bearer`. `auth.AllowProject(principal, slug)` is called by `storeapi`, the service API, `/ws` and `/mcp` before any project-scoped work and answers `403 forbidden` otherwise; the session routes carry no project and are scoped by account alone. The file is re-read when its mtime changes, so revocation and project changes need no restart. `mind-palace server token create --name <n> --project <slug>... [--account <id>]` prints the raw token once (account defaults to a new UUID; `--account` lets several machines share one account; `--project '*'` grants every project); `token list`, `token revoke <name>` and `token grant <name> --project <slug>` edit the file. `GET /api/v1/whoami` returns the principal with its projects and the server version, which `/clued-setup` and `check-setup` use as the connectivity probe; `check-setup` also warns when the current repository's project is not in the list.

### `server/sessionapi` (session.Store over HTTP)

`sessionapi.Handler(store session.Store, limits)` mounts one route per port method under `/api/v1/session/`, all JSON, all behind `auth`:

| Route | Store method |
|---|---|
| `PUT /sessions` | `UpsertSession` |
| `POST /sessions/{id}/touch` | `TouchSession` |
| `POST /sessions/{id}/git` | `SetGitInfoIfMissing` |
| `GET /sessions/{id}` | `GetSession` |
| `POST /sessions/find` | `FindSessions` |
| `POST /hook-events` (array) | `InsertHookEvent` per element |
| `POST /hook-events/by-tool-use` | `HookEventIDsByToolUse` |
| `PUT /transcript-lines` (array, ≤500) | `UpsertTranscriptLines` |
| `GET /transcript-lines/count?session=` | `CountTranscriptLines` |
| `POST /transcript-lines/query` | `TranscriptLines` |
| `PUT /subagent-lines` | `UpsertSubagentLine` |
| `GET /subagents?session=` | `SubagentIDs` |
| `POST /subagent-lines/query` | `SubagentLines` |
| `PUT /blobs` | `UpsertBlob` |
| `POST /blobs/query` | `Blobs` |
| `POST /bash-events` | `BashEvents` |
| `GET /sessions/{id}/full` | `FullSession` |

The handler overwrites `account_id` on every written document and passes the principal's account into every read; the enrichment methods are not exposed (enrichment is server-side). Body limit 16 MiB (the daemon's limit, which blobs already respect). Errors use the `httpapi` envelope so one decoder serves both APIs.

### `session/remotesession` (client adapter)

`remotesession.New(client *remote.Client) session.Store` implements every port method by calling the matching route; `Unenriched` and the `SetEnrichment*` methods return `session.ErrUnsupported` (a new sentinel) so `enrich.Loop` is never wired to it. `Close` is a no-op. It is selected in `cli/session.go` when `cfg.Remote()`, so `session find|search-commands|context|full|transcript|restore|backfill`, the `clued` MCP tools and `prime_context` work unchanged. `sessiontest.Run` runs against `httptest.NewServer(sessionapi.Handler(memsession.New()))` with a fixed principal, and once more against `httptest.NewTLSServer` with the test CA written to a temp `serverCaFile`.

### `session/ingest` (shared event bookkeeping)

`ingest.Service{Store, AccountID, Host, Git, Logger, WAL}` holds what `daemon.handleEvent` does apart from tailing: on first sight of a session upsert it with cwd, project path, git origin and branch, host and account; on later events fill missing git info; touch the session; stamp `account_id`, `host`, `created_at`; `InsertHookEvent`; on failure `wal.Append`. `FlushWAL(ctx)` is the daemon's `flushWAL`. The daemon keeps its tailing and loops and delegates to this service, with `daemon_test.go` unchanged as the regression gate. `hook relay` uses the same service with a `remotesession` store.

### `session/catchup` (per-hook transcript and artifact delta)

`catchup.Run(ctx, store, State, Paths)` pushes what the daemon's tailers would have: it reads the transcript from the saved byte offset (reusing `tail`'s line parsing and `seq` numbering), upserts the new `TranscriptLine`s in batches of 500, scans `<project>/<sid>/subagents/*.jsonl` and tool results and `<fileHistoryDir>/<sid>` for files whose size or mtime changed since the saved state, and upserts them as subagent lines and blobs exactly as `tail.WatchArtifacts` does (the mirroring functions are lifted out of `WatchArtifacts` into one-shot form so both callers share them). State lives in `<datadir>/catchup/<sid>.json` (`offset`, `seq`, per-artifact `{size, mtime}`), written atomically. A lock file `<sid>.lock` created with `O_EXCL` (stale after 60 s by mtime) serializes concurrent hooks for the same session; a relay that finds the lock held exits without pushing, because the next hook catches up. `Stop` and `SessionEnd` events always run a catch-up so the end of a turn is never left behind.

### `mind-palace hook relay` (CLI, replaces the daemon on the client)

Reads the hook JSON from stdin, loads `config.json`, builds the remote store, runs `ingest.Service.Handle`, then `catchup.Run` for the event's session, then `FlushWAL` with whatever time is left of a 5 s budget, and exits 0 whatever happened (failures are logged to `<datadir>/relay.log` and buffered in the WAL). `hooks/event-relay` keeps its curl path for local mode and, when `jq .serverUrl` is non-empty, pipes stdin to `${CLAUDE_PLUGIN_ROOT}/scripts/clued hook relay` instead; it stays registered `async`. `hooks/session-start` skips the daemon when `serverUrl` is set and runs `session backfill` and a catch-up for the starting session detached. `hooks/check-setup` probes `GET /api/v1/whoami` and prints one `systemMessage` naming the server when it is unreachable or the token is rejected. `collect/snapshot.Post` and `prime.DaemonHealthy` gain a remote path that inserts the `ContextCollected` event through the store instead of posting to loopback.

### `server/storeapi` and `server/projects` (store.Store over HTTP, per project)

`storeapi.Handler(open func(project string) (store.Store, error))` serves the port under `/api/v1/projects/{project}/store/`: `GET /{kind}` (List), `GET|PUT|DELETE /{kind}/{id}` (Get, Put, Delete), `GET|PUT|DELETE /{kind}/templates[/{id}]`. `project` is a slug (`[a-z0-9._-]+`, `/` encoded as `__`), validated inline before use and checked against the principal's project list with `auth.AllowProject`. `projects.Opener` opens one store per project from the server's storage definitions by copying each enabled `StorageDef` and setting its prefix option from the project (`collection: <project>_` for mongo, `tablePrefix: <project>_` for sql, `dir: <root>/<project>` for file), wrapping them in `store.NewMulti` and caching the result; `config.Validate` gains `AllowNoFileStore` so a server may run on a database alone.

### `store/remotestore` (client driver `clued`)

Registered in `init()` as driver `clued`; options `url`, `token` and `project` are filled by the client, not written by hand. Implements `store.Store` over `remote.Client` against `storeapi`; `Name` is the definition's name (`clued-server`), `Driver` is `clued`. `storetest.Run` runs against `httptest.NewServer(storeapi.Handler(...memstore...))`. When `cfg.Remote()` holds, the places that open a `Palace` on the client (`cli` plan and story commands, `mcp`, `docsource.Open`, the hook guards' status review) append `StorageDef{Name: "clued-server", Kind: "clued", Options: {url, token, project}}` after the repository's own definitions, so the file store stays primary and the server receives every write through the existing fan-out. The project slug comes from `gitinfo` (normalized origin) with an override under a new `remote.project` key in `mind-palace.config.yaml`; a repository with no origin uses the directory name and logs it. `getConfig` and `mind-palace stores` list the remote store so `mind-palace sync --from file --to clued-server` is discoverable after an outage.

### `mind-palace server run` (the server binary mode)

`cli/server.go` adds the `server` command group: `run`, `token create|list|revoke`. `run` loads the server's plan and story config (`--config`, `MIND_PALACE_CONFIG`) and session config (`--session-config`, `CLUED_MONGO_URL`, `CLUED_DB_NAME`), opens `mongosession`, builds one `http.ServeMux`: `/health` (unauthenticated, adds `version` and `auth: "bearer"`), then under `auth.Middleware`: `/api/v1/whoami`, `sessionapi`, `storeapi`, and the existing service API, `/ws` and `/mcp` from `server.New` with a per-request project resolver (project from the `X-Clued-Project` header or `?project=`; the MCP endpoint takes it from the same header, which `claude mcp add --header` sets). It starts `enrich.Loop` with the server's enabler list and the WAL-less ingest of client events, listens with `ListenAndServeTLS(--tls-cert, --tls-key)` or plain HTTP when the address is loopback or `--behind-proxy` is set, sets `ReadHeaderTimeout`, `MaxHeaderBytes` and a 16 MiB body limit, logs one `slog` line per request with principal name, method, path, status and duration, and shuts down gracefully on SIGTERM. Flags mirror `CLUED_SERVER_ADDR`, `CLUED_SERVER_TLS_CERT`, `CLUED_SERVER_TLS_KEY`, `CLUED_SERVER_TOKENS` and `CLUED_SERVER_BEHIND_PROXY` for containers.

### Client configuration and setup

`session.Config` gains `serverUrl`, `serverToken`, `serverCaFile` and `serverInsecure` (env `CLUED_SERVER_URL`, `CLUED_SERVER_TOKEN`, `CLUED_SERVER_CA_FILE`, `CLUED_SERVER_INSECURE`); every existing key stays, and `mongoUrl` is ignored with a logged notice when `serverUrl` is set. `config.json` is written 0600 because it now holds a token. `/clued-setup` (`commands/clued-setup.md`) gains a first question, local MongoDB or clued server; the server path asks for the URL and token, writes the config, runs `scripts/clued session whoami` (new thin command around `/api/v1/whoami`) and `register-hooks`, and starts nothing. `/clued-uninstall` removes the config and the catch-up state directory. `docs/server.md` documents running the server from the binary and from the Docker image (`Dockerfile.server`, `docker-compose.server.yml` with a `mongo:8` service), token management, TLS and reverse-proxy setups, and registering the HTTPS `/mcp` endpoint with `claude mcp add --transport http clued-remote https://<host>/mcp --header "Authorization: Bearer <token>" --header "X-Clued-Project: <project>"`.

### Error Handling

**Client ingest never blocks Claude.** `hook relay` exits 0 within its 5 s budget; a failed event insert goes to the WAL (as today), a failed catch-up leaves the offset unchanged so the next hook retries from the same place, and both are logged to `relay.log`. `check-setup` surfaces an unreachable server or rejected token once per session as a `systemMessage` instead of every hook failing silently.

**Plans and stories degrade to local.** A server failure on a write is a `FanoutError` from `MultiStore`, so the MCP tool returns the document with the existing warning that names `mind-palace sync`; reads come from the file store and never touch the server.

**Server errors are typed.** `sessionapi` and `storeapi` answer with the `httpapi` envelope: `not_found` (404), `invalid` (400, with the validation problems), `unauthorized` (401), `forbidden` (403, token revoked or project not allowed), `too_large` (413), `store` (502 when the backing database fails, so the client's WAL keeps the event). The client maps these to the port sentinels so callers see `ErrNotFound` exactly as they do from MongoDB.

**Mode conflicts are explicit.** `serverUrl` set together with a running local daemon is not an error: `session-start` does not start the daemon in remote mode and `event-relay` routes by `serverUrl` alone. An `http://` server URL off loopback fails fast with `ErrInsecureURL` at config load, in `check-setup` and in `/clued-setup`.

### Edge Cases

- **Two hooks for one session at once** (`PreToolUse` and `PostToolUse` of a fast tool, or a subagent's hooks): the lock file lets one catch-up run and the other skip; events themselves are independent inserts and need no lock.
- **A transcript rewritten or truncated** (session resumed with `--resume`, or the file replaced): the saved offset exceeds the file size, so catch-up resets to offset 0 and `seq` 0 and re-upserts; `UpsertTranscriptLines` is keyed by `{session_id, seq}` so this is idempotent.
- **Sessions that started before remote mode was configured**: `session backfill` on the next `SessionStart` walks `projectsDir` and upserts them through the remote store, as it does for MongoDB today.
- **A blob above 16 MiB**: refused with 413, logged once per path in the catch-up state so it is not retried on every hook; the daemon has the same limit today.
- **Offline for hours**: the WAL grows under `<datadir>`, and the first successful relay flushes it in order; transcript lines are not in the WAL, they are replayed from the file by offset.
- **A repository with no git origin**: the project slug is the directory name; the status review notes it so two unrelated folders named `scratch` on two machines are seen to share a namespace.
- **Token revoked while sessions run**: every relay logs 401 to `relay.log` and buffers to the WAL; `check-setup` reports it on the next session start; nothing is lost once a new token is written.
- **Server upgraded ahead of clients**: routes are under `/api/v1`; additive fields are tolerated by both sides (`json` decoders ignore unknown fields), and a breaking change gets `/api/v2` with `/health` listing supported versions.
- **Local MongoDB users**: nothing changes; `serverUrl` empty keeps the daemon, the curl relay and `mongosession`.

## Considerations

- **Security:** Transcripts, tool results and file-history blobs leave the machine, so the transport is HTTPS only (plain HTTP refused off loopback on both sides) and the token is the only credential on the client, held in `config.json` at 0600 and never logged. Tokens are 32 random bytes, stored as SHA-256 on the server, compared in constant time, revocable without restart. The server stamps the account from the token and ignores client-supplied `account_id`, so one token cannot read another's sessions; reads of enrichment internals are not exposed. Request bodies are capped (16 MiB) and headers limited; `ReadHeaderTimeout` guards slowloris. Project slugs and every path derived from requests are validated inline (`filepath.IsLocal`) before use, as the write guard does. The WebSocket keeps its origin check behind auth. The `privacy-redact` enricher runs on the server after ingest, so redaction does not prevent transmission; this is accepted (Robbie, 2026-10-12): the transport is TLS end to end and the server is the operator's own, so a client-side pass is not built and the relay stays stateless. Docker image runs as a non-root user with the token file and certificates mounted read-only.
- **Performance:** Each hook now costs one process start (bash shim plus the Go binary, tens of milliseconds) and one HTTPS connection with a fresh handshake, since no connection outlives the hook; hooks are async so Claude does not wait. Catch-up reads only the byte delta of the transcript and stats only the session's artifact files, so a hook on a long session costs the same as on a short one; lines go in batches of 500 and blobs are sent only when size or mtime changed. The server keeps the existing MongoDB indexes (`account_id`, `{session_id, seq}`) and runs enrichment where the data is. `FindSessions` and `FullSession` are the same single round trips the MongoDB adapter makes today, so the MCP tools see one HTTPS request per call plus the handshake. The per-project store cache on the server avoids reopening databases per request.

## Testing

- **Conformance (the core proof):** `sessiontest.Run` against `remotesession` over `httptest` + `sessionapi` + `memsession`, once plain and once TLS with a test CA; `storetest.Run` against `remotestore` over `httptest` + `storeapi` + `memstore`. Both suites run in CI without environment variables.
- **Unit:** `remote` (scheme rule, envelope decoding, error mapping, CA loading); `server/auth` (hashing, constant-time compare, revocation, mtime reload, 401 shape); `session/ingest` (ported from `daemon_test.go` cases on stamping, WAL fallback, git fill); `session/catchup` (offset resume, truncation reset, artifact change detection, lock skip, 500-line batches); `server/projects` (slug validation, prefix stamping per plugin); `cli` server command (flag and env precedence, refusal of plain HTTP off loopback without `--behind-proxy`).
- **Daemon regression:** `session/daemon` tests unchanged after the ingest extraction.
- **End to end in Go:** start `mind-palace server run` in-process on loopback with a token file and `memsession`/`memstore` (a `--store memory` test hook), run `hook relay` with a synthetic `PostToolUse` event and a temp `projectsDir` transcript, then assert `find_sessions`, `get_full_session` and `read_transcript` through `sessionmcp` over `remotesession`, and `plan_create` through a `Palace` whose second store is `clued`, then `mind-palace sync` after the server was stopped and restarted.
- **Plugin bash tests (`plugin/`):** `event-relay` routes to `hook relay` when `serverUrl` is set and to curl otherwise; `session-start` starts no daemon in remote mode; `check-setup` messages on 401 and on an unreachable server; skipped without `jq` as today.
- **Manual:** the walkthrough in `docs/server.md` on this cloud container (HTTPS-only egress) against the server, which is the scenario that motivated the plan; CI's `binaries` job after `scripts/build-binaries.sh`.

## Scope

**In scope:**

- `mind-palace server run` with bearer auth, TLS or `--behind-proxy`, `sessionapi`, `storeapi`, `whoami`, server-side enrichment, graceful shutdown, Docker image and compose file
- `remote` client, `remotesession` and the `clued` store driver, selected by `serverUrl` in `config.json`
- `hook relay` with per-hook transcript and artifact catch-up, WAL flush and lock, replacing the daemon in remote mode; hooks updated for both modes
- Project namespacing for plans and stories on the server, per-repository override, `sync` across file and server
- Project-scoped service API, WebSocket and MCP Streamable HTTP on the server behind auth, documented for `claude mcp add --transport http`
- `/clued-setup` and `/clued-uninstall` remote paths, `docs/server.md`, README and CLAUDE.md layout updates, `dist/` rebuild

**Out of scope:**

- A SQL or SQLite session store so the server can run without MongoDB — Server-side storage is the server's own business and `mongosession` exists; a single-binary server with SQLite is a follow-up plan once the protocol is in place.
- ACME certificate issuance inside the server — Certificate files and `--behind-proxy` cover the deployments at hand without a new dependency.
- Multi-user administration (web UI, OAuth, self-service tokens) — One operator mints tokens from the CLI; per-project token scopes are in scope (see the auth decision), the rest is not.
- Client-side redaction before upload — Decided against: redaction stays a server-side enricher, the transport is TLS and the server is the operator's own.
- Real-time push from server to client (SSE or WebSocket subscriptions on sessions) — Clients are hook-driven and short-lived; nothing on the client listens.
- Removing the local-MongoDB mode and the daemon — They keep working unchanged and remain the zero-server option for a single machine.
- Rate limiting and quotas — Single-operator service behind a reverse proxy where limits belong if needed.

## Open Questions

- [x] Should a token be restricted to a list of projects, or is per-account scoping enough for the first release? — Robbie, 2026-10-12: account plus project list. Folded into the auth decision, `server/auth`, `storeapi` and sections 1.2, 3.1 and 4.2.
- [x] Should `hook relay` run the `privacy-redact` enricher on the client before upload? — Robbie, 2026-10-12: server-side only. Recorded under Considerations and Out of scope.
- [x] Is `X-Clued-Project` the right way to select the project on the HTTPS `/mcp` endpoint? — Robbie, 2026-10-12: yes, one registration per server with the header; `docs/server.md` documents it as the pattern.
- [x] Should the Docker image be published by `release.yml` on `v*` tags? — Robbie, 2026-10-12: publish to GHCR as `ghcr.io/robbiebyrd/clued-server:<tag>` with the workflow's `GITHUB_TOKEN`. Folded into section 4.3.

---

# Part 2 — Implementation

**Architecture:** Hexagonal, as the rest of mind-palace: the ports `session.Store` and `store.Store` are unchanged; the server adds two HTTP transports over them (`server/sessionapi`, `server/storeapi`) and the client adds two adapters under them (`session/remotesession`, `store/remotestore`) sharing one HTTP client (`remote`). The daemon's bookkeeping moves into `session/ingest` and the tailers' one-shot form into `session/catchup`, both consumed by the new `hook relay` entrypoint. `cli/server.go` composes the server; the existing `server` package gains a per-request project resolver.

**Tech Stack:** Go 1.26 stdlib `net/http` (method patterns, `ListenAndServeTLS`, `httptest`), `crypto/sha256` and `crypto/subtle`, `log/slog`; existing gorilla/websocket, modelcontextprotocol/go-sdk, mongo-driver v2, cobra; bash hooks with `jq` and `curl`; Docker for the server image. No new Go dependencies.

## Constraints

- Keep `gofmt` clean and `go vet` silent; `go test -race ./...` passes without environment variables; the Mongo suites pass with `MIND_PALACE_TEST_MONGODB_URI`
- `config.json` keeps every existing key; new keys are additive and all have `CLUED_*` overrides
- Business rules live in `session/`, `store/`, `service/`, `server/auth`; `cli/` and the hooks only translate
- Paths from requests (project slugs, transcript paths) are validated inline with `filepath.IsLocal` / `filepath.Clean` where they are used
- Hooks the binary serves stay `${CLAUDE_PLUGIN_ROOT}/scripts/clued hook …` and print bare hook JSON
- Rebuild `dist/` once at the end of the change set with `scripts/build-binaries.sh` and commit it in the same change; CI's `binaries` job checks it
- Local-MongoDB mode and the daemon keep their current behaviour and tests

## Files

| File | Change | Responsibility |
|---|---|---|
| `mind-palace/remote/client.go` | create | HTTP client, scheme rule, envelope decoding, error mapping, `FromConfig` |
| `mind-palace/server/auth/auth.go` | create | Token file, middleware, principal, `whoami` |
| `mind-palace/server/sessionapi/sessionapi.go` | create | `session.Store` over HTTP |
| `mind-palace/session/remotesession/remotesession.go` | create | `session.Store` client adapter |
| `mind-palace/session/ingest/ingest.go` | create | Event bookkeeping shared by daemon and relay |
| `mind-palace/session/catchup/catchup.go` | create | Per-hook transcript and artifact delta with state and lock |
| `mind-palace/session/daemon/daemon.go` | modify | Delegate to `ingest`; tailers call the shared mirroring functions |
| `mind-palace/session/tail/artifacts.go` | modify | Expose one-shot mirroring used by `WatchArtifacts` and `catchup` |
| `mind-palace/session/config.go` | modify | `serverUrl`, `serverToken`, `serverCaFile`, `serverInsecure`, `Remote()` |
| `mind-palace/server/storeapi/storeapi.go` | create | `store.Store` over HTTP per project |
| `mind-palace/server/projects/projects.go` | create | Per-project store opening and cache |
| `mind-palace/store/remotestore/remotestore.go` | create | Driver `clued` |
| `mind-palace/config/config.go` | modify | `AllowNoFileStore`, `remote.project` key |
| `mind-palace/server/server.go` | modify | Per-request palace resolver, auth wrapping |
| `mind-palace/cli/server.go` | create | `server run`, `server token …` |
| `mind-palace/cli/hook.go` | modify | `hook relay` |
| `mind-palace/cli/session.go` | modify | Remote store selection, `session whoami` |
| `mind-palace/collect/snapshot/snapshot.go` | modify | Remote path for `ContextCollected` |
| `hooks/event-relay` | modify | Route to `hook relay` when `serverUrl` is set |
| `hooks/session-start` | modify | No daemon in remote mode; backfill and catch-up |
| `hooks/check-setup` | modify | `whoami` probe and message |
| `commands/clued-setup.md` | modify | Remote setup path |
| `commands/clued-uninstall.md` | modify | Remove catch-up state |
| `Dockerfile.server` | create | Server image |
| `docker-compose.server.yml` | create | Server plus MongoDB |
| `docs/server.md` | create | Running the server, tokens, TLS, MCP over HTTPS |
| `mind-palace/cmd/mind-palace/main.go` | modify | Blank-import `store/remotestore` |
| `dist/` | modify | Rebuilt binaries |

## Phase 1: Remote transport and the session store over HTTPS

### 1.1: `remote` client and config fields

**Files:**
- Create: `mind-palace/remote/client.go`
- Create: `mind-palace/remote/client_test.go`
- Modify: `mind-palace/session/config.go`
- Modify: `mind-palace/session/config_test.go`

**Depends on:** none

**Interfaces:**
- Produces: `remote.Client`, `remote.FromConfig`, `session.Config.Remote()`, sentinels `ErrInsecureURL`, `ErrUnauthorized`, `ErrForbidden`, `ErrTooLarge`

- [ ] **Step 1: Write failing tests** — scheme rule (https ok, http loopback ok, http remote refused, `serverInsecure` allows), envelope decode, error mapping per status, CA file loading, config fields and `CLUED_SERVER_*` overrides
- [ ] **Step 2: Run them and confirm they fail** — `cd mind-palace && go test ./remote/ ./session/`
- [ ] **Step 3: Implement `remote.Client` and the config fields** — `Do` with JSON in and out, bearer header, `User-Agent`, per-call timeout, TLS config from `serverCaFile`
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./remote/ ./session/`
- [ ] **Step 5: Commit** — `git commit -am "Add the remote HTTP client and server config fields"`

**Validation:** `cd mind-palace && go test -race ./remote/ ./session/`

### 1.2: Bearer tokens and the `whoami` route

**Files:**
- Create: `mind-palace/server/auth/auth.go`
- Create: `mind-palace/server/auth/auth_test.go`

**Depends on:** 1.1

**Interfaces:**
- Produces: `auth.Middleware`, `auth.Principal`, `auth.FromContext`, `auth.TokenFile` (`Create`, `List`, `Revoke`, `Lookup`), `auth.WhoamiHandler`

- [ ] **Step 1: Write failing tests** — token file round trip at 0600, SHA-256 hash, constant-time lookup, revoked token → 401, mtime reload, missing or malformed header → 401 with `WWW-Authenticate`, `whoami` body with projects, `AllowProject` for a listed slug, an unlisted slug and `*`, `token grant` adds a project
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./server/auth/`
- [ ] **Step 3: Implement the token file and middleware**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./server/auth/`
- [ ] **Step 5: Commit** — `git commit -am "Add bearer-token auth for the clued server"`

**Validation:** `cd mind-palace && go test -race ./server/auth/`

### 1.3: `sessionapi`: the session store as HTTP routes

**Files:**
- Create: `mind-palace/server/sessionapi/sessionapi.go`
- Create: `mind-palace/server/sessionapi/sessionapi_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `session.Store`, `auth.Principal`
- Produces: `sessionapi.Handler(store, Limits) http.Handler` with the route table from the design

- [ ] **Step 1: Write failing tests** — each route against `memsession`: account stamping on writes, account scoping on reads, 404 mapping of `ErrNotFound`, 413 above the body limit, 400 on malformed JSON, 500-line cap on transcript batches
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./server/sessionapi/`
- [ ] **Step 3: Implement the handler using the `httpapi` envelope helpers** — extract the envelope writer from `server/httpapi` into a small shared file if it is not already exported
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./server/sessionapi/`
- [ ] **Step 5: Commit** — `git commit -am "Serve the session store over HTTP"`

**Validation:** `cd mind-palace && go test -race ./server/sessionapi/`

### 1.4: `remotesession` adapter and conformance

**Files:**
- Create: `mind-palace/session/remotesession/remotesession.go`
- Create: `mind-palace/session/remotesession/remotesession_test.go`
- Modify: `mind-palace/session/store.go`
- Modify: `mind-palace/cli/session.go`
- Modify: `mind-palace/cli/session_test.go`

**Depends on:** 1.3

**Interfaces:**
- Produces: `remotesession.New(*remote.Client) session.Store`, `session.ErrUnsupported`; `openSessionStore` chooses it when `cfg.Remote()`

- [ ] **Step 1: Write the conformance harness** — `sessiontest.Run` over `httptest.NewServer(auth.Middleware(sessionapi.Handler(memsession.New())))` with a fixed token, and again over `httptest.NewTLSServer` with the test certificate written to a temp `serverCaFile`; a CLI test that `session find` with `CLUED_SERVER_URL` set uses the remote store
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./session/remotesession/ ./cli/`
- [ ] **Step 3: Implement the adapter and the CLI selection**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./session/... ./cli/`
- [ ] **Step 5: Commit** — `git commit -am "Add the remote session store adapter"`

**Validation:** `cd mind-palace && go test -race ./session/... ./cli/`

## Phase 2: Client ingest without a daemon

### 2.1: Extract `session/ingest` from the daemon

**Files:**
- Create: `mind-palace/session/ingest/ingest.go`
- Create: `mind-palace/session/ingest/ingest_test.go`
- Modify: `mind-palace/session/daemon/daemon.go`
- Test: `mind-palace/session/daemon/daemon_test.go`

**Depends on:** 1.1

**Interfaces:**
- Produces: `ingest.Service{Store, AccountID, Host, Git, Logger, WALPath}` with `Handle(ctx, event map[string]any) (Tracked, error)` and `FlushWAL(ctx)`

- [ ] **Step 1: Port the daemon's bookkeeping tests to `ingest_test.go`** — first-sight upsert, git fill on later events, touch, stamping, WAL fallback and flush order
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./session/ingest/`
- [ ] **Step 3: Move the code and make the daemon delegate** — the daemon keeps tailing, the loops and `/event`; `daemon_test.go` must pass unchanged
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./session/ingest/ ./session/daemon/`
- [ ] **Step 5: Commit** — `git commit -am "Extract hook-event ingest from the session daemon"`

**Validation:** `cd mind-palace && go test -race ./session/ingest/ ./session/daemon/`

### 2.2: `session/catchup`: per-hook transcript and artifact delta

**Files:**
- Create: `mind-palace/session/catchup/catchup.go`
- Create: `mind-palace/session/catchup/state.go`
- Create: `mind-palace/session/catchup/catchup_test.go`
- Modify: `mind-palace/session/tail/artifacts.go`

**Depends on:** 2.1

**Interfaces:**
- Consumes: `session.Store`, the transcript line parser and artifact mirroring in `session/tail`
- Produces: `catchup.Run(ctx, store, Paths, StateDir) (Report, error)`; `tail.MirrorArtifacts(ctx, store, sid, dirs, seen) (seen, error)` shared with `WatchArtifacts`

- [ ] **Step 1: Write failing tests** — resume from offset with correct `seq`, truncation resets to 0, batches of 500, changed subagent file and file-history blob are upserted once, unchanged ones are skipped, lock held → skip without error, stale lock reclaimed, state written atomically, transcript path must be under `projectsDir`
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./session/catchup/`
- [ ] **Step 3: Lift the one-shot mirroring out of `WatchArtifacts` and implement catch-up**
- [ ] **Step 4: Confirm green including the tail tests** — `cd mind-palace && go test -race ./session/catchup/ ./session/tail/`
- [ ] **Step 5: Commit** — `git commit -am "Add per-hook transcript and artifact catch-up"`

**Validation:** `cd mind-palace && go test -race ./session/catchup/ ./session/tail/`

### 2.3: `hook relay` and the remote snapshot path

**Files:**
- Modify: `mind-palace/cli/hook.go`
- Modify: `mind-palace/cli/hook_test.go`
- Modify: `mind-palace/collect/snapshot/snapshot.go`
- Modify: `mind-palace/collect/prime/prime.go`
- Modify: `mind-palace/cli/session.go`

**Depends on:** 1.4, 2.2

**Interfaces:**
- Produces: `mind-palace hook relay` (stdin event → ingest → catch-up → WAL flush, 5 s budget, exit 0); `mind-palace session whoami`; `snapshot.Post` and `prime.DaemonHealthy` take a store in remote mode

- [ ] **Step 1: Write failing tests** — relay against an `httptest` server over `sessionapi` with a temp `projectsDir`: event stored with account from the token, transcript lines pushed, server down → WAL file written and exit 0, budget respected; `prime_context` with `no_snapshot` unset inserts `ContextCollected` through the store
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./cli/ ./collect/...`
- [ ] **Step 3: Implement the command and the remote snapshot path**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./cli/ ./collect/...`
- [ ] **Step 5: Commit** — `git commit -am "Relay hook events to the clued server without a daemon"`

**Validation:** `cd mind-palace && go test -race ./cli/ ./collect/...`

### 2.4: Hooks for remote mode

**Files:**
- Modify: `hooks/event-relay`
- Modify: `hooks/session-start`
- Modify: `hooks/check-setup`
- Modify: `mind-palace/plugin/event_relay_test.go`
- Modify: `mind-palace/plugin/session_start_test.go`
- Modify: `mind-palace/plugin/check_setup_test.go`

**Depends on:** 2.3

**Interfaces:**
- Consumes: `jq .serverUrl` from `config.json`, `CLUED_SERVER_URL`

- [ ] **Step 1: Write failing plugin tests** — with `serverUrl` set: `event-relay` execs `scripts/clued hook relay` and never curls loopback; `session-start` starts no daemon and runs `session backfill`; `check-setup` prints a `systemMessage` on 401 and on connection refused; without it: current behaviour
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./plugin/`
- [ ] **Step 3: Change the three hooks**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./plugin/`
- [ ] **Step 5: Commit** — `git commit -am "Route the plugin hooks to the clued server in remote mode"`

**Validation:** `cd mind-palace && go test -race ./plugin/`

## Phase 3: Plans and stories to the server

### 3.1: `storeapi` and per-project stores on the server

**Files:**
- Create: `mind-palace/server/storeapi/storeapi.go`
- Create: `mind-palace/server/storeapi/storeapi_test.go`
- Create: `mind-palace/server/projects/projects.go`
- Create: `mind-palace/server/projects/projects_test.go`
- Modify: `mind-palace/config/config.go`
- Modify: `mind-palace/config/config_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `store.Store`, `config.StorageDef`, `store.Open`
- Produces: `storeapi.Handler(open)`, `projects.Opener`, `projects.Slug(origin)`, `config.Validate(cfg, config.AllowNoFileStore)`

- [ ] **Step 1: Write failing tests** — slug validation (reject `..`, `/`, empty), prefix stamping for file, sql and mongo definitions, cache returns the same store, `storeapi` routes against `memstore` incl. 404 and templates, 403 for a project outside the token's list, `Validate` with no file store allowed only under the option
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./server/storeapi/ ./server/projects/ ./config/`
- [ ] **Step 3: Implement**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./server/storeapi/ ./server/projects/ ./config/`
- [ ] **Step 5: Commit** — `git commit -am "Serve per-project plan and story stores over HTTP"`

**Validation:** `cd mind-palace && go test -race ./server/storeapi/ ./server/projects/ ./config/`

### 3.2: The `clued` store driver and conformance

**Files:**
- Create: `mind-palace/store/remotestore/remotestore.go`
- Create: `mind-palace/store/remotestore/remotestore_test.go`
- Modify: `mind-palace/cmd/mind-palace/main.go`
- Modify: `mind-palace/cmd/plan/main.go`
- Modify: `mind-palace/cmd/story/main.go`

**Depends on:** 3.1, 1.1

**Interfaces:**
- Produces: driver `clued` registered via `store.Register`

- [ ] **Step 1: Write the conformance harness** — `storetest.Run` over `httptest.NewServer(auth.Middleware(storeapi.Handler(...memstore per project...)))`; `Driver()` returns `clued`, `Name()` the definition name; `ErrNotFound` mapping
- [ ] **Step 2: Confirm it fails** — `cd mind-palace && go test ./store/remotestore/`
- [ ] **Step 3: Implement and register the driver**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./store/...`
- [ ] **Step 5: Commit** — `git commit -am "Add the clued remote store driver"`

**Validation:** `cd mind-palace && go test -race ./store/...`

### 3.3: Client fan-out to the server

**Files:**
- Modify: `mind-palace/cli/cli.go`
- Modify: `mind-palace/cli/palace.go`
- Modify: `mind-palace/collect/sources/docsource/docsource.go`
- Modify: `mind-palace/config/config.go`
- Modify: `mind-palace/service/review.go`
- Modify: `mind-palace.config.example.yaml`
- Test: `mind-palace/cli/cli_test.go`
- Test: `mind-palace/collect/sources/docsource/docsource_test.go`

**Depends on:** 3.2, 1.1

**Interfaces:**
- Produces: one `openPalace(ctx, cwd)` helper used by the CLI, `mcp`, `docsource` and the hook guards that appends `StorageDef{Name: "clued-server", Kind: "clued"}` when `cfg.Remote()`; `remote.project` in `mind-palace.config.yaml`; `getConfig` and `stores` list it

- [ ] **Step 1: Write failing tests** — with `CLUED_SERVER_URL` set the palace has two stores, file first; project slug from a fake origin and from the override; `plan_create` writes the file and the server; server down → `FanoutError` warning names `clued-server`; `sync --from file --to clued-server` repairs
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./cli/ ./collect/... ./config/`
- [ ] **Step 3: Implement the shared palace opener and the definition injection**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./...`
- [ ] **Step 5: Commit** — `git commit -am "Fan plan and story writes out to the clued server"`

**Validation:** `cd mind-palace && go test -race ./...`

## Phase 4: The server binary

### 4.1: `mind-palace server run` and `server token`

**Files:**
- Create: `mind-palace/cli/server.go`
- Create: `mind-palace/cli/server_test.go`
- Modify: `mind-palace/server/server.go`
- Modify: `mind-palace/cli/cli.go`

**Depends on:** 1.3, 3.1

**Interfaces:**
- Produces: `server run --addr --tls-cert --tls-key --behind-proxy --tokens --config --session-config` (env `CLUED_SERVER_*`), `server token create|list|revoke`; `/health` with `version` and `auth`; `enrich.Loop` on the server

- [ ] **Step 1: Write failing tests** — plain HTTP on a non-loopback address refused without `--behind-proxy`; TLS listener answers `/health` and 401 on `/api/v1/whoami` without a token; env and flag precedence; SIGTERM drains in-flight requests; `token create` prints once and the file holds only the hash; a `--store memory` test hook for in-process end-to-end tests
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./cli/`
- [ ] **Step 3: Implement the command group and the composition in `server.go`**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./cli/ ./server/...`
- [ ] **Step 5: Commit** — `git commit -am "Add the clued server command"`

**Validation:** `cd mind-palace && go test -race ./cli/ ./server/...`

### 4.2: Project-scoped service API, WebSocket and MCP over HTTPS

**Files:**
- Modify: `mind-palace/server/server.go`
- Modify: `mind-palace/server/httpapi/httpapi.go`
- Modify: `mind-palace/server/ws/ws.go`
- Modify: `mind-palace/server/mcpserver/mcpserver.go`
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`
- Test: `mind-palace/server/server_test.go`
- Test: `mind-palace/server/mcpserver/mcpserver_test.go`

**Depends on:** 4.1, 3.3

**Interfaces:**
- Produces: `server.Options.Resolve func(*http.Request) (*service.Palace, error)` (project from `X-Clued-Project` or `?project=`); `/mcp` serves plan, story and the disk-free session tools under auth; `sessionmcp.Tools.Subset(remote bool)`

- [ ] **Step 1: Write failing tests** — two projects see different plans through `/api/v1/plans`, `/ws` and `/mcp`; missing project → 400; a project outside the token's list → 403 on all three; MCP over HTTPS lists no `restore_session` or `prime_context`; `mind-palace serve` (local) still works with a fixed palace
- [ ] **Step 2: Confirm they fail** — `cd mind-palace && go test ./server/... ./session/sessionmcp/`
- [ ] **Step 3: Implement the resolver and the subset**
- [ ] **Step 4: Confirm green** — `cd mind-palace && go test -race ./server/... ./session/sessionmcp/`
- [ ] **Step 5: Commit** — `git commit -am "Serve the project-scoped API, WebSocket and MCP on the clued server"`

**Validation:** `cd mind-palace && go test -race ./server/... ./session/sessionmcp/`

### 4.3: End-to-end test and packaging

**Files:**
- Create: `mind-palace/cli/remote_e2e_test.go`
- Create: `Dockerfile.server`
- Create: `docker-compose.server.yml`
- Create: `docs/server.md`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/release.yml`

**Depends on:** 4.2, 2.4

- [ ] **Step 1: Write the end-to-end test** — in-process `server run --store memory` with a token file; `hook relay` with a synthetic event and a temp transcript; assert through `sessionmcp` over `remotesession` and `plan_create` through the two-store palace; stop and restart the server, run `sync`
- [ ] **Step 2: Confirm it fails, then passes** — `cd mind-palace && go test -race -run Remote ./cli/`
- [ ] **Step 3: Add the Docker image and compose file** — multi-stage build from `mind-palace/`, non-root user, `tokens.json` and certificates as read-only mounts, `mongo:8` service, `CLUED_SERVER_*` env; CI builds the image without pushing, and `.github/workflows/release.yml` pushes `ghcr.io/robbiebyrd/clued-server:<tag>` and `:latest` on `v*` tags with the workflow's `GITHUB_TOKEN` (`packages: write` permission), so `docker-compose.server.yml` pulls the published image by default
- [ ] **Step 4: Write `docs/server.md`** — binary and Docker setups, token management, TLS and reverse-proxy, client config, `claude mcp add --transport http`, `sync` after outages
- [ ] **Step 5: Commit** — `git commit -am "Package and document the clued server"`

**Validation:** `cd mind-palace && go test -race ./... && docker build -f Dockerfile.server -t clued-server .`

## Phase 5: Setup, documentation and binaries

### 5.1: Setup command, uninstall and repository docs

**Files:**
- Modify: `commands/clued-setup.md`
- Modify: `commands/clued-uninstall.md`
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `mind-palace/README.md`
- Modify: `hooks/uninstall`

**Depends on:** 2.4, 4.3

- [ ] **Step 1: Update `/clued-setup` with the server path** — ask local MongoDB or clued server; write `config.json` at 0600; run `scripts/clued session whoami`; register hooks; start nothing
- [ ] **Step 2: Update `/clued-uninstall` and `hooks/uninstall`** — remove the catch-up state directory and `relay.log`
- [ ] **Step 3: Update README, CLAUDE.md layout table and the mind-palace package map**
- [ ] **Step 4: Commit** — `git commit -am "Document and set up remote mode"`

**Validation:** `cd mind-palace && go test -race ./plugin/`

### 5.2: Rebuild the bundled binaries

**Files:**
- Modify: `dist/`

**Depends on:** 5.1

- [ ] **Step 1: Run the full check** — `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./...`
- [ ] **Step 2: Rebuild and verify** — `scripts/build-binaries.sh && scripts/build-binaries.sh --check`
- [ ] **Step 3: Commit `dist/` in the same change set** — `git add dist && git commit -m "Rebuild bundled binaries for the clued server"`

**Validation:** `scripts/build-binaries.sh --check`

## Acceptance Criteria

- [ ] A client with only `serverUrl` and `serverToken` in `config.json`, no MongoDB and no daemon, runs a Claude Code session whose hook events, transcript lines, subagent lines and blobs appear on the server, and `find_sessions`, `get_full_session` and `read_transcript` return them on that client
- [ ] Plans and stories created with the MCP tools on such a client are written under `docs/plans` and `docs/stories` and on the server under the repository's project; `mind-palace sync --from file --to clued-server` reconciles after a server outage
- [ ] The server refuses plain HTTP off loopback without `--behind-proxy`, refuses requests without a valid token with 401, stamps the token's account on every write and scopes every read by it, and keeps projects apart
- [ ] `sessiontest.Run` passes against `remotesession` and `storetest.Run` against the `clued` driver, both without environment variables, and once over TLS
- [ ] The HTTPS `/mcp` endpoint, registered with `claude mcp add --transport http`, serves the plan, story and disk-free session tools for the selected project
- [ ] Local-MongoDB mode, the daemon and every existing test behave as before; `go test -race ./...` is green with and without `MIND_PALACE_TEST_MONGODB_URI`
- [ ] `scripts/build-binaries.sh --check` passes on the committed `dist/`, and `docs/server.md` walks through the cloud-container scenario end to end

## Self-Review

| Requirement | Delivered by |
|---|---|
| Server application on an external host over HTTPS | 4.1, 4.3 |
| Server takes over storage and persistence of sessions | 1.3, 1.4, 2.3 |
| No MCP server daemon, database server or other daemon on the client | 2.1 to 2.4 (relay replaces the daemon), 4.2 (`/mcp` over HTTPS) |
| Client keeps local plans and stories and also sends them to the server | 3.1 to 3.3 |
| Authentication and isolation | 1.2, 1.3, 3.1 |
| Setup and documentation | 4.3, 5.1 |

**Placeholder scan:** Every section names its files, tests, commands and the interface it produces; no step says "TBD" or defers a decision, and the open questions are listed with an owner.
**Residual risk:** The per-hook catch-up relies on hooks firing; a session that ends by killing Claude Code leaves lines unpushed until the next session start's backfill, which walks `projectsDir` and catches them. The MCP-over-HTTPS project selection via header is untested against future Claude Code changes to `claude mcp add --header`.

## Next Steps

1. Create one story per section with `story_create` and link it with `story_addPlanLink`, starting with 1.1 to 1.4
2. Set the plan to `validated` then `ready` with `plan_setStatus` and start phase 1 on a branch from `main`
