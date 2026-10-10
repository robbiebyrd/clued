// Package mongostore stores plans in MongoDB.
//
// Options:
//
//	uri         connection string (default mongodb://localhost:27017)
//	database    database name (default "plans")
//	collection  collection prefix (default "plan_"): <prefix>plans, <prefix>templates
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/markdown"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
)

func init() {
	store.Register("mongodb", func(def config.StorageDef, _ string) (store.Store, error) {
		return New(def.Name, def.Options["uri"], def.Options["database"], def.Options["collection"]), nil
	})
	store.Register("mongo", func(def config.StorageDef, _ string) (store.Store, error) {
		return New(def.Name, def.Options["uri"], def.Options["database"], def.Options["collection"]), nil
	})
}

// Store is a MongoDB-backed plan store.
type Store struct {
	name   string
	uri    string
	dbName string
	prefix string
	client *mongo.Client
	plans  *mongo.Collection
	tmpls  *mongo.Collection
}

// New prepares a store (the connection is made in Init).
func New(name, uri, database, prefix string) *Store {
	if name == "" {
		name = "mongodb"
	}
	if uri == "" {
		uri = "mongodb://localhost:27017"
	}
	if database == "" {
		database = "plans"
	}
	if prefix == "" {
		prefix = "plan_"
	}
	return &Store{name: name, uri: uri, dbName: database, prefix: prefix}
}

type planDoc struct {
	ID       string `bson:"_id"`
	Path     string `bson:"path"`
	Type     string `bson:"type"`
	Status   string `bson:"status"`
	Priority string `bson:"priority"`
	Title    string `bson:"title"`
	Updated  string `bson:"updated"`
	Document string `bson:"document"`
}

type templateDoc struct {
	ID      string `bson:"_id"`
	Content string `bson:"content"`
	Updated string `bson:"updated"`
}

func (s *Store) Name() string { return s.name }
func (s *Store) Kind() string { return "mongodb" }

func (s *Store) Init(ctx context.Context) error {
	if s.client == nil {
		client, err := mongo.Connect(options.Client().ApplyURI(s.uri))
		if err != nil {
			return err
		}
		s.client = client
		db := client.Database(s.dbName)
		s.plans = db.Collection(s.prefix + "plans")
		s.tmpls = db.Collection(s.prefix + "templates")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.client.Ping(pingCtx, nil); err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	_, err := s.plans.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "status", Value: 1}}},
		{Keys: bson.D{{Key: "type", Value: 1}}},
	})
	return err
}

func (s *Store) Close() error {
	if s.client == nil {
		return nil
	}
	err := s.client.Disconnect(context.Background())
	s.client = nil
	return err
}

func decode(d planDoc) (*model.Plan, error) {
	fm, content, err := markdown.Parse(d.Document)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", d.Path, err)
	}
	return &model.Plan{Path: d.Path, FrontMatter: fm, Content: content}, nil
}

func (s *Store) ListPlans(ctx context.Context) ([]*model.Plan, error) {
	cur, err := s.plans.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []planDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]*model.Plan, 0, len(docs))
	for _, d := range docs {
		p, err := decode(d)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) GetPlan(ctx context.Context, id string) (*model.Plan, error) {
	var d planDoc
	err := s.plans.FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
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
	d := planDoc{ID: fm.ID, Path: p.Path, Type: fm.Type, Status: fm.Status, Priority: fm.Priority, Title: fm.Title, Updated: fm.Updated, Document: doc}
	_, err = s.plans.ReplaceOne(ctx, bson.M{"_id": fm.ID}, d, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) DeletePlan(ctx context.Context, id string) error {
	res, err := s.plans.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListTemplates(ctx context.Context) ([]*model.Template, error) {
	cur, err := s.tmpls.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []templateDoc
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]*model.Template, 0, len(docs))
	for _, d := range docs {
		out = append(out, &model.Template{ID: d.ID, Content: d.Content, Updated: d.Updated})
	}
	return out, nil
}

func (s *Store) GetTemplate(ctx context.Context, id string) (*model.Template, error) {
	var d templateDoc
	err := s.tmpls.FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &model.Template{ID: d.ID, Content: d.Content, Updated: d.Updated}, nil
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	updated := t.Updated
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	_, err := s.tmpls.ReplaceOne(ctx, bson.M{"_id": t.ID}, templateDoc{ID: t.ID, Content: t.Content, Updated: updated}, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) DeleteTemplate(ctx context.Context, id string) error {
	res, err := s.tmpls.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

var _ store.Store = (*Store)(nil)
