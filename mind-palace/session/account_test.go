package session

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const accountWarning = "clued: account ID unavailable — isolation is degraded"

// captureLog redirects the standard logger for the test and returns what it wrote.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	return &buf
}

func TestReadAccountIDFromValidFile(t *testing.T) {
	buf := captureLog(t)
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"lastKnownAccountUuid":"abc-123-uuid"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadAccountID(p); got != "abc-123-uuid" {
		t.Fatalf("ReadAccountID = %s", got)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected log output: %q", buf.String())
	}
}

func TestReadAccountIDUnknownAndWarns(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"absent file": func(t *testing.T) string { return filepath.Join(t.TempDir(), "nonexistent.json") },
		"unparseable": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "bad.json")
			os.WriteFile(p, []byte("not valid json {{{"), 0o644)
			return p
		},
		"missing key": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "nokey.json")
			os.WriteFile(p, []byte(`{"someOtherKey":"value"}`), 0o644)
			return p
		},
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			buf := captureLog(t)
			if got := ReadAccountID(mk(t)); got != "unknown" {
				t.Fatalf("ReadAccountID = %s, want unknown", got)
			}
			if !strings.Contains(buf.String(), accountWarning) {
				t.Fatalf("log = %q, want warning", buf.String())
			}
		})
	}
}
