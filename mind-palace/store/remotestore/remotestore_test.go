package remotestore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
	"github.com/robbiebyrd/clued/mind-palace/store/storetest"
)

const (
	testToken   = "test-token"
	testProject = "github.com__owner__repo"
)

// stubSource resolves testToken to a principal allowed on every project
// whose slug starts with testProject.
type stubSource struct{}

func (stubSource) Lookup(raw string) (auth.Principal, bool) {
	if raw != testToken {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: "test", AccountID: "acct", Projects: []string{"*"}}, true
}

// opener hands out one memstore per project.
type opener struct {
	mu     sync.Mutex
	stores map[string]*memstore.Store
}

func (o *opener) Open(_ context.Context, project string) (store.Store, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	s, ok := o.stores[project]
	if !ok {
		s = memstore.New(project)
		o.stores[project] = s
	}
	return s, nil
}

// newServer starts storeapi behind auth.Middleware with a memstore per
// project.
func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	o := &opener{stores: map[string]*memstore.Store{}}
	h := storeapi.Handler(storeapi.Options{
		Open: o.Open,
		Allow: func(r *http.Request, project string) bool {
			p, ok := auth.FromContext(r.Context())
			return ok && auth.AllowProject(p, project)
		},
	})
	srv := httptest.NewServer(auth.Middleware(stubSource{})(h))
	t.Cleanup(srv.Close)
	return srv
}

func def(url, token, project string) config.StorageDef {
	opts := map[string]string{}
	if url != "" {
		opts[OptionURL] = url
	}
	if token != "" {
		opts[OptionToken] = token
	}
	if project != "" {
		opts[OptionProject] = project
	}
	return config.StorageDef{Name: "clued-server", Kind: Driver, Options: opts}
}

var projectSeq atomic.Int64

func TestConformance(t *testing.T) {
	srv := newServer(t)
	storetest.Run(t, func(t *testing.T) store.Store {
		// A fresh project per store so every suite starts empty.
		project := fmt.Sprintf("%s-%d", testProject, projectSeq.Add(1))
		s, err := store.Open(def(srv.URL, testToken, project), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func TestRegistered(t *testing.T) {
	if !slices.Contains(store.Drivers(), Driver) {
		t.Fatalf("driver %q not registered: %v", Driver, store.Drivers())
	}
}

func TestNameAndDriver(t *testing.T) {
	s, err := New(def("https://clued.example", testToken, testProject))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "clued-server" || s.Driver() != "clued" {
		t.Errorf("name %q driver %q", s.Name(), s.Driver())
	}
	if s.Project() != testProject {
		t.Errorf("project %q", s.Project())
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	// An empty name falls back to the driver name.
	d := def("https://clued.example", testToken, testProject)
	d.Name = ""
	s, err = New(d)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name() != "clued" {
		t.Errorf("default name %q", s.Name())
	}
}

func TestMissingOptions(t *testing.T) {
	cases := map[string]config.StorageDef{
		"url":     def("", testToken, testProject),
		"token":   def("https://clued.example", "", testProject),
		"project": def("https://clued.example", testToken, ""),
	}
	for name, d := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := store.Open(d, nil); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("expected error naming %q, got %v", name, err)
			}
		})
	}
	t.Run("bad project slug", func(t *testing.T) {
		if _, err := New(def("https://clued.example", testToken, "../etc")); err == nil {
			t.Fatal("expected an error for an invalid project slug")
		}
	})
	t.Run("bad url", func(t *testing.T) {
		_, err := New(def("http://clued.example", testToken, testProject))
		if !errors.Is(err, remote.ErrInsecureURL) {
			t.Fatalf("expected ErrInsecureURL, got %v", err)
		}
		d := def("http://clued.example", testToken, testProject)
		d.Options[OptionInsecure] = "true"
		if _, err := New(d); err != nil {
			t.Fatalf("insecure override: %v", err)
		}
	})
	t.Run("bad caFile", func(t *testing.T) {
		d := def("https://clued.example", testToken, testProject)
		d.Options[OptionCAFile] = t.TempDir() + "/missing.pem"
		if _, err := New(d); err == nil {
			t.Fatal("expected an error for a missing CA file")
		}
	})
}

func TestInit(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)

	t.Run("ok", func(t *testing.T) {
		s, err := New(def(srv.URL, testToken, testProject))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Init(ctx); err != nil {
			t.Fatalf("init: %v", err)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		s, err := New(def(srv.URL, "nope", testProject))
		if err != nil {
			t.Fatal(err)
		}
		err = s.Init(ctx)
		if !errors.Is(err, remote.ErrUnauthorized) {
			t.Fatalf("expected ErrUnauthorized, got %v", err)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		dead := httptest.NewServer(http.NotFoundHandler())
		url := dead.URL
		dead.Close()
		s, err := New(def(url, testToken, testProject))
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Init(ctx); err == nil {
			t.Fatal("expected an error for an unreachable server")
		}
	})
}

func TestNotFoundMapping(t *testing.T) {
	ctx := context.Background()
	srv := newServer(t)
	s, err := New(def(srv.URL, testToken, testProject+"-nf"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, kind.Plan, "0001-aaa"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get: %v", err)
	}
	if err := s.Delete(ctx, kind.Plan, "0001-aaa"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete: %v", err)
	}
	if _, err := s.GetTemplate(ctx, kind.Story, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get template: %v", err)
	}
	if err := s.DeleteTemplate(ctx, kind.Story, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete template: %v", err)
	}
	// Other failures are not ErrNotFound.
	if _, err := s.List(ctx, "bogus"); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown kind: %v", err)
	}
	if err := s.Put(ctx, &model.Document{Kind: kind.Plan}); err == nil || errors.Is(err, store.ErrNotFound) {
		t.Errorf("put without id: %v", err)
	}
}

func TestForbiddenProject(t *testing.T) {
	ctx := context.Background()
	o := &opener{stores: map[string]*memstore.Store{}}
	h := storeapi.Handler(storeapi.Options{
		Open:  o.Open,
		Allow: func(r *http.Request, project string) bool { return project == testProject },
	})
	srv := httptest.NewServer(auth.Middleware(stubSource{})(h))
	defer srv.Close()
	s, err := New(def(srv.URL, testToken, "github.com__other__repo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(ctx); !errors.Is(err, remote.ErrForbidden) {
		t.Fatalf("expected ErrForbidden, got %v", err)
	}
}
