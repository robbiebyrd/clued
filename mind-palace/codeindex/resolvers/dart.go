package resolvers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"path"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Dart family. A package key is a library: the path of the library's
// file, shared by its parts (a file that says part of 'lib.dart' takes the
// key of the library it names), so a library and its parts see each
// other's top level. Import, export and part URIs resolve to paths: a
// relative URI against the directory of the file that writes it, a
// package:name/rest URI to rest under the lib/ directory of the package
// called name (the pubspec.yaml above the file, a path dependency, a
// workspace member or a package directory at the root named after it),
// dart: and unknown packages to the URI itself, which the project never
// holds.
//
// An import with a prefix (as p) binds p to the library and to every
// library it exports, transitively; one without a prefix opens them, and
// a show or hide combinator, on the import or on an export on the way,
// narrows that to the names it lets through. A library's exports bring
// nothing into its own scope. Names starting with an underscore are
// private to their library.
//
// The facts are read this way: an Import's Kind is "" or import, export,
// part, or part of (spelled with a space, an underscore or a hyphen); its
// Names are the names of its combinators. Whether those are shown or
// hidden, and whether a part directive is part of, is read from the
// directive's source text at Import.Start; a Kind naming hide decides it
// when the text cannot be read, and the names are shown otherwise.
func init() { Register(codeparse.FamilyDart, dart{}) }

type dart struct{}

var dartRules = Rules{Separators: []string{"."}, NameFallback: true, MemberFallback: true}

// Rules implements Resolver.
func (dart) Rules() Rules { return dartRules }

// PubspecFile is the package file the Dart family reads.
const PubspecFile = "pubspec.yaml"

// Dart directive kinds, as dartKind normalizes them.
const (
	dartImport = "import"
	dartExport = "export"
	dartPart   = "part"
	dartPartOf = "partof"
)

// dartDirective is one import, export, part or part-of directive of a
// file, with its combinators.
type dartDirective struct {
	kind   string
	spec   string
	prefix string
	filter dartFilter
}

// dartCond is one show or hide combinator.
type dartCond struct {
	show  bool
	names map[string]bool
}

// dartFilter is the combinators a name passes through, all of which must
// let it through; nil lets everything through.
type dartFilter []dartCond

func (fl dartFilter) allows(name string) bool {
	for _, c := range fl {
		if c.names[name] != c.show {
			return false
		}
	}
	return true
}

// then is fl followed by inner (an export's combinators inside an
// import's).
func (fl dartFilter) then(inner dartFilter) dartFilter {
	if len(fl) == 0 {
		return inner
	}
	if len(inner) == 0 {
		return fl
	}
	out := make(dartFilter, 0, len(fl)+len(inner))
	return append(append(out, fl...), inner...)
}

// Package implements Resolver: the file's own path, or, for a part, the
// path of the library it is part of. A part that names its library by its
// library name rather than its URI keeps its own path (Visible still lets
// it and its library see each other's private names).
func (dart) Package(ctx context.Context, v View, f *File) (string, error) {
	dirs, err := dartDirectives(ctx, v, f)
	if err != nil {
		return "", err
	}
	for _, d := range dirs {
		if d.kind == dartPartOf && dartIsURI(d.spec) {
			return DartURIPath(ctx, v, f.Dir(), d.spec)
		}
	}
	return f.Path, nil
}

// Scope implements Resolver: the imports of f's library (the library file
// and its parts), each with the libraries it exports, and the parts the
// library names that are keyed on their own.
func (dart) Scope(ctx context.Context, v View, f *File) (Scope, error) {
	var s Scope
	files := v.Package(codeparse.FamilyDart, f.Package)
	if len(files) == 0 {
		files = []*File{f}
	}
	for _, lf := range files {
		dirs, err := dartDirectives(ctx, v, lf)
		if err != nil {
			return s, err
		}
		for _, d := range dirs {
			switch d.kind {
			case dartPart:
				key, err := DartURIPath(ctx, v, lf.Dir(), d.spec)
				if err != nil {
					return s, err
				}
				if key != f.Package && len(v.Package(codeparse.FamilyDart, key)) > 0 {
					s.AddOpen(key)
				}
			case dartImport:
				key, err := DartURIPath(ctx, v, lf.Dir(), d.spec)
				if err != nil {
					return s, err
				}
				libs, err := dartNamespace(ctx, v, key, d.filter)
				if err != nil {
					return s, err
				}
				for _, l := range libs {
					if d.prefix != "" {
						s.AddQualifier(d.prefix, l.key)
						continue
					}
					dartBind(&s, v, l)
				}
			}
		}
	}
	return s, nil
}

// dartLib is a library an import reaches and the combinators on the way.
type dartLib struct {
	key    string
	filter dartFilter
}

// dartNamespace is the library key with every library it exports,
// transitively, and the parts of each that are keyed on their own, each
// with the combinators a name passes to reach the importer.
func dartNamespace(ctx context.Context, v View, key string, filter dartFilter) ([]dartLib, error) {
	out := []dartLib{{key, filter}}
	at := map[string]int{key: 0}
	for i := 0; i < len(out); i++ {
		cur := out[i]
		for _, lf := range v.Package(codeparse.FamilyDart, cur.key) {
			dirs, err := dartDirectives(ctx, v, lf)
			if err != nil {
				return nil, err
			}
			for _, d := range dirs {
				if d.kind != dartExport && d.kind != dartPart {
					continue
				}
				next, err := DartURIPath(ctx, v, lf.Dir(), d.spec)
				if err != nil {
					return nil, err
				}
				fl := cur.filter
				if d.kind == dartExport {
					fl = fl.then(d.filter)
				} else if len(v.Package(codeparse.FamilyDart, next)) == 0 {
					continue // a part keyed with its library
				}
				if j, seen := at[next]; seen {
					if len(fl) == 0 && len(out[j].filter) > 0 {
						out[j].filter = nil // reached unfiltered too
					}
					continue
				}
				at[next] = len(out)
				out = append(out, dartLib{next, fl})
			}
		}
	}
	return out, nil
}

// dartBind brings library l into scope unprefixed: its public top-level
// names that the combinators let through, as imported names, so a name
// two imports bring in is ambiguous (Dart's rule) rather than won by the
// first. A library the project does not hold is opened, or, through show,
// binds the names it shows.
func dartBind(s *Scope, v View, l dartLib) {
	files := v.Package(codeparse.FamilyDart, l.key)
	if len(files) == 0 && len(l.filter) == 0 {
		s.AddOpen(l.key)
		return
	}
	if len(files) == 0 {
		// Not in the project: bind what a show names, for nothing to find.
		for _, c := range l.filter {
			if !c.show {
				continue
			}
			for n := range c.names {
				if l.filter.allows(n) {
					s.AddName(n, l.key)
				}
			}
		}
		return
	}
	for _, lf := range files {
		if lf.Facts == nil {
			continue
		}
		for _, sym := range lf.Facts.Symbols {
			if sym.Parent == "" && !strings.HasPrefix(sym.Name, "_") && l.filter.allows(sym.Name) {
				s.AddName(sym.Name, l.key)
			}
		}
	}
}

// Visible implements Resolver: a public name always; a private one
// (leading underscore) from the same library, which takes in a part keyed
// on its own that names the library by its library name.
func (dart) Visible(f *File, c Candidate, _ Step) bool {
	if !strings.HasPrefix(c.Symbol.Name, "_") || c.File.Package == f.Package {
		return true
	}
	lib, part := dartLibraryName(f), dartPartOfName(f)
	clib, cpart := dartLibraryName(c.File), dartPartOfName(c.File)
	return (part != "" && (part == clib || part == cpart)) || (cpart != "" && cpart == lib)
}

// dartLibraryName is the library name a file declares (library a.b;), as
// the extractor records it in Facts.Package.
func dartLibraryName(f *File) string {
	if f.Facts == nil {
		return ""
	}
	return f.Facts.Package
}

// dartPartOfName is the library name of a part of a.b; directive, ""
// when f has none or names its library by URI. It reads the facts only
// (a Kind of part of), not the source.
func dartPartOfName(f *File) string {
	if f.Facts == nil {
		return ""
	}
	for _, im := range f.Facts.Imports {
		if dartKind(im.Kind) == dartPartOf && !dartIsURI(im.Spec) {
			return im.Spec
		}
	}
	return ""
}

// dartIsURI reports whether a part-of spec is a URI rather than a library
// name (a.b.c).
func dartIsURI(spec string) bool {
	return strings.HasSuffix(spec, ".dart") || strings.ContainsAny(spec, "/:")
}

// dartKind normalizes an Import.Kind: import, export, part or partof,
// with a trailing hide or show dropped.
func dartKind(k string) string {
	k = strings.ToLower(k)
	k = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(k)
	k = strings.TrimSuffix(strings.TrimSuffix(k, "hide"), "show")
	if k == "" {
		return dartImport
	}
	return k
}

// dartDirectives returns f's directives: kind, spec, prefix and
// combinators, the latter (and part versus part of) from the source when
// the facts leave them open.
func dartDirectives(ctx context.Context, v View, f *File) ([]dartDirective, error) {
	if f.Facts == nil {
		return nil, nil
	}
	var src []byte
	read := false
	source := func() ([]byte, error) {
		if !read {
			read = true
			c, ok, err := v.Content(ctx, f.Path)
			if err != nil {
				return nil, err
			}
			if ok {
				src = c
			}
		}
		return src, nil
	}
	out := make([]dartDirective, 0, len(f.Facts.Imports))
	for _, im := range f.Facts.Imports {
		raw := strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.ToLower(im.Kind))
		d := dartDirective{kind: dartKind(im.Kind), spec: im.Spec, prefix: im.Alias}
		switch d.kind {
		case dartImport, dartExport, dartPart, dartPartOf:
		default:
			continue
		}
		if d.kind == dartPart || len(im.Names) > 0 {
			text, err := source()
			if err != nil {
				return nil, err
			}
			word, conds, ok := dartParseDirective(text, im.Start.Offset)
			switch {
			case ok && d.kind == dartPart && word == dartPartOf:
				d.kind = dartPartOf
			case ok && (word == dartImport || word == dartExport):
				d.filter = conds
			case len(im.Names) > 0:
				d.filter = dartFilter{{show: !strings.HasSuffix(raw, "hide"), names: dartSet(im.Names)}}
			}
		}
		if d.kind == dartPartOf || d.kind == dartPart {
			d.prefix = ""
		}
		out = append(out, d)
	}
	return out, nil
}

func dartSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}

// dartParseDirective reads the directive that starts at offset in src: its
// word (import, export, part, partof) and its show and hide combinators.
// ok is false when no directive starts there.
func dartParseDirective(src []byte, offset int) (string, dartFilter, bool) {
	if offset < 0 || offset >= len(src) {
		return "", nil, false
	}
	// The directive's text up to its semicolon, string literals blanked.
	var b strings.Builder
	var quote byte
	for i := offset; i < len(src); i++ {
		c := src[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
				b.WriteByte(' ')
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			b.WriteByte(' ')
			continue
		}
		if c == ';' {
			break
		}
		if c == '/' && i+1 < len(src) && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	words := strings.FieldsFunc(b.String(), func(r rune) bool {
		return !(r == '_' || r == '$' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
	if len(words) == 0 {
		return "", nil, false
	}
	switch words[0] {
	case dartPart:
		if len(words) > 1 && words[1] == "of" {
			return dartPartOf, nil, true
		}
		return dartPart, nil, true
	case dartImport, dartExport:
	default:
		return "", nil, false
	}
	var fl dartFilter
	var cur *dartCond
	for _, w := range words[1:] {
		switch w {
		case "show", "hide":
			fl = append(fl, dartCond{show: w == "show", names: map[string]bool{}})
			cur = &fl[len(fl)-1]
		case "as", "deferred", "if":
			cur = nil
		default:
			if cur != nil {
				cur.names[w] = true
			}
		}
	}
	return words[0], fl, true
}

// DartURIPath resolves an import, export or part URI written in a file of
// directory dir to the path of the file it names: a relative URI against
// dir, package:name/rest to rest under the lib/ directory of the package
// called name (see DartPackageRoot). A dart: URI, a package the project
// does not hold or a path leaving the project is returned as written.
func DartURIPath(ctx context.Context, v View, dir, uri string) (string, error) {
	switch {
	case strings.HasPrefix(uri, "package:"):
		rest := strings.TrimPrefix(uri, "package:")
		name, file, ok := strings.Cut(rest, "/")
		if !ok || name == "" || file == "" {
			return uri, nil
		}
		root, found, err := DartPackageRoot(ctx, v, dir, name)
		if err != nil || !found {
			return uri, err
		}
		return dartLocal(path.Join(root, "lib", file), uri), nil
	case strings.Contains(uri, ":"):
		return uri, nil // dart:, http:, file:
	case strings.HasPrefix(uri, "/"):
		return uri, nil
	}
	return dartLocal(path.Join(dir, uri), uri), nil
}

// dartLocal is p when it stays inside the project, otherwise fallback.
func dartLocal(p, fallback string) string {
	p = path.Clean(p)
	if p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/") {
		return fallback
	}
	return p
}

// DartPackageRoot finds the directory of the package called name, as seen
// from directory dir: walking up from dir, a pubspec.yaml whose name it
// is, a path dependency (or override) of that name, or a workspace member
// of that name; then a package directory at the root named after it
// (<name>/, packages/<name>/, pkgs/<name>/).
func DartPackageRoot(ctx context.Context, v View, dir, name string) (string, bool, error) {
	check := func(d string) (pubspec, bool, error) {
		p := path.Join(d, PubspecFile)
		if !v.Has(p) {
			return pubspec{}, false, nil
		}
		content, ok, err := v.Content(ctx, p)
		if err != nil || !ok {
			return pubspec{}, false, err
		}
		return parsePubspec(content), true, nil
	}
	for d := path.Clean(dir); ; d = path.Dir(d) {
		ps, ok, err := check(d)
		if err != nil {
			return "", false, err
		}
		if ok {
			if ps.Name == name {
				return d, true, nil
			}
			if p, dep := ps.paths[name]; dep {
				if root := dartLocal(path.Join(d, p), ""); root != "" {
					return root, true, nil
				}
			}
			for _, m := range ps.Workspace {
				root := dartLocal(path.Join(d, m), "")
				if root == "" {
					continue
				}
				mps, ok, err := check(root)
				if err != nil {
					return "", false, err
				}
				if ok && mps.Name == name {
					return root, true, nil
				}
			}
		}
		if d == "." || d == "/" {
			break
		}
	}
	if strings.ContainsAny(name, "/\\") || name == "." || name == ".." {
		return "", false, nil
	}
	for _, d := range []string{name, "packages/" + name, "pkgs/" + name} {
		ps, ok, err := check(d)
		if err != nil {
			return "", false, err
		}
		if ok && ps.Name == name {
			return d, true, nil
		}
	}
	return "", false, nil
}

// pubspec is what the Dart family reads of a pubspec.yaml.
type pubspec struct {
	Name         string               `yaml:"name"`
	Workspace    []string             `yaml:"workspace"`
	Dependencies map[string]yaml.Node `yaml:"dependencies"`
	DevDeps      map[string]yaml.Node `yaml:"dev_dependencies"`
	Overrides    map[string]yaml.Node `yaml:"dependency_overrides"`
	paths        map[string]string    // package name → path dependency's directory
}

var pubspecCache = struct {
	sync.Mutex
	m map[[sha256.Size]byte]pubspec
}{m: map[[sha256.Size]byte]pubspec{}}

// pubspecCacheSize bounds the parsed pubspec files kept between calls.
const pubspecCacheSize = 1024

// parsePubspec reads a pubspec.yaml's name, workspace members and path
// dependencies; a file that does not parse reads as empty.
func parsePubspec(content []byte) pubspec {
	key := sha256.Sum256(content)
	pubspecCache.Lock()
	ps, ok := pubspecCache.m[key]
	pubspecCache.Unlock()
	if ok {
		return ps
	}
	ps = pubspec{}
	if err := yaml.NewDecoder(bytes.NewReader(content)).Decode(&ps); err != nil {
		ps = pubspec{}
	}
	ps.paths = map[string]string{}
	for _, deps := range []map[string]yaml.Node{ps.DevDeps, ps.Dependencies, ps.Overrides} {
		for n, node := range deps {
			var dep struct {
				Path string `yaml:"path"`
			}
			if node.Kind == yaml.MappingNode && node.Decode(&dep) == nil && dep.Path != "" {
				ps.paths[n] = dep.Path
			}
		}
	}
	ps.Dependencies, ps.DevDeps, ps.Overrides = nil, nil, nil
	pubspecCache.Lock()
	if len(pubspecCache.m) >= pubspecCacheSize {
		clear(pubspecCache.m)
	}
	pubspecCache.m[key] = ps
	pubspecCache.Unlock()
	return ps
}
