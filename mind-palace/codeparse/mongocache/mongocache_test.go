package mongocache

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

const uriEnv = "MIND_PALACE_TEST_MONGODB_URI"

var dbCounter atomic.Int64

// testDB connects to MIND_PALACE_TEST_MONGODB_URI and returns a fresh
// database, dropped on cleanup, and the URI; it skips without the variable.
func testDB(t *testing.T) (*mongo.Database, string) {
	t.Helper()
	uri := os.Getenv(uriEnv)
	if uri == "" {
		t.Skip("set " + uriEnv + " to run the MongoDB parse cache tests")
	}
	name := fmt.Sprintf("clued_codeparse_test_%d_%d", time.Now().UnixNano(), dbCounter.Add(1))
	return connect(t, uri, name), uri
}

func connect(t *testing.T, uri, name string) *mongo.Database {
	t.Helper()
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	db := client.Database(name)
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	return db
}

func newCache(t *testing.T, db *mongo.Database, opts Options) *Cache {
	t.Helper()
	c := New(db, opts)
	if err := c.EnsureIndexes(context.Background()); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}
	return c
}

func sampleFacts() codeparse.Facts {
	text := "func (s *Server) ServeHTTP() {}"
	return codeparse.Facts{
		Lang:    "go",
		Package: "srv",
		Symbols: []codeparse.Symbol{
			{ID: "srv.Server", Kind: codeparse.KindStruct, Name: "Server", QualName: "srv.Server",
				Start: codeparse.Pos{Line: 3, Offset: 13}, End: codeparse.Pos{Line: 3, Column: 22, Offset: 35}, Exported: true},
			{ID: "srv.Server.ServeHTTP", Kind: codeparse.KindMethod, Name: "ServeHTTP", QualName: "srv.Server.ServeHTTP",
				Signature: "func (s *Server) ServeHTTP()", Doc: "ServeHTTP serves.", Parent: "srv.Server",
				Start: codeparse.Pos{Line: 6, Offset: 60}, End: codeparse.Pos{Line: 6, Column: 31, Offset: 91}, Exported: true},
		},
		Imports: []codeparse.Import{{Spec: "net/http", Alias: "h", Names: []string{"a", "b"}, Start: codeparse.Pos{Line: 2, Offset: 7}}},
		Refs: []codeparse.Ref{{Name: "Handle", Qualifier: "h", Kind: codeparse.RefCall,
			Start: codeparse.Pos{Line: 6, Column: 20, Offset: 80}, Enclosing: "srv.Server.ServeHTTP"}},
		Chunks: []codeparse.Chunk{{ID: "srv.Server.ServeHTTP#1", Kind: codeparse.ChunkSymbol, SymbolID: "srv.Server.ServeHTTP",
			Start: codeparse.Pos{Line: 6, Offset: 60}, End: codeparse.Pos{Line: 6, Column: 31, Offset: 91},
			Text: text, Hash: codeparse.HashText(text)}},
	}
}

func TestGetPutRoundTrip(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	c := newCache(t, db, Options{})
	key := codeparse.ContentKey([]byte("package srv"), "1/go@abc")
	if _, ok, err := c.Get(ctx, key); ok || err != nil {
		t.Fatalf("Get on empty cache: %v %v", ok, err)
	}
	f := sampleFacts()
	if err := c.Put(ctx, key, f); err != nil {
		t.Fatal(err)
	}
	f.Symbols[0].Name = "changed after Put"
	got, ok, err := c.Get(ctx, key)
	if !ok || err != nil || !reflect.DeepEqual(got, sampleFacts()) {
		t.Fatalf("Get = %+v %v %v", got, ok, err)
	}
	got.Symbols[0].Name = "changed after Get"
	if again, _, _ := c.Get(ctx, key); again.Symbols[0].Name != "Server" {
		t.Error("Get returned a shared copy")
	}

	// Another version of the same content misses: lazy invalidation.
	if _, ok, _ := c.Get(ctx, codeparse.Key{SHA256: key.SHA256, Version: "2/go@abc"}); ok {
		t.Error("hit across versions")
	}
	if err := c.Put(ctx, key, codeparse.Facts{Lang: "go"}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := c.Get(ctx, key); !reflect.DeepEqual(got, codeparse.Facts{Lang: "go"}) {
		t.Errorf("Put did not replace: %+v", got)
	}
	if n, err := c.Len(ctx); n != 1 || err != nil {
		t.Errorf("Len = %d %v", n, err)
	}

	var r record
	if err := db.Collection(Collection).FindOne(ctx, bson.D{{Key: "_id", Value: key.SHA256 + "@1/go@abc"}}).Decode(&r); err != nil {
		t.Fatalf("stored record: %v", err)
	}
	if r.SHA256 != key.SHA256 || r.Version != key.Version || r.Lang != "go" || r.PutAt.IsZero() {
		t.Errorf("record = %+v", r)
	}
}

func TestInvalidKey(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	c := newCache(t, db, Options{})
	for _, k := range []codeparse.Key{{}, {SHA256: strings.Repeat("A", 64), Version: "1"}, {SHA256: strings.Repeat("a", 64)}} {
		if err := c.Put(ctx, k, sampleFacts()); !errors.Is(err, codeparse.ErrInvalidKey) {
			t.Errorf("Put(%+v) = %v", k, err)
		}
		if _, _, err := c.Get(ctx, k); !errors.Is(err, codeparse.ErrInvalidKey) {
			t.Errorf("Get(%+v) = %v", k, err)
		}
	}
	if _, err := c.DeleteVersion(ctx, ""); !errors.Is(err, codeparse.ErrInvalidKey) {
		t.Errorf("DeleteVersion(\"\") = %v", err)
	}
}

// Cached through a second client on the same database (a restart, another
// replica) finds what the first parsed: each version is parsed once.
func TestCachedSurvivesRestart(t *testing.T) {
	db, uri := testDB(t)
	ctx := context.Background()
	key := codeparse.ContentKey([]byte("package srv"), codeparse.Version)
	parses := 0
	parse := func() (codeparse.Facts, error) { parses++; return sampleFacts(), nil }

	first := newCache(t, db, Options{})
	if _, err := codeparse.Cached(ctx, first, key, parse); err != nil {
		t.Fatal(err)
	}
	second := newCache(t, connect(t, uri, db.Name()), Options{})
	got, err := codeparse.Cached(ctx, second, key, parse)
	if err != nil || !reflect.DeepEqual(got, sampleFacts()) {
		t.Fatalf("Cached after restart = %+v %v", got, err)
	}
	if parses != 1 {
		t.Errorf("parsed %d times, want 1", parses)
	}
	// A failed parse stores nothing.
	other := codeparse.ContentKey([]byte("broken"), codeparse.Version)
	if _, err := codeparse.Cached(ctx, second, other, func() (codeparse.Facts, error) { return codeparse.Facts{}, errors.New("boom") }); err == nil {
		t.Fatal("parse error not returned")
	}
	if _, ok, _ := second.Get(ctx, other); ok {
		t.Error("a failed parse was cached")
	}
}

// An entry that no longer decodes is a miss, and the next Put replaces it.
func TestUndecodableEntryIsAMiss(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	c := newCache(t, db, Options{Logger: slog.New(slog.DiscardHandler)})
	key := codeparse.ContentKey([]byte("x"), "1")
	if _, err := db.Collection(Collection).InsertOne(ctx, record{ID: id(key), SHA256: key.SHA256, Version: key.Version, Facts: []byte("{not json")}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.Get(ctx, key); ok || err != nil {
		t.Fatalf("Get of a corrupt entry = %v %v, want a miss", ok, err)
	}
	got, err := codeparse.Cached(ctx, c, key, func() (codeparse.Facts, error) { return sampleFacts(), nil })
	if err != nil || !reflect.DeepEqual(got, sampleFacts()) {
		t.Fatalf("Cached = %+v %v", got, err)
	}
	if got, ok, _ := c.Get(ctx, key); !ok || !reflect.DeepEqual(got, sampleFacts()) {
		t.Errorf("corrupt entry not replaced: %+v %v", got, ok)
	}
}

func TestPutTooLarge(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	c := newCache(t, db, Options{})
	key := codeparse.ContentKey([]byte("huge"), "1")
	big := codeparse.Facts{Lang: "go", Package: strings.Repeat("x", MaxFactsBytes)}
	if err := c.Put(ctx, key, big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Put = %v, want ErrTooLarge", err)
	}
	// Cached still hands the parse back, with the cache failure joined.
	got, err := codeparse.Cached(ctx, c, key, func() (codeparse.Facts, error) { return big, nil })
	if !errors.Is(err, ErrTooLarge) || got.Package != big.Package {
		t.Errorf("Cached = %d bytes, %v", len(got.Package), err)
	}
}

func TestDeleteVersion(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	c := newCache(t, db, Options{})
	for i, v := range []string{"1", "1", "2"} {
		if err := c.Put(ctx, codeparse.ContentKey([]byte{byte(i)}, v), codeparse.Facts{Lang: "go"}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := c.DeleteVersion(ctx, "1")
	if err != nil || n != 2 {
		t.Fatalf("DeleteVersion = %d %v", n, err)
	}
	if _, ok, _ := c.Get(ctx, codeparse.ContentKey([]byte{2}, "2")); !ok {
		t.Error("another version was deleted")
	}
	if n, _ := c.Len(ctx); n != 1 {
		t.Errorf("Len = %d", n)
	}
}

func ttlSeconds(t *testing.T, db *mongo.Database) (int32, bool) {
	t.Helper()
	cur, err := db.Collection(Collection).Indexes().List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var specs []bson.M
	if err := cur.All(context.Background(), &specs); err != nil {
		t.Fatal(err)
	}
	for _, s := range specs {
		if s["name"] == ttlIndex {
			v, _ := s["expireAfterSeconds"].(int32)
			return v, true
		}
	}
	return 0, false
}

func TestTTLIndex(t *testing.T) {
	db, _ := testDB(t)
	newCache(t, db, Options{TTL: time.Hour})
	if secs, ok := ttlSeconds(t, db); !ok || secs != 3600 {
		t.Fatalf("ttl index = %d %v, want 3600", secs, ok)
	}
	newCache(t, db, Options{TTL: 2 * time.Hour}) // moved: collMod
	if secs, ok := ttlSeconds(t, db); !ok || secs != 7200 {
		t.Fatalf("ttl index after change = %d %v, want 7200", secs, ok)
	}
	newCache(t, db, Options{}) // off: dropped
	if _, ok := ttlSeconds(t, db); ok {
		t.Fatal("ttl index kept with TTL off")
	}
	newCache(t, db, Options{}) // dropping an absent index is fine
}

// A read moves used_at forward once it is older than an eighth of the TTL,
// so used entries do not expire.
func TestGetTouchesUsedAt(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	c := newCache(t, db, Options{TTL: 8 * time.Hour, Now: clock})
	key := codeparse.ContentKey([]byte("touch"), "1")
	if err := c.Put(ctx, key, sampleFacts()); err != nil {
		t.Fatal(err)
	}
	usedAt := func() time.Time {
		var r record
		if err := db.Collection(Collection).FindOne(ctx, bson.D{{Key: "_id", Value: id(key)}}).Decode(&r); err != nil {
			t.Fatal(err)
		}
		return r.UsedAt
	}
	put := usedAt()
	advance(30 * time.Minute) // under TTL/8: no write
	if _, ok, _ := c.Get(ctx, key); !ok {
		t.Fatal("miss")
	}
	if !usedAt().Equal(put) {
		t.Errorf("used_at moved after 30m: %v", usedAt())
	}
	advance(time.Hour) // past TTL/8
	if _, ok, _ := c.Get(ctx, key); !ok {
		t.Fatal("miss")
	}
	if want := put.Add(90 * time.Minute); !usedAt().Equal(want) {
		t.Errorf("used_at = %v, want %v", usedAt(), want)
	}
}

func TestConcurrentUse(t *testing.T) {
	db, _ := testDB(t)
	ctx := context.Background()
	c := newCache(t, db, Options{})
	key := codeparse.ContentKey([]byte("shared"), "1")
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := codeparse.Cached(ctx, c, key, func() (codeparse.Facts, error) { return sampleFacts(), nil })
			if err == nil && !reflect.DeepEqual(got, sampleFacts()) {
				err = fmt.Errorf("Cached = %+v", got)
			}
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if n, _ := c.Len(ctx); n != 1 {
		t.Errorf("Len = %d", n)
	}
}
