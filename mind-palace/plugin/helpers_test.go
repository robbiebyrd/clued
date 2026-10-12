package plugin_test

import (
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// repoRoot returns the plugin root, two levels above this test file's package directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

type result struct {
	stdout, stderr string
	code           int
}

// runScript runs a script with bash, the given stdin and exactly the given environment.
func runScript(t *testing.T, script, stdin string, env []string, args ...string) result {
	t.Helper()
	return runScriptIn(t, "", script, stdin, env, args...)
}

// runScriptIn is runScript with the working directory set to dir ("" keeps
// the test's), which is what a hook sees as the project directory.
func runScriptIn(t *testing.T, dir, script, stdin string, env []string, args ...string) result {
	t.Helper()
	cmd := exec.Command("/bin/bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdin = bytes.NewBufferString(stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %s: %v", script, err)
		}
		code = ee.ExitCode()
	}
	return result{out.String(), errb.String(), code}
}

func requireJq(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not installed")
	}
}

// writeExecutable writes an executable script at dir/name.
func writeExecutable(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// closedPort returns a loopback port nothing listens on, so a test never
// reaches a real daemon on the default port.
func closedPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return strconv.Itoa(port)
}
