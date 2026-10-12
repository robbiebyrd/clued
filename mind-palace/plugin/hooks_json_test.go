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

const writeGuardMatcher = "Write|Edit|MultiEdit|NotebookEdit|Bash|EnterPlanMode|ExitPlanMode"

func TestHooksJSONRegistersWriteGuardAsBlockingPreToolUse(t *testing.T) {
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
			if strings.HasSuffix(h.Command, "/bin/clued hook write-guard") {
				guard = &doc.Hooks["PreToolUse"][i]
			}
		}
	}
	if guard == nil {
		t.Fatal("guard group missing from PreToolUse")
	}
	if guard.Matcher != writeGuardMatcher {
		t.Errorf("matcher = %q", guard.Matcher)
	}
	if len(guard.Hooks) != 1 {
		t.Fatalf("hooks = %d, want 1", len(guard.Hooks))
	}
	h := guard.Hooks[0]
	if h.Type != "command" || h.Command != "${CLAUDE_PLUGIN_ROOT}/bin/clued hook write-guard" {
		t.Errorf("hook = %+v", h)
	}
	if h.Async != nil {
		t.Error("a blocking hook must not set async")
	}
}

// readHooksJSON parses the plugin's hooks.json.
func readHooksJSON(t *testing.T) map[string][]hookGroup {
	t.Helper()
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
	return doc.Hooks
}

// findHook returns the hook under event whose command has the given suffix.
func findHook(groups []hookGroup, suffix string) *hookEntry {
	for _, g := range groups {
		for i, h := range g.Hooks {
			if strings.HasSuffix(h.Command, suffix) {
				return &g.Hooks[i]
			}
		}
	}
	return nil
}

func TestHooksJSONRunsStopGuardSynchronouslyOnStop(t *testing.T) {
	hooks := readHooksJSON(t)
	h := findHook(hooks["Stop"], "/bin/clued hook stop-guard")
	if h == nil {
		t.Fatal("stop-guard missing from Stop")
	}
	if h.Type != "command" || !strings.HasPrefix(h.Command, "${CLAUDE_PLUGIN_ROOT}/bin/clued") {
		t.Errorf("hook = %+v", *h)
	}
	if h.Async != nil {
		t.Error("a blocking hook must not set async")
	}
	if findHook(hooks["Stop"], "/hooks/event-relay") == nil {
		t.Error("Stop must still be relayed to the daemon")
	}
}

func TestHooksJSONRunsStatusReviewOnSessionStartAndThrottledOnPrompts(t *testing.T) {
	hooks := readHooksJSON(t)
	if h := findHook(hooks["SessionStart"], "/bin/clued hook status-review"); h == nil || h.Async != nil {
		t.Errorf("SessionStart status-review = %+v, want a synchronous hook", h)
	}
	h := findHook(hooks["UserPromptSubmit"], "/bin/clued hook status-review --every 30m")
	if h == nil || h.Async != nil {
		t.Errorf("UserPromptSubmit status-review = %+v, want a synchronous throttled hook", h)
	}
}
