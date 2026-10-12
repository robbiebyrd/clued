package gitprobe

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/collect"
)

// requireGit skips the test when git is absent and isolates it from the
// user's and the system's git configuration.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com",
		"commit", "-q", "--allow-empty", "-m", msg)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newRepo builds a repository on branch "main" with one commit of README.md.
func newRepo(t *testing.T) string {
	t.Helper()
	return newRepoOn(t, "main")
}

func newRepoOn(t *testing.T, branch string) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", branch)
	writeFile(t, dir, "README.md", "hello\n")
	runGit(t, dir, "add", "README.md")
	commit(t, dir, "init")
	return dir
}

// recordingRunner remembers every call and answers with a fixed script.
type recordingRunner struct {
	calls [][]string
	out   string
	err   error
}

func (r *recordingRunner) Run(_ context.Context, _ string, name string, args ...string) (string, error) {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.out, r.err
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		// shape
		{nil, false},
		{[]string{}, false},
		{[]string{"--version"}, false},
		{[]string{"-p", "log"}, false},
		// plain read-only subcommands
		{[]string{"log"}, true},
		{[]string{"log", "--oneline", "-n", "5"}, true},
		{[]string{"status", "--porcelain"}, true},
		{[]string{"diff", "main...HEAD", "--stat"}, true},
		{[]string{"show", "HEAD"}, true},
		{[]string{"rev-parse", "--verify", "--quiet", "refs/heads/main"}, true},
		{[]string{"describe", "--tags", "--abbrev=0"}, true},
		{[]string{"ls-files"}, true},
		{[]string{"blame", "README.md"}, true},
		{[]string{"shortlog", "-sn"}, true},
		{[]string{"rev-list", "--count", "HEAD"}, true},
		{[]string{"ls-tree", "HEAD"}, true},
		{[]string{"cat-file", "-p", "HEAD"}, true},
		{[]string{"name-rev", "HEAD"}, true},
		{[]string{"for-each-ref", "refs/heads"}, true},
		{[]string{"grep", "TODO"}, true},
		// reflog
		{[]string{"reflog"}, true},
		{[]string{"reflog", "show", "HEAD"}, true},
		{[]string{"reflog", "expire", "--all"}, false},
		{[]string{"reflog", "delete", "HEAD@{1}"}, false},
		// branch
		{[]string{"branch"}, true},
		{[]string{"branch", "--show-current"}, true},
		{[]string{"branch", "-a", "--list"}, true},
		{[]string{"branch", "-d", "feature"}, false},
		{[]string{"branch", "-D", "feature"}, false},
		{[]string{"branch", "-m", "old", "new"}, false},
		{[]string{"branch", "-M", "main"}, false},
		{[]string{"branch", "-c", "a", "b"}, false},
		{[]string{"branch", "-C", "a", "b"}, false},
		{[]string{"branch", "-u", "origin/main"}, false},
		{[]string{"branch", "-f", "main", "HEAD~1"}, false},
		{[]string{"branch", "--delete", "feature"}, false},
		{[]string{"branch", "--move", "a", "b"}, false},
		{[]string{"branch", "--copy", "a", "b"}, false},
		{[]string{"branch", "--force", "main"}, false},
		{[]string{"branch", "--set-upstream-to", "origin/main"}, false},
		{[]string{"branch", "--set-upstream-to=origin/main"}, false},
		{[]string{"branch", "--unset-upstream"}, false},
		{[]string{"branch", "--edit-description"}, false},
		{[]string{"branch", "--track", "x", "origin/x"}, false},
		{[]string{"branch", "--no-track", "x"}, false},
		{[]string{"branch", "--create-reflog", "x"}, false},
		// tag
		{[]string{"tag"}, true},
		{[]string{"tag", "-l"}, true},
		{[]string{"tag", "--list"}, true},
		{[]string{"tag", "-n"}, true},
		{[]string{"tag", "-n3"}, true},
		{[]string{"tag", "--sort=-creatordate"}, true},
		{[]string{"tag", "--contains=HEAD"}, true},
		{[]string{"tag", "--no-contains=HEAD"}, true},
		{[]string{"tag", "--points-at=HEAD"}, true},
		{[]string{"tag", "--merged=main", "--no-merged=dev"}, true},
		{[]string{"tag", "--format=%(refname)"}, true},
		{[]string{"tag", "--column", "--no-column"}, true},
		{[]string{"tag", "-i", "--ignore-case"}, true},
		{[]string{"tag", "v1.0.0"}, false},
		{[]string{"tag", "-a", "v1", "-m", "msg"}, false},
		{[]string{"tag", "-d", "v1"}, false},
		{[]string{"tag", "-l", "v1*"}, false},
		{[]string{"tag", "-f", "v1"}, false},
		// remote
		{[]string{"remote"}, true},
		{[]string{"remote", "-v"}, true},
		{[]string{"remote", "--verbose"}, true},
		{[]string{"remote", "show", "origin"}, true},
		{[]string{"remote", "get-url", "origin"}, true},
		{[]string{"remote", "add", "origin", "x"}, false},
		{[]string{"remote", "set-url", "origin", "x"}, false},
		{[]string{"remote", "remove", "origin"}, false},
		{[]string{"remote", "prune", "origin"}, false},
		// stash
		{[]string{"stash", "list"}, true},
		{[]string{"stash", "show", "-p", "stash@{0}"}, true},
		{[]string{"stash"}, false},
		{[]string{"stash", "push"}, false},
		{[]string{"stash", "pop"}, false},
		{[]string{"stash", "drop"}, false},
		// config
		{[]string{"config", "--get", "user.name"}, true},
		{[]string{"config", "--get-all", "remote.origin.fetch"}, true},
		{[]string{"config", "--get-regexp", "^remote"}, true},
		{[]string{"config", "--list"}, true},
		{[]string{"config", "-l"}, true},
		{[]string{"config"}, false},
		{[]string{"config", "user.name"}, false},
		{[]string{"config", "user.name", "x"}, false},
		{[]string{"config", "--unset", "user.name"}, false},
		{[]string{"config", "--global", "--get", "user.name"}, false},
		// symbolic-ref
		{[]string{"symbolic-ref", "HEAD"}, true},
		{[]string{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"}, true},
		{[]string{"symbolic-ref", "-q", "--short", "HEAD"}, true},
		{[]string{"symbolic-ref", "HEAD", "refs/heads/main"}, false},
		{[]string{"symbolic-ref", "--delete", "HEAD"}, false},
		{[]string{"symbolic-ref", "-d", "HEAD"}, false},
		// never
		{[]string{"push"}, false},
		{[]string{"push", "--dry-run"}, false},
		{[]string{"pull"}, false},
		{[]string{"fetch"}, false},
		{[]string{"commit", "-m", "x"}, false},
		{[]string{"reset", "--hard"}, false},
		{[]string{"checkout", "main"}, false},
		{[]string{"switch", "main"}, false},
		{[]string{"merge", "x"}, false},
		{[]string{"rebase", "x"}, false},
		{[]string{"clean", "-n"}, false},
		{[]string{"rm", "x"}, false},
		{[]string{"mv", "a", "b"}, false},
		{[]string{"add", "."}, false},
		{[]string{"restore", "x"}, false},
		{[]string{"worktree", "list"}, false},
		{[]string{"submodule", "status"}, false},
		{[]string{"gc"}, false},
		{[]string{"prune"}, false},
		{[]string{"filter-branch"}, false},
		{[]string{"update-ref", "HEAD", "x"}, false},
		{[]string{"notes", "list"}, false},
		{[]string{"am"}, false},
		{[]string{"apply", "x.patch"}, false},
		{[]string{"cherry-pick", "x"}, false},
		{[]string{"revert", "x"}, false},
		{[]string{"bisect", "start"}, false},
		{[]string{"init"}, false},
		{[]string{"clone", "x"}, false},
		{[]string{"LOG"}, false},
		// output and environment escapes on otherwise allowed subcommands
		{[]string{"log", "--output=x"}, false},
		{[]string{"log", "--output", "x"}, false},
		{[]string{"diff", "--output-indicator-new=+"}, false},
		{[]string{"show", "-o", "x"}, false},
		{[]string{"log", "--exec-path=/tmp"}, false},
		{[]string{"log", "--git-dir=/tmp"}, false},
		{[]string{"log", "--work-tree=/tmp"}, false},
		{[]string{"log", "-C", "/tmp"}, false},
		{[]string{"log", "-C/tmp"}, false},
		{[]string{"log", "-c", "core.pager=x"}, false},
		{[]string{"log", "-c"}, false},
		{[]string{"grep", "-o", "x"}, false},
		{[]string{"status", "--", "-o"}, false},
	}
	for _, c := range cases {
		if got := Allowed(c.args); got != c.want {
			t.Errorf("Allowed(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestGitTruncatesToLimit(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "second")
	out := Probe{Root: dir}.Git(context.Background(), []string{"log", "--oneline"}, 1)
	if strings.Count(out, "\n") != 0 || !strings.HasSuffix(out, " second") {
		t.Fatalf("Git log limited to 1 = %q", out)
	}
	full := Probe{Root: dir}.Git(context.Background(), []string{"log", "--oneline"}, 0)
	if strings.Count(full, "\n") != 1 {
		t.Fatalf("unlimited Git log = %q", full)
	}
}

func TestGitDisallowedReturnsFallbackWithoutRunning(t *testing.T) {
	cases := [][]string{
		{"push"},
		{"reset", "--hard"},
		{"log", "--output=x"},
		{},
	}
	for _, args := range cases {
		rec := &recordingRunner{out: "should not be seen"}
		got := Probe{Root: "/nowhere", Runner: rec}.Git(context.Background(), args, 0)
		if want := collect.Fallback("git", args); got != want {
			t.Errorf("Git(%q) = %q, want %q", args, got, want)
		}
		if len(rec.calls) != 0 {
			t.Errorf("Git(%q) ran %v, want no call", args, rec.calls)
		}
	}
}

func TestGitOutsideRepoReturnsSentinel(t *testing.T) {
	requireGit(t)
	got := Probe{Root: t.TempDir()}.Git(context.Background(), []string{"log", "--oneline"}, 0)
	if got != collect.NotGitRepo {
		t.Fatalf("Git outside a repo = %q, want %q", got, collect.NotGitRepo)
	}
}

func TestGitKeepsPartialStdoutOnError(t *testing.T) {
	rec := &recordingRunner{out: "a\nb\nc", err: errors.New("exit status 1")}
	got := Probe{Root: "/repo", Runner: rec}.Git(context.Background(), []string{"log"}, 2)
	if got != "a\nb" {
		t.Fatalf("Git with partial stdout = %q", got)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("calls = %v", rec.calls)
	}
	if want := []string{"git", "-C", "/repo", "log"}; strings.Join(rec.calls[0], " ") != strings.Join(want, " ") {
		t.Fatalf("call = %v, want %v", rec.calls[0], want)
	}
}

func TestGitEmptyRootUsesDot(t *testing.T) {
	rec := &recordingRunner{out: "x"}
	Probe{Runner: rec}.Git(context.Background(), []string{"status"}, 0)
	if len(rec.calls) != 1 || rec.calls[0][2] != "." {
		t.Fatalf("calls = %v, want -C .", rec.calls)
	}
}

func TestIsClean(t *testing.T) {
	dir := newRepo(t)
	p := Probe{Root: dir}
	if got := p.IsClean(context.Background()); got != collect.Yes {
		t.Fatalf("IsClean of fresh repo = %q", got)
	}
	writeFile(t, dir, "new.txt", "x\n")
	if got := p.IsClean(context.Background()); got != collect.No {
		t.Fatalf("IsClean with untracked file = %q", got)
	}
	runGit(t, dir, "add", "new.txt")
	commit(t, dir, "add new")
	if got := p.IsClean(context.Background()); got != collect.Yes {
		t.Fatalf("IsClean after commit = %q", got)
	}
}

func TestIsCleanOutsideRepo(t *testing.T) {
	requireGit(t)
	if got := (Probe{Root: t.TempDir()}).IsClean(context.Background()); got != collect.No {
		t.Fatalf("IsClean outside a repo = %q", got)
	}
}

func TestCommitsSinceTag(t *testing.T) {
	dir := newRepo(t)
	p := Probe{Root: dir}
	if got := p.CommitsSinceTag(context.Background()); got != collect.Unknown {
		t.Fatalf("CommitsSinceTag without tags = %q", got)
	}
	runGit(t, dir, "tag", "v1.0.0")
	if got := p.CommitsSinceTag(context.Background()); got != "0" {
		t.Fatalf("CommitsSinceTag right after tagging = %q", got)
	}
	commit(t, dir, "after tag")
	if got := p.CommitsSinceTag(context.Background()); got != "1" {
		t.Fatalf("CommitsSinceTag after one commit = %q", got)
	}
}

func TestCommitsSinceTagOutsideRepo(t *testing.T) {
	requireGit(t)
	if got := (Probe{Root: t.TempDir()}).CommitsSinceTag(context.Background()); got != collect.Unknown {
		t.Fatalf("CommitsSinceTag outside a repo = %q", got)
	}
}

func TestOriginDelegates(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://example.com/clued.git")
	if got := (Probe{Root: dir}).Origin(context.Background()); got != "https://example.com/clued.git" {
		t.Fatalf("Origin = %q", got)
	}
}

func TestBranchDelegates(t *testing.T) {
	dir := newRepo(t)
	if got := (Probe{Root: dir}).Branch(context.Background()); got != "main" {
		t.Fatalf("Branch = %q", got)
	}
}

func TestDefaultBranch(t *testing.T) {
	t.Run("main", func(t *testing.T) {
		dir := newRepo(t)
		if got := (Probe{Root: dir}).DefaultBranch(context.Background()); got != "main" {
			t.Fatalf("DefaultBranch = %q", got)
		}
	})
	t.Run("master", func(t *testing.T) {
		dir := newRepoOn(t, "master")
		if got := (Probe{Root: dir}).DefaultBranch(context.Background()); got != "master" {
			t.Fatalf("DefaultBranch = %q", got)
		}
	})
	t.Run("prefers origin HEAD", func(t *testing.T) {
		dir := newRepo(t)
		runGit(t, dir, "update-ref", "refs/remotes/origin/develop", "HEAD")
		runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
		if got := (Probe{Root: dir}).DefaultBranch(context.Background()); got != "develop" {
			t.Fatalf("DefaultBranch = %q", got)
		}
	})
	t.Run("none", func(t *testing.T) {
		dir := newRepoOn(t, "trunk")
		if got := (Probe{Root: dir}).DefaultBranch(context.Background()); got != "" {
			t.Fatalf("DefaultBranch = %q, want empty", got)
		}
	})
	t.Run("outside repo", func(t *testing.T) {
		requireGit(t)
		if got := (Probe{Root: t.TempDir()}).DefaultBranch(context.Background()); got != "" {
			t.Fatalf("DefaultBranch = %q, want empty", got)
		}
	})
}

func TestLog(t *testing.T) {
	dir := newRepo(t)
	commit(t, dir, "second")
	got := Probe{Root: dir}.Log(context.Background(), "main", 1)
	if strings.Count(got, "\n") != 0 || !strings.HasSuffix(got, " second") {
		t.Fatalf("Log(main, 1) = %q", got)
	}
	if got := (Probe{Root: dir}).Log(context.Background(), "no-such-ref", 1); got != "" {
		t.Fatalf("Log of a missing ref = %q, want empty", got)
	}
}

func TestDiffStat(t *testing.T) {
	dir := newRepo(t)
	p := Probe{Root: dir}
	if got := p.DiffStat(context.Background(), "main"); got != "" {
		t.Fatalf("DiffStat on main = %q, want empty", got)
	}
	runGit(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "README.md", "hello\nworld\n")
	runGit(t, dir, "add", "README.md")
	commit(t, dir, "change readme")
	got := p.DiffStat(context.Background(), "main")
	if !strings.Contains(got, "README.md") || !strings.Contains(got, "1 file changed") {
		t.Fatalf("DiffStat on feature = %q", got)
	}
	if got := p.DiffStat(context.Background(), "no-such-base"); got != "" {
		t.Fatalf("DiffStat against a missing base = %q, want empty", got)
	}
}

func TestStatusSummary(t *testing.T) {
	dir := newRepo(t)
	p := Probe{Root: dir}
	if got := p.StatusSummary(context.Background()); got != collect.Clean {
		t.Fatalf("StatusSummary of fresh repo = %q", got)
	}
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b.txt", "b\n")
	if got := p.StatusSummary(context.Background()); got != "2 changed files" {
		t.Fatalf("StatusSummary with two files = %q", got)
	}
}

func TestStatusSummaryOutsideRepo(t *testing.T) {
	requireGit(t)
	if got := (Probe{Root: t.TempDir()}).StatusSummary(context.Background()); got != "" {
		t.Fatalf("StatusSummary outside a repo = %q, want empty", got)
	}
}
