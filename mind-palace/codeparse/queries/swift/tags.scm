; Swift: definitions, references and imports, in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md).
;
; The grammar writes class, struct, enum, actor and extension as one
; class_declaration told apart by its declaration_kind, and puts a
; function's return type in field `name` too, so names are matched by node
; type. Only top-level and member declarations are definitions, never
; locals. A definition is exported when it is declared public or open (a
; public extension's members and a public protocol's requirements are
; public). An extension is named by the type it extends, so its members
; qualify as that type's. Doc comments are the /// comments on the lines
; just above a definition (the extractor's default), signatures its text up
; to the body, or its first line.

; ---- imports

; import Foundation, import Foo.Bar (a submodule)
((import_declaration
  (identifier) @import.spec) @import
  (#not-match? @import "import[ \t]+(typealias|struct|class|enum|protocol|let|var|func)[ \t]"))

; import struct Swift.Array: the module, and the one symbol it names
(import_declaration
  ["typealias" "struct" "class" "enum" "protocol" "let" "var" "func"]
  (identifier
    . (simple_identifier) @import.spec
    (simple_identifier) @import.name .)) @import

; @_exported import M re-exports M; @testable import M sees its internals.
(import_declaration
  (modifiers
    (attribute
      (user_type (type_identifier) @_attr)
      (#eq? @_attr "_exported")))
  (identifier) @import.spec
  (#set! import.kind "reexport")) @import

(import_declaration
  (modifiers
    (attribute
      (user_type (type_identifier) @_attr)
      (#eq? @_attr "testable")))
  (identifier) @import.spec
  (#set! import.kind "testable")) @import

; ---- types: class, struct, enum, actor, extension, protocol

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "class"
  name: (type_identifier) @name) @definition.class

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "struct"
  name: (type_identifier) @name) @definition.struct

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "enum"
  name: (type_identifier) @name) @definition.enum

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "actor"
  name: (type_identifier) @name) @definition.actor

; extension Point, extension Outer.Inner: the whole type is the name.
(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  name: (user_type) @name) @definition.extension

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  name: (type_identifier) @name) @definition.protocol

; ---- top-level functions, operators included

(source_file
  (function_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: (simple_identifier) @name) @definition.function)

(source_file
  (function_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: _ @name
    (#match? @name "^[-+*/%=!<>&|^~?.]")) @definition.function)

; ---- members of classes, structs and actors

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (function_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: (simple_identifier) @name) @definition.method))

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (function_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: _ @name
      (#match? @name "^[-+*/%=!<>&|^~?.]")) @definition.method))

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (init_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      "init" @name) @definition.method))

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (deinit_declaration
      "deinit" @name) @definition.method))

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (subscript_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      "subscript" @name) @definition.method))

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (property_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: (pattern
        bound_identifier: (simple_identifier) @name)) @definition.property))

(class_declaration
  declaration_kind: ["class" "struct" "actor"]
  body: (class_body
    (typealias_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: (type_identifier) @name) @definition.type))

; ---- members of enums

(enum_class_body
  (function_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: (simple_identifier) @name) @definition.method)

(enum_class_body
  (function_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: _ @name
    (#match? @name "^[-+*/%=!<>&|^~?.]")) @definition.method)

(enum_class_body
  (init_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    "init" @name) @definition.method)

(enum_class_body
  (deinit_declaration
    "deinit" @name) @definition.method)

(enum_class_body
  (subscript_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    "subscript" @name) @definition.method)

(enum_class_body
  (property_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: (pattern
      bound_identifier: (simple_identifier) @name)) @definition.property)

(enum_class_body
  (typealias_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: (type_identifier) @name) @definition.type)

; ---- members of extensions: a public extension's members are public

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (function_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: (simple_identifier) @name) @definition.method))

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (function_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: _ @name
      (#match? @name "^[-+*/%=!<>&|^~?.]")) @definition.method))

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (init_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      "init" @name) @definition.method))

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (deinit_declaration
      "deinit" @name) @definition.method))

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (subscript_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      "subscript" @name) @definition.method))

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (property_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: (pattern
        bound_identifier: (simple_identifier) @name)) @definition.property))

(class_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  declaration_kind: "extension"
  body: (class_body
    (typealias_declaration
      (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
      name: (type_identifier) @name) @definition.type))

; ---- requirements of protocols: as public as the protocol

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (protocol_function_declaration
      name: (simple_identifier) @name) @definition.method))

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (protocol_function_declaration
      name: _ @name
      (#match? @name "^[-+*/%=!<>&|^~?.]")) @definition.method))

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (init_declaration
      "init" @name) @definition.method))

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (subscript_declaration
      "subscript" @name) @definition.method))

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (protocol_property_declaration
      name: (pattern
        bound_identifier: (simple_identifier) @name)) @definition.property))

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (typealias_declaration
      name: (type_identifier) @name) @definition.type))

(protocol_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  body: (protocol_body
    (associatedtype_declaration
      name: (type_identifier) @name) @definition.associatedtype))

; ---- enum cases, one per name (case a, b)

(enum_entry
  name: (simple_identifier) @name) @definition.case

; ---- top-level constants, variables, type aliases and macros

(source_file
  (property_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    (value_binding_pattern mutability: "let")
    name: (pattern
      bound_identifier: (simple_identifier) @name)) @definition.constant)

(source_file
  (property_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    (value_binding_pattern mutability: "var")
    name: (pattern
      bound_identifier: (simple_identifier) @name)) @definition.variable)

(source_file
  (typealias_declaration
    (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
    name: (type_identifier) @name) @definition.type)

(macro_declaration
  (modifiers (visibility_modifier) @exported (#any-of? @exported "public" "open"))?
  (simple_identifier) @name) @definition.macro

; ---- inheritance: the superclass and conformances
;
; A class's first entry is its superclass (in a class without one, its
; first protocol: the resolver tells them apart by the target's kind); the
; other entries, and every entry of a struct, enum, actor or extension,
; are protocols it conforms to; a protocol extends the protocols it lists.
; Standard-library types (an enum's raw type) are left out. One pattern
; per shape of the type: a plain name, a generic one, a qualified one.

(class_declaration
  declaration_kind: "class"
  name: (type_identifier)
  .
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  name: (type_identifier)
  .
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name . (type_arguments) .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  name: (type_identifier)
  .
  (inheritance_specifier
    inherits_from: (user_type (type_identifier) @qualifier . (type_identifier) @name .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  (type_parameters)
  .
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  (type_parameters)
  .
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name . (type_arguments) .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  (type_parameters)
  .
  (inheritance_specifier
    inherits_from: (user_type (type_identifier) @qualifier . (type_identifier) @name .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  (inheritance_specifier)
  .
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name .)) @reference.implements
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  (inheritance_specifier)
  .
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name . (type_arguments) .)) @reference.implements
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: "class"
  (inheritance_specifier)
  .
  (inheritance_specifier
    inherits_from: (user_type (type_identifier) @qualifier . (type_identifier) @name .)) @reference.implements
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: ["struct" "enum" "actor" "extension"]
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name .)) @reference.implements
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: ["struct" "enum" "actor" "extension"]
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name . (type_arguments) .)) @reference.implements
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(class_declaration
  declaration_kind: ["struct" "enum" "actor" "extension"]
  (inheritance_specifier
    inherits_from: (user_type (type_identifier) @qualifier . (type_identifier) @name .)) @reference.implements
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(protocol_declaration
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(protocol_declaration
  (inheritance_specifier
    inherits_from: (user_type . (type_identifier) @name . (type_arguments) .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

(protocol_declaration
  (inheritance_specifier
    inherits_from: (user_type (type_identifier) @qualifier . (type_identifier) @name .)) @reference.extends
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"))

; ---- calls: f(x) and f { } (not the subscript a[i]), qualified by what
; they are reached through when that is a name, a member, self or super;
; Point(x:y:) is a call to the initializer.

(call_expression
  (simple_identifier) @name
  (call_suffix (value_arguments "("))
  (#not-any-of? @name
    "print" "debugPrint" "dump" "fatalError" "precondition"
    "preconditionFailure" "assert" "assertionFailure" "min" "max" "abs"
    "zip" "stride" "type" "swap" "repeatElement" "numericCast"
    "withExtendedLifetime" "withUnsafePointer" "withUnsafeMutablePointer"
    "withUnsafeBytes" "withUnsafeMutableBytes" "unsafeBitCast"
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error")) @reference.call

(call_expression
  (simple_identifier) @name
  (call_suffix (lambda_literal))) @reference.call

(call_expression
  (navigation_expression
    target: [(simple_identifier) (navigation_expression) (self_expression) (super_expression)] @qualifier
    suffix: (navigation_suffix
      suffix: (simple_identifier) @name))
  (call_suffix (value_arguments "("))) @reference.call

(call_expression
  (navigation_expression
    target: [(simple_identifier) (navigation_expression) (self_expression) (super_expression)] @qualifier
    suffix: (navigation_suffix
      suffix: (simple_identifier) @name))
  (call_suffix (lambda_literal))) @reference.call

(call_expression
  (navigation_expression
    suffix: (navigation_suffix
      suffix: (simple_identifier) @name))
  (call_suffix (value_arguments "("))) @reference.call

(call_expression
  (navigation_expression
    suffix: (navigation_suffix
      suffix: (simple_identifier) @name))
  (call_suffix (lambda_literal))) @reference.call

; Freestanding macros: #stringify(x), #Preview { }
(macro_invocation
  (simple_identifier) @name) @reference.call

; ---- types used (not standard-library types or built-in attributes)

((user_type
  . (type_identifier) @name) @reference.type
  (#not-any-of? @name
    "Int" "Int8" "Int16" "Int32" "Int64" "UInt" "UInt8" "UInt16"
    "UInt32" "UInt64" "Float" "Double" "Bool" "String" "Character" "Void"
    "Any" "AnyObject" "Self" "Never" "Optional" "Array" "Dictionary" "Set"
    "Error"
    "available" "discardableResult" "escaping" "autoclosure" "frozen"
    "inlinable" "usableFromInline" "main" "objc" "objcMembers" "nonobjc"
    "testable" "_exported" "propertyWrapper" "resultBuilder"
    "dynamicMemberLookup" "dynamicCallable" "freestanding" "attached"
    "preconcurrency" "unknown" "IBAction" "IBOutlet" "IBDesignable"
    "IBInspectable" "NSManaged" "NSCopying" "GKInspectable" "UIApplicationMain"
    "NSApplicationMain"))

(user_type
  (type_identifier) @qualifier
  .
  (type_identifier) @name) @reference.type
