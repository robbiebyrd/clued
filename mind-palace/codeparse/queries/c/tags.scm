; C: definitions, references and #include, in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md). C has no package
; clause. Doc comments are the comments on the lines just above a
; definition (the extractor's default); signatures are a function's text up
; to its body, a prototype's whole declaration, otherwise the first line.
;
; Visibility: a function or global whose specifiers say static is local to
; its translation unit; every other definition is captured @exported too
; (the profile's exported: declared). The static test reads the text before
; the declarator's first parenthesis, initializer or body, skipping
; __attribute__((...)) groups.
;
; No preprocessing: symbols in every branch of #if/#ifdef are indexed, and
; macros are definitions; a macro used as a function is a call reference.

; ---- includes

(preproc_include
  path: (string_literal) @import.spec
  (#set! import.kind "include")) @import

(preproc_include
  path: (system_lib_string) @import.spec
  (#set! import.kind "include_system")) @import

(preproc_include
  path: [(identifier) (call_expression)] @import.spec
  (#set! import.kind "include_macro")) @import

; ---- functions (definitions)

((function_definition
  declarator: [(function_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))
               (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))
               ; a function returning a function pointer: int (*f(void))(int)
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))))
               (pointer_declarator declarator: (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))))])
  @definition.function
  (#match? @definition.function "^([^(=;{]|__attribute__\\s*\\(\\(([^()]|\\([^()]*\\))*\\)\\))*\\bstatic\\b"))

((function_definition
  declarator: [(function_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))
               (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))))
               (pointer_declarator declarator: (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))))])
  @definition.function @exported
  (#not-match? @definition.function "^([^(=;{]|__attribute__\\s*\\(\\(([^()]|\\([^()]*\\))*\\)\\))*\\bstatic\\b"))

; ---- prototypes (declarations outside function bodies)

((declaration
  declarator: [(function_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))
               (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))))])
  @definition.function @signature
  (#not-has-ancestor? @definition.function function_definition)
  (#match? @definition.function "^([^(=;{]|__attribute__\\s*\\(\\(([^()]|\\([^()]*\\))*\\)\\))*\\bstatic\\b"))

((declaration
  declarator: [(function_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))
               (pointer_declarator declarator: (pointer_declarator declarator: (function_declarator declarator: (identifier) @name)))
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (function_declarator declarator: (identifier) @name))))])
  @definition.function @signature @exported
  (#not-has-ancestor? @definition.function function_definition)
  (#not-match? @definition.function "^([^(=;{]|__attribute__\\s*\\(\\(([^()]|\\([^()]*\\))*\\)\\))*\\bstatic\\b"))

; ---- globals (declarations outside function bodies, extern ones too)

((declaration
  declarator: [(identifier) @name
               (pointer_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (pointer_declarator declarator: (identifier) @name))
               (array_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (array_declarator declarator: (identifier) @name))
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (identifier) @name)))
               (init_declarator declarator: [
                 (identifier) @name
                 (pointer_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (array_declarator declarator: (identifier) @name))
                 (function_declarator declarator: (parenthesized_declarator
                   (pointer_declarator declarator: (identifier) @name)))])])
  @definition.variable
  (#not-has-ancestor? @definition.variable function_definition)
  (#match? @definition.variable "^([^(=;{]|__attribute__\\s*\\(\\(([^()]|\\([^()]*\\))*\\)\\))*\\bstatic\\b"))

((declaration
  declarator: [(identifier) @name
               (pointer_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (pointer_declarator declarator: (identifier) @name))
               (array_declarator declarator: (identifier) @name)
               (pointer_declarator declarator: (array_declarator declarator: (identifier) @name))
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (identifier) @name)))
               (init_declarator declarator: [
                 (identifier) @name
                 (pointer_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (pointer_declarator declarator: (identifier) @name))
                 (array_declarator declarator: (identifier) @name)
                 (pointer_declarator declarator: (array_declarator declarator: (identifier) @name))
                 (function_declarator declarator: (parenthesized_declarator
                   (pointer_declarator declarator: (identifier) @name)))])])
  @definition.variable @exported
  (#not-has-ancestor? @definition.variable function_definition)
  (#not-match? @definition.variable "^([^(=;{]|__attribute__\\s*\\(\\(([^()]|\\([^()]*\\))*\\)\\))*\\bstatic\\b"))

; ---- structs, unions and enums (with a body; without one they are type
; references below)

(struct_specifier
  name: (type_identifier) @name
  body: (field_declaration_list)) @definition.struct @exported

(union_specifier
  name: (type_identifier) @name
  body: (field_declaration_list)) @definition.union @exported

(enum_specifier
  name: (type_identifier) @name
  body: (enumerator_list)) @definition.enum @exported

; Enumerators are constants (nested in their enum when it has a name).
(enumerator_list
  (enumerator
    name: (identifier) @name) @definition.constant @exported)

; Struct and union members, plain, pointer, array, bit-field and
; function-pointer ones.
(field_declaration_list
  (field_declaration
    declarator: [(field_identifier) @name
                 (pointer_declarator declarator: (field_identifier) @name)
                 (pointer_declarator declarator: (pointer_declarator declarator: (field_identifier) @name))
                 (array_declarator declarator: (field_identifier) @name)
                 (pointer_declarator declarator: (array_declarator declarator: (field_identifier) @name))
                 (function_declarator declarator: (parenthesized_declarator
                   (pointer_declarator declarator: (field_identifier) @name)))
                 (function_declarator declarator: (field_identifier) @name)])
  @definition.field @exported)

; ---- typedefs

; A typedef of a struct, union or enum that has a tag and a body is the
; declarator alone, so the tagged type stays a top-level symbol of its own
; (typedef struct entry {...} entry_t: struct entry and type entry_t).
(type_definition
  type: [(struct_specifier name: (_) body: (_))
         (union_specifier name: (_) body: (_))
         (enum_specifier name: (_) body: (_))]
  declarator: [(type_identifier) @name @definition.type @exported
               (pointer_declarator declarator: (type_identifier) @name) @definition.type @exported]
  (#match? @_typedef "^typedef\\s+(\\w+\\s+)*(struct|union|enum)\\s+\\w+\\s*\\{")) @_typedef

; Any other typedef is the whole definition: an anonymous struct's members
; are the typedef's.
((type_definition
  declarator: [(type_identifier) @name
               (pointer_declarator declarator: (type_identifier) @name)
               (pointer_declarator declarator: (pointer_declarator declarator: (type_identifier) @name))
               (array_declarator declarator: (type_identifier) @name)
               (function_declarator declarator: (type_identifier) @name)
               (function_declarator declarator: (parenthesized_declarator
                 (pointer_declarator declarator: (type_identifier) @name)))])
  @definition.type @exported
  (#not-match? @definition.type "^typedef\\s+(\\w+\\s+)*(struct|union|enum)\\s+\\w+\\s*\\{"))

; ---- macros

(preproc_def
  name: (identifier) @name) @definition.macro @exported

(preproc_function_def
  name: (identifier) @name) @definition.macro @exported

; ---- references

; Calls of functions and function-like macros.
(call_expression
  function: (identifier) @name) @reference.call

; Calls through a struct member: ops->open(f), s.cb().
(call_expression
  function: (field_expression
    argument: (_) @qualifier
    field: (field_identifier) @name)) @reference.call

; Typedef names, and struct, union and enum tags used without a body.
((type_identifier) @name @reference.type)

; Upper-case names used as values (macro constants and enumerators by C's
; naming convention, in code and in #ifdef / defined()); lower-case
; identifiers are mostly locals and parameters, which are not tracked.
((identifier) @name @reference.value
  (#match? @name "^[A-Z][A-Z0-9_]*$")
  (#not-has-parent? @name call_expression preproc_params parameter_declaration))
