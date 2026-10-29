package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// pySrc builds, by hand, the facts the Python extractor gives for one
// file: no package clause (a module is named by its path), names
// qualified by their enclosing definitions only, exported unless they
// start with an underscore. A file's content is its facts as JSON.
type pySrc struct {
	f    codeparse.Facts
	line int
}

func pyFile() *pySrc { return &pySrc{f: codeparse.Facts{Lang: "python"}} }

func (p *pySrc) pos() codeparse.Pos {
	p.line++
	return codeparse.Pos{Line: p.line, Offset: p.line * 100}
}

// imp adds an import: import spec (no names, no alias), import spec as
// alias, from spec import names... (with alias for from spec import n as
// alias).
func (p *pySrc) imp(spec, alias string, names ...string) *pySrc {
	p.f.Imports = append(p.f.Imports, codeparse.Import{Spec: spec, Alias: alias, Names: names, Start: p.pos()})
	return p
}

// def adds a definition; local is Name or Parent.Name.
func (p *pySrc) def(kind, local string) *pySrc {
	name, parent := local, ""
	if i := strings.LastIndex(local, "."); i >= 0 {
		name, parent = local[i+1:], codeparse.SymbolID(local[:i], 1)
	}
	at := p.pos()
	p.f.Symbols = append(p.f.Symbols, codeparse.Symbol{ID: codeparse.SymbolID(local, 1), Kind: kind, Name: name,
		QualName: local, Start: at, End: at, Parent: parent, Exported: !strings.HasPrefix(name, "_")})
	return p
}

func (p *pySrc) fn(local string) *pySrc     { return p.def(codeparse.KindFunction, local) }
func (p *pySrc) class(local string) *pySrc  { return p.def(codeparse.KindClass, local) }
func (p *pySrc) method(local string) *pySrc { return p.def(codeparse.KindMethod, local) }

// ref adds a reference from enclosing ("" for module level) to target, a
// name with its qualifier (m.f, self.save, helper).
func (p *pySrc) ref(kind, enclosing, target string) *pySrc {
	qual, name := "", target
	if i := strings.LastIndex(target, "."); i >= 0 {
		qual, name = target[:i], target[i+1:]
	}
	r := codeparse.Ref{Name: name, Qualifier: qual, Kind: kind, Start: p.pos()}
	if enclosing != "" {
		r.Enclosing = codeparse.SymbolID(enclosing, 1)
	}
	p.f.Refs = append(p.f.Refs, r)
	return p
}

func (p *pySrc) call(enclosing, target string) *pySrc {
	return p.ref(codeparse.RefCall, enclosing, target)
}

func (p *pySrc) src() string {
	b, err := json.Marshal(p.f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// pyView is a resolvers.View over a fixed set of Python files.
type pyView struct {
	files map[string]*resolvers.File
	other map[string]bool
}

func (v pyView) Layer() string { return codegraph.BaseLayerID }
func (v pyView) Has(p string) bool {
	_, ok := v.files[p]
	return ok || v.other[p]
}
func (v pyView) Content(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func (v pyView) Package(family, key string) []*resolvers.File {
	var out []*resolvers.File
	for _, f := range v.files {
		if f.Family == family && f.Package == key {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func pythonResolver(t *testing.T) resolvers.Resolver {
	t.Helper()
	family, r, ok := resolvers.ForLang("python")
	if !ok || family != codeparse.FamilyPython {
		t.Fatalf("ForLang(python) = %q, %v", family, ok)
	}
	return r
}

// newPyView builds the view of tree (path → facts), with package keys
// from the resolver; other paths are visible files of no family.
func newPyView(t *testing.T, tree map[string]*pySrc, other ...string) pyView {
	t.Helper()
	r := pythonResolver(t)
	v := pyView{files: map[string]*resolvers.File{}, other: map[string]bool{}}
	for _, p := range other {
		v.other[p] = true
	}
	for p, s := range tree {
		facts := s.f
		v.files[p] = &resolvers.File{Path: p, Layer: codegraph.BaseLayerID, Lang: "python", Family: codeparse.FamilyPython, Facts: &facts}
	}
	for _, f := range v.files {
		key, err := r.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
	}
	return v
}

func TestModuleKeys_python(t *testing.T) {
	for p, want := range map[string]string{
		"a.py":                 "a",
		"pkg/mod.py":           "pkg/mod",
		"pkg/mod.pyi":          "pkg/mod",
		"pkg/__init__.py":      "pkg",
		"pkg/sub/__init__.pyi": "pkg/sub",
		"__init__.py":          ".",
		"src/lib/x.py":         "src/lib/x",
	} {
		if got := resolvers.PythonModuleKey(p); got != want {
			t.Errorf("PythonModuleKey(%q) = %q, want %q", p, got, want)
		}
	}
	r := pythonResolver(t)
	rules := r.Rules()
	if !reflect.DeepEqual(rules.Separators, []string{"."}) || !rules.NameFallback || !rules.MemberFallback || rules.CaseInsensitive {
		t.Errorf("rules = %+v", rules)
	}
	// The declared package (none in Python) and the profile never change the key.
	f := &resolvers.File{Path: "pkg/mod.py", Family: codeparse.FamilyPython,
		Facts:   &codeparse.Facts{Package: "other"},
		Profile: &codeparse.Profile{Scope: codeparse.Scope{PackageBy: codeparse.PackageByDirectory}}}
	if key, err := r.Package(context.Background(), pyView{}, f); err != nil || key != "pkg/mod" {
		t.Errorf("Package = %q, %v", key, err)
	}
}

func TestScope_python(t *testing.T) {
	tree := map[string]*pySrc{
		"app/__init__.py": pyFile().imp(".core", "", "*").imp(".models", "", "User", "Base").fn("version"),
		"app/core.py":     pyFile().fn("run"),
		"app/models.py":   pyFile().imp(".base", "", "Base").class("User"),
		"app/base.py":     pyFile().class("Base"),
		"app/util.pyi":    pyFile().fn("helper"),
		"app/sub/__init__.py": pyFile().
			imp("..", "", "util").          // a sibling package's module
			imp("..util", "", "helper").    // a name from a stub-only module
			imp(".", "", "deep").           // a submodule
			imp(".deep", "dp").             // relative, whole module (one spec)
			imp("....", "", "toohigh").     // above the project
			imp("app", "", "User", "Base"). // re-exported through two __init__/modules
			imp("app.models", "", "*").     // star
			imp("app.sub", "s", "deep").    // from m import x as s, x a submodule
			imp("app.models", "u", "User"). // a renamed name: u is User
			imp("app.core", "c").           // import m as c
			imp("app.sub.deep", "").        // binds app
			imp("os.path", "").             // outside the project: binds os
			imp("json", "", "loads").       // a name from outside the project
			imp("mystery", "", "*"),        // star from outside the project
		"app/sub/deep.py": pyFile().fn("work"),
	}
	v := newPyView(t, tree)
	sc, err := pythonResolver(t).Scope(context.Background(), v, v.files["app/sub/__init__.py"])
	if err != nil {
		t.Fatal(err)
	}
	wantQ := map[string][]string{
		"util": {"app/util"},
		"deep": {"app/sub/deep"},
		"dp":   {"app/sub/deep"},
		"s":    {"app/sub/deep"},
		"c":    {"app/core"},
		"app":  {"app", "app/core"}, // app's __init__ star-imports .core
		// import app.sub.deep binds the dotted paths to their own modules
		// (app.sub's __init__ star-imports app.models).
		"app.sub":      {"app/sub", "app/models"},
		"app.sub.deep": {"app/sub/deep"},
		"os":           {"os"},
		"os.path":      {"os.path"}, // outside the project: a key no file has
	}
	if !reflect.DeepEqual(sc.Qualifiers, wantQ) {
		t.Errorf("qualifiers:\n got %v\nwant %v", sc.Qualifiers, wantQ)
	}
	wantN := map[string][]string{
		"util":    {"app"},
		"helper":  {"app/util"},
		"deep":    {"app/sub"},
		"toohigh": {"...."},
		"User":    {"app/models"},
		"Base":    {"app/base"}, // app → .models (from .base import Base) → app/base
		"loads":   {"json"},
	}
	if !reflect.DeepEqual(sc.Names, wantN) {
		t.Errorf("names:\n got %v\nwant %v", sc.Names, wantN)
	}
	if want := map[string][]resolvers.Original{"u": {{Package: "app/models", Name: "User"}}}; !reflect.DeepEqual(sc.Renamed, want) {
		t.Errorf("renamed = %v, want %v", sc.Renamed, want)
	}
	if want := []string{"app/models", "mystery"}; !reflect.DeepEqual(sc.Open, want) {
		t.Errorf("open = %v, want %v", sc.Open, want)
	}
}

func TestScopeRoots_python(t *testing.T) {
	tree := map[string]*pySrc{
		"src/lib/__init__.py":       pyFile(),
		"src/lib/thing.py":          pyFile().fn("thing"),
		"tests/test_lib.py":         pyFile().imp("lib.thing", "", "thing").imp("conftest_helpers", ""),
		"tests/conftest_helpers.py": pyFile().fn("fixture"),
		"scripts/tool.py":           pyFile().imp("helpers", "").imp("lib", ""),
		"scripts/helpers.py":        pyFile().fn("do"),
		"pkg/__init__.py":           pyFile(),
		"pkg/a/__init__.py":         pyFile(),
		"pkg/a/mod.py":              pyFile().imp("pkg.b", "", "x").imp("ns.leaf", "").imp("top", ""),
		"pkg/b.py":                  pyFile().fn("x"),
		"ns/leaf.py":                pyFile(), // a namespace package: no ns/__init__.py
		"top.py":                    pyFile(),
	}
	v := newPyView(t, tree)
	r := pythonResolver(t)
	scope := func(p string) resolvers.Scope {
		t.Helper()
		sc, err := r.Scope(context.Background(), v, v.files[p])
		if err != nil {
			t.Fatal(err)
		}
		return sc
	}
	// tests/ is not a package: its own directory and src/ are roots.
	sc := scope("tests/test_lib.py")
	if got := sc.Names["thing"]; !reflect.DeepEqual(got, []string{"src/lib/thing"}) {
		t.Errorf("tests: thing from %v", got)
	}
	if got := sc.Qualifiers["conftest_helpers"]; !reflect.DeepEqual(got, []string{"tests/conftest_helpers"}) {
		t.Errorf("tests: conftest_helpers = %v", got)
	}
	sc = scope("scripts/tool.py")
	if got := sc.Qualifiers["helpers"]; !reflect.DeepEqual(got, []string{"scripts/helpers"}) {
		t.Errorf("scripts: helpers = %v", got)
	}
	if got := sc.Qualifiers["lib"]; !reflect.DeepEqual(got, []string{"src/lib"}) {
		t.Errorf("scripts: lib = %v", got)
	}
	// Inside a package, the root is above its outermost __init__.py.
	sc = scope("pkg/a/mod.py")
	want := map[string][]string{"ns": {"ns"}, "ns.leaf": {"ns/leaf"}, "top": {"top"}}
	if !reflect.DeepEqual(sc.Qualifiers, want) || !reflect.DeepEqual(sc.Names["x"], []string{"pkg/b"}) {
		t.Errorf("pkg: qualifiers %v names %v", sc.Qualifiers, sc.Names)
	}
	// Nothing to scope without facts.
	if sc, err := r.Scope(context.Background(), v, &resolvers.File{Path: "x.py"}); err != nil || sc.Qualifiers != nil || sc.Names != nil || sc.Open != nil {
		t.Errorf("no facts: %+v, %v", sc, err)
	}
}

func TestVisible_python(t *testing.T) {
	tree := map[string]*pySrc{
		"a.py": pyFile().fn("f").fn("_g").class("C").method("C.m").fn("f.inner").def(codeparse.KindVar, "C.attr"),
		"b.py": pyFile(),
	}
	v := newPyView(t, tree)
	r := pythonResolver(t)
	a, b := v.files["a.py"], v.files["b.py"]
	sym := func(id string) resolvers.Candidate {
		for i := range a.Facts.Symbols {
			if a.Facts.Symbols[i].ID == id {
				return resolvers.Candidate{File: a, Symbol: &a.Facts.Symbols[i]}
			}
		}
		t.Fatalf("no symbol %s", id)
		return resolvers.Candidate{}
	}
	for _, c := range []struct {
		from *resolvers.File
		id   string
		step resolvers.Step
		want bool
	}{
		{b, "f", resolvers.StepImport, true},
		{b, "_g", resolvers.StepImport, true}, // from a import _g
		{b, "_g", resolvers.StepQualified, true},
		{b, "C.m", resolvers.StepMember, true},
		{b, "f", resolvers.StepOpen, true},
		{b, "_g", resolvers.StepOpen, false}, // from a import * skips it
		{a, "_g", resolvers.StepOpen, true},
		{b, "f", resolvers.StepName, true},
		{b, "_g", resolvers.StepName, false},
		{a, "_g", resolvers.StepName, true},
		{b, "C.m", resolvers.StepName, false}, // a member is never a bare name
		{a, "f.inner", resolvers.StepScope, true},
		{a, "C.m", resolvers.StepScope, false},    // a method body does not see its class
		{a, "C.attr", resolvers.StepScope, false}, // nor its attributes
	} {
		if got := r.Visible(c.from, sym(c.id), c.step); got != c.want {
			t.Errorf("Visible(%s, %s, %s) = %v, want %v", c.from.Path, c.id, c.step, got, c.want)
		}
	}
}

// pyIndex indexes tree (path → content) as one commit with parse and
// returns the neighbors of a symbol in the base view, as target (or
// source, with from) → confidence.
func pyIndex(t *testing.T, tree map[string]string, parse codeindex.ParseFunc) func(rel codegraph.Relation, path, id string, from bool) map[string]string {
	t.Helper()
	ctx := context.Background()
	const proj = "u0123456789abcdef__python-resolver"
	const wt = "wt_0000000000000001"
	store, graphs := memcode.New(), memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{proj}, nil },
		Code: func(_ context.Context, p string) (code.Store, error) {
			if p != proj {
				return nil, code.ErrNotFound
			}
			return store, nil
		},
		Graphs:  graphs,
		Cursors: codeindex.NewMemCursors(nil),
		Apply: codeindex.NewResolver(codeindex.ResolverOptions{
			// Goldens parse under -race on slow runners: never the production timeout.
			Files: codeindex.FileApplier{ParseTimeout: 5 * time.Minute}}),
		Parse:  parse,
		Holder: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	head := pyOID("commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: head, Upstream: head, Full: true}
	for p, c := range tree {
		sum := sha256.Sum256([]byte(c))
		sha := hex.EncodeToString(sum[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(c)), GitOID: pyOID(c), Mode: "100644", Lang: "python"})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, proj); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, proj)
	if err != nil {
		t.Fatal(err)
	}
	return func(rel codegraph.Relation, path, id string, from bool) map[string]string {
		t.Helper()
		rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: rel})
		if err != nil {
			t.Fatalf("%s of %s#%s: %v", rel, path, id, err)
		}
		out := map[string]string{}
		for _, r := range rows {
			if from {
				out[r.From.ID] = r.Confidence
			} else {
				out[r.To.ID] = r.Confidence
			}
		}
		return out
	}
}

func wantPy(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

// jsonFacts reads the facts a pySrc wrote.
func jsonFacts(p string, content []byte) (codeparse.Facts, error) {
	if !strings.HasSuffix(p, ".py") && !strings.HasSuffix(p, ".pyi") {
		return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
	}
	var f codeparse.Facts
	err := json.Unmarshal(content, &f)
	return f, err
}

// TestResolve_python runs the engine over a Python project of hand-built
// facts: the edges a module's references get in the base view.
func TestResolve_python(t *testing.T) {
	tree := map[string]string{
		"app/__init__.py":     pyFile().imp(".core", "", "*").imp(".models", "", "User").fn("version").src(),
		"app/core.py":         pyFile().fn("run").fn("_private").class("Engine").method("Engine.start").src(),
		"app/models.py":       pyFile().class("User").method("User.save").src(),
		"app/util.py":         pyFile().fn("helper").fn("run").fn("getcwd").src(),
		"app/sub/__init__.py": pyFile().src(),
		"app/sub/sibling.py":  pyFile().fn("f").src(),
		"app/sub/deep.py": pyFile().
			imp("..util", "", "helper").imp(".", "", "sibling").imp("app.models", "m").
			fn("work").
			call("work", "helper").      // from ..util import helper
			call("work", "sibling.f").   // from . import sibling (a submodule)
			call("work", "m.User").      // import app.models as m
			call("work", "m.User.save"). // qualified through the module
			src(),
		"main.py": pyFile().
			imp("app", "").imp("app", "", "User").imp("os", "").
			imp("app.sub.sibling", "").imp("app.models", "U", "User").
			fn("check").class("Admin").method("Admin.promote").method("Admin.check").fn("main").fn("main.inner").
			ref(codeparse.RefExtends, "Admin", "User"). // class Admin(User)
			call("Admin.promote", "self.check").        // a member of the class
			call("Admin.promote", "check").             // the module's function, not the method
			call("main", "app.version").                // app/__init__.py
			call("main", "app.run").                    // app/core.py, star-imported by app
			call("main", "User").                       // re-exported by app from .models
			call("main", "os.getcwd").                  // outside the project
			call("main", "helper").                     // never imported: name fallback
			call("main", "_private").                   // not exported: no fallback
			call("main", "inner").                      // the enclosing function
			call("main", "Engine.start").               // qualified by the class name
			call("main", "nowhere").
			call("main", "app.sub.sibling.f"). // import app.sub.sibling: the dotted path
			call("main", "U").                 // from app.models import User as U
			call("main", "U.save").
			src(),
		"README.md": "# app\n",
	}
	neighbors := pyIndex(t, tree, jsonFacts)
	wantPy(t, "main", neighbors(codegraph.RelCallees, "main.py", "main", false), map[string]string{
		"app/__init__.py#version":  codegraph.ConfidenceExact,
		"app/core.py#run":          codegraph.ConfidenceExact,
		"app/models.py#User":       codegraph.ConfidenceExact,
		"app/util.py#helper":       codegraph.ConfidenceName,
		"main.py#main.inner":       codegraph.ConfidenceExact,
		"app/core.py#Engine.start": codegraph.ConfidenceQualified,
		"app/sub/sibling.py#f":     codegraph.ConfidenceExact,     // app.sub.sibling.f()
		"app/models.py#User.save":  codegraph.ConfidenceQualified, // U.save(): User.save
	})
	wantPy(t, "Admin.promote", neighbors(codegraph.RelCallees, "main.py", "Admin.promote", false), map[string]string{
		"main.py#Admin.check": codegraph.ConfidenceName,
		"main.py#check":       codegraph.ConfidenceExact,
	})
	wantPy(t, "work", neighbors(codegraph.RelCallees, "app/sub/deep.py", "work", false), map[string]string{
		"app/util.py#helper":      codegraph.ConfidenceExact,
		"app/sub/sibling.py#f":    codegraph.ConfidenceExact,
		"app/models.py#User":      codegraph.ConfidenceExact,
		"app/models.py#User.save": codegraph.ConfidenceQualified,
	})
	wantPy(t, "User's subclasses", neighbors(codegraph.RelImplementations, "app/models.py", "User", true), map[string]string{
		"main.py#Admin": codegraph.ConfidenceExact,
	})
}

// TestResolveGoldens_python parses the extractor's Python inputs
// (codeparse/testdata/python) with codeparse.Parse, laid out as a package
// next to a store module the service imports, and checks the edges of
// their references.
func TestResolveGoldens_python(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join("..", "..", "codeparse", "testdata", "python", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tree := map[string]string{
		"shop/__init__.py": "from .service import *\n",
		"shop/service.py":  read("service.py"),
		"shop/models.py":   read("models.py"),
		"shop/stubs.pyi":   read("stubs.pyi"),
		"shop/store.py": `class Base:
    pass


class Registry(type):
    pass


class NotFound(Exception):
    pass


Value = str


def checksum(item):
    return 0
`,
		"main.py": `import shop
from shop.models import make_item


def run():
    shop.sync(shop.ItemRepository(None, ""), [make_item("a")])
`,
	}
	parse := func(p string, content []byte) (codeparse.Facts, error) { return codeparse.Parse(p, content) }
	neighbors := pyIndex(t, tree, parse)
	wantPy(t, "ItemRepository.get", neighbors(codegraph.RelCallees, "shop/service.py", "ItemRepository.get", false), map[string]string{
		"shop/service.py#ServiceError":       codegraph.ConfidenceExact,     // the module's class, not a member of the enclosing class
		"shop/models.py#Item.parse":          codegraph.ConfidenceQualified, // Item from .models
		"shop/service.py#ItemRepository.get": codegraph.ConfidenceName,      // self.session.get: a member, nearest first
	})
	wantPy(t, "encode", neighbors(codegraph.RelCallees, "shop/service.py", "encode", false), map[string]string{
		"shop/store.py#checksum":    codegraph.ConfidenceExact, // from . import store
		"shop/models.py#Sized.size": codegraph.ConfidenceName,  // item.size: a member of an imported module
	})
	wantPy(t, "ItemRepository.put", neighbors(codegraph.RelCallees, "shop/service.py", "ItemRepository.put", false), map[string]string{
		"shop/service.py#encode":             codegraph.ConfidenceExact,
		"shop/service.py#ItemRepository.put": codegraph.ConfidenceName, // self.session.put
	})
	wantPy(t, "store.Base's subclasses", neighbors(codegraph.RelImplementations, "shop/store.py", "Base", true), map[string]string{
		"shop/service.py#ItemRepository": codegraph.ConfidenceExact,
	})
	wantPy(t, "store.NotFound's references", neighbors(codegraph.RelReferences, "shop/store.py", "NotFound", true), map[string]string{
		"shop/service.py#ItemRepository.get": codegraph.ConfidenceExact,
	})
	wantPy(t, "Item's references", neighbors(codegraph.RelReferences, "shop/models.py", "Item", true), map[string]string{
		"shop/models.py#Item.parse":          codegraph.ConfidenceExact, // the module's class from inside its own method
		"shop/models.py#make_item":           codegraph.ConfidenceExact,
		"shop/models.py#load_all":            codegraph.ConfidenceExact,
		"shop/stubs.pyi#Client.get":          codegraph.ConfidenceExact, // from .models import Item in a stub
		"shop/stubs.pyi#Client.get~2":        codegraph.ConfidenceExact,
		"shop/service.py#ItemRepository.get": codegraph.ConfidenceExact,
		"shop/service.py#ItemRepository.put": codegraph.ConfidenceExact,
		"shop/service.py#sync":               codegraph.ConfidenceExact,
	})
	// main: shop's __init__ star-imports .service; make_item from shop.models.
	wantPy(t, "run", neighbors(codegraph.RelCallees, "main.py", "run", false), map[string]string{
		"shop/service.py#sync":           codegraph.ConfidenceExact,
		"shop/service.py#ItemRepository": codegraph.ConfidenceExact,
		"shop/models.py#make_item":       codegraph.ConfidenceExact,
	})
}

func pyOID(label string) string {
	s := sha1.Sum([]byte(label))
	return hex.EncodeToString(s[:])
}
