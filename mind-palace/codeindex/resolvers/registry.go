// Package resolvers holds the name-resolution rules of each language
// family (codeparse's resolution families): how a file's package, module or
// namespace is named, what its imports bring into scope and which
// definitions a reference may reach. The engine that walks the scopes and
// writes the edges is codeindex/resolve.go; a family only answers its
// questions.
//
// Each family lives in its own file (<family>.go, named after the
// codeparse.Family* constant: golang.go for go) and registers itself from an
// init function:
//
//	func init() { Register(codeparse.FamilyPython, python{}) }
//
// so adding a family never edits this file or the engine. A family that
// differs from the common rules in one respect embeds Default and overrides
// that method.
package resolvers

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// Resolver is one family's rules. The engine calls Package for every file
// of the family visible in a view, then Scope for each file whose
// references it resolves, then Visible for each candidate definition.
// Implementations are stateless (or safe for concurrent use): one value
// serves every project and view.
type Resolver interface {
	// Rules are the family's fixed settings.
	Rules() Rules
	// Package returns the key of the package, module or namespace f belongs
	// to. Files of one family with equal keys share an unqualified scope,
	// and Scope's package keys name them. It may read other files of the
	// view (v.Content: a go.mod, a pubspec.yaml) but not v.Package, which
	// is filled from its results.
	Package(ctx context.Context, v View, f *File) (string, error)
	// Scope returns what f's imports bring into scope.
	Scope(ctx context.Context, v View, f *File) (Scope, error)
	// Visible reports whether the definition c may be the target of a
	// reference from f found by step (Go: another package's definitions
	// must be exported).
	Visible(f *File, c Candidate, step Step) bool
}

// Rules are a family's fixed settings.
type Rules struct {
	// Separators join the parts of qualified names and qualifiers (".",
	// "::"); the engine splits a reference's qualifier and matches
	// definitions' QualName with them. ["."] when empty.
	Separators []string
	// CaseInsensitive names compare without case (SQL, Common Lisp).
	CaseInsensitive bool
	// NameFallback lets a bare name that no scope defines resolve to any
	// visible definition of that name in the family (confidence name).
	// Off for languages whose bare names never cross a package unimported
	// (Go).
	NameFallback bool
	// MemberFallback lets a name reached through a qualifier that is not an
	// import (a receiver, self, an expression) resolve to a member of that
	// name: in the file, then the package, then the imported packages, then
	// anywhere in the family (confidence name).
	MemberFallback bool
}

// Scope is what a file's imports bring into scope, as package keys (the
// values Resolver.Package returns).
type Scope struct {
	// Qualifiers maps a qualifier as written (an import alias, a package's
	// default name) to the packages it names. A reference qualified by one
	// of them resolves only inside those packages; when they are not in
	// the project (a standard library) it stays unresolved.
	Qualifiers map[string][]string
	// Names maps a bare name imported explicitly (from m import a; import
	// {a} from "m"; use a::b) to the packages it comes from.
	Names map[string][]string
	// Open are packages whose definitions are reachable unqualified (Go's
	// dot imports, Java's on-demand imports, C's includes).
	Open []string
}

// AddQualifier records that qualifier q names package pkg.
func (s *Scope) AddQualifier(q, pkg string) {
	if s.Qualifiers == nil {
		s.Qualifiers = map[string][]string{}
	}
	s.Qualifiers[q] = appendNew(s.Qualifiers[q], pkg)
}

// AddName records that the bare name n is imported from pkg.
func (s *Scope) AddName(n, pkg string) {
	if s.Names == nil {
		s.Names = map[string][]string{}
	}
	s.Names[n] = appendNew(s.Names[n], pkg)
}

// AddOpen records pkg as reachable unqualified.
func (s *Scope) AddOpen(pkg string) { s.Open = appendNew(s.Open, pkg) }

func appendNew(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}

// Step is the rule that found a candidate, in the order the engine tries
// them.
type Step int

// Steps.
const (
	StepScope     Step = iota // the enclosing definitions of the reference, in its file
	StepFile                  // the file's top level
	StepPackage               // the file's package, module or namespace
	StepImport                // a package an import binds to the qualifier or the name
	StepOpen                  // a package imported unqualified
	StepQualified             // a definition whose qualified name ends with the qualifier and name
	StepMember                // a member reached through a qualifier that is not an import
	StepName                  // any definition of that name (NameFallback)
)

// String names the step.
func (s Step) String() string {
	switch s {
	case StepScope:
		return "scope"
	case StepFile:
		return "file"
	case StepPackage:
		return "package"
	case StepImport:
		return "import"
	case StepOpen:
		return "open"
	case StepQualified:
		return "qualified"
	case StepMember:
		return "member"
	case StepName:
		return "name"
	}
	return "step(" + fmt.Sprint(int(s)) + ")"
}

// View is what a resolver may read of the view it resolves in: the
// visible files of every family, their content, and the files of a
// package.
type View interface {
	// Layer is the view's top layer, the one whose edges are written.
	Layer() string
	// Has reports whether a file (of any language, go.mod included) is
	// visible at path.
	Has(path string) bool
	// Content returns the visible file's content; false when no file is
	// visible at path or its content was not collected.
	Content(ctx context.Context, path string) ([]byte, bool, error)
	// Package returns the files of family whose package key is key,
	// ordered by path. It is empty while Package keys are computed.
	Package(family, key string) []*File
}

// File is one visible file with facts, as a resolver sees it.
type File struct {
	// Path is the file's path in the project.
	Path string
	// Layer is the layer the visible version comes from.
	Layer string
	// Lang and Family are the file's language and resolution family.
	Lang   string
	Family string
	// Package is the key Resolver.Package returned ("" before).
	Package string
	// Facts are the parser's facts without chunks. Read-only.
	Facts *codeparse.Facts
	// Profile is the language's profile when the engine has profiles
	// loaded, nil otherwise.
	Profile *codeparse.Profile
}

// Dir is the file's directory, "." at the root.
func (f *File) Dir() string { return path.Dir(f.Path) }

// Candidate is a visible definition a reference may resolve to.
type Candidate struct {
	File   *File
	Symbol *codeparse.Symbol
}

// TopLevel reports whether the definition has no enclosing definition.
func (c Candidate) TopLevel() bool { return c.Symbol.Parent == "" }

var (
	mu       sync.RWMutex
	registry = map[string]Resolver{}
)

// Register makes r the resolver of family. It panics when family is not a
// codeparse resolution family or already has a resolver: both are
// programming errors found by the first test run.
func Register(family string, r Resolver) {
	if r == nil {
		panic("resolvers: Register " + family + " with a nil resolver")
	}
	known := false
	for _, f := range codeparse.Families() {
		if f == family {
			known = true
		}
	}
	if !known {
		panic("resolvers: Register: unknown family " + family)
	}
	mu.Lock()
	defer mu.Unlock()
	if _, dup := registry[family]; dup {
		panic("resolvers: Register: family " + family + " registered twice")
	}
	registry[family] = r
}

// Lookup returns the resolver of family.
func Lookup(family string) (Resolver, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := registry[family]
	return r, ok
}

// ForLang returns the resolution family of language lang and its resolver.
func ForLang(lang string) (string, Resolver, bool) {
	l, ok := codeparse.LookupLanguage(lang)
	if !ok {
		return "", nil, false
	}
	r, ok := Lookup(l.Family)
	return l.Family, r, ok
}

// Registered returns the families that have a resolver, sorted.
func Registered() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for f := range registry {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// Default is the common rules, for families to embed:
//   - Package: the declared package (Facts.Package) when the profile says
//     packageBy: declaration or the facts carry one, the directory for
//     packageBy: directory (or no package at all), the path without its
//     extension for packageBy: file, "" (one global scope) for none;
//   - Scope: an import with an alias binds it as a qualifier ("." and "*"
//     open the package, "_" binds nothing), explicit names are bound as
//     bare names, a whole-module import without an alias binds the last
//     segment of its spec; the spec itself is the package key;
//   - Visible: a definition of the same package always, another package's
//     only when exported.
//
// Its zero value has the Rules ["."], case-sensitive, with name and member
// fallbacks; set Rule to change them.
type Default struct {
	Rule *Rules
}

var _ Resolver = Default{}

// Rules implements Resolver.
func (d Default) Rules() Rules {
	if d.Rule != nil {
		return *d.Rule
	}
	return Rules{Separators: []string{"."}, NameFallback: true, MemberFallback: true}
}

// Package implements Resolver.
func (d Default) Package(_ context.Context, _ View, f *File) (string, error) {
	by := ""
	if f.Profile != nil {
		by = f.Profile.Scope.PackageBy
	}
	switch by {
	case codeparse.PackageByNone:
		return "", nil
	case codeparse.PackageByFile:
		return strings.TrimSuffix(f.Path, path.Ext(f.Path)), nil
	case codeparse.PackageByDirectory:
		return f.Dir(), nil
	}
	if f.Facts != nil && f.Facts.Package != "" {
		return f.Facts.Package, nil
	}
	return f.Dir(), nil
}

// Scope implements Resolver.
func (d Default) Scope(_ context.Context, _ View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	seps := d.Rules().Separators
	for _, im := range f.Facts.Imports {
		switch {
		case im.Alias == "_":
		case im.Alias == "." || im.Alias == "*":
			s.AddOpen(im.Spec)
		case len(im.Names) > 0:
			for _, n := range im.Names {
				s.AddName(n, im.Spec)
			}
			if im.Alias != "" {
				s.AddQualifier(im.Alias, im.Spec)
			}
		case im.Alias != "":
			s.AddQualifier(im.Alias, im.Spec)
		default:
			s.AddQualifier(LastSegment(im.Spec, seps), im.Spec)
		}
	}
	return s, nil
}

// Visible implements Resolver.
func (d Default) Visible(f *File, c Candidate, _ Step) bool {
	return c.File.Package == f.Package || c.Symbol.Exported
}

// LastSegment returns the last segment of an import spec: after the last
// "/" and the last of seps, with a file extension dropped ("./util.js" →
// util, "a.b.c" → c, "fmt" → fmt).
func LastSegment(spec string, seps []string) string {
	s := strings.TrimRight(spec, "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
		if ext := path.Ext(s); ext != "" && ext != s {
			s = strings.TrimSuffix(s, ext)
		}
	}
	for _, sep := range seps {
		if i := strings.LastIndex(s, sep); i >= 0 && i+len(sep) < len(s) {
			s = s[i+len(sep):]
		}
	}
	return s
}
