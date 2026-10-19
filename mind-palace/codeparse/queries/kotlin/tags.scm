; Kotlin: definitions, references, imports and the package header, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). The grammar (fwcd/tree-sitter-kotlin) gives its nodes no
; field names, so every pattern matches positional children, and a
; definition's signature is its first line (the extractor's default). Doc
; comments (KDoc /** */ and // lines) are the comments on the lines just
; above a definition (the extractor's default too).
;
; Visibility: Kotlin declarations are public unless a modifier says
; otherwise, and only `private` keeps one inside its file or class
; (`internal` is visible across the module, `protected` to subclasses).
; Tree-sitter cannot match the absence of a modifier, so each definition is
; written twice: once marked @exported when its text does not start with a
; modifier list holding `private`, once unmarked when it does. The two
; regular expressions are the same, negated, so every definition matches
; exactly one of the two. A modifier list is annotations (@Name,
; @Name(...), @use:Name) and modifier keywords, which is what the grammar
; puts in a `modifiers` node ahead of the declaration's keyword; the same
; prefix tells an enum class, a `const val` and an interface apart.
;
; Local declarations (inside a function, accessor, initializer or lambda
; body) are not definitions: #not-has-ancestor? leaves out anything under a
; `statements` block or an expression body. A script (.kts) is a
; source_file of top-level statements and declarations: its declarations
; are found the same way, and its calls (plugins { id("java") }) are call
; references.
;
; gotreesitter v0.56.0 can report the first alternative's @name for every
; match of an alternation whose alternatives capture @name, so each shape
; of a name is its own pattern below (the @receiver alternation is not
; affected: one receiver per definition).

; ---- package and imports

(package_header (identifier) @package)

; import a.b.C, import a.b.C as D
(import_header
  (identifier) @import.spec
  (import_alias (type_identifier) @import.alias)?) @import

; import a.b.* : every member of the package (or class) a.b.
((import_header
  (identifier) @import.spec
  (wildcard_import)) @import
  (#set! import.kind "wildcard"))

; ---- classes, interfaces, enums, objects, type aliases

; class: data, sealed, abstract, open, inner, value, annotation (not enum).
((class_declaration "class" (type_identifier) @name) @definition.class @exported
  (#not-match? @definition.class "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*enum\\s")
  (#not-match? @definition.class "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.class statements function_body))
((class_declaration "class" (type_identifier) @name) @definition.class
  (#not-match? @definition.class "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*enum\\s")
  (#match? @definition.class "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.class statements function_body))

; interface, fun interface
((class_declaration "interface" (type_identifier) @name) @definition.interface @exported
  (#not-match? @definition.interface "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.interface statements function_body))
((class_declaration "interface" (type_identifier) @name) @definition.interface
  (#match? @definition.interface "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.interface statements function_body))

; enum class, and its entries (always public).
((class_declaration "enum" (type_identifier) @name) @definition.enum @exported
  (#not-match? @definition.enum "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.enum statements function_body))
((class_declaration "enum" (type_identifier) @name) @definition.enum
  (#match? @definition.enum "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.enum statements function_body))

(enum_class_body
  (enum_entry (simple_identifier) @name) @definition.constant @exported)

; object Name, and a named companion object. An unnamed companion is no
; symbol: its members belong to the class, as Kotlin calls them.
((object_declaration (type_identifier) @name) @definition.object @exported
  (#not-match? @definition.object "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.object statements function_body))
((object_declaration (type_identifier) @name) @definition.object
  (#match? @definition.object "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.object statements function_body))

((companion_object (type_identifier) @name) @definition.object @exported
  (#not-match? @definition.object "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))
((companion_object (type_identifier) @name) @definition.object
  (#match? @definition.object "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))

; typealias Name = ...
((type_alias (type_identifier) @name) @definition.type @exported
  (#not-match? @definition.type "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))
((type_alias (type_identifier) @name) @definition.type
  (#match? @definition.type "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))

; ---- functions

; Functions of a file are functions, those of a class, interface, object,
; enum or enum entry are methods. An extension (fun T.name()) names its
; receiver type: the last identifier of the receiver, nullable or generic,
; is @receiver, so the function's parent is that type when the file
; defines it at the top level, and its qualified name is
; <package>.<Type>.<name> otherwise.
((function_declaration
  (receiver_type
    [(user_type (type_identifier) @receiver .)
     (user_type (type_identifier) @receiver . (type_arguments))
     (nullable_type (user_type (type_identifier) @receiver .))
     (nullable_type (user_type (type_identifier) @receiver . (type_arguments)))])?
  (simple_identifier) @name) @definition.function @exported
  (#has-parent? @definition.function source_file)
  (#not-match? @definition.function "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))
((function_declaration
  (receiver_type
    [(user_type (type_identifier) @receiver .)
     (user_type (type_identifier) @receiver . (type_arguments))
     (nullable_type (user_type (type_identifier) @receiver .))
     (nullable_type (user_type (type_identifier) @receiver . (type_arguments)))])?
  (simple_identifier) @name) @definition.function
  (#has-parent? @definition.function source_file)
  (#match? @definition.function "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))

((function_declaration
  (receiver_type
    [(user_type (type_identifier) @receiver .)
     (user_type (type_identifier) @receiver . (type_arguments))
     (nullable_type (user_type (type_identifier) @receiver .))
     (nullable_type (user_type (type_identifier) @receiver . (type_arguments)))])?
  (simple_identifier) @name) @definition.method @exported
  (#has-parent? @definition.method class_body enum_class_body)
  (#not-match? @definition.method "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.method statements function_body))
((function_declaration
  (receiver_type
    [(user_type (type_identifier) @receiver .)
     (user_type (type_identifier) @receiver . (type_arguments))
     (nullable_type (user_type (type_identifier) @receiver .))
     (nullable_type (user_type (type_identifier) @receiver . (type_arguments)))])?
  (simple_identifier) @name) @definition.method
  (#has-parent? @definition.method class_body enum_class_body)
  (#match? @definition.method "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.method statements function_body))

; A grammar quirk: at the top level, an annotation with arguments on the
; same line as a modifier and `fun` (@Deprecated("x") private fun f() {})
; parses as a bare annotation followed by an infix expression
; (private fun f {}). The function is recovered from that shape.
((infix_expression
  . (simple_identifier) @_modifier
  . (simple_identifier) @_fun
  . (call_expression . (simple_identifier) @name)) @definition.function @exported
  (#has-parent? @definition.function source_file)
  (#any-of? @_modifier "public" "internal" "protected")
  (#eq? @_fun "fun"))
((infix_expression
  . (simple_identifier) @_modifier
  . (simple_identifier) @_fun
  . (call_expression . (simple_identifier) @name)) @definition.function
  (#has-parent? @definition.function source_file)
  (#eq? @_modifier "private")
  (#eq? @_fun "fun"))

; ---- properties

; Properties of a file, class, interface or object, with an extension
; property's receiver as for functions; `const val` is a constant.
((property_declaration
  (receiver_type
    [(user_type (type_identifier) @receiver .)
     (user_type (type_identifier) @receiver . (type_arguments))
     (nullable_type (user_type (type_identifier) @receiver .))
     (nullable_type (user_type (type_identifier) @receiver . (type_arguments)))])?
  (variable_declaration (simple_identifier) @name)) @definition.property @exported
  (#not-match? @definition.property "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*const\\s")
  (#not-match? @definition.property "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.property statements function_body))
((property_declaration
  (receiver_type
    [(user_type (type_identifier) @receiver .)
     (user_type (type_identifier) @receiver . (type_arguments))
     (nullable_type (user_type (type_identifier) @receiver .))
     (nullable_type (user_type (type_identifier) @receiver . (type_arguments)))])?
  (variable_declaration (simple_identifier) @name)) @definition.property
  (#not-match? @definition.property "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*const\\s")
  (#match? @definition.property "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.property statements function_body))

((property_declaration
  (variable_declaration (simple_identifier) @name)) @definition.constant @exported
  (#match? @definition.constant "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*const\\s")
  (#not-match? @definition.constant "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.constant statements function_body))
((property_declaration
  (variable_declaration (simple_identifier) @name)) @definition.constant
  (#match? @definition.constant "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*const\\s")
  (#match? @definition.constant "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s")
  (#not-has-ancestor? @definition.constant statements function_body))

; Primary-constructor parameters declared with val or var are properties
; of the class; plain parameters are not.
((class_parameter
  (binding_pattern_kind)
  (simple_identifier) @name) @definition.property @exported
  (#not-match? @definition.property "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))
((class_parameter
  (binding_pattern_kind)
  (simple_identifier) @name) @definition.property
  (#match? @definition.property "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*private\\s"))

; ---- inheritance

; A class or object names its superclass with a constructor call
; (: Base()), its interfaces bare (: Named) or delegated (: Store by db);
; an interface extends interfaces. Of a qualified type, the identifier
; before the last is the qualifier (io in java.io.Closeable, Outer in
; Outer.Inner). Each type shape is one pattern.
(delegation_specifier (constructor_invocation (user_type (type_identifier) @name .))) @reference.extends
(delegation_specifier (constructor_invocation (user_type (type_identifier) @name . (type_arguments)))) @reference.extends
(delegation_specifier (constructor_invocation (user_type (type_identifier) @qualifier . (type_identifier) @name .))) @reference.extends
(delegation_specifier (constructor_invocation (user_type (type_identifier) @qualifier . (type_identifier) @name . (type_arguments)))) @reference.extends
((class_declaration (delegation_specifier (user_type (type_identifier) @name .))) @reference.implements
  (#not-match? @reference.implements "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((class_declaration (delegation_specifier (user_type (type_identifier) @name . (type_arguments)))) @reference.implements
  (#not-match? @reference.implements "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((class_declaration (delegation_specifier (user_type (type_identifier) @qualifier . (type_identifier) @name .))) @reference.implements
  (#not-match? @reference.implements "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((class_declaration (delegation_specifier (user_type (type_identifier) @qualifier . (type_identifier) @name . (type_arguments)))) @reference.implements
  (#not-match? @reference.implements "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((delegation_specifier (user_type (type_identifier) @name .)) @reference.implements
  (#has-parent? @reference.implements object_declaration companion_object object_literal))
((delegation_specifier (user_type (type_identifier) @name . (type_arguments))) @reference.implements
  (#has-parent? @reference.implements object_declaration companion_object object_literal))
((delegation_specifier (user_type (type_identifier) @qualifier . (type_identifier) @name .)) @reference.implements
  (#has-parent? @reference.implements object_declaration companion_object object_literal))
((delegation_specifier (user_type (type_identifier) @qualifier . (type_identifier) @name . (type_arguments))) @reference.implements
  (#has-parent? @reference.implements object_declaration companion_object object_literal))
(delegation_specifier (explicit_delegation (user_type (type_identifier) @name .))) @reference.implements
(delegation_specifier (explicit_delegation (user_type (type_identifier) @name . (type_arguments)))) @reference.implements
(delegation_specifier (explicit_delegation (user_type (type_identifier) @qualifier . (type_identifier) @name .))) @reference.implements
(delegation_specifier (explicit_delegation (user_type (type_identifier) @qualifier . (type_identifier) @name . (type_arguments)))) @reference.implements
((class_declaration (delegation_specifier (user_type (type_identifier) @name .))) @reference.extends
  (#match? @reference.extends "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((class_declaration (delegation_specifier (user_type (type_identifier) @name . (type_arguments)))) @reference.extends
  (#match? @reference.extends "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((class_declaration (delegation_specifier (user_type (type_identifier) @qualifier . (type_identifier) @name .))) @reference.extends
  (#match? @reference.extends "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))
((class_declaration (delegation_specifier (user_type (type_identifier) @qualifier . (type_identifier) @name . (type_arguments)))) @reference.extends
  (#match? @reference.extends "^(?:@[^\\s(]+(?:\\([^)]*\\))?\\s*|(?:public|protected|internal|private|open|final|abstract|sealed|data|inline|value|enum|annotation|inner|override|lateinit|const|suspend|tailrec|operator|infix|external|expect|actual)\\s+)*(?:fun\\s+)?interface\\s"))

; ---- calls

; f(x), and constructor calls (User("a")), which Kotlin writes the same.
; The standard library's most common top-level functions are left out.
((call_expression . (simple_identifier) @name) @reference.call
  (#not-any-of? @name
    "println" "print" "readLine" "readln" "listOf" "listOfNotNull" "mutableListOf"
    "arrayListOf" "emptyList" "setOf" "mutableSetOf" "hashSetOf" "emptySet" "mapOf"
    "mutableMapOf" "hashMapOf" "emptyMap" "arrayOf" "emptyArray" "intArrayOf"
    "sequenceOf" "buildList" "buildMap" "buildString" "require" "requireNotNull"
    "check" "checkNotNull" "error" "TODO" "repeat" "run" "let" "apply" "also" "with"
    "takeIf" "takeUnless" "lazy" "synchronized" "maxOf" "minOf" "Pair" "Triple"))

; receiver.f(x), Type.f(x), a?.f(x), super.f(x)
(call_expression
  . (navigation_expression
      . (_) @qualifier
      (navigation_suffix (simple_identifier) @name) .)) @reference.call

; ---- types

; Annotations: @Name and @Name(args).
(annotation (user_type (type_identifier) @name .)) @reference.type
(annotation (constructor_invocation (user_type (type_identifier) @name .))) @reference.type

; Every other written type: parameters, results, properties, type
; arguments, receivers, casts and checks. Supertypes and annotations are
; the references above, not type references too; Kotlin's built-in types
; are left out.
((user_type (type_identifier) @name .) @reference.type
  (#not-has-parent? @reference.type delegation_specifier constructor_invocation explicit_delegation annotation)
  (#not-any-of? @name
    "Any" "Unit" "Nothing" "Boolean" "Byte" "Short" "Int" "Long" "Float" "Double"
    "Char" "String" "Number" "UByte" "UShort" "UInt" "ULong"))
((user_type (type_identifier) @name . (type_arguments)) @reference.type
  (#not-has-parent? @reference.type delegation_specifier constructor_invocation explicit_delegation annotation))
((user_type (type_identifier) @qualifier . (type_identifier) @name .) @reference.type
  (#not-has-parent? @reference.type delegation_specifier constructor_invocation explicit_delegation annotation))
((user_type (type_identifier) @qualifier . (type_identifier) @name . (type_arguments)) @reference.type
  (#not-has-parent? @reference.type delegation_specifier constructor_invocation explicit_delegation annotation))
