package mongostore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/storetest"
)

// Runs only with MIND_PALACE_TEST_MONGODB_URI=mongodb://localhost:27017.
func TestMongo(t *testing.T) {
	uri := os.Getenv("MIND_PALACE_TEST_MONGODB_URI")
	if uri == "" {
		t.Skip("set MIND_PALACE_TEST_MONGODB_URI to run the MongoDB conformance suite")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		s := New("test", uri, "mind_palace_test", "t"+time.Now().Format("150405000000")+"_")
		if err := s.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			s.db.Drop(context.Background())
			s.Close()
		})
		return s
	})
}
