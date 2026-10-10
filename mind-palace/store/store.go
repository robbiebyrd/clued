// Package store defines the storage plugin interface for documents (plans and
// stories) and their templates, a plugin registry, and the fan-out MultiStore
// that keeps every enabled copy up to date. A plugin supports every document
// kind or none: the kind is a parameter of each call.
package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/model"
)

// ErrNotFound is returned when a document or template does not exist in a store.
var ErrNotFound = errors.New("not found")

// Store is the persistence contract every storage plugin implements.
// Documents are keyed by kind and ID; Path is carried along so copies stay
// interchangeable. Business rules (validation, workflow, links) live in the
// service layer, not here: a store only persists what it is given.
type Store interface {
	// Name is the configured instance name (used by sync and error messages).
	Name() string
	// Driver is the plugin kind (file, sqlite, postgres, …).
	Driver() string
	// Init prepares the backend (creates directories, tables, indexes).
	Init(ctx context.Context) error
	Close() error

	// List returns every document of a kind, ordered by ID.
	List(ctx context.Context, kind string) ([]*model.Document, error)
	Get(ctx context.Context, kind, id string) (*model.Document, error)
	// Put creates or replaces the document with the same kind and ID. When
	// the document's Path differs from the stored one (rename, archive) the
	// store moves it.
	Put(ctx context.Context, d *model.Document) error
	Delete(ctx context.Context, kind, id string) error

	// Templates are kept per kind.
	ListTemplates(ctx context.Context, kind string) ([]*model.Template, error)
	GetTemplate(ctx context.Context, kind, id string) (*model.Template, error)
	PutTemplate(ctx context.Context, t *model.Template) error
	DeleteTemplate(ctx context.Context, kind, id string) error
}

// Factory builds a store from its configuration. dirs maps each kind name
// to its configured directory, which file-like plugins use as their default
// roots.
type Factory func(def config.StorageDef, dirs map[string]string) (Store, error)

var (
	regMu     sync.RWMutex
	factories = map[string]Factory{}
)

// Register adds a plugin driver. Plugins call it from init().
func Register(driver string, f Factory) {
	regMu.Lock()
	defer regMu.Unlock()
	factories[driver] = f
}

// Drivers lists the registered plugin drivers.
func Drivers() []string {
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
func Open(def config.StorageDef, dirs map[string]string) (Store, error) {
	regMu.RLock()
	f, ok := factories[def.Kind]
	regMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("storage %q: unknown kind %q (registered: %v)", def.Name, def.Kind, Drivers())
	}
	return f(def, dirs)
}

// OpenAll builds and initialises every enabled store from the configuration.
func OpenAll(ctx context.Context, cfg *config.Config) (*MultiStore, error) {
	var stores []Store
	for _, def := range cfg.EnabledStorage() {
		s, err := Open(def, cfg.Dirs())
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
	return s + " run `mind-palace sync` to reconcile"
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

func (m *MultiStore) Name() string   { return "multi" }
func (m *MultiStore) Driver() string { return "multi" }

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

func (m *MultiStore) List(ctx context.Context, kind string) ([]*model.Document, error) {
	return m.Primary().List(ctx, kind)
}

func (m *MultiStore) Get(ctx context.Context, kind, id string) (*model.Document, error) {
	return m.Primary().Get(ctx, kind, id)
}

func (m *MultiStore) ListTemplates(ctx context.Context, kind string) ([]*model.Template, error) {
	return m.Primary().ListTemplates(ctx, kind)
}

func (m *MultiStore) GetTemplate(ctx context.Context, kind, id string) (*model.Template, error) {
	return m.Primary().GetTemplate(ctx, kind, id)
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

func (m *MultiStore) Put(ctx context.Context, d *model.Document) error {
	return m.fanout("put "+d.Kind+" "+d.ID(), func(s Store) error { return s.Put(ctx, d.Clone()) })
}

func (m *MultiStore) Delete(ctx context.Context, kind, id string) error {
	return m.fanout("delete "+kind+" "+id, func(s Store) error { return s.Delete(ctx, kind, id) })
}

func (m *MultiStore) PutTemplate(ctx context.Context, t *model.Template) error {
	return m.fanout("put "+t.Kind+" template "+t.ID, func(s Store) error {
		c := *t
		return s.PutTemplate(ctx, &c)
	})
}

func (m *MultiStore) DeleteTemplate(ctx context.Context, kind, id string) error {
	return m.fanout("delete "+kind+" template "+id, func(s Store) error { return s.DeleteTemplate(ctx, kind, id) })
}

var _ Store = (*MultiStore)(nil)
