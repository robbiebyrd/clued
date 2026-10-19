; Scheme: R7RS, R6RS, Guile and Chicken (6cdh/tree-sitter-scheme):
; definitions, references and imports, in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md). A library is a
; module symbol named by its library name, "(foo bar)", and there is no
; @package.
;
; The grammar knows no definitions: (define (f x) ...) is a function,
; (define x ...) a variable, and every other definition a form whose head
; starts with "def", captured as @definition.form and looked up in the
; profile's definingForms (an unknown definer becomes a call reference).
; Docs are the comments just above. gotreesitter mixes up @name across the
; branches of an alternation, so each spelling has a pattern of its own.

; ---- imports

; (import (scheme base) (srfi 1) srfi-13), (use-modules (ice-9 match)),
; Chicken's (use srfi-1) and (require-extension srfi-1)
((list
   .
   (symbol) @_head
   (list . (symbol) @_first) @import.spec @import)
  (#any-of? @_head "import" "use-modules" "use" "require-extension" "require-library")
  (#not-any-of? @_first "only" "except" "prefix" "rename" "for")
  (#set! import.kind "import"))

((list
   .
   (symbol) @_head
   (symbol) @import.spec @import)
  (#any-of? @_head "import" "use-modules" "use" "require-extension" "require-library")
  (#set! import.kind "import"))

; (import (only (srfi 1) fold reduce)), except, rename and for
((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (list) @import.spec) @import)
  (#any-of? @_head "import" "use-modules")
  (#any-of? @_modifier "only" "except" "rename" "for")
  (#set! import.kind "import"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (symbol) @import.spec) @import)
  (#any-of? @_head "import" "use-modules")
  (#any-of? @_modifier "only" "except" "rename" "for")
  (#set! import.kind "import"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (list) @import.spec
     (symbol) @import.name) @import)
  (#any-of? @_head "import" "use-modules")
  (#eq? @_modifier "only"))

((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (symbol) @import.spec
     (symbol) @import.name) @import)
  (#any-of? @_head "import" "use-modules")
  (#eq? @_modifier "only"))

; (import (prefix (foo bar) fb:))
((list
   .
   (symbol) @_head
   (list
     .
     (symbol) @_modifier
     .
     (list) @import.spec
     .
     (symbol) @import.alias) @import)
  (#eq? @_head "import")
  (#eq? @_modifier "prefix")
  (#set! import.kind "import"))

; Guile: (define-module (foo bar) #:use-module (ice-9 match))
((keyword) @_option
  .
  (list) @import.spec @import
  (#eq? @_option "#:use-module")
  (#set! import.kind "import"))

; (include "file.scm"), (load "file.scm")
((list
   .
   (symbol) @_head
   .
   (string) @import.spec) @import
  (#any-of? @_head "include" "include-ci" "load")
  (#set! import.kind "include"))

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

; (define-library (foo bar) ...), (define-module (foo bar) ...),
; (library (foo bar) ...)
((list
   .
   (symbol) @form
   .
   (list) @name) @definition.form
  (#match? @form "^(define-library|define-module|library)$"))

((list
   .
   (symbol) @form
   .
   (symbol) @name) @definition.form
  (#match? @form "^def")
  (#not-eq? @form "define"))

((list
   .
   (symbol) @form
   .
   (list . (symbol) @name)) @definition.form
  (#match? @form "^def")
  (#not-match? @form "^(define|define-library|define-module)$"))

; (define-record-type point (make-point x y) point? (x point-x))
((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list)
   .
   (symbol)
   (list . (symbol) @name) @definition.field)
  (#eq? @_head "define-record-type"))

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
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "use-modules" "define-record-type" "define-module" "syntax-rules"
    "case-lambda" "cond" "case" "match" "syntax-case" "define-library"
    "define-module" "library" "only" "except" "prefix" "rename" "for" "_")
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "parameterize" "fluid-let" "receive" "guard")
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))

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
    "guard")
  (#not-match? @name "^def")
  (#not-any-of? @name
    "lambda" "λ" "case-lambda" "named-lambda" "let" "let*" "letrec"
    "letrec*" "let-values" "let*-values" "let-syntax" "letrec-syntax" "if"
    "cond" "case" "when" "unless" "begin" "do" "set!" "quote" "quasiquote"
    "unquote" "and" "or" "else" "=>" "_" "..." "delay" "delay-force"
    "parameterize" "fluid-let" "receive" "guard" "syntax-rules"
    "syntax-case" "import" "export" "include" "include-ci" "load" "library"
    "use-modules" "use" "require-extension"))
