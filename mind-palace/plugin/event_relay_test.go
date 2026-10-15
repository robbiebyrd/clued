package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// fakeMindPalace puts a mind-palace on PATH that appends its arguments to
// a log, and returns the PATH and the log.
func fakeMindPalace(t *testing.T) (path, log string) {
	t.Helper()
	bin := t.TempDir()
	log = filepath.Join(t.TempDir(), "calls.log")
	writeExecutable(t, bin, "mind-palace", "#!/bin/sh\necho \"$*\" >> \""+log+"\"\n")
	return bin + ":/usr/bin:/bin", log
}

// waitForLog returns the log once it holds a line, or "" after a while.
func waitForLog(t *testing.T, log string, wait time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(log); err == nil && len(data) > 0 {
			return string(data)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

func relayMemoryEnv(t *testing.T, home, path string, extra ...string) []string {
	return append([]string{"HOME=" + home, "PATH=" + path, "CLUED_PORT=" + closedPort(t), "CLUED_WAL_PATH=" + filepath.Join(t.TempDir(), "events.wal"), "CLUED_RELEASE_URL=off"}, extra...)
}

func TestSessionEndRunsMemoryDetached(t *testing.T) {
	requireJq(t)
	path, log := fakeMindPalace(t)
	home := t.TempDir()
	start := time.Now()
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"s1","hook_event_name":"SessionEnd"}`, relayMemoryEnv(t, home, path))
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("got %+v", r)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("the hook waited for the memory run")
	}
	got := waitForLog(t, log, 5*time.Second)
	if !strings.Contains(got, "session memory run --session s1 --quiet") || strings.Contains(got, "--checkpoint") {
		t.Errorf("memory run = %q", got)
	}
	// Other events run nothing.
	path2, log2 := fakeMindPalace(t)
	runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"s1","hook_event_name":"Stop"}`, relayMemoryEnv(t, home, path2))
	if got := waitForLog(t, log2, 300*time.Millisecond); got != "" {
		t.Errorf("a Stop event ran %q", got)
	}
}

func TestPreCompactRunsCheckpoint(t *testing.T) {
	requireJq(t)
	path, log := fakeMindPalace(t)
	home := t.TempDir()
	runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"s2","hook_event_name":"PreCompact"}`, relayMemoryEnv(t, home, path))
	if got := waitForLog(t, log, 5*time.Second); !strings.Contains(got, "session memory run --session s2 --quiet --checkpoint") {
		t.Errorf("checkpoint run = %q", got)
	}
	// Nothing when the engine is off, in the config or the environment, or
	// when the session was started with CLUED_MEMORY=off.
	cfgDir := filepath.Join(home, ".claude", "plugins", "data", "clued")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"memory":{"engine":"off"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{nil, {"CLUED_MEMORY_ENGINE=off"}, {"CLUED_MEMORY=off"}} {
		offHome := home
		if extra != nil {
			offHome = t.TempDir()
		}
		path, log := fakeMindPalace(t)
		runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"s3","hook_event_name":"PreCompact"}`, relayMemoryEnv(t, offHome, path, extra...))
		if got := waitForLog(t, log, 300*time.Millisecond); got != "" {
			t.Errorf("memory off (%v) still ran %q", extra, got)
		}
	}
}
