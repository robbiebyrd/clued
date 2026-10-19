; Java: classes, interfaces, enums, records, annotation types and the
; module of a module-info.java; their methods, constructors, fields and
; constants; references (calls, object creation, types, extends and
; implements, annotations, field accesses and method references); imports
; and the package clause, in tree-sitter's tags-query convention plus
; clued's captures (see ../README.md).
;
; Doc comments are the Javadoc (or other) comments on the lines just above
; a definition (the extractor's default: annotations belong to the
; definition's modifiers, so the comment above them is its doc).
; Signatures are the definition's text up to its body, modifiers and
; annotations included. A definition is exported when it is declared
; public or protected, or is implicitly public (interface and annotation
; type members, enum constants, record components). A record component
; written on the record's first line takes the record's doc, where Javadoc
; documents components (@param).

; ---- package and imports

(package_declaration
  [(identifier) (scoped_identifier)] @package)

; import a.b.C; (the simple name C is the import's name)
(import_declaration
  . [(identifier) @import.name
     (scoped_identifier name: (identifier) @import.name)] @import.spec .) @import
  (#not-match? @import "^import\\s+static\\b")

; import a.b.*;
(import_declaration
  . [(identifier) (scoped_identifier)] @import.spec
  . (asterisk) .) @import
  (#not-match? @import "^import\\s+static\\b")
  (#set! import.kind "on_demand")

; import static a.b.C.m;
(import_declaration
  "static" . [(identifier) @import.name
              (scoped_identifier name: (identifier) @import.name)] @import.spec .) @import
  (#set! import.kind "static")

; import static a.b.C.*;
(import_declaration
  "static" . [(identifier) (scoped_identifier)] @import.spec
  . (asterisk) .) @import
  (#set! import.kind "static_on_demand")

; module-info.java: requires is an import of a module.
(requires_module_directive
  module: [(identifier) (scoped_identifier)] @import.spec) @import
  (#set! import.kind "requires")

; ---- module-info.java: the module (its name is dotted, never qualified)

(module_declaration
  name: [(identifier) (scoped_identifier)] @name) @definition.module @exported

; ---- types (top-level, nested, local and inside enums)

(class_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.class

(interface_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.interface

(enum_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.enum

(record_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.record

(annotation_type_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.annotation

; ---- members of classes, enums and records

(class_body
  (method_declaration
    (modifiers ["public" "protected"] @exported)?
    name: (identifier) @name) @definition.method)

(enum_body_declarations
  (method_declaration
    (modifiers ["public" "protected"] @exported)?
    name: (identifier) @name) @definition.method)

(constructor_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.constructor

; A record's compact constructor: public Point { ... }
(compact_constructor_declaration
  (modifiers ["public" "protected"] @exported)?
  name: (identifier) @name) @definition.constructor

(class_body
  (field_declaration
    (modifiers ["public" "protected"] @exported)?
    declarator: (variable_declarator
      name: (identifier) @name)) @definition.field)

(enum_body_declarations
  (field_declaration
    (modifiers ["public" "protected"] @exported)?
    declarator: (variable_declarator
      name: (identifier) @name)) @definition.field)

(enum_body
  (enum_constant
    name: (identifier) @name) @definition.constant @exported)

; Record components: each has a public accessor of its name.
(record_declaration
  parameters: (formal_parameters
    (formal_parameter
      name: (identifier) @name) @definition.field @exported))

; ---- members of interfaces and annotation types (implicitly public)

(interface_body
  (method_declaration
    name: (identifier) @name) @definition.method @exported)

(interface_body
  (constant_declaration
    declarator: (variable_declarator
      name: (identifier) @name)) @definition.constant @exported)

(annotation_type_body
  (annotation_type_element_declaration
    name: (identifier) @name) @definition.method @exported)

(annotation_type_body
  (constant_declaration
    declarator: (variable_declarator
      name: (identifier) @name)) @definition.constant @exported)

; ---- references

; Calls: m(), obj.m(), Type.m(), super.m().
(method_invocation
  !object
  name: (identifier) @name) @reference.call

(method_invocation
  object: (_) @qualifier
  name: (identifier) @name) @reference.call

; Object creation calls the type's constructor: new Foo<>(), new a.b.Foo().
(object_creation_expression
  type: [(type_identifier) @name
         (generic_type [(type_identifier) @name
                        (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)])
         (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)]) @reference.call

; Method references: Foo::bar, this::bar.
(method_reference
  . (_) @qualifier
  (identifier) @name .) @reference.call

; Inheritance: a class's superclass, the interfaces a class, enum or record
; implements, and those an interface extends.
(superclass
  [(type_identifier) @name
   (generic_type [(type_identifier) @name
                  (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)])
   (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)]) @reference.extends

(super_interfaces
  (type_list
    [(type_identifier) @name
     (generic_type [(type_identifier) @name
                    (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)])
     (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)] @reference.implements))

(extends_interfaces
  (type_list
    [(type_identifier) @name
     (generic_type [(type_identifier) @name
                    (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)])
     (scoped_type_identifier (_) @qualifier . (type_identifier) @name .)] @reference.extends))

; Annotations name a type: @Override, @a.b.Audited(level = 1).
(marker_annotation
  name: (identifier) @name) @reference.type

(annotation
  name: (identifier) @name) @reference.type

(marker_annotation
  name: (scoped_identifier
    scope: (_) @qualifier
    name: (identifier) @name)) @reference.type

(annotation
  name: (scoped_identifier
    scope: (_) @qualifier
    name: (identifier) @name)) @reference.type

; module-info.java: the services a module uses and provides, and the
; classes that provide them.
(uses_module_directive
  type: [(identifier) @name
         (scoped_identifier scope: (_) @qualifier name: (identifier) @name)]) @reference.type

(provides_module_directive
  [(identifier) @name
   (scoped_identifier scope: (_) @qualifier name: (identifier) @name)] @reference.type)

; Types: a.b.Foo, Map.Entry, and every simple type name. A type name in
; lower case is a package of a qualified name (java in java.io.File) by
; Java's naming convention, or the keyword var, and is left out.
(scoped_type_identifier
  (_) @qualifier
  . (type_identifier) @name .
  (#not-match? @name "^[a-z]")) @reference.type

((type_identifier) @name @reference.type
  (#not-match? @name "^[a-z]"))

; Field accesses that name a field: this.count, a constant or static field
; (Status.IN_STOCK, Config.timeout). Other accesses (order.items,
; java.util) are left out: their object's type, or whether they are a
; package, is not known here.
(field_access
  object: (this) @qualifier
  field: (identifier) @name) @reference.field

(field_access
  object: (_) @qualifier
  field: (identifier) @name
  (#match? @name "^[A-Z]")) @reference.field

(field_access
  object: (identifier) @qualifier
  field: (identifier) @name
  (#match? @qualifier "^[A-Z]")) @reference.field
