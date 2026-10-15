package plugin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyPrimeNeedsCredentials runs scripts/verify-prime.sh with no
// credentials anywhere: it must stop before starting any session, with exit
// code 3 and a message naming what it looked for.
func TestVerifyPrimeNeedsCredentials(t *testing.T) {
	requireJq(t)
	bin := t.TempDir()
	// A claude that fails the test if a session is started.
	writeExecutable(t, bin, "claude", "#!/bin/sh\n[ \"$1\" = --version ] && { echo '0.0.0 (Claude Code)'; exit 0; }\necho \"unexpected claude $*\" >&2\nexit 99\n")
	env := []string{"HOME=" + t.TempDir(), "PATH=" + bin + ":/usr/bin:/bin"}
	r := runScript(t, filepath.Join(repoRoot(t), "scripts", "verify-prime.sh"), "", env)
	if r.code != 3 {
		t.Fatalf("code = %d, want 3: %+v", r.code, r)
	}
	if !strings.Contains(r.stderr, "no Claude credentials") || !strings.Contains(r.stderr, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if strings.Contains(r.stderr, "unexpected claude") {
		t.Errorf("a session was started without credentials: %q", r.stderr)
	}
}

// TestVerifyPrimeLive runs the real check (several `claude -p` sessions,
// a few cents each). It needs Claude credentials and runs only when
// CLUED_VERIFY_PRIME=1, so CI and a plain `go test` skip it.
func TestVerifyPrimeLive(t *testing.T) {
	if os.Getenv("CLUED_VERIFY_PRIME") != "1" {
		t.Skip("set CLUED_VERIFY_PRIME=1 (with Claude credentials) to run real claude -p sessions")
	}
	requireJq(t)
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed")
	}
	cmd := exec.Command("/bin/bash", filepath.Join(repoRoot(t), "scripts", "verify-prime.sh"))
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 3 {
		t.Skipf("no Claude credentials: %s", out)
	}
	if err != nil {
		t.Fatalf("verify-prime.sh: %v\n%s", err, out)
	}
	t.Logf("%s", out)
}
