package resolvers

import (
	"context"
	"path"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Perl family. A file's package is the one its `package NAME;`
// statement declares (the extractor's Facts.Package); a file with none is
// in Perl's package main, keyed by its path so two scripts do not share
// their subs (a script that requires another opens it, below). Names are
// absolute: Package::sub names package Package wherever it is written, and
// a sub may be defined into any package (sub Other::f), so a qualified call
// is found by the definition's qualified name, not through the importing
// file's scope.
//
// Imports: `use Module LIST` binds the listed names (sigils dropped; a
// :tag opens the module), `use Module` with no list opens it (its @EXPORT
// is not known, so every exported top-level sub is reachable), and
// `require Module` imports nothing. A single-segment module the project
// holds (Carp, Fixture) is also bound as a qualifier, so Module::sub is an
// exact match in it; a module of several segments is not (the engine binds
// a qualifier by its first segment, and My of My::Report says nothing about
// My::Other). `require "file.pl"` of a script without a package opens it,
// `require "My/Module.pm"` is `require My::Module`. Pragmas (strict,
// parent, constant) import nothing.
//
// $self, $class, shift and __PACKAGE__ as an invocant name the file's own
// package, so $self->method is an exact match among its subs; that binding
// is left out when the file's subs belong to several packages, where the
// invocant's package depends on the statement it sits in (a sub declared
// by its full name, sub Other::f, does not count).
//
// Perl has no privacy: an underscore marks a sub private by convention
// (the profile's exported: noUnderscore), which keeps it from bare-name
// fallbacks and unqualified opens of another package (scripts all share
// package main), while a call that names its package or object reaches
// it. A definition reached through a
// package key (the package step, an import, an open) must belong to that
// package itself: a file whose later `package Other;` statements or `sub
// Other::f` define subs into another package keeps those out of its first
// package.
func init() { Register(codeparse.FamilyPerl, perl{}) }

type perl struct{}

var perlRules = Rules{Separators: []string{"::"}, NameFallback: true, MemberFallback: true}

// Rules implements Resolver.
func (perl) Rules() Rules { return perlRules }

// Package implements Resolver: the declared package, else the file's path
// (package main, one per script).
func (perl) Package(_ context.Context, _ View, f *File) (string, error) {
	if f.Facts != nil && f.Facts.Package != "" {
		return f.Facts.Package, nil
	}
	return f.Path, nil
}

// Import kinds the Perl extractor emits.
const (
	perlUse     = "use"
	perlRequire = "require"
	perlPragma  = "pragma"
)

// perlSelf are the invocants that name the file's own package.
var perlSelf = []string{"$self", "$class", "shift", "__PACKAGE__"}

// Scope implements Resolver.
func (perl) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	bind := func(module string) {
		if strings.Contains(module, "::") || len(v.Package(codeparse.FamilyPerl, module)) == 0 {
			return
		}
		for _, r := range f.Facts.Refs {
			if strings.HasPrefix(r.Qualifier, module+"::") {
				return // Module::Sub::f names another package
			}
		}
		s.AddQualifier(module, module)
	}
	for _, im := range f.Facts.Imports {
		spec, kind := im.Spec, im.Kind
		if kind == perlPragma {
			continue
		}
		if perlIsPath(spec) {
			if strings.HasSuffix(spec, ".pm") {
				spec = PerlModuleOfPath(spec)
			} else {
				if p, ok := perlScript(v, f, spec); ok {
					s.AddOpen(p)
				}
				continue
			}
		}
		if spec == "" {
			continue
		}
		bind(spec)
		if kind == perlRequire {
			continue
		}
		if len(im.Names) == 0 {
			s.AddOpen(spec)
			continue
		}
		for _, n := range im.Names {
			switch {
			case strings.HasPrefix(n, ":"):
				s.AddOpen(spec) // an export tag
			default:
				if n = strings.TrimLeft(n, "$@%&*"); n != "" {
					s.AddName(n, spec)
				}
			}
		}
	}
	if perlOnePackage(f) {
		for _, q := range perlSelf {
			s.AddQualifier(q, f.Package)
		}
	}
	return s, nil
}

// Visible implements Resolver.
func (perl) Visible(f *File, c Candidate, step Step) bool {
	switch step {
	case StepPackage, StepImport, StepOpen:
		if !PerlInOwnPackage(c) {
			return false
		}
	}
	switch step {
	case StepOpen, StepName:
		return c.Symbol.Exported || perlDeclared(c.File) == perlDeclared(f)
	}
	return true
}

// PerlInOwnPackage reports whether the definition c belongs to the package
// its file is keyed by: its qualified name is that package and its name
// (a file without a package: its bare name). A sub defined into another
// package (sub Other::f, or one after a later `package Other;`) does not;
// a package or class symbol, a namespace of its own, always does.
func PerlInOwnPackage(c Candidate) bool {
	sym := c.Symbol
	if !c.TopLevel() || strings.Contains(sym.Name, "::") ||
		sym.Kind == codeparse.KindPackage || sym.Kind == codeparse.KindClass {
		return true
	}
	return perlPackageOf(sym) == perlDeclared(c.File)
}

// perlPackageOf is the package a top-level definition's qualified name
// puts it in, "" for none (a script's subs).
func perlPackageOf(sym *codeparse.Symbol) string {
	if pkg, ok := strings.CutSuffix(sym.QualName, "::"+sym.Name); ok {
		return pkg
	}
	return ""
}

// perlDeclared is the package a file declares, "" for none.
func perlDeclared(f *File) string {
	if f.Facts == nil {
		return ""
	}
	return f.Facts.Package
}

// perlOnePackage reports whether every top-level sub of f belongs to the
// package it is keyed by, but for the subs defined into another package by
// their full name (sub Other::f), which leave the package of the code
// around them as it is.
func perlOnePackage(f *File) bool {
	for i := range f.Facts.Symbols {
		sym := &f.Facts.Symbols[i]
		if sym.Parent != "" || (sym.Kind != codeparse.KindFunction && sym.Kind != codeparse.KindMethod) || perlAbsolute(sym) {
			continue
		}
		if !PerlInOwnPackage(Candidate{File: f, Symbol: sym}) {
			return false
		}
	}
	return true
}

// perlAbsolute reports whether a sub is declared by its full name (sub
// Other::f): its signature, which starts with the declaration, names it
// with a package.
func perlAbsolute(sym *codeparse.Symbol) bool {
	fields := strings.Fields(sym.Signature)
	return len(fields) > 1 && (fields[0] == "sub" || fields[0] == "method") && strings.Contains(fields[1], "::")
}

// perlIsPath reports whether a require's spec is a file name rather than a
// module ("shapes/config.pl", "My/Module.pm").
func perlIsPath(spec string) bool {
	return strings.Contains(spec, "/") || path.Ext(spec) != ""
}

// PerlModuleOfPath is the module a .pm path below @INC names
// (My/Module.pm → My::Module); "" for a path that is not one.
func PerlModuleOfPath(p string) string {
	p = path.Clean(strings.TrimSuffix(p, ".pm"))
	if p == "." || path.IsAbs(p) || strings.HasPrefix(p, "../") || p == ".." {
		return ""
	}
	return strings.ReplaceAll(p, "/", "::")
}

// perlScript finds the package-less file a require of the path spec loads:
// relative to the requiring file, the project root or lib/. Its package
// key is its path.
func perlScript(v View, f *File, spec string) (string, bool) {
	if path.IsAbs(spec) {
		return "", false
	}
	for _, p := range []string{path.Join(f.Dir(), spec), path.Clean(spec), path.Join("lib", spec)} {
		if p == ".." || strings.HasPrefix(p, "../") {
			continue
		}
		if v.Has(p) && len(v.Package(codeparse.FamilyPerl, p)) > 0 {
			return p, true
		}
	}
	return "", false
}
