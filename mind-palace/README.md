# mind-palace — backend plan and story manager

`mind-palace` is a backend-only service for saving, retrieving and monitoring
two kinds of document:

- **Plans** — design and implementation plans (`docs/plans`).
- **Stories** — the issue tracker: bugs, features, improvements, chores and
  tasks (`docs/stories`).

It does not write documents itself: agents, skills and other tools use it to
create them from structured input, read them back, move them through a status
workflow, link them to each other and track progress. Both kinds share one
code base; every operation takes the kind as part of the call (the CLI group,
the HTTP path, the WebSocket `kind` field, the MCP tool prefix), and a store
plugin holds both kinds or neither. More kinds can be added in `kind/`.

Documents are Markdown files with YAML front matter, named
`AAAA-BBB-CCCC-description.md` (sequence, random id, type, slug). The file
format is the canonical representation in every store.

| | Plans | Stories |
|---|---|---|
| Types (default) | `drft`, `dsgn`, `impl` | `bugs` (bug, fix), `feat` (feature), `impr` (improvement, refactor), `chor` (chore), `task` |
| Extra front matter | | `purpose`, managed `started` |
| `links.plans` | `[planId, relation]`; parent, included, depends, blocks | `[planId, relation, [sections]?]`; included, depends, blocks; sections must exist in the plan's `progress` |
| `links.stories` | included, depends, blocks (target need not exist) | parent, included, depends, blocks (target must exist) |
| `links.repo` | `remote`, `local` | plus `pull_request`, `files` |
| `progress` keys | phase/section (`2`, `1.1`), may list `stories` | step at any depth (`2.1.3`) |
| Content the service edits | H1 | H1, `## Acceptance Criteria`, `## Work Log` |
| Completion rules | `complete` sets `completed` | `in_progress` sets `started`; `complete` requires every acceptance criterion checked (`force` does not override); finishing the last step while `in_progress` with all criteria checked auto-completes |
| `update` | lenient | rejects `progress` keys that no longer match a step heading (`UnknownStep`) |
| Default template | overridable, deletable (restores the built-in) | overridable, cannot be deleted |

Both kinds share: `id`, `title`, `type`, `status`, `priority`, optional
`effort`, managed `created`/`updated`/`completed`, `links` (`repo`, `specs`,
`web`, `stories`, `plans`) and `progress`. Plan files written with the earlier
top-level `plans` key still parse and are rewritten under `links.plans`.

## Build

```bash
cd mind-palace
go build -o mind-palace ./cmd/mind-palace
go build -o plan ./cmd/plan && go build -o story ./cmd/story   # optional kind-only binaries
go test ./...
```

Requires Go 1.26 (the toolchain is downloaded automatically by `go`).

## Quick start

```bash
# learn the content layouts and the creation schemas
mind-palace plan template get
mind-palace story template get
mind-palace story schema
mind-palace config                       # both kinds' types, statuses, synonyms, workflow, priorities, efforts

# create documents from JSON matching the schemas (file, - for stdin, or inline JSON)
mind-palace plan create --input plan.json
mind-palace story create --input story.json

mind-palace story list --type bugs --status in_progress
mind-palace story get 0002-a3f           # path, front matter and content
mind-palace story get 0002-a3f --content # raw Markdown

mind-palace story set-status 0002-a3f approved   # synonyms map to primary names (approved → ready)
mind-palace story link-plan 0002-a3f 0023-de3 included --sections 1.1,1.2
mind-palace story set-progress 0002-a3f 1.1 done
mind-palace story criteria 0002-a3f
mind-palace story check 0002-a3f 2                # mark criterion 2 done ([x]); "not_applicable" writes [~]
mind-palace story log 0002-a3f "Fixed the guard; 12/12 tests pass"
mind-palace story validate 0002-a3f
```

The `plan` and `story` binaries run the matching group directly
(`story get 0002-a3f` ≡ `mind-palace story get 0002-a3f`); top-level commands
(`serve`, `mcp`, `sync`, `config`, `stores`, `ops`, `schema`, `call`) pass
through.

Every command prints `{"ok": true, "result": …}` to stdout. Errors go to
stderr as `{"ok": false, "error": {"kind", "message", "problems"}}` with an
exit code per kind:

| Code | Kind | Raised when |
|---|---|---|
| 2 | BadRequest | Bad arguments, unknown operation or kind |
| 3 | ValidationError | The result of a write fails the JSON Schema (problems listed) |
| 4 | NotFound | No document matches the identifier, or a linked plan/story doesn't exist |
| 5 | InvalidTransition | The workflow doesn't allow the status change |
| 6 | ImmutableField | A write tries to change `id`, `created`, `updated`, `started` or `completed` |
| 7 | UnknownSection | A plan progress operation names a section with no numbered heading, or a story's plan link names a section missing from the plan's `progress` |
| 8 | LinkedPlan | `delete` on a plan that other plans or stories link to, without `--force` |
| 9 | Conflict | Duplicate id/template, protected default template, or a sync conflict |
| 10 | StorageError | A store failed |
| 11 | IncompleteCriteria | A story is moved to `complete` while an acceptance criterion is open |
| 12 | UnknownStep | A story progress operation (or `update`) names a step with no numbered heading |
| 13 | UnknownCriterion | A criteria operation names a position that doesn't exist |
| 14 | LinkedStory | `delete` on a story that other stories or plans link to, without `--force` |

`--format yaml` and `--compact` change the output shape; `--format text`
prints string results (content, templates) raw.

## Operations

Every entrypoint exposes the same operations with the same parameters for
both kinds; the document identifier parameter is named after the kind
(`plan` or `story`; `document` is always accepted) and story progress
operations take `step` where plans take `section`. `mind-palace ops` prints
every operation of every kind with its JSON Schema; `mind-palace call <op>
--kind story --params '{…}'` invokes any of them directly.

| Group | Operations |
|---|---|
| Documents | `create`, `get`, `getFrontMatter`, `getContent`, `list`, `update`, `delete`, `validate` |
| Templates | `getTemplate`, `listTemplates`, `createTemplate`, `updateTemplate`, `deleteTemplate` |
| Fields | `setTitle`, `setType`, `setStatus`, `getTransitions`, `setPriority`, `setEffort`, `clearEffort`, `patchFrontMatter`; stories: `setPurpose` |
| Links | `addPlanLink`, `removePlanLink`, `addStoryLink`, `removeStoryLink`, `addSpec`, `removeSpec`, `setWebLink`, `removeWebLink`, `setRepo`, `clearRepo`; stories: `setPlanSections`, `addFile`, `removeFile` |
| Progress | `getProgress`, `setProgress`, `removeProgress`; plans: `addProgressStory`, `removeProgressStory` |
| Criteria and work log (stories) | `getCriteria`, `setCriterion`, `addCriterion`, `removeCriterion`, `appendWorkLog` |
| Storage | `sync`, `getConfig`, `getSchema` |

An identifier is a document id (`0002-a3f`) or the path to its file. Plans and
stories have separate id sequences.

Rules that apply to every write:

- The result is validated against the kind's JSON Schema before saving; a write that fails is not saved.
- `updated` is stamped on every write; `id` and `created` are set once by `create`; `updated`, `started` and `completed` have no setters.
- Content (below the front matter) changes only through `update` and, for stories, the acceptance-criteria and work-log operations; front matter only through the setters.
- The content's H1 always matches `title`.
- Synonyms and labels are accepted for type, status, priority and effort and stored as their primary value (`refactor` → `impr`, `approved` → `ready`, `P1` → `1`, `Medium` → `M`).
- `create` seeds `progress` with `pending` for every numbered heading the body declares (unless `progress` is given).
- Reaching `complete` sets `completed` (and overwrites it on a later return); reaching `in_progress` sets `started` on stories. Neither is cleared by moving away, and both are omitted from the front matter until first set. Moving to `archived` moves the file into `archive/`; leaving it moves the file back.
- Timestamps are RFC 3339 UTC with millisecond precision (`2026-09-09T14:07:05.352Z`). RFC 3339 in any zone or precision, `YYYY-MM-DD HH:MM:SS UTC` and `YYYY-MM-DD` are accepted on input and normalised.
- Ids are always the full `AAAA-BBB` form; a short sequence part (`001-abc`) is zero-padded.
- `delete` refuses while any plan or story links to the document (unless forced); prefer `setStatus archived`.

## Entrypoints

```bash
mind-palace serve --addr 127.0.0.1:8087   # HTTP/REST + WebSocket + MCP (Streamable HTTP)
mind-palace mcp                           # MCP over stdio
```

### HTTP/REST (`/api/v1`)

- `POST /api/v1/{plans|stories}/ops/{op}` with the operation's JSON parameters as the body (any operation); `GET` works for read-only ones with query parameters. `GET /api/v1/{plans|stories}/ops` lists the kind's operations; `GET /api/v1/ops` lists both.
- Resource routes per kind: `GET|POST /plans`, `GET /plans/{id}`, `GET /plans/{id}/front-matter|content|transitions|progress|validate` (stories also `/criteria`), `PUT /plans/{id}/content`, `PUT /plans/{id}/status`, `PATCH /plans/{id}`, `DELETE /plans/{id}?force=1`, `GET /plans/templates`, `GET|PUT|DELETE /plans/templates/{id}`, `GET /plans/schema`; the same under `/stories`.
- `GET /config`; `/ops/{op}`, `/templates…` and `/schema` without a kind are shortcuts for plans.
- `GET /api/v1/events?kind=story&ids=0002-a3f` streams events as Server-Sent Events (`?plans=…` / `?stories=…` are shortcuts; no filter streams every kind).
- `GET /health`.

Responses use the same `{"ok": …}` envelope as the CLI; error kinds map to 400/404/409/500.

### WebSocket (`/ws`)

```jsonc
→ {"id": 1, "kind": "story", "op": "get", "params": {"story": "0002-a3f"}}    // kind defaults to plan
← {"id": 1, "kind": "story", "ok": true, "result": {…}}
→ {"id": 2, "op": "subscribe", "params": {"stories": ["0002-a3f"]}}         // or {"kind": "story", "ids": [...]}; omit both for every document
← {"id": 2, "ok": true, "result": {"subscribed": true, "kind": "story", "all": false, "ids": ["0002-a3f"]}}
← {"event": "story.updated", "kind": "story", "documentId": "0002-a3f", "operation": "setStatus", "document": {…}, "at": "…"}
→ {"op": "unsubscribe"}                                                      // or {"params": {"ids": [...]}}
```

A connection subscribes to one kind at a time (or to every kind without ids).

### MCP (`/mcp`, or stdio)

- One tool per operation and kind: `plan_create`, `story_setStatus`, … (`sync` and `getConfig` are shared; `--mcp-tool-prefix` adds a prefix to everything).
- Resources `plan://<id>`, `story://<id>` (Markdown) and `…/json`; subscribe to them for `resources/updated` notifications.
- `watch` tool: blocks until one of the given documents changes (`kind` + `ids`, or `plans` / `stories`) or a timeout passes, and returns the events.

Claude Code registration example (`.mcp.json`):

```json
{ "mind-palace": { "command": "mind-palace", "args": ["mcp"] } }
```

## Configuration

`mind-palace` looks for `--config`, then `$MIND_PALACE_CONFIG` (or the older
`$PLAN_CONFIG`), then `mind-palace.config.yaml` (`.yml`, `.json`,
`.mind-palace.yaml`, `.mind-palace.json`, or the older `plan.config.*` /
`.plan.*` names) in the working directory, then in each parent directory up
to your home directory; the nearest readable file wins. Relative `dir` values
and file-store directories are anchored to the config file's directory. Every
list replaces the default when given; see `mind-palace.config.example.yaml`.

```yaml
plans:
  dir: docs/plans             # archived plans go to docs/plans/archive
  types:                      # default: drft, dsgn, impl
    - {name: rfc, label: RFC}
  statuses:                   # primary name, label and synonyms (pending is required)
    - {name: pending, label: Pending, synonyms: [new, draft, todo]}
  workflow:                   # allowed transitions
    pending: [validated, blocked, rejected]
  priorities:
    - {value: "1", labels: [P1, Critical]}
  efforts:
    - {name: M, labels: [Medium], points: 5}
stories:
  dir: docs/stories
  types:                      # default: bugs, feat, impr, chor, task (with synonyms)
    - {name: bugs, label: Bug, synonyms: [bug, fix, defect]}
storage:
  - {name: file, kind: file}
  - {name: db, kind: postgres, options: {dsn: "postgres://…"}}
server:
  addr: 127.0.0.1:8087
```

The pre-mind-palace top-level keys (`plansDir`, `types`, `statuses`,
`workflow`, `priorities`, `efforts`) still apply to plans. The JSON Schemas'
enumerations are rewritten from the configuration, so `mind-palace schema
--kind story` always reflects the configured types, statuses, priorities and
efforts.

## Storage

Writes fan out to every enabled store; reads come from the first one (the
primary). File storage is the only plugin enabled by default and can never be
disabled: a `storage` list that omits the file entry or sets it `enabled:
false` is rejected at load time. Every plugin stores both kinds: documents
are keyed by kind and id, templates by kind and template id. If a secondary
copy fails, the primary write stands, the command succeeds with a `warning`,
and `mind-palace sync` reconciles.

| Kind | Options | Notes |
|---|---|---|
| `file` | `plansDir`, `storiesDir` (`dir` is an alias of `plansDir`) | Markdown files; `archive/` and `templates/` subfolders under each root |
| `sqlite` (`sqlite3`) | `dsn` (default `docs/mind-palace.sqlite`, next to the plans dir), `tablePrefix` (default `mp_`) | tables `mp_plans`, `mp_stories`, `mp_templates`; pure Go driver |
| `postgres` (`postgresql`, `pgx`) | `dsn`, `tablePrefix` | |
| `mysql` (`mariadb`) | `dsn`, `tablePrefix` | |
| `clickhouse` | `dsn`, `tablePrefix` | ReplacingMergeTree tables |
| `mongodb` (`mongo`) | `uri`, `database` (default `mind-palace`), `collection` prefix (default `mp_`) | |
| `firestore` | `project`, `database`, `collection` prefix (default `mp_`) | Application Default Credentials; honours `FIRESTORE_EMULATOR_HOST` |
| `memory` | | in-process, for tests |

Databases created by the earlier `plan` tool (tables `plan_plans`,
`plan_templates`) are not migrated: point `tablePrefix` / `collection` at a
fresh prefix and `sync` from the file store.

```bash
mind-palace sync --from db --to file --on-conflict error|skip|overwrite
```

`sync` copies plans, stories and templates. `error` stops at the first
document that differs, `skip` reports the conflicts and copies the rest,
`overwrite` makes the target match the source.

New plugins implement `store.Store` and register a factory with
`store.Register(driver, factory)`; `store/storetest` is the conformance suite
they must pass (it exercises every kind).

## Templates

Content is rendered from a Go `text/template` over the structured `body`
(`mind-palace plan template get` prints the plan default — Summary / Part 1 —
Design / Part 2 — Implementation; `mind-palace story template get` prints
the story default — Problem Statement / Steps / Acceptance Criteria / Files /
Proof / QA / Work Log). Templates are kept per kind:
`mind-palace story template create <id> --content t.md` adds alternatives,
selected with `create --template <id>`. Updating `default` overrides the
built-in; for plans, deleting the override restores it, while the story
default cannot be deleted (a story template must keep the Problem Statement,
Steps, Acceptance Criteria and Work Log sections the service reads).

## Layout

| Package | Responsibility |
|---|---|
| `kind` | The document kinds (plan, story) and the behaviour flags that differ between them |
| `model` | Document, front matter, links, progress types; YAML/JSON tuple forms; normalisation |
| `config` | Per-kind defaults, config file loading, synonym/label normalisation, workflow |
| `schema` | Embedded JSON Schemas (`plan.schema.json`, `story.schema.json`), configuration patching, validation |
| `markdown` | Front matter split/render, numbered headings, H1 sync, acceptance criteria and work log sections |
| `render` | Content templates and the body types of each kind |
| `service` | `Palace` (one `Service` per kind over a shared store), every operation and its rules; typed errors |
| `ops` | Per-kind operation registries shared by all entrypoints |
| `store`, `store/*` | Kind-aware store interface, fan-out, sync, plugins |
| `events` | In-process event bus (filter by kind and id) |
| `server/*` | HTTP, WebSocket and MCP entrypoints |
| `cli`, `cmd/*` | Command line: `mind-palace`, plus the `plan` and `story` wrappers |
