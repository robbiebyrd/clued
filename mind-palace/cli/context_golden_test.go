package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateGolden = flag.Bool("update", false, "rewrite the context golden files")

// goldenCases are probe invocations over testdata/context-repo whose answers
// do not depend on the machine: no git (the fixture sits inside this
// repository), no PATH lookups, no absolute paths.
var goldenCases = []struct {
	name string
	args []string
}{
	{"file-exists-yes", []string{"file-exists", "README.md"}},
	{"file-exists-dir", []string{"file-exists", "docs"}},
	{"file-exists-missing", []string{"file-exists", "SPEC.md"}},
	{"dir-exists-yes", []string{"dir-exists", "docs/sub"}},
	{"dir-exists-no", []string{"dir-exists", "README.md"}},
	{"count-files-md", []string{"count-files", "**/*.md"}},
	{"count-files-two-globs", []string{"count-files", "*.md", "pkg/*.go"}},
	{"list-files-docs", []string{"list-files", "docs/**/*.md"}},
	{"list-files-limit", []string{"list-files", "--limit", "1", "**/*.go"}},
	{"list-files-none", []string{"list-files", "nothing/*.zz"}},
	{"file-head-two", []string{"file-head", "README.md", "--lines", "2"}},
	{"file-head-default", []string{"file-head", "README.md"}},
	{"file-head-no-newline", []string{"file-head", "notes.txt"}},
	{"file-head-missing", []string{"file-head", "docs/SPEC.md"}},
	{"count-lines", []string{"count-lines", "README.md"}},
	{"count-lines-no-newline", []string{"count-lines", "notes.txt"}},
	{"count-lines-missing", []string{"count-lines", "missing.txt"}},
	{"find-files-go", []string{"find-files", "--name", "*.go"}},
	{"find-files-count", []string{"find-files", "--name", "*.go", "--count"}},
	{"find-files-depth", []string{"find-files", "--name", "*.md", "--maxdepth", "1"}},
	{"find-files-dir", []string{"find-files", "docs", "--name", "*.md"}},
	{"find-files-two-names", []string{"find-files", "--name", "*_test.go", "--name", "*.txt"}},
	{"find-files-none", []string{"find-files", "--name", "*.zz"}},
	{"count-files-with-content-hit", []string{"count-files-with-content", "stories/*.md", "TODO"}},
	{"count-files-with-content-miss", []string{"count-files-with-content", "stories/*.md", "NEVER"}},
	{"count-files-with-content-bad-regexp", []string{"count-files-with-content", "stories/*.md", "("}},
	{"json-field-string", []string{"json-field", "package.json", ".name"}},
	{"json-field-number", []string{"json-field", "package.json", "version"}},
	{"json-field-bool", []string{"json-field", "package.json", "private"}},
	{"json-field-object", []string{"json-field", "package.json", "scripts"}},
	{"json-field-nested", []string{"json-field", "package.json", "scripts.test"}},
	{"json-field-missing", []string{"json-field", "package.json", "nothing"}},
	{"json-field-no-file", []string{"json-field", "missing.json", "name"}},
	{"json-count-array", []string{"json-count", "package.json", "deps"}},
	{"json-count-object", []string{"json-count", "package.json", "scripts"}},
	{"json-count-scalar", []string{"json-count", "package.json", "name"}},
	{"json-count-where", []string{"json-count-where", "package.json", "deps", "kind", "dev"}},
	{"json-count-where-none", []string{"json-count-where", "package.json", "deps", "kind", "test"}},
	{"json-count-with-key", []string{"json-count-with-key", "package.json", "kind"}},
	{"json-count-with-key-absent", []string{"json-count-with-key", "package.json", "zzz"}},
	{"mcp-configured-yes", []string{"mcp-configured", "clued"}},
	{"mcp-configured-no", []string{"mcp-configured", "no-such-server"}},
	{"detect-stack", []string{"detect-stack"}},
	{"detect-coverage", []string{"detect-coverage"}},
	{"detect-frontend", []string{"detect-frontend"}},
	{"detect-css-framework", []string{"detect-css-framework"}},
	{"detect-port-none", []string{"detect-port"}},
	{"command-exists-missing", []string{"command-exists", "definitely-not-a-command-xyz"}},
	{"git-refused-push", []string{"git", "push", "origin", "main"}},
	{"git-refused-output", []string{"git", "log", "--output=/tmp/x"}},
	{"git-empty", []string{"git"}},
	{"gh-refused-merge", []string{"gh", "pr", "merge", "1"}},
	{"gh-refused-api-post", []string{"gh", "api", "-X", "POST", "repos/o/r/issues"}},
	{"unknown-subcommand", []string{"nonsense"}},
	{"missing-argument", []string{"json-field", "package.json"}},
}

func TestContextGolden(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "testdata", "context-repo"))
	if err != nil {
		t.Fatal(err)
	}
	expectedDir := filepath.Join("..", "testdata", "context-expected")
	// mcp-configured also searches the home directory: keep it empty.
	t.Setenv("HOME", t.TempDir())
	if *updateGolden {
		if err := os.MkdirAll(expectedDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, c := range goldenCases {
		if seen[c.name] {
			t.Fatalf("duplicate golden case %q", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			r := runContext(t, root, c.args...)
			if r.code != 0 {
				t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
			}
			path := filepath.Join(expectedDir, c.name+".txt")
			if *updateGolden {
				if err := os.WriteFile(path, []byte(r.stdout), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("no golden file (run with -update): %v", err)
			}
			if r.stdout != string(want) {
				t.Errorf("%v =\n%q\nwant\n%q", c.args, r.stdout, string(want))
			}
		})
	}
	if !*updateGolden {
		entries, err := os.ReadDir(expectedDir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if filepath.Ext(name) == ".txt" && !seen[name[:len(name)-4]] {
				t.Errorf("stale golden file %s has no case", name)
			}
		}
	}
}
