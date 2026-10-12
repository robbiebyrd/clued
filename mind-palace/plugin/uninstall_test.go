package plugin_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// healthProbe is a loopback server standing in for the daemon's /health: it
// answers 200 and counts the requests, so a test can tell whether the hook
// probed the daemon port at all.
func healthProbe(t *testing.T) (port string, hits *int32) {
	t.Helper()
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	_, port, _ = strings.Cut(strings.TrimPrefix(srv.URL, "http://127.0.0.1"), ":")
	if _, err := strconv.Atoi(port); err != nil {
		t.Fatalf("cannot read the port of %s", srv.URL)
	}
	return port, &n
}

// writeRelayState creates the files the hook relay leaves behind in remote
// mode: the catch-up state and lock of one session, and the failure log.
func writeRelayState(t *testing.T, home string) {
	t.Helper()
	catchup := dataPath(home, "catchup")
	if err := os.MkdirAll(catchup, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join(catchup, "s-1.json"): `{"offset":42,"seq":3}`,
		filepath.Join(catchup, "s-1.lock"): "",
		dataPath(home, "relay.log"):        "2026-10-12T00:00:00Z relay: insert: 401\n",
	} {
		if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func uninstallEnv(home, port string) []string {
	return []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + port}
}

func TestUninstallRemovesRelayStateAndSkipsTheDaemonInRemoteMode(t *testing.T) {
	requireJq(t)
	port, hits := healthProbe(t)

	t.Run("keeping the config", func(t *testing.T) {
		home := remoteHome(t, "https://clued.example.test", remoteToken)
		writeRelayState(t, home)
		r := runScript(t, hook(t, "uninstall"), "", uninstallEnv(home, port), "--keep-config")
		if r.code != 0 {
			t.Fatalf("got %+v", r)
		}
		mustNotExist(t, dataPath(home, "catchup"), "catch-up state")
		mustNotExist(t, dataPath(home, "relay.log"), "relay.log")
		if _, err := os.Stat(dataPath(home, "config.json")); err != nil {
			t.Errorf("--keep-config should keep config.json: %v", err)
		}
		for _, want := range []string{"removed catch-up state", "removed relay log", "uninstall complete"} {
			if !strings.Contains(r.stdout, want) {
				t.Errorf("stdout lacks %q: %q", want, r.stdout)
			}
		}
		if strings.Contains(r.stdout, "daemon") || strings.Contains(r.stdout, "config dir") {
			t.Errorf("remote mode with --keep-config should neither stop a daemon nor remove the config: %q", r.stdout)
		}
	})

	t.Run("removing everything", func(t *testing.T) {
		home := remoteHome(t, "https://clued.example.test", remoteToken)
		writeRelayState(t, home)
		r := runScript(t, hook(t, "uninstall"), "", uninstallEnv(home, port))
		if r.code != 0 {
			t.Fatalf("got %+v", r)
		}
		mustNotExist(t, filepath.Join(home, ".claude/plugins/data/clued"), "data dir")
		if !strings.Contains(r.stdout, "removed config dir") {
			t.Errorf("stdout should report the config dir: %q", r.stdout)
		}
	})

	t.Run("from the environment", func(t *testing.T) {
		home := remoteHome(t, "", "")
		writeRelayState(t, home)
		env := append(uninstallEnv(home, port), "CLUED_SERVER_URL=https://clued.example.test")
		r := runScript(t, hook(t, "uninstall"), "", env, "--keep-config")
		if r.code != 0 || strings.Contains(r.stdout, "daemon") {
			t.Fatalf("got %+v", r)
		}
		mustNotExist(t, dataPath(home, "catchup"), "catch-up state")
	})

	// A daemon answering on the port is never consulted in remote mode: the
	// hook does not probe it, let alone stop it.
	if n := atomic.LoadInt32(hits); n != 0 {
		t.Errorf("the daemon port was probed %d times in remote mode", n)
	}
}

func TestUninstallLocalModeWithoutADaemon(t *testing.T) {
	requireJq(t)
	home := remoteHome(t, "", "")
	if err := os.MkdirAll(dataPath(home, "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := runScript(t, hook(t, "uninstall"), "", uninstallEnv(home, closedPort(t)))
	if r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	if strings.Contains(r.stdout, "daemon stopped") {
		t.Errorf("no daemon answers, so none should be reported stopped: %q", r.stdout)
	}
	if strings.Contains(r.stdout, "catch-up") || strings.Contains(r.stdout, "relay log") {
		t.Errorf("local mode has no relay state to report: %q", r.stdout)
	}
	mustNotExist(t, filepath.Join(home, ".claude/plugins/data/clued"), "data dir")
	if !strings.HasSuffix(strings.TrimSpace(r.stdout), "uninstall complete") {
		t.Errorf("stdout should end with the completion line: %q", r.stdout)
	}
}

func TestUninstallWithNothingInstalled(t *testing.T) {
	home := t.TempDir()
	r := runScript(t, hook(t, "uninstall"), "", uninstallEnv(home, closedPort(t)))
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("got %+v, want exit 0 and no stderr", r)
	}
	if strings.TrimSpace(r.stdout) != "clued: uninstall complete" {
		t.Errorf("stdout = %q, want only the completion line", r.stdout)
	}
}
