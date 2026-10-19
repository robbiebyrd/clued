// Package codegraphtest is the conformance suite every codegraph adapter
// must pass. It pins the behaviour the port documents: what ApplyFile
// replaces and keeps, hiddenBy and tombstones across the base, branch and
// overlay layers, per-layer resolved edges, dropping a layer or the graph,
// and every typed read under each view, with their orders, limits and
// errors.
package codegraphtest

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	cg "github.com/robbiebyrd/clued/mind-palace/codegraph"
)

// Factory returns an Opener for one test. The suite opens graphs under keys
// of its own, random enough that suites sharing a backend do not collide,
// drops each graph when the test ends and then closes the Opener.
type Factory func(t *testing.T) cg.Opener

// Layer ids used throughout: the base, a branch layer on it, and two
// sessions' overlays on that branch.
var (
	base    = cg.BaseLayerID
	branch  = cg.BranchLayerID("feature", "abc123")
	overlay = cg.WorktreeLayerID("wt_12", "feature")
	other   = cg.WorktreeLayerID("wt_99", "feature")

	vBase    = cg.View{base}
	vBranch  = cg.View{base, branch}
	vOverlay = cg.View{base, branch, overlay}
	vOther   = cg.View{base, branch, other}
)

// Run executes the suite.
func Run(t *testing.T, newOpener Factory) {
	tests := []struct {
		name string
		fn   func(t *testing.T, g cg.Graph)
	}{
		{"Layers", testLayers},
		{"ApplyFileAndFiles", testApplyFileAndFiles},
		{"ApplyFileReplaces", testApplyFileReplaces},
		{"ApplyFileKeepsHiddenBy", testApplyFileKeepsHiddenBy},
		{"ApplyFileKeepsEdgesOfKeptSymbols", testApplyFileKeepsEdges},
		{"RemoveFile", testRemoveFile},
		{"HiddenByAcrossThreeLayers", testHiddenByThreeLayers},
		{"Tombstones", testTombstones},
		{"PerLayerEdges", testPerLayerEdges},
		{"PutEdges", testPutEdges},
		{"DropLayer", testDropLayer},
		{"Drop", testDrop},
		{"FindSymbols", testFindSymbols},
		{"Symbol", testSymbol},
		{"NeighborsSymbols", testNeighborsSymbols},
		{"NeighborsDepth", testNeighborsDepth},
		{"NeighborsImports", testNeighborsImports},
		{"Outline", testOutline},
		{"SearchText", testSearchText},
		{"ReadsValidate", testReadsValidate},
		{"ConcurrentWrites", testConcurrentWrites},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, g := open(t, newOpener)
			tc.fn(t, g)
		})
	}
	t.Run("Opener", func(t *testing.T) { testOpener(t, newOpener) })
}

var keySafe = regexp.MustCompile(`[^A-Za-z0-9_.\-]`)

// newKey returns a fresh graph key shaped like a namespace.
func newKey(t *testing.T) string {
	name := keySafe.ReplaceAllString(t.Name(), "-")
	key := fmt.Sprintf("u%016x__cgt-%s", rand.Uint64(), name)
	if len(key) > 200 {
		key = key[:200]
	}
	return key
}

// open opens a fresh graph and arranges its drop and the Opener's close.
func open(t *testing.T, newOpener Factory) (cg.Opener, cg.Graph) {
	t.Helper()
	o := newOpener(t)
	t.Cleanup(func() {
		if err := o.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	g, err := o.Open(context.Background(), newKey(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := g.Drop(context.Background()); err != nil {
			t.Errorf("Drop: %v", err)
		}
	})
	return o, g
}

// ---- fixtures ----

func layerOf(id string) cg.Layer {
	p, err := cg.ParseLayerID(id)
	if err != nil {
		panic(err)
	}
	l := cg.Layer{ID: id, Kind: p.Kind, Ref: p.Ref, Commit: p.Commit, Updated: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	switch p.Kind {
	case cg.LayerBranch:
		l.Parent = base
	case cg.LayerWorktree:
		l.Parent = branch
		l.Owner = "0123456789abcdef"
	}
	return l
}

func putLayers(t *testing.T, g cg.Graph, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := g.PutLayer(context.Background(), layerOf(id)); err != nil {
			t.Fatalf("PutLayer %s: %v", id, err)
		}
	}
}

// sym is a symbol; ApplyFile's helper fills its layer and path.
func sym(id, kind, name, qual string, start, end int) cg.Symbol {
	return cg.Symbol{ID: id, Kind: kind, Name: name, QualName: qual, Start: start, End: end,
		Signature: "func " + name + "()", Exported: true}
}

func child(s cg.Symbol, parent string) cg.Symbol { s.Parent = parent; return s }

// facts builds a file's facts, stamping layer and path on every node.
func facts(layer, path, lang, pkg string, syms ...cg.Symbol) cg.FileFacts {
	ff := cg.FileFacts{File: cg.File{Layer: layer, Path: path, Lang: lang, Package: pkg,
		SHA256: "sha-" + path, GitOID: "oid-" + path}}
	for _, s := range syms {
		s.Layer, s.Path = layer, path
		ff.Symbols = append(ff.Symbols, s)
	}
	return ff
}

func withChunks(ff cg.FileFacts, chunks ...cg.Chunk) cg.FileFacts {
	for _, c := range chunks {
		c.Layer, c.Path = ff.File.Layer, ff.File.Path
		ff.Chunks = append(ff.Chunks, c)
	}
	return ff
}

func withImports(ff cg.FileFacts, imports ...cg.Import) cg.FileFacts {
	ff.Imports = append(ff.Imports, imports...)
	return ff
}

func tombstone(layer, path string) cg.FileFacts {
	return cg.FileFacts{File: cg.File{Layer: layer, Path: path, Deleted: true}}
}

func apply(t *testing.T, g cg.Graph, ffs ...cg.FileFacts) {
	t.Helper()
	for _, ff := range ffs {
		if err := g.ApplyFile(context.Background(), ff); err != nil {
			t.Fatalf("ApplyFile %s:%s: %v", ff.File.Layer, ff.File.Path, err)
		}
	}
}

func setHidden(t *testing.T, g cg.Graph, layer, path string, hiddenBy ...string) {
	t.Helper()
	if err := g.SetHidden(context.Background(), layer, path, hiddenBy); err != nil {
		t.Fatalf("SetHidden %s:%s: %v", layer, path, err)
	}
}

func ref(layer, id string) cg.NodeRef { return cg.NodeRef{Layer: layer, ID: id} }

func edge(typ cg.EdgeType, layer string, from, to cg.NodeRef, line int) cg.Edge {
	return cg.Edge{Type: typ, Layer: layer, From: from, To: to, Confidence: cg.ConfidenceExact, Count: 1, Line: line}
}

func putEdges(t *testing.T, g cg.Graph, layer string, from []cg.NodeRef, edges ...cg.Edge) {
	t.Helper()
	if err := g.PutEdges(context.Background(), layer, from, edges); err != nil {
		t.Fatalf("PutEdges %s: %v", layer, err)
	}
}

// ---- assertions ----

func wantErr(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: err = %v, want %v", what, err, target)
	}
}

func files(t *testing.T, g cg.Graph, layer string) []cg.File {
	t.Helper()
	fs, err := g.Files(context.Background(), layer)
	if err != nil {
		t.Fatalf("Files %s: %v", layer, err)
	}
	return fs
}

func fileByPath(t *testing.T, g cg.Graph, layer, path string) cg.File {
	t.Helper()
	for _, f := range files(t, g, layer) {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no file %s:%s", layer, path)
	return cg.File{}
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func wantStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

func wantHidden(t *testing.T, g cg.Graph, layer, path string, want ...string) {
	t.Helper()
	wantStrings(t, "hiddenBy of "+layer+":"+path, sorted(fileByPath(t, g, layer, path).HiddenBy), sorted(want))
}

// symKeys formats symbols as layer/id.
func symKeys(syms []cg.Symbol) []string {
	out := make([]string, len(syms))
	for i, s := range syms {
		out[i] = s.Layer + "/" + s.ID
	}
	return out
}

func find(t *testing.T, g cg.Graph, v cg.View, q cg.SymbolQuery) []string {
	t.Helper()
	syms, err := g.FindSymbols(context.Background(), v, q)
	if err != nil {
		t.Fatalf("FindSymbols %q in %v: %v", q.Name, v, err)
	}
	return symKeys(syms)
}

func findName(t *testing.T, g cg.Graph, v cg.View, name string) []string {
	t.Helper()
	return find(t, g, v, cg.SymbolQuery{Name: name})
}

func search(t *testing.T, g cg.Graph, v cg.View, q cg.TextQuery) []string {
	t.Helper()
	hits, err := g.SearchText(context.Background(), v, q)
	if err != nil {
		t.Fatalf("SearchText %q in %v: %v", q.Query, v, err)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Score > hits[i-1].Score {
			t.Errorf("SearchText %q: score %v after %v, want best first", q.Query, hits[i].Score, hits[i-1].Score)
		}
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.Symbol.Layer + "/" + h.Symbol.ID
	}
	return out
}

func symbolLayer(t *testing.T, g cg.Graph, v cg.View, id string) string {
	t.Helper()
	info, err := g.Symbol(context.Background(), v, cg.SymbolKey{ID: id})
	if err != nil {
		t.Fatalf("Symbol %q in %v: %v", id, v, err)
	}
	return info.Symbol.Layer
}

func neighbors(t *testing.T, g cg.Graph, v cg.View, q cg.NeighborQuery) []cg.NeighborRow {
	t.Helper()
	rows, err := g.Neighbors(context.Background(), v, q)
	if err != nil {
		t.Fatalf("Neighbors %s of %q in %v: %v", q.Relation, q.ID+q.Path+q.Package, v, err)
	}
	return rows
}

// rowKeys formats rows as from>to@depth with layer/id endpoints (a file's
// or package's name for the others).
func rowKeys(rows []cg.NeighborRow) []string {
	end := func(n cg.NodeSummary) string {
		if n.Label == cg.LabelSymbol {
			return n.Layer + "/" + n.ID
		}
		return n.Layer + "/" + n.Name
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = fmt.Sprintf("%s>%s@%d", end(r.From), end(r.To), r.Depth)
	}
	return out
}

func walk(t *testing.T, g cg.Graph, v cg.View, rel cg.Relation, id string, depth int) []string {
	t.Helper()
	return rowKeys(neighbors(t, g, v, cg.NeighborQuery{ID: id, Relation: rel, Depth: depth}))
}

func outlinePaths(t *testing.T, g cg.Graph, v cg.View, q cg.OutlineQuery) []string {
	t.Helper()
	outs, err := g.Outline(context.Background(), v, q)
	if err != nil {
		t.Fatalf("Outline %+v in %v: %v", q, v, err)
	}
	out := make([]string, len(outs))
	for i, o := range outs {
		out[i] = o.File.Layer + ":" + o.File.Path
	}
	return out
}

// ---- writes ----

func testLayers(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	if ls, err := g.Layers(ctx); err != nil || len(ls) != 0 {
		t.Fatalf("Layers of a new graph = %v, %v; want none", ls, err)
	}
	putLayers(t, g, overlay, base, branch)
	ls, err := g.Layers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range ls {
		ids = append(ids, l.ID)
	}
	wantStrings(t, "layer ids", ids, sorted([]string{base, branch, overlay}))
	for _, l := range ls {
		want := layerOf(l.ID)
		if l.Kind != want.Kind || l.Parent != want.Parent || l.Owner != want.Owner ||
			l.Ref != want.Ref || l.Commit != want.Commit || !l.Updated.Equal(want.Updated) {
			t.Errorf("layer %s = %+v, want %+v", l.ID, l, want)
		}
	}

	// PutLayer updates in place.
	up := layerOf(branch)
	up.Commit, up.Updated = "def456", time.Date(2026, 10, 2, 8, 30, 0, 0, time.UTC)
	if err := g.PutLayer(ctx, up); err != nil {
		t.Fatal(err)
	}
	ls, _ = g.Layers(ctx)
	if len(ls) != 3 {
		t.Fatalf("after update: %d layers, want 3", len(ls))
	}
	for _, l := range ls {
		if l.ID == branch && (l.Commit != "def456" || !l.Updated.Equal(up.Updated)) {
			t.Errorf("updated layer = %+v, want commit def456 at %v", l, up.Updated)
		}
	}

	wantErr(t, "PutLayer with a mismatched kind", g.PutLayer(ctx, cg.Layer{ID: base, Kind: cg.LayerBranch}), cg.ErrInvalid)
	wantErr(t, "PutLayer with a bad id", g.PutLayer(ctx, cg.Layer{ID: "branch:nocommit", Kind: cg.LayerBranch}), cg.ErrInvalid)
	wantErr(t, "PutLayer with a bad parent", g.PutLayer(ctx, cg.Layer{ID: overlay, Kind: cg.LayerWorktree, Parent: "nope"}), cg.ErrInvalid)
}

func testApplyFileAndFiles(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base, overlay)

	wantErr(t, "ApplyFile in a layer never put",
		g.ApplyFile(ctx, facts(branch, "a.go", "go", "a")), cg.ErrUnknownLayer)
	wantErr(t, "ApplyFile with a bad path",
		g.ApplyFile(ctx, facts(base, "../a.go", "go", "a")), cg.ErrInvalid)
	wantErr(t, "ApplyFile with a symbol of another path",
		g.ApplyFile(ctx, cg.FileFacts{File: cg.File{Layer: base, Path: "a.go"},
			Symbols: []cg.Symbol{{Layer: base, Path: "b.go", ID: "x", Name: "x"}}}), cg.ErrInvalid)
	wantErr(t, "ApplyFile of a tombstone with symbols",
		g.ApplyFile(ctx, cg.FileFacts{File: cg.File{Layer: base, Path: "a.go", Deleted: true},
			Symbols: []cg.Symbol{{Layer: base, Path: "a.go", ID: "x", Name: "x"}}}), cg.ErrInvalid)
	wantErr(t, "Files of a bad layer id", func() error { _, err := g.Files(ctx, "nope"); return err }(), cg.ErrInvalid)
	wantErr(t, "Files of a layer never put", func() error { _, err := g.Files(ctx, branch); return err }(), cg.ErrNotFound)
	if fs := files(t, g, base); len(fs) != 0 {
		t.Fatalf("files after refused writes = %v, want none", fs)
	}

	b := facts(base, "pkg/b.go", "go", "pkg", sym("b#B", cg.SymbolFunction, "B", "pkg.B", 1, 3))
	b.File.ParseError = true
	a := facts(base, "a.go", "go", "main", sym("a#A", cg.SymbolFunction, "A", "main.A", 1, 3))
	a.PackageKind = cg.PackageKindPackage
	apply(t, g, b, a, facts(base, "README.md", "markdown", ""), tombstone(overlay, "a.go"))

	fs := files(t, g, base)
	var paths []string
	for _, f := range fs {
		paths = append(paths, f.Path)
	}
	wantStrings(t, "base paths", paths, []string{"README.md", "a.go", "pkg/b.go"})
	got := fileByPath(t, g, base, "pkg/b.go")
	want := b.File
	if got.Layer != want.Layer || got.Path != want.Path || got.SHA256 != want.SHA256 || got.GitOID != want.GitOID ||
		got.Lang != want.Lang || got.Package != want.Package || !got.ParseError || got.Deleted || len(got.HiddenBy) != 0 {
		t.Errorf("file = %+v, want %+v", got, want)
	}
	tomb := files(t, g, overlay)
	if len(tomb) != 1 || tomb[0].Path != "a.go" || !tomb[0].Deleted {
		t.Errorf("overlay files = %+v, want the a.go tombstone", tomb)
	}

	// A symbol or chunk id is unique within a layer: another path may not
	// take it.
	wantErr(t, "ApplyFile with a symbol id another file holds",
		g.ApplyFile(ctx, facts(base, "c.go", "go", "main", sym("a#A", cg.SymbolFunction, "A", "main.A", 1, 3))), cg.ErrInvalid)
	apply(t, g, withChunks(facts(base, "d.go", "go", "main"), cg.Chunk{ID: "chunk-d", Kind: "text", Start: 1, End: 2}))
	wantErr(t, "ApplyFile with a chunk id another file holds",
		g.ApplyFile(ctx, withChunks(facts(base, "e.go", "go", "main"), cg.Chunk{ID: "chunk-d", Kind: "text", Start: 1, End: 2})), cg.ErrInvalid)
	// The same id in another layer is a different node.
	putLayers(t, g, branch)
	apply(t, g, facts(branch, "c.go", "go", "main", sym("a#A", cg.SymbolFunction, "A", "main.A", 1, 3)))
}

func testApplyFileReplaces(t *testing.T, g cg.Graph) {
	putLayers(t, g, base)
	v1 := withImports(withChunks(facts(base, "a.go", "go", "one",
		sym("a#Old", cg.SymbolFunction, "Old", "one.Old", 1, 5),
		sym("a#Keep", cg.SymbolFunction, "Keep", "one.Keep", 7, 9)),
		cg.Chunk{ID: "c-old", Kind: "function", Symbol: "a#Old", Start: 1, End: 5},
		cg.Chunk{ID: "c-keep", Kind: "function", Symbol: "a#Keep", Start: 7, End: 9}),
		cg.Import{Package: "fmt"})
	apply(t, g, v1)

	v2 := withImports(withChunks(facts(base, "a.go", "go", "two",
		sym("a#Keep", cg.SymbolFunction, "Keep", "two.Keep", 2, 4),
		sym("a#New", cg.SymbolFunction, "New", "two.New", 6, 8)),
		cg.Chunk{ID: "c-keep2", Kind: "function", Symbol: "a#Keep", Start: 2, End: 4}),
		cg.Import{Package: "strings"})
	v2.File.SHA256 = "sha-2"
	apply(t, g, v2)

	if fs := files(t, g, base); len(fs) != 1 || fs[0].SHA256 != "sha-2" || fs[0].Package != "two" {
		t.Errorf("files after replace = %+v, want one a.go with sha-2 in package two", fs)
	}
	wantStrings(t, "Old after replace", findName(t, g, vBase, "Old"), nil)
	wantStrings(t, "New after replace", findName(t, g, vBase, "New"), []string{base + "/a#New"})
	info, err := g.Symbol(context.Background(), vBase, cg.SymbolKey{ID: "a#Keep"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Symbol.QualName != "two.Keep" || info.Symbol.Start != 2 {
		t.Errorf("kept symbol = %+v, want the new facts", info.Symbol)
	}
	if len(info.Chunks) != 1 || info.Chunks[0].ID != "c-keep2" {
		t.Errorf("kept symbol's chunks = %+v, want only c-keep2", info.Chunks)
	}
	wantStrings(t, "imports after replace",
		rowKeys(neighbors(t, g, vBase, cg.NeighborQuery{Path: "a.go", Relation: cg.RelImports})),
		[]string{base + "/a.go>" + base + "/strings@1"})
	wantStrings(t, "outline of package one", func() []string {
		_, err := g.Outline(context.Background(), vBase, cg.OutlineQuery{Package: "one"})
		wantErr(t, "Outline of a package no file is in", err, cg.ErrNotFound)
		return nil
	}(), nil)
	wantStrings(t, "outline of package two", outlinePaths(t, g, vBase, cg.OutlineQuery{Package: "two"}), []string{base + ":a.go"})

	// A tombstone over a parsed file in the same layer leaves nothing.
	apply(t, g, tombstone(base, "a.go"))
	wantStrings(t, "Keep after a tombstone", findName(t, g, vBase, "Keep"), nil)
	if fs := files(t, g, base); len(fs) != 1 || !fs[0].Deleted {
		t.Errorf("files after tombstone = %+v, want the a.go tombstone", fs)
	}
}

func testApplyFileKeepsHiddenBy(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base, branch, overlay)
	apply(t, g, facts(base, "a.go", "go", "a"))
	setHidden(t, g, base, "a.go", branch, overlay)
	wantHidden(t, g, base, "a.go", branch, overlay)

	// Re-applying with a nil hiddenBy keeps the stored one.
	apply(t, g, facts(base, "a.go", "go", "a", sym("a#A", cg.SymbolFunction, "A", "a.A", 1, 2)))
	wantHidden(t, g, base, "a.go", branch, overlay)
	// A list replaces it.
	ff := facts(base, "a.go", "go", "a")
	ff.File.HiddenBy = []string{branch}
	apply(t, g, ff)
	wantHidden(t, g, base, "a.go", branch)

	// SetHidden replaces and clears.
	setHidden(t, g, base, "a.go", overlay)
	wantHidden(t, g, base, "a.go", overlay)
	setHidden(t, g, base, "a.go")
	wantHidden(t, g, base, "a.go")

	wantErr(t, "SetHidden on a missing file", g.SetHidden(ctx, base, "nope.go", []string{overlay}), cg.ErrNotFound)
	wantErr(t, "SetHidden with a bad layer id", g.SetHidden(ctx, base, "a.go", []string{"nope"}), cg.ErrInvalid)
	wantErr(t, "SetHidden with a bad path", g.SetHidden(ctx, base, "/a.go", nil), cg.ErrInvalid)
}

// edgeWorld is two base files: a.go defines Foo and Baz, c.go's Caller
// calls both.
func edgeWorld(t *testing.T, g cg.Graph) {
	t.Helper()
	putLayers(t, g, base, branch, overlay)
	apply(t, g,
		facts(base, "a.go", "go", "a",
			sym("a.go#Foo", cg.SymbolFunction, "Foo", "a.Foo", 3, 5),
			sym("a.go#Baz", cg.SymbolFunction, "Baz", "a.Baz", 7, 9)),
		facts(base, "c.go", "go", "c", sym("c.go#Caller", cg.SymbolFunction, "Caller", "c.Caller", 3, 10)))
	caller := ref(base, "c.go#Caller")
	putEdges(t, g, base, []cg.NodeRef{caller},
		edge(cg.EdgeCalls, base, caller, ref(base, "a.go#Foo"), 4),
		edge(cg.EdgeCalls, base, caller, ref(base, "a.go#Baz"), 5))
}

func testApplyFileKeepsEdges(t *testing.T, g cg.Graph) {
	edgeWorld(t, g)
	caller := ref(base, "c.go#Caller")
	// An overlay edge into a base symbol, resolved by the overlay.
	putEdges(t, g, overlay, []cg.NodeRef{caller}, edge(cg.EdgeCalls, overlay, caller, ref(base, "a.go#Baz"), 5))

	// Foo keeps its (layer, id) with new lines; Baz goes away.
	apply(t, g, facts(base, "a.go", "go", "a", sym("a.go#Foo", cg.SymbolFunction, "Foo", "a.Foo", 13, 15)))
	wantStrings(t, "callees after re-apply", walk(t, g, vBase, cg.RelCallees, "c.go#Caller", 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Foo@1"})
	rows := neighbors(t, g, vBase, cg.NeighborQuery{ID: "a.go#Foo", Relation: cg.RelCallers})
	if len(rows) != 1 || rows[0].To.Start != 13 || rows[0].Line != 4 {
		t.Errorf("callers of the kept symbol = %+v, want one row to its new start 13 at line 4", rows)
	}
	// The overlay's edge into Baz went with it.
	wantStrings(t, "overlay callees after re-apply", walk(t, g, vOverlay, cg.RelCallees, "c.go#Caller", 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Foo@1"})

	// Re-applying the caller's file with the same id keeps its outgoing edges.
	apply(t, g, facts(base, "c.go", "go", "c", sym("c.go#Caller", cg.SymbolFunction, "Caller", "c.Caller", 1, 20)))
	wantStrings(t, "callees after re-applying the caller", walk(t, g, vBase, cg.RelCallees, "c.go#Caller", 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Foo@1"})
}

func testRemoveFile(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	edgeWorld(t, g)
	caller := ref(base, "c.go#Caller")
	apply(t, g, facts(overlay, "o.go", "go", "o", sym("o.go#Hook", cg.SymbolFunction, "Hook", "o.Hook", 1, 3)))
	hook := ref(overlay, "o.go#Hook")
	putEdges(t, g, overlay, []cg.NodeRef{hook}, edge(cg.EdgeCalls, overlay, hook, ref(base, "a.go#Foo"), 2))
	setHidden(t, g, base, "c.go", overlay)

	if err := g.RemoveFile(ctx, base, "a.go"); err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "Foo after removing its file", findName(t, g, vBase, "Foo"), nil)
	wantStrings(t, "callees of Caller", walk(t, g, vBase, cg.RelCallees, caller.ID, 1), nil)
	wantStrings(t, "callees of the overlay's Hook", walk(t, g, vOverlay, cg.RelCallees, hook.ID, 1), nil)
	var paths []string
	for _, f := range files(t, g, base) {
		paths = append(paths, f.Path)
	}
	wantStrings(t, "base paths", paths, []string{"c.go"})
	// RemoveFile does not touch other files' hiddenBy.
	wantHidden(t, g, base, "c.go", overlay)

	if err := g.RemoveFile(ctx, base, "a.go"); err != nil {
		t.Errorf("removing a missing file: %v", err)
	}
	if err := g.RemoveFile(ctx, other, "a.go"); err != nil {
		t.Errorf("removing a file of a layer never put: %v", err)
	}
	wantErr(t, "RemoveFile with a bad path", g.RemoveFile(ctx, base, "a/../b.go"), cg.ErrInvalid)
	wantErr(t, "RemoveFile with a bad layer", g.RemoveFile(ctx, "nope", "a.go"), cg.ErrInvalid)
}

// threeLayers holds a.go in all three layers (the branch replaces the
// base's, the overlay the branch's), b.go in the base only, and br.go added
// by the branch.
func threeLayers(t *testing.T, g cg.Graph) {
	t.Helper()
	putLayers(t, g, base, branch, overlay, other)
	apply(t, g,
		facts(base, "a.go", "go", "a", sym("a.go#Run", cg.SymbolFunction, "Run", "a.Run", 1, 5)),
		facts(base, "b.go", "go", "a", sym("b.go#Help", cg.SymbolFunction, "Help", "a.Help", 1, 5)),
		facts(branch, "a.go", "go", "a", sym("a.go#Run", cg.SymbolFunction, "Run", "a.Run", 1, 8)),
		facts(branch, "br.go", "go", "a", sym("br.go#Extra", cg.SymbolFunction, "Extra", "a.Extra", 1, 5)),
		facts(overlay, "a.go", "go", "a", sym("a.go#Run", cg.SymbolFunction, "Run", "a.Run", 1, 12)))
	setHidden(t, g, base, "a.go", branch, overlay)
	setHidden(t, g, branch, "a.go", overlay)
}

func testHiddenByThreeLayers(t *testing.T, g cg.Graph) {
	threeLayers(t, g)
	ctx := context.Background()
	for _, c := range []struct {
		name string
		view cg.View
		run  string // layer of the visible Run
	}{
		{"base", vBase, base}, {"branch", vBranch, branch}, {"overlay", vOverlay, overlay}, {"other", vOther, branch},
	} {
		wantStrings(t, c.name+": FindSymbols Run", findName(t, g, c.view, "Run"), []string{c.run + "/a.go#Run"})
		if got := symbolLayer(t, g, c.view, "a.go#Run"); got != c.run {
			t.Errorf("%s: Symbol Run in layer %s, want %s", c.name, got, c.run)
		}
		wantStrings(t, c.name+": Outline a.go", outlinePaths(t, g, c.view, cg.OutlineQuery{Path: "a.go"}), []string{c.run + ":a.go"})
		wantStrings(t, c.name+": SearchText run", search(t, g, c.view, cg.TextQuery{Query: "run"}), []string{c.run + "/a.go#Run"})
		// b.go is never replaced.
		wantStrings(t, c.name+": FindSymbols Help", findName(t, g, c.view, "Help"), []string{base + "/b.go#Help"})
	}
	wantStrings(t, "outline of package a in the overlay's view",
		outlinePaths(t, g, vOverlay, cg.OutlineQuery{Package: "a"}),
		[]string{overlay + ":a.go", base + ":b.go", branch + ":br.go"})
	wantStrings(t, "outline of package a in the base view",
		outlinePaths(t, g, vBase, cg.OutlineQuery{Package: "a"}), []string{base + ":a.go", base + ":b.go"})
	// The branch's own file is not in the base view.
	wantStrings(t, "Extra in the base view", findName(t, g, vBase, "Extra"), nil)
	_, err := g.Symbol(ctx, vBase, cg.SymbolKey{ID: "br.go#Extra"})
	wantErr(t, "Symbol of a branch file in the base view", err, cg.ErrNotFound)

	// Without hiddenBy every layer's copy is visible, and the highest one
	// answers for the id.
	setHidden(t, g, base, "a.go")
	setHidden(t, g, branch, "a.go")
	if got := symbolLayer(t, g, vOverlay, "a.go#Run"); got != overlay {
		t.Errorf("Symbol Run with nothing hidden in layer %s, want %s", got, overlay)
	}
	wantStrings(t, "FindSymbols Run with nothing hidden", findName(t, g, vOverlay, "Run"), []string{overlay + "/a.go#Run"})
	wantStrings(t, "outline of a.go with nothing hidden", outlinePaths(t, g, vOverlay, cg.OutlineQuery{Path: "a.go"}),
		[]string{base + ":a.go", branch + ":a.go", overlay + ":a.go"})
}

func testTombstones(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	threeLayers(t, g)
	// The overlay deletes b.go; the branch deletes nothing.
	apply(t, g, tombstone(overlay, "b.go"))
	setHidden(t, g, base, "b.go", overlay)

	tomb := fileByPath(t, g, overlay, "b.go")
	if !tomb.Deleted {
		t.Errorf("overlay b.go = %+v, want a tombstone", tomb)
	}
	wantStrings(t, "Help in the overlay's view", findName(t, g, vOverlay, "Help"), nil)
	wantStrings(t, "Help in the other overlay's view", findName(t, g, vOther, "Help"), []string{base + "/b.go#Help"})
	_, err := g.Symbol(ctx, vOverlay, cg.SymbolKey{QualName: "a.Help"})
	wantErr(t, "Symbol of a deleted file", err, cg.ErrNotFound)
	_, err = g.Outline(ctx, vOverlay, cg.OutlineQuery{Path: "b.go"})
	wantErr(t, "Outline of a deleted file", err, cg.ErrNotFound)
	_, err = g.Neighbors(ctx, vOverlay, cg.NeighborQuery{Path: "b.go", Relation: cg.RelImports})
	wantErr(t, "imports of a deleted file", err, cg.ErrNotFound)
	wantStrings(t, "outline of b.go in the branch view", outlinePaths(t, g, vBranch, cg.OutlineQuery{Path: "b.go"}), []string{base + ":b.go"})

	// A tombstone alone, without the hiddenBy entry, hides nothing below it
	// but is never visible itself.
	apply(t, g, tombstone(other, "br.go"))
	wantStrings(t, "Extra under a tombstone not recorded in hiddenBy", findName(t, g, vOther, "Extra"), []string{branch + "/br.go#Extra"})
	_, err = g.Outline(ctx, cg.View{other}, cg.OutlineQuery{Path: "br.go"})
	wantErr(t, "Outline of a tombstone", err, cg.ErrNotFound)

	// Re-creating the path in the overlay replaces the tombstone.
	apply(t, g, facts(overlay, "b.go", "go", "a", sym("b.go#Help", cg.SymbolFunction, "Help", "a.Help", 1, 9)))
	wantStrings(t, "Help after re-creating b.go", findName(t, g, vOverlay, "Help"), []string{overlay + "/b.go#Help"})
	if f := fileByPath(t, g, overlay, "b.go"); f.Deleted {
		t.Errorf("re-created b.go is still a tombstone")
	}
}

func testPerLayerEdges(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	edgeWorld(t, g)
	caller := ref(base, "c.go#Caller")
	// The overlay renames Foo to Bar in a.go and keeps Baz; the base caller
	// is re-resolved for the overlay's view.
	apply(t, g, facts(overlay, "a.go", "go", "a",
		sym("a.go#Bar", cg.SymbolFunction, "Bar", "a.Bar", 3, 5),
		sym("a.go#Baz", cg.SymbolFunction, "Baz", "a.Baz", 7, 9)))
	setHidden(t, g, base, "a.go", overlay)
	putEdges(t, g, overlay, []cg.NodeRef{caller},
		edge(cg.EdgeCalls, overlay, caller, ref(overlay, "a.go#Bar"), 4),
		edge(cg.EdgeCalls, overlay, caller, ref(overlay, "a.go#Baz"), 5))

	baseCallees := []string{base + "/c.go#Caller>" + base + "/a.go#Foo@1", base + "/c.go#Caller>" + base + "/a.go#Baz@1"}
	wantStrings(t, "callees in the base view", walk(t, g, vBase, cg.RelCallees, caller.ID, 1), baseCallees)
	wantStrings(t, "callees in the branch view", walk(t, g, vBranch, cg.RelCallees, caller.ID, 1), baseCallees)
	wantStrings(t, "callees in the overlay's view", walk(t, g, vOverlay, cg.RelCallees, caller.ID, 1),
		[]string{base + "/c.go#Caller>" + overlay + "/a.go#Bar@1", base + "/c.go#Caller>" + overlay + "/a.go#Baz@1"})
	wantStrings(t, "callers of Bar in the overlay's view", walk(t, g, vOverlay, cg.RelCallers, "a.go#Bar", 1),
		[]string{base + "/c.go#Caller>" + overlay + "/a.go#Bar@1"})
	wantStrings(t, "callers of Baz in the overlay's view", walk(t, g, vOverlay, cg.RelCallers, "a.go#Baz", 1),
		[]string{base + "/c.go#Caller>" + overlay + "/a.go#Baz@1"})
	wantStrings(t, "callers of Baz in the base view", walk(t, g, vBase, cg.RelCallers, "a.go#Baz", 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Baz@1"})
	_, err := g.Neighbors(ctx, vOverlay, cg.NeighborQuery{ID: "a.go#Foo", Relation: cg.RelCallers})
	wantErr(t, "callers of a symbol the overlay renamed", err, cg.ErrNotFound)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{ID: "a.go#Bar", Relation: cg.RelCallers})
	wantErr(t, "callers of an overlay symbol in the base view", err, cg.ErrNotFound)

	// An edge whose layer is in the view but whose endpoint is hidden stays
	// hidden: the base caller's file hidden by the overlay drops both.
	setHidden(t, g, base, "c.go", overlay)
	wantStrings(t, "callers of Bar with the caller hidden", walk(t, g, vOverlay, cg.RelCallers, "a.go#Bar", 1), nil)
}

func testPutEdges(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	edgeWorld(t, g)
	apply(t, g, facts(base, "d.go", "go", "c", sym("d.go#Other", cg.SymbolFunction, "Other", "c.Other", 1, 4)))
	caller, foo, baz, oth := ref(base, "c.go#Caller"), ref(base, "a.go#Foo"), ref(base, "a.go#Baz"), ref(base, "d.go#Other")
	putEdges(t, g, base, []cg.NodeRef{oth}, edge(cg.EdgeCalls, base, oth, foo, 2))
	putEdges(t, g, branch, []cg.NodeRef{caller}, edge(cg.EdgeCalls, branch, caller, oth, 7))

	// Replacing Caller's base edges leaves other sources and layers alone.
	putEdges(t, g, base, []cg.NodeRef{caller}, edge(cg.EdgeReferences, base, caller, foo, 6))
	wantStrings(t, "callees of Caller in the base view", walk(t, g, vBase, cg.RelCallees, caller.ID, 1), nil)
	wantStrings(t, "references from Caller", walk(t, g, vBase, cg.RelReferences, foo.ID, 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Foo@1"})
	wantStrings(t, "callers of Foo", walk(t, g, vBase, cg.RelCallers, foo.ID, 1),
		[]string{base + "/d.go#Other>" + base + "/a.go#Foo@1"})
	wantStrings(t, "callees of Caller in the branch view", walk(t, g, vBranch, cg.RelCallees, caller.ID, 1),
		[]string{base + "/c.go#Caller>" + base + "/d.go#Other@1"})

	// A missing endpoint refuses the whole write.
	err := g.PutEdges(ctx, base, []cg.NodeRef{caller}, []cg.Edge{
		edge(cg.EdgeCalls, base, caller, baz, 3),
		edge(cg.EdgeCalls, base, caller, ref(base, "nope"), 4)})
	wantErr(t, "PutEdges to a missing symbol", err, cg.ErrNotFound)
	err = g.PutEdges(ctx, base, []cg.NodeRef{ref(base, "ghost")}, []cg.Edge{edge(cg.EdgeCalls, base, ref(base, "ghost"), foo, 1)})
	wantErr(t, "PutEdges from a missing symbol", err, cg.ErrNotFound)
	wantStrings(t, "references after a refused write", walk(t, g, vBase, cg.RelReferences, foo.ID, 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Foo@1"})
	wantStrings(t, "callers of Baz after a refused write", walk(t, g, vBase, cg.RelCallers, baz.ID, 1), nil)

	// Contract violations.
	for _, c := range []struct {
		name  string
		layer string
		from  []cg.NodeRef
		edges []cg.Edge
		want  error
	}{
		{"edge of another layer", base, []cg.NodeRef{caller}, []cg.Edge{edge(cg.EdgeCalls, branch, caller, foo, 1)}, cg.ErrInvalid},
		{"edge from outside the sources", base, []cg.NodeRef{caller}, []cg.Edge{edge(cg.EdgeCalls, base, oth, foo, 1)}, cg.ErrInvalid},
		{"structural edge type", base, []cg.NodeRef{caller}, []cg.Edge{edge(cg.EdgeContains, base, caller, foo, 1)}, cg.ErrInvalid},
		{"bad layer id", "nope", nil, nil, cg.ErrInvalid},
		{"no confidence", base, []cg.NodeRef{caller}, []cg.Edge{{Type: cg.EdgeCalls, Layer: base, From: caller, To: foo}}, cg.ErrInvalid},
		{"layer never put", other, []cg.NodeRef{caller}, []cg.Edge{edge(cg.EdgeCalls, other, caller, foo, 1)}, cg.ErrUnknownLayer},
	} {
		wantErr(t, "PutEdges: "+c.name, g.PutEdges(ctx, c.layer, c.from, c.edges), c.want)
	}

	// An empty write clears the sources' edges in that layer.
	putEdges(t, g, base, []cg.NodeRef{caller, oth})
	wantStrings(t, "callers of Foo after clearing", walk(t, g, vBase, cg.RelCallers, foo.ID, 1), nil)
	wantStrings(t, "references after clearing", walk(t, g, vBase, cg.RelReferences, foo.ID, 1), nil)
	wantStrings(t, "branch edge after clearing the base", walk(t, g, vBranch, cg.RelCallees, caller.ID, 1),
		[]string{base + "/c.go#Caller>" + base + "/d.go#Other@1"})
}

func testDropLayer(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	threeLayers(t, g)
	apply(t, g, tombstone(overlay, "b.go"))
	setHidden(t, g, base, "b.go", overlay, other)
	run := ref(overlay, "a.go#Run")
	help := ref(base, "b.go#Help")
	extra := ref(branch, "br.go#Extra")
	putEdges(t, g, overlay, []cg.NodeRef{run}, edge(cg.EdgeCalls, overlay, run, extra, 2))
	// A branch edge into the overlay's symbol and a base edge that touches
	// nothing of the overlay.
	putEdges(t, g, branch, []cg.NodeRef{extra}, edge(cg.EdgeCalls, branch, extra, run, 3))
	putEdges(t, g, base, []cg.NodeRef{help}, edge(cg.EdgeReferences, base, help, ref(base, "a.go#Run"), 4))

	if err := g.DropLayer(ctx, overlay); err != nil {
		t.Fatal(err)
	}
	ls, _ := g.Layers(ctx)
	for _, l := range ls {
		if l.ID == overlay {
			t.Errorf("dropped layer still listed")
		}
	}
	_, err := g.Files(ctx, overlay)
	wantErr(t, "Files of a dropped layer", err, cg.ErrNotFound)
	wantHidden(t, g, base, "a.go", branch)
	wantHidden(t, g, base, "b.go", other)
	wantHidden(t, g, branch, "a.go")

	// The overlay's view now reads like the branch's.
	wantStrings(t, "Run in the overlay's view", findName(t, g, vOverlay, "Run"), []string{branch + "/a.go#Run"})
	wantStrings(t, "Help in the overlay's view", findName(t, g, vOverlay, "Help"), []string{base + "/b.go#Help"})
	wantStrings(t, "callers of Extra", walk(t, g, vOverlay, cg.RelCallers, extra.ID, 1), nil)
	wantStrings(t, "callees of Extra", walk(t, g, vBranch, cg.RelCallees, extra.ID, 1), nil)
	wantStrings(t, "base references survive", walk(t, g, vBase, cg.RelReferences, "a.go#Run", 1),
		[]string{base + "/b.go#Help>" + base + "/a.go#Run@1"})
	wantErr(t, "ApplyFile to a dropped layer", g.ApplyFile(ctx, facts(overlay, "x.go", "go", "x")), cg.ErrUnknownLayer)

	if err := g.DropLayer(ctx, overlay); err != nil {
		t.Errorf("dropping a missing layer: %v", err)
	}
	wantErr(t, "DropLayer with a bad id", g.DropLayer(ctx, "nope"), cg.ErrInvalid)

	// A dropped layer can be put again and starts empty.
	putLayers(t, g, overlay)
	if fs := files(t, g, overlay); len(fs) != 0 {
		t.Errorf("files of a re-created layer = %v, want none", fs)
	}
	// Packages went with the layer too: nothing imports or is in them.
	apply(t, g, withImports(facts(branch, "imp.go", "go", "a"), cg.Import{Package: "lib"}))
	if err := g.DropLayer(ctx, branch); err != nil {
		t.Fatal(err)
	}
	_, err = g.Neighbors(ctx, vBranch, cg.NeighborQuery{Package: "lib", Relation: cg.RelImporters})
	wantErr(t, "importers of a dropped layer's package", err, cg.ErrNotFound)
}

func testDrop(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	edgeWorld(t, g)
	if err := g.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if ls, err := g.Layers(ctx); err != nil || len(ls) != 0 {
		t.Errorf("Layers after Drop = %v, %v; want none", ls, err)
	}
	_, err := g.Files(ctx, base)
	wantErr(t, "Files after Drop", err, cg.ErrNotFound)
	wantStrings(t, "Foo after Drop", findName(t, g, vBase, "Foo"), nil)
	wantErr(t, "ApplyFile after Drop", g.ApplyFile(ctx, facts(base, "a.go", "go", "a")), cg.ErrUnknownLayer)
	if err := g.Drop(ctx); err != nil {
		t.Errorf("dropping a dropped graph: %v", err)
	}

	// The key can be rebuilt.
	edgeWorld(t, g)
	wantStrings(t, "callees after a rebuild", walk(t, g, vBase, cg.RelCallees, "c.go#Caller", 1),
		[]string{base + "/c.go#Caller>" + base + "/a.go#Foo@1", base + "/c.go#Caller>" + base + "/a.go#Baz@1"})
}

// ---- reads ----

func testFindSymbols(t *testing.T, g cg.Graph) {
	putLayers(t, g, base)
	apply(t, g,
		facts(base, "svc/f.go", "go", "svc",
			sym("f#Store", cg.SymbolType, "Store", "svc.Store", 3, 20),
			child(sym("f#Store.Open", cg.SymbolMethod, "Open", "svc.Store.Open", 5, 9), "f#Store"),
			sym("f#Open", cg.SymbolFunction, "Open", "svc.Open", 22, 30)),
		facts(base, "svcx/h.go", "go", "svcx", sym("h#Open", cg.SymbolFunction, "Open", "svcx.Open", 1, 4)),
		facts(base, "z.py", "python", "z", sym("z#Open", cg.SymbolFunction, "Open", "Open", 50, 60)))

	// Qualified-name matches first, then by path, start and id.
	wantStrings(t, "Open", findName(t, g, vBase, "Open"),
		[]string{base + "/z#Open", base + "/f#Store.Open", base + "/f#Open", base + "/h#Open"})
	wantStrings(t, "svc.Store.Open", findName(t, g, vBase, "svc.Store.Open"), []string{base + "/f#Store.Open"})
	wantStrings(t, "case-sensitive", findName(t, g, vBase, "open"), nil)
	wantStrings(t, "kinds", find(t, g, vBase, cg.SymbolQuery{Name: "Open", Filter: cg.Filter{Kinds: []string{cg.SymbolMethod}}}),
		[]string{base + "/f#Store.Open"})
	wantStrings(t, "langs", find(t, g, vBase, cg.SymbolQuery{Name: "Open", Filter: cg.Filter{Langs: []string{"python"}}}),
		[]string{base + "/z#Open"})
	wantStrings(t, "paths", find(t, g, vBase, cg.SymbolQuery{Name: "Open", Filter: cg.Filter{Paths: []string{"svc/"}}}),
		[]string{base + "/f#Store.Open", base + "/f#Open"})
	wantStrings(t, "limit", find(t, g, vBase, cg.SymbolQuery{Name: "Open", Limit: 2}),
		[]string{base + "/z#Open", base + "/f#Store.Open"})
	got, err := g.FindSymbols(context.Background(), vBase, cg.SymbolQuery{Name: "Store"})
	if err != nil || len(got) != 1 {
		t.Fatalf("FindSymbols Store = %v, %v", got, err)
	}
	s := got[0]
	if s.Kind != cg.SymbolType || s.QualName != "svc.Store" || s.Path != "svc/f.go" || s.Start != 3 || s.End != 20 ||
		s.Signature != "func Store()" || !s.Exported || s.Layer != base {
		t.Errorf("Store = %+v", s)
	}

	// The default limit and the cap.
	var many []cg.Symbol
	for i := 0; i < cg.DefaultLimit+10; i++ {
		many = append(many, sym(fmt.Sprintf("m#Dup%03d", i), cg.SymbolVar, "Dup", "", i+1, i+1))
	}
	apply(t, g, facts(base, "many.go", "go", "m", many...))
	if n := len(findName(t, g, vBase, "Dup")); n != cg.DefaultLimit {
		t.Errorf("Dup with no limit: %d results, want %d", n, cg.DefaultLimit)
	}
	if n := len(find(t, g, vBase, cg.SymbolQuery{Name: "Dup", Limit: cg.MaxLimit + 1})); n != cg.DefaultLimit+10 {
		t.Errorf("Dup over the cap: %d results, want %d", n, cg.DefaultLimit+10)
	}
	wantStrings(t, "Dup limit 2", find(t, g, vBase, cg.SymbolQuery{Name: "Dup", Limit: 2}),
		[]string{base + "/m#Dup000", base + "/m#Dup001"})
}

func testSymbol(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base, branch)
	apply(t, g, withChunks(facts(base, "o.go", "go", "pkg",
		sym("o#T", cg.SymbolStruct, "T", "pkg.T", 1, 20),
		child(sym("o#T.M", cg.SymbolMethod, "M", "pkg.T.M", 5, 8), "o#T"),
		child(sym("o#T.f", cg.SymbolField, "f", "pkg.T.f", 2, 2), "o#T"),
		child(sym("o#T.M.x", cg.SymbolVar, "x", "pkg.T.M.x", 6, 6), "o#T.M"),
		sym("o#F", cg.SymbolFunction, "F", "pkg.F", 22, 25)),
		cg.Chunk{ID: "ck2", Kind: "part", Symbol: "o#T", Start: 10, End: 20, Hash: "h2"},
		cg.Chunk{ID: "ck1", Kind: "part", Symbol: "o#T", Start: 1, End: 9, Hash: "h1"},
		cg.Chunk{ID: "ck3", Kind: "function", Symbol: "o#F", Start: 22, End: 25},
		cg.Chunk{ID: "ck0", Kind: "window", Start: 1, End: 30}))

	info, err := g.Symbol(ctx, vBase, cg.SymbolKey{ID: "o#T"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Symbol.QualName != "pkg.T" || info.Symbol.Layer != base || info.Lang != "go" {
		t.Errorf("Symbol o#T = %+v", info)
	}
	wantStrings(t, "children", symKeys(info.Children), []string{base + "/o#T.f", base + "/o#T.M"})
	var chunks []string
	for _, c := range info.Chunks {
		chunks = append(chunks, c.ID)
	}
	wantStrings(t, "chunks", chunks, []string{"ck1", "ck2"})
	if len(info.Chunks) == 2 && (info.Chunks[0].Hash != "h1" || info.Chunks[0].Kind != "part" ||
		info.Chunks[0].Path != "o.go" || info.Chunks[0].End != 9 || info.Chunks[0].Layer != base) {
		t.Errorf("chunk = %+v", info.Chunks[0])
	}

	byQual, err := g.Symbol(ctx, vBase, cg.SymbolKey{QualName: "pkg.T.M"})
	if err != nil || byQual.Symbol.ID != "o#T.M" || byQual.Symbol.Parent != "o#T" {
		t.Errorf("Symbol by qualName = %+v, %v", byQual, err)
	}
	wantStrings(t, "children of M", symKeys(byQual.Children), []string{base + "/o#T.M.x"})
	// The ID wins over the qualified name when both are given.
	both, err := g.Symbol(ctx, vBase, cg.SymbolKey{ID: "o#F", QualName: "pkg.T"})
	if err != nil || both.Symbol.ID != "o#F" {
		t.Errorf("Symbol by id and qualName = %+v, %v", both.Symbol, err)
	}

	// The branch holds the same file without the indexer having hidden the
	// base's yet: the highest layer of the view answers.
	apply(t, g, facts(branch, "o.go", "go", "pkg", sym("o#T", cg.SymbolStruct, "T", "pkg.T", 3, 30)))
	if got := symbolLayer(t, g, vBranch, "o#T"); got != branch {
		t.Errorf("Symbol o#T in the branch view from layer %s, want %s", got, branch)
	}
	if got := symbolLayer(t, g, vBase, "o#T"); got != base {
		t.Errorf("Symbol o#T in the base view from layer %s, want %s", got, base)
	}
	bq, err := g.Symbol(ctx, vBranch, cg.SymbolKey{QualName: "pkg.T"})
	if err != nil || bq.Symbol.Layer != branch || bq.Symbol.Start != 3 || len(bq.Children) != 0 {
		t.Errorf("Symbol pkg.T in the branch view = %+v, %v", bq, err)
	}

	_, err = g.Symbol(ctx, vBase, cg.SymbolKey{ID: "nope"})
	wantErr(t, "Symbol of a missing id", err, cg.ErrNotFound)
	_, err = g.Symbol(ctx, vBase, cg.SymbolKey{QualName: "pkg.Nope"})
	wantErr(t, "Symbol of a missing qualName", err, cg.ErrNotFound)
	_, err = g.Symbol(ctx, vBase, cg.SymbolKey{})
	wantErr(t, "Symbol without a key", err, cg.ErrInvalid)
}

func testNeighborsSymbols(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base)
	apply(t, g,
		facts(base, "t.go", "go", "t",
			sym("t#Target", cg.SymbolFunction, "Target", "t.Target", 10, 20),
			sym("t#Iface", cg.SymbolInterface, "Iface", "t.Iface", 30, 40)),
		facts(base, "c1.go", "go", "c", sym("c1#Call", cg.SymbolFunction, "Call", "c.Call", 5, 9)),
		facts(base, "c2.go", "go", "c",
			sym("c2#Late", cg.SymbolFunction, "Late", "c.Late", 30, 39),
			sym("c2#Early", cg.SymbolFunction, "Early", "c.Early", 1, 9)),
		facts(base, "r.go", "go", "r",
			sym("r#Ref", cg.SymbolVar, "Ref", "r.Ref", 1, 1),
			sym("r#Impl", cg.SymbolStruct, "Impl", "r.Impl", 3, 9),
			sym("r#Sub", cg.SymbolInterface, "Sub", "r.Sub", 11, 15)))
	call, late, early := ref(base, "c1#Call"), ref(base, "c2#Late"), ref(base, "c2#Early")
	rf, impl, sub := ref(base, "r#Ref"), ref(base, "r#Impl"), ref(base, "r#Sub")
	target, iface := ref(base, "t#Target"), ref(base, "t#Iface")
	e := edge(cg.EdgeCalls, base, call, target, 7)
	e.Confidence, e.Count = cg.ConfidenceQualified, 2
	putEdges(t, g, base, []cg.NodeRef{call, late, early, rf, impl, sub},
		e,
		edge(cg.EdgeCalls, base, late, target, 33),
		edge(cg.EdgeCalls, base, early, target, 2),
		edge(cg.EdgeReferences, base, rf, target, 1),
		edge(cg.EdgeImplements, base, impl, iface, 3),
		edge(cg.EdgeExtends, base, sub, iface, 11))

	// Callers, ordered by the caller's path then start.
	rows := neighbors(t, g, vBase, cg.NeighborQuery{ID: "t#Target", Relation: cg.RelCallers})
	wantStrings(t, "callers", rowKeys(rows), []string{
		base + "/c1#Call>" + base + "/t#Target@1",
		base + "/c2#Early>" + base + "/t#Target@1",
		base + "/c2#Late>" + base + "/t#Target@1"})
	if len(rows) > 0 {
		r := rows[0]
		wantFrom := cg.NodeSummary{Label: cg.LabelSymbol, Layer: base, ID: "c1#Call", Kind: cg.SymbolFunction,
			Name: "Call", QualName: "c.Call", Path: "c1.go", Start: 5}
		wantTo := cg.NodeSummary{Label: cg.LabelSymbol, Layer: base, ID: "t#Target", Kind: cg.SymbolFunction,
			Name: "Target", QualName: "t.Target", Path: "t.go", Start: 10}
		if r.From != wantFrom || r.To != wantTo || r.Relation != cg.RelCallers || r.Type != cg.EdgeCalls ||
			r.Path != "c1.go" || r.Line != 7 || r.Confidence != cg.ConfidenceQualified || r.Depth != 1 {
			t.Errorf("caller row = %+v", r)
		}
	}
	wantStrings(t, "callers limit 1", rowKeys(neighbors(t, g, vBase, cg.NeighborQuery{ID: "t#Target", Relation: cg.RelCallers, Limit: 1})),
		[]string{base + "/c1#Call>" + base + "/t#Target@1"})
	cr := neighbors(t, g, vBase, cg.NeighborQuery{ID: "c2#Late", Relation: cg.RelCallees})
	if len(cr) != 1 || cr[0].Relation != cg.RelCallees || cr[0].Path != "c2.go" || cr[0].Line != 33 || cr[0].To.ID != "t#Target" {
		t.Errorf("callees of Late = %+v", cr)
	}
	wantStrings(t, "references", walk(t, g, vBase, cg.RelReferences, "t#Target", 1), []string{base + "/r#Ref>" + base + "/t#Target@1"})
	impls := neighbors(t, g, vBase, cg.NeighborQuery{ID: "t#Iface", Relation: cg.RelImplementations})
	wantStrings(t, "implementations", rowKeys(impls), []string{
		base + "/r#Impl>" + base + "/t#Iface@1",
		base + "/r#Sub>" + base + "/t#Iface@1"})
	if len(impls) == 2 && (impls[0].Type != cg.EdgeImplements || impls[1].Type != cg.EdgeExtends) {
		t.Errorf("implementation types = %s, %s", impls[0].Type, impls[1].Type)
	}
	wantStrings(t, "callees of a leaf", walk(t, g, vBase, cg.RelCallees, "t#Target", 1), nil)

	_, err := g.Neighbors(ctx, vBase, cg.NeighborQuery{ID: "nope", Relation: cg.RelCallers})
	wantErr(t, "Neighbors of a missing symbol", err, cg.ErrNotFound)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{ID: "t#Target", Relation: "friends"})
	wantErr(t, "Neighbors with a bad relation", err, cg.ErrInvalid)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{ID: "t#Target", Relation: cg.RelCallers, Depth: cg.MaxDepth + 1})
	wantErr(t, "Neighbors deeper than MaxDepth", err, cg.ErrInvalid)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{Relation: cg.RelCallers})
	wantErr(t, "Neighbors without a start", err, cg.ErrInvalid)
}

func testNeighborsDepth(t *testing.T, g cg.Graph) {
	putLayers(t, g, base)
	var syms []cg.Symbol
	for i, n := range []string{"A", "B", "C", "D", "E", "X", "Y"} {
		syms = append(syms, sym("ch#"+n, cg.SymbolFunction, n, "ch."+n, 10*i+1, 10*i+9))
	}
	apply(t, g, facts(base, "ch.go", "go", "ch", syms...))
	r := func(n string) cg.NodeRef { return ref(base, "ch#"+n) }
	putEdges(t, g, base, []cg.NodeRef{r("A"), r("B"), r("C"), r("D"), r("X"), r("Y")},
		edge(cg.EdgeCalls, base, r("A"), r("B"), 2),
		edge(cg.EdgeCalls, base, r("B"), r("C"), 12),
		edge(cg.EdgeCalls, base, r("C"), r("D"), 22),
		edge(cg.EdgeCalls, base, r("D"), r("E"), 32),
		edge(cg.EdgeCalls, base, r("X"), r("Y"), 52),
		edge(cg.EdgeCalls, base, r("Y"), r("X"), 62),
		edge(cg.EdgeReferences, base, r("A"), r("C"), 3))
	k := func(from, to string, d int) string {
		return fmt.Sprintf("%s/ch#%s>%s/ch#%s@%d", base, from, base, to, d)
	}
	wantStrings(t, "callees depth 0", walk(t, g, vBase, cg.RelCallees, "ch#A", 0), []string{k("A", "B", 1)})
	wantStrings(t, "callees depth 1", walk(t, g, vBase, cg.RelCallees, "ch#A", 1), []string{k("A", "B", 1)})
	wantStrings(t, "callees depth 2", walk(t, g, vBase, cg.RelCallees, "ch#A", 2), []string{k("A", "B", 1), k("B", "C", 2)})
	wantStrings(t, "callees depth 3", walk(t, g, vBase, cg.RelCallees, "ch#A", 3),
		[]string{k("A", "B", 1), k("B", "C", 2), k("C", "D", 3)})
	wantStrings(t, "callers depth 3", walk(t, g, vBase, cg.RelCallers, "ch#E", 3),
		[]string{k("D", "E", 1), k("C", "D", 2), k("B", "C", 3)})
	// A cycle yields each edge once.
	wantStrings(t, "callees of a cycle", walk(t, g, vBase, cg.RelCallees, "ch#X", 3), []string{k("X", "Y", 1), k("Y", "X", 2)})
	// A walk follows only its relation's edge types.
	wantStrings(t, "references depth 3", walk(t, g, vBase, cg.RelReferences, "ch#C", 3), []string{k("A", "C", 1)})
	wantStrings(t, "callees depth 3 limit 2",
		rowKeys(neighbors(t, g, vBase, cg.NeighborQuery{ID: "ch#A", Relation: cg.RelCallees, Depth: 3, Limit: 2})),
		[]string{k("A", "B", 1), k("B", "C", 2)})
}

func testNeighborsImports(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base, branch)
	a := withImports(facts(base, "a.go", "go", "a"),
		cg.Import{Package: "fmt", Kind: cg.PackageKindPackage},
		cg.Import{Package: "strings", Alias: "str"},
		cg.Import{Package: "util.h", Kind: cg.PackageKindHeader})
	a.PackageKind = cg.PackageKindModule
	apply(t, g, a,
		withImports(facts(base, "x.go", "go", "x"), cg.Import{Package: "a", Kind: cg.PackageKindCrate}, cg.Import{Package: "fmt"}),
		withImports(facts(branch, "b.go", "go", "b"), cg.Import{Package: "fmt", Kind: cg.PackageKindModule}))

	rows := neighbors(t, g, vBase, cg.NeighborQuery{Path: "a.go", Relation: cg.RelImports})
	wantStrings(t, "imports of a.go", rowKeys(rows), []string{
		base + "/a.go>" + base + "/fmt@1",
		base + "/a.go>" + base + "/strings@1",
		base + "/a.go>" + base + "/util.h@1"})
	if len(rows) == 3 {
		wantFrom := cg.NodeSummary{Label: cg.LabelFile, Layer: base, Name: "a.go", Path: "a.go"}
		for i, kind := range []string{cg.PackageKindPackage, cg.PackageKindPackage, cg.PackageKindHeader} {
			r := rows[i]
			if r.From != wantFrom || r.To.Label != cg.LabelPackage || r.To.Layer != base || r.To.Kind != kind ||
				r.Relation != cg.RelImports || r.Type != cg.EdgeImports || r.Path != "a.go" || r.Depth != 1 {
				t.Errorf("import row %d = %+v, want package kind %s", i, r, kind)
			}
		}
		if rows[1].Alias != "str" || rows[0].Alias != "" {
			t.Errorf("aliases = %q, %q", rows[0].Alias, rows[1].Alias)
		}
	}
	// A package keeps the kind it was created with.
	xr := neighbors(t, g, vBase, cg.NeighborQuery{Path: "x.go", Relation: cg.RelImports, Depth: 3})
	if len(xr) != 2 || xr[0].To.Name != "a" || xr[0].To.Kind != cg.PackageKindModule {
		t.Errorf("imports of x.go = %+v, want package a of kind module first", xr)
	}

	wantStrings(t, "importers of fmt in the base view",
		rowKeys(neighbors(t, g, vBase, cg.NeighborQuery{Package: "fmt", Relation: cg.RelImporters})),
		[]string{base + "/a.go>" + base + "/fmt@1", base + "/x.go>" + base + "/fmt@1"})
	ir := neighbors(t, g, vBranch, cg.NeighborQuery{Package: "fmt", Relation: cg.RelImporters})
	wantStrings(t, "importers of fmt in the branch view", rowKeys(ir), []string{
		base + "/a.go>" + base + "/fmt@1",
		branch + "/b.go>" + branch + "/fmt@1",
		base + "/x.go>" + base + "/fmt@1"})
	for _, r := range ir {
		if r.Relation != cg.RelImporters || r.From.Label != cg.LabelFile || r.To.Label != cg.LabelPackage {
			t.Errorf("importer row = %+v", r)
		}
		if r.From.Layer == branch && r.To.Kind != cg.PackageKindModule {
			t.Errorf("the branch's own fmt has kind %q, want %q", r.To.Kind, cg.PackageKindModule)
		}
	}
	wantStrings(t, "importers limit 1",
		rowKeys(neighbors(t, g, vBranch, cg.NeighborQuery{Package: "fmt", Relation: cg.RelImporters, Limit: 1})),
		[]string{base + "/a.go>" + base + "/fmt@1"})
	// A hidden importer drops out.
	setHidden(t, g, base, "x.go", branch)
	wantStrings(t, "importers of fmt with x.go hidden",
		rowKeys(neighbors(t, g, vBranch, cg.NeighborQuery{Package: "fmt", Relation: cg.RelImporters})),
		[]string{base + "/a.go>" + base + "/fmt@1", branch + "/b.go>" + branch + "/fmt@1"})
	_, err := g.Neighbors(ctx, vBranch, cg.NeighborQuery{Path: "x.go", Relation: cg.RelImports})
	wantErr(t, "imports of a hidden file", err, cg.ErrNotFound)

	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{Package: "nope", Relation: cg.RelImporters})
	wantErr(t, "importers of a missing package", err, cg.ErrNotFound)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{Path: "nope.go", Relation: cg.RelImports})
	wantErr(t, "imports of a missing file", err, cg.ErrNotFound)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{Relation: cg.RelImports})
	wantErr(t, "imports without a path", err, cg.ErrInvalid)
	_, err = g.Neighbors(ctx, vBase, cg.NeighborQuery{Relation: cg.RelImporters})
	wantErr(t, "importers without a package", err, cg.ErrInvalid)
}

func testOutline(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base, overlay)
	o := facts(base, "pkg/o.go", "go", "pkg",
		sym("o#F", cg.SymbolFunction, "F", "pkg.F", 22, 25),
		sym("o#T", cg.SymbolStruct, "T", "pkg.T", 1, 20),
		child(sym("o#T.M", cg.SymbolMethod, "M", "pkg.T.M", 5, 8), "o#T"),
		child(sym("o#T.f", cg.SymbolField, "f", "pkg.T.f", 2, 2), "o#T"))
	o.Symbols[0].Doc = "F does things."
	apply(t, g, o,
		facts(base, "pkg/a.go", "go", "pkg", sym("a#G", cg.SymbolFunction, "G", "pkg.G", 1, 2)),
		facts(base, "pkg/empty.go", "go", "pkg"),
		facts(base, "other/z.go", "go", "other", sym("z#Z", cg.SymbolFunction, "Z", "other.Z", 1, 2)))

	outs, err := g.Outline(ctx, vBase, cg.OutlineQuery{Path: "pkg/o.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 {
		t.Fatalf("Outline pkg/o.go = %d files, want 1", len(outs))
	}
	f := outs[0]
	if f.File.Layer != base || f.File.Path != "pkg/o.go" || f.File.Lang != "go" || f.File.Package != "pkg" {
		t.Errorf("outline file = %+v", f.File)
	}
	var tree func(ns []cg.OutlineNode) string
	tree = func(ns []cg.OutlineNode) string {
		var parts []string
		for _, n := range ns {
			s := n.Symbol.Name
			if len(n.Children) > 0 {
				s += "(" + tree(n.Children) + ")"
			}
			parts = append(parts, s)
		}
		return strings.Join(parts, " ")
	}
	if got := tree(f.Symbols); got != "T(f M) F" {
		t.Errorf("outline tree = %q, want %q", got, "T(f M) F")
	}
	if len(f.Symbols) == 2 {
		if s := f.Symbols[1].Symbol; s.Signature != "func F()" || s.Doc != "F does things." || s.End != 25 || s.Layer != base {
			t.Errorf("outline symbol = %+v", s)
		}
	}

	wantStrings(t, "outline of package pkg", outlinePaths(t, g, vBase, cg.OutlineQuery{Package: "pkg"}),
		[]string{base + ":pkg/a.go", base + ":pkg/empty.go", base + ":pkg/o.go"})
	pk, _ := g.Outline(ctx, vBase, cg.OutlineQuery{Package: "pkg"})
	if len(pk) == 3 && len(pk[1].Symbols) != 0 {
		t.Errorf("outline of an empty file = %+v", pk[1].Symbols)
	}
	// The overlay hides a.go and adds its own file to the package.
	apply(t, g, facts(overlay, "pkg/n.go", "go", "pkg", sym("n#N", cg.SymbolFunction, "N", "pkg.N", 1, 2)))
	setHidden(t, g, base, "pkg/a.go", overlay)
	wantStrings(t, "outline of package pkg in the overlay's view",
		outlinePaths(t, g, cg.View{base, overlay}, cg.OutlineQuery{Package: "pkg"}),
		[]string{base + ":pkg/empty.go", overlay + ":pkg/n.go", base + ":pkg/o.go"})
	_, err = g.Outline(ctx, cg.View{base, overlay}, cg.OutlineQuery{Path: "pkg/a.go"})
	wantErr(t, "Outline of a hidden file", err, cg.ErrNotFound)
	_, err = g.Outline(ctx, vBase, cg.OutlineQuery{Path: "pkg/n.go"})
	wantErr(t, "Outline of a file of another layer", err, cg.ErrNotFound)
	_, err = g.Outline(ctx, vBase, cg.OutlineQuery{Package: "nope"})
	wantErr(t, "Outline of a missing package", err, cg.ErrNotFound)
	_, err = g.Outline(ctx, vBase, cg.OutlineQuery{})
	wantErr(t, "Outline without a path or package", err, cg.ErrInvalid)
	_, err = g.Outline(ctx, vBase, cg.OutlineQuery{Path: "pkg/o.go", Package: "pkg"})
	wantErr(t, "Outline with a path and a package", err, cg.ErrInvalid)
}

func testSearchText(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base, overlay)
	alloc := sym("s#Allocate", cg.SymbolFunction, "Allocate", "store.Allocate", 1, 9)
	alloc.Signature, alloc.Doc = "func Allocate(n int) []int", "Allocate reserves sequence numbers for plans."
	reserve := sym("s#Reserve", cg.SymbolFunction, "Reserve", "store.Reserve", 11, 19)
	reserve.Signature, reserve.Doc = "func Reserve()", "Reserve keeps a seat."
	render := sym("p#Render", cg.SymbolMethod, "Render", "web.Page.Render", 1, 9)
	render.Signature, render.Doc = "func (p *Page) Render(w io.Writer)", "Render writes the page as markdown."
	md := sym("md#render_markdown", cg.SymbolFunction, "render_markdown", "md.render_markdown", 1, 9)
	md.Signature, md.Doc = "def render_markdown(text)", "Render markdown text."
	apply(t, g,
		facts(base, "store/seq.go", "go", "store", alloc, reserve),
		facts(base, "web/page.go", "go", "web", render),
		facts(base, "tools/md.py", "python", "md", md))

	wantStrings(t, "sequence", search(t, g, vBase, cg.TextQuery{Query: "sequence"}), []string{base + "/s#Allocate"})
	wantStrings(t, "mixed case, every term", search(t, g, vBase, cg.TextQuery{Query: "Sequence NUMBERS"}), []string{base + "/s#Allocate"})
	wantStrings(t, "terms in different symbols", search(t, g, vBase, cg.TextQuery{Query: "sequence seat"}), nil)
	wantStrings(t, "a name", search(t, g, vBase, cg.TextQuery{Query: "reserve"}), []string{base + "/s#Reserve"})
	wantStrings(t, "a signature", search(t, g, vBase, cg.TextQuery{Query: "writer"}), []string{base + "/p#Render"})
	wantStrings(t, "a prefix", search(t, g, vBase, cg.TextQuery{Query: "sequen*"}), []string{base + "/s#Allocate"})
	wantStrings(t, "no match", search(t, g, vBase, cg.TextQuery{Query: "zebra"}), nil)
	wantStrings(t, "render", sorted(search(t, g, vBase, cg.TextQuery{Query: "render"})),
		sorted([]string{base + "/p#Render", base + "/md#render_markdown"}))
	wantStrings(t, "langs", search(t, g, vBase, cg.TextQuery{Query: "markdown", Filter: cg.Filter{Langs: []string{"python"}}}),
		[]string{base + "/md#render_markdown"})
	wantStrings(t, "kinds", search(t, g, vBase, cg.TextQuery{Query: "render", Filter: cg.Filter{Kinds: []string{cg.SymbolMethod}}}),
		[]string{base + "/p#Render"})
	wantStrings(t, "paths", search(t, g, vBase, cg.TextQuery{Query: "render", Filter: cg.Filter{Paths: []string{"web"}}}),
		[]string{base + "/p#Render"})
	if n := len(search(t, g, vBase, cg.TextQuery{Query: "render", Limit: 1})); n != 1 {
		t.Errorf("render limit 1: %d hits", n)
	}
	hits, err := g.SearchText(ctx, vBase, cg.TextQuery{Query: "allocate"})
	if err != nil || len(hits) != 1 || hits[0].Score <= 0 || hits[0].Symbol.Signature != alloc.Signature ||
		hits[0].Symbol.Doc != alloc.Doc || hits[0].Symbol.Path != "store/seq.go" {
		t.Errorf("SearchText allocate = %+v, %v", hits, err)
	}
	// Query syntax characters are text, never an error.
	for _, q := range []string{`render) | @name:{x}`, `"page`, `-render ~markdown`, `a.b(c)`} {
		if _, err := g.SearchText(ctx, vBase, cg.TextQuery{Query: q}); err != nil {
			t.Errorf("SearchText %q: %v", q, err)
		}
	}

	// Visibility: the overlay replaces seq.go without Allocate's doc.
	apply(t, g, facts(overlay, "store/seq.go", "go", "store", sym("s#Allocate", cg.SymbolFunction, "Allocate", "store.Allocate", 1, 9)))
	setHidden(t, g, base, "store/seq.go", overlay)
	ov := cg.View{base, overlay}
	wantStrings(t, "sequence in the overlay's view", search(t, g, ov, cg.TextQuery{Query: "sequence"}), nil)
	wantStrings(t, "reserve in the overlay's view", search(t, g, ov, cg.TextQuery{Query: "reserve"}), nil)
	wantStrings(t, "allocate in the overlay's view", search(t, g, ov, cg.TextQuery{Query: "allocate"}), []string{overlay + "/s#Allocate"})
	wantStrings(t, "sequence in the base view", search(t, g, vBase, cg.TextQuery{Query: "sequence"}), []string{base + "/s#Allocate"})

	_, err = g.SearchText(ctx, vBase, cg.TextQuery{Query: "  "})
	wantErr(t, "SearchText without a query", err, cg.ErrInvalid)
}

// testReadsValidate: every read refuses a malformed view.
func testReadsValidate(t *testing.T, g cg.Graph) {
	ctx := context.Background()
	putLayers(t, g, base)
	apply(t, g, facts(base, "a.go", "go", "a", sym("a#A", cg.SymbolFunction, "A", "a.A", 1, 2)))
	for name, v := range map[string]cg.View{"empty": {}, "malformed": {"nope"}, "repeated": {base, base}} {
		_, err := g.FindSymbols(ctx, v, cg.SymbolQuery{Name: "A"})
		wantErr(t, name+" view: FindSymbols", err, cg.ErrInvalid)
		_, err = g.Symbol(ctx, v, cg.SymbolKey{ID: "a#A"})
		wantErr(t, name+" view: Symbol", err, cg.ErrInvalid)
		_, err = g.Neighbors(ctx, v, cg.NeighborQuery{ID: "a#A", Relation: cg.RelCallers})
		wantErr(t, name+" view: Neighbors", err, cg.ErrInvalid)
		_, err = g.Outline(ctx, v, cg.OutlineQuery{Path: "a.go"})
		wantErr(t, name+" view: Outline", err, cg.ErrInvalid)
		_, err = g.SearchText(ctx, v, cg.TextQuery{Query: "a"})
		wantErr(t, name+" view: SearchText", err, cg.ErrInvalid)
	}
	_, err := g.FindSymbols(ctx, vBase, cg.SymbolQuery{})
	wantErr(t, "FindSymbols without a name", err, cg.ErrInvalid)
	// A view may name layers the graph does not hold.
	wantStrings(t, "A in a view with unknown layers", findName(t, g, cg.View{base, other}, "A"), []string{base + "/a#A"})
}

func testConcurrentWrites(t *testing.T, g cg.Graph) {
	putLayers(t, g, base)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmt.Sprintf("f%d.go", i)
			errs <- g.ApplyFile(context.Background(), facts(base, p, "go", "p",
				sym(p+"#S", cg.SymbolFunction, "S", fmt.Sprintf("p.S%d", i), 1, 2)))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if fs := files(t, g, base); len(fs) != n {
		t.Errorf("%d files after concurrent writes, want %d", len(fs), n)
	}
	if got := findName(t, g, vBase, "S"); len(got) != n {
		t.Errorf("%d symbols S after concurrent writes, want %d", len(got), n)
	}
}

func testOpener(t *testing.T, newOpener Factory) {
	ctx := context.Background()
	o, g := open(t, newOpener)
	if !strings.HasPrefix(g.Key(), "u") {
		t.Errorf("Key = %q", g.Key())
	}
	for _, bad := range []string{"", "-x", "a/b", "a b", "u1__" + strings.Repeat("x", 300)} {
		if _, err := o.Open(ctx, bad); !errors.Is(err, cg.ErrInvalid) {
			t.Errorf("Open(%q): err = %v, want ErrInvalid", bad, err)
		}
	}
	putLayers(t, g, base)
	apply(t, g, facts(base, "a.go", "go", "a", sym("a#A", cg.SymbolFunction, "A", "a.A", 1, 2)))

	// The same key opens the same graph.
	same, err := o.Open(ctx, g.Key())
	if err != nil {
		t.Fatal(err)
	}
	if same.Key() != g.Key() {
		t.Errorf("reopened key = %q, want %q", same.Key(), g.Key())
	}
	wantStrings(t, "A through a second handle", findName(t, same, vBase, "A"), []string{base + "/a#A"})

	// Another key is another graph.
	otherKey := newKey(t) + "-2"
	og, err := o.Open(ctx, otherKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = og.Drop(context.Background()) })
	if og.Key() != otherKey {
		t.Errorf("Key = %q, want %q", og.Key(), otherKey)
	}
	wantStrings(t, "A in another graph", findName(t, og, vBase, "A"), nil)
	putLayers(t, og, base)
	apply(t, og, facts(base, "b.go", "go", "b", sym("b#B", cg.SymbolFunction, "B", "b.B", 1, 2)))
	wantStrings(t, "B in the first graph", findName(t, g, vBase, "B"), nil)
	if err := og.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	wantStrings(t, "A after dropping another graph", findName(t, g, vBase, "A"), []string{base + "/a#A"})

	// Dropping through one handle empties the other.
	if err := same.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if ls, err := g.Layers(ctx); err != nil || len(ls) != 0 {
		t.Errorf("Layers after a drop through another handle = %v, %v", ls, err)
	}
}
