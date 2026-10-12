package cli

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// runContext runs `mind-palace context --root <root> <args...>` with the
// session config pointed at a missing file.
func runContext(t *testing.T, root string, args ...string) run {
	t.Helper()
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
	cmd := app.Root()
	cmd.SetArgs(append([]string{"context", "--config", filepath.Join(t.TempDir(), "none.json"), "--root", root}, args...))
	err := cmd.Execute()
	app.close()
	code := 0
	if err != nil {
		code = ExitCode(err)
	}
	return run{code: code, stdout: out.String(), stderr: errb.String()}
}

func answerOf(t *testing.T, r run) string {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d, stderr %q", r.code, r.stderr)
	}
	if !strings.HasSuffix(r.stdout, "\n") {
		t.Fatalf("answer must end with one newline: %q", r.stdout)
	}
	return strings.TrimSuffix(r.stdout, "\n")
}

func contextTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"README.md":          "# Title\nline two\nline three\n",
		"package.json":       `{"name":"demo","version":"1.0","scripts":{"test":"go test","build":"go build"},"deps":[{"name":"a","kind":"dev"},{"name":"b","kind":"prod"}],"devDependencies":{"tailwindcss":"3"}}` + "\n",
		"go.mod":             "module example.com/demo\n",
		".mcp.json":          `{"clued":{"command":"x"}}` + "\n",
		"docs/a.md":          "a\n",
		"docs/sub/b.md":      "b\n",
		"pkg/x.go":           "package pkg\n",
		"pkg/x_test.go":      "package pkg\n",
		".hidden/secret.go":  "package hidden\n",
		"stories/0001-x.txt": "TODO something\n",
	}
	for name, content := range files {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestContextFilesystemProbes(t *testing.T) {
	root := contextTree(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"file-exists", "README.md"}, "yes"},
		{[]string{"file-exists", "docs"}, "no"},
		{[]string{"file-exists", "missing.md"}, "no"},
		{[]string{"dir-exists", "docs"}, "yes"},
		{[]string{"dir-exists", "README.md"}, "no"},
		{[]string{"count-files", "docs/**/*.md"}, "2"},
		{[]string{"count-files", "*.md", "pkg/*.go"}, "3"},
		{[]string{"list-files", "docs/**/*.md"}, "docs/a.md\ndocs/sub/b.md"},
		{[]string{"list-files", "nothing/*.zz"}, "none"},
		{[]string{"list-files", "--limit", "2", "**/*.go"}, "pkg/x.go\npkg/x_test.go"},
		{[]string{"file-head", "README.md", "--lines", "2"}, "# Title\nline two"},
		{[]string{"file-head", "README.md"}, "# Title\nline two\nline three"},
		{[]string{"file-head", "nope/SPEC.md"}, "no SPEC.md"},
		{[]string{"count-lines", "README.md"}, "3"},
		{[]string{"count-lines", "missing"}, "0"},
		{[]string{"find-files", "--name", "*.go", "--count"}, "2"},
		{[]string{"find-files", "--name", "*.go"}, "pkg/x.go\npkg/x_test.go"},
		{[]string{"find-files", "--name", "*_test.go", "--name", "*.md", "--maxdepth", "1"}, "README.md"},
		{[]string{"find-files", "docs", "--name", "*.md"}, "docs/a.md\ndocs/sub/b.md"},
		{[]string{"find-files", "--name", "*.zz"}, "none"},
		{[]string{"count-files-with-content", "stories/*.txt", "TODO"}, "1"},
		{[]string{"count-files-with-content", "stories/*.txt", "DONE"}, "0"},
		{[]string{"pwd"}, root},
	}
	for _, c := range cases {
		if got := answerOf(t, runContext(t, root, c.args...)); got != c.want {
			t.Errorf("%v = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestContextJSONAndEnvProbes(t *testing.T) {
	root := contextTree(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"json-field", "package.json", ".name"}, "demo"},
		{[]string{"json-field", "package.json", "version"}, "1.0"},
		{[]string{"json-field", "package.json", "scripts"}, `{"build":"go build","test":"go test"}`},
		{[]string{"json-field", "package.json", "missing"}, "none"},
		{[]string{"json-count", "package.json", "deps"}, "2"},
		{[]string{"json-count", "package.json", "scripts"}, "2"},
		{[]string{"json-count-where", "package.json", "deps", "kind", "dev"}, "1"},
		{[]string{"json-count-with-key", "package.json", "kind"}, "2"},
		{[]string{"mcp-configured", "clued"}, "configured"},
		{[]string{"mcp-configured", "nonexistent-server"}, "not configured"},
		{[]string{"command-exists", "definitely-not-a-command-xyz"}, ""},
		{[]string{"detect-stack"}, "Go TS"},
		{[]string{"detect-coverage"}, "none found"},
		{[]string{"detect-frontend"}, "yes"},
		{[]string{"detect-css-framework"}, "tailwindcss"},
		{[]string{"detect-port"}, "not detected"},
		{[]string{"project-path"}, session.EncodeProjectPath(filepath.ToSlash(root))},
	}
	for _, c := range cases {
		if got := answerOf(t, runContext(t, root, c.args...)); got != c.want {
			t.Errorf("%v = %q, want %q", c.args, got, c.want)
		}
	}
	if got := answerOf(t, runContext(t, root, "command-exists", "go")); got == "" {
		t.Error("command-exists go should print a path")
	}
	if got := answerOf(t, runContext(t, root, "detect-build-tool")); !strings.HasPrefix(got, "=== Node Project ===") {
		t.Errorf("detect-build-tool = %q", got)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := fmt.Sprint(ln.Addr().(*net.TCPAddr).Port)
	if got := answerOf(t, runContext(t, root, "detect-port", "1", port)); got != port {
		t.Errorf("detect-port = %q, want %q", got, port)
	}
}

func gitTree(t *testing.T) string {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := contextTree(t)
	git := func(args ...string) {
		t.Helper()
		cmd := osexec.Command("git", append([]string{"-C", root, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	git("add", ".")
	git("commit", "-q", "-m", "first")
	git("tag", "v1")
	git("commit", "-q", "--allow-empty", "-m", "second")
	git("remote", "add", "origin", "https://example.com/demo.git")
	return root
}

func TestContextGitProbes(t *testing.T) {
	root := gitTree(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"git-is-clean"}, "yes"},
		{[]string{"git-commits-since-tag"}, "1"},
		{[]string{"git-origin"}, "https://example.com/demo.git"},
		{[]string{"git-branch"}, "main"},
		{[]string{"git-default-branch"}, "main"},
		{[]string{"git", "push"}, "[FALLBACK: git push]"},
		{[]string{"git", "reset", "--hard"}, "[FALLBACK: git reset --hard]"},
		{[]string{"git", "log", "--output=x"}, "[FALLBACK: git log --output=x]"},
		{[]string{"gh", "pr", "merge", "1"}, "[FALLBACK: gh pr merge 1]"},
		{[]string{"gh", "repo", "delete", "x"}, "[FALLBACK: gh repo delete x]"},
	}
	for _, c := range cases {
		if got := answerOf(t, runContext(t, root, c.args...)); got != c.want {
			t.Errorf("%v = %q, want %q", c.args, got, c.want)
		}
	}
	log := answerOf(t, runContext(t, root, "git", "log", "--oneline", "--limit", "1"))
	if strings.Count(log, "\n") != 0 || !strings.HasSuffix(log, " second") {
		t.Errorf("git log --limit 1 = %q", log)
	}
	log = answerOf(t, runContext(t, root, "git", "log", "--oneline", "--limit=1"))
	if !strings.HasSuffix(log, " second") {
		t.Errorf("git log --limit=1 = %q", log)
	}
	// --root after the subcommand is honoured by the passthrough too.
	app := New()
	var out bytes.Buffer
	app.Stdout, app.Stderr = &out, &out
	cmd := app.Root()
	cmd.SetArgs([]string{"context", "git", "--root", root, "rev-parse", "--abbrev-ref", "HEAD"})
	if err := cmd.Execute(); err != nil || strings.TrimSpace(out.String()) != "main" {
		t.Errorf("git with --root after the subcommand: %v %q", err, out.String())
	}

	empty := t.TempDir()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"git", "log"}, "not a git repo"},
		{[]string{"git-is-clean"}, "no"},
		{[]string{"git-commits-since-tag"}, "unknown"},
		{[]string{"git-origin"}, "none"},
		{[]string{"git-branch"}, "none"},
		{[]string{"git-default-branch"}, "none"},
	} {
		if got := answerOf(t, runContext(t, empty, c.args...)); got != c.want {
			t.Errorf("outside a repo %v = %q, want %q", c.args, got, c.want)
		}
	}
}

func TestContextUnknownAndMissingArgsExitZero(t *testing.T) {
	root := t.TempDir()
	if got := answerOf(t, runContext(t, root, "nonsense")); got != "unknown subcommand: nonsense" {
		t.Errorf("unknown subcommand = %q", got)
	}
	if got := answerOf(t, runContext(t, root, "file-exists")); got != "[FALLBACK: file-exists]" {
		t.Errorf("missing argument = %q", got)
	}
	if got := answerOf(t, runContext(t, root, "git")); got != "[FALLBACK: git]" {
		t.Errorf("bare git = %q", got)
	}
	r := runContext(t, root)
	if r.code != 0 || !strings.Contains(r.stdout, "prime") {
		t.Errorf("bare context should print help and exit 0: %+v", r)
	}
}

func TestContextSessionProbes(t *testing.T) {
	root := gitTree(t)
	st := memsession.New()
	sessionEnv(t, st)
	cwd := filepath.ToSlash(root)
	ctx := context.Background()
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s-here", AccountID: testAccount, ProjectPath: cwd, Cwd: cwd, LastSeen: sessionT0}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertHookEvent(ctx, session.Doc{
		"session_id": "s-here", "account_id": testAccount, "tool_name": "Bash",
		"tool_input": map[string]any{"command": "go test ./..."}, "created_at": sessionT0,
	}); err != nil {
		t.Fatal(err)
	}

	got := answerOf(t, runContext(t, root, "session"))
	if !strings.HasPrefix(got, "session: s-here\n") || !strings.Contains(got, "commands:\n  go test ./...") {
		t.Errorf("session = %q", got)
	}
	if got := answerOf(t, runContext(t, t.TempDir(), "session")); got != "none" {
		t.Errorf("session elsewhere = %q", got)
	}
	if got := answerOf(t, runContext(t, root, "account")); got != testAccount {
		t.Errorf("account = %q", got)
	}
	if got := answerOf(t, runContext(t, root, "daemon-status")); !strings.HasPrefix(got, "not running (port ") {
		t.Errorf("daemon-status = %q", got)
	}
	host := answerOf(t, runContext(t, root, "host"))
	if len(strings.Fields(host)) < 3 {
		t.Errorf("host = %q", host)
	}
}

func TestContextPrime(t *testing.T) {
	root := gitTree(t)
	st := memsession.New()
	sessionEnv(t, st)
	cwd := filepath.ToSlash(root)
	if err := st.UpsertSession(context.Background(), session.Session{SessionID: "s-prime", AccountID: testAccount, Cwd: cwd, ProjectPath: cwd, LastSeen: sessionT0}); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	report := answerOf(t, runContext(t, root, "prime"))
	if time.Since(start) > 20*time.Second {
		t.Fatalf("prime took %v", time.Since(start))
	}
	for _, want := range []string{
		"=== ENV ===\ncwd: " + filepath.ToSlash(root),
		"git_origin: https://example.com/demo.git",
		"account_id: " + testAccount,
		"=== CLUED ===",
		"=== FILES ===\n--- README.md ---\n# Title",
		"=== BRANCH ===\nmain (clean)",
		"=== LOG:main ===",
		"=== LOG:HEAD ===",
		"=== DIFF-STAT ===\nno diff (on main)",
		"=== STORIES ===\nno plans dir",
		"=== PLANS ===\nno plans dir",
		"=== SESSION ===\nsession: s-prime",
		"=== BUILD ===\n=== Node Project ===",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("prime lacks %q:\n%s", want, report)
		}
	}
	if strings.Count(report, "\n\n=== ") != 10 {
		t.Errorf("prime should have 11 sections:\n%s", report)
	}

	report = answerOf(t, runContext(t, root, "prime", "--no-store", "--no-docs"))
	if !strings.Contains(report, "=== SESSION ===\nstore disabled") {
		t.Errorf("--no-store: %s", report)
	}

	// A plans directory makes the palace sources available.
	plans := filepath.Join(root, "docs", "plans")
	if err := os.MkdirAll(plans, 0o755); err != nil {
		t.Fatal(err)
	}
	wd, _ := os.Getwd()
	src, err := os.ReadFile(filepath.Join(wd, "..", "testdata", "create-impl.json"))
	if err != nil {
		t.Fatal(err)
	}
	r := exec(t, plans, "plan", "create", "--input", string(src))
	if r.code != 0 {
		t.Fatalf("plan create: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if r := exec(t, plans, "plan", "set-status", id, "validated"); r.code != 0 {
		t.Fatalf("set-status: %+v", r)
	}
	if r := exec(t, plans, "plan", "set-status", id, "ready"); r.code != 0 {
		t.Fatalf("set-status: %+v", r)
	}
	report = answerOf(t, runContext(t, root, "prime", "--no-store"))
	if !strings.Contains(report, "=== PLANS ===\n"+id+" [impl/ready]") {
		t.Errorf("PLANS should list the ready plan:\n%s", report)
	}
	if !strings.Contains(report, "=== STORIES ===\nnone") {
		t.Errorf("STORIES should be none:\n%s", report)
	}
}
