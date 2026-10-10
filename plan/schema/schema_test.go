package schema

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/plan/config"
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
	v, err := New(config.Default())
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
	v, _ := New(config.Default())
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
	cfg := config.Default()
	cfg.Types = append(cfg.Types, config.TypeDef{Name: "rfc", Label: "RFC"})
	cfg.Statuses = append(cfg.Statuses, config.StatusDef{Name: "review", Label: "Review"})
	v, err := New(cfg)
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
