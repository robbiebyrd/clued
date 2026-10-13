package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	// session-start leaves detached hooks behind (backfill, memory-ingest)
	// that resolve the binary through the shim; wait until none of them is
	// still downloading into this home, or the temp dir cleanup races them.
	waitForReleaseDownloads(t, home)
}

// waitForReleaseDownloads returns once no shim holds a download lock under
// home's release directory.
func waitForReleaseDownloads(t *testing.T, home string) {
	t.Helper()
	locks, _ := filepath.Glob(filepath.Join(home, ".claude/plugins/data/clued/bin/releases/*/.lock"))
	for i := 0; i < 100 && len(locks) > 0; i++ {
		time.Sleep(50 * time.Millisecond)
		locks, _ = filepath.Glob(filepath.Join(home, ".claude/plugins/data/clued/bin/releases/*/.lock"))
	}
	time.Sleep(100 * time.Millisecond)
}
