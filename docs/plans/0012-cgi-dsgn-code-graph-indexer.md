---
id: 0012-cgi
title: 'Code graph indexer: tree-sitter parsing for 21 languages, a layered FalkorDB graph and agent tools'
type: dsgn
status: pending
priority: "3"
effort: XL
created: "2026-10-19T17:49:42.286Z"
updated: "2026-10-20T00:04:54.757Z"
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
    status: complete
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


## Work Log

### 2026-10-19 — 1.1 Parser spike (story 0078-k15)

**1.1 Parser spike (story 0078-k15): choice = gotreesitter v0.56.0 (github.com/odvcencio/gotreesitter, MIT), imported one grammar package at a time (`grammars/<lang>`), never the aggregate `grammars` package.** Evidence was gathered by the spike test (commit 4baad28, deleted afterwards), a C oracle (tree-sitter CLI 0.25.10 on the same pinned grammar commits) and a working wazero prototype. The host was a shared 4-vCPU sandbox (load average 8-16 from other agents), so absolute rates are noisy; ratios were measured back to back.

*Build and licences.* Both candidates cross-compile with `CGO_ENABLED=0 -trimpath -ldflags '-s -w'` for all 7 targets (darwin/amd64, darwin/arm64, linux/amd64, linux/arm64, linux/arm GOARM=6, windows/amd64, windows/arm64), linked into `cmd/mind-palace`. CI's `go-licenses check ./... --disallowed_types=forbidden,restricted,unknown` passes with either one linked (gotreesitter MIT; wazero Apache-2.0; gotreesitter's per-language packages pull in no other module). All 25 grammars we use are MIT except clojure (CC0-1.0), per gotreesitter's licenses/grammars.json. The aggregate `grammars` package embeds all 206 grammars, including GPL-3.0 (caddy, disassembly, jq) and MPL-2.0 (nim) tables that go-licenses cannot see. That is why codeparse imports only the per-language packages.

*Binary size, mind-palace with -s -w (MiB):* base 44.04/41.69/43.07/40.44/40.56/43.95/40.72 (in the target order above). With gotreesitter and all 25 grammars (parse+query reachable): 56.99/53.98/55.84/52.44/53.00/56.84/52.80, so **+12.1 to +13.0 MiB**. Of that, the runtime is about 9 MiB and the 25 compressed grammar blobs are 3.9 MiB. With wazero plus one grammar (Go, a 0.5 MiB .wasm): 47.29/44.84/46.25/43.56/42.44/47.19/43.81, so +3.2 MiB. The other 24 grammars as .wasm would add about 40 MiB uncompressed: the tree-sitter-wasms builds of 19 of them total 38.8 MiB (objc 7.4, ocaml 4.8, cpp 4.5, kotlin 3.9, c_sharp 3.8).

*Grammar and query support, all 21 languages and 25 grammars:* gotreesitter ships every one, with upstream node types: python, c, cpp, c_sharp, objc, swift, java, javascript (jsx included), typescript, tsx, sql (m-novikov/tree-sitter-sql), rust, go, php, perl, ocaml, dart, ruby, kotlin, lua, commonlisp, elisp, clojure, scheme, racket. For each grammar, a tags-convention query (@definition.*, @reference.call/class/implementation/mixin, @import, @package, @doc, with #eq?/#any-of?/#not-any-of? predicates, anchors and alternations) compiled and returned every expected definition, call and import on a sample: **25/25 pass**. One query (Python docstring) was checked against the C CLI and gave the same captures. gotreesitter has no explicit upstream tags.scm for these languages; `ResolveTagsQuery` infers one, and it is empty for perl, elisp and racket. clued's own query files are therefore required for every language, as the plan already says. `.mli` files parse cleanly with the ocaml grammar (`value_specification`, `module_type_definition`); the separate upstream `ocaml_interface` grammar is not shipped.

*Correctness against C on one public repository per language* (number of files with an error node; the 1 MiB cap applies). The pairs (C, gotreesitter) are identical file for file for python 91 files (0,0), java 264 (0,0), javascript 141 (0,0), rust 110 (0,0), php 125 (0,0), ruby 147 (0,0), lua 77 (0,0), elisp 49 (0,0), clojure 82 (0,0), racket 82 (0,0), c 52 (18,18), objc 49 (4,4), ocaml 291 (28,28), commonlisp 31 (4,4), scheme 234 (6,6). They also match for Go on clued plus clued-cloud, 597 files (0,0). The rest diverge, with errors only gotreesitter reports: **dart** 1303 files (0, 146: Flutter-style calls such as `runApp(` and `body: Center(` with trailing-comma arguments), **kotlin** 327 (6, 69: a one-line class body with a member, `class A { fun f(): Int }`, `actual`/`expect` members, generic `withType<…<*>>`; one file takes 8 s), **swift** 101 (5, 37: whole-file errors and 5-30 s parses on Alamofire's Request.swift and DataRequest.swift), **c_sharp** 951 (43, 61: 28 only in gotreesitter, mostly after `#pragma warning restore`; 10 only in C), **perl** 274 (14, 21-23), **cpp** 47 (19, 20). typescript 481 (4 C, 0-1 Go) and tsx 13 (1 C, 0 Go) favour gotreesitter. **sql**: 23 of 56 pgvector files have errors (PostgreSQL extension DDL and plpgsql). The C oracle could not run there because the pinned grammar's scanner.cc is C++ and the CLI skips it. m-novikov/tree-sitter-sql is old and dialect-limited.

*Throughput and memory, parse plus query, single-threaded:* clued (384 Go files, 3.1 MiB) 73 files/s, 0.58 MiB/s, with the query taking 5% of the time. clued-cloud (213 files, 2.1 MiB) 51 files/s, 0.50 MiB/s. Together that is 9.5 s and a peak live heap of 222 MiB. With 4 workers it is 73 files/s and a 444 MiB peak; scaling is poor on the oversubscribed host. Public corpus, 5,414 files and 38 MiB in 25 grammars, 4 workers: 103 files/s overall and a **1.1 GiB** peak heap. Per grammar (MiB/s): lua 7.5, elisp 5.1, php 4.0, racket 4.0, ocaml 3.3, ruby 2.9, rust 2.8, scheme 2.3, commonlisp 1.9, java 1.8, javascript 1.8, typescript 1.5, perl 1.3, cpp 1.3, tsx 1.2, python 1.2, clojure 1.2, c 0.9, dart 0.8, kotlin 0.7, c_sharp 0.56, objc 0.38, go 0.28, sql 0.12, swift 0.11. The 2 s cut-off (`SetTimeoutMicros`) works and stopped 11 swift and 2 c_sharp files. Against C on the same 597 Go files, the C CLI parses at 5.1 MiB/s (about 9x gotreesitter's parse-only 0.59 MiB/s) and wazero running the C runtime parses at 1.3 MiB/s (1.0 MiB/s with the query; about 2.2x gotreesitter) in 5 MiB of linear memory. On single large files gotreesitter is up to 25x slower than C (cobra's completions_test.go, 119 KB: 400 ms against 18 ms). A shared `*Query` and `*Language` across 4 goroutines, each with its own `Parser`, is clean under `-race`.

*wazero evaluation.* The prebuilt grammar .wasm files (npm tree-sitter-wasms 0.1.13, Unlicense, 36 grammars; web-tree-sitter 0.27.1) cannot be used from Go. They are Emscripten SIDE_MODULEs with a `dylink`/`dylink.0` section. They import `env.memory`, `env.__indirect_function_table`, `env.__memory_base`/`__table_base`/`__stack_pointer`, the `GOT.func.*`/`GOT.mem.*` globals and malloc/free from the main module. Loading them needs Emscripten's dynamic linker (relocations, the GOT, a shared table), and wazero has none. The prebuilt set also lacks 6 of our grammars (sql, perl, commonlisp, clojure, scheme, racket) and is built from 2023-era grammars (tree-sitter-cli 0.20.8). The workable wazero route is the one m-parse (vista-forge) takes. It compiles the tree-sitter C runtime, each grammar's parser.c/scanner.c and a C shim exposing a flat i32 handle ABI into a standalone WASI reactor. The spike built that for Go in a few minutes (clang 18, wasi-sysroot 25 and compiler-rt builtins downloaded from wasi-sdk releases; 530 KB .wasm; compile and instantiate 140-160 ms). It runs correctly with C parity by construction. Its costs:
- A C/wasm toolchain plus node and the tree-sitter CLI (swift and perl ship no parser.c; sql has a C++ scanner) to regenerate 25 committed .wasm artifacts on each grammar bump.
- A hand-written shim for nodes, cursors and query captures, with copies across linear memory.
- About 40 MiB more binary.
- A non-reentrant module per worker.
- **Interpreter only on linux/arm (32-bit): 0.02 MiB/s, 50x slower** (wazero's compiler covers amd64 and arm64 only).

It was not taken further.

*Why gotreesitter.* It meets every hard criterion with no build tooling: `go get`, 25/25 grammars, official node types, full query language with predicates, CGO-free on all 7 targets, permissive licence and a 3x smaller binary delta. It is also very actively maintained: 111 releases since 2026-02-20, v0.56.0 on 2026-10-13, locked-C parity boards. Its costs are speed (about 0.6 MiB/s per core on Go, 9x C), memory (hundreds of MiB per busy worker) and parity gaps in wave-2 grammars. The design absorbs those: each file version is parsed once by content hash, and the 2 s cut-off falls back to text. The risks are a single maintainer and a pre-1.0 API, so pin the version and keep it behind `codeparse` so a grammar can move to another runtime without changing callers.

*Grammars that need work* (none must be added to the runtime; all exist):
- dart, kotlin and swift parity (wave 2: file upstream issues with the reproducers above; until then rely on the 2 s cut-off and the text fallback, and keep whatever the query still matched).
- c_sharp after `#pragma`.
- perl (9 extra error files).
- sql: m-novikov grammar; consider adding DerekStride/tree-sitter-sql via gotreesitter's ts2go/grammargen pipeline in 0095-451.
- OCaml `.mli` through the ocaml grammar (the interface grammar is absent).
- `.asd`, `.lsp`, `.t`, `.rake`, `.mts`, `.cts`, `.jsx`, `.mm` and `.h` routing are clued's own detection (gotreesitter's registry lacks some of them).

*Usage for 0080-6sk* (verified program):
```go
import (
    ts "github.com/odvcencio/gotreesitter"
    golang "github.com/odvcencio/gotreesitter/grammars/go" // package names: golang (go), c_sharp, tsx, commonlisp, ...
)
lang := golang.Language()               // cached *ts.Language, decoded lazily, goroutine-safe
q, err := ts.NewQuery(queryText, lang)  // compile once per language; share across goroutines
p := ts.NewParser(lang)                 // one per goroutine (or ts.NewParserPool(lang, ...))
p.SetTimeoutMicros(2_000_000)           // the plan's 2 s cut-off
p.SetMemoryBudgetBytes(256 << 20)       // default is max(512 MiB, 512 B per input byte)
tree, err := p.Parse(src)
defer tree.Release()
if tree.ParseStopReason() != ts.ParseStopAccepted || tree.RootNode().HasError() { /* text fallback or partial facts */ }
cur := q.Exec(tree.RootNode(), lang, src)
for { m, ok := cur.NextMatch(); if !ok { break }
    for _, c := range m.Captures { // c.Name ("name", "definition.function", "import", ...), m.PatternIndex
        _ = c.Text(src); _ = c.Node.Type(lang); _ = c.Node.StartPoint() /* 0-based Row, byte Column */; _ = c.Node.StartByte() } }
```
Grammar quirks seen while writing the queries:
- The Python docstring is `(block . (string))` with no `expression_statement`.
- Swift `function_declaration` puts the return type under field `name` too, as upstream C does, so match `name: (simple_identifier)`.
- Kotlin nodes carry no field names (use positional children).
- Dart calls are `(identifier) . (selector (argument_part))` siblings.
- `Node.Text(src)` and `QueryCapture.Text(src)` return strings.
- `Node.SExpr(lang)` omits field names.


### 2026-10-20 — 5.1 Evaluation run and tool defaults (story 0126-j2n)

**Evaluation run, default condition (subset for budget reasons).** The owner capped the total spend at $50, so this is a subset, not the plan's 101 tasks x 3 arms x 3 runs: **28 tasks x 3 arms (grep, graph, context) x 1 run = 84 sessions**, chosen to keep every family and every language: clued-named-allocateid, clued-named-daemon-ok (shell), clued-concept-sequence-numbers, clued-concept-memory-engines (markdown), clued-impact-relaybudget, clued-edit-limits-model, cloud-named-namespace, cloud-concept-csp, cloud-impact-sessionscan, py-named-merge-setting, c-concept-hooks, cpp-impact-putvarint32, cs-named-serializeobject, objc-concept-server-trust, swift-impact-server-trust, java-named-getadapter, js-concept-etag, ts-impact-subscribe, sql-named-inventory-in-stock, rust-concept-recursion-limit, php-impact-handlerstack-create, perl-named-dequeue, ocaml-concept-safe-to-basic, dart-impact-observer-ontransition, ruby-named-route-bang, kotlin-concept-follow-ups, lua-impact-fzy-sorter, clj-named-wrap-params (hunchentoot and dash-el not indexed, other lisp tasks skipped). Model claude-opus-5-5 (Claude Code 2.1.304 default), `--budget 0.5`, `--plugin-dir` this branch. `--verify-only` passed first (clued, clued-cloud and corpus ground truth).

Setup: an isolated local clued server (`server run --code-store mongo --graph redis://127.0.0.1:6379`, own token file, database `clued_eval_0126j2n`), a scratch HOME whose clued config points at it, production `CLUED_SERVER_*` unset; clued (at this branch's HEAD), clued-cloud and 19 corpus repos code-synced from a checkout on their default branch with the upstream at the pinned commit, so each project's base layer is the commit the eval clones check out; all 21 graphs `--check` consistent. codeSync was off for the eval sessions themselves: their clones are detached, so a sync would push every file as an overlay (seen on a probe: 123 of 123 files); with it off the code tools read the base.

**Result: the agents never called a code tool.** 0 code tool calls in all 56 graph and context sessions; every session in every arm succeeded (100%). In Claude Code the plugin's 25 clued tools are deferred (listed by name, loaded through ToolSearch), and Opus 5.5 answered with Grep, Read and Bash. A probe with `ENABLE_TOOL_SEARCH=false` (all tools loaded up front) also made 0 code tool calls and cost 0.48 vs 0.25-0.29 USD for the same task. So the arm differences below are noise between identical configurations (graph +15%, context -6% median tokens overall, both with 0 code calls).

| arm | tasks | success | median tokens | median turns | median tool calls | median code tool calls | median cost |
|---|---:|---:|---:|---:|---:|---:|---:|
| grep | 28 | 100% | 112,727 | 4 | 4 | 0 | 0.153 |
| graph | 28 | 100% | 129,556 | 5 | 5 | 0 | 0.168 |
| context | 28 | 100% | 105,782 | 4 | 4.5 | 0 | 0.158 |

By family (grep / graph / context median tokens; success 100% everywhere): named 112,727 / 117,335 / 105,152 (10 tasks); concept 103,745 / 129,439 / 104,448 (9); impact 128,840 / 130,377 / 109,039 (8); edit 319,487 / 498,443 / 454,551 (1). By language, grep arm median tokens and turns: go 225,574 / 6 (7 tasks), shell 204,711 / 6, markdown 139,125 / 4, php 245,176 / 7, csharp 143,215 / 6, typescript 129,732 / 5, lua 127,947 / 5, java 120,766 / 5, dart 117,496 / 4, python 112,956 / 5, lisp 112,497 / 5, objc 107,048 / 4, swift 106,319 / 4, kotlin 103,745 / 4, c 103,798 / 4, rust 97,748 / 4, ruby 97,553 / 4, ocaml 89,708 / 4, javascript 88,843 / 4, cpp 73,856 / 3, sql 67,173 / 3, perl 65,766 / 3; success 100% in every language and arm. The tables follow.

**Retrieval without a model** (all 98 tasks on indexed repos, straight against the local graph routes): code_context recall of the ground-truth files 0.68 at 2,000 tokens, 0.72 at 4,000, 0.73 at 6,000, 8,000 and 12,000 (median answer 1,608 / 2,453 / 3,158 / 3,642 / 3,789 tokens); by family at 6,000: named 0.80, concept 0.62, impact 0.75. code_search of each truth symbol finds 0.80 of the truth files (named 0.90, concept 0.84, impact 0.79), median 762 tokens a query, cut at 2,000 for 4 of 98. callers depth 1 / 2 / 3: median 2 / 6.5 / 7 edges, 314 / 883 / 1,708 tokens, impact-file recall with search 0.86 / 0.86 / 0.85.

**Bug found (outside this story's files): code_context answers 503 on larger graphs.** On clued, clued-cloud, alamofire, bloc, gson, guzzle, newtonsoft-json and okhttp (37 of 98 tasks) every multi-word task failed: `UNWIND $ids AS id MATCH (s:Symbol {id: id}) …` (codegraph/falkor, symbol lookup by id for the full-text hits) hits FalkorDB's query timeout, 1,000 ms by default in the falkordb/falkordb image (`FALKORDB_ARGS=… TIMEOUT 1000 …`), and the route maps it to `unavailable`. code_search on the same words answers in 15 ms. Needs an index on Symbol.id or a bounded id list, in a follow-up story.

Spend so far (captured cost, about 1.2x billed): probe 0.08, smoke 1.65, eager probe 0.48, main 12.97, total 15.18 USD captured (about 18.2 USD billed).

#### Default condition: report (`eval/codesearch` Summarize over the 84 runs)

##### Overall

| tasks | arm | tasks | runs | success | median tokens | vs grep | median turns | median tool calls | median code tool calls | median cost (USD) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| all | grep | 28 | 28 | 100% | 112726.5 | - | 4 | 4 | 0 | 0.153 |
| all | graph | 28 | 28 | 100% | 129555.5 | +15% | 5 | 5 | 0 | 0.168 |
| all | context | 28 | 28 | 100% | 105781.5 | -6% | 4 | 4.5 | 0 | 0.158 |

##### By family

| family | arm | tasks | runs | success | median tokens | vs grep | median turns | median tool calls | median code tool calls | median cost (USD) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| named | grep | 10 | 10 | 100% | 112726.5 | - | 5 | 4.5 | 0 | 0.135 |
| named | graph | 10 | 10 | 100% | 117334.5 | +4% | 4.5 | 4.5 | 0 | 0.122 |
| named | context | 10 | 10 | 100% | 105151.5 | -7% | 4 | 4 | 0 | 0.119 |
| concept | grep | 9 | 9 | 100% | 103745 | - | 4 | 4 | 0 | 0.148 |
| concept | graph | 9 | 9 | 100% | 129439 | +25% | 5 | 5 | 0 | 0.169 |
| concept | context | 9 | 9 | 100% | 104448 | +1% | 4 | 4 | 0 | 0.166 |
| impact | grep | 8 | 8 | 100% | 128839.5 | - | 5 | 4 | 0 | 0.186 |
| impact | graph | 8 | 8 | 100% | 130377 | +1% | 5 | 4.5 | 0 | 0.176 |
| impact | context | 8 | 8 | 100% | 109039 | -15% | 4 | 5 | 0 | 0.181 |
| edit | grep | 1 | 1 | 100% | 319487 | - | 8 | 11 | 0 | 0.301 |
| edit | graph | 1 | 1 | 100% | 498443 | +56% | 11 | 12 | 0 | 0.383 |
| edit | context | 1 | 1 | 100% | 454551 | +42% | 10 | 12 | 0 | 0.381 |

##### By language

| language | arm | tasks | runs | success | median tokens | vs grep | median turns | median tool calls | median code tool calls | median cost (USD) |
|---|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| c | grep | 1 | 1 | 100% | 103798 | - | 4 | 4 | 0 | 0.147 |
| c | graph | 1 | 1 | 100% | 137519 | +32% | 5 | 6 | 0 | 0.181 |
| c | context | 1 | 1 | 100% | 104448 | +1% | 4 | 4 | 0 | 0.156 |
| cpp | grep | 1 | 1 | 100% | 73856 | - | 3 | 2 | 0 | 0.135 |
| cpp | graph | 1 | 1 | 100% | 104945 | +42% | 4 | 3 | 0 | 0.171 |
| cpp | context | 1 | 1 | 100% | 105885 | +43% | 4 | 5 | 0 | 0.176 |
| csharp | grep | 1 | 1 | 100% | 143215 | - | 6 | 6 | 0 | 0.145 |
| csharp | graph | 1 | 1 | 100% | 90534 | -37% | 4 | 5 | 0 | 0.124 |
| csharp | context | 1 | 1 | 100% | 88558 | -38% | 4 | 3 | 0 | 0.109 |
| dart | grep | 1 | 1 | 100% | 117496 | - | 4 | 4 | 0 | 0.203 |
| dart | graph | 1 | 1 | 100% | 106848 | -9% | 4 | 3 | 0 | 0.167 |
| dart | context | 1 | 1 | 100% | 112193 | -5% | 4 | 4 | 0 | 0.185 |
| go | grep | 7 | 7 | 100% | 225574 | - | 6 | 7 | 0 | 0.225 |
| go | graph | 7 | 7 | 100% | 188083 | -17% | 5 | 6 | 0 | 0.222 |
| go | context | 7 | 7 | 100% | 215548 | -4% | 6 | 6 | 0 | 0.256 |
| java | grep | 1 | 1 | 100% | 120766 | - | 5 | 6 | 0 | 0.148 |
| java | graph | 1 | 1 | 100% | 121449 | +1% | 5 | 6 | 0 | 0.156 |
| java | context | 1 | 1 | 100% | 94722 | -22% | 4 | 6 | 0 | 0.138 |
| javascript | grep | 1 | 1 | 100% | 88843 | - | 4 | 3 | 0 | 0.120 |
| javascript | graph | 1 | 1 | 100% | 71077 | -20% | 3 | 4 | 0 | 0.138 |
| javascript | context | 1 | 1 | 100% | 90285 | +2% | 4 | 3 | 0 | 0.126 |
| kotlin | grep | 1 | 1 | 100% | 103745 | - | 4 | 3 | 0 | 0.172 |
| kotlin | graph | 1 | 1 | 100% | 129439 | +25% | 5 | 4 | 0 | 0.169 |
| kotlin | context | 1 | 1 | 100% | 129426 | +25% | 5 | 4 | 0 | 0.174 |
| lisp | grep | 1 | 1 | 100% | 112497 | - | 5 | 4 | 0 | 0.107 |
| lisp | graph | 1 | 1 | 100% | 136689 | +22% | 6 | 5 | 0 | 0.116 |
| lisp | context | 1 | 1 | 100% | 136045 | +21% | 6 | 5 | 0 | 0.112 |
| lua | grep | 1 | 1 | 100% | 127947 | - | 5 | 4 | 0 | 0.152 |
| lua | graph | 1 | 1 | 100% | 131082 | +2% | 5 | 4 | 0 | 0.181 |
| lua | context | 1 | 1 | 100% | 93302 | -27% | 4 | 5 | 0 | 0.123 |
| markdown | grep | 1 | 1 | 100% | 139125 | - | 4 | 7 | 0 | 0.202 |
| markdown | graph | 1 | 1 | 100% | 244874 | +76% | 7 | 8 | 0 | 0.228 |
| markdown | context | 1 | 1 | 100% | 141291 | +2% | 4 | 7 | 0 | 0.212 |
| objc | grep | 1 | 1 | 100% | 107048 | - | 4 | 5 | 0 | 0.180 |
| objc | graph | 1 | 1 | 100% | 140976 | +32% | 5 | 5 | 0 | 0.194 |
| objc | context | 1 | 1 | 100% | 79515 | -26% | 3 | 3 | 0 | 0.166 |
| ocaml | grep | 1 | 1 | 100% | 89708 | - | 4 | 3 | 0 | 0.107 |
| ocaml | graph | 1 | 1 | 100% | 90820 | +1% | 4 | 4 | 0 | 0.112 |
| ocaml | context | 1 | 1 | 100% | 66913 | -25% | 3 | 3 | 0 | 0.100 |
| perl | grep | 1 | 1 | 100% | 65766 | - | 3 | 2 | 0 | 0.082 |
| perl | graph | 1 | 1 | 100% | 91048 | +38% | 4 | 3 | 0 | 0.102 |
| perl | context | 1 | 1 | 100% | 88650 | +35% | 4 | 3 | 0 | 0.095 |
| php | grep | 1 | 1 | 100% | 245176 | - | 7 | 6 | 0 | 0.292 |
| php | graph | 1 | 1 | 100% | 169981 | -31% | 5 | 7 | 0 | 0.255 |
| php | context | 1 | 1 | 100% | 196893 | -20% | 6 | 6 | 0 | 0.293 |
| python | grep | 1 | 1 | 100% | 112956 | - | 5 | 4 | 0 | 0.113 |
| python | graph | 1 | 1 | 100% | 113220 | +0% | 5 | 4 | 0 | 0.112 |
| python | context | 1 | 1 | 100% | 113342 | +0% | 5 | 4 | 0 | 0.111 |
| ruby | grep | 1 | 1 | 100% | 97553 | - | 4 | 5 | 0 | 0.125 |
| ruby | graph | 1 | 1 | 100% | 97089 | -0% | 4 | 4 | 0 | 0.120 |
| ruby | context | 1 | 1 | 100% | 96961 | -1% | 4 | 5 | 0 | 0.126 |
| rust | grep | 1 | 1 | 100% | 97748 | - | 4 | 4 | 0 | 0.148 |
| rust | graph | 1 | 1 | 100% | 124630 | +28% | 5 | 5 | 0 | 0.159 |
| rust | context | 1 | 1 | 100% | 105678 | +8% | 4 | 6 | 0 | 0.170 |
| shell | grep | 1 | 1 | 100% | 204711 | - | 6 | 5 | 0 | 0.251 |
| shell | graph | 1 | 1 | 100% | 203729 | -0% | 6 | 5 | 0 | 0.188 |
| shell | context | 1 | 1 | 100% | 171488 | -16% | 5 | 4 | 0 | 0.183 |
| sql | grep | 1 | 1 | 100% | 67173 | - | 3 | 2 | 0 | 0.096 |
| sql | graph | 1 | 1 | 100% | 91840 | +37% | 4 | 3 | 0 | 0.102 |
| sql | context | 1 | 1 | 100% | 67051 | -0% | 3 | 2 | 0 | 0.092 |
| swift | grep | 1 | 1 | 100% | 106319 | - | 4 | 4 | 0 | 0.154 |
| swift | graph | 1 | 1 | 100% | 107558 | +1% | 4 | 5 | 0 | 0.169 |
| swift | context | 1 | 1 | 100% | 105375 | -1% | 4 | 5 | 0 | 0.157 |
| typescript | grep | 1 | 1 | 100% | 129732 | - | 5 | 4 | 0 | 0.170 |
| typescript | graph | 1 | 1 | 100% | 129672 | -0% | 5 | 4 | 0 | 0.163 |
| typescript | context | 1 | 1 | 100% | 102365 | -21% | 4 | 3 | 0 | 0.151 |

