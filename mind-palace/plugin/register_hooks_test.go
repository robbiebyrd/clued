package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type settingsGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Command string `json:"command"`
	} `json:"hooks"`
}

type settings map[string]json.RawMessage

func (s settings) hooks(t *testing.T) map[string][]settingsGroup {
	t.Helper()
	raw, ok := s["hooks"]
	if !ok {
		t.Fatal("hooks key missing")
	}
	var h map[string][]settingsGroup
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	return h
}

// registerHooks runs hooks/register-hooks against dir/settings.json, seeded with seed
// (or absent when seed is empty), and returns the resulting settings.
func registerHooks(t *testing.T, dir, seed string, times int) settings {
	t.Helper()
	requireJq(t)
	path := filepath.Join(dir, "settings.json")
	os.Remove(path)
	if seed != "" {
		if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	env := append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	for i := 0; i < times; i++ {
		if r := runScript(t, filepath.Join(repoRoot(t), "hooks/register-hooks"), "", env); r.code != 0 {
			t.Fatalf("register-hooks failed: %+v", r)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var s settings
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func hasSuffixCommand(g settingsGroup, suffix string) bool {
	for _, h := range g.Hooks {
		if strings.HasSuffix(h.Command, suffix) {
			return true
		}
	}
	return false
}

func TestRegisterHooksCreatesHooksKeyInEmptySettings(t *testing.T) {
	registerHooks(t, t.TempDir(), "{}", 1).hooks(t)
}

func TestRegisterHooksWritesAllRequiredEventTypes(t *testing.T) {
	h := registerHooks(t, t.TempDir(), "{}", 1).hooks(t)
	for _, ev := range []string{"SessionStart", "PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop", "SessionEnd", "Notification", "SubagentStop"} {
		if len(h[ev]) == 0 {
			t.Errorf("%s missing", ev)
		}
	}
}

func TestRegisterHooksPointsToCorrectAbsolutePaths(t *testing.T) {
	h := registerHooks(t, t.TempDir(), "{}", 1).hooks(t)
	hooksDir := filepath.Join(repoRoot(t), "hooks")
	if got := h["PreToolUse"][0].Hooks[0].Command; got != filepath.Join(hooksDir, "event-relay") {
		t.Errorf("relay = %q", got)
	}
	g := h["SessionStart"][0]
	if !hasSuffixCommand(g, "check-setup") || !hasSuffixCommand(g, "session-start") {
		t.Errorf("SessionStart commands = %+v", g.Hooks)
	}
}

func TestRegisterHooksIsIdempotent(t *testing.T) {
	h := registerHooks(t, t.TempDir(), "{}", 2).hooks(t)
	// PreToolUse carries two clued entries: the relay and the plan guard.
	for ev, want := range map[string]int{"PreToolUse": 2, "PostToolUse": 1, "SessionStart": 1} {
		if len(h[ev]) != want {
			t.Errorf("%s has %d entries after second run, want %d", ev, len(h[ev]), want)
		}
	}
}

func TestRegisterHooksReplacesStaleCluedEntries(t *testing.T) {
	seed := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"/old/path/hooks/event-relay","async":true}]}]}}`
	h := registerHooks(t, t.TempDir(), seed, 1).hooks(t)
	if len(h["PreToolUse"]) != 2 {
		t.Fatalf("PreToolUse = %d entries, want relay and plan guard only", len(h["PreToolUse"]))
	}
	if got := h["PreToolUse"][0].Hooks[0].Command; got != filepath.Join(repoRoot(t), "hooks", "event-relay") {
		t.Errorf("relay = %q", got)
	}
}

func TestRegisterHooksPreservesNonCluedHooks(t *testing.T) {
	seed := `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"/other/plugin/hook"}]}]}}`
	h := registerHooks(t, t.TempDir(), seed, 1).hooks(t)
	if len(h["PreToolUse"]) != 3 {
		t.Fatalf("PreToolUse = %d entries, want 3", len(h["PreToolUse"]))
	}
	var other, relay bool
	for _, g := range h["PreToolUse"] {
		other = other || g.Hooks[0].Command == "/other/plugin/hook"
		relay = relay || g.Hooks[0].Command == filepath.Join(repoRoot(t), "hooks", "event-relay")
	}
	if !other || !relay {
		t.Errorf("other=%v relay=%v", other, relay)
	}
}

func TestRegisterHooksPreservesUnrelatedSettingsKeys(t *testing.T) {
	s := registerHooks(t, t.TempDir(), `{"model":"sonnet","spinnerTipsEnabled":false}`, 1)
	if string(s["model"]) != `"sonnet"` || string(s["spinnerTipsEnabled"]) != "false" {
		t.Errorf("model=%s spinnerTipsEnabled=%s", s["model"], s["spinnerTipsEnabled"])
	}
}

func TestRegisterHooksSucceedsWhenSettingsMissing(t *testing.T) {
	registerHooks(t, t.TempDir(), "", 1).hooks(t)
}

func TestRegisterHooksAddsWriteGuardWithItsMatcher(t *testing.T) {
	h := registerHooks(t, t.TempDir(), "{}", 1).hooks(t)
	var guards, relays int
	for _, g := range h["PreToolUse"] {
		if hasSuffixCommand(g, "/scripts/clued hook write-guard") {
			guards++
			if g.Matcher != writeGuardMatcher {
				t.Errorf("matcher = %q", g.Matcher)
			}
		}
		if hasSuffixCommand(g, "event-relay") {
			relays++
		}
	}
	if guards != 1 || relays != 1 {
		t.Errorf("guards=%d relays=%d, want 1 and 1", guards, relays)
	}
}

func TestRegisterHooksKeepsOneGuardWhenRunTwice(t *testing.T) {
	h := registerHooks(t, t.TempDir(), "{}", 2).hooks(t)
	n := 0
	for _, g := range h["PreToolUse"] {
		if hasSuffixCommand(g, "/scripts/clued hook write-guard") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("guards = %d, want 1", n)
	}
}

func TestRegisterHooksRetiresTheBashPlanGuard(t *testing.T) {
	seed := `{"hooks":{"PreToolUse":[{"matcher":"Write|Edit","hooks":[{"type":"command","command":"/old/clued/hooks/guard-superpowers-plans"}]}]}}`
	h := registerHooks(t, t.TempDir(), seed, 1).hooks(t)
	for _, g := range h["PreToolUse"] {
		if hasSuffixCommand(g, "guard-superpowers-plans") {
			t.Errorf("stale bash guard kept: %+v", g)
		}
	}
	if len(h["PreToolUse"]) != 2 {
		t.Errorf("PreToolUse = %d entries, want relay and write guard", len(h["PreToolUse"]))
	}
}

func TestRegisterHooksMirrorsTheBinaryHooks(t *testing.T) {
	h := registerHooks(t, t.TempDir(), "{}", 2).hooks(t)
	clued := filepath.Join(repoRoot(t), "scripts", "clued")
	want := map[string]string{
		"Stop":             clued + " hook stop-guard",
		"PreToolUse":       clued + " hook write-guard",
		"UserPromptSubmit": clued + " hook status-review --every 30m",
		"SessionStart":     clued + " hook status-review",
	}
	for ev, cmd := range want {
		n := 0
		for _, g := range h[ev] {
			for _, hk := range g.Hooks {
				if hk.Command == cmd {
					n++
				}
			}
		}
		if n != 1 {
			t.Errorf("%s: %q registered %d times after two runs, want 1", ev, cmd, n)
		}
	}
	if len(h["Stop"]) != 2 || len(h["UserPromptSubmit"]) != 2 {
		t.Errorf("Stop = %d groups, UserPromptSubmit = %d groups, want relay plus binary hook", len(h["Stop"]), len(h["UserPromptSubmit"]))
	}
}

func TestRegisterHooksReplacesStaleBinaryHooks(t *testing.T) {
	seed := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/path/scripts/clued hook stop-guard"}]}]}}`
	h := registerHooks(t, t.TempDir(), seed, 1).hooks(t)
	for _, g := range h["Stop"] {
		for _, hk := range g.Hooks {
			if strings.HasPrefix(hk.Command, "/old/path/") {
				t.Errorf("stale entry kept: %q", hk.Command)
			}
		}
	}
	if len(h["Stop"]) != 2 {
		t.Errorf("Stop = %d groups, want 2", len(h["Stop"]))
	}
}
