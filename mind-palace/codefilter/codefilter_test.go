package codefilter

import (
	"bytes"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session/redact"
)

func TestDecide(t *testing.T) {
	text := []byte("package main\n\nfunc main() {}\n")
	euro := []byte("price: 10 €\n")
	exclude, errs := NewExcludes([]string{"*.gen.go", "web/dist/", "/build/*.js", "testdata"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	paths, errs := redact.NewPaths([]string{"/work/repo/private", "/work/repo/*.local.go"})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	opts := Options{Root: "/work/repo", ExcludePaths: paths, Exclude: exclude}

	for _, tc := range []struct {
		name   string
		path   string
		size   int64
		head   []byte
		opts   Options
		want   Decision
		reason string
	}{
		{"plain text", "main.go", int64(len(text)), text, opts, Content, ""},
		{"utf-8 text", "docs/prices.txt", int64(len(euro)), euro, opts, Content, ""},
		{"empty file", "empty.txt", 0, nil, opts, Content, ""},

		{"empty path", "", 1, text, opts, Skip, ReasonInvalidPath},
		{"absolute path", "/etc/passwd", 1, text, opts, Skip, ReasonInvalidPath},
		{"escapes root", "../other/main.go", 1, text, opts, Skip, ReasonInvalidPath},
		{"escapes after clean", "a/../../b.go", 1, text, opts, Skip, ReasonInvalidPath},
		{"root itself", ".", 1, text, opts, Skip, ReasonInvalidPath},
		{"backslash", `a\b.go`, 1, text, opts, Skip, ReasonInvalidPath},

		{"codeSyncExclude basename at depth", "pkg/api/types.gen.go", 1, text, opts, Skip, "codeSyncExclude: *.gen.go"},
		{"codeSyncExclude directory", "web/dist/app.js", 1, text, opts, Skip, "codeSyncExclude: web/dist"},
		{"codeSyncExclude anchored", "build/out.js", 1, text, opts, Skip, "codeSyncExclude: build/*.js"},
		{"codeSyncExclude anchored not deeper", "src/build/out.js", 1, text, opts, Content, ""},
		{"codeSyncExclude named dir at depth", "pkg/x/testdata/in.txt", 1, text, opts, Skip, "codeSyncExclude: testdata"},
		{"excludePaths directory", "private/notes.md", 1, text, opts, Skip, "excludePaths: /work/repo/private"},
		{"excludePaths glob", "app.local.go", 1, text, opts, Skip, "excludePaths: /work/repo/*.local.go"},
		{"excludePaths needs root", "private/notes.md", int64(len(text)), text, Options{ExcludePaths: paths}, Content, ""},

		{"deny .env", ".env", 1, text, opts, Skip, "deny: .env"},
		{"deny nested .env", "services/api/.env", 1, text, opts, Skip, "deny: .env"},
		{"deny .env.local", ".env.local", 1, text, opts, Skip, "deny: .env.*"},
		{"deny pem", "certs/server.pem", 1, text, opts, Skip, "deny: *.pem"},
		{"deny key", "tls.key", 1, text, opts, Skip, "deny: *.key"},
		{"deny key upper case", "TLS.KEY", 1, text, opts, Skip, "deny: *.key"},
		{"deny p12", "a.p12", 1, text, opts, Skip, "deny: *.p12"},
		{"deny pfx", "a.pfx", 1, text, opts, Skip, "deny: *.pfx"},
		{"deny id_rsa", "home/.ssh/id_rsa", 1, text, opts, Skip, "deny: id_rsa*"},
		{"deny id_rsa.pub", "id_rsa.pub", 1, text, opts, Skip, "deny: id_rsa*"},
		{"deny id_ed25519", "id_ed25519", 1, text, opts, Skip, "deny: id_ed25519*"},
		{"deny kdbx", "vault.kdbx", 1, text, opts, Skip, "deny: *.kdbx"},
		{"deny npmrc", "frontend/.npmrc", 1, text, opts, Skip, "deny: .npmrc"},
		{"deny pypirc", ".pypirc", 1, text, opts, Skip, "deny: .pypirc"},
		{"deny netrc", ".netrc", 1, text, opts, Skip, "deny: .netrc"},
		{"deny credentials json", "gcp/credentials-prod.json", 1, text, opts, Skip, "deny: credentials*.json"},
		{"deny secret under config", "config/secrets.yml", 1, text, opts, Skip, "deny: " + denyUnderConfig},
		{"deny secret dir under config", "app/config/secret/db.yml", 1, text, opts, Skip, "deny: " + denyUnderConfig},
		{"deny secret deep under config", "config/prod/client_secret.txt", 1, text, opts, Skip, "deny: " + denyUnderConfig},
		{"secret outside config", "docs/secrets.md", int64(len(text)), text, opts, Content, ""},
		{"config named secret is the file", "config", int64(len(text)), text, opts, Content, ""},
		{"env-like but allowed", "environment.go", int64(len(text)), text, opts, Content, ""},
		{"credentials not json", "credentials.go", int64(len(text)), text, opts, Content, ""},
		{"deny wins over size", ".env", 1 << 30, nil, opts, Skip, "deny: .env"},

		{"over default cap", "big.txt", DefaultMaxFileBytes + 1, nil, opts, MetadataOnly, ReasonTooLarge},
		{"at default cap", "edge.txt", DefaultMaxFileBytes, bytes.Repeat([]byte("a"), int(DefaultMaxFileBytes)), opts, Content, ""},
		{"over custom cap", "main.go", 10, text[:10], Options{MaxFileBytes: 9}, MetadataOnly, ReasonTooLarge},
		{"under custom cap", "main.go", 10, text[:10], Options{MaxFileBytes: 10}, Content, ""},

		{"nul byte", "image.png", 8, []byte("\x89PNG\x00\x00\x00\x0d"), opts, MetadataOnly, ReasonBinary},
		{"nul byte late in sniff window", "data.bin", SniffBytes, append(bytes.Repeat([]byte("a"), SniffBytes-1), 0), opts, MetadataOnly, ReasonBinary},
		{"nul byte past sniff window", "late.txt", SniffBytes + 1, append(bytes.Repeat([]byte("a"), SniffBytes), 0), opts, Content, ""},
		{"invalid utf-8", "latin1.txt", 4, []byte("caf\xe9"), opts, MetadataOnly, ReasonNotUTF8},
		{"invalid utf-8 past the head checked in full", "late.txt", SniffBytes + 2, append(bytes.Repeat([]byte("a"), SniffBytes), 0xff, 'a'), opts, MetadataOnly, ReasonNotUTF8},
		{"rune cut off by a short head", "euro.txt", int64(len(euro)), euro[:len(euro)-2], opts, Content, ""},
		{"rune cut off in a complete file", "euro.txt", int64(len(euro) - 2), euro[:len(euro)-2], opts, MetadataOnly, ReasonNotUTF8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := Decide(tc.path, tc.size, tc.head, tc.opts)
			if got != tc.want || reason != tc.reason {
				t.Errorf("Decide(%q) = %v, %q; want %v, %q", tc.path, got, reason, tc.want, tc.reason)
			}
		})
	}
}

func TestDenyListIsTheFixedList(t *testing.T) {
	want := []string{".env", ".env.*", "*.pem", "*.key", "*.p12", "*.pfx", "id_rsa*", "id_ed25519*",
		"*.kdbx", ".npmrc", ".pypirc", ".netrc", "credentials*.json"}
	if strings.Join(denyNames, " ") != strings.Join(want, " ") {
		t.Errorf("denyNames = %v, want %v", denyNames, want)
	}
	// Options has nothing that turns the list off.
	if d, _ := Decide("id_rsa", 1, []byte("x"), Options{}); d != Skip {
		t.Errorf("deny list applied with zero Options: %v", d)
	}
}

func TestNewExcludes(t *testing.T) {
	e, errs := NewExcludes([]string{"vendor/", " ", "/", "[", "/gen/./x/../y", "*.pb.go"})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "codeSyncExclude[3]") {
		t.Fatalf("errs = %v", errs)
	}
	if got := strings.Join(e, " "); got != "vendor gen/y *.pb.go" {
		t.Errorf("Excludes = %q", got)
	}
	for rel, want := range map[string]string{
		"vendor/x/y.go":    "vendor",
		"a/vendor/x.go":    "vendor",
		"gen/y/z.txt":      "gen/y",
		"a/gen/y/z.txt":    "",
		"api/v1/api.pb.go": "*.pb.go",
		"vendored.go":      "",
	} {
		pat, ok := e.Match(rel)
		if pat != want || ok != (want != "") {
			t.Errorf("Match(%q) = %q, %v; want %q", rel, pat, ok, want)
		}
	}
	var none Excludes
	if _, ok := none.Match("a.go"); ok {
		t.Error("empty Excludes matched")
	}
}

func TestIsText(t *testing.T) {
	for _, tc := range []struct {
		in        []byte
		truncated bool
		want      bool
	}{
		{[]byte("hello"), false, true},
		{[]byte("h\x00llo"), false, false},
		{[]byte("€"), false, true},
		{[]byte("€")[:2], true, true},
		{[]byte("€")[:2], false, false},
		{[]byte("\xff"), true, false},
		{nil, false, true},
	} {
		if got := IsText(tc.in, tc.truncated); got != tc.want {
			t.Errorf("IsText(%q, %v) = %v", tc.in, tc.truncated, got)
		}
	}
}

func TestDecisionString(t *testing.T) {
	for d, want := range map[Decision]string{Skip: "skip", MetadataOnly: "metadata-only", Content: "content", Decision(9): "Decision(9)"} {
		if got := d.String(); got != want {
			t.Errorf("%d.String() = %q", int(d), got)
		}
	}
}

func TestRedactPathsMatchNamesThePattern(t *testing.T) {
	p, _ := redact.NewPaths([]string{"/a/b", "/x/*"})
	if pat, ok := p.Match("/x/y/z.go"); !ok || pat != "/x/*" {
		t.Errorf("Match = %q, %v", pat, ok)
	}
	if _, ok := p.Match("/a/bc"); ok {
		t.Error("/a/bc matched /a/b")
	}
	var m Matcher = p
	if _, ok := m.Match("/a/b/c"); !ok {
		t.Error("redact.Paths as Matcher did not match")
	}
}
