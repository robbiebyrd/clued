// Package codeindex is the code-graph indexer: it replays each namespaced
// project's change log (code.Store.Changes) in batches, under a per-project
// lease and a cursor stored in code_index_cursors, chooses which entries
// make up the base, the branch layers and the session overlays (layers.go),
// and writes the difference into the project's graph (codegraph.Graph).
//
// The Service owns the change-log consumer, the lease and the layer nodes;
// the files of each batch are written by an Applier, which parses through
// Service.Facts (the injected parser behind the parse cache). The clued
// server and clued-cloud run the same Service.
package codeindex

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// Defaults for Options.
const (
	DefaultBatchSize  = 500
	DefaultMaxBatches = 20
	DefaultLease      = time.Minute
	DefaultInterval   = 2 * time.Second
	DefaultMaxBackoff = 5 * time.Minute
	DefaultCacheSize  = 4096
)

// MinLease is the shortest lease New accepts: the keep-alive renews a lease
// every third of its period and no sooner than every 10 ms, so a shorter
// lease would expire before its first renewal.
const MinLease = 30 * time.Millisecond

// ErrNoParser is returned by Facts when the Service has no parser.
var ErrNoParser = errors.New("codeindex: no parser configured")

// ParseFunc parses one file version into its facts: the tree-sitter
// extractor (codeparse) in production, a fake in tests. It must be a pure
// function of its arguments, since its result is cached by content.
type ParseFunc func(path string, content []byte) (codeparse.Facts, error)

// Batch is one batch's work for an Applier: the project, its code store and
// graph, the selections before and after the batch, and the delta between
// them. The Service has already put the delta's layer nodes (lower layers
// first) and drops Delta.Dropped after Apply returns.
type Batch struct {
	Project string
	Code    code.Store
	Graph   codegraph.Graph
	Prev    Plan
	Plan    Plan
	Delta   Delta
}

// Applier writes a batch's files into the graph: facts for each put file,
// tombstones, removed paths and the hiddenBy lists of the layers below. An
// error stops the batch; the cursor does not move, so the batch is retried.
type Applier interface {
	Apply(ctx context.Context, s *Service, b Batch) error
}

// ApplierFunc adapts a function to Applier.
type ApplierFunc func(ctx context.Context, s *Service, b Batch) error

// Apply implements Applier.
func (f ApplierFunc) Apply(ctx context.Context, s *Service, b Batch) error { return f(ctx, s, b) }

// Options configure a Service.
type Options struct {
	// Projects lists the namespaced projects to index; Run polls it every
	// Interval. Required by Run only.
	Projects func(ctx context.Context) ([]string, error)
	// Code opens a project's code store. Required.
	Code func(ctx context.Context, project string) (code.Store, error)
	// Graphs opens a project's graph by its namespace. Required.
	Graphs codegraph.Opener
	// Cursors holds the cursors and leases. Required.
	Cursors Cursors
	// Apply writes each batch's files. When nil the Service keeps the
	// layer nodes current only.
	Apply Applier
	// Parse parses a file version (see ParseFunc). Facts fails with
	// ErrNoParser without it.
	Parse ParseFunc
	// ParseVersion is the facts version of a file version, the parse
	// cache key's version (Profile.FactsVersion or TextFactsVersion for the
	// detected language). Nil means codeparse.Version for every file.
	ParseVersion func(path string, content []byte) string
	// Cache is the parse cache; nil means a memory cache of
	// DefaultCacheSize entries.
	Cache codeparse.Cache
	// Workers is the number of projects indexed at once by Run (default 1).
	Workers int
	// BatchSize is the number of changes read per batch (default
	// DefaultBatchSize).
	BatchSize int
	// MaxBatches bounds the batches of one lease turn in Run, so one busy
	// project cannot starve the others (default DefaultMaxBatches).
	MaxBatches int
	// Lease is the lease's time to live, kept alive while a batch runs and
	// extended on every cursor save (default DefaultLease; raised to
	// MinLease when shorter).
	Lease time.Duration
	// Interval is how often Run polls the projects (default
	// DefaultInterval).
	Interval time.Duration
	// MaxBackoff caps the back-off after a failed turn (default
	// DefaultMaxBackoff); the first retry waits Interval.
	MaxBackoff time.Duration
	// Holder names this worker in leases (default host-pid-random).
	Holder string
	// Logger receives failures; nil means slog.Default().
	Logger *slog.Logger
	// Now returns the current time; nil means time.Now.
	Now func() time.Time
}

// Service is the indexer. It is safe for concurrent use.
type Service struct {
	opts  Options
	cache codeparse.Cache
	log   *slog.Logger

	mu      sync.Mutex
	busy    map[string]bool
	backoff map[string]backoffState
	kick    chan string
}

type backoffState struct {
	until time.Time
	wait  time.Duration
}

// New returns a Service.
func New(opts Options) (*Service, error) {
	if opts.Code == nil || opts.Graphs == nil || opts.Cursors == nil {
		return nil, errors.New("codeindex: Code, Graphs and Cursors are required")
	}
	if opts.Workers <= 0 {
		opts.Workers = 1
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = DefaultBatchSize
	}
	if opts.MaxBatches <= 0 {
		opts.MaxBatches = DefaultMaxBatches
	}
	if opts.Lease <= 0 {
		opts.Lease = DefaultLease
	}
	if opts.Lease < MinLease {
		opts.Lease = MinLease
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = DefaultMaxBackoff
	}
	if opts.Holder == "" {
		host, _ := os.Hostname()
		opts.Holder = host + "-" + strconv.Itoa(os.Getpid()) + "-" + newToken()[:8]
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	s := &Service{opts: opts, cache: opts.Cache, log: opts.Logger,
		busy: map[string]bool{}, backoff: map[string]backoffState{}, kick: make(chan string, 64)}
	if s.cache == nil {
		s.cache = codeparse.NewMemoryCache(DefaultCacheSize)
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s, nil
}

// Facts returns the facts of a file version from the parse cache, reading
// its blob from store and parsing it on a miss. A tombstone or a file whose
// content was not collected has no facts.
func (s *Service) Facts(ctx context.Context, store code.Store, f FileSource) (codeparse.Facts, error) {
	if f.Deleted || f.SHA256 == "" {
		return codeparse.Facts{}, nil
	}
	if s.opts.Parse == nil {
		return codeparse.Facts{}, ErrNoParser
	}
	blob, err := store.Blob(ctx, f.SHA256)
	if err != nil {
		return codeparse.Facts{}, fmt.Errorf("codeindex: blob of %s: %w", f.Path, err)
	}
	version := codeparse.Version
	if s.opts.ParseVersion != nil {
		version = s.opts.ParseVersion(f.Path, blob.Content)
	}
	key := codeparse.Key{SHA256: f.SHA256, Version: version}
	return codeparse.Cached(ctx, s.cache, key, func() (codeparse.Facts, error) {
		return s.opts.Parse(f.Path, blob.Content)
	})
}

// Result reports one lease turn on a project.
type Result struct {
	// Seq is the cursor after the turn.
	Seq int64
	// Changes and Batches count what the turn consumed.
	Changes int
	Batches int
	// CaughtUp is true when the turn reached the end of the log.
	CaughtUp bool
}

// Sync indexes project until it reaches the end of its change log, under
// the project's lease (ErrLeased when another worker holds it).
func (s *Service) Sync(ctx context.Context, project string) (Result, error) {
	return s.turn(ctx, project, 0)
}

// turn takes the project's lease and runs up to maxBatches batches (no
// bound when 0). The lease is kept alive while a batch runs (begin): the
// first batch of a large project parses its whole base tree, which outlives
// one lease period, and the cursor is saved only after it.
func (s *Service) turn(ctx context.Context, project string, maxBatches int) (res Result, err error) {
	t, err := s.begin(ctx, project)
	if err != nil {
		return Result{}, err
	}
	defer t.end()
	defer func() {
		// A lease lost while the turn ran cancelled its context; report the
		// loss on every path: in place of the cancellation the batch failed
		// with, and even when the turn otherwise succeeded, since the
		// project may be another worker's by now.
		if lost := t.stop(); lost != nil {
			err = lost
		}
	}()
	ctx = t.ctx
	store := t.store
	cur := t.cursor()
	res.Seq = cur.Seq
	var (
		g       codegraph.Graph
		prev    Plan
		entries = map[TreeKey][]code.Entry{}
		state   = treeStates(cur.Trees)
	)
	for maxBatches == 0 || res.Batches < maxBatches {
		changes, err := store.Changes(ctx, cur.Seq, s.opts.BatchSize)
		if err != nil {
			return res, fmt.Errorf("codeindex: changes of %s after %d: %w", project, cur.Seq, err)
		}
		if len(changes) == 0 {
			res.CaughtUp = true
			return res, nil
		}
		if g == nil {
			if g, err = s.opts.Graphs.Open(ctx, project); err != nil {
				return res, fmt.Errorf("codeindex: open graph of %s: %w", project, err)
			}
			if prev, err = PlanFromGraph(ctx, g); err != nil {
				return res, err
			}
		}
		touched := map[TreeKey]bool{}
		for _, ch := range changes {
			for _, k := range consume(state, ch) {
				touched[k] = true
			}
		}
		for k := range touched {
			delete(entries, k)
		}
		next, err := s.selectPlan(ctx, store, state, entries)
		if err != nil {
			return res, err
		}
		if err := s.write(ctx, Batch{Project: project, Code: store, Graph: g, Prev: prev, Plan: next, Delta: Diff(prev, next)}); err != nil {
			return res, err
		}
		cur.Seq = changes[len(changes)-1].Seq
		cur.Trees = sortedStates(state)
		if err := t.save(cur); err != nil {
			return res, err
		}
		prev = next
		res.Seq = cur.Seq
		res.Changes += len(changes)
		res.Batches++
		if len(changes) < s.opts.BatchSize {
			res.CaughtUp = true
			return res, nil
		}
	}
	return res, nil
}

// consume folds one change into the tree states and returns the trees it
// touched. A worktree tombstone (empty Ref) removes every tree of the
// worktree.
func consume(state map[TreeKey]*TreeState, ch code.Change) []TreeKey {
	if ch.Ref == "" {
		var gone []TreeKey
		for k := range state {
			if k.Worktree == ch.Worktree {
				delete(state, k)
				gone = append(gone, k)
			}
		}
		return gone
	}
	k := TreeKey{Worktree: ch.Worktree, Ref: ch.Ref}
	t := state[k]
	if t == nil {
		t = &TreeState{Worktree: ch.Worktree, Ref: ch.Ref}
		state[k] = t
	}
	if ch.Owner != "" {
		t.Owner = ch.Owner
	}
	t.Head = ch.Head
	if ch.Upstream != t.Upstream || t.UpstreamSeq == 0 {
		t.Upstream = ch.Upstream
		t.UpstreamSeq = ch.Seq
	}
	t.Seq = ch.Seq
	return []TreeKey{k}
}

func treeStates(ts []TreeState) map[TreeKey]*TreeState {
	m := make(map[TreeKey]*TreeState, len(ts))
	for _, t := range ts {
		m[t.Key()] = &t
	}
	return m
}

func sortedStates(m map[TreeKey]*TreeState) []TreeState {
	out := make([]TreeState, 0, len(m))
	for _, t := range m {
		out = append(out, *t)
	}
	slices.SortFunc(out, func(a, b TreeState) int {
		return cmp.Or(cmp.Compare(a.Worktree, b.Worktree), cmp.Compare(a.Ref, b.Ref))
	})
	return out
}

// selectPlan reads the worktrees and the trees not yet in entries (the
// cache of this turn) and selects the layers, leaving out the overlays that
// went idle (DropIdleOverlays, lifecycle.go).
func (s *Service) selectPlan(ctx context.Context, store code.Store, state map[TreeKey]*TreeState, entries map[TreeKey][]code.Entry) (Plan, error) {
	wts, err := store.Worktrees(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("codeindex: worktrees: %w", err)
	}
	byID := make(map[string]code.Worktree, len(wts))
	for _, w := range wts {
		byID[w.ID] = w
	}
	trees := make([]Tree, 0, len(state))
	for k, st := range state {
		w, ok := byID[k.Worktree]
		if !ok || w.Removed {
			continue
		}
		es, cached := entries[k]
		if !cached {
			if es, err = store.Tree(ctx, k.Worktree, k.Ref); err != nil {
				return Plan{}, fmt.Errorf("codeindex: tree %s %s: %w", k.Worktree, k.Ref, err)
			}
			entries[k] = es
		}
		trees = append(trees, Tree{Worktree: w, State: *st, Entries: es})
	}
	return DropIdleOverlays(Select(trees), wts, s.opts.Now(), OverlayIdle), nil
}

// write puts the delta's layer nodes, hands the files to the Applier and
// drops the dropped layers.
func (s *Service) write(ctx context.Context, b Batch) error {
	if b.Delta.Empty() {
		return nil
	}
	now := s.opts.Now().UTC()
	for _, ld := range b.Delta.Layers {
		l := ld.Layer
		l.Updated = now
		if err := b.Graph.PutLayer(ctx, l); err != nil {
			return fmt.Errorf("codeindex: put layer %s of %s: %w", l.ID, b.Project, err)
		}
	}
	if s.opts.Apply != nil {
		if err := s.opts.Apply.Apply(ctx, s, b); err != nil {
			return fmt.Errorf("codeindex: apply %s: %w", b.Project, err)
		}
	}
	for _, id := range b.Delta.Dropped {
		if err := b.Graph.DropLayer(ctx, id); err != nil {
			return fmt.Errorf("codeindex: drop layer %s of %s: %w", id, b.Project, err)
		}
	}
	return nil
}

// PlanFromGraph reads what a graph holds as a Plan (layer nodes and their
// files, tombstones included), the "before" of the first batch of a turn,
// so a graph that was dropped, unloaded or written by a crashed turn is
// brought to the selection by the next batch.
func PlanFromGraph(ctx context.Context, g codegraph.Graph) (Plan, error) {
	layers, err := g.Layers(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("codeindex: layers of %s: %w", g.Key(), err)
	}
	var p Plan
	for _, l := range layers {
		files, err := g.Files(ctx, l.ID)
		if err != nil {
			return Plan{}, fmt.Errorf("codeindex: files of %s in %s: %w", l.ID, g.Key(), err)
		}
		lp := LayerPlan{Layer: l}
		for _, f := range files {
			lp.Files = append(lp.Files, FileSource{Path: f.Path, SHA256: f.SHA256, GitOID: f.GitOID, Lang: f.Lang, Deleted: f.Deleted})
		}
		slices.SortFunc(lp.Files, func(a, b FileSource) int { return cmp.Compare(a.Path, b.Path) })
		if l.Kind == codegraph.LayerBase {
			p.DefaultBranch = l.Ref
		}
		p.Layers = append(p.Layers, lp)
	}
	sortLayers(p.Layers)
	return p, nil
}

// Kick asks Run to index project at its next opportunity, ahead of the
// poll (a change was just committed). It never blocks.
func (s *Service) Kick(project string) {
	select {
	case s.kick <- project:
	default:
	}
}

// Run indexes every project Options.Projects lists, polling every Interval,
// with Workers projects at once, until ctx is done; it returns ctx's error.
// A project whose turn fails backs off (Interval, doubling up to
// MaxBackoff) and its cursor stays where it was; a project leased by another
// worker is skipped until the next poll.
func (s *Service) Run(ctx context.Context) error {
	if s.opts.Projects == nil {
		return errors.New("codeindex: Run needs Options.Projects")
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range s.opts.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range jobs {
				s.work(ctx, p)
			}
		}()
	}
	defer func() {
		close(jobs)
		wg.Wait()
	}()
	dispatch := func(p string) bool {
		if !s.claim(p) {
			return true
		}
		select {
		case jobs <- p:
			return true
		case <-ctx.Done():
			s.unclaim(p)
			return false
		}
	}
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	poll := true
	for {
		if poll {
			projects, err := s.opts.Projects(ctx)
			if err != nil && ctx.Err() == nil {
				s.log.Warn("codeindex: list projects", "error", err)
			}
			for _, p := range projects {
				if !dispatch(p) {
					return ctx.Err()
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case p := <-s.kick:
			poll = false
			if !dispatch(p) {
				return ctx.Err()
			}
		case <-ticker.C:
			poll = true
		}
	}
}

// claim marks p busy unless it is already being indexed or backing off.
func (s *Service) claim(p string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy[p] {
		return false
	}
	if b, ok := s.backoff[p]; ok && s.opts.Now().Before(b.until) {
		return false
	}
	s.busy[p] = true
	return true
}

func (s *Service) unclaim(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.busy, p)
}

// work runs one turn on p and records its outcome.
func (s *Service) work(ctx context.Context, p string) {
	res, err := s.turn(ctx, p, s.opts.MaxBatches)
	s.mu.Lock()
	delete(s.busy, p)
	switch {
	case err == nil || errors.Is(err, ErrLeased):
		delete(s.backoff, p)
	case ctx.Err() != nil:
	default:
		b := s.backoff[p]
		b.wait = min(max(b.wait*2, s.opts.Interval), s.opts.MaxBackoff)
		b.until = s.opts.Now().Add(b.wait)
		s.backoff[p] = b
	}
	s.mu.Unlock()
	if err != nil && !errors.Is(err, ErrLeased) && ctx.Err() == nil {
		s.log.Warn("codeindex: index project", "project", p, "seq", res.Seq, "error", err)
	}
	if err == nil && !res.CaughtUp {
		s.Kick(p)
	}
}
