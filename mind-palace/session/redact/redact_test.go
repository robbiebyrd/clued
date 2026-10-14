package redact

import (
	"reflect"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	apiKey   = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123"
	awsKey   = "AKIAABCDEFGHIJKLMNOP"
	jwt      = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl"
	ghToken  = "ghp_0123456789abcdefghijklmnopqrstuvwxyzAB"
	slackTok = "xoxb-1234567890-abcdefghij"
	pemKey   = "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----"
)

func TestStringMasksEachRule(t *testing.T) {
	r, errs := New([]string{`internal-[0-9]{4}`})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	cases := []struct{ in, want, rule string }{
		{"key " + apiKey + " end", "key [REDACTED:api-key] end", "api-key"},
		{"aws " + awsKey, "aws [REDACTED:aws-key]", "aws-key"},
		{"token " + jwt, "token [REDACTED:jwt]", "jwt"},
		{"gh " + ghToken, "gh [REDACTED:github-token]", "github-token"},
		{"slack " + slackTok, "slack [REDACTED:slack-token]", "slack-token"},
		{"pem " + pemKey + " after", "pem [REDACTED:private-key] after", "private-key"},
		{"host internal-1234", "host [REDACTED:pattern-1]", "pattern-1"},
		{"keep <private>my diary</private> this", "keep [PRIVATE] this", "private"},
		{"a <private>one\nline two</private> b", "a [PRIVATE] b", "private"},
		{"unclosed <private>to the end\nnext line", "unclosed [PRIVATE]\nnext line", "private"},
	}
	for _, c := range cases {
		got, fired := r.String(c.in)
		if got != c.want || len(fired) != 1 || fired[0] != c.rule {
			t.Errorf("String(%q) = %q, %v; want %q, [%s]", c.in, got, fired, c.want, c.rule)
		}
	}
	if got, fired := r.String("me@example.com wrote plain text"); got != "me@example.com wrote plain text" || fired != nil {
		t.Errorf("clean text = %q, %v", got, fired)
	}
}

func TestNewSkipsABadPattern(t *testing.T) {
	r, errs := New([]string{"(", "ok-[0-9]+"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "redactPatterns[0]") {
		t.Fatalf("errs = %v", errs)
	}
	if got, _ := r.String("ok-42"); got != "[REDACTED:pattern-2]" {
		t.Errorf("the good pattern after a bad one = %q", got)
	}
}

func TestValueWalksMapsAndListsAndCopies(t *testing.T) {
	r, _ := New(nil)
	in := session.Doc{
		"tool_input": map[string]any{"command": "export KEY=" + apiKey, "n": 3.0},
		"list":       []any{"plain", awsKey, map[string]any{apiKey: "keys stay"}},
		"cwd":        "/repo",
	}
	out, fired := r.Value(in)
	want := session.Doc{
		"tool_input": map[string]any{"command": "export KEY=[REDACTED:api-key]", "n": 3.0},
		"list":       []any{"plain", "[REDACTED:aws-key]", map[string]any{apiKey: "keys stay"}},
		"cwd":        "/repo",
	}
	if !reflect.DeepEqual(out, want) || !reflect.DeepEqual(fired, []string{"api-key", "aws-key"}) {
		t.Errorf("Value = %#v, %v", out, fired)
	}
	if in["tool_input"].(map[string]any)["command"] != "export KEY="+apiKey {
		t.Error("Value changed its input")
	}
	clean := map[string]any{"a": "b"}
	if got, fired := r.Value(clean); fired != nil || !reflect.DeepEqual(got, clean) {
		t.Errorf("clean value = %v, %v", got, fired)
	}
}

func TestPathsExcludeADirectoryAndBelow(t *testing.T) {
	p, errs := NewPaths([]string{"/work/secret-*", "/clients/acme", "[", " "})
	if len(errs) != 1 {
		t.Fatalf("errs = %v", errs)
	}
	for path, want := range map[string]bool{
		"/work/secret-repo":         true,
		"/work/secret-repo/sub/dir": true,
		"/clients/acme":             true,
		"/clients/acme/app":         true,
		"/clients/acme-other":       false,
		"/work/open":                false,
		"":                          false,
	} {
		if got := p.Excludes(path); got != want {
			t.Errorf("Excludes(%q) = %v, want %v", path, got, want)
		}
	}
	home, _ := NewPaths([]string{"~/private"})
	if len(home) != 1 || strings.HasPrefix(home[0], "~") {
		t.Errorf("~ not expanded: %v", home)
	}
}
