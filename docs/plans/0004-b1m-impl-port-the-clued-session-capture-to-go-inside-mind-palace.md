---
id: 0004-b1m
title: Port the clued session capture to Go inside mind-palace
type: impl
status: in_progress
priority: "2"
effort: XL
created: "2026-10-10T02:52:36.193Z"
updated: "2026-10-10T02:56:46.663Z"
links:
  repo:
    remote: git@github.com:robbiebyrd/clued.git
    local: ~/Projects/clued
  specs:
    - ./docs/plans/0003-7lv-dsgn-design-port-the-clued-session-capture-from-typescript-to-go-inside-mind-palace.md
  plans:
    - ["0003-7lv", "depends"]
progress:
  "1":
    status: pending
  "1.1":
    status: pending
  "1.2":
    status: pending
  "1.3":
    status: pending
  "2":
    status: pending
  "2.1":
    status: pending
  "2.2":
    status: pending
  "2.3":
    status: pending
  "2.4":
    status: pending
  "2.5":
    status: pending
  "2.6":
    status: pending
  "2.7":
    status: pending
  "2.8":
    status: pending
  "2.9":
    status: pending
  "2.10":
    status: pending
  "3":
    status: pending
  "3.1":
    status: pending
  "3.2":
    status: pending
  "3.3":
    status: pending
  "3.4":
    status: pending
  "4":
    status: pending
  "4.1":
    status: pending
  "4.2":
    status: pending
  "4.3":
    status: pending
  "5":
    status: pending
  "5.1":
    status: pending
  "5.2":
    status: pending
---

# Port the clued session capture to Go inside mind-palace

## Summary

**Goal:** The `mind-palace` binary provides every function of `src/` and the enrichers (daemon, backfill, restore, session MCP tools), the plugin runs it instead of Node, and the TypeScript implementation is removed.

**Problem:** The design in 0003-7lv has to be delivered as twenty-odd Go packages, plugin glue and CI changes; done serially that is weeks of work, but the package boundaries let most of it proceed in parallel.

**Approach:** Five phases. Phase 1 lands the contracts (domain, ports, memory adapter, conformance suite, MCP seam). Phase 2 fans out ten independent subagents over adapters, enrichers and use cases. Phase 3 composes the daemon, the MCP tools and the CLI. Phase 4 wires the plugin, CI and docs in parallel. Phase 5 verifies end to end and, with approval, removes the Node code.

---

# Part 2 — Implementation

**Architecture:** Hexagonal `session` domain in mind-palace (see 0003-7lv for the full package list and the `Store`, `Lookup`, `Enricher` and `Config` contracts). **Orchestration:** the integration branch is `wip/session-port` (off `main`). Each section below is one subagent working in `.worktrees/<section-number>` on branch `wip/session-port/<section-number>`, following superpowers `subagent-driven-development`: the subagent receives the section text, the design plan, the exact interfaces it consumes and produces, the TDD rule, the list of files it may touch and its commit message. Sections marked with the same wave letter run concurrently; the orchestrator merges a wave into `wip/session-port` only when every section's own validation passed, then runs the full gate (`cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go test ./...`) before dispatching the next wave. Waves: **A** = 1.1 ∥ 1.3; **B** = 1.2; **C** = 2.1 … 2.10 (ten agents); **D** = 3.1 ∥ 3.2 ∥ 3.3; **E** = 3.4; **F** = 4.1 ∥ 4.2 ∥ 4.3; **G** = 5.1 then 5.2.

**Tech Stack:** Go 1.26 (module `github.com/robbiebyrd/clued/mind-palace`), standard library `net/http`, `os/exec`, `regexp`, `log/slog`; existing dependencies `go.mongodb.org/mongo-driver/v2`, `github.com/modelcontextprotocol/go-sdk`, `github.com/spf13/cobra`, `golang.org/x/sys/unix`, `golang.org/x/sync/errgroup`. No new dependencies.

## Constraints

- Every section is test-first: write the failing tests named in Step 1, watch them fail, implement, watch them pass, then commit. Tests exercise real logic against `memsession`; no mocks, no stubbed behaviour.
- A subagent edits only the files its section lists (plus `go.sum` if `go mod tidy` touches it) and never reformats files it does not own.
- All Go commands run from `mind-palace/`; `gofmt -l .` must print nothing and `go vet ./...` must pass before every commit.
- Field names written to MongoDB are the snake_case names the TypeScript code uses; collection names, indexes and the `session_full` pipeline are copied from `src/mongo.ts` verbatim.
- Behaviour parity is the acceptance test: when a port decision is unclear, the TypeScript source and its test are the specification.
- Nothing is pushed and no pull request is opened unless Boss asks. The Node code is not deleted before 5.2 and Boss's explicit approval.
- The orchestrator tracks progress with `mind-palace plan set-progress <this plan> <section> <status>` after each merge (this session has no TodoWrite tool; the plan's progress keys are the todo list).

## Files

| File | Change | Responsibility |
|---|---|---|
| `mind-palace/session/doc.go` | create | `Doc` map type and accessor helpers shared by enrichers, query and restore |
| `mind-palace/session/model.go` | create | `HostInfo`, `Session`, `TranscriptLine`, `SubagentLine`, `Blob`, query structs, `ErrNotFound` |
| `mind-palace/session/store.go` | create | `Store` and `Lookup` ports |
| `mind-palace/session/enricher.go` | create | `Enricher` struct, `Register`, `Active` |
| `mind-palace/session/config.go` | create | `Config`, `DefaultConfigPath`, `LoadConfig` (file, env, `~` expansion) |
| `mind-palace/session/account.go` | create | `ReadAccountID` |
| `mind-palace/session/lang.go` | create | `ExtToLang` table |
| `mind-palace/session/paths.go` | create | `DecodeProjectPath`, `EncodeProjectPath` |
| `mind-palace/session/memsession/memsession.go` | create | In-memory `Store` |
| `mind-palace/session/sessiontest/sessiontest.go` | create | Store conformance suite |
| `mind-palace/session/mongosession/mongosession.go` | create | MongoDB `Store`: collections, indexes, `session_full` view, queries |
| `mind-palace/session/tail/tail.go` | create | `TailFile`, `WatchDir` |
| `mind-palace/session/tail/artifacts.go` | create | `WatchArtifacts` (subagents, tool-results, file-history) |
| `mind-palace/session/wal/wal.go` | create | `Append`, `Flush` |
| `mind-palace/session/gitinfo/gitinfo.go` | create | `Origin`, `Branch` |
| `mind-palace/session/hostinfo/hostinfo.go` | create | `Read` |
| `mind-palace/session/enrichers/*.go` | create | One file per enricher, self-registering |
| `mind-palace/session/enrich/loop.go` | create | Enrichment loop |
| `mind-palace/session/query/query.go` | create | `FindSessions`, `SearchCommands`, `SessionContext`, `FullSession`, `ReadTranscript` |
| `mind-palace/session/restore/restore.go` | create | `Session` restore |
| `mind-palace/session/backfill/backfill.go` | create | `Run` |
| `mind-palace/session/daemon/daemon.go` | create | Ingest HTTP handler, session tracking, WAL flush, enrichment loop, `Run` |
| `mind-palace/session/sessionmcp/sessionmcp.go` | create | `Tools` provider: six MCP tools, standalone server |
| `mind-palace/server/mcpserver/mcpserver.go` | modify | `ToolProvider` interface and `Options.Tools` |
| `mind-palace/cli/session.go` | create | `session` command group |
| `mind-palace/cli/cli.go` | modify | Register the group; pass `sessionmcp` to `mcp` and `serve` |
| `mind-palace/plugin/register_hooks_test.go` | create | Go port of `register-hooks.test.ts` |
| `bin/clued` | create | Binary-resolving shell shim used by `.mcp.json` and the hooks |
| `bin/clued.cjs` | delete | npm bin (removed in 5.2) |
| `.mcp.json` | modify | `${CLAUDE_PLUGIN_ROOT}/bin/clued session mcp` |
| `hooks/session-start` | modify | Start the Go daemon and backfill through the shim; drop the MCP server start and `dist/` rsync |
| `hooks/uninstall` | modify | Stop the Go daemon, remove the downloaded binary |
| `commands/clued-setup.md` | modify | Download or build the binary |
| `skills/clued-setup.md` | modify | Same |
| `.github/workflows/release.yml` | create | Cross-compile and attach release binaries |
| `.github/workflows/mind-palace.yml` | modify | MongoDB service for the gated tests |
| `.github/workflows/build.yml` | delete | Node dist rebuild (removed in 5.2) |
| `.github/workflows/publish.yml` | delete | npm publish (removed in 5.2) |
| `README.md` | modify | Go-based install, verify, config and MCP sections |
| `mind-palace/README.md` | modify | Sessions section |
| `src/` | delete | TypeScript implementation (removed in 5.2) |
| `enrichers/` | delete | TypeScript enrichers (removed in 5.2) |
| `test/` | delete | TypeScript tests (removed in 5.2) |
| `dist/` | delete | Bundles (removed in 5.2) |
| `package.json` | delete | With `pnpm-lock.yaml`, `tsconfig.json`; `mise.toml` loses node/pnpm (removed in 5.2) |

## Phase 1: Contracts

### 1.1: Domain types, ports, registry, config (wave A)

**Files:**
- Create: `mind-palace/session/doc.go`
- Create: `mind-palace/session/model.go`
- Create: `mind-palace/session/store.go`
- Create: `mind-palace/session/enricher.go`
- Create: `mind-palace/session/config.go`
- Create: `mind-palace/session/account.go`
- Create: `mind-palace/session/lang.go`
- Create: `mind-palace/session/paths.go`
- Test: `mind-palace/session/doc_test.go`
- Test: `mind-palace/session/enricher_test.go`
- Test: `mind-palace/session/config_test.go`
- Test: `mind-palace/session/account_test.go`
- Test: `mind-palace/session/lang_test.go`
- Test: `mind-palace/session/paths_test.go`

**Depends on:** none

**Interfaces:**
- Consumes: `src/config.ts`, `src/account.ts`, `enrichers/lang.ts`, `src/backfill.ts` (`decodeProjectPath`), `src/restore.ts` (encoding), `src/enricher.ts` (`Enricher` shape), `src/mongo.ts` (document fields)
- Produces: Package `session` exactly as the design's component 1 declares it (`Doc` helpers, model structs with snake_case json/bson tags, `SessionQuery`/`LineQuery`/`CommandQuery`, `ErrNotFound`, `Lookup`, `Store`, `Enricher`, `Register`, `Active`, `Config`, `DefaultConfigPath`, `LoadConfig`, `ReadAccountID`, `ExtToLang`, `DecodeProjectPath`, `EncodeProjectPath`)

- [ ] **Step 1: Write the failing tests** — `config_test.go`: the 14 cases of `config.test.ts` (defaults, file overrides, env overrides, `~` expansion for projectsDir/mongoUrl/claudeAppConfigPath/fileHistoryDir, walPath next to the config file, `CLUED_WAL_PATH`). `account_test.go`: the 4 cases of `account.test.ts` (capture stderr and assert the warning text). `lang_test.go`: the 9 cases of `lang.test.ts`. `paths_test.go`: the 2 `decodeProjectPath` cases plus encode/decode round trip. `doc_test.go`: accessors on nested, missing and wrongly typed keys. `enricher_test.go`: `Register` panics on duplicate, `Active` drops `Enabled: false` and names in `disabled`, sorted output (the 3 loading cases of `enricher.test.ts`).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session`
- [ ] **Step 3: Implement** — Types and helpers per the design; `LoadConfig` reads the JSON file leniently (absent or unparsable → defaults), applies the seven `CLUED_*` variables, expands `~/`; `ReadAccountID` warns on stderr via `log` with the exact clued message.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./session && go test ./session`
- [ ] **Step 5: Commit** — `git commit -m 'feat(session): domain types, ports, enricher registry and config'`

**Validation:** `go test ./session`

### 1.2: Memory adapter and conformance suite (wave B)

**Files:**
- Create: `mind-palace/session/memsession/memsession.go`
- Create: `mind-palace/session/sessiontest/sessiontest.go`
- Test: `mind-palace/session/memsession/memsession_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `session.Store`; `src/mongo.ts` semantics (`$setOnInsert`, unique keys, view pipeline); `test/integration/mongo.test.ts`
- Produces: `memsession.New() *Store` (implements `session.Store`); `sessiontest.Run(t *testing.T, open func(*testing.T) session.Store)`

- [ ] **Step 1: Write the conformance suite** — `sessiontest.Run` with subtests for: session upsert sets fields and `started_at` once, `TouchSession` only updates `last_seen` for the owning account, `SetGitInfoIfMissing` is a no-op when `git_origin` exists, `GetSession`/`FindSessions` filters, projection and `last_seen` ordering with limit, hook event insert stamps nothing extra, transcript/subagent/blob upserts keyed as the unique indexes (`created_at` preserved, content replaced), `CountTranscriptLines`, `TranscriptLines` offset/limit/descending without `_id`, `SubagentIDs`/`SubagentLines` ordering, `Blobs`, `BashEvents` regex and session scoping newest first, `FullSession` shape (the 8 cases of `mongo.test.ts` including the view join with stripped fields and no blob content), `Unenriched`/`SetEnrichment`/`SetEnrichmentFailure` selection rules, `HookEventIDsByToolUse`. `memsession_test.go` calls `sessiontest.Run` with `memsession.New`.
- [ ] **Step 2: Run it and confirm it fails** — `go test ./session/memsession`
- [ ] **Step 3: Implement memsession** — Mutex-guarded slices/maps per collection; deep-copy documents on write and read; `(?i)` regexes; `FullSession` joins and sorts as the view pipeline; `_id` values are strings from a counter.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./session/... && go test ./session/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(session): in-memory store and conformance suite'`

**Validation:** `go test ./session/memsession`

### 1.3: MCP tool-provider seam (wave A)

**Files:**
- Modify: `mind-palace/server/mcpserver/mcpserver.go`
- Test: `mind-palace/server/mcpserver/mcpserver_test.go`

**Depends on:** none

**Interfaces:**
- Consumes: Existing `mcpserver.New`, `Options`
- Produces: `type ToolProvider interface { AddTools(s *mcp.Server, prefix string) }`; `Options.Tools []ToolProvider`; `Options.ExtraInstructions string` appended to the server instructions

- [ ] **Step 1: Write the failing test** — A test provider that adds one tool; `New` with it in `Options.Tools` lists the tool (with the prefix applied) next to the kind tools, and the instructions contain `ExtraInstructions`.
- [ ] **Step 2: Run it and confirm it fails** — `go test ./server/mcpserver`
- [ ] **Step 3: Implement** — Call each provider after `addTools`; concatenate the instructions.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./server/... && go test ./server/...`
- [ ] **Step 5: Commit** — `git commit -m 'feat(mcpserver): accept external tool providers'`

**Validation:** `go test ./server/mcpserver`

## Phase 2: Adapters, enrichers and use cases (wave C: ten subagents)

### 2.1: MongoDB adapter

**Files:**
- Create: `mind-palace/session/mongosession/mongosession.go`
- Test: `mind-palace/session/mongosession/mongosession_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `session.Store`, `sessiontest.Run`, `src/mongo.ts` (collections, 14 indexes, `SESSION_FULL_PIPELINE`), `src/mcp.ts`/`restore.ts`/`daemon.ts` queries
- Produces: `mongosession.New(cfg session.Config) *Store`; `(*Store).Connect(ctx) error`; implements `session.Store`

- [ ] **Step 1: Write the failing tests** — `sessiontest.Run` gated on `MIND_PALACE_TEST_MONGODB_URI` with a per-run database dropped on cleanup; a test that `Connect` twice leaves exactly one `session_full` view with the expected pipeline; a test that an existing non-unique index on `transcript_lines` produces a logged warning, not an error.
- [ ] **Step 2: Run them and confirm they fail** — `MIND_PALACE_TEST_MONGODB_URI=mongodb://localhost:27017 go test ./session/mongosession`
- [ ] **Step 3: Implement** — Same collection names, index models and view pipeline as `mongo.ts`; `UpsertTranscriptLines` as an unordered `BulkWrite` of upserts; filters with `$regex`/`$options: i`, `$in`, `$exists`; projections and sorts as in the TypeScript queries; `Unenriched` filters on `enrichments.<name>` and `enrichments.<name>_failed`; `Close` disconnects.
- [ ] **Step 4: Run the tests, gofmt and vet** — `gofmt -l . && go vet ./session/... && MIND_PALACE_TEST_MONGODB_URI=mongodb://localhost:27017 go test ./session/mongosession`
- [ ] **Step 5: Commit** — `git commit -m 'feat(session): MongoDB store adapter'`

**Validation:** `MIND_PALACE_TEST_MONGODB_URI=mongodb://localhost:27017 go test ./session/mongosession`

### 2.2: File tailer and artifact watchers

**Files:**
- Create: `mind-palace/session/tail/tail.go`
- Create: `mind-palace/session/tail/artifacts.go`
- Test: `mind-palace/session/tail/tail_test.go`
- Test: `mind-palace/session/tail/artifacts_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `session.Store` (via `memsession` in tests), `src/tailer.ts`, `src/artifact-watcher.ts`
- Produces: `tail.Options{Interval, WaitInterval time.Duration; Logger *slog.Logger}`; `tail.TailFile(path string, onLine func(string), opts Options) (stop func())`; `tail.WatchDir(path string, onChange func(name, fullPath string), opts Options) (stop func())`; `tail.WatchArtifacts(sessionID, sessionDir, fileHistoryPath, accountID string, host *session.HostInfo, store session.Store, opts Options) (stop func())`

- [ ] **Step 1: Write the failing tests** — `tail_test.go`: the 4 cases of `tailer.test.ts` (lines present at start, appended later, file appears later, stop halts delivery) with 10 ms intervals and a partial trailing line delivered only once completed. `artifacts_test.go`: the 5 cases of `artifact-watcher.test.ts` against `memsession` (tool-result blob, base64 file-history blob, subagent JSONL tailing plus meta blob, content update on overwrite, no duplicate tailer when mtime changes).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/tail`
- [ ] **Step 3: Implement** — Poll loops on goroutines with a stop channel; offset-based reads with `bufio`; mtime map for `WatchDir`; `WatchArtifacts` composes the three watchers and keeps a per-subagent seq counter.
- [ ] **Step 4: Run the tests with the race detector, gofmt and vet** — `gofmt -l . && go vet ./session/tail && go test -race ./session/tail`
- [ ] **Step 5: Commit** — `git commit -m 'feat(session): poll-based tailer and artifact watchers'`

**Validation:** `go test -race ./session/tail`

### 2.3: Write-ahead log

**Files:**
- Create: `mind-palace/session/wal/wal.go`
- Test: `mind-palace/session/wal/wal_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `src/wal.ts`
- Produces: `wal.Append(path string, event session.Doc) error`; `wal.Flush(ctx context.Context, path string, insert func(context.Context, session.Doc) error) error`

- [ ] **Step 1: Write the failing tests** — The 6 cases of `wal.test.ts`: append creates the file with one JSON line, appends keep separate lines, flush inserts every entry and empties the file, flush retains only failed entries, flush without a file is a no-op, malformed lines are skipped.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/wal`
- [ ] **Step 3: Implement** — `go test ./session/wal`
- [ ] **Step 4: gofmt, vet and commit** — `gofmt -l . && go vet ./session/wal && git commit -m 'feat(session): write-ahead log'`

**Validation:** `go test ./session/wal`

### 2.4: Git and host probes

**Files:**
- Create: `mind-palace/session/gitinfo/gitinfo.go`
- Create: `mind-palace/session/hostinfo/hostinfo.go`
- Test: `mind-palace/session/gitinfo/gitinfo_test.go`
- Test: `mind-palace/session/hostinfo/hostinfo_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `src/git.ts`, `src/host.ts`, `session.HostInfo`
- Produces: `gitinfo.Origin(ctx, dir string) string`; `gitinfo.Branch(ctx, dir string) string`; `hostinfo.Read() session.HostInfo`

- [ ] **Step 1: Write the failing tests** — `gitinfo_test.go`: the 7 cases of `git.test.ts` using temporary repositories created with `git init`, `git remote add`, a detached HEAD, a plain directory and a missing path. `hostinfo_test.go`: the 11 shape cases of `host.test.ts` (IPv4 and MAC formats when present, non-empty hostname/username/platform/arch/release/type, numeric uid).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/gitinfo ./session/hostinfo`
- [ ] **Step 3: Implement** — `exec.CommandContext` with a 2 s timeout, trimmed stdout, `""` on any error; `net.Interfaces` for the first up, non-loopback interface with an IPv4 address; `unix.Uname` for release and type.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./session/gitinfo ./session/hostinfo && go test ./session/gitinfo ./session/hostinfo && git commit -m 'feat(session): git and host probes'`

**Validation:** `go test ./session/gitinfo ./session/hostinfo`

### 2.5: Enrichers A — hook events

**Files:**
- Create: `mind-palace/session/enrichers/bash_binaries.go`
- Create: `mind-palace/session/enrichers/bash_outcome.go`
- Create: `mind-palace/session/enrichers/edit_diff_stats.go`
- Create: `mind-palace/session/enrichers/file_tracker.go`
- Create: `mind-palace/session/enrichers/prompt_features.go`
- Create: `mind-palace/session/enrichers/tool_classifier.go`
- Test: `mind-palace/session/enrichers/bash_binaries_test.go`
- Test: `mind-palace/session/enrichers/bash_outcome_test.go`
- Test: `mind-palace/session/enrichers/edit_diff_stats_test.go`
- Test: `mind-palace/session/enrichers/file_tracker_test.go`
- Test: `mind-palace/session/enrichers/prompt_features_test.go`
- Test: `mind-palace/session/enrichers/tool_classifier_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: `session.Enricher`, `session.Register`, `session.Doc`, `session.ExtToLang`; the six TypeScript modules and their tests
- Produces: Six registered enrichers with the same names, collection `hook_events`, result structs with snake_case tags

- [ ] **Step 1: Write the failing tests** — Port every case of `bash-outcome.test.ts` (8), `edit-diff-stats.test.ts` (7), `file-tracker.test.ts` (15), `prompt-features.test.ts` (14), `tool-classifier.test.ts` (13); `bash-binaries` has no TypeScript test, so write cases for pipelines, `&&`/`||`/`;`/newline separators, env-var prefixes, quoting, path stripping, builtin filtering and de-duplication. Each file also asserts name, collection and `Enabled`.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/enrichers -run 'BashBinaries|BashOutcome|EditDiffStats|FileTracker|PromptFeatures|ToolClassifier'`
- [ ] **Step 3: Implement** — One file per enricher with `func init() { session.Register(...) }`; RE2 translations of the JavaScript patterns; `file_tracker` keeps the directory-before-filename priority.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./session/enrichers && go test ./session/enrichers && git commit -m 'feat(session): hook-event enrichers'`

**Validation:** `go test ./session/enrichers -run 'BashBinaries|BashOutcome|EditDiffStats|FileTracker|PromptFeatures|ToolClassifier'`

### 2.6: Enrichers B — transcript lines, part 1

**Files:**
- Create: `mind-palace/session/enrichers/attachment_extractor.go`
- Create: `mind-palace/session/enrichers/code_language_detector.go`
- Create: `mind-palace/session/enrichers/error_flag.go`
- Create: `mind-palace/session/enrichers/file_snapshot_extractor.go`
- Create: `mind-palace/session/enrichers/intent_classifier.go`
- Create: `mind-palace/session/enrichers/line_classifier.go`
- Test: `mind-palace/session/enrichers/attachment_extractor_test.go`
- Test: `mind-palace/session/enrichers/code_language_detector_test.go`
- Test: `mind-palace/session/enrichers/error_flag_test.go`
- Test: `mind-palace/session/enrichers/file_snapshot_extractor_test.go`
- Test: `mind-palace/session/enrichers/intent_classifier_test.go`
- Test: `mind-palace/session/enrichers/line_classifier_test.go`

**Depends on:** 1.1

**Interfaces:**
- Consumes: Same as 2.5; the six TypeScript modules and their tests
- Produces: Six registered enrichers on `transcript_lines`

- [ ] **Step 1: Write the failing tests** — Port every case of `attachment-extractor.test.ts` (7), `code-language-detector.test.ts` (7), `error-flag.test.ts` (10), `file-snapshot-extractor.test.ts` (9), `intent-classifier.test.ts` (12), `line-classifier.test.ts` (12).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/enrichers -run 'AttachmentExtractor|CodeLanguageDetector|ErrorFlag|FileSnapshotExtractor|IntentClassifier|LineClassifier'`
- [ ] **Step 3: Implement** — `ERROR_TEXT_RE` and the intent regexes as RE2 (the `\b(what|how|…)` openers and `exit code [^0]` port directly); fence detection with `(?m)^```(\w+)\s*$`.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./session/enrichers && go test ./session/enrichers && git commit -m 'feat(session): transcript enrichers (classification and extraction)'`

**Validation:** `go test ./session/enrichers -run 'AttachmentExtractor|CodeLanguageDetector|ErrorFlag|FileSnapshotExtractor|IntentClassifier|LineClassifier'`

### 2.7: Enrichers C — transcript lines, part 2 and hook-linker

**Files:**
- Create: `mind-palace/session/enrichers/message_stats.go`
- Create: `mind-palace/session/enrichers/privacy_redact.go`
- Create: `mind-palace/session/enrichers/skill_detector.go`
- Create: `mind-palace/session/enrichers/thinking_stats.go`
- Create: `mind-palace/session/enrichers/tool_use_summary.go`
- Create: `mind-palace/session/enrichers/hook_linker.go`
- Test: `mind-palace/session/enrichers/message_stats_test.go`
- Test: `mind-palace/session/enrichers/privacy_redact_test.go`
- Test: `mind-palace/session/enrichers/skill_detector_test.go`
- Test: `mind-palace/session/enrichers/thinking_stats_test.go`
- Test: `mind-palace/session/enrichers/tool_use_summary_test.go`
- Test: `mind-palace/session/enrichers/hook_linker_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: Same as 2.5 plus `session.Lookup` (`memsession` in the hook-linker test); the six TypeScript modules and their tests
- Produces: Six registered enrichers on `transcript_lines`; `privacy-redact` with `Enabled: false`; `hook-linker` with `BatchLimit: 20`

- [ ] **Step 1: Write the failing tests** — Port every case of `message-stats.test.ts` (6), `privacy-redact.test.ts` (8), `skill-detector.test.ts` (6), `thinking-stats.test.ts` (8), `tool-use-summary.test.ts` (7) and `integration/hook-linker.test.ts` (6, against `memsession` with inserted hook events).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/enrichers -run 'MessageStats|PrivacyRedact|SkillDetector|ThinkingStats|ToolUseSummary|HookLinker'`
- [ ] **Step 3: Implement** — `privacy_redact` JSON-encodes non-string lines before applying the four patterns (the JavaScript `\b…\b` email/JWT patterns need explicit boundaries in RE2); `hook_linker` collects ids from `attachment.toolUseID` and `tool_use` blocks, de-duplicates, and calls `Lookup`.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./session/enrichers && go test ./session/enrichers && git commit -m 'feat(session): transcript enrichers (stats, redaction, hook linking)'`

**Validation:** `go test ./session/enrichers -run 'MessageStats|PrivacyRedact|SkillDetector|ThinkingStats|ToolUseSummary|HookLinker'`

### 2.8: Enrichment loop

**Files:**
- Create: `mind-palace/session/enrich/loop.go`
- Test: `mind-palace/session/enrich/loop_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `session.Store`, `session.Enricher`, `memsession`
- Produces: `enrich.Loop(ctx context.Context, store session.Store, enrichers []session.Enricher, interval time.Duration, logger *slog.Logger)` (blocks until ctx ends; one pass per tick); `enrich.Pass(ctx, store, enrichers, logger) error` for tests and a future one-shot command

- [ ] **Step 1: Write the failing tests** — The 4 loop cases of `enricher.test.ts` with ad-hoc enrichers against `memsession`: result written under `enrichments.<name>`, failure written under `enrichments.<name>_failed` with message and time, non-matching docs left untouched, batch limit respected; plus: a store query error is logged and the loop continues.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/enrich`
- [ ] **Step 3: Implement** — `go test ./session/enrich`
- [ ] **Step 4: gofmt, vet and commit** — `gofmt -l . && go vet ./session/enrich && git commit -m 'feat(session): enrichment loop'`

**Validation:** `go test ./session/enrich`

### 2.9: Query and restore use cases

**Files:**
- Create: `mind-palace/session/query/query.go`
- Create: `mind-palace/session/restore/restore.go`
- Test: `mind-palace/session/query/query_test.go`
- Test: `mind-palace/session/restore/restore_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `session.Store`, `memsession`; `src/mcp.ts` tool functions; `src/restore.ts`
- Produces: `query.Service{Store session.Store; AccountID string}` with `FindSessions(ctx, FindSessionsArgs) ([]session.Doc, error)`, `SearchCommands(ctx, SearchCommandsArgs) ([]CommandHit, error)`, `SessionContext(ctx, sessionID string) (Context, error)`, `FullSession(ctx, sessionID string) (session.Doc, error)`, `ReadTranscript(ctx, ReadTranscriptArgs, progress func(done, total int)) ([]session.Doc, error)`; argument structs with the tool parameter names and defaults (10/20/200, cap 500); `restore.Args{SessionID, ProjectPath, ProjectsDir string}`, `restore.Result{FilesWritten, BytesWritten int; Missing []string}`, `restore.Session(ctx, store session.Store, accountID string, args Args, fileHistoryDir string) (Result, error)`

- [ ] **Step 1: Write the failing tests** — `query_test.go`: the query-level cases of `integration/mcp.test.ts` against `memsession` (find by git_origin/project_path, empty result, `event_count`; search across sessions, scoped by git_origin, no match, foreign `session_id` → empty; context metadata/top_commands/first and last lines, unknown and foreign session → `ErrNotFound`; read_transcript pagination and progress callbacks per batch; full session and its errors; account isolation for every call). `restore_test.go`: the cases of `integration/restore.test.ts` (transcript, subagents, three blob types, `missing` list, `project_path` override, no path information error) writing into `t.TempDir()`.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/query ./session/restore`
- [ ] **Step 3: Implement** — `go test ./session/query ./session/restore`
- [ ] **Step 4: gofmt, vet and commit** — `gofmt -l . && go vet ./session/query ./session/restore && git commit -m 'feat(session): query and restore use cases'`

**Validation:** `go test ./session/query ./session/restore`

### 2.10: Backfill

**Files:**
- Create: `mind-palace/session/backfill/backfill.go`
- Test: `mind-palace/session/backfill/backfill_test.go`

**Depends on:** 1.2

**Interfaces:**
- Consumes: `session.Store`, `session.DecodeProjectPath`, `memsession`; `src/backfill.ts`
- Produces: `backfill.GitInfo func(ctx context.Context, dir string) (origin, branch string)`; `backfill.Run(ctx, store session.Store, projectsDir, accountID string, git GitInfo, logger *slog.Logger) (Stats, error)` with `Stats{Sessions, Lines int}`

- [ ] **Step 1: Write the failing tests** — The 7 cases of `integration/backfill.test.ts` against `memsession` and a temporary projects tree (decode, upsert sessions and lines, idempotent re-run, empty directory, git origin via an injected `GitInfo`, account stamping) plus: a malformed line is stored as `{raw}` and an unreadable file is logged and skipped.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/backfill`
- [ ] **Step 3: Implement** — `errgroup.SetLimit(5)`; per-session upsert with `last_seen` now and `started_at` on insert; one `UpsertTranscriptLines` per file.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./session/backfill && go test ./session/backfill && git commit -m 'feat(session): backfill'`

**Validation:** `go test ./session/backfill`

## Phase 3: Composition and entrypoints

### 3.1: Ingest daemon (wave D)

**Files:**
- Create: `mind-palace/session/daemon/daemon.go`
- Test: `mind-palace/session/daemon/daemon_test.go`

**Depends on:** 2.1, 2.2, 2.3, 2.8

**Interfaces:**
- Consumes: `session.Store`, `tail.TailFile`, `tail.WatchArtifacts`, `wal.Append`/`Flush`, `enrich.Loop`, `backfill.GitInfo` signature for git lookups
- Produces: `daemon.Options{Config session.Config; Store session.Store; Enrichers []session.Enricher; AccountID string; Host session.HostInfo; Git backfill.GitInfo; Logger *slog.Logger; Tail tail.Options}`; `daemon.New(Options) *Daemon`; `(*Daemon).Handler() http.Handler`; `(*Daemon).Run(ctx, addr string) error`; `daemon.ErrAddrInUse`

- [ ] **Step 1: Write the failing tests** — `httptest` against `memsession`: the 7 cases of `integration/daemon.test.ts` (health, event stored with `account_id`/`host`/`created_at`, session created and transcript tailed from a temp file, 404 on unknown routes, git origin set at creation, absent for a non-git cwd, filled in when a later event carries a git cwd) plus WAL fallback when the store's insert fails (wrap `memsession` in a failing `Store`), WAL flush on start, 400 on malformed JSON, and `Run` returning `ErrAddrInUse` when the port is bound.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/daemon`
- [ ] **Step 3: Implement** — Tracked-session map guarded by a mutex; tailers and watchers stopped on shutdown; 60 s WAL ticker; enrichment loop goroutine; `http.Server` with `ReadHeaderTimeout`; bind address from `Options`.
- [ ] **Step 4: gofmt, vet, test (race) and commit** — `gofmt -l . && go vet ./session/daemon && go test -race ./session/daemon && git commit -m 'feat(session): ingest daemon'`

**Validation:** `go test -race ./session/daemon`

### 3.2: Session MCP tools (wave D)

**Files:**
- Create: `mind-palace/session/sessionmcp/sessionmcp.go`
- Test: `mind-palace/session/sessionmcp/sessionmcp_test.go`

**Depends on:** 1.3, 2.9

**Interfaces:**
- Consumes: `mcpserver.ToolProvider`, `query.Service`, `restore.Session`, go-sdk `mcp.Server`/`mcp.Client`/`mcp.NewInMemoryTransports`; `src/mcp-protocol.ts` tool schemas
- Produces: `sessionmcp.New(cfg session.Config, connect func(ctx) (session.Store, error), accountID string) *Tools`; `(*Tools).AddTools(s *mcp.Server, prefix string)`; `(*Tools).Server() *mcp.Server` (name `clued`); `sessionmcp.Instructions` (one paragraph for the shared server)

- [ ] **Step 1: Write the failing tests** — Drive a real `mcp.Client` over in-memory transports against `Tools.Server()` backed by `memsession`: the tool list has the six names with the schemas from `mcp-protocol.ts`; every tool case of `integration/mcp.test.ts` (results as JSON text, errors as `IsError` with the message, account isolation, progress notifications before the final `read_transcript` result); `restore_session` writes into a temp dir; the lazy `connect` is called once on first tool call.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./session/sessionmcp`
- [ ] **Step 3: Implement** — Tools registered with raw JSON input schemas; `req.Session.NotifyProgress` when `req.Params.Meta.ProgressToken` is set; store connected once under a mutex.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./session/sessionmcp && go test ./session/sessionmcp && git commit -m 'feat(session): MCP session tools'`

**Validation:** `go test ./session/sessionmcp`

### 3.3: CLI session group — query, restore, backfill (wave D)

**Files:**
- Create: `mind-palace/cli/session.go`
- Modify: `mind-palace/cli/cli.go`
- Test: `mind-palace/cli/session_test.go`

**Depends on:** 2.1, 2.4, 2.9, 2.10

**Interfaces:**
- Consumes: `session.LoadConfig`, `session.ReadAccountID`, `mongosession.New`, `query.Service`, `restore.Session`, `backfill.Run`, `gitinfo`, existing `App.print`/`fail`/`exitCodes`
- Produces: `mind-palace session` group with persistent `--config`; subcommands `find`, `search-commands`, `context`, `full`, `transcript`, `restore`, `backfill`; `App.sessionStore(ctx) (session.Store, error)` and `App.sessionConfig()` for 3.4; `session.ErrNotFound` → exit 4; store errors → exit 10

- [ ] **Step 1: Write the failing tests** — `session_test.go` runs the group through `cli.App` with `Stdout`/`Stderr` buffers and a `memsession` injected through a package-level `openStore` variable (overridden in tests only): each subcommand prints the `{ok:true,result}` envelope with the expected shape, limits and flags map to the argument structs, unknown session exits 4 with the error envelope, `backfill` reports counts, `restore` writes to a temp dir. One test asserts `cli.go` only gained the `session` group (the plan/story groups' command lists are unchanged).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./cli`
- [ ] **Step 3: Implement** — Cobra commands mirroring the existing style in `cli.go`; `--config` defaults to `session.DefaultConfigPath()`; `backfill` uses `gitinfo` for `GitInfo`.
- [ ] **Step 4: gofmt, vet, test and commit** — `gofmt -l . && go vet ./cli && go test ./cli && git commit -m 'feat(cli): session query, restore and backfill commands'`

**Validation:** `go test ./cli`

### 3.4: CLI daemon and MCP wiring (wave E)

**Files:**
- Modify: `mind-palace/cli/session.go`
- Modify: `mind-palace/cli/cli.go`
- Test: `mind-palace/cli/session_test.go`
- Test: `mind-palace/cli/cli_test.go`

**Depends on:** 3.1, 3.2, 3.3

**Interfaces:**
- Consumes: `daemon.New`/`Run`, `sessionmcp.New`/`Server`/`AddTools`/`Instructions`, `mcpserver.Options.Tools`, `hostinfo.Read`, `session.Active`
- Produces: `mind-palace session daemon [--addr]` (exit 0 on `ErrAddrInUse`, 1 on connect failure), `mind-palace session mcp [--mcp-tool-prefix]`; `mind-palace mcp` and `mind-palace serve` include the session tools

- [ ] **Step 1: Write the failing tests** — `session daemon` on a free port answers `/health` and stops on context cancel; a second instance on the same port exits 0; `session mcp` over stdio pipes answers `initialize` and lists the six tools; `mind-palace mcp` (existing stdio test pattern in `cli_test.go`) lists `plan_*`, `story_*` and the session tools.
- [ ] **Step 2: Run them and confirm they fail** — `go test ./cli`
- [ ] **Step 3: Implement** — `go test ./cli`
- [ ] **Step 4: Build the binary and smoke-test against a live MongoDB** — `go build -o mind-palace ./cmd/mind-palace && CLUED_MONGO_URL=mongodb://localhost:27017 CLUED_DB_NAME=clued_smoke ./mind-palace session daemon & sleep 1; curl -sf http://127.0.0.1:8085/health; kill %1`
- [ ] **Step 5: gofmt, vet, test and commit** — `gofmt -l . && go vet ./... && go test ./... && git commit -m 'feat(cli): session daemon and MCP commands'`

**Validation:** `go test ./cli && go build ./...`

## Phase 4: Plugin integration (wave F)

### 4.1: Shim, hooks, setup and uninstall

**Files:**
- Create: `bin/clued`
- Create: `mind-palace/plugin/register_hooks_test.go`
- Modify: `.mcp.json`
- Modify: `hooks/session-start`
- Modify: `hooks/uninstall`
- Modify: `hooks/check-setup`
- Modify: `commands/clued-setup.md`
- Modify: `skills/clued-setup.md`
- Modify: `commands/clued-uninstall.md`
- Test: `mind-palace/plugin/shim_test.go`

**Depends on:** 3.4

**Interfaces:**
- Consumes: `mind-palace session daemon|backfill|mcp`; config keys `port`, `devRepoPath`
- Produces: `bin/clued <args>` resolving `~/.claude/plugins/data/clued/bin/mind-palace`, then `mind-palace` on PATH, then a build from `$devRepoPath/mind-palace` (rebuilt when any `.go` file is newer than the binary); hooks that start, check and stop the Go daemon

- [ ] **Step 1: Write the failing tests** — `register_hooks_test.go`: the 8 cases of `register-hooks.test.ts` running `hooks/register-hooks` with `CLAUDE_CONFIG_DIR` pointing at a temp dir (skip when `jq` is absent). `shim_test.go`: `bin/clued` prefers the downloaded binary, falls back to PATH, builds from `devRepoPath` when set, and fails with a clear message otherwise (use fake executables in temp dirs).
- [ ] **Step 2: Run them and confirm they fail** — `go test ./plugin`
- [ ] **Step 3: Implement** — `bin/clued` in bash; `session-start` runs `bin/clued session daemon` behind the health check and `bin/clued session backfill`, no MCP server start, no rsync; `.mcp.json` → `{"clued": {"command": "${CLAUDE_PLUGIN_ROOT}/bin/clued", "args": ["session", "mcp"]}}`; `uninstall` pkills `session daemon` and removes `~/.claude/plugins/data/clued/bin`; setup command and skill download the release asset for `uname -s`/`uname -m` (or build when `go` is present and no asset matches) and no longer mention `mcpPort` or port 8086.
- [ ] **Step 4: Test and commit** — `go test ./plugin && git commit -m 'feat(plugin): run the Go binary from the hooks and .mcp.json'`

**Validation:** `go test ./plugin && bash -n bin/clued hooks/session-start hooks/uninstall`

### 4.2: Release workflow and CI MongoDB

**Files:**
- Create: `.github/workflows/release.yml`
- Modify: `.github/workflows/mind-palace.yml`

**Depends on:** 3.4

**Interfaces:**
- Consumes: Existing `mind-palace.yml`
- Produces: `release.yml`: on tags `v*`, matrix `darwin/linux × amd64/arm64`, `CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=$TAG'`, assets `mind-palace_<os>_<arch>` attached with `gh release upload`; `mind-palace.yml`: `services: mongo` (image `mongo:7`, port 27017) and `MIND_PALACE_TEST_MONGODB_URI` in the test step

- [ ] **Step 1: Write the workflows** — Validate the YAML with `actionlint` if installed, otherwise with a Python YAML load.
- [ ] **Step 2: Run the gated suite locally the way CI will** — `MIND_PALACE_TEST_MONGODB_URI=mongodb://localhost:27017 go test ./...`
- [ ] **Step 3: Commit** — `git commit -m 'ci: release binaries and MongoDB-backed tests'`

**Validation:** `python3 -c 'import yaml,sys; [yaml.safe_load(open(p)) for p in sys.argv[1:]]' ../.github/workflows/release.yml ../.github/workflows/mind-palace.yml`

### 4.3: Documentation

**Files:**
- Modify: `README.md`
- Modify: `mind-palace/README.md`

**Depends on:** 3.4

**Interfaces:**
- Consumes: Final command names and flags from 3.4 and 4.1
- Produces: README sections: prerequisites without Node, setup (binary download), verify (`bin/clued session daemon` health, `mind-palace session find`), manual configuration (config.json keys without `mcpPort`), MCP registration (stdio via `.mcp.json`; `mind-palace serve` for Streamable HTTP), session commands; `mind-palace/README.md`: a Sessions section (config file, commands, MCP tools, store layout, test gate)

- [ ] **Step 1: Update both READMEs** — Every command shown is run once against the built binary and its output pasted or paraphrased accurately.
- [ ] **Step 2: Check links and commands** — `grep -n 'dist/\|npx\|node ' ../README.md; test $? -eq 1`
- [ ] **Step 3: Commit** — `git commit -m 'docs: describe the Go session capture'`

**Validation:** `grep -c 'session daemon' ../README.md`

## Phase 5: Verification and retirement (wave G, sequential)

### 5.1: End-to-end verification

**Depends on:** 4.1, 4.2, 4.3

- [ ] **Step 1: Full gate with MongoDB** — `test -z "$(gofmt -l .)" && go vet ./... && MIND_PALACE_TEST_MONGODB_URI=mongodb://localhost:27017 go test -race ./...`
- [ ] **Step 2: Install the binary locally** — `go build -o ~/.claude/plugins/data/clued/bin/mind-palace ./cmd/mind-palace`
- [ ] **Step 3: Stop the Node services and start the Go daemon through the hook** — Stop `dist/daemon.mjs` and `dist/mcp.mjs`, run `hooks/session-start`, confirm `curl -sf http://127.0.0.1:8085/health` and that `ps` shows `mind-palace session daemon`.
- [ ] **Step 4: Capture a live Claude Code session** — Open a new Claude Code session in a git repository, run a few Bash, Read and Edit tools and a subagent; confirm in MongoDB that `sessions` has the session with `git_origin`, `transcript_lines` grows, `subagent_lines`, `blobs` (all three types) and `hook_events` are populated and `enrichments.*` appear within 10 s.
- [ ] **Step 5: Compare query results with the Node server** — Run `find_sessions`, `search_commands`, `get_session_context`, `read_transcript` and `get_full_session` against the same database through `node dist/mcp.mjs --stdio` and `bin/clued session mcp`; the results must be equal apart from `hook_event_ids` typing. Confirm the tools work from inside Claude Code through `.mcp.json`.
- [ ] **Step 6: Backfill and restore round trip** — `mind-palace session backfill` twice (counts equal, no duplicates); `mind-palace session restore <id> --projects-dir $TMPDIR/restore` and diff the restored JSONL and blobs against the originals.
- [ ] **Step 7: Record the evidence** — Paste the commands and their output into this plan's work notes (`mind-palace plan update`) and mark 5.1 complete.

**Validation:** `curl -sf http://127.0.0.1:8085/health`

### 5.2: Remove the TypeScript implementation (requires Boss's explicit approval)

**Files:**
- Modify: `mise.toml`
- Modify: `.gitignore`
- Modify: `README.md`
- Modify: `.claude-plugin/plugin.json`
- Delete: `src/`
- Delete: `enrichers/`
- Delete: `test/`
- Delete: `dist/`
- Delete: `bin/clued.cjs`
- Delete: `package.json`
- Delete: `pnpm-lock.yaml`
- Delete: `tsconfig.json`
- Delete: `.github/workflows/build.yml`
- Delete: `.github/workflows/publish.yml`

**Depends on:** 5.1

- [ ] **Step 1: Get approval** — Ask Boss to confirm the deletion list; do not proceed without a yes.
- [ ] **Step 2: Delete and tidy** — `git rm -r` the listed paths; drop `node_modules/` from `.gitignore` only if nothing else needs it; replace the node/pnpm entries in `mise.toml` with `go`; remove remaining Node references from the README and the plugin description.
- [ ] **Step 3: Full gate** — `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go test ./... && go test ./plugin`
- [ ] **Step 4: Commit** — `git commit -m 'chore: retire the TypeScript session capture in favour of mind-palace'`

**Validation:** `test ! -d ../src && test ! -d ../enrichers && go test ./...`

## Acceptance Criteria

- [ ] `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go test -race ./...` passes, and the same with `MIND_PALACE_TEST_MONGODB_URI` set against a live MongoDB
- [ ] Every test case in the TypeScript suite has a Go counterpart (the parity table in the design plan, checked off per section)
- [ ] `hooks/session-start` starts `mind-palace session daemon` without Node installed; hook events, transcript lines, subagent lines, the three blob types and enrichments reach MongoDB in the same collections and shapes as before
- [ ] `bin/clued session mcp` serves `find_sessions`, `get_session_context`, `get_full_session`, `search_commands`, `read_transcript` and `restore_session` from Claude Code with results equal to the Node server's on the same database
- [ ] `mind-palace mcp` and `mind-palace serve` expose the session tools next to the plan and story tools
- [ ] `mind-palace session backfill` is idempotent and `mind-palace session restore` reproduces a session's files byte for byte
- [ ] The TypeScript implementation and the Node tooling are gone from the repository (after Boss's approval in 5.2)

## Self-Review

| Requirement | Delivered by |
|---|---|
| Every `src/` function ported | 1.1 (config, account, lang, paths), 2.1 (mongo), 2.2 (tailer, artifact-watcher), 2.3 (wal), 2.4 (git, host), 2.8 (enricher loop), 2.9 (mcp tool functions, restore), 2.10 (backfill), 3.1 (daemon), 3.2 (mcp, mcp-protocol) |
| Enrichers ported | 2.5, 2.6, 2.7 (18 of 19; line-summary dropped by design) |
| Functions added to mind-palace | 1.3, 3.3, 3.4 |
| Maximum concurrency | Waves A (2), C (10), D (3), F (3) run in parallel subagents; only 1.2, 3.4, 5.1 and 5.2 are serial |
| TDD and no mocks | Step 1 of every section; `memsession` + `sessiontest` |
| Plugin keeps working | 4.1, 4.2, 4.3, 5.1 |

**Placeholder scan:** None: every section names its files, tests, interfaces and commands. `build.ts` and `line-summary` are intentionally not ported (design decisions).
**Residual risk:** RE2 lacks lookahead and Unicode word boundaries, so a few JavaScript regexes need hand translation; the ported tests catch divergences. The MongoDB-gated tests need a local MongoDB for 2.1, 3.4 and 5.1. Distribution (release download) is an open design question Boss may change, which affects only 4.1 and 4.2.

## Next Steps

1. Boss reviews the design plan's open questions, then `mind-palace plan set-status` both plans to `ready`
2. Create `wip/session-port` from `main` and dispatch wave A
3. Optionally create one mind-palace story per section so subagents log work against it
