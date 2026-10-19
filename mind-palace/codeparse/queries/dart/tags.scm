; Dart (and Flutter, which is Dart): definitions, references, imports and
; the library name, in tree-sitter's tags-query convention plus clued's
; captures (see ../README.md).
;
; The grammar writes a function, method or constructor with a body as two
; siblings, its signature and then its function_body, with nothing around
; both. Such a definition is captured on its function_body, so that it
; holds what its body holds (local functions, the references it encloses,
; its chunk); its @name and @signature come from the signature just before
; it, and its start is where the body starts. A definition without a body
; (abstract, external, a constructor that only initialises, a field) is
; captured on its declaration.
;
; Docs are the extractor's default, the comments on the lines just above
; the definition: a member's annotations (@override) stand between them,
; and so does the first line of a signature cut over several lines, so
; those definitions get no doc. Capturing the doc comments and annotations
; before a signature takes patterns of three or more siblings, some
; quantified, which gotreesitter v0.56.0 matches in time quadratic in the
; number of a class's or file's members (it collects every later candidate
; sibling before it checks an anchor) and drops past its work budget. So
; every definition pattern starts at a named parent (a pattern starting at
; any node is tried at every node of the tree, at twenty times the cost)
; and spans at most two siblings. gotreesitter can also give every match of
; an alternation whose branches capture @name the first branch's name, so
; each signature has a pattern of its own.

; ---- library name and directives

(library_name (dotted_identifier_list) @package)

; `part of a.b;` names the library a part belongs to, so a part's names
; are qualified like its library's.
(part_of_directive (dotted_identifier_list) @package)

; Every pattern of an import captures its URI, so that the matches of one
; directive merge: the URI, the `as` prefix, `deferred`, the `show` names
; (`hide` names are not imported).
(library_import (import_specification (configurable_uri . (uri) @import.spec))) @import
(library_import (import_specification (uri) @import.spec)) @import
(library_import (import_specification (configurable_uri . (uri) @import.spec) (identifier) @import.alias)) @import
(library_import (import_specification (uri) @import.spec (identifier) @import.alias)) @import
(library_import (import_specification (uri) @import.spec "deferred" (#set! import.kind "deferred"))) @import
(library_import (import_specification (configurable_uri . (uri) @import.spec) (combinator "show" (identifier) @import.name))) @import
(library_import (import_specification (uri) @import.spec (combinator "show" (identifier) @import.name))) @import

(library_export (configurable_uri . (uri) @import.spec) (#set! import.kind "export")) @import
(library_export (uri) @import.spec (#set! import.kind "export")) @import
(library_export (configurable_uri . (uri) @import.spec) (combinator "show" (identifier) @import.name) (#set! import.kind "export")) @import
(library_export (uri) @import.spec (combinator "show" (identifier) @import.name) (#set! import.kind "export")) @import

; The URI a conditional import or export takes on some platforms:
; `import 'stub.dart' if (dart.library.io) 'io.dart';`.
(configuration_uri (uri) @import.spec (#set! import.kind "conditional")) @import

(part_directive (uri) @import.spec (#set! import.kind "part")) @import
(part_of_directive (uri) @import.spec (#set! import.kind "part_of")) @import

; ---- types (their annotations are inside them, so the default doc works)

(class_definition
  name: (identifier) @name) @definition.class

(mixin_declaration
  (identifier) @name) @definition.mixin

(extension_declaration
  name: (identifier) @name) @definition.extension

; extension type Meters(int value): a class over its representation field.
(extension_type_declaration
  name: (identifier) @name) @definition.class

(extension_type_declaration
  representation: (representation_declaration
    name: (identifier) @name) @definition.field)

(enum_declaration
  name: (identifier) @name) @definition.enum

(enum_constant
  name: (identifier) @name) @definition.constant

; typedef Name<T> = Type;
(type_alias
  .
  (type_identifier) @name
  "=") @definition.type

; typedef int Compare(int a, int b);
(type_alias
  (type_identifier) @name
  .
  (type_parameters)?
  .
  (formal_parameter_list)) @definition.type

; ---- top-level functions, getters and setters

(program
  (function_signature
    name: (identifier) @name) @signature
  .
  (function_body) @definition.function)

(program
  (getter_signature
    name: (identifier) @name) @signature
  .
  (function_body) @definition.property)

(program
  (setter_signature
    name: (identifier) @name) @signature
  .
  (function_body) @definition.property)

; external functions: a signature and no body.
(program
  (function_signature
    name: (identifier) @name) @definition.function
  .
  ";")

; Local functions, nested in the function or method around them.
(local_function_declaration
  (lambda_expression
    parameters: (function_signature
      name: (identifier) @name) @signature)) @definition.function

; ---- members with a body: methods and operators, getters and setters,
; constructors, in classes, mixins and extension types (class_body),
; extensions and enums. A constructor is named by its last name, Shape in
; Shape(...) and circle in Shape.circle(...), so it reads Shape.circle
; (extensions have no constructors).

(class_body (method_signature (function_signature name: (identifier) @name)) @signature
  . (function_body) @definition.method)
(class_body (method_signature (operator_signature [(binary_operator) "~" "[]" "[]="] @name)) @signature
  . (function_body) @definition.method)
(class_body (method_signature (getter_signature name: (identifier) @name)) @signature
  . (function_body) @definition.property)
(class_body (method_signature (setter_signature name: (identifier) @name)) @signature
  . (function_body) @definition.property)
(class_body (method_signature (constructor_signature (identifier) @name . (formal_parameter_list))) @signature
  . (function_body) @definition.constructor)
(class_body (method_signature (factory_constructor_signature (identifier) @name . (formal_parameter_list))) @signature
  . (function_body) @definition.constructor)
(class_body (method_signature (constant_constructor_signature (identifier) @name . (formal_parameter_list))) @signature
  . (function_body) @definition.constructor)

(extension_body (method_signature (function_signature name: (identifier) @name)) @signature
  . (function_body) @definition.method)
(extension_body (method_signature (operator_signature [(binary_operator) "~" "[]" "[]="] @name)) @signature
  . (function_body) @definition.method)
(extension_body (method_signature (getter_signature name: (identifier) @name)) @signature
  . (function_body) @definition.property)
(extension_body (method_signature (setter_signature name: (identifier) @name)) @signature
  . (function_body) @definition.property)

(enum_body (method_signature (function_signature name: (identifier) @name)) @signature
  . (function_body) @definition.method)
(enum_body (method_signature (operator_signature [(binary_operator) "~" "[]" "[]="] @name)) @signature
  . (function_body) @definition.method)
(enum_body (method_signature (getter_signature name: (identifier) @name)) @signature
  . (function_body) @definition.property)
(enum_body (method_signature (setter_signature name: (identifier) @name)) @signature
  . (function_body) @definition.property)
(enum_body (method_signature (constructor_signature (identifier) @name . (formal_parameter_list))) @signature
  . (function_body) @definition.constructor)
(enum_body (method_signature (factory_constructor_signature (identifier) @name . (formal_parameter_list))) @signature
  . (function_body) @definition.constructor)
(enum_body (method_signature (constant_constructor_signature (identifier) @name . (formal_parameter_list))) @signature
  . (function_body) @definition.constructor)

; ---- members without a body, captured on their declaration

(declaration
  (function_signature
    name: (identifier) @name)) @definition.method

(declaration
  (operator_signature
    [(binary_operator) "~" "[]" "[]="] @name)) @definition.method

(declaration
  (getter_signature
    name: (identifier) @name)) @definition.property

(declaration
  (setter_signature
    name: (identifier) @name)) @definition.property

(declaration
  (constructor_signature
    (identifier) @name
    .
    (formal_parameter_list))) @definition.constructor

(declaration
  (factory_constructor_signature
    (identifier) @name
    .
    (formal_parameter_list))) @definition.constructor

(declaration
  (constant_constructor_signature
    (identifier) @name
    .
    (formal_parameter_list))) @definition.constructor

(declaration
  (redirecting_factory_constructor_signature
    (identifier) @name
    .
    (formal_parameter_list))) @definition.constructor

; Fields, one per name (`int _x, y;`), with the whole declaration as their
; signature: `final String label;`, `static const max = 3;`.
(declaration
  (initialized_identifier_list
    (initialized_identifier
      .
      (identifier) @name) @definition.field)) @signature

(declaration
  (static_final_declaration_list
    (static_final_declaration
      .
      (identifier) @name) @definition.field)) @signature

; ---- top-level variables and constants (not locals): `const` and `final`
; ones, bound once, are constants; `var`, typed and `late` ones variables.

(program
  (static_final_declaration_list
    (static_final_declaration
      .
      (identifier) @name) @definition.constant))

(program
  (initialized_identifier_list
    (initialized_identifier
      .
      (identifier) @name) @definition.variable))

; ---- references

; Calls: f(x), f<T>(x), Widget(child: ...): the callee and its argument
; part are siblings.
((identifier) @name @reference.call
  .
  (selector (argument_part))
  (#not-any-of? @name "print" "identical"))

; Member calls: a.f(x), a?.f(x), a.b().f(x), this.f(x); the receiver is
; the qualifier when it is a name or this. The selector before the argument
; part is an unconditional (.f) or conditional (?.f) assignable selector.
((selector (_ (identifier) @name)) @reference.call
  .
  (selector (argument_part)))

([(identifier) (this)] @qualifier
  .
  (selector (_ (identifier) @name)) @reference.call
  .
  (selector (argument_part)))

; super.f(x) is a statement, a return value or an arrow body.
(expression_statement (super) @qualifier
  . (unconditional_assignable_selector (identifier) @name) @reference.call
  . (selector (argument_part)))
(return_statement (super) @qualifier
  . (unconditional_assignable_selector (identifier) @name) @reference.call
  . (selector (argument_part)))
(function_body (super) @qualifier
  . (unconditional_assignable_selector (identifier) @name) @reference.call
  . (selector (argument_part)))

; Cascades: b..add(x)..clear().
(cascade_section
  (cascade_selector (identifier) @name)
  .
  (argument_part)) @reference.call

; Instance creation: new Foo(), const Foo(), new Foo.named(),
; const EdgeInsets.all(8).
(new_expression (type_identifier) @name . (arguments)) @reference.call
(const_object_expression (type_identifier) @name . (arguments)) @reference.call
(new_expression (type_identifier) @qualifier . (identifier) @name) @reference.call
(const_object_expression (type_identifier) @qualifier . (identifier) @name) @reference.call

; Inheritance: extends, with, implements, and the superclass a mixin
; requires (mixin M on Base).
(superclass
  .
  (type_identifier) @name) @reference.extends

(mixins
  (type_identifier) @name) @reference.mixin

(mixin_declaration
  (type_identifier) @name) @reference.extends

(interfaces
  (type_identifier) @name) @reference.implements

; Types written with an import prefix: math.Point, io.File.
((type_identifier) @qualifier
  .
  "."
  .
  (type_identifier) @name @reference.type)

; Types, but for the dart:core ones nearly every file names. A type is
; UpperCamelCase (Dart's style), which also leaves out the lowercase
; prefix of math.Point.
((type_identifier) @name @reference.type
  (#match? @name "^_*[A-Z]")
  (#not-any-of? @name
    "Never" "Null" "Object" "String" "Function" "Record" "Symbol" "Type"
    "List" "Map" "Set" "Iterable" "Future"))
