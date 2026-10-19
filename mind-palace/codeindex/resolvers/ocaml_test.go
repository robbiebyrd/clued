package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// ocamlGolden reads an input of the OCaml extraction goldens.
func ocamlGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../codeparse/testdata/ocaml/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ocamlView is a resolvers.View over a tree of files: OCaml files are
// parsed with codeparse.Parse and keyed with the OCaml resolver, as the
// engine does.
type ocamlView struct {
	tree  map[string]string
	files map[string]*resolvers.File
	pkgs  map[string][]*resolvers.File
}

func newOCamlView(t *testing.T, tree map[string]string) *ocamlView {
	t.Helper()
	ctx := context.Background()
	eng, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	v := &ocamlView{tree: tree, files: map[string]*resolvers.File{}, pkgs: map[string][]*resolvers.File{}}
	res, ok := resolvers.Lookup(codeparse.FamilyOCaml)
	if !ok {
		t.Fatal("no ocaml resolver")
	}
	var paths []string
	for p := range tree {
		if ext := path.Ext(p); ext == ".ml" || ext == ".mli" {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		facts, err := codeparse.Parse(p, []byte(tree[p]))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		v.files[p] = &resolvers.File{Path: p, Layer: codegraph.BaseLayerID, Lang: facts.Lang,
			Family: codeparse.FamilyOCaml, Facts: &facts, Profile: eng.Profiles()["ocaml"]}
	}
	for _, p := range paths {
		f := v.files[p]
		if f.Package, err = res.Package(ctx, v, f); err != nil {
			t.Fatalf("package of %s: %v", p, err)
		}
	}
	for _, p := range paths {
		f := v.files[p]
		v.pkgs[f.Package] = append(v.pkgs[f.Package], f)
	}
	return v
}

func (v *ocamlView) Layer() string     { return codegraph.BaseLayerID }
func (v *ocamlView) Has(p string) bool { _, ok := v.tree[p]; return ok }
func (v *ocamlView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v.tree[p]
	return []byte(c), ok, nil
}
func (v *ocamlView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilyOCaml {
		return nil
	}
	return v.pkgs[key]
}

func (v *ocamlView) keys() map[string]string {
	out := map[string]string{}
	for p, f := range v.files {
		out[p] = f.Package
	}
	return out
}

func TestRegistered_ocaml(t *testing.T) {
	family, res, ok := resolvers.ForLang("ocaml")
	if !ok || family != codeparse.FamilyOCaml {
		t.Fatalf("ForLang(ocaml) = %q, %v", family, ok)
	}
	if r := res.Rules(); !reflect.DeepEqual(r.Separators, []string{"."}) || r.NameFallback || !r.MemberFallback || r.CaseInsensitive {
		t.Errorf("rules = %+v", r)
	}
	for in, want := range map[string]string{"lib/shapes.ml": "Shapes", "store_intf.mli": "Store_intf", "Main.ml": "Main", "a/_x.ml": "_x"} {
		if got := resolvers.OCamlModuleName(in); got != want {
			t.Errorf("OCamlModuleName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPackageKeys_ocaml(t *testing.T) {
	const m = "let x = 1\n"
	v := newOCamlView(t, map[string]string{
		// A wrapped library; the .mli beside its .ml is the interface.
		"lib/dune":       "; geometry\n(library\n (name geo) ; the name\n (libraries yojson))\n#| (library (name other)) |#\n",
		"lib/shapes.ml":  ocamlGolden(t, "shapes.ml"),
		"lib/shapes.mli": ocamlGolden(t, "shapes.mli"),
		"lib/units.mli":  "val cm : float -> float\n",
		// include_subdirs: the library covers its subdirectories.
		"core/dune":            "(include_subdirs unqualified)\n(library (name core_lib))\n",
		"core/util/strings.ml": m,
		// Two libraries in one directory, split by (modules ...).
		"two/dune":     "(library (name a_lib) (modules alpha))\n(library (name \"b_lib\") (wrapped false) (modules :standard \\ alpha))\n",
		"two/alpha.ml": m,
		"two/beta.ml":  m,
		// An executable: modules keyed by directory.
		"bin/dune":    "(executable (name main) (libraries geo))\n",
		"bin/main.ml": m,
		// Below an executable's dune without include_subdirs: no library.
		"lib/sub/dune":    "(rule (targets x) (action (run touch x)))\n",
		"lib/sub/deep.ml": m,
		// No dune at all.
		"widgets.ml":   ocamlGolden(t, "widgets.ml"),
		"tools/gen.ml": m,
	})
	want := map[string]string{
		"lib/shapes.ml":        "Geo.Shapes",
		"lib/shapes.mli":       "Geo.Shapes" + resolvers.OCamlInterfaceSuffix,
		"lib/units.mli":        "Geo.Units",
		"core/util/strings.ml": "Core_lib.Strings",
		"two/alpha.ml":         "A_lib.Alpha",
		"two/beta.ml":          "Beta",
		"bin/main.ml":          "bin/Main",
		"lib/sub/deep.ml":      "lib/sub/Deep",
		"widgets.ml":           "Widgets",
		"tools/gen.ml":         "tools/Gen",
	}
	if got := v.keys(); !reflect.DeepEqual(got, want) {
		t.Errorf("package keys:\n got %v\nwant %v", got, want)
	}
}

func TestPackageProfileOptions_ocaml(t *testing.T) {
	ctx := context.Background()
	res, _ := resolvers.Lookup(codeparse.FamilyOCaml)
	prof := &codeparse.Profile{Scope: codeparse.Scope{PackageBy: codeparse.PackageByFile, Options: map[string]string{
		"interfaceExtension": ".rei", "implementationExtension": ".re", "libraryFile": "BUILD"}}}
	v := &ocamlView{tree: map[string]string{"x/BUILD": "(library (name lib))", "x/a.re": "", "x/a.rei": ""}}
	for p, want := range map[string]string{"x/a.re": "Lib.A", "x/a.rei": "Lib.A" + resolvers.OCamlInterfaceSuffix} {
		got, err := res.Package(ctx, v, &resolvers.File{Path: p, Facts: &codeparse.Facts{}, Profile: prof})
		if err != nil || got != want {
			t.Errorf("Package(%s) = %q, %v; want %q", p, got, err, want)
		}
	}
}

func TestScope_ocaml(t *testing.T) {
	ctx := context.Background()
	res, _ := resolvers.Lookup(codeparse.FamilyOCaml)
	v := newOCamlView(t, map[string]string{
		"lib/dune":           "(library (name geo))\n",
		"lib/store.ml":       ocamlGolden(t, "store.ml"),
		"lib/store_intf.ml":  "module type S = sig type key end\n",
		"lib/store_error.ml": "exception Missing of string\n",
		"lib/config.ml":      "module Nested = struct let v = 1 end\ntype t = { host : string }\n",
		"bin/dune":           "(executables (names main))\n",
		"bin/main.ml":        "open Geo\nopen Geo.Config\nlet run m = Store.keys m + Nested.v + Hashtbl.length m + Helper.f ()\n",
		"bin/helper.ml":      "let f () = 1\n",
	})
	sc, err := res.Scope(ctx, v, v.files["lib/store.ml"])
	if err != nil {
		t.Fatal(err)
	}
	wantQ := map[string][]string{
		"Map": {"Map"}, "SMap": {"Map"}, "L": {"List"}, "Json": {"Yojson"}, "Yojson": {"Yojson"},
		"Option": {"Option"}, "Hashtbl": {"Hashtbl"},
		"Store_error": {"Geo.Store_error"}, "Config": {"Geo.Config"},
	}
	if !reflect.DeepEqual(sc.Qualifiers, wantQ) {
		t.Errorf("store.ml qualifiers:\n got %v\nwant %v", sc.Qualifiers, wantQ)
	}
	if want := []string{"Geo.Store_intf"}; !reflect.DeepEqual(sc.Open, want) {
		t.Errorf("store.ml opens = %v, want %v", sc.Open, want)
	}

	sc, err = res.Scope(ctx, v, v.files["bin/main.ml"])
	if err != nil {
		t.Fatal(err)
	}
	// Nested is defined by the opened Geo.Config: left to the engine.
	wantQ = map[string][]string{"Store": {"Geo.Store"}, "Hashtbl": {"Hashtbl"}, "Helper": {"bin/Helper"}}
	if !reflect.DeepEqual(sc.Qualifiers, wantQ) {
		t.Errorf("main.ml qualifiers:\n got %v\nwant %v", sc.Qualifiers, wantQ)
	}
	if want := []string{"Geo.Config"}; !reflect.DeepEqual(sc.Open, want) {
		t.Errorf("main.ml opens = %v, want %v", sc.Open, want)
	}
}

func TestVisible_ocaml(t *testing.T) {
	res, _ := resolvers.Lookup(codeparse.FamilyOCaml)
	ml := &resolvers.File{Path: "shapes.ml", Package: "Shapes"}
	mli := &resolvers.File{Path: "shapes.mli", Package: "Shapes" + resolvers.OCamlInterfaceSuffix}
	alone := &resolvers.File{Path: "units.mli", Package: "Units"}
	other := &resolvers.File{Path: "main.ml", Package: "Main"}
	sym := &codeparse.Symbol{Name: "area", Exported: true}
	for _, c := range []struct {
		from, to *resolvers.File
		want     bool
	}{
		{other, ml, true}, {other, mli, false}, {mli, mli, true}, {ml, mli, false}, {other, alone, true},
	} {
		if got := res.Visible(c.from, resolvers.Candidate{File: c.to, Symbol: sym}, resolvers.StepImport); got != c.want {
			t.Errorf("Visible(%s → %s) = %v, want %v", c.from.Path, c.to.Path, got, c.want)
		}
	}
}

// TestResolve_ocaml indexes a small dune project through codeindex with
// the real parser and checks the edges the engine writes with the OCaml
// rules.
func TestResolve_ocaml(t *testing.T) {
	ctx := context.Background()
	tree := map[string]string{
		"lib/dune":           "(library (name geo) (libraries yojson))\n",
		"lib/shapes.ml":      ocamlGolden(t, "shapes.ml"),
		"lib/shapes.mli":     ocamlGolden(t, "shapes.mli"),
		"lib/units.mli":      "val cm : float -> float\n",
		"lib/store.ml":       ocamlGolden(t, "store.ml"),
		"lib/store_intf.ml":  "module type S = sig\n  type key\n  type t\n  val find : t -> key -> string option\nend\n",
		"lib/store_error.ml": "exception Missing of string\n",
		"lib/config.ml":      "type t = { host : string }\n",
		"bin/dune":           "(executable (name main) (libraries geo))\n",
		"bin/helper.ml":      "let greet name = \"hello \" ^ name\n",
		"bin/main.ml": "open Geo\n\n" +
			"let run () =\n  let c = Units.cm 2.0 in\n  Helper.greet (Shapes.describe (Shapes.Circle ({ x = c; y = 0. }, 1.)))\n\n" +
			"let area_of (s : Shapes.shape) = Shapes.area s\n\n" +
			"let first m = Store.keys m |> List.hd\n\n" +
			"let len l = List.length l\n",
		"widgets.ml": ocamlGolden(t, "widgets.ml"),
	}
	const project = "u0123456789abcdef__ocaml-resolve"
	store, graphs := memcode.New(), memgraph.New()
	eng, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{Profiles: eng.Profiles()}),
		Parse:    codeparse.Parse, ParseVersion: codeparse.ParseVersion,
		Holder: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	const wt = "wt_00000000000000a1"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	head := hexSum(sha1.New(), "commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: head, Upstream: head, Full: true}
	for p, c := range tree {
		sha := hexSum(sha256.New(), c)
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(c)),
			GitOID: hexSum(sha1.New(), c), Mode: "100644", Lang: codeparse.Detect(p, nil, nil).Lang})
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
	t.Cleanup(func() { _ = g.Drop(context.Background()) })

	neighbors := func(rel codegraph.Relation, node string, out bool) map[string]string {
		t.Helper()
		rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: node, Relation: rel})
		if err != nil {
			t.Fatalf("%s of %s: %v", rel, node, err)
		}
		got := map[string]string{}
		for _, r := range rows {
			if out {
				got[r.To.ID] = r.Confidence
			} else {
				got[r.From.ID] = r.Confidence
			}
		}
		return got
	}
	want := func(what string, got, want map[string]string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", what, got, want)
		}
	}

	// Module.name through the opened wrapped library, the directory and an
	// interface without implementation; the .ml beside an .mli is the
	// target; List is outside the project.
	want("callees of main.run", neighbors(codegraph.RelCallees, "bin/main.ml#run", true), map[string]string{
		"lib/units.mli#cm":       "exact",
		"bin/helper.ml#greet":    "exact",
		"lib/shapes.ml#describe": "exact",
	})
	want("callees of main.area_of", neighbors(codegraph.RelCallees, "bin/main.ml#area_of", true),
		map[string]string{"lib/shapes.ml#area": "exact"})
	want("callees of main.first", neighbors(codegraph.RelCallees, "bin/main.ml#first", true),
		map[string]string{"lib/store.ml#keys": "exact"})
	want("callees of main.len", neighbors(codegraph.RelCallees, "bin/main.ml#len", true), map[string]string{})
	want("references to shapes.ml shape", neighbors(codegraph.RelReferences, "lib/shapes.ml#shape", false),
		map[string]string{"bin/main.ml#area_of": "exact"})
	// The interface's own references stay in the interface.
	if got := neighbors(codegraph.RelReferences, "lib/shapes.mli#shape", false); got["lib/shapes.mli#area"] != "exact" || got["bin/main.ml#area_of"] != "" {
		t.Errorf("references to shapes.mli shape = %v", got)
	}

	// store.ml: include, an exception of another module, module aliases
	// and functor applications of modules outside the project, a functor's
	// parameter, a local module.
	want("implementations of Store_intf.S", neighbors(codegraph.RelImplementations, "lib/store_intf.ml#S", false),
		map[string]string{"lib/store.ml#Make": "exact"})
	want("references to Store_error.Missing", neighbors(codegraph.RelReferences, "lib/store_error.ml#Missing", false),
		map[string]string{"lib/store.ml#lookup": "exact"})
	want("callees of store.lookup", neighbors(codegraph.RelCallees, "lib/store.ml#lookup", true),
		map[string]string{"lib/store.ml#Make.find": "name"})
	want("callees of store.to_json", neighbors(codegraph.RelCallees, "lib/store.ml#to_json", true), map[string]string{})
	want("callees of store.count_keys", neighbors(codegraph.RelCallees, "lib/store.ml#count_keys", true),
		map[string]string{"lib/store.ml#keys": "exact"})
	want("callees of store.Make.find", neighbors(codegraph.RelCallees, "lib/store.ml#Make.find", true),
		map[string]string{"lib/store.ml#BACKEND.get": "name"})
	want("callees of store.Memory.connect", neighbors(codegraph.RelCallees, "lib/store.ml#Memory.connect", true), map[string]string{})
	want("references to Config.t.host", neighbors(codegraph.RelReferences, "lib/config.ml#t.host", false),
		map[string]string{"lib/store.ml#host": "name"})

	// widgets.ml (no dune): nested modules by qualified name, classes.
	want("callees of Nested.ask_twice", neighbors(codegraph.RelCallees, "widgets.ml#Nested.ask_twice", true),
		map[string]string{"widgets.ml#Nested.Deeper.ask": "qualified"})
	want("callees of handler", neighbors(codegraph.RelCallees, "widgets.ml#handler", true), map[string]string{})
	if got := neighbors(codegraph.RelImplementations, "widgets.ml#base", false); !slices.Contains(keysOf(got), "widgets.ml#button") {
		t.Errorf("extends of base = %v", got)
	}
}

func keysOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hexSum(h interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
}, s string) string {
	_, _ = h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}
