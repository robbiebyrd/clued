# Working in this repository

`clued` is a Claude Code plugin (bash hooks, a shim and two slash commands at
the repository root) plus one Go module, `mind-palace/`, that holds all the
logic: the session-capture daemon, the session MCP tools, and the plan and
story service. There is no Node code left; `bin/clued` runs the `mind-palace`
binary.

## Layout

| Path | What it is |
|---|---|
| `hooks.json`, `hooks/` | Hook registrations and the bash hooks (`event-relay`, `session-start`, `check-setup`, `guard-superpowers-plans`, `register-hooks`, `uninstall`) |
| `bin/clued` | Shim that locates `mind-palace` (dev checkout via `devRepoPath`, then PATH, then `~/.claude/plugins/data/clued/bin`) |
| `.mcp.json` | The two stdio MCP servers the plugin registers: `clued` (session tools) and `mind-palace` (plan and story tools) |
| `commands/`, `skills/` | `/clued-setup`, `/clued-uninstall`, and the `plan-storage` skill |
| `mind-palace/` | The Go module (`github.com/robbiebyrd/clued/mind-palace`); see its README for the package map |
| `mind-palace/session/` | Session capture: config, Mongo and in-memory stores, daemon, tailing, backfill, enrichers, queries, restore, `sessionmcp` |
| `mind-palace/plugin/` | Go tests that execute the bash hooks and the shim |
| `docs/plans/`, `docs/stories/` | This repository's own plans and stories, managed by `mind-palace` |
| `docs/superpowers/specs/` | Design documents (brainstorm output); linked from plans with `plan_addSpec` |
| `docs/superpowers/plans/` | Historical plans from the retired TypeScript implementation; writes here are blocked by a hook |

## Build and test

Everything runs from `mind-palace/` with Go 1.26 (the toolchain downloads
itself; `mise.toml` pins it too):

```bash
cd mind-palace
test -z "$(gofmt -l .)" && go vet ./... && go build ./...
go test -race ./...
MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./...   # also the MongoDB suites
```

- CI runs exactly those commands with a MongoDB service container, so run
  them before pushing. Without the env var the Mongo-backed tests skip and
  everything runs against the in-memory stores.
- Hook or shim changes are tested by `go test ./plugin/`; those tests run the
  real bash scripts and skip when `jq` is not installed.
- Other store plugins (Postgres, MySQL, ClickHouse, Mongo, Firestore) run
  their conformance suite only when their `MIND_PALACE_TEST_*` DSN is set.
- Build the binary with `go build -o mind-palace ./cmd/mind-palace`; `cmd/plan`
  and `cmd/story` are thin wrappers around the same CLI.

## Conventions

- Keep `gofmt` clean and `go vet` silent; CI fails on either.
- Business rules live in `service/` (plans and stories) and `session/`
  (capture); entrypoints (`cli/`, `server/`, `sessionmcp`) only translate.
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
- Do not write under `docs/superpowers/plans/`. Plans for work in this repo
  are created with the `plan_*` MCP tools (or `mind-palace plan create`) and
  live in `docs/plans/`; stories in `docs/stories/`.
- Releases: push a `v*` tag; `.github/workflows/release.yml` builds and
  uploads the `mind-palace_<os>_<arch>` assets that `/clued-setup` downloads.
