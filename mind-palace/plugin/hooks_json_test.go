package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type hookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Async   *bool  `json:"async"`
}

type hookGroup struct {
	Matcher string      `json:"matcher"`
	Hooks   []hookEntry `json:"hooks"`
}

func TestHooksJSONRegistersPlanGuardAsBlockingWriteEditPreToolUse(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	var guard *hookGroup
	for i, g := range doc.Hooks["PreToolUse"] {
		for _, h := range g.Hooks {
			if strings.HasSuffix(h.Command, "/hooks/guard-superpowers-plans") {
				guard = &doc.Hooks["PreToolUse"][i]
			}
		}
	}
	if guard == nil {
		t.Fatal("guard group missing from PreToolUse")
	}
	if guard.Matcher != "Write|Edit" {
		t.Errorf("matcher = %q", guard.Matcher)
	}
	if len(guard.Hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(guard.Hooks))
	}
	h := guard.Hooks[0]
	if h.Type != "command" || h.Command != "${CLAUDE_PLUGIN_ROOT}/hooks/guard-superpowers-plans" {
		t.Errorf("hook = %+v", h)
	}
	if h.Async != nil {
		t.Error("a blocking hook must not set async")
	}
}
