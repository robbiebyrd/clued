; Rust: items, references, `use` declarations, `extern crate` and `mod`
; declarations, in tree-sitter's tags-query convention plus clued's captures
; (see ../README.md). A file is its own module (its place in the `mod` tree
; from the crate root is resolved later), so there is no @package; names are
; qualified with `::`. Methods nest in their impl, which is named after its
; type, so a method's qualified name is `Type::method`; an impl of a trait
; refers to the trait as @reference.implements.
;
; Docs are the extractor's default, the comments on the lines just above an
; item. An item whose `///` comment stands above its attributes
; (`#[derive(...)]`) gets no doc: attributes are the item's siblings, and
; patterns over siblings cost time quadratic in the number of children in
; the runtime (a 130 KB file took seconds), so the query has none.
;
; Every form has a pattern of its own, without `[...]` alternations: the
; runtime can give every match of an alternation the first alternative's
; @name.
;
; Visibility: an item with a visibility modifier (`pub`, `pub(crate)`, ...)
; is @exported, trait items and the methods of a trait impl as the trait,
; enum variants and their fields as the enum. `macro_rules!` macros are
; scoped by text, so they are not @exported (#[macro_export] included).

; ---- items whose kind does not depend on where they stand

(struct_item
  (visibility_modifier)? @exported
  name: (type_identifier) @name) @definition.struct

(enum_item
  (visibility_modifier)? @exported
  name: (type_identifier) @name) @definition.enum

(union_item
  (visibility_modifier)? @exported
  name: (type_identifier) @name) @definition.union

(trait_item
  (visibility_modifier)? @exported
  name: (type_identifier) @name) @definition.trait

(type_item
  (visibility_modifier)? @exported
  name: (type_identifier) @name) @definition.type

(associated_type
  name: (type_identifier) @name) @definition.type

(const_item
  (visibility_modifier)? @exported
  name: (identifier) @name) @definition.constant

(static_item
  (visibility_modifier)? @exported
  name: (identifier) @name) @definition.variable

(mod_item
  (visibility_modifier)? @exported
  name: (identifier) @name) @definition.module

(macro_definition
  name: (identifier) @name) @definition.macro

; ---- impls, named after their type

(impl_item
  type: (type_identifier) @name) @definition.impl

(impl_item
  type: (generic_type
    type: (type_identifier) @name)) @definition.impl

(impl_item
  type: (scoped_type_identifier
    name: (type_identifier) @name)) @definition.impl

(impl_item
  type: (generic_type
    type: (scoped_type_identifier
      name: (type_identifier) @name))) @definition.impl

(impl_item
  type: (reference_type
    type: (type_identifier) @name)) @definition.impl

(impl_item
  type: (reference_type
    type: (generic_type
      type: (type_identifier) @name))) @definition.impl

(impl_item
  type: (primitive_type) @name) @definition.impl

; ---- functions: in the file, a module, a block (nested functions), an
; extern block, an error node

(source_file
  (function_item
    (visibility_modifier)? @exported
    name: (identifier) @name) @definition.function)

(mod_item
  body: (declaration_list
    (function_item
      (visibility_modifier)? @exported
      name: (identifier) @name) @definition.function))

(block
  (function_item
    name: (identifier) @name) @definition.function)

(foreign_mod_item
  body: (declaration_list
    (function_signature_item
      (visibility_modifier)? @exported
      name: (identifier) @name) @definition.function))

(ERROR
  (function_item
    (visibility_modifier)? @exported
    name: (identifier) @name) @definition.function)

; ---- methods: of an inherent impl (by their own visibility), of a trait
; impl and of a trait, required or provided (as visible as the trait)

(impl_item
  !trait
  body: (declaration_list
    (function_item
      (visibility_modifier)? @exported
      name: (identifier) @name) @definition.method))

(impl_item
  trait: (_) @exported
  body: (declaration_list
    (function_item
      name: (identifier) @name) @definition.method))

(trait_item
  (visibility_modifier)? @exported
  body: (declaration_list
    (function_item
      name: (identifier) @name) @definition.method))

(trait_item
  (visibility_modifier)? @exported
  body: (declaration_list
    (function_signature_item
      name: (identifier) @name) @definition.method))

; ---- fields and variants

(struct_item
  body: (field_declaration_list
    (field_declaration
      (visibility_modifier)? @exported
      name: (field_identifier) @name) @definition.field))

(union_item
  body: (field_declaration_list
    (field_declaration
      (visibility_modifier)? @exported
      name: (field_identifier) @name) @definition.field))

(enum_item
  (visibility_modifier)? @exported
  body: (enum_variant_list
    (enum_variant
      name: (identifier) @name) @definition.variant))

(enum_item
  (visibility_modifier)? @exported
  body: (enum_variant_list
    (enum_variant
      body: (field_declaration_list
        (field_declaration
          name: (field_identifier) @name) @definition.field))))

; ---- imports: `use`, `extern crate`, `mod name;`
;
; @import.spec is the path the names come from, @import.name the names (as
; written, `Entry as E` included, inside a `{...}` list); a glob is kind
; "glob", `extern crate` kind "extern_crate", a `mod name;` declaration
; (the file of a child module) kind "mod".

; use a::b::C;
(use_declaration
  argument: (scoped_identifier
    path: (_) @import.spec
    name: (identifier) @import.name)) @import

; use a;
(use_declaration
  argument: (identifier) @import.spec) @import

; use a::b::C as D;  use a as b;
(use_declaration
  argument: (use_as_clause
    path: (scoped_identifier
      path: (_) @import.spec
      name: (identifier) @import.name)
    alias: (identifier) @import.alias)) @import

(use_declaration
  argument: (use_as_clause
    path: (identifier) @import.spec
    alias: (identifier) @import.alias)) @import

; use a::b::*;  use super::*;
(use_declaration
  argument: (use_wildcard (scoped_identifier) @import.spec)
  (#set! import.kind "glob")) @import

(use_declaration
  argument: (use_wildcard (identifier) @import.spec)
  (#set! import.kind "glob")) @import

(use_declaration
  argument: (use_wildcard (self) @import.spec)
  (#set! import.kind "glob")) @import

(use_declaration
  argument: (use_wildcard (super) @import.spec)
  (#set! import.kind "glob")) @import

(use_declaration
  argument: (use_wildcard (crate) @import.spec)
  (#set! import.kind "glob")) @import

; use a::b::{C, d::E as F, self};  every element as written, in order
(use_declaration
  argument: (scoped_use_list
    path: (_) @import.spec
    list: (use_list (_) @import.name
      (#not-match? @import.name "^/[/*]")))) @import

; extern crate serde;  extern crate foo as bar;
(extern_crate_declaration
  name: (identifier) @import.spec
  alias: (identifier)? @import.alias
  (#set! import.kind "extern_crate")) @import

; mod inner;
(mod_item
  name: (identifier) @import.spec
  !body
  (#set! import.kind "mod")) @import

; ---- references

; f(x), Point::new(x), crate::store::open(), p.draw(), f::<T>(x)
(call_expression
  function: (identifier) @name
  (#not-any-of? @name "Some" "Ok" "Err")) @reference.call

(call_expression
  function: (scoped_identifier
    path: (_) @qualifier
    name: (identifier) @name)) @reference.call

(call_expression
  function: (field_expression
    value: (_) @qualifier
    field: (field_identifier) @name)) @reference.call

(call_expression
  function: (generic_function
    function: (identifier) @name)) @reference.call

(call_expression
  function: (generic_function
    function: (scoped_identifier
      path: (_) @qualifier
      name: (identifier) @name))) @reference.call

(call_expression
  function: (generic_function
    function: (field_expression
      value: (_) @qualifier
      field: (field_identifier) @name))) @reference.call

; Macro invocations, but the standard library's.
(macro_invocation
  macro: (identifier) @name
  (#not-any-of? @name
    "assert" "assert_eq" "assert_ne" "cfg" "column" "compile_error" "concat"
    "dbg" "debug_assert" "debug_assert_eq" "debug_assert_ne" "env" "eprint"
    "eprintln" "file" "format" "format_args" "include" "include_bytes"
    "include_str" "line" "matches" "module_path" "option_env" "panic" "print"
    "println" "stringify" "todo" "unimplemented" "unreachable" "vec" "write"
    "writeln")) @reference.macro

(macro_invocation
  macro: (scoped_identifier
    path: (_) @qualifier
    name: (identifier) @name)) @reference.macro

; impl Trait for Type
(impl_item
  trait: (type_identifier) @name) @reference.implements

(impl_item
  trait: (scoped_type_identifier
    path: (_) @qualifier
    name: (type_identifier) @name)) @reference.implements

(impl_item
  trait: (generic_type
    type: (type_identifier) @name)) @reference.implements

(impl_item
  trait: (generic_type
    type: (scoped_type_identifier
      path: (_) @qualifier
      name: (type_identifier) @name))) @reference.implements

; trait Draw: Debug + fmt::Display
(trait_item
  bounds: (trait_bounds (type_identifier) @name)) @reference.extends

(trait_item
  bounds: (trait_bounds (scoped_type_identifier
    path: (_) @qualifier
    name: (type_identifier) @name))) @reference.extends

(trait_item
  bounds: (trait_bounds (generic_type
    type: (type_identifier) @name))) @reference.extends

(trait_item
  bounds: (trait_bounds (generic_type
    type: (scoped_type_identifier
      path: (_) @qualifier
      name: (type_identifier) @name)))) @reference.extends

; Types, qualified (fmt::Formatter) and bare; Self is not a reference.
(scoped_type_identifier
  path: (_) @qualifier
  name: (type_identifier) @name) @reference.type

((type_identifier) @name @reference.type
  (#not-eq? @name "Self"))
