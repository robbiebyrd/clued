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
| `scripts/build-binaries.sh` | Reproducible cross-compile of `mind-palace` for 7 platforms plus `SHA256SUMS` into a directory (`dist/` by default, git-ignored); `--target os/arch` builds one (CI's per-target matrix), `--sums DIR` writes the checksums over collected binaries (the release job); never committed |
| `.mcp.json` | The two stdio MCP servers the plugin registers: `clued` (session tools) and `mind-palace` (plan and story tools) |
| `commands/`, `skills/` | `/clued-setup`, `/clued-uninstall`, and the `plan-storage` skill |
| `mind-palace/` | The Go module (`github.com/robbiebyrd/clued/mind-palace`); see its README for the package map |
| `mind-palace/session/` | Session capture: config (including the remote-mode keys), Mongo and in-memory stores, daemon, tailing, backfill, enrichers, queries, restore, `sessionmcp` |
| `mind-palace/memory/` | Reads a claude-remember memory store into hashed records and posts them to the daemon as `MemorySaved` events (`session memory-ingest`); searched with `search_memory` |
| `mind-palace/session/limits/` | Turns the JSON Claude Code pipes to its status line into `LimitsSampled` events (context fill, five-hour and seven-day limits, model, cost) and decides which samples are worth storing; the entrypoint is `mind-palace hook status`, installed as the status line by `register-hooks` when the config sets `statusLine: true` |
| `mind-palace/session/ingest/` | Hook-event bookkeeping shared by the daemon and `hook relay`: session tracking, stamping, store write, WAL fallback |
| `mind-palace/session/catchup/` | What `hook relay` runs instead of a resident tailer: pushes one session's transcript delta and changed artifact files, state and lock files under `<config dir>/catchup/` |
| `mind-palace/session/remotesession/` | `session.Store` over a clued server (one HTTP call per port method to `server/sessionapi`) |
| `mind-palace/remote/` | The HTTP client every remote adapter shares: base URL, bearer token, CA file, the rule that refuses plain `http://` off loopback, envelope decoding into typed errors |
| `mind-palace/clientcfg/` | Opens the plan and story services the way the client does: the repository's config plus, in remote mode, the `clued-server` store appended as a secondary store; `remote.project` overrides the project slug |
| `mind-palace/server/auth/` | Bearer-token authentication for the clued server: hashed token file (`server token create|list|revoke|grant`), constant-time compare, mtime reload, principal on the context |
| `mind-palace/server/device/` | The device login of the clued server (`session login` on a new machine, `server device approve` on a linked one): pending codes in memory, approval mints a token in the token file under the approver's account; the routes are in `server/device_routes.go` |
| `mind-palace/server/sessionapi/` | The `session.Store` port over HTTP under `/api/v1/session`, scoped to the token's account |
| `mind-palace/server/storeapi/` | The `store.Store` port over HTTP under `/api/v1/projects/{project}/store/` |
| `mind-palace/server/projects/` | Project slugs (`github.com__owner__repo`) and one store per project built from the server's storage definitions |
| `mind-palace/store/remotestore/` | Storage plugin `clued`: plans and stories on a clued server through `remote` and `server/storeapi` |
| `mind-palace/hookguard/` | Rules behind the hooks the binary serves (`mind-palace hook stop-guard`, `hook write-guard`); the status review (`hook status-review`, `review`) lives in `service/review.go`; `hook relay` (remote mode) lives in `cli/hook_relay.go` over `session/ingest` and `session/catchup` |
| `mind-palace/plugin/` | Go tests that execute the bash hooks and the shim, in local and remote mode (the remote tests start a clued server in the test) |
| `Dockerfile.server` | The clued server image (`ghcr.io/robbiebyrd/clued-server`, published by the release workflow); it compiles nothing and copies `mind-palace_linux_<arch>` from `BINARIES` (`dist/` by default) |
| `mind-palace/server/stack/` | What `mind-palace server init` generates under `~/.clued-server`: the Compose stack (nginx front-end, clued server, `mongo:8`), the nginx config, the storage config, a private CA and certificate, the first token |
| `PROTOCOL.md` | The protocol reference: every entrypoint (hooks, shim, daemon, MCP servers, the server's HTTP API, CLI) with its exact JSON, routes, parameters and exit codes; update it when an entrypoint changes |
| `docs/server.md` | Running the clued server (binary, or the Docker stack from `server init`), tokens, TLS, nginx and other front-ends, the HTTPS `/mcp` endpoint |
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
  tests), `mind-palace server token create|list|revoke|grant` and
  `mind-palace server init` (the Docker stack; its files are generated by
  `server/stack`, never committed, and `/clued-setup` starts it).
- No binary is committed. CI's `binaries` job cross-compiles every target
  on each pull request and each push to main (pushes to other branches
  run nothing, so a pull request is not checked twice), one matrix job per target (the matrix must list the
  script's targets; a step checks it), and keeps each as a workflow
  artifact that the `image` and `release` jobs reuse; the binaries users
  run come from GitHub releases. A change the binary compiles in reaches
  plugin installs only through a release, and every push to main is one:
  CI bumps the patch version itself (next bullet under Conventions). Bump
  `version` in `.claude-plugin/plugin.json` and `marketplace.json` by hand
  only for a new minor or major.

## Conventions

- Keep `gofmt` clean and `go vet` silent; CI fails on either.
- Business rules live in `service/` (plans and stories), `session/`
  (capture, with `session/ingest` and `session/catchup` for the relay) and
  `hookguard/` (hook decisions); entrypoints (`cli/`, `server/`,
  `sessionmcp`) only translate. Hooks the binary serves are `mind-palace
  hook …` subcommands (`write-guard`, `stop-guard`, `status-review`,
  `prime`, and `relay`, which `hooks/event-relay` and `hooks/session-start`
  run in remote mode) registered in `hooks.json` and `hooks/register-hooks` as
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
- Releases are automatic; never push a `v*` tag or edit the version for a
  patch by hand. `scripts/release-plan.sh` decides what a run releases: a
  version in `.claude-plugin/plugin.json` with no release yet (a minor or
  major bumped by hand) as it is; on a push to main whose version is
  already released, the next patch version; a release with missing assets
  is completed. The `version` job runs it first so the `binaries` matrix
  stamps that version into the binaries as `remote.Version`. The `release`
  job of `.github/workflows/mind-palace.yml` runs on every push to main
  after `test`, `binaries` and `image` passed, on the latest main: it skips
  when main's code moved past the pushed commit (the later push's run
  releases it), otherwise bumps the
  patch version in `plugin.json` and `marketplace.json` (both must agree;
  `go test ./plugin/` checks it) and commits that to main as
  `Release v<next> [skip ci]`, collects the matrix's binaries
  (`scripts/build-binaries.sh --sums`), or rebuilds them in the job when
  the plan changed since the builds (a release finished in between took
  the version they carry), assembles and pushes
  `ghcr.io/robbiebyrd/clued-server:v<version>` (and `:latest`) from the
  Linux ones and creates the release `v<version>`, tag included, with the
  `mind-palace_<os>_<arch>` assets and `SHA256SUMS` that `scripts/clued`
  downloads on first use. A manual run of the workflow completes a release
  with missing assets but bumps nothing. The job pushes to main with the
  workflow's token, so a branch rule that blocks it fails the release.
