package plugin_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fakeBinary = "#!/bin/sh\necho \"%s $*\"\n"

type shimEnv struct {
	home, pathDir string
	// dist stands in for the plugin's bin/dist; it starts empty so the
	// archives committed in the repository never leak into a test.
	dist string
}

func newShimEnv(t *testing.T) shimEnv {
	t.Helper()
	return shimEnv{home: t.TempDir(), pathDir: t.TempDir(), dist: t.TempDir()}
}

// bundledAssetName is the archive name bin/clued looks for on this machine.
func bundledAssetName(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skipf("no bundled binary naming for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "arm":
	default:
		t.Skipf("no bundled binary naming for %s", runtime.GOARCH)
	}
	return "mind-palace_" + runtime.GOOS + "_" + runtime.GOARCH
}

// writeDist fills dir like scripts/build-binaries.sh does, with a fake binary
// for this platform that prints label and its arguments. It returns the sha256
// recorded in SHA256SUMS, or the one given in override instead.
func writeDist(t *testing.T, dir, label string, override string) string {
	t.Helper()
	name := bundledAssetName(t)
	body := []byte(strings.Replace(fakeBinary, "%s", label, 1))
	digest := sha256.Sum256(body)
	sum := hex.EncodeToString(digest[:])
	if override != "" {
		sum = override
	}
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".gz"), gz.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sums := fmt.Sprintf("%s  mind-palace_other_arch\n%s  %s\n", strings.Repeat("0", 64), sum, name)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return sum
}

// fakeDist returns a dist directory holding a fake bundled binary that prints label.
func fakeDist(t *testing.T, label string) string {
	t.Helper()
	dir := t.TempDir()
	writeDist(t, dir, label, "")
	return dir
}

func (e shimEnv) bundledBinary(sum string) string {
	return filepath.Join(e.dataDir(), "bin", "bundled", sum, "mind-palace")
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
	env := []string{"HOME=" + e.home, "PATH=" + path, "GOCACHE=" + goCache(t), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "TMPDIR=" + os.TempDir(), "CLUED_DIST_DIR=" + e.dist}
	return runScript(t, filepath.Join(repoRoot(t), "bin/clued"), "", env, args...)
}

func goCache(t *testing.T) string {
	if v := os.Getenv("GOCACHE"); v != "" {
		return v
	}
	return filepath.Join(t.TempDir(), "gocache")
}

func TestShimPrefersMindPalaceOnPathWhenNothingIsBundled(t *testing.T) {
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

func TestShimDevBuildOutranksPathWhenDevRepoPathIsSet(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	e := newShimEnv(t)
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	e.writeConfig(t, `{"devRepoPath":"`+fakeDevRepo(t, "dev")+`"}`)
	r := e.run(t, filepath.Dir(goBin), "x")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "dev x" {
		t.Errorf("got %+v, want the dev build to win over PATH", r)
	}
}

func TestShimUnpacksBundledBinaryOnceAndVerifiesIt(t *testing.T) {
	e := newShimEnv(t)
	sum := writeDist(t, e.dist, "bundled", "")
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	e.writeDownloaded(t, "downloaded")

	r := e.run(t, "", "session", "mcp")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "bundled session mcp" {
		t.Fatalf("bundled binary should win over PATH and the download: %+v", r)
	}
	if _, err := os.Stat(e.bundledBinary(sum)); err != nil {
		t.Fatalf("unpacked binary missing: %v", err)
	}

	// The archive is only needed once: the unpacked copy is reused afterwards.
	if err := os.Remove(filepath.Join(e.dist, bundledAssetName(t)+".gz")); err != nil {
		t.Fatal(err)
	}
	r = e.run(t, "", "again")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "bundled again" {
		t.Errorf("second run should use the unpacked copy: %+v", r)
	}
}

func TestShimLocatePrintsTheResolvedBinary(t *testing.T) {
	e := newShimEnv(t)
	sum := writeDist(t, e.dist, "bundled", "")
	r := e.run(t, "", "--locate")
	if r.code != 0 || strings.TrimSpace(r.stdout) != e.bundledBinary(sum) {
		t.Errorf("got %+v, want the unpacked path", r)
	}
	empty := newShimEnv(t)
	r = empty.run(t, "", "--locate")
	if r.code != 1 || r.stdout != "" {
		t.Errorf("nothing to locate: got %+v", r)
	}
}

func TestShimReplacesUnpackedCopyWhenThePluginUpdates(t *testing.T) {
	e := newShimEnv(t)
	oldSum := writeDist(t, e.dist, "v1", "")
	if r := e.run(t, "", "x"); strings.TrimSpace(r.stdout) != "v1 x" {
		t.Fatalf("got %+v", r)
	}
	newSum := writeDist(t, e.dist, "v2", "")
	r := e.run(t, "", "y")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "v2 y" {
		t.Fatalf("new archive should be unpacked: %+v", r)
	}
	if _, err := os.Stat(e.bundledBinary(newSum)); err != nil {
		t.Errorf("new copy missing: %v", err)
	}
	if _, err := os.Stat(e.bundledBinary(oldSum)); !os.IsNotExist(err) {
		t.Errorf("old copy should be removed, stat err = %v", err)
	}
}

func TestShimRefusesBundledBinaryWithWrongHash(t *testing.T) {
	e := newShimEnv(t)
	bad := strings.Repeat("ab", 32)
	writeDist(t, e.dist, "bundled", bad)
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	r := e.run(t, "", "z")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "path z" {
		t.Errorf("should fall back to PATH: %+v", r)
	}
	if !strings.Contains(r.stderr, "SHA256SUMS") {
		t.Errorf("stderr should explain the mismatch: %q", r.stderr)
	}
	if _, err := os.Stat(e.bundledBinary(bad)); !os.IsNotExist(err) {
		t.Errorf("mismatched binary must not be kept, stat err = %v", err)
	}
}

func TestShimDevBuildOutranksBundledBinary(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	e := newShimEnv(t)
	writeDist(t, e.dist, "bundled", "")
	e.writeConfig(t, `{"devRepoPath":"`+fakeDevRepo(t, "dev")+`"}`)
	r := e.run(t, filepath.Dir(goBin), "x")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "dev x" {
		t.Errorf("got %+v, want the dev build to win over the bundled binary", r)
	}
}

// TestDistShipsEveryTarget checks the committed bin/dist against the target
// list in scripts/build-binaries.sh; the hashes themselves are verified by
// `scripts/build-binaries.sh --check` in CI.
func TestDistShipsEveryTarget(t *testing.T) {
	dist := filepath.Join(repoRoot(t), "bin", "dist")
	sums, err := os.ReadFile(filepath.Join(dist, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"mind-palace_darwin_amd64", "mind-palace_darwin_arm64",
		"mind-palace_linux_amd64", "mind-palace_linux_arm64", "mind-palace_linux_arm",
		"mind-palace_windows_amd64.exe", "mind-palace_windows_arm64.exe",
	}
	listed := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			t.Errorf("malformed SHA256SUMS line %q", line)
			continue
		}
		listed[fields[1]] = true
	}
	for _, name := range want {
		if !listed[name] {
			t.Errorf("%s missing from SHA256SUMS", name)
		}
		if _, err := os.Stat(filepath.Join(dist, name+".gz")); err != nil {
			t.Errorf("%s.gz missing: %v", name, err)
		}
	}
	if len(listed) != len(want) {
		t.Errorf("SHA256SUMS lists %d binaries, want %d", len(listed), len(want))
	}
}
