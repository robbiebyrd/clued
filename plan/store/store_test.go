package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/robbiebyrd/clued/plan/config"
	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
	"github.com/robbiebyrd/clued/plan/store/memstore"
)

func plan(id string) *model.Plan {
	return &model.Plan{Path: model.FileName(id, "dsgn", "x"), FrontMatter: model.FrontMatter{ID: id, Title: "x", Type: "dsgn", Status: "pending", Priority: "1"}, Content: "# x\n"}
}

func TestMultiFanout(t *testing.T) {
	ctx := context.Background()
	a, b := memstore.New("a"), memstore.New("b")
	m := store.NewMulti(a, b)
	if err := m.PutPlan(ctx, plan("0001-aaa")); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*memstore.Store{a, b} {
		if _, err := s.GetPlan(ctx, "0001-aaa"); err != nil {
			t.Errorf("%s missing plan: %v", s.Name(), err)
		}
	}
	// Secondary failure is reported but the primary write sticks.
	b.FailWrites = errors.New("db down")
	err := m.PutPlan(ctx, plan("0002-bbb"))
	var fe *store.FanoutError
	if !errors.As(err, &fe) || fe.Failed["b"] == nil {
		t.Fatalf("expected fanout error, got %v", err)
	}
	if _, err := a.GetPlan(ctx, "0002-bbb"); err != nil {
		t.Error("primary should hold the plan")
	}
	// Primary failure is a hard error.
	a.FailWrites = errors.New("disk full")
	if err := m.PutPlan(ctx, plan("0003-ccc")); err == nil || errors.As(err, &fe) {
		t.Errorf("expected primary error, got %v", err)
	}
	a.FailWrites, b.FailWrites = nil, nil
	// Delete tolerates copies that already lack the plan.
	b.DeletePlan(ctx, "0001-aaa")
	if err := m.DeletePlan(ctx, "0001-aaa"); err != nil {
		t.Errorf("delete: %v", err)
	}
	if _, ok := m.Find("b"); !ok {
		t.Error("Find")
	}
}

func TestSyncModes(t *testing.T) {
	ctx := context.Background()
	src, dst := memstore.New("src"), memstore.New("dst")
	src.PutPlan(ctx, plan("0001-aaa"))
	src.PutPlan(ctx, plan("0002-bbb"))
	same := plan("0003-ccc")
	src.PutPlan(ctx, same)
	dst.PutPlan(ctx, same)
	conflict := plan("0002-bbb")
	conflict.Content = "# changed\n"
	dst.PutPlan(ctx, conflict)
	src.PutTemplate(ctx, &model.Template{ID: "t", Content: "a"})
	dst.PutTemplate(ctx, &model.Template{ID: "t", Content: "b"})

	rep, err := store.Sync(ctx, src, dst, store.ConflictError)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("expected conflict error, got %v", err)
	}
	if len(rep.Copied) != 1 || rep.Copied[0] != "0001-aaa" {
		t.Errorf("copied = %v", rep.Copied)
	}
	rep, err = store.Sync(ctx, src, dst, store.ConflictSkip)
	if err != nil || len(rep.Conflicts) != 1 || len(rep.Unchanged) != 2 || len(rep.TemplateConfl) != 1 {
		t.Errorf("skip: %+v %v", rep, err)
	}
	got, _ := dst.GetPlan(ctx, "0002-bbb")
	if got.Content != "# changed\n" {
		t.Error("skip should not overwrite")
	}
	rep, err = store.Sync(ctx, src, dst, store.ConflictOverwrite)
	if err != nil || len(rep.Overwritten) != 1 || len(rep.Templates) != 1 {
		t.Errorf("overwrite: %+v %v", rep, err)
	}
	got, _ = dst.GetPlan(ctx, "0002-bbb")
	if got.Content != "# x\n" {
		t.Error("overwrite should replace")
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

func TestPlansEqualNormalises(t *testing.T) {
	a, b := plan("0001-aaa"), plan("0001-aaa")
	a.FrontMatter.Links = &model.Links{Web: map[string]string{}, Plans: []model.Link{}}
	if !store.PlansEqual(a, b) {
		t.Error("empty collections should compare equal to nil")
	}
}
