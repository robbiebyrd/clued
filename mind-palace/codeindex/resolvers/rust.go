package resolvers

import (
	"bufio"
	"bytes"
	"context"
	"path"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Rust family. A file is a module of its crate's mod tree, and its
// package key is the module's path from the crate: the crate's name, then
// "::" and the modules below the root (mycrate, mycrate::net::http). The
// path follows the file system the way `mod name;` does: a crate root
// (src/lib.rs, src/main.rs, a [lib] or [[bin]] path of Cargo.toml) holds
// the crate's top level, name.rs or name/mod.rs next to a root or a mod.rs
// is its child name, and the children of name.rs live in the directory
// name/. The crate's name is Cargo.toml's [lib] name, else its [package]
// name with '-' as '_'; a binary under src/bin/, and a target under tests/,
// examples/ or benches/, is a crate of its own keyed <name>/bin/<target>
// (tests, examples, benches alike). src/main.rs shares the library's key,
// so a binary's `mod x;` and the library's agree on src/x.rs. Without a
// Cargo.toml the directory above a src/ directory names the crate ("crate"
// at the project root).
//
// A `use` path starts at the crate (crate::), the current module (self::,
// or a module the file declares), its parent (super::), or a crate by name
// (::name or name). A path naming a module of the project binds its last
// segment (or the alias) as a qualifier of that module; a path naming an
// item of the project binds the item's name to the module that holds it,
// so a bare reference is exact and Type::method is found by its qualified
// name; a path outside the project binds both, so its references stay
// unresolved. A glob opens a module; `mod name;` binds name, `extern crate
// a as b;` binds b, and crate:: and super:: references are bound too.
// `impl Trait for Type` is a definition named Type whose implements
// reference names Trait, resolved like any other reference; the impl block
// itself is never a target, so a reference to Type reaches the struct or
// enum, and Type::method the impl's method.
//
// Every module of a crate sees every definition of that crate (pub(crate)
// is the common case); another crate sees what is pub. Bare names that no
// rule binds fall back to any definition of that name (confidence name),
// which covers re-exports, globs of enums and items of inline modules.
//
// Not followed: #[path] attributes, re-exports through `pub use` (they
// resolve by name), aliases of items (use a::f as g; g() is unresolved:
// the engine looks definitions up by the referenced name), and paths of
// three or more segments through file modules (crate::a::f(): a qualifier
// binds its first segment only, and the rest must be part of a
// definition's qualified name, which in Rust never holds the file's
// module path).
func init() { Register(codeparse.FamilyRust, rust{}) }

type rust struct{}

var rustRules = Rules{Separators: []string{"::"}, NameFallback: true, MemberFallback: true}

// Rules implements Resolver.
func (rust) Rules() Rules { return rustRules }

// Defaults of the Rust profile's scope options.
const (
	RustManifest = "Cargo.toml"
	RustModFile  = "mod.rs"
)

// rustCrateRoots are the default crate roots below a package directory.
var rustCrateRoots = []string{"src/lib.rs", "src/main.rs"}

// rustTargetDirs hold one crate per file (x.rs) or directory (x/main.rs).
var rustTargetDirs = []string{"src/bin", "tests", "examples", "benches"}

// rustOptions are the profile's options with their defaults.
type rustOptions struct {
	manifest string
	roots    []string
	modFile  string
}

func rustOpts(f *File) rustOptions {
	o := rustOptions{manifest: RustManifest, roots: rustCrateRoots, modFile: RustModFile}
	if f.Profile == nil {
		return o
	}
	opts := f.Profile.Scope.Options
	if m := strings.TrimSpace(opts["manifest"]); m != "" {
		o.manifest = m
	}
	if r := strings.Fields(opts["crateRoots"]); len(r) > 0 {
		o.roots = r
	}
	if m := strings.TrimSpace(opts["modFile"]); m != "" {
		o.modFile = m
	}
	return o
}

// Package implements Resolver: the module path of the file.
func (rust) Package(ctx context.Context, v View, f *File) (string, error) {
	o := rustOpts(f)
	for d := f.Dir(); ; d = path.Dir(d) {
		mf := joinPath(d, o.manifest)
		if v.Has(mf) {
			content, ok, err := v.Content(ctx, mf)
			if err != nil {
				return "", err
			}
			if ok {
				if m, isPkg := ParseCargoManifest(content); isPkg {
					return rustPackageKey(m, d, f.Path, o), nil
				}
			}
		}
		if d == "." || d == "/" {
			break
		}
	}
	// No manifest: the directory above src/ names the crate.
	elems := strings.Split(f.Path, "/")
	for i, e := range elems[:len(elems)-1] {
		if e != "src" {
			continue
		}
		dir := strings.Join(elems[:i], "/")
		name := "crate"
		if dir != "" {
			name = RustCrateName(path.Base(dir))
		} else {
			dir = "."
		}
		return rustPackageKey(CargoManifest{Name: name}, dir, f.Path, o), nil
	}
	return rustTreeKey("crate", ".", f.Path, o), nil
}

// CargoManifest is what the Rust family reads of a Cargo.toml.
type CargoManifest struct {
	// Name is the library crate's name ([lib] name, else [package] name),
	// '-' replaced by '_'.
	Name string
	// Lib is the [lib] path ("" for src/lib.rs).
	Lib string
	// Bins are the [[bin]] targets with a path, name → path.
	Bins map[string]string
}

// ParseCargoManifest reads the crate name and target paths of a
// Cargo.toml; false when it declares no [package] (a virtual workspace).
func ParseCargoManifest(content []byte) (CargoManifest, bool) {
	var m CargoManifest
	var pkgName, libName, binName, binPath string
	section, isPkg := "", false
	flushBin := func() {
		if binName != "" && binPath != "" {
			if m.Bins == nil {
				m.Bins = map[string]string{}
			}
			m.Bins[binName] = path.Clean(binPath)
		}
		binName, binPath = "", ""
	}
	sc := bufio.NewScanner(bytes.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if section == "[[bin]]" {
				flushBin()
			}
			if end := strings.LastIndexByte(line, ']'); end > 0 {
				line = line[:end+1]
			}
			if strings.HasPrefix(line, "[[") {
				section = "[[" + strings.TrimSpace(strings.Trim(line, "[]")) + "]]"
			} else {
				section = "[" + strings.TrimSpace(strings.Trim(line, "[]")) + "]"
			}
			if section == "[package]" {
				isPkg = true
			}
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		str, ok := tomlString(strings.TrimSpace(val))
		if !ok {
			continue
		}
		switch section + key {
		case "[package]name":
			pkgName = str
		case "[lib]name":
			libName = str
		case "[lib]path":
			m.Lib = path.Clean(str)
		case "[[bin]]name":
			binName = str
		case "[[bin]]path":
			binPath = str
		}
	}
	if section == "[[bin]]" {
		flushBin()
	}
	m.Name = RustCrateName(pkgName)
	if libName != "" {
		m.Name = RustCrateName(libName)
	}
	return m, isPkg && m.Name != ""
}

// tomlString reads a basic or literal TOML string at the start of s.
func tomlString(s string) (string, bool) {
	if len(s) < 2 || (s[0] != '"' && s[0] != '\'') {
		return "", false
	}
	end := strings.IndexByte(s[1:], s[0])
	if end < 0 {
		return "", false
	}
	return s[1 : 1+end], true
}

// RustCrateName is the identifier a package name gives its crate.
func RustCrateName(name string) string { return strings.ReplaceAll(strings.TrimSpace(name), "-", "_") }

func joinPath(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// rustPackageKey is the key of the file p of the package in directory dir.
func rustPackageKey(m CargoManifest, dir, p string, o rustOptions) string {
	rel := p
	if dir != "." {
		rel = strings.TrimPrefix(p, dir+"/")
	}
	// [[bin]] targets with a path: a crate of their own.
	for name, bp := range m.Bins {
		if rel == bp {
			return m.Name + "/bin/" + name
		}
		if bd := path.Dir(bp); path.Base(bp) == "main.rs" && strings.HasPrefix(rel, bd+"/") {
			return rustTreeKey(m.Name+"/bin/"+name, bd, rel, o)
		}
	}
	// Targets with one crate per file or directory.
	for _, td := range rustTargetDirs {
		rest, ok := strings.CutPrefix(rel, td+"/")
		if !ok {
			continue
		}
		kind := path.Base(td)
		target, below, nested := strings.Cut(rest, "/")
		if !nested {
			return m.Name + "/" + kind + "/" + strings.TrimSuffix(target, ".rs")
		}
		return rustTreeKey(m.Name+"/"+kind+"/"+target, td+"/"+target, td+"/"+target+"/"+below, o, "main.rs")
	}
	// The library (and src/main.rs): a [lib] path roots the tree in its
	// directory.
	if m.Lib != "" {
		if rel == m.Lib {
			return m.Name
		}
		if ld := path.Dir(m.Lib); ld == "." || strings.HasPrefix(rel, ld+"/") {
			return rustTreeKey(m.Name, ld, rel, o, path.Base(m.Lib))
		}
	}
	for _, r := range o.roots {
		if rel == r {
			return m.Name
		}
	}
	if rd := rustRootDir(o.roots); rd != "" && strings.HasPrefix(rel, rd+"/") {
		return rustTreeKey(m.Name, rd, rel, o)
	}
	// Anything else (build.rs, a script): a crate of its own.
	return m.Name + "/" + strings.TrimSuffix(rel, ".rs")
}

// rustRootDir is the directory of the crate roots (src), "" when they
// differ.
func rustRootDir(roots []string) string {
	dir := ""
	for i, r := range roots {
		d := path.Dir(r)
		if i > 0 && d != dir {
			return ""
		}
		dir = d
	}
	if dir == "." {
		return ""
	}
	return dir
}

// rustTreeKey is the module path of rel in the mod tree rooted at the
// directory root with key key: the root files (crate roots, extra) are the
// key itself, a/b.rs and a/b/mod.rs are key::a::b.
func rustTreeKey(key, root, rel string, o rustOptions, extra ...string) string {
	if root != "." {
		rel = strings.TrimPrefix(rel, root+"/")
	}
	if !strings.Contains(rel, "/") {
		for _, r := range o.roots {
			if rel == path.Base(r) {
				return key
			}
		}
		for _, r := range extra {
			if rel == r {
				return key
			}
		}
		if rel == o.modFile && root != "." {
			return key
		}
	}
	segs := strings.Split(strings.TrimSuffix(rel, ".rs"), "/")
	if len(segs) > 1 && segs[len(segs)-1]+".rs" == o.modFile {
		segs = segs[:len(segs)-1]
	}
	return key + "::" + strings.Join(segs, "::")
}

// rustCrate is the crate part of a package key.
func rustCrate(key string) string {
	if i := strings.Index(key, "::"); i >= 0 {
		return key[:i]
	}
	return key
}

// rustParent is the module above key; false at a crate root.
func rustParent(key string) (string, bool) {
	i := strings.LastIndex(key, "::")
	if i < 0 {
		return "", false
	}
	return key[:i], true
}

// RustUse is one path a use form brings in.
type RustUse struct {
	// Path is the full path as written, by segment ("" first for ::name).
	Path []string
	// Alias is the name the path is bound to ("" for its last segment).
	Alias string
	// Glob is a path::* form; Path is the module.
	Glob bool
}

// RustUses expands a use import (Spec, Names as written, Alias, Kind glob)
// into the paths it binds: use a::{b, c::d as e, f::*, self}.
func RustUses(im codeparse.Import) []RustUse {
	spec := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(im.Spec), "use ")), ";")
	var out []RustUse
	switch {
	case im.Kind == "glob":
		segs := rustSegs(spec)
		if n := len(segs); n > 0 && segs[n-1] == "*" {
			segs = segs[:n-1]
		}
		out = append(out, RustUse{Path: segs, Glob: true})
	case len(im.Names) > 0:
		prefix := rustSegs(spec)
		for _, n := range im.Names {
			start := len(out)
			parseUseTree(prefix, n, &out)
			// use a::b as c: the alias of the one name.
			if im.Alias != "" && len(im.Names) == 1 && len(out) == start+1 && out[start].Alias == "" && !out[start].Glob {
				out[start].Alias = im.Alias
			}
		}
	default:
		parseUseTree(nil, spec, &out)
		if im.Alias != "" && len(out) == 1 && !out[0].Glob {
			out[0].Alias = im.Alias
		}
	}
	return out
}

// rustSegs splits a path at "::", spaces removed; "" when s is empty.
func rustSegs(s string) []string {
	s = strings.Join(strings.Fields(s), "")
	if s == "" {
		return nil
	}
	return strings.Split(s, "::")
}

// parseUseTree appends the paths of one use tree below prefix.
func parseUseTree(prefix []string, s string, out *[]RustUse) {
	s = strings.TrimSpace(s)
	if s == "" {
		return
	}
	if i := strings.IndexByte(s, '{'); i >= 0 {
		j := strings.LastIndexByte(s, '}')
		if j < i {
			j = len(s)
		}
		head := strings.TrimSuffix(strings.TrimSpace(s[:i]), "::")
		p := append(append([]string{}, prefix...), rustSegs(head)...)
		for _, part := range splitTopLevel(s[i+1 : j]) {
			parseUseTree(p, part, out)
		}
		return
	}
	alias := ""
	if f := strings.Fields(s); len(f) >= 3 && f[len(f)-2] == "as" {
		alias = f[len(f)-1]
		s = strings.Join(f[:len(f)-2], " ")
	}
	segs := append(append([]string{}, prefix...), rustSegs(s)...)
	if len(segs) == 0 {
		return
	}
	switch segs[len(segs)-1] {
	case "*":
		*out = append(*out, RustUse{Path: segs[:len(segs)-1], Glob: true})
	case "self":
		if len(segs) > 1 {
			*out = append(*out, RustUse{Path: segs[:len(segs)-1], Alias: alias})
		}
	default:
		*out = append(*out, RustUse{Path: segs, Alias: alias})
	}
}

// splitTopLevel splits s at the commas outside braces.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// Scope implements Resolver.
func (rust) Scope(_ context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	exists := func(key string) bool { return len(v.Package(codeparse.FamilyRust, key)) > 0 }
	own := rustCrate(f.Package)
	// Modules the file declares, as children of its module.
	local := map[string]bool{}
	for _, sym := range f.Facts.Symbols {
		if sym.Kind == codeparse.KindModule && sym.Parent == "" {
			local[sym.Name] = true
		}
	}
	for _, im := range f.Facts.Imports {
		if im.Kind == "mod" {
			local[strings.TrimSpace(im.Spec)] = true
		}
	}
	// key translates a path (by segment) used in module mod (the file's,
	// or an inline module's below it) to a package key, and reports
	// whether the path is the project's.
	key := func(mod string, segs []string) (string, bool) {
		if len(segs) == 0 {
			return mod, true
		}
		base, rest := "", segs[1:]
		switch head := segs[0]; {
		case head == "crate" || head == "$crate":
			base = own
		case head == "self":
			base = mod
		case head == "super":
			base = mod
			i := 0
			for ; i < len(segs) && segs[i] == "super"; i++ {
				p, ok := rustParent(base)
				if !ok {
					return "", false
				}
				base = p
			}
			rest = segs[i:]
		case head == "" && len(segs) > 1:
			base, rest = segs[1], segs[2:]
		case mod == f.Package && local[head]:
			base = mod + "::" + head
		default:
			base = head
		}
		k := base
		if len(rest) > 0 {
			k += "::" + strings.Join(rest, "::")
		}
		return k, rustCrate(k) == own || exists(rustCrate(k))
	}
	for _, im := range f.Facts.Imports {
		mod := f.Package
		if inl := rustInlineModule(f.Facts, im.Start); inl != "" {
			mod += "::" + inl
		}
		switch im.Kind {
		case "mod":
			name := strings.TrimSpace(im.Spec)
			if child := mod + "::" + name; exists(child) {
				s.AddQualifier(name, child)
			}
			continue
		case "extern_crate":
			name := strings.TrimSpace(im.Spec)
			target := name
			if name == "self" {
				target = own
			}
			alias := im.Alias
			if alias == "" {
				alias = name
			}
			if alias != "_" {
				s.AddQualifier(alias, target)
			}
			continue
		}
		for _, u := range RustUses(im) {
			k, inProject := key(mod, u.Path)
			if k == "" {
				continue
			}
			if u.Glob {
				s.AddOpen(k)
				continue
			}
			last := u.Path[len(u.Path)-1]
			name := u.Alias
			if name == "" {
				name = last
			}
			if name == "_" {
				continue
			}
			parent, hasParent := rustParent(k)
			switch {
			case inProject && exists(k): // a module
				s.AddQualifier(name, k)
			case inProject: // an item of a module
				if hasParent && u.Alias == "" {
					s.AddName(last, parent)
				}
			default: // outside the project: keep its references unresolved
				s.AddQualifier(name, k)
				if hasParent && u.Alias == "" {
					s.AddName(last, parent)
				}
			}
		}
	}
	// Path roots and crates named in qualified references.
	for _, ref := range f.Facts.Refs {
		if ref.Qualifier == "" {
			continue
		}
		head, _, _ := strings.Cut(ref.Qualifier, "::")
		if _, bound := s.Qualifiers[head]; bound {
			continue
		}
		switch {
		case head == "crate" || head == "$crate":
			s.AddQualifier(head, own)
		case head == "super":
			if p, ok := rustParent(f.Package); ok {
				s.AddQualifier(head, p)
			}
		case head == "self" || head == "Self" || strings.ContainsAny(head, ".()[]<> "):
		case local[head]:
			if child := f.Package + "::" + head; exists(child) {
				s.AddQualifier(head, child)
			}
		case exists(head) && rustCrate(head) == head:
			s.AddQualifier(head, head)
		}
	}
	return s, nil
}

// rustInlineModule is the path ("a::b") of the inline modules of facts
// that contain pos, "" at the file's top level.
func rustInlineModule(facts *codeparse.Facts, pos codeparse.Pos) string {
	byID := map[string]*codeparse.Symbol{}
	for i := range facts.Symbols {
		byID[facts.Symbols[i].ID] = &facts.Symbols[i]
	}
	var inner *codeparse.Symbol
	for i := range facts.Symbols {
		sym := &facts.Symbols[i]
		if sym.Kind != codeparse.KindModule || !sym.Start.Before(pos) || !pos.Before(sym.End) {
			continue
		}
		if inner == nil || inner.Start.Before(sym.Start) {
			inner = sym
		}
	}
	var segs []string
	for sym := inner; sym != nil; sym = byID[sym.Parent] {
		if sym.Kind == codeparse.KindModule {
			segs = append([]string{sym.Name}, segs...)
		}
		if sym.Parent == "" {
			break
		}
	}
	return strings.Join(segs, "::")
}

// Visible implements Resolver: a crate sees all of its definitions,
// another crate only what is pub. An impl block is never a target: it is
// named after its type but no path names it (its methods stay targets).
func (rust) Visible(f *File, c Candidate, _ Step) bool {
	if c.Symbol.Kind == codeparse.KindImpl {
		return false
	}
	return rustCrate(c.File.Package) == rustCrate(f.Package) || c.Symbol.Exported
}
