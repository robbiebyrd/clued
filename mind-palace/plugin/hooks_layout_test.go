package plugin_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The plugin's hooks are registered once: by hooks/hooks.json, which Claude
// Code loads from an installed plugin, or by the settings mirror
// (`register-hooks --settings`), never by both, since a hook registered in
// both places runs twice. Settings entries call the launcher in the data
// dir, so a moved plugin root does not leave them dangling.

// hookIdentity maps a hook command to what it runs, whichever way it is
// registered: "clued <args>" for the shim, "hooks <name>" for a bash hook.
var (
	shimCommand = regexp.MustCompile(`(?:/scripts/clued|/bin/clued-hook)"? (.+)$`)
	bashHook    = regexp.MustCompile(`(?:/hooks/|/bin/clued-hook"? )(event-relay|session-start|check-setup)"?$`)
)

func hookIdentity(cmd string) string {
	if m := bashHook.FindStringSubmatch(cmd); m != nil {
		return "hooks " + m[1]
	}
	if m := shimCommand.FindStringSubmatch(cmd); m != nil {
		return "clued " + m[1]
	}
	return cmd
}

// identities lists, per event, what the groups run.
func identities(groups map[string][]hookGroup) map[string][]string {
	out := map[string][]string{}
	for ev, gs := range groups {
		for _, g := range gs {
			for _, h := range g.Hooks {
				out[ev] = append(out[ev], hookIdentity(h.Command))
			}
		}
		sort.Strings(out[ev])
	}
	return out
}

// settingsHooks reads dir/settings.json's hooks (nil when there are none).
func settingsHooks(t *testing.T, dir string) map[string][]hookGroup {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "settings.json"))
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

// copyPluginRoot copies the plugin's hooks, scripts and manifest into
// parent/name and returns that root.
func copyPluginRoot(t *testing.T, parent, name string) string {
	t.Helper()
	src := repoRoot(t)
	dst := filepath.Join(parent, name)
	for _, sub := range []string{"hooks", "scripts", ".claude-plugin"} {
		err := filepath.WalkDir(filepath.Join(src, sub), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			if d.IsDir() {
				return os.MkdirAll(filepath.Join(dst, rel), 0o755)
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dst, rel), b, info.Mode().Perm())
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

// runRegisterHooks runs root/hooks/register-hooks with the settings in cfg
// and the launcher under data.
func runRegisterHooks(t *testing.T, root, cfg, data string, args ...string) {
	t.Helper()
	requireJq(t)
	env := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CLAUDE_CONFIG_DIR=" + cfg, "CLAUDE_PLUGIN_DATA=" + data, "CLUED_STATUS_LINE=1"}
	if r := runScript(t, filepath.Join(root, "hooks", "register-hooks"), "", env, args...); r.code != 0 {
		t.Fatalf("register-hooks %v: %+v", args, r)
	}
}

func TestHookConfigLocation(t *testing.T) {
	root := repoRoot(t)
	if _, err := os.Stat(filepath.Join(root, "hooks.json")); err == nil {
		t.Error("hooks.json at the plugin root: Claude Code loads a plugin's hooks from hooks/hooks.json")
	}
	if len(readHooksJSON(t)) == 0 {
		t.Error("hooks/hooks.json registers no hook")
	}
	var manifest map[string]json.RawMessage
	readJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), &manifest)
	if h, ok := manifest["hooks"]; ok && string(h) != `"./hooks/hooks.json"` {
		t.Errorf("plugin.json names hooks %s; the default hooks/hooks.json is the one file", h)
	}
}

func TestNoDuplicateRegistration(t *testing.T) {
	plugin := identities(readHooksJSON(t))
	// Settings as an older clued left them: the mirror with absolute
	// plugin-root paths, plus a hook of someone else's.
	cfg := t.TempDir()
	legacy := map[string]any{"hooks": map[string]any{
		"SessionStart": []any{map[string]any{"hooks": []any{
			map[string]any{"type": "command", "command": "/old/clued/hooks/check-setup"},
			map[string]any{"type": "command", "command": "/old/clued/scripts/clued hook prime"},
		}}},
		"PreToolUse": []any{
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/old/clued/hooks/event-relay", "async": true}}},
			map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/other/plugin/hook"}}},
		},
	}}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(cfg, "settings.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	runRegisterHooks(t, repoRoot(t), cfg, filepath.Join(cfg, "data"))
	settings := identities(settingsHooks(t, cfg))
	for ev, cmds := range settings {
		for _, c := range cmds {
			for _, p := range plugin[ev] {
				if c == p {
					t.Errorf("%s: %q is registered in hooks/hooks.json and in settings.json", ev, c)
				}
			}
		}
	}
	if got := settings["PreToolUse"]; len(got) != 1 || got[0] != "/other/plugin/hook" {
		t.Errorf("PreToolUse in settings = %v, want only the other plugin's hook", got)
	}

	// The opt-in mirror registers exactly what hooks/hooks.json does.
	runRegisterHooks(t, repoRoot(t), cfg, filepath.Join(cfg, "data"), "--settings")
	mirror := identities(settingsHooks(t, cfg))
	mirror["PreToolUse"] = removeString(mirror["PreToolUse"], "/other/plugin/hook")
	for ev, want := range plugin {
		if strings.Join(mirror[ev], "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: mirror %v, plugin %v", ev, mirror[ev], want)
		}
	}
	for ev := range mirror {
		if _, ok := plugin[ev]; !ok && len(mirror[ev]) > 0 {
			t.Errorf("%s mirrored but not in hooks/hooks.json", ev)
		}
	}

	// A run without a flag keeps the mirror; --plugin removes it.
	runRegisterHooks(t, repoRoot(t), cfg, filepath.Join(cfg, "data"))
	if len(identities(settingsHooks(t, cfg))["SessionStart"]) == 0 {
		t.Error("a plain run removed the mirror --settings wrote")
	}
	runRegisterHooks(t, repoRoot(t), cfg, filepath.Join(cfg, "data"), "--plugin")
	if got := identities(settingsHooks(t, cfg)); len(got) != 1 || len(got["PreToolUse"]) != 1 {
		t.Errorf("after --plugin, settings hooks = %v, want only the other plugin's", got)
	}
}

func removeString(list []string, s string) []string {
	var out []string
	for _, v := range list {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

// settingsCommandPaths lists the executable (first word) of every hook
// command in cfg/settings.json, and of its status line.
func settingsCommandPaths(t *testing.T, cfg string) []string {
	t.Helper()
	var paths []string
	for _, gs := range settingsHooks(t, cfg) {
		for _, g := range gs {
			for _, h := range g.Hooks {
				paths = append(paths, commandPath(h.Command))
			}
		}
	}
	var s struct {
		StatusLine struct {
			Command string `json:"command"`
		} `json:"statusLine"`
	}
	readJSON(t, filepath.Join(cfg, "settings.json"), &s)
	if s.StatusLine.Command != "" {
		paths = append(paths, commandPath(s.StatusLine.Command))
	}
	return paths
}

// commandPath is the executable a hook command runs: its quoted first
// word, or its first word.
func commandPath(cmd string) string {
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : end+1]
		}
	}
	return strings.Fields(cmd)[0]
}

// assertLauncherResolves fails unless the launcher in data names root and
// every hook it can run exists there.
func assertLauncherResolves(t *testing.T, data, root string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(data, "plugin-root"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != root {
		t.Fatalf("plugin-root = %q, want %q", got, root)
	}
	for _, p := range []string{"scripts/clued", "hooks/event-relay", "hooks/session-start", "hooks/check-setup"} {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("the launcher would run %s: %v", p, err)
		}
	}
}

func TestSettingsPathsExist(t *testing.T) {
	parent := t.TempDir()
	root := copyPluginRoot(t, parent, "clued")
	cfg, data := filepath.Join(parent, "config"), filepath.Join(parent, "data")
	runRegisterHooks(t, root, cfg, data, "--settings")
	paths := settingsCommandPaths(t, cfg)
	if len(paths) == 0 {
		t.Fatal("no settings command")
	}
	for _, p := range paths {
		if fi, err := os.Stat(p); err != nil || fi.Mode()&0o111 == 0 {
			t.Errorf("settings command %s is not an executable file (%v)", p, err)
		}
	}
	assertLauncherResolves(t, data, root)
}

func TestSettingsSurvivePluginMove(t *testing.T) {
	parent := t.TempDir()
	root := copyPluginRoot(t, parent, "clued")
	cfg, data := filepath.Join(parent, "config"), filepath.Join(parent, "data")
	runRegisterHooks(t, root, cfg, data, "--settings")
	before := readSettings(t, cfg)

	moved := filepath.Join(parent, "clued~g2")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	// What session-start runs from the new root on the next session start.
	runRegisterHooks(t, moved, cfg, data)
	if after := readSettings(t, cfg); after != before {
		t.Errorf("settings changed on the move; the launcher path should be stable:\nbefore %s\nafter  %s", before, after)
	}
	for _, p := range settingsCommandPaths(t, cfg) {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("settings command %s: %v", p, err)
		}
	}
	assertLauncherResolves(t, data, moved)

	// The launcher runs the moved root's hooks.
	requireJq(t)
	env := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CLUED_RELEASE_URL=off"}
	r := runScript(t, filepath.Join(data, "bin", "clued-hook"), "", env, "--locate")
	if strings.Contains(r.stderr, "No such file") {
		t.Errorf("launcher did not reach the moved root: %+v", r)
	}
}
