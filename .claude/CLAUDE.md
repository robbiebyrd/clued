# Working in this repository

`clued` is a Claude Code plugin (bash hooks, a shim and two slash commands at
the repository root) plus one Go module, `mind-palace/`, that holds all the
logic: the session-capture daemon, the session MCP tools, and the plan and
story service. There is no Node code left; `scripts/clued` runs the `mind-palace`
binary.

## Layout

| Path | What it is |
|---|---|
| `hooks/hooks.json`, `hooks/` | The plugin's hook registrations (the only ones: Claude Code loads `hooks/hooks.json`, never a root `hooks.json`) and the bash hooks (`event-relay`, `session-start`, `check-setup`, `register-hooks`, `remove-remember-listener`, `uninstall`) |
| `scripts/eval-codesearch.sh` | Manual, paid run of the code graph evaluation (`eval/codesearch`) in headless `claude -p` sessions per arm; `--verify-only` checks the ground truth without sessions |
| `scripts/verify-prime.sh` | Manual check, on each release, that the prime report reaches the model once with the plugin's hooks, the settings mirror, an upgrade and a resume (real `claude -p` sessions; `plugin/verify_prime_test.go` runs it only with `CLUED_VERIFY_PRIME=1`) |
| `scripts/clued` | Shim that locates `mind-palace` (dev checkout via `devRepoPath`, then the release pinned by `.claude-plugin/plugin.json`'s `version`, downloaded once into `~/.claude/plugins/data/clued/bin/releases/v<version>/`, then PATH, then `~/.claude/plugins/data/clued/bin/mind-palace`); `--locate` prints the choice |
| `CONTRIBUTING.md`, `SECURITY.md`, `TRADEMARKS.md` | Contributor terms (DCO 1.1 sign-off, `## Sign-off`), private security reports to `security@clued.cloud`, the trademark note; `scripts/check-dco.sh` and `.github/workflows/dco.yml` enforce the sign-off on pull requests |
| `LICENSE`, `NOTICE` | Apache-2.0 (verbatim) and the NOTICE (`Copyright 2026 Space Hippo, LC`), with byte-identical copies in `mind-palace/` so the Go module zip carries them; `plugin/license_test.go` checks both pairs |
| `.claude/CLAUDE.md` | This file; it lives under `.claude/` because a `CLAUDE.md` at the plugin root fails `claude plugin validate . --strict` (`plugin/license_test.go` runs it when `claude` is installed) |
| `.claude-plugin/plugin.json` | The plugin manifest; its `version` names the GitHub release (`v<version>`) whose binaries the shim downloads |
| `scripts/build-binaries.sh` | Reproducible cross-compile of `mind-palace` for 7 platforms plus `SHA256SUMS` into a directory (`dist/` by default, git-ignored); `--target os/arch` builds one (CI's per-target matrix), `--sums DIR` writes the checksums over collected binaries (the release job), `--licenses DIR` saves every linked module's licence with `go-licenses` as `THIRD_PARTY_LICENSES/` and `THIRD_PARTY_LICENSES.tar.gz` (a release asset, and `/usr/share/licenses/clued-server/` in the image; CI's `go-licenses check` refuses forbidden, restricted and unknown licences); never committed |
| `.mcp.json` | The two stdio MCP servers the plugin registers: `clued` (session tools) and `mind-palace` (plan and story tools) |
| `commands/`, `skills/` | `/clued-setup`, `/clued-uninstall`, and the `plan-storage` skill |
| `mind-palace/` | The Go module (`github.com/robbiebyrd/clued/mind-palace`); see its README for the package map |
| `mind-palace/session/` | Session capture: config (including the remote-mode keys), Mongo and in-memory stores, daemon, tailing, backfill, enrichers, queries, restore, `sessionmcp` |
| `mind-palace/session/memory/`, `mind-palace/session/summary/` | Native memory (replaces the claude-remember integration): per-session notes (`session/summary`, kind `session-summary`), day, recent and monthly roll-ups and PreCompact checkpoints stored as `MemorySaved` records, the `extract` (default), `claude` and `hosted` engines, and the budgeted MEMORY bundle prime injects; `session memory …`, searched with `search_memory`; see `docs/memory.md` |
| `mind-palace/session/otel/` | A session as an OpenTelemetry trace sent as OTLP/HTTP JSON to `otelEndpoint` when it ends (daemon, relay, `session otel-export`); state under `<config dir>/otel/` |
| `mind-palace/session/otlp/` | Claude Code's own OpenTelemetry logs received over OTLP/HTTP at `/v1/logs` (clued server and daemon): the `api_request`, `api_error` and `tool_result` events of sessions the account holds, allow-listed attributes only, stored as `session.OtelEvent` and summed by `get_session_context` |
| `mind-palace/session/render/`, `mind-palace/session/export/` | A session as Markdown or self-contained HTML; `session export` / `export_session` (md, html, jsonl, zip; redacted unless `--raw`) and `session import` |
| `mind-palace/session/redact/` | Capture-time redaction and exclusion: a `session.Store` decorator `cli` wraps around every session store it opens (`redactAtCapture`, `redactPatterns`, `excludePaths`), so neither the store nor the server receives the original secret |
| `mind-palace/session/limits/` | Turns the JSON Claude Code pipes to its status line into `LimitsSampled` events (context fill, five-hour and seven-day limits, model, cost) and decides which samples are worth storing; the entrypoint is `mind-palace hook status`, installed as the status line by `register-hooks` when the config sets `statusLine: true` |
| `mind-palace/session/ingest/` | Hook-event bookkeeping shared by the daemon and `hook relay`: session tracking, stamping, store write, WAL fallback |
| `mind-palace/session/catchup/` | What `hook relay` runs instead of a resident tailer: pushes one session's transcript delta and changed artifact files, state and lock files under `<config dir>/catchup/` |
| `mind-palace/session/codesync/` | Code sync (remote mode, opt-in `codeSync`): what `hook relay` runs last on file-changing events and `session code-sync` runs unbudgeted; mirrors a worktree's files to the server's code store through `code/remotecode` (git enumeration, `codefilter`, redaction before hashing, manifest, blob upload, commit), state and lock files under `<config dir>/codesync/` |
| `mind-palace/session/remotesession/` | `session.Store` over a clued server (one HTTP call per port method to `server/sessionapi`) |
| `mind-palace/code/` | Collected code: the model, the `code.Store` port and its validators (`Canonical*` for query filters), the `codetest` conformance suite, the `memcode` and `mongocode` stores and `remotecode` (the port over `server/codeapi`) |
| `mind-palace/codefilter/` | What of a repository may leave for the code store (`Decide`): `codeSyncExclude`, `excludePaths`, the built-in deny list of secret files, the size cap, binary detection; shared by `session/codesync` and the GitHub sync |
| `mind-palace/codeparse/` | One file version to its facts (plan 0012): language detection (`detect.go`, `.h` by content and neighbours), the tree-sitter engine on gotreesitter (pure Go) driven per language by `queries/<lang>/profile.yaml` and `tags.scm` with grammars registered in `grammars/<lang>.go` (adding a language is those files plus goldens in `testdata/<lang>/`; see `queries/README.md`), structural chunks, Markdown and line-window fallbacks, the parse cache port keyed by SHA-256 and facts version (`mongocache`: `code_parses`) |
| `mind-palace/codegraph/` | The code graph port (`Graph`, `Opener`), schema, layers (`base`, `branch:<ref>@<commit>`, `wt:<worktree>:<ref>`), views and `hiddenBy` visibility, typed reads; `memgraph` and `falkor` (FalkorDB over the Redis protocol, parameterized queries only) pass `codegraphtest` (FalkorDB when `MIND_PALACE_TEST_FALKORDB_URL` is set) |
| `mind-palace/codeindex/` | The indexer: change-log consumer per project under a lease and cursor (`code_index_cursors`), layer selection, the file applier, the resolver (engine in `resolve.go`, one rule set per language family in `resolvers/<family>.go`), idle overlays, `Rebuild`, `Status`, `Check`/`RunChecks`; run by `server run --graph` and `server graph rebuild|status` |
| `mind-palace/eval/` | `eval/codesearch`: the code graph tools' evaluation (tasks with ground truth on clued, clued-cloud and a pinned corpus, three arms, scoring and report); the loader and scorer run on every `go test`, the paid run is `scripts/eval-codesearch.sh` (`CLUED_EVAL=1`) |
| `mind-palace/ghapp/` | GitHub App plumbing, standard library only: the RS256 app JWT, installation tokens (cached until five minutes before expiry), webhook verification (`Verify`, `X-Hub-Signature-256`, `hmac.Equal`, 25 MiB cap), a read-only REST client (installations, repositories, branches, open pulls, compare, trees, blobs, contents) with retries and `RateLimitError`, typed webhook events (`ParseEvent`); `ghapp/ghapptest` is the in-process fake GitHub every test uses, never api.github.com |
| `mind-palace/ghsync/` | The GitHub App sync rules (plan 0011): `Service` with `Handle`, `Work`, `Reconcile`, `EnqueueImport`; the `Claims`, `Jobs` (`memjobs` in memory) and `StateStore` ports; code sync into the GitHub-backed worktree `gh_<repository id>` (compare or full import, before-tip rule, `codefilter` and redaction), the branch policy (`github.*` in `mind-palace.config.yaml` over the page setting), plans and stories from the default branch (`store.ConflictNewer`, archive on delete) and the activity log (`code.Store.AppendEvents`); the host (Clued Cloud) owns the webhook, claims and namespaces; see `docs/github-app.md` |
| `mind-palace/remote/` | The HTTP client every remote adapter shares: base URL, bearer token, CA file, the rule that refuses plain `http://` off loopback, envelope decoding into typed errors |
| `mind-palace/clientcfg/` | Opens the plan and story services the way the client does: the repository's config plus, in remote mode, the `clued-server` store appended as a secondary store; `remote.project` overrides the project slug |
| `mind-palace/server/auth/` | Bearer-token authentication for the clued server: hashed token file (`server token create|list|revoke|grant`), constant-time compare, mtime reload, principal on the context |
| `mind-palace/server/device/` | The device login of the clued server (`session login` on a new machine, `server device approve` on a linked one): pending codes in memory, approval mints a token in the token file under the approver's account; the routes are in `server/device_routes.go` |
| `mind-palace/server/sessionapi/` | The `session.Store` port over HTTP under `/api/v1/session`, scoped to the token's account |
| `mind-palace/server/storeapi/` | The `store.Store` port over HTTP under `/api/v1/projects/{project}/store/` |
| `mind-palace/server/codeapi/` | The write-only code transport under `/api/v1/projects/{project}/code/` (manifest, blobs, worktrees, changes, sessions, capabilities), served with `server run --code-store` (`CLUED_SERVER_CODE_STORE`, `mongo` by default and in the `server init` stack); owner and session stamped from the token, `BeforeCodeWrite` refusals (403 `forbidden`, 413 `quota`); and the code graph read routes under `…/code/graph/` (`graph.go`: `search`, `symbol`, `neighbors`, `outline`, `context`, the caller's own view only, token-capped), served with `server run --graph` (`CLUED_SERVER_GRAPH`); the agent tools over them are `session/sessionmcp/code_tools.go` |
| `mind-palace/server/projects/` | Project slugs (`github.com__owner__repo`) and one store per project built from the server's storage definitions |
| `mind-palace/store/remotestore/` | Storage plugin `clued`: plans and stories on a clued server through `remote` and `server/storeapi` |
| `mind-palace/hookguard/` | Rules behind the hooks the binary serves (`mind-palace hook stop-guard`, `hook write-guard`); the status review (`hook status-review`, `review`) lives in `service/review.go`; `hook relay` (remote mode) lives in `cli/hook_relay.go` over `session/ingest`, `session/catchup` and `session/codesync` |
| `mind-palace/plugin/` | Go tests that execute the bash hooks and the shim, in local and remote mode (the remote tests start a clued server in the test) |
| `Dockerfile.server` | The clued server image (`ghcr.io/robbiebyrd/clued-server`, published by the release workflow); it compiles nothing and copies `mind-palace_linux_<arch>` from `BINARIES` (`dist/` by default) |
| `mind-palace/server/stack/` | What `mind-palace server init` generates under `~/.clued-server`: the stack (nginx front-end, clued server, `mongo:8`, FalkorDB for the code graph with its 0600 `falkordb.password`, `falkordb.env` and `graph.env`) as a Compose file with literal values, or as `up.sh`, `down.sh` and a launchd agent for Apple Containers (`--runtime`, `auto` picks compose when docker is installed), `stack.json`, the nginx config, the storage config, a private CA and certificate, the first token |
| `PROTOCOL.md` | The protocol reference: every entrypoint (hooks, shim, daemon, MCP servers, the server's HTTP API, CLI) with its exact JSON, routes, parameters and exit codes; update it when an entrypoint changes |
| `docs/server.md` | Running the clued server (binary, or the container stack from `server init`: Docker Compose, or Apple Containers), tokens, TLS, nginx and other front-ends, the HTTPS `/mcp` endpoint |
| `docs/code-graph.md` | The code graph: layers and views, what the `code_*` tools return, privacy, the languages per plan 0012 and their state, rebuild and checks, FalkorDB in the stack |
| `docs/github-app.md` | The clued GitHub App: permissions, events, claims, branch policy, code, plans and stories sync and conflicts, the activity log, what is stored, uninstall and deletion |
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
  is set. The FalkorDB code graph adapter runs `codegraphtest` only when
  `MIND_PALACE_TEST_FALKORDB_URL` is set (`redis://127.0.0.1:6379`; CI runs
  a pinned `falkordb/falkordb` service container).
- Build the binary with `go build -o mind-palace ./cmd/mind-palace`; `cmd/plan`
  and `cmd/story` are thin wrappers around the same CLI. The same binary is
  the clued server: `mind-palace server run` (flags fall back to
  `CLUED_SERVER_ADDR`, `CLUED_SERVER_TLS_CERT`, `CLUED_SERVER_TLS_KEY`,
  `CLUED_SERVER_BEHIND_PROXY`, `CLUED_SERVER_TOKENS`; `--store memory` for
  tests), `mind-palace server token create|list|revoke|grant` and
  `mind-palace server init` (the container stack, Docker Compose or Apple
  Containers; its files are generated by `server/stack`, never committed,
  and `/clued-setup` starts it).
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
  run in remote mode) registered in `hooks/hooks.json` as
  `"${CLAUDE_PLUGIN_ROOT}/scripts/clued" hook …`; they print bare hook JSON,
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
- Each hook is registered exactly once, in `hooks/hooks.json`
  (`plugin/hooks_layout_test.go`). `hooks/register-hooks` writes no hook
  into `settings.json` by default and removes an older clued's mirror (a
  hook registered in both places runs twice); `--settings` mirrors
  `hooks/hooks.json` through the launcher
  `${CLAUDE_PLUGIN_DATA:-~/.claude/plugins/data/clued}/bin/clued-hook`
  (which reads `plugin-root`, so a moved plugin root does not strand it),
  for setups where the plugin's hooks are not loaded. The status line is
  the one clued entry settings keep. See README "Where the hooks are
  registered" and anthropics/claude-code#16538.
- Paths that come from hook requests are validated inline with
  `filepath.IsLocal` / `filepath.Clean` + prefix checks (CodeQL needs to see
  the guard at the point of use).
- Do not write under `docs/superpowers/plans/`, and do not write or edit the
  files under `docs/plans/` and `docs/stories/` by hand: the `write-guard`
  hook denies it. Plans for work in this repo are created with the `plan_*`
  MCP tools (or `mind-palace plan create`), stories with `story_*`, and both
  change through the setters (`plan_setStatus`, `story_setCriterion`, …) or
  `plan_update` / `story_update` for the content.
- Every commit is signed off under the DCO (`git commit -s`;
  CONTRIBUTING.md#sign-off): the `DCO` workflow (`scripts/check-dco.sh`)
  fails a pull request with a commit whose author has no matching
  `Signed-off-by:`. An agent committing as Robbie Byrd adds
  `Signed-off-by: Robbie Byrd <me@robbiebyrd.com>` alongside the
  `Co-Authored-By:` trailer; an agent committing under its own identity
  signs off as that identity (what `git commit -s` writes). Security
  reports go to `security@clued.cloud` (SECURITY.md), licensing to
  `legal@clued.cloud`; the code is Apache-2.0 (LICENSE, NOTICE), the
  names and logo are Space Hippo, LC's (TRADEMARKS.md).
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
  downloads on first use, plus `THIRD_PARTY_LICENSES.tar.gz`. A manual run of the workflow completes a release
  with missing assets but bumps nothing. The job pushes to main with the
  workflow's token, so a branch rule that blocks it fails the release.
