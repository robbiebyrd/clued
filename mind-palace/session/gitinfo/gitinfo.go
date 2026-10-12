// Package gitinfo reads the git origin and branch of a working directory.
package gitinfo

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const gitTimeout = 2 * time.Second

// Origin returns the URL of the "origin" remote of the repository at dir,
// or "" when there is none or git fails.
func Origin(ctx context.Context, dir string) string {
	return run(ctx, dir, "remote", "get-url", "origin")
}

// Branch returns the checked-out branch of the repository at dir, or ""
// on a detached HEAD, outside a repository, or when git fails.
func Branch(ctx context.Context, dir string) string {
	return run(ctx, dir, "branch", "--show-current")
}

func run(ctx context.Context, dir string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
