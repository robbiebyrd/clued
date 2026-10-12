# Working in this repository

`clued` is a Claude Code plugin (bash hooks, a shim and two slash commands at
the repository root) plus one Go module, `mind-palace/`, that holds all the
logic: the session-capture daemon, the session MCP tools, and the plan and
story service. There is no Node code left; `scripts/clued` runs the `mind-palace`
binary.

## Layout

| Path | What it is |
|---|---|
| `hooks.json`, `hooks/` | Hook registrations and the bash hooks (`event-relay`, `session-start`, `check-setup`, `register-hooks`, `install-remember-listener`, `uninstall`) |
| `scripts/clued` | Shim that locates `mind-palace` (dev checkout via `devRepoPath`, then the release pinned by `.claude-plugin/plugin.json`'s `version`, downloaded once into `~/.claude/plugins/data/clued/bin/releases/v<version>/`, then PATH, then `~/.claude/plugins/data/clued/bin/mind-palace`); `--locate` prints the choice |
| `.claude-plugin/plugin.json` | The plugin manifest; its `version` names the GitHub release (`v<version>`) whose binaries the shim downloads |
| `scripts/build-binaries.sh` | Reproducible cross-compile of `mind-palace` for 7 platforms plus `SHA256SUMS` into a directory (`dist/` by default, git-ignored); run by CI and by the release workflow, never committed |
| `.mcp.json` | The two stdio MCP servers the plugin registers: `clued` (session tools) and `mind-palace` (plan and story tools) |
| `commands/`, `skills/` | `/clued-setup`, `/clued-uninstall`, and the `plan-storage` skill |
| `mind-palace/` | The Go module (`github.com/robbiebyrd/clued/mind-palace`); see its README for the package map |
| `mind-palace/session/` | Session capture: config (including the remote-mode keys), Mongo and in-memory stores, daemon, tailing, backfill, enrichers, queries, restore, `sessionmcp` |
| `mind-palace/memory/` | Reads a claude-remember memory store into hashed records and posts them to the daemon as `MemorySaved` events (`session memory-ingest`); searched with `search_memory` |
| `mind-palace/session/ingest/` | Hook-event bookkeeping shared by the daemon and `hook relay`: session tracking, stamping, store write, WAL fallback |
| `mind-palace/session/catchup/` | What `hook relay` runs instead of a resident tailer: pushes one session's transcript delta and changed artifact files, state and lock files under `<config dir>/catchup/` |
| `mind-palace/session/remotesession/` | `session.Store` over a clued server (one HTTP call per port method to `server/sessionapi`) |
| `mind-palace/remote/` | The HTTP client every remote adapter shares: base URL, bearer token, CA file, the rule that refuses plain `http://` off loopback, envelope decoding into typed errors |
| `mind-palace/clientcfg/` | Opens the plan and story services the way the client does: the repository's config plus, in remote mode, the `clued-server` store appended as a secondary store; `remote.project` overrides the project slug |
| `mind-palace/server/auth/` | Bearer-token authentication for the clued server: hashed token file (`server token create|list|revoke|grant`), constant-time compare, mtime reload, principal on the context |
| `mind-palace/server/sessionapi/` | The `session.Store` port over HTTP under `/api/v1/session`, scoped to the token's account |
| `mind-palace/server/storeapi/` | The `store.Store` port over HTTP under `/api/v1/projects/{project}/store/` |
| `mind-palace/server/projects/` | Project slugs (`github.com__owner__repo`) and one store per project built from the server's storage definitions |
| `mind-palace/store/remotestore/` | Storage plugin `clued`: plans and stories on a clued server through `remote` and `server/storeapi` |
| `mind-palace/hookguard/` | Rules behind the hooks the binary serves (`mind-palace hook stop-guard`, `hook write-guard`); the status review (`hook status-review`, `review`) lives in `service/review.go`; `hook relay` (remote mode) lives in `cli/hook_relay.go` over `session/ingest` and `session/catchup` |
| `mind-palace/plugin/` | Go tests that execute the bash hooks and the shim, in local and remote mode (the remote tests start a clued server in the test) |
| `Dockerfile.server`, `docker-compose.server.yml` | The clued server image and a compose file with a `mongo:8` service |
| `docs/server.md` | Running the clued server (binary or Docker), tokens, TLS and reverse proxies, the HTTPS `/mcp` endpoint |
| `docs/plans/`, `docs/stories/` | This repository's own plans and stories, managed by `mind-palace` |
| `docs/superpowers/specs/` | Design documents (brainstorm output); linked from plans with `plan_addSpec` |
| `docs/superpowers/plans/` | Historical plans from the retired TypeScript implementation; writes here are blocked by the write guard |

## Build and test

Everything runs from `mind-palace/` with Go 1.26 (the toolchain downloads
itself; `mise.toml` pins it too):

```bash
cd mind-palace
test -z "$(gofmt -l .)" && go vet ./... && go build ./...
go test -race ./...
MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./...   # also the MongoDB suites
```

Use port `27017` for a local `mongod` (what CI's service container uses) or
`27018` for the MongoDB that `docker-compose.transcripts.yml` starts.

- CI runs exactly those commands with a MongoDB service container, so run
  them before pushing. Without the env var the Mongo-backed tests skip and
  everything runs against the in-memory stores.
- Hook or shim changes are tested by `go test ./plugin/`; those tests run the
  real bash scripts and skip when `jq` is not installed.
- Other store plugins (Postgres, MySQL, ClickHouse, Mongo, Firestore) run
  their conformance suite only when their `MIND_PALACE_TEST_*` DSN is set.
  The WebDAV store runs it against an in-process `golang.org/x/net/webdav`
  server always, and against a real server when `MIND_PALACE_TEST_WEBDAV_URL`
  is set.
- Build the binary with `go build -o mind-palace ./cmd/mind-palace`; `cmd/plan`
  and `cmd/story` are thin wrappers around the same CLI. The same binary is
  the clued server: `mind-palace server run` (flags fall back to
  `CLUED_SERVER_ADDR`, `CLUED_SERVER_TLS_CERT`, `CLUED_SERVER_TLS_KEY`,
  `CLUED_SERVER_BEHIND_PROXY`, `CLUED_SERVER_TOKENS`; `--store memory` for
  tests) and `mind-palace server token create|list|revoke|grant`.
- No binary is committed. CI's `binaries` job cross-compiles every target
  on each push (and keeps them as a workflow artifact); the binaries users
  run come from GitHub releases. A change the binary compiles in reaches
  plugin installs only through a release: bump `version` in
  `.claude-plugin/plugin.json` and, once merged, push the matching
  `v<version>` tag.

## Conventions

- Keep `gofmt` clean and `go vet` silent; CI fails on either.
- Business rules live in `service/` (plans and stories), `session/`
  (capture, with `session/ingest` and `session/catchup` for the relay) and
  `hookguard/` (hook decisions); entrypoints (`cli/`, `server/`,
  `sessionmcp`) only translate. Hooks the binary serves are `mind-palace
  hook …` subcommands (`write-guard`, `stop-guard`, `status-review`, and
  `relay`, which `hooks/event-relay` and `hooks/session-start` run in remote
  mode) registered in `hooks.json` and `hooks/register-hooks` as
  `${CLAUDE_PLUGIN_ROOT}/scripts/clued hook …`; they print bare hook JSON,
  not the `{"ok": …}` envelope, and `hook relay` prints nothing and always
  exits 0.
  Store plugins persist what they are given and must pass `store/storetest`;
  session stores pass `session/sessiontest`. The remote adapters
  (`store/remotestore`, `session/remotesession`) run those suites over
  `httptest` against `server/storeapi` and `server/sessionapi`.
- The clued server (`server/auth`, `server/sessionapi`, `server/storeapi`,
  `server/projects`, wired in `server/clued.go`) is the bare transport: it
  authenticates, stamps the token's account, namespaces by project and
  persists; it holds no plan or story rules. Clients open their services
  through `clientcfg.Open` so no write misses the server in remote mode.
- Document kinds (plan, story) are described in `kind/`; a behaviour that
  differs between kinds belongs there as a flag, not as a type switch
  elsewhere.
- The config file read by the plugin (`~/.claude/plugins/data/clued/config.json`,
  `CLUED_*` env overrides) is per machine and must keep its existing keys;
  `serverUrl` and `serverToken` (plus `serverCaFile`, `serverInsecure`) put
  it in remote mode, so `/clued-setup` writes it with mode `0600` and nothing
  may log the token. `mind-palace.config.yaml` is per repository (its
  `remote.project` key overrides the project slug on the server). The hooks
  detect the mode the same way: `serverUrl` from the file, overridden by
  `CLUED_SERVER_URL`.
- Paths that come from hook requests are validated inline with
  `filepath.IsLocal` / `filepath.Clean` + prefix checks (CodeQL needs to see
  the guard at the point of use).
- Do not write under `docs/superpowers/plans/`, and do not write or edit the
  files under `docs/plans/` and `docs/stories/` by hand: the `write-guard`
  hook denies it. Plans for work in this repo are created with the `plan_*`
  MCP tools (or `mind-palace plan create`), stories with `story_*`, and both
  change through the setters (`plan_setStatus`, `story_setCriterion`, …) or
  `plan_update` / `story_update` for the content.
- Releases: push the `v<version>` tag that matches `.claude-plugin/plugin.json`;
  `.github/workflows/release.yml` checks that, runs `scripts/build-binaries.sh`
  and uploads the `mind-palace_<os>_<arch>` assets and `SHA256SUMS` that
  `scripts/clued` downloads on first use.
