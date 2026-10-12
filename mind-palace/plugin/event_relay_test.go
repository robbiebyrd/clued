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
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_WAL_PATH=" + walPath, "CLUED_DIST_DIR=" + t.TempDir()}
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
