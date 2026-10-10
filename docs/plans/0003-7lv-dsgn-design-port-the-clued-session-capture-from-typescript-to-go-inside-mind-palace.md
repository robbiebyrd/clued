---
id: 0003-7lv
title: 'Design: port the clued session capture from TypeScript to Go inside mind-palace'
type: dsgn
status: pending
priority: "2"
effort: XL
created: "2026-10-10T02:48:36.221Z"
updated: "2026-10-10T02:48:36.221Z"
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  specs:
    - ./docs/superpowers/specs/2026-09-22-clued-plugin-design.md
---

# Design: port the clued session capture from TypeScript to Go inside mind-palace

## Summary

**Goal:** Every function in `src/` (daemon, tailer, artifact watcher, WAL, backfill, restore, MCP session tools, Mongo access, config, git/host/account probes) and the enrichers the daemon loads run as Go code inside the `mind-palace` binary, with the Node/TypeScript implementation retired.

**Problem:** clued is two code bases in two runtimes. The session-capture side (`src/`, `enrichers/`, 2 780 + 600 lines of TypeScript) has its own build (esbuild), test runner, npm distribution and a hand-rolled MCP JSON-RPC/SSE transport, while `mind-palace/` already ships a Go CLI, HTTP server and MCP server (official go-sdk) with a MongoDB driver in its module. The plugin cannot ship as one binary, and every change to session capture needs Node tooling.

**Approach:** Add a `session` domain to mind-palace in a hexagonal layout: pure domain types and ports in `session`, adapters for MongoDB, files, git and host probes, use cases for capture, enrichment, query, restore and backfill, and entrypoints through the existing CLI and MCP server. Port test-first with a one-to-one test parity table, and split the work by package so independent subagents build the pieces concurrently.

---

# Part 1 — Design

## Current State

**TypeScript side (`src/`, 15 files):**

| File | What it does |
|---|---|
| `daemon.ts` | HTTP ingest on 127.0.0.1:8085 (`POST /event`, `GET /health`); per-session tracking (upsert session, git origin/branch discovery, transcript tailing, artifact watching); WAL flush every 60 s; enrichment loop; exits 0 on EADDRINUSE |
| `tailer.ts` | `tailFile(path, onLine)`: position-based tail with fs.watch + 2 s poll, waits 500 ms for the file to appear |
| `artifact-watcher.ts` | `watchDir` (mtime diff, fs.watch + 2 s poll) over `<sessionDir>/subagents` (tail `*.jsonl` once per subagent, store `*.meta.json` blobs), `<sessionDir>/tool-results` (utf8 blobs) and `<fileHistoryDir>/<session>` (base64 blobs) |
| `wal.ts` | `appendToWal`, `flushWal` (re-inserts, keeps failures, skips malformed lines) |
| `backfill.ts` | `decodeProjectPath`; walks `projectsDir`, upserts sessions and bulk-upserts transcript lines in batches of 5 sessions; standalone entrypoint |
| `restore.ts` | `restoreSession`: rebuilds `<proj>/<id>.jsonl`, subagent JSONLs and the three blob types from MongoDB; reports files/bytes written and missing parts |
| `mcp.ts` | Tools `find_sessions`, `get_session_context`, `get_full_session`, `search_commands`, `read_transcript` (progress notifications), `restore_session`; stdio transport (`--stdio`, used by `.mcp.json`) and a hand-rolled SSE server on :8086; lazy Mongo connect; every query scoped by `account_id` |
| `mcp-protocol.ts` | JSON-RPC dispatch for initialize/ping/tools/list/tools/call |
| `mongo.ts` | Collections `sessions`, `hook_events`, `transcript_lines`, `subagent_lines`, `blobs`; 14 indexes (unique on `{session_id}`, `{session_id,seq}`, `{session_id,subagent_id,seq}`, `{session_id,blob_type,name}`); `session_full` aggregation view (created, or `collMod` when it exists) |
| `enricher.ts` | Loads `enrichers/*.{ts,mjs}` dynamically; every 5 s, per enricher, finds up to `batchLimit` docs lacking `enrichments.<name>` and `enrichments.<name>_failed`, runs `matches`/`enrich`, writes result or failure |
| `config.ts` | `~/.claude/plugins/data/clued/config.json` + `CLUED_*` env overrides + `~` expansion; defaults (Mongo :27018, ports 8085/8086, projectsDir, fileHistoryDir, claudeAppConfigPath, walPath next to config) |
| `git.ts`, `host.ts`, `account.ts` | `git remote get-url origin` / `git branch --show-current` with 2 s timeout; hostname/ip/mac/user/uid/platform/arch/release/type; `lastKnownAccountUuid` from the Claude desktop config, `unknown` with a warning otherwise |
| `build.ts` | esbuild bundles to `dist/` (daemon, backfill, mcp, enrichers) |

**Enrichers (`enrichers/`, 19 modules + `lang.ts`):** on `hook_events`: bash-binaries, bash-outcome, edit-diff-stats, file-tracker, prompt-features, tool-classifier; on `transcript_lines`: attachment-extractor, code-language-detector, error-flag, file-snapshot-extractor, hook-linker (queries `hook_events` by `tool_use_id`, batchLimit 20), intent-classifier, line-classifier, message-stats, privacy-redact (disabled by default), skill-detector, thinking-stats, tool-use-summary; line-summary is a disabled stub with no implementation.

**Tests (`test/`, 34 files, 3 728 lines):** one unit file per module and enricher (node:test), plus integration tests (artifact-watcher, backfill, daemon, hook-linker, mcp, mongo, restore) that need a live MongoDB via `CLUED_MONGO_URL`, and a bash test for `hooks/register-hooks`.

**Plugin glue:** `hooks/event-relay` posts each hook payload to `:8085/event` (WAL file on failure); `hooks/session-start` starts `node dist/daemon.mjs`, `node dist/mcp.mjs` and `node dist/backfill.mjs`, syncs `dist/` from `devRepoPath`; `.mcp.json` runs `npx -y @robbiebyrd/clued --stdio`; `bin/clued.cjs` is the npm bin; `.github/workflows/build.yml` rebuilds `dist/` on push and `publish.yml` publishes to npm.

**mind-palace side:** Go 1.26 module `github.com/robbiebyrd/clued/mind-palace` (14 450 lines) with `kind`, `model`, `config`, `schema`, `markdown`, `render`, `service`, `ops`, `store/*` (plugin registry via `store.Register`, `storetest` conformance suite, Mongo tests gated on `MIND_PALACE_TEST_MONGODB_URI`), `events`, `server/{httpapi,ws,mcpserver}`, `cli`, `cmd/*`. `go.mongodb.org/mongo-driver/v2` and `modelcontextprotocol/go-sdk` are already dependencies. `mcpserver.New` registers one tool per kind operation plus `watch`; `mind-palace mcp` serves stdio, `mind-palace serve` mounts Streamable HTTP at `/mcp`. `cli.App.init` opens every configured store, and `filestore.Init` creates the plans/stories directories, so the palace cannot be started from an arbitrary project directory without side effects.

## Decisions

- **A new `session` domain lives inside the mind-palace module (`mind-palace/session/...`); it is neither a new document kind nor a separate module.** — Boss asked for the functions to be added to mind-palace. Sessions are not Markdown documents, so the kind machinery (templates, workflow, links) does not apply, and one module gives one binary.
- **Hexagonal layout: `session` holds the domain types, `Doc` helpers, the `Store` and `Lookup` ports, the enricher struct and registry, config and the account probe; adapters live in sub-packages (`mongosession`, `memsession`, `tail`, `wal`, `gitinfo`, `hostinfo`, `enrichers`); use cases in `enrich`, `query`, `restore`, `backfill`, `daemon`; entrypoints in `sessionmcp`, the existing `cli` and `server/mcpserver`.** — CLAUDE.md requires the hexagonal pattern. The TypeScript code passes the Mongo handle everywhere; putting a port between use cases and MongoDB lets every use case be tested against a memory adapter with real logic and no mocks, and keeps each package small enough for one subagent.
- **Configuration stays in `~/.claude/plugins/data/clued/config.json` with the same keys (`mongoUrl`, `dbName`, `port`, `projectsDir`, `fileHistoryDir`, `disabledEnrichers`, `claudeAppConfigPath`, `walPath`) and the same `CLUED_*` environment overrides and `~` expansion; `mind-palace session --config <path>` overrides the location. `mcpPort` is no longer read.** — This file is per-machine state read by `hooks/event-relay`, `hooks/session-start`, `hooks/uninstall` and `/clued-setup` with `jq`; `mind-palace.config.yaml` is per-repository state for plans and stories. Keeping the two apart avoids rewriting the hooks and the setup wizard. Unknown keys are ignored by `encoding/json`, so an existing file keeps working.
- **The MongoDB adapter keeps the collection names, document shapes, index definitions and the `session_full` view exactly as `mongo.ts` defines them.** — Existing databases stay queryable and the Node and Go implementations can be compared on the same data during verification. The only shape change is `hook-linker`'s `hook_event_ids`, stored as hex strings instead of ObjectIds, because the domain must not depend on BSON types.
- **Enrichers compile in and self-register from `init()` in `session/enrichers` (one file per enricher, mirroring `store.Register`); each is a `session.Enricher` struct value with `Name`, `Collection`, `Enabled`, `BatchLimit`, `Matches` and `Enrich`; `disabledEnrichers` and `Enabled: false` are honoured; `line-summary` is not ported.** — Go has no dynamic module loading worth its complexity; a registry keeps the three enricher groups independent for parallel work. `line-summary` has no implementation and CLAUDE.md forbids placeholder code.
- **The enrichment loop, query, restore and backfill use cases depend only on the `Store` port. `session/memsession` is a complete in-memory implementation and `session/sessiontest` is a conformance suite run against it always and against `mongosession` when `MIND_PALACE_TEST_MONGODB_URI` is set.** — Mirrors mind-palace's `storetest` pattern, gives every unit test real behaviour without mocks, and lets the daemon and MCP tests run on every `go test` while the Mongo-backed ones run in CI with a service container and locally on demand.
- **The session MCP tools (`find_sessions`, `get_session_context`, `get_full_session`, `search_commands`, `read_transcript`, `restore_session`, names unchanged) are a `sessionmcp.Tools` provider. `mind-palace session mcp` serves them alone over stdio (what `.mcp.json` runs); `mcpserver.Options.Tools` lets `mind-palace mcp` and `mind-palace serve` add them next to the plan and story tools. The hand-rolled SSE server on :8086 is retired.** — Starting the palace creates `docs/plans` and `docs/stories` in the working directory, so the plugin needs a session-only entrypoint. The go-sdk provides stdio and Streamable HTTP, and Claude Code already connects over stdio; keeping the tool names means existing prompts and skills keep working.
- **The ingest daemon remains its own long-running process: `mind-palace session daemon` listens on 127.0.0.1:`port` with `POST /event` and `GET /health`, runs the WAL flush, the enrichment loop and the per-session tailers, and exits 0 when the port is taken.** — `hooks/event-relay` posts to it and `hooks/session-start` health-checks it; its lifecycle is per machine, not per repository like `mind-palace serve`.
- **Tailing and directory watching are poll-based: 500 ms until the path exists, 2 s afterwards, with an mtime map for directories and a byte offset for files. No fsnotify dependency.** — The TypeScript code already relies on the 2 s poll as its safety net because kqueue does not report new files reliably; a poll alone is portable, testable with a short interval, and keeps the dependency list unchanged.
- **Distribution: a release workflow cross-compiles `mind-palace` (darwin/linux × amd64/arm64) and attaches the binaries to a GitHub release; `/clued-setup` downloads the matching asset into `~/.claude/plugins/data/clued/bin/`; a `bin/clued` shell shim (replacing `bin/clued.cjs`) resolves that binary, then `mind-palace` on `PATH`, then builds from `devRepoPath` when it is set; `.mcp.json` and the hooks call the shim. npm publishing stops.** — The plugin cache has no toolchain, so the binary must be prebuilt or downloaded. The shim keeps the hooks and `.mcp.json` free of path logic and preserves the `devRepoPath` developer loop (rebuild instead of rsync).
- **Test parity: every TypeScript test case gets a Go counterpart (table under Testing), written before the implementation of its section.** — CLAUDE.md requires TDD and full coverage; the existing suite is the specification of the behaviour being ported.
- **The TypeScript implementation, the Node tooling and the npm workflows are deleted only in the last phase, after the Go binary has been verified against a live Claude Code session, and only with Boss's explicit go-ahead.** — CLAUDE.md forbids discarding implementations without permission; until verification the Node code is the reference.
- **Concurrency protocol: every implementation section is one subagent in its own git worktree (`.worktrees/<section>`) on a branch off the integration branch `wip/session-port`; a subagent edits only the files its section lists, commits with the section's message, and reports back; the orchestrator merges each wave into the integration branch and runs `gofmt -l`, `go vet ./...` and `go test ./...` before starting the next wave.** — Package boundaries make the sections conflict-free; worktrees isolate them; a per-wave gate keeps the integration branch green.

### Rejected alternatives

- **Model sessions as a mind-palace `kind`.** — Kinds are Markdown documents with front matter, templates, a status workflow and links; none of that applies to hook events, transcript lines or blobs.
- **A separate Go module or binary (`clued-go`).** — Boss asked for the functions inside mind-palace; a second binary doubles distribution work.
- **Store sessions through the mind-palace `store.Store` plugins.** — That interface is keyed by kind and id and persists rendered Markdown; session data is five collections with different keys and an aggregation view.
- **Keep a port-level SSE MCP server on :8086.** — The plugin already uses stdio, the go-sdk offers stdio and Streamable HTTP, and the SSE variant was hand-rolled JSON-RPC that the go-sdk replaces.
- **Use fsnotify for tailing.** — Adds a dependency and still needs the poll on macOS; the poll alone meets the 2 s latency the TypeScript code accepted.
- **Merge the clued config into `mind-palace.config.yaml`.** — Different scope (machine vs repository) and the shell hooks read the JSON file with `jq`.
- **Port `line-summary` as a disabled stub.** — It contains no behaviour; CLAUDE.md forbids stub code.

## Design

### `session` — domain, ports, config (package `session`)

Pure package (standard library only).

```go
// Doc is a stored document (hook event, transcript line, session…) as decoded JSON/BSON.
type Doc map[string]any
func (d Doc) Map(key string) Doc            // nested object or nil
func (d Doc) String(key string) (string, bool)
func (d Doc) Line() Doc                     // d["line"]
func (d Doc) Message() Doc                  // Line().Map("message")
func (d Doc) Content() []Doc                // Message()["content"] blocks
func (d Doc) ToolInput() Doc                // d["tool_input"]
func (d Doc) ToolResponse() Doc             // d["tool_response"]
func (d Doc) Attachment() Doc               // Line().Map("attachment")

type HostInfo struct { Hostname, IP, MAC, Username string; UID int; Platform, Arch, OSRelease, OSType string } // json/bson tags: hostname, ip, mac, username, uid, platform, arch, os_release, os_type; ip and mac are null when unknown

type Session struct { SessionID, TranscriptPath, Cwd, ProjectPath, GitOrigin, GitBranch, AccountID string; Host *HostInfo; LastSeen time.Time } // empty strings are not written
type TranscriptLine struct { SessionID string; Seq int; Line any; AccountID string; Host *HostInfo }
type SubagentLine struct { SessionID, SubagentID string; Seq int; Line any; AccountID string; Host *HostInfo }
type Blob struct { SessionID, BlobType, Name, Content, Encoding, AccountID string } // blob_type: subagent-meta | tool-result | file-history; encoding: utf8 | base64

type SessionQuery struct { AccountID, ProjectPathRe, GitOriginRe, QueryRe string; Limit int } // regexes case-insensitive; QueryRe matches project_path or cwd; Limit capped at 500
type LineQuery struct { AccountID, SessionID string; Offset, Limit int; Descending bool }       // empty AccountID = no account filter
type CommandQuery struct { AccountID, PatternRe string; SessionIDs []string; Limit int }          // tool_name Bash, tool_input.command matches; newest first

var ErrNotFound = errors.New("session not found")

type Lookup interface { HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error) }

type Store interface {
    Lookup
    UpsertSession(ctx context.Context, s Session) error                       // $set present fields, $setOnInsert started_at
    TouchSession(ctx context.Context, accountID, sessionID string, at time.Time) error
    SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error // only when git_origin is absent
    GetSession(ctx context.Context, accountID, sessionID string) (Doc, error)  // ErrNotFound
    FindSessions(ctx context.Context, q SessionQuery) ([]Doc, error)           // projection session_id, project_path, git_origin, cwd, started_at, last_seen; sorted last_seen desc
    InsertHookEvent(ctx context.Context, ev Doc) error
    UpsertTranscriptLines(ctx context.Context, lines []TranscriptLine) error   // unordered bulk upsert on {session_id, seq}; created_at on insert only
    CountTranscriptLines(ctx context.Context, accountID, sessionID string) (int, error)
    TranscriptLines(ctx context.Context, q LineQuery) ([]Doc, error)           // without _id, ordered by seq
    UpsertSubagentLine(ctx context.Context, l SubagentLine) error
    SubagentIDs(ctx context.Context, accountID, sessionID string) ([]string, error)
    SubagentLines(ctx context.Context, accountID, sessionID, subagentID string) ([]Doc, error)
    UpsertBlob(ctx context.Context, b Blob) error                              // content updates, created_at on insert only
    Blobs(ctx context.Context, accountID, sessionID string) ([]Doc, error)
    BashEvents(ctx context.Context, q CommandQuery) ([]Doc, error)             // projection session_id, tool_input, created_at
    FullSession(ctx context.Context, accountID, sessionID string) (Doc, error) // the session_full join; ErrNotFound
    Unenriched(ctx context.Context, collection, enricher string, limit int) ([]Doc, error) // lacks enrichments.<name> and enrichments.<name>_failed; includes _id
    SetEnrichment(ctx context.Context, collection string, id any, enricher string, result any) error
    SetEnrichmentFailure(ctx context.Context, collection string, id any, enricher, message string, at time.Time) error
    Close(ctx context.Context) error
}

type Enricher struct { Name, Collection string; Enabled bool; BatchLimit int; Matches func(Doc) bool; Enrich func(ctx context.Context, doc Doc, lookup Lookup) (any, error) }
func Register(e Enricher)                        // panics on duplicate name
func Active(disabled []string) []Enricher        // Enabled and not disabled, sorted by name

type Config struct { MongoURL, DBName string; Port int; ProjectsDir, FileHistoryDir string; DisabledEnrichers []string; ClaudeAppConfigPath, WalPath string }
func DefaultConfigPath() string                   // ~/.claude/plugins/data/clued/config.json
func LoadConfig(path string) Config               // defaults ← file ← CLUED_MONGO_URL, CLUED_DB_NAME, CLUED_PORT, CLUED_PROJECTS_DIR, CLUED_FILE_HISTORY_DIR, CLUED_CLAUDE_APP_CONFIG_PATH, CLUED_WAL_PATH; ~ expansion; walPath defaults next to the config file
func ReadAccountID(path string) string            // lastKnownAccountUuid or "unknown" (warns on stderr)
func ExtToLang(path string) string                // the lang.ts table; "" when unknown
func DecodeProjectPath(dirName string) string     // "-Users-x-y" → "/Users/x/y"
func EncodeProjectPath(path string) string        // inverse used by restore
```

JSON/BSON field names are the snake_case names the TypeScript code writes (`session_id`, `transcript_path`, `created_at`, `blob_type`, …).

### `session/memsession` and `session/sessiontest`

`memsession.New() *Store` implements `session.Store` with maps guarded by a mutex: documents are `session.Doc` values with generated `_id` strings and `created_at` stamps, regex filters use `regexp` with `(?i)`, `FullSession` performs the same join as the Mongo view (sub-arrays sorted as the view sorts them, redundant fields and blob `content` stripped).

`sessiontest.Run(t, func(t *testing.T) session.Store)` is the conformance suite every adapter must pass: session upsert/touch/git-info semantics, `started_at` set once, unique keys on transcript/subagent/blob upserts, `created_at` preserved on re-upsert, ordering and projections of every read, account scoping, `FullSession` shape, enrichment selection and recording. It is the Go form of `test/integration/mongo.test.ts` plus the behaviours the use-case tests rely on.

### `session/mongosession`

`New(cfg session.Config) *Store`; `Connect(ctx)` connects, creates the 14 indexes from `mongo.ts` (index errors are logged as warnings, as the Node code does for IndexKeySpecsConflict), and creates or `collMod`s the `session_full` view with the same pipeline. Implements every `Store` method with the driver's `UpdateOne`/`BulkWrite`/`Find` and the same filters, projections, sorts and `$setOnInsert` behaviour as the TypeScript queries. `HookEventIDsByToolUse` returns ObjectID hex strings. Test: `sessiontest.Run` gated on `MIND_PALACE_TEST_MONGODB_URI`, using a per-run database name and dropping it on cleanup, plus a view test that asserts the pipeline is replaced on a second `Connect`.

### `session/tail`

`TailFile(path string, onLine func(string), opts Options) (stop func())`: polls (`Options.Interval`, default 2 s; `Options.WaitInterval` 500 ms until the file exists), reads from the last byte offset, splits on newlines, skips blank lines, delivers complete lines in order, ignores transient stat/open errors, guards against overlapping reads.

`WatchDir(path string, onChange func(name, fullPath string), opts Options) (stop func())`: on each poll lists regular files and calls `onChange` when a file is new or its mtime changed.

`WatchArtifacts(sessionID, sessionDir, fileHistoryPath, accountID string, host *session.HostInfo, store session.Store, opts Options) (stop func())`: the three watchers from `artifact-watcher.ts` (subagent JSONL tailers started once per subagent id with their own `seq` counter, `*.meta.json` → `subagent-meta` blobs, tool-results → utf8 blobs, file-history → base64 blobs). Store errors are logged, never fatal.

### `session/wal`

`Append(path string, event session.Doc) error` (creates the directory, appends one JSON line). `Flush(ctx, path string, insert func(ctx, session.Doc) error) error`: no file → nothing; re-inserts each line, keeps the lines whose insert failed, drops malformed lines, rewrites the file (empty when all succeeded).

### `session/gitinfo`, `session/hostinfo`

`gitinfo.Origin(ctx, dir) string` and `gitinfo.Branch(ctx, dir) string` run `git -C <dir> remote get-url origin` / `git -C <dir> branch --show-current` with a 2 s timeout and return `""` on any failure (no git, no remote, detached HEAD, missing directory).

`hostinfo.Read() session.HostInfo`: `os.Hostname`, first non-loopback IPv4 interface with its hardware address, `os/user.Current` (username, numeric uid), `runtime.GOOS`/`GOARCH`, kernel release and type from `unix.Uname`.

### `session/enrichers`

One file per enricher, each calling `session.Register` from `init()`, with the same names, collections, `Enabled` flags, batch limits, `matches` conditions and result fields as the TypeScript modules. Three independent groups for parallel work:

- **A, `hook_events`:** bash-binaries (`extractBinaries` with the builtin list), bash-outcome, edit-diff-stats, file-tracker (`detectArtifact` priority rules), prompt-features (`classifyIntent`, `looks_like_task_start`), tool-classifier.
- **B, `transcript_lines`:** attachment-extractor, code-language-detector (fence regex per call), error-flag (`ERROR_TEXT_RE`, precedence hook_error → tool_failure → error_text), file-snapshot-extractor, intent-classifier, line-classifier.
- **C, `transcript_lines`:** message-stats, privacy-redact (`Enabled: false`; api-key, email, aws-key, jwt patterns; JSON-encodes object lines), skill-detector, thinking-stats, tool-use-summary, hook-linker (`BatchLimit: 20`, uses `Lookup`).

Results are small structs with snake_case JSON/BSON tags so the stored shape matches the Node output. Regular expressions are translated to RE2; where a JavaScript pattern used `\b` with Unicode or lookahead it is reproduced with explicit alternatives and covered by the ported test.

### `session/enrich`

`Loop(ctx, store session.Store, enrichers []session.Enricher, interval time.Duration, logger *slog.Logger)` runs until `ctx` ends: every `interval` (5 s in the daemon), for each enricher, `store.Unenriched(collection, name, batchLimit or 100)`, skip docs where `Matches` is false, call `Enrich`, then `SetEnrichment` or `SetEnrichmentFailure` with the error message. Query failures are logged and skipped. Tests use `memsession` with a 10 ms interval and cover success, failure recording, non-matching docs and batch limits (the four `startEnrichmentLoop` cases).

### `session/query`, `session/restore`, `session/backfill`

`query.Service{Store, AccountID}` exposes `FindSessions(ctx, FindSessionsArgs) ([]SessionSummary, error)` (adds `event_count`), `SearchCommands(ctx, SearchCommandsArgs) ([]CommandHit, error)` (ownership check for `session_id`, git-origin scoping, joins project_path/git_origin), `SessionContext(ctx, id) (Context, error)` (ten most recent distinct Bash commands from the last 100 events, first 20 and last 20 lines, last lines only when more than 20 exist), `FullSession(ctx, id) (session.Doc, error)` and `ReadTranscript(ctx, ReadTranscriptArgs, progress func(done, total int)) ([]session.Doc, error)` (batches of 50, progress after each batch, stops early on a short batch). Limits cap at 500. Unknown or foreign sessions return `session.ErrNotFound`.

`restore.Session(ctx, store, accountID, args, fileHistoryDir) (Result, error)` reproduces `restore.ts`: target directory from `project_path` override, else the transcript path's parent name, else the encoded stored project path; writes the transcript in batches of 500, subagent JSONLs, the three blob types; returns `files_written`, `bytes_written`, `missing`.

`backfill.Run(ctx, store, projectsDir, accountID string, logger) (Stats, error)`: walks the project directories, upserts each session (with git origin/branch from the decoded project path) and bulk-upserts its lines, five sessions at a time with errors logged per file, and returns session and line counts.

### `session/daemon`

`New(cfg session.Config, store session.Store, enrichers []session.Enricher, accountID string, host session.HostInfo, logger) *Daemon` with `Handler() http.Handler` (`GET /health` → 200 `ok`; `POST /event` → parse JSON, track the session, touch `last_seen`, insert the hook event with `account_id`, `host`, `created_at`, append to the WAL when the insert fails; 400 on bad JSON; 404 otherwise) and `Run(ctx, addr) error` (flush the WAL on start and every 60 s, run the enrichment loop, serve until `ctx` ends, stop every tailer). Session tracking reproduces `trackSession`: first sight upserts the session and starts the transcript tailer (seq from 0, upsert on `{session_id, seq}`) and the artifact watchers; later events with a `cwd` fill in git info once. `Run` returns `ErrAddrInUse` so the CLI can exit 0. Tests: `httptest` against `memsession` for every `daemon.test.ts` case (health, event insert, session creation and tailing, 404, git origin now/later/never) plus WAL fallback using a store wrapper that fails inserts.

### `session/sessionmcp` and the `mcpserver` seam

`mcpserver.ToolProvider` interface `{ AddTools(s *mcp.Server, prefix string) }` and `mcpserver.Options.Tools []ToolProvider`, applied in `New` after the kind tools; the server `Instructions` gain one line about session tools when a provider is present.

`sessionmcp.New(cfg session.Config, connect func(ctx) (session.Store, error), accountID string) *Tools` implements `ToolProvider` with the six tools, JSON input schemas identical to `mcp-protocol.ts`, results as JSON text content, errors as `IsError` results, lazy store connection on first call, and `read_transcript` progress via `req.Session.NotifyProgress` when the request carries a progress token. `Tools.Server() *mcp.Server` builds a standalone server (name `clued`, version from the build) for `mind-palace session mcp`. Tests drive a real `mcp.Client` over `mcp.NewInMemoryTransports` against `memsession` for every `mcp.test.ts` case (tool list and schemas, each tool's results, account isolation, progress notifications, errors).

### CLI `session` group (`cli`)

`mind-palace session` with persistent `--config` (clued config path) and subcommands: `daemon [--addr]`, `backfill`, `mcp [--mcp-tool-prefix]`, `find [--project-path re] [--git-origin re] [--query re] [--limit n]`, `search-commands <pattern> [--session id] [--git-origin re] [--limit n]`, `context <session>`, `full <session>`, `transcript <session> [--offset n] [--limit n]`, `restore <session> [--project-path p] [--projects-dir d]`. Output uses the existing `{"ok": …}` envelope and `--format`/`--compact` flags; errors map `session.ErrNotFound` → exit 4, store errors → 10. `mind-palace mcp` and `mind-palace serve` pass `sessionmcp` as a tool provider. The `plan` and `story` binaries are untouched.

### Plugin glue, distribution, CI, docs

`bin/clued` (bash): resolve `~/.claude/plugins/data/clued/bin/mind-palace`, else `mind-palace` on PATH, else build from `devRepoPath` (`go build -o … ./cmd/mind-palace` when a Go source is newer than the binary); exec it with the given arguments. `.mcp.json` → `${CLAUDE_PLUGIN_ROOT}/bin/clued session mcp`. `hooks/session-start`: start `bin/clued session daemon` when the health check fails, run `bin/clued session backfill`, drop the MCP server start and the `dist/` rsync. `hooks/uninstall`: `pkill -f "session daemon"`, remove the downloaded binary. `commands/clued-setup.md` and `skills/clued-setup.md`: download the release asset for the detected OS/arch (or build from source when Go is present and no release matches). `.github/workflows/release.yml`: on tag `v*`, `go build` the four targets and upload them to the release; `mind-palace.yml` gains a `mongo` service and `MIND_PALACE_TEST_MONGODB_URI`. README: replace the Node sections (install, verify, manual config, MCP registration) and add the session commands; `mind-palace/README.md` gains a Sessions section; `build.yml` and `publish.yml` are removed with the Node code in the last phase.

### Error Handling

Mirrors the TypeScript behaviour: a failed hook-event insert appends the event to the WAL and the request still returns 200; the WAL flush keeps failed lines and drops malformed ones; tailers and watchers log transient file-system errors and keep polling; enrichment failures are recorded under `enrichments.<name>_failed` with message and time and never stop the loop; index creation errors are warnings; the daemon exits 0 when its port is already bound (another instance runs) and 1 when MongoDB is unreachable at start; MCP tool failures become `IsError` results with the message; CLI errors use the mind-palace envelope and exit codes.

### Edge Cases

- Only one tailer per subagent id even when the file's mtime keeps changing (otherwise `seq` restarts and violates the unique index).
- A daemon restart restarts `seq` at 0 for a live session; upserting on `{session_id, seq}` makes that idempotent, as in Node.
- `DecodeProjectPath` is best-effort: hyphens in path components are indistinguishable from separators.
- Restore refuses a session with no `transcript_path`, no stored `project_path` and no override.
- Blob encodings: `file-history` is base64, the other two utf8; unknown blob types are skipped on restore.
- `account_id` falls back to `unknown` with a warning; every query still filters by it.
- `hook_event_ids` become hex strings (new documents only).
- Lines that are not valid JSON are stored as `{"raw": "<line>"}`.

## Considerations

- **Security:** The daemon binds 127.0.0.1 only. Every query and restore is scoped by `account_id`. Restore writes only under the resolved projects directory and the file-history directory. No new network dependencies.
- **Performance:** Backfill bulk-upserts per session, five sessions concurrently (errgroup with a limit of 5). `read_transcript` pages in batches of 50 with progress notifications; restore pages in batches of 500. Enrichment batches honour `BatchLimit`. Polling at 2 s matches the existing latency.

## Testing

**Levels.** Unit tests with the standard `testing` package for every pure package (enrichers, doc helpers, config, lang, wal, tail with short intervals, gitinfo with temporary repositories, hostinfo shape checks, account). Use-case tests (`enrich`, `query`, `restore`, `backfill`, `daemon`, `sessionmcp`) run against `memsession` on every `go test`. The `sessiontest` conformance suite runs against `memsession` always and `mongosession` when `MIND_PALACE_TEST_MONGODB_URI` is set; the daemon and MCP packages also run their suites against Mongo under the same gate. `hooks/register-hooks` keeps its test as a Go test that executes the script against a temporary settings file. No mocks anywhere; test output must stay pristine.

**Parity table (TypeScript → Go).**

| TypeScript test file | Go test |
|---|---|
| `account.test.ts` | `session/account_test.go` |
| `config.test.ts` | `session/config_test.go` |
| `lang.test.ts` | `session/lang_test.go` |
| `enricher.test.ts` (loading) | `session/enricher_test.go` (registry, `Active`) |
| `enricher.test.ts` (loop) | `session/enrich/loop_test.go` |
| `wal.test.ts` | `session/wal/wal_test.go` |
| `tailer.test.ts` | `session/tail/tail_test.go` |
| `integration/artifact-watcher.test.ts` | `session/tail/artifacts_test.go` (memsession) |
| `git.test.ts` | `session/gitinfo/gitinfo_test.go` |
| `host.test.ts` | `session/hostinfo/hostinfo_test.go` |
| `integration/mongo.test.ts` | `session/sessiontest` + `session/mongosession/mongosession_test.go` |
| `integration/backfill.test.ts` | `session/backfill/backfill_test.go` |
| `integration/restore.test.ts` | `session/restore/restore_test.go` |
| `integration/daemon.test.ts` | `session/daemon/daemon_test.go` |
| `mcp-protocol.test.ts`, `integration/mcp.test.ts` | `session/sessionmcp/sessionmcp_test.go` |
| `integration/hook-linker.test.ts` | `session/enrichers/hook_linker_test.go` |
| one file per enricher | `session/enrichers/<name>_test.go` |
| `register-hooks.test.ts` | `mind-palace/plugin/register_hooks_test.go` |

**Verification.** `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go test ./...` on every merge; the Mongo-gated run locally against a live MongoDB before the phase that removes the Node code; a manual end-to-end check (hooks → daemon → MongoDB → MCP tools in Claude Code) recorded in the implementation plan.

## Scope

**In scope:**

- Every exported function of `src/*.ts` except `build.ts` (build tooling with no Go counterpart) and the stdio/SSE plumbing replaced by the go-sdk
- All enrichers except `line-summary`
- Hooks, `.mcp.json`, setup/uninstall commands, release workflow, README updates
- Removal of the TypeScript implementation and Node tooling (final phase, gated on approval)

**Out of scope:**

- New session-capture features or enrichers — This is a port; behaviour parity is the target
- Storing session data through mind-palace's document stores — Different data model; MongoDB stays the only session backend
- Deprecating the `@robbiebyrd/clued` npm package — Boss decides what to do with the published package
- Windows support — The hooks are bash and the host probe uses `uname`; the Node version was not exercised on Windows either
- Merging the daemon into `mind-palace serve` — Different lifecycle; can be revisited later

## Open Questions

- [ ] Distribution: GitHub release binaries downloaded by `/clued-setup` (recommended, decision 10) or `go install` from source at setup time? — Boss
- [ ] Retiring the SSE MCP server on :8086 and the `mcpPort` key (decision 7) — acceptable, or must the HTTP transport stay reachable from the daemon? — Boss
- [ ] Keep the clued config at `~/.claude/plugins/data/clued/config.json` (decision 3) or fold it into `mind-palace.config.yaml`? — Boss
- [ ] Explicit approval to delete `src/`, `enrichers/`, `test/`, `dist/`, the npm/esbuild tooling and `build.yml`/`publish.yml` once verification passes (decision 12). — Boss
- [ ] Should each implementation section get a mind-palace story so subagents log work against it? — Boss
