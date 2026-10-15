package plugin_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// apacheLicenseSHA256 is the sha256 of the verbatim Apache-2.0 text at
// https://www.apache.org/licenses/LICENSE-2.0.txt (202 lines).
const apacheLicenseSHA256 = "cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30"

// readPair reads name at the repository root and in mind-palace/ and fails
// unless both exist and are byte-equal.
func readPair(t *testing.T, name string) []byte {
	t.Helper()
	root := repoRoot(t)
	top, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatalf("root %s: %v", name, err)
	}
	mod, err := os.ReadFile(filepath.Join(root, "mind-palace", name))
	if err != nil {
		t.Fatalf("mind-palace/%s: %v", name, err)
	}
	if !bytes.Equal(top, mod) {
		t.Fatalf("%s and mind-palace/%s differ", name, name)
	}
	return top
}

// TestLicenseFilesMatch checks that the Apache-2.0 licence is the verbatim
// text and that the Go module carries a byte-identical copy (a module zip
// only holds files under the module directory).
func TestLicenseFilesMatch(t *testing.T) {
	b := readPair(t, "LICENSE")
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != apacheLicenseSHA256 {
		t.Fatalf("LICENSE sha256 = %s, want %s (the verbatim Apache-2.0 text)", got, apacheLicenseSHA256)
	}
}

func TestNoticeFiles(t *testing.T) {
	b := readPair(t, "NOTICE")
	if !strings.Contains(string(b), "Copyright 2026 Space Hippo, LC") {
		t.Fatalf("NOTICE lacks the copyright line:\n%s", b)
	}
}

// TestManifestLicense checks the SPDX id in the plugin manifest and in the
// marketplace entry.
func TestManifestLicense(t *testing.T) {
	root := repoRoot(t)
	var plugin struct {
		License string `json:"license"`
	}
	readJSON(t, filepath.Join(root, ".claude-plugin", "plugin.json"), &plugin)
	if plugin.License != "Apache-2.0" {
		t.Errorf("plugin.json license = %q, want Apache-2.0", plugin.License)
	}
	var market struct {
		Plugins []struct {
			License string `json:"license"`
		} `json:"plugins"`
	}
	readJSON(t, filepath.Join(root, ".claude-plugin", "marketplace.json"), &market)
	if len(market.Plugins) == 0 || market.Plugins[0].License != "Apache-2.0" {
		t.Errorf("marketplace.json plugins[0].license is not Apache-2.0: %+v", market.Plugins)
	}
}

// TestPluginValidateStrict runs `claude plugin validate --strict` on the
// marketplace and the plugin manifest when claude is installed (it needs
// no credentials; a throwaway CLAUDE_CONFIG_DIR keeps the user's untouched).
func TestPluginValidateStrict(t *testing.T) {
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed")
	}
	root := repoRoot(t)
	for _, target := range []string{root, filepath.Join(root, ".claude-plugin", "plugin.json")} {
		cmd := exec.Command(claude, "plugin", "validate", target, "--strict")
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+t.TempDir())
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("claude plugin validate %s --strict: %v\n%s", target, err, b)
		}
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

// TestServerImageLabels checks the OCI labels and the third-party licence
// tree of the server image.
func TestServerImageLabels(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot(t), "Dockerfile.server"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`org.opencontainers.image.licenses="Apache-2.0"`,
		`org.opencontainers.image.title="clued-server"`,
		`org.opencontainers.image.vendor="Space Hippo, LC"`,
		`/usr/share/licenses/clued-server/`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("Dockerfile.server lacks %s", want)
		}
	}
}

// TestBuildBinariesLicenses runs `build-binaries.sh --licenses`, which
// downloads go-licenses and the module's dependencies, so it is skipped
// with -short.
func TestBuildBinariesLicenses(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads go-licenses; skipped with -short")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	root := repoRoot(t)
	out := t.TempDir()
	cmd := exec.Command("/bin/bash", filepath.Join(root, "scripts", "build-binaries.sh"), "--licenses", out)
	cmd.Env = os.Environ()
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build-binaries.sh --licenses: %v\n%s", err, b)
	}
	tree := filepath.Join(out, "THIRD_PARTY_LICENSES")
	if _, err := os.Stat(filepath.Join(tree, "github.com", "robbiebyrd", "clued", "mind-palace", "LICENSE")); err != nil {
		t.Errorf("own licence missing: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(tree, "golang.org", "x", "*", "LICENSE"))
	if len(matches) == 0 {
		t.Errorf("no golang.org/x/*/LICENSE under %s", tree)
	}
	if _, err := os.Stat(filepath.Join(out, "THIRD_PARTY_LICENSES.tar.gz")); err != nil {
		t.Errorf("THIRD_PARTY_LICENSES.tar.gz missing: %v", err)
	}
}
