// Package codeparse_test holds the parser spike of plan 0012 section 1.1
// (story 0078-k15). It is temporary: the story deletes it once the numbers
// are recorded in the plan's work log.
package codeparse_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars/c"
	"github.com/odvcencio/gotreesitter/grammars/c_sharp"
	"github.com/odvcencio/gotreesitter/grammars/clojure"
	"github.com/odvcencio/gotreesitter/grammars/commonlisp"
	"github.com/odvcencio/gotreesitter/grammars/cpp"
	"github.com/odvcencio/gotreesitter/grammars/dart"
	"github.com/odvcencio/gotreesitter/grammars/elisp"
	golang "github.com/odvcencio/gotreesitter/grammars/go"
	"github.com/odvcencio/gotreesitter/grammars/java"
	"github.com/odvcencio/gotreesitter/grammars/javascript"
	"github.com/odvcencio/gotreesitter/grammars/kotlin"
	"github.com/odvcencio/gotreesitter/grammars/lua"
	"github.com/odvcencio/gotreesitter/grammars/objc"
	"github.com/odvcencio/gotreesitter/grammars/ocaml"
	"github.com/odvcencio/gotreesitter/grammars/perl"
	"github.com/odvcencio/gotreesitter/grammars/php"
	"github.com/odvcencio/gotreesitter/grammars/python"
	"github.com/odvcencio/gotreesitter/grammars/racket"
	"github.com/odvcencio/gotreesitter/grammars/ruby"
	"github.com/odvcencio/gotreesitter/grammars/rust"
	"github.com/odvcencio/gotreesitter/grammars/scheme"
	"github.com/odvcencio/gotreesitter/grammars/sql"
	"github.com/odvcencio/gotreesitter/grammars/swift"
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"github.com/odvcencio/gotreesitter/grammars/typescript"
)

// spikeLang is one grammar: how to load it, which files it takes, a sample
// with a tags-style query (tree-sitter's @definition.* / @reference.call
// convention plus clued's @import) and the captures the sample must yield.
type spikeLang struct {
	name   string
	exts   []string
	lang   func() *ts.Language
	sample string
	query  string
	want   []string // "capture:text", where text is the @name capture (or the @import node)
}

const lispCallsCL = `((list_lit . (sym_lit) @name) @reference.call
 (#not-any-of? @name "defvar" "defparameter" "defconstant" "defclass" "defstruct" "defgeneric" "defpackage" "in-package" "quote" "function"))`

var spikeLangs = []spikeLang{
	{
		name: "go", exts: []string{".go"}, lang: golang.Language,
		sample: "package demo\n\nimport \"fmt\"\n\ntype Shape interface{ Area() float64 }\n\ntype Box struct{ W float64 }\n\nfunc (b Box) Area() float64 { return b.W * b.W }\n\nfunc Hello() { fmt.Println(helper()) }\n",
		query: `
(function_declaration name: (identifier) @name) @definition.function
(method_declaration name: (field_identifier) @name) @definition.method
(type_spec name: (type_identifier) @name type: (interface_type)) @definition.interface
(type_spec name: (type_identifier) @name type: (struct_type)) @definition.class
(call_expression function: [(identifier) @name (selector_expression field: (field_identifier) @name)]) @reference.call
(import_spec path: (interpreted_string_literal) @import)
(package_clause (package_identifier) @package)`,
		want: []string{"definition.function:Hello", "definition.method:Area", "definition.interface:Shape", "definition.class:Box", "reference.call:Println", "reference.call:helper", `import:"fmt"`, "package:demo"},
	},
	{
		name: "python", exts: []string{".py", ".pyi"}, lang: python.Language,
		sample: "import os\nfrom pkg.mod import thing\n\nclass Animal(Base):\n    def speak(self):\n        \"\"\"Speak.\"\"\"\n        return helper(self)\n\ndef main():\n    os.path.join(\"a\")\n",
		query: `
(class_definition name: (identifier) @name) @definition.class
(function_definition name: (identifier) @name) @definition.function
(function_definition body: (block . (string) @doc))
(call function: [(identifier) @name (attribute attribute: (identifier) @name)]) @reference.call
(import_statement name: (dotted_name) @import)
(import_from_statement module_name: (dotted_name) @import)`,
		want: []string{"definition.class:Animal", "definition.function:speak", "definition.function:main", "reference.call:helper", "reference.call:join", "import:os", "import:pkg.mod", `doc:"""Speak."""`},
	},
	{
		name: "c", exts: []string{".c", ".h"}, lang: c.Language,
		sample: "#include <stdio.h>\n#include \"util.h\"\n\nstruct point { int x; };\ntypedef int myint;\n#define MAX 10\n\nstatic int add(int a, int b) { return helper(a) + b; }\nint main(void) { printf(\"%d\", add(1, 2)); return 0; }\n",
		query: `
(function_definition declarator: (function_declarator declarator: (identifier) @name)) @definition.function
(struct_specifier name: (type_identifier) @name body: (_)) @definition.class
(type_definition declarator: (type_identifier) @name) @definition.type
(preproc_def name: (identifier) @name) @definition.macro
(call_expression function: (identifier) @name) @reference.call
(preproc_include path: (_) @import)`,
		want: []string{"definition.function:add", "definition.function:main", "definition.class:point", "definition.type:myint", "definition.macro:MAX", "reference.call:helper", "reference.call:printf", "import:<stdio.h>", `import:"util.h"`},
	},
	{
		name: "cpp", exts: []string{".cc", ".cpp", ".cxx", ".hpp", ".hh"}, lang: cpp.Language,
		sample: "#include <vector>\nnamespace geo {\nclass Shape {\n public:\n  virtual double area() const;\n};\ndouble Shape::area() const { return compute(1); }\ntemplate <typename T> T twice(T v) { return v * 2; }\n}\n",
		query: `
(namespace_definition name: (namespace_identifier) @name) @definition.module
(class_specifier name: (type_identifier) @name) @definition.class
(field_declaration declarator: (function_declarator declarator: (field_identifier) @name)) @definition.method
(function_definition declarator: (function_declarator declarator: [(identifier) @name (qualified_identifier name: (identifier) @name)])) @definition.function
(call_expression function: [(identifier) @name (qualified_identifier name: (identifier) @name) (field_expression field: (field_identifier) @name)]) @reference.call
(preproc_include path: (_) @import)`,
		want: []string{"definition.module:geo", "definition.class:Shape", "definition.method:area", "definition.function:area", "definition.function:twice", "reference.call:compute", "import:<vector>"},
	},
	{
		name: "c_sharp", exts: []string{".cs"}, lang: c_sharp.Language,
		sample: "using System.Text;\nnamespace Demo;\n\npublic interface IShape { double Area(); }\npublic class Box : IShape {\n    public int Size { get; set; }\n    public double Area() { return Helper.Compute(Size); }\n}\npublic record Point(int X, int Y);\npublic enum Color { Red }\n",
		query: `
[(namespace_declaration name: (_) @name) (file_scoped_namespace_declaration name: (_) @name)] @definition.module
(class_declaration name: (identifier) @name) @definition.class
(record_declaration name: (identifier) @name) @definition.class
(interface_declaration name: (identifier) @name) @definition.interface
(enum_declaration name: (identifier) @name) @definition.enum
(method_declaration name: (identifier) @name) @definition.method
(property_declaration name: (identifier) @name) @definition.property
(invocation_expression function: [(identifier) @name (member_access_expression name: (identifier) @name)]) @reference.call
(using_directive (_) @import)`,
		want: []string{"definition.module:Demo", "definition.class:Box", "definition.class:Point", "definition.interface:IShape", "definition.enum:Color", "definition.method:Area", "definition.property:Size", "reference.call:Compute", "import:System.Text"},
	},
	{
		name: "objc", exts: []string{".m", ".mm"}, lang: objc.Language,
		sample: "#import <Foundation/Foundation.h>\n@protocol Greeter\n- (void)greet;\n@end\n@interface Person : NSObject <Greeter>\n- (void)greet;\n@end\n@implementation Person\n- (void)greet { [self sayTo:@\"x\"]; NSLog(@\"hi\"); }\n@end\nint helper(int a) { return a; }\n",
		query: `
(protocol_declaration . (identifier) @name) @definition.interface
(class_interface . (identifier) @name) @definition.class
(class_implementation . (identifier) @name) @definition.class
(method_definition (identifier) @name) @definition.method
(method_declaration (identifier) @name) @definition.method
(function_definition declarator: (function_declarator declarator: (identifier) @name)) @definition.function
(message_expression method: (identifier) @name) @reference.call
(call_expression function: (identifier) @name) @reference.call
(preproc_include path: (_) @import)`,
		want: []string{"definition.interface:Greeter", "definition.class:Person", "definition.method:greet", "definition.function:helper", "reference.call:sayTo", "reference.call:NSLog", "import:<Foundation/Foundation.h>"},
	},
	{
		name: "swift", exts: []string{".swift"}, lang: swift.Language,
		sample: "import Foundation\nprotocol Shape { func area() -> Double }\nstruct Box: Shape {\n    func area() -> Double { return compute(1) }\n}\nclass Node {}\nenum Color { case red }\nextension Box { func grow() {} }\nfunc topLevel() { print(\"x\") }\n",
		query: `
(protocol_declaration name: (type_identifier) @name) @definition.interface
(class_declaration name: (type_identifier) @name) @definition.class
(class_declaration name: (user_type (type_identifier) @name)) @definition.extension
(function_declaration name: (simple_identifier) @name) @definition.function
(protocol_function_declaration name: (simple_identifier) @name) @definition.method
(call_expression (simple_identifier) @name) @reference.call
(import_declaration (identifier) @import)`,
		want: []string{"definition.interface:Shape", "definition.class:Box", "definition.class:Node", "definition.class:Color", "definition.extension:Box", "definition.function:area", "definition.function:grow", "definition.function:topLevel", "definition.method:area", "reference.call:compute", "reference.call:print", "import:Foundation"},
	},
	{
		name: "java", exts: []string{".java"}, lang: java.Language,
		sample: "package com.example;\nimport java.util.List;\npublic class Box implements Shape {\n    private int size;\n    public double area() { return Helper.compute(size); }\n}\ninterface Shape { double area(); }\nenum Color { RED }\nrecord Point(int x, int y) {}\n",
		query: `
(class_declaration name: (identifier) @name) @definition.class
(record_declaration name: (identifier) @name) @definition.class
(interface_declaration name: (identifier) @name) @definition.interface
(enum_declaration name: (identifier) @name) @definition.enum
(method_declaration name: (identifier) @name) @definition.method
(method_invocation name: (identifier) @name) @reference.call
(superclass (type_identifier) @name) @reference.class
(super_interfaces (type_list (type_identifier) @name)) @reference.implementation
(import_declaration (scoped_identifier) @import)
(package_declaration (scoped_identifier) @package)`,
		want: []string{"definition.class:Box", "definition.class:Point", "definition.interface:Shape", "definition.enum:Color", "definition.method:area", "reference.call:compute", "reference.implementation:Shape", "import:java.util.List", "package:com.example"},
	},
	{
		name: "javascript", exts: []string{".js", ".mjs", ".cjs", ".jsx"}, lang: javascript.Language,
		sample: "import { a } from \"./a.js\";\nconst fs = require(\"fs\");\nexport class Animal {\n  speak() { return helper(this); }\n}\nfunction main() { fs.readFileSync(\"x\"); }\nexport const arrow = () => main();\nconst el = <div>{a}</div>;\n",
		query: `
(class_declaration name: (identifier) @name) @definition.class
(method_definition name: (property_identifier) @name) @definition.method
(function_declaration name: (identifier) @name) @definition.function
(variable_declarator name: (identifier) @name value: [(arrow_function) (function_expression)]) @definition.function
(call_expression function: [(identifier) @name (member_expression property: (property_identifier) @name)]) @reference.call
(import_statement source: (string) @import)`,
		want: []string{"definition.class:Animal", "definition.method:speak", "definition.function:main", "definition.function:arrow", "reference.call:helper", "reference.call:readFileSync", "reference.call:require", `import:"./a.js"`},
	},
	{
		name: "typescript", exts: []string{".ts", ".mts", ".cts"}, lang: typescript.Language,
		sample: "import { a } from \"./a\";\nexport interface Shape { area(): number }\nexport type Id = string;\nenum Color { Red }\nnamespace NS { export const x = 1; }\nexport class Box implements Shape {\n  area(): number { return compute(1); }\n}\nexport function make(): Box { return new Box(); }\n",
		query: `
(interface_declaration name: (type_identifier) @name) @definition.interface
(type_alias_declaration name: (type_identifier) @name) @definition.type
(enum_declaration name: (identifier) @name) @definition.enum
(internal_module name: (identifier) @name) @definition.module
(class_declaration name: (type_identifier) @name) @definition.class
(method_definition name: (property_identifier) @name) @definition.method
(function_declaration name: (identifier) @name) @definition.function
(call_expression function: [(identifier) @name (member_expression property: (property_identifier) @name)]) @reference.call
(new_expression constructor: (identifier) @name) @reference.class
(implements_clause (type_identifier) @name) @reference.implementation
(import_statement source: (string) @import)`,
		want: []string{"definition.interface:Shape", "definition.type:Id", "definition.enum:Color", "definition.module:NS", "definition.class:Box", "definition.method:area", "definition.function:make", "reference.call:compute", "reference.class:Box", "reference.implementation:Shape", `import:"./a"`},
	},
	{
		name: "tsx", exts: []string{".tsx"}, lang: tsx.Language,
		sample: "import React from \"react\";\ninterface Props { name: string }\nexport function Hello(props: Props) { return <div>{format(props.name)}</div>; }\nexport const App = () => <Hello name=\"x\" />;\n",
		query: `
(interface_declaration name: (type_identifier) @name) @definition.interface
(function_declaration name: (identifier) @name) @definition.function
(variable_declarator name: (identifier) @name value: (arrow_function)) @definition.function
(call_expression function: (identifier) @name) @reference.call
(jsx_self_closing_element name: (identifier) @name) @reference.call
(import_statement source: (string) @import)`,
		want: []string{"definition.interface:Props", "definition.function:Hello", "definition.function:App", "reference.call:format", "reference.call:Hello", `import:"react"`},
	},
	{
		name: "sql", exts: []string{".sql"}, lang: sql.Language,
		sample: "CREATE TABLE users (id integer PRIMARY KEY, name text);\nCREATE VIEW active_users AS SELECT id FROM users WHERE id > 0;\nCREATE INDEX users_name ON users (name);\nCREATE FUNCTION add_one(i integer) RETURNS integer AS $$ SELECT i + 1 $$ LANGUAGE sql;\nINSERT INTO users (id, name) VALUES (1, 'a');\nSELECT u.name FROM users u JOIN orders o ON o.user_id = u.id;\n",
		query: `
(create_table_statement . (identifier) @name) @definition.table
(create_view_statement . (identifier) @name) @definition.view
(create_index_statement name: (identifier) @name) @definition.index
(create_function_statement . (identifier) @name) @definition.function
(table_column name: (identifier) @name) @definition.column
(from_clause (identifier) @name) @reference.table
(join_clause (identifier) @name) @reference.table
(insert_statement . (identifier) @name) @reference.table`,
		want: []string{"definition.table:users", "definition.view:active_users", "definition.index:users_name", "definition.function:add_one", "definition.column:name", "reference.table:users", "reference.table:orders"},
	},
	{
		name: "rust", exts: []string{".rs"}, lang: rust.Language,
		sample: "use std::fmt;\nmod inner {}\npub struct Point { x: i32 }\npub enum Color { Red }\npub trait Shape { fn area(&self) -> f64; }\nimpl Shape for Point { fn area(&self) -> f64 { compute(1) } }\nmacro_rules! sq { ($x:expr) => { $x * $x }; }\nfn main() { println!(\"{}\", helper()); }\n",
		query: `
(mod_item name: (identifier) @name) @definition.module
(struct_item name: (type_identifier) @name) @definition.class
(enum_item name: (type_identifier) @name) @definition.enum
(trait_item name: (type_identifier) @name) @definition.interface
(function_item name: (identifier) @name) @definition.function
(function_signature_item name: (identifier) @name) @definition.method
(macro_definition name: (identifier) @name) @definition.macro
(impl_item trait: (type_identifier) @name) @reference.implementation
(call_expression function: [(identifier) @name (scoped_identifier name: (identifier) @name) (field_expression field: (field_identifier) @name)]) @reference.call
(macro_invocation macro: (identifier) @name) @reference.call
(use_declaration argument: (_) @import)`,
		want: []string{"definition.module:inner", "definition.class:Point", "definition.enum:Color", "definition.interface:Shape", "definition.function:area", "definition.function:main", "definition.method:area", "definition.macro:sq", "reference.implementation:Shape", "reference.call:compute", "reference.call:println", "import:std::fmt"},
	},
	{
		name: "php", exts: []string{".php"}, lang: php.Language,
		sample: "<?php\nnamespace App\\Models;\nuse App\\Contracts\\Shape;\nrequire_once 'util.php';\ninterface Named { public function name(); }\ntrait Greets { public function greet() {} }\nclass Box implements Shape {\n    const SIZE = 1;\n    public function area() { return helper($this->size); }\n}\nfunction top() { return strlen(\"x\"); }\n",
		query: `
(namespace_definition name: (namespace_name) @name) @definition.module
(interface_declaration name: (name) @name) @definition.interface
(trait_declaration name: (name) @name) @definition.trait
(class_declaration name: (name) @name) @definition.class
(method_declaration name: (name) @name) @definition.method
(function_definition name: (name) @name) @definition.function
(const_element (name) @name) @definition.constant
(function_call_expression function: [(name) @name (qualified_name (name) @name)]) @reference.call
(member_call_expression name: (name) @name) @reference.call
(namespace_use_clause [(qualified_name) (name)] @import)
(require_once_expression (_) @import)`,
		want: []string{`definition.module:App\Models`, "definition.interface:Named", "definition.trait:Greets", "definition.class:Box", "definition.method:area", "definition.function:top", "definition.constant:SIZE", "reference.call:helper", "reference.call:strlen", `import:App\Contracts\Shape`, "import:'util.php'"},
	},
	{
		name: "perl", exts: []string{".pl", ".pm", ".t"}, lang: perl.Language,
		sample: "package My::Module;\nuse strict;\nuse List::Util qw(sum);\nrequire Foo::Bar;\nsub greet {\n    my ($name) = @_;\n    return helper($name) . Other::Pkg::fn(1);\n}\n1;\n",
		query: `
(package_statement (package) @name) @definition.module
(subroutine_declaration_statement name: (bareword) @name) @definition.function
(function_call_expression function: (function) @name) @reference.call
(use_statement (package) @import)
(require_expression (bareword) @import)`,
		want: []string{"definition.module:My::Module", "definition.function:greet", "reference.call:helper", "reference.call:Other::Pkg::fn", "import:List::Util", "import:Foo::Bar"},
	},
	{
		name: "ocaml", exts: []string{".ml", ".mli"}, lang: ocaml.Language,
		sample: "open Printf\nmodule M = struct\n  let inner x = x + 1\nend\ntype shape = Circle of float | Square of float\nlet area s = match s with Circle r -> compute r | Square w -> w *. w\nclass point = object method x = 0 end\nval sig_only : int\n",
		query: `
(module_binding (module_name) @name) @definition.module
(type_binding name: (type_constructor) @name) @definition.type
(let_binding pattern: (value_name) @name (parameter)) @definition.function
(value_specification (value_name) @name) @definition.function
(class_binding (class_name) @name) @definition.class
(method_definition (method_name) @name) @definition.method
(application_expression function: (value_path (value_name) @name)) @reference.call
(open_module module: (module_path) @import)`,
		want: []string{"definition.module:M", "definition.type:shape", "definition.function:inner", "definition.function:area", "definition.function:sig_only", "definition.class:point", "definition.method:x", "reference.call:compute", "import:Printf"},
	},
	{
		name: "dart", exts: []string{".dart"}, lang: dart.Language,
		sample: "import 'package:flutter/material.dart';\npart 'model.g.dart';\nmixin Walker {}\nenum Color { red }\nextension Ext on String { int twice() => length * 2; }\nclass MyApp extends StatelessWidget {\n  Widget build(BuildContext context) { return helper(context); }\n}\nvoid main() { runApp(MyApp()); }\n",
		query: `
(class_definition name: (identifier) @name) @definition.class
(mixin_declaration (identifier) @name) @definition.mixin
(enum_declaration name: (identifier) @name) @definition.enum
(extension_declaration name: (identifier) @name) @definition.extension
(function_signature name: (identifier) @name) @definition.function
(_ (identifier) @name . (selector (argument_part))) @reference.call
(import_or_export (library_import (import_specification (configurable_uri (uri) @import))))
(part_directive (uri) @import)`,
		want: []string{"definition.class:MyApp", "definition.mixin:Walker", "definition.enum:Color", "definition.extension:Ext", "definition.function:twice", "definition.function:build", "definition.function:main", "reference.call:helper", "reference.call:runApp", "reference.call:MyApp", "import:'package:flutter/material.dart'", "import:'model.g.dart'"},
	},
	{
		name: "ruby", exts: []string{".rb", ".rake"}, lang: ruby.Language,
		sample: "require \"json\"\nrequire_relative \"util\"\nmodule Outer\n  class Animal < Base\n    include Comparable\n    SIZE = 1\n    def speak\n      helper(1)\n    end\n    def self.build; end\n  end\nend\n",
		query: `
(module name: (constant) @name) @definition.module
(class name: (constant) @name) @definition.class
(method name: (identifier) @name) @definition.method
(singleton_method name: (identifier) @name) @definition.method
(assignment left: (constant) @name) @definition.constant
((call method: (identifier) @_m arguments: (argument_list (string (string_content) @import))) (#any-of? @_m "require" "require_relative"))
((call method: (identifier) @_m arguments: (argument_list (constant) @name)) @reference.mixin (#any-of? @_m "include" "extend"))
((call method: (identifier) @name) @reference.call (#not-any-of? @name "require" "require_relative" "include" "extend"))`,
		want: []string{"definition.module:Outer", "definition.class:Animal", "definition.method:speak", "definition.method:build", "definition.constant:SIZE", "import:json", "import:util", "reference.mixin:Comparable", "reference.call:helper"},
	},
	{
		// Kotlin's sample keeps class bodies on their own lines: gotreesitter
		// v0.56.0 flags a one-line body with a member ("class A { fun f(): Int }")
		// as an error where the C runtime does not (see TestSpikeKnownDivergences).
		name: "kotlin", exts: []string{".kt", ".kts"}, lang: kotlin.Language,
		sample: "package com.example\nimport kotlin.math.max\ninterface Shape {\n    fun area(): Double\n}\nclass Box(val w: Double) : Shape {\n    override fun area(): Double = compute(w)\n}\nobject Registry {\n    val items = listOf(1)\n}\nfun String.shout(): String = uppercase()\nfun main() { println(max(1, 2)) }\n",
		query: `
(class_declaration (type_identifier) @name) @definition.class
(object_declaration (type_identifier) @name) @definition.object
(function_declaration (simple_identifier) @name) @definition.function
(property_declaration (variable_declaration (simple_identifier) @name)) @definition.property
(call_expression (simple_identifier) @name) @reference.call
(import_header (identifier) @import)
(package_header (identifier) @package)`,
		want: []string{"definition.class:Shape", "definition.class:Box", "definition.object:Registry", "definition.function:area", "definition.function:shout", "definition.function:main", "definition.property:items", "reference.call:compute", "reference.call:println", "reference.call:max", "import:kotlin.math.max", "package:com.example"},
	},
	{
		name: "lua", exts: []string{".lua"}, lang: lua.Language,
		sample: "local util = require(\"util\")\nlocal M = {}\nlocal function helper(x) return x end\nfunction M.go(x) return helper(x) end\nfunction M:method() util.call(self) end\nfunction global_fn() end\nreturn M\n",
		query: `
(function_declaration name: (identifier) @name) @definition.function
(function_declaration name: (dot_index_expression field: (identifier) @name)) @definition.function
(function_declaration name: (method_index_expression method: (identifier) @name)) @definition.method
((function_call name: (identifier) @_r arguments: (arguments (string (string_content) @import))) (#eq? @_r "require"))
(function_call name: [(identifier) @name (dot_index_expression field: (identifier) @name) (method_index_expression method: (identifier) @name)]) @reference.call`,
		want: []string{"definition.function:helper", "definition.function:go", "definition.function:global_fn", "definition.method:method", "import:util", "reference.call:helper", "reference.call:call"},
	},
	{
		name: "commonlisp", exts: []string{".lisp", ".lsp", ".cl", ".asd"}, lang: commonlisp.Language,
		sample: "(defpackage :demo (:use :cl))\n(in-package :demo)\n(defvar *count* 0)\n(defun add (a b) (helper a) (+ a b))\n(defmacro twice (x) `(progn ,x ,x))\n(defclass point () ((x :initarg :x)))\n(defmethod area ((p point)) (compute p))\n",
		query: `
(defun_header keyword: (defun_keyword) @_k function_name: (sym_lit) @name (#eq? @_k "defun")) @definition.function
(defun_header keyword: (defun_keyword) @_k function_name: (sym_lit) @name (#eq? @_k "defmacro")) @definition.macro
(defun_header keyword: (defun_keyword) @_k function_name: (sym_lit) @name (#eq? @_k "defmethod")) @definition.method
((list_lit . (sym_lit) @_h . (sym_lit) @name) @definition.class (#any-of? @_h "defclass" "defstruct"))
((list_lit . (sym_lit) @_h . (sym_lit) @name) @definition.variable (#any-of? @_h "defvar" "defparameter" "defconstant"))
((list_lit . (sym_lit) @_h . (kwd_lit) @name) @definition.module (#eq? @_h "defpackage"))
((list_lit . (sym_lit) @_h . (kwd_lit) @package) (#eq? @_h "in-package"))
` + lispCallsCL,
		want: []string{"definition.function:add", "definition.macro:twice", "definition.method:area", "definition.class:point", "definition.variable:*count*", "definition.module::demo", "package::demo", "reference.call:helper", "reference.call:compute"},
	},
	{
		name: "elisp", exts: []string{".el"}, lang: elisp.Language,
		sample: ";;; demo.el --- demo\n(require 'cl-lib)\n(defvar demo-count 0)\n(defun demo-add (a b) (demo-helper a) (+ a b))\n(defmacro demo-twice (x) `(progn ,x ,x))\n(provide 'demo)\n",
		query: `
(function_definition name: (symbol) @name) @definition.function
(macro_definition name: (symbol) @name) @definition.macro
((special_form . _ @_h . (symbol) @name) @definition.variable (#any-of? @_h "defvar" "defconst" "defcustom"))
((list . (symbol) @_h . (quote (symbol) @import)) (#eq? @_h "require"))
((list . (symbol) @_h . (quote (symbol) @package)) (#eq? @_h "provide"))
((list . (symbol) @name) @reference.call (#not-any-of? @name "require" "provide"))`,
		want: []string{"definition.function:demo-add", "definition.macro:demo-twice", "definition.variable:demo-count", "import:cl-lib", "package:demo", "reference.call:demo-helper"},
	},
	{
		name: "clojure", exts: []string{".clj", ".cljs", ".cljc", ".edn"}, lang: clojure.Language,
		sample: "(ns demo.core\n  (:require [clojure.string :as str]))\n(def counter 0)\n(defn add [a b] (helper a) (+ a b))\n(defmacro twice [x] `(do ~x ~x))\n(defprotocol Shape (area [s]))\n(defrecord Box [w])\n(str/join \",\" [1 2])\n",
		query: `
((list_lit . (sym_lit name: (sym_name) @_h) . (sym_lit name: (sym_name) @name)) @definition.module (#eq? @_h "ns"))
((list_lit . (sym_lit name: (sym_name) @_h) . (sym_lit name: (sym_name) @name)) @definition.function (#any-of? @_h "defn" "defn-"))
((list_lit . (sym_lit name: (sym_name) @_h) . (sym_lit name: (sym_name) @name)) @definition.macro (#eq? @_h "defmacro"))
((list_lit . (sym_lit name: (sym_name) @_h) . (sym_lit name: (sym_name) @name)) @definition.variable (#eq? @_h "def"))
((list_lit . (sym_lit name: (sym_name) @_h) . (sym_lit name: (sym_name) @name)) @definition.interface (#eq? @_h "defprotocol"))
((list_lit . (sym_lit name: (sym_name) @_h) . (sym_lit name: (sym_name) @name)) @definition.class (#any-of? @_h "defrecord" "deftype"))
((list_lit . (kwd_lit name: (kwd_name) @_k) (vec_lit . (sym_lit) @import)) (#eq? @_k "require"))
((list_lit . (sym_lit name: (sym_name) @name)) @reference.call (#not-any-of? @name "ns" "def" "defn" "defn-" "defmacro" "defprotocol" "defrecord" "deftype"))`,
		want: []string{"definition.module:demo.core", "definition.function:add", "definition.macro:twice", "definition.variable:counter", "definition.interface:Shape", "definition.class:Box", "import:clojure.string", "reference.call:helper", "reference.call:join"},
	},
	{
		name: "scheme", exts: []string{".scm", ".ss"}, lang: scheme.Language,
		sample: "(import (scheme base))\n(define (add a b) (helper a) (+ a b))\n(define counter 0)\n(define-syntax swap! (syntax-rules () ((_ a b) (let ((t a)) (set! a b) (set! b t)))))\n(define-record-type point (make-point x) point? (x point-x))\n",
		query: `
((list . (symbol) @_h . (list . (symbol) @name)) @definition.function (#eq? @_h "define"))
((list . (symbol) @_h . (symbol) @name) @definition.variable (#eq? @_h "define"))
((list . (symbol) @_h . (symbol) @name) @definition.macro (#any-of? @_h "define-syntax" "define-macro"))
((list . (symbol) @_h . (symbol) @name) @definition.class (#eq? @_h "define-record-type"))
((list . (symbol) @_h . (list) @import) (#eq? @_h "import"))
((list . (symbol) @name) @reference.call (#not-any-of? @name "define" "define-syntax" "define-record-type" "import" "let" "set!" "syntax-rules" "_"))`,
		want: []string{"definition.function:add", "definition.variable:counter", "definition.macro:swap!", "definition.class:point", "import:(scheme base)", "reference.call:helper"},
	},
	{
		name: "racket", exts: []string{".rkt"}, lang: racket.Language,
		sample: "#lang racket\n(require racket/list)\n(provide add)\n(define (add a b) (helper a) (+ a b))\n(define counter 0)\n(struct point (x y))\n",
		query: `
((list . (symbol) @_h . (list . (symbol) @name)) @definition.function (#eq? @_h "define"))
((list . (symbol) @_h . (symbol) @name) @definition.variable (#eq? @_h "define"))
((list . (symbol) @_h . (symbol) @name) @definition.class (#eq? @_h "struct"))
((list . (symbol) @_h . (symbol) @import) (#eq? @_h "require"))
((list . (symbol) @name) @reference.call (#not-any-of? @name "define" "struct" "require" "provide"))`,
		want: []string{"definition.function:add", "definition.variable:counter", "definition.class:point", "import:racket/list", "reference.call:helper"},
	},
}

// captures runs q over the tree and returns "capture:text" for every
// non-underscore capture, keyed for definitions and references by the
// @name capture of the same match.
func captures(q *ts.Query, tree *ts.Tree, lang *ts.Language, src []byte) []string {
	var out []string
	cur := q.Exec(tree.RootNode(), lang, src)
	for {
		m, ok := cur.NextMatch()
		if !ok {
			break
		}
		var name, kind string
		for _, c := range m.Captures {
			switch {
			case c.Name == "name":
				name = c.Text(src)
			case strings.HasPrefix(c.Name, "definition.") || strings.HasPrefix(c.Name, "reference."):
				kind = c.Name
			case strings.HasPrefix(c.Name, "_"):
			default:
				out = append(out, c.Name+":"+c.Text(src))
			}
		}
		if kind != "" && name != "" {
			out = append(out, kind+":"+name)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

// TestSpikeGrammars checks that every grammar loads, parses its sample
// without an error node, compiles a tags-style query and yields the
// expected definitions, references and imports.
func TestSpikeGrammars(t *testing.T) {
	for _, sl := range spikeLangs {
		t.Run(sl.name, func(t *testing.T) {
			lang := sl.lang()
			src := []byte(sl.sample)
			tree, err := ts.NewParser(lang).Parse(src)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			defer tree.Release()
			if tree.RootNode().HasError() {
				t.Errorf("sample has an error node:\n%s", tree.RootNode().SExpr(lang))
			}
			q, err := ts.NewQuery(sl.query, lang)
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			got := captures(q, tree, lang, src)
			for _, w := range sl.want {
				if !slices.Contains(got, w) {
					t.Errorf("missing %q; got %q", w, got)
				}
			}
		})
	}
}

// TestSpikeKnownDivergences records inputs where gotreesitter v0.56.0 and
// the C runtime (tree-sitter 0.25.10 CLI on the same pinned grammar)
// disagree. It fails once they agree, so the note can be dropped.
func TestSpikeKnownDivergences(t *testing.T) {
	src := []byte("class A { fun f(): Int }\n")
	tree, err := ts.NewParser(kotlin.Language()).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	defer tree.Release()
	if !tree.RootNode().HasError() {
		t.Errorf("kotlin one-line class body now parses cleanly; drop the divergence note")
	}
}

// corpusStats is the result of parsing one directory tree for one grammar.
type corpusStats struct {
	files, errFiles, timeouts int
	bytes                     int64
	elapsed                   time.Duration
	captures                  int
	errPaths                  []string
	stops                     map[ts.ParseStopReason]int
	slowest, parseDur, query  time.Duration
	slowestPath               string
}

// TestSpikeCorpus parses real repositories. CODEPARSE_SPIKE_CORPUS names a
// directory whose subdirectories are named after the grammars above (one
// public repository each); CODEPARSE_SPIKE_REPOS is a colon-separated list
// of further repositories parsed with every grammar whose extensions match
// (clued, clued-cloud). CODEPARSE_SPIKE_ERRS, when set, is a directory that
// receives one <lang>.txt per grammar with the files that had an error node,
// for comparison with the C runtime.
func TestSpikeCorpus(t *testing.T) {
	corpus, repos := os.Getenv("CODEPARSE_SPIKE_CORPUS"), os.Getenv("CODEPARSE_SPIKE_REPOS")
	if corpus == "" && repos == "" {
		t.Skip("CODEPARSE_SPIKE_CORPUS / CODEPARSE_SPIKE_REPOS not set")
	}
	workers := runtime.GOMAXPROCS(0)
	if w, err := strconv.Atoi(os.Getenv("CODEPARSE_SPIKE_WORKERS")); err == nil && w > 0 {
		workers = w
	}
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // samples the heap every 5 ms for the peak
		defer wg.Done()
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for {
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
			metrics.Read(sample)
			if v := sample[0].Value.Uint64(); v > peak.Load() {
				peak.Store(v)
			}
		}
	}()

	type job struct {
		sl   spikeLang
		root string
	}
	var jobs []job
	for _, sl := range spikeLangs {
		if corpus != "" {
			if _, err := os.Stat(filepath.Join(corpus, sl.name)); err == nil {
				jobs = append(jobs, job{sl, filepath.Join(corpus, sl.name)})
			}
		}
		for _, r := range strings.Split(repos, ":") {
			if r != "" {
				jobs = append(jobs, job{sl, r})
			}
		}
	}
	var total corpusStats
	for _, j := range jobs {
		st := parseTree(t, j.sl, j.root, workers)
		if st.files == 0 {
			continue
		}
		total.files += st.files
		total.bytes += st.bytes
		total.elapsed += st.elapsed
		t.Logf("%-10s %-38s files=%5d KiB=%7d errFiles=%4d (%.1f%%) stopped=%d %v captures=%7d files/s=%7.0f MiB/s=%5.2f parse=%s query=%s slowest=%s %s",
			j.sl.name, filepath.Base(j.root), st.files, st.bytes>>10, st.errFiles, 100*float64(st.errFiles)/float64(st.files), st.timeouts, st.stops,
			st.captures, float64(st.files)/st.elapsed.Seconds(), float64(st.bytes)/(1<<20)/st.elapsed.Seconds(),
			st.parseDur.Round(time.Millisecond), st.query.Round(time.Millisecond),
			st.slowest.Round(time.Millisecond), filepath.Base(st.slowestPath))
		if dir := os.Getenv("CODEPARSE_SPIKE_ERRS"); dir != "" && strings.HasPrefix(j.root, corpus) {
			_ = os.WriteFile(filepath.Join(dir, j.sl.name+".txt"), []byte(strings.Join(st.errPaths, "\n")+"\n"), 0o644)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("TOTAL files=%d MiB=%.1f wall=%s files/s=%.0f peakHeapObjects=%.0fMiB workers=%d",
		total.files, float64(total.bytes)/(1<<20), total.elapsed.Round(time.Millisecond),
		float64(total.files)/total.elapsed.Seconds(), float64(peak.Load())/(1<<20), workers)
}

// parseTree parses (and queries) every file under root that matches sl's
// extensions, on workers goroutines, each with its own parser.
func parseTree(t *testing.T, sl spikeLang, root string, workers int) corpusStats {
	t.Helper()
	var paths []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if slices.Contains(sl.exts, filepath.Ext(p)) {
			if info, err := d.Info(); err == nil && info.Size() <= 1<<20 {
				paths = append(paths, p)
			}
		}
		return nil
	})
	st := corpusStats{stops: map[ts.ParseStopReason]int{}}
	if len(paths) == 0 {
		return st
	}
	lang := sl.lang()
	q, err := ts.NewQuery(sl.query, lang)
	if err != nil {
		t.Fatalf("%s query: %v", sl.name, err)
	}
	var mu sync.Mutex
	ch := make(chan string)
	var wg sync.WaitGroup
	start := time.Now()
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := ts.NewParser(lang)
			p.SetTimeoutMicros(2_000_000) // the plan's 2 s cut-off
			for path := range ch {
				src, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				t0 := time.Now()
				tree, err := p.Parse(src)
				took := time.Since(t0)
				if err != nil || tree == nil {
					mu.Lock()
					st.files++
					st.errFiles++
					st.errPaths = append(st.errPaths, path)
					mu.Unlock()
					continue
				}
				t1 := time.Now()
				n := len(captures(q, tree, lang, src))
				qd := time.Since(t1)
				reason := tree.ParseStopReason()
				stopped := reason != ts.ParseStopAccepted && reason != ts.ParseStopNone
				hasErr := tree.RootNode().HasError()
				tree.Release()
				mu.Lock()
				st.files++
				st.bytes += int64(len(src))
				st.captures += n
				st.parseDur += took
				st.query += qd
				if stopped {
					st.timeouts++
					st.stops[reason]++
				}
				if took > st.slowest {
					st.slowest, st.slowestPath = took, path
				}
				if hasErr || stopped {
					st.errFiles++
					st.errPaths = append(st.errPaths, path)
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		ch <- p
	}
	close(ch)
	wg.Wait()
	st.elapsed = time.Since(start)
	sort.Strings(st.errPaths)
	return st
}
