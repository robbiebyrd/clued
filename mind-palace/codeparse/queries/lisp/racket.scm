; Racket (6cdh/tree-sitter-racket): definitions, references and imports, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). A Racket file is its own module, so there is no @package;
; its #lang is an import of kind "lang".
;
; The grammar knows no definitions: (define (f x) ...) is a function,
; (define x ...) a variable, and every other definition a form whose head
; starts with "def" (define-syntax-rule, define/public) or is struct or
; module, captured as @definition.form and looked up in the profile's
; definingForms (an unknown definer becomes a call reference). Square
; brackets parse as lists. Docs are the comments just above. gotreesitter
; mixes up @name across the branches of an alternation, so each spelling
; has a pattern of its own.

; ---- imports

(extension
  (lang_name) @import.spec
  (#set! import.kind "lang")) @import

; (require racket/list "util.rkt")
((list
   .
   (symbol) @_head
   (symbol) @import.spec @import)
  (#eq? @_head "require")
  (#set! import.kind "require"))

((list
   .
   (symbol) @_head
   (string) @import.spec @import)
  (#eq? @_head "require")
  (#set! import.kind "require"))

; (require (only-in "util.rkt" helper) (except-in racket/list first)
;          (rename-in m [a b]))
((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (symbol) @import.spec) @import)
  (#eq? @_head "require")
  (#any-of? @_modifier "only-in" "except-in" "rename-in")
  (#set! import.kind "require"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (string) @import.spec) @import)
  (#eq? @_head "require")
  (#any-of? @_modifier "only-in" "except-in" "rename-in")
  (#set! import.kind "require"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (symbol) @import.spec
     (symbol) @import.name) @import)
  (#eq? @_head "require")
  (#eq? @_modifier "only-in"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (string) @import.spec
     (symbol) @import.name) @import)
  (#eq? @_head "require")
  (#eq? @_modifier "only-in"))

; (require (for-syntax racket/base syntax/parse)): one import per module
((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     (symbol) @import.spec @import))
  (#eq? @_head "require")
  (#any-of? @_modifier "for-syntax" "for-template" "for-label")
  (#set! import.kind "require"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     (string) @import.spec @import))
  (#eq? @_head "require")
  (#any-of? @_modifier "for-syntax" "for-template" "for-label")
  (#set! import.kind "require"))

; (require (prefix-in s: racket/string))
((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (symbol) @import.alias
     .
     (symbol) @import.spec) @import)
  (#eq? @_head "require")
  (#eq? @_modifier "prefix-in")
  (#set! import.kind "require"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (symbol) @import.alias
     .
     (string) @import.spec) @import)
  (#eq? @_head "require")
  (#eq? @_modifier "prefix-in")
  (#set! import.kind "require"))

; ---- definitions

; (define (f x) ...), (define ((curried a) b) ...)
((list
   .
   (symbol) @_head
   .
   (list . (symbol) @name)) @definition.function
  (#eq? @_head "define"))

((list
   .
   (symbol) @_head
   .
   (list . (list . (symbol) @name))) @definition.function
  (#eq? @_head "define"))

; (define x 1)
((list
   .
   (symbol) @_head
   .
   (symbol) @name) @definition.variable
  (#eq? @_head "define"))

((list
   .
   (symbol) @form
   .
   (symbol) @name) @definition.form
  (#match? @form "^(def|struct$|module)")
  (#not-eq? @form "define"))

((list
   .
   (symbol) @form
   .
   (list . (symbol) @name)) @definition.form
  (#match? @form "^(def|struct$)")
  (#not-eq? @form "define"))

; ---- struct fields and inheritance

; (struct point (x [y #:mutable]) #:transparent), (struct p3 point (z))
((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list
     (symbol) @name @definition.field))
  (#any-of? @_head "struct" "define-struct"))

((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list
     (list . (symbol) @name) @definition.field))
  (#any-of? @_head "struct" "define-struct"))

((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (symbol)
   .
   (list
     (symbol) @name @definition.field))
  (#any-of? @_head "struct" "define-struct"))

((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (symbol)
   .
   (list
     (list . (symbol) @name) @definition.field))
  (#any-of? @_head "struct" "define-struct"))

((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (symbol) @name @reference.extends)
  (#any-of? @_head "struct" "define-struct"))

; (class object% ...), (class* object% (shape<%>) ...),
; (interface (base<%>) ...)
((list
   .
   (symbol) @_head
   .
   (symbol) @name @reference.extends)
  (#any-of? @_head "class" "class*" "mixin"))

((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list
     (symbol) @name @reference.implements))
  (#eq? @_head "class*"))

((list
   .
   (symbol) @_head
   .
   (list
     (symbol) @name @reference.extends))
  (#eq? @_head "interface"))

; #'f (syntax) names a binding.
(syntax
  (symbol) @name) @reference.value

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
((program
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

((quasiquote
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

((unquote
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

((unquote_splicing
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

((quasisyntax
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

((unsyntax
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

; An argument of a form that is not a binder or a data form.
((list
   .
   (symbol) @_parent
   (list
     .
     (symbol) @name) @reference.call)
  (#not-any-of? @_parent
    "lambda" "λ" "named-lambda" "let" "let*" "letrec" "letrec*"
    "let-values" "let*-values" "let-syntax" "letrec-syntax" "do"
    "parameterize" "fluid-let" "receive" "guard" "quote" "import" "export"
    "define-record-type" "syntax-rules" "case-lambda" "cond" "case" "match"
    "syntax-case" "require" "provide" "struct" "define-struct" "only-in"
    "except-in" "rename-in" "prefix-in" "for-syntax" "for-label"
    "contract-out" "define-values" "match-define" "match" "match*"
    "with-handlers" "class*" "interface" "for" "for/list" "for/vector"
    "for/hash" "for/fold" "for/and" "for/or" "for/first" "for/last"
    "for/sum" "for*" "for*/list" "for*/fold" "let/ec" "class" "mixin"
    "init-field" "init" "field" "inherit" "super-new" "new" "instantiate"
    "make-object" "define-syntax-rule" "_")
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

; The body of a binder: every argument after its bindings or parameters.
((list
   .
   (symbol) @_parent
   .
   (_)
   (list
     .
     (symbol) @name) @reference.call)
  (#any-of? @_parent
    "lambda" "λ" "named-lambda" "let" "let*" "letrec" "letrec*"
    "let-values" "let*-values" "let-syntax" "letrec-syntax" "do"
    "parameterize" "fluid-let" "receive" "guard" "for" "for/list"
    "for/vector" "for/hash" "for/fold" "for/and" "for/or" "for/first"
    "for/last" "for/sum" "for*" "for*/list" "for*/fold" "let/ec"
    "with-handlers" "define-values" "match-define" "match" "match*"
    "define-syntax-rule")
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))

; Clause bodies: (cond ((p x) (f x))).
((list
   .
   (symbol) @_parent
   (list
     (list
       .
       (symbol) @name) @reference.call))
  (#any-of? @_parent
    "cond" "case" "case-lambda" "syntax-rules" "syntax-case" "match"
    "guard" "match*" "match-lambda" "with-handlers")
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "load" "struct"
    "define-struct" "module" "module+" "module*" "require" "provide"
    "class" "class*" "interface" "mixin" "super-new" "match" "match*"
    "match-define" "match-lambda" "for" "for/list" "for/vector" "for/hash"
    "for/fold" "for/and" "for/or" "for/first" "for/last" "for/sum" "for*"
    "for*/list" "for*/fold" "with-handlers" "begin0" "define-values"
    "let/ec" "only-in" "except-in" "rename-in" "prefix-in" "for-syntax"
    "contract-out" "all-defined-out" "all-from-out" "init-field" "init"
    "field" "inherit" "new"))
