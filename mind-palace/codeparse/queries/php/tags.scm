; PHP: definitions, references, imports and the namespace, in tree-sitter's
; tags-query convention plus clued's captures (see ../README.md). Doc
; comments are the comments (docblocks) on the lines just above a
; definition (the extractor's default), signatures its text up to the body.
;
; Namespaces: `namespace A\B;` is the file's package; a braced
; `namespace A\B { ... }` is a namespace definition holding its contents
; (a braced file may have several, and the global `namespace { ... }` has
; no name, so what it holds is unqualified).
;
; Visibility: top-level declarations are exported; a member is exported
; when it is public, explicitly or by having no visibility modifier
; (scope.exported: declared).
;
; Known limits: members of anonymous classes are not definitions (as
; closures are not); a function declared inside a function body nests under
; it although PHP declares it globally; a promoted constructor property
; nests under __construct. Qualified references keep their namespace part
; as the qualifier (`App\Support` in `\App\Support\sum()`, `\` for a single
; leading backslash, `namespace` for `namespace\f()`), without the leading
; backslash of a longer name. gotreesitter does not match the repeated
; `prefix:` field of a qualified name or an anchor next to an anonymous
; node, so these patterns use neither.

; ---- namespace and imports

(namespace_definition
  name: (namespace_name) @package
  !body)

; use A\B;  use A\B as C;  use A\B, C\D;
(namespace_use_declaration
  !type
  (namespace_use_clause
    !type
    . [(qualified_name) (name)] @import.spec
    alias: (name)? @import.alias) @import)

; use function A\f;  use const A\C;
(namespace_use_declaration
  (namespace_use_clause
    type: "function"
    . [(qualified_name) (name)] @import.spec
    alias: (name)? @import.alias) @import
  (#set! import.kind "function"))

(namespace_use_declaration
  (namespace_use_clause
    type: "const"
    . [(qualified_name) (name)] @import.spec
    alias: (name)? @import.alias) @import
  (#set! import.kind "const"))

; use A\{B, C as D};  one import per clause: the group's prefix and the name
(namespace_use_declaration
  !type
  (namespace_name) @import.spec
  body: (namespace_use_group
    (namespace_use_clause
      !type
      . (name) @import.name
      alias: (name)? @import.alias) @import))

; use function A\{f, g as h};  use const A\{X, Y as Z};
(namespace_use_declaration
  type: "function"
  (namespace_name) @import.spec
  body: (namespace_use_group
    (namespace_use_clause
      . (name) @import.name
      alias: (name)? @import.alias) @import)
  (#set! import.kind "function"))

(namespace_use_declaration
  type: "const"
  (namespace_name) @import.spec
  body: (namespace_use_group
    (namespace_use_clause
      . (name) @import.name
      alias: (name)? @import.alias) @import)
  (#set! import.kind "const"))

; use A\{function f, const X};  a kind per clause in a mixed group
(namespace_use_declaration
  !type
  (namespace_name) @import.spec
  body: (namespace_use_group
    (namespace_use_clause
      type: "function"
      . (name) @import.name
      alias: (name)? @import.alias) @import)
  (#set! import.kind "function"))

(namespace_use_declaration
  !type
  (namespace_name) @import.spec
  body: (namespace_use_group
    (namespace_use_clause
      type: "const"
      . (name) @import.name
      alias: (name)? @import.alias) @import)
  (#set! import.kind "const"))

; require / include: the spec is the path expression (a string literal
; unquoted, else its text, `__DIR__ . '/x.php'`), inside parentheses too.
(require_expression
  (_) @import.spec
  (#not-match? @import.spec "^\\(")) @import
  (#set! import.kind "require")
(require_expression
  (parenthesized_expression (_) @import.spec)) @import
  (#set! import.kind "require")

(require_once_expression
  (_) @import.spec
  (#not-match? @import.spec "^\\(")) @import
  (#set! import.kind "require_once")
(require_once_expression
  (parenthesized_expression (_) @import.spec)) @import
  (#set! import.kind "require_once")

(include_expression
  (_) @import.spec
  (#not-match? @import.spec "^\\(")) @import
  (#set! import.kind "include")
(include_expression
  (parenthesized_expression (_) @import.spec)) @import
  (#set! import.kind "include")

(include_once_expression
  (_) @import.spec
  (#not-match? @import.spec "^\\(")) @import
  (#set! import.kind "include_once")
(include_once_expression
  (parenthesized_expression (_) @import.spec)) @import
  (#set! import.kind "include_once")

; ---- types and functions (top level, or in a braced namespace or a block)

(namespace_definition
  name: (namespace_name) @name
  body: (compound_statement)) @definition.namespace @exported

(class_declaration
  name: (name) @name) @definition.class @exported

(interface_declaration
  name: (name) @name) @definition.interface @exported

(trait_declaration
  name: (name) @name) @definition.trait @exported

(enum_declaration
  name: (name) @name) @definition.enum @exported

(function_definition
  name: (name) @name) @definition.function @exported

; const X = 1;  outside a class
(const_declaration
  (const_element (name) @name)
  (#not-has-parent? @definition.constant declaration_list enum_declaration_list)) @definition.constant @exported

; define('X', 1);  a defining form (profile definingForms)
(function_call_expression
  function: (name) @form
  arguments: (arguments
    . (argument (string (string_content) @name)))
  (#match? @form "^[dD][eE][fF][iI][nN][eE]$")) @definition.form @exported

; ---- class, interface, trait and enum members (not anonymous classes')
;
; Each member kind has three patterns: public (exported), no visibility
; modifier (public by default, exported; `var $x` too) and private or
; protected.

((declaration_list
  (method_declaration
    (visibility_modifier) @_vis
    name: (name) @name
    (#eq? @_vis "public")) @definition.method @exported) @_body
  (#not-has-parent? @_body anonymous_class))

((declaration_list
  (method_declaration
    (visibility_modifier)* @_vis
    name: (name) @name
    (#count? @_vis == 0)) @definition.method @exported) @_body
  (#not-has-parent? @_body anonymous_class))

((declaration_list
  (method_declaration
    (visibility_modifier) @_vis
    name: (name) @name
    (#any-of? @_vis "private" "protected")) @definition.method) @_body
  (#not-has-parent? @_body anonymous_class))

(enum_declaration_list
  (method_declaration
    (visibility_modifier) @_vis
    name: (name) @name
    (#eq? @_vis "public")) @definition.method @exported)

(enum_declaration_list
  (method_declaration
    (visibility_modifier)* @_vis
    name: (name) @name
    (#count? @_vis == 0)) @definition.method @exported)

(enum_declaration_list
  (method_declaration
    (visibility_modifier) @_vis
    name: (name) @name
    (#any-of? @_vis "private" "protected")) @definition.method)

(enum_declaration_list
  (enum_case
    name: (name) @name) @definition.constant @exported)

; Class constants: the declaration is the node (it carries the visibility),
; one definition per element.
((declaration_list
  (const_declaration
    (visibility_modifier) @_vis
    (const_element (name) @name)
    (#eq? @_vis "public")) @definition.constant @exported) @_body
  (#not-has-parent? @_body anonymous_class))

((declaration_list
  (const_declaration
    (visibility_modifier)* @_vis
    (const_element (name) @name)
    (#count? @_vis == 0)) @definition.constant @exported) @_body
  (#not-has-parent? @_body anonymous_class))

((declaration_list
  (const_declaration
    (visibility_modifier) @_vis
    (const_element (name) @name)
    (#any-of? @_vis "private" "protected")) @definition.constant) @_body
  (#not-has-parent? @_body anonymous_class))

(enum_declaration_list
  (const_declaration
    (visibility_modifier) @_vis
    (const_element (name) @name)
    (#eq? @_vis "public")) @definition.constant @exported)

(enum_declaration_list
  (const_declaration
    (visibility_modifier)* @_vis
    (const_element (name) @name)
    (#count? @_vis == 0)) @definition.constant @exported)

(enum_declaration_list
  (const_declaration
    (visibility_modifier) @_vis
    (const_element (name) @name)
    (#any-of? @_vis "private" "protected")) @definition.constant)

; Properties, one per element.
((declaration_list
  (property_declaration
    (visibility_modifier) @_vis
    (property_element name: (variable_name (name) @name))
    (#eq? @_vis "public")) @definition.property @exported) @_body
  (#not-has-parent? @_body anonymous_class))

((declaration_list
  (property_declaration
    (visibility_modifier)* @_vis
    (property_element name: (variable_name (name) @name))
    (#count? @_vis == 0)) @definition.property @exported) @_body
  (#not-has-parent? @_body anonymous_class))

((declaration_list
  (property_declaration
    (visibility_modifier) @_vis
    (property_element name: (variable_name (name) @name))
    (#any-of? @_vis "private" "protected")) @definition.property) @_body
  (#not-has-parent? @_body anonymous_class))

; Promoted constructor properties.
(property_promotion_parameter
  visibility: (visibility_modifier) @_vis
  name: (variable_name (name) @name)
  (#eq? @_vis "public")
  (#not-has-ancestor? @_vis anonymous_class)) @definition.property @exported

(property_promotion_parameter
  !visibility
  name: (variable_name (name) @name)
  (#not-has-ancestor? @name anonymous_class)) @definition.property @exported

(property_promotion_parameter
  visibility: (visibility_modifier) @_vis
  name: (variable_name (name) @name)
  (#any-of? @_vis "private" "protected")
  (#not-has-ancestor? @_vis anonymous_class)) @definition.property

; ---- references: calls

(function_call_expression
  function: (name) @name
  (#not-match? @name "^[dD][eE][fF][iI][nN][eE]$")) @reference.call

(function_call_expression
  function: (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.call

(function_call_expression
  function: (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.call

; namespace\f() is relative to the current namespace.
(function_call_expression
  function: (relative_name
    "namespace" @qualifier
    (name) @name) @_relative
  (#match? @_relative "^namespace\\s*\\\\\\s*[^\\\\]+$")) @reference.call

(function_call_expression
  function: (relative_name
    (namespace_name) @qualifier
    (name) @name)) @reference.call

(member_call_expression
  object: (_) @qualifier
  name: (name) @name) @reference.call

(nullsafe_member_call_expression
  object: (_) @qualifier
  name: (name) @name) @reference.call

(scoped_call_expression
  scope: (_) @qualifier
  name: (name) @name) @reference.call

; ---- references: types (declarations, new, instanceof, attributes)

(named_type
  (name) @name
  (#not-any-of? @name "self" "static" "parent")) @reference.type

(named_type
  (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.type

(named_type
  (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.type

(object_creation_expression
  (name) @name
  (#not-any-of? @name "self" "static" "parent")) @reference.type

(object_creation_expression
  (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.type

(object_creation_expression
  (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.type

(binary_expression
  operator: "instanceof"
  right: (name) @name
  (#not-any-of? @name "self" "static" "parent")) @reference.type

(binary_expression
  operator: "instanceof"
  right: (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.type

(binary_expression
  operator: "instanceof"
  right: (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.type

(attribute
  (name) @name) @reference.type

(attribute
  (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.type

(attribute
  (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.type

; ---- references: inheritance (extends, implements, trait use)

(base_clause
  (name) @name) @reference.extends

(base_clause
  (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.extends

(base_clause
  (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.extends

(class_interface_clause
  (name) @name) @reference.implements

(class_interface_clause
  (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.implements

(class_interface_clause
  (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.implements

(use_declaration
  (name) @name) @reference.mixin

(use_declaration
  (qualified_name
    (namespace_name) @qualifier
    (name) @name)) @reference.mixin

(use_declaration
  (qualified_name
    "\\" @qualifier
    (name) @name) @_global
  (#match? @_global "^\\\\[^\\\\]+$")) @reference.mixin

; ---- references: class constants and enum cases (A::X, self::X)

(class_constant_access_expression
  (_) @qualifier
  (name) @name .
  (#not-eq? @name "class")) @reference.constant
