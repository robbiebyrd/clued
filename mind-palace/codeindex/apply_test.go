package codeindex

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/falkor"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// fakeParse is the parser of these tests. Each line of a file is one fact:
// "package p", "import spec [alias]", "func Name", "type Name",
// "method Type.Name", "call Name"; "!fail", "!sleep", "!panic" and
// "!invalid" make it fail, stall, panic or return facts that break the
// contract.
func fakeParse(calls *atomic.Int64) ParseFunc {
	return func(path string, content []byte) (codeparse.Facts, error) {
		if calls != nil {
			calls.Add(1)
		}
		var f codeparse.Facts
		f.Lang = "go"
		ids := map[string]string{} // bare name → symbol id
		offset := 0
		for i, line := range strings.Split(string(content), "\n") {
			start := codeparse.Pos{Line: i + 1, Offset: offset}
			end := codeparse.Pos{Line: i + 1, Column: len(line), Offset: offset + len(line)}
			offset += len(line) + 1
			word, rest, _ := strings.Cut(line, " ")
			qual := func(name string) string {
				if f.Package == "" {
					return name
				}
				return f.Package + "." + name
			}
			addSym := func(kind, name, parent string) {
				id := codeparse.SymbolID(qual(name), 1)
				ids[name] = id
				f.Symbols = append(f.Symbols, codeparse.Symbol{ID: id, Kind: kind, Name: name[strings.LastIndex(name, ".")+1:],
					QualName: qual(name), Signature: line, Start: start, End: end, Parent: parent, Exported: true})
				f.Chunks = append(f.Chunks, codeparse.Chunk{ID: codeparse.ChunkID(id, i+1, 1), Kind: codeparse.ChunkSymbol,
					SymbolID: id, Start: start, End: end, Text: line, Hash: codeparse.HashText(line)})
			}
			switch word {
			case "!fail":
				return codeparse.Facts{}, errors.New("syntax error")
			case "!sleep":
				time.Sleep(300 * time.Millisecond)
			case "!panic":
				panic("parser bug")
			case "!invalid":
				f.Symbols = append(f.Symbols, codeparse.Symbol{ID: "dup", Kind: "function", Name: "x", Start: start, End: end},
					codeparse.Symbol{ID: "dup", Kind: "function", Name: "y", Start: start, End: end})
			case "package":
				f.Package = rest
			case "import":
				spec, alias, _ := strings.Cut(rest, " ")
				f.Imports = append(f.Imports, codeparse.Import{Spec: spec, Alias: alias, Start: start})
			case "func":
				addSym(codeparse.KindFunction, rest, "")
			case "type":
				addSym(codeparse.KindStruct, rest, "")
			case "method":
				typ, _, _ := strings.Cut(rest, ".")
				addSym(codeparse.KindMethod, rest, ids[typ])
			case "call":
				f.Refs = append(f.Refs, codeparse.Ref{Name: rest, Kind: codeparse.RefCall, Start: start})
			}
		}
		return f, nil
	}
}

// graphBackends are the graphs the apply tests run on: memgraph always,
// FalkorDB when MIND_PALACE_TEST_FALKORDB_URL is set.
func graphBackends(t *testing.T, run func(t *testing.T, graphs codegraph.Opener)) {
	t.Run("memgraph", func(t *testing.T) { run(t, memgraph.New()) })
	t.Run("falkordb", func(t *testing.T) {
		u := os.Getenv("MIND_PALACE_TEST_FALKORDB_URL")
		if u == "" {
			t.Skip("MIND_PALACE_TEST_FALKORDB_URL is not set")
		}
		o, err := falkor.New(u)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = o.Close() })
		run(t, o)
	})
}

// applyEnv is a Service with the FileApplier and the fake parser over a
// memcode store and graphs, on a project key of its own (so parallel runs
// on one FalkorDB never meet).
type applyEnv struct {
	*fixture
	project string
	graphs  codegraph.Opener
	g       codegraph.Graph
	calls   *atomic.Int64
}

func newApplyEnv(t *testing.T, graphs codegraph.Opener, mod func(*Options)) *applyEnv {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	e := &applyEnv{project: "u0123456789abcdef__apply-" + hex.EncodeToString(b), graphs: graphs, calls: &atomic.Int64{}}
	e.fixture = newFixture(t, func(o *Options) {
		o.Code = func(context.Context, string) (code.Store, error) { return e.store, nil }
		o.Graphs = graphs
		o.Apply = FileApplier{}
		o.Parse = fakeParse(e.calls)
		if mod != nil {
			mod(o)
		}
	})
	g, err := graphs.Open(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	e.g = g
	t.Cleanup(func() { _ = g.Drop(context.Background()) })
	return e
}

func (e *applyEnv) sync() {
	e.t.Helper()
	if _, err := e.svc.Sync(e.ctx, e.project); err != nil {
		e.t.Fatal(err)
	}
}

// plan is the selection the graph holds now.
func (e *applyEnv) plan() Plan {
	e.t.Helper()
	p, err := PlanFromGraph(e.ctx, e.g)
	if err != nil {
		e.t.Fatal(err)
	}
	return p
}

// write applies the move from prev to next as one batch, the way the
// Service does (layer nodes, Apply, dropped layers).
func (e *applyEnv) write(prev, next Plan) error {
	return e.svc.write(e.ctx, Batch{Project: e.project, Code: e.store, Graph: e.g, Prev: prev, Plan: next, Delta: Diff(prev, next)})
}

func (e *applyEnv) blob(content string) string {
	e.t.Helper()
	if err := e.store.PutBlob(e.ctx, code.Blob{SHA256: shaOf(content), Size: int64(len(content)), Content: []byte(content)}); err != nil {
		e.t.Fatal(err)
	}
	return shaOf(content)
}

// checkHidden checks every file's hiddenBy in the graph against the rule,
// computed independently of the applier: a file is hidden by every layer
// above it in a chain of parents that holds its path.
func (e *applyEnv) checkHidden() {
	e.t.Helper()
	p := e.plan()
	want := map[string]map[string][]string{}
	for _, l := range p.Layers {
		want[l.Layer.ID] = map[string][]string{}
		for _, f := range l.Files {
			want[l.Layer.ID][f.Path] = nil
		}
	}
	for _, h := range p.Layers {
		v, err := p.View(h.Layer.ID)
		if err != nil {
			continue // a parent the graph does not hold: nothing below it
		}
		for _, f := range h.Files {
			for _, a := range v[:len(v)-1] {
				if _, ok := want[a][f.Path]; ok {
					want[a][f.Path] = append(want[a][f.Path], h.Layer.ID)
				}
			}
		}
	}
	for _, l := range p.Layers {
		files, err := e.g.Files(e.ctx, l.Layer.ID)
		if err != nil {
			e.t.Fatal(err)
		}
		for _, f := range files {
			w := want[l.Layer.ID][f.Path]
			slices.Sort(w)
			got := append([]string(nil), f.HiddenBy...)
			slices.Sort(got)
			if len(w) == 0 && len(got) == 0 {
				continue
			}
			if !reflect.DeepEqual(got, w) {
				e.t.Errorf("hiddenBy of %s in %s = %v, want %v", f.Path, l.Layer.ID, got, w)
			}
		}
	}
}

// hidden returns the hiddenBy of path in layer.
func (e *applyEnv) hidden(layer, path string) []string {
	e.t.Helper()
	f := e.file(layer, path)
	return f.HiddenBy
}

func (e *applyEnv) file(layer, path string) codegraph.File {
	e.t.Helper()
	files, err := e.g.Files(e.ctx, layer)
	if err != nil {
		e.t.Fatalf("Files(%s): %v", layer, err)
	}
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	e.t.Fatalf("no file %s in %s", path, layer)
	return codegraph.File{}
}

// see reports, for each name, whether a symbol of that name is visible
// in the view, and in which layer.
func (e *applyEnv) see(view codegraph.View, names ...string) map[string]string {
	e.t.Helper()
	out := map[string]string{}
	for _, n := range names {
		syms, err := e.g.FindSymbols(e.ctx, view, codegraph.SymbolQuery{Name: n})
		if err != nil && !errors.Is(err, codegraph.ErrNotFound) {
			e.t.Fatalf("FindSymbols(%s): %v", n, err)
		}
		switch len(syms) {
		case 0:
			out[n] = "-"
		case 1:
			out[n] = syms[0].Layer
		default:
			out[n] = "many"
		}
	}
	return out
}

func (e *applyEnv) view(layers ...string) codegraph.View {
	e.t.Helper()
	v, err := codegraph.NewView(layers...)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

func wantSee(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: visible %v, want %v", what, got, want)
	}
}

func TestApplyThreeLayers(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, nil)
		m, x, y := wtID(1), wtID(2), wtID(3)
		for _, id := range []string{m, x, y} {
			e.worktree(id, "0123456789abcdef", "", "main")
		}
		a1, b1, c1, e1 := "package p\nfunc A\n", "package p\nfunc B\n", "package p\nfunc C\n", "package p\nfunc E\n"
		b2, d1 := "package p\nfunc B2\n", "package p\nimport fmt\nimport strings s\ntype D\nmethod D.Run\n"
		a3, d3, n1 := "package p\nfunc A3\n", "package p\nfunc D3\n", "package p\nfunc N\n"
		u, f1 := commitID("main"), commitID("f1")
		e.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true,
			Upserts: []code.Entry{clean("a.go", a1), clean("b.go", b1), clean("c.go", c1), clean("e.go", e1)}}, a1, b1, c1, e1)
		// x is a clean checkout of feat at f1: b.go changed, c.go deleted,
		// d.go added. y sits on the same commit with local edits: a.go and
		// d.go edited, e.go deleted, n.go new.
		e.commit(code.Change{Worktree: x, Ref: "feat", Head: f1, Upstream: f1, Full: true,
			Upserts: []code.Entry{clean("a.go", a1), clean("b.go", b2), clean("d.go", d1), clean("e.go", e1)}}, b2, d1)
		e.commit(code.Change{Worktree: y, Ref: "feat", Head: f1, Upstream: f1, SessionID: "s1", Full: true,
			Upserts: []code.Entry{dirty("a.go", a3, a1), clean("b.go", b2), dirty("d.go", d3, d1), dirty("n.go", n1, "")}}, a3, d3, n1)
		e.sync()

		br, ov := codegraph.BranchLayerID("feat", f1), codegraph.WorktreeLayerID(y, "feat")
		e.checkHidden()
		if got := e.hidden("base", "b.go"); !reflect.DeepEqual(got, []string{br}) {
			t.Errorf("base b.go hiddenBy = %v", got)
		}
		if got := e.hidden("base", "e.go"); !reflect.DeepEqual(got, []string{ov}) {
			t.Errorf("base e.go hiddenBy = %v (y's tombstone)", got)
		}
		if f := e.file(ov, "e.go"); !f.Deleted {
			t.Errorf("y's e.go = %+v, want a tombstone", f)
		}
		if f := e.file(br, "d.go"); f.Package != "p" || f.Lang != "go" || f.ParseError || f.SHA256 != shaOf(d1) {
			t.Errorf("branch d.go = %+v", f)
		}
		names := []string{"A", "B", "C", "E", "B2", "D", "Run", "A3", "D3", "N"}
		wantSee(t, "base", e.see(codegraph.BaseView(), names...), map[string]string{
			"A": "base", "B": "base", "C": "base", "E": "base", "B2": "-", "D": "-", "Run": "-", "A3": "-", "D3": "-", "N": "-"})
		wantSee(t, "branch", e.see(e.view("base", br), names...), map[string]string{
			"A": "base", "B": "-", "C": "-", "E": "base", "B2": br, "D": br, "Run": br, "A3": "-", "D3": "-", "N": "-"})
		wantSee(t, "x's overlay", e.see(e.view("base", br, codegraph.WorktreeLayerID(x, "feat")), names...), map[string]string{
			"A": "base", "B": "-", "C": "-", "E": "base", "B2": br, "D": br, "Run": br, "A3": "-", "D3": "-", "N": "-"})
		wantSee(t, "y's overlay", e.see(e.view("base", br, ov), names...), map[string]string{
			"A": "-", "B": "-", "C": "-", "E": "-", "B2": br, "D": "-", "Run": "-", "A3": ov, "D3": ov, "N": ov})

		// The structure under a symbol and the imports reached the graph
		// with graph ids.
		info, err := e.g.Symbol(e.ctx, e.view("base", br), codegraph.SymbolKey{ID: SymbolNodeID("d.go", "p.D")})
		if err != nil || len(info.Children) != 1 || info.Children[0].ID != SymbolNodeID("d.go", "p.D.Run") ||
			len(info.Chunks) != 1 || info.Chunks[0].ID != ChunkNodeID("d.go", "p.D#1") {
			t.Errorf("Symbol(d.go#p.D) = %+v, %v", info, err)
		}
		rows, err := e.g.Neighbors(e.ctx, e.view("base", br), codegraph.NeighborQuery{Path: "d.go", Relation: codegraph.RelImports})
		if err != nil || len(rows) != 2 {
			t.Errorf("imports of d.go = %+v, %v", rows, err)
		}

		// y reverts a.go, restores e.go and drops n.go: the base files come
		// back in its view.
		e.commit(code.Change{Worktree: y, Ref: "feat", Head: f1, Upstream: f1, SessionID: "s1", Full: true,
			Upserts: []code.Entry{clean("a.go", a1), clean("b.go", b2), dirty("d.go", d3, d1), clean("e.go", e1)}})
		e.sync()
		e.checkHidden()
		if got := e.hidden("base", "a.go"); len(got) != 0 {
			t.Errorf("base a.go hiddenBy after revert = %v", got)
		}
		wantSee(t, "y's overlay after revert", e.see(e.view("base", br, ov), names...), map[string]string{
			"A": "base", "B": "-", "C": "-", "E": "base", "B2": br, "D": "-", "Run": "-", "A3": "-", "D3": ov, "N": "-"})

		// main moves a.go on: feat still has the old version, which the
		// branch layer now holds and which hides the new base file.
		a4 := "package p\nfunc A4\n"
		u2 := commitID("main2")
		e.commit(code.Change{Worktree: m, Ref: "main", Head: u2, Upstream: u2, Full: true,
			Upserts: []code.Entry{clean("a.go", a4), clean("b.go", b1), clean("c.go", c1), clean("e.go", e1)}}, a4)
		e.sync()
		e.checkHidden()
		if f := e.file(br, "a.go"); f.SHA256 != shaOf(a1) {
			t.Errorf("branch a.go = %+v, want feat's version", f)
		}
		if got := e.hidden("base", "a.go"); !reflect.DeepEqual(got, []string{br}) {
			t.Errorf("base a.go hiddenBy = %v", got)
		}
		wantSee(t, "branch after main moved", e.see(e.view("base", br), "A", "A4"), map[string]string{"A": br, "A4": "-"})
		wantSee(t, "base after main moved", e.see(codegraph.BaseView(), "A", "A4"), map[string]string{"A": "-", "A4": "base"})
	})
}

func TestApplyHiddenByFollowsPlans(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, nil)
		sa, sa2, sb := e.blob("package p\nfunc A\n"), e.blob("package p\nfunc A2\n"), e.blob("package p\nfunc B\n")
		sc, sd, sd2 := e.blob("package p\nfunc C\n"), e.blob("package p\nfunc D\n"), e.blob("package p\nfunc D2\n")
		base := codegraph.Layer{ID: "base", Kind: codegraph.LayerBase, Ref: "main", Commit: "c1"}
		br1 := codegraph.Layer{ID: "branch:f@1", Kind: codegraph.LayerBranch, Parent: "base", Ref: "f", Commit: "1"}
		br2 := codegraph.Layer{ID: "branch:f@2", Kind: codegraph.LayerBranch, Parent: "base", Ref: "f", Commit: "2"}
		wt := codegraph.Layer{ID: "wt:wt_0000000000000001:f", Kind: codegraph.LayerWorktree, Parent: br1.ID, Owner: "0123456789abcdef", Ref: "f"}

		// b.go's overlay version has no known content: it still hides the
		// base file.
		p1 := Plan{DefaultBranch: "main", Layers: []LayerPlan{
			{Layer: base, Files: []FileSource{{Path: "a.go", SHA256: sa}, {Path: "b.go", SHA256: sb}}},
			{Layer: br1, Files: []FileSource{{Path: "a.go", SHA256: sa2}}},
			{Layer: wt, Files: []FileSource{{Path: "b.go", Lang: "go"}, {Path: "d.go", SHA256: sd2}}},
		}}
		if err := e.write(Plan{}, p1); err != nil {
			t.Fatal(err)
		}
		e.checkHidden()
		if f := e.file(wt.ID, "b.go"); f.SHA256 != "" || f.Lang != "go" || f.ParseError || f.Deleted {
			t.Errorf("content-less b.go = %+v", f)
		}
		wantSee(t, "overlay", e.see(e.view("base", br1.ID, wt.ID), "A", "A2", "B", "D2"),
			map[string]string{"A": "-", "A2": br1.ID, "B": "-", "D2": wt.ID})

		// The overlay moves onto a new branch layer (the old one goes); the
		// base gains c.go, deleted in the new branch layer, and d.go, which
		// the overlay already holds.
		wt2 := wt
		wt2.Parent = br2.ID
		p2 := Plan{DefaultBranch: "main", Layers: []LayerPlan{
			{Layer: base, Files: []FileSource{{Path: "a.go", SHA256: sa}, {Path: "b.go", SHA256: sb}, {Path: "c.go", SHA256: sc}, {Path: "d.go", SHA256: sd}}},
			{Layer: br2, Files: []FileSource{{Path: "c.go", Deleted: true}}},
			{Layer: wt2, Files: []FileSource{{Path: "b.go", Lang: "go"}, {Path: "d.go", SHA256: sd2}}},
		}}
		if err := e.write(e.plan(), p2); err != nil {
			t.Fatal(err)
		}
		e.checkHidden()
		if got := e.hidden("base", "a.go"); len(got) != 0 {
			t.Errorf("base a.go hiddenBy = %v after its branch layer was dropped", got)
		}
		if got := e.hidden("base", "c.go"); !reflect.DeepEqual(got, []string{br2.ID}) {
			t.Errorf("base c.go hiddenBy = %v", got)
		}
		if got := e.hidden("base", "d.go"); !reflect.DeepEqual(got, []string{wt.ID}) {
			t.Errorf("base d.go hiddenBy = %v", got)
		}
		wantSee(t, "branch", e.see(e.view("base", br2.ID), "A", "C", "D", "D2"),
			map[string]string{"A": "base", "C": "-", "D": "base", "D2": "-"})
		wantSee(t, "overlay", e.see(e.view("base", br2.ID, wt.ID), "A", "B", "C", "D", "D2"),
			map[string]string{"A": "base", "B": "-", "C": "-", "D": "-", "D2": wt.ID})

		// The overlay drops back to the base: its files hide only the
		// base, and the branch layer's tombstone keeps hiding c.go in the
		// branch's views.
		wt3 := wt
		wt3.Parent = "base"
		p3 := p2
		p3.Layers = []LayerPlan{p2.Layers[0], p2.Layers[1], {Layer: wt3, Files: []FileSource{{Path: "c.go", SHA256: sc}}}}
		if err := e.write(e.plan(), p3); err != nil {
			t.Fatal(err)
		}
		e.checkHidden()
		if got := e.hidden("base", "c.go"); !reflect.DeepEqual(got, []string{br2.ID, wt.ID}) {
			t.Errorf("base c.go hiddenBy = %v", got)
		}
		if got := e.hidden("base", "d.go"); len(got) != 0 {
			t.Errorf("base d.go hiddenBy = %v", got)
		}
		wantSee(t, "overlay on base", e.see(e.view("base", wt.ID), "B", "C", "D"),
			map[string]string{"B": "base", "C": wt.ID, "D": "base"})
	})
}

// failingOpener opens graphs whose ApplyFile fails once the budget runs out.
type failingOpener struct {
	codegraph.Opener
	budget *atomic.Int64
}

func (o failingOpener) Open(ctx context.Context, key string) (codegraph.Graph, error) {
	g, err := o.Opener.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return failingGraph{Graph: g, budget: o.budget}, nil
}

type failingGraph struct {
	codegraph.Graph
	budget *atomic.Int64
}

func (g failingGraph) ApplyFile(ctx context.Context, ff codegraph.FileFacts) error {
	if g.budget.Add(-1) < 0 {
		return codegraph.ErrUnavailable
	}
	return g.Graph.ApplyFile(ctx, ff)
}

func TestApplyInterruptedBatchIsCompletedByTheNextTurn(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		budget := &atomic.Int64{}
		budget.Store(1 << 30)
		e := newApplyEnv(t, failingOpener{Opener: graphs, budget: budget}, nil)
		m, y := wtID(1), wtID(2)
		e.worktree(m, "0123456789abcdef", "", "main")
		e.worktree(y, "0123456789abcdef", "", "main")
		a, b, c := "package p\nfunc A\n", "package p\nfunc B\n", "package p\nfunc C\n"
		u := commitID("main")
		e.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true,
			Upserts: []code.Entry{clean("a.go", a), clean("b.go", b), clean("c.go", c)}}, a, b, c)
		e.sync()
		a2, b2 := "package p\nfunc A2\n", "package p\nfunc B2\n"
		e.commit(code.Change{Worktree: y, Ref: "main", Head: u, Upstream: u, Full: true,
			Upserts: []code.Entry{dirty("a.go", a2, a), dirty("b.go", b2, b), clean("c.go", c)}}, a2, b2)
		budget.Store(1)
		if _, err := e.svc.Sync(e.ctx, e.project); !errors.Is(err, codegraph.ErrUnavailable) {
			t.Fatalf("Sync with a failing graph = %v", err)
		}
		budget.Store(1 << 30)
		e.sync()
		e.checkHidden()
		ov := codegraph.WorktreeLayerID(y, "main")
		wantSee(t, "overlay", e.see(e.view("base", ov), "A", "A2", "B", "B2", "C"),
			map[string]string{"A": "-", "A2": ov, "B": "-", "B2": ov, "C": "base"})
	})
}

func TestApplyTextFallback(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, func(o *Options) { o.Apply = FileApplier{ParseTimeout: 50 * time.Millisecond} })
		m := wtID(1)
		e.worktree(m, "0123456789abcdef", "", "main")
		ok, fail, slow, boom, bad := "package p\nfunc OK\n", "!fail\nfunc F\n", "!sleep\nfunc S\n", "!panic\n", "!invalid\n"
		u := commitID("main")
		e.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{
			clean("ok.go", ok), clean("fail.go", fail), clean("fail2.go", fail), clean("slow.go", slow), clean("slow2.go", slow),
			clean("boom.go", boom), clean("bad.go", bad)}}, ok, fail, slow, boom, bad)
		e.sync()
		for _, p := range []string{"fail.go", "fail2.go", "slow.go", "slow2.go", "boom.go", "bad.go"} {
			if f := e.file("base", p); !f.ParseError || f.Lang != "go" || f.SHA256 == "" {
				t.Errorf("%s = %+v, want indexed as text", p, f)
			}
		}
		if f := e.file("base", "ok.go"); f.ParseError {
			t.Errorf("ok.go = %+v", f)
		}
		wantSee(t, "base", e.see(codegraph.BaseView(), "OK", "F", "S"), map[string]string{"OK": "base", "F": "-", "S": "-"})
		// ok, fail (once for both paths: a parse error is cached), slow
		// twice (a timeout is not), boom, bad.
		if n := e.calls.Load(); n != 6 {
			t.Errorf("parser calls = %d, want 6", n)
		}
	})
}

func TestApplyWithoutParserIndexesText(t *testing.T) {
	e := newApplyEnv(t, memgraph.New(), func(o *Options) { o.Parse = nil })
	m := wtID(1)
	e.worktree(m, "0123456789abcdef", "", "main")
	u := commitID("main")
	e.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true,
		Upserts: []code.Entry{clean("a.go", "package p\nfunc A\n")}}, "package p\nfunc A\n")
	e.sync()
	if f := e.file("base", "a.go"); !f.ParseError || f.Lang != "go" {
		t.Errorf("a.go = %+v", f)
	}
	wantSee(t, "base", e.see(codegraph.BaseView(), "A"), map[string]string{"A": "-"})
}

func TestTextFacts(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 130; i++ {
		sb.WriteString("line\n")
	}
	content := []byte(sb.String())
	f := TextFacts(content, codeparse.ParseTimeout)
	if err := f.Validate(); err != nil {
		t.Fatal(err)
	}
	var got [][2]int
	for _, c := range f.Chunks {
		if c.Kind != codeparse.ChunkWindow || c.SymbolID != "" {
			t.Errorf("chunk %+v", c)
		}
		got = append(got, [2]int{c.Start.Line, c.End.Line})
	}
	if want := [][2]int{{1, 60}, {51, 110}, {101, 130}}; !reflect.DeepEqual(got, want) {
		t.Errorf("windows = %v, want %v", got, want)
	}
	if f.Chunks[0].ID != "L1#1" || f.Chunks[1].Text != strings.Repeat("line\n", 60) || f.ParseError != codeparse.ParseTimeout {
		t.Errorf("facts = %+v", f.Chunks[0])
	}
	last := f.Chunks[2]
	if last.End.Offset != len(content)-1 || last.End.Column != 4 {
		t.Errorf("last window ends at %+v", last.End)
	}
	if c := TextFacts([]byte("one\ntwo"), codeparse.ParseFailed).Chunks; len(c) != 1 || c[0].End.Line != 2 || c[0].Text != "one\ntwo" {
		t.Errorf("short file = %+v", c)
	}
	if c := TextFacts(nil, codeparse.ParseFailed).Chunks; c != nil {
		t.Errorf("empty file = %+v", c)
	}
	if n := len(TextFacts([]byte(strings.Repeat("x\n", 60000)), codeparse.ParseFailed).Chunks); n != MaxChunksPerFile {
		t.Errorf("huge file has %d windows", n)
	}
}

func TestGraphFacts(t *testing.T) {
	facts, err := fakeParse(nil)("cli/run.go", []byte("package cli\nimport fmt\nimport fmt\nimport os o\ntype Server\nmethod Server.Run\ncall Println"))
	if err != nil {
		t.Fatal(err)
	}
	ff := GraphFacts("base", FileSource{Path: "cli/run.go", SHA256: shaOf("x"), GitOID: oid("x"), Lang: "golang"}, facts)
	if err := ff.Validate(); err != nil {
		t.Fatal(err)
	}
	if ff.File.Lang != "go" || ff.File.Package != "cli" || ff.PackageKind != codegraph.PackageKindPackage || ff.File.HiddenBy != nil {
		t.Errorf("file = %+v kind %q", ff.File, ff.PackageKind)
	}
	want := []codegraph.Symbol{
		{Layer: "base", ID: "cli/run.go#cli.Server", Kind: "struct", Name: "Server", QualName: "cli.Server",
			Signature: "type Server", Path: "cli/run.go", Start: 5, End: 5, Exported: true},
		{Layer: "base", ID: "cli/run.go#cli.Server.Run", Kind: "method", Name: "Run", QualName: "cli.Server.Run",
			Signature: "method Server.Run", Path: "cli/run.go", Start: 6, End: 6, Exported: true, Parent: "cli/run.go#cli.Server"},
	}
	if !reflect.DeepEqual(ff.Symbols, want) {
		t.Errorf("symbols = %+v", ff.Symbols)
	}
	if len(ff.Chunks) != 2 || ff.Chunks[1].ID != "cli/run.go#cli.Server.Run#1" || ff.Chunks[1].Symbol != "cli/run.go#cli.Server.Run" {
		t.Errorf("chunks = %+v", ff.Chunks)
	}
	if wantIm := []codegraph.Import{{Package: "fmt", Kind: "package"}, {Package: "os", Kind: "package", Alias: "o"}}; !reflect.DeepEqual(ff.Imports, wantIm) {
		t.Errorf("imports = %+v", ff.Imports)
	}
	tomb := GraphFacts("base", FileSource{Path: "x.go", Deleted: true}, facts)
	if err := tomb.Validate(); err != nil || !tomb.File.Deleted || len(tomb.Symbols) > 0 {
		t.Errorf("tombstone = %+v, %v", tomb, err)
	}
	text := GraphFacts("base", FileSource{Path: "x.txt", SHA256: shaOf("t")}, TextFacts([]byte("t"), codeparse.ParseNoGrammar))
	if !text.File.ParseError || text.File.Lang != "" || len(text.Chunks) != 1 || text.Chunks[0].ID != "x.txt#L1#1" {
		t.Errorf("text = %+v", text)
	}
}

func TestPackageKind(t *testing.T) {
	for _, c := range []struct{ lang, imp, want string }{
		{"go", "", codegraph.PackageKindPackage},
		{"python", "", codegraph.PackageKindModule},
		{"typescript", "", codegraph.PackageKindModule},
		{"csharp", "using", codegraph.PackageKindNamespace},
		{"rust", "use", codegraph.PackageKindCrate},
		{"c", "include", codegraph.PackageKindHeader},
		{"cpp", "", codegraph.PackageKindHeader},
		{"swift", "include", codegraph.PackageKindHeader},
		{"sql", "", codegraph.PackageKindSchema},
		{"", "", codegraph.PackageKindPackage},
	} {
		if got := PackageKind(c.lang, c.imp); got != c.want {
			t.Errorf("PackageKind(%q, %q) = %q, want %q", c.lang, c.imp, got, c.want)
		}
	}
}
