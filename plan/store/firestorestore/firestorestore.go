// Package firestorestore stores plans in Google Cloud Firestore.
//
// Options:
//
//	project     GCP project id (required; falls back to $GOOGLE_CLOUD_PROJECT)
//	database    Firestore database id (default "(default)")
//	collection  collection prefix (default "plan_"): <prefix>plans, <prefix>templates
//
// Credentials come from Application Default Credentials
// (GOOGLE_APPLICATION_CREDENTIALS, gcloud auth, or the runtime identity);
// set FIRESTORE_EMULATOR_HOST to use the local emulator.
package firestorestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/markdown"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
)

func init() {
	store.Register("firestore", func(def config.StorageDef, _ string) (store.Store, error) {
		project := def.Options["project"]
		if project == "" {
			project = os.Getenv("GOOGLE_CLOUD_PROJECT")
		}
		if project == "" {
			return nil, fmt.Errorf("storage %q: option project (or $GOOGLE_CLOUD_PROJECT) is required for firestore", def.Name)
		}
		return New(def.Name, project, def.Options["database"], def.Options["collection"]), nil
	})
}

// Store is a Firestore-backed plan store.
type Store struct {
	name     string
	project  string
	database string
	prefix   string
	client   *firestore.Client
}

// New prepares a store (the client is created in Init).
func New(name, project, database, prefix string) *Store {
	if name == "" {
		name = "firestore"
	}
	if database == "" {
		database = firestore.DefaultDatabaseID
	}
	if prefix == "" {
		prefix = "plan_"
	}
	return &Store{name: name, project: project, database: database, prefix: prefix}
}

type planDoc struct {
	ID       string `firestore:"id"`
	Path     string `firestore:"path"`
	Type     string `firestore:"type"`
	Status   string `firestore:"status"`
	Priority string `firestore:"priority"`
	Title    string `firestore:"title"`
	Updated  string `firestore:"updated"`
	Document string `firestore:"document"`
}

type templateDoc struct {
	ID      string `firestore:"id"`
	Content string `firestore:"content"`
	Updated string `firestore:"updated"`
}

func (s *Store) Name() string { return s.name }
func (s *Store) Kind() string { return "firestore" }

func (s *Store) Init(ctx context.Context) error {
	if s.client != nil {
		return nil
	}
	client, err := firestore.NewClientWithDatabase(ctx, s.project, s.database)
	if err != nil {
		return fmt.Errorf("connect firestore: %w", err)
	}
	s.client = client
	return nil
}

func (s *Store) Close() error {
	if s.client == nil {
		return nil
	}
	err := s.client.Close()
	s.client = nil
	return err
}

func (s *Store) plans() *firestore.CollectionRef { return s.client.Collection(s.prefix + "plans") }
func (s *Store) tmpls() *firestore.CollectionRef { return s.client.Collection(s.prefix + "templates") }

func notFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

func decode(d planDoc) (*model.Plan, error) {
	fm, content, err := markdown.Parse(d.Document)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.Path, err)
	}
	return &model.Plan{Path: d.Path, FrontMatter: fm, Content: content}, nil
}

func (s *Store) ListPlans(ctx context.Context) ([]*model.Plan, error) {
	snaps, err := s.plans().OrderBy("id", firestore.Asc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]*model.Plan, 0, len(snaps))
	for _, snap := range snaps {
		var d planDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		p, err := decode(d)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) GetPlan(ctx context.Context, id string) (*model.Plan, error) {
	snap, err := s.plans().Doc(id).Get(ctx)
	if notFound(err) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var d planDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return decode(d)
}

func (s *Store) PutPlan(ctx context.Context, p *model.Plan) error {
	doc, err := markdown.RenderPlan(p)
	if err != nil {
		return err
	}
	fm := p.FrontMatter
	_, err = s.plans().Doc(fm.ID).Set(ctx, planDoc{ID: fm.ID, Path: p.Path, Type: fm.Type, Status: fm.Status, Priority: fm.Priority, Title: fm.Title, Updated: fm.Updated, Document: doc})
	return err
}

func (s *Store) DeletePlan(ctx context.Context, id string) error {
	ref := s.plans().Doc(id)
	if _, err := ref.Get(ctx); err != nil {
		if notFound(err) {
			return store.ErrNotFound
		}
		return err
	}
	_, err := ref.Delete(ctx)
	return err
}

func (s *Store) ListTemplates(ctx context.Context) ([]*model.Template, error) {
	snaps, err := s.tmpls().OrderBy("id", firestore.Asc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]*model.Template, 0, len(snaps))
	for _, snap := range snaps {
		var d templateDoc
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		out = append(out, &model.Template{ID: d.ID, Content: d.Content, Updated: d.Updated})
	}
	return out, nil
}

func (s *Store) GetTemplate(ctx context.Context, id string) (*model.Template, error) {
	snap, err := s.tmpls().Doc(id).Get(ctx)
	if notFound(err) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var d templateDoc
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return &model.Template{ID: d.ID, Content: d.Content, Updated: d.Updated}, nil
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	updated := t.Updated
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.tmpls().Doc(t.ID).Set(ctx, templateDoc{ID: t.ID, Content: t.Content, Updated: updated})
	return err
}

func (s *Store) DeleteTemplate(ctx context.Context, id string) error {
	ref := s.tmpls().Doc(id)
	if _, err := ref.Get(ctx); err != nil {
		if notFound(err) {
			return store.ErrNotFound
		}
		return err
	}
	_, err := ref.Delete(ctx)
	return err
}

var _ store.Store = (*Store)(nil)
var _ = errors.Is
