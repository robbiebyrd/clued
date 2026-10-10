package mongosession_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/mongosession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessiontest"
)

const uriEnv = "MIND_PALACE_TEST_MONGODB_URI"

var dbCounter atomic.Int64

// testDB skips the test unless a MongoDB is configured, and returns a client
// and the name of a per-run database that is dropped on cleanup.
func testDB(t *testing.T) (uri, dbName string, client *mongo.Client) {
	t.Helper()
	uri = os.Getenv(uriEnv)
	if uri == "" {
		t.Skipf("%s not set", uriEnv)
	}
	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	dbName = fmt.Sprintf("clued_sessions_test_%d_%d", time.Now().UnixNano(), dbCounter.Add(1))
	t.Cleanup(func() {
		ctx := context.Background()
		if err := client.Database(dbName).Drop(ctx); err != nil {
			t.Errorf("drop %s: %v", dbName, err)
		}
		_ = client.Disconnect(ctx)
	})
	return uri, dbName, client
}

func TestConformance(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) session.Store {
		uri, dbName, _ := testDB(t)
		s := mongosession.New(session.Config{MongoURL: uri, DBName: dbName})
		s.Logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
		if err := s.Connect(context.Background()); err != nil {
			t.Fatalf("Connect: %v", err)
		}
		t.Cleanup(func() { _ = s.Close(context.Background()) })
		return s
	})
}

func TestConnectTwiceLeavesOneViewWithPipeline(t *testing.T) {
	uri, dbName, client := testDB(t)
	ctx := context.Background()
	s := mongosession.New(session.Config{MongoURL: uri, DBName: dbName})
	s.Logger = slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	t.Cleanup(func() { _ = s.Close(ctx) })
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("first Connect: %v", err)
	}
	// A view left behind with a stale pipeline must be replaced, not kept.
	stale := bson.D{{Key: "collMod", Value: "session_full"}, {Key: "viewOn", Value: "sessions"}, {Key: "pipeline", Value: bson.A{}}}
	if err := client.Database(dbName).RunCommand(ctx, stale).Err(); err != nil {
		t.Fatalf("make view stale: %v", err)
	}
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("second Connect: %v", err)
	}

	specs, err := client.Database(dbName).ListCollectionSpecifications(ctx, bson.D{{Key: "name", Value: "session_full"}})
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("session_full listed %d times, want 1", len(specs))
	}
	if specs[0].Type != "view" {
		t.Errorf("session_full type = %q, want view", specs[0].Type)
	}
	var opts struct {
		ViewOn   string
		Pipeline []struct {
			Lookup struct{ From string } `bson:"$lookup"`
		}
	}
	if err := bson.Unmarshal(specs[0].Options, &opts); err != nil {
		t.Fatalf("decode options: %v", err)
	}
	if opts.ViewOn != "sessions" {
		t.Errorf("viewOn = %q, want sessions", opts.ViewOn)
	}
	if len(opts.Pipeline) != 4 {
		t.Fatalf("pipeline has %d stages, want 4", len(opts.Pipeline))
	}
	var froms []string
	for _, stage := range opts.Pipeline {
		froms = append(froms, stage.Lookup.From)
	}
	if got := strings.Join(froms, ","); got != "transcript_lines,subagent_lines,blobs,hook_events" {
		t.Errorf("lookup order = %s", got)
	}
}

func TestExistingNonUniqueIndexIsAWarningNotAnError(t *testing.T) {
	uri, dbName, client := testDB(t)
	ctx := context.Background()
	_, err := client.Database(dbName).Collection("transcript_lines").Indexes().CreateOne(ctx,
		mongo.IndexModel{Keys: bson.D{{Key: "session_id", Value: 1}, {Key: "seq", Value: 1}}})
	if err != nil {
		t.Fatalf("seed index: %v", err)
	}

	var logs bytes.Buffer
	s := mongosession.New(session.Config{MongoURL: uri, DBName: dbName})
	s.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	t.Cleanup(func() { _ = s.Close(ctx) })
	if err := s.Connect(ctx); err != nil {
		t.Fatalf("Connect returned an error: %v", err)
	}
	out := logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, "transcript_lines") {
		t.Errorf("expected a logged index warning for transcript_lines, got %q", out)
	}
	if n := strings.Count(out, "level=WARN"); n != 1 {
		t.Errorf("got %d warnings, want exactly 1: %s", n, out)
	}
}
