# plan — backend plan manager

`plan` is a backend-only service for saving, retrieving and monitoring Plans.
It does not write plans itself: agents, skills and other tools use it to
create plans from structured input, read them back, move them through a
status workflow, link them to other plans and stories, and track per-section
progress. Stories will join Plans as an architectural equal later.

Plans are Markdown files with YAML front matter, named
`AAAA-BBB-CCCC-description-of-plan.md` (sequence, random id, type, slug).
The file format is the canonical representation in every store.

Front matter carries `id`, `title`, `type`, `status`, `priority`, optional
`effort`, the managed `created`/`updated`/`completed` timestamps, `links`
and `progress`. `links` holds the repository (`repo`), spec documents
(`specs`), named web links (`web`), and `[id, relation]` tuples to stories
(`stories`) and to other plans (`plans`); relations are `parent`, `included`,
`depends` and `blocks`. Files written with the earlier top-level `plans` key
still parse and are rewritten under `links.plans` on their next write.

## Build

```bash
cd plan
go build -o plan ./cmd/plan        # or: go install ./cmd/plan
go test ./...
```

Requires Go 1.26 (the toolchain is downloaded automatically by `go`).

## Quick start

```bash
# learn the content layout and the creation schema
plan template get
plan schema
plan config                        # types, statuses, synonyms, workflow, priorities, efforts

# create a plan from JSON matching the schema (file, - for stdin, or inline JSON)
plan create --input plan.json
plan create --input '{"frontMatter":{"title":"Add search","type":"drft","priority":"P2"},
                      "body":{"summary":{"goal":"…","problem":"…"},"design":{}}}'

plan list --type impl --status in_progress
plan get 0002-a3f                  # path, front matter and content
plan get 0002-a3f --content        # raw Markdown
plan get docs/plans/0002-a3f-dsgn-add-search.md --front-matter

plan set-status 0002-a3f approved  # synonyms map to primary names (approved → ready)
plan transitions 0002-a3f
plan set-progress 0002-a3f 1.1 in_progress
plan progress 0002-a3f
plan validate 0002-a3f
```

Every command prints `{"ok": true, "result": …}` to stdout. Errors go to
stderr as `{"ok": false, "error": {"kind", "message", "problems"}}` with an
exit code per kind:

| Code | Kind | Raised when |
|---|---|---|
| 2 | BadRequest | Bad arguments or unknown operation |
| 3 | ValidationError | The result of a write fails the JSON Schema (problems listed) |
| 4 | NotFound | No plan matches the identifier, or a linked plan doesn't exist |
| 5 | InvalidTransition | The workflow doesn't allow the status change |
| 6 | ImmutableField | A write tries to change `id`, `created`, `updated` or `completed` |
| 7 | UnknownSection | A progress operation names a section with no numbered heading |
| 8 | LinkedPlan | `delete` on a plan other plans link to, without `--force` |
| 9 | Conflict | Duplicate id/template, or a sync conflict |
| 10 | StorageError | A store failed |

`--format yaml` and `--compact` change the output shape; `--format text`
prints string results (content, templates) raw.

## Operations

Every entrypoint exposes the same operations with the same parameters.
`plan ops` prints them all with their JSON Schemas; `plan call <op> --params '{…}'`
invokes any of them directly.

| Group | Operations |
|---|---|
| Plans | `create`, `get`, `getFrontMatter`, `getContent`, `list`, `update`, `delete`, `validate` |
| Templates | `getTemplate`, `listTemplates`, `createTemplate`, `updateTemplate`, `deleteTemplate` |
| Fields | `setTitle`, `setType`, `setStatus`, `getTransitions`, `setPriority`, `setEffort`, `clearEffort`, `patchFrontMatter` |
| Links | `addPlanLink`, `removePlanLink`, `addStoryLink`, `removeStoryLink`, `addSpec`, `removeSpec`, `setWebLink`, `removeWebLink`, `setRepo`, `clearRepo` |
| Progress | `getProgress`, `setProgress`, `addProgressStory`, `removeProgressStory`, `removeProgress` |
| Storage | `sync`, `getConfig`, `getSchema` |

A plan identifier is a plan id (`0002-a3f`) or the path to its file.

Rules that apply to every write:

- The result is validated against the Plan JSON Schema before saving; a write that fails is not saved.
- `updated` is stamped on every write; `id` and `created` are set once by `create`; `updated` and `completed` have no setters.
- Content (below the front matter) changes only through `update`; front matter only through the setters.
- Plan→plan links live in `links.plans`; `addPlanLink` checks the target exists and `delete` refuses while other plans link to the plan (unless forced).
- The content's H1 always matches `title`.
- Synonyms and labels are accepted for status, priority and effort and stored as their primary value (`approved` → `ready`, `P1` → `1`, `Medium` → `M`).
- `create` seeds `progress` with `pending` for every phase and section the body declares (unless `progress` is given).
- Moving to `complete` sets `completed`; moving away clears it. Moving to `archived` moves the file into `archive/`.

## Entrypoints

```bash
plan serve --addr 127.0.0.1:8087      # HTTP/REST + WebSocket + MCP (Streamable HTTP)
plan mcp                              # MCP over stdio
```

### HTTP/REST (`/api/v1`)

- `POST /api/v1/ops/{op}` with the operation's JSON parameters as the body (any operation); `GET` works for read-only ones with query parameters.
- `GET /api/v1/ops` lists operations and their schemas.
- Resource routes: `GET|POST /plans`, `GET /plans/{id}`, `GET /plans/{id}/front-matter|content|transitions|progress|validate`, `PUT /plans/{id}/content`, `PUT /plans/{id}/status`, `PATCH /plans/{id}`, `DELETE /plans/{id}?force=1`, `GET /templates`, `GET|PUT|DELETE /templates/{id}`, `GET /config`, `GET /schema`.
- `GET /api/v1/events?plans=0002-a3f,0003-b4c` streams plan events as Server-Sent Events.
- `GET /health`.

Responses use the same `{"ok": …}` envelope as the CLI; error kinds map to 400/404/409/500.

### WebSocket (`/ws`)

```jsonc
→ {"id": 1, "op": "get", "params": {"plan": "0002-a3f"}}
← {"id": 1, "ok": true, "result": {…}}
→ {"id": 2, "op": "subscribe", "params": {"plans": ["0002-a3f"]}}   // omit plans for every plan
← {"id": 2, "ok": true, "result": {"subscribed": true, "all": false, "plans": ["0002-a3f"]}}
← {"event": "plan.updated", "planId": "0002-a3f", "operation": "setStatus", "plan": {…}, "at": "…"}
→ {"op": "unsubscribe"}                                              // or {"params": {"plans": [...]}}
```

### MCP (`/mcp`, or stdio)

- One tool per operation, same names and parameters (`--mcp-tool-prefix` adds a prefix).
- Resources `plan://<id>` (Markdown) and `plan://<id>/json`; subscribe to them for `resources/updated` notifications.
- `watch` tool: blocks until one of the given plans changes (or a timeout) and returns the events, for clients without resource subscriptions.

Claude Code registration example (`.mcp.json`):

```json
{ "plans": { "command": "plan", "args": ["mcp", "--dir", "docs/plans"] } }
```

## Configuration

`plan` looks for `--config`, then `$PLAN_CONFIG`, then `plan.config.yaml`
(`.yml`, `.json`, `.plan.yaml`, `.plan.json`) in the working directory, then
in each parent directory up to your home directory; the nearest readable
file wins, and the search never goes above your home directory.
Relative `plansDir` and file-store `dir` values are anchored to the config
file's directory, so storage lands in the same place from any subdirectory.
Every list replaces the default when given; see `plan.config.example.yaml`.

```yaml
plansDir: docs/plans          # file storage root; archived plans go to docs/plans/archive
types:                        # default: drft, dsgn, impl
  - {name: rfc, label: RFC}
statuses:                     # primary name, label and synonyms (pending is required)
  - {name: pending, label: Pending, synonyms: [new, draft, todo]}
workflow:                     # allowed transitions
  pending: [validated, blocked, rejected]
priorities:
  - {value: "1", labels: [P1, Critical]}
efforts:
  - {name: M, labels: [Medium], points: 5}
storage:
  - {name: file, kind: file}
  - {name: db, kind: postgres, options: {dsn: "postgres://…"}}
server:
  addr: 127.0.0.1:8087
```

The JSON Schema's enumerations are rewritten from the configuration, so
`plan schema` always reflects the configured types, statuses, priorities and
efforts.

## Storage

Writes fan out to every enabled store; reads come from the first one (the
primary). File storage (`docs/plans` in the working directory) is the only
plugin enabled by default and can only be disabled when another is enabled.
If a secondary copy fails, the primary write stands, the command succeeds
with a `warning`, and `plan sync` reconciles.

| Kind | Options | Notes |
|---|---|---|
| `file` | `dir` | Markdown files; `archive/` and `templates/` subfolders |
| `sqlite` (`sqlite3`) | `dsn` (default `<plansDir>/plans.sqlite`), `tablePrefix` | pure Go driver |
| `postgres` (`postgresql`, `pgx`) | `dsn`, `tablePrefix` | |
| `mysql` (`mariadb`) | `dsn`, `tablePrefix` | |
| `clickhouse` | `dsn`, `tablePrefix` | ReplacingMergeTree tables |
| `mongodb` (`mongo`) | `uri`, `database`, `collection` | |
| `firestore` | `project`, `database`, `collection` | Application Default Credentials; honours `FIRESTORE_EMULATOR_HOST` |
| `memory` | | in-process, for tests |

```bash
plan sync --from db --to file --on-conflict error|skip|overwrite
```

`error` stops at the first plan that differs, `skip` reports the conflicts
and copies the rest, `overwrite` makes the target match the source.

New plugins implement `store.Store` and register a factory with
`store.Register(kind, factory)`; `store/storetest` is the conformance suite
they must pass.

## Templates

Content is rendered from a Go `text/template` over the structured `body`
(`plan template get` prints the default, which produces the standard
Summary / Part 1 — Design / Part 2 — Implementation layout).
`plan template create <id> --content t.md` adds alternatives, selected with
`plan create --template <id>`. Updating `default` overrides the built-in;
deleting the override restores it.

## Layout

| Package | Responsibility |
|---|---|
| `model` | Plan, front matter, links, progress types; YAML/JSON tuple forms |
| `config` | Defaults, config file loading, synonym/label normalisation, workflow |
| `schema` | Embedded JSON Schema, configuration patching, validation |
| `markdown` | Front matter split/render, numbered headings, H1 sync |
| `render` | Content templates and the body types |
| `service` | Every operation and its rules; typed errors |
| `ops` | Operation registry shared by all entrypoints |
| `store`, `store/*` | Store interface, fan-out, sync, plugins |
| `events` | In-process event bus |
| `server/*` | HTTP, WebSocket and MCP entrypoints |
| `cli`, `cmd/plan` | Command line |
