; Clojure, ClojureScript and EDN (sogaiu/tree-sitter-clojure): definitions,
; references, imports and the namespace, in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md).
;
; The grammar knows no definitions: every one is a form whose head starts
; with "def", captured as @definition.form and looked up in the profile's
; definingForms (an unknown definer, such as a library's defsomething,
; becomes a call reference). A symbol's metadata (^:private, ^String) is
; part of its node, so names are taken from its sym_name. A docstring comes
; after the name and before the parameters or the value. Patterns with a
; doc come before the same pattern without one, so the extractor keeps the
; first. gotreesitter mixes up @name across the branches of an
; alternation, so each spelling has a pattern of its own.

; ---- namespace

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit name: (sym_name) @package))
  (#eq? @_head "ns"))

; ---- imports

; (ns foo (:require [clojure.string :as str] [foo.util :refer [a b]] foo.x))
((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (vec_lit
     .
     (sym_lit name: (sym_name) @import.spec)) @import)
  (#any-of? @_option "require" "use" "require-macros" "use-macros")
  (#set! import.kind "require"))

((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (sym_lit name: (sym_name) @import.spec) @import)
  (#any-of? @_option "require" "use" "require-macros" "use-macros")
  (#set! import.kind "require"))

; [foo.util :as u :refer [a b]] in a require list, quoted or behind a reader
; conditional; a destructuring vector ([a b :as all]) sits in a vector.
((vec_lit
   .
   (sym_lit name: (sym_name) @import.spec)
   (kwd_lit name: (kwd_name) @_as)
   .
   (sym_lit name: (sym_name) @import.alias)) @import
  (#has-parent? @import list_lit quoting_lit read_cond_lit)
  (#any-of? @_as "as" "as-alias")
  (#set! import.kind "require"))

((vec_lit
   .
   (sym_lit name: (sym_name) @import.spec)
   (kwd_lit name: (kwd_name) @_refer)
   .
   (vec_lit
     (sym_lit name: (sym_name) @import.name))) @import
  (#has-parent? @import list_lit quoting_lit read_cond_lit)
  (#any-of? @_refer "refer" "only")
  (#set! import.kind "require"))

; (require '[clojure.set :as set]) and (require 'clojure.set) at the top
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   (quoting_lit
     (vec_lit
       .
       (sym_lit name: (sym_name) @import.spec)) @import))
  (#any-of? @_head "require" "use")
  (#set! import.kind "require"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   (quoting_lit
     (sym_lit name: (sym_name) @import.spec)) @import)
  (#any-of? @_head "require" "use")
  (#set! import.kind "require"))

; (:import (java.util Date UUID) java.io.File)
((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (list_lit . (sym_lit name: (sym_name) @import.spec)) @import)
  (#eq? @_option "import")
  (#set! import.kind "import"))

((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (vec_lit . (sym_lit name: (sym_name) @import.spec)) @import)
  (#eq? @_option "import")
  (#set! import.kind "import"))

((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (list_lit . (sym_lit name: (sym_name) @import.spec) (sym_lit name: (sym_name) @import.name)) @import)
  (#eq? @_option "import"))

((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (vec_lit . (sym_lit name: (sym_name) @import.spec) (sym_lit name: (sym_name) @import.name)) @import)
  (#eq? @_option "import"))

((list_lit
   .
   (kwd_lit name: (kwd_name) @_option)
   (sym_lit name: (sym_name) @import.spec) @import)
  (#eq? @_option "import")
  (#set! import.kind "import"))

; ---- forms whose head starts with "def" (definingForms decides)

; (defn f "Doc." [x] ...), (def v "Doc." 1)
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @form)
   .
   (sym_lit name: (sym_name) @name)
   .
   (str_lit) @doc
   .
   (_)) @definition.form
  (#match? @form "^def"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @form)
   .
   (sym_lit name: (sym_name) @name)) @definition.form
  (#match? @form "^def"))

; defprotocol methods: (defprotocol P (area [this] "Doc."))
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   (list_lit
     .
     (sym_lit name: (sym_name) @name)
     (str_lit) @doc) @definition.method)
  (#any-of? @_head "defprotocol" "definterface"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   (list_lit
     .
     (sym_lit name: (sym_name) @name)) @definition.method)
  (#any-of? @_head "defprotocol" "definterface"))

; defrecord and deftype: fields and method bodies
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   .
   (vec_lit
     (sym_lit name: (sym_name) @name) @definition.field))
  (#any-of? @_head "defrecord" "deftype"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   .
   (vec_lit)
   (list_lit
     .
     (sym_lit name: (sym_name) @name)) @definition.method)
  (#any-of? @_head "defrecord" "deftype"))

; ---- implementations and types

; (defrecord R [a] P Q ...), (deftype T [] P ...)
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   .
   (vec_lit)
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.implements)
  (#any-of? @_head "defrecord" "deftype"))

; (reify P ...), (extend-protocol P T ...), (extend-type T P ...)
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.implements)
  (#eq? @_head "reify"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.implements)
  (#eq? @_head "extend-protocol"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.type)
  (#eq? @_head "extend-protocol"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.type)
  (#eq? @_head "extend-type"))

((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit)
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.implements)
  (#eq? @_head "extend-type"))

; (proxy [Base Iface] [] ...)
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (vec_lit
     (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.extends))
  (#eq? @_head "proxy"))

; (new Date), and type hints: ^String s
((list_lit
   .
   (sym_lit !namespace name: (sym_name) @_head)
   .
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.type)
  (#eq? @_head "new"))

((meta_lit
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name) @reference.type)
  (#match? @name "^[A-Z]"))

; #'foo/bar names a var.
(var_quoting_lit
  (sym_lit namespace: (_)? @qualifier name: (sym_name) @name)) @reference.value

; #(f %) calls f.
((anon_fn_lit
   .
   (sym_lit namespace: (_)? @qualifier name: (sym_name) @name)) @reference.call
  (#not-match? @name "^\\.|\\.$"))

; ---- calls
;
; A call is a list headed by a symbol where it is evaluated: a top-level
; form, a definition's body, an argument of another call, the body of a
; binding form (after its bindings) or a clause's body. Parameter lists,
; bindings, slots, quoted data and import specs are not calls. Special
; forms and the common binding and control macros are left out, and so are
; definers: their heads go through definingForms. A query cannot see every
; macro's shape: a parameter list nested one level deeper (a local
; function of flet or labels, a handler-case clause, a macro's pattern)
; still reads as a call of its first parameter.

; Top level, definition bodies and the other places every list is code.
((source
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((vec_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((map_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((set_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((anon_fn_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((read_cond_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((syn_quoting_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((unquoting_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((unquote_splicing_lit
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

((list_lit
   .
   (vec_lit)
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))

; An argument of a form that is not a binder or a data form.
((list_lit
   .
   (sym_lit name: (sym_name) @_parent)
   (list_lit
     .
     (sym_lit
       namespace: (_)? @qualifier
       name: (sym_name) @name)) @reference.call)
  (#not-any-of? @_parent
    "quote" "ns" "defprotocol" "definterface" "defrecord" "deftype" "reify"
    "extend-protocol" "extend-type" "proxy" "letfn" "require" "import"
    "use" "case")
  (#not-match? @name "^def|^\\.|\\.$")
  (#not-any-of? @name
    "fn" "fn*" "let" "let*" "letfn" "loop" "recur" "if" "if-not" "do"
    "quote" "var" "throw" "try" "catch" "finally" "set!" "new"
    "monitor-enter" "monitor-exit" "when" "when-not" "when-let" "when-some"
    "when-first" "if-let" "if-some" "cond" "condp" "case" "and" "or" "->"
    "->>" "some->" "some->>" "cond->" "cond->>" "as->" "doto" "comment"
    "ns" "in-ns" "require" "import" "use" "refer" "binding" "with-open"
    "doseq" "dotimes" "for" "reify" "proxy" "extend-protocol" "extend-type"))
