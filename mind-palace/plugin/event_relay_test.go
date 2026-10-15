package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEventRelayFallsBackToAPrivateWALWhenNothingListens(t *testing.T) {
	home := t.TempDir()
	walDir := filepath.Join(t.TempDir(), "data", "clued")
	walPath := filepath.Join(walDir, "events.wal")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_WAL_PATH=" + walPath, "CLUED_RELEASE_URL=off"}
	body := `{"session_id":"s1","tool_name":"Bash"}`

	r := runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), body, env)
	if r.code != 0 {
		t.Fatalf("code = %d, stderr %q", r.code, r.stderr)
	}

	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("WAL not written: %v", err)
	}
	if string(data) != body+"\n" {
		t.Errorf("WAL = %q, want the body as one line", data)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Errorf("WAL has %d lines, want 1", got)
	}
	for path, want := range map[string]os.FileMode{walPath: 0o600, walDir: 0o700} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s mode = %#o, want %#o", path, info.Mode().Perm(), want)
		}
	}
}

func TestEventRelayNoCaptureExitsSilently(t *testing.T) {
	home := t.TempDir()
	walPath := filepath.Join(t.TempDir(), "events.wal")
	port, hits := healthProbe(t)
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + port, "CLUED_WAL_PATH=" + walPath, "CLUED_RELEASE_URL=off", "CLUED_NO_CAPTURE=1"}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"engine","hook_event_name":"SessionStart"}`, env)
	if r.code != 0 || r.stdout != "" || r.stderr != "" {
		t.Fatalf("got %+v", r)
	}
	if _, err := os.Stat(walPath); !os.IsNotExist(err) {
		t.Error("an engine run's event reached the WAL")
	}
	if *hits != 0 {
		t.Error("an engine run's event reached the daemon")
	}
	// session-start and check-setup do nothing either.
	for _, hook := range []string{"hooks/session-start", "hooks/check-setup"} {
		r := runScript(t, filepath.Join(repoRoot(t), hook), `{"session_id":"engine"}`, env)
		if r.code != 0 || r.stdout != "" || r.stderr != "" {
			t.Errorf("%s: %+v", hook, r)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Error("session-start started something for an engine run")
	}
}

func TestEventRelayMarksMemoryOff(t *testing.T) {
	requireJq(t)
	home := t.TempDir()
	walPath := filepath.Join(t.TempDir(), "events.wal")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_WAL_PATH=" + walPath, "CLUED_RELEASE_URL=off", "CLUED_MEMORY=off"}
	if r := runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"s1","tool_name":"Bash"}`, env); r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	data, _ := os.ReadFile(walPath)
	if strings.TrimSpace(string(data)) != `{"session_id":"s1","tool_name":"Bash","clued_memory":"off"}` {
		t.Errorf("WAL = %q", data)
	}
}
