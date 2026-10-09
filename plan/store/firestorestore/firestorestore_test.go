package firestorestore

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/plan/store"
	"github.com/robbiebyrd/clued/plan/store/storetest"
)

// Runs only with PLAN_TEST_FIRESTORE_PROJECT set (use FIRESTORE_EMULATOR_HOST
// for the local emulator).
func TestFirestore(t *testing.T) {
	project := os.Getenv("PLAN_TEST_FIRESTORE_PROJECT")
	if project == "" {
		t.Skip("set PLAN_TEST_FIRESTORE_PROJECT to run the Firestore conformance suite")
	}
	storetest.Run(t, func(t *testing.T) store.Store {
		s := New("test", project, "", "t"+time.Now().Format("150405000000")+"_")
		if err := s.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx := context.Background()
			for _, c := range []string{s.prefix + "plans", s.prefix + "templates"} {
				docs, _ := s.client.Collection(c).Documents(ctx).GetAll()
				for _, d := range docs {
					d.Ref.Delete(ctx)
				}
			}
			s.Close()
		})
		return s
	})
}
