// Package storetest is a conformance suite every storage plugin must pass.
package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/robbiebyrd/clued/plan/model"
	"github.com/robbiebyrd/clued/plan/store"
)

// Factory returns a fresh, empty, initialised store for one test.
type Factory func(t *testing.T) store.Store

func plan(id, typ, status string) *model.Plan {
	path := model.FileName(id, typ, "conformance")
	if status == "archived" {
		path = "archive/" + path
	}
	return &model.Plan{
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
}

// Run executes the suite.
func Run(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("plans", func(t *testing.T) {
		s := newStore(t)
		if list, err := s.ListPlans(ctx); err != nil || len(list) != 0 {
			t.Fatalf("empty list: %v %v", list, err)
		}
		if _, err := s.GetPlan(ctx, "0001-aaa"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing plan: %v", err)
		}
		p := plan("0001-aaa", "impl", "pending")
		if err := s.PutPlan(ctx, p); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetPlan(ctx, "0001-aaa")
		if err != nil {
			t.Fatal(err)
		}
		if !store.PlansEqual(got, p) {
			t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, p)
		}
		if got.FrontMatter.Progress["1.10"].Status != "blocked" || got.FrontMatter.Links.Web["jira"] != "https://jira/1" || got.FrontMatter.Links.Plans[0].ID != "0009-zzz" {
			t.Errorf("nested data lost: %+v", got.FrontMatter)
		}
		// Replace, rename and archive.
		p.FrontMatter.Status = "archived"
		p.FrontMatter.Type = "dsgn"
		p.Path = "archive/" + model.FileName("0001-aaa", "dsgn", "conformance")
		p.Content += "\nmore\n"
		if err := s.PutPlan(ctx, p); err != nil {
			t.Fatal(err)
		}
		got, _ = s.GetPlan(ctx, "0001-aaa")
		if got.Path != p.Path || got.FrontMatter.Status != "archived" || got.Content != p.Content {
			t.Errorf("replace: %+v", got)
		}
		s.PutPlan(ctx, plan("0002-bbb", "dsgn", "ready"))
		s.PutPlan(ctx, plan("0003-ccc", "drft", "ready"))
		list, err := s.ListPlans(ctx)
		if err != nil || len(list) != 3 {
			t.Fatalf("list: %d %v", len(list), err)
		}
		if list[0].ID() != "0001-aaa" || list[2].ID() != "0003-ccc" {
			t.Errorf("list order: %s %s %s", list[0].ID(), list[1].ID(), list[2].ID())
		}
		if err := s.DeletePlan(ctx, "0002-bbb"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeletePlan(ctx, "0002-bbb"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("double delete: %v", err)
		}
		if list, _ := s.ListPlans(ctx); len(list) != 2 {
			t.Errorf("after delete: %d", len(list))
		}
	})

	t.Run("templates", func(t *testing.T) {
		s := newStore(t)
		if list, err := s.ListTemplates(ctx); err != nil || len(list) != 0 {
			t.Fatalf("empty templates: %v %v", list, err)
		}
		if _, err := s.GetTemplate(ctx, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing template: %v", err)
		}
		if err := s.PutTemplate(ctx, &model.Template{ID: "x", Content: "# {{ .Title }}\n"}); err != nil {
			t.Fatal(err)
		}
		if err := s.PutTemplate(ctx, &model.Template{ID: "x", Content: "# {{ .Title }}!\n", Updated: "2026-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetTemplate(ctx, "x")
		if err != nil || got.Content != "# {{ .Title }}!\n" || got.Updated == "" {
			t.Errorf("template: %+v %v", got, err)
		}
		s.PutTemplate(ctx, &model.Template{ID: "a", Content: "a"})
		list, _ := s.ListTemplates(ctx)
		if len(list) != 2 || list[0].ID != "a" {
			t.Errorf("list templates: %+v", list)
		}
		if err := s.DeleteTemplate(ctx, "x"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteTemplate(ctx, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("double delete template: %v", err)
		}
	})
}
