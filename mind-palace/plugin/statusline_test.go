package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// statusLineOf reads settings.statusLine.
func statusLineOf(t *testing.T, s settings) map[string]any {
	t.Helper()
	raw, ok := s["statusLine"]
	if !ok {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// writeCluedConfig writes the plugin config under HOME with statusLine set.
func writeCluedConfig(t *testing.T, home string, statusLine bool) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "plugins", "data", "clued")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{"port": 8085, "statusLine": statusLine})
	if err := os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRegisterHooksInstallsAndRestoresTheStatusLine(t *testing.T) {
	requireJq(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLUED_STATUS_LINE", "")
	dataDir := writeCluedConfig(t, home, true)
	dir := t.TempDir()
	seed := `{"statusLine": {"type": "command", "command": "my-status", "padding": 1}, "other": 1}`

	s := registerHooks(t, dir, seed, 1)
	sl := statusLineOf(t, s)
	want := launcherIn(dir) + " hook status"
	if sl == nil || sl["command"] != want || sl["type"] != "command" || sl["padding"] != float64(1) {
		t.Fatalf("statusLine = %v, want command %q with padding kept", sl, want)
	}
	kept, err := os.ReadFile(filepath.Join(dataDir, "statusline.json"))
	if err != nil || !strings.Contains(string(kept), `"then": "my-status"`) && !strings.Contains(string(kept), `"then":"my-status"`) {
		t.Errorf("statusline.json = %s (%v)", kept, err)
	}

	// Running again keeps the kept command and changes nothing.
	s = registerHooks(t, dir, readSettings(t, dir), 1)
	if statusLineOf(t, s)["command"] != want {
		t.Errorf("second run changed the status line: %v", statusLineOf(t, s))
	}
	if kept2, _ := os.ReadFile(filepath.Join(dataDir, "statusline.json")); string(kept2) != string(kept) {
		t.Errorf("second run rewrote statusline.json: %s", kept2)
	}

	// Turning it off puts the previous command back.
	writeCluedConfig(t, home, false)
	s = registerHooks(t, dir, readSettings(t, dir), 1)
	if sl := statusLineOf(t, s); sl == nil || sl["command"] != "my-status" || sl["padding"] != float64(1) {
		t.Errorf("status line not restored: %v", sl)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "statusline.json")); !os.IsNotExist(err) {
		t.Error("statusline.json should be removed once restored")
	}

	// With no previous command, enabling then disabling removes the key.
	t.Setenv("CLUED_STATUS_LINE", "1")
	s = registerHooks(t, dir, `{"other": 1}`, 1)
	if statusLineOf(t, s)["command"] != want {
		t.Errorf("env enable: %v", statusLineOf(t, s))
	}
	t.Setenv("CLUED_STATUS_LINE", "0")
	s = registerHooks(t, dir, readSettings(t, dir), 1)
	if statusLineOf(t, s) != nil {
		t.Errorf("status line should be gone: %v", statusLineOf(t, s))
	}
	if _, ok := s["other"]; !ok {
		t.Error("unrelated keys must survive")
	}
}

func TestRegisterHooksLeavesTheStatusLineAloneByDefault(t *testing.T) {
	requireJq(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLUED_STATUS_LINE", "")
	dir := t.TempDir()
	s := registerHooks(t, dir, `{"statusLine": {"type": "command", "command": "my-status"}}`, 1)
	if sl := statusLineOf(t, s); sl == nil || sl["command"] != "my-status" {
		t.Errorf("statusLine = %v", sl)
	}
	s = registerHooks(t, dir, `{}`, 1)
	if statusLineOf(t, s) != nil {
		t.Errorf("no status line should be added: %v", statusLineOf(t, s))
	}
}

func readSettings(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
