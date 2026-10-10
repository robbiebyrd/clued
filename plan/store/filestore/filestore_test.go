package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
)

func newPlan(id, typ, slug string) *model.Plan {
	return &model.Plan{
		Path: model.FileName(id, typ, slug),
		FrontMatter: model.FrontMatter{ID: id, Title: "T " + id, Type: typ, Status: "pending", Priority: "1",
			Created: "2026-01-01T00:00:00Z", Updated: "2026-01-01T00:00:00Z",
			Plans:    []model.Link{{ID: "0009-zzz", Relation: "blocks"}},
			Progress: model.Progress{"1.1": {Status: "pending"}}},
		Content: "# T " + id + "\n\n## Phase 1: A\n\n### 1.1: B\n",
	}
}

func TestFileStoreCRUD(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := New("file", dir)
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	// Non-plan files are ignored.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi"), 0o644)

	p := newPlan("0001-abc", "dsgn", "first")
	if err := s.PutPlan(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001-abc-dsgn-first.md")); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetPlan(ctx, "0001-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !store.PlansEqual(got, p) {
		t.Errorf("round trip mismatch:\n%+v\n%+v", got, p)
	}
	// Rename (type change) moves the file.
	p.Path = model.FileName("0001-abc", "impl", "first")
	p.FrontMatter.Type = "impl"
	if err := s.PutPlan(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001-abc-dsgn-first.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("old file should be removed after rename")
	}
	// Archive moves into archive/.
	p.Path = "archive/" + model.FileName("0001-abc", "impl", "first")
	p.FrontMatter.Status = "archived"
	if err := s.PutPlan(ctx, p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "0001-abc-impl-first.md")); err != nil {
		t.Error("archived file missing")
	}
	got, _ = s.GetPlan(ctx, "0001-abc")
	if got.Path != p.Path || got.FrontMatter.Status != "archived" {
		t.Errorf("archived plan: %+v", got)
	}
	list, _ := s.ListPlans(ctx)
	if len(list) != 1 {
		t.Errorf("list = %d", len(list))
	}
	if err := s.DeletePlan(ctx, "0001-abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPlan(ctx, "0001-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.DeletePlan(ctx, "0001-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
}

func TestFileStoreTemplates(t *testing.T) {
	ctx := context.Background()
	s := New("file", t.TempDir())
	s.Init(ctx)
	if list, err := s.ListTemplates(ctx); err != nil || len(list) != 0 {
		t.Errorf("empty templates: %v %v", list, err)
	}
	if err := s.PutTemplate(ctx, &model.Template{ID: "tiny", Content: "# {{ .Title }}\n"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTemplate(ctx, "tiny")
	if err != nil || got.Content != "# {{ .Title }}\n" || got.Updated == "" {
		t.Errorf("get template: %+v %v", got, err)
	}
	if _, err := s.GetTemplate(ctx, "../etc/passwd"); err == nil {
		t.Error("path traversal id accepted")
	}
	if err := s.DeleteTemplate(ctx, "tiny"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplate(ctx, "tiny"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
}
