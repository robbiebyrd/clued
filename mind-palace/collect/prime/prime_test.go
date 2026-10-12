package prime

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/collect"
	"github.com/robbiebyrd/clued/mind-palace/collect/envprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/fsprobe"
	"github.com/robbiebyrd/clued/mind-palace/collect/gitprobe"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

var t0 = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

// fakeSessions is an in-test SessionSource.
type fakeSessions struct {
	snap  *SessionSnapshot
	err   error
	block time.Duration // delay before answering
	// honourCtx makes the delay abort on ctx.Done, as a real store does.
	honourCtx bool
	cwd       string
}

func (f *fakeSessions) Current(ctx context.Context, cwd string) (*SessionSnapshot, error) {
	f.cwd = cwd
	if f.block > 0 {
		if f.honourCtx {
			select {
			case <-time.After(f.block):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			time.Sleep(f.block)
		}
	}
	return f.snap, f.err
}

// fakeDocs is an in-test DocSource.
type fakeDocs struct {
	stories, plans []DocSummary
	content        map[string]string
	err            error
}

func (f *fakeDocs) ActiveStories(context.Context) ([]DocSummary, error) { return f.stories, f.err }
func (f *fakeDocs) ActivePlans(context.Context) ([]DocSummary, error)   { return f.plans, f.err }
func (f *fakeDocs) PlanContent(_ context.Context, id string) (string, error) {
	c, ok := f.content[id]
	if !ok {
		return "", errors.New("no content")
	}
	return c, nil
}

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
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lines(n int, prefix string) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}
	return b.String()
}

// repo builds a git repository on main with README.md (90 lines), AGENTS.md,
// go.mod, a docs/plans file and one commit.
func repo(t *testing.T) string {
	t.Helper()
	requireGit(t)
	dir := t.TempDir()
	writeFile(t, dir, "README.md", lines(90, "readme"))
	writeFile(t, dir, "AGENTS.md", "agents\n")
	writeFile(t, dir, "go.mod", "module example.com/x\n")
	writeFile(t, dir, "docs/plans/0001-abc-impl-x.md", "---\nid: 0001-abc\n---\n# x\n")
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-q", "-m", "first")
	return dir
}

type stubRunner struct{}

func (stubRunner) Run(_ context.Context, _ string, name string, _ ...string) (string, error) {
	return name + " v1", nil
}

func collector(t *testing.T, root string) *Collector {
	t.Helper()
	runner := collect.ExecRunner{Timeout: 2 * time.Second}
	return &Collector{
		Root: root,
		FS:   &fsprobe.Probe{Root: root},
		Git:  &gitprobe.Probe{Root: root, Runner: runner},
		Env:  &envprobe.Probe{Root: root, Runner: stubRunner{}},
		Host: func() session.HostInfo {
			ip := "10.0.0.1"
			return session.HostInfo{Hostname: "box", Username: "me", Platform: "linux", Arch: "amd64", OSRelease: "6.1", IP: &ip, MAC: &ip}
		},
		AccountID:         func() string { return "acct-1" },
		ConfigPath:        "/home/me/.claude/plugins/data/clued/config.json",
		DaemonPort:        1, // nothing listens on port 1
		DisabledEnrichers: []string{"privacy-redact"},
		Sessions: &fakeSessions{snap: &SessionSnapshot{
			SessionID: "sess-9", StartedAt: t0, LastSeen: t0.Add(time.Hour), TopCommands: []string{"go test ./...", "ls"},
		}},
		Docs: &fakeDocs{
			stories: []DocSummary{{ID: "0001-fvi", Type: "feat", Status: "in_progress", Title: "Add the story service"}},
			plans: []DocSummary{
				{ID: "0005-15r", Type: "dsgn", Status: "in_progress", Title: "Context collector"},
				{ID: "0002-2ub", Type: "impl", Status: "ready", Title: "Plan service"},
			},
			content: map[string]string{"0005-15r": lines(120, "plan")},
		},
	}
}

func sectionMap(t *testing.T, r collect.Report) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, s := range r.Sections {
		m[s.Name] = s.Body
	}
	return m
}

func TestCollectSectionsInOrder(t *testing.T) {
	root := repo(t)
	r := collector(t, root).Collect(context.Background(), Options{})
	var names []string
	for _, s := range r.Sections {
		names = append(names, s.Name)
	}
	want := []string{"ENV", "CLUED", "FILES", "BRANCH", "LOG:main", "LOG:HEAD", "DIFF-STAT", "STORIES", "PLANS", "SESSION", "BUILD"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("sections = %v, want %v", names, want)
	}
	if !strings.HasPrefix(r.String(), "=== ENV ===\n") {
		t.Fatalf("report does not start with ENV:\n%s", r.String())
	}
}

func TestEnvSection(t *testing.T) {
	root := repo(t)
	runGit(t, root, "remote", "add", "origin", "https://example.com/x.git")
	env := sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))["ENV"]
	for _, want := range []string{
		"cwd: " + collect.DisplayPath(root),
		"project_path: " + session.EncodeProjectPath(filepath.ToSlash(root)),
		"git_origin: https://example.com/x.git",
		"git_branch: main",
		"account_id: acct-1",
		"host: box me linux/amd64 6.1",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("ENV lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "10.0.0.1") {
		t.Errorf("ENV must not print ip or mac:\n%s", env)
	}
}

func TestCluedSection(t *testing.T) {
	root := repo(t)
	c := collector(t, root)
	body := sectionMap(t, c.Collect(context.Background(), Options{}))["CLUED"]
	if !strings.Contains(body, "daemon: not running (port 1)") {
		t.Errorf("closed port must read not running:\n%s", body)
	}
	if !strings.Contains(body, "disabled enrichers: privacy-redact") || !strings.Contains(body, "config: /home/me/.claude/plugins/data/clued/config.json") {
		t.Errorf("CLUED body:\n%s", body)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	c.DaemonPort = ln.Addr().(*net.TCPAddr).Port
	body = sectionMap(t, c.Collect(context.Background(), Options{}))["CLUED"]
	if !strings.Contains(body, fmt.Sprintf("daemon: healthy (port %d)", c.DaemonPort)) {
		t.Errorf("healthy daemon not reported:\n%s", body)
	}
}

func TestFilesSection(t *testing.T) {
	root := repo(t)
	body := sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))["FILES"]
	if !strings.Contains(body, "--- README.md ---\nreadme 1\n") || !strings.Contains(body, "readme 80\n... (10 more lines)") || strings.Contains(body, "readme 81") {
		t.Errorf("README must be truncated at 80 lines with the remainder noted:\n%s", body)
	}
	for _, want := range []string{"SPEC.md: no", "CHANGELOG.md: no", "CLAUDE.md: no", "--- AGENTS.md ---\nagents", "docs/plans: 1 files", "docs/stories: 0 files"} {
		if !strings.Contains(body, want) {
			t.Errorf("FILES lacks %q:\n%s", want, body)
		}
	}
}

func TestBranchLogAndDiffSections(t *testing.T) {
	root := repo(t)
	m := sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))
	if m["BRANCH"] != "main (clean)" {
		t.Errorf("BRANCH = %q", m["BRANCH"])
	}
	if !strings.HasSuffix(m["LOG:main"], " first") || !strings.HasSuffix(m["LOG:HEAD"], " first") {
		t.Errorf("logs = %q / %q", m["LOG:main"], m["LOG:HEAD"])
	}
	if m["DIFF-STAT"] != "no diff (on main)" {
		t.Errorf("DIFF-STAT = %q", m["DIFF-STAT"])
	}

	runGit(t, root, "checkout", "-q", "-b", "feature")
	writeFile(t, root, "new.txt", "hello\n")
	runGit(t, root, "add", "new.txt")
	runGit(t, root, "commit", "-q", "-m", "add new")
	writeFile(t, root, "dirty.txt", "x\n")
	m = sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))
	if m["BRANCH"] != "feature (1 changed file)" {
		t.Errorf("BRANCH = %q", m["BRANCH"])
	}
	if !strings.Contains(m["DIFF-STAT"], "new.txt") {
		t.Errorf("DIFF-STAT = %q", m["DIFF-STAT"])
	}
	if !strings.HasSuffix(m["LOG:main"], " first") || strings.Contains(m["LOG:main"], "add new") {
		t.Errorf("LOG:main should stay on main: %q", m["LOG:main"])
	}
}

func TestDetachedHeadAndNoDefaultBranch(t *testing.T) {
	requireGit(t)
	root := t.TempDir()
	writeFile(t, root, "a.txt", "a\n")
	runGit(t, root, "init", "-q", "-b", "trunk")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "one")
	runGit(t, root, "checkout", "-q", "--detach")
	m := sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))
	if m["BRANCH"] != "detached HEAD (clean)" {
		t.Errorf("BRANCH = %q", m["BRANCH"])
	}
	if m["LOG:base"] != collect.NoDefaultBranch || m["DIFF-STAT"] != collect.NoDefaultBranch {
		t.Errorf("without a default branch: LOG:base=%q DIFF-STAT=%q", m["LOG:base"], m["DIFF-STAT"])
	}
	if !strings.Contains(m["ENV"], "git_branch: none") {
		t.Errorf("ENV branch on detached HEAD: %s", m["ENV"])
	}
}

func TestOutsideGitRepo(t *testing.T) {
	root := t.TempDir()
	m := sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))
	for _, name := range []string{"BRANCH", "LOG:base", "LOG:HEAD", "DIFF-STAT"} {
		if m[name] != collect.NotGitRepo {
			t.Errorf("%s = %q, want %q", name, m[name], collect.NotGitRepo)
		}
	}
	if !strings.Contains(m["ENV"], "git_origin: none") {
		t.Errorf("ENV = %s", m["ENV"])
	}
	if m["BUILD"] != collect.NoBuildTool {
		t.Errorf("BUILD = %q", m["BUILD"])
	}
}

func TestStoriesAndPlansSections(t *testing.T) {
	root := repo(t)
	c := collector(t, root)
	m := sectionMap(t, c.Collect(context.Background(), Options{}))
	if m["STORIES"] != "0001-fvi [feat/in_progress] Add the story service" {
		t.Errorf("STORIES = %q", m["STORIES"])
	}
	plans := m["PLANS"]
	if !strings.HasPrefix(plans, "0005-15r [dsgn/in_progress] Context collector\n0002-2ub [impl/ready] Plan service\n\n--- 0005-15r ---\nplan 1\n") {
		t.Errorf("PLANS head:\n%s", plans)
	}
	if !strings.Contains(plans, "plan 100\n... (20 more lines)") || strings.Contains(plans, "plan 101") {
		t.Errorf("plan content must stop at 100 lines:\n%s", plans)
	}

	c.Docs = &fakeDocs{}
	m = sectionMap(t, c.Collect(context.Background(), Options{}))
	if m["STORIES"] != collect.None || m["PLANS"] != collect.None {
		t.Errorf("empty docs: STORIES=%q PLANS=%q", m["STORIES"], m["PLANS"])
	}

	c.Docs = nil
	m = sectionMap(t, c.Collect(context.Background(), Options{}))
	if m["STORIES"] != collect.NoPlansDir || m["PLANS"] != collect.NoPlansDir {
		t.Errorf("nil docs: STORIES=%q PLANS=%q", m["STORIES"], m["PLANS"])
	}

	c.Docs = &fakeDocs{stories: []DocSummary{{ID: "x"}}}
	m = sectionMap(t, c.Collect(context.Background(), Options{NoDocs: true}))
	if m["STORIES"] != collect.NoPlansDir {
		t.Errorf("NoDocs: STORIES=%q", m["STORIES"])
	}

	c.Docs = &fakeDocs{err: errors.New("boom")}
	m = sectionMap(t, c.Collect(context.Background(), Options{}))
	if m["STORIES"] != "[FALLBACK: STORIES]" || m["PLANS"] != "[FALLBACK: PLANS]" {
		t.Errorf("failing docs: STORIES=%q PLANS=%q", m["STORIES"], m["PLANS"])
	}
}

func TestSessionSection(t *testing.T) {
	root := repo(t)
	c := collector(t, root)
	m := sectionMap(t, c.Collect(context.Background(), Options{}))
	want := "session: sess-9\nstarted: 2026-03-04T05:06:07Z\nlast seen: 2026-03-04T06:06:07Z\ncommands:\n  go test ./...\n  ls"
	if m["SESSION"] != want {
		t.Errorf("SESSION =\n%q\nwant\n%q", m["SESSION"], want)
	}
	if got := c.Sessions.(*fakeSessions).cwd; got != filepath.ToSlash(root) {
		t.Errorf("session looked up for %q, want the absolute root %q", got, root)
	}

	c.Sessions = &fakeSessions{err: errors.New("mongo down")}
	if got := sectionMap(t, c.Collect(context.Background(), Options{}))["SESSION"]; got != collect.StoreUnavailable {
		t.Errorf("failing store: %q", got)
	}
	c.Sessions = &fakeSessions{}
	if got := sectionMap(t, c.Collect(context.Background(), Options{}))["SESSION"]; got != collect.None {
		t.Errorf("no session: %q", got)
	}
	c.Sessions = &fakeSessions{snap: &SessionSnapshot{SessionID: "s"}}
	if got := sectionMap(t, c.Collect(context.Background(), Options{}))["SESSION"]; got != "session: s\ncommands: none" {
		t.Errorf("bare session: %q", got)
	}
	if got := sectionMap(t, c.Collect(context.Background(), Options{NoStore: true}))["SESSION"]; got != collect.StoreDisabled {
		t.Errorf("NoStore: %q", got)
	}
	c.Sessions = nil
	if got := sectionMap(t, c.Collect(context.Background(), Options{}))["SESSION"]; got != collect.StoreDisabled {
		t.Errorf("nil source: %q", got)
	}
}

func TestBuildSection(t *testing.T) {
	root := repo(t)
	if got := sectionMap(t, collector(t, root).Collect(context.Background(), Options{}))["BUILD"]; got != "=== Go Project ===\ngo v1" {
		t.Errorf("BUILD = %q", got)
	}
}

func TestBudgetFallsBackButRendersTheRest(t *testing.T) {
	root := repo(t)
	c := collector(t, root)
	c.Budget = 300 * time.Millisecond
	c.Sessions = &fakeSessions{block: 2 * time.Second}
	start := time.Now()
	m := sectionMap(t, c.Collect(context.Background(), Options{}))
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("Collect ignored the budget")
	}
	if m["SESSION"] != "[FALLBACK: SESSION]" {
		t.Errorf("SESSION = %q", m["SESSION"])
	}
	if !strings.Contains(m["ENV"], "cwd: ") || m["BRANCH"] != "main (clean)" {
		t.Errorf("earlier sections must still render: ENV=%q BRANCH=%q", m["ENV"], m["BRANCH"])
	}
	if len(m) != 11 {
		t.Errorf("report has %d sections, want 11", len(m))
	}
}

func TestSlowStoreHitsTheStoreTimeoutNotTheBudget(t *testing.T) {
	root := repo(t)
	c := collector(t, root)
	c.Sessions = &fakeSessions{block: StoreTimeout + 5*time.Second, honourCtx: true}
	start := time.Now()
	m := sectionMap(t, c.Collect(context.Background(), Options{}))
	if elapsed := time.Since(start); elapsed > StoreTimeout+2*time.Second {
		t.Fatalf("Collect took %v, the store timeout did not fire", elapsed)
	}
	if m["SESSION"] != collect.StoreUnavailable {
		t.Errorf("SESSION = %q", m["SESSION"])
	}
	if m["BUILD"] == "" {
		t.Error("later sections must still render")
	}
}

func TestPanickingSectionFallsBack(t *testing.T) {
	name, body := withBudget(context.Background(), "X", func(context.Context) (string, string) { panic("boom") })
	if name != "X" || body != "[FALLBACK: X]" {
		t.Errorf("panic → %q %q", name, body)
	}
}

func TestNilProbes(t *testing.T) {
	c := &Collector{Root: t.TempDir()}
	m := sectionMap(t, c.Collect(context.Background(), Options{}))
	if len(m) != 11 {
		t.Fatalf("sections = %d", len(m))
	}
	if m["FILES"] != "[FALLBACK: FILES]" || m["BRANCH"] != collect.NotGitRepo || m["SESSION"] != collect.StoreDisabled {
		t.Errorf("nil probes: %v", m)
	}
	if strings.Contains(m["ENV"], "account_id") || strings.Contains(m["ENV"], "host:") {
		t.Errorf("nil account and host funcs must omit their lines: %s", m["ENV"])
	}
	if !strings.Contains(m["CLUED"], "daemon: not configured") {
		t.Errorf("CLUED = %s", m["CLUED"])
	}
}
