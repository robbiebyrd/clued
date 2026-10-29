package resolvers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// rustGolden reads the facts the Rust extractor gives for one of its
// golden inputs (codeparse/testdata/rust).
func rustGolden(t *testing.T, name string) codeparse.Facts {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "codeparse", "testdata", "rust", name+".facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f codeparse.Facts
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// rustSrc builds facts the way the Rust extractor writes them, by hand:
// no package, names qualified with "::" by their enclosing definitions.
type rustSrc struct {
	f    codeparse.Facts
	line int
}

func rustFile() *rustSrc { return &rustSrc{f: codeparse.Facts{Lang: "rust"}} }

func (r *rustSrc) pos() codeparse.Pos {
	r.line++
	return codeparse.Pos{Line: r.line, Offset: r.line * 100}
}

// use adds a use form: spec and names as written, alias.
func (r *rustSrc) use(spec, alias string, names ...string) *rustSrc {
	r.f.Imports = append(r.f.Imports, codeparse.Import{Spec: spec, Alias: alias, Names: names, Start: r.pos()})
	return r
}

func (r *rustSrc) imp(kind, spec string) *rustSrc {
	r.f.Imports = append(r.f.Imports, codeparse.Import{Spec: spec, Kind: kind, Start: r.pos()})
	return r
}

// def adds a definition; local is Name or Parent::Name (the parent's id).
func (r *rustSrc) def(kind, local string, exported bool) *rustSrc {
	name, parent := local, ""
	if i := strings.LastIndex(local, "::"); i >= 0 {
		name, parent = local[i+2:], local[:i]
	}
	p := r.pos()
	r.f.Symbols = append(r.f.Symbols, codeparse.Symbol{ID: local, Kind: kind, Name: name, QualName: local,
		Start: p, End: p, Parent: parent, Exported: exported})
	return r
}

// ref adds a reference from enclosing to target (a name with its
// qualifier: geometry::arc, self.x.f, helper).
func (r *rustSrc) ref(kind, enclosing, target string) *rustSrc {
	qual, name := "", target
	if i := strings.LastIndex(target, "::"); i >= 0 {
		qual, name = target[:i], target[i+2:]
	} else if i := strings.LastIndex(target, "."); i >= 0 {
		qual, name = target[:i], target[i+1:]
	}
	r.f.Refs = append(r.f.Refs, codeparse.Ref{Name: name, Qualifier: qual, Kind: kind, Start: r.pos(), Enclosing: enclosing})
	return r
}

func (r *rustSrc) facts() codeparse.Facts { return r.f }

// fakeView is a resolvers.View over files and package keys given by hand.
type fakeView struct {
	content  map[string]string
	packages map[string]bool
}

func (v fakeView) Layer() string     { return "base" }
func (v fakeView) Has(p string) bool { _, ok := v.content[p]; return ok }
func (v fakeView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v.content[p]
	return []byte(c), ok, nil
}
func (v fakeView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilyRust || !v.packages[key] {
		return nil
	}
	return []*resolvers.File{{Path: key, Family: family, Package: key}}
}

func rustResolver(t *testing.T) resolvers.Resolver {
	t.Helper()
	r, ok := resolvers.Lookup(codeparse.FamilyRust)
	if !ok {
		t.Fatal("no resolver registered for rust")
	}
	if fam, _, ok := resolvers.ForLang("rust"); !ok || fam != codeparse.FamilyRust {
		t.Fatalf("ForLang(rust) = %q, %v", fam, ok)
	}
	return r
}

func TestCargoManifest_rust(t *testing.T) {
	m, ok := resolvers.ParseCargoManifest([]byte(`# the manifest
[package]
name = "kv-store"   # with a dash
version = "0.1.0"

[lib]
name = 'kv'
path = "lib/kv.rs"

[[bin]]
name = "kvctl"
path = "tools/ctl/main.rs"

[[ bin ]]
name = "nopath"

[dependencies]
name = "not-the-package"
`))
	want := resolvers.CargoManifest{Name: "kv", Lib: "lib/kv.rs", Bins: map[string]string{"kvctl": "tools/ctl/main.rs"}}
	if !ok || !reflect.DeepEqual(m, want) {
		t.Errorf("manifest = %+v, %v; want %+v", m, ok, want)
	}
	if m, ok := resolvers.ParseCargoManifest([]byte("[package]\nname = \"my-crate\"\n")); !ok || m.Name != "my_crate" {
		t.Errorf("package name = %+v, %v", m, ok)
	}
	if _, ok := resolvers.ParseCargoManifest([]byte("[workspace]\nmembers = [\"a\"]\n")); ok {
		t.Error("a virtual workspace is a package")
	}
}

func TestPackage_rust(t *testing.T) {
	r := rustResolver(t)
	v := fakeView{content: map[string]string{
		"Cargo.toml":       "[workspace]\nmembers = [\"kv\", \"tool\"]\n",
		"kv/Cargo.toml":    "[package]\nname = \"kv-store\"\n",
		"tool/Cargo.toml":  "[package]\nname = \"tool\"\n[lib]\nname = \"toolkit\"\npath = \"lib/toolkit.rs\"\n[[bin]]\nname = \"ctl\"\npath = \"cmd/ctl/main.rs\"\n",
		"bare/src/lib.rs":  "",
		"loose/x.rs":       "",
		"kv/src/a/mod.rs":  "",
		"kv/src/a/b.rs":    "",
		"kv/src/a/b/c.rs":  "",
		"kv/src/main.rs":   "",
		"kv/src/lib.rs":    "",
		"kv/src/bin/x.rs":  "",
		"kv/build.rs":      "",
		"tool/lib/util.rs": "",
	}}
	profile := &codeparse.Profile{Scope: codeparse.Scope{Options: map[string]string{
		"manifest": "Cargo.toml", "crateRoots": "src/lib.rs src/main.rs", "modFile": "mod.rs"}}}
	for p, want := range map[string]string{
		"kv/src/lib.rs":               "kv_store",
		"kv/src/main.rs":              "kv_store",
		"kv/src/store.rs":             "kv_store::store",
		"kv/src/a/mod.rs":             "kv_store::a",
		"kv/src/a/b.rs":               "kv_store::a::b",
		"kv/src/a/b/c.rs":             "kv_store::a::b::c",
		"kv/src/bin/x.rs":             "kv_store/bin/x",
		"kv/src/bin/y/main.rs":        "kv_store/bin/y",
		"kv/src/bin/y/args.rs":        "kv_store/bin/y::args",
		"kv/tests/it.rs":              "kv_store/tests/it",
		"kv/tests/common/mod.rs":      "kv_store/tests/common",
		"kv/examples/demo.rs":         "kv_store/examples/demo",
		"kv/benches/speed.rs":         "kv_store/benches/speed",
		"kv/build.rs":                 "kv_store/build",
		"tool/lib/toolkit.rs":         "toolkit",
		"tool/lib/util.rs":            "toolkit::util",
		"tool/cmd/ctl/main.rs":        "toolkit/bin/ctl",
		"tool/cmd/ctl/flags.rs":       "toolkit/bin/ctl::flags",
		"bare/src/lib.rs":             "bare",
		"bare/src/net/http.rs":        "bare::net::http",
		"src/main.rs":                 "crate",
		"loose/x.rs":                  "crate::loose::x",
		"my-tool/src/deep/inner.rs":   "my_tool::deep::inner",
		"kv/src/a/b/c/d/mod.rs":       "kv_store::a::b::c::d",
		"kv/src/bin/y/sub/mod.rs":     "kv_store/bin/y::sub",
		"kv/examples/multi/main.rs":   "kv_store/examples/multi",
		"kv/examples/multi/helper.rs": "kv_store/examples/multi::helper",
	} {
		for _, prof := range []*codeparse.Profile{nil, profile} {
			f := &resolvers.File{Path: p, Lang: "rust", Family: codeparse.FamilyRust, Facts: &codeparse.Facts{Lang: "rust"}, Profile: prof}
			got, err := r.Package(context.Background(), v, f)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("Package(%s) = %q, want %q", p, got, want)
			}
		}
	}
}

func TestUses_rust(t *testing.T) {
	type use struct {
		Path  string
		Alias string
		Glob  bool
	}
	var got []use
	for _, im := range rustGolden(t, "imports.rs").Imports {
		if im.Kind == "mod" || im.Kind == "extern_crate" {
			continue
		}
		for _, u := range resolvers.RustUses(im) {
			got = append(got, use{strings.Join(u.Path, "::"), u.Alias, u.Glob})
		}
	}
	want := []use{
		{"std::io", "", false},
		{"std::collections::HashMap", "", false},
		{"std::sync::Arc", "", false},
		{"std::sync::Mutex", "", false},
		{"std::sync::mpsc::Sender", "Tx", false},
		{"std::fmt", "", false},
		{"std::fmt::Display", "", false},
		{"crate::config::Config", "Settings", false},
		{"super", "", true},
		{"self::parser::ast::Expr", "", false},
		{"self::parser::ast::Stmt", "", false},
		{"self::parser::lexer", "", true},
		{"api::Client", "", false},
		{"::serde::Deserialize", "", false},
		{"tokio", "", false},
		{"log", "logging", false},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("uses:\n got %v\nwant %v", got, want)
	}
	// Forms the goldens do not hold: a whole tree in the spec, nested
	// braces with self and aliases, a glob spec.
	var tree []use
	for _, im := range []codeparse.Import{
		{Spec: "a::{b::{self as bb, c}, d as e, f::*}"},
		{Spec: "x::y::*", Kind: "glob"},
		{Spec: "p::q", Names: []string{"self"}},
	} {
		for _, u := range resolvers.RustUses(im) {
			tree = append(tree, use{strings.Join(u.Path, "::"), u.Alias, u.Glob})
		}
	}
	wantTree := []use{{"a::b", "bb", false}, {"a::b::c", "", false}, {"a::d", "e", false}, {"a::f", "", true},
		{"x::y", "", true}, {"p::q", "", false}}
	if !reflect.DeepEqual(tree, wantTree) {
		t.Errorf("trees:\n got %v\nwant %v", tree, wantTree)
	}
}

func TestScope_rust(t *testing.T) {
	r := rustResolver(t)
	facts := rustGolden(t, "imports.rs")
	v := fakeView{packages: map[string]bool{
		"kv": true, "kv::imports": true, "kv::imports::parser": true, "kv::imports::api": true,
		"kv::imports::parser::lexer": true, "serde_json": true,
	}}
	f := &resolvers.File{Path: "src/imports.rs", Lang: "rust", Family: codeparse.FamilyRust, Package: "kv::imports", Facts: &facts}
	s, err := r.Scope(context.Background(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	wantQ := map[string][]string{
		"alloc":       {"alloc"},
		"json":        {"serde_json"},
		"parser":      {"kv::imports::parser"},
		"api":         {"kv::imports::api"},
		"io":          {"std::io"},
		"HashMap":     {"std::collections::HashMap"},
		"Arc":         {"std::sync::Arc"},
		"Mutex":       {"std::sync::Mutex"},
		"Tx":          {"std::sync::mpsc::Sender"},
		"fmt":         {"std::fmt"},
		"Display":     {"std::fmt::Display"},
		"Deserialize": {"serde::Deserialize"},
		"tokio":       {"tokio"},
		"logging":     {"log"},
	}
	wantN := map[string][]string{
		"io": {"std"}, "HashMap": {"std::collections"}, "Arc": {"std::sync"}, "Mutex": {"std::sync"},
		"fmt": {"std"}, "Display": {"std::fmt"},
		"Expr": {"kv::imports::parser::ast"}, "Stmt": {"kv::imports::parser::ast"},
		"Client":      {"kv::imports::api"},
		"Deserialize": {"serde"},
	}
	wantO := []string{"kv", "kv::imports::parser::lexer"}
	if !reflect.DeepEqual(s.Qualifiers, wantQ) {
		t.Errorf("qualifiers:\n got %v\nwant %v", s.Qualifiers, wantQ)
	}
	if !reflect.DeepEqual(s.Names, wantN) {
		t.Errorf("names:\n got %v\nwant %v", s.Names, wantN)
	}
	if !reflect.DeepEqual(s.Open, wantO) {
		t.Errorf("open: got %v, want %v", s.Open, wantO)
	}

	// use super::* inside an inline module opens the file's module; the
	// crate:: and super:: roots and a crate named by a reference are bound.
	gen := rustGolden(t, "generated.rs")
	gen.Refs = append(gen.Refs,
		codeparse.Ref{Name: "f", Qualifier: "crate", Kind: codeparse.RefCall},
		codeparse.Ref{Name: "g", Qualifier: "super", Kind: codeparse.RefCall},
		codeparse.Ref{Name: "h", Qualifier: "serde_json::de", Kind: codeparse.RefCall},
		codeparse.Ref{Name: "i", Qualifier: "self", Kind: codeparse.RefCall},
		codeparse.Ref{Name: "j", Qualifier: "crate::imports::parser::Ast", Kind: codeparse.RefCall}, // through file modules
		codeparse.Ref{Name: "k", Qualifier: "::kv::imports::api", Kind: codeparse.RefCall})          // from the root
	gen.Imports = append(gen.Imports, codeparse.Import{Spec: "crate::imports::api::Client", Alias: "C"}) // renamed
	g := &resolvers.File{Path: "src/a/generated.rs", Lang: "rust", Family: codeparse.FamilyRust, Package: "kv::a::generated", Facts: &gen}
	s, err = r.Scope(context.Background(), v, g)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"kv::a::generated"}; !reflect.DeepEqual(s.Open, want) {
		t.Errorf("generated open = %v, want %v", s.Open, want)
	}
	if want := map[string][]string{"deep": {"kv::a::generated::outer::inner"}}; !reflect.DeepEqual(s.Names, want) {
		t.Errorf("generated names = %v, want %v", s.Names, want)
	}
	if want := map[string][]string{"crate": {"kv"}, "super": {"kv::a"}, "serde_json": {"serde_json"},
		"crate::imports::parser": {"kv::imports::parser"}, "kv::imports::api": {"kv::imports::api"}}; !reflect.DeepEqual(s.Qualifiers, want) {
		t.Errorf("generated qualifiers = %v, want %v", s.Qualifiers, want)
	}
	if want := map[string][]resolvers.Original{"C": {{Package: "kv::imports::api", Name: "Client"}}}; !reflect.DeepEqual(s.Renamed, want) {
		t.Errorf("generated renamed = %v, want %v", s.Renamed, want)
	}
}

func TestVisible_rust(t *testing.T) {
	r := rustResolver(t)
	own := &resolvers.File{Path: "src/a.rs", Package: "kv::a"}
	sameCrate := &resolvers.File{Path: "src/b.rs", Package: "kv::b"}
	bin := &resolvers.File{Path: "src/bin/x.rs", Package: "kv/bin/x"}
	private := &codeparse.Symbol{Kind: codeparse.KindFunction, Name: "f"}
	public := &codeparse.Symbol{Kind: codeparse.KindFunction, Name: "g", Exported: true}
	impl := &codeparse.Symbol{Kind: codeparse.KindImpl, Name: "T"}
	for _, c := range []struct {
		from *resolvers.File
		sym  *codeparse.Symbol
		want bool
	}{
		{own, private, true}, {bin, private, false}, {bin, public, true}, {sameCrate, private, true}, {own, impl, false},
	} {
		if got := r.Visible(c.from, resolvers.Candidate{File: sameCrate, Symbol: c.sym}, resolvers.StepImport); got != c.want {
			t.Errorf("Visible(%s → %s) = %v, want %v", c.from.Package, c.sym.Name, got, c.want)
		}
	}
	if rules := r.Rules(); !reflect.DeepEqual(rules.Separators, []string{"::"}) || !rules.NameFallback || !rules.MemberFallback {
		t.Errorf("rules = %+v", rules)
	}
}

// TestResolve_rust indexes a crate through the engine (codeindex over the
// memory stores) with the goldens' facts and hand-built ones, and checks
// the edges.
func TestResolve_rust(t *testing.T) {
	const project = "u0123456789abcdef__github.com__o__rust"
	ctx := context.Background()
	store, graphs := memcode.New(), memgraph.New()
	parse := func(p string, content []byte) (codeparse.Facts, error) {
		if !strings.HasSuffix(p, ".rs") {
			return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
		}
		var f codeparse.Facts
		err := json.Unmarshal(content, &f)
		return f, err
	}
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply: codeindex.NewResolver(codeindex.ResolverOptions{
			// Goldens parse under -race on slow runners: never the production timeout.
			Files: codeindex.FileApplier{ParseTimeout: 5 * time.Minute}}),
		Parse:  parse,
		Holder: "test",
	})
	if err != nil {
		t.Fatal(err)
	}

	enc := func(f codeparse.Facts) string {
		f.Chunks = nil
		b, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	shapes := rustFile().
		def(codeparse.KindTrait, "Shape", true).def(codeparse.KindMethod, "Shape::paint", true).
		def(codeparse.KindStruct, "Circle", true).
		def(codeparse.KindImpl, "Circle", false).def(codeparse.KindMethod, "Circle::center", true).
		def(codeparse.KindFunction, "area", true).facts()
	// The impl's id differs from the struct's, as the extractor numbers it.
	shapes.Symbols[3].ID = "Circle~2"
	shapes.Symbols[4].Parent = "Circle~2"
	storage := rustFile().
		def(codeparse.KindFunction, "reserve", true).
		ref(codeparse.RefValue, "reserve", "super::MAX_ENTRIES"). // the parent module
		ref(codeparse.RefType, "reserve", "crate::Store").        // the crate root
		ref(codeparse.RefCall, "reserve", "clamp").               // an inline module's item, by name
		facts()
	errs := rustFile().def(codeparse.KindStruct, "Error", true).facts()
	tool := rustFile().
		use("kv_store", "", "Store").use("kv_store::storage", "", "*").use("kv_store", "sh", "shapes").
		def(codeparse.KindFunction, "main", false).
		ref(codeparse.RefType, "main", "Store").             // an imported item
		ref(codeparse.RefCall, "main", "Store::new").        // its method, by qualified name
		ref(codeparse.RefCall, "main", "kv_store::open").    // the crate by name
		ref(codeparse.RefValue, "main", "kv_store::OPENED"). // private to the crate
		ref(codeparse.RefCall, "main", "reserve").           // a glob import
		ref(codeparse.RefCall, "main", "sh::area").          // a module alias
		use("kv_store::shapes::Circle", "Round").use("kv_store::shapes::area", "surface").
		ref(codeparse.RefCall, "main", "surface").                // use …::area as surface
		ref(codeparse.RefCall, "main", "Round::center").          // use …::Circle as Round
		ref(codeparse.RefCall, "main", "kv_store::shapes::area"). // a path through a file module
		facts()
	tree := map[string]string{
		"Cargo.toml":        "[package]\nname = \"kv-store\"\nversion = \"0.1.0\"\n",
		"src/lib.rs":        enc(rustGolden(t, "lib.rs")),
		"src/references.rs": enc(rustGolden(t, "references.rs")),
		"src/shapes.rs":     enc(shapes),
		"src/storage.rs":    enc(storage),
		"src/error.rs":      enc(errs),
		"src/bin/tool.rs":   enc(tool),
		"README.md":         "# kv\n",
	}
	sha := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("c", 40)
	ch := code.Change{Worktree: wt, Ref: "main", Head: commit, Upstream: commit, Full: true}
	for p, c := range tree {
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha(c), Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		lang := "rust"
		if !strings.HasSuffix(p, ".rs") {
			lang = ""
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha(c), Size: int64(len(c)), GitOID: sha(c)[:40], Mode: "100644", Lang: lang})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, project); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	// neighbors returns the other end of p#id's rel edges, with their
	// confidence (callees: the targets; references, implementations: the
	// sources).
	neighbors := func(rel codegraph.Relation, p, id string) map[string]string {
		t.Helper()
		rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, id), Relation: rel})
		if err != nil {
			t.Fatalf("%s of %s#%s: %v", rel, p, id, err)
		}
		got := map[string]string{}
		for _, r := range rows {
			other := r.To.ID
			if rel != codegraph.RelCallees {
				other = r.From.ID
			}
			got[other] = r.Confidence
		}
		return got
	}
	check := func(what string, got, want map[string]string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", what, got, want)
		}
	}
	const (
		lib, refs, shp = "src/lib.rs", "src/references.rs", "src/shapes.rs"
		sto, errf, bin = "src/storage.rs", "src/error.rs", "src/bin/tool.rs"
	)
	n := codeindex.SymbolNodeID

	// lib.rs: `mod storage;` binds storage, `pub mod error;` error, the
	// inline module util is reached by its qualified name, Store::new by
	// the impl method's (the impl blocks named Store are no targets).
	// Qualified names are matched whole (AbsoluteQualifiers).
	check("open calls", neighbors(codegraph.RelCallees, lib, "open"), map[string]string{
		n(sto, "reserve"):     codegraph.ConfidenceExact,
		n(lib, "util::clamp"): codegraph.ConfidenceExact,
		n(lib, "Store::new"):  codegraph.ConfidenceExact,
	})
	check("Error referrers", neighbors(codegraph.RelReferences, errf, "Error"), map[string]string{n(lib, "open"): codegraph.ConfidenceExact})
	check("Store referrers", neighbors(codegraph.RelReferences, lib, "Store"), map[string]string{
		n(lib, "open"): codegraph.ConfidenceExact, n(lib, "Store::new"): codegraph.ConfidenceExact,
		n(sto, "reserve"): codegraph.ConfidenceExact, // crate::Store
		n(bin, "main"):    codegraph.ConfidenceExact, // use kv_store::Store
	})
	// impl Storable for u64 implements the trait; fmt::Display (std) and
	// HashMap stay unresolved.
	check("Storable implementations", neighbors(codegraph.RelImplementations, lib, "Storable"), map[string]string{n(lib, "u64"): codegraph.ConfidenceExact})

	// references.rs: use crate::shapes::{Circle, Shape} binds both names to
	// the shapes module; impl Draw for Circle implements the file's Draw.
	check("Shape implementations", neighbors(codegraph.RelImplementations, shp, "Shape"), map[string]string{n(refs, "Draw"): codegraph.ConfidenceExact})
	check("Draw implementations", neighbors(codegraph.RelImplementations, refs, "Draw"), map[string]string{n(refs, "Circle"): codegraph.ConfidenceExact})
	check("Circle referrers", neighbors(codegraph.RelReferences, shp, "Circle"), map[string]string{n(refs, "Circle::draw"): codegraph.ConfidenceExact})
	check("Circle::draw calls", neighbors(codegraph.RelCallees, refs, "Circle::draw"), map[string]string{
		n(refs, "render"):        codegraph.ConfidenceExact,
		n(shp, "Circle::center"): codegraph.ConfidenceName, // self.center(): a member of an imported module
	})
	check("render calls", neighbors(codegraph.RelCallees, refs, "render"), map[string]string{n(shp, "Shape::paint"): codegraph.ConfidenceName})
	check("Canvas referrers", neighbors(codegraph.RelReferences, refs, "Canvas"), map[string]string{
		n(refs, "Draw::draw"): codegraph.ConfidenceExact, n(refs, "Circle::draw"): codegraph.ConfidenceExact,
		n(refs, "render"): codegraph.ConfidenceExact,
	})

	// storage.rs: super:: and crate:: reach the crate root; a bare name of
	// an inline module's item falls back to its name.
	check("reserve calls", neighbors(codegraph.RelCallees, sto, "reserve"), map[string]string{n(lib, "util::clamp"): codegraph.ConfidenceName})
	check("MAX_ENTRIES referrers", neighbors(codegraph.RelReferences, lib, "MAX_ENTRIES"), map[string]string{n(sto, "reserve"): codegraph.ConfidenceExact})

	// The binary is a crate of its own: the library by name, its pub items
	// only (OPENED is private), a glob import, a module alias, renamed
	// items (area as surface; Circle as Round, whose method is found by
	// the original's qualified name), a path through a file module.
	check("main calls", neighbors(codegraph.RelCallees, bin, "main"), map[string]string{
		n(lib, "Store::new"):     codegraph.ConfidenceExact,
		n(lib, "open"):           codegraph.ConfidenceExact,
		n(sto, "reserve"):        codegraph.ConfidenceExact,
		n(shp, "area"):           codegraph.ConfidenceExact,
		n(shp, "Circle::center"): codegraph.ConfidenceQualified,
	})
	check("OPENED referrers", neighbors(codegraph.RelReferences, lib, "OPENED"), map[string]string{})
}
