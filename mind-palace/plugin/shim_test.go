package plugin_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const fakeBinary = "#!/bin/sh\necho \"%s $*\"\n"

type shimEnv struct {
	home, pathDir string
	// releaseURL is where scripts/clued downloads the pinned release from;
	// "off" keeps the real GitHub release out of every test.
	releaseURL string
}

func newShimEnv(t *testing.T) shimEnv {
	t.Helper()
	return shimEnv{home: t.TempDir(), pathDir: t.TempDir(), releaseURL: "off"}
}

// releaseAssetName is the release asset scripts/clued looks for on this machine.
func releaseAssetName(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		t.Skipf("no release asset naming for %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64", "arm":
	default:
		t.Skipf("no release asset naming for %s", runtime.GOARCH)
	}
	return "mind-palace_" + runtime.GOOS + "_" + runtime.GOARCH
}

// pluginVersion is the release the plugin manifest pins.
func pluginVersion(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version == "" {
		t.Fatal("plugin.json has no version")
	}
	return manifest.Version
}

// releaseServer stands in for GitHub's release downloads: it serves a
// directory of v<version>/ folders over HTTP and counts the requests.
type releaseServer struct {
	dir  string
	url  string
	hits atomic.Int32
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	s := &releaseServer{dir: t.TempDir()}
	files := http.FileServer(http.Dir(s.dir))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	s.url = srv.URL
	return s
}

// publish writes release v<version> like scripts/build-binaries.sh does,
// with a fake binary for this platform that prints label and its arguments.
// It returns the sha256 recorded in SHA256SUMS, or the one given in override
// instead.
func (s *releaseServer) publish(t *testing.T, version, label, override string) string {
	t.Helper()
	name := releaseAssetName(t)
	body := []byte(strings.Replace(fakeBinary, "%s", label, 1))
	digest := sha256.Sum256(body)
	sum := hex.EncodeToString(digest[:])
	if override != "" {
		sum = override
	}
	dir := filepath.Join(s.dir, "v"+version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatal(err)
	}
	sums := fmt.Sprintf("%s  mind-palace_other_arch\n%s  %s\n", strings.Repeat("0", 64), sum, name)
	if err := os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	return sum
}

// pinnedRelease returns a release server that publishes the version the
// plugin pins, with a fake binary printing label.
func pinnedRelease(t *testing.T, label string) *releaseServer {
	t.Helper()
	s := newReleaseServer(t)
	s.publish(t, pluginVersion(t), label, "")
	return s
}

func (e shimEnv) releaseBinary(version string) string {
	return filepath.Join(e.dataDir(), "bin", "releases", "v"+version, "mind-palace")
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

// run executes scripts/clued with PATH limited to the fake dir plus system dirs (and go when needed).
func (e shimEnv) run(t *testing.T, extraPath string, args ...string) result {
	t.Helper()
	path := e.pathDir + ":/usr/bin:/bin"
	if extraPath != "" {
		path += ":" + extraPath
	}
	env := []string{"HOME=" + e.home, "PATH=" + path, "GOCACHE=" + goCache(t), "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local", "TMPDIR=" + os.TempDir(), "CLUED_RELEASE_URL=" + e.releaseURL}
	return runScript(t, filepath.Join(repoRoot(t), "scripts/clued"), "", env, args...)
}

func goCache(t *testing.T) string {
	if v := os.Getenv("GOCACHE"); v != "" {
		return v
	}
	return filepath.Join(t.TempDir(), "gocache")
}

func TestShimPrefersMindPalaceOnPathWhenNoReleaseIsReachable(t *testing.T) {
	e := newShimEnv(t)
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	e.writeDownloaded(t, "downloaded")
	r := e.run(t, "", "session", "daemon", "--addr", ":1")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "path session daemon --addr :1" {
		t.Errorf("got %+v", r)
	}
}

func TestShimFallsBackToAManuallyInstalledBinary(t *testing.T) {
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
	if !strings.Contains(r.stderr, "mind-palace") || !strings.Contains(r.stderr, "/clued-setup") || !strings.Contains(r.stderr, "v"+pluginVersion(t)) {
		t.Errorf("stderr should name the binary, /clued-setup and the pinned release: %q", r.stderr)
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

func TestShimDownloadsThePinnedReleaseOnceAndVerifiesIt(t *testing.T) {
	e := newShimEnv(t)
	rel := pinnedRelease(t, "release")
	e.releaseURL = rel.url
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	e.writeDownloaded(t, "downloaded")

	r := e.run(t, "", "session", "mcp")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "release session mcp" {
		t.Fatalf("the pinned release should win over PATH and the manual install: %+v", r)
	}
	if !strings.Contains(r.stderr, "downloading mind-palace v"+pluginVersion(t)) {
		t.Errorf("stderr should announce the download: %q", r.stderr)
	}
	if info, err := os.Stat(e.releaseBinary(pluginVersion(t))); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("downloaded binary: err = %v, mode = %v, want an executable", err, info)
	}
	if n := rel.hits.Load(); n != 2 {
		t.Errorf("server saw %d requests, want 2 (SHA256SUMS and the asset)", n)
	}

	// The download happens once: later runs use the installed copy.
	r = e.run(t, "", "again")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "release again" || r.stderr != "" {
		t.Errorf("second run should use the installed copy silently: %+v", r)
	}
	if n := rel.hits.Load(); n != 2 {
		t.Errorf("server saw %d requests after the second run, want still 2", n)
	}
}

func TestShimLocatePrintsTheResolvedBinary(t *testing.T) {
	e := newShimEnv(t)
	e.releaseURL = pinnedRelease(t, "release").url
	r := e.run(t, "", "--locate")
	if r.code != 0 || strings.TrimSpace(r.stdout) != e.releaseBinary(pluginVersion(t)) {
		t.Errorf("got %+v, want the downloaded path", r)
	}
	empty := newShimEnv(t)
	r = empty.run(t, "", "--locate")
	if r.code != 1 || r.stdout != "" {
		t.Errorf("nothing to locate: got %+v", r)
	}
}

func TestShimReplacesTheCopyOfAnOlderRelease(t *testing.T) {
	e := newShimEnv(t)
	e.releaseURL = pinnedRelease(t, "new").url
	old := e.releaseBinary("0.0.0")
	if err := os.MkdirAll(filepath.Dir(old), 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Dir(old), "mind-palace", strings.Replace(fakeBinary, "%s", "old", 1))
	r := e.run(t, "", "y")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "new y" {
		t.Fatalf("the pinned release should be downloaded: %+v", r)
	}
	if _, err := os.Stat(filepath.Dir(old)); !os.IsNotExist(err) {
		t.Errorf("the older release should be removed, stat err = %v", err)
	}
}

func TestShimRefusesAReleaseBinaryWithWrongHash(t *testing.T) {
	e := newShimEnv(t)
	rel := newReleaseServer(t)
	rel.publish(t, pluginVersion(t), "release", strings.Repeat("ab", 32))
	e.releaseURL = rel.url
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	r := e.run(t, "", "z")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "path z" {
		t.Errorf("should fall back to PATH: %+v", r)
	}
	if !strings.Contains(r.stderr, "SHA256SUMS") {
		t.Errorf("stderr should explain the mismatch: %q", r.stderr)
	}
	if _, err := os.Stat(e.releaseBinary(pluginVersion(t))); !os.IsNotExist(err) {
		t.Errorf("mismatched binary must not be kept, stat err = %v", err)
	}
}

func TestShimDoesNotRetryAFailedDownloadOnEveryHook(t *testing.T) {
	e := newShimEnv(t)
	rel := newReleaseServer(t) // publishes nothing: every request is a 404
	e.releaseURL = rel.url
	writeExecutable(t, e.pathDir, "mind-palace", strings.Replace(fakeBinary, "%s", "path", 1))
	r := e.run(t, "", "a")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "path a" {
		t.Fatalf("should fall back to PATH: %+v", r)
	}
	if !strings.Contains(r.stderr, "v"+pluginVersion(t)) || !strings.Contains(r.stderr, "SHA256SUMS") {
		t.Errorf("stderr should say which release could not be fetched: %q", r.stderr)
	}
	first := rel.hits.Load()
	if first == 0 {
		t.Fatal("the server saw no request")
	}
	r = e.run(t, "", "b")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "path b" || r.stderr != "" {
		t.Errorf("the second run should fall back silently: %+v", r)
	}
	if n := rel.hits.Load(); n != first {
		t.Errorf("the failed download was retried at once: %d requests, want %d", n, first)
	}
	// Once the retry delay has passed the download is attempted again.
	failed := filepath.Join(filepath.Dir(e.releaseBinary(pluginVersion(t))), ".download-failed")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(failed, old, old); err != nil {
		t.Fatal(err)
	}
	rel.publish(t, pluginVersion(t), "release", "")
	r = e.run(t, "", "c")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "release c" {
		t.Errorf("the download should be retried after the delay: %+v", r)
	}
}

func TestShimConcurrentHooksDownloadOnce(t *testing.T) {
	e := newShimEnv(t)
	rel := pinnedRelease(t, "release")
	e.releaseURL = rel.url
	var wg sync.WaitGroup
	results := make([]result, 4)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i] = e.run(t, "", "n", fmt.Sprint(i))
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.code != 0 || strings.TrimSpace(r.stdout) != fmt.Sprintf("release n %d", i) {
			t.Errorf("run %d: %+v", i, r)
		}
	}
	if n := rel.hits.Load(); n != 2 {
		t.Errorf("server saw %d requests, want 2: one hook downloads, the others wait for it", n)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(e.releaseBinary(pluginVersion(t))), ".lock")); !os.IsNotExist(err) {
		t.Errorf("the lock should be released, stat err = %v", err)
	}
}

func TestShimDevBuildOutranksTheRelease(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not installed")
	}
	e := newShimEnv(t)
	rel := pinnedRelease(t, "release")
	e.releaseURL = rel.url
	e.writeConfig(t, `{"devRepoPath":"`+fakeDevRepo(t, "dev")+`"}`)
	r := e.run(t, filepath.Dir(goBin), "x")
	if r.code != 0 || strings.TrimSpace(r.stdout) != "dev x" {
		t.Errorf("got %+v, want the dev build to win over the release", r)
	}
	if n := rel.hits.Load(); n != 0 {
		t.Errorf("a developer's shim should not download: %d requests", n)
	}
}

// TestPluginManifestPinsARelease checks that .claude-plugin/plugin.json
// names the release scripts/clued downloads and that the shim, without an
// override, fetches it from this repository's GitHub releases.
func TestPluginManifestPinsARelease(t *testing.T) {
	version := pluginVersion(t)
	parts := strings.Split(version, ".")
	if len(parts) != 3 {
		t.Errorf("version %q is not MAJOR.MINOR.PATCH", version)
	}
	for _, p := range parts {
		if p == "" || strings.Trim(p, "0123456789") != "" {
			t.Errorf("version %q is not MAJOR.MINOR.PATCH", version)
		}
	}
	shim, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "clued"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(shim), "https://github.com/robbiebyrd/clued/releases/download") {
		t.Error("scripts/clued should download from this repository's GitHub releases by default")
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "build-binaries.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64", "linux/arm", "windows/amd64", "windows/arm64"} {
		if !strings.Contains(string(script), "\n  "+target+"\n") {
			t.Errorf("scripts/build-binaries.sh does not build %s", target)
		}
	}
}
