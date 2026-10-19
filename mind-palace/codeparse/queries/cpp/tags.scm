; C++: definitions, references, includes and using directives, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). There is no package clause: namespaces are definitions,
; so what they contain is qualified by position (a::b::f). Doc comments
; are the comments on the lines just above a definition (the extractor's
; default), signatures its text up to the body.
;
; Where two patterns match one definition the extractor keeps the first
; match, and the cursor yields a pattern rooted at a parent before one
; rooted at the definition itself, then patterns in file order: the
; specific forms (a method in its class, a member template, a template's
; doc, a constant) come first and rest on that.

; ---- includes, using directives and namespace aliases

(preproc_include
  path: (system_lib_string) @import.spec
  (#set! import.kind "include_system")) @import

(preproc_include
  path: (string_literal) @import.spec
  (#set! import.kind "include")) @import

(using_declaration
  "namespace"
  [(identifier) (qualified_identifier) (nested_namespace_specifier)] @import.spec
  (#set! import.kind "using_namespace")) @import

(using_declaration
  !name
  (qualified_identifier
    name: (_) @import.name) @import.spec
  (#set! import.kind "using")) @import

(namespace_alias_definition
  name: (namespace_identifier) @import.alias
  [(namespace_identifier) (nested_namespace_specifier)] @import.spec
  (#set! import.kind "namespace_alias")) @import

; ---- methods declared or defined in their class

(field_declaration_list
  [(field_declaration
     declarator: [(function_declarator declarator: [(field_identifier) (operator_name) (destructor_name)] @name)
                  (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                  (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])
   (declaration
     declarator: (function_declarator declarator: [(identifier) (destructor_name) (operator_name)] @name))
   (function_definition
     declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name) (destructor_name)] @name)
                  (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                  (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])] @definition.method)

; Member templates: the templated member is the definition, its doc the
; comment just above the template line.

(field_declaration_list
  (comment) @doc
  .
  (template_declaration
    [(function_definition
       declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])
     (declaration
       declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])
     (field_declaration
       declarator: [(function_declarator declarator: [(field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])] @definition.method))

(field_declaration_list
  (template_declaration
    [(function_definition
       declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])
     (declaration
       declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])
     (field_declaration
       declarator: [(function_declarator declarator: [(field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(field_identifier) (operator_name)] @name))])] @definition.method))

; ---- templates: the definition is the templated entity, its doc the
; comment just above the template line

((comment) @doc
  .
  (template_declaration
    [(class_specifier name: [(type_identifier) @name (template_type name: (type_identifier) @name)] body: (_)) @definition.class
     (struct_specifier name: [(type_identifier) @name (template_type name: (type_identifier) @name)] body: (_)) @definition.struct
     (union_specifier name: (type_identifier) @name body: (_)) @definition.union
     (alias_declaration name: (type_identifier) @name) @definition.type
     (function_definition
       declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name))]) @definition.function
     (declaration
       declarator: [(function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name)
                    (pointer_declarator declarator: (function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name))
                    (reference_declarator (function_declarator declarator: [(identifier) (field_identifier) (operator_name)] @name))]) @definition.function]))

; ---- fields

(field_declaration_list
  (field_declaration
    declarator: [(field_identifier) @name
                 (pointer_declarator declarator: (field_identifier) @name)
                 (pointer_declarator declarator: (pointer_declarator declarator: (field_identifier) @name))
                 (reference_declarator (field_identifier) @name)
                 (array_declarator declarator: (field_identifier) @name)]) @definition.field)

; ---- namespace and file scope constants and variables (not locals): one
; pattern per scope that holds declarations, a constant before a variable

(translation_unit
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(declaration_list
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(linkage_specification
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(preproc_if
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(preproc_ifdef
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(preproc_else
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(preproc_elif
  (declaration
    (type_qualifier) @_const
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]
    (#any-of? @_const "const" "constexpr" "constinit")) @definition.constant)

(translation_unit
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

(declaration_list
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

(linkage_specification
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

(preproc_if
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

(preproc_ifdef
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

(preproc_else
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

(preproc_elif
  (declaration
    declarator: [(identifier) @name
                 (init_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (init_declarator declarator: (array_declarator declarator: (identifier) @name))]) @definition.variable)

; ---- functions, and methods defined outside their class (A::f, also
; ns::A::f and A<T>::f): the receiver is the innermost scope. Without
; semantic analysis ns::f, a free function defined through its namespace,
; reads the same as a method and is one, with receiver ns.

(function_definition
  declarator: [(function_declarator declarator: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (destructor_name) (operator_name)] @name))
               (pointer_declarator declarator: (function_declarator declarator: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name)))
               (reference_declarator (function_declarator declarator: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name)))]) @definition.method

(function_definition
  declarator: [(function_declarator declarator: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (destructor_name) (operator_name)] @name)))
               (pointer_declarator declarator: (function_declarator declarator: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name))))
               (reference_declarator (function_declarator declarator: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name))))]) @definition.method

(function_definition
  declarator: [(function_declarator declarator: (qualified_identifier name: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (destructor_name) (operator_name)] @name))))
               (pointer_declarator declarator: (function_declarator declarator: (qualified_identifier name: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name)))))
               (reference_declarator (function_declarator declarator: (qualified_identifier name: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name)))))]) @definition.method

(function_definition
  declarator: [(function_declarator declarator: (qualified_identifier name: (qualified_identifier name: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (destructor_name) (operator_name)] @name)))))
               (pointer_declarator declarator: (function_declarator declarator: (qualified_identifier name: (qualified_identifier name: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name))))))
               (reference_declarator (function_declarator declarator: (qualified_identifier name: (qualified_identifier name: (qualified_identifier name: (qualified_identifier scope: [(namespace_identifier) @receiver (template_type name: (type_identifier) @receiver)] name: [(identifier) (operator_name)] @name))))))]) @definition.method

(function_definition
  declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
               (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
               (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name)))
               (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function

; Prototypes at namespace or file scope.

(translation_unit
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(declaration_list
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(linkage_specification
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(preproc_if
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(preproc_ifdef
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(preproc_else
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(preproc_elif
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

(template_declaration
  (declaration
    declarator: [(function_declarator declarator: [(identifier) (operator_name)] @name)
                 (pointer_declarator declarator: (function_declarator declarator: [(identifier) (operator_name)] @name))
                 (reference_declarator (function_declarator declarator: [(identifier) (operator_name)] @name))]) @definition.function)

; ---- namespaces and types (a forward declaration defines nothing)

(namespace_definition
  name: [(namespace_identifier) (nested_namespace_specifier)] @name
  body: (_)) @definition.namespace

(class_specifier
  name: (type_identifier) @name
  body: (_)) @definition.class

(struct_specifier
  name: (type_identifier) @name
  body: (_)) @definition.struct

(union_specifier
  name: (type_identifier) @name
  body: (_)) @definition.union

; Explicit and partial specializations (template <> struct Hash<int>)
; define the template's name again.

(class_specifier
  name: (template_type name: (type_identifier) @name)
  body: (_)) @definition.class

(struct_specifier
  name: (template_type name: (type_identifier) @name)
  body: (_)) @definition.struct

(enum_specifier
  name: (type_identifier) @name
  body: (_)) @definition.enum

(enumerator_list
  (enumerator
    name: (identifier) @name) @definition.constant)

(type_definition
  declarator: [(type_identifier) @name
               (pointer_declarator declarator: (type_identifier) @name)
               (function_declarator declarator: (parenthesized_declarator (pointer_declarator declarator: (type_identifier) @name)))]) @definition.type

(alias_declaration
  name: (type_identifier) @name) @definition.type

; ---- macros (an include guard, with no value, defines nothing)

(preproc_def
  name: (identifier) @name
  value: (_)) @definition.macro

(preproc_function_def
  name: (identifier) @name) @definition.macro

; ---- references: calls

(call_expression
  function: (identifier) @name) @reference.call

(call_expression
  function: (qualified_identifier
    scope: (_) @qualifier
    name: [(identifier) @name
           (template_function name: (identifier) @name)])) @reference.call

(call_expression
  function: (field_expression
    argument: (_) @qualifier
    field: [(field_identifier) @name
            (template_method name: (field_identifier) @name)])) @reference.call

(call_expression
  function: (template_function
    name: (identifier) @name
    (#not-any-of? @name "static_cast" "dynamic_cast" "const_cast" "reinterpret_cast"))) @reference.call

; ---- references: inheritance and types

(base_class_clause
  [(type_identifier) @name
   (qualified_identifier scope: (_) @qualifier name: (type_identifier) @name)
   (template_type name: (type_identifier) @name)
   (qualified_identifier scope: (_) @qualifier name: (template_type name: (type_identifier) @name))] @reference.extends)

(qualified_identifier
  scope: (_) @qualifier
  name: [(type_identifier) @name
         (template_type name: (type_identifier) @name)]) @reference.type

((type_identifier) @name @reference.type)
