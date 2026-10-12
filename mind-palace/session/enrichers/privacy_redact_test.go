package enrichers

import (
	"context"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func privacyRedactOf(t *testing.T, line any) string {
	t.Helper()
	r, err := PrivacyRedact.Enrich(context.Background(), session.Doc{"line": line}, nil)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return r.(privacyRedactResult).RedactedLine
}

func TestPrivacyRedactMetadata(t *testing.T) {
	if PrivacyRedact.Name != "privacy-redact" || PrivacyRedact.Collection != "transcript_lines" {
		t.Fatalf("name/collection = %q/%q", PrivacyRedact.Name, PrivacyRedact.Collection)
	}
	if PrivacyRedact.Enabled || PrivacyRedact.BatchLimit != 0 {
		t.Fatalf("enabled/batch = %v/%d", PrivacyRedact.Enabled, PrivacyRedact.BatchLimit)
	}
}

func TestPrivacyRedactMatchesDocsWithALine(t *testing.T) {
	if !PrivacyRedact.Matches(session.Doc{"line": "hello"}) {
		t.Error("string line should match")
	}
	if PrivacyRedact.Matches(session.Doc{"line": nil}) {
		t.Error("nil line should not match")
	}
	if PrivacyRedact.Matches(session.Doc{}) {
		t.Error("missing line should not match")
	}
}

func TestPrivacyRedactAPIKey(t *testing.T) {
	got := privacyRedactOf(t, "token=sk-abc123DEF456ghi789JKL012")
	if !strings.Contains(got, "[REDACTED:api-key]") || strings.Contains(got, "sk-abc123") {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactEmail(t *testing.T) {
	if got := privacyRedactOf(t, "contact me at user@example.com please"); got != "contact me at [REDACTED:email] please" {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactEmailUpperCaseTLD(t *testing.T) {
	if got := privacyRedactOf(t, "USER@EXAMPLE.COM"); got != "[REDACTED:email]" {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactEmailCaseFoldingStaysASCII(t *testing.T) {
	// JavaScript's i flag does not fold U+212A (Kelvin sign) to K, so a TLD
	// that needs it to reach two letters is not an email.
	line := "x@y.c\u212ac"
	if got := privacyRedactOf(t, line); got != line {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactAWSKey(t *testing.T) {
	if got := privacyRedactOf(t, "key AKIAIOSFODNN7EXAMPLE end"); got != "key [REDACTED:aws-key] end" {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	if got := privacyRedactOf(t, "auth: "+jwt); got != "auth: [REDACTED:jwt]" {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactJWTWordBoundaries(t *testing.T) {
	// \b needs a word/non-word transition: a JWT glued to a preceding word
	// character has no boundary before "eyJ", so it is left alone.
	line := "xeyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abc_def"
	if got := privacyRedactOf(t, line); strings.Contains(got, "[REDACTED:jwt]") {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactDoesNotMutateDoc(t *testing.T) {
	doc := session.Doc{"line": "email: user@example.com"}
	if _, err := PrivacyRedact.Enrich(context.Background(), doc, nil); err != nil {
		t.Fatal(err)
	}
	if doc["line"] != "email: user@example.com" {
		t.Fatalf("line mutated: %v", doc["line"])
	}
}

func TestPrivacyRedactObjectLineIsJSONEncodedFirst(t *testing.T) {
	got := privacyRedactOf(t, map[string]any{"text": "sk-abc123DEF456ghi789JKL012"})
	if got != `{"text":"[REDACTED:api-key]"}` {
		t.Fatalf("got %q", got)
	}
}

func TestPrivacyRedactJSONEncodingMatchesJavaScript(t *testing.T) {
	// JSON.stringify leaves <, > and & unescaped and adds no trailing newline.
	if got := privacyRedactOf(t, map[string]any{"a": "<b>&"}); got != `{"a":"<b>&"}` {
		t.Fatalf("got %q", got)
	}
}
