package resolvers

import (
	"context"
	"path"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Lua family. Every file is a module of its own (the profile's
// packageBy: file): its key is its path without the .lua extension
// (lib/base.lua → lib/base, lib/base/init.lua → lib/base/init).
//
// require("a.b") names the module package.path finds for a.b: the files
// a/b.lua and a/b/init.lua at the project's root, then under the usual
// source roots (src, lua, lib), then relative to the requiring file's
// directory and each directory above it, nearest first; the first that
// the project holds is the module. A require the project cannot satisfy
// (cjson, socket.http) keys the module by its path (cjson, socket/http),
// which no file has, so references through it stay unresolved.
//
// What a require brings into scope:
//   - local m = require("a.b") binds m as the module's qualifier: m.f, m.T.f
//     and m:f reach the module's definitions (the table the module returns
//     and what it holds);
//   - local f = require("a.b").f binds the bare name f to the module;
//   - a require whose value is not kept (require "a.b", pcall(require,
//     "a.b")) runs the module for its globals: the module is open;
//   - dofile("x.lua") and loadfile("x.lua") (kind include) open the chunk
//     they name, a path from the project's root or the file's directory.
//
// Lua has no export form: a global (function f(), f = function, M = {}) is
// reachable from every chunk, a local only in its file, and a module's API
// is the table it returns. The extractor marks globals, the returned table
// and every function stored in a table as exported; a global's declaration
// does not start with "local". So another file's definition is visible:
//   - through a require's qualifier (StepImport, StepQualified): any
//     top-level definition (the returned table, and the locals a module
//     returns as { f = f }) or exported member;
//   - as a bare name (StepOpen, StepName): only a global;
//   - as a member through an expression (StepMember): an exported member of
//     a table that is itself exported (a global or the returned table), so
//     self:speak() never reaches into another file's private class.
func init() { Register(codeparse.FamilyLua, lua{}) }

type lua struct{}

var luaRules = Rules{Separators: []string{".", ":"}, NameFallback: true, MemberFallback: true}

// Rules implements Resolver.
func (lua) Rules() Rules { return luaRules }

// LuaExt is the extension of a Lua module file.
const LuaExt = ".lua"

// luaImportInclude is the import kind of dofile and loadfile (the
// extractor's import.kind).
const luaImportInclude = "include"

// luaRoots are the directories, below the project's root, that a Lua
// project's package.path commonly starts at.
var luaRoots = []string{"src", "lua", "lib"}

// Package implements Resolver: the file's path without its extension.
func (lua) Package(_ context.Context, _ View, f *File) (string, error) {
	return LuaModuleKey(f.Path), nil
}

// LuaModuleKey is the package key of the Lua file at p: p without its
// extension.
func LuaModuleKey(p string) string { return strings.TrimSuffix(p, path.Ext(p)) }

// Scope implements Resolver.
func (lua) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	for _, im := range f.Facts.Imports {
		if im.Kind == luaImportInclude {
			if key, ok := luaInclude(v, f, im.Spec); ok {
				s.AddOpen(key)
			}
			continue
		}
		key, ok := LuaRequire(v, f.Dir(), im.Spec)
		if !ok {
			continue
		}
		switch {
		case len(im.Names) > 0:
			for _, n := range im.Names {
				s.AddName(n, key)
			}
		case im.Alias != "":
			s.AddQualifier(im.Alias, key)
		default:
			s.AddOpen(key)
		}
	}
	return s, nil
}

// LuaRequire returns the package key of the module require(spec) names
// from a file in directory dir: the first module the view holds among the
// candidates (see the family's comment), else the spec's own path. False
// when spec is not a module name (empty, absolute, or climbing out of the
// project with "..").
func LuaRequire(v View, dir, spec string) (string, bool) {
	rel, ok := luaModulePath(spec)
	if !ok {
		return "", false
	}
	var bases []string
	bases = append(bases, ".")
	bases = append(bases, luaRoots...)
	for d := dir; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		bases = append(bases, d)
	}
	for _, b := range bases {
		for _, key := range []string{path.Join(b, rel), path.Join(b, rel, "init")} {
			if len(v.Package(codeparse.FamilyLua, key)) > 0 {
				return key, true
			}
		}
	}
	return rel, true
}

// luaModulePath turns a module name into a path below a package.path root
// (a.b → a/b), false when it is not a local path.
func luaModulePath(spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || strings.Contains(spec, "..") {
		return "", false
	}
	rel := path.Clean(strings.ReplaceAll(spec, ".", "/"))
	if rel == "." || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// luaInclude returns the package key of the chunk dofile or loadfile
// names: spec from the project's root, then from the file's directory.
func luaInclude(v View, f *File, spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || path.IsAbs(spec) {
		return "", false
	}
	for _, base := range []string{".", f.Dir()} {
		p := path.Join(base, spec)
		if p == ".." || strings.HasPrefix(p, "../") {
			continue
		}
		key := LuaModuleKey(p)
		if len(v.Package(codeparse.FamilyLua, key)) > 0 {
			return key, true
		}
	}
	return "", false
}

// Visible implements Resolver.
func (lua) Visible(f *File, c Candidate, step Step) bool {
	if c.File == f || c.File.Package == f.Package {
		return true
	}
	switch step {
	case StepImport:
		return c.TopLevel() || c.Symbol.Exported
	case StepQualified:
		return c.Symbol.Exported || (c.TopLevel() && luaGlobal(c.Symbol))
	case StepMember:
		return c.Symbol.Exported && luaTableExported(c)
	}
	return c.TopLevel() && luaGlobal(c.Symbol)
}

// luaGlobal reports whether a top-level definition is a global: exported
// and not declared local (the returned table is exported but local).
func luaGlobal(sym *codeparse.Symbol) bool {
	return sym.Exported && !strings.HasPrefix(sym.Signature, "local ")
}

// luaTableExported reports whether the table a member belongs to (the
// first part of its qualified name, a top-level definition of its file) is
// exported: a global or the table the file returns. A member whose table
// the file does not define keeps its own mark.
func luaTableExported(c Candidate) bool {
	root := c.Symbol.QualName
	if i := strings.IndexAny(root, ".:"); i >= 0 {
		root = root[:i]
	}
	if c.File.Facts == nil {
		return c.Symbol.Exported
	}
	for i := range c.File.Facts.Symbols {
		s := &c.File.Facts.Symbols[i]
		if s.Parent == "" && s.Name == root && s != c.Symbol {
			return s.Exported
		}
	}
	return c.Symbol.Exported
}
