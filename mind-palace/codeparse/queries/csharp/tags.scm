; C#: definitions, references, `using` directives and the namespace, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). Doc comments (`///`) are the comments on the lines just
; above a definition, attributes included (the extractor's default);
; signatures its text up to the body, or its first line.
;
; A definition is exported when it carries a public or protected modifier,
; or is a member of an interface or an enum. Each such definition has two
; patterns, the one with @exported first: the extractor keeps the first
; match of a definition matched twice.

; ---- namespace and using directives
;
; The file's first namespace (the outermost) is its package; a namespace
; nested in it does not extend the qualified names.

(file_scoped_namespace_declaration
  name: (_) @package)

(namespace_declaration
  name: (_) @package)

; using N; global using N;
(using_directive
  !name
  [(identifier) (qualified_name) (alias_qualified_name)] @import.spec) @import

; using static N.T; (merges with the match above)
(using_directive
  "static"
  [(identifier) (qualified_name) (alias_qualified_name) (generic_name)] @import.spec
  (#set! import.kind "static")) @import

; using A = N.T;
(using_directive
  name: (identifier) @import.alias
  "="
  .
  (_) @import.spec) @import

; ---- types
;
; The name is matched without its field: a declaration the parser recovered
; around a syntax error keeps its identifier but loses the field.

(class_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (identifier) @name) @definition.class
(class_declaration
  (identifier) @name) @definition.class

(struct_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (identifier) @name) @definition.struct
(struct_declaration
  (identifier) @name) @definition.struct

(record_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (identifier) @name) @definition.record
(record_declaration
  (identifier) @name) @definition.record

(interface_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (identifier) @name) @definition.interface
(interface_declaration
  (identifier) @name) @definition.interface

(enum_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (identifier) @name) @definition.enum
(enum_declaration
  (identifier) @name) @definition.enum

(delegate_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  name: (identifier) @name) @definition.delegate
(delegate_declaration
  name: (identifier) @name) @definition.delegate

; Enum members are constants of their enum.
(enum_member_declaration
  name: (identifier) @name @exported) @definition.constant

; A positional record's parameters are its public properties.
(record_declaration
  (parameter_list
    (parameter
      name: (identifier) @name @exported) @definition.property))

; ---- interface members (public by default)

(interface_declaration
  (declaration_list
    (method_declaration
      name: (identifier) @name @exported) @definition.method))

(interface_declaration
  (declaration_list
    (property_declaration
      name: (identifier) @name @exported) @definition.property))

(interface_declaration
  (declaration_list
    (event_field_declaration
      (variable_declaration
        (variable_declarator
          name: (identifier) @name @exported))) @definition.event))

; ---- members

(method_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  name: (identifier) @name) @definition.method
(method_declaration
  name: (identifier) @name) @definition.method

(constructor_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  name: (identifier) @name) @definition.constructor
(constructor_declaration
  name: (identifier) @name) @definition.constructor

(destructor_declaration
  name: (identifier) @name) @definition.destructor

(operator_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  operator: _ @name) @definition.operator
(operator_declaration
  operator: _ @name) @definition.operator

(conversion_operator_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  type: (_) @name) @definition.operator
(conversion_operator_declaration
  type: (_) @name) @definition.operator

(property_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  name: (identifier) @name) @definition.property
(property_declaration
  name: (identifier) @name) @definition.property

(indexer_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  "this" @name) @definition.indexer
(indexer_declaration
  "this" @name) @definition.indexer

(event_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  name: (identifier) @name) @definition.event
(event_declaration
  name: (identifier) @name) @definition.event

(event_field_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.event
(event_field_declaration
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.event

; Fields: `const` ones are constants.
(field_declaration
  (modifier) @_const (#eq? @_const "const")
  (modifier) @exported (#any-of? @exported "public" "protected")
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.constant
(field_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (modifier) @_const (#eq? @_const "const")
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.constant
(field_declaration
  (modifier) @_const (#eq? @_const "const")
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.constant

(field_declaration
  (modifier) @exported (#any-of? @exported "public" "protected")
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.field
(field_declaration
  (variable_declaration
    (variable_declarator
      name: (identifier) @name))) @definition.field

; ---- inheritance

; An interface extends its bases.
(interface_declaration
  (base_list
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])] @reference.extends))

; A struct (record struct too) only implements interfaces.
(struct_declaration
  (base_list
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])] @reference.implements))

; A class or record names its base class first, then its interfaces; a
; first base named like an interface (IName) is one.
(class_declaration
  (base_list
    .
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])
     (primary_constructor_base_type
       type: [(identifier) @name (generic_name (identifier) @name)])] @reference.extends
    (#not-match? @name "^I[A-Z]")))

(record_declaration
  (base_list
    .
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])
     (primary_constructor_base_type
       type: [(identifier) @name (generic_name (identifier) @name)])] @reference.extends
    (#not-match? @name "^I[A-Z]")))

(class_declaration
  (base_list
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])] @reference.implements
    (#match? @name "^I[A-Z]")))

(record_declaration
  (base_list
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])] @reference.implements
    (#match? @name "^I[A-Z]")))

; ---- calls

(invocation_expression
  function: [(identifier) @name
             (generic_name (identifier) @name)]
  (#not-any-of? @name "nameof")) @reference.call

(invocation_expression
  function: (member_access_expression
    expression: (_) @qualifier
    name: [(identifier) @name
           (generic_name (identifier) @name)])) @reference.call

(invocation_expression
  function: (conditional_access_expression
    condition: (_) @qualifier
    (member_binding_expression
      name: [(identifier) @name
             (generic_name (identifier) @name)]))) @reference.call

; ---- types

; Every node with a type (declarations, parameters, casts, `new`, `typeof`,
; patterns) and every method's return type. A record's base is its
; inheritance above; a pointer type is left out, as `c.r * k` in an
; argument parses as a declaration of a pointer.
(_
  type: [(identifier) @name
         (generic_name (identifier) @name)
         (qualified_name
           qualifier: (_) @qualifier
           name: [(identifier) @name (generic_name (identifier) @name)])] @reference.type
  (#not-has-parent? @reference.type primary_constructor_base_type pointer_type))

(_
  returns: [(identifier) @name
            (generic_name (identifier) @name)
            (qualified_name
              qualifier: (_) @qualifier
              name: [(identifier) @name (generic_name (identifier) @name)])] @reference.type)

; Type arguments (List<Order>) and constraints (where T : IShape).
(type_argument_list
  [(identifier) @name
   (generic_name (identifier) @name)
   (qualified_name
     qualifier: (_) @qualifier
     name: [(identifier) @name (generic_name (identifier) @name)])] @reference.type)

(type_parameter_constraint
  (type
    [(identifier) @name
     (generic_name (identifier) @name)
     (qualified_name
       qualifier: (_) @qualifier
       name: [(identifier) @name (generic_name (identifier) @name)])]) @reference.type)

; Attributes name their class ([Serializable] is SerializableAttribute).
(attribute
  name: [(identifier) @name
         (qualified_name
           qualifier: (_) @qualifier
           name: (identifier) @name)] @reference.type)
