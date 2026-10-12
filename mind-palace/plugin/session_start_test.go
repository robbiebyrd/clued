package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionStartSurfacesShimErrorFromDaemonLog(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_RELEASE_URL=off"}
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

func TestSessionStartRecordsTheBinaryTheDaemonWasStartedFrom(t *testing.T) {
	home := t.TempDir()
	rel := pinnedRelease(t, "release")
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_RELEASE_URL=" + rel.url}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", env)
	if r.code != 0 {
		t.Fatalf("code = %d", r.code)
	}
	marker, err := os.ReadFile(filepath.Join(home, ".claude/plugins/data/clued/daemon.binary"))
	if err != nil {
		t.Fatalf("daemon.binary missing: %v", err)
	}
	located := runScript(t, filepath.Join(repoRoot(t), "scripts/clued"), "", env, "--locate")
	if strings.TrimSpace(string(marker)) != strings.TrimSpace(located.stdout) || located.stdout == "" {
		t.Errorf("marker %q should hold the located binary %q", marker, located.stdout)
	}
	if strings.Contains(r.stderr, "restarted") {
		t.Errorf("no daemon was running, so no restart expected: %q", r.stderr)
	}
}
