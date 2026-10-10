package plugin_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeBinary = "#!/bin/sh\necho \"%s $*\"\n"

type shimEnv struct {
	home, pathDir string
}

func newShimEnv(t *testing.T) shimEnv {
	t.Helper()
	return shimEnv{home: t.TempDir(), pathDir: t.TempDir()}
}

func (e shimEnv) dataDir() string { return filepath.Join(e.home, ".claude/plugins/data/clued") }

func (e shimEnv) downloadedBinary() string { return filepath.Join(e.dataDir(), "bin", "mind-palace") }

func (e shimEnv) writeConfig(t *testing.T, body string) {
	t.Helper()
	if err := os.MkdirAll(e.dataDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dataDir(), "config.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (e shimEnv) writeDownloaded(t *testing.T, label string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(e.downloadedBinary()), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Dir(e.downloadedBinary()), "mind-palace", strings.Replace(fakeBinary, "%s", label, 1))
}

// run executes bin/clued with PATH limited to the fake dir plus system dirs (and go when needed).
func (e shimEnv) run(t *testing.T, extraPath string, args ...string) result {
	t.Helper()
	path := e.pathDir + ":/usr/bin:/bin"
	if extraPath != "" {
		path += ":" + extraPath
	}
	env := []string{"HOME=" + e.home, "PATH=" + path, "GOCACHE=" + goCache(t), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "TMPDIR=" + os.TempDir()}
	return runScript(t, filepath.Join(repoRoot(t), "bin/clued"), "", env, args...)
}

func goCache(t *testing.T) string {
	if v := os.Getenv("GOCACHE"); v != "" {
		return v
	}
	return filepath.Join(t.TempDir(), "gocache")
}

func TestShimPrefersMindPalaceOnPath(t *testing.T) {
	e := newShimEnv(t)
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	e.writeDownloaded(t, "downloaded")
	r := e.run(t, "", "session", "daemon", "--addr", ":1")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "path session daemon --addr :1" {
		t.Errorf("got %+v", r)
	}
}

func TestShimFallsBackToDownloadedBinary(t *testing.T) {
	e := newShimEnv(t)
	e.writeDownloaded(t, "downloaded")
	r := e.run(t, "", "session", "mcp")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "downloaded session mcp" {
		t.Errorf("got %+v", r)
	}
}

func TestShimErrorsClearlyWhenNothingResolves(t *testing.T) {
	e := newShimEnv(t)
	r := e.run(t, "", "session", "mcp")
	if r.code != 1 {
		t.Errorf("code = %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "mind-palace") || !strings.Contains(r.stderr, "/clued-setup") {
		t.Errorf("stderr should name the binary and /clued-setup: %q", r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("stdout = %q", r.stdout)
	}
}

func TestShimErrorsWhenDevRepoPathHasNoModule(t *testing.T) {
	e := newShimEnv(t)
	e.writeConfig(t, `{"devRepoPath":"`+t.TempDir()+`"}`)
	r := e.run(t, "")
	if r.code != 1 || !strings.Contains(r.stderr, "mind-palace") {
		t.Errorf("got %+v", r)
	}
}

// fakeDevRepo creates a tiny Go module at dir/mind-palace whose binary prints label and its arguments.
func fakeDevRepo(t *testing.T, label string) string {
	t.Helper()
	repo := t.TempDir()
	mod := filepath.Join(repo, "mind-palace")
	if err := os.MkdirAll(filepath.Join(mod, "cmd/mind-palace"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoMain(t, mod, label)
	if err := os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module fake\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func writeGoMain(t *testing.T, mod, label string) {
	t.Helper()
	src := "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\nfunc main() { fmt.Println(\"" + label + " \" + strings.Join(os.Args[1:], \" \")) }\n"
	if err := os.WriteFile(filepath.Join(mod, "cmd/mind-palace/main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestShimBuildsFromDevRepoPathAndRebuildsWhenSourceIsNewer(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	repo := fakeDevRepo(t, "v1")
	e := newShimEnv(t)
	e.writeConfig(t, `{"devRepoPath":"`+repo+`"}`)
	goDir := filepath.Dir(goBin)

	r := e.run(t, goDir, "hello")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "v1 hello" {
		t.Fatalf("first build: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir(), "bin", ".rebuilt")); err != nil {
		t.Errorf("rebuild marker missing after build: %v", err)
	}
	os.Remove(filepath.Join(e.dataDir(), "bin", ".rebuilt"))

	r = e.run(t, goDir, "again")
	if strings.TrimSpace(r.stdout) != "v1 again" {
		t.Fatalf("unchanged source: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir(), "bin", ".rebuilt")); err == nil {
		t.Error("marker written although nothing was rebuilt")
	}

	writeGoMain(t, filepath.Join(repo, "mind-palace"), "v2")
	future := time.Now().Add(time.Minute)
	if err := os.Chtimes(filepath.Join(repo, "mind-palace/cmd/mind-palace/main.go"), future, future); err != nil {
		t.Fatal(err)
	}
	r = e.run(t, goDir, "third")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "v2 third" {
		t.Errorf("rebuild after change: %+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.dataDir(), "bin", ".rebuilt")); err != nil {
		t.Errorf("rebuild marker missing after rebuild: %v", err)
	}
}

func TestShimBuildOnlyBuildsWithoutRunning(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	repo := fakeDevRepo(t, "v1")
	e := newShimEnv(t)
	e.writeConfig(t, `{"devRepoPath":"`+repo+`"}`)
	r := e.run(t, filepath.Dir(goBin), "--build-only")
	if r.code != 0 || r.stdout != "" {
		t.Errorf("got %+v", r)
	}
	if _, err := os.Stat(e.downloadedBinary()); err != nil {
		t.Errorf("binary not built: %v", err)
	}
}
