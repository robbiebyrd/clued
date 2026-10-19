; Go: definitions, references, imports and the package clause, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). Doc comments are the comments on the lines just above a
; definition (the extractor's default), signatures its text up to the body.

; ---- package and imports

(package_clause (package_identifier) @package)

(import_spec
  name: (_)? @import.alias
  path: (_) @import.spec) @import

; ---- functions and methods

(function_declaration
  name: (identifier) @name) @definition.function

(method_declaration
  receiver: (parameter_list
    (parameter_declaration
      type: [(type_identifier) @receiver
             (pointer_type (type_identifier) @receiver)
             (generic_type type: (type_identifier) @receiver)
             (pointer_type (generic_type type: (type_identifier) @receiver))]))
  name: (field_identifier) @name) @definition.method

; ---- types

(type_spec
  name: (type_identifier) @name
  type: (struct_type)) @definition.struct

(type_spec
  name: (type_identifier) @name
  type: (interface_type)) @definition.interface

(type_spec
  name: (type_identifier) @name
  type: [(type_identifier) (qualified_type) (generic_type) (pointer_type)
         (slice_type) (array_type) (map_type) (channel_type)
         (function_type) (parenthesized_type)]) @definition.type

(type_alias
  name: (type_identifier) @name) @definition.type

; Struct fields, named and embedded.
(field_declaration_list
  (field_declaration
    name: (field_identifier) @name) @definition.field)

(field_declaration_list
  (field_declaration
    !name
    type: [(type_identifier) @name
           (pointer_type (type_identifier) @name)
           (qualified_type name: (type_identifier) @name)
           (pointer_type (qualified_type name: (type_identifier) @name))
           (generic_type type: (type_identifier) @name)]) @definition.field)

; Interface methods.
(interface_type
  (method_elem
    name: (field_identifier) @name) @definition.method)

; ---- package-level constants and variables (not locals)

(source_file
  (const_declaration
    (const_spec
      name: (identifier) @name
      (#not-eq? @name "_")) @definition.constant))

(source_file
  (var_declaration
    (var_spec
      name: (identifier) @name
      (#not-eq? @name "_")) @definition.variable))

(source_file
  (var_declaration
    (var_spec_list
      (var_spec
        name: (identifier) @name
        (#not-eq? @name "_")) @definition.variable)))

; ---- references

(call_expression
  function: (identifier) @name
  (#not-any-of? @name
    "append" "cap" "clear" "close" "complex" "copy" "delete" "imag" "len"
    "make" "max" "min" "new" "panic" "print" "println" "real" "recover"
    "bool" "byte" "complex64" "complex128" "error" "float32" "float64"
    "int" "int8" "int16" "int32" "int64" "rune" "string" "uint" "uint8"
    "uint16" "uint32" "uint64" "uintptr")) @reference.call

(call_expression
  function: (selector_expression
    operand: (_) @qualifier
    field: (field_identifier) @name)) @reference.call

(qualified_type
  package: (package_identifier) @qualifier
  name: (type_identifier) @name) @reference.type

((type_identifier) @name @reference.type
  (#not-any-of? @name
    "any" "bool" "byte" "comparable" "complex64" "complex128" "error"
    "float32" "float64" "int" "int8" "int16" "int32" "int64" "rune"
    "string" "uint" "uint8" "uint16" "uint32" "uint64" "uintptr"))
