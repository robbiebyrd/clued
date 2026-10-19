; Objective-C: classes (@interface), class extensions, categories,
; @implementation blocks, protocols, methods (named by their selector's
; first keyword), properties and instance variables, plus the C layer:
; functions, structs, unions, enums, typedefs, macros and file-level
; variables. In tree-sitter's tags-query convention plus clued's captures
; (see ../README.md). Doc comments are the comments on the lines just above
; a definition (the extractor's default), signatures its text up to the
; body or its first line.
;
; The grammar does not know Foundation's macros: NS_ENUM / NS_OPTIONS and
; NS_ASSUME_NONNULL_BEGIN parse with error nodes, so no pattern below
; depends on a definition sitting directly under translation_unit.

; ---- imports: #import / #include, and @import of a module

((preproc_include
  path: (system_lib_string) @import.spec) @import
  (#match? @import "^#[ \t]*import")
  (#set! import.kind "import_system"))

((preproc_include
  path: (string_literal) @import.spec) @import
  (#match? @import "^#[ \t]*import")
  (#set! import.kind "import"))

((preproc_include
  path: (system_lib_string) @import.spec) @import
  (#match? @import "^#[ \t]*include")
  (#set! import.kind "include_system"))

((preproc_include
  path: (string_literal) @import.spec) @import
  (#match? @import "^#[ \t]*include")
  (#set! import.kind "include"))

((module_import
  .
  path: (identifier) @import.spec
  (identifier)* @import.name) @import
  (#set! import.kind "module"))

; ---- classes, extensions and categories

; @interface Shape : NSObject <Drawable> (a class extension is
; @interface Shape () and has no superclass).
(class_interface
  .
  (identifier) @name
  superclass: (_)) @definition.class

; A root class (no superclass), not a class extension.
((class_interface
  .
  (identifier) @name
  !superclass
  !category) @definition.class
  (#not-match? @definition.class "^@interface[ \t\n]+[A-Za-z_][A-Za-z0-9_]*[ \t\n]*(<[^>]*>[ \t\n]*)?\\("))

; @interface Shape () — a class extension, named by its class.
((class_interface
  .
  (identifier) @name
  !superclass
  !category) @definition.extension
  (#match? @definition.extension "^@interface[ \t\n]+[A-Za-z_][A-Za-z0-9_]*[ \t\n]*(<[^>]*>[ \t\n]*)?\\("))

; @interface Shape (Debug) — a category, a member of its class.
(class_interface
  .
  (identifier) @receiver
  category: (identifier) @name) @definition.category

; @implementation Shape
(class_implementation
  .
  (identifier) @name
  !category) @definition.impl

; @implementation Shape (Debug)
(class_implementation
  .
  (identifier) @receiver
  category: (identifier) @name) @definition.impl

; @protocol Drawable <NSObject> ... @end
(protocol_declaration
  .
  (identifier) @name) @definition.protocol

; ---- methods: declarations and definitions, named by the first keyword
; of the selector (initWithName for initWithName:sides:)

(method_declaration
  (method_type)
  .
  (identifier) @name) @definition.method

(method_declaration
  .
  (identifier) @name) @definition.method

(method_definition
  (method_type)
  .
  (identifier) @name) @definition.method

(method_definition
  .
  (identifier) @name) @definition.method

; ---- properties and instance variables (gotreesitter v0.56.0 can give
; every match of an alternation the first branch's @name, so each
; declarator shape is a pattern of its own: a name, a pointer, a pointer to
; a pointer, a block and a function pointer)

(property_declaration
  (struct_declaration
    (struct_declarator
      (identifier) @name))) @definition.property

(property_declaration
  (struct_declaration
    (struct_declarator
      (pointer_declarator declarator: (identifier) @name)))) @definition.property

(property_declaration
  (struct_declaration
    (struct_declarator
      (pointer_declarator declarator: (pointer_declarator declarator: (identifier) @name))))) @definition.property

(property_declaration
  (struct_declaration
    (struct_declarator
      (function_declarator declarator: (parenthesized_declarator (block_pointer_declarator declarator: (identifier) @name)))))) @definition.property

(property_declaration
  (struct_declaration
    (struct_declarator
      (function_declarator declarator: (parenthesized_declarator (pointer_declarator declarator: (identifier) @name)))))) @definition.property

(instance_variable
  (struct_declaration
    (struct_declarator
      (identifier) @name))) @definition.field

(instance_variable
  (struct_declaration
    (struct_declarator
      (pointer_declarator declarator: (identifier) @name)))) @definition.field

(instance_variable
  (struct_declaration
    (struct_declarator
      (pointer_declarator declarator: (pointer_declarator declarator: (identifier) @name))))) @definition.field

(instance_variable
  (struct_declaration
    (struct_declarator
      (function_declarator declarator: (parenthesized_declarator (block_pointer_declarator declarator: (identifier) @name)))))) @definition.field

(instance_variable
  (struct_declaration
    (struct_declarator
      (function_declarator declarator: (parenthesized_declarator (pointer_declarator declarator: (identifier) @name)))))) @definition.field

; ---- C functions (definitions, and prototypes outside a body)

(function_definition
  declarator: (function_declarator declarator: (identifier) @name)) @definition.function

(function_definition
  declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))) @definition.function

(function_definition
  declarator: (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))) @definition.function

((declaration
  declarator: (function_declarator declarator: (identifier) @name)) @definition.function
  (#not-has-ancestor? @definition.function function_definition method_definition block_literal))

((declaration
  declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))) @definition.function
  (#not-has-ancestor? @definition.function function_definition method_definition block_literal))

((declaration
  declarator: (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))) @definition.function
  (#not-has-ancestor? @definition.function function_definition method_definition block_literal))

; ---- C types: structs, unions, enums and typedefs

; typedef struct { ... } Name; (also when the struct has a tag: the typedef
; is the one definition, named by its declarator)
(type_definition
  type: (struct_specifier body: (_))
  declarator: (type_identifier) @name) @definition.struct

(type_definition
  type: (union_specifier body: (_))
  declarator: (type_identifier) @name) @definition.union

(type_definition
  type: (enum_specifier body: (_))
  declarator: (type_identifier) @name) @definition.enum

((struct_specifier
  name: (type_identifier) @name
  body: (_)) @definition.struct
  (#not-has-parent? @definition.struct type_definition))

((union_specifier
  name: (type_identifier) @name
  body: (_)) @definition.union
  (#not-has-parent? @definition.union type_definition))

((enum_specifier
  name: (type_identifier) @name
  body: (_)) @definition.enum
  (#not-has-parent? @definition.enum type_definition))

; typedef of anything without a body: a name, a pointer, a block or a
; function pointer.

((type_definition
  type: [(primitive_type) (type_identifier) (sized_type_specifier)
         (typedefed_specifier) (generic_specifier)
         (struct_specifier !body) (union_specifier !body) (enum_specifier !body)] @_type
  declarator: (type_identifier) @name) @definition.type
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$"))

((type_definition
  type: [(primitive_type) (type_identifier) (sized_type_specifier)
         (typedefed_specifier) (generic_specifier)
         (struct_specifier !body) (union_specifier !body) (enum_specifier !body)] @_type
  declarator: (pointer_declarator declarator: (type_identifier) @name)) @definition.type
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$"))

((type_definition
  type: [(primitive_type) (type_identifier) (sized_type_specifier)
         (typedefed_specifier) (generic_specifier)
         (struct_specifier !body) (union_specifier !body) (enum_specifier !body)] @_type
  declarator: (function_declarator declarator: (parenthesized_declarator (block_pointer_declarator declarator: (type_identifier) @name)))) @definition.type
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$"))

((type_definition
  type: [(primitive_type) (type_identifier) (sized_type_specifier)
         (typedefed_specifier) (generic_specifier)
         (struct_specifier !body) (union_specifier !body) (enum_specifier !body)] @_type
  declarator: (function_declarator declarator: (parenthesized_declarator (pointer_declarator declarator: (type_identifier) @name)))) @definition.type
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$"))

; typedef NS_ENUM(NSInteger, Name) { ... }: the grammar reads the macro as
; a type and the name as an error, and its enumerators as declarators.
; Where the macro follows NS_ASSUME_NONNULL_BEGIN it reads it as a call
; (the next pattern) and the enumerators as expressions, which are lost.
((type_definition
  type: (type_identifier) @_macro
  (ERROR
    .
    (type_identifier) @name)) @definition.enum
  (#any-of? @_macro "NS_ENUM" "NS_OPTIONS" "NS_CLOSED_ENUM" "NS_ERROR_ENUM" "CF_ENUM" "CF_OPTIONS"))

((type_definition
  type: (type_identifier) @_macro
  declarator: (type_identifier) @name @definition.constant)
  (#any-of? @_macro "NS_ENUM" "NS_OPTIONS" "NS_CLOSED_ENUM" "NS_ERROR_ENUM" "CF_ENUM" "CF_OPTIONS"))

; At the top of a file it is an error node holding the macro, the
; parenthesised type and the name, and the rest of the file is read as a
; compound statement (which is why locals are told apart by their function,
; method or block, not by a compound statement).
((ERROR
  .
  (type_identifier) @_macro
  .
  (parenthesized_declarator)
  .
  (type_identifier) @name) @definition.enum
  (#any-of? @_macro "NS_ENUM" "NS_OPTIONS" "NS_CLOSED_ENUM" "NS_ERROR_ENUM" "CF_ENUM" "CF_OPTIONS"))

((function_declarator
  declarator: (identifier) @_macro
  parameters: (parameter_list
    (parameter_declaration)
    .
    (parameter_declaration
      type: (type_identifier) @name))) @definition.enum
  (#any-of? @_macro "NS_ENUM" "NS_OPTIONS" "NS_CLOSED_ENUM" "NS_ERROR_ENUM" "CF_ENUM" "CF_OPTIONS"))

(field_declaration_list
  (field_declaration
    declarator: (field_identifier) @name) @definition.field)

(field_declaration_list
  (field_declaration
    declarator: (pointer_declarator declarator: (field_identifier) @name)) @definition.field)

(field_declaration_list
  (field_declaration
    declarator: (array_declarator declarator: (field_identifier) @name)) @definition.field)

(enumerator_list
  (enumerator
    name: (identifier) @name) @definition.constant)

; ---- macros

(preproc_def
  name: (identifier) @name) @definition.macro

(preproc_function_def
  name: (identifier) @name) @definition.macro

; ---- file-level constants and variables (not locals). A declaration
; whose type is a macro (NS_ASSUME_NONNULL_BEGIN misread before an
; @protocol forward declaration) defines nothing.

((declaration
  type: (_) @_type
  declarator: (identifier) @name) @definition.constant
  (#match? @definition.constant "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.constant function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (init_declarator declarator: (identifier) @name)) @definition.constant
  (#match? @definition.constant "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.constant function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (pointer_declarator declarator: (identifier) @name)) @definition.constant
  (#match? @definition.constant "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.constant function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))) @definition.constant
  (#match? @definition.constant "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.constant function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (identifier) @name) @definition.variable
  (#not-match? @definition.variable "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.variable function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (init_declarator declarator: (identifier) @name)) @definition.variable
  (#not-match? @definition.variable "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.variable function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (pointer_declarator declarator: (identifier) @name)) @definition.variable
  (#not-match? @definition.variable "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.variable function_definition method_definition block_literal))

((declaration
  type: (_) @_type
  declarator: (init_declarator declarator: (pointer_declarator declarator: (identifier) @name))) @definition.variable
  (#not-match? @definition.variable "(^|[^A-Za-z0-9_])const([^A-Za-z0-9_]|$)")
  (#not-match? @_type "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-has-ancestor? @definition.variable function_definition method_definition block_literal))

; ---- references: messages (selector-based calls), C calls

; [receiver keyword:arg other:arg] — the call is to the first keyword.
(message_expression
  receiver: (_) @qualifier
  .
  method: (identifier) @name) @reference.send

((call_expression
  function: (identifier) @name) @reference.call
  (#not-has-parent? @reference.call at_expression))

(call_expression
  function: (field_expression
    argument: (_) @qualifier
    field: (field_identifier) @name)) @reference.call

; ---- references: inheritance and conformance

(class_interface
  superclass: (identifier) @name) @reference.extends

(class_implementation
  superclass: (identifier) @name) @reference.extends

; <Drawable, NSCopying> after a class (the grammar reads the list as type
; arguments; generic parameters are type_identifiers and stay out).
(class_interface
  (parameterized_arguments
    (type_name
      (identifier) @name) @reference.implements))

(class_interface
  (protocol_reference_list
    (identifier) @name @reference.implements))

(protocol_declaration
  (protocol_reference_list
    (identifier) @name @reference.extends))

; ---- references: types

; @class Widget; @protocol Fwd; @protocol(Drawable)
(class_declaration
  (identifier) @name @reference.type)

(protocol_forward_declaration
  (identifier) @name @reference.type)

(at_expression
  (call_expression
    function: (identifier) @_protocol
    arguments: (argument_list
      (identifier) @name @reference.type))
  (#eq? @_protocol "protocol"))

; Types of methods and casts: (NSString *), (instancetype); a class's
; protocol list is an implements reference above.
((type_name
  (identifier) @name @reference.type) @_type
  (#not-has-parent? @_type parameterized_arguments)
  (#not-any-of? @name "id" "instancetype" "Class" "SEL" "IMP" "BOOL"))

; id<Drawable>
(typedefed_specifier
  (protocol_reference_list
    (identifier) @name @reference.type))

; C++ keywords are what the grammar makes of Objective-C++ (.mm) forms
; it does not know.
((type_identifier) @name @reference.type
  (#not-match? @name "^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")
  (#not-any-of? @name "id" "instancetype" "Class" "SEL" "IMP" "BOOL" "self"
    "namespace" "class" "template" "typename" "explicit" "virtual" "public"
    "private" "protected" "using" "operator" "new" "delete" "friend" "auto"))

; ---- references: values

; self.name, obj.count
((field_expression
  argument: (_) @qualifier
  field: (field_identifier) @name) @reference.field
  (#not-has-parent? @reference.field call_expression))

; @synthesize name = _name; @dynamic name;
(property_implementation
  .
  (identifier) @name @reference.property)
