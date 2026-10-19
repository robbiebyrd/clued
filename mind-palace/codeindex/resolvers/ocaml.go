package resolvers

import (
	"context"
	"path"
	"regexp"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The OCaml family. Every file is a module named after the file (foo.ml
// and foo.mli are module Foo), so a package key names one module:
//
//   - a module of a wrapped dune library (a dune file whose library stanza
//     covers the file: in its directory, or above it with include_subdirs)
//     is <Library>.<Module> (Geo.Shapes), the path other libraries reach
//     it by;
//   - a module of an unwrapped library ((wrapped false)) is <Module>;
//   - any other module (an executable, a test, a project without dune) is
//     <directory>/<Module>, <Module> at the root.
//
// An .mli is the interface of the .ml beside it: when both are visible the
// interface's key carries OCamlInterfaceSuffix, so a reference from another
// module reaches the implementation's definition alone (a program that
// compiles names only what the .mli declares); an .mli without an .ml is
// the module.
//
// Modules are reached without imports: a qualifier (Module.name,
// Module.Sub.name) is bound to the module the compiler would pick from the
// file, and Lib.Module (Geo.Shapes.area) to the module of a wrapped
// library by its path — its own library's module, a module of the same directory, a module
// of a library the file opens (open Geo), a library's main module (geo.ml
// of library geo), then an unwrapped or root module. A qualifier that names
// a module defined in the file (or a functor's parameter) is left to the
// engine's qualified-name and member rules; one that names no module of the
// project (List, Hashtbl) is bound to an empty package, so it stays
// unresolved, unless a module the file opens defines a module of that
// name. A module alias (module L = List) binds its name like its
// target. open, include and local opens (let open M in, M.(e)) make the
// module's definitions reachable unqualified in the whole file. Bare names
// never leave the file's module except through an open.
func init() { Register(codeparse.FamilyOCaml, ocaml{}) }

type ocaml struct{}

var ocamlRules = Rules{Separators: []string{"."}, MemberFallback: true}

// OCamlInterfaceSuffix ends the package key of an .mli whose .ml is
// visible beside it.
const OCamlInterfaceSuffix = "#mli"

// Profile option names and defaults the OCaml family reads
// (codeparse/queries/ocaml/profile.yaml, scope.options).
const (
	ocamlOptInterface      = "interfaceExtension"
	ocamlOptImplementation = "implementationExtension"
	ocamlOptLibraryFile    = "libraryFile"

	OCamlInterfaceExt      = ".mli"
	OCamlImplementationExt = ".ml"
	OCamlLibraryFile       = "dune"
)

// symbol kind the OCaml profile gives a module with parameters.
const ocamlKindFunctor = "functor"

// Rules implements Resolver.
func (ocaml) Rules() Rules { return ocamlRules }

type ocamlOptions struct{ intf, impl, libraryFile string }

func ocamlOpts(f *File) ocamlOptions {
	o := ocamlOptions{intf: OCamlInterfaceExt, impl: OCamlImplementationExt, libraryFile: OCamlLibraryFile}
	if f.Profile == nil {
		return o
	}
	opts := f.Profile.Scope.Options
	if v := opts[ocamlOptInterface]; v != "" {
		o.intf = v
	}
	if v := opts[ocamlOptImplementation]; v != "" {
		o.impl = v
	}
	if v := opts[ocamlOptLibraryFile]; v != "" {
		o.libraryFile = v
	}
	return o
}

// OCamlModuleName is the module a file defines: its name without the
// extension, first letter capitalized (shapes.ml → Shapes).
func OCamlModuleName(p string) string {
	base := path.Base(p)
	base = strings.TrimSuffix(base, path.Ext(base))
	if base != "" && base[0] >= 'a' && base[0] <= 'z' {
		base = string(base[0]-'a'+'A') + base[1:]
	}
	return base
}

// Package implements Resolver.
func (ocaml) Package(ctx context.Context, v View, f *File) (string, error) {
	o := ocamlOpts(f)
	mod := OCamlModuleName(f.Path)
	lib, err := ocamlLibraryOf(ctx, v, o, f.Dir(), mod)
	if err != nil {
		return "", err
	}
	key := lib.key(f.Dir(), mod)
	if ext := path.Ext(f.Path); ext == o.intf && v.Has(strings.TrimSuffix(f.Path, ext)+o.impl) {
		key += OCamlInterfaceSuffix
	}
	return key, nil
}

// ocamlLib is the dune library a module belongs to ("" for none).
type ocamlLib struct {
	name      string
	unwrapped bool
}

// key is the package key of module mod of directory dir in the library.
func (l ocamlLib) key(dir, mod string) string {
	switch {
	case l.name == "":
		return path.Join(dir, mod)
	case l.unwrapped:
		return mod
	}
	return OCamlModuleName(l.name) + "." + mod
}

// ocamlLibraryOf finds the library module mod of directory dir belongs to:
// the nearest dune file with a library, executable or test stanza, in dir
// itself or, with include_subdirs, above it.
func ocamlLibraryOf(ctx context.Context, v View, o ocamlOptions, dir, mod string) (ocamlLib, error) {
	for d := dir; ; d = path.Dir(d) {
		file := o.libraryFile
		if d != "." {
			file = d + "/" + file
		}
		if v.Has(file) {
			content, ok, err := v.Content(ctx, file)
			if err != nil {
				return ocamlLib{}, err
			}
			if ok {
				st := parseDune(content)
				if d == dir || st.includeSubdirs {
					if lib, ok := st.libraryOf(mod); ok {
						return lib, nil
					}
				}
				if st.buildsModules {
					return ocamlLib{}, nil
				}
			}
		}
		if d == "." || d == "/" {
			return ocamlLib{}, nil
		}
	}
}

// duneFile is what the family reads of a dune file.
type duneFile struct {
	libraries      []duneLibrary
	buildsModules  bool // a library, executable(s) or test(s) stanza
	includeSubdirs bool
}

type duneLibrary struct {
	ocamlLib
	modules []string // an explicit (modules ...) list, nil for all
}

// libraryOf is the library whose modules include mod: the one listing it,
// else the first without a list.
func (d duneFile) libraryOf(mod string) (ocamlLib, bool) {
	for _, l := range d.libraries {
		for _, m := range l.modules {
			if strings.EqualFold(m, mod) {
				return l.ocamlLib, true
			}
		}
	}
	for _, l := range d.libraries {
		if l.modules == nil {
			return l.ocamlLib, true
		}
	}
	return ocamlLib{}, false
}

func parseDune(content []byte) duneFile {
	var d duneFile
	for _, st := range parseSexps(string(content)) {
		list, ok := st.([]any)
		if !ok || len(list) == 0 {
			continue
		}
		head, _ := list[0].(string)
		switch head {
		case "library":
			d.buildsModules = true
			var lib duneLibrary
			for _, field := range list[1:] {
				fl, ok := field.([]any)
				if !ok || len(fl) < 2 {
					continue
				}
				name, _ := fl[0].(string)
				switch name {
				case "name":
					lib.name, _ = fl[1].(string)
				case "wrapped":
					lib.unwrapped = fl[1] == "false"
				case "modules":
					lib.modules = duneModuleList(fl[1:])
				}
			}
			if lib.name != "" {
				d.libraries = append(d.libraries, lib)
			}
		case "executable", "executables", "test", "tests":
			d.buildsModules = true
		case "include_subdirs":
			d.includeSubdirs = len(list) > 1 && list[1] != "no"
		}
	}
	return d
}

// duneModuleList is an explicit list of module names; nil when the field
// uses the ordered-set language (:standard, \, nested forms).
func duneModuleList(items []any) []string {
	out := []string{}
	for _, it := range items {
		s, ok := it.(string)
		if !ok || strings.HasPrefix(s, ":") || s == `\` {
			return nil
		}
		out = append(out, s)
	}
	return out
}

// parseSexps reads the s-expressions of a dune file: atoms and quoted
// strings as strings, lists as []any; comments (; …, #| … |#, #; datum)
// are dropped. Unbalanced input yields what was read.
func parseSexps(s string) []any {
	p := sexpParser{s: s}
	var out []any
	for {
		v, ok := p.next()
		if !ok {
			return out
		}
		if v != nil && v != errClose {
			out = append(out, v)
		}
	}
}

type sexpParser struct {
	s string
	i int
}

var errClose = struct{}{}

// next returns the next datum; ok false at the end of input. A closing
// parenthesis is returned as errClose for the enclosing list.
func (p *sexpParser) next() (any, bool) {
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.i++
		case c == ';':
			for p.i < len(p.s) && p.s[p.i] != '\n' {
				p.i++
			}
		case strings.HasPrefix(p.s[p.i:], "#|"):
			if j := strings.Index(p.s[p.i+2:], "|#"); j >= 0 {
				p.i += 2 + j + 2
			} else {
				p.i = len(p.s)
			}
		case strings.HasPrefix(p.s[p.i:], "#;"):
			p.i += 2
			if _, ok := p.next(); !ok {
				return nil, false
			}
		case c == '(':
			p.i++
			list := []any{}
			for {
				v, ok := p.next()
				if !ok || v == errClose {
					return list, true
				}
				list = append(list, v)
			}
		case c == ')':
			p.i++
			return errClose, true
		case c == '"':
			var b strings.Builder
			for p.i++; p.i < len(p.s) && p.s[p.i] != '"'; p.i++ {
				if p.s[p.i] == '\\' && p.i+1 < len(p.s) {
					p.i++
				}
				b.WriteByte(p.s[p.i])
			}
			p.i++
			return b.String(), true
		default:
			j := p.i
			for j < len(p.s) && !strings.ContainsRune(" \t\r\n();\"", rune(p.s[j])) {
				j++
			}
			atom := p.s[p.i:j]
			p.i = j
			return atom, true
		}
	}
	return nil, false
}

var (
	ocamlModulePath = regexp.MustCompile(`^[A-Z][A-Za-z0-9_']*(\.[A-Z][A-Za-z0-9_']*)*$`)
	ocamlFunctorArg = regexp.MustCompile(`\(\s*([A-Z][A-Za-z0-9_']*)\s*:`)
)

// Scope implements Resolver.
func (ocaml) Scope(ctx context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	o := ocamlOpts(f)
	lib, err := ocamlLibraryOf(ctx, v, o, f.Dir(), OCamlModuleName(f.Path))
	if err != nil {
		return s, err
	}
	sc := ocamlScope{v: v, dir: f.Dir(), lib: lib, local: map[string]bool{}, alias: map[string]string{}}

	// Modules the file defines, and functors' parameters.
	children := map[string]bool{}
	for _, sym := range f.Facts.Symbols {
		children[sym.Parent] = true
	}
	for _, sym := range f.Facts.Symbols {
		switch sym.Kind {
		case codeparse.KindModule:
			sc.local[sym.Name] = true
		case ocamlKindFunctor:
			sc.local[sym.Name] = true
			for _, m := range ocamlFunctorArg.FindAllStringSubmatch(sym.Signature, -1) {
				sc.local[m[1]] = true
			}
		}
	}

	// Opens of a single name that is no module may open a wrapped library.
	for _, im := range f.Facts.Imports {
		if ocamlModulePath.MatchString(im.Spec) && !strings.Contains(im.Spec, ".") && !sc.local[im.Spec] {
			if _, ok := sc.lookup(im.Spec); !ok {
				sc.libs = appendNew(sc.libs, im.Spec)
			}
		}
	}

	// Top-level module aliases (module L = List; module J = Yojson.Safe):
	// a module without definitions whose first reference is a module path.
	head := map[string]*codeparse.Ref{}
	for i := range f.Facts.Refs {
		r := &f.Facts.Refs[i]
		if r.Enclosing != "" && head[r.Enclosing] == nil {
			head[r.Enclosing] = r
		}
	}
	for _, sym := range f.Facts.Symbols {
		r := head[sym.ID]
		if sym.Kind != codeparse.KindModule || sym.Parent != "" || children[sym.ID] || r == nil {
			continue
		}
		root := r.Name
		if r.Qualifier != "" {
			root, _, _ = strings.Cut(r.Qualifier, ".")
		}
		if !ocamlModulePath.MatchString(root) || sc.local[root] {
			continue
		}
		key, found := sc.lookup(root)
		switch {
		case !found:
			sc.alias[sym.Name] = root // outside the project
		case r.Qualifier == "" && r.Kind == codeparse.RefType:
			sc.alias[sym.Name] = key
		}
	}

	// open, include and local opens.
	for _, im := range f.Facts.Imports {
		if !ocamlModulePath.MatchString(im.Spec) {
			continue
		}
		root, rest, multi := strings.Cut(im.Spec, ".")
		if key, ok := sc.alias[root]; ok {
			if !multi && len(v.Package(codeparse.FamilyOCaml, key)) > 0 {
				s.AddOpen(key)
			}
			continue
		}
		if sc.local[root] {
			continue // a module of the file: its names are its members
		}
		if multi && !strings.Contains(rest, ".") {
			if key := OCamlModuleName(root) + "." + rest; len(v.Package(codeparse.FamilyOCaml, key)) > 0 {
				s.AddOpen(key) // a module of a wrapped library
				continue
			}
		}
		if key, ok := sc.lookup(root); ok && !multi {
			s.AddOpen(key)
		}
	}

	// Qualifiers.
	for _, r := range f.Facts.Refs {
		root, rest, multi := strings.Cut(r.Qualifier, ".")
		if !ocamlModulePath.MatchString(root) {
			continue
		}
		if multi && sc.alias[root] == "" && !sc.local[root] {
			// Lib.Module.name: a module of a wrapped library by its
			// path (the engine binds the longest leading part).
			sub, _, _ := strings.Cut(rest, ".")
			if key := OCamlModuleName(root) + "." + sub; len(v.Package(codeparse.FamilyOCaml, key)) > 0 {
				s.AddQualifier(root+"."+sub, key)
			}
		}
		if _, done := s.Qualifiers[root]; done {
			continue
		}
		if key, ok := sc.alias[root]; ok {
			s.AddQualifier(root, key)
			continue
		}
		if sc.local[root] {
			continue
		}
		if key, ok := sc.lookup(root); ok {
			s.AddQualifier(root, key)
		} else if !sc.opensModule(s.Open, root) {
			s.AddQualifier(root, root) // no module of the project: an empty package
		}
	}
	return s, nil
}

// ocamlScope finds the modules a file's names reach.
type ocamlScope struct {
	v     View
	dir   string
	lib   ocamlLib
	libs  []string // libraries the file opens
	local map[string]bool
	alias map[string]string // alias → package key
}

// opensModule reports whether one of the opened packages defines a
// top-level module named name (reached unqualified through the open).
func (sc *ocamlScope) opensModule(open []string, name string) bool {
	for _, key := range open {
		for _, f := range sc.v.Package(codeparse.FamilyOCaml, key) {
			if f.Facts == nil {
				continue
			}
			for _, sym := range f.Facts.Symbols {
				if sym.Parent == "" && sym.Name == name && (sym.Kind == codeparse.KindModule || sym.Kind == ocamlKindFunctor) {
					return true
				}
			}
		}
	}
	return false
}

// lookup returns the package key of the module the file reaches by name.
func (sc *ocamlScope) lookup(name string) (string, bool) {
	keys := []string{sc.lib.key(sc.dir, name)}
	if sc.lib.name != "" {
		keys = append(keys, path.Join(sc.dir, name))
	}
	for _, l := range sc.libs {
		keys = append(keys, OCamlModuleName(l)+"."+name)
	}
	keys = append(keys, name+"."+name, name)
	for _, k := range keys {
		if len(sc.v.Package(codeparse.FamilyOCaml, k)) > 0 {
			return k, true
		}
	}
	return "", false
}

// Visible implements Resolver: an interface whose implementation is
// visible is reached from its own file only (its definitions' targets are
// the implementation's).
func (ocaml) Visible(f *File, c Candidate, _ Step) bool {
	if c.File != f && strings.HasSuffix(c.File.Package, OCamlInterfaceSuffix) {
		return false
	}
	return c.File.Package == f.Package || c.Symbol.Exported
}
