package docsource

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect/prime"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/markdown"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

var ctx = context.Background()

func memPalace(t *testing.T) *service.Palace {
	t.Helper()
	p, err := service.NewPalace(config.Default(), memstore.New(""), events.New())
	if err != nil {
		t.Fatalf("NewPalace: %v", err)
	}
	return p
}

// createStory makes a story in the given status; its one acceptance
// criterion is done so complete passes the criteria gate.
func createStory(t *testing.T, p *service.Palace, status, title string) *model.Document {
	t.Helper()
	input := fmt.Sprintf(`{
	  "frontMatter": {"title": %q, "purpose": "p", "type": "task", "status": %q, "priority": "P2", "effort": "S"},
	  "body": {
	    "problemStatement": {"statement": "s", "issue": "i", "impact": "x"},
	    "steps": [{"number": "1", "name": "Do", "description": "it."}],
	    "acceptanceCriteria": [{"text": "done", "state": %q}],
	    "files": [{"path": "a.go"}]
	  }
	}`, title, status, markdown.StateDone)
	d, err := p.Stories().Create(ctx, json.RawMessage(input), "")
	if err != nil {
		t.Fatalf("create %s story: %v", status, err)
	}
	return d
}

func createPlan(t *testing.T, p *service.Palace, status, title string) *model.Document {
	t.Helper()
	input := fmt.Sprintf(`{"frontMatter":{"title":%q,"type":"drft","status":%q,"priority":"1"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`, title, status)
	d, err := p.Plans().Create(ctx, json.RawMessage(input), "")
	if err != nil {
		t.Fatalf("create %s plan: %v", status, err)
	}
	return d
}

func ids(docs []prime.DocSummary) []string {
	out := make([]string, len(docs))
	for i, d := range docs {
		out[i] = d.ID
	}
	return out
}

func TestActiveStories(t *testing.T) {
	p := memPalace(t)
	// Created out of status order so the result is visibly sorted by id.
	var want []string
	for _, status := range []string{"in_progress", "complete", "ready", "blocked", "rejected", "pending", "archived", "validated", "ready"} {
		d := createStory(t, p, status, "Story "+status)
		if status == "pending" || status == "ready" || status == "in_progress" {
			want = append(want, d.ID())
		}
	}
	// ids are allocated in sequence, so creation order is id order.
	got, err := New(p).ActiveStories(ctx)
	if err != nil {
		t.Fatalf("ActiveStories: %v", err)
	}
	if strings.Join(ids(got), ",") != strings.Join(want, ",") {
		t.Fatalf("ActiveStories ids = %v, want %v", ids(got), want)
	}
	first := got[0]
	if first.Type != "task" || first.Status != "in_progress" || first.Title != "Story in_progress" {
		t.Errorf("first summary = %+v", first)
	}
	for _, d := range got {
		if d.Status != "pending" && d.Status != "ready" && d.Status != "in_progress" {
			t.Errorf("status %q is not active", d.Status)
		}
	}
}

func TestActiveStoriesEmptyPalace(t *testing.T) {
	got, err := New(memPalace(t)).ActiveStories(ctx)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("ActiveStories on an empty palace = %#v, %v; want empty non-nil", got, err)
	}
}

func TestActivePlans(t *testing.T) {
	p := memPalace(t)
	ready1 := createPlan(t, p, "ready", "Ready one")
	prog1 := createPlan(t, p, "in_progress", "Progress one")
	createPlan(t, p, "pending", "Pending")
	prog2 := createPlan(t, p, "in_progress", "Progress two")
	ready2 := createPlan(t, p, "ready", "Ready two")
	createPlan(t, p, "complete", "Complete")

	src := New(p)
	got, err := src.ActivePlans(ctx)
	if err != nil {
		t.Fatalf("ActivePlans: %v", err)
	}
	want := []string{prog1.ID(), prog2.ID(), ready1.ID(), ready2.ID()}
	if strings.Join(ids(got), ",") != strings.Join(want, ",") {
		t.Fatalf("ActivePlans ids = %v, want %v", ids(got), want)
	}
	if got[0].Type != "drft" || got[0].Status != "in_progress" || got[0].Title != "Progress one" {
		t.Errorf("first summary = %+v", got[0])
	}

	content, err := src.PlanContent(ctx, prog1.ID())
	if err != nil {
		t.Fatalf("PlanContent: %v", err)
	}
	if !strings.HasPrefix(content, "# Progress one") {
		t.Errorf("PlanContent = %q, want the Markdown starting with the H1", content)
	}
	if content != prog1.Content {
		t.Errorf("PlanContent differs from the created document's content")
	}
	if _, err := src.PlanContent(ctx, "9999-zzz"); err == nil {
		t.Errorf("PlanContent of a missing plan succeeded")
	}
	if err := src.Close(); err != nil {
		t.Errorf("Close of a New source: %v", err)
	}
}

// writePlan renders a plan created in a memstore palace to a file, exactly
// as the file store would write it.
func writePlan(t *testing.T, dir, status, title string) *model.Document {
	t.Helper()
	d := createPlan(t, memPalace(t), status, title)
	text, err := markdown.RenderDocument(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, d.Path), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestOpenWithoutPalaceIsNil(t *testing.T) {
	root := t.TempDir()
	src, err := Open(ctx, root)
	if err != nil || src != nil {
		t.Fatalf("Open(empty root) = %v, %v; want nil, nil", src, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Open created entries in the root: %v", entries)
	}
	// A docs directory without a plans directory does not count either.
	if err := os.MkdirAll(filepath.Join(root, "docs", "stories"), 0o755); err != nil {
		t.Fatal(err)
	}
	if src, err := Open(ctx, root); err != nil || src != nil {
		t.Fatalf("Open(root with docs/stories only) = %v, %v; want nil, nil", src, err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs", "plans")); !os.IsNotExist(err) {
		t.Errorf("docs/plans exists after Open: %v", err)
	}
}

func TestOpenPlansDir(t *testing.T) {
	root := t.TempDir()
	plan := writePlan(t, filepath.Join(root, "docs", "plans"), "in_progress", "File plan")
	writePlan(t, filepath.Join(root, "docs", "plans"), "pending", "Idle plan")

	src, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if src == nil {
		t.Fatal("Open returned nil for a root with docs/plans")
	}
	defer src.Close()

	got, err := src.ActivePlans(ctx)
	if err != nil {
		t.Fatalf("ActivePlans: %v", err)
	}
	if len(got) != 1 || got[0].ID != plan.ID() || got[0].Status != "in_progress" || got[0].Title != "File plan" {
		t.Fatalf("ActivePlans = %+v, want the in_progress plan %s", got, plan.ID())
	}
	content, err := src.PlanContent(ctx, plan.ID())
	if err != nil || !strings.HasPrefix(content, "# File plan") {
		t.Errorf("PlanContent = %q, %v", content, err)
	}
	stories, err := src.ActiveStories(ctx)
	if err != nil || len(stories) != 0 {
		t.Errorf("ActiveStories = %v, %v; want none", stories, err)
	}
	if dir := src.palace.Config().Plans.Dir; dir != filepath.Join(root, "docs", "plans") {
		t.Errorf("plans dir = %q, want it under the root", dir)
	}
	if err := src.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestOpenConfigFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mind-palace.config.yaml"), []byte("plans:\n  dir: notes/plans\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := writePlan(t, filepath.Join(root, "notes", "plans"), "ready", "Noted plan")
	// A plan in the default directory is not this project's.
	writePlan(t, filepath.Join(root, "docs", "plans"), "in_progress", "Stray plan")

	src, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if src == nil {
		t.Fatal("Open returned nil for a root with a config file")
	}
	defer src.Close()

	if dir := src.palace.Config().Plans.Dir; dir != filepath.Join(root, "notes", "plans") {
		t.Errorf("plans dir = %q, want %q", dir, filepath.Join(root, "notes", "plans"))
	}
	if dir := src.palace.Config().Stories.Dir; dir != filepath.Join(root, "docs", "stories") {
		t.Errorf("stories dir = %q, want the default under the root", dir)
	}
	got, err := src.ActivePlans(ctx)
	if err != nil {
		t.Fatalf("ActivePlans: %v", err)
	}
	if len(got) != 1 || got[0].ID != plan.ID() || got[0].Status != "ready" {
		t.Fatalf("ActivePlans = %+v, want only the ready plan %s", got, plan.ID())
	}
}

func TestOpenConfigFileWithoutPlansDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mind-palace.config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, err := Open(ctx, root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if src == nil {
		t.Fatal("Open returned nil although a config file exists")
	}
	defer src.Close()
	got, err := src.ActivePlans(ctx)
	if err != nil || len(got) != 0 {
		t.Errorf("ActivePlans = %v, %v; want none", got, err)
	}
}
