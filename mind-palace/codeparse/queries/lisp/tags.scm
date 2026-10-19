; Common Lisp (theHamsta/tree-sitter-commonlisp): definitions, references,
; imports and the package, in tree-sitter's tags-query convention plus
; clued's captures (see ../README.md). It is also the query of a Lisp file
; without a dialect.
;
; The grammar parses defun, defmacro, defmethod and defgeneric as a `defun`
; node. Every other definition is a form whose head starts with "def",
; captured as @definition.form and looked up in the profile's definingForms
; (an unknown definer becomes a call reference). A doc is the docstring or
; (:documentation "...") when there is one, else the comments just above;
; patterns with a doc come before the same pattern without one, so the
; extractor keeps the first. gotreesitter mixes up @name across the
; branches of an alternation, so each spelling has a pattern of its own.

; ---- package

((list_lit
   .
   (sym_lit) @_head
   .
   (kwd_lit (kwd_symbol) @package))
  (#match? @_head "^(?i)in-package$"))

((list_lit
   .
   (sym_lit) @_head
   .
   (sym_lit) @package)
  (#match? @_head "^(?i)in-package$"))

; ---- imports

; (require :foo), (require 'foo), (require "foo")
((list_lit
   .
   (sym_lit) @_head
   .
   (kwd_lit (kwd_symbol) @import.spec)) @import
  (#match? @_head "^(?i)require$")
  (#set! import.kind "require"))

((list_lit
   .
   (sym_lit) @_head
   .
   (quoting_lit (sym_lit) @import.spec)) @import
  (#match? @_head "^(?i)require$")
  (#set! import.kind "require"))

((list_lit
   .
   (sym_lit) @_head
   .
   (str_lit) @import.spec) @import
  (#match? @_head "^(?i)require$")
  (#set! import.kind "require"))

; (ql:quickload :foo), (asdf:load-system :foo)
((list_lit
   .
   (package_lit symbol: (sym_lit) @_head)
   .
   (kwd_lit (kwd_symbol) @import.spec)) @import
  (#match? @_head "^(?i)(quickload|load-system|require-system)$")
  (#set! import.kind "load"))

((list_lit
   .
   (package_lit symbol: (sym_lit) @_head)
   .
   (quoting_lit (sym_lit) @import.spec)) @import
  (#match? @_head "^(?i)(quickload|load-system|require-system)$")
  (#set! import.kind "load"))

((list_lit
   .
   (package_lit symbol: (sym_lit) @_head)
   .
   (str_lit) @import.spec) @import
  (#match? @_head "^(?i)(quickload|load-system|require-system)$")
  (#set! import.kind "load"))

; defpackage: (:use :cl :alexandria), one import per package
((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   (kwd_lit (kwd_symbol) @import.spec)) @import
  (#eq? @_option "use")
  (#set! import.kind "use"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   (sym_lit) @import.spec) @import
  (#eq? @_option "use")
  (#set! import.kind "use"))

; (:import-from :alexandria #:when-let #:if-let), shadowing too
((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (kwd_lit (kwd_symbol) @import.spec)) @import
  (#any-of? @_option "import-from" "shadowing-import-from")
  (#set! import.kind "import_from"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (sym_lit) @import.spec) @import
  (#any-of? @_option "import-from" "shadowing-import-from")
  (#set! import.kind "import_from"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (kwd_lit (kwd_symbol) @import.spec)
   (kwd_lit (kwd_symbol) @import.name)) @import
  (#any-of? @_option "import-from" "shadowing-import-from"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (kwd_lit (kwd_symbol) @import.spec)
   (sym_lit) @import.name) @import
  (#any-of? @_option "import-from" "shadowing-import-from"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (sym_lit) @import.spec
   (kwd_lit (kwd_symbol) @import.name)) @import
  (#any-of? @_option "import-from" "shadowing-import-from"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (sym_lit) @import.spec
   (sym_lit) @import.name) @import
  (#any-of? @_option "import-from" "shadowing-import-from"))

; (:local-nicknames (#:a #:alexandria))
((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   (list_lit
     .
     (kwd_lit (kwd_symbol) @import.alias)
     .
     (kwd_lit (kwd_symbol) @import.spec)) @import)
  (#eq? @_option "local-nicknames")
  (#set! import.kind "use"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   (list_lit
     .
     (kwd_lit (kwd_symbol) @import.alias)
     .
     (sym_lit) @import.spec) @import)
  (#eq? @_option "local-nicknames")
  (#set! import.kind "use"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   (list_lit
     .
     (sym_lit) @import.alias
     .
     (kwd_lit (kwd_symbol) @import.spec)) @import)
  (#eq? @_option "local-nicknames")
  (#set! import.kind "use"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   (list_lit
     .
     (sym_lit) @import.alias
     .
     (sym_lit) @import.spec) @import)
  (#eq? @_option "local-nicknames")
  (#set! import.kind "use"))

; ASDF: (defsystem "foo" :depends-on ("bar" :baz)), the system's own
; dependencies (a component's :depends-on names other components)
((list_lit
   .
   (sym_lit) @_head
   (kwd_lit (kwd_symbol) @_option)
   .
   (list_lit
     (str_lit) @import.spec @import))
  (#match? @_head "^(?i)defsystem$")
  (#eq? @_option "depends-on")
  (#set! import.kind "depends_on"))

((list_lit
   .
   (sym_lit) @_head
   (kwd_lit (kwd_symbol) @_option)
   .
   (list_lit
     (kwd_lit (kwd_symbol) @import.spec) @import))
  (#match? @_head "^(?i)defsystem$")
  (#eq? @_option "depends-on")
  (#set! import.kind "depends_on"))

((list_lit
   .
   (sym_lit) @_head
   (kwd_lit (kwd_symbol) @_option)
   .
   (list_lit
     (sym_lit) @import.spec @import))
  (#match? @_head "^(?i)defsystem$")
  (#eq? @_option "depends-on")
  (#set! import.kind "depends_on"))

((list_lit
   .
   (package_lit symbol: (sym_lit) @_head)
   (kwd_lit (kwd_symbol) @_option)
   .
   (list_lit
     (str_lit) @import.spec @import))
  (#match? @_head "^(?i)defsystem$")
  (#eq? @_option "depends-on")
  (#set! import.kind "depends_on"))

((list_lit
   .
   (package_lit symbol: (sym_lit) @_head)
   (kwd_lit (kwd_symbol) @_option)
   .
   (list_lit
     (kwd_lit (kwd_symbol) @import.spec) @import))
  (#match? @_head "^(?i)defsystem$")
  (#eq? @_option "depends-on")
  (#set! import.kind "depends_on"))

((list_lit
   .
   (package_lit symbol: (sym_lit) @_head)
   (kwd_lit (kwd_symbol) @_option)
   .
   (list_lit
     (sym_lit) @import.spec @import))
  (#match? @_head "^(?i)defsystem$")
  (#eq? @_option "depends-on")
  (#set! import.kind "depends_on"))

; ---- defun, defmacro, defmethod, defgeneric

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature
     .
     (str_lit) @doc
     .
     (_))) @definition.function
  (#eq? @_kw "defun"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature)) @definition.function
  (#eq? @_kw "defun"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature
     .
     (str_lit) @doc
     .
     (_))) @definition.macro
  (#eq? @_kw "defmacro"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature)) @definition.macro
  (#eq? @_kw "defmacro"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature
     .
     (str_lit) @doc
     .
     (_))) @definition.method
  (#eq? @_kw "defmethod"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature)) @definition.method
  (#eq? @_kw "defmethod"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature
     (list_lit
       .
       (kwd_lit (kwd_symbol) @_option)
       .
       (str_lit) @doc))) @definition.function
  (#eq? @_kw "defgeneric")
  (#eq? @_option "documentation"))

((list_lit
   (defun
     (defun_header
       keyword: (defun_keyword) @_kw
       function_name: (_) @name) @signature)) @definition.function
  (#eq? @_kw "defgeneric"))

; ---- forms whose head starts with "def" (definingForms decides)

; (defvar *x* 1 "Doc.")
((list_lit
   .
   (sym_lit) @form
   .
   (sym_lit) @name
   .
   (_)
   .
   (str_lit) @doc .) @definition.form
  (#match? @form "^(?i)(defvar|defparameter|defconstant)$"))

; (defclass point (base) (...) (:documentation "Doc.")), defpackage too
((list_lit
   .
   (sym_lit) @form
   .
   (sym_lit) @name
   (list_lit
     .
     (kwd_lit (kwd_symbol) @_option)
     .
     (str_lit) @doc)) @definition.form
  (#match? @form "^(?i)def")
  (#eq? @_option "documentation"))

((list_lit
   .
   (sym_lit) @form
   .
   (kwd_lit (kwd_symbol) @name)
   (list_lit
     .
     (kwd_lit (kwd_symbol) @_option)
     .
     (str_lit) @doc)) @definition.form
  (#match? @form "^(?i)def")
  (#eq? @_option "documentation"))

; (defstruct point "Doc." x y), (defstruct (point (:include base)) "Doc." x)
((list_lit
   .
   (sym_lit) @form
   .
   (sym_lit) @name
   .
   (str_lit) @doc) @definition.form
  (#match? @form "^(?i)defstruct$"))

((list_lit
   .
   (sym_lit) @form
   .
   (list_lit . (sym_lit) @name)
   .
   (str_lit) @doc) @definition.form
  (#match? @form "^(?i)defstruct$"))

((list_lit
   .
   (sym_lit) @form
   .
   [(sym_lit) (package_lit)] @name) @definition.form
  (#match? @form "^(?i)def"))

((list_lit
   .
   (sym_lit) @form
   .
   (kwd_lit (kwd_symbol) @name)) @definition.form
  (#match? @form "^(?i)def"))

((list_lit
   .
   (sym_lit) @form
   .
   (list_lit . (sym_lit) @name)) @definition.form
  (#match? @form "^(?i)defstruct$"))

; (asdf:defsystem #:foo ...)
((list_lit
   .
   (package_lit symbol: (sym_lit) @form)
   .
   (kwd_lit (kwd_symbol) @name)) @definition.form
  (#match? @form "^(?i)defsystem$"))

((list_lit
   .
   (package_lit symbol: (sym_lit) @form)
   .
   (sym_lit) @name) @definition.form
  (#match? @form "^(?i)defsystem$"))

; ---- slots

; (defclass point () ((x :initarg :x) y))
((list_lit
   .
   (sym_lit) @_head
   .
   (sym_lit)
   .
   (list_lit)
   .
   (list_lit
     (sym_lit) @name @definition.field))
  (#match? @_head "^(?i)(defclass|define-condition)$"))

((list_lit
   .
   (sym_lit) @_head
   .
   (sym_lit)
   .
   (list_lit)
   .
   (list_lit
     (list_lit . (sym_lit) @name) @definition.field))
  (#match? @_head "^(?i)(defclass|define-condition)$"))

; (defstruct point x (y 0))
((list_lit
   .
   (sym_lit) @_head
   .
   [(sym_lit) (list_lit)]
   (sym_lit) @name @definition.field)
  (#match? @_head "^(?i)defstruct$"))

((list_lit
   .
   (sym_lit) @_head
   .
   [(sym_lit) (list_lit)]
   (list_lit . (sym_lit) @name) @definition.field)
  (#match? @_head "^(?i)defstruct$"))

; ---- inheritance and types

; (defclass point (base mixin) ...), (define-condition my-error (error) ...)
((list_lit
   .
   (sym_lit) @_head
   .
   (sym_lit)
   .
   (list_lit
     (sym_lit) @name @reference.extends))
  (#match? @_head "^(?i)(defclass|define-condition)$"))

((list_lit
   .
   (sym_lit) @_head
   .
   (sym_lit)
   .
   (list_lit
     (package_lit package: (_)? @qualifier symbol: (_) @name) @reference.extends))
  (#match? @_head "^(?i)(defclass|define-condition)$"))

; (defstruct (point (:include base)) ...)
((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (sym_lit) @name @reference.extends)
  (#eq? @_option "include"))

((list_lit
   .
   (kwd_lit (kwd_symbol) @_option)
   .
   (package_lit package: (_)? @qualifier symbol: (_) @name) @reference.extends)
  (#eq? @_option "include"))

; defmethod specializers: (defmethod area ((p point)) ...)
((defun_header
   keyword: (defun_keyword) @_kw
   lambda_list: (list_lit
     (list_lit
       .
       (sym_lit)
       .
       (sym_lit) @name @reference.type)))
  (#eq? @_kw "defmethod")
  (#not-eq? @name "t"))

((defun_header
   keyword: (defun_keyword) @_kw
   lambda_list: (list_lit
     (list_lit
       .
       (sym_lit)
       .
       (package_lit package: (_)? @qualifier symbol: (_) @name) @reference.type)))
  (#eq? @_kw "defmethod")
  (#not-eq? @name "t"))

; (make-instance 'point), (typep x 'point)
((list_lit
   .
   (sym_lit) @_head
   (quoting_lit
     (sym_lit) @name) @reference.type)
  (#any-of? @_head
    "make-instance" "make-condition" "change-class" "typep" "subtypep"
    "find-class"))

((list_lit
   .
   (sym_lit) @_head
   (quoting_lit
     (package_lit package: (_)? @qualifier symbol: (_) @name)) @reference.type)
  (#any-of? @_head
    "make-instance" "make-condition" "change-class" "typep" "subtypep"
    "find-class"))

; #'foo and #'pkg:foo name a function.
(var_quoting_lit
  (sym_lit) @name) @reference.call

(var_quoting_lit
  (package_lit package: (_)? @qualifier symbol: (_) @name)) @reference.call

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
     (sym_lit) @name) @reference.call)
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

((defun
   (list_lit
     .
     (sym_lit) @name) @reference.call)
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

((syn_quoting_lit
   (list_lit
     .
     (sym_lit) @name) @reference.call)
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

((unquoting_lit
   (list_lit
     .
     (sym_lit) @name) @reference.call)
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

; An argument of a form that is not a binder or a data form.
((list_lit
   .
   [(sym_lit) (package_lit)] @_parent
   (list_lit
     .
     (sym_lit) @name) @reference.call)
  (#not-any-of? @_parent
    "quote" "let" "let*" "flet" "labels" "macrolet" "symbol-macrolet"
    "multiple-value-bind" "destructuring-bind" "do" "do*" "dolist"
    "dotimes" "with-slots" "with-accessors" "defclass" "define-condition"
    "defstruct" "defpackage" "define-package" "deftype" "declare" "declaim"
    "in-package" "defsystem" "case" "ecase" "ccase" "typecase" "etypecase"
    "ctypecase" "handler-case" "restart-case" "handler-bind" "when-let"
    "when-let*" "if-let" "if-let*" "with-open-file" "with-open-stream"
    "with-input-from-string" "with-output-to-string" "with-gensyms"
    "with-unique-names")
  (#not-has-ancestor? @name defun_header)
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

; The body of a binder: every argument after its bindings or parameters.
((list_lit
   .
   [(sym_lit) (package_lit)] @_parent
   .
   (_)
   (list_lit
     .
     (sym_lit) @name) @reference.call)
  (#any-of? @_parent
    "let" "let*" "flet" "labels" "macrolet" "symbol-macrolet"
    "multiple-value-bind" "destructuring-bind" "do" "do*" "dolist"
    "dotimes" "with-slots" "with-accessors" "when-let" "when-let*" "if-let"
    "if-let*" "with-open-file" "with-open-stream" "with-input-from-string"
    "with-output-to-string" "with-gensyms" "with-unique-names")
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

; Clause bodies: (cond ((p x) (f x))).
((list_lit
   .
   [(sym_lit) (package_lit)] @_parent
   (list_lit
     (list_lit
       .
       (sym_lit) @name) @reference.call))
  (#any-of? @_parent
    "cond" "case" "ecase" "ccase" "typecase" "etypecase" "ctypecase")
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

((source
   (list_lit
     .
     (package_lit
       package: (_)? @qualifier
       symbol: (_) @name)) @reference.call)
  (#not-match? @name "^(?i)def|^&"))

((defun
   (list_lit
     .
     (package_lit
       package: (_)? @qualifier
       symbol: (_) @name)) @reference.call)
  (#not-match? @name "^(?i)def|^&"))

((syn_quoting_lit
   (list_lit
     .
     (package_lit
       package: (_)? @qualifier
       symbol: (_) @name)) @reference.call)
  (#not-match? @name "^(?i)def|^&"))

((unquoting_lit
   (list_lit
     .
     (package_lit
       package: (_)? @qualifier
       symbol: (_) @name)) @reference.call)
  (#not-match? @name "^(?i)def|^&"))

((list_lit
   .
   [(sym_lit) (package_lit)] @_parent
   (list_lit
     .
     (package_lit
       package: (_)? @qualifier
       symbol: (_) @name)) @reference.call)
  (#not-any-of? @_parent
    "quote" "let" "let*" "flet" "labels" "macrolet" "symbol-macrolet"
    "multiple-value-bind" "destructuring-bind" "do" "do*" "dolist"
    "dotimes" "with-slots" "with-accessors" "defclass" "define-condition"
    "defstruct" "defpackage" "define-package" "deftype" "declare" "declaim"
    "in-package" "defsystem" "case" "ecase" "ccase" "typecase" "etypecase"
    "ctypecase" "handler-case" "restart-case" "handler-bind" "when-let"
    "when-let*" "if-let" "if-let*" "with-open-file" "with-open-stream"
    "with-input-from-string" "with-output-to-string" "with-gensyms"
    "with-unique-names")
  (#not-has-ancestor? @name defun_header)
  (#not-match? @name "^(?i)def|^&"))

((list_lit
   .
   [(sym_lit) (package_lit)] @_parent
   .
   (_)
   (list_lit
     .
     (package_lit
       package: (_)? @qualifier
       symbol: (_) @name)) @reference.call)
  (#any-of? @_parent
    "let" "let*" "flet" "labels" "macrolet" "symbol-macrolet"
    "multiple-value-bind" "destructuring-bind" "do" "do*" "dolist"
    "dotimes" "with-slots" "with-accessors" "when-let" "when-let*" "if-let"
    "if-let*" "with-open-file" "with-open-stream" "with-input-from-string"
    "with-output-to-string" "with-gensyms" "with-unique-names")
  (#not-match? @name "^(?i)def|^&"))

((list_lit
   .
   [(sym_lit) (package_lit)] @_parent
   (list_lit
     (list_lit
       .
       (package_lit
         package: (_)? @qualifier
         symbol: (_) @name)) @reference.call))
  (#any-of? @_parent
    "cond" "case" "ecase" "ccase" "typecase" "etypecase" "ctypecase")
  (#not-match? @name "^(?i)def|^&"))

; handler-case and restart-case: the protected form, and each clause's
; body after its type and lambda list.
((list_lit
   .
   (sym_lit) @_parent
   .
   (list_lit
     .
     (sym_lit) @name) @reference.call)
  (#any-of? @_parent "handler-case" "restart-case")
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

((list_lit
   .
   (sym_lit) @_parent
   .
   (_)
   (list_lit
     .
     (_)
     .
     (_)
     (list_lit
       .
       (sym_lit) @name) @reference.call))
  (#any-of? @_parent "handler-case" "restart-case")
  (#not-match? @name "^(?i)def|^&")
  (#not-any-of? @name
    "quote" "function" "lambda" "let" "let*" "flet" "labels" "macrolet"
    "symbol-macrolet" "if" "when" "unless" "cond" "case" "ecase" "ccase"
    "typecase" "etypecase" "ctypecase" "progn" "prog1" "prog2" "progv"
    "block" "return" "return-from" "tagbody" "go" "catch" "throw"
    "unwind-protect" "setq" "setf" "psetq" "psetf" "and" "or" "declare"
    "declaim" "locally" "the" "eval-when" "load-time-value"
    "multiple-value-bind" "multiple-value-call" "multiple-value-prog1"
    "destructuring-bind" "loop" "do" "do*" "dolist" "dotimes"
    "handler-case" "handler-bind" "restart-case" "ignore-errors"
    "in-package" "require" "otherwise" "t" "nil"))

; The condition type a handler-case clause catches.
((list_lit
   .
   (sym_lit) @_parent
   .
   (_)
   (list_lit
     .
     (sym_lit) @name @reference.type))
  (#eq? @_parent "handler-case")
  (#not-any-of? @name "t" ":no-error"))
