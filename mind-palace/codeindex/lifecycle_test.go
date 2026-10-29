package codeindex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
)

// graphDump is everything a graph holds that the indexer writes, in a
// comparable form: the layer nodes (without Updated), each layer's files
// with sorted hiddenBy, and, per layer, the outline of every path the
// layer holds as its view sees it.
type graphDump struct {
	Layers   []codegraph.Layer
	Files    map[string][]codegraph.File
	Outlines map[string][]codegraph.FileOutline
}

func dumpGraph(t *testing.T, ctx context.Context, g codegraph.Graph) graphDump {
	t.Helper()
	d := graphDump{Files: map[string][]codegraph.File{}, Outlines: map[string][]codegraph.FileOutline{}}
	layers, err := g.Layers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p, err := PlanFromGraph(ctx, g)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range layers {
		l.Updated = time.Time{}
		d.Layers = append(d.Layers, l)
		files, err := g.Files(ctx, l.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i := range files {
			slices.Sort(files[i].HiddenBy)
			if len(files[i].HiddenBy) == 0 {
				files[i].HiddenBy = nil
			}
		}
		d.Files[l.ID] = files
		view, err := p.View(l.ID)
		if err != nil {
			t.Fatalf("view of %s: %v", l.ID, err)
		}
		for _, f := range files {
			outs, err := g.Outline(ctx, view, codegraph.OutlineQuery{Path: f.Path})
			if err != nil && !errors.Is(err, codegraph.ErrNotFound) {
				t.Fatalf("outline of %s in %s: %v", f.Path, l.ID, err)
			}
			for i := range outs {
				slices.Sort(outs[i].File.HiddenBy)
				if len(outs[i].File.HiddenBy) == 0 {
					outs[i].File.HiddenBy = nil
				}
			}
			d.Outlines[l.ID] = append(d.Outlines[l.ID], outs...)
		}
	}
	return d
}

func diffDumps(t *testing.T, what string, got, want graphDump) {
	t.Helper()
	if !reflect.DeepEqual(got.Layers, want.Layers) {
		t.Errorf("%s: layers = %+v\nwant %+v", what, got.Layers, want.Layers)
	}
	for id, w := range want.Files {
		if g := got.Files[id]; !reflect.DeepEqual(g, w) {
			t.Errorf("%s: files of %s = %+v\nwant %+v", what, id, g, w)
		}
	}
	for id, w := range want.Outlines {
		if g := got.Outlines[id]; !reflect.DeepEqual(g, w) {
			t.Errorf("%s: outlines of %s = %+v\nwant %+v", what, id, g, w)
		}
	}
	if len(got.Files) != len(want.Files) || len(got.Outlines) != len(want.Outlines) {
		t.Errorf("%s: %d file layers and %d outline layers, want %d and %d", what,
			len(got.Files), len(got.Outlines), len(want.Files), len(want.Outlines))
	}
}

// lifecycleHistory records a history with every kind of layer and every
// lifecycle event, syncing in between (small batches, so several batches
// per turn): a base from a GitHub worktree, branch layers from GitHub and
// client trees, overlays with edits, deletions and new files, main moving
// on, a worktree removed and a branch deleted on GitHub.
func lifecycleHistory(e *applyEnv) (gh, m, x, y, z string) {
	gh, m, x, y, z = "gh_42", wtID(1), wtID(2), wtID(3), wtID(4)
	owner := "0123456789abcdef"
	e.worktree(gh, owner, code.SourceGitHub, "main")
	for _, id := range []string{m, x, y, z} {
		e.worktree(id, owner, "", "main")
	}
	a1, b1, c1, e1 := "package p\nfunc A\n", "package p\nfunc B\ncall A\n", "package p\nfunc C\n", "package p\nfunc E\n"
	b2, d1 := "package p\nfunc B2\n", "package p\nimport fmt\nimport strings s\ntype D\nmethod D.Run\n"
	a3, d3, n1 := "package p\nfunc A3\n", "package p\nfunc D3\n", "package p\nfunc N\n"
	u, f1, g1 := commitID("main"), commitID("f1"), commitID("g1")
	main := []code.Entry{clean("a.go", a1), clean("b.go", b1), clean("c.go", c1), clean("e.go", e1)}
	e.commit(code.Change{Worktree: gh, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: main}, a1, b1, c1, e1)
	e.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: main})
	e.sync()
	e.commit(code.Change{Worktree: x, Ref: "feat", Head: f1, Upstream: f1, Full: true,
		Upserts: []code.Entry{clean("a.go", a1), clean("b.go", b2), clean("d.go", d1), clean("e.go", e1)}}, b2, d1)
	e.commit(code.Change{Worktree: y, Ref: "feat", Head: f1, Upstream: f1, SessionID: "s1", Full: true,
		Upserts: []code.Entry{dirty("a.go", a3, a1), clean("b.go", b2), dirty("d.go", d3, d1), dirty("n.go", n1, "")}}, a3, d3, n1)
	e.commit(code.Change{Worktree: gh, Ref: "gone", Head: g1, Upstream: g1, Full: true,
		Upserts: []code.Entry{clean("a.go", a1), clean("c.go", b2)}})
	e.sync()
	// y keeps editing, z works on a never-pushed branch, main moves on.
	e.commit(code.Change{Worktree: y, Ref: "feat", Head: f1, Upstream: f1, SessionID: "s1",
		Upserts: []code.Entry{dirty("e.go", a3, e1)}, Deletes: []string{"n.go"}})
	e.commit(code.Change{Worktree: z, Ref: "wip", Head: commitID("wip"), Full: true,
		Upserts: []code.Entry{unpushed("a.go", a3), clean("b.go", b1), clean("c.go", c1)}})
	a4 := "package p\nfunc A4\n"
	u2 := commitID("main2")
	main2 := []code.Entry{clean("a.go", a4), clean("b.go", b1), clean("c.go", c1), clean("e.go", e1)}
	e.commit(code.Change{Worktree: gh, Ref: "main", Head: u2, Upstream: u2, Full: true, Upserts: main2}, a4)
	e.sync()
	// x's checkout is removed, the gone branch is deleted on GitHub.
	if err := e.store.RemoveWorktree(e.ctx, x); err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.RemoveRef(e.ctx, gh, "gone"); err != nil {
		e.t.Fatal(err)
	}
	e.sync()
	return gh, m, x, y, z
}

func TestRebuildEqualsIncremental(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, func(o *Options) { o.BatchSize = 2 })
		lifecycleHistory(e)
		incremental := dumpGraph(t, e.ctx, e.g)
		if len(incremental.Layers) < 4 {
			t.Fatalf("history left too few layers: %+v", incremental.Layers)
		}
		symbols := 0
		for _, outs := range incremental.Outlines {
			for _, o := range outs {
				symbols += len(o.Symbols)
			}
		}
		if symbols == 0 {
			t.Fatal("the dump holds no symbol: the comparison would prove nothing")
		}
		seq := e.svc.mustStatus(t, e.project).Seq

		// A rebuild from the cursor: same graph.
		res, err := e.svc.Rebuild(e.ctx, e.project)
		if err != nil {
			t.Fatal(err)
		}
		if res.Seq != seq || res.Changes != 0 || res.Layers != len(incremental.Layers) {
			t.Errorf("rebuild = %+v, want seq %d, no change read, %d layers", res, seq, len(incremental.Layers))
		}
		diffDumps(t, "rebuild from the cursor", dumpGraph(t, e.ctx, e.g), incremental)

		// A rebuild with no cursor (a new worker, a lost cursor collection)
		// reads the log for the trees' metadata and gets the same graph.
		e.curs = NewMemCursors(nil)
		fresh, err := New(Options{Code: e.svc.opts.Code, Graphs: graphs, Cursors: e.curs, Apply: FileApplier{},
			Parse: fakeParse(nil), BatchSize: 3})
		if err != nil {
			t.Fatal(err)
		}
		res, err = fresh.Rebuild(e.ctx, e.project)
		if err != nil {
			t.Fatal(err)
		}
		if res.Seq != seq || res.Changes != int(seq) {
			t.Errorf("rebuild without a cursor = %+v, want seq %d after reading %d changes", res, seq, seq)
		}
		diffDumps(t, "rebuild without a cursor", dumpGraph(t, e.ctx, e.g), incremental)

		// The rebuilt graph is consistent, and incremental indexing goes on
		// from it.
		if rep, err := fresh.Check(e.ctx, e.project, false); err != nil || !rep.Consistent {
			t.Errorf("check after rebuild = %+v, %v", rep, err)
		}
		if r, err := fresh.Sync(e.ctx, e.project); err != nil || r.Changes != 0 || r.Seq != seq {
			t.Errorf("sync after rebuild = %+v, %v", r, err)
		}
	})
}

func (s *Service) mustStatus(t *testing.T, project string) Status {
	t.Helper()
	st, err := s.Status(context.Background(), project)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestWorktreeRemovalAndBranchDeletionDropLayers(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, nil)
		_, m, x, y, z := lifecycleHistory(e)
		var ids []string
		for _, l := range dumpGraph(t, e.ctx, e.g).Layers {
			ids = append(ids, l.ID)
		}
		f1 := commitID("f1")
		want := []string{"base", codegraph.BranchLayerID("feat", f1), codegraph.WorktreeLayerID(m, "main"),
			codegraph.WorktreeLayerID(y, "feat"), codegraph.WorktreeLayerID(z, "wip")}
		slices.Sort(want)
		if !reflect.DeepEqual(ids, want) {
			t.Errorf("layers = %v, want %v", ids, want)
		}
		for _, gone := range []string{codegraph.WorktreeLayerID(x, "feat"), codegraph.BranchLayerID("gone", commitID("g1"))} {
			if slices.Contains(ids, gone) {
				t.Errorf("layer %s survived its worktree or branch", gone)
			}
		}
		// The feat branch layer stays: y still sits on it.
		files, err := e.g.Files(e.ctx, codegraph.BranchLayerID("feat", f1))
		if err != nil || len(files) == 0 {
			t.Errorf("feat branch layer files = %v, %v", files, err)
		}
		// Deleting the branch on GitHub too and removing y's worktree
		// drops it.
		if err := e.store.RemoveWorktree(e.ctx, y); err != nil {
			t.Fatal(err)
		}
		e.sync()
		if l, err := e.g.Layers(e.ctx); err != nil || slices.ContainsFunc(l, func(l codegraph.Layer) bool { return l.Ref == "feat" }) {
			t.Errorf("layers after y's removal = %+v, %v", l, err)
		}
		e.checkHidden()
	})
}

func TestIdleOverlayIsDropped(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		c := &clock{now: time.Now()}
		e := newApplyEnv(t, graphs, func(o *Options) { o.Now = c.Now })
		m, a := wtID(1), wtID(2)
		e.worktree(m, "u1", "", "main")
		e.worktree(a, "u1", "", "main")
		u := commitID("main")
		a1, a2 := "package p\nfunc A\n", "package p\nfunc A2\n"
		e.commit(code.Change{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{clean("a.go", a1)}}, a1)
		e.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{dirty("a.go", a2, a1)}}, a2)
		e.sync()
		clean, edited := codegraph.WorktreeLayerID(m, "main"), codegraph.WorktreeLayerID(a, "main")
		has := func(id string) bool {
			t.Helper()
			ls, err := e.g.Layers(e.ctx)
			if err != nil {
				t.Fatal(err)
			}
			return slices.ContainsFunc(ls, func(l codegraph.Layer) bool { return l.ID == id })
		}
		if !has(clean) || !has(edited) {
			t.Fatal("both overlays should exist while active")
		}

		// Fifteen days later, a quiet project: the check sees the clean
		// overlay as drift and the repair drops it; the edited one stays.
		c.Add(OverlayIdle + 24*time.Hour)
		rep, err := e.svc.Check(e.ctx, e.project, false)
		if err != nil || rep.Consistent || rep.Repaired {
			t.Fatalf("check of an idle overlay = %+v, %v", rep, err)
		}
		if rep, err = e.svc.Check(e.ctx, e.project, true); err != nil || !rep.Repaired {
			t.Fatalf("repair = %+v, %v", rep, err)
		}
		if has(clean) || !has(edited) {
			t.Errorf("after the repair: clean overlay %v (want gone), edited %v (want kept)", has(clean), has(edited))
		}
		if rep, err := e.svc.Check(e.ctx, e.project, false); err != nil || !rep.Consistent {
			t.Errorf("check after repair = %+v, %v", rep, err)
		}

		// Activity within the period (the clock back) brings it back with
		// the next batch.
		c.Add(-(OverlayIdle + 24*time.Hour))
		e.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{dirty("a.go", a2, a1)}})
		e.sync()
		if !has(clean) {
			t.Error("an active clean overlay should be selected again")
		}

		// An idle project's next batch drops it as well.
		c.Add(OverlayIdle + time.Hour)
		e.commit(code.Change{Worktree: a, Ref: "main", Head: u, Upstream: u, Full: true, Upserts: []code.Entry{dirty("a.go", a2, a1)}})
		e.sync()
		if has(clean) {
			t.Error("the next batch should drop the idle clean overlay")
		}
	})
}

func TestDropIdleOverlays(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	old, recent := now.Add(-OverlayIdle-time.Minute), now.Add(-time.Hour)
	w1, w2, w3 := wtID(1), wtID(2), wtID(3)
	p := Plan{DefaultBranch: "main", Layers: []LayerPlan{
		{Layer: codegraph.Layer{ID: "base", Kind: codegraph.LayerBase}},
		{Layer: codegraph.Layer{ID: codegraph.WorktreeLayerID(w1, "main"), Kind: codegraph.LayerWorktree}},
		{Layer: codegraph.Layer{ID: codegraph.WorktreeLayerID(w2, "main"), Kind: codegraph.LayerWorktree},
			Files: []FileSource{{Path: "a.go", Deleted: true}}},
		{Layer: codegraph.Layer{ID: codegraph.WorktreeLayerID(w3, "main"), Kind: codegraph.LayerWorktree}},
		{Layer: codegraph.Layer{ID: codegraph.WorktreeLayerID(wtID(9), "main"), Kind: codegraph.LayerWorktree}},
	}}
	wts := []code.Worktree{
		{ID: w1, UpdatedAt: old, Sessions: []code.SessionSeen{{ID: "s", LastAt: old}}},
		{ID: w2, UpdatedAt: old},
		{ID: w3, UpdatedAt: old, Sessions: []code.SessionSeen{{ID: "s", LastAt: recent}}},
	}
	got := layerIDs(DropIdleOverlays(p, wts, now, OverlayIdle))
	want := []string{"base", codegraph.WorktreeLayerID(w2, "main"), codegraph.WorktreeLayerID(w3, "main"), codegraph.WorktreeLayerID(wtID(9), "main")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("kept %v, want %v (idle and empty dropped; a deletion, recent activity or an unknown worktree keep it)", got, want)
	}
	if got := DropIdleOverlays(p, wts, now, 0); len(got.Layers) != len(p.Layers) {
		t.Error("no idle period drops nothing")
	}
}

func TestCheckDetectsAndRepairsDrift(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, nil)
		lifecycleHistory(e)
		rep, err := e.svc.Check(e.ctx, e.project, false)
		if err != nil || !rep.Consistent || rep.Behind || len(rep.Layers) == 0 {
			t.Fatalf("check of an indexed project = %+v, %v", rep, err)
		}
		before := dumpGraph(t, e.ctx, e.g)

		// A crash mid-batch: a base file lost, another with the wrong blob,
		// an extra layer.
		if err := e.g.RemoveFile(e.ctx, "base", "c.go"); err != nil {
			t.Fatal(err)
		}
		if err := e.g.ApplyFile(e.ctx, codegraph.FileFacts{File: codegraph.File{Layer: "base", Path: "e.go", SHA256: shaOf("x"), GitOID: oid("x")}}); err != nil {
			t.Fatal(err)
		}
		stray := codegraph.BranchLayerID("stray", commitID("s"))
		if err := e.g.PutLayer(e.ctx, codegraph.Layer{ID: stray, Kind: codegraph.LayerBranch, Parent: "base", Ref: "stray"}); err != nil {
			t.Fatal(err)
		}
		rep, err = e.svc.Check(e.ctx, e.project, false)
		if err != nil || rep.Consistent {
			t.Fatalf("check after drift = %+v, %v", rep, err)
		}
		bad := map[string]bool{}
		for _, l := range rep.Layers {
			if !l.OK {
				bad[l.Layer] = true
			}
		}
		if !reflect.DeepEqual(bad, map[string]bool{"base": true, stray: true}) {
			t.Errorf("drifted layers = %v, want base and the stray layer", bad)
		}
		rep, err = e.svc.Check(e.ctx, e.project, true)
		if err != nil || !rep.Repaired {
			t.Fatalf("repair = %+v, %v", rep, err)
		}
		diffDumps(t, "after repair", dumpGraph(t, e.ctx, e.g), before)

		// A log ahead of the cursor is not compared.
		e.commit(code.Change{Worktree: wtID(1), Ref: "main", Head: commitID("main2"), Upstream: commitID("main2"), Full: true,
			Upserts: []code.Entry{clean("a.go", "package p\nfunc A4\n")}})
		if rep, err := e.svc.Check(e.ctx, e.project, true); err != nil || !rep.Behind || rep.Consistent || rep.Repaired {
			t.Errorf("check behind the log = %+v, %v", rep, err)
		}
	})
}

func TestPurgeDropsGraphAndCursor(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newApplyEnv(t, graphs, nil)
		lifecycleHistory(e)
		full := dumpGraph(t, e.ctx, e.g)
		if err := e.svc.Purge(e.ctx, e.project); err != nil {
			t.Fatal(err)
		}
		if ls, err := e.g.Layers(e.ctx); err != nil || len(ls) != 0 {
			t.Errorf("layers after purge = %v, %v", ls, err)
		}
		st := e.svc.mustStatus(t, e.project)
		if st.Seq != 0 || st.Trees != 0 || st.Pending == 0 || len(st.Layers) != 0 {
			t.Errorf("status after purge = %+v", st)
		}
		// Indexed again, the project replays its log from the start.
		e.sync()
		diffDumps(t, "reindexed after purge", dumpGraph(t, e.ctx, e.g), full)
	})
}

func TestLifecycleRespectsTheLease(t *testing.T) {
	e := newApplyEnv(t, memgraph.New(), nil)
	lifecycleHistory(e)
	l, _, err := e.curs.Acquire(e.ctx, e.project, "other", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Rebuild(e.ctx, e.project); !errors.Is(err, ErrLeased) {
		t.Errorf("Rebuild under another lease = %v", err)
	}
	if err := e.svc.Purge(e.ctx, e.project); !errors.Is(err, ErrLeased) {
		t.Errorf("Purge under another lease = %v", err)
	}
	if _, err := e.svc.Check(e.ctx, e.project, true); !errors.Is(err, ErrLeased) {
		t.Errorf("Check under another lease = %v", err)
	}
	st := e.svc.mustStatus(t, e.project)
	if st.Holder != "other" || st.LeaseUntil.IsZero() {
		t.Errorf("status while leased = %+v", st)
	}
	if err := e.curs.Release(e.ctx, l); err != nil {
		t.Fatal(err)
	}
	if st := e.svc.mustStatus(t, e.project); st.Holder != "" || st.Pending != 0 || len(st.Layers) == 0 || st.Trees == 0 {
		t.Errorf("status after release = %+v", st)
	}
	for _, bad := range []string{"", "../x", "a b"} {
		if _, err := e.svc.Rebuild(e.ctx, bad); !errors.Is(err, codegraph.ErrInvalid) {
			t.Errorf("Rebuild(%q) = %v", bad, err)
		}
		if _, err := e.svc.Status(e.ctx, bad); !errors.Is(err, codegraph.ErrInvalid) {
			t.Errorf("Status(%q) = %v", bad, err)
		}
	}
}

// slowApplier delays every batch, so a rebuild outlives its lease period.
type slowApplier struct {
	Applier
	delay time.Duration
}

func (a slowApplier) Apply(ctx context.Context, s *Service, b Batch) error {
	time.Sleep(a.delay)
	return a.Applier.Apply(ctx, s, b)
}

func TestRebuildKeepsItsLeaseAlive(t *testing.T) {
	e := newApplyEnv(t, memgraph.New(), nil)
	lifecycleHistory(e)
	want := dumpGraph(t, e.ctx, e.g)
	// A rebuild writes the whole graph in one go, so it must extend the
	// lease itself (the incremental path does the same per batch: see
	// TestSyncKeepsItsLeaseAlive).
	e.svc.opts.Lease = 300 * time.Millisecond
	e.svc.opts.Apply = slowApplier{Applier: FileApplier{}, delay: time.Second}
	res, err := e.svc.Rebuild(e.ctx, e.project)
	if err != nil {
		t.Fatalf("Rebuild with a short lease = %v", err)
	}
	if st := e.svc.mustStatus(t, e.project); st.Seq != res.Seq || st.Pending != 0 {
		t.Errorf("cursor after rebuild = %+v, rebuild %+v", st, res)
	}
	diffDumps(t, "slow rebuild", dumpGraph(t, e.ctx, e.g), want)
}

func TestRunChecksRepairsInTheBackground(t *testing.T) {
	logs := &lockedLog{}
	e := newApplyEnv(t, memgraph.New(), func(o *Options) {
		o.Logger = slog.New(slog.NewTextHandler(logs, nil))
	})
	e.svc.opts.Projects = func(context.Context) ([]string, error) { return []string{e.project}, nil }
	lifecycleHistory(e)
	want := dumpGraph(t, e.ctx, e.g)
	if err := e.g.RemoveFile(e.ctx, "base", "a.go"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(e.ctx)
	done := make(chan error, 1)
	go func() { done <- e.svc.RunChecks(ctx, 20*time.Millisecond, true) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// The repair lands before its log line: wait for both.
		if rep, err := e.svc.Check(e.ctx, e.project, false); err == nil && rep.Consistent && strings.Contains(logs.String(), "drifted") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("RunChecks = %v", err)
	}
	diffDumps(t, "after background repair", dumpGraph(t, e.ctx, e.g), want)
	if !strings.Contains(logs.String(), "drifted") {
		t.Errorf("drift not logged: %s", logs.String())
	}
	s, _ := New(Options{Code: e.svc.opts.Code, Graphs: e.graphs, Cursors: e.curs})
	if err := s.RunChecks(e.ctx, time.Millisecond, false); err == nil {
		t.Error("RunChecks without Projects succeeded")
	}
}

// lockedLog is a log destination safe for concurrent writes and reads.
type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestFilesHash(t *testing.T) {
	a := []FileSource{{Path: "b.go", SHA256: "1", GitOID: "x"}, {Path: "a.go", Deleted: true}}
	b := []FileSource{{Path: "a.go", Deleted: true, Lang: "go"}, {Path: "b.go", SHA256: "1", GitOID: "x", Size: 9}}
	if FilesHash(a) != FilesHash(b) {
		t.Error("the hash depends on order or on fields Diff ignores")
	}
	for _, c := range [][]FileSource{
		{{Path: "b.go", SHA256: "2", GitOID: "x"}, {Path: "a.go", Deleted: true}},
		{{Path: "b.go", SHA256: "1", GitOID: "y"}, {Path: "a.go", Deleted: true}},
		{{Path: "b.go", SHA256: "1", GitOID: "x"}, {Path: "a.go"}},
		{{Path: "b.go", SHA256: "1", GitOID: "x"}},
	} {
		if FilesHash(c) == FilesHash(a) {
			t.Errorf("%+v hashes like %+v", c, a)
		}
	}
}

func TestMongoCursorsPeek(t *testing.T) {
	uri := os.Getenv("MIND_PALACE_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set MIND_PALACE_TEST_MONGODB_URI to run the MongoDB cursor tests")
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database(fmt.Sprintf("clued_codeindex_peek_%d_%d", time.Now().UnixNano(), dbCounter.Add(1)))
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	testPeek(t, NewMongoCursors(db, "", nil))
}

func TestMemCursorsPeek(t *testing.T) { testPeek(t, NewMemCursors(nil)) }

func testPeek(t *testing.T, cs interface {
	Cursors
	CursorPeeker
}) {
	ctx := context.Background()
	cur, l, err := cs.Peek(ctx, "p")
	if err != nil || cur.Seq != 0 || cur.Project != "p" || l.Holder != "" {
		t.Errorf("Peek of a new project = %+v, %+v, %v", cur, l, err)
	}
	lease, _, err := cs.Acquire(ctx, "p", "w1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Save(ctx, lease, Cursor{Project: "p", Seq: 7, Trees: []TreeState{{Worktree: wtID(1), Ref: "main", Seq: 7}}}, time.Minute); err != nil {
		t.Fatal(err)
	}
	cur, l, err = cs.Peek(ctx, "p")
	if err != nil || cur.Seq != 7 || len(cur.Trees) != 1 || l.Holder != "w1" || l.Token != "" || l.Until.IsZero() {
		t.Errorf("Peek while leased = %+v, %+v, %v", cur, l, err)
	}
	if err := cs.Release(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if cur, l, err = cs.Peek(ctx, "p"); err != nil || cur.Seq != 7 || l.Holder != "" {
		t.Errorf("Peek after release = %+v, %+v, %v", cur, l, err)
	}
}
