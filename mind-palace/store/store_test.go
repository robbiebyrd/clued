package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

func plan(id string) *model.Document {
	return &model.Document{Kind: kind.Plan, Path: model.FileName(id, "dsgn", "x"), FrontMatter: model.FrontMatter{ID: id, Title: "x", Type: "dsgn", Status: "pending", Priority: "1"}, Content: "# x\n"}
}

func story(id string) *model.Document {
	return &model.Document{Kind: kind.Story, Path: model.FileName(id, "bugs", "x"), FrontMatter: model.FrontMatter{ID: id, Title: "x", Type: "bugs", Status: "pending", Priority: "1"}, Content: "# x\n"}
}

func TestMultiFanout(t *testing.T) {
	ctx := context.Background()
	a, b := memstore.New("a"), memstore.New("b")
	m := store.NewMulti(a, b)
	if err := m.Put(ctx, plan("0001-aaa")); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*memstore.Store{a, b} {
		if _, err := s.Get(ctx, kind.Plan, "0001-aaa"); err != nil {
			t.Errorf("%s missing plan: %v", s.Name(), err)
		}
	}
	// Secondary failure is reported but the primary write sticks.
	b.FailWrites = errors.New("db down")
	err := m.Put(ctx, story("0002-bbb"))
	var fe *store.FanoutError
	if !errors.As(err, &fe) || fe.Failed["b"] == nil {
		t.Fatalf("expected fanout error, got %v", err)
	}
	if _, err := a.Get(ctx, kind.Story, "0002-bbb"); err != nil {
		t.Error("primary should hold the story")
	}
	// Primary failure is a hard error.
	a.FailWrites = errors.New("disk full")
	if err := m.Put(ctx, plan("0003-ccc")); err == nil || errors.As(err, &fe) {
		t.Errorf("expected primary error, got %v", err)
	}
	a.FailWrites, b.FailWrites = nil, nil
	// Delete tolerates copies that already lack the document.
	b.Delete(ctx, kind.Plan, "0001-aaa")
	if err := m.Delete(ctx, kind.Plan, "0001-aaa"); err != nil {
		t.Errorf("delete: %v", err)
	}
	if _, ok := m.Find("b"); !ok {
		t.Error("Find")
	}
}

func TestSyncModes(t *testing.T) {
	ctx := context.Background()
	src, dst := memstore.New("src"), memstore.New("dst")
	src.Put(ctx, plan("0001-aaa"))
	src.Put(ctx, plan("0002-bbb"))
	src.Put(ctx, story("0001-aaa"))
	same := plan("0003-ccc")
	src.Put(ctx, same)
	dst.Put(ctx, same)
	conflict := plan("0002-bbb")
	conflict.Content = "# changed\n"
	dst.Put(ctx, conflict)
	src.PutTemplate(ctx, &model.Template{Kind: kind.Story, ID: "t", Content: "a"})
	dst.PutTemplate(ctx, &model.Template{Kind: kind.Story, ID: "t", Content: "b"})

	rep, err := store.Sync(ctx, src, dst, store.ConflictError)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected conflict error, got %v", err)
	}
	if got := rep.Documents[kind.Plan].Copied; len(got) != 1 || got[0] != "0001-aaa" {
		t.Errorf("copied = %v", got)
	}
	if got := rep.Conflicts(); len(got) != 1 || got[0] != "plan 0002-bbb" {
		t.Errorf("conflicts = %v", got)
	}
	rep, err = store.Sync(ctx, src, dst, store.ConflictSkip)
	if err != nil || len(rep.Documents[kind.Plan].Conflicts) != 1 || len(rep.Documents[kind.Plan].Unchanged) != 2 || len(rep.TemplateConfl) != 1 || rep.TemplateConfl[0] != "story/t" {
		t.Errorf("skip: %+v %v", rep, err)
	}
	if got := rep.Documents[kind.Story]; len(got.Copied) != 1 && len(got.Unchanged) != 1 {
		t.Errorf("stories not synced: %+v", got)
	}
	got, _ := dst.Get(ctx, kind.Plan, "0002-bbb")
	if got.Content != "# changed\n" {
		t.Error("skip should not overwrite")
	}
	rep, err = store.Sync(ctx, src, dst, store.ConflictOverwrite)
	if err != nil || len(rep.Documents[kind.Plan].Overwritten) != 1 || len(rep.Templates) != 1 {
		t.Errorf("overwrite: %+v %v", rep, err)
	}
	got, _ = dst.Get(ctx, kind.Plan, "0002-bbb")
	if got.Content != "# x\n" {
		t.Error("overwrite should replace")
	}
	if _, err := dst.Get(ctx, kind.Story, "0001-aaa"); err != nil {
		t.Error("story not copied by sync")
	}
	if _, err := store.ParseConflictMode("nope"); err == nil {
		t.Error("bad mode accepted")
	}
}

func TestOpenAll(t *testing.T) {
	cfg := config.Default()
	cfg.Storage = []config.StorageDef{{Name: "m1", Kind: "memory"}, {Name: "m2", Kind: "memory"}}
	m, err := store.OpenAll(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Stores()) != 2 || m.Primary().Name() != "m1" {
		t.Errorf("stores: %v", m.Stores())
	}
	cfg.Storage = []config.StorageDef{{Name: "x", Kind: "nope"}}
	if _, err := store.OpenAll(context.Background(), cfg); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestDocumentsEqualNormalises(t *testing.T) {
	a, b := plan("0001-aaa"), plan("0001-aaa")
	a.FrontMatter.Links = &model.Links{Web: map[string]string{}, Plans: []model.Link{}}
	if !store.DocumentsEqual(a, b) {
		t.Error("empty collections should compare equal to nil")
	}
	c := story("0001-aaa")
	c.Path, c.FrontMatter.Type = b.Path, b.FrontMatter.Type
	if store.DocumentsEqual(c, b) {
		t.Error("different kinds must not compare equal")
	}
}
