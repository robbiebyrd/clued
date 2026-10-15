// Package remotestore is the storage plugin that keeps plans and stories on
// a clued server. It implements store.Store over remote.Client against the
// routes of server/storeapi, one project per store instance, and is
// registered as driver "clued". The client fills its options from the
// plugin's config.json (url, token) and the repository's project slug; they
// are not meant to be written by hand.
//
// Options on the StorageDef:
//
//	url      the server URL (https://, or http:// on localhost)   required
//	token    the bearer token the server minted                   required
//	project  the project slug the documents live under            required
//	caFile   a PEM bundle trusted in addition to the system roots  optional
//	insecure "true" allows a plain http:// url off loopback        optional
//
// The server answers store.ErrNotFound as a not_found envelope, which Do
// turns into remote.ErrNotFound and this package maps back to
// store.ErrNotFound, so callers see the same sentinel as from any other
// plugin. Authentication and authorization failures surface as
// remote.ErrUnauthorized and remote.ErrForbidden.
package remotestore

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Driver is the plugin kind this package registers.
const Driver = "clued"

// Option names on config.StorageDef.Options.
const (
	OptionURL      = "url"
	OptionToken    = "token"
	OptionProject  = "project"
	OptionCAFile   = "caFile"
	OptionInsecure = "insecure"
	// OptionKey is an environment key, exchanged for a token when
	// OptionToken is empty; OptionTokenCache is where that token is kept.
	OptionKey        = "key"
	OptionTokenCache = "tokenCache"
)

func init() {
	store.Register(Driver, func(def config.StorageDef, _ map[string]string) (store.Store, error) {
		return New(def)
	})
}

// Store is one project's documents on a clued server.
type Store struct {
	name    string
	project string
	client  *remote.Client

	// seen holds the front_matter.updated of each document this store read
	// (Get, List) and of its own later writes ("<kind>/<id>"); Put sends it
	// as If-Match so the server refuses a write over a copy someone else
	// changed since (412, which Put returns as store.ErrPrecondition). A
	// document this store never read is written unconditionally, as when
	// the server is only a fan-out copy of a local primary.
	mu   sync.Mutex
	seen map[string]string
}

// New builds a store from its definition. The url, token and project
// options are required; caFile and insecure are passed on to remote.New.
// The server is not contacted until Init.
func New(def config.StorageDef) (*Store, error) {
	opt := func(name string) string { return strings.TrimSpace(def.Options[name]) }
	for _, required := range []string{OptionURL, OptionProject} {
		if opt(required) == "" {
			return nil, fmt.Errorf("remotestore: storage %q: option %q is required", def.Name, required)
		}
	}
	if opt(OptionToken) == "" && opt(OptionKey) == "" {
		return nil, fmt.Errorf("remotestore: storage %q: option %q (or %q) is required", def.Name, OptionToken, OptionKey)
	}
	insecure := false
	if raw := opt(OptionInsecure); raw != "" {
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("remotestore: storage %q: option %q must be a boolean: %w", def.Name, OptionInsecure, err)
		}
		insecure = b
	}
	client, err := remote.New(session.Config{
		ServerURL:      opt(OptionURL),
		ServerToken:    opt(OptionToken),
		ServerKey:      opt(OptionKey),
		TokenCache:     opt(OptionTokenCache),
		ServerCAFile:   opt(OptionCAFile),
		ServerInsecure: insecure,
	})
	if err != nil {
		return nil, fmt.Errorf("remotestore: storage %q: %w", def.Name, err)
	}
	return NewWithClient(def.Name, opt(OptionProject), client)
}

// NewWithClient builds a store for project over an existing client. An
// empty name defaults to the driver name; the project must be a valid slug.
func NewWithClient(name, project string, client *remote.Client) (*Store, error) {
	if name == "" {
		name = Driver
	}
	if client == nil {
		return nil, fmt.Errorf("remotestore: storage %q: client is required", name)
	}
	if !projects.ValidSlug(project) {
		return nil, fmt.Errorf("remotestore: storage %q: invalid project slug %q", name, project)
	}
	return &Store{name: name, project: project, client: client, seen: map[string]string{}}, nil
}

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return Driver }

// Client is the client the store talks to the server with.
func (s *Store) Client() *remote.Client { return s.client }

// Project is the slug the store's documents live under.
func (s *Store) Project() string { return s.project }

// Init makes one cheap request (a list of the first kind) so a bad URL,
// a rejected token or a project outside the token's list fails here rather
// than on the first write.
func (s *Store) Init(ctx context.Context) error {
	names := kind.Names()
	if len(names) == 0 {
		return nil
	}
	var docs []*model.Document
	if err := s.client.Do(ctx, http.MethodGet, s.path(names[0]), nil, &docs); err != nil {
		return fmt.Errorf("remotestore: %w", mapErr(err))
	}
	return nil
}

// Close is a no-op: nothing outlives a request.
func (s *Store) Close() error { return nil }

// path builds the route under the project's store, escaping every segment.
func (s *Store) path(segments ...string) string {
	parts := []string{storeapi.Prefix, url.PathEscape(s.project), "store"}
	for _, seg := range segments {
		parts = append(parts, url.PathEscape(seg))
	}
	return strings.Join(parts, "/")
}

// checkKind rejects unknown kinds before a request is made, as the other
// plugins do.
func checkKind(name string) error {
	if _, ok := kind.Get(name); !ok || !isKindName(name) {
		return fmt.Errorf("unknown document kind %q", name)
	}
	return nil
}

// isKindName accepts the singular kind names storeapi routes on.
func isKindName(name string) bool {
	for _, n := range kind.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// mapErr turns the client's sentinels into the store's.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if remote.IsNotFound(err) {
		return fmt.Errorf("%w: %v", store.ErrNotFound, err)
	}
	if errors.Is(err, remote.ErrPrecondition) {
		return fmt.Errorf("%w: %v", store.ErrPrecondition, err)
	}
	return err
}

// remember records the updated of documents read from or written to the
// server; forget drops one (deleted, or not found).
func (s *Store) remember(docs ...*model.Document) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range docs {
		if d != nil && d.FrontMatter.Updated != "" {
			s.seen[d.Kind+"/"+d.ID()] = d.FrontMatter.Updated
		}
	}
}

func (s *Store) forget(kindName, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.seen, kindName+"/"+id)
}

// lastSeen is the updated this store last read or wrote for a document.
func (s *Store) lastSeen(kindName, id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.seen[kindName+"/"+id]
	return u, ok
}

func (s *Store) List(ctx context.Context, kindName string) ([]*model.Document, error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	var docs []*model.Document
	if err := s.client.Do(ctx, http.MethodGet, s.path(kindName), nil, &docs); err != nil {
		return nil, mapErr(err)
	}
	for _, d := range docs {
		if d.Kind == "" {
			d.Kind = kindName
		}
	}
	s.remember(docs...)
	return docs, nil
}

func (s *Store) Get(ctx context.Context, kindName, id string) (*model.Document, error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	var d model.Document
	if err := s.client.Do(ctx, http.MethodGet, s.path(kindName, id), nil, &d); err != nil {
		if remote.IsNotFound(err) {
			s.forget(kindName, id)
		}
		return nil, mapErr(err)
	}
	if d.Kind == "" {
		d.Kind = kindName
	}
	s.remember(&d)
	return &d, nil
}

func (s *Store) Put(ctx context.Context, d *model.Document) error {
	if d == nil {
		return errors.New("remotestore: nil document")
	}
	if err := checkKind(d.Kind); err != nil {
		return err
	}
	if d.ID() == "" {
		return fmt.Errorf("remotestore: %s document has no id", d.Kind)
	}
	var header http.Header
	if prev, ok := s.lastSeen(d.Kind, d.ID()); ok {
		header = http.Header{"If-Match": {storeapi.ETag(prev)}}
	}
	if err := s.client.DoHeader(ctx, http.MethodPut, s.path(d.Kind, d.ID()), header, d, nil); err != nil {
		return mapErr(err)
	}
	if header != nil {
		s.remember(d)
	}
	return nil
}

// PutIf writes d only while the server's copy has prevUpdated
// (store.ConditionalPutter): a PUT with If-Match, 412 being
// store.ErrPrecondition.
func (s *Store) PutIf(ctx context.Context, d *model.Document, prevUpdated string) error {
	if d == nil {
		return errors.New("remotestore: nil document")
	}
	if err := checkKind(d.Kind); err != nil {
		return err
	}
	header := http.Header{"If-Match": {storeapi.ETag(prevUpdated)}}
	if err := s.client.DoHeader(ctx, http.MethodPut, s.path(d.Kind, d.ID()), header, d, nil); err != nil {
		return mapErr(err)
	}
	s.remember(d)
	return nil
}

func (s *Store) Delete(ctx context.Context, kindName, id string) error {
	if err := checkKind(kindName); err != nil {
		return err
	}
	err := s.client.Do(ctx, http.MethodDelete, s.path(kindName, id), nil, nil)
	if err == nil || remote.IsNotFound(err) {
		s.forget(kindName, id)
	}
	return mapErr(err)
}

// lockRetry is how long Lock waits before asking for a held lease again.
const lockRetry = 100 * time.Millisecond

// Lock takes the server's lease on a document (store.Locker) through the
// storeapi lock route, asking again while another client holds it until
// ctx is done; the returned function releases it. A server without the
// route (404) holds nothing and Lock returns at once, as before leases
// existed. When the server is the primary store (a shared project, see
// clientcfg), claims from every member's client serialise on it.
func (s *Store) Lock(ctx context.Context, kindName, id string) (func(), error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	for {
		var got storeapi.Lease
		err := s.client.Do(ctx, http.MethodPost, s.path(kindName, id, "lock"), map[string]string{}, &got)
		var re *remote.Error
		switch {
		case err == nil:
			return func() {
				rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
				defer cancel()
				_ = s.client.Do(rctx, http.MethodPost, s.path(kindName, id, "unlock"), storeapi.Lease{Lease: got.Lease}, nil)
			}, nil
		case remote.IsNotFound(err), errors.As(err, &re) && (re.Status == http.StatusNotFound || re.Status == http.StatusMethodNotAllowed):
			return func() {}, nil
		case errors.As(err, &re) && re.Kind == storeapi.ErrLocked:
		default:
			return nil, mapErr(err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("remotestore: %s %s: %w (%v)", kindName, id, store.ErrLeased, ctx.Err())
		case <-time.After(lockRetry):
		}
	}
}

func (s *Store) ListTemplates(ctx context.Context, kindName string) ([]*model.Template, error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	var ts []*model.Template
	if err := s.client.Do(ctx, http.MethodGet, s.path(kindName, "templates"), nil, &ts); err != nil {
		return nil, mapErr(err)
	}
	return ts, nil
}

func (s *Store) GetTemplate(ctx context.Context, kindName, id string) (*model.Template, error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	var t model.Template
	if err := s.client.Do(ctx, http.MethodGet, s.path(kindName, "templates", id), nil, &t); err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	if t == nil {
		return errors.New("remotestore: nil template")
	}
	if err := checkKind(t.Kind); err != nil {
		return err
	}
	if t.ID == "" {
		return fmt.Errorf("remotestore: %s template has no id", t.Kind)
	}
	return mapErr(s.client.Do(ctx, http.MethodPut, s.path(t.Kind, "templates", t.ID), t, nil))
}

func (s *Store) DeleteTemplate(ctx context.Context, kindName, id string) error {
	if err := checkKind(kindName); err != nil {
		return err
	}
	return mapErr(s.client.Do(ctx, http.MethodDelete, s.path(kindName, "templates", id), nil, nil))
}

var (
	_ store.Store             = (*Store)(nil)
	_ store.ConditionalPutter = (*Store)(nil)
	_ store.Locker            = (*Store)(nil)
)
