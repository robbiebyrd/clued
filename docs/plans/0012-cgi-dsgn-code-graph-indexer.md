---
id: 0012-cgi
title: 'Code graph indexer: tree-sitter parsing for 21 languages, a layered FalkorDB graph and agent tools'
type: dsgn
status: pending
priority: "3"
effort: XL
created: "2026-10-19T17:49:42.286Z"
updated: "2026-10-19T17:51:56.623Z"
tags:
  - code-graph
  - falkordb
  - tree-sitter
  - search
  - mcp
  - server
  - cloud
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 0
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
  plans:
    - ["0010-awy", "depends"]
progress:
  "1":
    status: pending
  "1.1":
    status: pending
  "1.2":
    status: pending
  "2":
    status: pending
  "2.1":
    status: pending
  "3":
    status: pending
  "3.1":
    status: pending
  "3.2":
    status: pending
  "4":
    status: pending
  "4.1":
    status: pending
  "4.2":
    status: pending
  "5":
    status: pending
  "5.1":
    status: pending
  "6":
    status: pending
  "6.1":
    status: pending
  "6.2":
    status: pending
  "6.3":
    status: pending
---

# Code graph indexer: tree-sitter parsing for 21 languages, a layered FalkorDB graph and agent tools

## Summary

**Goal:** An agent in a Claude Code session can ask the clued server for a symbol, its callers and callees, or code matching a description, and get a compact, token-capped answer drawn from a code graph that matches its own working tree (base, branch and its session overlay), instead of spending turns and tokens on grep and whole-file reads.

**Problem:** Plans 0010 and 0011 collect every project's code continuously (blobs, per-worktree trees, a change log, and the facts that place each file in the base, a branch layer or a session overlay), but nothing turns it into something an agent can query. Today an agent finds code by grep, glob and reading whole files; most of its tokens go on finding, not on the code it ends up using. Indexes others built for this went stale, split code mid-function, or returned chunks without context; vector search in particular showed little payoff for code, which is why Claude Code moved from a vector index to agentic search.

**Approach:** A server-side indexer replays the 0010 change log per project, parses each file version once with a pure-Go tree-sitter runtime (cached by content hash) for Python, C, C++, C#, Objective-C, Swift, Java, JavaScript, TypeScript, SQL, Rust, Go, PHP, Perl, OCaml, Dart (including Flutter projects), Ruby, Kotlin, Lua and Lisp, and builds one FalkorDB graph per namespaced project with base, branch and session-overlay layers whose visibility is resolved per query. Extraction is driven by per-language query files (tree-sitter's tags-query convention) plus a small resolution profile per language family, so adding a language is data, not code. Cross-file edges (calls, references, implementations, includes) are resolved per layer. Agents reach the graph through a few typed, token-capped MCP tools that read the session's own view, with exact-name and full-text search; no raw Cypher leaves the server. Vector embeddings are deliberately left out. An evaluation harness on our own repositories measures tokens, turns and success with and without the tools.

---

# Part 1 — Design

## Current State

- Plan 0010 (pending): `code.Store` per namespaced project holds blobs (SHA-256 of redacted content), trees per (worktree, ref) with `git_oid`, `dirty`, `unpushed`, `session`, an ordered change log (`Changes(afterSeq)`), worktrees with owner, default branch and session activity. It fixes the layering: base (default branch at its pushed commit), branch layers per (ref, upstream commit), session overlays per (worktree, ref); one FalkorDB graph per namespaced project; file-local facts cached by content.
- Plan 0011 (pending): a GitHub App keeps the base and branch layers current from pushes, with a branch policy (default branch plus active branches by default).
- Plan 0008 established token-capped tool results; the `clued` MCP server (`session/sessionmcp`) serves session tools over the remote client.
- Both binaries build with `CGO_ENABLED=0`; the mainstream Go tree-sitter binding needs cgo. `go-licenses` refuses forbidden, restricted and unknown licences in `mind-palace`.
- FalkorDB is not deployed anywhere yet; `server init` generates nginx, the clued server and `mongo:8`.

## Decisions

- **Tree-sitter parsing is required; the parser must be pure Go (gotreesitter or WebAssembly grammars under wazero), chosen by a spike.** — The graph needs symbols and references from every supported language, and both binaries are built without cgo for seven targets.
- **No vector embeddings; search is exact name, qualified name and full text, expanded through the graph.** — Little payoff for code relative to the graph and exact names, while adding a third-party data flow, cost and a consent step; agents already phrase most lookups with names, and the graph answers the 'how is it connected' questions vectors can't.
- **Generic extraction from per-language query files and resolution profiles.** — Twenty-one languages can't each have hand-written extractors; tree-sitter's tags-query convention already covers most of them, so a language is data plus a profile entry.
- **Retrieval units are symbols and structural chunks, parsed once per content hash.** — Returning a function with its signature and line range is what saves the agent's tokens; caching by content shares the parse across branches, layers and members (the 'easy part of C' from 0010).
- **Visibility through `hiddenBy` on lower-layer files plus per-layer edges.** — A view query stays a property filter over a short list of layer ids, with no per-query set of visible paths, while overlays can still redirect base callers to renamed or rewritten symbols.
- **The graph is a rebuildable cache; Mongo stays the source of truth.** — FalkorDB holds graphs in memory; being able to drop, unload and rebuild them bounds memory and removes backups and migrations.
- **Typed, token-capped operations instead of raw Cypher, answering for the caller's own view.** — Tenancy, overlay privacy and token budgets are enforced in one place; agents get stable, small results.
- **Name resolution scoped by each language's imports, includes, packages, modules or namespaces, with a confidence on each edge.** — It works the same way for every tree-sitter language and is good enough to beat grep; precise resolvers can upgrade edges per language later.

### Rejected alternatives

- **Vector embeddings on symbol chunks (in FalkorDB or a separate vector database) with hybrid search.** — Decided against: little payoff for code, plus cost, a third-party data flow and a consent step.
- **Fixed-size text chunks.** — Splits code mid-function and loses the signature; structural chunks are the main reason code retrieval works at all.
- **cgo tree-sitter in a separate indexer binary.** — Adds a second build and release pipeline and a native toolchain; pure-Go options exist and the spike decides between them.
- **SCIP or LSIF indexes produced by language-specific indexers.** — Precise, but needs a toolchain per language on the server and a build of each project; a later precise resolver can import SCIP into the same edges.
- **Materializing a full graph per view.** — Rejected in 0010: cost multiplies by branches and members; layers with `hiddenBy` keep cost proportional to diffs.
- **A hand-written extractor per language.** — Doesn't scale to 21 languages; query files plus profiles do, and upstream tags queries give a head start.

## Design

### Parsing (`codeparse`)

**Spike first (phase 1.1).** Compare gotreesitter and tree-sitter WebAssembly grammars under wazero on: builds with `CGO_ENABLED=0` for all 7 release targets; passes `go-licenses check`; has a working grammar for every supported language (table below), with the official grammars' node types; runs tree-sitter queries (needed for extraction); throughput on the clued repository (files/s, peak memory); binary size added; maintenance (releases, issues). Record the choice and the numbers in the plan's work log. `go/parser` is the fallback for Go. If a grammar is missing from the chosen runtime, it is added to it (gotreesitter converts upstream `parser.c` tables; wazero loads the upstream `.wasm` build), and the spike says which ones need that.

**Facts for one file** (a pure function of its bytes and the parser version):
```go
type Facts struct {
    Lang     string
    Package  string            // Go package, Python module path, TS module specifier
    Symbols  []Symbol          // func, method, type, interface, class, struct field, const, var, enum
    Imports  []Import          // spec, alias, imported names
    Refs     []Ref             // unresolved: name, qualifier, kind (call, type, value, extends, implements), position, enclosing symbol
    Chunks   []Chunk           // retrieval units
}
type Symbol struct { ID, Kind, Name, QualName, Signature, Doc string; Start, End Pos; Parent string; Exported bool }
type Chunk struct { ID, Kind string; SymbolID string; Start, End Pos; Text string; Hash string }
```
- **Chunks follow structure**: one per top-level function, method or type (with its doc comment and signature), large bodies split at statement boundaries into ≤ 1,500-token parts that repeat the signature; Markdown is chunked by heading; other text files by 60-line windows with 10 lines of overlap.
- **Cache**: `code_parses` (Mongo, per namespaced project or shared per deployment, keyed by `(sha256, parser version)`), so a file version is parsed once for every branch, layer and member. A parser upgrade bumps the version and re-parses lazily.
- Limits: files over 1 MiB are already content-less (0010); a parse is cut off after 2 s and the file is indexed as text.

### Languages

Extraction is generic: each language has a **query file** in tree-sitter's tags-query convention (`@definition.function`, `@definition.class`, `@definition.method`, `@definition.interface`, `@definition.module`, `@reference.call`, `@reference.class`, `@reference.implementation`, plus clued's `@import`, `@package`, `@doc`, `@signature` captures) and a **resolution profile** for its family (how names are scoped and imported). Upstream grammars ship `tags.scm` for many of these languages; clued's query files start from them and add the missing captures. Adding a language is a grammar, a query file and a profile entry, no new Go code.

| Language | Files | Symbols | Imports and scope (resolution profile) |
|---|---|---|---|
| Python | `.py`, `.pyi` | functions, classes, methods, module variables | `import` / `from … import`, modules and packages by path |
| C | `.c`, `.h` | functions, structs, unions, enums, typedefs, macros, globals | `#include` (quoted and angle), translation-unit scope, header/source pairing by name |
| C++ | `.cc`, `.cpp`, `.cxx`, `.hpp`, `.hh`, `.h` | classes, structs, functions, methods, namespaces, templates, enums | `#include`, namespaces, `using`, out-of-line method definitions (`A::f`) |
| C# | `.cs` | classes, structs, records, interfaces, enums, methods, properties | namespaces (file-scoped too), `using`, partial classes merged |
| Objective-C | `.m`, `.mm`, `.h` | `@interface`, `@implementation`, `@protocol`, methods (selectors), categories, C functions | `#import` / `#include`, `@import`, selector-based calls |
| Swift | `.swift` | classes, structs, enums, protocols, extensions, functions, methods | `import` (modules), extensions merged into their type, protocol conformance |
| Java | `.java` | classes, interfaces, enums, records, methods, fields | `package`, `import` (single and on demand), `extends` / `implements` |
| JavaScript | `.js`, `.mjs`, `.cjs`, `.jsx` | functions, classes, methods, exported bindings | ES `import` / `export`, CommonJS `require`, relative module paths |
| TypeScript | `.ts`, `.tsx`, `.mts`, `.cts`, `.d.ts` | the JavaScript set plus interfaces, type aliases, enums, namespaces | as JavaScript plus `tsconfig.json` `paths` and `baseUrl` when present |
| SQL | `.sql` | tables, views, columns, functions, procedures, triggers, indexes | schema-qualified names; references from queries (`FROM`, `JOIN`, `INSERT INTO`, `CALL`) to tables, views and routines; dialect-tolerant grammar, statements it can't parse are indexed as text |
| Rust | `.rs` | functions, structs, enums, traits, impls, modules, macros | `mod` tree from the crate root, `use`, `impl Trait for Type` |
| Go | `.go` | functions, methods, types, interfaces, constants, variables | packages by directory, `import` paths and aliases, `go.mod` module path |
| PHP | `.php` | classes, interfaces, traits, functions, methods, constants | `namespace`, `use`, `require` / `include` |
| Perl | `.pl`, `.pm`, `.t` | packages, subs | `package`, `use` / `require`, `Package::sub` calls |
| OCaml | `.ml`, `.mli` | modules, functors, types, values, functions, classes | `open`, qualified `Module.name`, `.mli` / `.ml` pairing, dune library names when present |
| Dart (and Flutter) | `.dart` | classes, mixins, extensions, enums, functions, methods | `import` / `export` / `part`, `package:` URIs via `pubspec.yaml`; Flutter is Dart and needs no separate grammar, widgets are classes |
| Ruby | `.rb`, `.rake`, `Gemfile` | classes, modules, methods, constants | `require` / `require_relative`, nested `Module::Class`, mixins (`include`, `extend`) |
| Kotlin | `.kt`, `.kts` | classes, objects, interfaces, functions, properties, extension functions | `package`, `import`, extension receivers |
| Lua | `.lua` | functions, local functions, table-field functions (`M.f`, `M:f`) | `require`, module tables |
| Lisp | `.lisp`, `.lsp`, `.cl`, `.asd`, `.el`, `.clj`, `.cljs`, `.cljc`, `.edn`, `.scm`, `.ss`, `.rkt` | `defun`, `defmacro`, `defvar`, `defclass`, `defmethod`, `defpackage` / `ns`, `define` | packages / namespaces (`in-package`, `ns`, `require`), symbol names qualified by package |

- **Lisp** covers Common Lisp, Emacs Lisp, Clojure and Scheme/Racket, each through its own grammar and query file under the one Lisp entry (decided).
- **Ambiguous extensions**: `.h` is C, C++ or Objective-C by content (`@interface`, `class`, `template`, `namespace`) and by the other files in the same directory; `.m` is Objective-C (MATLAB is not supported).
- **Mixed files**: `.vue`, `.svelte` and HTML script blocks, SQL embedded in other languages, and Markdown code fences are indexed as text in this plan.
- **Waves**: all 21 ship in this plan, in three waves so the first is usable early: (1) Go, Python, TypeScript, JavaScript, Java, C#, Rust; (2) C, C++, Objective-C, Swift, Kotlin, Dart, PHP, Ruby; (3) SQL, Perl, OCaml, Lua, Lisp.

### Graph schema (`codegraph`)

One FalkorDB graph per namespaced project; the graph key is the namespace (`u<user>__<slug>` / `w<workspace>__<slug>`), so tenancy is physical.

| Node | Key properties |
|---|---|
| `Layer` | `id` (`base`, `branch:<ref>@<commit>`, `wt:<worktree>:<ref>`), `kind`, `parent`, `owner`, `ref`, `commit`, `updated` |
| `File` | `layer`, `path`, `sha256`, `git_oid`, `lang`, `package`, `hiddenBy` (list of layer ids) |
| `Package` | `layer`, `name`, `kind` (package, module, namespace, crate, header, schema) |
| `Symbol` | `layer`, `id`, `kind`, `name`, `qualName`, `signature`, `doc`, `path`, `start`, `end`, `exported` |
| `Chunk` | `layer`, `id`, `kind`, `path`, `start`, `end`, `hash` |

| Edge | Meaning |
|---|---|
| `CONTAINS` | Layer→File, File→Symbol, Symbol→Symbol (methods, fields), File→Chunk, Symbol→Chunk |
| `IN_PACKAGE` | File→Package |
| `IMPORTS` | File→Package (`alias`), including C-family `#include` / `#import` to header files |
| `CALLS`, `REFERENCES`, `IMPLEMENTS`, `EXTENDS` | Symbol→Symbol, with `layer`, `confidence` (`exact`, `qualified`, `name`), `count`, first `line` |

**Visibility of a view.** A view is an ordered list of layers, e.g. `[base, branch:feature@abc, wt:wt_12…:feature]`. A file in a lower layer is hidden in that view when a higher layer replaces or deletes its path; the indexer records this as the higher layer's id in the lower file's `hiddenBy`. A node is visible when `n.layer IN $view AND none(h IN file.hiddenBy WHERE h IN $view)`; an edge is visible when its `layer` is in the view and both endpoints are visible. Deletions are recorded as a `File{deleted: true}` tombstone in the higher layer.

**Indexes**: range indexes on `File(layer, path)`, `Symbol(name)`, `Symbol(qualName)`, `Symbol(layer, id)`; full-text on `Symbol.name`, `Symbol.signature`, `Symbol.doc` (FalkorDB's full-text procedures, verified in the spike). No vector index.

**The graph is a derived cache.** Mongo (0010's `code.Store` plus the parse cache) is the source of truth; any graph can be dropped and rebuilt (`mind-palace server graph rebuild <project>`). This bounds FalkorDB's in-memory footprint (idle projects' graphs can be unloaded) and means no graph backups.

**Port**: `codegraph.Graph` with `ApplyFile`, `RemoveFile`, `SetHidden`, `PutEdges`, `DropLayer`, `Drop`, and typed reads (`FindSymbols`, `Symbol`, `Neighbors`, `Outline`, `SearchText`), each taking a `View`. FalkorDB adapter over its Redis protocol (`GRAPH.QUERY` with parameters, never string-built queries); in-memory adapter for tests.

### Indexer (`codeindex`)

**Input**: `code.Store.Changes(afterSeq)` per namespaced project, with a cursor stored per project (`code_index_cursors`). Workers take one project at a time (a lease, as 0011's jobs); `server run` and clued-cloud run the same `codeindex.Service`.

**Choosing layer sources** (from 0010's facts):
- **Base**: the default branch's entries with `dirty: false, unpushed: false`, from the GitHub-backed worktree (0011) when there is one, else from client worktrees on the default branch, newest upstream commit wins; it moves when a newer upstream commit is seen.
- **Branch layer** per (ref, upstream commit): clean, pushed entries of that ref, holding only paths whose `git_oid` differs from the base, plus deletions. Kept while any overlay sits on it or it's the ref's newest; older ones are dropped.
- **Session overlay** per (worktree, ref): entries with `dirty` or `unpushed`, plus deletions relative to its branch layer (or the base when the branch has no upstream). Owner and `session` tags copied from the entries.

**Per changed file in a layer**: load facts from the parse cache (parse on a miss), `ApplyFile` (replace the layer's File, Symbols, Chunks for that path), update `hiddenBy` on the same path in the layers below, then queue resolution.

**Resolution** (per layer, after a batch): each `Ref` is resolved against the symbols visible in that layer's view, following the language's resolution profile: same scope, same file, same package/module/namespace, then imported or included ones by alias and qualifier (C-family includes resolved by path against the project's include directories when found in build files such as `CMakeLists.txt` or `compile_commands.json`, else by relative path and basename); ambiguous matches keep the best by kind and visibility and record `confidence: name`. When a layer changes the definitions in a file, the references *into* those names from lower layers are re-resolved for this layer's view (found through the `Symbol(name)` index), and the resulting edges are written with this layer's id, so a base caller of a function an overlay renamed points at the overlay's symbol only in that overlay's view. Re-resolution is bounded per batch (default 5,000 references) and continues in the next batch.

**Lifecycle**: an overlay with no session activity for 14 days and nothing dirty or unpushed is dropped (0010's rule); a worktree removal or a GitHub branch deletion drops its layers; a project purge drops the graph.

**Rebuild**: drop the graph and replay the project's current trees (not the whole log) through the same path; used after schema or parser changes, and to load an unloaded project on first query.

### Read API and agent tools (first exposure)

**Routes**, mounted beside 0010's write routes under `/api/v1/projects/{project}/code/graph/`, behind `auth.Middleware` and `NamespaceCtx` (fail closed), all `POST` with typed bodies:

| Route | Body | Result |
|---|---|---|
| `search` | `{view, query, kinds?, langs?, paths?, limit}` | exact and qualified name matches first, then full-text matches over names, signatures and doc comments, ranked, with symbols and chunks: `qualName`, `kind`, `signature`, `path:start-end`, score, a one-line doc |
| `symbol` | `{view, id or qualName, include: [body, doc, children]}` | the symbol with its source span (from the blob, redacted) |
| `neighbors` | `{view, id, relation: callers|callees|references|implementations|imports|importers, depth ≤ 3, limit}` | the subgraph as rows (`from`, `relation`, `to`, `path:line`, confidence) |
| `outline` | `{view, path or package}` | the symbol tree with signatures, no bodies |
| `context` | `{view, task, budget}` | a bundle: top search hits, their signatures, one hop of callers and callees, and the bodies that fit the budget |

- **View resolution**: `view` is `{session_id}` (the server finds the session's worktree and ref from 0010's session records, which must belong to the caller), or `{ref}` (base plus that branch layer), or omitted (base). A caller never sees another account's overlay; overlays are private to their owner and can never be shared, in a workspace too (decided in 0010).
- **Token caps**: every result is sized and cut the way the session tools already are (`max_tokens` and `cursor`, `session/sessionmcp/budget.go`, plan 0008) at `budget` (default 2,000 tokens; `context` default 6,000), with a `truncated` flag and the cursor to continue.
- **MCP tools** in the `clued` server: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`. They pass the current session id automatically, describe results as `path:line` so the agent can open exactly the lines it needs, and are listed only when the project is indexed and the entitlement allows.
- Bodies are served from the stored (redacted) blobs; nothing returns more than the caller could have collected themselves, plus pushed code of the project they can read.

### Evaluation (`eval/codesearch`)

A fixed set of tasks, each with a ground truth (the files and symbols a correct answer needs):
- about 40 on clued and clued-cloud (Go, Markdown, shell);
- about 3 per other supported language on a pinned corpus of well-known public repositories (one or two per language, commit pinned), so every language's extraction and resolution is exercised.

Families: **named lookups** ('where is `allocateID` defined and who calls it?'), **concept lookups** ('where are plan sequence numbers allocated?'), **impact** ('what breaks if `relayBudget` changes?'), and **small edits** that need two or three files.

Each task runs in a headless Claude Code session (`claude -p`, remote mode, the same model and prompt) in three arms: grep/read only; plus the graph tools; plus `code_context` only. The harness reads tokens, turns and tool calls from clued's own capture (`usage`, `get_activities`) and scores success against the ground truth (and, for edits, the tests). Each arm runs three times per task; the report gives medians per task family and per language. It decides tool defaults (budgets, depth, which tools are listed) and guards against regressions; it is not a gate for shipping.

### Deployment

- **Self-hosted**: `server init` adds a `falkordb/falkordb` container (pinned version, no port published outside the stack network, password from the generated secrets, persistence off since the graph is rebuildable) and passes `--graph redis://…` to `server run`; Apple Containers gets the same in `up.sh`. Without `--graph` the indexer and the read routes are off and the MCP tools aren't listed.
- **clued-cloud**: a FalkorDB deployment (sized from the evaluation's memory numbers), `--graph-url` / `CLUED_CLOUD_GRAPH_URL`, `codeindex` workers in the process, `Limits.CodeGraph` (off by default until launch), metrics (`code_index_files{lang}`, `code_index_lag_seconds`, `code_graph_queries{op,outcome}`, `code_parse_errors{lang}`), purgers that drop the project's graph and caches, graph unloading for projects idle 30 days.
- The Go side talks to FalkorDB over the Redis protocol; any client library added must pass `go-licenses`.

### Error Handling

- A file that fails to parse or times out: indexed as text (file node, window chunks, full-text), with a `parse_error` flag.
- FalkorDB unavailable: the indexer pauses with back-off and the cursor doesn't move; read routes return 503 `unavailable` and the MCP tools say the index is unavailable (the agent falls back to grep).
- A project not yet indexed: `search` and friends return `indexing` with progress; `context` returns what is ready.
- A session id that isn't the caller's or isn't known: 404; a view whose branch layer was dropped falls back to the newest branch layer of that ref, then the base.
- A graph that drifts from the store (bug, crash mid-batch): `rebuild` restores it; a periodic consistency check compares file counts and hashes per layer.

### Edge Cases

- Generated or vendored code that is synced: indexed like any file; `codeSyncExclude` keeps it out at the source.
- Huge files near the 1 MiB limit with thousands of symbols: chunk count capped per file (1,000), the rest searchable by full-text only.
- Mixed-language files (Vue, Svelte, HTML with scripts, SQL inside strings, Markdown fences): indexed as text; TSX and JSX use their own grammars.
- A `.h` file in a repository mixing C, C++ and Objective-C: classified by content and neighbours; a wrong guess still yields file, include and text results.
- C and C++ macros and conditional compilation: parsed as written (no preprocessing); symbols in every branch of `#if` are indexed.
- SQL dialects (PostgreSQL, MySQL, SQLite, T-SQL): statements the grammar can't parse are indexed as text; schema objects are still found by the statements it can.
- Lisp macros that define things (`defsomething`): the query file lists the defining forms per dialect; unknown ones are references only.
- Same symbol name in many packages: `confidence: name` edges are kept but ranked below qualified ones; tools show the confidence.
- A rename in an overlay: the base symbol is hidden in that view, base callers are re-resolved to the new name where they reference it, and stay unresolved (shown as such) where they don't.
- Two overlays editing one file: each view sees its own version; nothing is merged.
- A session in a worktree that isn't synced (codeSync off): the view falls back to the branch layer or the base, and the tools say the results don't include local edits.
- A parser upgrade: version bump, lazy re-parse, and a background rebuild of active graphs.

## Considerations

- **Security:** - The read routes are the first exposure of collected code: authenticated, namespaced through `NamespaceCtx`, and scoped to the caller's own view; overlays of other accounts are never visible; unknown projects, sessions and symbols are 404.
- No raw Cypher from clients; FalkorDB queries are parameterized.
- Bodies come from redacted blobs; deny-listed files have no content and therefore no chunks.
- No code leaves the deployment: no embedding or other third-party service is called.
- FalkorDB is reachable only inside the stack network or the cloud's private network, with a password; graphs carry no secrets beyond the redacted code.
- Purges drop graphs and caches with the project, account or workspace.
- **Performance:** - Parse once per file version (cache); re-resolve only dependents of changed definitions, bounded per batch.
- Typical query: index lookups plus a 1 to 3 hop expansion over a filtered view, answered in milliseconds; results are capped by tokens before they leave the server.
- Index lag target: under 10 s from a 0010 change to a visible overlay update for an active session; `code_index_lag_seconds` tracks it.
- Memory: FalkorDB holds active projects only; idle graphs unload and rebuild on demand from Mongo.
- First indexing of a large repository runs in the background; queries answer from whatever layers are ready and say so.

## Testing

- `codeparse`: golden facts for a corpus per language, all 21 (symbols, imports, refs, chunks), `.h` classification, chunk boundaries and size limits, timeout fallback, cache hits by `(sha256, version)`, `CGO_ENABLED=0` cross-build in CI for all targets, `go-licenses check`.
- `codegraphtest` suite (memory and FalkorDB adapters; FalkorDB via `MIND_PALACE_TEST_FALKORDB_URL`, skipped when unset, CI service container `falkordb/falkordb`): apply and remove files, `hiddenBy` visibility across three layers, per-layer edges, tombstones, drop layer and graph, every typed read with views, parameterized queries only.
- `codeindex`: replay of a recorded change log into the memory graph; base, branch and overlay selection from 0010's flags; rename and delete in an overlay; dependents re-resolution; idle overlay drop; rebuild equals incremental result.
- Read API: auth, namespacing, view resolution (own session, other account's session 404, ref, base), token caps and continuation, `indexing` and `unavailable` answers.
- MCP tools: listed only when indexed and entitled; results in `path:line` form; plugin test against a server with the memory graph.
- Evaluation harness: deterministic task loader and scorer tests; the full run is manual (`CLUED_EVAL=1`), like `scripts/verify-prime.sh`.
- clued-cloud: entitlement, metrics, purgers dropping graphs and caches, unload and on-demand rebuild.

## Scope

**In scope:**

- A parser spike choosing between gotreesitter (pure-Go tree-sitter port, MIT) and tree-sitter WebAssembly grammars under wazero, against fixed criteria that include a working grammar for every supported language.
- `mind-palace/codeparse`: file → facts (symbols, imports, references, chunks) for Python, C, C++, C#, Objective-C, Swift, Java, JavaScript, TypeScript, SQL, Rust, Go, PHP, Perl, OCaml, Dart (including Flutter projects), Ruby, Kotlin, Lua and Lisp; extraction from per-language query files and resolution profiles; a Markdown heading chunker and a line-window fallback for everything else; a parse cache keyed by (blob SHA-256, parser version).
- `mind-palace/codegraph`: the graph port, its schema and layer model, a FalkorDB adapter and an in-memory adapter that both pass a `codegraphtest` suite.
- `mind-palace/codeindex`: the indexer that consumes `code.Store.Changes`, chooses layer sources, writes file facts, resolves cross-file edges per layer, maintains visibility, drops idle overlays, and rebuilds a project's graph from scratch.
- The first read path for collected code: a typed query API under `/api/v1/projects/{project}/code/graph/` and MCP tools `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context`, each answering for the caller's own view and capped in tokens; search is exact-name, qualified-name and full-text.
- An evaluation harness (`eval/codesearch`) with tasks on clued, clued-cloud and a fixed corpus of public repositories covering the supported languages, run with and without the tools, measuring tokens, turns and success from clued's own capture.
- Deployment: FalkorDB in the `server init` stack and in clued-cloud, `server run --graph`, the entitlement, metrics, purgers, docs.

**Out of scope:**

- Vector embeddings and semantic (vector) search. — Decided: little payoff for code compared with the graph, exact-name and full-text search, and it would add a third-party data flow, cost and consent. The schema leaves room (chunk nodes keyed by content hash) if that ever changes.
- Precise, compiler-grade resolution (types, overloads, generics, dynamic dispatch). — Name resolution scoped by imports and packages is enough to beat grep; edges carry a confidence so a precise resolver (e.g. `go/types` for Go) can replace them later without a schema change.
- Languages beyond the 21 listed (and Lisp dialects other than Common Lisp, Emacs Lisp, Clojure and Scheme/Racket). — Other files get file nodes, full-text and window chunks; another language is a grammar plus a query file and a resolution profile.
- Web pages or visualizations of the graph. — Agents first; server-rendered SVG views (the CSP forbids scripts) are a follow-up plan.
- Local mode (no server). — The graph runs next to the collected code; a local backend follows the 0010 local-mode follow-up.
- Raw Cypher from clients. — Every read goes through typed operations so tenancy, layer visibility and token caps can't be bypassed.
- Git history (blame, symbol history across commits). — The graph reflects current layers; the change log keeps history for a later feature.

## Constraints

- Depends on 0010's `code.Store`, layer facts and session records; uses 0011's GitHub-backed worktree when present but doesn't require it.
- `CGO_ENABLED=0` for all 7 targets and `go-licenses check` for every new dependency.
- No raw Cypher from clients; queries parameterized.
- Tool results token-capped with the existing `max_tokens` / `cursor` convention (plan 0008).
- Rules in the public module (`codeparse`, `codegraph`, `codeindex`); clued-cloud composes, pins the public module by commit.
- No third-party service sees code: no embeddings.

## Phase 1: Parsing

### 1.1: Parser spike

**Files:**
- Create: `mind-palace/codeparse/spike_test.go`

**Depends on:** none

- [ ] **Step 1: Build both candidates with `CGO_ENABLED=0` for the 7 targets and run `go-licenses check`**
- [ ] **Step 2: Parse the clued and clued-cloud repositories and the language corpus with each; check a grammar exists and runs queries for each of the 21 languages; record correctness on a sample, files/s, peak memory and binary size**
- [ ] **Step 3: Choose, record the numbers and the decision in the plan's work log, delete the spike test**

### 1.2: `codeparse` facts, query files and cache

**Files:**
- Create: `mind-palace/codeparse/facts.go`
- Create: `mind-palace/codeparse/extract.go`
- Create: `mind-palace/codeparse/profiles.go`
- Create: `mind-palace/codeparse/detect.go`
- Create: `mind-palace/codeparse/queries/`
- Create: `mind-palace/codeparse/markdown.go`
- Create: `mind-palace/codeparse/text.go`
- Create: `mind-palace/codeparse/chunk.go`
- Create: `mind-palace/codeparse/cache.go`
- Test: `mind-palace/codeparse/codeparse_test.go`
- Test: `mind-palace/codeparse/testdata/`

**Depends on:** 1.1

**Interfaces:**
- Produces: `codeparse.Parse(path, content) (Facts, error)`, `codeparse.Version`, `codeparse.Cache` (memory and Mongo)

- [ ] **Step 1: Generic extractor running a language's query file; resolution profiles per family; language detection by extension and content (`.h`, `.m`)**
- [ ] **Step 2: Wave 1 query files and golden tests: Go, Python, TypeScript, JavaScript, Java, C#, Rust**
- [ ] **Step 3: Wave 2: C, C++, Objective-C, Swift, Kotlin, Dart, PHP, Ruby**
- [ ] **Step 4: Wave 3: SQL, Perl, OCaml, Lua, Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme/Racket)**
- [ ] **Step 5: Structural chunker with signature repetition and size limits; Markdown and window fallbacks; parse cache keyed by `(sha256, version)`**

## Phase 2: Graph port and FalkorDB

### 2.1: Port, schema, adapters, suite

**Files:**
- Create: `mind-palace/codegraph/graph.go`
- Create: `mind-palace/codegraph/view.go`
- Create: `mind-palace/codegraph/memgraph/memgraph.go`
- Create: `mind-palace/codegraph/falkor/falkor.go`
- Create: `mind-palace/codegraph/codegraphtest/codegraphtest.go`
- Modify: `.github/workflows/mind-palace.yml`
- Test: `mind-palace/codegraph/memgraph/memgraph_test.go`
- Test: `mind-palace/codegraph/falkor/falkor_test.go`

**Depends on:** none

**Interfaces:**
- Produces: `codegraph.Graph`, `codegraph.View`, typed read results

- [ ] **Step 1: Port and `View` with the visibility rule; in-memory adapter**
- [ ] **Step 2: FalkorDB adapter over the Redis protocol with parameterized `GRAPH.QUERY`, schema and index creation, full-text procedures verified**
- [ ] **Step 3: `codegraphtest` suite; CI service container `falkordb/falkordb` (pinned) with `MIND_PALACE_TEST_FALKORDB_URL`**

## Phase 3: Indexer

### 3.1: Change-log consumer and layer builder

**Files:**
- Create: `mind-palace/codeindex/service.go`
- Create: `mind-palace/codeindex/layers.go`
- Create: `mind-palace/codeindex/apply.go`
- Create: `mind-palace/codeindex/cursor.go`
- Test: `mind-palace/codeindex/codeindex_test.go`

**Depends on:** 1.2, 2.1

- [ ] **Step 1: Per-project cursor and lease; batch `Changes(afterSeq)`**
- [ ] **Step 2: Base, branch and overlay source selection from 0010's flags (GitHub-backed worktree preferred for base and branch)**
- [ ] **Step 3: `ApplyFile`, tombstones and `hiddenBy` maintenance**

### 3.2: Resolution, lifecycle, rebuild

**Files:**
- Create: `mind-palace/codeindex/resolve.go`
- Create: `mind-palace/codeindex/lifecycle.go`
- Create: `mind-palace/cli/server_graph.go`
- Test: `mind-palace/codeindex/resolve_test.go`

**Depends on:** 3.1

- [ ] **Step 1: Name resolution following each language's profile (imports, includes, packages, modules, namespaces), with confidence; per-layer edges**
- [ ] **Step 2: Dependents re-resolution for changed definitions, bounded per batch**
- [ ] **Step 3: Idle overlay drop, layer drop on worktree removal and branch deletion; `mind-palace server graph rebuild|status`**

## Phase 4: Read API and agent tools

### 4.1: Typed read routes

**Files:**
- Create: `mind-palace/server/codeapi/graph.go`
- Modify: `mind-palace/server/clued.go`
- Modify: `mind-palace/cli/server_run.go`
- Test: `mind-palace/server/codeapi/graph_test.go`

**Depends on:** 3.1

- [ ] **Step 1: `search`, `symbol`, `neighbors`, `outline`, `context` with view resolution from session records (caller-owned only)**
- [ ] **Step 2: Token caps and continuation with the `max_tokens` / `cursor` convention of `session/sessionmcp/budget.go`; `indexing` and `unavailable` answers**
- [ ] **Step 3: `server run --graph` wiring; off without it**

### 4.2: MCP tools

**Files:**
- Create: `mind-palace/session/sessionmcp/code_tools.go`
- Modify: `mind-palace/session/sessionmcp/sessionmcp.go`
- Modify: `mind-palace/code/remotecode/remotecode.go`
- Test: `mind-palace/session/sessionmcp/code_tools_test.go`
- Test: `mind-palace/plugin/code_tools_test.go`

**Depends on:** 4.1

- [ ] **Step 1: `code_search`, `code_symbol`, `code_neighbors`, `code_outline`, `code_context` passing the current session id; `path:line` results**
- [ ] **Step 2: List the tools only when the project is indexed and allowed (capabilities)**

## Phase 5: Evaluation

### 5.1: Harness, tasks and baseline

**Files:**
- Create: `mind-palace/eval/codesearch/tasks.yaml`
- Create: `mind-palace/eval/codesearch/harness.go`
- Create: `mind-palace/eval/codesearch/score.go`
- Create: `scripts/eval-codesearch.sh`
- Create: `mind-palace/eval/codesearch/corpus.yaml`
- Test: `mind-palace/eval/codesearch/harness_test.go`

**Depends on:** 4.2

- [ ] **Step 1: About 40 tasks on clued and clued-cloud plus about 3 per language on a pinned public corpus, with ground truth, across named, concept, impact and edit families**
- [ ] **Step 2: Runner for the arms (grep only; graph tools; `code_context` only) reading tokens, turns and tool calls from clued's capture; three runs per task**
- [ ] **Step 3: Run it, record the report in the plan's work log, and set tool defaults (budgets, depth, listed tools) from it**

## Phase 6: Deployment and documentation

### 6.1: Self-hosted stack

**Files:**
- Modify: `mind-palace/server/stack/stack.go`
- Modify: `mind-palace/server/stack/templates.go`
- Modify: `mind-palace/server/stack/apple.go`
- Modify: `mind-palace/cli/server_init.go`
- Test: `mind-palace/server/stack/stack_test.go`

**Depends on:** 4.1

- [ ] **Step 1: FalkorDB container (pinned, internal network only, password, no persistence) in Compose and Apple Containers; `--graph` passed to `server run`**

### 6.2: clued-cloud

**Files:**
- Create: `clued-cloud/cloud/codegraph.go`
- Modify: `clued-cloud/go.mod`
- Modify: `clued-cloud/cloud/cloud.go`
- Modify: `clued-cloud/cmd/clued-cloud/main.go`
- Modify: `clued-cloud/account/plans.go`
- Modify: `clued-cloud/metrics/metrics.go`
- Modify: `clued-cloud/docker-compose.yml`
- Modify: `clued-cloud/README.md`
- Test: `clued-cloud/cloud/codegraph_test.go`

**Depends on:** 4.1

- [ ] **Step 1: Pin the public module; `--graph-url`; `codeindex` workers; `Limits.CodeGraph`**
- [ ] **Step 2: Metrics with help text; purgers dropping graphs and caches; unload idle graphs and rebuild on demand**

### 6.3: Docs

**Files:**
- Create: `docs/code-graph.md`
- Modify: `PROTOCOL.md`
- Modify: `docs/server.md`
- Modify: `README.md`
- Modify: `.claude/CLAUDE.md`
- Modify: `mind-palace/README.md`

**Depends on:** 6.1, 6.2

- [ ] **Step 1: `docs/code-graph.md`: layers and views, what the tools return, privacy, supported languages, rebuild**
- [ ] **Step 2: PROTOCOL.md: the graph routes and MCP tools; layout tables for `codeparse/`, `codegraph/`, `codeindex/`, `eval/`**

## Acceptance Criteria

- [ ] The parser choice builds with `CGO_ENABLED=0` for all 7 targets and passes `go-licenses check`; the spike's numbers are recorded.
- [ ] All 21 languages (Python, C, C++, C#, Objective-C, Swift, Java, JavaScript, TypeScript, SQL, Rust, Go, PHP, Perl, OCaml, Dart including Flutter, Ruby, Kotlin, Lua, Lisp) produce symbols, imports, references and structural chunks from query files and resolution profiles; other files are indexed as text; each file version is parsed once (cache).
- [ ] One FalkorDB graph per namespaced project holds base, branch and session-overlay layers; a view shows exactly the files of its layers with lower files hidden where replaced or deleted; `codegraphtest` passes for the memory and FalkorDB adapters.
- [ ] Calls, references and implementations are resolved per layer, with confidence; an overlay's rename or rewrite is reflected in that overlay's view only.
- [ ] A change collected by 0010 is visible in the session's view within 10 seconds under normal load; `rebuild` produces the same graph as incremental indexing.
- [ ] `code_search`, `code_symbol`, `code_neighbors`, `code_outline` and `code_context` answer for the caller's own session view, never expose another account's overlay, and stay within their token budgets.
- [ ] The evaluation harness runs the arms on our repositories and the language corpus and reports tokens, turns and success per family and language; the report is recorded and sets the tool defaults.
- [ ] The `server init` stack and clued-cloud run FalkorDB internally; entitlements, metrics and purgers work; docs describe layers, tools, privacy and rebuild; `gofmt`, `go vet` and `go test -race ./...` pass in both repositories with and without MongoDB and FalkorDB.
- [ ] No vector embeddings are computed and no code is sent to a third-party service.

## Self-Review

| Requirement | Delivered by |
|---|---|
| AST / tree-sitter parsing alongside FalkorDB | 1.1, 1.2 |
| The 21 languages | 1.2, Steps 2 to 4; Design, Languages |
| Structural chunking | 1.2, Step 5 |
| No vector embeddings | Decisions; Scope (out of scope) |
| Agents find data faster with less token burn | 4.1, 4.2 (typed, token-capped tools); 5.1 (measured) |
| Layered graph from 0010 (base, branch, session overlay) | 2.1, 3.1, 3.2 |
**Residual risk:** The pure-Go tree-sitter options are young (gotreesitter is pre-1.0) and may lack some of the 21 grammars, which then have to be added to the runtime. Name-based resolution produces wrong edges in dynamic languages (Python, Ruby, Perl, Lua, Lisp, JavaScript) and in C/C++ without preprocessing; confidence makes that visible but not correct. SQL dialects vary widely. The token savings are measured, not assumed.

## Next Steps

1. Precise resolvers per language (`go/types` for Go, `compile_commands.json` with preprocessing for C/C++, `tsconfig` project references for TypeScript) upgrading edge confidence.
2. Server-rendered SVG views of a symbol's neighbourhood in clued-cloud.
3. Use the 0011 activity log in tools ('what changed on this branch since main', CI status of the commit a symbol was last changed in).
4. More grammars from the chosen runtime's registry.
5. Embedded languages (SQL in strings, scripts in HTML, Vue and Svelte single-file components).
