package gitinfo

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

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

func newRepo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "test-branch")
	runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	return dir
}

func TestOriginReturnsRemoteURL(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "remote", "add", "origin", "https://example.com/clued.git")
	if got := Origin(context.Background(), dir); got != "https://example.com/clued.git" {
		t.Fatalf("Origin = %q", got)
	}
}

func TestOriginEmptyWithoutRemote(t *testing.T) {
	dir := newRepo(t)
	if got := Origin(context.Background(), dir); got != "" {
		t.Fatalf("Origin = %q, want empty", got)
	}
}

func TestOriginEmptyForPlainDirectory(t *testing.T) {
	requireGit(t)
	if got := Origin(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("Origin = %q, want empty", got)
	}
}

func TestOriginEmptyForMissingPath(t *testing.T) {
	requireGit(t)
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	if got := Origin(context.Background(), missing); got != "" {
		t.Fatalf("Origin = %q, want empty", got)
	}
}

func TestBranchReturnsName(t *testing.T) {
	dir := newRepo(t)
	if got := Branch(context.Background(), dir); got != "test-branch" {
		t.Fatalf("Branch = %q", got)
	}
}

func TestBranchEmptyForPlainDirectory(t *testing.T) {
	requireGit(t)
	if got := Branch(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("Branch = %q, want empty", got)
	}
}

func TestBranchEmptyForDetachedHead(t *testing.T) {
	dir := newRepo(t)
	runGit(t, dir, "checkout", "-q", "--detach")
	if got := Branch(context.Background(), dir); got != "" {
		t.Fatalf("Branch = %q, want empty", got)
	}
}

func TestBranchEmptyForMissingPath(t *testing.T) {
	requireGit(t)
	missing := filepath.Join(t.TempDir(), "does", "not", "exist")
	if got := Branch(context.Background(), missing); got != "" {
		t.Fatalf("Branch = %q, want empty", got)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", msg)
}

// withUpstream pushes the current branch to a new bare origin and sets it
// as the upstream.
func withUpstream(t *testing.T, dir string) string {
	t.Helper()
	bare := t.TempDir()
	runGit(t, bare, "init", "-q", "--bare")
	runGit(t, dir, "remote", "add", "origin", bare)
	runGit(t, dir, "push", "-q", "-u", "origin", "HEAD")
	return bare
}

func sortedPaths(entries []StatusEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, string([]byte{e.Index, e.Worktree})+" "+e.Path+"<"+e.From)
	}
	sort.Strings(out)
	return out
}

func TestRootFromSubdirectory(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a/b/c.txt", "x")
	got := Root(context.Background(), filepath.Join(dir, "a", "b"))
	want, _ := filepath.EvalSymlinks(dir)
	if got, _ = filepath.EvalSymlinks(got); got != want {
		t.Fatalf("Root = %q, want %q", got, want)
	}
}

func TestRootEmptyOutsideRepo(t *testing.T) {
	requireGit(t)
	if got := Root(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("Root = %q, want empty", got)
	}
}

func TestHead(t *testing.T) {
	dir := newRepo(t)
	got := Head(context.Background(), dir)
	if len(got) != 40 || got != gitOut(t, dir, "rev-parse", "HEAD") {
		t.Fatalf("Head = %q", got)
	}
	runGit(t, dir, "checkout", "-q", "--detach")
	if again := Head(context.Background(), dir); again != got {
		t.Fatalf("Head on detached HEAD = %q, want %q", again, got)
	}
}

func TestHeadEmptyBeforeFirstCommitAndOutsideRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if got := Head(context.Background(), dir); got != "" {
		t.Fatalf("Head outside repo = %q", got)
	}
	runGit(t, dir, "init", "-q")
	if got := Head(context.Background(), dir); got != "" {
		t.Fatalf("Head on unborn branch = %q", got)
	}
}

func TestStatusPathsClean(t *testing.T) {
	dir := newRepo(t)
	got, err := StatusPaths(context.Background(), dir)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("StatusPaths = %v, %v; want empty", got, err)
	}
}

func TestStatusPathsKinds(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "keep.txt", "keep\n")
	writeFile(t, dir, "old name.txt", "a file that will be renamed\n")
	writeFile(t, dir, "gone.txt", "gone\n")
	commitAll(t, dir, "files")

	writeFile(t, dir, "keep.txt", "changed\n")
	runGit(t, dir, "mv", "old name.txt", "new\nname.txt")
	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "untracked dir/deep/u.txt", "u\n")
	writeFile(t, dir, "staged.txt", "s\n")
	runGit(t, dir, "add", "staged.txt")
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "ignored.log", "i\n")

	entries, err := StatusPaths(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		" D gone.txt<",
		" M keep.txt<",
		"?? .gitignore<",
		"?? untracked dir/deep/u.txt<",
		"A  staged.txt<",
		"R  new\nname.txt<old name.txt",
	}
	if got := sortedPaths(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("StatusPaths =\n%q\nwant\n%q", got, want)
	}
	for _, e := range entries {
		switch e.Path {
		case "gone.txt":
			if !e.Deleted() || e.Untracked() {
				t.Errorf("gone.txt: Deleted=%v Untracked=%v", e.Deleted(), e.Untracked())
			}
		case "untracked dir/deep/u.txt":
			if !e.Untracked() || e.Deleted() {
				t.Errorf("untracked: Deleted=%v Untracked=%v", e.Deleted(), e.Untracked())
			}
		case "keep.txt":
			if e.Untracked() || e.Deleted() {
				t.Errorf("keep.txt: Deleted=%v Untracked=%v", e.Deleted(), e.Untracked())
			}
		}
	}
}

func TestStatusPathsRootRelativeFromSubdirectory(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "sub/a.txt", "a")
	writeFile(t, dir, "top.txt", "t")
	entries, err := StatusPaths(context.Background(), filepath.Join(dir, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"?? sub/a.txt<", "?? top.txt<"}
	if got := sortedPaths(entries); !reflect.DeepEqual(got, want) {
		t.Fatalf("StatusPaths = %q, want %q", got, want)
	}
}

func TestStatusPathsErrorOutsideRepo(t *testing.T) {
	requireGit(t)
	if got, err := StatusPaths(context.Background(), t.TempDir()); err == nil {
		t.Fatalf("StatusPaths outside repo = %v, want an error", got)
	}
}

func TestParseStatusRejectsMalformed(t *testing.T) {
	for _, fields := range [][]string{{"M"}, {"MMxpath"}, {"R  new"}} {
		if got, err := parseStatus(fields); err == nil {
			t.Errorf("parseStatus(%q) = %v, want an error", fields, got)
		}
	}
	got, err := parseStatus([]string{" M a", "C  b", "a", "?? c"})
	if err != nil || len(got) != 3 || got[1].From != "a" || got[1].Path != "b" || got[2].Path != "c" {
		t.Fatalf("parseStatus = %+v, %v", got, err)
	}
}

func TestListFiles(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "build/\n*.tmp\n")
	writeFile(t, dir, "src/main.go", "package main\n")
	writeFile(t, dir, "tracked deleted.txt", "x\n")
	commitAll(t, dir, "files")
	if err := os.Remove(filepath.Join(dir, "tracked deleted.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "src/new file.go", "package main\n")
	writeFile(t, dir, "build/out.bin", "ignored")
	writeFile(t, dir, "scratch.tmp", "ignored")
	writeFile(t, dir, "quote\"and\ttab.txt", "odd name")

	got, err := ListFiles(context.Background(), filepath.Join(dir, "src"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	want := []string{".gitignore", "quote\"and\ttab.txt", "src/main.go", "src/new file.go", "tracked deleted.txt"}
	sort.Strings(want)
	// From a subdirectory ls-files lists only that subdirectory, by full name.
	wantSub := []string{"src/main.go", "src/new file.go"}
	if !reflect.DeepEqual(got, wantSub) {
		t.Fatalf("ListFiles(src) = %q, want %q", got, wantSub)
	}
	got, err = ListFiles(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListFiles = %q, want %q", got, want)
	}
}

func TestListFilesErrorOutsideRepo(t *testing.T) {
	requireGit(t)
	if got, err := ListFiles(context.Background(), t.TempDir()); err == nil {
		t.Fatalf("ListFiles outside repo = %v, want an error", got)
	}
}

func TestTreeOIDs(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "dir/b c.txt", "b\n")
	writeFile(t, dir, "run.sh", "#!/bin/sh\n")
	if err := os.Chmod(filepath.Join(dir, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "files")
	writeFile(t, dir, "a.txt", "dirty\n")     // worktree edits do not show
	writeFile(t, dir, "untracked.txt", "u\n") // nor untracked files

	tree, err := TreeOIDs(context.Background(), filepath.Join(dir, "dir"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tree) != 4 {
		t.Fatalf("TreeOIDs = %v, want 4 entries", tree)
	}
	for path, mode := range map[string]string{"a.txt": "100644", "dir/b c.txt": "100644", "run.sh": "100755", "link": "120000"} {
		e, ok := tree[path]
		if !ok || e.Mode != mode || e.OID != gitOut(t, dir, "rev-parse", "HEAD:"+path) {
			t.Errorf("TreeOIDs[%q] = %+v, %v; want mode %s", path, e, ok, mode)
		}
	}
}

func TestTreeOIDsSkipsSubmodules(t *testing.T) {
	dir := newRepo(t)
	sub := newRepo(t)
	runGit(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "vendor/sub")
	commitAll(t, dir, "submodule")
	tree, err := TreeOIDs(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tree["vendor/sub"]; ok {
		t.Fatalf("TreeOIDs lists the submodule: %v", tree)
	}
	if _, ok := tree[".gitmodules"]; !ok {
		t.Fatalf("TreeOIDs = %v, want .gitmodules", tree)
	}
}

func TestTreeOIDsEmptyBeforeFirstCommit(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	writeFile(t, dir, "a.txt", "a")
	tree, err := TreeOIDs(context.Background(), dir)
	if err != nil || tree == nil || len(tree) != 0 {
		t.Fatalf("TreeOIDs = %v, %v; want empty", tree, err)
	}
}

func TestTreeOIDsErrorOutsideRepo(t *testing.T) {
	requireGit(t)
	if tree, err := TreeOIDs(context.Background(), t.TempDir()); err == nil {
		t.Fatalf("TreeOIDs outside repo = %v, want an error", tree)
	}
}

func TestUpstreamAndUnpushedPaths(t *testing.T) {
	dir := newRepo(t)
	writeFile(t, dir, "pushed.txt", "p\n")
	writeFile(t, dir, "to rename.txt", "r\n")
	writeFile(t, dir, "to delete.txt", "d\n")
	commitAll(t, dir, "base")
	withUpstream(t, dir)
	pushed := gitOut(t, dir, "rev-parse", "HEAD")

	commit, ahead, ok := Upstream(context.Background(), dir)
	if !ok || commit != pushed || ahead != 0 {
		t.Fatalf("Upstream in sync = %q, %d, %v", commit, ahead, ok)
	}
	paths, err := UnpushedPaths(context.Background(), dir, commit)
	if err != nil || paths == nil || len(paths) != 0 {
		t.Fatalf("UnpushedPaths in sync = %q, %v", paths, err)
	}

	writeFile(t, dir, "new.txt", "n\n")
	commitAll(t, dir, "one")
	runGit(t, dir, "mv", "to rename.txt", "renamed.txt")
	runGit(t, dir, "rm", "-q", "to delete.txt")
	commitAll(t, dir, "two")
	writeFile(t, dir, "pushed.txt", "uncommitted\n") // not unpushed: not committed

	commit, ahead, ok = Upstream(context.Background(), filepath.Join(dir))
	if !ok || commit != pushed || ahead != 2 {
		t.Fatalf("Upstream ahead = %q, %d, %v", commit, ahead, ok)
	}
	want := []string{"new.txt", "renamed.txt", "to delete.txt", "to rename.txt"}
	for _, up := range []string{commit, ""} {
		paths, err = UnpushedPaths(context.Background(), dir, up)
		if err != nil {
			t.Fatal(err)
		}
		sort.Strings(paths)
		if !reflect.DeepEqual(paths, want) {
			t.Fatalf("UnpushedPaths(%q) = %q, want %q", up, paths, want)
		}
	}
}

func TestUpstreamBehind(t *testing.T) {
	dir := newRepo(t)
	withUpstream(t, dir)
	writeFile(t, dir, "a.txt", "a")
	commitAll(t, dir, "ahead")
	runGit(t, dir, "push", "-q")
	runGit(t, dir, "reset", "-q", "--hard", "HEAD~1")
	commit, ahead, ok := Upstream(context.Background(), dir)
	if !ok || ahead != 0 || commit == Head(context.Background(), dir) {
		t.Fatalf("Upstream behind = %q, %d, %v", commit, ahead, ok)
	}
}

func TestUpstreamNone(t *testing.T) {
	dir := newRepo(t)
	if commit, ahead, ok := Upstream(context.Background(), dir); ok || commit != "" || ahead != 0 {
		t.Fatalf("Upstream without one = %q, %d, %v", commit, ahead, ok)
	}
	if paths, err := UnpushedPaths(context.Background(), dir, ""); err == nil {
		t.Fatalf("UnpushedPaths without upstream = %q, want an error", paths)
	}
}

func TestUpstreamNoneOnDetachedHead(t *testing.T) {
	dir := newRepo(t)
	withUpstream(t, dir)
	runGit(t, dir, "checkout", "-q", "--detach")
	if commit, ahead, ok := Upstream(context.Background(), dir); ok || commit != "" || ahead != 0 {
		t.Fatalf("Upstream on detached HEAD = %q, %d, %v", commit, ahead, ok)
	}
	if got := Branch(context.Background(), dir); got != "" {
		t.Fatalf("Branch = %q", got)
	}
	if got := Head(context.Background(), dir); got == "" {
		t.Fatal("Head empty on detached HEAD")
	}
}

func TestUpstreamOutsideRepo(t *testing.T) {
	requireGit(t)
	if commit, ahead, ok := Upstream(context.Background(), t.TempDir()); ok || commit != "" || ahead != 0 {
		t.Fatalf("Upstream outside repo = %q, %d, %v", commit, ahead, ok)
	}
}

func TestUnpushedPathsRejectsOptionRevision(t *testing.T) {
	dir := newRepo(t)
	if _, err := UnpushedPaths(context.Background(), dir, "--output=/tmp/x"); err == nil {
		t.Fatal("UnpushedPaths accepted an option as revision")
	}
}

func TestSHA256Repository(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q", "--object-format=sha256", "-b", "main").CombinedOutput(); err != nil {
		t.Skipf("git cannot create SHA-256 repositories: %v %s", err, out)
	}
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")
	bare := t.TempDir()
	runGit(t, bare, "init", "-q", "--bare", "--object-format=sha256")
	runGit(t, dir, "remote", "add", "origin", bare)
	runGit(t, dir, "push", "-q", "-u", "origin", "HEAD")
	writeFile(t, dir, "b.txt", "b\n")
	commitAll(t, dir, "second")

	head := Head(context.Background(), dir)
	if len(head) != 64 {
		t.Fatalf("Head = %q, want 64 hex characters", head)
	}
	tree, err := TreeOIDs(context.Background(), dir)
	if err != nil || len(tree["a.txt"].OID) != 64 || len(tree["b.txt"].OID) != 64 {
		t.Fatalf("TreeOIDs = %v, %v", tree, err)
	}
	commit, ahead, ok := Upstream(context.Background(), dir)
	if !ok || len(commit) != 64 || ahead != 1 {
		t.Fatalf("Upstream = %q, %d, %v", commit, ahead, ok)
	}
	paths, err := UnpushedPaths(context.Background(), dir, commit)
	if err != nil || !reflect.DeepEqual(paths, []string{"b.txt"}) {
		t.Fatalf("UnpushedPaths = %q, %v", paths, err)
	}
}

func TestDefaultBranch(t *testing.T) {
	requireGit(t)
	newOn := func(t *testing.T, branch string) string {
		dir := t.TempDir()
		runGit(t, dir, "init", "-q", "-b", branch)
		runGit(t, dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "--allow-empty", "-m", "init")
		return dir
	}
	for _, branch := range []string{"main", "master"} {
		if got := DefaultBranch(context.Background(), newOn(t, branch)); got != branch {
			t.Errorf("DefaultBranch on %s = %q", branch, got)
		}
	}
	dir := newOn(t, "main")
	runGit(t, dir, "update-ref", "refs/remotes/origin/develop", "HEAD")
	runGit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	if got := DefaultBranch(context.Background(), dir); got != "develop" {
		t.Errorf("DefaultBranch with origin/HEAD = %q", got)
	}
	if got := DefaultBranch(context.Background(), newOn(t, "trunk")); got != "" {
		t.Errorf("DefaultBranch without main or master = %q", got)
	}
	if got := DefaultBranch(context.Background(), t.TempDir()); got != "" {
		t.Errorf("DefaultBranch outside repo = %q", got)
	}
}

func TestDefaultBranchWithUsesTheGivenRunner(t *testing.T) {
	var calls [][]string
	got := DefaultBranchWith(context.Background(), func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, args)
		if args[0] == "rev-parse" && args[len(args)-1] == "refs/heads/master" {
			return "abc", nil
		}
		return "", errors.New("no")
	})
	if got != "master" || len(calls) != 3 {
		t.Fatalf("DefaultBranchWith = %q after %q", got, calls)
	}
}
