package ghapp_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/ghapp"
	"github.com/robbiebyrd/clued/mind-palace/ghapp/ghapptest"
)

// --- events ------------------------------------------------------------------

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "events", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parseFixture[T ghapp.Event](t *testing.T, event, file string) T {
	t.Helper()
	ev, err := ghapp.ParseEvent(event, fixture(t, file))
	if err != nil {
		t.Fatalf("ParseEvent(%s): %v", event, err)
	}
	typed, ok := ev.(T)
	if !ok {
		t.Fatalf("ParseEvent(%s) = %T", event, ev)
	}
	if ev.EventName() != event {
		t.Errorf("EventName = %q, want %q", ev.EventName(), event)
	}
	// Nothing outside the typed fields survives: no e-mail address, no body.
	out, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"me@example.com", "noreply@github.com", "Long description"} {
		if strings.Contains(string(out), leak) {
			t.Errorf("%s re-encoded payload keeps %q", event, leak)
		}
	}
	// The re-encoded payload decodes to the same value.
	again, err := ghapp.ParseEvent(event, out)
	if err != nil {
		t.Fatalf("re-decode %s: %v", event, err)
	}
	if b, _ := json.Marshal(again); string(b) != string(out) {
		t.Errorf("%s does not round-trip:\n%s\n%s", event, out, b)
	}
	return typed
}

const (
	shaA = "1111111111111111111111111111111111111111"
	shaB = "2222222222222222222222222222222222222222"
	shaC = "3333333333333333333333333333333333333333"
)

func checkEnvelope(t *testing.T, ev ghapp.Event, action string) {
	t.Helper()
	if ev.EventAction() != action {
		t.Errorf("action = %q, want %q", ev.EventAction(), action)
	}
	if ev.InstallationID() != 55501234 {
		t.Errorf("installation = %d", ev.InstallationID())
	}
	if ev.RepositoryID() != 812345678 {
		t.Errorf("repository = %d", ev.RepositoryID())
	}
}

func TestParsePush(t *testing.T) {
	ev := parseFixture[*ghapp.PushEvent](t, ghapp.EventPush, "push.json")
	checkEnvelope(t, ev, "")
	if br, ok := ev.Branch(); !ok || br != "main" {
		t.Errorf("Branch = %q, %v", br, ok)
	}
	if ev.Before != shaA || ev.After != shaB || ev.Forced || ev.Created || ev.Deleted {
		t.Errorf("refs = %+v", ev)
	}
	if ev.Pusher.Name != "robbiebyrd" || ev.Sender == nil || ev.Sender.Login != "robbiebyrd" {
		t.Errorf("pusher %+v sender %+v", ev.Pusher, ev.Sender)
	}
	if len(ev.Commits) != 2 || ev.Commits[0].ID != shaC || ev.Commits[0].Author.Username != "robbiebyrd" {
		t.Fatalf("commits = %+v", ev.Commits)
	}
	if !slices.Equal(ev.Commits[0].Added, []string{"docs/new.md"}) || !slices.Equal(ev.Commits[1].Removed, []string{"old.txt"}) {
		t.Errorf("commit files = %+v", ev.Commits)
	}
	if want := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC); !ev.Commits[0].Timestamp.Equal(want) {
		t.Errorf("timestamp = %v", ev.Commits[0].Timestamp)
	}
	// Push payloads carry the repository's times as Unix seconds.
	if want := time.Unix(1792411200, 0); !ev.Repository.PushedAt.Equal(want) {
		t.Errorf("pushed_at = %v", ev.Repository.PushedAt)
	}
	if ev.Repository.DefaultBranch != "main" || ev.Repository.Owner.Login != "robbiebyrd" {
		t.Errorf("repository = %+v", ev.Repository)
	}
	if ev.HeadCommit == nil || ev.HeadCommit.ID != shaB {
		t.Errorf("head commit = %+v", ev.HeadCommit)
	}
}

func TestParseCreateDelete(t *testing.T) {
	c := parseFixture[*ghapp.CreateEvent](t, ghapp.EventCreate, "create.json")
	checkEnvelope(t, c, "")
	if c.Ref != "feature/x" || c.RefType != "branch" || c.MasterBranch != "main" {
		t.Errorf("create = %+v", c)
	}
	d := parseFixture[*ghapp.DeleteEvent](t, ghapp.EventDelete, "delete.json")
	checkEnvelope(t, d, "")
	if d.Ref != "feature/x" || d.RefType != "branch" {
		t.Errorf("delete = %+v", d)
	}
}

func TestParsePullRequest(t *testing.T) {
	ev := parseFixture[*ghapp.PullRequestEvent](t, ghapp.EventPullRequest, "pull_request.json")
	checkEnvelope(t, ev, "closed")
	pr := ev.PullRequest
	if ev.Number != 42 || pr.Number != 42 || !pr.Merged || pr.MergeCommitSHA != shaC || pr.State != "closed" {
		t.Errorf("pull request = %+v", pr)
	}
	if pr.Head.Ref != "feature/x" || pr.Head.SHA != shaB || pr.Base.Ref != "main" || pr.Base.SHA != shaA {
		t.Errorf("head/base = %+v / %+v", pr.Head, pr.Base)
	}
	if pr.MergedAt.IsZero() || pr.User == nil || pr.User.Login != "robbiebyrd" {
		t.Errorf("merged_at %v user %+v", pr.MergedAt, pr.User)
	}
}

func TestParseCI(t *testing.T) {
	cs := parseFixture[*ghapp.CheckSuiteEvent](t, ghapp.EventCheckSuite, "check_suite.json")
	checkEnvelope(t, cs, "completed")
	if cs.CheckSuite.HeadSHA != shaB || cs.CheckSuite.Conclusion != "success" || cs.CheckSuite.App == nil || cs.CheckSuite.App.Slug != "github-actions" {
		t.Errorf("check suite = %+v", cs.CheckSuite)
	}
	cr := parseFixture[*ghapp.CheckRunEvent](t, ghapp.EventCheckRun, "check_run.json")
	checkEnvelope(t, cr, "completed")
	if cr.CheckRun.Name != "test" || cr.CheckRun.Conclusion != "failure" || cr.CheckRun.CheckSuite == nil || cr.CheckRun.CheckSuite.HeadBranch != "main" {
		t.Errorf("check run = %+v", cr.CheckRun)
	}
	st := parseFixture[*ghapp.StatusEvent](t, ghapp.EventStatus, "status.json")
	checkEnvelope(t, st, "")
	if st.SHA != shaB || st.State != "success" || st.Context != "ci/external" || len(st.Branches) != 1 || st.Branches[0].Name != "main" {
		t.Errorf("status = %+v", st)
	}
	wr := parseFixture[*ghapp.WorkflowRunEvent](t, ghapp.EventWorkflowRun, "workflow_run.json")
	checkEnvelope(t, wr, "completed")
	if wr.WorkflowRun.Name != "mind-palace" || wr.WorkflowRun.HeadSHA != shaB || wr.WorkflowRun.Conclusion != "success" || wr.WorkflowRun.RunNumber != 321 {
		t.Errorf("workflow run = %+v", wr.WorkflowRun)
	}
	if wr.Workflow == nil || wr.Workflow.Path != ".github/workflows/mind-palace.yml" {
		t.Errorf("workflow = %+v", wr.Workflow)
	}
}

func TestParseInstallationEvents(t *testing.T) {
	ev := parseFixture[*ghapp.InstallationEvent](t, ghapp.EventInstallation, "installation.json")
	if ev.EventAction() != "created" || ev.InstallationID() != 55501234 || ev.RepositoryID() != 0 {
		t.Errorf("installation envelope: %q %d %d", ev.EventAction(), ev.InstallationID(), ev.RepositoryID())
	}
	inst := ev.Installation
	if inst.Account.Login != "robbiebyrd" || inst.AppID != 424242 || inst.Suspended() || inst.Permissions["contents"] != "read" {
		t.Errorf("installation = %+v", inst)
	}
	if len(ev.Repositories) != 2 || ev.Repositories[1].FullName != "robbiebyrd/clued-cloud" {
		t.Errorf("repositories = %+v", ev.Repositories)
	}
	if ev.Sender == nil || ev.Sender.ID != 1234567 {
		t.Errorf("sender = %+v", ev.Sender)
	}
	ir := parseFixture[*ghapp.InstallationRepositoriesEvent](t, ghapp.EventInstallationRepositories, "installation_repositories.json")
	if ir.EventAction() != "added" || ir.InstallationID() != 55501234 || len(ir.RepositoriesAdded) != 1 || ir.RepositoriesAdded[0].ID != 812345680 || len(ir.RepositoriesRemoved) != 0 {
		t.Errorf("installation_repositories = %+v", ir)
	}
}

func TestParseRepository(t *testing.T) {
	ev := parseFixture[*ghapp.RepositoryEvent](t, ghapp.EventRepository, "repository.json")
	checkEnvelope(t, ev, "renamed")
	if ev.PreviousName() != "clued" || ev.Repository.Name != "clued2" || ev.PreviousOwner() != "" {
		t.Errorf("renamed: from %q to %q", ev.PreviousName(), ev.Repository.Name)
	}
	tr := parseFixture[*ghapp.RepositoryEvent](t, ghapp.EventRepository, "repository_transferred.json")
	if tr.EventAction() != "transferred" || tr.PreviousOwner() != "space-hippo" || tr.PreviousName() != "" {
		t.Errorf("transferred: %q from %q", tr.EventAction(), tr.PreviousOwner())
	}
	p := parseFixture[*ghapp.PingEvent](t, ghapp.EventPing, "ping.json")
	if p.Zen == "" || p.HookID != 123456 || p.InstallationID() != 0 {
		t.Errorf("ping = %+v", p)
	}
}

func TestParseEventErrors(t *testing.T) {
	if _, err := ghapp.ParseEvent("issues", []byte(`{}`)); !errors.Is(err, ghapp.ErrUnknownEvent) {
		t.Errorf("unknown event: %v", err)
	}
	if _, err := ghapp.ParseEvent(ghapp.EventPush, []byte(`{"ref":`)); err == nil {
		t.Error("truncated payload decoded")
	}
	if _, err := ghapp.ParseEvent(ghapp.EventPush, []byte(`{"repository":{"pushed_at":"yesterday"}}`)); err == nil {
		t.Error("bad timestamp decoded")
	}
}

func TestTimestamp(t *testing.T) {
	var v struct {
		A, B, C, D ghapp.Timestamp
	}
	if err := json.Unmarshal([]byte(`{"A":"2026-10-19T12:00:00Z","B":1792411200,"C":null,"D":""}`), &v); err != nil {
		t.Fatal(err)
	}
	if !v.A.Equal(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)) || !v.B.Equal(time.Unix(1792411200, 0)) || !v.C.IsZero() || !v.D.IsZero() {
		t.Errorf("decoded %+v", v)
	}
	b, _ := json.Marshal(v)
	if want := `{"A":"2026-10-19T12:00:00Z","B":"2026-10-19T12:00:00Z","C":null,"D":null}`; string(b) != want {
		t.Errorf("encoded %s, want %s", b, want)
	}
}

// --- client against the fake ------------------------------------------------

type world struct {
	fake *ghapptest.Fake
	app  *ghapp.App
	repo *ghapptest.Repo
	inst ghapp.Installation
}

func newWorld(t *testing.T) *world {
	t.Helper()
	f := ghapptest.New(t)
	repo := f.AddRepo("octo", "hello")
	inst := f.AddInstallation(ghapp.Installation{}, repo)
	return &world{fake: f, app: f.App(), repo: repo, inst: inst}
}

// client returns an installation client that never really sleeps and
// records the waits it asked for.
func (w *world) client(t *testing.T) (*ghapp.Client, *[]time.Duration) {
	t.Helper()
	c, err := w.app.InstallationClient(context.Background(), w.inst.ID)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var waits []time.Duration
	c.Sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		defer mu.Unlock()
		waits = append(waits, d)
		return nil
	}
	return c, &waits
}

func TestRepositoryAndListings(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	other := w.fake.AddRepo("octo", "world")
	hidden := w.fake.AddRepo("someone", "else")
	w.fake.SetInstallationRepos(w.inst.ID, w.repo, other)
	w.fake.PageSize = 1

	w.repo.Commit("main", "init", ghapptest.Put("README.md", "hi\n"))
	for _, b := range []string{"a", "b", "c"} {
		w.repo.Commit(b, "on "+b, ghapptest.Put(b+".txt", b))
	}
	w.repo.OpenPull("a", "", ghapp.PullRequest{Title: "A"})
	w.repo.OpenPull("b", "", ghapp.PullRequest{})
	w.repo.OpenPull("c", "", ghapp.PullRequest{})
	w.repo.ClosePull(2, true)

	c, _ := w.client(t)
	r, err := c.Repository(ctx, "octo", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID != w.repo.ID() || r.DefaultBranch != "main" || r.FullName != "octo/hello" || r.PushedAt.IsZero() {
		t.Errorf("repository = %+v", r)
	}
	if byID, err := c.RepositoryByID(ctx, w.repo.ID()); err != nil || byID.FullName != "octo/hello" {
		t.Errorf("RepositoryByID = %+v, %v", byID, err)
	}
	if _, err := c.Repository(ctx, "someone", "else"); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("repository outside the installation: %v", err)
	}
	if _, err := c.RepositoryByID(ctx, hidden.ID()); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("repository by id outside the installation: %v", err)
	}

	repos, err := c.InstallationRepositories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 || repos[0].Name != "hello" || repos[1].Name != "world" {
		t.Errorf("installation repositories = %+v", repos)
	}

	branches, err := c.Branches(ctx, "octo", "hello")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range branches {
		names = append(names, b.Name)
		if b.Commit.SHA != w.repo.Head(b.Name) {
			t.Errorf("branch %s at %s, want %s", b.Name, b.Commit.SHA, w.repo.Head(b.Name))
		}
	}
	if !slices.Equal(names, []string{"a", "b", "c", "main"}) {
		t.Errorf("branches = %v", names)
	}
	if b, err := c.Branch(ctx, "octo", "hello", "b"); err != nil || b.Commit.SHA != w.repo.Head("b") {
		t.Errorf("Branch = %+v, %v", b, err)
	}
	if _, err := c.Branch(ctx, "octo", "hello", "nope"); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("missing branch: %v", err)
	}

	pulls, err := c.OpenPulls(ctx, "octo", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(pulls) != 2 || pulls[0].Title != "A" || pulls[0].Head.Ref != "a" || pulls[0].Base.Ref != "main" || pulls[1].Number != 3 {
		t.Errorf("open pulls = %+v", pulls)
	}
	if pulls[0].Head.SHA != w.repo.Head("a") {
		t.Errorf("pull head sha = %s", pulls[0].Head.SHA)
	}
	// Each list page was one request: 2 repos, 4 branches, 2 open pulls.
	if n := w.fake.Count(http.MethodGet, "/repos/octo/hello/branches"); n != 4+2 {
		t.Errorf("branch requests = %d", n)
	}
	if rl := c.RateLimit(); rl.Limit != ghapptest.DefaultRateLimit || rl.Remaining >= rl.Limit || rl.Reset.IsZero() || rl.Resource != "core" {
		t.Errorf("rate limit = %+v", rl)
	}
}

func TestTreeBlobAndGitIDs(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	head := w.repo.Commit("main", "init",
		ghapptest.Put("hello.txt", "hello\n"),
		ghapptest.Put("src/a/b.go", "package a\n"),
		ghapptest.Put("src/c.go", "package src\n"),
		ghapptest.PutMode("run.sh", ghapp.ModeExecutable, []byte("#!/bin/sh\n")),
		ghapptest.Symlink("link", "hello.txt"),
		ghapptest.Submodule("vendor/lib", shaC),
	)
	c, _ := w.client(t)

	tree, err := c.Tree(ctx, "octo", "hello", head, true)
	if err != nil {
		t.Fatal(err)
	}
	if tree.Truncated || tree.SHA != w.repo.TreeSHA(head) {
		t.Errorf("tree sha %s truncated %v", tree.SHA, tree.Truncated)
	}
	got := map[string]ghapp.TreeEntry{}
	for _, e := range tree.Entries {
		got[e.Path] = e
	}
	want := map[string][2]string{ // path → mode, type
		"hello.txt": {ghapp.ModeFile, ghapp.TypeBlob}, "link": {ghapp.ModeSymlink, ghapp.TypeBlob},
		"run.sh": {ghapp.ModeExecutable, ghapp.TypeBlob}, "src": {ghapp.ModeTree, ghapp.TypeTree},
		"src/a": {ghapp.ModeTree, ghapp.TypeTree}, "src/a/b.go": {ghapp.ModeFile, ghapp.TypeBlob},
		"src/c.go": {ghapp.ModeFile, ghapp.TypeBlob}, "vendor": {ghapp.ModeTree, ghapp.TypeTree},
		"vendor/lib": {ghapp.ModeSubmodule, ghapp.TypeCommit},
	}
	if len(got) != len(want) {
		t.Errorf("entries = %+v", tree.Entries)
	}
	for p, mt := range want {
		if e := got[p]; e.Mode != mt[0] || e.Type != mt[1] {
			t.Errorf("%s = %+v, want mode %s type %s", p, e, mt[0], mt[1])
		}
	}
	// Blob ids are git's: the same id a client computes from its checkout.
	if e := got["hello.txt"]; e.SHA != "ce013625030ba8dba906f756967f9e9ca394464a" || e.Size != 6 {
		t.Errorf("hello.txt = %+v", e)
	}
	if got["vendor/lib"].SHA != shaC {
		t.Errorf("submodule = %+v", got["vendor/lib"])
	}

	blob, err := c.Blob(ctx, "octo", "hello", got["run.sh"].SHA)
	if err != nil {
		t.Fatal(err)
	}
	if string(blob.Content) != "#!/bin/sh\n" || blob.Size != 10 || blob.SHA != got["run.sh"].SHA {
		t.Errorf("blob = %+v", blob)
	}
	big := strings.Repeat("0123456789", 1000) // wrapped base64 over many lines
	h2 := w.repo.Commit("main", "big", ghapptest.Put("big.txt", big))
	if b, err := c.Blob(ctx, "octo", "hello", w.repo.Files(h2)["big.txt"]); err != nil || string(b.Content) != big {
		t.Errorf("big blob: %v", err)
	}
	if _, err := c.Blob(ctx, "octo", "hello", shaA); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("missing blob: %v", err)
	}

	// Non-recursive by branch name: the root only.
	root, err := c.Tree(ctx, "octo", "hello", "main", false)
	if err != nil {
		t.Fatal(err)
	}
	var ps []string
	for _, e := range root.Entries {
		ps = append(ps, e.Path)
	}
	if !slices.Equal(ps, []string{"big.txt", "hello.txt", "link", "run.sh", "src", "vendor"}) {
		t.Errorf("root entries = %v", ps)
	}
}

// TestTreeSHAMatchesGit checks the fake's tree ids against git itself.
func TestTreeSHAMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	w := newWorld(t)
	files := map[string]string{"a.txt": "a\n", "dir/b.txt": "b\n", "dir/sub/c.txt": "c\n", "dir-x": "x\n", "z/y.md": "# y\n"}
	var changes []ghapptest.Change
	dir := t.TempDir()
	for p, content := range files {
		changes = append(changes, ghapptest.Put(p, content))
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	head := w.repo.Commit("main", "init", changes...)
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	if want := git("write-tree"); w.repo.TreeSHA(head) != want {
		t.Errorf("tree sha = %s, git says %s", w.repo.TreeSHA(head), want)
	}
}

func TestTreeTruncationAndSubtreeWalk(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	var changes []ghapptest.Change
	for _, d := range []string{"a", "b", "c"} {
		for i := range 3 {
			changes = append(changes, ghapptest.Put(fmt.Sprintf("%s/f%d.txt", d, i), d+fmt.Sprint(i)))
		}
	}
	head := w.repo.Commit("main", "init", changes...)
	w.repo.SetTreeLimit(5)
	c, _ := w.client(t)

	tree, err := c.Tree(ctx, "octo", "hello", head, true)
	if err != nil {
		t.Fatal(err)
	}
	if !tree.Truncated || len(tree.Entries) != 5 {
		t.Fatalf("truncated %v with %d entries", tree.Truncated, len(tree.Entries))
	}
	// Walk the root and each subtree instead, as the sync does.
	root, err := c.Tree(ctx, "octo", "hello", head, false)
	if err != nil {
		t.Fatal(err)
	}
	if root.Truncated {
		t.Fatal("non-recursive root truncated")
	}
	var paths []string
	for _, e := range root.Entries {
		if e.Type != ghapp.TypeTree {
			continue
		}
		sub, err := c.Tree(ctx, "octo", "hello", e.SHA, true)
		if err != nil {
			t.Fatal(err)
		}
		if sub.Truncated {
			t.Fatalf("subtree %s truncated", e.Path)
		}
		for _, se := range sub.Entries {
			paths = append(paths, e.Path+"/"+se.Path)
		}
	}
	if len(paths) != 9 {
		t.Errorf("walked %v", paths)
	}
	w.repo.SetTreeLimit(0)
	w.fake.TreeLimit = 100
	if tr, _ := c.Tree(ctx, "octo", "hello", head, true); tr.Truncated || len(tr.Entries) != 12 {
		t.Errorf("under the limit: truncated %v, %d entries", tr.Truncated, len(tr.Entries))
	}
}

func TestCompare(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	base := w.repo.Commit("main", "init",
		ghapptest.Put("keep.txt", "keep"),
		ghapptest.Put("edit.txt", "v1"),
		ghapptest.Put("gone.txt", "bye"),
		ghapptest.Put("old/name.txt", "moved content"),
	)
	w.repo.Commit("main", "one", ghapptest.Put("edit.txt", "v2"), ghapptest.Delete("gone.txt"))
	head := w.repo.Commit("main", "two", ghapptest.Delete("old/name.txt"), ghapptest.Put("new/name.txt", "moved content"), ghapptest.Put("added.txt", "new"))
	c, _ := w.client(t)

	cmp, err := c.Compare(ctx, "octo", "hello", base, head)
	if err != nil {
		t.Fatal(err)
	}
	if cmp.Status != "ahead" || cmp.AheadBy != 2 || cmp.BehindBy != 0 || cmp.TotalCommits != 2 || cmp.Truncated {
		t.Errorf("comparison = %+v", cmp)
	}
	if len(cmp.Commits) != 2 || cmp.Commits[1].SHA != head || cmp.MergeBase.SHA != base {
		t.Errorf("commits = %+v merge base %s", cmp.Commits, cmp.MergeBase.SHA)
	}
	files := w.repo.Files(head)
	type f struct{ status, prev, sha string }
	got := map[string]f{}
	for _, cf := range cmp.Files {
		got[cf.Filename] = f{cf.Status, cf.PreviousFilename, cf.SHA}
	}
	want := map[string]f{
		"added.txt":    {ghapp.FileAdded, "", files["added.txt"]},
		"edit.txt":     {ghapp.FileModified, "", files["edit.txt"]},
		"gone.txt":     {ghapp.FileRemoved, "", ghapptest.GitBlobSHA([]byte("bye"))},
		"new/name.txt": {ghapp.FileRenamed, "old/name.txt", files["new/name.txt"]},
	}
	if len(got) != len(want) {
		t.Errorf("files = %+v", cmp.Files)
	}
	for name, wf := range want {
		if got[name] != wf {
			t.Errorf("%s = %+v, want %+v", name, got[name], wf)
		}
	}

	// Branch names resolve; identical and behind comparisons.
	if same, err := c.Compare(ctx, "octo", "hello", "main", head); err != nil || same.Status != "identical" || len(same.Files) != 0 {
		t.Errorf("identical = %+v, %v", same, err)
	}
	if back, err := c.Compare(ctx, "octo", "hello", head, base); err != nil || back.Status != "behind" || back.BehindBy != 2 {
		t.Errorf("behind = %+v, %v", back, err)
	}

	// A force push: main rewound to base and given another commit diverges.
	w.repo.SetBranch("main", base)
	forced := w.repo.Commit("main", "rewrite", ghapptest.Put("keep.txt", "changed"))
	div, err := c.Compare(ctx, "octo", "hello", head, forced)
	if err != nil {
		t.Fatal(err)
	}
	if div.Status != "diverged" || div.AheadBy != 1 || div.BehindBy != 2 || div.MergeBase.SHA != base {
		t.Errorf("diverged = %+v", div)
	}
	push := w.repo.PushEvent("main", head, forced)
	if !push.Forced || len(push.Commits) != 1 || push.Commits[0].ID != forced {
		t.Errorf("force push event = %+v", push)
	}

	if _, err := c.Compare(ctx, "octo", "hello", shaA, head); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("unknown base: %v", err)
	}
}

func TestCompareTruncatedAtGitHubsCap(t *testing.T) {
	w := newWorld(t)
	base := w.repo.Commit("main", "init", ghapptest.Put("README.md", "x"))
	var changes []ghapptest.Change
	for i := range ghapp.MaxCompareFiles + 5 {
		changes = append(changes, ghapptest.Put(fmt.Sprintf("gen/%04d.txt", i), fmt.Sprint(i)))
	}
	head := w.repo.Commit("main", "many", changes...)
	c, _ := w.client(t)
	cmp, err := c.Compare(context.Background(), "octo", "hello", base, head)
	if err != nil {
		t.Fatal(err)
	}
	if !cmp.Truncated || len(cmp.Files) != ghapp.MaxCompareFiles {
		t.Errorf("truncated %v with %d files", cmp.Truncated, len(cmp.Files))
	}
}

func TestCompareTooLarge(t *testing.T) {
	w := newWorld(t)
	w.repo.Commit("main", "init", ghapptest.Put("a", "a"))
	w.fake.Inject(ghapptest.Fault{Path: "/repos/octo/hello/compare/", Status: http.StatusUnprocessableEntity, Message: "Server Error: Sorry, this diff is taking too long to generate. The diff is too large."})
	c, _ := w.client(t)
	_, err := c.Compare(context.Background(), "octo", "hello", "main", "main")
	if !errors.Is(err, ghapp.ErrTooLarge) || errors.Is(err, ghapp.ErrRateLimited) {
		t.Errorf("err = %v", err)
	}
	var apiErr *ghapp.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnprocessableEntity || apiErr.Path != "/repos/octo/hello/compare/main...main" {
		t.Errorf("api error = %+v", apiErr)
	}
}

func TestContents(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	first := w.repo.Commit("main", "init",
		ghapptest.Put("mind-palace.config.yaml", "remote:\n  project: x\n"),
		ghapptest.Put("docs/plans/0001-a.md", "# A\n"),
		ghapptest.Put("docs/plans/0002-b.md", "# B\n"),
		ghapptest.Put("docs/stories/s.md", "# S\n"),
	)
	w.repo.Commit("main", "more", ghapptest.Put("docs/plans/0003-c.md", "# C\n"), ghapptest.Put("huge.bin", strings.Repeat("x", 1<<20+1)))
	c, _ := w.client(t)

	cfg, err := c.Contents(ctx, "octo", "hello", "mind-palace.config.yaml", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Type != "file" || string(cfg.Content) != "remote:\n  project: x\n" || cfg.SHA != w.repo.Files("main")["mind-palace.config.yaml"] {
		t.Errorf("config = %+v", cfg)
	}
	dir, err := c.Contents(ctx, "octo", "hello", "docs/plans", "main")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range dir.Entries {
		names = append(names, e.Name)
		if e.Type != "file" || e.Path != "docs/plans/"+e.Name {
			t.Errorf("entry = %+v", e)
		}
	}
	if dir.Type != "dir" || !slices.Equal(names, []string{"0001-a.md", "0002-b.md", "0003-c.md"}) {
		t.Errorf("dir = %+v", dir)
	}
	// At an older commit.
	old, err := c.Contents(ctx, "octo", "hello", "docs/plans", first)
	if err != nil || len(old.Entries) != 2 {
		t.Errorf("at %s: %+v, %v", first, old, err)
	}
	root, err := c.Contents(ctx, "octo", "hello", "", "")
	if err != nil || root.Type != "dir" || len(root.Entries) != 3 {
		t.Errorf("root = %+v, %v", root, err)
	}
	if _, err := c.Contents(ctx, "octo", "hello", "docs/nope.md", ""); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := c.Contents(ctx, "octo", "hello", "README.md", "no-such-branch"); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("missing ref: %v", err)
	}
	// Over 1 MB the contents API returns no content: fetch the blob instead.
	_, err = c.Contents(ctx, "octo", "hello", "huge.bin", "")
	if !errors.Is(err, ghapp.ErrTooLarge) {
		t.Fatalf("huge file: %v", err)
	}
	if b, err := c.Blob(ctx, "octo", "hello", w.repo.Files("main")["huge.bin"]); err != nil || len(b.Content) != 1<<20+1 {
		t.Errorf("huge blob: %v", err)
	}
}

// --- retries, rate limits and typed errors -----------------------------------

func TestRetriesServerErrorsWithBackoff(t *testing.T) {
	w := newWorld(t)
	w.repo.Commit("main", "init", ghapptest.Put("a", "a"))
	c, waits := w.client(t)
	w.fake.FailNext("/repos/octo/hello", http.StatusBadGateway, 2)
	if _, err := c.Repository(context.Background(), "octo", "hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*waits, []time.Duration{500 * time.Millisecond, time.Second}) {
		t.Errorf("waits = %v", *waits)
	}

	*waits = nil
	w.fake.FailNext("/repos/octo/hello", http.StatusServiceUnavailable, 0)
	_, err := c.Repository(context.Background(), "octo", "hello")
	var apiErr *ghapp.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable || !apiErr.Temporary() {
		t.Fatalf("err = %v", err)
	}
	if len(*waits) != ghapp.DefaultMaxRetries {
		t.Errorf("retried %d times", len(*waits))
	}
	w.fake.ClearFaults()

	c.MaxRetries = -1
	*waits = nil
	w.fake.FailNext("/repos/octo/hello", http.StatusInternalServerError, 1)
	if _, err := c.Repository(context.Background(), "octo", "hello"); err == nil || len(*waits) != 0 {
		t.Errorf("no retries: err %v waits %v", err, *waits)
	}
}

func TestSecondaryRateLimit(t *testing.T) {
	w := newWorld(t)
	c, waits := w.client(t)
	ctx := context.Background()

	// A short Retry-After is waited out in-process.
	w.fake.RateLimitNext("/repos/", 2*time.Second, 1)
	if _, err := c.Repository(ctx, "octo", "hello"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*waits, []time.Duration{2 * time.Second}) {
		t.Errorf("waits = %v", *waits)
	}

	// A long one is handed back so the caller pauses the installation.
	*waits = nil
	w.fake.RateLimitNext("/repos/", 2*time.Minute, 1)
	_, err := c.Repository(ctx, "octo", "hello")
	if !errors.Is(err, ghapp.ErrRateLimited) || errors.Is(err, ghapp.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if d, ok := ghapp.RetryAfter(err); !ok || d != 2*time.Minute {
		t.Errorf("RetryAfter = %v, %v", d, ok)
	}
	if len(*waits) != 0 {
		t.Errorf("slept %v", *waits)
	}
	// 429 without Retry-After: at least a minute.
	w.fake.FailNext("/repos/", http.StatusTooManyRequests, 1)
	_, err = c.Repository(ctx, "octo", "hello")
	if d, ok := ghapp.RetryAfter(err); !ok || d != time.Minute {
		t.Errorf("429: RetryAfter = %v, %v (%v)", d, ok, err)
	}
	// A plain 403 is not a rate limit.
	w.fake.FailNext("/repos/", http.StatusForbidden, 1)
	_, err = c.Repository(ctx, "octo", "hello")
	if _, ok := ghapp.RetryAfter(err); ok || err == nil {
		t.Errorf("plain 403: %v", err)
	}
}

func TestPrimaryRateLimit(t *testing.T) {
	w := newWorld(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	w.fake.Now = func() time.Time { return now }
	w.app.Now = w.fake.Now
	c, waits := w.client(t)
	c.Now = w.fake.Now
	w.fake.SetRateRemaining(w.inst.ID, 1)
	ctx := context.Background()
	if _, err := c.Repository(ctx, "octo", "hello"); err != nil {
		t.Fatal(err)
	}
	if rl := c.RateLimit(); rl.Remaining != 0 || rl.Used != ghapptest.DefaultRateLimit {
		t.Errorf("rate limit = %+v", rl)
	}
	_, err := c.Repository(ctx, "octo", "hello")
	var apiErr *ghapp.APIError
	if !errors.As(err, &apiErr) || !apiErr.RateLimited || apiErr.Status != http.StatusForbidden {
		t.Fatalf("err = %v", err)
	}
	if !apiErr.Reset.Equal(now.Add(time.Hour)) || apiErr.RetryAfter != time.Hour {
		t.Errorf("reset %v retry after %v", apiErr.Reset, apiErr.RetryAfter)
	}
	if len(*waits) != 0 {
		t.Errorf("slept %v", *waits)
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("message = %v", err)
	}
}

func TestInstallationLifecycleErrors(t *testing.T) {
	w := newWorld(t)
	ctx := context.Background()
	c, _ := w.client(t)

	// A revoked token is refreshed once, transparently.
	w.fake.RevokeTokens()
	if _, err := c.Repository(ctx, "octo", "hello"); err != nil {
		t.Fatalf("after revocation: %v", err)
	}
	if n := w.fake.Count(http.MethodPost, "/app/installations/"); n != 2 {
		t.Errorf("token requests = %d", n)
	}

	// Suspended: 403 on both the token and the calls.
	w.fake.Suspend(w.inst.ID, true)
	if _, err := c.Repository(ctx, "octo", "hello"); err == nil || errors.Is(err, ghapp.ErrRateLimited) {
		t.Errorf("suspended call: %v", err)
	}
	w.app.Invalidate(w.inst.ID)
	_, err := w.app.InstallationClient(ctx, w.inst.ID)
	var tokErr *ghapp.TokenError
	if !errors.As(err, &tokErr) || tokErr.Status != http.StatusForbidden {
		t.Errorf("suspended token: %v", err)
	}
	w.fake.Suspend(w.inst.ID, false)

	// Uninstalled: the token request is a 404, existing tokens a 401.
	c2, _ := w.client(t)
	w.fake.RemoveInstallation(w.inst.ID)
	if _, err := c2.Repository(ctx, "octo", "hello"); !errors.Is(err, ghapp.ErrInstallationNotFound) {
		t.Errorf("call after uninstall: %v", err)
	}
	w.app.Invalidate(w.inst.ID)
	if _, err := w.app.InstallationClient(ctx, w.inst.ID); !errors.Is(err, ghapp.ErrInstallationNotFound) {
		t.Errorf("client after uninstall: %v", err)
	}
}

func TestAppClient(t *testing.T) {
	f := ghapptest.New(t)
	f.PageSize = 2
	var ids []int64
	for i := range 5 {
		inst := f.AddInstallation(ghapp.Installation{Account: ghapp.User{Login: fmt.Sprintf("org%d", i), Type: "Organization"}})
		ids = append(ids, inst.ID)
	}
	f.Suspend(ids[1], true)
	app := f.App()
	c := app.AppClient()
	ctx := context.Background()

	inst, err := c.Installation(ctx, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	if inst.Account.Login != "org1" || inst.Account.Type != "Organization" || !inst.Suspended() || inst.AppID != f.AppID {
		t.Errorf("installation = %+v", inst)
	}
	all, err := c.Installations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 || all[4].ID != ids[4] {
		t.Errorf("installations = %+v", all)
	}
	if _, err := c.Installation(ctx, 999999); !errors.Is(err, ghapp.ErrNotFound) {
		t.Errorf("unknown installation: %v", err)
	}
	// An installation token is not an app credential, and vice versa.
	f.AddRepo("o", "r")
	if _, err := c.Repository(ctx, "o", "r"); !errors.Is(err, ghapp.ErrUnauthorized) {
		t.Errorf("JWT on a repository route: %v", err)
	}
	other := &ghapp.App{ID: f.AppID + 1, Key: f.Key, BaseURL: f.URL, HTTPClient: f.Server.Client()}
	if _, err := other.AppClient().Installation(ctx, ids[0]); !errors.Is(err, ghapp.ErrUnauthorized) {
		t.Errorf("JWT of another app: %v", err)
	}
}

func TestPaginationStaysOnHost(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("per_page") != "100" {
			t.Errorf("per_page = %q", r.URL.Query().Get("per_page"))
		}
		w.Header().Set("Link", `<https://evil.example/repos/o/r/branches?page=2>; rel="next"`)
		_, _ = io.WriteString(w, `[{"name":"main","commit":{"sha":"`+shaA+`"}}]`)
	}))
	defer srv.Close()
	c := ghapp.NewClient(srv.URL, srv.Client(), func(context.Context) (string, error) { return "t", nil })
	if _, err := c.Branches(context.Background(), "o", "r"); err == nil || !strings.Contains(err.Error(), "leaves") {
		t.Errorf("err = %v", err)
	}
}

func TestPaginationPageCap(t *testing.T) {
	w := newWorld(t)
	w.fake.PageSize = 1
	for _, b := range []string{"a", "b", "c"} {
		w.repo.Commit(b, b, ghapptest.Put(b, b))
	}
	c, _ := w.client(t)
	c.MaxPages = 2
	if _, err := c.Branches(context.Background(), "octo", "hello"); err == nil || !strings.Contains(err.Error(), "more than 2 pages") {
		t.Errorf("err = %v", err)
	}
}

func TestRetryAfterHTTPDateAndContextCancel(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", now.Add(3*time.Second).Format(http.TimeFormat))
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"message":"You have exceeded a secondary rate limit."}`)
	}))
	defer srv.Close()
	c := ghapp.NewClient(srv.URL, srv.Client(), func(context.Context) (string, error) { return "t", nil })
	c.Now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	c.Sleep = func(ctx context.Context, d time.Duration) error {
		if d != 3*time.Second {
			t.Errorf("wait = %v", d)
		}
		cancel()
		return ctx.Err()
	}
	_, err := c.Repository(ctx, "o", "r")
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Errorf("err = %v after %d calls", err, calls)
	}
}

// --- webhook delivery from the fake -----------------------------------------

func TestPushEventDelivery(t *testing.T) {
	w := newWorld(t)
	secret := []byte("s3cret")
	first := w.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	second := w.repo.Commit("main", "edit", ghapptest.Put("a.txt", "b"), ghapptest.Put("b.txt", "b"))
	third := w.repo.Commit("main", "drop", ghapptest.Delete("b.txt"))
	feature := w.repo.Commit("feature", "f", ghapptest.Put("f.txt", "f"))

	type got struct {
		delivery string
		ev       ghapp.Event
	}
	received := make(chan got, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, ghapp.MaxWebhookBody))
		if err := ghapp.Verify(secret, body, r.Header.Get(ghapp.SignatureHeader)); err != nil {
			http.Error(rw, err.Error(), http.StatusUnauthorized)
			return
		}
		ev, err := ghapp.ParseEvent(r.Header.Get(ghapp.EventHeader), body)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadRequest)
			return
		}
		received <- got{r.Header.Get(ghapp.DeliveryHeader), ev}
		rw.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	ctx := context.Background()

	push := w.repo.PushEvent("main", first, third)
	resp, err := ghapptest.Deliver(ctx, srv.Client(), srv.URL, secret, "d-1", push)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status %d", resp.StatusCode)
	}
	g := <-received
	pe := g.ev.(*ghapp.PushEvent)
	if g.delivery != "d-1" || pe.InstallationID() != w.inst.ID || pe.RepositoryID() != w.repo.ID() || pe.Forced || pe.Created {
		t.Errorf("push = %+v", pe)
	}
	if len(pe.Commits) != 2 || pe.Commits[0].ID != second || pe.Commits[1].ID != third || pe.HeadCommit.ID != third {
		t.Fatalf("commits = %+v", pe.Commits)
	}
	if !slices.Equal(pe.Commits[0].Modified, []string{"a.txt"}) || !slices.Equal(pe.Commits[0].Added, []string{"b.txt"}) || !slices.Equal(pe.Commits[1].Removed, []string{"b.txt"}) {
		t.Errorf("files = %+v", pe.Commits)
	}

	// A new branch lists only its own commits; a deleted one none.
	created := w.repo.PushEvent("feature", ghapp.ZeroSHA, feature)
	if !created.Created || len(created.Commits) != 1 || created.Commits[0].ID != feature {
		t.Errorf("created = %+v", created)
	}
	w.repo.DeleteBranch("feature")
	deleted := w.repo.PushEvent("feature", feature, ghapp.ZeroSHA)
	if !deleted.Deleted || len(deleted.Commits) != 0 || deleted.HeadCommit != nil {
		t.Errorf("deleted = %+v", deleted)
	}

	// A tampered body is refused by the receiver.
	resp, err = ghapptest.DeliverRaw(ctx, srv.Client(), srv.URL, []byte("wrong"), "", ghapp.EventPush, fixture(t, "push.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong secret: %d", resp.StatusCode)
	}

	// A repository event built from the envelope.
	w.repo.Update(func(r *ghapp.Repository) { r.Name = "renamed" })
	re := &ghapp.RepositoryEvent{Envelope: w.repo.Envelope("renamed", nil)}
	resp, err = ghapptest.Deliver(ctx, srv.Client(), srv.URL, secret, "", re)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	g = <-received
	if g.delivery == "" || g.ev.(*ghapp.RepositoryEvent).Repository.FullName != "octo/renamed" {
		t.Errorf("repository event = %+v", g.ev)
	}
	if _, ok := w.fake.Repo("octo/renamed"); !ok {
		t.Error("renamed repository not found by its new name")
	}
}
