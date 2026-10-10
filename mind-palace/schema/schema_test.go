package schema

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
)

func load(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestValidateCreate(t *testing.T) {
	v, err := New("plan", &config.Default().Plans)
	if err != nil {
		t.Fatal(err)
	}
	dsgn := load(t, "../testdata/create-dsgn.json")
	if p := v.ValidateCreate(dsgn); len(p) != 0 {
		t.Errorf("dsgn should validate: %v", p)
	}
	impl := load(t, "../testdata/create-impl.json")
	// Raw input uses synonyms; before normalisation each of status, priority
	// and effort must be reported.
	p := v.ValidateCreate(impl)
	for _, field := range []string{"/frontMatter/status", "/frontMatter/priority", "/frontMatter/effort"} {
		found := false
		for _, s := range p {
			if strings.HasPrefix(s, field+":") {
				found = true
			}
		}
		if !found {
			t.Errorf("un-normalised input should report %s, got %v", field, p)
		}
	}
	fm := impl["frontMatter"].(map[string]any)
	fm["status"], fm["priority"], fm["effort"] = "ready", "2", "M"
	if p := v.ValidateCreate(impl); len(p) != 0 {
		t.Errorf("normalised impl should validate: %v", p)
	}
	// impl without specs must fail.
	delete(fm["links"].(map[string]any), "specs")
	p = v.ValidateCreate(impl)
	found := false
	for _, s := range p {
		if strings.Contains(s, "specs") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a specs problem, got %v", p)
	}
}

func TestStoredFrontMatterRequiresManagedFields(t *testing.T) {
	v, _ := New("plan", &config.Default().Plans)
	fm := map[string]any{"title": "x", "type": "dsgn", "status": "pending", "priority": "1"}
	if p := v.ValidateFrontMatter(fm); len(p) != 0 {
		t.Errorf("plain front matter: %v", p)
	}
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("stored front matter must require id/created/updated/completed")
	}
	fm["id"], fm["created"], fm["updated"] = "0001-abc", "2026-01-01T00:00:00.000Z", "2026-01-01T00:00:00.000Z"
	if p := v.ValidateStoredFrontMatter(fm); len(p) != 0 {
		t.Errorf("complete stored front matter: %v", p)
	}
	fm["completed"] = ""
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("completed must never be an empty string")
	}
	fm["created"] = "2026-01-01T00:00:00Z"
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("timestamps must carry millisecond precision")
	}
	fm["created"] = "2026-01-01T00:00:00.000Z"
	delete(fm, "completed")
	fm["status"] = "complete"
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("complete status without completed timestamp must fail")
	}
	fm["completed"] = "2026-01-02T00:00:00.000Z"
	if p := v.ValidateStoredFrontMatter(fm); len(p) != 0 {
		t.Errorf("complete with timestamp: %v", p)
	}
	fm["progress"] = map[string]any{"1.1": map[string]any{"status": "pending"}, "x": map[string]any{"status": "pending"}}
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("bad progress key must fail")
	}
	fm["progress"] = map[string]any{"1.1": map[string]any{"status": "pending", "stories": []any{"001-abc"}}}
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("three-digit story id must fail")
	}
}

func TestConfiguredEnums(t *testing.T) {
	cfg := config.Default().Plans
	cfg.Types = append(cfg.Types, config.TypeDef{Name: "rfc", Label: "RFC"})
	cfg.Statuses = append(cfg.Statuses, config.StatusDef{Name: "review", Label: "Review"})
	v, err := New("plan", &cfg)
	if err != nil {
		t.Fatal(err)
	}
	in := map[string]any{
		"frontMatter": map[string]any{"title": "x", "type": "rfc", "status": "review", "priority": "1"},
		"body":        map[string]any{"summary": map[string]any{"goal": "g", "problem": "p"}},
	}
	if p := v.ValidateCreate(in); len(p) != 0 {
		t.Errorf("configured type/status should validate: %v", p)
	}
	if !strings.Contains(string(v.JSON()), `"rfc"`) {
		t.Error("effective schema should contain configured type")
	}
}

func TestStorySchema(t *testing.T) {
	v, err := New("story", &config.Default().Stories)
	if err != nil {
		t.Fatal(err)
	}
	if v.Kind() != "story" || !strings.Contains(string(v.JSON()), `"bugs"`) {
		t.Error("story validator")
	}
	in := map[string]any{
		"frontMatter": map[string]any{"title": "x", "purpose": "why", "type": "bugs", "status": "pending", "priority": "1",
			"links": map[string]any{"plans": []any{[]any{"0001-abc", "included", []any{"1.1", "2"}}}, "repo": map[string]any{"pull_request": "https://github.com/o/p/pull/1", "files": []any{"a.go"}}}},
		"body": map[string]any{"problemStatement": map[string]any{"statement": "s"}, "steps": []any{map[string]any{"number": "1", "name": "n", "description": "d"}}, "acceptanceCriteria": []any{map[string]any{"text": "t"}}},
	}
	if p := v.ValidateCreate(in); len(p) != 0 {
		t.Errorf("story input should validate: %v", p)
	}
	fm := in["frontMatter"].(map[string]any)
	fm["links"].(map[string]any)["plans"] = []any{[]any{"0001-abc", "parent"}}
	if p := v.ValidateCreate(in); len(p) == 0 {
		t.Error("parent is not a story→plan relation")
	}
	fm["links"].(map[string]any)["plans"] = []any{[]any{"0001-abc", "included", []any{"1.1.1"}}}
	if p := v.ValidateCreate(in); len(p) == 0 {
		t.Error("plan section numbers are at most two levels")
	}
	delete(fm, "links")
	fm["progress"] = map[string]any{"2.1.3": map[string]any{"status": "pending"}}
	if p := v.ValidateCreate(in); len(p) != 0 {
		t.Errorf("deep step keys are allowed for stories: %v", p)
	}
	fm["progress"] = map[string]any{"2.1": map[string]any{"status": "pending", "stories": []any{"0001-abc"}}}
	if p := v.ValidateCreate(in); len(p) == 0 {
		t.Error("story progress entries carry no stories")
	}
	delete(fm, "progress")
	fm["status"] = "in_progress"
	fm["id"], fm["created"], fm["updated"] = "0001-abc", "2026-01-01T00:00:00.000Z", "2026-01-01T00:00:00.000Z"
	if p := v.ValidateStoredFrontMatter(fm); len(p) == 0 {
		t.Error("in_progress without started must fail")
	}
	fm["started"] = "2026-01-01T00:00:00.000Z"
	if p := v.ValidateStoredFrontMatter(fm); len(p) != 0 {
		t.Errorf("in_progress with started: %v", p)
	}
	// Plans reject the story-only fields.
	pv, _ := New("plan", &config.Default().Plans)
	if p := pv.ValidateFrontMatter(map[string]any{"title": "x", "type": "dsgn", "status": "pending", "priority": "1", "purpose": "no"}); len(p) == 0 {
		t.Error("plan front matter must reject purpose")
	}
	if p := pv.ValidateFrontMatter(map[string]any{"title": "x", "type": "dsgn", "status": "pending", "priority": "1", "links": map[string]any{"plans": []any{[]any{"0001-abc", "parent", []any{"1"}}}}}); len(p) == 0 {
		t.Error("plan links must reject sections")
	}
}
