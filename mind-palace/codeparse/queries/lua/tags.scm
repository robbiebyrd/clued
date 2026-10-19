; Lua: functions, local functions, table-field functions (M.f, M:f),
; module tables, require imports and calls, in tree-sitter's tags-query
; convention plus clued's captures (see ../README.md). Doc comments are the
; comments on the lines just above a definition (the extractor's default:
; `---` LuaLS annotations and `--[[ ]]` blocks included), signatures its
; text up to the body. A Lua file has no package form; the file is the
; module (profile: packageBy file).

; ---- imports: require "a.b", require("a.b"), local m = require("a.b")

(function_call
  name: (identifier) @_require
  arguments: (arguments . (string content: (string_content) @import.spec))
  (#eq? @_require "require")
  (#set! import.kind "require")) @import

(variable_declaration
  (assignment_statement
    (variable_list . name: (identifier) @import.alias)
    (expression_list . value: (function_call
      name: (identifier) @_require
      arguments: (arguments . (string content: (string_content) @import.spec))
      (#eq? @_require "require")) @import)))

; Members taken from a required module: local a, b = m.a, m.b is left to
; the resolver; local f = require("m").f names f.
(variable_declaration
  (assignment_statement
    (variable_list . name: (identifier) @import.alias)
    (expression_list . value: (dot_index_expression
      table: (function_call
        name: (identifier) @_require
        arguments: (arguments . (string content: (string_content) @import.spec))
        (#eq? @_require "require")) @import
      field: (identifier) @import.name))))

; pcall(require, "m"): an optional dependency.
(function_call
  name: (identifier) @_pcall
  arguments: (arguments . (identifier) @_require . (string content: (string_content) @import.spec))
  (#eq? @_pcall "pcall")
  (#eq? @_require "require")
  (#set! import.kind "require")) @import

(function_call
  name: (identifier) @_dofile
  arguments: (arguments . (string content: (string_content) @import.spec))
  (#any-of? @_dofile "dofile" "loadfile")
  (#set! import.kind "include")) @import

; ---- functions

; function f() (global, exported) and local function f() (file-private).
((function_declaration
  name: (identifier) @name) @definition.function @exported
  (#not-match? @definition.function "^local[ \t]"))

((function_declaration
  name: (identifier) @name) @definition.function
  (#match? @definition.function "^local[ \t]"))

; function M.f() and function a.b.c(): a function stored in a table.
(function_declaration
  name: (dot_index_expression
    table: (_) @receiver
    field: (identifier) @name)) @definition.function @exported

; function M:m(): a method (self is implicit).
(function_declaration
  name: (method_index_expression
    table: (_) @receiver
    method: (identifier) @name)) @definition.method @exported

; f = function() end (global, a statement of its own) and local f =
; function() end (the whole local declaration).
(chunk
  (assignment_statement
    (variable_list . name: (identifier) @name)
    (expression_list . value: (function_definition))) @definition.function @exported)

(block
  (assignment_statement
    (variable_list . name: (identifier) @name)
    (expression_list . value: (function_definition))) @definition.function @exported)

(variable_declaration
  (assignment_statement
    (variable_list . name: (identifier) @name)
    (expression_list . value: (function_definition)))) @definition.function

; M.f = function() end; self.f = function() end inside a constructor is an
; instance field, not a definition of its own.
(assignment_statement
  (variable_list . name: (dot_index_expression
    table: (_) @receiver
    field: (identifier) @name))
  (expression_list . value: (function_definition))
  (#not-eq? @receiver "self")) @definition.function @exported

; { f = function() end }: a function in a table constructor, nested in the
; table's definition when the table is one.
(field
  name: (identifier) @name
  value: (function_definition)) @definition.function @exported

; ---- module tables (top level): local M = {}, M = {}, and class tables
; made with local C = setmetatable({}, ...)

; A local table is exported when the file returns it (return M). The
; pattern matches the same node as the plain one below; the extractor keeps
; one definition per node, this one (it comes first).
(chunk
  (variable_declaration
    (assignment_statement
      (variable_list . name: (identifier) @name)
      (expression_list . value: (table_constructor)))) @definition.module @exported
  (return_statement (expression_list . (identifier) @_returned .))
  (#eq? @name @_returned))

(chunk
  (variable_declaration
    (assignment_statement
      (variable_list . name: (identifier) @name)
      (expression_list . value: (table_constructor)))) @definition.module)

(chunk
  (assignment_statement
    (variable_list . name: (identifier) @name)
    (expression_list . value: (table_constructor))) @definition.module @exported)

(chunk
  (variable_declaration
    (assignment_statement
      (variable_list . name: (identifier) @name)
      (expression_list . value: (function_call
        name: (identifier) @_setmetatable
        arguments: (arguments . (table_constructor)))))) @definition.class @exported
  (return_statement (expression_list . (identifier) @_returned .))
  (#eq? @_setmetatable "setmetatable")
  (#eq? @name @_returned))

(chunk
  (variable_declaration
    (assignment_statement
      (variable_list . name: (identifier) @name)
      (expression_list . value: (function_call
        name: (identifier) @_setmetatable
        arguments: (arguments . (table_constructor)))))) @definition.class
  (#eq? @_setmetatable "setmetatable"))

; Constants and fields of a module table: M.VERSION = "1" and the plain
; fields of a top-level table constructor. The two constructor forms are
; separate patterns: under gotreesitter v0.56.0 an alternation around them
; gave every field of one table the first field's @name.
(chunk
  (assignment_statement
    (variable_list . name: (dot_index_expression
      table: (identifier) @receiver
      field: (identifier) @name))
    (expression_list . value: [(string) (number) (true) (false) (table_constructor)])) @definition.field @exported)

(chunk
  (variable_declaration
    (assignment_statement
      (expression_list . value: (table_constructor
        (field
          name: (identifier) @name
          value: [(string) (number) (true) (false) (table_constructor)]) @definition.field @exported)))))

(chunk
  (assignment_statement
    (variable_list . name: (identifier))
    (expression_list . value: (table_constructor
      (field
        name: (identifier) @name
        value: [(string) (number) (true) (false) (table_constructor)]) @definition.field @exported))))

; ---- references

; Inheritance: setmetatable(Derived, { __index = Base }) and
; setmetatable({}, { __index = Base }).
(function_call
  name: (identifier) @_setmetatable
  arguments: (arguments
    (table_constructor
      (field
        name: (identifier) @_index
        value: [(identifier) @name
                (dot_index_expression table: (_) @qualifier field: (identifier) @name)])))
  (#eq? @_setmetatable "setmetatable")
  (#eq? @_index "__index")) @reference.extends

; setmetatable(o, Account): o is an Account (a class table used as the
; metatable); setmetatable(o, self) inside a constructor names nothing.
(function_call
  name: (identifier) @_setmetatable
  arguments: (arguments . (_) . [(identifier) @name
                                 (dot_index_expression table: (_) @qualifier field: (identifier) @name)] .)
  (#eq? @_setmetatable "setmetatable")
  (#not-eq? @name "self")) @reference.type

; Calls: f(), m.f(), obj:m(), with the standard library's globals left out
; of the bare calls.
(function_call
  name: (identifier) @name
  (#not-any-of? @name
    "assert" "collectgarbage" "dofile" "error" "getmetatable" "ipairs"
    "load" "loadfile" "loadstring" "module" "next" "pairs" "pcall" "print"
    "rawequal" "rawget" "rawlen" "rawset" "require" "select" "setfenv"
    "getfenv" "setmetatable" "tonumber" "tostring" "type" "unpack"
    "xpcall")) @reference.call

(function_call
  name: (dot_index_expression
    table: (_) @qualifier
    field: (identifier) @name)) @reference.call

(function_call
  name: (method_index_expression
    table: (_) @qualifier
    method: (identifier) @name)) @reference.call
