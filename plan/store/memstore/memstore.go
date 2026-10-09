// Package memstore is an in-memory storage plugin. It is the reference
// implementation of the Store interface and the test double for the service.
package memstore

import (
	"context"
	"sort"
	"sync"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
)

func init() {
	store.Register("memory", func(def config.StorageDef, _ string) (store.Store, error) {
		return New(def.Name), nil
	})
}

// Store keeps plans and templates in maps.
type Store struct {
	name      string
	mu        sync.RWMutex
	plans     map[string]*model.Plan
	templates map[string]*model.Template
	// FailWrites makes every write fail; used to exercise fan-out errors.
	FailWrites error
}

// New returns an empty store.
func New(name string) *Store {
	if name == "" {
		name = "memory"
	}
	return &Store{name: name, plans: map[string]*model.Plan{}, templates: map[string]*model.Template{}}
}

func (s *Store) Name() string               { return s.name }
func (s *Store) Kind() string               { return "memory" }
func (s *Store) Init(context.Context) error { return nil }
func (s *Store) Close() error               { return nil }

func (s *Store) ListPlans(context.Context) ([]*model.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Plan, 0, len(s.plans))
	for _, p := range s.plans {
		out = append(out, p.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (s *Store) GetPlan(_ context.Context, id string) (*model.Plan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.plans[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return p.Clone(), nil
}

func (s *Store) PutPlan(_ context.Context, p *model.Plan) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[p.ID()] = p.Clone()
	return nil
}

func (s *Store) DeletePlan(_ context.Context, id string) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.plans, id)
	return nil
}

func (s *Store) ListTemplates(context.Context) ([]*model.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Template, 0, len(s.templates))
	for _, t := range s.templates {
		c := *t
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) GetTemplate(_ context.Context, id string) (*model.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	c := *t
	return &c, nil
}

func (s *Store) PutTemplate(_ context.Context, t *model.Template) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *t
	s.templates[t.ID] = &c
	return nil
}

func (s *Store) DeleteTemplate(_ context.Context, id string) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.templates[id]; !ok {
		return store.ErrNotFound
	}
	delete(s.templates, id)
	return nil
}

var _ store.Store = (*Store)(nil)
