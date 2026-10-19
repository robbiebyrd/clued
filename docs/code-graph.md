# The code graph

The code graph is the first read path for the code clued collects (code sync,
[README.md](../README.md#code-sync), and the GitHub App,
[github-app.md](github-app.md)). The clued server parses every file version
it holds, builds one graph per project with the symbols, imports, references
and retrieval chunks of each file, and answers typed, token-capped questions
about it: where is this defined, who calls it, what is in this file, what
code does this task need. An agent asks through five MCP tools of the `clued`
server (`code_search`, `code_symbol`, `code_neighbors`, `code_outline`,
`code_context`) and gets back `path:line` answers for its own working tree,
instead of spending turns and tokens on grep and whole-file reads.

The design is plan 0012-cgi (`docs/plans/0012-cgi-dsgn-code-graph-indexer.md`);
the exact routes, bodies and errors are in [PROTOCOL.md](../PROTOCOL.md)
sections 6.1.7 (the tools) and 7.14 (the routes); running it is in
[server.md](server.md#the-code-graph). This page explains what the graph
holds, what each tool returns, who can see what, which languages are
understood today, and how a graph is rebuilt.

Contents

- [How it fits together](#how-it-fits-together)
- [Layers and views](#layers-and-views)
- [What the graph holds](#what-the-graph-holds)
- [The agent tools](#the-agent-tools)
- [Privacy](#privacy)
- [Languages](#languages)
- [Rebuild, status and checks](#rebuild-status-and-checks)
- [FalkorDB in the stack](#falkordb-in-the-stack)
- [Evaluation](#evaluation)
- [Limits](#limits)

## How it fits together

```
client (code sync) ──► /api/v1/projects/{p}/code/   write-only code transport
                              │
                              ▼
                       code.Store (MongoDB)          blobs, trees, change log: the source of truth
                              │  Changes(afterSeq)
                              ▼
                       codeindex.Service            per project: lease, cursor, layer selection
                         │  codeparse (tree-sitter)    file → facts, cached by content
                         │  codeindex resolver         references → edges, per layer
                              ▼
                       codegraph.Graph (FalkorDB)   one graph per project, base/branch/overlay layers
                              │  typed reads, never Cypher
                              ▼
          /api/v1/projects/{p}/code/graph/{search,symbol,neighbors,outline,context}
                              │
                              ▼
          clued MCP server: code_search, code_symbol, code_neighbors, code_outline, code_context
```

| Package | Role |
|---|---|
| `mind-palace/codeparse` | One file version to its facts: detection, the tree-sitter engine (pure Go, [gotreesitter](https://github.com/odvcencio/gotreesitter)) driven by a query file and a profile per language, structural chunks, Markdown and line-window fallbacks, the parse cache port |
| `mind-palace/codeparse/mongocache` | The parse cache in MongoDB (`code_parses`, keyed by SHA-256 and facts version) |
| `mind-palace/codegraph` | The graph port, its schema, layers and views; `memgraph` (in memory) and `falkor` (FalkorDB over the Redis protocol) pass the same `codegraphtest` suite |
| `mind-palace/codeindex` | The indexer: change-log consumer, layer selection, the file applier and `hiddenBy`, the resolver with one rule set per language family (`codeindex/resolvers`), idle overlays, rebuild, status and consistency checks |
| `mind-palace/server/codeapi` (`graph.go`) | The five graph read routes, view resolution, token caps |
| `mind-palace/session/sessionmcp` (`code_tools.go`) | The five MCP tools over `code/remotecode` |
| `mind-palace/eval/codesearch` | The evaluation harness (`scripts/eval-codesearch.sh`) |

The graph is a **derived cache**. MongoDB's code store is the source of
truth; any project's graph can be dropped and written again from it
(`mind-palace server graph rebuild`), which is also why FalkorDB runs with
persistence off and needs no backup.

## Layers and views

A project's code exists in many versions at once: the default branch as
pushed, other pushed branches, and each checkout's uncommitted and unpushed
edits. The graph keeps them as **layers** that sit on top of each other:

| Layer | Id | Holds |
|---|---|---|
| Base | `base` | The default branch's clean, pushed files. The GitHub-backed worktree (`gh_<repository id>`) supplies it when the GitHub App covers the project; otherwise the client checkouts on the default branch do, the newest upstream commit winning |
| Branch layer | `branch:<ref>@<commit>` | One per pushed branch and upstream commit: only the paths whose git blob id differs from the base, plus tombstones for base paths the branch deleted. The ref's newest layer is kept, older ones go once no checkout sits on them |
| Session overlay | `wt:<worktree>:<ref>` | One per checkout and branch: the dirty and unpushed files, plus tombstones for files the checkout no longer has. It belongs to the account that owns the worktree |

A **view** is an ordered list of layers, lowest first, for example
`["base", "branch:feature@3f2a…", "wt:wt_0123456789abcdef:feature"]`. When a
higher layer replaces or deletes a path, the indexer records that layer's id
in the lower file's `hiddenBy`; a node is visible in a view when its layer is
in the view and none of its file's `hiddenBy` is. Each layer is therefore
stored as a diff, and a query reads a short list of layer ids, never a set
of paths. Two overlays editing one file each see their own version; nothing
is merged.

What a read sees (the request's `view`):

| `view` | Layers |
|---|---|
| omitted | `base` |
| `{"ref": "feature"}` | `base` plus the newest branch layer of `feature` |
| `{"session_id": "…"}` | the overlay of the worktree the session works in, over its branch layer and the base |
| `{"session_id": "…", "ref": "feature"}` | that session's overlay on `feature` |

The MCP tools always send the current session (`CLAUDE_SESSION_ID`, else the
newest captured session in the working directory), so an agent sees its own
edits. A view whose branch layer was dropped falls back to the ref's newest
branch layer, then to the base. A session whose worktree has no overlay
(code sync off for it, nothing indexed yet) reads its branch instead, and
the answer says `"localEdits": false` with a note; the tools pass that note
to the agent so it knows the results do not include its local changes.

**References follow the view.** Calls, references, implementations and
extensions are resolved per layer: an edge carries the id of the layer that
resolved it and is visible only in views that hold that layer and both of
its ends. When an overlay renames or rewrites a function, the base callers of
that name are re-resolved in the overlay's view and point at the overlay's
version there, and only there; callers that no longer match stay unresolved
in that view. Re-resolution of other files is bounded to 5,000 references per
batch (`codeindex.DefaultMaxRefs`); the rest carries over to the next batch.

**Lifecycle.** An overlay with no session activity on its worktree for 14
days and nothing dirty or unpushed is dropped; a removed worktree or a
branch deleted on GitHub takes its layers with it; a graph can be dropped and
rebuilt at any time.

## What the graph holds

| Node | Key properties |
|---|---|
| `Layer` | `id`, `kind` (`base`, `branch`, `wt`), `parent`, `owner` (an overlay's account), `ref`, `commit`, `updated` |
| `File` | `layer`, `path`, `sha256`, `git_oid`, `lang`, `package`, `hiddenBy`, `deleted` (a tombstone), `parse_error` |
| `Package` | `layer`, `name`, `kind` (`package`, `module`, `namespace`, `crate`, `header`, `schema`) |
| `Symbol` | `layer`, `id` (`<path>#<qualified name>`, `~2`, `~3` … for repeats), `kind`, `name`, `qualName`, `signature`, `doc`, `path`, `start`, `end` (1-based lines), `exported` |
| `Chunk` | `layer`, `id`, `kind` (`symbol`, `part`, `section`, `window`), `path`, `start`, `end`, `hash` |

| Edge | Between | Written by |
|---|---|---|
| `CONTAINS` | Layer→File, File→Symbol, Symbol→Symbol (methods, fields), File→Chunk, Symbol→Chunk | the file applier, from the facts |
| `IN_PACKAGE` | File→Package | the file applier |
| `IMPORTS` | File→Package, with `alias`; C-family `#include` too | the file applier, from the import forms as written |
| `CALLS`, `REFERENCES`, `IMPLEMENTS`, `EXTENDS` | Symbol→Symbol, with `layer`, `confidence`, `count`, first `line` | the resolver |

**Confidence** says how sure a resolved edge is: `exact` (one definition found
by a scope or import rule), `qualified` (found by its qualified name) or
`name` (a member reached through an unknown qualifier, a name-only fallback,
or several candidates of which the best by kind, export and path was kept).
The tools show it, and rank `name` edges last.

**Chunks** are the retrieval units: one per top-level function, method or
type with its doc comment and signature; a body over 1,500 tokens is cut at
statement boundaries into parts that each repeat the signature; the code
between definitions (package clause, imports, top-level statements) goes into
windows, so every line of a parsed file is in some chunk. Markdown files are
chunked by heading; every other text file, and a file of a language whose
parse failed, by 60-line windows overlapping by 10 lines. A file keeps at most
1,000 chunks. The chunk's text stays in the blob store; the graph keeps its
lines and hash.

**Indexes**: range indexes on `Layer(id)`, `File(layer, path)`,
`Package(layer, name)`, `Symbol(name)`, `Symbol(qualName)`, `Symbol(layer,
id)`, `Chunk(layer, id)`; a full-text index over `Symbol.name`,
`Symbol.signature` and `Symbol.doc` (weighted 3, 2, 1, without stemming).
There is no vector index and no embedding: search is exact name, qualified
name and full text, expanded through the graph.

## The agent tools

The five tools are part of the `clued` MCP server the plugin runs (stdio, in
remote mode). They are **listed only when the project is indexed**: at start
the server asks the graph once (a one-result search on the base); a project
that answers `ready` or `indexing` gets the tools, a server without the graph
routes (no `--graph`), or one that refuses the project or the token, never
does, and an empty project or an unreachable graph is asked again every two
minutes (for up to a day), the tools appearing (`tools/list_changed`) once
it is indexed. The server's own HTTPS `/mcp` does not offer them: it has no
working tree of the caller's.

Every tool is read-only, takes `ref` (read that branch instead of the
session's own view), `max_tokens` and `cursor`, and starts its result with:

```json
{"status": "ready", "note": "…", "view": {"session": "…", "ref": "main", "localEdits": true, "note": "…"}}
```

`status` is `ready`, `indexing` (the index has not caught up with every
collected change; `note` says how many are pending and that the results may
miss recent code) or `empty` (no code collected; `note` says to search the
files directly). Every location is `path:line` or `path:start-end`, so the
agent opens exactly those lines.

| Tool | Arguments | Returns |
|---|---|---|
| `code_search` | `query` (a name such as `Alloc`, `pkg.Alloc`, `T.M`, or words), `kinds`, `langs`, `paths` (path prefixes), `limit` (default 20) | `results`: qualified-name matches, then exact-name matches, then full-text matches over names, signatures and doc comments; each with `at`, `kind`, `name`, `qualName`, `signature`, a one-line `doc`, `match` (`qualified`, `exact`, `text`) and `id` |
| `code_symbol` | `id` (from a search or an outline) or `qualName`; `include` (`body`, `doc`, `children`; default all) | `symbol` (`at`, `kind`, `name`, `qualName`, `signature`, `doc`, `lang`, `exported`, `parent`, `id`), `children` (methods, fields: `at`, `kind`, `name`, `signature`, `id`), `body` (`at`, `text`: the source lines from the stored, redacted blob) or `bodyNote` when the content was not collected; a body cut by the budget continues with `next` |
| `code_neighbors` | `relation`: `callers`, `callees`, `references`, `implementations` (of a symbol `id`), `imports` (of a file `path`), `importers` (of a `package`, as files import it); `depth` (1 to 3, default 1; imports and importers are one hop), `limit` | `edges`: `at` (the site, `path:line`), `relation`, `from` and `to` (qualified names) with `fromAt`, `toAt`, `fromId`, `toId`, `confidence`, `alias` (imports) and `depth` past the first hop |
| `code_outline` | `path` or `package` | `files` (`path`, `lang`, `package`, `parseError`) and `symbols`: the symbol tree in order, `depth` for nesting, with `at`, `kind`, `name`, `signature`, `exported`, `id`; no bodies |
| `code_context` | `task` (plain words naming the identifiers known) | `hits` (the best eight symbols for the task's words: name matches count most, then full-text prefix matches), `neighbors` (one hop of their callers and callees, five each), `bodies` (the hits' source, best first, as many as fit) |

Budgets: 2,000 tokens by default, 6,000 for `code_context`, at least 200; a
result is sized the way the session tools are (the JSON's length over four).
A list that does not fit says `truncated` and gives `next`; calling again
with the same arguments and `cursor` set to `next` continues. `code_context`
gives the hits and the neighbours a third of the budget each and fills the
rest with bodies; its `next` continues the bodies.

When the server answers 404 for the session (code sync off for this
worktree, or the session was never seen on a synced worktree), the tool asks
again for the working directory's branch and adds a note saying the results
do not include local edits. When the graph cannot be reached the tool fails
with "the code index is unavailable right now: search and read the files
directly", so the agent falls back to grep.

A typical use:

```text
code_search {"query": "allocateID"}
  → results[0]: {"at": "mind-palace/service/documents.go:310-340", "kind": "method", "name": "allocateID",
                  "qualName": "service.Service.allocateID", "match": "exact",
                  "id": "mind-palace/service/documents.go#service.Service.allocateID", …}
code_neighbors {"id": "mind-palace/service/documents.go#service.Service.allocateID", "relation": "callers"}
  → edges: [{"at": "mind-palace/service/documents.go:80", "relation": "callers", "from": "service.Service.Create",
             "to": "service.Service.allocateID", "confidence": "name", …}]
```

## Privacy

The graph routes are the first routes that return collected code, so they
are narrower than anything else on the server:

- **Your own view only.** A read is authenticated, scoped to the projects the
  token covers and opened under the server's namespace for the account (on
  Clued Cloud the project key is per user, `u<user id>__<slug>`, or per
  workspace, `w<workspace id>__<slug>`). A `session_id` must be a session
  seen on a worktree the caller owns; another account's session, an unknown
  one and a GitHub-backed worktree's are all 404, exactly alike.
- **Overlays are never shared.** Only overlays whose owner is the caller are
  ever put in a view, in a workspace project too. Members of one project
  share the base and the branch layers (pushed code), never each other's
  uncommitted or unpushed edits.
- **No raw queries.** Clients send typed bodies; FalkorDB queries are constant
  Cypher texts with parameters. Every id, path, ref and filter is validated
  before it reaches the graph.
- **Redacted content only.** Bodies come from the stored blobs, which hold what
  the clients sent after their redaction (`redactAtCapture`, `redactPatterns`)
  and never the deny-listed files (`.env`, keys, credentials), which have no
  content and therefore no chunks. Nothing returns more than the caller could
  have collected, plus the pushed code of a project the caller can read.
- **No third party.** Parsing, resolution and search run on the server; no
  embedding or other external service sees code.
- **FalkorDB stays inside.** In the generated stack FalkorDB publishes no
  port, requires a password generated into a 0600 file, and keeps nothing on
  disk. Graphs carry no secrets beyond the redacted code.

## Languages

Plan 0012 covers 21 languages. Detection (`codeparse/detect.go`) knows all
of them; a language is **parsed** once its grammar, query file and profile
have landed (`codeparse/grammars/<lang>.go`, `codeparse/queries/<lang>/`),
and its references are **resolved into edges** once its family's resolver is
registered (`codeindex/resolvers/<family>.go`). Adding a language is that
data plus a resolver entry, no change to the engine; the query captures and
the files a language adds are listed in
`mind-palace/codeparse/queries/README.md`.

What each stage gives:

- **Parsed** (grammar and query file present): symbols with kinds,
  qualified names, signatures, doc comments and export flags; imports
  (`IMPORTS` edges and `imports`/`importers`); unresolved references;
  structural chunks; `code_search`, `code_symbol` and `code_outline` work.
- **Resolved** (family resolver registered): the references become `CALLS`,
  `REFERENCES`, `IMPLEMENTS` and `EXTENDS` edges, so `callers`, `callees`,
  `references`, `implementations` and the neighbours of `code_context` work.
- **Detected only** (a language whose grammar or query a build lacks): the
  file is in the graph with its language, `parseError` set (cause `no
  grammar`) and 60-line window chunks; no symbols.

State of this tree (the directories above are the authority for a given
build): every language of the plan is parsed, and every family has a
resolver.

| Language | Files | Family | Parsed | Resolved |
|---|---|---|---|---|
| Go | `.go` | `go` | yes | yes |
| C | `.c`, `.h` | `cfamily` | yes | yes |
| C# | `.cs` | `csharp` | yes | yes |
| Java | `.java` | `jvm` | yes | yes |
| JavaScript | `.js`, `.mjs`, `.cjs`, `.jsx` | `jsts` | yes | yes |
| Lua | `.lua` | `lua` | yes | yes |
| OCaml | `.ml`, `.mli` | `ocaml` | yes | yes |
| Python | `.py`, `.pyi` | `python` | yes | yes |
| TypeScript | `.ts`, `.tsx`, `.mts`, `.cts`, `.d.ts` | `jsts` | yes | yes |
| Kotlin | `.kt`, `.kts` | `jvm` | yes | yes |
| Rust | `.rs` | `rust` | yes | yes |
| C++ | `.cc`, `.cpp`, `.cxx`, `.hpp`, `.hh`, `.h` | `cfamily` | yes | yes |
| Objective-C | `.m`, `.mm`, `.h` | `cfamily` | yes | yes |
| Swift | `.swift` | `swift` | yes | yes |
| Dart (and Flutter) | `.dart` | `dart` | yes | yes |
| PHP | `.php` | `php` | yes | yes |
| Ruby | `.rb`, `.rake`, `Gemfile` | `ruby` | yes | yes |
| SQL | `.sql` | `sql` | yes | yes |
| Perl | `.pl`, `.pm`, `.t` | `perl` | yes | yes |
| Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme, Racket) | `.lisp`, `.lsp`, `.cl`, `.asd`, `.el`, `.clj`, `.cljs`, `.cljc`, `.edn`, `.scm`, `.ss`, `.rkt` | `lisp` | yes | yes |

Markdown (`.md`, `.markdown`, `.mdx`) is chunked by heading and every other
text file by line windows; neither has symbols, so they do not show up in
`code_search` today (the full-text index covers symbols). Binary files have
no chunks.

`.h` is shared by C, C++ and Objective-C: a header with an Objective-C form
(`@interface`, `#import`, …) is Objective-C, one with a C++ form (`class`,
`template`, `namespace`, `std::`, …) C++, otherwise it follows the files
beside it (`.m`/`.mm`, C++ sources, `.c`) and defaults to C. `.m` is always
Objective-C. Mixed files (Vue, Svelte, HTML with scripts, SQL in
strings, Markdown code fences) are indexed as text.

### What each language extracts, and its known limits

The query files record their own limits; these are the ones that change what
an agent gets back.

- **Go** (`queries/go/`): functions, methods (the receiver type is the parent
  and qualifies the name), types, struct fields (named and embedded),
  interface methods, package-level constants and variables (not locals);
  imports with aliases; the `go.mod` module path turns a directory into its
  import path. Exported by capitalisation. Go is the reference language; a
  bare name never resolves across packages without an import (no name-only
  fallback).
- **C** (`queries/c/`): functions, prototypes and `extern` declarations (a
  header's API, so a file that declares and defines `f` has `f` and `f~2`),
  globals, structs, unions, enums and their enumerators, members (bit-field
  and function-pointer ones too), typedefs, macros; `#include` quoted and
  angle-bracketed. `static` definitions are not exported. No preprocessing:
  every branch of `#if`/`#ifdef` is indexed and a macro used as a function is
  a call. Lower-case identifiers used as values (mostly locals and
  parameters) are not tracked; upper-case ones (macro constants,
  enumerators) are. `#include NAME` through a macro is kept but never
  resolved.
- **C#** (`queries/csharp/`): the outermost namespace (block or file-scoped)
  is the package; classes, structs, records (positional parameters as
  properties), interfaces, enums, delegates, events, methods, constructors,
  operators, indexers, properties and fields; `using`, `using static` and
  aliases; the parts of a partial class share one qualified name, which the
  family resolver treats as one type.
  Exported when `public` or `protected`, or a member of an interface or enum.
  A class's first base named like an interface (`IName`) is taken as one.
  Pointer types are left out of the type references (`c.r * k` in an
  argument parses as a declaration).
- **Java** (`queries/java/`): classes, interfaces, enums, records (components
  as public accessors), annotation types and `module-info.java`; methods,
  constructors, fields, constants; single, on-demand and static imports
  (`java.lang` implicit), `requires`, `uses` and `provides`. Field accesses
  are kept only where they name a field (`this.count`, `Status.IN_STOCK`);
  `order.items` and `java.util` are left out, since whether the object is a
  value or a package is not known without types. Lower-case type names are
  taken as package parts.
- **JavaScript** (`queries/javascript/`, JSX included): functions, top-level
  bindings to functions, classes with their methods and fields,
  constructor-function methods (`Store.prototype.get = …`), CommonJS exports,
  top-level constants and variables; ES imports and re-exports, `require` and
  `import()` as imports; capitalised JSX elements as component references.
  A definition inside `export` or assigned to `exports.x` is exported; a
  name exported later (`export { a }`, `module.exports = { a }`) is not
  marked by the query (the resolver counts a name another file imports as
  exported). Class members carry no export flag.
- **Lua** (`queries/lua/`): global and local functions, table-field
  functions (`M.f`, `M:f`), functions in table constructors, module tables
  (`local M = {}` exported when the file returns it), class tables made with
  `setmetatable`, module-table constants; `require` in its forms, including
  `pcall(require, …)`; inheritance through `__index`. Local functions are
  file-private. Calls of the standard library's globals are left out.
  Under gotreesitter v0.56.0 an alternation around table constructors gave
  every field the first field's name, so they are separate patterns.
- **OCaml** (`queries/ocaml/`): modules, functors, module types, types with
  their constructors and fields, module-level functions and values (not
  local `let`s), operators, classes, external declarations; `open`,
  `include` and local opens; pipelines (`|>`, `@@`) and functor applications
  as calls. A file is its own module (`foo.ml` is `Foo`). gotreesitter ships
  no `ocaml_interface` grammar, so `.mli` files are parsed with the `ocaml`
  grammar, which covers signature items.
- **Python** (`queries/python/`): a file is its own module (`a/b.py` is
  `a/b`, an `__init__.py` its directory); classes, functions, methods,
  class attributes, module-level constants and variables; `import` and
  `from … import` (relative too) with aliases; decorators as call
  references. The docstring is the doc, else the comments above, past the
  decorators; a decorated definition's signature is its `def` or `class`
  line. Exported unless the name starts with an underscore.
- **TypeScript** (`queries/typescript/`, `.tsx` with JSX references): a
  file is its own module; functions, classes with methods and fields,
  interfaces, type aliases, enums, namespaces and `declare module` blocks
  (which qualify what they hold), constants and variables; ES imports,
  re-exports, `require` and `import()`, each import name as written (`a as
  b`, `default as c`) so the resolver knows the local name. A declaration
  under `export`, and a public class, interface or enum member, is
  exported.
- **Kotlin** (`queries/kotlin/`): the `package` header is the package;
  classes, objects, interfaces, enum classes, type aliases, functions,
  extension functions (qualified by their receiver), properties and `const
  val`s, never locals; imports with aliases and wildcards; calls in `.kts`
  scripts. Everything not `private` is exported. The grammar has no field
  names, so a signature is the first line (annotation-only lines skipped)
  and docs (KDoc) are the comments above, past annotations.
- **Rust** (`queries/rust/`): a file is a module of its crate's `mod`
  tree, names joined with `::`; functions, structs, enums and variants,
  unions, traits, type aliases, constants, statics, modules, `macro_rules!`
  macros, and impls named after their type, so a method is `Type::method`
  and `impl Trait for Type` references the trait as an implementation;
  `use` (globs and aliases), `extern crate` and `mod` declarations. An item
  with a visibility modifier is exported (macros never). A `///` doc above
  `#[derive(...)]` attributes is found past them.
- **C++** (`queries/cpp/`): no package clause; namespaces are definitions,
  so names qualify by position (`a::b::f`); classes, structs, unions,
  enums, methods, fields, functions, templates, typedefs and aliases,
  constants, macros; `#include`, `using`, `using namespace` and namespace
  aliases. A method defined outside its class (`A::f`) takes `A` as its
  receiver; a receiver that already holds a separator (`n::A::f`) is
  absolute and not prefixed again. Every definition is exported; no
  preprocessing, as for C.
- **Objective-C** (`queries/objc/`): `@interface`, class extensions,
  categories, `@implementation`, protocols, properties, instance variables,
  and the C layer (functions, structs, enums, typedefs, macros); methods
  and messages are named by their full selector, joined from `@name.part`
  (`initWithName:sides:`, `[self.items insertObject:x atIndex:0]` calls
  `insertObject:atIndex:`); `#import`, `#include` and `@import`.
  Foundation's `NS_ENUM` and `NS_ASSUME_NONNULL_BEGIN` parse with error
  nodes, which the patterns tolerate.
- **Swift** (`queries/swift/`): classes, structs, enums and their cases,
  actors, protocols and associated types, extensions (named by the type
  they extend, so their members qualify as that type's), functions,
  methods, properties, macros; top-level and member declarations only,
  never locals; `import`, `@testable` and `@_exported`. Exported when
  `public` or `open`. Signatures skip annotation-only lines (`@objc`
  written alone).
- **Dart** (`queries/dart/`, Flutter included): classes, mixins,
  extensions, enums, typedefs, functions, methods, constructors, getters,
  setters, fields and top-level variables; `import`, `export`, `part`,
  `part of`, deferred and conditional imports; `library a.b;` is the
  package. A definition with a body is anchored on its body, its name and
  signature taken from the signature before it; docs are found above the
  signature, past annotations (`@override`). Exported unless the name
  starts with an underscore.
- **PHP** (`queries/php/`): `namespace A\B;` qualifies the definitions
  that follow it, up to the next `namespace` statement; a braced `namespace
  A\B { … }` holds its contents. Classes, interfaces, traits, enums,
  methods, properties, class constants, functions and `define()`
  constants; `use` (function, const, grouped and aliased), `require` and
  `include` in their forms. Top-level declarations are exported, members
  when public. Names compare without case. Anonymous classes' members are
  not definitions.
- **Ruby** (`queries/ruby/`): modules, classes, methods (`Shop::Order#total`
  for instance methods), constants and the definitions written as calls
  (`attr_*`, `define_method`, Rake's `namespace` and `task`); `require`,
  `require_relative`, `load`, `autoload` and Gemfile `gem`s; superclasses
  and `include`/`extend`/`prepend` mixins. Names are qualified by lexical
  nesting. Bare identifiers are not references: a zero-argument call
  cannot be told from a local.
- **SQL** (`queries/sql/`, PostgreSQL-flavoured grammar): schemas, tables,
  views, columns (`ALTER TABLE … ADD` too), indexes, sequences, types,
  domains, enums, functions, procedures and triggers; a schema-qualified
  name (`s.t`) is `s.t`; references to the tables and routines queries
  reach; `CREATE EXTENSION` and psql `\i` as imports; `USE db` is the
  package. Names compare without case and lose their quotes. Routine
  bodies are dollar-quoted strings, so nothing inside them is seen;
  unparsed statements keep what parsed around them.
- **Perl** (`queries/perl/`): `package NAME;` qualifies the subs after it
  up to the next `package` statement, and `sub Other::f` is `Other::f`
  wherever it is written (a receiver that holds a separator is absolute);
  subs, constants, package variables, Moose `has` attributes and 5.38
  `class`/`field`/`method`; `use`, `require` and pragmas; `use parent`,
  `@ISA` and Moose's `extends`/`with` as inheritance. A POD block or the
  comments above are the doc. Exported unless the name starts with an
  underscore. A forward declaration (`sub f;`) runs on to the end of the
  file in this grammar.
- **Lisp** (`queries/lisp/`, one query per dialect: `tags.scm` for Common
  Lisp and files without a dialect, `elisp.scm`, `clojure.scm`,
  `scheme.scm`, `racket.scm`): `defun`, `defmacro`, `defmethod`,
  `defgeneric` and every other form whose head starts with `def` (looked up
  in the profile's `definingForms`; an unknown definer is a call); the
  docstring or the comments above as the doc. Packages come from
  `in-package` (Common Lisp), `ns` (Clojure), the file or its library name
  (Scheme, Racket), none in Emacs Lisp. Imports are `defpackage` clauses,
  `:require`, `require`, `import`, `#lang` and the like. A Racket or
  Scheme reference written `prefix:name` (after `prefix-in str:`) is split
  into qualifier `str` and name, so it resolves through the prefix.

A file the grammar rejects keeps whatever the query matched in the tree
around the error; a parse cut off after 2 s, or one that fails, indexes the
file as text with `parseError` set.

**Caching.** Facts are a pure function of a file's bytes, its language and
the facts version (the engine's `codeparse.Version` plus a digest of the
language's profile and query files), so a file version is parsed once for
every branch, layer and member that holds it, and changing a language's
query re-parses that language alone (a change of `codeparse.Version`, now
`"3"`, re-parses everything). `server run --code-store mongo` and the
`server graph` commands keep the facts in the persistent MongoDB parse cache
(`codeparse/mongocache`, collection `code_parses` in the session database,
an entry expiring 30 days after its last use), so a restart or a rebuild
does not parse again; an in-memory setup (`--code-store memory`) keeps a
memory cache of 4,096 file versions that lives as long as the indexer. A
composing server passes its own as `codeindex.Options.Cache`.

### How references resolve

The engine (`codeindex/resolve.go`) tries, in order, the reference's
enclosing definitions, its file, its package, what an import binds to the
qualifier or name, the packages opened unqualified, a definition whose
qualified name ends with the qualifier and name, a member reached through a
qualifier that is not an import, and last, where the family allows it, any
visible definition of that name (confidence `name`). Each family's resolver
(`codeindex/resolvers/<family>.go`, whose doc comments hold the full rules)
says what a package is, what imports bind and what is visible:

| Family | Packages and imports | Name fallback and visibility |
|---|---|---|
| `go` | A package is its directory, keyed by import path (`go.mod`); an import binds its alias or the package's name, `.` opens it | No name fallback; another package's definitions must be exported |
| `cfamily` | No packages: a file sees its `#include`/`#import` chain (quoted next to the file, then CMake and `compile_commands.json` include directories, the root, path suffix, base name) and its header/source pair, so callers land on the body, not the prototype | Name fallback; `std::` stays unresolved; C++ bare names only through the global, an enclosing or a `using` namespace; `static` is file-private |
| `csharp` | The outermost namespace, with its parents opened; `using` opens a namespace, an alias binds a qualifier, `using static` reaches through the fallback | Name fallback; public, protected and internal members are visible, private ones only within their (partial) type |
| `dart` | A library (its parts share its key); `package:` and relative URIs resolve to files; a prefix binds the library and its exports, no prefix opens them, `show`/`hide` filter | Name fallback; a leading underscore is library-private |
| `jsts` | A file is its module; relative specs, `tsconfig` `paths`/`baseUrl`, `package.json` `imports`/`exports` and workspaces; default and namespace imports bind a qualifier, named imports names; re-exports are followed | Name fallback, never to a name the file imports from outside the project; module-level definitions visible when exported, imported by name, or in a script |
| `jvm` | Java and Kotlin share packages (the declaration); a single import binds its name, on-demand and implicit (`java.lang`, Kotlin defaults) imports open the package | Name fallback; an explicit import shadows other definitions of its name; Kotlin `private` is file-private |
| `lisp` | Per dialect: `in-package`/`defpackage` (`:use`, `:import-from`, nicknames), Clojure `ns` and `:require`, one global Emacs Lisp namespace, Scheme and Racket modules by path or library name, prefixes bound as qualifiers | Name fallback within the dialect group; case-insensitive; no member fallback (a qualifier is a package) |
| `lua` | `require` binds the module's returned table as a qualifier | Name fallback to globals only; members through an expression only of exported tables |
| `ocaml` | No imports: a qualifier binds the module the compiler would pick (own library, directory, opened library); `open`, `include` and local opens open a module | No name fallback: bare names leave the module only through an open |
| `perl` | `package NAME;` (else `main`, keyed by path); `use Module LIST` binds the names, `use Module` opens it; `Package::sub` is absolute; `$self` names the file's package | Name fallback; a leading underscore keeps a sub from fallbacks and opens, not from qualified calls |
| `php` | The namespace; `use` binds a name and a qualifier, `use function`/`use const` the name; functions and constants fall back to the global namespace; `require`/`include` mark a file's namespace imported | No name fallback; case-insensitive |
| `python` | A module per file (`__init__.py` for its directory); relative and absolute imports, `from m import x` followed through `__init__.py` re-exports, `*` opens | Name fallback to other modules' exported top-level definitions only; class members only through `self` and the member fallback |
| `ruby` | A file's feature (path below `lib/`); `require`, `require_relative`, `load` open features, followed transitively; `autoload` imports a name; lexical nesting for constants | Name fallback (Rails and Zeitwerk autoload); every definition visible except Rake tasks |
| `rust` | The module's path in its crate's `mod` tree (`Cargo.toml`, `src/lib.rs`, `src/main.rs`); `use` paths from `crate::`, `self::`, `super::` or a crate name bind a module qualifier or an item name; globs open | Name fallback (covers re-exports); a crate sees all its own definitions, another crate only `pub` ones |
| `sql` | A file's default schema (`USE`, else `public`); schema-qualified references by qualified name; a table alias reaches a column | Name fallback to an object of that name in any schema; case-insensitive |
| `swift` | A module is a SwiftPM target (or an Xcode top directory); `import M` opens M and binds it as a qualifier; extensions merge into their type | Name fallback within the module and imported modules; another module's definitions only when `public`/`open`, or imported `@testable` |

## Rebuild, status and checks

The server's indexer (`server run --graph`) replays each project's change log
in batches of 500 under a one-minute lease, so only one worker writes a
project at a time; a write to the code transport wakes it at once, and it
polls every 2 seconds otherwise. When FalkorDB cannot be reached it backs
off (up to 5 minutes) and its cursor does not move. Every hour it also
compares each project's graph with its current trees and repairs drift.

The same work can be started by hand, beside a running server and against
the same MongoDB and FalkorDB (they take the project's lease, so they never
write while the server's indexer does):

```bash
export CLUED_SERVER_GRAPH='redis://default:<password>@127.0.0.1:6379'
mind-palace server graph status github.com__owner__repo
mind-palace server graph status github.com__owner__repo --check            # compare with the code store
mind-palace server graph status github.com__owner__repo --check --repair   # and write the difference
mind-palace server graph rebuild github.com__owner__repo                    # drop and rebuild
```

A project is named by its key as the code store names it (on a self-hosted
server, the project slug). `rebuild` drops the graph and replays the
project's current trees (not the whole log) through the same selection and
file writes as the incremental path, then moves the cursor to the end of the
log; it is the answer to a parser or schema change, a graph that drifted,
and a graph that was unloaded. `status` prints the cursor, the changes not
yet indexed, the worker holding the lease and each layer's file count and
hash. Both need `--code-store mongo` (a memory code store lives in the server
process). The exact output is in [PROTOCOL.md](../PROTOCOL.md) section 9.

## FalkorDB in the stack

The stack `mind-palace server init` generates runs a FalkorDB container
(`falkordb/falkordb:v4.22.1` by default, `--falkordb-image` to change it)
next to the server and MongoDB, and turns the graph on:

| File | Mode | Holds |
|---|---|---|
| `falkordb.password` | 0600 | FalkorDB's password: 32 random bytes as hex, generated once and kept on reruns (a hand-made one must be 16 or more letters and digits) |
| `falkordb.env` | 0600 | FalkorDB's environment: `REDIS_ARGS=--requirepass … --save --appendonly no` (persistence off) and `REDISCLI_AUTH` for the health check |
| `graph.env` | 0600 | The server's `CLUED_SERVER_GRAPH=redis://default:<password>@falkordb:6379` |

The password never appears in the compose file, a command line or the
`init` result (which says `"graph": "generated"` or `"kept"`). FalkorDB
publishes no port and runs without its browser; the server waits for it to
be healthy. Apple Containers get the same in `up.sh`, which renders
`run/graph.env` with FalkorDB's address of that start. Since nothing is
persisted, a restarted FalkorDB is empty; the server's hourly consistency
check writes the graphs again from the code store, and `server graph
rebuild` does it at once. See [server.md](server.md#the-code-graph).

## Evaluation

`mind-palace/eval/codesearch` measures whether the tools save anything: 101
tasks with ground truth (29 on clued and 13 on clued-cloud in Go, Markdown
and shell, the rest on a pinned corpus of public repositories, about three
per supported language), in four families (named lookups, concept lookups,
impact questions, small edits checked by a command). Each task runs in a
headless `claude -p` session in three arms (grep and read only; plus the
graph tools; plus `code_context` only), three times by default, and the
report gives tokens, turns, tool calls and success per family and language,
read from clued's own capture of those sessions. The run is manual and paid:

```bash
scripts/eval-codesearch.sh --verify-only                 # check the ground truth, no sessions
scripts/eval-codesearch.sh --arms grep,graph --runs 1    # needs a server with the graph and Claude credentials
```

The tool defaults above (budgets, depth, which tools are listed) are the
plan's; the evaluation run (story 0126) is what confirms or changes them.

## Limits

| Limit | Value |
|---|---|
| File content parsed | what the code store holds: up to 1 MiB per file (larger files are path and size only) |
| Parse time | 2 s per file, then text |
| Chunks | at most 1,500 tokens each, 1,000 per file |
| Search query, context task, ids, package names | 1,024 bytes |
| `kinds`, `langs`, `paths` filters | 32 values each |
| Results per read | `search` 20 by default, `neighbors` 50, at most 500 |
| `neighbors` depth | 1 to 3 |
| Token budget | 2,000 by default (`code_context` 6,000), at least 200 |
| Dependents re-resolved per batch | 5,000 references |
| Idle overlay | dropped after 14 days without session activity and with nothing dirty or unpushed |

Name resolution is scoped by each language's imports, packages and
namespaces; it is not compiler-grade. Overloads, generics, dynamic dispatch
and C/C++ without preprocessing can give wrong or missing edges, which is
what `confidence` makes visible. Git history (blame, a symbol across
commits) is not in the graph: it reflects the current layers.
