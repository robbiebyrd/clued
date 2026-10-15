package plugin_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
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
