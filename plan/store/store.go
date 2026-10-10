// Package store defines the storage plugin interface for plans and templates,
// a plugin registry, and the fan-out MultiStore that keeps every enabled copy
// up to date.
package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/model"
)

// ErrNotFound is returned when a plan or template does not exist in a store.
var ErrNotFound = errors.New("not found")

// Store is the persistence contract every storage plugin implements. Plans
// are keyed by ID; Path is carried along so copies stay interchangeable.
// Business rules (validation, workflow, links) live in the service layer, not
// here: a store only persists what it is given.
type Store interface {
	// Name is the configured instance name (used by sync and error messages).
	Name() string
	// Kind is the plugin kind (file, sqlite, postgres, …).
	Kind() string
	// Init prepares the backend (creates directories, tables, indexes).
	Init(ctx context.Context) error
	Close() error

	ListPlans(ctx context.Context) ([]*model.Plan, error)
	GetPlan(ctx context.Context, id string) (*model.Plan, error)
	// PutPlan creates or replaces the plan with the same ID. When the plan's
	// Path differs from the stored one (rename, archive) the store moves it.
	PutPlan(ctx context.Context, p *model.Plan) error
	DeletePlan(ctx context.Context, id string) error

	ListTemplates(ctx context.Context) ([]*model.Template, error)
	GetTemplate(ctx context.Context, id string) (*model.Template, error)
	PutTemplate(ctx context.Context, t *model.Template) error
	DeleteTemplate(ctx context.Context, id string) error
}

// Factory builds a store from its configuration. plansDir is the configured
// plans directory, which file-like plugins use as their default root.
type Factory func(def config.StorageDef, plansDir string) (Store, error)

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds a plugin kind. Plugins call it from init().
func Register(kind string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	factories[kind] = f
}

// Kinds lists the registered plugin kinds.
func Kinds() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(factories))
	for k := range factories {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Open builds a store from a definition using the registered factory.
func Open(def config.StorageDef, plansDir string) (Store, error) {
	regMu.RLock()
	f, ok := factories[def.Kind]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("storage %q: unknown kind %q (registered: %v)", def.Name, def.Kind, Kinds())
	}
	return f(def, plansDir)
}

// OpenAll builds and initialises every enabled store from the configuration.
func OpenAll(ctx context.Context, cfg *config.Config) (*MultiStore, error) {
	var stores []Store
	for _, def := range cfg.EnabledStorage() {
		s, err := Open(def, cfg.PlansDir)
		if err != nil {
			closeAll(stores)
			return nil, err
		}
		if err := s.Init(ctx); err != nil {
			closeAll(stores)
			return nil, fmt.Errorf("storage %q: init: %w", def.Name, err)
		}
		stores = append(stores, s)
	}
	if len(stores) == 0 {
		return nil, errors.New("no storage plugin is enabled")
	}
	return NewMulti(stores...), nil
}

func closeAll(stores []Store) {
	for _, s := range stores {
		_ = s.Close()
	}
}

// FanoutError reports stores that failed during a fan-out write. The write
// succeeded on the stores not listed.
type FanoutError struct {
	Op     string
	Failed map[string]error
}

func (e *FanoutError) Error() string {
	names := make([]string, 0, len(e.Failed))
	for n := range e.Failed {
		names = append(names, n)
	}
	sort.Strings(names)
	s := fmt.Sprintf("%s failed on %d store(s):", e.Op, len(names))
	for _, n := range names {
		s += fmt.Sprintf(" %s: %v;", n, e.Failed[n])
	}
	return s + " run `plan sync` to reconcile"
}

// MultiStore fans writes out to every store and reads from the primary (the
// first one). It implements Store itself so the service does not care how
// many copies exist.
type MultiStore struct {
	stores []Store
}

// NewMulti wraps one or more stores. The first is the primary.
func NewMulti(stores ...Store) *MultiStore {
	return &MultiStore{stores: stores}
}

// Stores returns the wrapped stores in order (primary first).
func (m *MultiStore) Stores() []Store { return m.stores }

// Primary returns the read store.
func (m *MultiStore) Primary() Store { return m.stores[0] }

// Find returns a wrapped store by name.
func (m *MultiStore) Find(name string) (Store, bool) {
	for _, s := range m.stores {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

func (m *MultiStore) Name() string { return "multi" }
func (m *MultiStore) Kind() string { return "multi" }

func (m *MultiStore) Init(ctx context.Context) error {
	for _, s := range m.stores {
		if err := s.Init(ctx); err != nil {
			return fmt.Errorf("storage %q: %w", s.Name(), err)
		}
	}
	return nil
}

func (m *MultiStore) Close() error {
	var errs []error
	for _, s := range m.stores {
		if err := s.Close(); err != nil {
			errs = append(errs, fmt.Errorf("storage %q: %w", s.Name(), err))
		}
	}
	return errors.Join(errs...)
}

func (m *MultiStore) ListPlans(ctx context.Context) ([]*model.Plan, error) {
	return m.Primary().ListPlans(ctx)
}

func (m *MultiStore) GetPlan(ctx context.Context, id string) (*model.Plan, error) {
	return m.Primary().GetPlan(ctx, id)
}

func (m *MultiStore) ListTemplates(ctx context.Context) ([]*model.Template, error) {
	return m.Primary().ListTemplates(ctx)
}

func (m *MultiStore) GetTemplate(ctx context.Context, id string) (*model.Template, error) {
	return m.Primary().GetTemplate(ctx, id)
}

// fanout runs fn on every store. The primary must succeed; failures on the
// other copies are reported as a FanoutError after all stores were tried.
// A primary ErrNotFound is returned as is so callers can detect it.
func (m *MultiStore) fanout(op string, fn func(s Store) error) error {
	failed := map[string]error{}
	for i, s := range m.stores {
		if err := fn(s); err != nil {
			if i == 0 {
				if errors.Is(err, ErrNotFound) {
					return ErrNotFound
				}
				return fmt.Errorf("%s on primary store %q: %w", op, s.Name(), err)
			}
			if errors.Is(err, ErrNotFound) {
				continue // already gone on this copy
			}
			failed[s.Name()] = err
		}
	}
	if len(failed) > 0 {
		return &FanoutError{Op: op, Failed: failed}
	}
	return nil
}

func (m *MultiStore) PutPlan(ctx context.Context, p *model.Plan) error {
	return m.fanout("put plan "+p.ID(), func(s Store) error { return s.PutPlan(ctx, p.Clone()) })
}

func (m *MultiStore) DeletePlan(ctx context.Context, id string) error {
	return m.fanout("delete plan "+id, func(s Store) error { return s.DeletePlan(ctx, id) })
}

func (m *MultiStore) PutTemplate(ctx context.Context, t *model.Template) error {
	return m.fanout("put template "+t.ID, func(s Store) error {
		c := *t
		return s.PutTemplate(ctx, &c)
	})
}

func (m *MultiStore) DeleteTemplate(ctx context.Context, id string) error {
	return m.fanout("delete template "+id, func(s Store) error { return s.DeleteTemplate(ctx, id) })
}

var _ Store = (*MultiStore)(nil)
