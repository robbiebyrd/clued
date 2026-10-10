package filestore

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/plan/store"
	"github.com/robbiebyrd/clued/plan/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		s := New("file", t.TempDir())
		if err := s.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	})
}
