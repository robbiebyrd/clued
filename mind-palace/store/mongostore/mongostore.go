// Package mongostore stores documents in MongoDB.
//
// Options:
//
//	uri         connection string (default mongodb://localhost:27017)
//	database    database name (default "mind-palace")
//	collection  collection prefix (default "mp_"): <prefix>plans, <prefix>stories, <prefix>templates,
//	            <prefix>leases (document leases, store.Leaser)
package mongostore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

func init() {
	f := func(def config.StorageDef, _ map[string]string) (store.Store, error) {
		return New(def.Name, def.Options["uri"], def.Options["database"], def.Options["collection"]), nil
	}
	store.Register("mongodb", f)
	store.Register("mongo", f)
}

// Store is a MongoDB-backed document store.
type Store struct {
	name   string
	uri    string
	dbName string
	prefix string
	client *mongo.Client
	db     *mongo.Database
	tmpls  *mongo.Collection
	leases *mongo.Collection
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
		database = "mind-palace"
	}
	if prefix == "" {
		prefix = "mp_"
	}
	return &Store{name: name, uri: uri, dbName: database, prefix: prefix}
}

type docRecord struct {
	ID       string `bson:"_id"`
	Path     string `bson:"path"`
	Type     string `bson:"type"`
	Status   string `bson:"status"`
	Priority string `bson:"priority"`
	Title    string `bson:"title"`
	Updated  string `bson:"updated"`
	Document string `bson:"document"`
}

type templateRecord struct {
	Key     string `bson:"_id"` // "<kind>:<id>"
	Kind    string `bson:"kind"`
	ID      string `bson:"id"`
	Content string `bson:"content"`
	Updated string `bson:"updated"`
}

func templateKey(kindName, id string) string { return kindName + ":" + id }

func (s *Store) Name() string   { return s.name }
func (s *Store) Driver() string { return "mongodb" }

func (s *Store) collection(kindName string) (*mongo.Collection, error) {
	k, ok := kind.Get(kindName)
	if !ok {
		return nil, fmt.Errorf("unknown document kind %q", kindName)
	}
	return s.db.Collection(s.prefix + k.Plural), nil
}

func (s *Store) Init(ctx context.Context) error {
	if s.client == nil {
		client, err := mongo.Connect(options.Client().ApplyURI(s.uri))
		if err != nil {
			return err
		}
		s.client = client
		s.db = client.Database(s.dbName)
		s.tmpls = s.db.Collection(s.prefix + "templates")
		s.leases = s.db.Collection(s.prefix + "leases")
	}
	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.client.Ping(pingCtx, nil); err != nil {
		return fmt.Errorf("connect mongodb: %w", err)
	}
	for _, k := range kind.All() {
		col, _ := s.collection(k.Name)
		if _, err := col.Indexes().CreateMany(ctx, []mongo.IndexModel{
			{Keys: bson.D{{Key: "status", Value: 1}}},
			{Keys: bson.D{{Key: "type", Value: 1}}},
		}); err != nil {
			return err
		}
	}
	if _, err := s.tmpls.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "kind", Value: 1}}}); err != nil {
		return err
	}
	// Expired leases are free whether or not the TTL monitor removed them
	// yet; the index only keeps the collection small.
	_, err := s.leases.Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "expires", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)})
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
	cur, err := col.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var docs []docRecord
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	out := make([]*model.Document, 0, len(docs))
	for _, d := range docs {
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
	var d docRecord
	err = col.FindOne(ctx, bson.M{"_id": id}).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return decode(kindName, d)
}

// record renders d into its stored form.
func (s *Store) record(d *model.Document) (*mongo.Collection, docRecord, error) {
	col, err := s.collection(d.Kind)
	if err != nil {
		return nil, docRecord{}, err
	}
	doc, err := markdown.RenderDocument(d)
	if err != nil {
		return nil, docRecord{}, err
	}
	fm := d.FrontMatter
	id, ok := model.CanonicalID(fm.ID)
	if !ok {
		return nil, docRecord{}, fmt.Errorf("invalid document id %q", fm.ID)
	}
	return col, docRecord{ID: id, Path: d.Path, Type: fm.Type, Status: fm.Status, Priority: fm.Priority, Title: fm.Title, Updated: fm.Updated, Document: doc}, nil
}

func (s *Store) Put(ctx context.Context, d *model.Document) error {
	col, rec, err := s.record(d)
	if err != nil {
		return err
	}
	_, err = col.ReplaceOne(ctx, bson.M{"_id": rec.ID}, rec, options.Replace().SetUpsert(true))
	return err
}

// PutIf replaces d only while the stored record's updated is prevUpdated
// (store.ConditionalPutter): one conditional ReplaceOne without upsert, so
// a concurrent writer between the read and this write makes it match
// nothing and return store.ErrPrecondition.
func (s *Store) PutIf(ctx context.Context, d *model.Document, prevUpdated string) error {
	col, rec, err := s.record(d)
	if err != nil {
		return err
	}
	res, err := col.ReplaceOne(ctx, bson.M{"_id": rec.ID, "updated": prevUpdated}, rec)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%w: %s %s", store.ErrPrecondition, d.Kind, rec.ID)
	}
	return nil
}

// AcquireLease takes the lease on a document for ttl (store.Leaser) in
// one upsert: a free or expired lease record matches and is taken over; an
// unexpired one makes the upsert insert a duplicate _id, which is
// store.ErrLeased. Every process sharing the database sees the lease.
func (s *Store) AcquireLease(ctx context.Context, kindName, id string, ttl time.Duration) (string, error) {
	if _, err := s.collection(kindName); err != nil {
		return "", err
	}
	lease, now := store.NewLeaseID(), time.Now().UTC()
	_, err := s.leases.UpdateOne(ctx,
		bson.M{"_id": kindName + "/" + id, "expires": bson.M{"$lte": now}},
		bson.M{"$set": bson.M{"lease": lease, "expires": now.Add(ttl)}},
		options.UpdateOne().SetUpsert(true))
	if mongo.IsDuplicateKeyError(err) {
		return "", fmt.Errorf("%w: %s %s", store.ErrLeased, kindName, id)
	}
	if err != nil {
		return "", err
	}
	return lease, nil
}

// ReleaseLease ends the lease when lease still holds it (store.Leaser).
func (s *Store) ReleaseLease(ctx context.Context, kindName, id, lease string) error {
	_, err := s.leases.DeleteOne(ctx, bson.M{"_id": kindName + "/" + id, "lease": lease})
	return err
}

func (s *Store) Delete(ctx context.Context, kindName, id string) error {
	col, err := s.collection(kindName)
	if err != nil {
		return err
	}
	res, err := col.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *Store) ListTemplates(ctx context.Context, kindName string) ([]*model.Template, error) {
	cur, err := s.tmpls.Find(ctx, bson.M{"kind": kindName}, options.Find().SetSort(bson.D{{Key: "id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	var recs []templateRecord
	if err := cur.All(ctx, &recs); err != nil {
		return nil, err
	}
	out := make([]*model.Template, 0, len(recs))
	for _, r := range recs {
		out = append(out, &model.Template{Kind: r.Kind, ID: r.ID, Content: r.Content, Updated: r.Updated})
	}
	return out, nil
}

func (s *Store) GetTemplate(ctx context.Context, kindName, id string) (*model.Template, error) {
	var r templateRecord
	err := s.tmpls.FindOne(ctx, bson.M{"_id": templateKey(kindName, id)}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, store.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &model.Template{Kind: r.Kind, ID: r.ID, Content: r.Content, Updated: r.Updated}, nil
}

func (s *Store) PutTemplate(ctx context.Context, t *model.Template) error {
	updated := t.Updated
	if updated == "" {
		updated = time.Now().UTC().Format(time.RFC3339Nano)
	}
	k, ok := kind.Get(t.Kind)
	if !ok {
		return fmt.Errorf("unknown document kind %q", t.Kind)
	}
	id, ok := model.CanonicalTemplateID(t.ID)
	if !ok {
		return fmt.Errorf("invalid template id %q", t.ID)
	}
	rec := templateRecord{Key: templateKey(k.Name, id), Kind: k.Name, ID: id, Content: t.Content, Updated: updated}
	_, err := s.tmpls.ReplaceOne(ctx, bson.M{"_id": rec.Key}, rec, options.Replace().SetUpsert(true))
	return err
}

func (s *Store) DeleteTemplate(ctx context.Context, kindName, id string) error {
	res, err := s.tmpls.DeleteOne(ctx, bson.M{"_id": templateKey(kindName, id)})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return store.ErrNotFound
	}
	return nil
}

var (
	_ store.Store             = (*Store)(nil)
	_ store.ConditionalPutter = (*Store)(nil)
	_ store.Leaser            = (*Store)(nil)
)
