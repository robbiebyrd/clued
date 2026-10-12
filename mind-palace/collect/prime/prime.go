package prime

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/collect/envprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/fsprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/ghprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/gitprobe"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

// DefaultBudget bounds one Collect call.
const DefaultBudget = 20 * time.Second

const (
	baseLogCount  = 15
	headLogCount  = 10
	planHeadLines = 100
	healthTimeout = time.Second
)

// keyDocs are the documents the FILES section inlines, with their line caps.
var keyDocs = []struct {
	name     string
	maxLines int
}{
	{"README.md", 80},
	{"SPEC.md", 120},
	{"CHANGELOG.md", 30},
	{"AGENTS.md", 60},
	{"CLAUDE.md", 60},
}

// Collector gathers the report. Nil probes and sources print their sentinel
// instead of failing.
type Collector struct {
	Root string // project directory, made absolute by Collect
	Home string // user home, for display only

	FS  *fsprobe.Probe
	Git *gitprobe.Probe
	GH  *ghprobe.Probe
	Env *envprobe.Probe

	Sessions SessionSource // nil prints "store disabled"
	Docs     DocSource     // nil prints "no plans dir"

	Host      func() session.HostInfo // nil omits the host line
	AccountID func() string           // nil omits the account line

	ConfigPath        string
	DaemonPort        int
	DisabledEnrichers []string

	Budget time.Duration // zero means DefaultBudget
}

// Options switches sections off.
type Options struct {
	NoStore bool // SESSION prints "store disabled"
	NoDocs  bool // STORIES and PLANS print "no plans dir"
}

type sectionFn func(ctx context.Context) (name, body string)

// Collect builds the report. Every section runs under the shared budget;
// one that panics or outlives the budget prints its fallback while the
// others still render.
func (c *Collector) Collect(ctx context.Context, opts Options) collect.Report {
	root := c.Root
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	budget := c.Budget
	if budget <= 0 {
		budget = DefaultBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	sections := []struct {
		name string
		fn   sectionFn
	}{
		{"ENV", func(ctx context.Context) (string, string) { return "ENV", c.env(ctx, root) }},
		{"CLUED", func(ctx context.Context) (string, string) { return "CLUED", c.clued(ctx) }},
		{"FILES", func(ctx context.Context) (string, string) { return "FILES", c.files() }},
		{"BRANCH", func(ctx context.Context) (string, string) { return "BRANCH", c.branch(ctx) }},
		{"LOG:base", c.baseLog},
		{"LOG:HEAD", func(ctx context.Context) (string, string) { return "LOG:HEAD", c.headLog(ctx) }},
		{"DIFF-STAT", func(ctx context.Context) (string, string) { return "DIFF-STAT", c.diffStat(ctx) }},
		{"STORIES", func(ctx context.Context) (string, string) { return "STORIES", c.stories(ctx, opts) }},
		{"PLANS", func(ctx context.Context) (string, string) { return "PLANS", c.plans(ctx, opts) }},
		{"SESSION", func(ctx context.Context) (string, string) { return "SESSION", c.sessionSection(ctx, root, opts) }},
		{"BUILD", func(ctx context.Context) (string, string) { return "BUILD", c.build(ctx) }},
	}
	var report collect.Report
	for _, s := range sections {
		name, body := withBudget(ctx, s.name, s.fn)
		report.Sections = append(report.Sections, collect.Section{Name: name, Body: body})
	}
	return report
}

type sectionResult struct{ name, body string }

// withBudget runs fn and returns its section, or the fallback when fn panics
// or the context ends first. A probe that ignores the context keeps running
// in its goroutine until it returns; its result is then discarded.
func withBudget(ctx context.Context, fallbackName string, fn sectionFn) (string, string) {
	done := make(chan sectionResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- sectionResult{fallbackName, collect.Fallback(fallbackName, nil)}
			}
		}()
		name, body := fn(ctx)
		done <- sectionResult{name, body}
	}()
	select {
	case r := <-done:
		return r.name, r.body
	case <-ctx.Done():
		return fallbackName, collect.Fallback(fallbackName, nil)
	}
}

func orNone(s string) string {
	if s == "" {
		return collect.None
	}
	return s
}

func (c *Collector) env(ctx context.Context, root string) string {
	lines := []string{
		"cwd: " + collect.DisplayPath(root),
		"project_path: " + session.EncodeProjectPath(filepath.ToSlash(root)),
	}
	origin, branch := "", ""
	if c.Git != nil {
		origin, branch = c.Git.Origin(ctx), c.Git.Branch(ctx)
	}
	lines = append(lines, "git_origin: "+orNone(origin), "git_branch: "+orNone(branch))
	if c.AccountID != nil {
		lines = append(lines, "account_id: "+orNone(c.AccountID()))
	}
	if c.Host != nil {
		lines = append(lines, "host: "+HostLine(c.Host()))
	}
	return strings.Join(lines, "\n")
}

// HostLine summarises a host without its ip or mac: hostname, username,
// platform/arch and OS release.
func HostLine(h session.HostInfo) string {
	return fmt.Sprintf("%s %s %s/%s %s", orNone(h.Hostname), orNone(h.Username), h.Platform, h.Arch, orNone(h.OSRelease))
}

func (c *Collector) clued(ctx context.Context) string {
	lines := []string{"config: " + orNone(c.ConfigPath)}
	if c.DaemonPort > 0 {
		state := "not running"
		if DaemonHealthy(ctx, c.DaemonPort) {
			state = "healthy"
		}
		lines = append(lines, fmt.Sprintf("daemon: %s (port %d)", state, c.DaemonPort))
	} else {
		lines = append(lines, "daemon: not configured")
	}
	disabled := collect.None
	if len(c.DisabledEnrichers) > 0 {
		disabled = strings.Join(c.DisabledEnrichers, " ")
	}
	lines = append(lines, "disabled enrichers: "+disabled)
	return strings.Join(lines, "\n")
}

// DaemonHealthy reports whether the clued daemon answers GET /health on
// 127.0.0.1:port within one second.
func DaemonHealthy(ctx context.Context, port int) bool {
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c *Collector) files() string {
	if c.FS == nil {
		return collect.Fallback("FILES", nil)
	}
	var lines []string
	for _, d := range keyDocs {
		if c.FS.FileExists(d.name) != collect.Yes {
			lines = append(lines, d.name+": no")
			continue
		}
		total := collect.ParseLimit(c.FS.CountLines(d.name))
		head := c.FS.FileHead(d.name, d.maxLines)
		block := "--- " + d.name + " ---\n" + head
		if more := total - d.maxLines; more > 0 {
			block += fmt.Sprintf("\n... (%d more lines)", more)
		}
		lines = append(lines, block)
	}
	lines = append(lines,
		"docs/plans: "+c.FS.CountFiles([]string{"docs/plans/*.md"})+" files",
		"docs/stories: "+c.FS.CountFiles([]string{"docs/stories/*.md"})+" files",
	)
	return strings.Join(lines, "\n")
}

func (c *Collector) branch(ctx context.Context) string {
	if c.Git == nil {
		return collect.NotGitRepo
	}
	status := c.Git.StatusSummary(ctx)
	if status == "" {
		return collect.NotGitRepo
	}
	branch := c.Git.Branch(ctx)
	if branch == "" {
		branch = collect.DetachedHead
	}
	body := branch + " (" + status + ")"
	if c.GH != nil && branch != collect.DetachedHead {
		body += "\npr: " + c.GH.PRForBranch(ctx, branch)
	}
	return body
}

// inRepo reports whether the root is inside a git repository.
func (c *Collector) inRepo(ctx context.Context) bool {
	return c.Git != nil && c.Git.StatusSummary(ctx) != ""
}

func (c *Collector) baseLog(ctx context.Context) (string, string) {
	if !c.inRepo(ctx) {
		return "LOG:base", collect.NotGitRepo
	}
	base := c.Git.DefaultBranch(ctx)
	if base == "" {
		return "LOG:base", collect.NoDefaultBranch
	}
	return "LOG:" + base, orNone(c.Git.Log(ctx, base, baseLogCount))
}

func (c *Collector) headLog(ctx context.Context) string {
	if !c.inRepo(ctx) {
		return collect.NotGitRepo
	}
	return orNone(c.Git.Log(ctx, "HEAD", headLogCount))
}

func (c *Collector) diffStat(ctx context.Context) string {
	if !c.inRepo(ctx) {
		return collect.NotGitRepo
	}
	base := c.Git.DefaultBranch(ctx)
	if base == "" {
		return collect.NoDefaultBranch
	}
	if stat := c.Git.DiffStat(ctx, base); stat != "" {
		return stat
	}
	return "no diff (on " + base + ")"
}

func docLine(d DocSummary) string {
	return fmt.Sprintf("%s [%s/%s] %s", d.ID, d.Type, d.Status, d.Title)
}

func (c *Collector) stories(ctx context.Context, opts Options) string {
	if c.Docs == nil || opts.NoDocs {
		return collect.NoPlansDir
	}
	docs, err := c.Docs.ActiveStories(ctx)
	if err != nil {
		return collect.Fallback("STORIES", nil)
	}
	if len(docs) == 0 {
		return collect.None
	}
	lines := make([]string, 0, len(docs))
	for _, d := range docs {
		lines = append(lines, docLine(d))
	}
	return strings.Join(lines, "\n")
}

func (c *Collector) plans(ctx context.Context, opts Options) string {
	if c.Docs == nil || opts.NoDocs {
		return collect.NoPlansDir
	}
	docs, err := c.Docs.ActivePlans(ctx)
	if err != nil {
		return collect.Fallback("PLANS", nil)
	}
	if len(docs) == 0 {
		return collect.None
	}
	lines := make([]string, 0, len(docs)+2)
	for _, d := range docs {
		lines = append(lines, docLine(d))
	}
	top := docs[0]
	content, err := c.Docs.PlanContent(ctx, top.ID)
	if err == nil && content != "" {
		all := strings.Split(strings.TrimRight(content, "\n"), "\n")
		head := all
		if len(all) > planHeadLines {
			head = all[:planHeadLines]
		}
		block := "--- " + top.ID + " ---\n" + strings.Join(head, "\n")
		if more := len(all) - planHeadLines; more > 0 {
			block += fmt.Sprintf("\n... (%d more lines)", more)
		}
		lines = append(lines, "", block)
	}
	return strings.Join(lines, "\n")
}

func (c *Collector) sessionSection(ctx context.Context, root string, opts Options) string {
	if c.Sessions == nil || opts.NoStore {
		return collect.StoreDisabled
	}
	snap, err := c.Sessions.Current(ctx, filepath.ToSlash(root))
	if err != nil {
		return collect.StoreUnavailable
	}
	return SessionBody(snap)
}

// SessionBody renders a session snapshot as the SESSION section does; a nil
// snapshot is "none".
func SessionBody(snap *SessionSnapshot) string {
	if snap == nil {
		return collect.None
	}
	lines := []string{"session: " + snap.SessionID}
	if !snap.StartedAt.IsZero() {
		lines = append(lines, "started: "+snap.StartedAt.UTC().Format(time.RFC3339))
	}
	if !snap.LastSeen.IsZero() {
		lines = append(lines, "last seen: "+snap.LastSeen.UTC().Format(time.RFC3339))
	}
	if len(snap.TopCommands) == 0 {
		lines = append(lines, "commands: "+collect.None)
	} else {
		lines = append(lines, "commands:")
		for _, cmd := range snap.TopCommands {
			lines = append(lines, "  "+cmd)
		}
	}
	return strings.Join(lines, "\n")
}

func (c *Collector) build(ctx context.Context) string {
	if c.Env == nil {
		return collect.NoBuildTool
	}
	return c.Env.DetectBuildTool(ctx)
}
