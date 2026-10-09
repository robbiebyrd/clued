---
id: "0001-24c"
title: "Plan manager service"
type: "dsgn"
status: "pending"
priority: "2"
effort: "XL"
created: "2026-10-09T20:37:01.000Z"
updated: "2026-10-09T20:37:01.000Z"
completed: ""
links:
  specs:
    - "./plans/plan-service.md"
    - "./plans/service-plan-template.md"
  repo:
    remote: "https://github.com/robbiebyrd/clued.git"
---

# Plan manager service

## Summary

**Goal:** Agents, skills and tools can save, find, change and watch Plans (and later Stories) through one service with one set of rules, whichever entrypoint (CLI, HTTP, WebSocket, MCP) and whichever storage backend they use.

**Problem:** Plans written by agents today are loose Markdown files (`docs/superpowers/plans/`, `docs/superpowers/specs/`) with no shared IDs, statuses, links or progress tracking. Nothing validates them, nothing tells a second agent that a plan it depends on just finished, and nothing keeps a database copy in step with the files.

**Approach:** A standalone Go module inside this repo (`planner/`) built around a single operation registry. Every operation in `plans/plan-service.md` is defined once (input, validation, handler) and exposed automatically on all four entrypoints. Storage plugins implement a small record-level interface; the service fans writes out to every enabled store and records every change in an event log that powers monitoring and subscriptions.

---

# Part 1 — Design

## Current State

| Piece | Where | State |
|---|---|---|
| Plan format, front matter, statuses, workflow, priorities, effort, ops table, errors, storage and entrypoint requirements | `plans/plan-service.md` | Written; this design implements it. Inconsistencies are listed under Open Questions. |
| Plan Markdown template | `plans/service-plan-template.md` | Written; becomes the default template, embedded in the binary. |
| Story format | `plans/story-service` (no extension) | Partial: filename, types, a front matter example. "Architecturally equals" to Plans. |
| Existing clued runtime | `src/daemon.ts`, `src/mcp.ts`, `src/mcp-protocol.ts` | TypeScript/Node, zero-framework `http.createServer`, hand-rolled MCP JSON-RPC over SSE and stdio, loopback-only ports 8085/8086, `/health` probes. |
| Config | `src/config.ts` | JSON at `~/.claude/plugins/data/clued/config.json`, `CLUED_*` env overrides, `~` expansion. |
| Lifecycle | `hooks/session-start` | Health-checks each server and self-heals it on session start. |
| MongoDB | `src/mongo.ts` | Shared clued instance (`mongodb://localhost:27018`, db `claude_sessions`), indexes created on connect. |
| Build / CI | `package.json`, `.github/workflows/build.yml` | pnpm, esbuild bundles committed to `dist/`, oxlint, `node --test`. No Go toolchain yet. |
| Specs convention | `docs/superpowers/specs/*.md` | Prose design docs with "Repository Changes" tables. This doc instead uses the plan template, so it doubles as the first plan the service will manage. |

## Decisions

- **Go, as a separate module at `planner/`** — the spec asks for Go, and a single static binary suits a CLI that agents call hundreds of times per session (no Node start-up, no `npx` resolution). It shares the repo, config directory and MongoDB with clued but nothing in `src/` imports it or vice versa.
- **One binary, `planner`, with noun groups** — `planner plan …`, `planner story …`, `planner template …`, `planner store …`, `planner serve`. Spec examples map one-to-one (`plan get 0002-a3f` → `planner plan get 0002-a3f`). A `plan` shim script can be installed for the bare form if wanted (see Open Questions).
- **An operation registry is the single source of truth** — each op is a Go value with a name, input struct (JSON-tagged, with a generated JSON Schema), output type and handler. CLI flags, REST routes, WebSocket methods and MCP tools are generated from it, which is what makes "all entrypoints accept the same function names and inputs and return the same data" hold by construction instead of by discipline.
- **Plans and Stories are two instances of one generic "kind"** — a `Kind` bundles its filename pattern, folder, ID format, types, statuses, synonyms, workflow, priorities, schema, template and `completed` semantics. The service, stores, events and entrypoints are written against `Kind`, so Stories later is configuration plus a schema, not new plumbing.
- **Storage plugins implement records, not operations** — plugins store and fetch whole documents (`Get`, `List`, `Put`, `Delete`, `MaxSeq`, events). Every op's logic (validation, workflow, synonyms, timestamps, link checks) runs once in the service. This deviates from the spec sentence "plug-ins should adhere to the interface above": with ~40 ops and 7 backends, per-plugin ops would mean 280 implementations that drift apart.
- **The Markdown file is the canonical representation everywhere** — every store keeps the exact rendered file bytes alongside indexed columns/fields. Sync, conflict detection and export compare bytes (SHA-256), so a Postgres copy can always be turned back into the identical `.md` file.
- **Front matter is written canonically** — fixed key order matching the template, progress keys always quoted, timestamps in UTC milliseconds. Comments inside front matter are not preserved on write. Content below the front matter is never reformatted.
- **One primary store, any number of replicas** — reads come from the primary (file store unless configured otherwise). A write succeeds when the primary accepts it; replica writes run after and failures are queued for retry, never silently dropped.
- **Every write is an event** — the service appends a structured event (what changed, from/to, actor) to the workspace's event log in the same step as the write. Subscriptions, history, and monitoring are all reads of that log, so the CLI does not need the server running for changes to be observed.
- **Monitoring reports, it does not act** — the service never changes a status on its own. It emits derived events (a dependency finished, a plan became unblocked, all progress sections complete, a file was edited outside the service and is now invalid) and leaves the decision to the subscriber.
- **A workspace is the unit of identity** — plan IDs are unique per workspace, not globally. A workspace is the git root (fallback: the working directory) for file storage, and its normalized git remote (fallback: absolute path) as the key in shared databases. This lets one Postgres or MongoDB hold plans from many repos without `0001-…` collisions.
- **Optimistic concurrency with content hashes** — every read returns a `version` (SHA-256 of the file bytes). Any write may pass `ifVersion`; stores reject a stale write with `Conflict`. The file store additionally takes a workspace lock for the read-modify-write and for sequence allocation.
- **Same transport style as clued** — loopback-only by default, `/health` endpoints, self-healed by `hooks/session-start`, config next to clued's under `~/.claude/plugins/data/clued/`.

### Rejected alternatives

- **Write it in TypeScript next to `src/`** — would reuse clued's MCP and Mongo code, but the spec asks for Go, and SQL/Firestore/ClickHouse drivers plus a CLI start-up budget favour a compiled binary.
- **CLI always forwards to a running server** — simpler eventing, but makes every agent call depend on a background process being healthy. Direct writes plus a shared event log keep the CLI self-sufficient.
- **Storing parsed fields only (no raw file) in databases** — loses formatting and makes round-trip export lossy.
- **Automatic reciprocal links** (adding `blocks` on B when A `depends` on B) — would turn one write into two documents and two events, and conflicts with the rule that a write touches one plan. Inbound links are computed on read instead.
- **Event sourcing as the storage model** (plans rebuilt from events) — powerful but makes every plugin harder and file storage no longer human-editable.

## Design

### Module layout

```
planner/
  go.mod                         module github.com/robbiebyrd/clued/planner
  cmd/planner/main.go            entrypoint; wires config, stores, registry, entrypoints
  internal/kind/                 Kind definitions (plan, story); config → Kind
  internal/doc/                  parse/render: front matter (YAML), content, headings, filename
  internal/schema/               embedded plan/story JSON Schemas; enum injection from config
  internal/workflow/             status normalization, transitions, getTransitions
  internal/service/              one file per op group: plans.go, fields.go, links.go, progress.go, templates.go
  internal/ops/                  registry: Op{Name, Input, Output, Handler, CLI}
  internal/events/               event types, log writer/reader, derived-event monitor
  internal/store/                Store interface, conformance test suite
  internal/store/file|sqlite|postgres|mysql|clickhouse|mongo|firestore/
  internal/replicate/            fan-out, outbox, sync
  internal/api/cli|http|ws|mcp/  generated from the registry
  schemas/plan.schema.json       the Creation schema from plans/plan-service.md
  schemas/story.schema.json
  templates/default.md.tmpl      plans/service-plan-template.md as a Go text/template
```

Libraries (all widely used, pure Go where possible so cross-compiling needs no cgo): `spf13/cobra` (CLI), `go.yaml.in/yaml/v3` (front matter), `santhosh-tekuri/jsonschema/v6` (2020-12 validation), `yuin/goldmark` (heading scan), `fsnotify/fsnotify`, `modelcontextprotocol/go-sdk` (MCP streamable HTTP + stdio), `coder/websocket`, `modernc.org/sqlite`, `jackc/pgx/v5`, `go-sql-driver/mysql`, `ClickHouse/clickhouse-go/v2`, `mongo-driver/v2`, `cloud.google.com/go/firestore`.

### Data model

The in-memory document, shared by every layer:

```go
type Document struct {
    Kind        string            // "plan" | "story"
    Workspace   string            // workspace key, see below
    ID          string            // "0002-a3f"
    Seq         int               // 2
    FileName    string            // "0002-a3f-dsgn-description-of-plan.md"
    FrontMatter FrontMatter       // typed struct matching the schema's frontMatter
    Content     string            // Markdown below the closing "---", untouched
    Raw         []byte            // canonical rendered file
    Version     string            // sha256(Raw), hex
    Archived    bool              // status == archived → lives in archive/
}
```

`FrontMatter` is a typed struct whose fields and order mirror `plans/service-plan-template.md`: `id, title, type, status, priority, effort, created, updated, completed, plans, links{specs, repo, web, stories}, progress`. Pairs like `["0031-34c", "blocks"]` are a `Link{Target, Relation}` type with custom YAML/JSON marshalling so they stay two-element arrays on disk and on the wire.

**Identifiers.** `id` = `AAAA-BBB`: `AAAA` is `max(seq in workspace across all stores) + 1`, zero-padded to 4 (5+ digits once past 9999, which the pattern should allow; see Open Questions); `BBB` is 3 random `[a-z0-9]` characters from `crypto/rand`. The random part means two branches or machines that both allocate `0007` still get distinct IDs, and lookup by ID never relies on the sequence alone. The filename is `<id>-<type>-<slug>.md`, type always lowercase.

**Identifier resolution** (`:planIdentifier`): accepted forms are the full ID (`0002-a3f`), a filename, or a path. Paths are resolved and must sit inside the workspace's plans folder (or its `archive/`); anything else is `NotFound`, never a read of an arbitrary file.

**Normalization on input.** Status, priority, effort and type accept synonyms and labels (`approved`→`ready`, `P1`/`Critical`→`1`, `Medium`/`5`→`M`). Lookup is case-insensitive and treats `-`/`_`/space alike. Unknown values fail with `ValidationError` listing the accepted values.

**Derived (read-only) fields**, returned by `get`/`list` but never stored in front matter:

```json
{
  "inbound":  [{"from": "0031-34c", "relation": "depends"}],
  "rollup":   {"total": 9, "byStatus": {"complete": 6, "in_progress": 1, "blocked": 1, "validated": 1}},
  "blockedBy": ["0001-3sd"]
}
```

`blockedBy` lists plans this one `depends` on, plus plans that declared they `blocks` it, that are not yet `complete`.

**Kinds and configuration.** A Kind is built from defaults overlaid by config. Plan defaults are exactly the lists in `plans/plan-service.md`; Story defaults copy the plan statuses, workflow and priorities and use the story types (`bugs, feat, impr, chor, task`). Per-kind settings that differ between the two specs are explicit:

| Setting | Plan | Story |
|---|---|---|
| `completedOnReopen` | `clear` (spec: "moving away from complete clears it") | `keep` (story spec: "removing from Complete does not remove this field") |
| `folder` | `docs/plans` | `docs/stories` |
| `idPattern` | `^[0-9]{4}-[a-z0-9]{3}$` | same (see Open Questions) |

Because enums are configurable, the JSON Schema used for validation is the embedded base schema with its `enum` lists replaced from the active Kind at start-up. `planner plan schema` prints the effective schema so agents can validate before calling.

### Configuration

Two layers, merged with the later winning:

1. Global: `~/.claude/plugins/data/clued/planner.json` (same directory as clued's `config.json`).
2. Workspace: `docs/plans/.planner.json` (committed, so a repo can pin its own statuses and workflow).

Env overrides follow clued's pattern: `CLUED_PLANNER_*`.

```jsonc
{
  "root": "git",                         // "git" (git root, fallback cwd) | "cwd"
  "stores": [
    { "name": "files",  "type": "file",  "primary": true },
    { "name": "mongo",  "type": "mongo", "url": "mongodb://localhost:27018", "db": "clued_plans" },
    { "name": "pg",     "type": "postgres", "dsn": "env:PLANNER_PG_DSN", "enabled": false }
  ],
  "server":  { "httpPort": 8087, "bind": "127.0.0.1", "token": "env:PLANNER_TOKEN" },
  "kinds": {
    "plan": {
      "types":      { "drft": "Draft", "dsgn": "Design", "impl": "Implementation" },
      "statuses":   { "pending": ["new", "draft", "todo"], "ready": ["approve", "approved"], "...": [] },
      "initial":    ["pending"],
      "workflow":   { "pending": ["blocked", "rejected", "validated"], "...": [] },
      "priorities": { "0": ["P0", "Emergency"], "1": ["P1", "Critical", "Block"], "...": [] },
      "effort":     { "XS": ["Extra Small", "2"], "M": ["Medium", "5"], "...": [] }
    }
  }
}
```

Secrets are referenced as `env:NAME`, never written in the file. The file store cannot be disabled unless another store is enabled and marked primary (spec rule). Config is validated on load; a workflow naming an unknown status, or a primary store that is disabled, stops start-up with a clear error.

### Operations (API surface)

All ops from `plans/plan-service.md` are implemented as written, with these clarifications and small additions. Every op takes an optional `workspace` (defaults to the one inferred from cwd for the CLI and from server config for servers), and every write op takes optional `ifVersion` and `actor` (free text such as an agent or Claude session ID; recorded on the event).

| Group | Ops |
|---|---|
| Plans | `create`, `get` (`--front-matter`, `--content` flags = `getFrontMatter`, `getContent`), `list`, `update`, `delete`, `validate` |
| Fields | `setTitle`, `setType`, `setStatus`, `getTransitions`, `setPriority`, `setEffort`, `clearEffort`, `patchFrontMatter` |
| Links | `addPlanLink`, `removePlanLink`, `addStoryLink`, `removeStoryLink`, `addSpec`, `removeSpec`, `setWebLink`, `removeWebLink`, `setRepo`, `clearRepo` |
| Progress | `getProgress`, `setProgress`, `addProgressStory`, `removeProgressStory`, `removeProgress` |
| Templates | `getTemplate`, `createTemplate`, `updateTemplate`, `deleteTemplate`, `listTemplates` *(added)* |
| Monitoring *(added)* | `history`, `watch`/`subscribe`, `unsubscribe` |
| Admin *(added)* | `import`, `schema`, `store status`, `store sync`, `store retry` |

Behaviour worth pinning down:

- **create** renders the chosen template (`templateId`, default `default`) with `{frontMatter, body}`, assigns `id`, `created = updated = now`, defaults `status` to the Kind's first `initial` status, validates, then writes. A `status` outside `initial` is rejected unless `force` (see Open Questions).
- **update** replaces the content. If the new content's H1 differs from `title`, the write fails with `ValidationError` pointing at `setTitle`; if the H1 is missing it is inserted. `progress` keys that no longer match a numbered heading make the write fail, so content and progress cannot silently drift.
- **setStatus** to `archived` moves the file to `archive/`; leaving `archived` moves it back. The filename never changes on status changes.
- **setType** renames the file's type part and re-validates against the new type's requirements (e.g. `dsgn` → `impl` requires `links.specs` and Part 2), so a promotion that is not ready fails cleanly.
- **delete** consults the inbound-link index; fails with `LinkedPlan` (listing the linkers) unless `force`. With `force`, links pointing at the deleted plan are left in place and reported as dangling by `validate`.
- **addPlanLink** verifies the target exists in the workspace; **addStoryLink** verifies only once Stories ship (until then, any well-formed story ID is accepted and flagged by `validate`).
- **import** *(added)* takes an existing `.md` file with or without front matter, assigns an ID if missing, normalizes and validates it, and stores it. This is how agents that already wrote a Markdown plan hand it over, and how `docs/superpowers/plans/*.md` get migrated. The service still never authors content.
- **list** filters: `type`, `status` (synonyms allowed, repeatable), `priority` (≤, =), `linkedTo` (plan or story ID, either direction), `archived` (excluded by default), `updatedSince`, `blocked` (has non-complete `blockedBy`), text `q` over title. Sort by `seq` (default), `updated`, `priority`.

Errors are the spec's six plus `Conflict` (stale `ifVersion`), `StoreUnavailable` (primary unreachable) and `InvalidConfig`. Every error has a stable `code`, a `message`, and a `details` array (for `ValidationError`, one entry per schema problem with its JSON pointer).

### Entrypoints

All four are generated from the registry. Names are the op names from the spec; inputs are the op's JSON input; output is the op's JSON output. Golden tests (see Testing) assert that the same call returns byte-identical JSON on every entrypoint.

**CLI** — `planner <kind> <op-in-kebab-case> [positional] [--flags]`, with positional arguments as in the spec examples (`planner plan set-status 0002-a3f approved`). `--input file.json` or `--input -` supplies the full JSON input for any op. Output is human-readable by default and `--json` gives the canonical JSON, which is what agents and skills should use. Exit codes: 0 ok, 2 validation/usage, 3 not found, 4 conflict or invalid transition, 5 store failure. The CLI talks to stores directly; it does not need the server.

**HTTP/REST** — `planner serve` (port `8087`, loopback by default).

| Method & path | Op |
|---|---|
| `POST /v1/{kind}s` | create |
| `GET /v1/{kind}s` | list (query params = filters) |
| `GET /v1/{kind}s/{id}` | get (`?part=frontMatter|content`) |
| `PUT /v1/{kind}s/{id}/content` | update |
| `DELETE /v1/{kind}s/{id}` | delete (`?force=true`) |
| `POST /v1/{kind}s/{id}/{op}` | every other op, e.g. `/v1/plans/0002-a3f/setStatus` |
| `GET /v1/{kind}s/{id}/history` | history |
| `GET /v1/events` | Server-Sent Events stream of the event log (same filters as subscribe) |
| `GET /v1/templates[/{id}]`, `PUT`, `POST`, `DELETE` | template ops |
| `GET /health` | liveness, matching clued's convention |

`ETag` = `version`, and `If-Match` maps to `ifVersion`. Errors map to 400/404/409/412/422/503 with the JSON error body.

**WebSocket** — `GET /v1/ws`, JSON-RPC 2.0. Requests are `{"method": "plan.setStatus", "params": {...}}` for any op. Subscriptions: `{"method": "subscribe", "params": {"kind": "plan", "ids": ["0002-a3f"], "filter": {"status": ["in_progress"]}, "events": ["statusChanged", "progressChanged"], "since": 1042}}` returns a subscription ID; events arrive as `{"method": "event", "params": {...}}` notifications. `since` replays from the log, so a client that reconnects misses nothing.

**MCP** — streamable HTTP at `/mcp` on the same server, plus `planner mcp --stdio` for plugin `.mcp.json` registration (same pattern as clued's `--stdio`). Each op is a tool named `plan_<op>` (`plan_set_status`, …) with the op's generated input schema. Each plan is also an MCP resource `plan://{workspace}/{id}` supporting `resources/subscribe`; on any event for that plan the server sends `notifications/resources/updated`, and the client re-reads it. This is MCP's native subscribe mechanism, so it works in Claude Code without custom client code. The `list` filter and `history` ops cover the "stream of updates for many plans" case for MCP clients that do not hold resources.

### Storage

```go
type Store interface {
    Name() string
    Capabilities() Caps                         // Watch, Transactions, Events
    Get(ctx context.Context, ws, kind, id string) (*Document, error)
    List(ctx context.Context, ws, kind string, f Filter) ([]*Document, error)
    Put(ctx context.Context, d *Document, ifVersion string) error   // ErrConflict if stale
    Delete(ctx context.Context, ws, kind, id, ifVersion string) error
    MaxSeq(ctx context.Context, ws, kind string) (int, error)
    Inbound(ctx context.Context, ws, kind, id string) ([]Link, error)
    AppendEvents(ctx context.Context, ws string, evs []Event) error
    ReadEvents(ctx context.Context, ws string, after int64, f EventFilter) ([]Event, error)
    Templates() TemplateStore
    Watch(ctx context.Context, ws string) (<-chan ExternalChange, error) // optional (Caps.Watch)
    Close() error
}
```

A shared conformance test suite runs against every implementation, so "a store works" has one definition.

**File store (default, always available).** Plans in `<workspace>/docs/plans/`, archived in `docs/plans/archive/`, templates in `docs/plans/templates/<id>.md.tmpl`. Writes are atomic (write temp file, fsync, rename). A workspace lock (`flock` on `docs/plans/.planner.lock`, gitignored) serializes read-modify-write and sequence allocation across concurrent CLI calls. `List`/`Inbound` scan front matter only (fast for hundreds of files; an in-process cache keyed by mtime keeps the server cheap). The event log is a JSONL file in the clued data directory, `~/.claude/plugins/data/clued/planner/<workspace-hash>/events.jsonl`, so plan history does not create git noise. `Watch` uses fsnotify to see edits made outside the service (an agent editing the Markdown directly, a `git pull`).

**Relational stores (SQLite, PostgreSQL, MySQL/MariaDB).** Same schema, dialect-specific DDL, migrations embedded and applied on connect:

```sql
documents (workspace, kind, id, seq, type, title, status, priority, effort,
           created, updated, completed, archived, file_name,
           front_matter JSON, content TEXT, raw TEXT, version CHAR(64),
           PRIMARY KEY (workspace, kind, id))
links     (workspace, kind, id, target_kind, target_id, relation)   -- rebuilt on each Put; powers Inbound and linkedTo
events    (seq BIGINT autoincrement, workspace, kind, id, type, at, actor, payload JSON)
templates (workspace, kind, template_id, body TEXT, version)
```

`Put` with `ifVersion` is `UPDATE … WHERE version = ?` and checks rows affected. `MaxSeq` is `SELECT max(seq)` within the same transaction as the insert.

**ClickHouse.** Implemented with `ReplacingMergeTree(updated)` and `FINAL` reads. ClickHouse has no row-level compare-and-set, so it is accepted only as a replica (analytics/reporting copy), never as primary. See Open Questions.

**Document stores (MongoDB, Firestore).** One collection/collection-group per kind (`plans`, `stories`) plus `plan_events` and `plan_templates`. Document `_id` = `<workspace>/<id>`; front matter fields are top-level for querying; `raw` and `version` stored alongside. MongoDB reuses clued's connection settings (`mongoUrl`, default database `clued_plans`), and indexes are created on connect like `src/mongo.ts`. Compare-and-set uses `updateOne({_id, version})` (Mongo) and transactions with a precondition (Firestore). Mongo change streams are used for `Watch` only when the deployment is a replica set; otherwise the server polls `plan_events` by sequence.

### Replication and sync

```
write op ──► service validates ──► primary.Put (+ AppendEvents) ──► return to caller
                                          │
                                          └─► for each replica: Put(raw) ── fail ─► outbox
```

- The primary write and its event are the commit point. Replicas receive the same canonical document and event. A failed replica write goes to an outbox (`~/.claude/plugins/data/clued/planner/<workspace-hash>/outbox.jsonl`, or an `outbox` table in a DB primary) and is retried on the next write, by `planner store retry`, and by the server every minute. `planner store status` shows each store's health and backlog.
- **Sync** (`planner store sync --from mongo --to files --on-conflict error|skip|overwrite [--dry-run]`) compares every document in the workspace by `(id, version)`. Missing in target → copy. Same version → nothing. Different versions → a conflict, handled by the chosen mode: `error` stops before writing anything and lists conflicts, `skip` writes everything else and reports conflicts, `overwrite` takes the source. Events are copied the same way (missing sequences only). The result is a report (copied, unchanged, conflicts, errors) in JSON. This also covers "download all plans from Postgres as Markdown files": sync with a file store as target.

### Monitoring and status changes

Every write produces one event with a common envelope:

```json
{
  "seq": 1043,
  "at": "2026-10-09T21:02:11.402Z",
  "workspace": "github.com/robbiebyrd/clued",
  "kind": "plan",
  "id": "0002-a3f",
  "type": "statusChanged",
  "actor": "claude-session:5b1c…",
  "version": "9f2c…",
  "changes": [{ "path": "/status", "from": "ready", "to": "in_progress" }],
  "forced": false
}
```

Event types: `created`, `contentUpdated`, `fieldChanged` (title, type, priority, effort, repo, specs, web), `statusChanged`, `linkAdded`, `linkRemoved`, `progressChanged` (path `/progress/1.1/status`), `deleted`, and `externalChange` (a file edited outside the service; payload says whether it still validates).

**Delivery.** The CLI appends to the log under the workspace lock. `planner serve` tails the log (fsnotify on the JSONL file, or polling `events` by `seq` for DB primaries) and pushes matching events to WebSocket, SSE and MCP subscribers. Because the log is ordered and every subscriber can pass `since`, delivery is at-least-once with no gaps across reconnects.

**Derived events.** A monitor inside the server reads the same stream and emits events about relationships, which are what "monitor" mostly means for agents waiting on each other:

| Derived event | Fires when |
|---|---|
| `dependencyCompleted` | A plan's status becomes `complete`; sent once for each plan with an inbound `depends` on it (or that it `blocks`). |
| `unblocked` | That completion leaves a plan with an empty `blockedBy`. |
| `progressComplete` | Every `progress` entry of a plan is `complete` while the plan itself is not. A natural prompt to call `setStatus complete`. |
| `invalidExternalEdit` | An `externalChange` fails validation. The file is left as it is; `validate` shows the problems. |
| `replicaLagging` | A store's outbox backlog is non-empty for more than five minutes. |

Derived events carry `"derived": true` and the `seq` of the event that caused them, and are written to the log so `history` and replays include them. None of them changes a plan.

**Status changes.** `setStatus` normalizes the requested status, checks the workflow edge `current → requested`, and fails with `InvalidTransition` (including the allowed list, the same data as `getTransitions`) unless `force`. A forced move is recorded with `"forced": true`. `completed` is set on entering `complete`, and on leaving it either cleared (plans) or kept (stories) per the Kind. `setProgress` uses the same status list and normalization but no workflow checks, because sections move freely while work is underway; it requires the section to exist as a numbered heading (`## Phase N` → `"N"`, `### N.M` → `"N.M"`), else `UnknownSection`.

### Server lifecycle and plugin integration

- `planner serve` runs one process for all workspaces it is asked about (stores are opened lazily per workspace). It binds `127.0.0.1:8087` by default; binding anything else requires `server.token`, sent as `Authorization: Bearer`.
- `hooks/session-start` gains a third health check and self-heal block, identical in shape to the daemon and MCP blocks, that starts `planner serve` if it is installed and not answering on `/health`.
- `.mcp.json` gains a `planner` entry using `planner mcp --stdio`, so Claude Code sessions get the `plan_*` tools without the server running.
- A `skills/planner.md` skill (later phase) tells agents the CLI conventions: always `--json`, get the template with `planner template get`, create with `--input -`.

### Error Handling

| Failure | Behaviour |
|---|---|
| Write fails schema | Nothing written, no event. `ValidationError` with one detail per problem (JSON pointer + message). |
| Primary store unreachable | `StoreUnavailable`; nothing written anywhere. |
| Replica unreachable | Write succeeds; replica entry in outbox; `replicaLagging` if it persists. |
| Stale `ifVersion` | `Conflict` with the current version; caller re-reads and retries. |
| Two CLI processes allocate a sequence at once | Serialized by the workspace lock (file) or transaction (DB); the random suffix covers distinct machines or branches. |
| Hand-edited file is invalid | Still readable via `get` (with a `problems` array); any write op on it fails until it validates, except `update`, `patchFrontMatter` and `import`, which can repair it. |
| Event log write fails after the document write | The write is reported as succeeded with a warning, and the event is queued in the outbox; the monitor rescans that plan on start-up. |
| Server down | CLI and stdio MCP keep working; only WebSocket/SSE/HTTP and derived events pause, and replay from `since` when it returns. |

### Edge Cases

- Duplicate IDs across branches after a merge (same `AAAA-BBB` is astronomically unlikely; same `AAAA` with different `BBB` is fine). `validate --workspace` reports two files with the same `id`.
- A file whose name disagrees with its front matter `id`/`type` (renamed by hand) is reported by `validate`; `setType` or `import` fixes it.
- `progress` keys written unquoted in YAML (`1.10` read as `1.1`) are rejected on parse with a message explaining quoting, matching the template's warning.
- Sequence past `9999` (pattern currently fixes four digits).
- Deleting a template that existing plans were created from is allowed; plans do not record their template.

## Considerations

- **Security:** New local network surface (HTTP/WS/MCP on loopback, like clued's existing ports); non-loopback binding requires a token. Plan identifiers that are paths are confined to the workspace's plans folder. `links.repo.local`, `links.specs` and `links.web` are stored as data and never opened, fetched or executed by the service. Plan content is untrusted Markdown from agents and is never interpreted beyond heading scanning. Store credentials come from `env:` references, never from committed config.
- **Performance:** CLI cold start target under 50 ms for `get` on the file store. File `list` scans front matter only; the server caches by mtime. Fan-out to replicas happens after the primary write, so the caller waits only for the primary (and replicas when `--sync-replicas` is passed, for scripts that need it).

## Testing

- **Unit (Go `testing`):** front matter parse/render round-trip on every example in `plans/`; canonical rendering stability (render → parse → render is byte-identical); synonym normalization tables; workflow transitions against the spec's edge list; schema validation of valid and invalid fixtures per plan type; heading scanner for progress keys; ID allocation and filename building.
- **Store conformance suite:** one table-driven suite (`internal/store/storetest`) run against every plugin: CRUD, compare-and-set, `MaxSeq`, `Inbound`, events append/read, templates. File and SQLite run in the default `go test ./...`; Postgres, MySQL, MongoDB and ClickHouse run via a `docker-compose.planner.yml` (same approach as clued's integration tests); Firestore against its emulator.
- **Entrypoint parity (golden):** a script of ops is executed through CLI `--json`, HTTP, WebSocket and MCP against a temp workspace; all four outputs must equal the same golden JSON. This is the test that keeps "same function names, inputs and outputs" true.
- **Sync and replication:** fault-injecting fake store to test the outbox and each `--on-conflict` mode.
- **Events:** subscribe, perform writes from a separate CLI process, assert the server delivers them in order; reconnect with `since` and assert no gaps; derived events for depends/blocks chains.
- **CI:** a `planner.yml` workflow (`go vet`, `golangci-lint`, `go test ./...`, integration job with service containers), path-filtered to `planner/**` so the Node build is untouched.

## Scope

**In scope:**

- The `planner/` Go module, the Plan kind, all ops in `plans/plan-service.md`, the template ops, and the additions marked *(added)* above.
- File, SQLite, PostgreSQL, MySQL/MariaDB, ClickHouse (replica-only), MongoDB and Firestore stores; fan-out, outbox and sync.
- CLI, HTTP/REST (+SSE), WebSocket and MCP (streamable HTTP + stdio) entrypoints.
- Event log, subscriptions, history and derived events.
- The generic Kind layer, with Story defaults defined but not exposed.
- Suggested rollout order: (1) core, file store, CLI; (2) event log, `serve`, HTTP/WS/MCP, derived events; (3) SQLite + MongoDB, fan-out, sync; (4) PostgreSQL, MySQL; (5) Firestore, ClickHouse; (6) Stories. Each becomes its own `impl` plan linked here.

**Out of scope:**

- Writing or generating plan content — by definition; the service stores what tools give it.
- A UI — backend only.
- Story ops and story schema — deferred until `plans/story-service` is finished; the Kind layer keeps them a configuration change.
- Two-way sync with Jira/GitHub Issues — `links.web` stores URLs only.
- Automatic status changes from monitoring — the service reports, callers decide.
- Changing the existing clued daemon or MCP server beyond the `session-start` and `.mcp.json` additions.

## Open Questions

- [ ] **Go confirmed?** The spec says Go and this design follows it; the rest of clued is TypeScript. Saying "TypeScript" instead keeps the design and swaps the libraries. — Robbie
- [ ] **Command name.** `planner plan get …` (this design) or a bare `plan get …` as in the spec examples (needs a `plan`/`story` shim on `PATH`)? — Robbie
- [ ] **Where files live.** Spec says `docs/plans` in the working directory; this design uses the git root so agents in a subfolder find the same plans. Keep cwd instead? — Robbie
- [ ] **Distribution.** How the Go binary reaches users: GitHub Releases via GoReleaser plus an install step in `/clued-setup` (proposed), or npm platform packages like esbuild so `npx @robbiebyrd/clued` can fetch it. — Robbie
- [ ] **Create with a non-initial status.** The schema requires `status` on input but the spec says new plans start at `pending` via "(new) → Pending". Proposed: only `initial` statuses on create unless `force`. — Robbie
- [ ] **`links.specs` values.** The template comment says "plan IDs or paths", the schema accepts only paths/URLs. Allow plan IDs (and resolve them)? This doc's own `specs` can't list `plans/story-service` because it has no file extension. — Robbie
- [ ] **ClickHouse as primary.** Proposed replica-only because it has no row-level compare-and-set. OK? — Robbie
- [ ] **Sequence width.** Pattern fixes four digits; allow five or more after `9999`? — Robbie
- [ ] **Story spec gaps** (for when Stories start): story IDs are 3 or 4 digits in the schema but 4 in the filename spec; the example uses `type: bug` while the filename code is `bugs`; `plan`, `dependencies`, `depends_on` and `links.plans` overlap; priority is an unquoted number in stories and a quoted string in plans; `links.web.repo` vs plans' `links.repo`. — Robbie
- [ ] **Small spec typos to fix when convenient:** filename examples use uppercase `DSGN`/`IMPL` (this design writes lowercase); the Template ops table reuses `plan get 0002-a3f` as every example; "save them using the" is cut off; the second "Plan Status" heading is about section progress; `Validated -> Rejeceted`. — Robbie
