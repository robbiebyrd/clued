package filestore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/storetest"
)

func TestConformance(t *testing.T) {
	storetest.Run(t, func(t *testing.T) store.Store {
		root := t.TempDir()
		s := New("file", map[string]string{"plan": filepath.Join(root, "plans"), "story": filepath.Join(root, "stories")})
		if err := s.Init(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	})
}
