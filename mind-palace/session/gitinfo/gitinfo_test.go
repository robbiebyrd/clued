package gitinfo

import (
	"context"
	"os/exec"
	"path/filepath"
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
