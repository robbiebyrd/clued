package ghsync

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

const docsRepo = "owner/repo"

// fakeRepo is an in-memory repository: commit → path → content. Directories
// are implied by the paths.
type fakeRepo struct {
	mu      sync.Mutex
	commits map[string]map[string]string
	reads   int
	fail    error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{commits: map[string]map[string]string{}} }

// commit stores a commit's tree.
func (r *fakeRepo) commit(sha string, files map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commits[sha] = files
}

func (r *fakeRepo) tree(repo, commit string) (map[string]string, error) {
	if r.fail != nil {
		return nil, r.fail
	}
	if repo != docsRepo {
		return nil, fmt.Errorf("repository %s: %w", repo, fs.ErrNotExist)
	}
	t, ok := r.commits[commit]
	if !ok {
		return nil, fmt.Errorf("commit %s: %w", commit, fs.ErrNotExist)
	}
	return t, nil
}

func (r *fakeRepo) ListDir(_ context.Context, repo, commit, dir string) ([]DocEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, err := r.tree(repo, commit)
	if err != nil {
		return nil, err
	}
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	seen := map[string]bool{}
	var out []DocEntry
	for p := range t {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		name, _, isDir := strings.Cut(rest, "/")
		if seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, DocEntry{Name: name, Dir: isDir})
	}
	if len(out) == 0 && dir != "" {
		return nil, fmt.Errorf("%s: %w", dir, fs.ErrNotExist)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name }) // any order
	return out, nil
}

func (r *fakeRepo) ReadFile(_ context.Context, repo, commit, file string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, err := r.tree(repo, commit)
	if err != nil {
		return nil, err
	}
	c, ok := t[file]
	if !ok {
		return nil, fmt.Errorf("%s: %w", file, fs.ErrNotExist)
	}
	r.reads++
	return []byte(c), nil
}

// docsEnv is a Service whose Docs opener hands out one palace per slug over
// memory stores, and a recorder that keeps every call.
type docsEnv struct {
	svc     *Service
	repo    *fakeRepo
	mu      sync.Mutex
	palaces map[string]*service.Palace
	opened  []string
	records [][]DocsEvent
	recSlug []string
	recErr  error
}

var docsTarget = Target{Kind: TargetUser, ID: "0123456789abcdef", Owner: "0123456789abcdef"}

func newDocsEnv(t *testing.T) *docsEnv {
	t.Helper()
	env := &docsEnv{repo: newFakeRepo(), palaces: map[string]*service.Palace{}}
	c := newClock()
	svc, err := New(Options{
		Jobs:   NewMemJobs(c.Now),
		Claims: claimsFunc(func(context.Context, int64, int64) (Claim, error) { return Claim{}, ErrNotClaimed }),
		Code:   func(context.Context, Target, string) (CodeStore, error) { return nil, nil },
		Docs: func(_ context.Context, target Target, slug string) (*service.Palace, error) {
			if target != docsTarget {
				return nil, fmt.Errorf("unexpected target %+v", target)
			}
			return env.palace(t, slug), nil
		},
		Now: c.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.svc = svc
	return env
}

type claimsFunc func(ctx context.Context, installation, repository int64) (Claim, error)

func (f claimsFunc) Claim(ctx context.Context, installation, repository int64) (Claim, error) {
	return f(ctx, installation, repository)
}

func (e *docsEnv) palace(t *testing.T, slug string) *service.Palace {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	e.opened = append(e.opened, slug)
	if p, ok := e.palaces[slug]; ok {
		return p
	}
	p, err := service.NewPalace(nil, memstore.New("server"), nil)
	if err != nil {
		t.Fatal(err)
	}
	e.palaces[slug] = p
	return p
}

func (e *docsEnv) record(_ context.Context, target Target, slug string, events []DocsEvent) error {
	if target != docsTarget {
		return fmt.Errorf("unexpected target %+v", target)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.records = append(e.records, append([]DocsEvent(nil), events...))
	e.recSlug = append(e.recSlug, slug)
	return e.recErr
}

func (e *docsEnv) sync(t *testing.T, commit, before string) *DocsReport {
	t.Helper()
	rep, err := e.svc.SyncDocs(context.Background(), e.repo, e.record, DocsRequest{Target: docsTarget, Repo: docsRepo, Commit: commit, Before: before, Delivery: "d-" + commit})
	if err != nil {
		t.Fatalf("SyncDocs(%s): %v", commit, err)
	}
	return rep
}

const defaultSlug = "github.com__owner__repo"

func (e *docsEnv) serverDoc(t *testing.T, k, id string) *model.Document {
	t.Helper()
	d, err := e.palace(t, defaultSlug).Store().Get(context.Background(), k, id)
	if err != nil {
		t.Fatalf("server %s %s: %v", k, id, err)
	}
	return d
}

// docFile renders a document file.
func docFile(id, typ, title, status, updated, extra string) string {
	return fmt.Sprintf("---\nid: %s\ntitle: %s\ntype: %s\nstatus: %s\npriority: \"2\"\ncreated: \"2026-10-01T10:00:00.000Z\"\nupdated: %q\n%s---\n\n# %s\n\nBody of %s.\n",
		id, title, typ, status, updated, extra, title, id)
}

func planFile(id, title, updated string) (string, string) {
	return "docs/plans/" + model.FileName(id, "dsgn", model.Slugify(title)), docFile(id, "dsgn", title, "pending", updated, "")
}

func storyFile(id, title, updated string) (string, string) {
	return "docs/stories/" + model.FileName(id, "feat", model.Slugify(title)), docFile(id, "feat", title, "ready", updated, "") + criteria
}

// criteria is the section a story needs to be valid.
const criteria = "\n## Acceptance Criteria\n\n- [ ] It works\n"

func tree(pairs ...string) map[string]string {
	m := map[string]string{"README.md": "# repo\n", "go.mod": "module x\n"}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

// summary renders events as "kind docKind id action" lines, sorted.
func summary(events []DocsEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, strings.TrimSpace(strings.Join([]string{e.Kind, e.DocKind, e.ID, e.Action}, " ")))
	}
	sort.Strings(out)
	return out
}

func wantEvents(t *testing.T, got []DocsEvent, want ...string) {
	t.Helper()
	sort.Strings(want)
	if want == nil {
		want = []string{}
	}
	if s := summary(got); !reflect.DeepEqual(s, want) {
		t.Fatalf("events = %q, want %q\n%+v", s, want, got)
	}
}

func TestSyncDocsImport(t *testing.T) {
	env := newDocsEnv(t)
	p1, c1 := planFile("0001-abc", "First plan", "2026-10-02T10:00:00.000Z")
	s1, c2 := storyFile("0001-xyz", "First story", "2026-10-02T11:00:00Z")
	env.repo.commit("c1", tree(p1, c1, s1, c2, "docs/plans/notes.txt", "not a document", "docs/plans/templates/plan.md", "# template\n"))

	rep := env.sync(t, "c1", strings.Repeat("0", 40))
	if rep.Slug != defaultSlug {
		t.Fatalf("slug = %q", rep.Slug)
	}
	wantEvents(t, rep.Events, "plan_sync plan 0001-abc created", "plan_sync story 0001-xyz created")
	for _, e := range rep.Events {
		if e.Repo != docsRepo || e.Commit != "c1" || e.Delivery != "d-c1" || e.At.IsZero() {
			t.Fatalf("event not stamped: %+v", e)
		}
	}
	if rep.Events[0].Path != p1 {
		t.Fatalf("path = %q, want %q", rep.Events[0].Path, p1)
	}
	if len(env.records) != 1 || env.recSlug[0] != defaultSlug || len(env.records[0]) != 2 {
		t.Fatalf("records = %+v (%v)", env.records, env.recSlug)
	}

	d := env.serverDoc(t, "story", "0001-xyz")
	if d.FrontMatter.Updated != "2026-10-02T11:00:00.000Z" {
		t.Fatalf("updated not normalised: %q", d.FrontMatter.Updated)
	}
	if d.Path != path.Base(s1) {
		t.Fatalf("path = %q", d.Path)
	}
	// The synced documents are valid for the server's services.
	vr, err := env.palace(t, defaultSlug).Plans().Validate(context.Background(), "0001-abc")
	if err != nil || !vr.Valid {
		t.Fatalf("Validate = %+v, %v", vr, err)
	}

	// The same commit again changes nothing and records nothing.
	rep = env.sync(t, "c1", "")
	wantEvents(t, rep.Events)
	if len(env.records) != 1 {
		t.Fatalf("recorder called for a no-op sync: %d", len(env.records))
	}
	if got := rep.Sync.Documents["plan"].Unchanged; !reflect.DeepEqual(got, []string{"0001-abc"}) {
		t.Fatalf("unchanged = %v", got)
	}
}

func TestSyncDocsNewer(t *testing.T) {
	env := newDocsEnv(t)
	ctx := context.Background()
	p1, c1 := planFile("0001-abc", "First plan", "2026-10-02T10:00:00.000Z")
	p2, c2 := planFile("0002-def", "Second plan", "2026-10-02T10:00:00.000Z")
	env.repo.commit("c1", tree(p1, c1, p2, c2))
	env.sync(t, "c1", "")

	// An MCP edit to 0001 after the commit, not merged yet.
	plans := env.palace(t, defaultSlug).Plans()
	plans.SetClock(func() time.Time { return mustTime(t, "2026-10-05T09:00:00Z") })
	if _, err := plans.SetPriority(ctx, "0001-abc", "1"); err != nil {
		t.Fatal(err)
	}

	// The repository changes both: 0001 with an older timestamp than the
	// MCP edit, 0002 with a newer one.
	p1b, c1b := planFile("0001-abc", "First plan", "2026-10-04T10:00:00.000Z")
	p2b, c2b := planFile("0002-def", "Second plan", "2026-10-04T10:00:00.000Z")
	c1b = strings.Replace(c1b, "Body of", "Edited body of", 1)
	c2b = strings.Replace(c2b, "Body of", "Edited body of", 1)
	env.repo.commit("c2", tree(p1b, c1b, p2b, c2b))
	rep := env.sync(t, "c2", "c1")
	wantEvents(t, rep.Events, "plan_sync_conflict plan 0001-abc", "plan_sync plan 0002-def updated")

	if d := env.serverDoc(t, "plan", "0001-abc"); d.FrontMatter.Priority != "1" || strings.Contains(d.Content, "Edited") {
		t.Fatalf("the newer MCP edit was overwritten: %+v", d.FrontMatter)
	}
	if d := env.serverDoc(t, "plan", "0002-def"); !strings.Contains(d.Content, "Edited body") {
		t.Fatalf("0002 not updated: %q", d.Content)
	}

	// The merged edit (same or later timestamp) wins again.
	p1c, c1c := planFile("0001-abc", "First plan", "2026-10-05T09:00:00.000Z")
	c1c = strings.Replace(c1c, "priority: \"2\"", "priority: \"0\"", 1)
	env.repo.commit("c3", tree(p1c, c1c, p2b, c2b))
	rep = env.sync(t, "c3", "c2")
	wantEvents(t, rep.Events, "plan_sync plan 0001-abc updated")
	if d := env.serverDoc(t, "plan", "0001-abc"); d.FrontMatter.Priority != "0" {
		t.Fatalf("priority = %q", d.FrontMatter.Priority)
	}
}

func TestSyncDocsInvalidSkipped(t *testing.T) {
	env := newDocsEnv(t)
	good, gc := planFile("0001-abc", "Good plan", "2026-10-02T10:00:00Z")
	badH1Path, badH1 := planFile("0002-bad", "Bad heading", "2026-10-02T10:00:00Z")
	badH1 = strings.Replace(badH1, "# Bad heading", "# Something else", 1)
	badStatusPath, badStatus := storyFile("0003-bst", "Bad status", "2026-10-02T10:00:00Z")
	badStatus = strings.Replace(badStatus, "status: ready", "status: nonsense", 1)
	brokenPath := "docs/stories/0004-brk-feat-broken.md"
	broken := "---\nid: [unclosed\n---\n# Broken\n"
	mismatchPath := "docs/stories/0005-mis-feat-mismatch.md"
	mismatch := docFile("0006-oth", "feat", "Mismatch", "ready", "2026-10-02T10:00:00Z", "") + criteria
	// A link to a plan the repository does not hold is not a reason to skip.
	linkedPath, linked := storyFile("0007-lnk", "Linked story", "2026-10-02T10:00:00Z")
	linked = strings.Replace(linked, "priority:", "links:\n  plans:\n    - [\"0099-zzz\", \"included\"]\npriority:", 1)
	dupPath := "docs/plans/archive/0001-abc-dsgn-old-copy.md"
	env.repo.commit("c1", tree(good, gc, badH1Path, badH1, badStatusPath, badStatus, brokenPath, broken, mismatchPath, mismatch, linkedPath, linked, dupPath, "whatever"))

	rep := env.sync(t, "c1", "")
	wantEvents(t, rep.Events,
		"plan_sync plan 0001-abc created",
		"plan_sync story 0007-lnk created",
		"plan_sync_problem plan 0001-abc",
		"plan_sync_problem plan 0002-bad",
		"plan_sync_problem story 0003-bst",
		"plan_sync_problem story 0004-brk",
		"plan_sync_problem story 0005-mis",
	)
	for _, e := range rep.Events {
		if e.Kind == EventPlanSyncProblem && len(e.Problems) == 0 {
			t.Fatalf("problem without a message: %+v", e)
		}
		if e.Kind == EventPlanSyncProblem && e.ID == "0001-abc" && e.Path != dupPath {
			t.Fatalf("duplicate reported at %q", e.Path)
		}
	}
	ctx := context.Background()
	st := env.palace(t, defaultSlug).Store()
	for _, id := range []string{"0003-bst", "0004-brk", "0005-mis", "0006-oth"} {
		if _, err := st.Get(ctx, "story", id); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("invalid story %s written: %v", id, err)
		}
	}
	if _, err := st.Get(ctx, "plan", "0002-bad"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("invalid plan written: %v", err)
	}
}

func TestSyncDocsArchivesDeleted(t *testing.T) {
	env := newDocsEnv(t)
	ctx := context.Background()
	p1, c1 := planFile("0001-abc", "Kept plan", "2026-10-02T10:00:00Z")
	p2, c2 := planFile("0002-def", "Deleted plan", "2026-10-02T10:00:00Z")
	s1, sc1 := storyFile("0001-xyz", "Deleted story", "2026-10-02T10:00:00Z")
	s2, sc2 := storyFile("0002-mov", "Moved story", "2026-10-02T10:00:00Z")
	env.repo.commit("c1", tree(p1, c1, p2, c2, s1, sc1, s2, sc2))
	env.sync(t, "c1", "")

	// A story created through MCP and not merged: not in the repository.
	stories := env.palace(t, defaultSlug).Stories()
	input, err := os.ReadFile("../testdata/create-story.json")
	if err != nil {
		t.Fatal(err)
	}
	created, err := stories.Create(ctx, input, "")
	if err != nil {
		t.Fatal(err)
	}

	// c2 deletes 0002-def and 0001-xyz, archives 0002-mov in the repository.
	moved := strings.Replace(sc2, "status: ready", "status: archived", 1)
	env.repo.commit("c2", tree(p1, c1, "docs/stories/archive/"+path.Base(s2), moved))
	rep := env.sync(t, "c2", "c1")
	wantEvents(t, rep.Events,
		"plan_sync plan 0002-def archived",
		"plan_sync story 0001-xyz archived",
		"plan_sync story 0002-mov updated",
	)
	if !reflect.DeepEqual(rep.Archived, []string{"plan 0002-def", "story 0001-xyz"}) {
		t.Fatalf("archived = %v", rep.Archived)
	}
	for _, c := range []struct{ k, id string }{{"plan", "0002-def"}, {"story", "0001-xyz"}, {"story", "0002-mov"}} {
		d := env.serverDoc(t, c.k, c.id)
		if d.FrontMatter.Status != "archived" || !strings.HasPrefix(d.Path, "archive/") {
			t.Fatalf("%s %s = %q at %q", c.k, c.id, d.FrontMatter.Status, d.Path)
		}
	}
	if d := env.serverDoc(t, "plan", "0001-abc"); d.FrontMatter.Status != "pending" {
		t.Fatalf("kept plan status = %q", d.FrontMatter.Status)
	}
	if d := env.serverDoc(t, "story", created.ID()); d.FrontMatter.Status != created.FrontMatter.Status {
		t.Fatalf("unmerged MCP story touched: %q", d.FrontMatter.Status)
	}

	// Syncing c2 again after c1 archives nothing new.
	rep = env.sync(t, "c2", "c1")
	wantEvents(t, rep.Events)

	// A first push (before all zeros) or a vanished before archives nothing.
	env.repo.commit("c3", tree())
	rep = env.sync(t, "c3", strings.Repeat("0", 40))
	wantEvents(t, rep.Events)
	rep = env.sync(t, "c3", "gone")
	wantEvents(t, rep.Events)
	if d := env.serverDoc(t, "plan", "0001-abc"); d.FrontMatter.Status != "pending" {
		t.Fatalf("plan archived without a before: %q", d.FrontMatter.Status)
	}
}

func TestSyncDocsRepoConfig(t *testing.T) {
	env := newDocsEnv(t)
	cfg := strings.Join([]string{
		"plans:",
		"  dir: ./planning/plans",
		"stories:",
		"  dir: planning/stories",
		"  types:",
		"    - name: epic",
		"      label: Epic",
		"remote:",
		"  project: Acme/Board",
		"github:",
		"  branches: default",
		"codeSync: false",
		"",
	}, "\n")
	ignored, ic := planFile("0009-ign", "Ignored plan", "2026-10-02T10:00:00Z")
	story := "planning/stories/" + model.FileName("0001-epc", "epic", "an-epic")
	env.repo.commit("c1", tree("mind-palace.config.yaml", cfg,
		"planning/plans/"+path.Base(ignored), ic,
		story, docFile("0001-epc", "epic", "An epic", "ready", "2026-10-02T10:00:00Z", "")+criteria,
		ignored, ic))
	rep := env.sync(t, "c1", "")
	if rep.Slug != "acme__board" {
		t.Fatalf("slug = %q", rep.Slug)
	}
	wantEvents(t, rep.Events, "plan_sync plan 0009-ign created", "plan_sync story 0001-epc created")
	if rep.Events[0].Path != "planning/plans/"+path.Base(ignored) {
		t.Fatalf("path = %q", rep.Events[0].Path)
	}
	if _, ok := env.palaces[defaultSlug]; ok {
		t.Fatal("the default slug was opened despite remote.project")
	}

	// A JSON config is read as JSON.
	env.repo.commit("c2", tree("mind-palace.config.json", `{"plans":{"dir":"p"},"github":{"activeDays":3}}`,
		"p/"+path.Base(ignored), ic))
	rep = env.sync(t, "c2", "")
	if rep.Slug != defaultSlug {
		t.Fatalf("slug = %q", rep.Slug)
	}
	wantEvents(t, rep.Events, "plan_sync plan 0009-ign created")
}

func TestSyncDocsInvalidConfig(t *testing.T) {
	for name, cfg := range map[string]string{
		"unknown key":     "plans:\n  bogus: 1\n",
		"bad github":      "github:\n  branches: some\n",
		"escaping dir":    "plans:\n  dir: ../elsewhere\n",
		"not yaml":        "plans: [\n",
		"absolute dir":    "stories:\n  dir: /etc\n",
		"bad kind config": "plans:\n  statuses: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			env := newDocsEnv(t)
			p1, c1 := planFile("0001-abc", "First plan", "2026-10-02T10:00:00Z")
			env.repo.commit("c1", tree("mind-palace.config.yaml", cfg, p1, c1))
			rep := env.sync(t, "c1", "")
			if len(rep.Events) == 0 {
				t.Fatal("no events")
			}
			for _, e := range rep.Events {
				if e.Kind != EventPlanSyncProblem || e.Path != "mind-palace.config.yaml" || len(e.Problems) == 0 {
					t.Fatalf("event = %+v, want a config problem", e)
				}
			}
			if p, ok := env.palaces[defaultSlug]; ok {
				if _, err := p.Store().Get(context.Background(), "plan", "0001-abc"); err == nil {
					t.Fatal("documents synced under an invalid config")
				}
			}
			if len(env.records) != 1 {
				t.Fatalf("problem not recorded: %d", len(env.records))
			}
		})
	}
}

func TestSyncDocsErrors(t *testing.T) {
	env := newDocsEnv(t)
	ctx := context.Background()
	p1, c1 := planFile("0001-abc", "First plan", "2026-10-02T10:00:00Z")
	env.repo.commit("c1", tree(p1, c1))
	ok := DocsRequest{Target: docsTarget, Repo: docsRepo, Commit: "c1"}

	if _, err := env.svc.SyncDocs(ctx, nil, nil, ok); err == nil {
		t.Fatal("nil fetcher accepted")
	}
	bad := ok
	bad.Target = Target{Kind: TargetUser, ID: "a", Owner: "b"}
	if _, err := env.svc.SyncDocs(ctx, env.repo, nil, bad); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("invalid target: %v", err)
	}
	bad = ok
	bad.Commit = ""
	if _, err := env.svc.SyncDocs(ctx, env.repo, nil, bad); err == nil {
		t.Fatal("missing commit accepted")
	}
	bad = ok
	bad.Slug = "../escape"
	if _, err := env.svc.SyncDocs(ctx, env.repo, nil, bad); err == nil {
		t.Fatal("invalid slug accepted")
	}
	// A missing commit is an error a retry may fix (fs.ErrNotExist).
	bad = ok
	bad.Commit = "nope"
	if _, err := env.svc.SyncDocs(ctx, env.repo, nil, bad); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing commit: %v", err)
	}
	// A failing fetcher is an error.
	env.repo.fail = errors.New("rate limited")
	if _, err := env.svc.SyncDocs(ctx, env.repo, nil, ok); err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("fetch failure: %v", err)
	}
	env.repo.fail = nil

	// A nil recorder is fine; the report still lists the events.
	rep, err := env.svc.SyncDocs(ctx, env.repo, nil, ok)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents(t, rep.Events, "plan_sync plan 0001-abc created")

	// A recorder failure is returned after the sync.
	p2, c2 := planFile("0002-def", "Second plan", "2026-10-02T10:00:00Z")
	env.repo.commit("c2", tree(p1, c1, p2, c2))
	env.recErr = errors.New("log down")
	if _, err := env.svc.SyncDocs(ctx, env.repo, env.record, DocsRequest{Target: docsTarget, Repo: docsRepo, Commit: "c2"}); err == nil || !strings.Contains(err.Error(), "log down") {
		t.Fatalf("recorder failure: %v", err)
	}
	if _, err := env.palace(t, defaultSlug).Store().Get(ctx, "plan", "0002-def"); err != nil {
		t.Fatalf("sync not done before recording: %v", err)
	}

	// A failing project store is an error.
	failing := memstore.New("down")
	failing.FailWrites = errors.New("disk full")
	p, err := service.NewPalace(nil, failing, nil)
	if err != nil {
		t.Fatal(err)
	}
	env.mu.Lock()
	env.palaces["github.com__owner__other"] = p
	env.mu.Unlock()
	if _, err := env.svc.SyncDocs(ctx, env.repo, nil, DocsRequest{Target: docsTarget, Repo: docsRepo, Slug: "github.com__owner__other", Commit: "c1"}); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("store failure: %v", err)
	}
}

func TestDocsTouched(t *testing.T) {
	cases := []struct {
		changed []string
		dirs    []string
		want    bool
	}{
		{[]string{"main.go", "docs/plans/0001-abc-dsgn-x.md"}, nil, true},
		{[]string{"docs/stories/archive/0001-abc-feat-x.md"}, nil, true},
		{[]string{"docs/plansx/a.md", "README.md"}, nil, false},
		{[]string{"mind-palace.config.yaml"}, nil, true},
		{[]string{"sub/mind-palace.config.yaml"}, nil, false},
		{[]string{"planning/p/1.md"}, []string{"./planning/p"}, true},
		{[]string{"docs/plans/1.md"}, []string{"planning/p"}, false},
		{nil, nil, false},
	}
	for _, c := range cases {
		if got := DocsTouched(c.changed, c.dirs...); got != c.want {
			t.Errorf("DocsTouched(%v, %v) = %v, want %v", c.changed, c.dirs, got, c.want)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
