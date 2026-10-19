; Emacs Lisp (Wilfred/tree-sitter-elisp): definitions, references and
; imports, in tree-sitter's tags-query convention plus clued's captures
; (see ../README.md). Emacs Lisp has one global namespace, so there is no
; @package.
;
; The grammar parses defun and defsubst as a `function_definition`, defmacro
; as a `macro_definition`, and defvar, defconst and the other special forms
; as a `special_form` whose head is anonymous (matched on its text). Every
; other definition is a form whose head starts with "def", "cl-def" or
; "define-", captured as @definition.form and looked up in the profile's
; definingForms (an unknown definer becomes a call reference). Patterns with
; a doc come before the same pattern without one, so the extractor keeps
; the first. gotreesitter mixes up @name across the branches of an
; alternation, so each spelling has a pattern of its own.

; ---- imports

; (require 'foo)
((list
   .
   (symbol) @_head
   .
   (quote (symbol) @import.spec)) @import
  (#eq? @_head "require")
  (#set! import.kind "require"))

; (use-package magit ...)
((list
   .
   (symbol) @_head
   .
   (symbol) @import.spec) @import
  (#eq? @_head "use-package")
  (#set! import.kind "use_package"))

; (autoload 'magit-status "magit")
((list
   .
   (symbol) @_head
   .
   (quote (symbol) @import.name)
   .
   (string) @import.spec) @import
  (#eq? @_head "autoload")
  (#set! import.kind "autoload"))

; ---- defun, defsubst, defmacro, defvar, defconst

(function_definition
  name: (symbol) @name
  docstring: (string) @doc) @definition.function

(function_definition
  name: (symbol) @name) @definition.function

(macro_definition
  name: (symbol) @name
  docstring: (string) @doc) @definition.macro

(macro_definition
  name: (symbol) @name) @definition.macro

((special_form
   .
   (symbol) @name
   .
   (_)
   .
   (string) @doc) @definition.variable @_form
  (#match? @_form "^\\(defvar\\s"))

((special_form
   .
   (symbol) @name) @definition.variable @_form
  (#match? @_form "^\\(defvar\\s"))

((special_form
   .
   (symbol) @name
   .
   (_)
   .
   (string) @doc) @definition.constant @_form
  (#match? @_form "^\\(defconst\\s"))

((special_form
   .
   (symbol) @name) @definition.constant @_form
  (#match? @_form "^\\(defconst\\s"))

; ---- forms whose head is a definer (definingForms decides)

; (cl-defun f (x &key y) "Doc." body), (cl-defgeneric area (s) "Doc.")
((list
   .
   (symbol) @form
   .
   (symbol) @name
   .
   (list)
   .
   (string) @doc) @definition.form
  (#match? @form "^(cl-defun|cl-defmacro|cl-defsubst|cl-defgeneric|define-inline)$"))

; (defcustom foo t "Doc." :type 'boolean), defface, defvar-local, defgroup
((list
   .
   (symbol) @form
   .
   (symbol) @name
   .
   (_)
   .
   (string) @doc) @definition.form
  (#match? @form "^(defcustom|defface|defvar-local|defgroup)$"))

; (define-minor-mode foo-mode "Doc." ...)
((list
   .
   (symbol) @form
   .
   (symbol) @name
   .
   (string) @doc) @definition.form
  (#match? @form "^(define-minor-mode|define-globalized-minor-mode|cl-defstruct|defclass)$"))

; (define-derived-mode foo-mode prog-mode "Foo" "Doc.")
((list
   .
   (symbol) @form
   .
   (symbol) @name
   .
   (symbol)
   .
   (string)
   .
   (string) @doc) @definition.form
  (#eq? @form "define-derived-mode"))

; (defclass shape (base) (slots) "Doc."), EIEIO
((list
   .
   (symbol) @form
   .
   (symbol) @name
   .
   (list)
   .
   (list)
   .
   (string) @doc) @definition.form
  (#eq? @form "defclass"))

((list
   .
   (symbol) @form
   .
   (symbol) @name) @definition.form
  (#match? @form "^(cl-)?def|^define-"))

; (defalias 'foo #'bar), (define-error 'foo-error "Message")
((list
   .
   (symbol) @form
   .
   (quote (symbol) @name)) @definition.form
  (#match? @form "^(cl-)?def|^define-"))

; (cl-defstruct (point (:include base)) "Doc." x y)
((list
   .
   (symbol) @form
   .
   (list . (symbol) @name)
   .
   (string) @doc) @definition.form
  (#eq? @form "cl-defstruct"))

((list
   .
   (symbol) @form
   .
   (list . (symbol) @name)) @definition.form
  (#eq? @form "cl-defstruct"))

; ---- slots

; (cl-defstruct point "Doc." x (y 0))
((list
   .
   (symbol) @_head
   .
   [(symbol) (list)]
   (symbol) @name @definition.field)
  (#eq? @_head "cl-defstruct"))

((list
   .
   (symbol) @_head
   .
   [(symbol) (list)]
   (list . (symbol) @name) @definition.field)
  (#eq? @_head "cl-defstruct"))

; (defclass point (base) ((x :initarg :x)))
((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list)
   .
   (list
     (list . (symbol) @name) @definition.field))
  (#eq? @_head "defclass"))

; ---- inheritance and types

; (defclass point (base mixin) ...)
((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list
     (symbol) @name @reference.extends))
  (#eq? @_head "defclass"))

; (cl-defstruct (point (:include base)) ...)
((list
   .
   (symbol) @_option
   .
   (symbol) @name @reference.extends)
  (#eq? @_option ":include"))

; (define-derived-mode foo-mode prog-mode ...)
((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (symbol) @name @reference.extends)
  (#eq? @_head "define-derived-mode"))

; (define-error 'foo-error "Message" 'parent-error)
((list
   .
   (symbol) @_head
   .
   (quote)
   .
   (string)
   .
   (quote (symbol) @name) @reference.extends)
  (#eq? @_head "define-error"))

; cl-defmethod specializers: (cl-defmethod area ((s shape)) ...)
((list
   .
   (symbol) @_head
   .
   (symbol)
   .
   (list
     (list
       .
       (symbol)
       .
       (symbol) @name @reference.type)))
  (#eq? @_head "cl-defmethod")
  (#not-eq? @name "t"))

; #'foo and (function foo) name a function.
((quote (symbol) @name) @reference.call @_quote
  (#match? @_quote "^#'"))

((special_form
   .
   (symbol) @name) @reference.call @_form
  (#match? @_form "^\\(function\\s"))

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
((source_file
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

((function_definition
   parameters: (_)
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

((macro_definition
   parameters: (_)
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

((special_form
   .
   (_)
   (list
     .
     (symbol) @name) @reference.call)
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

; An argument of a form that is not a binder or a data form.
((list
   .
   (symbol) @_parent
   (list
     .
     (symbol) @name) @reference.call)
  (#not-any-of? @_parent
    "dolist" "dotimes" "cl-flet" "cl-labels" "cl-letf" "cl-letf*"
    "pcase-let" "pcase-let*" "pcase-dolist" "when-let" "when-let*" "if-let"
    "if-let*" "and-let*" "seq-let" "cl-destructuring-bind"
    "cl-multiple-value-bind" "let-alist" "with-slots" "quote" "defclass"
    "cl-defstruct" "cl-defun" "cl-defmacro" "cl-defsubst" "cl-defgeneric"
    "cl-defmethod" "define-inline" "pcase" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "require" "provide" "autoload" "declare"
    "interactive")
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

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
    "dolist" "dotimes" "cl-flet" "cl-labels" "cl-letf" "cl-letf*"
    "pcase-let" "pcase-let*" "pcase-dolist" "when-let" "when-let*" "if-let"
    "if-let*" "and-let*" "seq-let" "cl-destructuring-bind"
    "cl-multiple-value-bind" "let-alist" "with-slots")
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

; The body of a definer: every argument after its name and parameters.
((list
   .
   (symbol) @_parent
   .
   (_)
   .
   (_)
   (list
     .
     (symbol) @name) @reference.call)
  (#any-of? @_parent
    "cl-defun" "cl-defmacro" "cl-defsubst" "cl-defgeneric" "cl-defmethod"
    "define-inline")
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

; Clause bodies: (cond ((p x) (f x))).
((list
   .
   (symbol) @_parent
   (list
     (list
       .
       (symbol) @name) @reference.call))
  (#any-of? @_parent
    "pcase" "pcase-exhaustive" "cl-case" "cl-ecase" "cl-typecase")
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

; The first argument of a special form that evaluates it (if, and, or,
;; progn, while, ...): the context above skips the first argument, which
;; is a parameter list in lambda and the bindings in let.
((special_form
   .
   (list
     .
     (symbol) @name) @reference.call) @_form
  (#match? @_form "^\\((if|and|or|progn|prog1|prog2|while|setq|setq-default|unwind-protect|save-excursion|save-restriction|save-current-buffer|catch|condition-case)[\\s)]")
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))

; cond clauses: (cond ((p x) (f x)))
((special_form
   (list
     (list
       .
       (symbol) @name) @reference.call)) @_form
  (#match? @_form "^\\(cond\\s")
  (#not-match? @name "^(cl-)?def|^define-|^[:&]")
  (#not-any-of? @name
    "lambda" "quote" "function" "when" "unless" "dolist" "dotimes" "pcase"
    "pcase-let" "pcase-let*" "pcase-dolist" "pcase-exhaustive" "cl-case"
    "cl-ecase" "cl-typecase" "cl-loop" "cl-flet" "cl-labels" "cl-letf"
    "cl-block" "cl-return" "cl-return-from" "cl-destructuring-bind"
    "cl-multiple-value-bind" "when-let" "when-let*" "if-let" "if-let*"
    "and-let*" "seq-let" "let-alist" "push" "pop" "setf" "cl-incf"
    "cl-decf" "cl-pushnew" "declare" "require" "provide"
    "eval-when-compile" "eval-and-compile" "ignore-errors"
    "with-eval-after-load" "use-package" "autoload" "t" "nil"))
