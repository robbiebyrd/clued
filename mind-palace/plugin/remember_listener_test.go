package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRemember creates a claude-remember plugin root with the hooks.d
// directories the plugin ships.
func fakeRemember(t *testing.T, events ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, ev := range events {
		if err := os.MkdirAll(filepath.Join(root, "hooks.d", ev), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func runInstallListener(t *testing.T, roots ...string) result {
	t.Helper()
	env := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CLUED_REMEMBER_ROOTS=" + strings.Join(roots, ":")}
	return runScript(t, filepath.Join(repoRoot(t), "hooks/install-remember-listener"), "", env)
}

func TestInstallRememberListenerWritesAnExecutableListenerForBothEvents(t *testing.T) {
	root := fakeRemember(t, "after_save", "after_consolidate", "after_user_prompt")
	r := runInstallListener(t, root)
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("got %+v", r)
	}
	for _, ev := range []string{"after_save", "after_consolidate"} {
		path := filepath.Join(root, "hooks.d", ev, "70-clued-ingest.sh")
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("%s: %v", ev, err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Errorf("%s mode = %o, want 0755 (claude-remember refuses group-writable listeners)", ev, info.Mode().Perm())
		}
		body, _ := os.ReadFile(path)
		want := `exec "` + filepath.Join(repoRoot(t), "scripts", "clued") + `" session memory-ingest --dir "$REMEMBER_DIR" --cwd "${PROJECT_DIR:-$PWD}" --quiet`
		if !strings.HasPrefix(string(body), "#!/bin/bash\n") || !strings.Contains(string(body), want) {
			t.Errorf("%s body = %q", ev, body)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_user_prompt", "70-clued-ingest.sh")); !os.IsNotExist(err) {
		t.Error("the listener must not run on every prompt")
	}
}

func TestInstallRememberListenerIsIdempotentAndSkipsRootsWithoutHooks(t *testing.T) {
	root := fakeRemember(t, "after_save")
	plain := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	for i := 0; i < 2; i++ {
		if r := runInstallListener(t, root, plain, missing); r.code != 0 || r.stderr != "" {
			t.Fatalf("run %d: %+v", i, r)
		}
	}
	entries, err := os.ReadDir(filepath.Join(root, "hooks.d", "after_save"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "70-clued-ingest.sh" {
		t.Errorf("after_save holds %v (%v), want exactly the listener", entries, err)
	}
	if entries, _ := os.ReadDir(plain); len(entries) != 0 {
		t.Errorf("a root without hooks.d was written to: %v", entries)
	}
	if r := runInstallListener(t); r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("no claude-remember installed should be silent: %+v", r)
	}
}

func TestInstallRememberListenerFindsInstalledPluginsFile(t *testing.T) {
	requireJq(t)
	home := t.TempDir()
	root := fakeRemember(t, "after_save")
	plugins := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":2,"plugins":{"remember@dpt-plugins":[{"scope":"user","installPath":"` + root + `"}],"other@x":[{"installPath":"/nowhere"}]}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/install-remember-listener"), "", []string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_save", "70-clued-ingest.sh")); err != nil {
		t.Errorf("listener not installed from installed_plugins.json: %v", err)
	}
}

func TestSessionStartInstallsTheRememberListener(t *testing.T) {
	root := fakeRemember(t, "after_save", "after_consolidate")
	env := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_DIST_DIR=" + t.TempDir(), "CLUED_REMEMBER_ROOTS=" + root}
	if r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", env); r.code != 0 {
		t.Fatalf("session-start: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_consolidate", "70-clued-ingest.sh")); err != nil {
		t.Errorf("session-start did not install the listener: %v", err)
	}
}
