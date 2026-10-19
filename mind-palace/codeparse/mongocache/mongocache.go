// Package mongocache is the MongoDB codeparse.Cache: one document per parse
// in the code_parses collection, keyed by the content's SHA-256 and the facts
// version, so a file version is parsed once across branches, layers and
// members, and the cache survives restarts.
//
// Facts are stored serialized (their JSON form, the shape codeparse fixes
// with its tags), not mapped field by field, so a change to Facts needs no
// change here. Invalidation is lazy: a version bump makes the old entries
// unreachable, and an entry that no longer decodes is a miss that the next
// Put overwrites. Unreachable entries are removed by DeleteVersion or, when
// Options.TTL is set, by the TTL index once they go unused that long.
package mongocache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// Collection is the default collection name.
const Collection = "code_parses"

// MaxFactsBytes bounds the serialized facts of one entry, below MongoDB's
// 16 MiB document limit; Put refuses larger facts with ErrTooLarge (Cached
// still returns them, so the file is indexed, only not cached).
const MaxFactsBytes = 15 << 20

// ErrTooLarge is returned by Put for facts whose serialized form exceeds
// MaxFactsBytes.
var ErrTooLarge = errors.New("mongocache: facts too large to cache")

// ttlIndex names the TTL index on used_at.
const ttlIndex = "code_parses_ttl"

// Options configure a Cache.
type Options struct {
	// Collection overrides the collection name (default Collection).
	Collection string
	// TTL, when positive, expires entries not read or written for that
	// long; zero keeps entries until DeleteVersion removes them.
	TTL time.Duration
	// Logger receives failures of best-effort writes (the used_at touch);
	// nil means slog.Default().
	Logger *slog.Logger
	// Now returns the current time; nil means time.Now (tests).
	Now func() time.Time
}

// Cache is a codeparse.Cache over a MongoDB collection. It is safe for
// concurrent use.
type Cache struct {
	coll   *mongo.Collection
	ttl    time.Duration
	logger *slog.Logger
	now    func() time.Time
}

var _ codeparse.Cache = (*Cache)(nil)

// New returns a cache on db; call EnsureIndexes once before use.
func New(db *mongo.Database, opts Options) *Cache {
	name := opts.Collection
	if name == "" {
		name = Collection
	}
	c := &Cache{coll: db.Collection(name), ttl: opts.TTL, logger: opts.Logger, now: opts.Now}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// record is the stored form of one entry. _id is "<sha256>@<version>", so a
// lookup is one primary-key read and a Put one upsert; the key fields are
// kept too, for DeleteVersion and inspection.
type record struct {
	ID      string    `bson:"_id"`
	SHA256  string    `bson:"sha256"`
	Version string    `bson:"version"`
	Facts   []byte    `bson:"facts"`
	PutAt   time.Time `bson:"put_at"`
	UsedAt  time.Time `bson:"used_at"`
	Lang    string    `bson:"lang,omitempty"`
}

func id(key codeparse.Key) string { return key.SHA256 + "@" + key.Version }

// EnsureIndexes creates the version index (for DeleteVersion) and, when TTL
// is set, the TTL index on used_at, changing its expiry when it moved; with
// no TTL it drops a TTL index left by an earlier configuration.
func (c *Cache) EnsureIndexes(ctx context.Context) error {
	ix := c.coll.Indexes()
	if _, err := ix.CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "version", Value: 1}}}); err != nil {
		return fmt.Errorf("mongocache: version index: %w", err)
	}
	if c.ttl <= 0 {
		if err := ix.DropOne(ctx, ttlIndex); err != nil && !isIndexNotFound(err) {
			return fmt.Errorf("mongocache: drop ttl index: %w", err)
		}
		return nil
	}
	secs := int32(max(c.ttl/time.Second, 1))
	model := mongo.IndexModel{Keys: bson.D{{Key: "used_at", Value: 1}},
		Options: options.Index().SetName(ttlIndex).SetExpireAfterSeconds(secs)}
	if _, err := ix.CreateOne(ctx, model); err == nil {
		return nil
	}
	cmd := bson.D{{Key: "collMod", Value: c.coll.Name()},
		{Key: "index", Value: bson.D{{Key: "name", Value: ttlIndex}, {Key: "expireAfterSeconds", Value: secs}}}}
	if err := c.coll.Database().RunCommand(ctx, cmd).Err(); err != nil {
		return fmt.Errorf("mongocache: ttl index: %w", err)
	}
	return nil
}

// isIndexNotFound reports a DropOne of an index (or collection) that does
// not exist.
func isIndexNotFound(err error) bool {
	var ce mongo.CommandError
	return errors.As(err, &ce) && (ce.Code == 27 || ce.Code == 26) // IndexNotFound, NamespaceNotFound
}

// Get implements codeparse.Cache. An entry that does not decode is reported
// as a miss, so the caller re-parses and its Put replaces it.
func (c *Cache) Get(ctx context.Context, key codeparse.Key) (codeparse.Facts, bool, error) {
	if err := key.Validate(); err != nil {
		return codeparse.Facts{}, false, err
	}
	var r record
	err := c.coll.FindOne(ctx, bson.D{{Key: "_id", Value: id(key)}}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return codeparse.Facts{}, false, nil
	}
	if err != nil {
		return codeparse.Facts{}, false, fmt.Errorf("mongocache: get: %w", err)
	}
	var facts codeparse.Facts
	if err := json.Unmarshal(r.Facts, &facts); err != nil {
		c.logger.Warn("mongocache: undecodable entry, re-parsing", "key", r.ID, "error", err)
		return codeparse.Facts{}, false, nil
	}
	c.touch(ctx, r)
	return facts, true, nil
}

// touch moves used_at forward when the TTL is on and the entry was last
// used more than an eighth of the TTL ago, so a hot entry costs a write at
// most a few times per TTL. It is best effort: a failure is logged.
func (c *Cache) touch(ctx context.Context, r record) {
	if c.ttl <= 0 {
		return
	}
	now := c.now().UTC()
	if now.Sub(r.UsedAt) < c.ttl/8 {
		return
	}
	_, err := c.coll.UpdateOne(ctx, bson.D{{Key: "_id", Value: r.ID}},
		bson.D{{Key: "$max", Value: bson.D{{Key: "used_at", Value: now}}}})
	if err != nil {
		c.logger.Warn("mongocache: touch", "key", r.ID, "error", err)
	}
}

// Put implements codeparse.Cache.
func (c *Cache) Put(ctx context.Context, key codeparse.Key, facts codeparse.Facts) error {
	if err := key.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(facts)
	if err != nil {
		return fmt.Errorf("mongocache: encode facts: %w", err)
	}
	if len(data) > MaxFactsBytes {
		return fmt.Errorf("%w: %s (%d bytes)", ErrTooLarge, id(key), len(data))
	}
	now := c.now().UTC()
	r := record{ID: id(key), SHA256: key.SHA256, Version: key.Version, Facts: data, PutAt: now, UsedAt: now, Lang: facts.Lang}
	_, err = c.coll.ReplaceOne(ctx, bson.D{{Key: "_id", Value: r.ID}}, r, options.Replace().SetUpsert(true))
	if err != nil && !mongo.IsDuplicateKeyError(err) {
		return fmt.Errorf("mongocache: put: %w", err)
	}
	// A duplicate key is a concurrent upsert of the same key winning the
	// insert; both hold the facts of the same bytes, so either will do.
	return nil
}

// DeleteVersion removes every entry parsed with version (one no longer
// produced, after a bump) and returns how many it removed.
func (c *Cache) DeleteVersion(ctx context.Context, version string) (int64, error) {
	if version == "" {
		return 0, fmt.Errorf("%w: empty version", codeparse.ErrInvalidKey)
	}
	res, err := c.coll.DeleteMany(ctx, bson.D{{Key: "version", Value: version}})
	if err != nil {
		return 0, fmt.Errorf("mongocache: delete version: %w", err)
	}
	return res.DeletedCount, nil
}

// Len returns the number of entries stored.
func (c *Cache) Len(ctx context.Context) (int64, error) {
	return c.coll.CountDocuments(ctx, bson.D{})
}
