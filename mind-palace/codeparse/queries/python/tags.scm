; Python: definitions, references and imports, in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md). A file is its own
; module (scope.packageBy: file), so there is no @package. A docstring is
; the string a body starts with, (block . (string)), which this grammar
; does not wrap in an expression_statement; without one, the comments on
; the lines just above are the doc. A decorated definition is its def or
; class (the decorators stay outside it, so the signature is the def line),
; and a decorator is a call reference.

; ---- imports

; import a.b, c as d: one import per module.
(import_statement
  name: (dotted_name) @import.spec
  (#set! import.kind "import")) @import

(import_statement
  name: (aliased_import
    name: (dotted_name) @import.spec
    alias: (identifier) @import.alias)
  (#set! import.kind "import")) @import

; from m import a, b (the names of one statement merge), from . import a,
; from m import *.
(import_from_statement
  module_name: (_) @import.spec
  name: (dotted_name) @import.name
  (#set! import.kind "from")) @import

(import_from_statement
  module_name: (_) @import.spec
  (wildcard_import) @import.name
  (#set! import.kind "from")) @import

; from m import a as b: the alias binds one name, so it is an import of its
; own (at the aliased name).
(import_from_statement
  module_name: (_) @import.spec
  name: (aliased_import
    name: (dotted_name) @import.name
    alias: (identifier) @import.alias) @import
  (#set! import.kind "from"))

(future_import_statement
  "__future__" @import.spec
  name: (dotted_name) @import.name
  (#set! import.kind "from")) @import

; ---- classes

(class_definition
  name: (identifier) @name
  body: (block . (string) @doc)) @definition.class

(class_definition
  name: (identifier) @name) @definition.class

; ---- methods: a def directly in a class body (decorated or not)

(class_definition
  body: (block
    (function_definition
      name: (identifier) @name
      body: (block . (string) @doc)) @definition.method))

(class_definition
  body: (block
    (decorated_definition
      definition: (function_definition
        name: (identifier) @name
        body: (block . (string) @doc)) @definition.method)))

(class_definition
  body: (block
    (function_definition
      name: (identifier) @name) @definition.method))

(class_definition
  body: (block
    (decorated_definition
      definition: (function_definition
        name: (identifier) @name) @definition.method)))

; ---- functions: every other def (module level, nested, conditional)

((function_definition
  name: (identifier) @name
  body: (block . (string) @doc)) @definition.function
  (#not-has-parent? @definition.function block decorated_definition))

((decorated_definition
  definition: (function_definition
    name: (identifier) @name
    body: (block . (string) @doc)) @definition.function) @_decorated
  (#not-has-parent? @_decorated block))

((block
  (function_definition
    name: (identifier) @name
    body: (block . (string) @doc)) @definition.function) @_body
  (#not-has-parent? @_body class_definition))

((block
  (decorated_definition
    definition: (function_definition
      name: (identifier) @name
      body: (block . (string) @doc)) @definition.function)) @_body
  (#not-has-parent? @_body class_definition))

((function_definition
  name: (identifier) @name) @definition.function
  (#not-has-parent? @definition.function block decorated_definition))

((decorated_definition
  definition: (function_definition
    name: (identifier) @name) @definition.function) @_decorated
  (#not-has-parent? @_decorated block))

((block
  (function_definition
    name: (identifier) @name) @definition.function) @_body
  (#not-has-parent? @_body class_definition))

((block
  (decorated_definition
    definition: (function_definition
      name: (identifier) @name) @definition.function)) @_body
  (#not-has-parent? @_body class_definition))

; ---- class attributes: x = 1, x: int (not self.x in methods)

(class_definition
  body: (block
    (assignment
      left: (identifier) @name) @definition.field))

; ---- module variables and constants (UPPER_CASE), also under a
; module-level if or try; never inside a def or class.

((assignment
  left: (identifier) @name) @definition.constant
  (#match? @name "^_*[A-Z][A-Z0-9_]*$")
  (#not-has-parent? @definition.constant assignment)
  (#not-has-ancestor? @definition.constant function_definition class_definition lambda))

((assignment
  left: (identifier) @name) @definition.variable
  (#not-match? @name "^_*[A-Z][A-Z0-9_]*$")
  (#not-has-parent? @definition.variable assignment)
  (#not-has-ancestor? @definition.variable function_definition class_definition lambda))

; A, B = ... and (A, B) = ...: one pattern per form and kind (an
; alternation over nodes with repeated children gives one match for all of
; them here).
((assignment
  left: (pattern_list (identifier) @name)) @definition.constant
  (#match? @name "^_*[A-Z][A-Z0-9_]*$")
  (#not-has-ancestor? @definition.constant function_definition class_definition lambda))

((assignment
  left: (pattern_list (identifier) @name)) @definition.variable
  (#not-match? @name "^_*[A-Z][A-Z0-9_]*$")
  (#not-has-ancestor? @definition.variable function_definition class_definition lambda))

((assignment
  left: (tuple_pattern (identifier) @name)) @definition.constant
  (#match? @name "^_*[A-Z][A-Z0-9_]*$")
  (#not-has-ancestor? @definition.constant function_definition class_definition lambda))

((assignment
  left: (tuple_pattern (identifier) @name)) @definition.variable
  (#not-match? @name "^_*[A-Z][A-Z0-9_]*$")
  (#not-has-ancestor? @definition.variable function_definition class_definition lambda))

; type Alias = ... (Python 3.12)
(type_alias_statement
  left: (type [(identifier) @name
               (generic_type (identifier) @name)])) @definition.type

; ---- references: calls (builtins left out) and decorators

((call
  function: (identifier) @name) @reference.call
  (#not-any-of? @name
    "abs" "aiter" "all" "anext" "any" "ascii" "bin" "bool" "breakpoint"
    "bytearray" "bytes" "callable" "chr" "classmethod" "compile" "complex"
    "delattr" "dict" "dir" "divmod" "enumerate" "eval" "exec" "filter"
    "float" "format" "frozenset" "getattr" "globals" "hasattr" "hash" "help"
    "hex" "id" "input" "int" "isinstance" "issubclass" "iter" "len" "list"
    "locals" "map" "max" "memoryview" "min" "next" "object" "oct" "open"
    "ord" "pow" "print" "property" "range" "repr" "reversed" "round" "set"
    "setattr" "slice" "sorted" "staticmethod" "str" "sum" "super" "tuple"
    "type" "vars" "zip" "__import__" "self" "cls"))

(call
  function: (attribute
    object: (_) @qualifier
    attribute: (identifier) @name)) @reference.call

((decorator
  (identifier) @name) @reference.call
  (#not-any-of? @name "property" "staticmethod" "classmethod"))

(decorator
  (attribute
    object: (_) @qualifier
    attribute: (identifier) @name) @reference.call)

; ---- inheritance: the bases of a class (Base, pkg.Base, Generic[T])

(class_definition
  superclasses: (argument_list
    [(identifier) @name
     (attribute
       object: (_) @qualifier
       attribute: (identifier) @name)
     (subscript
       value: [(identifier) @name
               (attribute
                 object: (_) @qualifier
                 attribute: (identifier) @name)])]) @reference.extends
  (#not-eq? @name "object"))

; ---- types: names in annotations and caught exceptions

((identifier) @name @reference.type
  (#has-ancestor? @name type)
  (#not-has-parent? @name attribute keyword_argument)
  (#not-any-of? @name
    "None" "bool" "bytearray" "bytes" "complex" "dict" "float" "frozenset"
    "int" "list" "object" "set" "str" "tuple" "type"))

((attribute
  object: (_) @qualifier
  attribute: (identifier) @name) @reference.type
  (#has-ancestor? @reference.type type)
  (#not-has-parent? @reference.type attribute))

(except_clause
  value: (identifier) @name) @reference.type

(except_clause
  value: (attribute
    object: (_) @qualifier
    attribute: (identifier) @name)) @reference.type

(except_clause
  value: (as_pattern
    (identifier) @name)) @reference.type

(except_clause
  value: (as_pattern
    (attribute
      object: (_) @qualifier
      attribute: (identifier) @name))) @reference.type

; except (A, b.C) [as e]
((tuple
  (identifier) @name) @reference.type
  (#has-parent? @reference.type except_clause as_pattern))

((tuple
  (attribute
    object: (_) @qualifier
    attribute: (identifier) @name)) @reference.type
  (#has-parent? @reference.type except_clause as_pattern))
