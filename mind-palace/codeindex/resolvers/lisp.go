package resolvers

import (
	"context"
	"path"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Lisp family: Common Lisp, Emacs Lisp, Clojure, Scheme and Racket, the
// dialects of the one Lisp language (Facts.Dialect, else the file's
// extension). Each dialect names its packages its own way, so a package key
// is the dialect's group and a name ("commonlisp:alexandria",
// "clojure:my.app.core", "scheme:lib/util"), and a definition is visible
// only from its own group (Scheme and Racket share one):
//
//   - Common Lisp: a file belongs to the package of its in-package form
//     (cl-user without one; a file whose only package form is the
//     defpackage of one package, as package.lisp, belongs to that
//     package). Package names compare without case and without their
//     #:, : or string spelling. The :use, :import-from and
//     :local-nicknames clauses of a package's defpackage hold for every
//     file of the package: :use opens the used package, :import-from binds
//     its names, a local nickname is a qualifier. pkg:name and pkg::name
//     reach any package of the project.
//   - Clojure: a file belongs to its ns (user without one). A :require
//     binds the namespace's full name and its :as alias as qualifiers and
//     its :refer names as bare names; :import (Java classes) binds
//     nothing; ns/name reaches any namespace of the project.
//   - Emacs Lisp: one global namespace; require and provide load code but
//     scope nothing.
//   - Scheme and Racket: a file is its own module, keyed by its path
//     without the extension, unless it declares a library name
//     (define-library, define-module, as the package or a top-level module
//     definition: (foo bar) is foo/bar). An import or
//     require names a module by a path relative to the file ("util.rkt"),
//     a library name ((foo bar)) or a collection path (mylib/util), looked
//     up from the file's directory towards the root; a whole-module import
//     opens it, only-in names bind bare names and a prefix (prefix-in,
//     (prefix (lib) p:)) binds a qualifier.
//
// Names compare without case for the whole family, as Common Lisp reads
// them (the other dialects rarely tell two definitions apart by case
// alone). A bare name no scope defines falls back to any definition of the
// dialect group with that name (confidence name); a qualifier that names no
// package of the project leaves the reference unresolved (no member
// fallback: a Lisp qualifier is a package, never an expression). A Scheme or
// Racket prefix (prefix-in str:, (prefix (lib) p:)) is bound as a
// qualifier, so a reference reaches it only when the extractor splits the
// prefix off the name.
func init() { Register(codeparse.FamilyLisp, lisp{}) }

type lisp struct{}

var lispRules = Rules{Separators: []string{":", "::", "/"}, CaseInsensitive: true, NameFallback: true}

// Lisp dialects (Facts.Dialect, codeparse's Languages table).
const (
	LispCommonLisp = "commonlisp"
	LispElisp      = "elisp"
	LispClojure    = "clojure"
	LispScheme     = "scheme"
	LispRacket     = "racket"
)

// Rules implements Resolver.
func (lisp) Rules() Rules { return lispRules }

// LispDialect is the dialect of a Lisp file: its facts' dialect, else the
// one its extension implies, else Common Lisp (the language's own
// grammar).
func LispDialect(f *File) string {
	if f.Facts != nil && f.Facts.Dialect != "" {
		return f.Facts.Dialect
	}
	if d := codeparse.Detect(f.Path, nil, nil).Dialect; d != "" {
		return d
	}
	return LispCommonLisp
}

// lispGroup is the dialect whose packages a dialect shares: Racket's
// modules are Scheme's.
func lispGroup(dialect string) string {
	if dialect == LispRacket {
		return LispScheme
	}
	return dialect
}

func lispKey(group, name string) string { return group + ":" + name }

// LispPackageName is the canonical name of a Common Lisp package as written
// in in-package, defpackage or a qualifier (:foo, #:foo, "FOO", 'foo, foo):
// lower case, without its reader prefix or quotes; common-lisp-user is
// cl-user and common-lisp is cl.
func LispPackageName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "'#:")
	s = strings.Trim(s, `"|`)
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "common-lisp-user":
		return "cl-user"
	case "common-lisp":
		return "cl"
	}
	return s
}

// SchemeLibraryName is a Scheme library name as a path: (foo bar) and
// (foo bar (1 2)) are foo/bar, a symbol is itself.
func SchemeLibraryName(s string) string {
	s = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "'"))
	if !strings.HasPrefix(s, "(") {
		return strings.Trim(s, `"`)
	}
	var parts []string
	depth := 0
	for _, tok := range strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(s)) {
		switch tok {
		case "(":
			depth++
		case ")":
			depth--
		default:
			if depth == 1 { // a version list is one level deeper
				parts = append(parts, tok)
			}
		}
	}
	return strings.Join(parts, "/")
}

// clojureName is a namespace as written, without a quote.
func clojureName(s string) string { return strings.TrimLeft(strings.TrimSpace(s), "'") }

// Package implements Resolver.
func (lisp) Package(_ context.Context, _ View, f *File) (string, error) {
	declared := ""
	if f.Facts != nil {
		declared = f.Facts.Package
	}
	switch g := lispGroup(LispDialect(f)); g {
	case LispCommonLisp:
		name := LispPackageName(declared)
		if name == "" || name == "cl-user" {
			if defined := lispDefinedPackages(f); len(defined) == 1 {
				name = defined[0].name
			}
		}
		if name == "" {
			name = "cl-user"
		}
		return lispKey(g, name), nil
	case LispClojure:
		name := clojureName(declared)
		if name == "" {
			name = "user"
		}
		return lispKey(g, name), nil
	case LispScheme:
		if name := SchemeLibraryName(declared); name != "" {
			return lispKey(g, name), nil
		}
		if name := schemeDeclaredLibrary(f); name != "" {
			return lispKey(g, name), nil
		}
		return lispKey(g, strings.TrimSuffix(f.Path, path.Ext(f.Path))), nil
	default: // Emacs Lisp: one global namespace
		return lispKey(g, ""), nil
	}
}

// schemeDeclaredLibrary is the library a Scheme file declares
// (define-library, define-module, library: a top-level module whose name is
// a list), "" when it declares none or several. A Racket module form names
// a submodule, never the file.
func schemeDeclaredLibrary(f *File) string {
	if f.Facts == nil || LispDialect(f) != LispScheme {
		return ""
	}
	name := ""
	for _, s := range f.Facts.Symbols {
		if s.Parent != "" || s.Kind != codeparse.KindModule || !strings.HasPrefix(strings.TrimSpace(s.Name), "(") {
			continue
		}
		if name != "" {
			return ""
		}
		name = SchemeLibraryName(s.Name)
	}
	return name
}

// lispPackageDef is a Common Lisp defpackage of a file: the package's name
// and the span of its form.
type lispPackageDef struct {
	name       string
	start, end int
}

// lispDefinedPackages returns the packages f defines at top level.
func lispDefinedPackages(f *File) []lispPackageDef {
	if f.Facts == nil {
		return nil
	}
	var out []lispPackageDef
	for _, s := range f.Facts.Symbols {
		if s.Parent == "" && s.Kind == codeparse.KindPackage {
			if n := LispPackageName(s.Name); n != "" {
				out = append(out, lispPackageDef{n, s.Start.Offset, s.End.Offset})
			}
		}
	}
	return out
}

// Scope implements Resolver.
func (lisp) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	switch g := lispGroup(LispDialect(f)); g {
	case LispCommonLisp:
		commonLispScope(&s, v, f)
	case LispClojure:
		clojureScope(&s, v, f)
	case LispScheme:
		schemeScope(&s, v, f)
	}
	return s, nil
}

// commonLispScope collects the :use and :import-from clauses of f's
// package, from the defpackage forms of every file of the package (f
// included), and binds the package qualifiers f's references write.
func commonLispScope(s *Scope, v View, f *File) {
	files := []*File{f}
	for _, o := range v.Package(codeparse.FamilyLisp, f.Package) {
		if o.Path != f.Path {
			files = append(files, o)
		}
	}
	for _, o := range files {
		if o.Facts == nil {
			continue
		}
		defs := lispDefinedPackages(o)
		for _, im := range o.Facts.Imports {
			owner := ""
			for _, d := range defs {
				if im.Start.Offset >= d.start && im.Start.Offset <= d.end {
					owner = d.name
				}
			}
			switch {
			case owner != "" && lispKey(LispCommonLisp, owner) != f.Package:
				continue // another package's defpackage
			case owner == "" && o != f:
				continue // a top-level form of another file
			}
			pkg := lispKey(LispCommonLisp, LispPackageName(im.Spec))
			switch {
			case len(im.Names) > 0:
				for _, n := range im.Names {
					if n = LispPackageName(n); n != "" {
						s.AddName(n, pkg)
					}
				}
			case im.Alias != "":
				s.AddQualifier(im.Alias, pkg) // :local-nicknames
			case im.Kind == "use":
				s.AddOpen(pkg)
			}
		}
	}
	// Names compare without case: bind the spellings f writes too.
	for _, r := range f.Facts.Refs {
		if r.Qualifier != "" {
			continue
		}
		if pkgs, ok := s.Names[strings.ToLower(r.Name)]; ok && r.Name != strings.ToLower(r.Name) {
			for _, p := range pkgs {
				s.AddName(r.Name, p)
			}
		}
	}
	qualifyRefs(s, v, f, func(q string) string { return lispKey(LispCommonLisp, LispPackageName(q)) })
}

// clojureScope binds f's requires.
func clojureScope(s *Scope, v View, f *File) {
	for _, im := range f.Facts.Imports {
		if im.Kind == "import" {
			continue // Java classes
		}
		spec := clojureName(im.Spec)
		if spec == "" {
			continue
		}
		pkg := lispKey(LispClojure, spec)
		s.AddQualifier(spec, pkg)
		if im.Alias != "" {
			s.AddQualifier(clojureName(im.Alias), pkg)
		}
		for _, n := range im.Names {
			s.AddName(clojureName(n), pkg)
		}
	}
	qualifyRefs(s, v, f, func(q string) string { return lispKey(LispClojure, clojureName(q)) })
}

// qualifyRefs binds each qualifier f's references write that is not bound
// yet and names a package of the project (key gives its package key).
func qualifyRefs(s *Scope, v View, f *File, key func(string) string) {
	for _, r := range f.Facts.Refs {
		if r.Qualifier == "" {
			continue
		}
		q := lispQualifierHead(r.Qualifier)
		if _, ok := s.Qualifiers[q]; ok || q == "" {
			continue
		}
		if pkg := key(q); len(v.Package(codeparse.FamilyLisp, pkg)) > 0 {
			s.AddQualifier(q, pkg)
		}
	}
}

// lispQualifierHead is the first part of a qualifier, the part the engine
// looks up in Scope.Qualifiers.
func lispQualifierHead(q string) string {
	for _, sep := range []string{"::", ":", "/"} {
		q = strings.Split(q, sep)[0]
	}
	return q
}

// schemeScope binds f's imports, requires and includes.
func schemeScope(s *Scope, v View, f *File) {
	for _, im := range f.Facts.Imports {
		if im.Kind == "lang" {
			continue // #lang racket/base: the language, not a module of the project
		}
		pkg, ok := schemeModule(v, f, im)
		if !ok {
			continue
		}
		switch {
		case im.Alias != "":
			alias := strings.TrimRight(im.Alias, ":")
			if alias == "" {
				alias = im.Alias
			}
			s.AddQualifier(alias, pkg)
			for _, n := range im.Names {
				s.AddName(n, pkg)
			}
		case len(im.Names) > 0:
			for _, n := range im.Names {
				s.AddName(n, pkg)
			}
		default:
			s.AddOpen(pkg)
		}
	}
}

// schemeModule finds the package key of the module an import names, among
// the project's files.
func schemeModule(v View, f *File, im codeparse.Import) (string, bool) {
	has := func(name string) (string, bool) {
		k := lispKey(LispScheme, name)
		return k, name != "" && len(v.Package(codeparse.FamilyLisp, k)) > 0
	}
	spec := strings.TrimSpace(im.Spec)
	if strings.HasPrefix(spec, `"`) || im.Kind == "include" || lispFileExt(spec) {
		// A file, relative to the importing one.
		rel := strings.Trim(spec, `"`)
		if rel == "" || path.IsAbs(rel) {
			return "", false
		}
		p := path.Join(f.Dir(), rel)
		return has(strings.TrimSuffix(p, path.Ext(p)))
	}
	name := SchemeLibraryName(spec)
	if name == "" || path.IsAbs(name) {
		return "", false
	}
	if k, ok := has(name); ok { // a declared library, or a module at the root
		return k, true
	}
	first, rest, _ := strings.Cut(name, "/")
	for d := f.Dir(); ; d = path.Dir(d) {
		try := []string{path.Join(d, name), path.Join(d, name, "main")}
		if path.Base(d) == first { // d is the collection the path starts with
			if rest != "" {
				try = append(try, path.Join(d, rest))
			} else {
				try = append(try, path.Join(d, "main"))
			}
		}
		for _, n := range try {
			if k, ok := has(n); ok {
				return k, true
			}
		}
		if d == "." || d == "/" {
			return "", false
		}
	}
}

// lispFileExt reports whether spec names a file by a Lisp extension
// ("util.rkt", "private/util.rkt"), unlike a dotted module name
// (chicken.string).
func lispFileExt(spec string) bool {
	if strings.HasPrefix(spec, "(") {
		return false
	}
	switch path.Ext(spec) {
	case ".rkt", ".scm", ".ss", ".sls", ".sld", ".sch", ".lisp", ".lsp", ".cl", ".el":
		return true
	}
	return false
}

// Visible implements Resolver: a definition of the same dialect group, of
// the same package or exported.
func (lisp) Visible(f *File, c Candidate, _ Step) bool {
	if lispGroup(LispDialect(c.File)) != lispGroup(LispDialect(f)) {
		return false
	}
	return c.File.Package == f.Package || c.Symbol.Exported
}
