// Package firestorestore stores documents in Google Cloud Firestore.
//
// Options:
//
//	project     GCP project id (required; falls back to $GOOGLE_CLOUD_PROJECT)
//	database    Firestore database id (default "(default)")
//	collection  collection prefix (default "mp_"): <prefix>plans, <prefix>stories, <prefix>templates
//
// Credentials come from Application Default Credentials
// (GOOGLE_APPLICATION_CREDENTIALS, gcloud auth, or the runtime identity);
// set FIRESTORE_EMULATOR_HOST to use the local emulator.
package firestorestore

import (
	"context"
	"fmt"
	"os"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

func init() {
	store.Register("firestore", func(def config.StorageDef, _ map[string]string) (store.Store, error) {
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

// Store is a Firestore-backed document store.
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
		prefix = "mp_"
	}
	return &Store{name: name, project: project, database: database, prefix: prefix}
}

type docRecord struct {
	ID       string `firestore:"id"`
	Path     string `firestore:"path"`
	Type     string `firestore:"type"`
	Status   string `firestore:"status"`
	Priority string `firestore:"priority"`
	Title    string `firestore:"title"`
	Updated  string `firestore:"updated"`
	Document string `firestore:"document"`
}

type templateRecord struct {
	Kind    string `firestore:"kind"`
	ID      string `firestore:"id"`
	Content string `firestore:"content"`
	Updated string `firestore:"updated"`
}

func templateKey(kindName, id string) string { return kindName + ":" + id }

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return "firestore" }

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

func (s *Store) collection(kindName string) (*firestore.CollectionRef, error) {
	k, ok := kind.Get(kindName)
	if !ok {
		return nil, fmt.Errorf("unknown document kind %q", kindName)
	}
	return s.client.Collection(s.prefix + k.Plural), nil
}

func (s *Store) tmpls() *firestore.CollectionRef { return s.client.Collection(s.prefix + "templates") }

func notFound(err error) bool {
	return status.Code(err) == codes.NotFound
}

func decode(kindName string, d docRecord) (*model.Document, error) {
	fm, content, err := markdown.Parse(d.Document)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.Path, err)
	}
	return &model.Document{Kind: kindName, Path: d.Path, FrontMatter: fm, Content: content}, nil
}

func (s *Store) List(ctx context.Context, kindName string) ([]*model.Document, error) {
	col, err := s.collection(kindName)
	if err != nil {
		return nil, err
	}
	snaps, err := col.OrderBy("id", firestore.Asc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]*model.Document, 0, len(snaps))
	for _, snap := range snaps {
		var d docRecord
		if err := snap.DataTo(&d); err != nil {
			return nil, err
		}
		doc, err := decode(kindName, d)
		if err != nil {
			return nil, err
		}
		out = append(out, doc)
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, kindName, id string) (*model.Document, error) {
	col, err := s.collection(kindName)
	if err != nil {
		return nil, err
	}
	snap, err := col.Doc(id).Get(ctx)
	if notFound(err) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var d docRecord
	if err := snap.DataTo(&d); err != nil {
		return nil, err
	}
	return decode(kindName, d)
}

func (s *Store) Put(ctx context.Context, d *model.Document) error {
	col, err := s.collection(d.Kind)
	if err != nil {
		return err
	}
	doc, err := markdown.RenderDocument(d)
	if err != nil {
		return err
	}
	fm := d.FrontMatter
	_, err = col.Doc(fm.ID).Set(ctx, docRecord{ID: fm.ID, Path: d.Path, Type: fm.Type, Status: fm.Status, Priority: fm.Priority, Title: fm.Title, Updated: fm.Updated, Document: doc})
	return err
}

func (s *Store) Delete(ctx context.Context, kindName, id string) error {
	col, err := s.collection(kindName)
	if err != nil {
		return err
	}
	return deleteDoc(ctx, col.Doc(id))
}

func deleteDoc(ctx context.Context, ref *firestore.DocumentRef) error {
	if _, err := ref.Get(ctx); err != nil {
		if notFound(err) {
			return store.ErrNotFound
		}
		return err
	}
	_, err := ref.Delete(ctx)
	return err
}

func (s *Store) ListTemplates(ctx context.Context, kindName string) ([]*model.Template, error) {
	snaps, err := s.tmpls().Where("kind", "==", kindName).OrderBy("id", firestore.Asc).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	out := make([]*model.Template, 0, len(snaps))
	for _, snap := range snaps {
		var r templateRecord
		if err := snap.DataTo(&r); err != nil {
			return nil, err
		}
		out = append(out, &model.Template{Kind: r.Kind, ID: r.ID, Content: r.Content, Updated: r.Updated})
	}
	return out, nil
}

func (s *Store) GetTemplate(ctx context.Context, kindName, id string) (*model.Template, error) {
	snap, err := s.tmpls().Doc(templateKey(kindName, id)).Get(ctx)
	if notFound(err) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var r templateRecord
	if err := snap.DataTo(&r); err != nil {
		return nil, err
	}
	return &model.Template{Kind: r.Kind, ID: r.ID, Content: r.Content, Updated: r.Updated}, nil
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	updated := t.Updated
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.tmpls().Doc(templateKey(t.Kind, t.ID)).Set(ctx, templateRecord{Kind: t.Kind, ID: t.ID, Content: t.Content, Updated: updated})
	return err
}

func (s *Store) DeleteTemplate(ctx context.Context, kindName, id string) error {
	return deleteDoc(ctx, s.tmpls().Doc(templateKey(kindName, id)))
}

var _ store.Store = (*Store)(nil)
