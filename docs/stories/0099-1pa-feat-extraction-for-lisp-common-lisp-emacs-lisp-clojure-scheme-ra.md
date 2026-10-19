---
id: 0099-1pa
title: Extraction for Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme/Racket)
purpose: 'Adds Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme/Racket) to the indexer: files .lisp, .lsp, .cl, .asd, .el, .clj, .cljs, .cljc, .edn, .scm, .ss, .rkt.'
type: feat
status: complete
priority: "3"
effort: L
created: "2026-10-19T18:09:28.522Z"
updated: "2026-10-19T21:11:19.811Z"
started: "2026-10-19T20:48:46.361Z"
completed: "2026-10-19T21:11:19.811Z"
tags:
  - plan-0012-cgi
  - wave-3
  - language
  - lang-lisp
milestone: ""
due_date: ""
planned_start: ""
planned_end: ""
order: 3
links:
  repo:
    remote: https://github.com/robbiebyrd/clued
    files:
      - mind-palace/codeparse/queries/lisp/
      - mind-palace/codeparse/testdata/lisp/
      - mind-palace/codeparse/grammars/lisp.go
  web:
    plan-0012-cgi: https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md
  stories:
    - ["0080-6sk", "depends"]
  plans:
    - ["0012-cgi", "included", ["1.2"]]
progress:
  "1":
    status: complete
  "2":
    status: complete
  "3":
    status: complete
  "4":
    status: complete
  "5":
    status: complete
  "6":
    status: complete
---

# Extraction for Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme/Racket)

## Problem Statement

Adds Lisp (Common Lisp, Emacs Lisp, Clojure, Scheme/Racket) to the indexer: files .lisp, .lsp, .cl, .asd, .el, .clj, .cljs, .cljc, .edn, .scm, .ss, .rkt. Each language is independent data, so this story can run alongside every other language story.

**Working on this concurrently.** This story is in wave 3 of plan 0012-cgi. Start it when these are complete: `0080-6sk` Generic extractor, chunker, golden harness and the Go reference language. Change only the files listed under Files; any other story touching the same file is ordered before or after this one through its dependencies, so two agents never edit one file at once. If the work needs a file outside the list, stop and record why in the work log instead of editing it. `go.mod`/`go.sum` conflicts are resolved by merging main and running `go mod tidy`. Unblocks: `0120-fq2` Resolver for Lisp dialects.

**Impact:** Blocks the later stories of plan 0012-cgi that depend on it.

## Steps

### 1: Read the plan section

Read plan 0012-cgi sections 1.2 ([plan](https://github.com/robbiebyrd/clued/blob/main/docs/plans/0012-cgi-dsgn-code-graph-indexer.md)) and the contracts this story builds on; set the story `in_progress` with `story_setStatus`.

### 2: Grammar

Register the grammar for the runtime the spike chose; add it to the runtime if the spike listed it as missing.

### 3: Query file

Start from the upstream `tags.scm` if one exists; capture `defun`, `defmacro`, `defvar`, `defclass`, `defmethod`, `defpackage`/`ns`, `define`; add `@import`, `@package`, `@doc`, `@signature`.

### 4: Profile

`profile.yaml`: extensions (.lisp, .lsp, .cl, .asd, .el, .clj, .cljs, .cljc, .edn, .scm, .ss, .rkt), family, defining forms; scope rules for resolution later: `in-package`, `ns`, `require`, package-qualified symbols.

### 5: Goldens

At least four inputs: definitions of every kind, imports, references (calls, types, inheritance), and an awkward case (generated code, macros, nested definitions or dialect quirks); `go test ./codeparse/ -run TestGolden/lisp -update`, then review the facts by hand.

### 6: Verify and hand off

Run `cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/lisp'`, append a work-log entry with what changed and the test counts, tick the criteria, and open a pull request that names this story id.

## Acceptance Criteria

- [x] VERIFY: cd mind-palace && go test -race ./codeparse/ -run 'TestGolden/lisp'
- [x] [MANUAL] Only files under this story's three paths changed
- [x] VERIFY: cd mind-palace && test -z "$(gofmt -l .)" && go vet ./... && go build ./... && go test -race ./codeparse/ -run 'TestGolden/lisp'
- [x] [MANUAL] Only the files listed under Files are changed (`git diff --name-only origin/main...HEAD`; `go.mod`/`go.sum` excepted)

## Files

- `mind-palace/codeparse/queries/lisp/` - query file(s) and `profile.yaml`
- `mind-palace/codeparse/testdata/lisp/` - golden inputs and `*.facts.json`
- `mind-palace/codeparse/grammars/lisp.go` - grammar registration (and the grammar itself if the spike found it missing)

## Work Log

### 2026-10-19T21:11:14.651Z - Added codeparse/grammars/lisp.go (registers the gotreesitter commonlisp, elisp, clojure, scheme and racket grammars) and queries/lisp/: profile.yaml (family lisp, the 12 extensions, grammar commonlisp + tags.scm for the language default, one dialect entry per grammar with its own query: tags.scm = Common Lisp, elisp.scm, clojure.scm, scheme.scm, racket.scm; definingForms per dialect; scope separators [':', '::', '/'], packageBy declaration, caseInsensitive false with options caseInsensitiveDialects=commonlisp, packageForms, fileModuleDialects, globalDialects, qualifierSeparators for resolver 0120). Queries: definitions via grammar nodes (CL defun/defmacro/defmethod/defgeneric with header signature, elisp function_definition/macro_definition/defvar/defconst) and @definition.form for def*-heads (unknown definers become calls), docstrings and (:documentation), slots/fields, CL in-package and Clojure ns as @package, imports (require, ql/asdf load, defpackage :use/:import-from/:local-nicknames, ASDF :depends-on, use-package, autoload, ns :require/:import with :as/:refer, R7RS/Guile/Chicken import with only/prefix/except/rename, include, Racket require with only-in/prefix-in/for-syntax and #lang), calls in evaluated positions only (top level, bodies, arguments of non-binder forms, binder bodies, clause bodies; special forms and binders excluded), extends (defclass/define-condition supers, :include, define-derived-mode, struct supers, class, proxy), implements (defrecord/deftype/reify/extend-protocol, class*), types (defmethod specializers, make-instance 'x, handler-case types, type hints, new, extend-type). gotreesitter's alternation @name bug avoided: one pattern per spelling. Goldens: 12 inputs (geometry.lisp, legacy.lisp upper-case/nested, geometry.asd, broken.lisp, tidy-mode.el, shapes.clj, util.cljc reader conditionals, deps.edn, broken.clj, queue.scm R7RS library, modules.ss Guile/Chicken, shapes.rkt); TestGolden/lisp 12/12 pass with -race; gofmt, go vet ./..., go build ./... clean; go test -race ./codeparse/... passes (TestGolden/go/large.go flaked once under machine load: its parse hit the 2 s cut-off, unrelated to Lisp). Known limits, documented in the queries: a parameter list one level deeper than the form shows (flet/labels functions, handler-case clauses, a macro's pattern, cl-defun defaults) reads as a call of its first parameter; upper-case CL special forms are not in the skip lists; a defsystem named by a string has no symbol (names are not unquoted by the engine); single-line struct fields inherit the struct's comment doc (engine fallback). Only files under the story's three paths changed.
