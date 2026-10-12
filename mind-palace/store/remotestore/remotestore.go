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
}

// New builds a store from its definition. The url, token and project
// options are required; caFile and insecure are passed on to remote.New.
// The server is not contacted until Init.
func New(def config.StorageDef) (*Store, error) {
	opt := func(name string) string { return strings.TrimSpace(def.Options[name]) }
	for _, required := range []string{OptionURL, OptionToken, OptionProject} {
		if opt(required) == "" {
			return nil, fmt.Errorf("remotestore: storage %q: option %q is required", def.Name, required)
		}
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
	return &Store{name: name, project: project, client: client}, nil
}

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return Driver }

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
	if _, err := s.List(ctx, names[0]); err != nil {
		return fmt.Errorf("remotestore: %w", err)
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
	return err
}

func (s *Store) List(ctx context.Context, kindName string) ([]*model.Document, error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	var docs []*model.Document
	if err := s.client.Do(ctx, http.MethodGet, s.path(kindName), nil, &docs); err != nil {
		return nil, mapErr(err)
	}
	return docs, nil
}

func (s *Store) Get(ctx context.Context, kindName, id string) (*model.Document, error) {
	if err := checkKind(kindName); err != nil {
		return nil, err
	}
	var d model.Document
	if err := s.client.Do(ctx, http.MethodGet, s.path(kindName, id), nil, &d); err != nil {
		return nil, mapErr(err)
	}
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
	return mapErr(s.client.Do(ctx, http.MethodPut, s.path(d.Kind, d.ID()), d, nil))
}

func (s *Store) Delete(ctx context.Context, kindName, id string) error {
	if err := checkKind(kindName); err != nil {
		return err
	}
	return mapErr(s.client.Do(ctx, http.MethodDelete, s.path(kindName, id), nil, nil))
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

var _ store.Store = (*Store)(nil)
