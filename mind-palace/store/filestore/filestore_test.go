package filestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

func newDoc(k, id, typ, slug string) *model.Document {
	return &model.Document{
		Kind: k,
		Path: model.FileName(id, typ, slug),
		FrontMatter: model.FrontMatter{ID: id, Title: "T " + id, Type: typ, Status: "pending", Priority: "1",
			Created: "2026-01-01T00:00:00Z", Updated: "2026-01-01T00:00:00Z",
			Links:    &model.Links{Plans: []model.Link{{ID: "0009-zzz", Relation: "blocks"}}},
			Progress: model.Progress{"1.1": {Status: "pending"}}},
		Content: "# T " + id + "\n\n## Phase 1: A\n\n### 1.1: B\n",
	}
}

func TestFileStoreCRUD(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dir := filepath.Join(root, "plans")
	s := New("file", map[string]string{kind.Plan: dir, kind.Story: filepath.Join(root, "stories")})
	if err := s.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "stories", "archive")); err != nil {
		t.Error("init should create every kind's directories")
	}
	// Non-document files are ignored.
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("# hi"), 0o644)

	d := newDoc(kind.Plan, "0001-abc", "dsgn", "first")
	if err := s.Put(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001-abc-dsgn-first.md")); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, kind.Plan, "0001-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !store.DocumentsEqual(got, d) {
		t.Errorf("round trip mismatch:\n%+v\n%+v", got, d)
	}
	// Stories live in their own root with the same id space.
	st := newDoc(kind.Story, "0001-abc", "bugs", "first")
	if err := s.Put(ctx, st); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "stories", "0001-abc-bugs-first.md")); err != nil {
		t.Error("story not written to the stories root")
	}
	if got, _ := s.Get(ctx, kind.Plan, "0001-abc"); got.FrontMatter.Type != "dsgn" {
		t.Error("story overwrote the plan with the same id")
	}
	// Rename (type change) moves the file.
	d.Path = model.FileName("0001-abc", "impl", "first")
	d.FrontMatter.Type = "impl"
	if err := s.Put(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "0001-abc-dsgn-first.md")); !errors.Is(err, os.ErrNotExist) {
		t.Error("old file should be removed after rename")
	}
	// Archive moves into archive/.
	d.Path = "archive/" + model.FileName("0001-abc", "impl", "first")
	d.FrontMatter.Status = "archived"
	if err := s.Put(ctx, d); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "archive", "0001-abc-impl-first.md")); err != nil {
		t.Error("archived file missing")
	}
	got, _ = s.Get(ctx, kind.Plan, "0001-abc")
	if got.Path != d.Path || got.FrontMatter.Status != "archived" {
		t.Errorf("archived document: %+v", got)
	}
	list, _ := s.List(ctx, kind.Plan)
	if len(list) != 1 {
		t.Errorf("list = %d", len(list))
	}
	if err := s.Delete(ctx, kind.Plan, "0001-abc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, kind.Plan, "0001-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := s.Delete(ctx, kind.Plan, "0001-abc"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
	if _, err := s.Get(ctx, kind.Story, "0001-abc"); err != nil {
		t.Error("deleting the plan must not touch the story")
	}
	if err := s.Put(ctx, &model.Document{Kind: "bogus", Path: "x.md", FrontMatter: model.FrontMatter{ID: "0001-abc"}}); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestFileStoreTemplates(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := New("file", map[string]string{kind.Plan: filepath.Join(root, "plans"), kind.Story: filepath.Join(root, "stories")})
	s.Init(ctx)
	if list, err := s.ListTemplates(ctx, kind.Plan); err != nil || len(list) != 0 {
		t.Errorf("empty templates: %v %v", list, err)
	}
	if err := s.PutTemplate(ctx, &model.Template{Kind: kind.Story, ID: "tiny", Content: "# {{ .Title }}\n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "stories", "templates", "tiny.md")); err != nil {
		t.Error("story template not written under the stories root")
	}
	got, err := s.GetTemplate(ctx, kind.Story, "tiny")
	if err != nil || got.Content != "# {{ .Title }}\n" || got.Updated == "" || got.Kind != kind.Story {
		t.Errorf("get template: %+v %v", got, err)
	}
	if _, err := s.GetTemplate(ctx, kind.Plan, "tiny"); !errors.Is(err, store.ErrNotFound) {
		t.Error("templates are per kind")
	}
	if _, err := s.GetTemplate(ctx, kind.Story, "../etc/passwd"); err == nil {
		t.Error("path traversal id accepted")
	}
	if err := s.DeleteTemplate(ctx, kind.Story, "tiny"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteTemplate(ctx, kind.Story, "tiny"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete: %v", err)
	}
}

func TestFactoryOptions(t *testing.T) {
	dirs := map[string]string{kind.Plan: "/cfg/plans", kind.Story: "/cfg/stories"}
	s, err := store.Open(config.StorageDef{Name: "f", Kind: "file", Options: map[string]string{"dir": "/opt/plans"}}, dirs)
	if err != nil {
		t.Fatal(err)
	}
	fs := s.(*Store)
	if fs.Root(kind.Plan) != "/opt/plans" || fs.Root(kind.Story) != "/cfg/stories" {
		t.Errorf("legacy dir option: %q %q", fs.Root(kind.Plan), fs.Root(kind.Story))
	}
	s, _ = store.Open(config.StorageDef{Name: "f", Kind: "file", Options: map[string]string{"storiesDir": "/opt/stories"}}, dirs)
	if fs := s.(*Store); fs.Root(kind.Story) != "/opt/stories" || fs.Root(kind.Plan) != "/cfg/plans" {
		t.Errorf("storiesDir option: %q %q", fs.Root(kind.Plan), fs.Root(kind.Story))
	}
	s, _ = store.Open(config.StorageDef{Name: "f", Kind: "file"}, nil)
	if fs := s.(*Store); fs.Root(kind.Plan) != "docs/plans" || fs.Root(kind.Story) != "docs/stories" {
		t.Errorf("defaults: %q %q", fs.Root(kind.Plan), fs.Root(kind.Story))
	}
}
