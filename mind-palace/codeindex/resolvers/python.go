package resolvers

import (
	"context"
	"crypto/sha256"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Python family. Every module is a namespace of its own, named by its
// path: a file's package key is its path without the extension (a/b.py →
// a/b), and a package's __init__.py (or .pyi) is keyed by its directory
// (a/__init__.py → a), so a stub and its module share one key. An import
// spec is located among the project's files:
//
//   - a relative spec (.m, ..p.m, .) from the importing file's package,
//     one directory up per dot after the first;
//   - an absolute one (a.b.m) below the root of the importer's package
//     (the directory above the outermost one with an __init__.py), the
//     project root, then src/;
//
// and one the project does not hold (the standard library, a dependency)
// keeps its dotted spec as the key, which no file has, so references
// through it stay unresolved.
//
// import a.b.c binds a (the top package) and import a.b.c as m binds m to
// a.b.c; from m import x binds the bare name x to m, followed through the
// __init__.py re-exports of m (from .impl import x, from .impl import *)
// to the modules that define it, and binds x as a qualifier when it is a
// submodule (m/x.py); from m import * opens m. A module's qualifier also
// reaches what its __init__.py star-imports. from m import x as y binds y
// only when x is a submodule: a renamed name is not traced. A module the
// view holds no facts for because it only imports (an __init__.py that
// re-exports) is parsed from its content for its imports.
//
// Python reaches nothing unimported, so a bare name the scopes do not
// define falls back (confidence name) only to top-level definitions, the
// exported ones (no leading underscore) of other modules; a star import
// opens only exported names too. A method body does not see its class's
// attributes unqualified, so a bare name never resolves to a class member
// through the enclosing scope (self.f goes through the member fallback).
func init() { Register(codeparse.FamilyPython, python{}) }

type python struct{}

var pythonRules = Rules{Separators: []string{"."}, NameFallback: true, MemberFallback: true}

// pythonMaxDepth bounds how far re-exports and star imports are followed.
const pythonMaxDepth = 8

// Rules implements Resolver.
func (python) Rules() Rules { return pythonRules }

// Package implements Resolver: PythonModuleKey of the file's path.
func (python) Package(_ context.Context, _ View, f *File) (string, error) {
	return PythonModuleKey(f.Path), nil
}

// PythonModuleKey is the package key of the Python file at p: the path
// without its .py or .pyi extension, and the directory for a package's
// __init__ ("." at the project root).
func PythonModuleKey(p string) string {
	if ext := path.Ext(p); ext == ".py" || ext == ".pyi" {
		p = strings.TrimSuffix(p, ext)
	}
	if path.Base(p) == "__init__" {
		return path.Dir(p)
	}
	return p
}

// Scope implements Resolver.
func (python) Scope(ctx context.Context, v View, f *File) (Scope, error) {
	var s Scope
	if f.Facts == nil {
		return s, nil
	}
	r := &pyRun{ctx: ctx, v: v, files: map[string][]*File{}}
	for _, im := range f.Facts.Imports {
		r.bind(&s, f, im)
		if r.err != nil {
			return Scope{}, r.err
		}
	}
	return s, nil
}

// pyRun is one Scope call: the view, the modules' files read so far and
// the first error.
type pyRun struct {
	ctx   context.Context
	v     View
	files map[string][]*File
	err   error
}

// bind records what one import of f brings into scope.
func (r *pyRun) bind(s *Scope, f *File, im codeparse.Import) {
	v := r.v
	spec := strings.TrimSpace(im.Spec)
	if spec == "" {
		return
	}
	star := im.Alias == "*"
	var names []string
	for _, n := range im.Names {
		if n == "*" {
			star = true
		} else if n != "" {
			names = append(names, n)
		}
	}
	switch {
	case star:
		key, _, _ := pyLocate(v, f, spec)
		for _, k := range r.starClosure(key) {
			s.AddOpen(k)
		}
	case len(names) > 0:
		mod, _, _ := pyLocate(v, f, spec)
		for _, n := range names {
			sub, _, isSub := pyLocate(v, f, pySubmodule(spec, n))
			if im.Alias != "" && len(names) == 1 {
				if isSub {
					s.AddQualifier(im.Alias, sub)
				}
				continue
			}
			if isSub {
				for _, k := range r.starClosure(sub) {
					s.AddQualifier(n, k)
				}
			}
			for _, k := range r.nameSources(mod, n) {
				s.AddName(n, k)
			}
		}
	case im.Alias != "":
		key, _, _ := pyLocate(v, f, spec)
		for _, k := range r.starClosure(key) {
			s.AddQualifier(im.Alias, k)
		}
	case strings.HasPrefix(spec, "."):
		// A relative module without names (from . import m written as one
		// spec): it binds its last part.
		key, _, _ := pyLocate(v, f, spec)
		if q := LastSegment(strings.TrimLeft(spec, "."), pythonRules.Separators); q != "" {
			for _, k := range r.starClosure(key) {
				s.AddQualifier(q, k)
			}
		}
	default:
		// import a.b.c binds a, the top package, located where a.b.c is.
		top, _, _ := strings.Cut(spec, ".")
		_, root, ok := pyLocate(v, f, spec)
		key := top
		if ok {
			key = pyJoin(root, top)
		} else if k, _, found := pyLocate(v, f, top); found {
			key = k
		}
		for _, k := range r.starClosure(key) {
			s.AddQualifier(top, k)
		}
	}
}

// pySubmodule is the spec of submodule n of spec ("." and "m" → .m, "a" and
// "m" → a.m).
func pySubmodule(spec, n string) string {
	if strings.HasSuffix(spec, ".") {
		return spec + n
	}
	return spec + "." + n
}

// pyLocate finds the module spec names when imported from f: its package
// key, the root directory it was found under, and whether the project
// holds it. A module it does not hold keys by the spec itself (absolute)
// or by the path it would have (relative).
func pyLocate(v View, f *File, spec string) (key, root string, ok bool) {
	rest := strings.TrimLeft(spec, ".")
	dots := len(spec) - len(rest)
	rel := strings.ReplaceAll(rest, ".", "/")
	if dots > 0 {
		base := f.Dir()
		for i := 1; i < dots; i++ {
			if base == "." {
				return spec, "", false // above the project
			}
			base = path.Dir(base)
		}
		key = pyJoin(base, rel)
		return key, base, pyExists(v, key)
	}
	if rel == "" {
		return spec, "", false
	}
	for _, root := range pyRoots(v, f) {
		if key := pyJoin(root, rel); pyExists(v, key) {
			return key, root, true
		}
	}
	return spec, "", false
}

// pyRoots are the directories an absolute import is looked up under, for
// a module imported from f: the root of f's package, the project root, and
// src/.
func pyRoots(v View, f *File) []string {
	d := f.Dir()
	for d != "." && pyIsPackage(v, d) {
		d = path.Dir(d)
	}
	roots := []string{d}
	for _, r := range []string{".", "src"} {
		if !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	return roots
}

func pyJoin(dir, rel string) string { return path.Join(dir, rel) }

// pyIsPackage reports whether directory dir holds an __init__.py or .pyi.
func pyIsPackage(v View, dir string) bool {
	prefix := ""
	if dir != "." {
		prefix = dir + "/"
	}
	return v.Has(prefix+"__init__.py") || v.Has(prefix+"__init__.pyi")
}

// pyExists reports whether a module or package keyed key is visible.
func pyExists(v View, key string) bool {
	if key != "." && (v.Has(key+".py") || v.Has(key+".pyi")) {
		return true
	}
	return pyIsPackage(v, key)
}

// starClosure is key and the modules its files star-import (from .impl
// import *), transitively: what a qualifier naming key reaches.
func (r *pyRun) starClosure(key string) []string {
	out := []string{key}
	for i := 0; i < len(out) && i < pythonMaxDepth*4; i++ {
		for _, f := range r.moduleFiles(out[i]) {
			if f.Facts == nil {
				continue
			}
			for _, im := range f.Facts.Imports {
				if im.Alias != "*" && !slices.Contains(im.Names, "*") {
					continue
				}
				if k, _, ok := pyLocate(r.v, f, strings.TrimSpace(im.Spec)); ok {
					out = appendNew(out, k)
				}
			}
		}
	}
	return out
}

// nameSources are the modules a name imported from module key comes
// from: key when it defines the name at top level (or is not in the
// project), else the modules its files re-export the name from (from .impl
// import name, from .impl import *), followed transitively; key itself
// when no re-export leads to a definition.
func (r *pyRun) nameSources(key, name string) []string {
	var out []string
	r.sources(key, name, map[string]bool{}, 0, &out)
	if len(out) == 0 {
		return []string{key}
	}
	return out
}

func (r *pyRun) sources(key, name string, seen map[string]bool, depth int, out *[]string) {
	if seen[key] || depth > pythonMaxDepth {
		return
	}
	seen[key] = true
	files := r.moduleFiles(key)
	for _, f := range files {
		if f.Facts == nil {
			continue
		}
		for _, sym := range f.Facts.Symbols {
			if sym.Parent == "" && sym.Name == name {
				*out = appendNew(*out, key)
				return
			}
		}
	}
	for _, f := range files {
		if f.Facts == nil {
			continue
		}
		for _, im := range f.Facts.Imports {
			reexports := im.Alias == "*" || slices.Contains(im.Names, "*") ||
				(slices.Contains(im.Names, name) && (im.Alias == "" || len(im.Names) > 1))
			if !reexports {
				continue
			}
			if k, _, ok := pyLocate(r.v, f, strings.TrimSpace(im.Spec)); ok {
				r.sources(k, name, seen, depth+1, out)
			}
		}
	}
}

// moduleFiles are the files of the module keyed key. The view holds only
// files with definitions or references, so a module that only imports (an
// __init__.py re-exporting its submodules) is read from its content and
// parsed for its imports.
func (r *pyRun) moduleFiles(key string) []*File {
	if fs, ok := r.files[key]; ok {
		return fs
	}
	fs := r.v.Package(codeparse.FamilyPython, key)
	if len(fs) == 0 && r.err == nil {
		for _, p := range pyModulePaths(key) {
			if !r.v.Has(p) {
				continue
			}
			content, ok, err := r.v.Content(r.ctx, p)
			if err != nil {
				r.err = err
				break
			}
			if !ok {
				continue
			}
			if facts := pyImportsOf(p, content); facts != nil {
				fs = append(fs, &File{Path: p, Layer: r.v.Layer(), Lang: "python", Family: codeparse.FamilyPython, Package: key, Facts: facts})
			}
		}
	}
	r.files[key] = fs
	return fs
}

// pyModulePaths are the paths a module keyed key may have.
func pyModulePaths(key string) []string {
	prefix := ""
	if key != "." {
		prefix = key + "/"
	}
	paths := []string{prefix + "__init__.py", prefix + "__init__.pyi"}
	if key != "." {
		paths = append([]string{key + ".py", key + ".pyi"}, paths...)
	}
	return paths
}

// pyImportCacheSize bounds the import-only modules whose imports are kept.
const pyImportCacheSize = 4096

var pyImportCache = struct {
	sync.Mutex
	m map[[sha256.Size]byte]*codeparse.Facts
}{m: map[[sha256.Size]byte]*codeparse.Facts{}}

// pyImportsOf parses a module's content for its imports alone; nil when it
// has none or does not parse.
func pyImportsOf(p string, content []byte) *codeparse.Facts {
	sum := sha256.Sum256(content)
	pyImportCache.Lock()
	facts, ok := pyImportCache.m[sum]
	pyImportCache.Unlock()
	if ok {
		return facts
	}
	if full, err := codeparse.Parse(p, content); err == nil && full.ParseError == "" && len(full.Imports) > 0 {
		facts = &codeparse.Facts{Lang: full.Lang, Imports: full.Imports}
	}
	pyImportCache.Lock()
	if len(pyImportCache.m) >= pyImportCacheSize {
		clear(pyImportCache.m)
	}
	pyImportCache.m[sum] = facts
	pyImportCache.Unlock()
	return facts
}

// Visible implements Resolver: an explicit import or a qualifier reaches
// any definition (Python hides nothing); a star import and the name
// fallback reach other modules' exported definitions, and the fallback
// only top-level ones. The enclosing scope never yields a class member.
func (python) Visible(f *File, c Candidate, step Step) bool {
	switch step {
	case StepScope:
		return !pyClassMember(c)
	case StepOpen:
		return c.File.Package == f.Package || c.Symbol.Exported
	case StepName:
		return c.TopLevel() && (c.File.Package == f.Package || c.Symbol.Exported)
	}
	return true
}

// pyClassMember reports whether c is defined in a class body.
func pyClassMember(c Candidate) bool {
	if c.TopLevel() {
		return false
	}
	if c.Symbol.Kind == codeparse.KindMethod {
		return true
	}
	if c.File.Facts == nil {
		return false
	}
	for _, s := range c.File.Facts.Symbols {
		if s.ID == c.Symbol.Parent {
			return s.Kind == codeparse.KindClass
		}
	}
	return false
}
