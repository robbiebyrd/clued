; Perl: packages, subs, constants, package variables, imports and calls, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). Doc comments are the comments on the lines just above a
; definition, or the POD block standing right before it (matched first,
; so it wins over the same definition without it); signatures are the
; text up to the body (name, prototype, attributes, signature).
;
; The grammar writes a qualified name as one token (Carp::croak,
; My::Other::helper); #strip! cuts it into the bare name and its package.
;
; A `package NAME;` statement qualifies the definitions after it, up to the
; next one (the extractor scopes statement packages); `sub Other::f` is
; Other::f wherever it is written. Known limit: a forward declaration
; (`sub f;`) is parsed by the grammar as a sub whose body runs on to the
; end of the file.

; ---- package and imports

; The file's package is its first `package NAME;` statement; each one
; qualifies the definitions up to the next. A block package is a symbol
; instead (below).
(package_statement
  name: (package) @package
  ";")

; `use Module` and `require Module`; `no Module` imports nothing. Pragmas
; are the lowercase modules (perlmodlib's convention: strict, parent, lib).
(use_statement
  "use"
  module: (package) @import.spec
  (#match? @import.spec "^[a-z]")
  (#set! import.kind "pragma")) @import

(use_statement
  "use"
  module: (package) @import.spec
  (#not-match? @import.spec "^[a-z]")
  (#set! import.kind "use")) @import

; Imported names of a module: a quoted name, or the word of a qw(...)
; holding one. `(_ (string_content))` is any string: '', "" or qw().
(use_statement
  "use"
  module: (package) @import.spec
  (#not-match? @import.spec "^[a-z]")
  (_ (string_content) @import.name)
  (#match? @import.name "^[A-Za-z_:$@%&]+$")) @import

(use_statement
  "use"
  module: (package) @import.spec
  (#not-match? @import.spec "^[a-z]")
  (list_expression (_ (string_content) @import.name))) @import

(require_expression
  (bareword) @import.spec
  (#set! import.kind "require")) @import

(require_expression
  (_ (string_content) @import.spec)
  (#set! import.kind "require")) @import

; ---- packages and classes

((pod) @doc
  .
  (package_statement
    name: (package) @name
    (block)) @definition.package)

(package_statement
  name: (package) @name
  (block)) @definition.package

((pod) @doc
  .
  (class_statement
    name: (package) @name) @definition.class)

(class_statement
  name: (package) @name) @definition.class

; ---- subs and methods

((pod) @doc
  .
  (subroutine_declaration_statement
    name: (bareword) @name
    (#not-match? @name "::")) @definition.function)

(subroutine_declaration_statement
  name: (bareword) @name
  (#not-match? @name "::")) @definition.function

; sub My::Other::helper { ... }: the package is the receiver.
((pod) @doc
  .
  (subroutine_declaration_statement
    name: (bareword) @name @receiver
    (#match? @name "::")
    (#strip! @name "^.*::")
    (#strip! @receiver "::[^:]*$")) @definition.function)

(subroutine_declaration_statement
  name: (bareword) @name @receiver
  (#match? @name "::")
  (#strip! @name "^.*::")
  (#strip! @receiver "::[^:]*$")) @definition.function

((pod) @doc
  .
  (method_declaration_statement
    name: (bareword) @name) @definition.method)

(method_declaration_statement
  name: (bareword) @name) @definition.method

; ---- constants, package variables, fields and Moose attributes

(use_statement
  module: (package) @_constant
  (#eq? @_constant "constant")
  (list_expression . (autoquoted_bareword) @name)) @definition.constant

(use_statement
  module: (package) @_constant
  (#eq? @_constant "constant")
  (anonymous_hash_expression
    (list_expression (autoquoted_bareword) @name))) @definition.constant

(use_statement
  module: (package) @_constant
  (#eq? @_constant "constant")
  (anonymous_hash_expression
    (list_expression
      (list_expression (autoquoted_bareword) @name)))) @definition.constant

(expression_statement
  (variable_declaration
    "our"
    variable: (_ (varname) @name))
  (#not-has-ancestor? @name subroutine_declaration_statement method_declaration_statement anonymous_subroutine_expression)) @definition.variable

(expression_statement
  (variable_declaration
    "our"
    variables: (_ (varname) @name))
  (#not-has-ancestor? @name subroutine_declaration_statement method_declaration_statement anonymous_subroutine_expression)) @definition.variable

(expression_statement
  (assignment_expression
    left: (variable_declaration
      "our"
      variable: (_ (varname) @name)))
  (#not-has-ancestor? @name subroutine_declaration_statement method_declaration_statement anonymous_subroutine_expression)) @definition.variable

(expression_statement
  (assignment_expression
    left: (variable_declaration
      "our"
      variables: (_ (varname) @name)))
  (#not-has-ancestor? @name subroutine_declaration_statement method_declaration_statement anonymous_subroutine_expression)) @definition.variable

(class_statement
  (block
    (expression_statement
      (variable_declaration
        "field"
        variable: (_ (varname) @name))) @definition.field))

(class_statement
  (block
    (expression_statement
      (assignment_expression
        left: (variable_declaration
          "field"
          variable: (_ (varname) @name)))) @definition.field))

; Moose, Moo, Mouse: has 'name' => (...) and has name => (...); the list
; form has [qw(a b)] => (...) is not matched.
(expression_statement
  (list_expression
    (ambiguous_function_call_expression
      function: (function) @form
      arguments: (_ (string_content) @name))
    (#eq? @form "has"))) @definition.form

(expression_statement
  (list_expression
    (ambiguous_function_call_expression
      function: (function) @form
      arguments: (autoquoted_bareword) @name)
    (#eq? @form "has"))) @definition.form

; ---- inheritance

; use parent / use base, @ISA, Moose's extends and with, a class's :isa.
; A qw() of several names is one string to the grammar and is left out.
(use_statement
  module: (package) @_parent
  (#any-of? @_parent "parent" "base")
  (_ (string_content) @name)
  (#match? @name "^[A-Za-z_][A-Za-z0-9_:]*$")) @reference.extends

(use_statement
  module: (package) @_parent
  (#any-of? @_parent "parent" "base")
  (list_expression
    (_ (string_content) @name))) @reference.extends

(assignment_expression
  left: (variable_declaration
    variable: (array (varname) @_isa))
  (#eq? @_isa "ISA")
  right: (_ (string_content) @name)
  (#match? @name "^[A-Za-z_][A-Za-z0-9_:]*$")) @reference.extends

(assignment_expression
  left: (array (varname) @_isa)
  (#eq? @_isa "ISA")
  right: (_ (string_content) @name)
  (#match? @name "^[A-Za-z_][A-Za-z0-9_:]*$")) @reference.extends

(ambiguous_function_call_expression
  function: (function) @_extends
  (#eq? @_extends "extends")
  arguments: (_ (string_content) @name)) @reference.extends

(ambiguous_function_call_expression
  function: (function) @_extends
  (#eq? @_extends "extends")
  arguments: (list_expression
    (_ (string_content) @name))) @reference.extends

(ambiguous_function_call_expression
  function: (function) @_with
  (#eq? @_with "with")
  arguments: (_ (string_content) @name)) @reference.mixin

(ambiguous_function_call_expression
  function: (function) @_with
  (#eq? @_with "with")
  arguments: (list_expression
    (_ (string_content) @name))) @reference.mixin

(class_statement
  attributes: (attrlist
    (attribute
      name: (attribute_name) @_isa
      (#eq? @_isa "isa")
      value: (attribute_value) @name))) @reference.extends

; ---- calls

; Builtins are not references; the list is perlfunc's named operators the
; grammar parses as ordinary calls.
; A `function` node is only ever the callee of a call, written with or
; without parentheses.
(_
  function: (function) @name
  (#not-match? @name "::")
  (#not-any-of? @name
    "abs" "accept" "alarm" "atan2" "bind" "binmode" "bless" "caller"
    "chdir" "chmod" "chomp" "chop" "chown" "chr" "chroot" "close" "closedir"
    "connect" "cos" "crypt" "dbmclose" "dbmopen" "defined" "delete" "die"
    "do" "dump" "each" "eof" "eval" "exec" "exists" "exit" "exp" "fcntl"
    "fileno" "flock" "fork" "format" "formline" "getc" "glob" "gmtime"
    "goto" "grep" "hex" "index" "int" "ioctl" "join" "keys" "kill" "last"
    "lc" "lcfirst" "length" "link" "listen" "local" "localtime" "log"
    "lstat" "map" "mkdir" "next" "oct" "open" "opendir" "ord" "pack" "pipe"
    "pop" "pos" "print" "printf" "prototype" "push" "quotemeta" "rand"
    "read" "readdir" "readline" "readlink" "recv" "redo" "ref" "rename"
    "require" "reset" "return" "reverse" "rewinddir" "rindex" "rmdir" "say"
    "scalar" "seek" "seekdir" "select" "send" "setpgrp" "shift" "sin" "sleep"
    "socket" "sort" "splice" "split" "sprintf" "sqrt" "srand" "stat" "substr"
    "symlink" "syscall" "sysopen" "sysread" "sysseek" "system" "syswrite"
    "tell" "telldir" "tie" "tied" "time" "times" "truncate" "uc" "ucfirst"
    "umask" "undef" "unlink" "unpack" "unshift" "untie" "utime" "values"
    "vec" "wait" "waitpid" "wantarray" "warn" "write"
    "extends" "with" "has")
  (#strip! @name "^&")) @reference.call

; Package::sub(...), &Package::sub: the package is the qualifier.
(_
  function: (function) @name @qualifier
  (#match? @name "::")
  (#strip! @name "^.*::")
  (#strip! @qualifier "^&|::[^:]*$")) @reference.call

; $obj->method, Class->method, $self->SUPER::method.
(method_call_expression
  invocant: (_) @qualifier
  method: (method) @name
  (#strip! @name "^.*::")) @reference.call
