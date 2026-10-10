package sessiontest

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/mongosession"
)

// MongoURIEnv names the environment variable that enables the MongoDB run.
const MongoURIEnv = "MIND_PALACE_TEST_MONGODB_URI"

var dbCounter atomic.Int64

// EachStore runs fn as a "memsession" subtest and, when MongoURIEnv is set, as
// a "mongosession" subtest on a per-run database that is dropped on cleanup.
// Each subtest receives a fresh, empty store.
func EachStore(t *testing.T, fn func(t *testing.T, st session.Store)) {
	t.Helper()
	t.Run("memsession", func(t *testing.T) { fn(t, memsession.New()) })
	uri := os.Getenv(MongoURIEnv)
	if uri == "" {
		return
	}
	t.Run("mongosession", func(t *testing.T) { fn(t, mongoStore(t, uri)) })
}

func mongoStore(t *testing.T, uri string) session.Store {
	t.Helper()
	ctx := context.Background()
	dbName := fmt.Sprintf("clued_sessions_test_%d_%d", time.Now().UnixNano(), dbCounter.Add(1))
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	s := mongosession.New(session.Config{MongoURL: uri, DBName: dbName})
	s.Logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	t.Cleanup(func() {
		_ = s.Close(ctx)
		if err := client.Database(dbName).Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", dbName, err)
		}
		_ = client.Disconnect(ctx)
	})
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return s
}
