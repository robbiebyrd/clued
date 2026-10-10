package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStartSurfacesShimErrorFromDaemonLog(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", env)
	if r.code != 0 {
		t.Fatalf("code = %d", r.code)
	}
	logBytes, err := os.ReadFile(filepath.Join(home, ".claude/plugins/data/clued/daemon.log"))
	if err != nil {
		t.Fatalf("daemon.log missing: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(logBytes)), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(r.stderr, last) {
		t.Errorf("stderr %q should contain the log's last line %q", r.stderr, last)
	}
	if strings.Contains(r.stderr, "check MongoDB") {
		t.Errorf("misleading MongoDB message in %q", r.stderr)
	}
}
