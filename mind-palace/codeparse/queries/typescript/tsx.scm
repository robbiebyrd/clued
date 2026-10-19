; TypeScript, tsx dialect (.tsx): a copy of tags.scm, unchanged, followed by
; the JSX references at the end; keep the two in step.
;
; TypeScript (.ts, .mts, .cts, .d.ts): definitions, references and
; imports in tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). The tsx dialect runs tsx.scm, which is this file followed
; by the JSX references; keep the two in step.
;
; - A TypeScript file is its own module, so there is no @package
;   (profile.yaml: packageBy: file). Namespaces and `declare module` blocks
;   are definitions that qualify what they hold.
; - Docs are the comments on the lines just above a definition (JSDoc
;   included; the extractor's default), signatures its text up to the body.
; - @exported (scope.exported: declared) marks a declaration under
;   `export`, and the public members of classes, interfaces and enums.
; - Where two patterns match one definition, the extractor keeps the match
;   that comes first: the one whose root starts earlier, then the earlier
;   pattern. So the `export` patterns (rooted at export_statement) win over
;   the bare ones, a function-valued variable is matched as a function
;   before it is matched as a constant, and the private members come before
;   the public ones.
; - No alternation [...] holds two branches that capture @name: gotreesitter
;   v0.56.0 can give every match the first branch's @name, so such forms
;   are separate patterns.
; - Import names are the specifiers as written (`a`, `a as b`, `default as
;   c`, `b: c` in a destructured require), so the resolver knows the local
;   name; a default or namespace import's local name is its alias.

; ---- imports, re-exports and require

(import_statement
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "import")) @import

(import_statement
  (import_clause (identifier) @import.alias)
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "import")) @import

(import_statement
  (import_clause (namespace_import (identifier) @import.alias))
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "import")) @import

(import_statement
  (import_clause (named_imports (import_specifier) @import.name))
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "import")) @import

; import x = require("m")
(import_statement
  (import_require_clause
    (identifier) @import.alias
    source: (string (string_fragment) @import.spec))
  (#set! import.kind "require")) @import

; export * from "m", export * as ns from "m", export { a, b as c } from "m"
(export_statement
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "export")) @import

(export_statement
  (namespace_export (identifier) @import.alias)
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "export")) @import

(export_statement
  (export_clause (export_specifier) @import.name)
  source: (string (string_fragment) @import.spec)
  (#set! import.kind "export")) @import

; require("m"), const m = require("m"), const { a, b: c } = require("m")
(call_expression
  function: (identifier) @_require
  arguments: (arguments . (string (string_fragment) @import.spec))
  (#eq? @_require "require")
  (#set! import.kind "require")) @import

(variable_declarator
  name: (identifier) @import.alias
  value: (call_expression
    function: (identifier) @_require
    arguments: (arguments . (string (string_fragment) @import.spec))
    (#eq? @_require "require")
    (#set! import.kind "require")) @import)

(variable_declarator
  name: (object_pattern
    [(shorthand_property_identifier_pattern) (pair_pattern)] @import.name)
  value: (call_expression
    function: (identifier) @_require
    arguments: (arguments . (string (string_fragment) @import.spec))
    (#eq? @_require "require")
    (#set! import.kind "require")) @import)

; import("m")
(call_expression
  function: (import)
  arguments: (arguments . (string (string_fragment) @import.spec))
  (#set! import.kind "dynamic")) @import

; ---- exported declarations (before the bare ones below)

(export_statement
  declaration: (function_declaration name: (identifier) @name) @definition.function) @exported

(export_statement
  declaration: (generator_function_declaration name: (identifier) @name) @definition.function) @exported

(export_statement
  declaration: (function_signature name: (identifier) @name) @definition.function) @exported

(export_statement
  declaration: (class_declaration name: (type_identifier) @name) @definition.class) @exported

(export_statement
  declaration: (abstract_class_declaration name: (type_identifier) @name) @definition.class) @exported

(export_statement
  declaration: (interface_declaration name: (type_identifier) @name) @definition.interface) @exported

(export_statement
  declaration: (type_alias_declaration name: (type_identifier) @name) @definition.type) @exported

(export_statement
  declaration: (enum_declaration name: (identifier) @name) @definition.enum) @exported

(export_statement
  declaration: (internal_module name: [(identifier) (nested_identifier)] @name) @definition.namespace) @exported

(export_statement
  declaration: (module name: [(identifier) (nested_identifier)] @name) @definition.module) @exported

(export_statement
  declaration: (module name: (string (string_fragment) @name)) @definition.module) @exported

; export declare ...

(export_statement
  declaration: (ambient_declaration
    (function_signature name: (identifier) @name) @definition.function)) @exported

(export_statement
  declaration: (ambient_declaration
    (class_declaration name: (type_identifier) @name) @definition.class)) @exported

(export_statement
  declaration: (ambient_declaration
    (abstract_class_declaration name: (type_identifier) @name) @definition.class)) @exported

(export_statement
  declaration: (ambient_declaration
    (interface_declaration name: (type_identifier) @name) @definition.interface)) @exported

(export_statement
  declaration: (ambient_declaration
    (type_alias_declaration name: (type_identifier) @name) @definition.type)) @exported

(export_statement
  declaration: (ambient_declaration
    (enum_declaration name: (identifier) @name) @definition.enum)) @exported

(export_statement
  declaration: (ambient_declaration
    (internal_module name: [(identifier) (nested_identifier)] @name) @definition.namespace)) @exported

(export_statement
  declaration: (ambient_declaration
    (module name: [(identifier) (nested_identifier)] @name) @definition.module)) @exported

(export_statement
  declaration: (ambient_declaration
    (module name: (string (string_fragment) @name)) @definition.module)) @exported

; ---- declarations (anywhere: nested functions and classes are definitions)

(function_declaration name: (identifier) @name) @definition.function

(generator_function_declaration name: (identifier) @name) @definition.function

; Overload signatures and `declare function`.
(function_signature name: (identifier) @name) @definition.function

(class_declaration name: (type_identifier) @name) @definition.class

(abstract_class_declaration name: (type_identifier) @name) @definition.class

(interface_declaration name: (type_identifier) @name) @definition.interface

(type_alias_declaration name: (type_identifier) @name) @definition.type

(enum_declaration name: (identifier) @name) @definition.enum

; namespace A.B { }
(internal_module name: [(identifier) (nested_identifier)] @name) @definition.namespace

; module A { }, declare module "m" { }
(module name: [(identifier) (nested_identifier)] @name) @definition.module

(module
  name: (string (string_fragment) @name)) @definition.module

; ---- module-level variables (not locals): in the file, under export or
; declare, and in a namespace or module body. A variable holding a function
; is a function; otherwise const is a constant, let and var a variable.

(program
  (export_statement
    declaration: (_
      (variable_declarator
        name: (identifier) @name
        value: [(arrow_function) (function_expression) (generator_function)]) @definition.function)) @exported)

(program
  (export_statement
    declaration: (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
    (#match? @_decl "^const\\b")) @exported)

(program
  (export_statement
    declaration: (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
    (#match? @_decl "^(let|var)\\b")) @exported)

(program
  (export_statement
    declaration: (ambient_declaration
      (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
      (#match? @_decl "^const\\b"))) @exported)

(program
  (export_statement
    declaration: (ambient_declaration
      (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
      (#match? @_decl "^(let|var)\\b"))) @exported)

(program
  (_
    (variable_declarator
      name: (identifier) @name
      value: [(arrow_function) (function_expression) (generator_function)]) @definition.function))

(program
  (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
  (#match? @_decl "^const\\b"))

(program
  (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
  (#match? @_decl "^(let|var)\\b"))

(program
  (ambient_declaration
    (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
    (#match? @_decl "^const\\b")))

(program
  (ambient_declaration
    (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
    (#match? @_decl "^(let|var)\\b")))

; In a namespace body.
(internal_module
  body: (statement_block
    (export_statement
      declaration: (_
        (variable_declarator
          name: (identifier) @name
          value: [(arrow_function) (function_expression) (generator_function)]) @definition.function)) @exported))

(internal_module
  body: (statement_block
    (export_statement
      declaration: (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
      (#match? @_decl "^const\\b")) @exported))

(internal_module
  body: (statement_block
    (export_statement
      declaration: (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
      (#match? @_decl "^(let|var)\\b")) @exported))

(internal_module
  body: (statement_block
    (_
      (variable_declarator
        name: (identifier) @name
        value: [(arrow_function) (function_expression) (generator_function)]) @definition.function)))

(internal_module
  body: (statement_block
    (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
    (#match? @_decl "^const\\b")))

(internal_module
  body: (statement_block
    (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
    (#match? @_decl "^(let|var)\\b")))

; In a module body.
(module
  body: (statement_block
    (export_statement
      declaration: (_
        (variable_declarator
          name: (identifier) @name
          value: [(arrow_function) (function_expression) (generator_function)]) @definition.function)) @exported))

(module
  body: (statement_block
    (export_statement
      declaration: (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
      (#match? @_decl "^const\\b")) @exported))

(module
  body: (statement_block
    (export_statement
      declaration: (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
      (#match? @_decl "^(let|var)\\b")) @exported))

(module
  body: (statement_block
    (_
      (variable_declarator
        name: (identifier) @name
        value: [(arrow_function) (function_expression) (generator_function)]) @definition.function)))

(module
  body: (statement_block
    (_ (variable_declarator name: (identifier) @name) @definition.constant) @_decl
    (#match? @_decl "^const\\b")))

(module
  body: (statement_block
    (_ (variable_declarator name: (identifier) @name) @definition.variable) @_decl
    (#match? @_decl "^(let|var)\\b")))

; ---- class members: private and protected ones first, then the public
; (exported) ones

(class_body
  (method_definition
    (accessibility_modifier) @_access
    name: (_) @name
    (#any-of? @_access "private" "protected")) @definition.method)

(class_body
  (method_signature
    (accessibility_modifier) @_access
    name: (_) @name
    (#any-of? @_access "private" "protected")) @definition.method)

(class_body
  (abstract_method_signature
    (accessibility_modifier) @_access
    name: (_) @name
    (#any-of? @_access "private" "protected")) @definition.method)

(class_body
  (method_definition
    name: (private_property_identifier) @name) @definition.method)

(class_body
  (method_signature
    name: (private_property_identifier) @name) @definition.method)

(class_body
  (public_field_definition
    (accessibility_modifier) @_access
    name: (_) @name
    (#any-of? @_access "private" "protected")) @definition.field)

(class_body
  (public_field_definition
    name: (private_property_identifier) @name) @definition.field)

(class_body
  (method_definition
    name: (property_identifier) @name) @definition.method @exported)

(class_body
  (method_signature
    name: (property_identifier) @name) @definition.method @exported)

(class_body
  (abstract_method_signature
    name: (property_identifier) @name) @definition.method @exported)

(class_body
  (public_field_definition
    name: (property_identifier) @name) @definition.field @exported)

; ---- interface and object-type members, enum members (all public)

(interface_body
  (method_signature name: (property_identifier) @name) @definition.method) @exported

(interface_body
  (property_signature name: (property_identifier) @name) @definition.field) @exported

(type_alias_declaration
  value: (object_type
    (method_signature name: (property_identifier) @name) @definition.method)) @exported

(type_alias_declaration
  value: (object_type
    (property_signature name: (property_identifier) @name) @definition.field)) @exported

(enum_body
  (property_identifier) @name @definition.constant) @exported

(enum_body
  (enum_assignment name: (property_identifier) @name) @definition.constant) @exported

; ---- references

(call_expression
  function: (identifier) @name
  (#not-eq? @name "require")) @reference.call

(call_expression
  function: (member_expression
    object: (_) @qualifier
    property: [(property_identifier) (private_property_identifier)] @name)) @reference.call

; A bare decorator (@Input); a decorator call (@Component(...)) is a call.
(decorator
  (identifier) @name) @reference.call

(decorator
  (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.call

(new_expression
  constructor: (identifier) @name) @reference.class

(new_expression
  constructor: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.class

(extends_clause
  value: (identifier) @name) @reference.extends

(extends_clause
  value: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.extends

(extends_type_clause
  type: (type_identifier) @name) @reference.extends

(extends_type_clause
  type: (nested_type_identifier
    module: (_) @qualifier
    name: (type_identifier) @name)) @reference.extends

(extends_type_clause
  type: (generic_type
    name: (type_identifier) @name)) @reference.extends

(extends_type_clause
  type: (generic_type
    name: (nested_type_identifier
      module: (_) @qualifier
      name: (type_identifier) @name))) @reference.extends

(implements_clause
  (type_identifier) @name) @reference.implements

(implements_clause
  (nested_type_identifier
    module: (_) @qualifier
    name: (type_identifier) @name)) @reference.implements

(implements_clause
  (generic_type
    name: (type_identifier) @name)) @reference.implements

(implements_clause
  (generic_type
    name: (nested_type_identifier
      module: (_) @qualifier
      name: (type_identifier) @name))) @reference.implements

(nested_type_identifier
  module: (_) @qualifier
  name: (type_identifier) @name) @reference.type

((type_identifier) @name @reference.type)

; ---- JSX (tsx dialect only): a component used as an element is a call; a
; lowercase name is an intrinsic element (div, button) and no reference.

(jsx_opening_element
  name: (identifier) @name
  (#match? @name "^[A-Z]")) @reference.call

(jsx_self_closing_element
  name: (identifier) @name
  (#match? @name "^[A-Z]")) @reference.call

(jsx_opening_element
  name: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.call

(jsx_self_closing_element
  name: (member_expression
    object: (_) @qualifier
    property: (property_identifier) @name)) @reference.call
