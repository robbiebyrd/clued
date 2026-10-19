; Ruby: modules, classes, methods, constants and the definitions written as
; calls (attr_*, define_method, Rake's namespace and task), requires and
; Gemfile gems, calls, constant references, superclasses and mixins, in
; tree-sitter's tags-query convention plus clued's captures (see
; ../README.md). Ruby has no package declaration: names are qualified by
; the lexical nesting of module and class bodies. Doc comments are the
; comments on the lines just above a definition (the extractor's default;
; =begin/=end blocks too), signatures its text up to the body.
;
; Bare identifiers are not references: without local-variable tracking a
; zero-argument call (save) cannot be told from a local (user).

; ---- requires and gems

((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list . (string (string_content) @import.spec))) @import
  (#eq? @_m "require")
  (#set! import.kind "require"))

((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list . (string (string_content) @import.spec))) @import
  (#eq? @_m "require_relative")
  (#set! import.kind "require_relative"))

((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list . (string (string_content) @import.spec))) @import
  (#eq? @_m "load")
  (#set! import.kind "load"))

((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list
    . (simple_symbol) @import.name
    . (string (string_content) @import.spec))) @import
  (#eq? @_m "autoload")
  (#strip! @import.name "^:")
  (#set! import.kind "autoload"))

; Gemfile: gem "rails", "~> 7.1".
((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list . (string (string_content) @import.spec))) @import
  (#eq? @_m "gem")
  (#set! import.kind "gem"))

; ---- modules and classes (Shop::Billing names Billing inside Shop)

(module
  name: (constant) @name) @definition.module

(module
  name: (scope_resolution
    scope: (_) @receiver
    name: (constant) @name)) @definition.module

(class
  name: (constant) @name) @definition.class

(class
  name: (scope_resolution
    scope: (_) @receiver
    name: (constant) @name)) @definition.class

; Foo = Class.new(Base) do ... end, Point = Struct.new(:x, :y),
; Helpers = Module.new.
(assignment
  left: (constant) @name
  right: (call
    receiver: (constant) @_r
    method: (identifier) @_m)
  (#any-of? @_r "Class" "Struct" "Data")
  (#any-of? @_m "new" "define")) @definition.class

(assignment
  left: (constant) @name
  right: (call
    receiver: (constant) @_r
    method: (identifier) @_m)
  (#eq? @_r "Module")
  (#eq? @_m "new")) @definition.module

; ---- methods (a def at the top level is a function)

((method
  name: (_) @name) @definition.function
  (#has-parent? @definition.function program))

((method
  name: (_) @name) @definition.method
  (#not-has-parent? @definition.method program))

; def self.find, def Shop.helper (Shop is the receiver).
(singleton_method
  object: [(constant) (scope_resolution)] @receiver
  name: (_) @name) @definition.method

(singleton_method
  object: [(self) (identifier) (instance_variable) (class_variable) (global_variable)]
  name: (_) @name) @definition.method

(alias
  name: (_) @name) @definition.method

; ---- constants (not the class and module forms above)

((assignment
  left: (constant) @name
  right: (_) @_v) @definition.constant
  (#not-match? @_v "^(Class|Struct|Data)\\s*\\.\\s*(new|define)\\b")
  (#not-match? @_v "^Module\\s*\\.\\s*new\\b"))

(assignment
  left: (scope_resolution
    scope: (_) @receiver
    name: (constant) @name)) @definition.constant

(operator_assignment
  left: (constant) @name) @definition.constant

(assignment
  left: (left_assignment_list
    (constant) @name @definition.constant))

; ---- definitions written as calls (profile definingForms)
;
; One pattern per argument shape: gotreesitter v0.56.0 can give every
; match of an alternation the first alternative's @name.

; attr_reader :id, :items and attr_writer "customer": one per name.
((call
  !receiver
  method: (identifier) @form
  arguments: (argument_list (simple_symbol) @name)) @definition.form
  (#any-of? @form "attr_reader" "attr_writer" "attr_accessor" "attr")
  (#strip! @name "^:"))

((call
  !receiver
  method: (identifier) @form
  arguments: (argument_list (string (string_content) @name))) @definition.form
  (#any-of? @form "attr_reader" "attr_writer" "attr_accessor" "attr")
  (#strip! @name "^:"))

; define_method(:void!), alias_method :to_s, :number, namespace :db,
; task :import, task "purge": the first argument.
((call
  !receiver
  method: (identifier) @form
  arguments: (argument_list . (simple_symbol) @name)) @definition.form
  (#any-of? @form "define_method" "define_singleton_method" "alias_method" "namespace" "task")
  (#strip! @name "^:"))

((call
  !receiver
  method: (identifier) @form
  arguments: (argument_list . (string (string_content) @name))) @definition.form
  (#any-of? @form "define_method" "define_singleton_method" "alias_method" "namespace" "task")
  (#strip! @name "^:"))

; task migrate: :environment, task :seed => [:migrate].
((call
  !receiver
  method: (identifier) @form
  arguments: (argument_list . (pair key: (hash_key_symbol) @name))) @definition.form
  (#eq? @form "task")
  (#strip! @name "^:"))

((call
  !receiver
  method: (identifier) @form
  arguments: (argument_list . (pair key: (simple_symbol) @name))) @definition.form
  (#eq? @form "task")
  (#strip! @name "^:"))

; Rake: desc "..." just before a task is the task's doc.
((call
  !receiver
  method: (identifier) @_desc
  arguments: (argument_list . (string) @doc))
  .
  (call
    !receiver
    method: (identifier) @form
    arguments: (argument_list . (simple_symbol) @name)) @definition.form
  (#eq? @_desc "desc")
  (#eq? @form "task")
  (#strip! @name "^:"))

((call
  !receiver
  method: (identifier) @_desc
  arguments: (argument_list . (string) @doc))
  .
  (call
    !receiver
    method: (identifier) @form
    arguments: (argument_list . (string (string_content) @name))) @definition.form
  (#eq? @_desc "desc")
  (#eq? @form "task")
  (#strip! @name "^:"))

((call
  !receiver
  method: (identifier) @_desc
  arguments: (argument_list . (string) @doc))
  .
  (call
    !receiver
    method: (identifier) @form
    arguments: (argument_list . (pair key: (hash_key_symbol) @name))) @definition.form
  (#eq? @_desc "desc")
  (#eq? @form "task")
  (#strip! @name "^:"))

((call
  !receiver
  method: (identifier) @_desc
  arguments: (argument_list . (string) @doc))
  .
  (call
    !receiver
    method: (identifier) @form
    arguments: (argument_list . (pair key: (simple_symbol) @name))) @definition.form
  (#eq? @_desc "desc")
  (#eq? @form "task")
  (#strip! @name "^:"))

; ---- superclasses and mixins

(superclass
  (constant) @name) @reference.superclass

(superclass
  (scope_resolution
    scope: (_)? @qualifier
    name: (constant) @name)) @reference.superclass

; class LineItem < Struct.new(:sku) extends Struct.
(superclass
  (call receiver: (constant) @name)) @reference.superclass

((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list (constant) @name)) @reference.mixin
  (#any-of? @_m "include" "extend" "prepend"))

((call
  !receiver
  method: (identifier) @_m
  arguments: (argument_list
    (scope_resolution
      scope: (_)? @qualifier
      name: (constant) @name))) @reference.mixin
  (#any-of? @_m "include" "extend" "prepend"))

; ---- calls

((call
  receiver: (_)? @qualifier
  method: [(identifier) (constant)] @name) @reference.call
  (#not-any-of? @name
    "require" "require_relative" "load" "autoload" "gem"
    "include" "extend" "prepend"
    "attr_reader" "attr_writer" "attr_accessor" "attr"
    "define_method" "define_singleton_method" "alias_method"
    "namespace" "task" "desc"
    "private" "public" "protected" "module_function"
    "private_constant" "public_constant" "private_class_method"
    "puts" "print" "p" "pp" "raise" "fail" "lambda" "proc" "loop"
    "format" "sprintf" "catch" "throw" "block_given?" "freeze"))

; ---- constant references (not superclasses or mixins, captured above)
;
; A constant is a type (a class or module) unless it is written in
; SCREAMING_CASE, Ruby's convention for a value (MAX_ITEMS).

((constant) @name @reference.type
  (#not-has-parent? @name superclass scope_resolution argument_list call)
  (#not-match? @name "^[A-Z][A-Z0-9_]+$"))

((constant) @name @reference.constant
  (#not-has-parent? @name superclass scope_resolution argument_list call)
  (#match? @name "^[A-Z][A-Z0-9_]+$"))

((scope_resolution
  scope: (_)? @qualifier
  name: (constant) @name) @reference.type
  (#not-has-parent? @reference.type superclass argument_list)
  (#not-match? @name "^[A-Z][A-Z0-9_]+$"))

((scope_resolution
  scope: (_)? @qualifier
  name: (constant) @name) @reference.constant
  (#not-has-parent? @reference.constant superclass argument_list)
  (#match? @name "^[A-Z][A-Z0-9_]+$"))

; The namespace a qualified constant is reached through (Shop in
; Shop::Mailer).
(scope_resolution
  scope: (constant) @name) @reference.type

; A receiver: Repository.instance, ENV.fetch (a Struct.new superclass is
; an extends, above).
((call
  receiver: (constant) @name) @reference.type
  (#not-has-parent? @reference.type superclass)
  (#not-match? @name "^[A-Z][A-Z0-9_]+$"))

((call
  receiver: (constant) @name) @reference.constant
  (#match? @name "^[A-Z][A-Z0-9_]+$"))

; Arguments: raise Error, is_a?(Invoice) (rescue clauses are bare
; constants, above).

((call
  method: (_) @_m
  arguments: (argument_list (constant) @name)) @reference.type
  (#not-any-of? @_m "include" "extend" "prepend")
  (#not-match? @name "^[A-Z][A-Z0-9_]+$"))

((call
  method: (_) @_m
  arguments: (argument_list
    (scope_resolution
      scope: (_)? @qualifier
      name: (constant) @name))) @reference.type
  (#not-any-of? @_m "include" "extend" "prepend")
  (#not-match? @name "^[A-Z][A-Z0-9_]+$"))

((call
  method: (_) @_m
  arguments: (argument_list (constant) @name)) @reference.constant
  (#not-any-of? @_m "include" "extend" "prepend")
  (#match? @name "^[A-Z][A-Z0-9_]+$"))

((call
  method: (_) @_m
  arguments: (argument_list
    (scope_resolution
      scope: (_)? @qualifier
      name: (constant) @name))) @reference.constant
  (#not-any-of? @_m "include" "extend" "prepend")
  (#match? @name "^[A-Z][A-Z0-9_]+$"))
