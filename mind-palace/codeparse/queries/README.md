# Language query files

Each directory here is one language of `codeparse`, named by its ID in the
Languages table of `../detect.go`. The extractor (`../extract.go`) is the
same for every language; a language is its grammar, a query file and a
profile. Go (`go/`) is the reference.

## What a language story adds

Exactly these files, and no change to any shared file:

| File | What it holds |
|---|---|
| `mind-palace/codeparse/grammars/<lang>.go` | `func init() { Register("<grammar>", <pkg>.Language) }`, importing only `github.com/odvcencio/gotreesitter/grammars/<grammar>`, never the aggregate `grammars` package (it embeds GPL and MPL grammars). One `Register` per grammar the language uses (a dialect's grammar too). Do not end the file name in `_<goos>` or `_<goarch>` (`_js`, `_wasm`): Go would build it for that platform only. |
| `mind-palace/codeparse/queries/<lang>/profile.yaml` | Family, extensions and filenames exactly as the Languages table has them, the grammar name when it is not the language ID, per-dialect grammar and query, `kinds`, `definingForms`, `scope` (see `Profile` in `../profiles.go`). |
| `mind-palace/codeparse/queries/<lang>/tags.scm` | The query (and one `.scm` per dialect that needs its own). |
| `mind-palace/codeparse/testdata/<lang>/<input>` | Inputs of the language: a typical file, one with a syntax error, and whatever the language's edge cases are. An input named `x.in` is parsed as `x`, for files a repository tool must not see (a broken `.go` file and `gofmt`). |
| `mind-palace/codeparse/testdata/<lang>/<input>.facts.json` | The expected facts, written by `go test ./codeparse -run TestGolden -update` and reviewed by hand. |
| `go.mod`, `go.sum` | Only if the grammar package is not already required (all gotreesitter grammars live in its one module). |

`go test ./codeparse/...` then runs `TestGolden/<lang>/<input>`, checks
that the grammar is registered, the query compiles and uses only the
captures below, and that a language with a profile has goldens. Changing a
language's files changes its profile digest, so cached facts of that
language alone are parsed again.

## Captures

Patterns follow tree-sitter's tags-query convention. Each match carries one
role capture and the captures that describe it:

| Capture | Meaning |
|---|---|
| `@definition.<kind>` | The whole definition node. `<kind>` maps through the profile's `kinds`, then `DefaultKinds` (`function`, `method`, `class`, `struct`, `interface`, `enum`, `type`, `field`, `constant`, `variable`, `module`, `namespace`, `macro`, `impl`, ...), else the word itself. |
| `@definition.form` | A definition written as a call (Lisp `defun`, Ruby `attr_reader`); `@form` is its head, looked up in `definingForms`. A head not listed becomes a call reference. |
| `@reference.<kind>` | A reference: `call` (`send`, `macro`), `type` (`class`, `interface`, `struct`, `enum`, `trait`, `module`), `extends` (`superclass`, `inherits`, `mixin`), `implements` (`implementation`), `value` (`variable`, `constant`, `field`, `property`); a profile's `kinds` may map another `reference.<word>` to one of `call`, `type`, `extends`, `implements`, `value`. |
| `@name` | The defined or referenced name (required for both). |
| `@name.part` | A piece of a name written in several places, in place of `@name` (Objective-C captures each selector keyword and its colon). The pieces of every match of one definition or reference node (same kind) are joined in source order without white space: `initWithName:sides:`. |
| `@doc` | The doc comment or docstring (repeatable). Without it, the comments standing on the lines just above the definition are its doc, past any attribute, decorator or annotation nodes between them (`#[derive]`, `@decorator`). Comment markers and string quotes are stripped. |
| `@signature` | The node whose text is the signature. Without it, the definition's text up to its `body` field, or its first line, less the lines before it that hold only annotations (`@Deprecated(...)`, `#[derive(...)]`, `[Serializable]`). |
| `@receiver` | The type a definition written outside it belongs to (a Go method's receiver, `A` in C++'s `A::f`): it becomes the symbol's parent and qualifies its name. |
| `@qualifier` | What a reference is reached through (`pkg` in `pkg.F`, the receiver expression in `s.f()`). |
| `@exported` | Present when a definition is exported, for `scope.exported: declared`. |
| `@import` | A whole import, include, use or require form, with `@import.spec` (the path or module; quotes and angle brackets are stripped; the whole form when absent), `@import.alias`, `@import.name` (repeatable) and `(#set! import.kind "<word>")` (`include_system`, `require`, ...). Matches of one node merge. |
| `@package` | The file's package, module or namespace declaration (`Facts.Package`, the first one, is the prefix of qualified names). One whose declaration has no body (Perl's `package A;`, PHP's `namespace A;`) qualifies the definitions from there to the next one. A receiver that already holds a separator (`sub Other::f`) is not prefixed. |
| `@_anything` | Free for predicates; ignored. |

The extractor nests definitions by position (a field in its struct, a
method in its class), qualifies names with the package and the enclosing
definitions joined by the profile's first separator, numbers repeated
names (`init`, `init~2`), drops references at a definition's own name, and
cuts chunks (one per top-level definition, split at statement boundaries
above 1,500 tokens, the code between definitions as windows). A parse cut
off after 2 s, or one that fails, indexes the file as text with
`parseError`; a tree with error nodes keeps whatever the query matched.
