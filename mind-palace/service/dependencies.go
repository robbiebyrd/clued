package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// Dependency is a document that holds another up: one it depends on
// (Relation "depends") or one whose blocks link names it ("blocked_by").
type Dependency struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Title    string `json:"title,omitempty"`
	Status   string `json:"status"`
	Relation string `json:"relation"`
}

func (d Dependency) String() string {
	what := "must finish first"
	if d.Relation == "blocked_by" {
		what = "blocks it"
	}
	return fmt.Sprintf("%s %s (%s) %s", d.Kind, d.ID, d.Status, what)
}

// finished reports whether a status no longer holds dependents up.
func finished(status string) bool {
	return status == "complete" || status == "rejected" || status == "archived"
}

// openDependencies lists what holds d up. Starting needs every document d
// depends on (its depends links, of either kind) finished; completing also
// needs every document whose blocks link names d finished. A depends target
// that no longer exists counts as open, so a typo never unblocks work.
func (s *Service) openDependencies(ctx context.Context, d *model.Document, completing bool) ([]Dependency, error) {
	var open []Dependency
	targets := []struct {
		kind  string
		links []model.Link
	}{{kind.Plan, d.FrontMatter.PlanLinks()}, {kind.Story, d.FrontMatter.StoryLinks()}}
	for _, t := range targets {
		other := s.other(t.kind)
		if other == nil {
			continue
		}
		for _, l := range t.links {
			if l.Relation != "depends" {
				continue
			}
			dep, err := other.Resolve(ctx, l.ID)
			if IsKind(err, KindNotFound) {
				open = append(open, Dependency{Kind: t.kind, ID: l.ID, Status: "not found", Relation: "depends"})
				continue
			}
			if err != nil {
				return nil, err
			}
			if !finished(dep.FrontMatter.Status) {
				open = append(open, Dependency{Kind: t.kind, ID: dep.ID(), Title: dep.FrontMatter.Title, Status: dep.FrontMatter.Status, Relation: "depends"})
			}
		}
	}
	if completing {
		for _, other := range s.palace.Services() {
			docs, err := s.palace.store.List(ctx, other.kind.Name)
			if err != nil {
				return nil, storageErr(err)
			}
			for _, e := range docs {
				if (other.kind.Name == s.kind.Name && e.ID() == d.ID()) || finished(e.FrontMatter.Status) {
					continue
				}
				links := e.FrontMatter.PlanLinks()
				if s.kind.Name == kind.Story {
					links = e.FrontMatter.StoryLinks()
				}
				for _, l := range links {
					if l.Relation == "blocks" && model.NormalizeID(l.ID) == d.ID() {
						open = append(open, Dependency{Kind: other.kind.Name, ID: e.ID(), Title: e.FrontMatter.Title, Status: e.FrontMatter.Status, Relation: "blocked_by"})
						break
					}
				}
			}
		}
	}
	return open, nil
}

// dependencyGate refuses a start (or, with completing, a completion) while
// dependencies are open.
func (s *Service) dependencyGate(ctx context.Context, d *model.Document, completing bool) error {
	open, err := s.openDependencies(ctx, d, completing)
	if err != nil || len(open) == 0 {
		return err
	}
	verb := "start"
	if completing {
		verb = "complete"
	}
	problems := make([]string, len(open))
	for i, dep := range open {
		problems[i] = dep.String()
	}
	return &Error{Kind: KindBlockedByDependency, Problems: problems,
		Message: fmt.Sprintf("%s %s cannot %s: %d unfinished dependencies; use force to override", s.kind.Name, d.ID(), verb, len(open))}
}

// Dependencies lists what holds a document up: what it depends on and, when
// completing is set, what blocks its completion.
func (s *Service) Dependencies(ctx context.Context, identifier string) ([]Dependency, error) {
	d, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	open, err := s.openDependencies(ctx, d, true)
	if open == nil {
		open = []Dependency{}
	}
	return open, err
}

// ReadyFilter narrows the ready queue.
type ReadyFilter struct {
	Tag        string `json:"tag,omitempty"`
	Milestone  string `json:"milestone,omitempty"`
	Unassigned bool   `json:"unassigned,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

// Ready lists the documents that can start now: their status may move to
// in_progress under the workflow and nothing they depend on is unfinished.
// They come by priority (0 first), then order (unset last), then id.
func (s *Service) Ready(ctx context.Context, f ReadyFilter) ([]Summary, error) {
	docs, err := s.palace.store.List(ctx, s.kind.Name)
	if err != nil {
		return nil, storageErr(err)
	}
	var ready []*model.Document
	for _, d := range docs {
		fm := d.FrontMatter
		if fm.Status == "in_progress" || !s.cfg.CanTransition(fm.Status, "in_progress") {
			continue
		}
		if (f.Tag != "" && !contains(fm.Tags, f.Tag)) || (f.Milestone != "" && fm.Milestone != f.Milestone) || (f.Unassigned && fm.Assignee != "") {
			continue
		}
		open, err := s.openDependencies(ctx, d, false)
		if err != nil {
			return nil, err
		}
		if len(open) == 0 {
			ready = append(ready, d)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		a, b := ready[i].FrontMatter, ready[j].FrontMatter
		if pa, pb := priorityRank(a.Priority), priorityRank(b.Priority); pa != pb {
			return pa < pb
		}
		if oa, ob := orderRank(a.Order), orderRank(b.Order); oa != ob {
			return oa < ob
		}
		return a.ID < b.ID
	})
	if f.Limit > 0 && len(ready) > f.Limit {
		ready = ready[:f.Limit]
	}
	out := make([]Summary, 0, len(ready))
	for _, d := range ready {
		out = append(out, Summary{Kind: s.kind.Name, Path: d.Path, FrontMatter: d.FrontMatter})
	}
	return out, nil
}

func priorityRank(p string) int {
	if n, err := strconv.Atoi(p); err == nil {
		return n
	}
	return 99
}

func orderRank(o int) int {
	if o <= 0 {
		return int(^uint(0) >> 1)
	}
	return o
}

// claimLockTimeout bounds the wait for another claim of the same document.
const claimLockTimeout = 10 * time.Second

// Claim takes a document for an assignee in one step: it sets the assignee
// and moves the document to in_progress, under the workflow and the
// dependency rules. A document already held by another assignee is a
// Conflict unless force is set (force also overrides the workflow and the
// dependencies, as setStatus's does). Claiming one's own in-progress
// document again changes nothing. Claims are serialised within the process
// and, when the primary store can lock (the file store), across processes;
// the document is read again after the write, so a claim lost to a store
// that cannot lock is reported as a Conflict.
func (s *Service) Claim(ctx context.Context, identifier, assignee string, force bool) (*model.Document, error) {
	if !s.kind.HasAssignee {
		return nil, newErr(KindBadRequest, "%ss have no assignee field", s.kind.Name)
	}
	a, err := normalizeAssignee(assignee)
	if err != nil {
		return nil, err
	}
	if a == "" {
		return nil, newErr(KindBadRequest, "claim needs an assignee (provider:handle, e.g. github:octocat)")
	}
	first, err := s.Resolve(ctx, identifier)
	if err != nil {
		return nil, err
	}
	s.palace.claimMu.Lock()
	defer s.palace.claimMu.Unlock()
	lockCtx, cancel := context.WithTimeout(ctx, claimLockTimeout)
	defer cancel()
	unlock, err := s.lock(lockCtx, first.ID())
	if err != nil {
		return nil, newErr(KindConflict, "%s %s is being claimed elsewhere: %v", s.kind.Name, first.ID(), err)
	}
	defer unlock()

	d, err := s.Resolve(ctx, first.ID()) // read again under the lock
	if err != nil {
		return nil, err
	}
	held := d.FrontMatter.Assignee
	if held == a && d.FrontMatter.Status == "in_progress" {
		return d, nil
	}
	if held != "" && held != a && !force {
		return nil, newErr(KindConflict, "%s %s is claimed by %s; use force to take it over", s.kind.Name, d.ID(), held)
	}
	d.FrontMatter.Assignee = a
	if _, err := s.applyStatus(ctx, d, "in_progress", force); err != nil {
		return nil, err
	}
	d.Path = s.pathFor(d, "")
	saved, err := s.save(ctx, d, "claim", events.ActionUpdated)
	if err != nil {
		return saved, err
	}
	if after, err := s.Resolve(ctx, d.ID()); err == nil && after.FrontMatter.Assignee != a {
		return nil, newErr(KindConflict, "%s %s was claimed by %s at the same time", s.kind.Name, d.ID(), after.FrontMatter.Assignee)
	}
	return saved, nil
}

// lock takes the store's lock on a document when it has one.
func (s *Service) lock(ctx context.Context, id string) (func(), error) {
	type locker interface {
		Lock(ctx context.Context, kind, id string) (func(), error)
	}
	if l, ok := s.palace.store.(locker); ok {
		return l.Lock(ctx, s.kind.Name, id)
	}
	return func() {}, nil
}
