package codeindex

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
)

// The lifecycle of a project's graph (plan 0012, "Lifecycle", "Rebuild"
// and "Error Handling"), which keeps graphs bounded and recoverable:
//
//   - An overlay with no session activity on its worktree for OverlayIdle
//     and nothing dirty or unpushed (no files, tombstones included) is left
//     out of the selection, so the next batch, or the next consistency
//     check with repair, drops it; activity or a change brings it back.
//   - A worktree removal (RemoveWorktree's tombstone) or a branch deleted on
//     GitHub (RemoveRef's ref tombstone) empties its trees, and selection
//     ignores empty trees and removed worktrees, so the layers they made go
//     with the batch that consumes the tombstone.
//   - Purge drops a project's graph and forgets its cursor.
//   - Rebuild drops the graph and replays the project's current trees (not
//     the log) through the same selection and Applier, which gives the
//     graph the incremental path converges to.
//   - Check compares, per layer, the graph's file count and file hash with
//     the selection's, and with repair writes the difference; RunChecks
//     runs it periodically over every project.

// OverlayIdle is how long an overlay with nothing dirty or unpushed is kept
// after its worktree's last session activity (plan 0010's rule).
const OverlayIdle = 14 * 24 * time.Hour

// Defaults of the lifecycle operations.
const (
	// DefaultCheckInterval is how often RunChecks checks every project
	// when its interval is zero.
	DefaultCheckInterval = time.Hour
	// StatusPendingCap bounds the changes Status counts past the cursor.
	StatusPendingCap = 10000
)

// lastActivity is the worktree's last session activity: the latest LastAt
// of its sessions, or its last write when that is later (a commit is
// activity too).
func lastActivity(w code.Worktree) time.Time {
	last := w.UpdatedAt
	for _, s := range w.Sessions {
		if s.LastAt.After(last) {
			last = s.LastAt
		}
	}
	return last
}

// DropIdleOverlays returns p without the overlays that hold no file (nothing
// dirty or unpushed, no deletion) and whose worktree saw no activity in the
// idle period before now. An overlay of a worktree wts does not list is
// kept. Nothing sits on an overlay, so no other layer changes.
func DropIdleOverlays(p Plan, wts []code.Worktree, now time.Time, idle time.Duration) Plan {
	if idle <= 0 {
		return p
	}
	byID := make(map[string]code.Worktree, len(wts))
	for _, w := range wts {
		byID[w.ID] = w
	}
	out := Plan{DefaultBranch: p.DefaultBranch, Layers: make([]LayerPlan, 0, len(p.Layers))}
	for _, l := range p.Layers {
		if l.Layer.Kind == codegraph.LayerWorktree && len(l.Files) == 0 {
			if id, err := codegraph.ParseLayerID(l.Layer.ID); err == nil {
				if w, ok := byID[id.Worktree]; ok && now.Sub(lastActivity(w)) >= idle {
					continue
				}
			}
		}
		out.Layers = append(out.Layers, l)
	}
	return out
}

// FilesHash is the consistency hash of a layer's files: SHA-256 over each
// file's path, blob, git blob id and tombstone flag (the fields Diff
// compares), ordered by path, in hex.
func FilesHash(files []FileSource) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b FileSource) int { return cmp.Compare(a.Path, b.Path) })
	h := sha256.New()
	for _, f := range sorted {
		h.Write([]byte(f.Path))
		h.Write([]byte{0})
		h.Write([]byte(f.SHA256))
		h.Write([]byte{0})
		h.Write([]byte(f.GitOID))
		h.Write([]byte{0})
		h.Write([]byte(strconv.FormatBool(f.Deleted)))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lifecycleTurn is a lifecycle operation's hold on a project: the lease,
// kept alive in the background while the operation runs (a rebuild of a
// large project outlives one lease period), the cursor read with it and the
// project's code store.
type lifecycleTurn struct {
	s       *Service
	project string
	lease   Lease
	cur     Cursor
	store   code.Store

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{} // closed by stop
	exited chan struct{} // closed when keepAlive returns
	mu     sync.Mutex
	lost   error
}

// begin takes project's lease and opens its code store; the turn's context
// is cancelled when the lease is lost. end must be called.
func (s *Service) begin(ctx context.Context, project string) (*lifecycleTurn, error) {
	if err := codegraph.ValidKey(project); err != nil {
		return nil, err
	}
	lease, cur, err := s.opts.Cursors.Acquire(ctx, project, s.opts.Holder, s.opts.Lease)
	if err != nil {
		return nil, err
	}
	t := &lifecycleTurn{s: s, project: project, lease: lease, cur: cur, done: make(chan struct{}), exited: make(chan struct{})}
	t.ctx, t.cancel = context.WithCancel(ctx)
	go t.keepAlive(cur)
	store, err := s.opts.Code(t.ctx, project)
	if err != nil {
		t.end()
		return nil, fmt.Errorf("codeindex: open code store of %s: %w", project, err)
	}
	t.store = store
	return t, nil
}

// keepAlive extends the lease every third of its period by saving the
// cursor as it was acquired, until the turn ends; a lost lease cancels the
// turn's context.
func (t *lifecycleTurn) keepAlive(cur Cursor) {
	defer close(t.exited)
	tick := time.NewTicker(max(t.s.opts.Lease/3, 10*time.Millisecond))
	defer tick.Stop()
	for {
		select {
		case <-t.done:
			return
		case <-t.ctx.Done():
			return
		case <-tick.C:
			if _, err := t.s.opts.Cursors.Save(t.ctx, t.lease, cur, t.s.opts.Lease); err != nil {
				if t.ctx.Err() != nil {
					return
				}
				t.mu.Lock()
				t.lost = err
				t.mu.Unlock()
				t.cancel()
				return
			}
		}
	}
}

// stop ends the keep-alive, waiting for a save in flight so it cannot
// overwrite the cursor saved after it, and reports a lost lease.
func (t *lifecycleTurn) stop() error {
	select {
	case <-t.done:
	default:
		close(t.done)
	}
	<-t.exited
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lost != nil {
		return fmt.Errorf("codeindex: %s: %w", t.project, t.lost)
	}
	return nil
}

// end stops the keep-alive and releases the lease.
func (t *lifecycleTurn) end() {
	_ = t.stop()
	t.cancel()
	if err := t.s.opts.Cursors.Release(context.WithoutCancel(t.ctx), t.lease); err != nil {
		t.s.log.Warn("codeindex: release lease", "project", t.project, "error", err)
	}
}

// catchUp folds the log past the cursor into the tree states, reading the
// changes' metadata only (no tree, no write), and returns the states, the
// new cursor position and how many changes it read.
func (t *lifecycleTurn) catchUp() (map[TreeKey]*TreeState, int64, int, error) {
	state := treeStates(t.cur.Trees)
	seq, n := t.cur.Seq, 0
	for {
		changes, err := t.store.Changes(t.ctx, seq, t.s.opts.BatchSize)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("codeindex: changes of %s after %d: %w", t.project, seq, err)
		}
		for _, ch := range changes {
			consume(state, ch)
		}
		n += len(changes)
		if len(changes) > 0 {
			seq = changes[len(changes)-1].Seq
		}
		if len(changes) < t.s.opts.BatchSize {
			return state, seq, n, nil
		}
	}
}

// RebuildResult reports a rebuild.
type RebuildResult struct {
	Project string `json:"project"`
	// Seq is the cursor after the rebuild: the end of the log.
	Seq int64 `json:"seq"`
	// Changes counts the log entries read past the old cursor (for the
	// trees' metadata only).
	Changes int `json:"changes"`
	// Layers and Files count what the rebuilt graph holds (tombstones
	// included).
	Layers int `json:"layers"`
	Files  int `json:"files"`
}

// Rebuild drops project's graph and writes it again from the project's
// current trees, through the same selection and Applier as the incremental
// path, under the project's lease (ErrLeased while another worker holds
// it). The trees' heads and upstreams come from the cursor, brought to the
// end of the log first; a project never indexed reads the whole log for
// them. It is the answer to a schema or parser change, a graph that
// drifted, and an unloaded graph.
func (s *Service) Rebuild(ctx context.Context, project string) (RebuildResult, error) {
	t, err := s.begin(ctx, project)
	if err != nil {
		return RebuildResult{}, err
	}
	defer t.end()
	res := RebuildResult{Project: project}
	state, seq, n, err := t.catchUp()
	if err != nil {
		return res, err
	}
	res.Changes = n
	next, err := s.selectPlan(t.ctx, t.store, state, map[TreeKey][]code.Entry{})
	if err != nil {
		return res, err
	}
	g, err := s.opts.Graphs.Open(t.ctx, project)
	if err != nil {
		return res, fmt.Errorf("codeindex: open graph of %s: %w", project, err)
	}
	if err := g.Drop(t.ctx); err != nil {
		return res, fmt.Errorf("codeindex: drop graph of %s: %w", project, err)
	}
	if err := s.write(t.ctx, Batch{Project: project, Code: t.store, Graph: g, Plan: next, Delta: Diff(Plan{}, next)}); err != nil {
		return res, err
	}
	if err := t.stop(); err != nil {
		return res, err
	}
	cur := t.cur
	cur.Seq = seq
	cur.Trees = sortedStates(state)
	if _, err := s.opts.Cursors.Save(t.ctx, t.lease, cur, s.opts.Lease); err != nil {
		return res, fmt.Errorf("codeindex: save cursor of %s: %w", project, err)
	}
	res.Seq = seq
	res.Layers = len(next.Layers)
	for _, l := range next.Layers {
		res.Files += len(l.Files)
	}
	return res, nil
}

// Purge drops project's graph and forgets its cursor and lease, under the
// lease (ErrLeased while another worker holds it): the answer to a project
// deleted or purged from the code store. A project indexed again later
// starts from the beginning of its log.
func (s *Service) Purge(ctx context.Context, project string) error {
	t, err := s.begin(ctx, project)
	if err != nil {
		return err
	}
	defer t.end()
	g, err := s.opts.Graphs.Open(t.ctx, project)
	if err != nil {
		return fmt.Errorf("codeindex: open graph of %s: %w", project, err)
	}
	if err := g.Drop(t.ctx); err != nil {
		return fmt.Errorf("codeindex: drop graph of %s: %w", project, err)
	}
	if err := t.stop(); err != nil {
		return err
	}
	if err := s.opts.Cursors.Delete(t.ctx, project); err != nil {
		return fmt.Errorf("codeindex: delete cursor of %s: %w", project, err)
	}
	return nil
}

// LayerCheck compares one layer of the selection with the graph.
type LayerCheck struct {
	Layer string `json:"layer"`
	// Files and Hash are the selection's (FilesHash); GraphFiles and
	// GraphHash the graph's. Either side is empty when it lacks the layer.
	Files      int    `json:"files"`
	Hash       string `json:"hash,omitempty"`
	GraphFiles int    `json:"graphFiles"`
	GraphHash  string `json:"graphHash,omitempty"`
	// Node is false when the layer node's fields differ (or one side lacks
	// the layer).
	Node bool `json:"node"`
	// OK is true when the counts, hashes and nodes agree.
	OK bool `json:"ok"`
}

// CheckReport is a consistency check of one project.
type CheckReport struct {
	Project string `json:"project"`
	// Seq is the cursor the check ran at.
	Seq int64 `json:"seq"`
	// Behind is true when the log has changes past the cursor; the graph
	// is then not compared (the store's trees are ahead of it).
	Behind bool `json:"behind,omitempty"`
	// Layers are the layers of either side, ordered as a Plan orders them.
	Layers []LayerCheck `json:"layers,omitempty"`
	// Consistent is true when every layer agrees (false while Behind).
	Consistent bool `json:"consistent"`
	// Repaired is true when the check wrote the difference.
	Repaired bool `json:"repaired,omitempty"`
}

// Check compares project's graph with the selection of its current trees,
// layer by layer (file counts, FilesHash, layer nodes), under the lease
// (ErrLeased while another worker holds it). With repair it writes the
// difference through the Applier when they disagree, which also drops the
// overlays that went idle in a project with no new change. A project whose
// log is ahead of its cursor is reported Behind and not compared.
func (s *Service) Check(ctx context.Context, project string, repair bool) (CheckReport, error) {
	t, err := s.begin(ctx, project)
	if err != nil {
		return CheckReport{}, err
	}
	defer t.end()
	rep := CheckReport{Project: project, Seq: t.cur.Seq}
	pending, err := t.store.Changes(t.ctx, t.cur.Seq, 1)
	if err != nil {
		return rep, fmt.Errorf("codeindex: changes of %s after %d: %w", project, t.cur.Seq, err)
	}
	if len(pending) > 0 {
		rep.Behind = true
		return rep, nil
	}
	want, err := s.selectPlan(t.ctx, t.store, treeStates(t.cur.Trees), map[TreeKey][]code.Entry{})
	if err != nil {
		return rep, err
	}
	g, err := s.opts.Graphs.Open(t.ctx, project)
	if err != nil {
		return rep, fmt.Errorf("codeindex: open graph of %s: %w", project, err)
	}
	have, err := PlanFromGraph(t.ctx, g)
	if err != nil {
		return rep, err
	}
	rep.Layers, rep.Consistent = compareLayers(want, have)
	if rep.Consistent || !repair {
		return rep, nil
	}
	if err := s.write(t.ctx, Batch{Project: project, Code: t.store, Graph: g, Prev: have, Plan: want, Delta: Diff(have, want)}); err != nil {
		return rep, err
	}
	if err := t.stop(); err != nil {
		return rep, err
	}
	rep.Repaired = true
	return rep, nil
}

// compareLayers compares the selection with the graph's plan per layer.
func compareLayers(want, have Plan) ([]LayerCheck, bool) {
	var all []LayerPlan
	seen := map[string]bool{}
	for _, p := range []Plan{want, have} {
		for _, l := range p.Layers {
			if !seen[l.Layer.ID] {
				seen[l.Layer.ID] = true
				all = append(all, LayerPlan{Layer: l.Layer})
			}
		}
	}
	sortLayers(all)
	ok := true
	out := make([]LayerCheck, 0, len(all))
	for _, a := range all {
		c := LayerCheck{Layer: a.Layer.ID}
		w, inWant := want.Layer(a.Layer.ID)
		h, inHave := have.Layer(a.Layer.ID)
		if inWant {
			c.Files, c.Hash = len(w.Files), FilesHash(w.Files)
		}
		if inHave {
			c.GraphFiles, c.GraphHash = len(h.Files), FilesHash(h.Files)
		}
		c.Node = inWant && inHave && sameLayer(w.Layer, h.Layer)
		c.OK = c.Node && c.Files == c.GraphFiles && c.Hash == c.GraphHash
		ok = ok && c.OK
		out = append(out, c)
	}
	return out, ok
}

// RunChecks checks every project Options.Projects lists every interval
// (DefaultCheckInterval when zero), repairing drift when repair is set,
// until ctx is done; it returns ctx's error. A project leased by another
// worker or behind its log is skipped until the next round; drift and
// failures are logged.
func (s *Service) RunChecks(ctx context.Context, interval time.Duration, repair bool) error {
	if s.opts.Projects == nil {
		return errors.New("codeindex: RunChecks needs Options.Projects")
	}
	if interval <= 0 {
		interval = DefaultCheckInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		projects, err := s.opts.Projects(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("codeindex: list projects", "error", err)
		}
		for _, p := range projects {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rep, err := s.Check(ctx, p, repair)
			switch {
			case errors.Is(err, ErrLeased) || ctx.Err() != nil:
			case err != nil:
				s.log.Warn("codeindex: consistency check", "project", p, "error", err)
			case !rep.Behind && !rep.Consistent:
				var drifted []string
				for _, l := range rep.Layers {
					if !l.OK {
						drifted = append(drifted, l.Layer)
					}
				}
				s.log.Warn("codeindex: graph drifted from the code store", "project", p, "layers", drifted, "repaired", rep.Repaired)
			}
		}
	}
}

// LayerStatus is one layer of a graph as Status reports it.
type LayerStatus struct {
	Layer      codegraph.Layer `json:"layer"`
	Files      int             `json:"files"`
	Tombstones int             `json:"tombstones"`
	Hash       string          `json:"hash"`
}

// Status is where a project's indexing stands.
type Status struct {
	Project string `json:"project"`
	// Seq is the cursor and UpdatedAt its last save (zero when never
	// indexed).
	Seq       int64     `json:"seq"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	// Pending counts the changes past the cursor, at most
	// StatusPendingCap; PendingMore is true when there are more.
	Pending     int  `json:"pending"`
	PendingMore bool `json:"pendingMore,omitempty"`
	// Holder and LeaseUntil name the worker indexing it now, if any.
	Holder     string    `json:"holder,omitempty"`
	LeaseUntil time.Time `json:"leaseUntil,omitzero"`
	// Trees counts the (worktree, ref) trees the cursor knows.
	Trees int `json:"trees"`
	// Layers are the graph's layers with their file counts and hashes.
	Layers []LayerStatus `json:"layers"`
}

// CursorPeeker reads a cursor and its lease without taking the lease; both
// MemCursors and MongoCursors implement it.
type CursorPeeker interface {
	// Peek returns the project's cursor (Seq 0 when never indexed) and its
	// current lease (zero when free), the lease's token left out.
	Peek(ctx context.Context, project string) (Cursor, Lease, error)
}

// Status reports project's indexing without taking its lease: the cursor
// and lease (when the Cursors is a CursorPeeker), the changes not yet
// indexed and the graph's layers.
func (s *Service) Status(ctx context.Context, project string) (Status, error) {
	if err := codegraph.ValidKey(project); err != nil {
		return Status{}, err
	}
	st := Status{Project: project, Layers: []LayerStatus{}}
	if pk, ok := s.opts.Cursors.(CursorPeeker); ok {
		cur, lease, err := pk.Peek(ctx, project)
		if err != nil {
			return st, err
		}
		st.Seq, st.UpdatedAt, st.Trees = cur.Seq, cur.UpdatedAt, len(cur.Trees)
		if !lease.Until.IsZero() && s.opts.Now().Before(lease.Until) {
			st.Holder, st.LeaseUntil = lease.Holder, lease.Until
		}
	}
	store, err := s.opts.Code(ctx, project)
	if err != nil {
		return st, fmt.Errorf("codeindex: open code store of %s: %w", project, err)
	}
	pending, err := store.Changes(ctx, st.Seq, StatusPendingCap+1)
	if err != nil {
		return st, fmt.Errorf("codeindex: changes of %s after %d: %w", project, st.Seq, err)
	}
	st.Pending = min(len(pending), StatusPendingCap)
	st.PendingMore = len(pending) > StatusPendingCap
	g, err := s.opts.Graphs.Open(ctx, project)
	if err != nil {
		return st, fmt.Errorf("codeindex: open graph of %s: %w", project, err)
	}
	have, err := PlanFromGraph(ctx, g)
	if err != nil {
		return st, err
	}
	for _, l := range have.Layers {
		ls := LayerStatus{Layer: l.Layer, Files: len(l.Files), Hash: FilesHash(l.Files)}
		for _, f := range l.Files {
			if f.Deleted {
				ls.Tombstones++
			}
		}
		st.Layers = append(st.Layers, ls)
	}
	return st, nil
}

var (
	_ CursorPeeker = (*MemCursors)(nil)
	_ CursorPeeker = (*MongoCursors)(nil)
)

// Peek implements CursorPeeker.
func (m *MemCursors) Peek(ctx context.Context, project string) (Cursor, Lease, error) {
	if err := ctx.Err(); err != nil {
		return Cursor{}, Lease{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.docs[project]
	if d == nil {
		return Cursor{Project: project}, Lease{}, nil
	}
	l := d.lease
	l.Token = ""
	if !m.now().Before(l.Until) {
		l = Lease{}
	}
	return cloneCursor(d.cursor), l, nil
}

// Peek implements CursorPeeker.
func (m *MongoCursors) Peek(ctx context.Context, project string) (Cursor, Lease, error) {
	var d cursorDoc
	err := m.coll.FindOne(ctx, bson.D{{Key: "_id", Value: project}}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Cursor{Project: project}, Lease{}, nil
	}
	if err != nil {
		return Cursor{}, Lease{}, fmt.Errorf("codeindex: read cursor %s: %w", project, err)
	}
	var l Lease
	if d.Token != "" && mongoTime(m.now()).Before(d.LeaseUntil) {
		l = Lease{Project: project, Holder: d.Holder, Until: d.LeaseUntil}
	}
	return d.cursor(), l, nil
}
