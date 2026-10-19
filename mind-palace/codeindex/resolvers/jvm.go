package resolvers

import (
	"context"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The JVM family: Java and Kotlin, which share packages (a Kotlin file and
// a Java file declaring the same package are one package). A file's package
// is its package declaration (Default). Imports name types and members, not
// packages, so a single import binds its local name (the alias Kotlin's
// "as" gives, else the last segment) as a qualifier of the package that
// holds it: the longest prefix of the spec that is a package of the view,
// else the spec without its last segment. Without an alias, and when that
// package is the spec's parent, the name is bound as a bare name too; a
// nested type's import (a.b.Outer.Inner) binds the qualifier only, since
// the engine's import step reaches top-level definitions. An alias that
// renames (Kotlin's import a.b.Box as Crate) binds Crate as Box renamed
// (Scope.Renamed): Crate() is Box, Crate.f is Box.f. On-demand imports (Java's
// import a.b.*, Kotlin's wildcard a.b.*) open the package, as do the
// implicit ones (the profile's implicitImports or defaultImports option,
// else JVMImplicitImports). Static imports bind no package: they name
// members, which the name fallback reaches; module-info directives bind
// nothing.
//
// Visible adds Java's and Kotlin's rules to the common one (the same
// package sees everything, another package only what is exported):
//   - Kotlin's private (an unexported definition) is visible in its own
//     file only;
//   - an explicit import shadows the package's, the on-demand imports' and
//     every other definition of its local name: those steps keep only the
//     definition the import names;
//   - the import step keeps only a definition an explicit import names (a
//     qualifier bound to an imported class never reaches a top-level
//     definition of that class's package);
//   - a Kotlin extension declared outside its receiver's file (a top-level
//     definition whose qualified name holds its receiver: pkg.String.shout)
//     is reachable from another package only when imported by name or on
//     demand; it is a member of its receiver for the engine
//     (ReceiverMembers: "a".shout() finds it).
func init() { Register(codeparse.FamilyJVM, jvm{}) }

type jvm struct{ Default }

const jvmKotlin = "kotlin"

var jvmRules = Rules{Separators: []string{"."}, NameFallback: true, MemberFallback: true, ReceiverMembers: true}

// Rules implements Resolver.
func (jvm) Rules() Rules { return jvmRules }

// Options of the JVM family read from a profile's scope.options, and the
// import kinds' default words.
const (
	JVMOptionImplicitImports      = "implicitImports" // Java's profile
	JVMOptionDefaultImports       = "defaultImports"  // Kotlin's profile
	JVMOptionOnDemandImport       = "onDemandImport"
	JVMOptionStaticImport         = "staticImport"
	JVMOptionStaticOnDemandImport = "staticOnDemandImport"

	jvmOnDemandKind       = "on_demand"
	jvmWildcardKind       = "wildcard" // Kotlin's import a.b.*
	jvmStaticKind         = "static"
	jvmStaticOnDemandKind = "static_on_demand"
)

// JVMImplicitImports are the packages a file of the language imports on
// demand without writing it, used when the profile names none.
var JVMImplicitImports = map[string][]string{
	"java": {"java.lang"},
	"kotlin": {"kotlin", "kotlin.annotation", "kotlin.collections", "kotlin.comparisons", "kotlin.io",
		"kotlin.ranges", "kotlin.sequences", "kotlin.text", "kotlin.jvm", "java.lang"},
}

// jvmModuleKinds are module-info directives, which import no names.
var jvmModuleKinds = map[string]bool{"requires": true, "exports": true, "opens": true, "uses": true, "provides": true}

// jvmImportForm is what one import form does.
type jvmImportForm int

const (
	jvmSkip           jvmImportForm = iota
	jvmSingle                       // a type, or a Kotlin top-level definition
	jvmStatic                       // a static member
	jvmOnDemand                     // every type of a package
	jvmStaticOnDemand               // every static member of a type
)

// jvmImport is one import as the family reads it.
type jvmImport struct {
	form  jvmImportForm
	spec  string // without a trailing ".*"
	local string // the name it binds (single and static forms)
}

func jvmOption(f *File, key, def string) string {
	if f.Profile != nil {
		if v, ok := f.Profile.Scope.Options[key]; ok && v != "" {
			return v
		}
	}
	return def
}

// jvmImports classifies f's imports.
func jvmImports(f *File) []jvmImport {
	if f.Facts == nil || len(f.Facts.Imports) == 0 {
		return nil
	}
	onDemand := jvmOption(f, JVMOptionOnDemandImport, jvmOnDemandKind)
	static := jvmOption(f, JVMOptionStaticImport, jvmStaticKind)
	staticOnDemand := jvmOption(f, JVMOptionStaticOnDemandImport, jvmStaticOnDemandKind)
	out := make([]jvmImport, 0, len(f.Facts.Imports))
	for _, im := range f.Facts.Imports {
		spec, star := strings.CutSuffix(strings.TrimSpace(im.Spec), ".*")
		if spec == "" || jvmModuleKinds[im.Kind] || im.Alias == "_" {
			continue
		}
		star = star || im.Alias == "*"
		var form jvmImportForm
		switch {
		case im.Kind == staticOnDemand, im.Kind == static && star:
			form = jvmStaticOnDemand
		case im.Kind == onDemand, im.Kind == jvmWildcardKind, star:
			form = jvmOnDemand
		case im.Kind == static:
			form = jvmStatic
		default:
			form = jvmSingle
		}
		x := jvmImport{form: form, spec: spec}
		if form == jvmSingle || form == jvmStatic {
			switch {
			case im.Alias != "":
				x.local = im.Alias
			case len(im.Names) == 1:
				x.local = im.Names[0]
			default:
				x.local = LastSegment(spec, jvmRules.Separators)
			}
		}
		out = append(out, x)
	}
	return out
}

// jvmImplicit returns the packages f imports implicitly: the profile's
// implicitImports or defaultImports (a list separated by commas or spaces,
// a trailing ".*" dropped), else JVMImplicitImports.
func jvmImplicit(f *File) []string {
	v := jvmOption(f, JVMOptionImplicitImports, "")
	if v == "" {
		v = jvmOption(f, JVMOptionDefaultImports, "")
	}
	if v == "" {
		return JVMImplicitImports[f.Lang]
	}
	var out []string
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if p = strings.TrimSuffix(p, ".*"); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Scope implements Resolver.
func (jvm) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	for _, p := range jvmImplicit(f) {
		s.AddOpen(p)
	}
	for _, im := range jvmImports(f) {
		switch im.form {
		case jvmOnDemand:
			s.AddOpen(im.spec)
		case jvmSingle:
			parent, _, ok := cutLast(im.spec)
			if !ok {
				continue
			}
			owner := jvmOwner(v, im.spec)
			if owner == "" {
				owner = parent
			}
			if im.local != LastSegment(im.spec, jvmRules.Separators) {
				// import x.Y as Z: Z is Y renamed (a nested type's alias,
				// a.b.Outer.Inner as Z, names Outer.Inner in its package).
				s.AddRenamed(im.local, owner, strings.TrimPrefix(im.spec, owner+"."))
				continue
			}
			if owner == parent {
				s.AddName(im.local, owner)
			}
			s.AddQualifier(im.local, owner)
		}
	}
	return s, nil
}

// jvmOwner is the longest proper prefix of spec that is a package of the
// view, "" when none is.
func jvmOwner(v View, spec string) string {
	for p, _, ok := cutLast(spec); ok; p, _, ok = cutLast(p) {
		if len(v.Package(codeparse.FamilyJVM, p)) > 0 {
			return p
		}
	}
	return ""
}

// cutLast splits a dotted name at its last dot.
func cutLast(s string) (string, string, bool) {
	i := strings.LastIndex(s, ".")
	if i <= 0 || i == len(s)-1 {
		return "", "", false
	}
	return s[:i], s[i+1:], true
}

// Visible implements Resolver.
func (jvm) Visible(f *File, c Candidate, step Step) bool {
	if !c.Symbol.Exported {
		// Kotlin's private is the file's (a member's, its class's, which
		// the file holds); Java's unexported definitions are the package's.
		if c.File.Lang == jvmKotlin && c.File.Path != f.Path || c.File.Package != f.Package {
			return false
		}
	}
	imports := jvmImports(f)
	switch step {
	case StepImport:
		return jvmNames(imports, c)
	case StepPackage, StepOpen, StepName:
		if jvmShadowed(imports, c) {
			return false
		}
	}
	if c.File.Package != f.Package && jvmExtension(c) {
		return jvmNames(imports, c) || jvmOpens(f, imports, c.File.Package)
	}
	return true
}

// jvmNames reports whether a single or static import names the
// definition c, by its own name or renamed (import x.Y as Z).
func jvmNames(imports []jvmImport, c Candidate) bool {
	for _, im := range imports {
		if (im.form == jvmSingle || im.form == jvmStatic) && jvmMatches(im.spec, c) {
			return true
		}
	}
	return false
}

// jvmShadowed reports whether a single or static import binds c's name to
// another definition.
func jvmShadowed(imports []jvmImport, c Candidate) bool {
	shadowed := false
	for _, im := range imports {
		if (im.form != jvmSingle && im.form != jvmStatic) || im.local != c.Symbol.Name {
			continue
		}
		if im.spec == c.Symbol.QualName || jvmMatches(im.spec, c) && im.local == LastSegment(im.spec, jvmRules.Separators) {
			return false
		}
		shadowed = true
	}
	return shadowed
}

// jvmMatches reports whether an import spec names c: its qualified name,
// or its package and name (a Kotlin extension, whose qualified name holds
// its receiver).
func jvmMatches(spec string, c Candidate) bool {
	return spec == c.Symbol.QualName || spec == jvmPrefix(c.File)+c.Symbol.Name
}

// jvmOpens reports whether f imports pkg on demand, implicitly or not.
func jvmOpens(f *File, imports []jvmImport, pkg string) bool {
	for _, im := range imports {
		if im.form == jvmOnDemand && im.spec == pkg {
			return true
		}
	}
	for _, p := range jvmImplicit(f) {
		if p == pkg {
			return true
		}
	}
	return false
}

// jvmExtension reports whether c is a Kotlin extension: a top-level
// definition whose qualified name is not its package and name.
func jvmExtension(c Candidate) bool {
	return c.TopLevel() && c.Symbol.QualName != jvmPrefix(c.File)+c.Symbol.Name
}

// jvmPrefix is the prefix the extractor gives a file's qualified names.
func jvmPrefix(f *File) string {
	if f.Facts == nil || f.Facts.Package == "" {
		return ""
	}
	return f.Facts.Package + "."
}
