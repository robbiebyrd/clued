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
| `scripts/clued` | Shim that locates `mind-palace` (dev checkout via `devRepoPath`, then the bundled binary, then PATH, then `~/.claude/plugins/data/clued/bin`); `--locate` prints the choice |
| `dist/` | Pre-built `mind-palace_<os>_<arch>[.exe].gz` for 7 platforms plus `SHA256SUMS`; produced by `scripts/build-binaries.sh`, unpacked by the shim on first use |
| `scripts/build-binaries.sh` | Reproducible cross-compile of the bundled binaries (`--check` verifies them, `--raw DIR` builds release assets) |
| `.mcp.json` | The two stdio MCP servers the plugin registers: `clued` (session tools) and `mind-palace` (plan and story tools) |
| `commands/`, `skills/` | `/clued-setup`, `/clued-uninstall`, and the `plan-storage` skill |
| `mind-palace/` | The Go module (`github.com/robbiebyrd/clued/mind-palace`); see its README for the package map |
| `mind-palace/session/` | Session capture: config, Mongo and in-memory stores, daemon, tailing, backfill, enrichers, queries, restore, `sessionmcp` |
| `mind-palace/memory/` | Reads a claude-remember memory store into hashed records and posts them to the daemon as `MemorySaved` events (`session memory-ingest`); searched with `search_memory` |
| `mind-palace/hookguard/` | Rules behind the hooks the binary serves (`mind-palace hook stop-guard`, `hook write-guard`); the status review (`hook status-review`, `review`) lives in `service/review.go` |
| `mind-palace/plugin/` | Go tests that execute the bash hooks and the shim |
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
  and `cmd/story` are thin wrappers around the same CLI.
- The binaries under `dist/` must match the Go source: after changing
  anything the binary compiles in, run `scripts/build-binaries.sh` from the
  repository root and commit `dist/` in the same change. CI's `binaries`
  job runs `scripts/build-binaries.sh --check` and fails on a mismatch. Each
  rebuild adds about 100 MB to the repository, so rebuild once per change
  set, not per commit.

## Conventions

- Keep `gofmt` clean and `go vet` silent; CI fails on either.
- Business rules live in `service/` (plans and stories), `session/`
  (capture) and `hookguard/` (hook decisions); entrypoints (`cli/`,
  `server/`, `sessionmcp`) only translate. Hooks the binary serves are
  `mind-palace hook …` subcommands registered in `hooks.json` and
  `hooks/register-hooks` as `${CLAUDE_PLUGIN_ROOT}/scripts/clued hook …`; they
  print bare hook JSON, not the `{"ok": …}` envelope.
  Store plugins persist what they are given and must pass `store/storetest`.
- Document kinds (plan, story) are described in `kind/`; a behaviour that
  differs between kinds belongs there as a flag, not as a type switch
  elsewhere.
- The config file read by the plugin (`~/.claude/plugins/data/clued/config.json`,
  `CLUED_*` env overrides) is per machine and must keep its existing keys;
  `mind-palace.config.yaml` is per repository.
- Paths that come from hook requests are validated inline with
  `filepath.IsLocal` / `filepath.Clean` + prefix checks (CodeQL needs to see
  the guard at the point of use).
- Do not write under `docs/superpowers/plans/`, and do not write or edit the
  files under `docs/plans/` and `docs/stories/` by hand: the `write-guard`
  hook denies it. Plans for work in this repo are created with the `plan_*`
  MCP tools (or `mind-palace plan create`), stories with `story_*`, and both
  change through the setters (`plan_setStatus`, `story_setCriterion`, …) or
  `plan_update` / `story_update` for the content.
- Releases: push a `v*` tag; `.github/workflows/release.yml` runs
  `scripts/build-binaries.sh --raw` and uploads the `mind-palace_<os>_<arch>`
  assets and `SHA256SUMS` that `/clued-setup` falls back to on platforms
  without a bundled binary.
