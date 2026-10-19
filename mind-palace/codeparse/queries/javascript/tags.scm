; JavaScript (.js, .mjs, .cjs, .jsx; JSX is part of the grammar):
; definitions, references and imports in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md). It starts from the
; upstream tree-sitter-javascript tags.scm and narrows it: only top-level
; bindings are constants and variables, object-literal members only where
; they are module.exports, and require/import() are imports, not calls.
;
; Doc comments are the comments on the lines just above a definition (the
; extractor's default; JSDoc markers are stripped), signatures its text up
; to the body. A definition inside `export` or assigned to `exports.x` /
; `module.exports.x` carries @exported (profile: scope.exported: declared);
; class members are reached through their class and carry no flag, and a
; name exported later by `export { a }` or `module.exports = { a }` is not
; marked (the resolver reads those forms). There is no package clause: a
; file is its own module (packageBy: file).

; ---- ES imports

(import_statement
  source: (string) @import.spec
  (#set! import.kind "import")) @import

(import_statement
  (import_clause (identifier) @import.alias)
  source: (string) @import.spec
  (#set! import.kind "import")) @import

(import_statement
  (import_clause (namespace_import (identifier) @import.alias))
  source: (string) @import.spec
  (#set! import.kind "import")) @import

; `import {a, b as c, default as d}`: each specifier as written (`b as c`),
; as TypeScript's are; the resolver reads the exported and the local name.
(import_statement
  (import_clause
    (named_imports
      (import_specifier) @import.name))
  source: (string) @import.spec
  (#set! import.kind "import")) @import

; ---- re-exports: export ... from

(export_statement
  source: (string) @import.spec
  (#set! import.kind "export")) @import

(export_statement
  (namespace_export (_) @import.alias)
  source: (string) @import.spec
  (#set! import.kind "export")) @import

(export_statement
  (export_clause
    (export_specifier) @import.name)
  source: (string) @import.spec
  (#set! import.kind "export")) @import

; ---- dynamic import() and CommonJS require

(call_expression
  function: (import)
  arguments: (arguments . (string) @import.spec)
  (#set! import.kind "dynamic")) @import

((call_expression
  function: (identifier) @_require
  arguments: (arguments . (string) @import.spec)) @import
  (#eq? @_require "require")
  (#set! import.kind "require"))

; `const x = require('m')`: x is the module's local name.
(variable_declarator
  name: (identifier) @import.alias
  value: (call_expression
    function: (identifier) @_require
    arguments: (arguments . (string) @import.spec)) @import
  (#eq? @_require "require")
  (#set! import.kind "require"))

; `const {a, b: c} = require('m')`: each property as written (`b: c`).
(variable_declarator
  name: (object_pattern
    [(shorthand_property_identifier_pattern) (pair_pattern)] @import.name)
  value: (call_expression
    function: (identifier) @_require
    arguments: (arguments . (string) @import.spec)) @import
  (#eq? @_require "require")
  (#set! import.kind "require"))

; ---- functions

((function_declaration
  name: (identifier) @name) @definition.function
  (#not-has-parent? @definition.function export_statement))

((generator_function_declaration
  name: (identifier) @name) @definition.function
  (#not-has-parent? @definition.function export_statement))

(export_statement
  declaration: [(function_declaration name: (identifier) @name)
                (generator_function_declaration name: (identifier) @name)] @definition.function) @exported

; A top-level binding to a function: `const f = (x) => ...`.
((_
  (variable_declarator
    name: (identifier) @name
    value: [(arrow_function) (function_expression) (generator_function)]) @definition.function) @_decl
  (#has-parent? @_decl program))

(export_statement
  declaration: (_
    (variable_declarator
      name: (identifier) @name
      value: [(arrow_function) (function_expression) (generator_function)]) @definition.function)) @exported

; ---- classes

((class_declaration
  name: (identifier) @name) @definition.class
  (#not-has-parent? @definition.class export_statement))

(export_statement
  declaration: (class_declaration
    name: (identifier) @name) @definition.class) @exported

((_
  (variable_declarator
    name: (identifier) @name
    value: (class)) @definition.class) @_decl
  (#has-parent? @_decl program))

(export_statement
  declaration: (_
    (variable_declarator
      name: (identifier) @name
      value: (class)) @definition.class)) @exported

; Methods (getters, setters, static, #private, generators and the
; constructor) and fields of a class; a field holding a function is a
; method.
(class_body
  (method_definition
    name: [(property_identifier) (private_property_identifier)
           (computed_property_name)] @name) @definition.method)

(class_body
  (field_definition
    property: [(property_identifier) (private_property_identifier)] @name
    value: [(arrow_function) (function_expression)]) @definition.method)

(class_body
  (field_definition
    property: [(property_identifier) (private_property_identifier)] @name
    !value) @definition.field)

(class_body
  (field_definition
    property: [(property_identifier) (private_property_identifier)] @name
    value: [(identifier) (member_expression) (subscript_expression)
            (call_expression) (new_expression) (number) (string)
            (template_string) (regex) (true) (false) (null) (undefined)
            (object) (array) (binary_expression) (unary_expression)
            (ternary_expression) (parenthesized_expression)
            (await_expression) (this) (class)]) @definition.field)

; Constructor-function methods: `Store.prototype.get = function ...`.
(expression_statement
  (assignment_expression
    left: (member_expression
      object: (member_expression
        object: (identifier) @receiver
        property: (property_identifier) @_prototype)
      property: (property_identifier) @name)
    right: [(arrow_function) (function_expression) (generator_function)]) @definition.method
  (#eq? @_prototype "prototype"))

; ---- CommonJS exports

; `exports.f = function ...`, `module.exports.f = (...) => ...`
(expression_statement
  (assignment_expression
    left: (member_expression
      object: (_) @_exports
      property: (property_identifier) @name)
    right: [(arrow_function) (function_expression) (generator_function)]) @definition.function @exported
  (#any-of? @_exports "exports" "module.exports"))

; `exports.X = <value>`: a constant the module exports.
(expression_statement
  (assignment_expression
    left: (member_expression
      object: (_) @_exports
      property: (property_identifier) @name)
    right: [(number) (string) (template_string) (regex) (true) (false)
            (null) (object) (array) (new_expression)
            (call_expression)]) @definition.constant @exported
  (#any-of? @_exports "exports" "module.exports"))

; `module.exports = { f() {}, g: function () {} }`
(assignment_expression
  left: (member_expression) @_exports
  right: (object
    (method_definition
      name: (property_identifier) @name) @definition.function @exported)
  (#eq? @_exports "module.exports"))

(assignment_expression
  left: (member_expression) @_exports
  right: (object
    (pair
      key: (property_identifier) @name
      value: [(arrow_function) (function_expression)]) @definition.function @exported)
  (#eq? @_exports "module.exports"))

; ---- top-level constants and variables (not locals)

((lexical_declaration
  "const"
  (variable_declarator
    name: (identifier) @name
    value: [(identifier) (member_expression) (subscript_expression)
            (call_expression) (new_expression) (number) (string)
            (template_string) (regex) (true) (false) (null) (undefined)
            (object) (array) (binary_expression) (unary_expression)
            (ternary_expression) (parenthesized_expression)
            (await_expression)] @_value) @definition.constant) @_decl
  (#has-parent? @_decl program)
  (#not-match? @_value "^require\\s*\\("))

(export_statement
  declaration: (lexical_declaration
    "const"
    (variable_declarator
      name: (identifier) @name
      value: [(identifier) (member_expression) (subscript_expression)
              (call_expression) (new_expression) (number) (string)
              (template_string) (regex) (true) (false) (null) (undefined)
              (object) (array) (binary_expression) (unary_expression)
              (ternary_expression) (parenthesized_expression)
              (await_expression)]) @definition.constant)) @exported

((lexical_declaration
  "let"
  (variable_declarator
    name: (identifier) @name
    !value) @definition.variable) @_decl
  (#has-parent? @_decl program))

((lexical_declaration
  "let"
  (variable_declarator
    name: (identifier) @name
    value: [(identifier) (member_expression) (subscript_expression)
            (call_expression) (new_expression) (number) (string)
            (template_string) (regex) (true) (false) (null) (undefined)
            (object) (array) (binary_expression) (unary_expression)
            (ternary_expression) (parenthesized_expression)
            (await_expression)] @_value) @definition.variable) @_decl
  (#has-parent? @_decl program)
  (#not-match? @_value "^require\\s*\\("))

((variable_declaration
  (variable_declarator
    name: (identifier) @name
    !value) @definition.variable) @_decl
  (#has-parent? @_decl program))

((variable_declaration
  (variable_declarator
    name: (identifier) @name
    value: [(identifier) (member_expression) (subscript_expression)
            (call_expression) (new_expression) (number) (string)
            (template_string) (regex) (true) (false) (null) (undefined)
            (object) (array) (binary_expression) (unary_expression)
            (ternary_expression) (parenthesized_expression)
            (await_expression)] @_value) @definition.variable) @_decl
  (#has-parent? @_decl program)
  (#not-match? @_value "^require\\s*\\("))

(export_statement
  declaration: [(lexical_declaration "let"
                  (variable_declarator
                    name: (identifier) @name
                    !value) @definition.variable)
                (variable_declaration
                  (variable_declarator
                    name: (identifier) @name
                    !value) @definition.variable)]) @exported

(export_statement
  declaration: (lexical_declaration
    "let"
    (variable_declarator
      name: (identifier) @name
      value: [(identifier) (member_expression) (subscript_expression)
              (call_expression) (new_expression) (number) (string)
              (template_string) (regex) (true) (false) (null) (undefined)
              (object) (array) (binary_expression) (unary_expression)
              (ternary_expression) (parenthesized_expression)
              (await_expression)]) @definition.variable)) @exported

(export_statement
  declaration: (variable_declaration
    (variable_declarator
      name: (identifier) @name
      value: [(identifier) (member_expression) (subscript_expression)
              (call_expression) (new_expression) (number) (string)
              (template_string) (regex) (true) (false) (null) (undefined)
              (object) (array) (binary_expression) (unary_expression)
              (ternary_expression) (parenthesized_expression)
              (await_expression)]) @definition.variable)) @exported

; ---- references

; Calls of a bare name; require and import() are imports, and the
; built-in conversions are not worth an edge.
((call_expression
  function: (identifier) @name) @reference.call
  (#not-any-of? @name
    "require" "Array" "BigInt" "Boolean" "Number" "Object" "String"
    "Symbol" "parseInt" "parseFloat" "isNaN" "isFinite"))

(call_expression
  function: (member_expression
    object: (_) @qualifier
    property: [(property_identifier) (private_property_identifier)] @name)) @reference.call

; `new C()` and `new ns.C()`: C is a class.
(new_expression
  constructor: (identifier) @name) @reference.class

(new_expression
  constructor: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.class

; `class A extends B`, `extends ns.B`, `extends mixin(B)`.
(class_heritage
  (identifier) @name) @reference.extends

(class_heritage
  (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.extends

; `instanceof C` names a class.
(binary_expression
  operator: "instanceof"
  right: (identifier) @name) @reference.class

; JSX: a capitalized element is a component the file renders; lowercase
; ones are HTML tags.
((jsx_opening_element
  name: (identifier) @name) @reference.call
  (#match? @name "^[A-Z]"))

((jsx_self_closing_element
  name: (identifier) @name) @reference.call
  (#match? @name "^[A-Z]"))

(jsx_opening_element
  name: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.call

(jsx_self_closing_element
  name: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.call
