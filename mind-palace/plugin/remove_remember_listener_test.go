package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// oldListener is the listener an older clued wrote, with the marker line
// remove-remember-listener looks for.
const oldListener = "#!/bin/bash\n# Installed by the clued plugin (hooks/install-remember-listener)\n# on every clued session start.\nexec clued session memory-ingest --quiet\n"

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

// plantListener writes the old listener into every event directory of root.
func plantListener(t *testing.T, root string, events ...string) {
	t.Helper()
	for _, ev := range events {
		if err := os.WriteFile(filepath.Join(root, "hooks.d", ev, "70-clued-ingest.sh"), []byte(oldListener), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func runRemoveListener(t *testing.T, roots ...string) result {
	t.Helper()
	env := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CLUED_REMEMBER_ROOTS=" + strings.Join(roots, ":")}
	return runScript(t, filepath.Join(repoRoot(t), "hooks/remove-remember-listener"), "", env)
}

func TestRemoveDeletesOnlyCluedListener(t *testing.T) {
	root := fakeRemember(t, "after_save", "after_consolidate", "after_user_prompt")
	plantListener(t, root, "after_save", "after_consolidate")
	// A file of the same name that clued did not write, and another listener.
	foreign := filepath.Join(root, "hooks.d", "after_user_prompt", "70-clued-ingest.sh")
	if err := os.WriteFile(foreign, []byte("#!/bin/bash\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "hooks.d", "after_save", "10-other.sh")
	if err := os.WriteFile(other, []byte("#!/bin/bash\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	notOurs := filepath.Join(fakeRemember(t, "after_save"), "hooks.d", "after_save", "70-clued-ingest.sh")
	if err := os.WriteFile(notOurs, []byte("#!/bin/bash\n# someone else's script\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := runRemoveListener(t, root, filepath.Dir(filepath.Dir(filepath.Dir(notOurs))))
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("got %+v", r)
	}
	for _, ev := range []string{"after_save", "after_consolidate"} {
		if _, err := os.Stat(filepath.Join(root, "hooks.d", ev, "70-clued-ingest.sh")); !os.IsNotExist(err) {
			t.Errorf("%s: clued's listener is still there (%v)", ev, err)
		}
	}
	for _, kept := range []string{foreign, other, notOurs} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
}

func TestRemoveNoRootsIsNoop(t *testing.T) {
	if r := runRemoveListener(t); r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Errorf("no claude-remember installed: %+v", r)
	}
	plain := t.TempDir()
	if r := runRemoveListener(t, plain, filepath.Join(t.TempDir(), "gone")); r.code != 0 || r.stderr != "" {
		t.Errorf("roots without hooks.d: %+v", r)
	}
}

func TestRemoveFindsInstalledPluginsFile(t *testing.T) {
	requireJq(t)
	home := t.TempDir()
	root := fakeRemember(t, "after_save")
	plantListener(t, root, "after_save")
	plugins := filepath.Join(home, ".claude", "plugins")
	if err := os.MkdirAll(plugins, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"version":2,"plugins":{"remember@dpt-plugins":[{"scope":"user","installPath":"` + root + `"}]}}`
	if err := os.WriteFile(filepath.Join(plugins, "installed_plugins.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/remove-remember-listener"), "", []string{"HOME=" + home, "PATH=/usr/bin:/bin"})
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("got %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_save", "70-clued-ingest.sh")); !os.IsNotExist(err) {
		t.Errorf("listener found through installed_plugins.json not removed: %v", err)
	}
}

func sessionStartEnv(t *testing.T, home, roots string) []string {
	return []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_RELEASE_URL=off", "CLUED_REMEMBER_ROOTS=" + roots}
}

func TestSessionStartRemovesListenerOnce(t *testing.T) {
	home := t.TempDir()
	root := fakeRemember(t, "after_save", "after_consolidate")
	plantListener(t, root, "after_save", "after_consolidate")
	if r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", sessionStartEnv(t, home, root)); r.code != 0 {
		t.Fatalf("session-start: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_consolidate", "70-clued-ingest.sh")); !os.IsNotExist(err) {
		t.Errorf("session-start did not remove the listener: %v", err)
	}
	if _, err := os.Stat(dataPath(home, "remember-listener-removed")); err != nil {
		t.Fatalf("no stamp: %v", err)
	}
	// Once: with the stamp in place a later start leaves claude-remember alone.
	plantListener(t, root, "after_save")
	if r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", sessionStartEnv(t, home, root)); r.code != 0 {
		t.Fatalf("second session-start: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_save", "70-clued-ingest.sh")); err != nil {
		t.Errorf("the removal ran again despite the stamp: %v", err)
	}
	waitForReleaseDownloads(t, home)
}

func TestSessionStartNeverInstallsListener(t *testing.T) {
	home := t.TempDir()
	root := fakeRemember(t, "after_save", "after_consolidate")
	if r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", sessionStartEnv(t, home, root)); r.code != 0 {
		t.Fatalf("session-start: %+v", r)
	}
	time.Sleep(200 * time.Millisecond)
	for _, ev := range []string{"after_save", "after_consolidate"} {
		if entries, _ := os.ReadDir(filepath.Join(root, "hooks.d", ev)); len(entries) != 0 {
			t.Errorf("%s holds %v: clued writes nothing into claude-remember", ev, entries)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot(t), "hooks", "install-remember-listener")); !os.IsNotExist(err) {
		t.Error("hooks/install-remember-listener must be gone")
	}
	waitForReleaseDownloads(t, home)
}

func TestUninstallRemovesListener(t *testing.T) {
	home := t.TempDir()
	root := fakeRemember(t, "after_save")
	plantListener(t, root, "after_save")
	if err := os.MkdirAll(dataPath(home, "memory"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{dataPath(home, "memory/runs.json"), dataPath(home, "remember-listener-removed")} {
		if err := os.WriteFile(f, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env := append(uninstallEnv(home, closedPort(t)), "CLUED_REMEMBER_ROOTS="+root)
	if r := runScript(t, hook(t, "uninstall"), "", env, "--keep-config"); r.code != 0 {
		t.Fatalf("uninstall: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(root, "hooks.d", "after_save", "70-clued-ingest.sh")); !os.IsNotExist(err) {
		t.Errorf("uninstall left the listener: %v", err)
	}
	for _, gone := range []string{dataPath(home, "memory/runs.json"), dataPath(home, "remember-listener-removed")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived uninstall --keep-config", gone)
		}
	}
}
