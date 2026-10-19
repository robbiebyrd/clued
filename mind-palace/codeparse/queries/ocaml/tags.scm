; OCaml: implementations (.ml) and interfaces (.mli, the "interface"
; dialect, parsed with the same grammar and query), in tree-sitter's
; tags-query convention plus clued's captures (see ../README.md). A file is
; its own module, so there is no @package; doc comments are the (** *)
; comments on the lines just above a definition (the extractor's default),
; signatures its text up to the body.

; ---- imports: open, include and local opens

(open_module
  module: (_) @import.spec
  (#set! import.kind "open")) @import

(include_module
  module: (_) @import.spec
  (#set! import.kind "include")) @import

(let_expression
  (open_module
    module: (_) @import.spec
    (#set! import.kind "local_open")) @import)

(local_open_expression
  .
  (module_path) @import.spec
  (#set! import.kind "local_open")) @import

; ---- modules, functors and module types

; A functor: a module binding with parameters. Listed before the plain
; module so that its kind wins for the same node.
(module_definition
  (module_binding
    (module_name) @name
    (module_parameter)) @definition.functor)

(module_definition
  (module_binding
    (module_name) @name) @definition.module)

(module_type_definition
  (module_type_name) @name) @definition.interface

; ---- types, their constructors and fields

(type_definition
  (type_binding
    name: (type_constructor) @name
    body: (variant_declaration)) @definition.enum)

(type_definition
  (type_binding
    name: (type_constructor) @name
    body: (record_declaration)) @definition.struct)

(type_definition
  (type_binding
    name: (type_constructor) @name) @definition.type)

(variant_declaration
  (constructor_declaration
    (constructor_name) @name) @definition.variant)

(record_declaration
  (field_declaration
    (field_name) @name) @definition.field)

(exception_definition
  (constructor_declaration
    (constructor_name) @name)) @definition.exception

; ---- functions and values (module level only, not local lets)

; A binding with parameters, or bound to fun / function, is a function.
(compilation_unit
  (value_definition
    (let_binding
      pattern: (value_name) @name
      (parameter)) @definition.function))

(structure
  (value_definition
    (let_binding
      pattern: (value_name) @name
      (parameter)) @definition.function))

(compilation_unit
  (value_definition
    (let_binding
      pattern: (value_name) @name
      body: [(fun_expression) (function_expression)]) @definition.function))

(structure
  (value_definition
    (let_binding
      pattern: (value_name) @name
      body: [(fun_expression) (function_expression)]) @definition.function))

; An operator definition, let (+++) a b = ...
(compilation_unit
  (value_definition
    (let_binding
      pattern: (parenthesized_operator) @name) @definition.function))

(structure
  (value_definition
    (let_binding
      pattern: (parenthesized_operator) @name) @definition.function))

; Any other binding of a name is a value.
(compilation_unit
  (value_definition
    (let_binding
      pattern: (value_name) @name) @definition.value))

(structure
  (value_definition
    (let_binding
      pattern: (value_name) @name) @definition.value))

; Interface items: val with a function type is a function, else a value.
(value_specification
  (value_name) @name
  type: (function_type)) @definition.function

(value_specification
  (value_name) @name) @definition.value

(value_specification
  (parenthesized_operator) @name) @definition.function

(external
  (value_name) @name) @definition.function

; ---- classes

(class_definition
  (class_binding
    (class_name) @name) @definition.class)

(class_type_definition
  (class_type_binding
    (class_type_name) @name) @definition.interface)

(method_definition
  (method_name) @name) @definition.method

(method_specification
  (method_name) @name) @definition.method

(instance_variable_definition
  (instance_variable_name) @name) @definition.field

; ---- references: calls

(application_expression
  function: (value_path
    (module_path) @qualifier
    (value_name) @name)) @reference.call

(application_expression
  function: (value_path
    .
    (value_name) @name
    (#not-any-of? @name
      "raise" "raise_notrace" "failwith" "invalid_arg" "ignore" "not"
      "fst" "snd" "ref" "incr" "decr" "succ" "pred" "min" "max" "compare"
      "print_string" "print_endline" "print_int" "print_float"
      "print_newline" "prerr_string" "prerr_endline" "string_of_int"
      "int_of_string" "string_of_float" "float_of_string" "float_of_int"
      "int_of_float"))) @reference.call

; Pipelines: x |> f and f @@ x call f.
(infix_expression
  operator: (_) @_op
  right: (value_path
    (module_path)? @qualifier
    (value_name) @name)
  (#eq? @_op "|>")) @reference.call

(infix_expression
  left: (value_path
    (module_path)? @qualifier
    (value_name) @name)
  operator: (_) @_op
  (#eq? @_op "@@")) @reference.call

(method_invocation
  object: (_) @qualifier
  method: (method_name) @name) @reference.call

; Functor application, F (X): a call of the functor; Set.Make qualified.
(module_application
  functor: (module_path
    (module_path) @qualifier
    (module_name) @name)) @reference.call

(module_application
  functor: (module_path
    .
    (module_name) @name)) @reference.call

; ---- references: types, module types and constructors

(type_constructor_path
  (extended_module_path) @qualifier
  (type_constructor) @name) @reference.type

(type_constructor_path
  .
  (type_constructor) @name
  (#not-any-of? @name
    "int" "float" "string" "bool" "char" "unit" "bytes" "exn" "list"
    "array" "option" "result" "ref" "lazy_t" "int32" "int64" "nativeint"
    "format" "format4" "format6" "seq")) @reference.type

; A module bound to a path (module L = List) or passed to a functor.
(module_binding
  body: (module_path
    (module_path)? @qualifier
    (module_name) @name)) @reference.module

(module_binding
  body: (extended_module_path
    (extended_module_path)? @qualifier
    (module_name) @name)) @reference.module

(parenthesized_module_expression
  (module_path
    (module_path)? @qualifier
    (module_name) @name)) @reference.module

; A module or functor result constrained by a module type implements it.
(module_binding
  module_type: (module_type_path
    (extended_module_path)? @qualifier
    (module_type_name) @name)) @reference.implementation

(module_binding
  module_type: (module_type_constraint
    module_type: (module_type_path
      (extended_module_path)? @qualifier
      (module_type_name) @name))) @reference.implementation

; Functor parameters, first-class modules and module type of name a type.
(module_parameter
  module_type: (module_type_path
    (extended_module_path)? @qualifier
    (module_type_name) @name)) @reference.interface

(package_expression
  module_type: (module_type_path
    (extended_module_path)? @qualifier
    (module_type_name) @name)) @reference.interface

; Classes: new c is a use of the class, inherit c extends it.
(new_expression
  (class_path
    (module_path)? @qualifier
    (class_name) @name)) @reference.class

(inheritance_definition
  class: [(class_path (class_name) @name)
          (class_application class: (class_path (class_name) @name))]) @reference.extends

; Constructors used in expressions and patterns (exceptions, variants).
(constructor_path
  (module_path)? @qualifier
  (constructor_name) @name
  (#not-any-of? @name "Some" "None" "Ok" "Error" "true" "false")) @reference.value

; Record fields read through a value, r.M.x.
(field_get_expression
  field: (field_path
    (module_path)? @qualifier
    (field_name) @name)) @reference.field
