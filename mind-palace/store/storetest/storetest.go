// Package storetest is a conformance suite every storage plugin must pass.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Factory returns a fresh, empty, initialised store for one test.
type Factory func(t *testing.T) store.Store

func document(k, id, typ, status string) *model.Document {
	path := model.FileName(id, typ, "conformance")
	if status == "archived" {
		path = "archive/" + path
	}
	d := &model.Document{
		Kind: k,
		Path: path,
		FrontMatter: model.FrontMatter{
			ID: id, Title: "Conformance " + id, Type: typ, Status: status, Priority: "2", Effort: "M",
			Created: "2026-01-01T00:00:00.000Z", Updated: "2026-01-01T00:00:00.000Z",
			Links: &model.Links{
				Repo:    &model.Repo{Remote: "git@github.com:org/p.git", Local: "~/p"},
				Specs:   []string{"./docs/x.md"},
				Web:     map[string]string{"jira": "https://jira/1"},
				Stories: []model.Link{{ID: "0001-abc", Relation: "included"}},
				Plans:   []model.Link{{ID: "0009-zzz", Relation: "blocks"}},
			},
			Progress: model.Progress{"1": {Status: "pending"}, "1.1": {Status: "complete", Stories: []string{"0001-abc"}}, "1.10": {Status: "blocked"}},
		},
		Content: fmt.Sprintf("# Conformance %s\n\n## Phase 1: A\n\n### 1.1: B\n\n### 1.10: C\n\nUnicode ✓ and `code`\n", id),
	}
	if k == kind.Story {
		d.FrontMatter.Purpose = "Prove the store round-trips stories"
		d.FrontMatter.Started = "2026-01-02T00:00:00.000Z"
		d.FrontMatter.Links.Repo.PullRequest = "https://github.com/org/p/pull/1"
		d.FrontMatter.Links.Repo.Files = []string{"src/a.go"}
		d.FrontMatter.Links.Plans = []model.Link{{ID: "0009-zzz", Relation: "included", Sections: []string{"1.1", "2"}}}
		d.FrontMatter.Progress = model.Progress{"1": {Status: "pending"}, "1.1": {Status: "complete"}, "2.1.3": {Status: "blocked"}}
		d.Content = fmt.Sprintf("# Conformance %s\n\n## Steps\n\n### 1: A\n\n#### 1.1: B\n\n### 2: C\n\n#### 2.1: D\n\n##### 2.1.3: E\n\n## Acceptance Criteria\n\n- [ ] it works\n\n## Work Log\n", id)
	}
	return d
}

// Run executes the suite for every kind.
func Run(t *testing.T, newStore Factory) {
	ctx := context.Background()

	for _, k := range kind.All() {
		k := k
		t.Run(k.Plural, func(t *testing.T) {
			s := newStore(t)
			typ := "impl"
			if k.Name == kind.Story {
				typ = "bugs"
			}
			if list, err := s.List(ctx, k.Name); err != nil || len(list) != 0 {
				t.Fatalf("empty list: %v %v", list, err)
			}
			if _, err := s.Get(ctx, k.Name, "0001-aaa"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("missing document: %v", err)
			}
			d := document(k.Name, "0001-aaa", typ, "pending")
			if err := s.Put(ctx, d); err != nil {
				t.Fatal(err)
			}
			got, err := s.Get(ctx, k.Name, "0001-aaa")
			if err != nil {
				t.Fatal(err)
			}
			if !store.DocumentsEqual(got, d) {
				t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, d)
			}
			if got.Kind != k.Name || got.FrontMatter.Links.Web["jira"] != "https://jira/1" || got.FrontMatter.Links.Plans[0].ID != "0009-zzz" {
				t.Errorf("nested data lost: %+v", got.FrontMatter)
			}
			if k.Name == kind.Story && (got.FrontMatter.Purpose == "" || got.FrontMatter.Started == "" || len(got.FrontMatter.Links.Plans[0].Sections) != 2 || got.FrontMatter.Progress["2.1.3"].Status != "blocked") {
				t.Errorf("story fields lost: %+v", got.FrontMatter)
			}
			// Replace, rename and archive.
			d.FrontMatter.Status = "archived"
			d.Path = "archive/" + model.FileName("0001-aaa", typ, "conformance")
			d.Content += "\nmore\n"
			if err := s.Put(ctx, d); err != nil {
				t.Fatal(err)
			}
			got, _ = s.Get(ctx, k.Name, "0001-aaa")
			if got.Path != d.Path || got.FrontMatter.Status != "archived" || got.Content != d.Content {
				t.Errorf("replace: %+v", got)
			}
			s.Put(ctx, document(k.Name, "0002-bbb", typ, "ready"))
			s.Put(ctx, document(k.Name, "0003-ccc", typ, "ready"))
			list, err := s.List(ctx, k.Name)
			if err != nil || len(list) != 3 {
				t.Fatalf("list: %d %v", len(list), err)
			}
			if list[0].ID() != "0001-aaa" || list[2].ID() != "0003-ccc" {
				t.Errorf("list order: %s %s %s", list[0].ID(), list[1].ID(), list[2].ID())
			}
			// Other kinds are not affected.
			for _, other := range kind.All() {
				if other.Name == k.Name {
					continue
				}
				if list, err := s.List(ctx, other.Name); err != nil || len(list) != 0 {
					t.Errorf("%s leaked into %s: %d %v", k.Name, other.Name, len(list), err)
				}
				if _, err := s.Get(ctx, other.Name, "0001-aaa"); !errors.Is(err, store.ErrNotFound) {
					t.Errorf("%s id visible as %s: %v", k.Name, other.Name, err)
				}
			}
			if err := s.Delete(ctx, k.Name, "0002-bbb"); err != nil {
				t.Fatal(err)
			}
			if err := s.Delete(ctx, k.Name, "0002-bbb"); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("double delete: %v", err)
			}
			if list, _ := s.List(ctx, k.Name); len(list) != 2 {
				t.Errorf("after delete: %d", len(list))
			}
			if _, err := s.List(ctx, "bogus"); err == nil {
				t.Error("unknown kind should fail")
			}
		})
	}

	t.Run("templates", func(t *testing.T) {
		s := newStore(t)
		for _, k := range kind.All() {
			if list, err := s.ListTemplates(ctx, k.Name); err != nil || len(list) != 0 {
				t.Fatalf("%s: empty templates: %v %v", k.Name, list, err)
			}
			if _, err := s.GetTemplate(ctx, k.Name, "x"); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("%s: missing template: %v", k.Name, err)
			}
			if err := s.PutTemplate(ctx, &model.Template{Kind: k.Name, ID: "x", Content: "# {{ .Title }}\n"}); err != nil {
				t.Fatal(err)
			}
			if err := s.PutTemplate(ctx, &model.Template{Kind: k.Name, ID: "x", Content: "# " + k.Name + " {{ .Title }}!\n", Updated: "2026-01-01T00:00:00Z"}); err != nil {
				t.Fatal(err)
			}
		}
		for _, k := range kind.All() {
			got, err := s.GetTemplate(ctx, k.Name, "x")
			if err != nil || got.Kind != k.Name || got.Content != "# "+k.Name+" {{ .Title }}!\n" || got.Updated == "" {
				t.Errorf("%s template: %+v %v", k.Name, got, err)
			}
			s.PutTemplate(ctx, &model.Template{Kind: k.Name, ID: "a", Content: "a"})
			list, _ := s.ListTemplates(ctx, k.Name)
			if len(list) != 2 || list[0].ID != "a" || list[1].Kind != k.Name {
				t.Errorf("%s list templates: %+v", k.Name, list)
			}
		}
		if err := s.DeleteTemplate(ctx, kind.Plan, "x"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteTemplate(ctx, kind.Plan, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("double delete template: %v", err)
		}
		if _, err := s.GetTemplate(ctx, kind.Story, "x"); err != nil {
			t.Errorf("story template deleted with plan template: %v", err)
		}
	})
}
