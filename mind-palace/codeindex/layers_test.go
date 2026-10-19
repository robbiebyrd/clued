package codeindex

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

const project = "u0123456789abcdef__github.com__o__r"

// oid is a fake git blob id for a committed version label.
func oid(label string) string {
	s := sha1.Sum([]byte(label))
	return hex.EncodeToString(s[:])
}

// commit is a fake commit id.
func commitID(label string) string { return oid("commit " + label) }

func shaOf(content string) string {
	s := sha256.Sum256([]byte(content))
	return hex.EncodeToString(s[:])
}

func wtID(n int) string { return fmt.Sprintf("wt_%016x", n) }

// fixture is a recorded change log in a memcode store, replayed by a
// Service into a memory graph.
type fixture struct {
	t     *testing.T
	ctx   context.Context
	store *memcode.Store
	graph *memgraph.Opener
	curs  *MemCursors
	rec   *recorder
	svc   *Service
}

// recorder is an Applier that writes each delta's files as bare File nodes
// (no facts) and remembers the batches, so tests see both the selection and
// what reached the graph.
type recorder struct {
	mu      sync.Mutex
	batches []Batch
	fail    error
}

func (r *recorder) Apply(ctx context.Context, s *Service, b Batch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail != nil {
		return r.fail
	}
	r.batches = append(r.batches, b)
	for _, ld := range b.Delta.Layers {
		for _, f := range ld.Put {
			ff := codegraph.FileFacts{File: codegraph.File{Layer: ld.Layer.ID, Path: f.Path, SHA256: f.SHA256,
				GitOID: f.GitOID, Lang: f.Lang, Deleted: f.Deleted}}
			if err := b.Graph.ApplyFile(ctx, ff); err != nil {
				return err
			}
		}
		for _, p := range ld.Remove {
			if err := b.Graph.RemoveFile(ctx, ld.Layer.ID, p); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *recorder) last() Batch {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.batches) == 0 {
		return Batch{}
	}
	return r.batches[len(r.batches)-1]
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.batches)
}

func newFixture(t *testing.T, mod func(*Options)) *fixture {
	t.Helper()
	f := &fixture{t: t, ctx: context.Background(), store: memcode.New(), graph: memgraph.New(),
		curs: NewMemCursors(nil), rec: &recorder{}}
	opts := Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code: func(_ context.Context, p string) (code.Store, error) {
			if p != project {
				return nil, code.ErrNotFound
			}
			return f.store, nil
		},
		Graphs:  f.graph,
		Cursors: f.curs,
		Apply:   f.rec,
		Holder:  "test",
	}
	if mod != nil {
		mod(&opts)
	}
	svc, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	f.svc = svc
	return f
}

func (f *fixture) worktree(id, owner, source, def string) {
	f.t.Helper()
	if err := f.store.PutWorktree(f.ctx, code.Worktree{ID: id, Owner: owner, Source: source, DefaultBranch: def}); err != nil {
		f.t.Fatal(err)
	}
}

// commit records a change, uploading the blobs its entries name (the
// contents table maps a sha back to its text).
func (f *fixture) commit(ch code.Change, contents ...string) int64 {
	f.t.Helper()
	for _, c := range contents {
		if err := f.store.PutBlob(f.ctx, code.Blob{SHA256: shaOf(c), Size: int64(len(c)), Content: []byte(c)}); err != nil {
			f.t.Fatal(err)
		}
	}
	seq, err := f.store.Commit(f.ctx, ch)
	if err != nil {
		f.t.Fatal(err)
	}
	return seq
}

func (f *fixture) sync() Result {
	f.t.Helper()
	res, err := f.svc.Sync(f.ctx, project)
	if err != nil {
		f.t.Fatal(err)
	}
	return res
}

func (f *fixture) plan() Plan { return f.rec.last().Plan }

// clean is a committed, pushed file: its content is its git version.
func clean(path, content string) code.Entry {
	return code.Entry{Path: path, SHA256: shaOf(content), Size: int64(len(content)), GitOID: oid(content), Mode: "100644", Lang: "go"}
}

// dirty is an edited file whose HEAD version is head ("" for untracked).
func dirty(path, content, head string) code.Entry {
	e := code.Entry{Path: path, SHA256: shaOf(content), Size: int64(len(content)), Dirty: true, Mode: "100644"}
	if head != "" {
		e.GitOID = oid(head)
	}
	return e
}

// unpushed is a committed file whose path differs from the upstream.
func unpushed(path, content string) code.Entry {
	e := clean(path, content)
	e.Unpushed = true
	return e
}

func files(l LayerPlan) map[string]FileSource {
	m := map[string]FileSource{}
	for _, f := range l.Files {
		m[f.Path] = f
	}
	return m
}

func layerIDs(p Plan) []string {
	var ids []string
	for _, l := range p.Layers {
		ids = append(ids, l.Layer.ID)
	}
	return ids
}

func mustLayer(t *testing.T, p Plan, id string) LayerPlan {
	t.Helper()
	l, ok := p.Layer(id)
	if !ok {
		t.Fatalf("no layer %s in %v", id, layerIDs(p))
	}
	return l
}

// wantFiles checks a layer's files: path → sha of its content, or "-" for a
// tombstone, or "?" for a file whose content is unknown.
func wantFiles(t *testing.T, l LayerPlan, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, f := range l.Files {
		switch {
		case f.Deleted:
			got[f.Path] = "-"
		case f.SHA256 == "":
			got[f.Path] = "?"
		default:
			got[f.Path] = f.SHA256
		}
	}
	w := map[string]string{}
	for p, c := range want {
		if c == "-" || c == "?" {
			w[p] = c
		} else {
			w[p] = shaOf(c)
		}
	}
	if !reflect.DeepEqual(got, w) {
		t.Errorf("layer %s files = %v, want %v", l.Layer.ID, got, w)
	}
}

func TestBaseFromCleanPushedDefaultBranchEntries(t *testing.T) {
	f := newFixture(t, nil)
	a, b := wtID(1), wtID(2)
	f.worktree(a, "u1", "", "main")
	f.worktree(b, "u2", "", "main")
	u1 := commitID("u1")
	// Session is stamped by server/codeapi from the change's SessionID.
	ups := []code.Entry{clean("a.go", "a1"), dirty("b.go", "b2", "b1"), unpushed("c.go", "c2"), dirty("d.txt", "scratch", "")}
	for i := range ups {
		ups[i].Session = "s-a"
	}
	f.commit(code.Change{Owner: "u1", Worktree: a, Ref: "main", Head: commitID("ha"), Upstream: u1, Ahead: 1, SessionID: "s-a", Full: true,
		Upserts: ups}, "a1", "b2", "c2", "scratch")
	f.commit(code.Change{Owner: "u2", Worktree: b, Ref: "main", Head: u1, Upstream: u1, SessionID: "s-b", Full: true,
		Upserts: []code.Entry{clean("a.go", "a1"), clean("b.go", "b1")}}, "a1", "b1")
	f.sync()
	p := f.plan()

	if p.DefaultBranch != "main" {
		t.Errorf("default branch = %q", p.DefaultBranch)
	}
	wantIDs := []string{"base", codegraph.WorktreeLayerID(a, "main"), codegraph.WorktreeLayerID(b, "main")}
	if got := layerIDs(p); !reflect.DeepEqual(got, wantIDs) {
		t.Fatalf("layers = %v, want %v", got, wantIDs)
	}
	base := mustLayer(t, p, "base")
	if base.Layer.Commit != u1 || base.Layer.Ref != "main" || base.Layer.Kind != codegraph.LayerBase {
		t.Errorf("base layer = %+v", base.Layer)
	}
	// b.go's committed content comes from b's clean entry; a's dirty edit,
	// its unpushed commit and its untracked file stay out of the base.
	wantFiles(t, base, map[string]string{"a.go": "a1", "b.go": "b1"})

	ova := mustLayer(t, p, codegraph.WorktreeLayerID(a, "main"))
	if ova.Layer.Parent != "base" || ova.Layer.Owner != "u1" || ova.Layer.Kind != codegraph.LayerWorktree {
		t.Errorf("overlay a = %+v", ova.Layer)
	}
	wantFiles(t, ova, map[string]string{"b.go": "b2", "c.go": "c2", "d.txt": "scratch"})
	for _, fs := range ova.Files {
		if fs.Session != "s-a" {
			t.Errorf("overlay file %s session = %q, want s-a", fs.Path, fs.Session)
		}
	}
	if fb := files(ova)["b.go"]; fb.GitOID != oid("b1") {
		t.Errorf("dirty b.go keeps its HEAD oid: %q", fb.GitOID)
	}
	wantFiles(t, mustLayer(t, p, codegraph.WorktreeLayerID(b, "main")), map[string]string{})

	// The layer nodes reached the graph.
	g, _ := f.graph.Open(f.ctx, project)
	ls, _ := g.Layers(f.ctx)
	if len(ls) != 3 {
		t.Errorf("graph layers = %+v", ls)
	}
}

func TestBaseKnownOnlyThroughDirtyCheckoutHasNoContent(t *testing.T) {
	f := newFixture(t, nil)
	a := wtID(1)
	f.worktree(a, "u1", "", "main")
	f.commit(code.Change{Owner: "u1", Worktree: a, Ref: "main", Head: commitID("h"), Upstream: commitID("h"), Full: true,
		Upserts: []code.Entry{dirty("x.go", "edit", "x1")}}, "edit")
	f.sync()
	base := mustLayer(t, f.plan(), "base")
	wantFiles(t, base, map[string]string{"x.go": "?"})
	if x := files(base)["x.go"]; x.GitOID != oid("x1") {
		t.Errorf("x.go oid = %q", x.GitOID)
	}
}

func TestNewestUpstreamWinsBase(t *testing.T) {
	f := newFixture(t, nil)
	a, b := wtID(1), wtID(2)
	f.worktree(a, "u1", "", "main")
	f.worktree(b, "u1", "", "main")
	u1, u2, u3 := commitID("1"), commitID("2"), commitID("3")
	f.commit(code.Change{Worktree: a, Ref: "main", Head: u1, Upstream: u1, Full: true, Upserts: []code.Entry{clean("x.go", "v1"), clean("old.go", "o")}}, "v1", "o")
	f.commit(code.Change{Worktree: b, Ref: "main", Head: u2, Upstream: u2, Full: true, Upserts: []code.Entry{clean("x.go", "v2")}}, "v2")
	f.sync()
	base := mustLayer(t, f.plan(), "base")
	if base.Layer.Commit != u2 {
		t.Fatalf("base commit = %s, want u2", base.Layer.Commit)
	}
	wantFiles(t, base, map[string]string{"x.go": "v2"})
	// a, still on the older upstream, sees the newer base minus what its
	// checkout does not have.
	wantFiles(t, mustLayer(t, f.plan(), codegraph.WorktreeLayerID(a, "main")), map[string]string{})

	// A later change at the old upstream does not move the base back.
	f.commit(code.Change{Worktree: a, Ref: "main", Head: u1, Upstream: u1, Upserts: []code.Entry{clean("y.go", "y")}}, "y")
	f.sync()
	if c := mustLayer(t, f.plan(), "base").Layer.Commit; c != u2 {
		t.Errorf("base commit after an old-upstream change = %s, want u2", c)
	}
	// A newer upstream moves it.
	f.commit(code.Change{Worktree: a, Ref: "main", Head: u3, Upstream: u3, Full: true, Upserts: []code.Entry{clean("x.go", "v3")}}, "v3")
	f.sync()
	base = mustLayer(t, f.plan(), "base")
	if base.Layer.Commit != u3 {
		t.Errorf("base commit = %s, want u3", base.Layer.Commit)
	}
	wantFiles(t, base, map[string]string{"x.go": "v3"})
	// The graph holds exactly the selection (the recorder wrote the delta).
	g, _ := f.graph.Open(f.ctx, project)
	fromGraph, err := PlanFromGraph(f.ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	if d := Diff(fromGraph, f.plan()); !d.Empty() {
		t.Errorf("graph differs from the selection: %+v", d)
	}
}

func TestBaseBlobChosenByMajority(t *testing.T) {
	f := newFixture(t, nil)
	u := commitID("1")
	// Three members committed one file (one git oid) but redacted it
	// differently: two sent r1, one r2.
	for i, content := range []string{"r2", "r1", "r1"} {
		id := wtID(i + 1)
		f.worktree(id, fmt.Sprintf("u%d", i), "", "main")
		e := code.Entry{Path: "cfg.go", SHA256: shaOf(content), Size: 2, GitOID: oid("orig")}
		f.commit(code.Change{Worktree: id, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{e}}, content)
	}
	f.sync()
	wantFiles(t, mustLayer(t, f.plan(), "base"), map[string]string{"cfg.go": "r1"})
}

func TestGitHubWorktreePreferredForBaseAndBranch(t *testing.T) {
	f := newFixture(t, nil)
	gh, a := "gh_42", wtID(1)
	f.worktree(gh, "u1", code.SourceGitHub, "main")
	f.worktree(a, "u1", "", "trunk") // a client disagreeing on the default branch
	hMain, fFeat := commitID("gh-main"), commitID("gh-feat")
	f.commit(code.Change{Worktree: gh, Ref: "main", Head: hMain, Full: true, Upserts: []code.Entry{clean("a.go", "gh"), clean("b.go", "b")}}, "gh", "b")
	f.commit(code.Change{Worktree: gh, Ref: "feat", Head: fFeat, Full: true, Upserts: []code.Entry{clean("a.go", "gh-feat"), clean("b.go", "b")}}, "gh-feat", "b")
	// The client is on main with a newer upstream and on feat at the same
	// commit as GitHub, with different (e.g. differently redacted) blobs.
	f.commit(code.Change{Worktree: a, Ref: "main", Head: commitID("c"), Upstream: commitID("c"), Full: true, Upserts: []code.Entry{clean("a.go", "client")}}, "client")
	f.commit(code.Change{Worktree: a, Ref: "feat", Head: fFeat, Upstream: fFeat, Full: true,
		Upserts: []code.Entry{{Path: "a.go", SHA256: shaOf("client-feat"), GitOID: oid("gh-feat"), Size: 11}, clean("b.go", "b"), dirty("n.go", "new", "")}}, "client-feat", "new")
	f.sync()
	p := f.plan()
	if p.DefaultBranch != "main" {
		t.Errorf("default branch = %q, want GitHub's main", p.DefaultBranch)
	}
	base := mustLayer(t, p, "base")
	if base.Layer.Commit != hMain || !reflect.DeepEqual(base.Sources, []TreeKey{{gh, "main"}}) {
		t.Errorf("base = %+v sources %v", base.Layer, base.Sources)
	}
	wantFiles(t, base, map[string]string{"a.go": "gh", "b.go": "b"})
	br := mustLayer(t, p, codegraph.BranchLayerID("feat", fFeat))
	if !reflect.DeepEqual(br.Sources, []TreeKey{{gh, "feat"}}) || br.Layer.Parent != "base" {
		t.Errorf("branch = %+v sources %v", br.Layer, br.Sources)
	}
	wantFiles(t, br, map[string]string{"a.go": "gh-feat"})
	// GitHub's worktree has no overlay; the client's feat overlay sits on
	// the branch layer.
	if _, ok := p.Layer(codegraph.WorktreeLayerID(gh, "main")); ok {
		t.Error("the GitHub worktree got an overlay")
	}
	ov := mustLayer(t, p, codegraph.WorktreeLayerID(a, "feat"))
	if ov.Layer.Parent != codegraph.BranchLayerID("feat", fFeat) {
		t.Errorf("overlay parent = %q", ov.Layer.Parent)
	}
	wantFiles(t, ov, map[string]string{"n.go": "new"})
	view, err := p.View(ov.Layer.ID)
	if err != nil || !reflect.DeepEqual(view, codegraph.View{"base", br.Layer.ID, ov.Layer.ID}) {
		t.Errorf("view = %v, %v", view, err)
	}
	// The client's main tree sits on the base (GitHub's), so its own
	// pushed commit is not a branch layer.
	for _, id := range layerIDs(p) {
		if id == codegraph.BranchLayerID("main", commitID("c")) {
			t.Error("default branch got a branch layer")
		}
	}
}

func TestBranchLayerHoldsDiffsAndDeletions(t *testing.T) {
	f := newFixture(t, nil)
	m, a, b := wtID(1), wtID(2), wtID(3)
	for _, id := range []string{m, a, b} {
		f.worktree(id, "u-"+id, "", "main")
	}
	u, f1 := commitID("main"), commitID("f1")
	f.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true,
		Upserts: []code.Entry{clean("a.go", "a1"), clean("b.go", "b1"), clean("c.go", "c1"), clean("u.go", "u1")}}, "a1", "b1", "c1", "u1")
	// Two checkouts on feat at the same pushed commit share one branch
	// layer: b.go changed, c.go deleted, d.go added; e.go is an unpushed
	// commit of a; u.go's pushed version is unknown (unpushed in a) and is
	// not deleted by the branch.
	f.commit(code.Change{Worktree: a, Ref: "feat", Head: commitID("fa"), Upstream: f1, Ahead: 1, SessionID: "sa", Full: true,
		Upserts: []code.Entry{clean("a.go", "a1"), clean("b.go", "b2"), clean("d.go", "d1"), unpushed("e.go", "e1"), unpushed("u.go", "u2")}},
		"b2", "d1", "e1", "u2")
	f.commit(code.Change{Worktree: b, Ref: "feat", Head: f1, Upstream: f1, SessionID: "sb", Full: true,
		Upserts: []code.Entry{clean("b.go", "b2"), dirty("d.go", "d-edit", "d1"), clean("u.go", "u1")}}, "d-edit")
	f.sync()
	p := f.plan()
	br := mustLayer(t, p, codegraph.BranchLayerID("feat", f1))
	if !reflect.DeepEqual(br.Sources, []TreeKey{{a, "feat"}, {b, "feat"}}) {
		t.Errorf("branch sources = %v", br.Sources)
	}
	wantFiles(t, br, map[string]string{"b.go": "b2", "c.go": "-", "d.go": "d1"})
	wantFiles(t, mustLayer(t, p, codegraph.WorktreeLayerID(a, "feat")), map[string]string{"e.go": "e1", "u.go": "u2"})
	// b deleted a.go locally (not committed): a tombstone in its overlay
	// only, since a's checkout still shows it pushed.
	wantFiles(t, mustLayer(t, p, codegraph.WorktreeLayerID(b, "feat")), map[string]string{"a.go": "-", "d.go": "d-edit"})
	if n := len(layerIDs(p)); n != 5 {
		t.Errorf("layers = %v", layerIDs(p))
	}
}

func TestOlderBranchLayerKeptWhileATreeSitsOnIt(t *testing.T) {
	f := newFixture(t, nil)
	a, b := wtID(1), wtID(2)
	f.worktree(a, "u1", "", "main")
	f.worktree(b, "u2", "", "main")
	f1, f2 := commitID("f1"), commitID("f2")
	f.commit(code.Change{Worktree: a, Ref: "feat", Head: f1, Upstream: f1, Full: true, Upserts: []code.Entry{clean("x.go", "x1")}}, "x1")
	f.commit(code.Change{Worktree: b, Ref: "feat", Head: f2, Upstream: f2, Full: true, Upserts: []code.Entry{clean("x.go", "x2")}}, "x2")
	f.sync()
	old, cur := codegraph.BranchLayerID("feat", f1), codegraph.BranchLayerID("feat", f2)
	wantFiles(t, mustLayer(t, f.plan(), old), map[string]string{"x.go": "x1"})
	wantFiles(t, mustLayer(t, f.plan(), cur), map[string]string{"x.go": "x2"})
	if p := mustLayer(t, f.plan(), codegraph.WorktreeLayerID(a, "feat")).Layer.Parent; p != old {
		t.Errorf("a's overlay sits on %q, want %q", p, old)
	}
	// a pulls: nothing sits on f1 any more, so its layer is dropped.
	f.commit(code.Change{Worktree: a, Ref: "feat", Head: f2, Upstream: f2, Full: true, Upserts: []code.Entry{clean("x.go", "x2")}})
	f.sync()
	if _, ok := f.plan().Layer(old); ok {
		t.Error("old branch layer still selected")
	}
	if d := f.rec.last().Delta; !reflect.DeepEqual(d.Dropped, []string{old}) {
		t.Errorf("dropped = %v", d.Dropped)
	}
	g, _ := f.graph.Open(f.ctx, project)
	ls, _ := g.Layers(f.ctx)
	for _, l := range ls {
		if l.ID == old {
			t.Error("old branch layer still in the graph")
		}
	}
}

func TestNeverPushedBranchOverlaySitsOnBase(t *testing.T) {
	f := newFixture(t, nil)
	m, a := wtID(1), wtID(2)
	f.worktree(m, "u1", "", "main")
	f.worktree(a, "u1", "", "main")
	u := commitID("main")
	f.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{clean("a.go", "a1"), clean("gone.go", "g")}}, "a1", "g")
	f.commit(code.Change{Worktree: a, Ref: "wip", Head: commitID("w"), Ahead: 3, Full: true,
		Upserts: []code.Entry{unpushed("a.go", "a1"), unpushed("n.go", "n")}}, "n")
	f.sync()
	p := f.plan()
	if got := layerIDs(p); !reflect.DeepEqual(got, []string{"base", codegraph.WorktreeLayerID(m, "main"), codegraph.WorktreeLayerID(a, "wip")}) {
		t.Fatalf("layers = %v (no branch layer for a branch never pushed)", got)
	}
	ov := mustLayer(t, p, codegraph.WorktreeLayerID(a, "wip"))
	if ov.Layer.Parent != "base" {
		t.Errorf("parent = %q", ov.Layer.Parent)
	}
	wantFiles(t, ov, map[string]string{"a.go": "a1", "n.go": "n", "gone.go": "-"})
}

func TestRemovedWorktreeDropsItsLayers(t *testing.T) {
	f := newFixture(t, nil)
	m, a := wtID(1), wtID(2)
	f.worktree(m, "u1", "", "main")
	f.worktree(a, "u1", "", "main")
	u, f1 := commitID("main"), commitID("f1")
	f.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{clean("a.go", "a1")}}, "a1")
	f.commit(code.Change{Worktree: a, Ref: "feat", Head: f1, Upstream: f1, Full: true, Upserts: []code.Entry{clean("a.go", "a2"), dirty("b.go", "b", "")}}, "a2", "b")
	f.sync()
	if n := len(f.plan().Layers); n != 4 {
		t.Fatalf("layers = %v", layerIDs(f.plan()))
	}
	if err := f.store.RemoveWorktree(f.ctx, a); err != nil {
		t.Fatal(err)
	}
	f.sync()
	if got := layerIDs(f.plan()); !reflect.DeepEqual(got, []string{"base", codegraph.WorktreeLayerID(m, "main")}) {
		t.Errorf("layers after removal = %v", got)
	}
	want := []string{codegraph.WorktreeLayerID(a, "feat"), codegraph.BranchLayerID("feat", f1)}
	if d := f.rec.last().Delta; !reflect.DeepEqual(d.Dropped, want) {
		t.Errorf("dropped = %v, want %v (higher layers first)", d.Dropped, want)
	}
}

func TestSyncBatchesCursorAndLease(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.BatchSize = 2 })
	a := wtID(1)
	f.worktree(a, "u1", "", "main")
	u := commitID("main")
	for i := range 5 {
		c := fmt.Sprintf("v%d", i)
		f.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Upserts: []code.Entry{clean(fmt.Sprintf("f%d.go", i), c)}}, c)
	}
	res := f.sync()
	if res.Seq != 5 || res.Changes != 5 || res.Batches != 3 || !res.CaughtUp {
		t.Errorf("result = %+v", res)
	}
	if n := len(mustLayer(t, f.plan(), "base").Files); n != 5 {
		t.Errorf("base files = %d", n)
	}
	l, cur, err := f.curs.Acquire(f.ctx, project, "other", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Seq != 5 || len(cur.Trees) != 1 || cur.Trees[0].Upstream != u || cur.Trees[0].Seq != 5 || cur.Trees[0].UpstreamSeq != 1 {
		t.Errorf("cursor = %+v", cur)
	}
	// Another worker holds the lease.
	if _, err := f.svc.Sync(f.ctx, project); !errors.Is(err, ErrLeased) {
		t.Errorf("Sync under another lease = %v, want ErrLeased", err)
	}
	_ = f.curs.Release(f.ctx, l)
	// Nothing new: no batch, nothing written.
	n := f.rec.count()
	if res := f.sync(); res.Batches != 0 || !res.CaughtUp || res.Seq != 5 {
		t.Errorf("idle result = %+v", res)
	}
	if f.rec.count() != n {
		t.Error("an idle sync applied a batch")
	}

	// A failing apply leaves the cursor where it was; the retry catches up.
	f.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Deletes: []string{"f0.go"}})
	f.rec.fail = codegraph.ErrUnavailable
	if _, err := f.svc.Sync(f.ctx, project); !errors.Is(err, codegraph.ErrUnavailable) {
		t.Fatalf("Sync = %v, want ErrUnavailable", err)
	}
	_, cur, _ = f.curs.Acquire(f.ctx, project, "probe", time.Second)
	if cur.Seq != 5 {
		t.Errorf("cursor moved to %d on a failed batch", cur.Seq)
	}
	f.curs.docs[project].lease = Lease{}
	f.rec.fail = nil
	if res := f.sync(); res.Seq != 6 {
		t.Errorf("retry result = %+v", res)
	}
	d := f.rec.last().Delta
	if len(d.Layers) != 1 || !reflect.DeepEqual(d.Layers[0].Remove, []string{"f0.go"}) || len(d.Layers[0].Put) != 0 {
		t.Errorf("retry delta = %+v", d)
	}
}

func TestNewServiceResumesFromGraphAndCursor(t *testing.T) {
	f := newFixture(t, nil)
	a := wtID(1)
	f.worktree(a, "u1", "", "main")
	u := commitID("main")
	f.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Full: true,
		Upserts: []code.Entry{clean("a.go", "a"), clean("b.go", "b")}}, "a", "b")
	f.sync()
	// A new process: same cursors and graph, fresh Service and recorder.
	rec := &recorder{}
	svc, err := New(Options{Code: func(context.Context, string) (code.Store, error) { return f.store, nil },
		Graphs: f.graph, Cursors: f.curs, Apply: rec, Holder: "second"})
	if err != nil {
		t.Fatal(err)
	}
	f.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Upserts: []code.Entry{clean("b.go", "b2")}}, "b2")
	if _, err := svc.Sync(f.ctx, project); err != nil {
		t.Fatal(err)
	}
	d := rec.last().Delta
	if len(d.Layers) != 1 || d.Layers[0].Layer.ID != "base" || len(d.Layers[0].Put) != 1 || d.Layers[0].Put[0].Path != "b.go" {
		t.Errorf("delta after restart = %+v, want only b.go in base", d)
	}
}

func TestNilApplierKeepsLayerNodes(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Apply = nil })
	a := wtID(1)
	f.worktree(a, "u1", "", "main")
	f1 := commitID("f1")
	f.commit(code.Change{Worktree: a, Ref: "feat", Head: f1, Upstream: f1, Full: true, Upserts: []code.Entry{clean("a.go", "a")}}, "a")
	f.sync()
	g, _ := f.graph.Open(f.ctx, project)
	ls, _ := g.Layers(f.ctx)
	var ids []string
	for _, l := range ls {
		ids = append(ids, l.ID)
		if l.Updated.IsZero() {
			t.Errorf("layer %s has no Updated", l.ID)
		}
	}
	// No client worktree on main: no base, the branch layer has no parent.
	want := []string{codegraph.BranchLayerID("feat", f1), codegraph.WorktreeLayerID(a, "feat")}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("graph layers = %v, want %v", ids, want)
	}
}

func TestDiff(t *testing.T) {
	base := codegraph.Layer{ID: "base", Kind: codegraph.LayerBase, Ref: "main", Commit: "c1"}
	ov := codegraph.Layer{ID: "wt:wt_1:main", Kind: codegraph.LayerWorktree, Parent: "base", Owner: "u"}
	prev := Plan{Layers: []LayerPlan{
		{Layer: base, Files: []FileSource{{Path: "a", SHA256: "1"}, {Path: "b", SHA256: "2"}}},
		{Layer: ov, Files: []FileSource{{Path: "x", SHA256: "9"}}},
	}}
	if d := Diff(prev, prev); !d.Empty() {
		t.Errorf("Diff(p, p) = %+v", d)
	}
	base2 := base
	base2.Commit = "c2"
	next := Plan{Layers: []LayerPlan{
		{Layer: base2, Files: []FileSource{{Path: "a", SHA256: "1", Lang: "go", Session: "ignored"}, {Path: "c", SHA256: "3"}}},
	}}
	d := Diff(prev, next)
	want := Delta{Layers: []LayerDelta{{Layer: base2, Put: []FileSource{{Path: "c", SHA256: "3"}}, Remove: []string{"b"}}},
		Dropped: []string{"wt:wt_1:main"}}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("Diff = %+v, want %+v", d, want)
	}
	if d := Diff(Plan{}, next); len(d.Layers) != 1 || !d.Layers[0].New || len(d.Layers[0].Put) != 2 {
		t.Errorf("Diff from empty = %+v", d)
	}
}

func TestPlanView(t *testing.T) {
	p := Plan{Layers: []LayerPlan{
		{Layer: codegraph.Layer{ID: "base", Kind: codegraph.LayerBase}},
		{Layer: codegraph.Layer{ID: "branch:f@c", Kind: codegraph.LayerBranch, Parent: "base"}},
		{Layer: codegraph.Layer{ID: "wt:wt_1:f", Kind: codegraph.LayerWorktree, Parent: "branch:f@c"}},
		{Layer: codegraph.Layer{ID: "wt:wt_2:f", Kind: codegraph.LayerWorktree, Parent: "branch:f@gone"}},
	}}
	v, err := p.View("wt:wt_1:f")
	if err != nil || !reflect.DeepEqual(v, codegraph.View{"base", "branch:f@c", "wt:wt_1:f"}) {
		t.Errorf("View = %v, %v", v, err)
	}
	if _, err := p.View("wt:wt_2:f"); !errors.Is(err, codegraph.ErrNotFound) {
		t.Errorf("View with a missing parent = %v", err)
	}
}

func TestFactsParsesOncePerContentAndVersion(t *testing.T) {
	var calls atomic.Int64
	version := "v1"
	f := newFixture(t, func(o *Options) {
		o.Parse = func(path string, content []byte) (codeparse.Facts, error) {
			calls.Add(1)
			return codeparse.Facts{Lang: "go", Package: string(content)}, nil
		}
		o.ParseVersion = func(string, []byte) string { return version }
	})
	sha := shaOf("package p")
	if err := f.store.PutBlob(f.ctx, code.Blob{SHA256: sha, Size: 9, Content: []byte("package p")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a.go", "b.go"} {
		facts, err := f.svc.Facts(f.ctx, f.store, FileSource{Path: p, SHA256: sha})
		if err != nil || facts.Package != "package p" {
			t.Fatalf("Facts(%s) = %+v, %v", p, facts, err)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("parsed %d times, want 1 (cached by content)", n)
	}
	version = "v2"
	if _, err := f.svc.Facts(f.ctx, f.store, FileSource{Path: "a.go", SHA256: sha}); err != nil || calls.Load() != 2 {
		t.Errorf("a version bump re-parses: calls %d, %v", calls.Load(), err)
	}
	if facts, err := f.svc.Facts(f.ctx, f.store, FileSource{Path: "a.go", Deleted: true}); err != nil || facts.Lang != "" {
		t.Errorf("tombstone facts = %+v, %v", facts, err)
	}
	if _, err := f.svc.Facts(f.ctx, f.store, FileSource{Path: "a.go", SHA256: shaOf("missing")}); !errors.Is(err, code.ErrNotFound) {
		t.Errorf("missing blob = %v", err)
	}
	g := newFixture(t, nil)
	if _, err := g.svc.Facts(g.ctx, g.store, FileSource{Path: "a.go", SHA256: sha}); !errors.Is(err, ErrNoParser) {
		t.Errorf("no parser = %v", err)
	}
}

func TestRunIndexesProjects(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.Interval = 5 * time.Millisecond; o.Workers = 2; o.BatchSize = 1; o.MaxBatches = 1 })
	a := wtID(1)
	f.worktree(a, "u1", "", "main")
	u := commitID("main")
	for i := range 3 {
		c := fmt.Sprintf("v%d", i)
		f.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Upserts: []code.Entry{clean("f.go", c)}}, c)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.svc.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l, cur, err := f.curs.Acquire(context.Background(), project, "probe", time.Millisecond)
		if err == nil {
			_ = f.curs.Release(context.Background(), l)
			if cur.Seq == 3 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run did not catch up: %+v %v", cur, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.svc.Kick(project)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Run = %v", err)
	}
	wantFiles(t, mustLayer(t, f.plan(), "base"), map[string]string{"f.go": "v2"})
}

func TestNewRequiresStores(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("New without stores succeeded")
	}
	s, err := New(Options{Code: func(context.Context, string) (code.Store, error) { return nil, nil },
		Graphs: memgraph.New(), Cursors: NewMemCursors(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background()); err == nil {
		t.Error("Run without Projects succeeded")
	}
}

// clock is a settable test clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// testCursors runs the lease and cursor rules against a Cursors.
func testCursors(t *testing.T, newCursors func(now func() time.Time) Cursors) {
	ctx := context.Background()
	c := &clock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	cs := newCursors(c.Now)
	const p = "u0000000000000001__x"

	l1, cur, err := cs.Acquire(ctx, p, "w1", time.Minute)
	if err != nil || cur.Seq != 0 || len(cur.Trees) != 0 || cur.Project != p {
		t.Fatalf("first Acquire = %+v, %v", cur, err)
	}
	if _, _, err := cs.Acquire(ctx, p, "w2", time.Minute); !errors.Is(err, ErrLeased) {
		t.Errorf("second holder = %v, want ErrLeased", err)
	}
	trees := []TreeState{{Worktree: "wt_1", Ref: "main", Owner: "u", Head: "h", Upstream: "u1", UpstreamSeq: 3, Seq: 7}}
	l1, err = cs.Save(ctx, l1, Cursor{Project: p, Seq: 7, Trees: trees}, time.Minute)
	if err != nil {
		t.Fatalf("Save = %v", err)
	}
	if _, err := cs.Save(ctx, l1, Cursor{Project: "other", Seq: 1}, time.Minute); err == nil {
		t.Error("Save of another project's cursor succeeded")
	}
	// The same holder may take a fresh lease; the old token is then lost.
	l1b, cur, err := cs.Acquire(ctx, p, "w1", time.Minute)
	if err != nil || cur.Seq != 7 || !reflect.DeepEqual(cur.Trees, trees) {
		t.Fatalf("re-Acquire = %+v, %v", cur, err)
	}
	if _, err := cs.Save(ctx, l1, Cursor{Project: p, Seq: 8}, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Save with a replaced lease = %v, want ErrLeaseLost", err)
	}
	// Expiry: another worker takes over and the old lease is lost.
	c.Add(2 * time.Minute)
	l2, cur, err := cs.Acquire(ctx, p, "w2", time.Minute)
	if err != nil || cur.Seq != 7 {
		t.Fatalf("Acquire after expiry = %+v, %v", cur, err)
	}
	if _, err := cs.Save(ctx, l1b, Cursor{Project: p, Seq: 9}, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Save with an expired lease = %v", err)
	}
	if err := cs.Release(ctx, l1b); err != nil {
		t.Errorf("Release of a lost lease = %v", err)
	}
	// Save extends the lease.
	c.Add(50 * time.Second)
	if l2, err = cs.Save(ctx, l2, Cursor{Project: p, Seq: 9}, time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Add(50 * time.Second)
	if _, _, err := cs.Acquire(ctx, p, "w3", time.Minute); !errors.Is(err, ErrLeased) {
		t.Errorf("extended lease = %v, want ErrLeased", err)
	}
	// Release frees it at once.
	if err := cs.Release(ctx, l2); err != nil {
		t.Fatal(err)
	}
	l3, cur, err := cs.Acquire(ctx, p, "w3", time.Minute)
	if err != nil || cur.Seq != 9 || len(cur.Trees) != 0 {
		t.Fatalf("Acquire after release = %+v, %v", cur, err)
	}
	// Delete forgets the cursor.
	if err := cs.Delete(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Save(ctx, l3, Cursor{Project: p, Seq: 10}, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Save after Delete = %v", err)
	}
	if _, cur, err := cs.Acquire(ctx, p, "w4", time.Minute); err != nil || cur.Seq != 0 {
		t.Errorf("Acquire after Delete = %+v, %v", cur, err)
	}
	// Projects are independent.
	if _, _, err := cs.Acquire(ctx, p+"2", "w5", time.Minute); err != nil {
		t.Errorf("other project = %v", err)
	}
	if _, _, err := cs.Acquire(ctx, "", "w", time.Minute); err == nil {
		t.Error("Acquire without a project succeeded")
	}
}

func TestMemCursors(t *testing.T) {
	testCursors(t, func(now func() time.Time) Cursors { return NewMemCursors(now) })
}

var dbCounter atomic.Int64

func TestMongoCursors(t *testing.T) {
	uri := os.Getenv("MIND_PALACE_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set MIND_PALACE_TEST_MONGODB_URI to run the MongoDB cursor tests")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database(fmt.Sprintf("clued_codeindex_test_%d_%d", time.Now().UnixNano(), dbCounter.Add(1)))
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	testCursors(t, func(now func() time.Time) Cursors { return NewMongoCursors(db, "", now) })

	// Concurrent acquires: exactly one wins.
	cs := NewMongoCursors(db, "race", nil)
	var wins atomic.Int64
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := cs.Acquire(context.Background(), "race", fmt.Sprintf("w%d", i), time.Minute); err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrLeased) {
				t.Errorf("Acquire = %v", err)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Errorf("%d concurrent acquires won, want 1", wins.Load())
	}
}
