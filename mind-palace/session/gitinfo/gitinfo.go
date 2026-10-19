// Package gitinfo reads facts about the git repository of a working
// directory: the origin and branch, and for code sync the worktree root,
// HEAD, the status, the file list, HEAD's tree, the upstream commit, the
// unpushed paths and the default branch.
//
// Every call runs `git -C <dir> …` as argv, never through a shell, bounded
// by a 2 s timeout. Lists are read with -z and split on NUL, so paths with
// spaces, quotes or newlines come back verbatim; they are slash-separated
// and relative to the repository root.
package gitinfo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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

// Root returns the top-level directory of the worktree containing dir, or
// "" outside a repository or when git fails.
func Root(ctx context.Context, dir string) string {
	return run(ctx, dir, "rev-parse", "--show-toplevel")
}

// Head returns the commit HEAD points at (40 hex characters, or 64 in a
// SHA-256 repository), or "" before the first commit, outside a repository
// or when git fails.
func Head(ctx context.Context, dir string) string {
	return run(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
}

// StatusEntry is one path `git status --porcelain` reports.
type StatusEntry struct {
	// Index and Worktree are the X and Y status letters: ' ' unchanged,
	// 'M' modified, 'T' type changed, 'A' added, 'D' deleted, 'R' renamed,
	// 'C' copied, 'U' unmerged, '?' untracked.
	Index, Worktree byte
	// Path is the path in the worktree (the new path of a rename or copy).
	Path string
	// From is the original path of a rename or copy, "" otherwise.
	From string
}

// Untracked reports whether git does not track the path.
func (e StatusEntry) Untracked() bool { return e.Index == '?' }

// Deleted reports whether the path no longer exists in the worktree.
func (e StatusEntry) Deleted() bool {
	return e.Worktree == 'D' || (e.Index == 'D' && e.Worktree == ' ')
}

// StatusPaths returns every changed, staged, unmerged or untracked path of
// the repository at dir (`status --porcelain -z --untracked-files=all`),
// untracked directories expanded to their files. A clean worktree is an
// empty slice; an error means git failed and nothing is known.
func StatusPaths(ctx context.Context, dir string) ([]StatusEntry, error) {
	out, err := runZ(ctx, dir, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseStatus(out)
}

func parseStatus(fields []string) ([]StatusEntry, error) {
	entries := make([]StatusEntry, 0, len(fields))
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 || f[2] != ' ' {
			return nil, fmt.Errorf("git status: malformed entry %q", f)
		}
		e := StatusEntry{Index: f[0], Worktree: f[1], Path: f[3:]}
		if isRenameOrCopy(e.Index) || isRenameOrCopy(e.Worktree) {
			i++
			if i >= len(fields) {
				return nil, fmt.Errorf("git status: rename of %q without its source", e.Path)
			}
			e.From = fields[i]
		}
		entries = append(entries, e)
	}
	return entries, nil
}

func isRenameOrCopy(c byte) bool { return c == 'R' || c == 'C' }

// ListFiles returns the tracked and the untracked but not ignored files of
// the repository at dir (`ls-files -z -co --exclude-standard`), each once,
// in git's order. Tracked files deleted from the worktree are still listed.
func ListFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := runZ(ctx, dir, "ls-files", "-z", "-co", "--exclude-standard", "--full-name")
	if err != nil {
		return nil, err
	}
	files := make([]string, 0, len(out))
	seen := make(map[string]struct{}, len(out))
	for _, p := range out {
		if _, dup := seen[p]; dup { // an unmerged path is listed once per stage
			continue
		}
		seen[p] = struct{}{}
		files = append(files, p)
	}
	return files, nil
}

// TreeEntry is one blob of a commit's tree.
type TreeEntry struct {
	Mode string // "100644", "100755" or "120000" (a symbolic link)
	OID  string // git's blob id: 40 hex characters, or 64 in a SHA-256 repository
}

// TreeOIDs maps every file of HEAD's tree to its mode and blob id
// (`ls-tree -r -z --full-tree HEAD`). Submodules are left out. Before the
// first commit the map is empty; an error means git failed.
func TreeOIDs(ctx context.Context, dir string) (map[string]TreeEntry, error) {
	out, err := runZ(ctx, dir, "ls-tree", "-r", "-z", "--full-tree", "HEAD")
	if err != nil {
		if Root(ctx, dir) != "" && Head(ctx, dir) == "" {
			return map[string]TreeEntry{}, nil
		}
		return nil, err
	}
	tree := make(map[string]TreeEntry, len(out))
	for _, line := range out {
		meta, path, ok := strings.Cut(line, "\t")
		parts := strings.Fields(meta)
		if !ok || len(parts) != 3 || path == "" {
			return nil, fmt.Errorf("git ls-tree: malformed entry %q", line)
		}
		if parts[1] != "blob" {
			continue
		}
		tree[path] = TreeEntry{Mode: parts[0], OID: parts[2]}
	}
	return tree, nil
}

// Upstream returns the commit the current branch's upstream points at and
// how many commits HEAD is ahead of it. ok is false, with "" and 0, when
// the branch has no upstream, HEAD is detached, or git fails.
func Upstream(ctx context.Context, dir string) (commit string, ahead int, ok bool) {
	commit = run(ctx, dir, "rev-parse", "--verify", "--quiet", "@{upstream}^{commit}")
	if commit == "" {
		return "", 0, false
	}
	count := run(ctx, dir, "rev-list", "--count", commit+"..HEAD")
	n, err := strconv.Atoi(count)
	if err != nil || n < 0 {
		return "", 0, false
	}
	return commit, n, true
}

// UnpushedPaths returns the paths that differ between upstream and HEAD
// (`diff --name-only -z --no-renames <upstream> HEAD`): both sides of a
// rename, added, changed and deleted files. upstream "" means
// "@{upstream}", which fails when the branch has none.
func UnpushedPaths(ctx context.Context, dir, upstream string) ([]string, error) {
	if upstream == "" {
		upstream = "@{upstream}"
	}
	if strings.HasPrefix(upstream, "-") {
		return nil, fmt.Errorf("git diff: invalid revision %q", upstream)
	}
	return runZ(ctx, dir, "diff", "--name-only", "-z", "--no-renames", "--no-ext-diff",
		"--no-relative", upstream, "HEAD", "--")
}

// DefaultBranch returns the branch origin/HEAD points at, else "main" or
// "master" when that local branch exists, else "".
func DefaultBranch(ctx context.Context, dir string) string {
	return DefaultBranchWith(ctx, func(ctx context.Context, args ...string) (string, error) {
		out, err := output(ctx, dir, args...)
		return strings.TrimSpace(string(out)), err
	})
}

// DefaultBranchWith is DefaultBranch over a caller's git runner: git runs
// one git command (args without "git" and "-C") in the repository and
// returns its stdout. It lets a caller with its own runner and timeout
// share the one implementation.
func DefaultBranchWith(ctx context.Context, git func(ctx context.Context, args ...string) (string, error)) string {
	if out, err := git(ctx, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(out), "origin/"); name != "" {
			return name
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return ""
}

func run(ctx context.Context, dir string, args ...string) string {
	out, err := output(ctx, dir, args...)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// runZ runs a git command printing NUL-terminated fields and returns them
// untrimmed; an empty output is an empty slice.
func runZ(ctx context.Context, dir string, args ...string) ([]string, error) {
	out, err := output(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return []string{}, nil
	}
	if out[len(out)-1] != 0 {
		return nil, errors.New("git " + args[0] + ": output not NUL-terminated")
	}
	fields := bytes.Split(out[:len(out)-1], []byte{0})
	list := make([]string, len(fields))
	for i, f := range fields {
		list[i] = string(f)
	}
	return list, nil
}

func output(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("git %s: %w", args[0], ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("git %s: %w: %s", args[0], err, msg)
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}
