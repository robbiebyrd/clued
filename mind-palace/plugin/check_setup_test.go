package plugin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const checkSetupInstallCmd = "go install github.com/robbiebyrd/clued/mind-palace/cmd/mind-palace@latest"

// runCheckSetup runs hooks/check-setup with no bundled binaries unless dist is set.
func runCheckSetup(t *testing.T, home, path string) result {
	t.Helper()
	return runCheckSetupWithDist(t, home, path, t.TempDir())
}

func runCheckSetupWithDist(t *testing.T, home, path, dist string) result {
	t.Helper()
	return runScript(t, filepath.Join(repoRoot(t), "hooks/check-setup"), "", []string{"HOME=" + home, "PATH=" + path, "CLUED_DIST_DIR=" + dist})
}

func homeWithConfig(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/plugins/data/clued")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func systemMessage(t *testing.T, r result) string {
	t.Helper()
	var m struct {
		SystemMessage string `json:"systemMessage"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &m); err != nil {
		t.Fatalf("stdout is not JSON: %q: %v", r.stdout, err)
	}
	return m.SystemMessage
}

func TestCheckSetupTellsUserToInstallMindPalaceWhenNotOnPath(t *testing.T) {
	r := runCheckSetup(t, homeWithConfig(t), "/usr/bin:/bin")
	msg := systemMessage(t, r)
	if !strings.Contains(msg, checkSetupInstallCmd) {
		t.Errorf("install command missing from: %s", msg)
	}
	if !strings.Contains(msg, "download") {
		t.Errorf("download fallback missing from: %s", msg)
	}
	if strings.Contains(msg, "/clued-setup") {
		t.Errorf("config is present, so no setup nag expected: %s", msg)
	}
}

func TestCheckSetupSilentWhenConfigExistsAndMindPalaceOnPath(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, bin, "mind-palace", "#!/bin/sh\n")
	r := runCheckSetup(t, homeWithConfig(t), bin+":/usr/bin:/bin")
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
}

func TestCheckSetupReportsBothProblemsInOneJSONMessage(t *testing.T) {
	r := runCheckSetup(t, t.TempDir(), "/usr/bin:/bin")
	msg := systemMessage(t, r)
	if !strings.Contains(msg, "/clued-setup") || !strings.Contains(msg, checkSetupInstallCmd) {
		t.Errorf("both problems expected in: %s", msg)
	}
}

func TestCheckSetupSilentWhenDownloadedBinaryExists(t *testing.T) {
	home := homeWithConfig(t)
	bin := filepath.Join(home, ".claude/plugins/data/clued/bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, bin, "mind-palace", "#!/bin/sh\n")
	r := runCheckSetup(t, home, "/usr/bin:/bin")
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
}

func TestCheckSetupSilentWhenBundledBinaryExists(t *testing.T) {
	home := homeWithConfig(t)
	r := runCheckSetupWithDist(t, home, "/usr/bin:/bin", fakeDist(t, "bundled"))
	if r.stdout != "" {
		t.Errorf("stdout = %q, want empty", r.stdout)
	}
}

func TestCheckSetupNamesPlatformWhenNoBundledBinaryFits(t *testing.T) {
	r := runCheckSetup(t, homeWithConfig(t), "/usr/bin:/bin")
	msg := systemMessage(t, r)
	if !strings.Contains(msg, "bundled") {
		t.Errorf("message should say no binary is bundled for this platform: %s", msg)
	}
}
