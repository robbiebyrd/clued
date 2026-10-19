// Package gitprobe answers questions about the git repository of a working
// directory: cleanliness, commits since the latest tag, the default branch,
// a bounded log, a diff stat and a status summary, plus an escape hatch that
// runs an arbitrary read-only git command through an allowlist.
//
// Every probe returns exactly one plain-text answer and never an error:
// failures map to the sentinels of package collect or to "".
package gitprobe

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
)

// DefaultTimeout bounds one git call when the Probe has no Runner.
const DefaultTimeout = 2 * time.Second

// Probe runs git against one repository.
type Probe struct {
	Root   string         // repository directory; "" means the working directory
	Runner collect.Runner // nil means collect.ExecRunner{Timeout: DefaultTimeout}
}

func (p Probe) root() string {
	if p.Root == "" {
		return "."
	}
	return p.Root
}

func (p Probe) runner() collect.Runner {
	if p.Runner == nil {
		return collect.ExecRunner{Timeout: DefaultTimeout}
	}
	return p.Runner
}

// run executes `git -C <root> args...` through the Runner.
func (p Probe) run(ctx context.Context, args ...string) (string, error) {
	return p.runner().Run(ctx, "", "git", append([]string{"-C", p.root()}, args...)...)
}

// Git runs an arbitrary read-only git command and keeps the first limit
// lines of its output (limit <= 0 keeps everything). A command Allowed
// rejects is answered with collect.Fallback without running anything; a
// failing command answers with its partial output, or collect.NotGitRepo
// when it produced none.
func (p Probe) Git(ctx context.Context, args []string, limit int) string {
	if !Allowed(args) {
		return collect.Fallback("git", args)
	}
	out, err := p.run(ctx, args...)
	if err != nil && out == "" {
		return collect.NotGitRepo
	}
	return collect.Truncate(out, limit)
}

// IsClean is collect.Yes when `git status --porcelain` prints nothing, and
// collect.No otherwise or when git fails.
func (p Probe) IsClean(ctx context.Context) string {
	out, err := p.run(ctx, "status", "--porcelain")
	if err != nil || out != "" {
		return collect.No
	}
	return collect.Yes
}

// CommitsSinceTag counts the commits after the latest tag reachable from
// HEAD, or collect.Unknown when there is no tag or git fails.
func (p Probe) CommitsSinceTag(ctx context.Context) string {
	tag, err := p.run(ctx, "describe", "--tags", "--abbrev=0")
	if err != nil || tag == "" {
		return collect.Unknown
	}
	count, err := p.run(ctx, "rev-list", "--count", tag+"..HEAD")
	if err != nil {
		return collect.Unknown
	}
	n, err := strconv.Atoi(strings.TrimSpace(count))
	if err != nil {
		return collect.Unknown
	}
	return strconv.Itoa(n)
}

// Origin is the URL of the "origin" remote, or "" when there is none.
func (p Probe) Origin(ctx context.Context) string {
	return gitinfo.Origin(ctx, p.root())
}

// Branch is the checked-out branch, or "" on a detached HEAD or outside a
// repository.
func (p Probe) Branch(ctx context.Context) string {
	return gitinfo.Branch(ctx, p.root())
}

// DefaultBranch is the branch origin/HEAD points at, else "main" or "master"
// when that local branch exists, else "" (gitinfo.DefaultBranchWith over
// the Probe's Runner).
func (p Probe) DefaultBranch(ctx context.Context) string {
	return gitinfo.DefaultBranchWith(ctx, p.run)
}

// Log is `git log --oneline -n <n> <ref>`, or "" when git fails.
func (p Probe) Log(ctx context.Context, ref string, n int) string {
	out, err := p.run(ctx, "log", "--oneline", "-n", strconv.Itoa(n), ref)
	if err != nil {
		return ""
	}
	return out
}

// DiffStat is `git diff <base>...HEAD --stat`, or "" when git fails or
// there is no difference.
func (p Probe) DiffStat(ctx context.Context, base string) string {
	out, err := p.run(ctx, "diff", base+"...HEAD", "--stat")
	if err != nil {
		return ""
	}
	return out
}

// StatusSummary is collect.Clean when nothing is changed, "1 changed file" or "<N> changed files"
// counting the lines of `git status --porcelain` otherwise, or "" when git
// fails.
func (p Probe) StatusSummary(ctx context.Context) string {
	out, err := p.run(ctx, "status", "--porcelain")
	if err != nil {
		return ""
	}
	if out == "" {
		return collect.Clean
	}
	n := strings.Count(out, "\n") + 1
	if n == 1 {
		return "1 changed file"
	}
	return strconv.Itoa(n) + " changed files"
}

// Allowed reports whether args is a read-only git command: the first
// argument must be an allowed subcommand and no argument may write a ref,
// a file or redirect git to another repository.
func Allowed(args []string) bool {
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return false
	}
	for _, a := range args {
		if a == "-o" || strings.HasPrefix(a, "--output") {
			return false
		}
		for _, p := range []string{"--exec-path", "--git-dir", "--work-tree", "-C", "-c"} {
			if strings.HasPrefix(a, p) {
				return false
			}
		}
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "log", "status", "diff", "show", "rev-parse", "describe", "ls-files",
		"blame", "shortlog", "rev-list", "ls-tree", "cat-file", "name-rev",
		"for-each-ref", "grep":
		return true
	case "reflog":
		return !containsAny(rest, "expire", "delete")
	case "branch":
		return branchAllowed(rest)
	case "tag":
		return tagAllowed(rest)
	case "remote":
		return remoteAllowed(rest)
	case "stash":
		return len(rest) > 0 && (rest[0] == "list" || rest[0] == "show")
	case "config":
		return len(rest) > 0 && containsAny([]string{rest[0]},
			"--get", "--get-all", "--get-regexp", "--list", "-l")
	case "symbolic-ref":
		return symbolicRefAllowed(rest)
	}
	return false
}

func containsAny(args []string, words ...string) bool {
	for _, a := range args {
		for _, w := range words {
			if a == w {
				return true
			}
		}
	}
	return false
}

func branchAllowed(rest []string) bool {
	for _, a := range rest {
		if strings.HasPrefix(a, "--set-upstream-to=") {
			return false
		}
	}
	return !containsAny(rest,
		"-d", "-D", "-m", "-M", "-c", "-C", "-u", "-f",
		"--delete", "--move", "--copy", "--force", "--set-upstream-to",
		"--unset-upstream", "--edit-description", "--track", "--no-track",
		"--create-reflog")
}

var tagListFlags = []string{
	"-l", "--list", "-n", "--sort", "--contains", "--no-contains",
	"--points-at", "--merged", "--no-merged", "--format", "--column",
	"--no-column", "-i", "--ignore-case",
}

func tagAllowed(rest []string) bool {
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") || !tagFlagAllowed(a) {
			return false
		}
	}
	return true
}

func tagFlagAllowed(a string) bool {
	for _, f := range tagListFlags {
		if a == f {
			return true
		}
		if strings.HasPrefix(a, f+"=") && f != "-l" && f != "-n" && f != "-i" {
			return true
		}
		if f == "-n" && strings.HasPrefix(a, "-n") && isDigits(a[2:]) {
			return true
		}
	}
	return false
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func remoteAllowed(rest []string) bool {
	if len(rest) == 0 {
		return true
	}
	switch rest[0] {
	case "-v", "--verbose":
		return len(rest) == 1
	case "show", "get-url":
		return true
	}
	return false
}

// symbolicRefAllowed admits reading one ref: a second non-flag argument
// would set it, and -d/--delete would remove it.
func symbolicRefAllowed(rest []string) bool {
	refs := 0
	for _, a := range rest {
		if a == "-d" || a == "--delete" {
			return false
		}
		if !strings.HasPrefix(a, "-") {
			refs++
		}
	}
	return refs <= 1
}
