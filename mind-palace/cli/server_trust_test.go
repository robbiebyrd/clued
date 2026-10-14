package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/server/stack"
)

// fakeTrust makes server trust plan for a Debian machine with Chrome and
// run nothing for real; it returns the commands run.
func fakeTrust(t *testing.T, root, sudo bool) *[]string {
	t.Helper()
	var ran []string
	savedRunner, savedEnv, savedRoot := trustRunner, trustEnv, trustAsRoot
	t.Cleanup(func() { trustRunner, trustEnv, trustAsRoot = savedRunner, savedEnv, savedRoot })
	trustRunner = func(_ context.Context, name string, args ...string) ([]byte, error) {
		line := strings.Join(append([]string{name}, args...), " ")
		ran = append(ran, line)
		if line == "sudo -n true" && !sudo {
			return nil, fmt.Errorf("a password is required")
		}
		return nil, nil
	}
	trustEnv = func() stack.TrustEnv {
		return stack.TrustEnv{GOOS: "linux", Home: "/home/me",
			LookPath: func(c string) (string, error) {
				if c == "update-ca-certificates" || c == "certutil" {
					return "/usr/bin/" + c, nil
				}
				return "", errors.New("no")
			},
			Exists: func(p string) bool { return p == "/usr/local/share/ca-certificates" || p == "/home/me/.pki/nssdb" },
			Glob:   func(string) ([]string, error) { return nil, nil },
		}
	}
	trustAsRoot = func() bool { return root }
	return &ran
}

func TestServerTrustInstallsWhatItCanAndPrintsTheRest(t *testing.T) {
	clearServerEnv(t)
	dir := filepath.Join(t.TempDir(), "stack")
	serverResult(t, runServer(t, "init", "--dir", dir, "--name", "laptop", "--no-token"))

	ran := fakeTrust(t, false, false)
	res := serverResult(t, runServer(t, "trust", "--dir", dir, "--url", "https://127.0.0.1:1"))
	pending, _ := res["pending"].([]any)
	if len(pending) != 1 || !strings.HasPrefix(pending[0].(string), "sudo install -m 0644 "+filepath.Join(dir, stack.CAFile)) {
		t.Fatalf("pending = %v", res["pending"])
	}
	if !strings.Contains(strings.Join(*ran, "\n"), "certutil -d sql:/home/me/.pki/nssdb -A") {
		t.Errorf("the NSS step did not run: %v", *ran)
	}
	if res["verified"] != nil {
		t.Errorf("verified with steps pending: %v", res["verified"])
	}
	if fp, _ := res["fingerprint"].(string); len(fp) != 64 {
		t.Errorf("fingerprint = %v", res["fingerprint"])
	}

	// With passwordless sudo nothing is left, and the server is checked.
	fakeTrust(t, false, true)
	res = serverResult(t, runServer(t, "trust", "--dir", dir, "--url", "https://127.0.0.1:1"))
	if pending, _ := res["pending"].([]any); len(pending) != 0 || res["verified"] != false || res["verify_error"] == "" {
		t.Errorf("with sudo: pending %v, verified %v (%v)", res["pending"], res["verified"], res["verify_error"])
	}

	// --dry-run runs nothing; --remove plans the removal.
	ran = fakeTrust(t, true, false)
	res = serverResult(t, runServer(t, "trust", "--dir", dir, "--dry-run"))
	if len(*ran) != 0 || len(res["pending"].([]any)) != 2 {
		t.Errorf("dry run ran %v, listed %v", *ran, res["pending"])
	}
	res = serverResult(t, runServer(t, "trust", "--dir", dir, "--remove"))
	if res["removed"] != true || !strings.Contains(strings.Join(*ran, "\n"), "rm -f /usr/local/share/ca-certificates/clued-server-ca-") {
		t.Errorf("remove ran %v", *ran)
	}
}

func TestServerTrustNeedsACA(t *testing.T) {
	fakeTrust(t, false, false)
	r := runServer(t, "trust", "--dir", t.TempDir())
	if r.code == 0 || !strings.Contains(r.stderr, "run server init first") {
		t.Errorf("trust without a CA = %+v", r)
	}
}

func TestStackURLReadsThePublishedPort(t *testing.T) {
	clearServerEnv(t)
	dir := filepath.Join(t.TempDir(), "stack")
	serverResult(t, runServer(t, "init", "--dir", dir, "--name", "laptop", "--no-token", "--port", "9443"))
	if got := stackURL(dir); got != "https://localhost:9443" {
		t.Errorf("stackURL = %q", got)
	}
	none := filepath.Join(t.TempDir(), "none")
	serverResult(t, runServer(t, "init", "--dir", none, "--name", "laptop", "--no-token", "--proxy", "none"))
	if got := stackURL(none); got != "" {
		t.Errorf("stackURL with --proxy none = %q", got)
	}
}
