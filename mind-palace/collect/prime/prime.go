package prime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/collect/envprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/fsprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/ghprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/gitprobe"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

// DefaultBudget bounds one Collect call.
const DefaultBudget = 20 * time.Second

// StoreTimeout bounds the SESSION lookup, connect included: a MongoDB that is
// down must cost a sentinel line, not the whole budget.
const StoreTimeout = 2 * time.Second

// RemoteStoreTimeout bounds the same lookups when the store is a clued
// server: every store call is an HTTPS round trip, so the local bound would
// report a healthy server as unavailable.
const RemoteStoreTimeout = 6 * time.Second

// StoreFailure is the sentinel a SESSION or MEMORY lookup prints when the
// store fails: collect.StoreUnavailable, followed by the cause when it is
// one the user can act on (a lookup that ran out of time, a token the
// server rejected or a project it refused).
func StoreFailure(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return collect.StoreUnavailable + " (timeout)"
	case errors.Is(err, remote.ErrUnauthorized):
		return collect.StoreUnavailable + " (unauthorized)"
	case errors.Is(err, remote.ErrForbidden):
		return collect.StoreUnavailable + " (forbidden)"
	}
	return collect.StoreUnavailable
}

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
	Memory   MemorySource  // nil prints "store disabled"
	Docs     DocSource     // nil prints "no plans dir"

	Host      func() session.HostInfo // nil omits the host line
	AccountID func() string           // nil omits the account line

	ConfigPath string
	DaemonPort int
	// Server, when set, is the clued server of remote mode: the CLUED
	// section then reports whether it accepts the token (ServerHealthy)
	// instead of whether the daemon runs on DaemonPort.
	Server            *remote.Client
	DisabledEnrichers []string
	EnabledEnrichers  []string // enrichers switched on among those that ship disabled

	Budget time.Duration // zero means DefaultBudget
	// StoreTimeout bounds each store lookup (SESSION, MEMORY); zero means
	// the package's StoreTimeout.
	StoreTimeout time.Duration
}

func (c *Collector) storeTimeout() time.Duration {
	if c.StoreTimeout > 0 {
		return c.StoreTimeout
	}
	return StoreTimeout
}

// Options switches sections off.
type Options struct {
	NoStore bool // SESSION prints "store disabled"
	NoDocs  bool // STORIES and PLANS print "no plans dir"
}

type sectionFn func(ctx context.Context) (name, body string)

// Result is a collected report plus what the SESSION section found.
type Result struct {
	Report collect.Report
	// SessionID is the captured session the SESSION section showed; empty
	// when none was found, the store was skipped, or the lookup failed.
	SessionID string
}

// Collect builds the report. Every section runs under the shared budget;
// one that panics or outlives the budget prints its fallback while the
// others still render.
func (c *Collector) Collect(ctx context.Context, opts Options) collect.Report {
	return c.CollectResult(ctx, opts).Report
}

// CollectResult is Collect with the session id the SESSION section found.
func (c *Collector) CollectResult(ctx context.Context, opts Options) Result {
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

	var found struct {
		sync.Mutex
		id string
	}
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
		{"SESSION", func(ctx context.Context) (string, string) {
			body, id := c.sessionSection(ctx, root, opts)
			found.Lock()
			found.id = id
			found.Unlock()
			return "SESSION", body
		}},
		{"MEMORY", func(ctx context.Context) (string, string) { return "MEMORY", c.memory(ctx, root, opts) }},
		{"BUILD", func(ctx context.Context) (string, string) { return "BUILD", c.build(ctx) }},
	}
	var report collect.Report
	for _, s := range sections {
		name, body := withBudget(ctx, s.name, s.fn)
		report.Sections = append(report.Sections, collect.Section{Name: name, Body: body})
	}
	found.Lock()
	defer found.Unlock()
	return Result{Report: report, SessionID: found.id}
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
	switch {
	case c.Server != nil:
		state := "unreachable"
		if ServerHealthy(ctx, c.Server) {
			state = "healthy"
		}
		lines = append(lines, fmt.Sprintf("server: %s (%s)", state, c.Server.BaseURL()))
	case c.DaemonPort > 0:
		state := "not running"
		if DaemonHealthy(ctx, c.DaemonPort) {
			state = "healthy"
		}
		lines = append(lines, fmt.Sprintf("daemon: %s (port %d)", state, c.DaemonPort))
	default:
		lines = append(lines, "daemon: not configured")
	}
	lines = append(lines,
		"disabled enrichers: "+namesOrNone(c.DisabledEnrichers),
		"enabled enrichers: "+namesOrNone(c.EnabledEnrichers),
	)
	return strings.Join(lines, "\n")
}

func namesOrNone(names []string) string {
	if len(names) == 0 {
		return collect.None
	}
	return strings.Join(names, " ")
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

// sessionSection returns the SESSION body and the session id it found.
func (c *Collector) sessionSection(ctx context.Context, root string, opts Options) (string, string) {
	if c.Sessions == nil || opts.NoStore {
		return collect.StoreDisabled, ""
	}
	ctx, cancel := context.WithTimeout(ctx, c.storeTimeout())
	defer cancel()
	snap, err := c.Sessions.Current(ctx, filepath.ToSlash(root))
	if err != nil {
		return StoreFailure(err), ""
	}
	if snap == nil {
		return collect.None, ""
	}
	return SessionBody(snap), snap.SessionID
}

// SessionBody renders a session snapshot as the SESSION section does; a nil
// snapshot is "none".
func SessionBody(snap *SessionSnapshot) string {
	if snap == nil {
		return collect.None
	}
	lines := []string{"session: " + snap.SessionID}
	if snap.Model != "" {
		lines = append(lines, "model: "+snap.Model)
	}
	if snap.LiveState != "" {
		lines = append(lines, "state: "+snap.LiveState)
	}
	if snap.Hotness != "" {
		lines = append(lines, "hotness: "+snap.Hotness)
	}
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

// The MEMORY section shows this many records, each cut to memoryTextLines.
const (
	memoryCount     = 5
	memoryTextLines = 8
)

// memory renders the MEMORY section: the newest records claude-remember
// wrote for the project (captured by `session memory-ingest`), so a session
// starts with what the previous ones concluded.
func (c *Collector) memory(ctx context.Context, root string, opts Options) string {
	if c.Memory == nil || opts.NoStore {
		return collect.StoreDisabled
	}
	ctx, cancel := context.WithTimeout(ctx, c.storeTimeout())
	defer cancel()
	notes, err := c.Memory.Recent(ctx, filepath.ToSlash(root), memoryCount)
	if err != nil {
		return StoreFailure(err)
	}
	return MemoryBody(notes)
}

// MemoryBody renders memory notes as the MEMORY section does: a header
// line per note (kind, day, time and branch) and its text indented, cut
// to memoryTextLines lines; no notes is "none".
func MemoryBody(notes []MemoryNote) string {
	if len(notes) == 0 {
		return collect.None
	}
	var lines []string
	for _, n := range notes {
		head := n.Kind
		if head == "" {
			head = "memory"
		}
		day := n.Day
		if day == "" && !n.CreatedAt.IsZero() {
			day = n.CreatedAt.UTC().Format("2006-01-02")
		}
		if day != "" {
			head += " " + day
		}
		if n.Time != "" {
			head += " " + n.Time
		}
		if n.Branch != "" {
			head += " (" + n.Branch + ")"
		}
		lines = append(lines, head)
		text := strings.Split(strings.TrimRight(n.Text, "\n"), "\n")
		shown := text
		if len(text) > memoryTextLines {
			shown = text[:memoryTextLines]
		}
		for _, l := range shown {
			lines = append(lines, "  "+l)
		}
		if more := len(text) - memoryTextLines; more > 0 {
			lines = append(lines, fmt.Sprintf("  ... (%d more lines)", more))
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
