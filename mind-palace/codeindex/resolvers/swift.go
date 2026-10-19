package resolvers

import (
	"context"
	"path"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Swift family. A package is a module, a build target: a SwiftPM
// target is the directory below one of the target roots (Sources/<T>/,
// Tests/<T>/, every directory under it included) of the nearest
// Package.swift, so its key is the target's name, the name other modules
// import it by. A file outside any target root (an Xcode project) belongs
// to the module named by its top directory (MyApp/Views/A.swift → MyApp),
// a file at the root to ".". Every file of a module sees the module's
// definitions without an import.
//
// import M opens M (its public definitions are reachable unqualified) and
// binds M as a qualifier; import M.Sub (a submodule) does the same for M;
// import struct M.T imports the one name T from M. @_exported import M in
// a module re-exports M to the modules that import it. The file's own
// module name qualifies its own definitions (MyKit.Point).
//
// Extensions merge into their type: the extractor names an extension and
// qualifies its members as the type it extends (Point.description), so a
// qualified reference reaches them as the type's members; an extension
// itself is never a target, so a reference to Point links the type, not
// one of its extensions (and an extension of a type outside the project,
// such as String, never stands in for it). Protocol conformance is an
// implements reference to the protocol (defRank prefers protocols).
//
// Another module's definitions are visible when declared public or open,
// or when the file imports that module @testable. A bare name no scope
// defines (an implicit self member, a member of the type an extension
// extends, an inherited one) falls back to a definition of that name in
// the file's own module or a module it imports.
func init() { Register(codeparse.FamilySwift, swift{}) }

type swift struct{}

var swiftRules = Rules{Separators: []string{"."}, NameFallback: true, MemberFallback: true, ReceiverMembers: true}

// Swift profile options and their values (queries/swift/profile.yaml).
const (
	// SwiftOptModule is how modules are formed; SwiftModuleTarget (the
	// default) is the build-target rule, anything else falls back to the
	// profile's packageBy (Default.Package).
	SwiftOptModule    = "module"
	SwiftModuleTarget = "target"
	// SwiftOptTargetRoots lists the directories that hold one directory per
	// target, comma-separated.
	SwiftOptTargetRoots = "targetRoots"
	// SwiftManifest is the SwiftPM package manifest.
	SwiftManifest = "Package.swift"
	// SwiftImportReexport and SwiftImportTestable are the import kinds of
	// @_exported import and @testable import.
	SwiftImportReexport = "reexport"
	SwiftImportTestable = "testable"
)

// SwiftDefaultTargetRoots are the SwiftPM target roots used when the
// profile names none.
var SwiftDefaultTargetRoots = []string{"Sources", "Tests"}

// Rules implements Resolver.
func (swift) Rules() Rules { return swiftRules }

// Package implements Resolver: the file's module.
func (swift) Package(ctx context.Context, v View, f *File) (string, error) {
	roots := SwiftDefaultTargetRoots
	if f.Profile != nil {
		opts := f.Profile.Scope.Options
		if m := opts[SwiftOptModule]; m != "" && m != SwiftModuleTarget {
			return Default{Rule: &swiftRules}.Package(ctx, v, f)
		}
		if r := opts[SwiftOptTargetRoots]; strings.TrimSpace(r) != "" {
			roots = nil
			for _, s := range strings.Split(r, ",") {
				if s = strings.Trim(strings.TrimSpace(s), "/"); s != "" {
					roots = append(roots, s)
				}
			}
		}
	}
	return SwiftModule(v, f.Path, roots), nil
}

// SwiftModule is the module of the Swift file at p: the target directory
// below a target root of the nearest visible Package.swift (or, without
// one, below the first target root on the path), else the path's top
// directory, else "." for a file at the root.
func SwiftModule(v View, p string, roots []string) string {
	segs := strings.Split(path.Dir(p), "/")
	if path.Dir(p) == "." {
		segs = nil
	}
	isRoot := func(s string) bool {
		for _, r := range roots {
			if s == r {
				return true
			}
		}
		return false
	}
	// The nearest manifest: the target must sit directly below its roots.
	for i := len(segs); i >= 0; i-- {
		manifest := strings.Join(append(segs[:i:i], SwiftManifest), "/")
		if v != nil && v.Has(manifest) {
			if i+1 < len(segs) && isRoot(segs[i]) {
				return segs[i+1]
			}
			break
		}
	}
	for i := 0; i+1 < len(segs); i++ {
		if isRoot(segs[i]) {
			return segs[i+1]
		}
	}
	if len(segs) > 0 {
		return segs[0]
	}
	return "."
}

// SwiftImportModule is the module an import spec names: its first
// component (os.log → os).
func SwiftImportModule(spec string) string {
	m, _, _ := strings.Cut(spec, ".")
	return m
}

// Scope implements Resolver.
func (swift) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Package != "" && f.Package != "." {
		s.AddQualifier(f.Package, f.Package)
	}
	if f.Facts == nil {
		return s, nil
	}
	var open []string
	for _, im := range f.Facts.Imports {
		m := SwiftImportModule(im.Spec)
		if m == "" {
			continue
		}
		s.AddQualifier(m, m)
		if len(im.Names) > 0 {
			for _, n := range im.Names {
				s.AddName(n, m)
			}
			continue
		}
		s.AddOpen(m)
		open = append(open, m)
	}
	// Modules the opened ones re-export, transitively.
	seen := map[string]bool{f.Package: true}
	for len(open) > 0 {
		m := open[0]
		open = open[1:]
		if seen[m] {
			continue
		}
		seen[m] = true
		for _, g := range v.Package(codeparse.FamilySwift, m) {
			if g.Facts == nil {
				continue
			}
			for _, im := range g.Facts.Imports {
				if im.Kind != SwiftImportReexport || len(im.Names) > 0 {
					continue
				}
				if r := SwiftImportModule(im.Spec); r != "" && !seen[r] {
					s.AddQualifier(r, r)
					s.AddOpen(r)
					open = append(open, r)
				}
			}
		}
	}
	return s, nil
}

// Visible implements Resolver: an extension is never a target; the same
// module sees everything; another module only what is public or open, or
// everything when imported @testable; a name-only fallback stays within
// the file's module and the modules it imports.
func (swift) Visible(f *File, c Candidate, step Step) bool {
	if c.Symbol.Kind == codeparse.KindExtension {
		return false
	}
	if c.File.Package == f.Package {
		return true
	}
	imported, testable := swiftImports(f, c.File.Package)
	if !c.Symbol.Exported && !testable {
		return false
	}
	if step == StepName {
		return imported
	}
	return true
}

// swiftImports reports whether f imports module m, and whether @testable.
func swiftImports(f *File, m string) (imported, testable bool) {
	if f.Facts == nil {
		return false, false
	}
	for _, im := range f.Facts.Imports {
		if SwiftImportModule(im.Spec) != m {
			continue
		}
		imported = true
		if im.Kind == SwiftImportTestable {
			testable = true
		}
	}
	return imported, testable
}
