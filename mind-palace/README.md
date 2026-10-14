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
| Extra front matter | | `purpose`, `assignee`, managed `started` |
| `links.plans` | `[planId, relation]`; parent, included, depends, blocks | `[planId, relation, [sections]?]`; included, depends, blocks; sections must exist in the plan's `progress` |
| `links.stories` | included, depends, blocks (target need not exist) | parent, included, depends, blocks (target must exist) |
| `links.repo` | `remote`, `local` | plus `pull_request`, `files` |
| `progress` keys | phase/section (`2`, `1.1`), may list `stories` | step at any depth (`2.1.3`) |
| Content the service edits | H1 | H1, `## Acceptance Criteria`, `## Work Log` |
| Completion rules | `complete` sets `completed` | `in_progress` sets `started`; `complete` requires every acceptance criterion checked (`force` does not override); finishing the last step while `in_progress` with all criteria checked auto-completes |
| `update` | lenient | rejects `progress` keys that no longer match a step heading (`UnknownStep`) |
| Default template | overridable, deletable (restores the built-in) | overridable, cannot be deleted |

Both kinds share: `id`, `title`, `type`, `status`, `priority`, optional
`effort`, managed `created`/`updated`/`completed`, the planning fields
below, `links` (`repo`, `specs`, `web`, `stories`, `plans`) and `progress`.
Plan files written with the earlier top-level `plans` key still parse and
are rewritten under `links.plans`.

The planning fields are always written, with an empty value when unset:

```yaml
tags: ["feature", "security"]   # free-form labels, no whitespace, de-duplicated
milestone: milestone_beta       # the milestone the document ships in
due_date: ""                    # calendar dates, YYYY-MM-DD (a timestamp keeps its UTC day)
planned_start: ""
planned_end: ""                 # never before planned_start
order: 0                        # sort position, lower first
assignee: github:robbiebyrd     # stories only, provider:handle; omitted when unset
```

Files written before these fields existed still validate; they gain the keys
on their next write.

## Build

```bash
cd mind-palace
go build -o mind-palace ./cmd/mind-palace
go build -o plan ./cmd/plan && go build -o story ./cmd/story   # optional kind-only binaries
go test -race ./...                                          # in-memory stores only
MIND_PALACE_TEST_MONGODB_URI=mongodb://127.0.0.1:27017 go test -race ./...   # plus the MongoDB suites
```

Requires Go 1.26 (the toolchain is downloaded automatically by `go`). The
`plugin` package tests execute the Claude Code plugin's bash hooks and shim
from the repository root, so they need `bash`, `jq` and `curl`. Release
binaries for darwin, linux and windows are built by the `release` job of the
`Mind palace` workflow when a push to main carries a new `version` in
`.claude-plugin/plugin.json`; no tag is pushed by hand.

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
mind-palace story set-assignee 0002-a3f github:robbiebyrd
mind-palace story set-tags 0002-a3f feature,security
mind-palace story set-milestone 0002-a3f milestone_beta
mind-palace story set-planned 0002-a3f --start 2026-02-01 --end 2026-02-28
mind-palace story list --milestone milestone_beta --tag security --assignee github:robbiebyrd
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
| 15 | BlockedByDependency | starting a document while something it `depends` on is unfinished, or completing it while something that `blocks` it is, without `--force` |

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
| Fields | `setTitle`, `setType`, `setStatus`, `getTransitions`, `ready`, `setPriority`, `setEffort`, `clearEffort`, `setTags`, `addTag`, `removeTag`, `setMilestone`, `setDueDate`, `setPlannedDates`, `setOrder`, `patchFrontMatter`; stories: `setPurpose`, `setAssignee`, `claim` |
| Links | `addPlanLink`, `removePlanLink`, `addStoryLink`, `removeStoryLink`, `dependencies`, `addSpec`, `removeSpec`, `setWebLink`, `removeWebLink`, `setRepo`, `clearRepo`; stories: `setPlanSections`, `addFile`, `removeFile` |
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
- `tags` are trimmed and de-duplicated and may not contain whitespace; `due_date`, `planned_start` and `planned_end` accept any timestamp form and are stored as `YYYY-MM-DD`; `planned_end` may not be before `planned_start`; `order` is an integer; `assignee` is `provider:handle` without whitespace and only stories have it. `list` filters on `tag`, `milestone` and (stories) `assignee`.
- `create` seeds `progress` with `pending` for every numbered heading the body declares (unless `progress` is given).
- Reaching `complete` sets `completed` (and overwrites it on a later return); reaching `in_progress` sets `started` on stories. Neither is cleared by moving away, and both are omitted from the front matter until first set. Moving to `archived` moves the file into `archive/`; leaving it moves the file back.
- Timestamps are RFC 3339 UTC with millisecond precision (`2026-09-09T14:07:05.352Z`). RFC 3339 in any zone or precision, `YYYY-MM-DD HH:MM:SS UTC` and `YYYY-MM-DD` are accepted on input and normalised.
- Ids are always the full `AAAA-BBB` form; a short sequence part (`001-abc`) is zero-padded.
- `delete` refuses while any plan or story links to the document (unless forced); prefer `setStatus archived`.
- Dependencies are enforced: a document cannot move to `in_progress` while a plan or story it `depends` on is unfinished (not complete, rejected or archived), nor to `complete` while that holds or a document whose `blocks` link names it is unfinished (`BlockedByDependency`, exit 15, listing them; `force` overrides). A story whose last step completes stays in progress while one is open. `ready` lists the documents that can start now, by priority, order and id; `dependencies` lists what holds one up.
- `claim` (stories) sets the assignee and starts the story in one write, so parallel agents never take the same work: another assignee's story is a `Conflict` (unless forced) and the holder's own claim changes nothing. Claims are serialised in the process and, on the file store, across processes by a lock file under the system temp directory.

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

`mcp` and `serve` also expose the session tools; pass `--no-session-tools` when
another server (for example `session mcp`) already provides them.

`mcp --tools core` (or `MIND_PALACE_MCP_TOOLS=core`) serves only the core
operations (reading and writing documents, the workflow, criteria, progress,
links, the ready queue) and leaves the inlined creation schema out of
`<kind>_create`: 52 tools and about 29 KB of `tools/list`, against 104 tools
and about 81 KB for `full`, the default. The plugin serves the core tier;
every operation stays available through the CLI, HTTP and `--tools full`.

### Review and hooks

```bash
mind-palace review                          # plans whose stories are all complete, stories whose steps and criteria are all done, still open
mind-palace hook stop-guard                 # Stop hook: reads the hook JSON on stdin, prints a block decision on violations
mind-palace hook status-review --every 30m  # SessionStart/UserPromptSubmit hook: asks about the review's findings
```

`review` reports documents whose work is finished while their status is still
open (`Palace.Review`): a plan is listed when it has at least one story (in
`links.stories`, on its progress entries or linking back to it through
`links.plans`) and every one of them is `complete`; a story when every step is
complete and no acceptance criterion is open. Complete, archived and rejected
documents are skipped. The `hook` commands print the JSON Claude Code expects
rather than the usual envelope; the `stop-guard` rules are in `hookguard`.

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
| `webdav` (`dav`) | `url` (required), `plansPath` (default `plans`), `storiesPath` (default `stories`), `username`, `password` or `token`, `timeout` (default `30s`) | Markdown files in a WebDAV folder (Nextcloud, ownCloud, mod_dav, a NAS) with the file store's layout under each path; credentials may also come from the URL's userinfo or `MIND_PALACE_WEBDAV_USERNAME` / `_PASSWORD` / `_TOKEN`; missing folders are created on first use |
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

## Sessions

The `memory` package captures what the claude-remember plugin writes for a
project: `mind-palace session memory-ingest` scans the store, posts each new
record to the daemon as a `MemorySaved` hook event (the store keeps one per
account and record hash) and `session search-memory` / the `search_memory`
tool search them across sessions.

`mind-palace` also holds the clued session capture: the Claude Code plugin's
hook events and transcripts are mirrored into MongoDB and queried through the
`session` group, with no Node runtime involved. `go test ./...` runs the
session tests against an in-memory store; the MongoDB-backed ones run only
when `MIND_PALACE_TEST_MONGODB_URI` points at a MongoDB.

**Config.** `~/.claude/plugins/data/clued/config.json` (override with
`session --config`). Keys: `mongoUrl` (default `mongodb://localhost:27018`),
`dbName` (`claude_sessions`), `port` (`8085`), `projectsDir`
(`~/.claude/projects`), `fileHistoryDir` (`~/.claude/file-history`),
`disabledEnrichers`, `enabledEnrichers` (switches on enrichers that ship
disabled, such as `privacy-redact`), `claudeAppConfigPath` and `devRepoPath` (read by the
plugin's `scripts/clued` shim, not by the binary). The write-ahead log defaults
to `events.wal` next to the config file and is set only through
`CLUED_WAL_PATH`, not a config key. Environment variables override the file:
`CLUED_MONGO_URL`, `CLUED_DB_NAME`, `CLUED_PORT`, `CLUED_PROJECTS_DIR`,
`CLUED_FILE_HISTORY_DIR`, `CLUED_CLAUDE_APP_CONFIG_PATH`, `CLUED_WAL_PATH`.

**Commands.**

```bash
mind-palace session find [--project-path re] [--git-origin re] [--query re] [--limit n]
mind-palace session search-commands <pattern> [--session id] [--git-origin re] [--limit n]
mind-palace session search <words…> [--session id] [--git-origin re] [--project re] [--limit n]
mind-palace session usage [--by session|model|day] [--session id] [--git-origin re] [--project re] [--since day] [--until day] [--limit n]
mind-palace session context <session>
mind-palace session full <session>
mind-palace session transcript <session> [--offset n] [--limit n]
mind-palace session restore <session> [--project-path p] [--projects-dir d]
mind-palace session backfill
mind-palace session daemon [--addr host:port]   # POST /event, GET /health; default 127.0.0.1:8085
mind-palace session mcp                         # session tools only, over stdio
mind-palace session whoami                      # remote mode: the token's name, account and projects
mind-palace hook relay                          # remote mode, run by the hooks: store one event, push the transcript delta
mind-palace server run [--addr] [--tls-cert --tls-key | --behind-proxy]   # the clued server
mind-palace server token create|list|revoke|grant                        # its bearer tokens
```

**Remote mode.** With `serverUrl` and `serverToken` set (env
`CLUED_SERVER_URL`, `CLUED_SERVER_TOKEN`; optional `serverCaFile`,
`serverInsecure`), the client keeps no MongoDB and runs no daemon: the
session store is `session/remotesession` over a clued server, each hook runs
`hook relay` (`session/ingest` plus `session/catchup`, state under
`<config dir>/catchup/`, failures in `relay.log`), and the plan and story
services opened through `clientcfg` add the server as the `clued-server`
store. `server run` serves `server/sessionapi`, `server/storeapi` and
`/api/v1/whoami` behind `server/auth`. The repository README's "Remote mode"
section and `docs/server.md` describe setting it up.

The daemon stores each `POST /event` (store writes happen off the request
path), and on the first event of a session starts tailing its transcript and
watching its subagent files, tool-result blobs and file-history backups. It
only tails transcripts under `projectsDir`, requires session ids to be a
single path element, rejects browser-originated requests and caps bodies at
16 MiB. Events the plugin's hook could not deliver are appended to the
write-ahead log and replayed at start and every 60 seconds. Every document
is stamped with the Claude account id (from `claudeAppConfigPath`, or
`unknown`) and the host (hostname, user, platform, release, architecture,
primary IP and MAC); queries and restores are scoped to the account.

The enrichment loop runs every 5 seconds over the enrichers in
`session/enrichers` (six on `hook_events`, fourteen on `transcript_lines`;
`privacy-redact` is off unless listed in `enabledEnrichers`) and stores results under
`enrichments.<name>`. Documents an enricher does not apply to are marked
skipped so they leave its queue; transient write failures are retried; a
panicking enricher is recorded, not fatal. Restore writes only under the
projects and file-history directories and produces equivalent JSON whose key
order may differ from the original file.

**MCP tools.** `find_sessions`, `get_session_context`, `get_full_session`,
`search_commands`, `search_memory`, `file_history`, `find_errors`,
`search_transcripts`, `usage`, `read_transcript`, `restore_session`, `forget_session` and `prime_context`
(the context collector's report for a `cwd`; see `mind-palace context --help`).
`session mcp` serves just these; `mind-palace mcp` and `mind-palace serve`
expose them next to the plan and story tools.

**Store layout.** The `session` package defines the store interface;
`session/mongosession` is the MongoDB implementation (collections `sessions`,
`hook_events`, `transcript_lines`, `subagent_lines`, `blobs` and the
`session_full` view, as the TypeScript plugin created them) and
`session/memsession` the in-memory one used by tests. `daemon`, `tail`,
`backfill`, `enrich`/`enrichers`, `query`, `restore`, `wal`, `gitinfo`,
`hostinfo` and `sessionmcp` sit on top of it.

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
| `store/remotestore` | The `clued` storage plugin: plans and stories on a clued server, one project per store instance, over `remote` against `server/storeapi` |
| `events` | In-process event bus (filter by kind and id) |
| `server/*` | HTTP, WebSocket and MCP entrypoints, and the clued server (`server/clued.go`, `mind-palace server run`) |
| `server/auth` | Bearer tokens for the clued server: hashed token file managed by `server token`, constant-time compare, mtime reload, 401/403 in the httpapi envelope |
| `server/sessionapi` | The `session.Store` port over HTTP (`/api/v1/session/…`), every route scoped to the token's account |
| `server/storeapi` | The `store.Store` port over HTTP (`/api/v1/projects/{project}/store/…`); the raw document transport behind `store/remotestore` |
| `server/projects` | Project slugs derived from a git origin and one `store.Store` per project built from the server's storage definitions |
| `server/stack` | The Docker Compose stack `mind-palace server init` generates (nginx front-end, clued server, MongoDB), its nginx and storage configs, a private CA with a server certificate, and the first token |
| `remote` | The HTTP client the remote adapters share: base URL, bearer token, CA file, the plain-`http://`-off-loopback refusal, envelope decoding into typed errors |
| `clientcfg` | Opens the plan and story services as the client does: the repository's config plus, in remote mode, the `clued-server` store as a secondary store; resolves the project slug (`remote.project`, git origin, directory name) |
| `session`, `session/*` | Claude Code session capture: config (local and remote mode), MongoDB and in-memory stores, daemon, transcript tailing, backfill, enrichers, queries, restore and the session MCP tools |
| `session/redact` | Capture-time redaction (`redactAtCapture`, `redactPatterns`) and exclusion (`excludePaths`) as a `session.Store` decorator every client store is wrapped in, so no write path stores a secret |
| `session/ingest` | Hook-event bookkeeping shared by the daemon and `hook relay`: session tracking, stamping, store write, WAL fallback |
| `session/catchup` | One-shot replacement for the tailers: pushes a session's transcript delta and changed artifact files, with state and lock files per session |
| `session/remotesession` | `session.Store` over a clued server (one HTTP call per port method to `server/sessionapi`); the query service, restore, backfill and the MCP tools run unchanged on it |
| `hookguard` | Rules behind the Claude Code hooks the binary serves (the Stop guard) |
| `plugin` | Tests for the Claude Code plugin glue (`scripts/clued`, the bash hooks, `hooks.json`) in local and remote mode |
| `cli`, `cmd/*` | Command line: `mind-palace`, plus the `plan` and `story` wrappers |
