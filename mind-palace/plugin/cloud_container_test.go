package plugin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Claude Code cloud container (CLAUDE_CODE_REMOTE=true) is reclaimed after
// the session, so the hooks never run local mode there.

func TestCheckSetupSendsACloudContainerWithoutAServerToCluedCloud(t *testing.T) {
	requireJq(t)
	bin := t.TempDir()
	writeExecutable(t, bin, "mind-palace", "#!/bin/sh\n")
	for _, home := range []string{t.TempDir(), homeWithConfig(t)} {
		r := runScript(t, filepath.Join(repoRoot(t), "hooks/check-setup"), "",
			[]string{"HOME=" + home, "PATH=" + bin + ":/usr/bin:/bin", "CLUED_RELEASE_URL=off", "CLAUDE_CODE_REMOTE=true"})
		msg := systemMessage(t, r)
		for _, want := range []string{"cloud container", "https://clued.cloud", "/clued-setup", "CLUED_SERVER_URL", "CLUED_SERVER_TOKEN"} {
			if !strings.Contains(msg, want) {
				t.Errorf("message lacks %q: %s", want, msg)
			}
		}
		if strings.Count(msg, "/clued-setup") != 1 {
			t.Errorf("one setup nag expected: %s", msg)
		}
	}
}

func TestCheckSetupAcceptsAServerFromTheEnvironmentWithoutAConfigFile(t *testing.T) {
	bin := t.TempDir()
	writeExecutable(t, bin, "mind-palace", "#!/bin/sh\n")
	// Without jq the whoami probe is skipped; with it the stub binary's
	// silent success leaves nothing to report either.
	for _, remote := range []string{"", "CLAUDE_CODE_REMOTE=true"} {
		env := []string{"HOME=" + t.TempDir(), "PATH=" + bin + ":/usr/bin:/bin", "CLUED_RELEASE_URL=off", "CLUED_SERVER_URL=https://clued.cloud", "CLUED_SERVER_TOKEN=x"}
		if remote != "" {
			env = append(env, remote)
		}
		r := runScript(t, filepath.Join(repoRoot(t), "hooks/check-setup"), "", env)
		if strings.Contains(r.stdout, "/clued-setup") || strings.Contains(r.stdout, "cloud container") {
			t.Errorf("%q: the environment names a server, so no setup nag expected: %s", remote, r.stdout)
		}
	}
}

func TestSessionStartStartsNoDaemonInACloudContainer(t *testing.T) {
	home := t.TempDir()
	env := []string{"HOME=" + home, "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_RELEASE_URL=off", "CLAUDE_CODE_REMOTE=true"}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/session-start"), "", env)
	if r.code != 0 {
		t.Fatalf("code = %d, stderr %q", r.code, r.stderr)
	}
	for _, f := range []string{"daemon.log", "daemon.binary"} {
		if _, err := os.Stat(filepath.Join(home, ".claude/plugins/data/clued", f)); err == nil {
			t.Errorf("%s written: a daemon was started in a cloud container", f)
		}
	}
}

func TestEventRelayWritesNoWALInACloudContainerWithoutAServer(t *testing.T) {
	walPath := filepath.Join(t.TempDir(), "events.wal")
	env := []string{"HOME=" + t.TempDir(), "PATH=/usr/bin:/bin", "CLUED_PORT=" + closedPort(t), "CLUED_WAL_PATH=" + walPath, "CLUED_RELEASE_URL=off", "CLAUDE_CODE_REMOTE=true"}
	r := runScript(t, filepath.Join(repoRoot(t), "hooks/event-relay"), `{"session_id":"s1"}`, env)
	if r.code != 0 {
		t.Fatalf("code = %d", r.code)
	}
	if _, err := os.Stat(walPath); err == nil {
		t.Error("a WAL was written in a cloud container")
	}
}
