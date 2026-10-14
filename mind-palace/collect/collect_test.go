package collect

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not on PATH", name)
	}
}

func TestExecRunnerReturnsTrimmedStdout(t *testing.T) {
	requireTool(t, "sh")
	out, err := (ExecRunner{}).Run(context.Background(), "", "sh", "-c", "printf '  hello\\n\\n'")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "  hello" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestExecRunnerRunsInDir(t *testing.T) {
	requireTool(t, "sh")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := (ExecRunner{}).Run(context.Background(), dir, "sh", "-c", "ls")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "marker.txt" {
		t.Fatalf("stdout = %q", out)
	}
}

func TestExecRunnerKeepsPartialStdoutWithError(t *testing.T) {
	requireTool(t, "sh")
	out, err := (ExecRunner{}).Run(context.Background(), "", "sh", "-c", "echo partial; echo oops >&2; exit 3")
	if err == nil {
		t.Fatal("expected an error")
	}
	if out != "partial" {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Fatalf("error should carry stderr, got %v", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("error should wrap *exec.ExitError, got %T", err)
	}
}

func TestExecRunnerMissingCommand(t *testing.T) {
	_, err := (ExecRunner{}).Run(context.Background(), "", "definitely-not-a-command-xyz")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestExecRunnerHonoursTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no sleep on windows")
	}
	requireTool(t, "sleep")
	start := time.Now()
	_, err := (ExecRunner{Timeout: 100 * time.Millisecond}).Run(context.Background(), "", "sleep", "5")
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("took %v, the timeout did not fire", elapsed)
	}
}

func TestExecRunnerHonoursParentContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (ExecRunner{}).Run(ctx, "", "sh", "-c", "echo hi"); err == nil {
		t.Fatal("expected an error from a cancelled context")
	}
}

func TestReportString(t *testing.T) {
	r := Report{Sections: []Section{
		{Name: "ENV", Body: "cwd: /x\n"},
		{Name: "LOG:main", Body: "abc first\ndef second"},
		{Name: "EMPTY", Body: ""},
	}}
	want := "=== ENV ===\ncwd: /x\n\n=== LOG:main ===\nabc first\ndef second\n\n=== EMPTY ===\n"
	if got := r.String(); got != want {
		t.Fatalf("String() =\n%q\nwant\n%q", got, want)
	}
	var empty Report
	if empty.String() != "" {
		t.Fatal("an empty report renders as the empty string")
	}
}

func TestTruncate(t *testing.T) {
	s := "a\nb\nc\nd"
	cases := []struct {
		limit int
		want  string
	}{
		{0, s}, {-1, s}, {10, s}, {2, "a\nb"}, {1, "a"},
	}
	for _, c := range cases {
		if got := Truncate(s, c.limit); got != c.want {
			t.Errorf("Truncate(limit=%d) = %q, want %q", c.limit, got, c.want)
		}
	}
	if got := Truncate("", 3); got != "" {
		t.Errorf("Truncate of empty = %q", got)
	}
}

func TestParseLimit(t *testing.T) {
	cases := map[string]int{"": 0, "x": 0, "-5": 0, "0": 0, "7": 7, " 3 ": 3}
	for in, want := range cases {
		if got := ParseLimit(in); got != want {
			t.Errorf("ParseLimit(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := ExpandHome("~"); got != home {
		t.Errorf("ExpandHome(~) = %q", got)
	}
	if got := ExpandHome("~/x/y"); got != filepath.Join(home, "x", "y") {
		t.Errorf("ExpandHome(~/x/y) = %q", got)
	}
	if got := ExpandHome("~user/x"); got != "~user/x" {
		t.Errorf("ExpandHome(~user/x) = %q, must be untouched", got)
	}
	if got := ExpandHome("plain/path"); got != "plain/path" {
		t.Errorf("ExpandHome(plain/path) = %q", got)
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(t.TempDir(), "elsewhere")
	if got := Resolve(root, abs); got != abs {
		t.Errorf("absolute path changed: %q", got)
	}
	if got := Resolve(root, "a/b"); got != filepath.Join(root, "a", "b") {
		t.Errorf("relative path = %q", got)
	}
	if got := Resolve(root, ""); got != root {
		t.Errorf("empty path = %q, want root", got)
	}
	home, err := os.UserHomeDir()
	if err == nil {
		if got := Resolve(root, "~/z"); got != filepath.Join(home, "z") {
			t.Errorf("~ path = %q", got)
		}
	}
	cwd, _ := os.Getwd()
	if got := Resolve("", "rel"); got != filepath.Join(cwd, "rel") {
		t.Errorf("empty root resolves against the working directory, got %q", got)
	}
}

func TestDisplayPath(t *testing.T) {
	if got := DisplayPath(filepath.Join("a", "b", "c.txt")); got != "a/b/c.txt" {
		t.Errorf("DisplayPath = %q", got)
	}
	if got := DisplayPath(`x\y`); runtime.GOOS != "windows" && got != `x\y` {
		t.Errorf("DisplayPath on a non-windows backslash name = %q", got)
	}
}

func TestFallback(t *testing.T) {
	if got := Fallback("gh", []string{"pr", "merge"}); got != "[FALLBACK: gh pr merge]" {
		t.Errorf("Fallback = %q", got)
	}
	if got := Fallback("prime", nil); got != "[FALLBACK: prime]" {
		t.Errorf("Fallback without args = %q", got)
	}
}

func TestNoFile(t *testing.T) {
	if got := NoFile(filepath.Join("docs", "SPEC.md")); got != "no SPEC.md" {
		t.Errorf("NoFile = %q", got)
	}
}

func TestCappedTrimsTheLongestSectionsFirst(t *testing.T) {
	long := strings.Repeat("a line of the log\n", 200)
	r := Report{Sections: []Section{{Name: "BRANCH", Body: "main"}, {Name: "LOG", Body: long}, {Name: "STORIES", Body: "0001 ready"}}}
	if got := r.Capped(0); got != r.String() {
		t.Error("Capped(0) should not cut")
	}
	got := r.Capped(500)
	if len(got) > 500 {
		t.Fatalf("Capped(500) is %d bytes", len(got))
	}
	for _, want := range []string{"=== BRANCH ===\nmain", "=== LOG ===", Trimmed, "=== STORIES ===\n0001 ready"} {
		if !strings.Contains(got, want) {
			t.Errorf("capped report lacks %q:\n%s", want, got)
		}
	}
	if short := r.Capped(len(r.String())); short != r.String() {
		t.Error("a report that fits is cut")
	}
	if tiny := r.Capped(10); len(tiny) != 10 {
		t.Errorf("Capped(10) = %d bytes", len(tiny))
	}
}
