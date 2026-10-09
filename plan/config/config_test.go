package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{"approved": "ready", "Ready": "ready", "In Progress": "in_progress", "in-progress": "in_progress", "wontfix": "rejected", "parked": "archived", "draft": "pending"} {
		if got, ok := c.NormalizeStatus(in); !ok || got != want {
			t.Errorf("status %q = %q,%v want %q", in, got, ok, want)
		}
	}
	if _, ok := c.NormalizeStatus("bogus"); ok {
		t.Error("bogus status accepted")
	}
	for in, want := range map[any]string{"P1": "1", "critical": "1", 3: "3", "3": "3", float64(0): "0", "Feature Request": "5"} {
		if got, ok := c.NormalizePriority(in); !ok || got != want {
			t.Errorf("priority %v = %q,%v want %q", in, got, ok, want)
		}
	}
	for in, want := range map[any]string{"M": "M", "medium": "M", 5: "M", "13": "XL", "extra small": "XS"} {
		if got, ok := c.NormalizeEffort(in); !ok || got != want {
			t.Errorf("effort %v = %q,%v want %q", in, got, ok, want)
		}
	}
	if got, ok := c.NormalizeType("Implementation"); !ok || got != "impl" {
		t.Errorf("type = %q", got)
	}
	if !c.CanTransition("pending", "validated") || c.CanTransition("pending", "complete") {
		t.Error("workflow defaults wrong")
	}
	if len(c.Transitions("complete")) != 2 {
		t.Errorf("transitions from complete: %v", c.Transitions("complete"))
	}
}

func TestLoadOverlay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.config.yaml")
	os.WriteFile(path, []byte(`
plansDir: plans
statuses:
  - {name: pending, label: Pending, synonyms: [new]}
  - {name: done, label: Done}
workflow:
  pending: [done]
storage:
  - {name: file, kind: file, enabled: false}
  - {name: db, kind: sqlite, options: {dsn: ":memory:"}}
`), 0o644)
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.PlansDir != "plans" || len(c.Statuses) != 2 || len(c.Types) != 3 {
		t.Errorf("overlay: %+v", c)
	}
	if got := c.EnabledStorage(); len(got) != 1 || got[0].Name != "db" {
		t.Errorf("enabled storage: %+v", got)
	}
	if c.Source != path {
		t.Errorf("source = %q", c.Source)
	}
	// Disabling every store is rejected.
	os.WriteFile(path, []byte("storage:\n  - {name: file, kind: file, enabled: false}\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("expected error when no storage is enabled")
	}
	// Missing default status is rejected.
	os.WriteFile(path, []byte("statuses:\n  - {name: done, label: Done}\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("expected error when pending is missing")
	}
}

func TestStrictConfigAndNormalisationEdges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "plan.config.yaml")
	os.WriteFile(path, []byte("plansDirr: x\n"), 0o644)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "plansDirr") {
		t.Errorf("unknown YAML key not reported: %v", err)
	}
	jpath := filepath.Join(dir, "plan.config.json")
	os.WriteFile(jpath, []byte(`{"typos": 1}`), 0o644)
	if _, err := Load(jpath); err == nil {
		t.Error("unknown JSON key not reported")
	}
	os.WriteFile(path, []byte("types:\n  - {name: 'a-b', label: X}\n"), 0o644)
	if _, err := Load(path); err == nil {
		t.Error("type with punctuation accepted")
	}
	os.WriteFile(path, []byte(""), 0o644)
	if c, err := Load(path); err != nil || len(c.Types) != 3 {
		t.Errorf("empty config file: %v", err)
	}
	c := Default()
	for _, in := range []any{1.5, float32(2.5), "1.5", true, nil} {
		if got, ok := c.NormalizePriority(in); ok {
			t.Errorf("priority %v accepted as %q", in, got)
		}
		if got, ok := c.NormalizeEffort(in); ok {
			t.Errorf("effort %v accepted as %q", in, got)
		}
	}
	if got, ok := c.NormalizePriority(2.0); !ok || got != "2" {
		t.Errorf("integral float rejected: %q %v", got, ok)
	}
}
