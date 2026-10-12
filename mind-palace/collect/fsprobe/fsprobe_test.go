package fsprobe

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// buildTree writes the fixture tree every test probes and returns its root.
func buildTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	var long strings.Builder
	for i := 1; i <= 15; i++ {
		long.WriteString("L" + strconv.Itoa(i) + "\n")
	}
	files := map[string]string{
		"README.md":                 "# Title\nline2\nline3\n",
		"main.go":                   "package main\n",
		"empty.txt":                 "",
		"nonl.txt":                  "a\nb\nc",
		"crlf.txt":                  "one\r\ntwo\r\n",
		"long.txt":                  long.String(),
		".dot.md":                   "dot\n",
		".hidden/secret.md":         "hidden\n",
		"stories/0001-pending-x.md": "status: pending\n",
		"stories/0002-ready-y.md":   "status: ready\n",
		"docs/a/b.md":               "b\n",
		"docs/c.md":                 "c\n",
		"pkg/x_test.go":             "package pkg\n",
		"pkg/deep/z.go":             "package deep\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestFileExists(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	if got := p.FileExists("README.md"); got != collect.Yes {
		t.Errorf("FileExists(README.md) = %q, want %q", got, collect.Yes)
	}
	if got := p.FileExists("missing.md"); got != collect.No {
		t.Errorf("FileExists(missing.md) = %q, want %q", got, collect.No)
	}
	if got := p.FileExists("docs"); got != collect.No {
		t.Errorf("FileExists(docs) = %q, want %q (a directory is not a file)", got, collect.No)
	}
	if got := p.FileExists("docs/a/b.md"); got != collect.Yes {
		t.Errorf("FileExists(docs/a/b.md) = %q, want %q", got, collect.Yes)
	}
}

func TestDirExists(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	if got := p.DirExists("docs"); got != collect.Yes {
		t.Errorf("DirExists(docs) = %q, want %q", got, collect.Yes)
	}
	if got := p.DirExists("README.md"); got != collect.No {
		t.Errorf("DirExists(README.md) = %q, want %q", got, collect.No)
	}
	if got := p.DirExists("nope"); got != collect.No {
		t.Errorf("DirExists(nope) = %q, want %q", got, collect.No)
	}
	if got := p.DirExists("."); got != collect.Yes {
		t.Errorf("DirExists(.) = %q, want %q", got, collect.Yes)
	}
}

func TestGlob(t *testing.T) {
	root := buildTree(t)
	cases := []struct {
		pattern string
		want    []string
	}{
		{"*.md", []string{"README.md"}},
		{"docs/**/*.md", []string{"docs/a/b.md", "docs/c.md"}},
		{"stories/*-pending-*.md", []string{"stories/0001-pending-x.md"}},
		{"**/*.go", []string{"main.go", "pkg/deep/z.go", "pkg/x_test.go"}},
		{"pkg/*.go", []string{"pkg/x_test.go"}},
		{"docs/c.md", []string{"docs/c.md"}},
		{"stories/000?-ready-y.md", []string{"stories/0002-ready-y.md"}},
		{"*/*", []string{"docs/c.md", "pkg/x_test.go", "stories/0001-pending-x.md", "stories/0002-ready-y.md"}},
		{"**/*.md", []string{"README.md", "docs/a/b.md", "docs/c.md", "stories/0001-pending-x.md", "stories/0002-ready-y.md"}},
		{".hidden/*", []string{".hidden/secret.md"}},
		{".*", []string{".dot.md"}},
		{"docs", nil},
		{"missing/*.md", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := Glob(root, c.pattern)
		if strings.Join(got, "\n") != strings.Join(c.want, "\n") {
			t.Errorf("Glob(%q) = %q, want %q", c.pattern, got, c.want)
		}
	}
}

func TestGlobAbsolutePattern(t *testing.T) {
	root := buildTree(t)
	got := Glob("", filepath.Join(root, "docs", "*.md"))
	want := []string{collect.DisplayPath(filepath.Join(root, "docs", "c.md"))}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("Glob(abs) = %q, want %q", got, want)
	}
	for _, g := range got {
		if !filepath.IsAbs(filepath.FromSlash(g)) {
			t.Errorf("absolute pattern returned a relative path %q", g)
		}
	}
}

func TestListFiles(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	if got, want := p.ListFiles([]string{"docs/**/*.md"}, 0), "docs/a/b.md\ndocs/c.md"; got != want {
		t.Errorf("ListFiles = %q, want %q", got, want)
	}
	if got := p.ListFiles([]string{"*.zzz"}, 0); got != collect.None {
		t.Errorf("ListFiles(no match) = %q, want %q", got, collect.None)
	}
	if got := p.ListFiles(nil, 0); got != collect.None {
		t.Errorf("ListFiles(nil) = %q, want %q", got, collect.None)
	}
	if got, want := p.ListFiles([]string{"stories/*.md"}, 1), "stories/0001-pending-x.md"; got != want {
		t.Errorf("ListFiles(limit 1) = %q, want %q", got, want)
	}
	// Several globs merge in order without duplicates.
	got := p.ListFiles([]string{"stories/*.md", "*.md", "README.md"}, 0)
	want := "stories/0001-pending-x.md\nstories/0002-ready-y.md\nREADME.md"
	if got != want {
		t.Errorf("ListFiles(merge) = %q, want %q", got, want)
	}
}

func TestCountFiles(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	if got := p.CountFiles([]string{"*.md", "**/*.md"}); got != "5" {
		t.Errorf("CountFiles(overlapping) = %q, want 5", got)
	}
	if got := p.CountFiles([]string{"*.zzz"}); got != collect.Zero {
		t.Errorf("CountFiles(none) = %q, want %q", got, collect.Zero)
	}
	if got := p.CountFiles(nil); got != collect.Zero {
		t.Errorf("CountFiles(nil) = %q, want %q", got, collect.Zero)
	}
}

func TestFileHead(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	if got, want := p.FileHead("README.md", 2), "# Title\nline2"; got != want {
		t.Errorf("FileHead(2) = %q, want %q", got, want)
	}
	if got, want := p.FileHead("README.md", 100), "# Title\nline2\nline3"; got != want {
		t.Errorf("FileHead(100) = %q, want %q", got, want)
	}
	got := p.FileHead("long.txt", 0)
	if n := len(strings.Split(got, "\n")); n != 10 {
		t.Errorf("FileHead(0) returned %d lines, want 10: %q", n, got)
	}
	if !strings.HasPrefix(got, "L1\n") || !strings.HasSuffix(got, "\nL10") {
		t.Errorf("FileHead(0) = %q", got)
	}
	if got, want := p.FileHead("missing.txt", 3), collect.NoFile("missing.txt"); got != want {
		t.Errorf("FileHead(missing) = %q, want %q", got, want)
	}
	if got, want := p.FileHead("docs", 3), collect.NoFile("docs"); got != want {
		t.Errorf("FileHead(dir) = %q, want %q", got, want)
	}
	if got, want := p.FileHead("nonl.txt", 10), "a\nb\nc"; got != want {
		t.Errorf("FileHead(nonl) = %q, want %q", got, want)
	}
	if got, want := p.FileHead("nonl.txt", 3), "a\nb\nc"; got != want {
		t.Errorf("FileHead(nonl, 3) = %q, want %q", got, want)
	}
	if got, want := p.FileHead("crlf.txt", 1), "one\r"; got != want {
		t.Errorf("FileHead(crlf) = %q, want %q", got, want)
	}
	if got := p.FileHead("empty.txt", 5); got != "" {
		t.Errorf("FileHead(empty) = %q, want empty", got)
	}
}

func TestCountLines(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	cases := map[string]string{
		"empty.txt":   "0",
		"README.md":   "3",
		"nonl.txt":    "2",
		"crlf.txt":    "2",
		"missing.txt": collect.Zero,
		"docs":        collect.Zero,
	}
	for path, want := range cases {
		if got := p.CountLines(path); got != want {
			t.Errorf("CountLines(%s) = %q, want %q", path, got, want)
		}
	}
}

func TestFindFiles(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	got := p.FindFiles("", []string{"*.go", "*_test.go"}, 0, 0, false)
	if want := "main.go\npkg/deep/z.go\npkg/x_test.go"; got != want {
		t.Errorf("FindFiles(two patterns) = %q, want %q", got, want)
	}
	if got, want := p.FindFiles(".", []string{"*.go"}, 1, 0, false), "main.go"; got != want {
		t.Errorf("FindFiles(maxDepth 1) = %q, want %q", got, want)
	}
	if got, want := p.FindFiles(".", []string{"*.go"}, 2, 0, false), "main.go\npkg/x_test.go"; got != want {
		t.Errorf("FindFiles(maxDepth 2) = %q, want %q", got, want)
	}
	if got := p.FindFiles(".", []string{"*.go"}, 0, 0, true); got != "3" {
		t.Errorf("FindFiles(countOnly) = %q, want 3", got)
	}
	if got := p.FindFiles(".", []string{"*.zzz"}, 0, 0, true); got != collect.Zero {
		t.Errorf("FindFiles(countOnly none) = %q, want %q", got, collect.Zero)
	}
	if got := p.FindFiles(".", []string{"secret.md"}, 0, 0, false); got != collect.None {
		t.Errorf("FindFiles(hidden dir) = %q, want %q", got, collect.None)
	}
	if got := p.FindFiles(".", []string{"*.zzz"}, 0, 0, false); got != collect.None {
		t.Errorf("FindFiles(none) = %q, want %q", got, collect.None)
	}
	if got, want := p.FindFiles(".", []string{"*.md"}, 1, 0, false), ".dot.md\nREADME.md"; got != want {
		t.Errorf("FindFiles(dot file) = %q, want %q", got, want)
	}
	if got, want := p.FindFiles(".", []string{"*.go"}, 0, 2, false), "main.go\npkg/deep/z.go"; got != want {
		t.Errorf("FindFiles(limit 2) = %q, want %q", got, want)
	}
	if got, want := p.FindFiles("docs", nil, 0, 0, false), "docs/a/b.md\ndocs/c.md"; got != want {
		t.Errorf("FindFiles(docs, no names) = %q, want %q", got, want)
	}
	if got := p.FindFiles("missing", nil, 0, 0, false); got != collect.None {
		t.Errorf("FindFiles(missing dir) = %q, want %q", got, collect.None)
	}
	if got, want := p.FindFiles("", nil, 1, 0, true), "7"; got != want {
		t.Errorf("FindFiles(all top-level) = %q, want %q", got, want)
	}
}

func TestCountFilesWithContent(t *testing.T) {
	p := Probe{Root: buildTree(t)}
	if got := p.CountFilesWithContent("stories/*.md", `status:\s+pending`); got != "1" {
		t.Errorf("CountFilesWithContent(match) = %q, want 1", got)
	}
	if got := p.CountFilesWithContent("**/*.md", `^status:`); got != "2" {
		t.Errorf("CountFilesWithContent(**) = %q, want 2", got)
	}
	if got := p.CountFilesWithContent("stories/*.md", `nomatch`); got != collect.Zero {
		t.Errorf("CountFilesWithContent(no match) = %q, want %q", got, collect.Zero)
	}
	if got := p.CountFilesWithContent("stories/*.md", `(`); got != collect.Zero {
		t.Errorf("CountFilesWithContent(bad regexp) = %q, want %q", got, collect.Zero)
	}
	if got := p.CountFilesWithContent("", `x`); got != collect.Zero {
		t.Errorf("CountFilesWithContent(empty glob) = %q, want %q", got, collect.Zero)
	}
	if got := p.CountFilesWithContent("*.md", ""); got != collect.Zero {
		t.Errorf("CountFilesWithContent(empty pattern) = %q, want %q", got, collect.Zero)
	}
}

func TestMatchName(t *testing.T) {
	cases := []struct {
		name, pattern string
		want          bool
	}{
		{"main.go", "*.go", true},
		{"main.go", "*.md", false},
		{"x_test.go", "*_test.go", true},
		{"main.go", "*_test.go", false},
		{"a.go", "?.go", true},
		{"ab.go", "?.go", false},
		{"README.md", "README.md", true},
		{"README.md", "README", false},
		{"README.md.bak", "README.md", false},
		{"a/b.go", "*.go", false},
		{"a/b.go", "**.go", true},
		{"0001-pending-x.md", "*-pending-*.md", true},
		{"0002-ready-y.md", "*-pending-*.md", false},
		{"a+b.txt", "a+b.txt", true},
		{"aab.txt", "a+b.txt", false},
		{"file.go", "file.g.", false},
		{"", "*", true},
		{".dot", ".*", true},
	}
	for _, c := range cases {
		if got := MatchName(c.name, c.pattern); got != c.want {
			t.Errorf("MatchName(%q, %q) = %v, want %v", c.name, c.pattern, got, c.want)
		}
	}
}

func TestPwd(t *testing.T) {
	root := buildTree(t)
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := (Probe{Root: root}).Pwd(); got != abs {
		t.Errorf("Pwd = %q, want %q", got, abs)
	}
	if got := (Probe{Root: root + string(filepath.Separator) + "docs" + string(filepath.Separator) + ".."}).Pwd(); got != abs {
		t.Errorf("Pwd(uncleaned) = %q, want %q", got, abs)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got := (Probe{}).Pwd(); got != wd {
		t.Errorf("Pwd(empty root) = %q, want %q", got, wd)
	}
}
