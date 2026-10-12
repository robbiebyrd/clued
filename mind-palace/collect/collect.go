// Package collect is the domain of the context collector: the command-runner
// port the probes depend on, the sentinel vocabulary every probe answers
// with, the report a composite probe renders, and the path and limit helpers
// the probe packages share.
//
// Every probe follows one contract: it returns exactly one plain-text answer,
// never an error to its caller, and maps every failure to a sentinel below.
package collect

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Sentinels: the fixed vocabulary of probe answers.
const (
	Yes              = "yes"
	No               = "no"
	None             = "none"
	Zero             = "0"
	Unknown          = "unknown"
	Configured       = "configured"
	NotConfigured    = "not configured"
	NotDetected      = "not detected"
	NoBuildTool      = "no build tool detected"
	NotGitRepo       = "not a git repo"
	NoDefaultBranch  = "no default branch"
	StoreUnavailable = "store unavailable"
	StoreDisabled    = "store disabled"
	NoPlansDir       = "no plans dir"
	Clean            = "clean"
	DetachedHead     = "detached HEAD"
)

// DefaultTimeout bounds one external command when a Runner has none.
const DefaultTimeout = 10 * time.Second

// Runner runs an external command in a directory and returns its stdout.
type Runner interface {
	// Run executes name with args in dir ("" means the process working
	// directory). It returns stdout with trailing whitespace trimmed (leading
	// whitespace is kept: a porcelain status line may start with one); on a
	// non-zero exit it returns the partial stdout together with the error.
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
}

// ExecRunner runs commands with os/exec, as argv, never through a shell.
type ExecRunner struct {
	// Timeout bounds each call; zero means DefaultTimeout.
	Timeout time.Duration
}

// Run implements Runner.
func (r ExecRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), " \t\r\n")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, fmt.Errorf("%s: %w", name, ctxErr)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return out, fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return out, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}

// Section is one named block of a Report.
type Section struct {
	Name string
	Body string
}

// Report is what a composite probe renders.
type Report struct {
	Sections []Section
}

// String renders the sections as "=== NAME ===" blocks separated by one blank
// line. Trailing newlines of a body are dropped so the spacing stays uniform.
func (r Report) String() string {
	var b strings.Builder
	for i, s := range r.Sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString("=== ")
		b.WriteString(s.Name)
		b.WriteString(" ===\n")
		b.WriteString(strings.TrimRight(s.Body, "\n"))
	}
	return b.String()
}

// Fallback is the answer of a probe that failed before producing one.
func Fallback(name string, args []string) string {
	if len(args) == 0 {
		return "[FALLBACK: " + name + "]"
	}
	return "[FALLBACK: " + name + " " + strings.Join(args, " ") + "]"
}

// NoFile is the answer for a file that could not be read.
func NoFile(path string) string {
	return "no " + filepath.Base(path)
}

// Truncate keeps the first limit lines of s; limit <= 0 keeps everything.
func Truncate(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= limit {
		return s
	}
	return strings.Join(lines[:limit], "\n")
}

// ParseLimit reads a --limit value; anything that is not a positive integer
// means unlimited (0).
func ParseLimit(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ExpandHome replaces a leading "~" or "~/" with the home directory.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// Resolve makes p absolute: "~" is expanded, an absolute path is kept, and a
// relative one is joined to root (or to the working directory when root is "").
func Resolve(root, p string) string {
	p = ExpandHome(p)
	if filepath.IsAbs(p) {
		return p
	}
	if root == "" {
		if wd, err := os.Getwd(); err == nil {
			root = wd
		}
	}
	return filepath.Join(root, p)
}

// DisplayPath renders a path with forward slashes on every platform.
func DisplayPath(p string) string {
	return filepath.ToSlash(p)
}
