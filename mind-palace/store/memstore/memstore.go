// Package memstore is an in-memory storage plugin. It is the reference
// implementation of the Store interface and the test double for the service.
package memstore

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

func init() {
	store.Register("memory", func(def config.StorageDef, _ map[string]string) (store.Store, error) {
		return New(def.Name), nil
	})
}

type key struct{ kind, id string }

// Store keeps documents and templates in maps.
type Store struct {
	name      string
	mu        sync.RWMutex
	docs      map[key]*model.Document
	templates map[key]*model.Template
	// FailWrites makes every write fail; used to exercise fan-out errors.
	FailWrites error
}

// New returns an empty store.
func New(name string) *Store {
	if name == "" {
		name = "memory"
	}
	return &Store{name: name, docs: map[key]*model.Document{}, templates: map[key]*model.Template{}}
}

func (s *Store) Name() string               { return s.name }
func (s *Store) Driver() string             { return "memory" }
func (s *Store) Init(context.Context) error { return nil }
func (s *Store) Close() error               { return nil }

// checkKind rejects unknown document kinds like every other plugin.
func checkKind(kindName string) error {
	if _, ok := kind.Get(kindName); !ok {
		return fmt.Errorf("unknown document kind %q", kindName)
	}
	return nil
}

func (s *Store) List(_ context.Context, kind string) ([]*model.Document, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Document, 0)
	for k, d := range s.docs {
		if k.kind == kind {
			out = append(out, d.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID() < out[j].ID() })
	return out, nil
}

func (s *Store) Get(_ context.Context, kind, id string) (*model.Document, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.docs[key{kind, id}]
	if !ok {
		return nil, store.ErrNotFound
	}
	return d.Clone(), nil
}

func (s *Store) Put(_ context.Context, d *model.Document) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	if err := checkKind(d.Kind); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.docs[key{d.Kind, d.ID()}] = d.Clone()
	return nil
}

func (s *Store) Delete(_ context.Context, kind, id string) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{kind, id}
	if _, ok := s.docs[k]; !ok {
		return store.ErrNotFound
	}
	delete(s.docs, k)
	return nil
}

func (s *Store) ListTemplates(_ context.Context, kind string) ([]*model.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*model.Template, 0)
	for k, t := range s.templates {
		if k.kind == kind {
			c := *t
			out = append(out, &c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) GetTemplate(_ context.Context, kind, id string) (*model.Template, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[key{kind, id}]
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
	if err := checkKind(t.Kind); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := *t
	s.templates[key{t.Kind, t.ID}] = &c
	return nil
}

func (s *Store) DeleteTemplate(_ context.Context, kind, id string) error {
	if s.FailWrites != nil {
		return s.FailWrites
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{kind, id}
	if _, ok := s.templates[k]; !ok {
		return store.ErrNotFound
	}
	delete(s.templates, k)
	return nil
}

var _ store.Store = (*Store)(nil)
