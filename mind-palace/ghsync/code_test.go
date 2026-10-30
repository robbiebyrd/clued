package ghsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/ghapp"
	"github.com/robbiebyrd/clued/mind-palace/ghapp/ghapptest"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

var codeTarget = Target{Kind: TargetUser, ID: "fedcba9876543210", Owner: "fedcba9876543210"}

const codeSlug = "github.com__octo__app"

// codeEnv is a Service over a fake GitHub with one claimed installation of
// one repository (octo/app), memory code stores and palaces per slug.
type codeEnv struct {
	t     *testing.T
	f     *ghapptest.Fake
	repo  *ghapptest.Repo
	inst  int64
	clock *clock
	jobs  *MemJobs
	state *MemState
	svc   *Service

	mu       sync.Mutex
	claim    Claim
	stores   map[string]*memcode.Store
	palaces  map[string]*service.Palace
	codeErr  error
	patterns []string
	seq      int
}

type codeOpt func(*Options)

func newCodeEnv(t *testing.T, opts ...codeOpt) *codeEnv {
	t.Helper()
	env := &codeEnv{t: t, f: ghapptest.New(t), clock: newClock(), state: NewMemState(), stores: map[string]*memcode.Store{}, palaces: map[string]*service.Palace{}}
	env.repo = env.f.AddRepo("octo", "app")
	env.inst = env.f.AddInstallation(ghapp.Installation{}, env.repo).ID
	env.claim = Claim{Installation: env.inst, Target: codeTarget, CodeSync: true}
	env.jobs = NewMemJobs(env.clock.Now)
	o := Options{
		App:    env.f.App(),
		Jobs:   env.jobs,
		Claims: claimsFunc(env.claimFor),
		Code:   env.openCode,
		Docs:   env.openDocs,
		Redact: func(context.Context, Target) ([]string, error) { return env.patterns, nil },
		State:  env.state,
		Now:    env.clock.Now,
	}
	for _, fn := range opts {
		fn(&o)
	}
	svc, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	env.svc = svc
	return env
}

func (e *codeEnv) claimFor(_ context.Context, installation, repository int64) (Claim, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if installation != e.inst || (repository != 0 && repository != e.repo.ID()) {
		return Claim{}, ErrNotClaimed
	}
	return e.claim, nil
}

func (e *codeEnv) setClaim(fn func(*Claim)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.claim)
}

func (e *codeEnv) openCode(_ context.Context, target Target, slug string) (CodeStore, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if target != codeTarget {
		return nil, fmt.Errorf("unexpected target %+v", target)
	}
	if e.codeErr != nil {
		return nil, e.codeErr
	}
	s, ok := e.stores[slug]
	if !ok {
		s = memcode.New()
		e.stores[slug] = s
	}
	return s, nil
}

func (e *codeEnv) openDocs(_ context.Context, target Target, slug string) (*service.Palace, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if target != codeTarget {
		return nil, fmt.Errorf("unexpected target %+v", target)
	}
	p, ok := e.palaces[slug]
	if !ok {
		var err error
		if p, err = service.NewPalace(nil, memstore.New("server"), nil); err != nil {
			return nil, err
		}
		e.palaces[slug] = p
	}
	return p, nil
}

func (e *codeEnv) store() *memcode.Store {
	e.t.Helper()
	s, err := e.openCode(context.Background(), codeTarget, codeSlug)
	if err != nil {
		e.t.Fatal(err)
	}
	return s.(*memcode.Store)
}

func (e *codeEnv) worktree() string { return WorktreeID(e.repo.ID()) }

// delivery wraps a typed event as the webhook endpoint queues it.
func (e *codeEnv) delivery(ev ghapp.Event) Delivery {
	e.t.Helper()
	payload, err := json.Marshal(ev)
	if err != nil {
		e.t.Fatal(err)
	}
	e.seq++
	d := Delivery{ID: "d" + strconv.Itoa(e.seq), Event: ev.EventName(), Action: ev.EventAction(), Installation: e.inst, Repository: ev.RepositoryID(), Payload: payload, ReceivedAt: e.clock.Now()}
	if pe, ok := ev.(*ghapp.PushEvent); ok {
		d.Ref, d.Before, d.After = pe.Ref, pe.Before, pe.After
	}
	return d
}

// handle applies ev directly.
func (e *codeEnv) handle(ev ghapp.Event) {
	e.t.Helper()
	if err := e.svc.Handle(context.Background(), e.delivery(ev)); err != nil {
		e.t.Fatalf("Handle(%s): %v", ev.EventName(), err)
	}
}

// push commits changes on branch and delivers the push.
func (e *codeEnv) push(branch string, changes ...ghapptest.Change) (before, after string) {
	e.t.Helper()
	before = e.repo.Head(branch)
	if before == "" {
		before = ghapp.ZeroSHA
	}
	after = e.repo.Commit(branch, "change", changes...)
	e.handle(e.repo.PushEvent(branch, before, after))
	return before, after
}

// importRepo runs an import job of the repository.
func (e *codeEnv) importRepo() {
	e.t.Helper()
	if _, err := e.jobs.Enqueue(context.Background(), Job{Kind: JobImport, Installation: e.inst, Repository: e.repo.ID()}); err != nil {
		e.t.Fatal(err)
	}
	e.run()
}

// run drains the queue and fails on a job that did not finish.
func (e *codeEnv) run() {
	e.t.Helper()
	if _, err := e.svc.RunPending(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

// tree returns path → entry of the branch's stored tree.
func (e *codeEnv) tree(branch string) map[string]code.Entry {
	e.t.Helper()
	entries, err := e.store().Tree(context.Background(), e.worktree(), branch)
	if err != nil {
		e.t.Fatal(err)
	}
	out := map[string]code.Entry{}
	for _, en := range entries {
		out[en.Path] = en
	}
	return out
}

// lastChange is the newest change of the code log.
func (e *codeEnv) lastChange() code.Change {
	e.t.Helper()
	cs, err := e.store().Changes(context.Background(), 0, 10000)
	if err != nil || len(cs) == 0 {
		e.t.Fatalf("changes: %v (%d)", err, len(cs))
	}
	return cs[len(cs)-1]
}

func (e *codeEnv) changes() int {
	cs, _ := e.store().Changes(context.Background(), 0, 10000)
	return len(cs)
}

func (e *codeEnv) content(t *testing.T, en code.Entry) string {
	t.Helper()
	b, err := e.store().Blob(context.Background(), en.SHA256)
	if err != nil {
		t.Fatalf("blob of %s: %v", en.Path, err)
	}
	return string(b.Content)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// wantFiles checks the stored tree of branch equals the fake's files at
// that branch, every entry clean and pushed with GitHub's blob id and the
// content's SHA-256.
func (e *codeEnv) wantFiles(t *testing.T, branch string) {
	t.Helper()
	got := e.tree(branch)
	for p, en := range got {
		if en.Mode == ghapp.ModeSubmodule {
			delete(got, p)
		}
	}
	want := e.repo.Files(branch)
	if len(got) != len(want) {
		t.Fatalf("%s: %d entries, want %d (%v)", branch, len(got), len(want), keys(got))
	}
	for p, oid := range want {
		en, ok := got[p]
		if !ok {
			t.Fatalf("%s: missing %s", branch, p)
		}
		if en.GitOID != oid || en.Dirty || en.Unpushed || en.Session != "" {
			t.Errorf("%s %s: %+v, want clean, pushed, git oid %s", branch, p, en, oid)
		}
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestImportAndIncrementalPush(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("README.md", "hello\n"), ghapptest.Put("src/a.go", "package a\n"), ghapptest.Put("src/b.go", "package b\n"), ghapptest.PutMode("run.sh", ghapp.ModeExecutable, []byte("#!/bin/sh\n")))
	env.importRepo()
	env.wantFiles(t, "main")
	c := env.lastChange()
	head := env.repo.Head("main")
	if !c.Full || c.Ref != "main" || c.Head != head || c.Upstream != head || c.Ahead != 0 || c.SessionID != "" {
		t.Fatalf("import change = %+v", c)
	}
	tr := env.tree("main")
	if en := tr["src/a.go"]; en.SHA256 != sha256Hex("package a\n") || en.Size != 10 || en.Mode != ghapp.ModeFile || en.Lang == "" {
		t.Errorf("src/a.go = %+v", en)
	}
	if en := tr["run.sh"]; en.Mode != ghapp.ModeExecutable {
		t.Errorf("run.sh mode = %q", en.Mode)
	}
	wts, _ := env.store().Worktrees(context.Background())
	if len(wts) != 1 || wts[0].ID != env.worktree() || wts[0].Source != code.SourceGitHub || wts[0].Host != GitHubHost || wts[0].Owner != codeTarget.Owner || wts[0].DefaultBranch != "main" || wts[0].Origin != "https://github.com/octo/app" {
		t.Fatalf("worktrees = %+v", wts)
	}

	env.f.ResetRequests()
	_, after := env.push("main",
		ghapptest.Put("src/a.go", "package a // changed\n"),
		ghapptest.Put("src/new/c.go", "package c\n"),
		ghapptest.Delete("README.md"),
		ghapptest.Delete("src/b.go"),
		ghapptest.Put("lib/b.go", "package b\n"), // a rename
	)
	env.wantFiles(t, "main")
	c = env.lastChange()
	if c.Full || c.Head != after || c.Upstream != after {
		t.Fatalf("push change = %+v, want incremental at %s", c, after)
	}
	if !slices.Contains(c.Deletes, "src/b.go") || !slices.Contains(c.Deletes, "README.md") {
		t.Errorf("deletes = %v", c.Deletes)
	}
	if n := env.f.Count("GET", "/repos/octo/app/compare/"); n != 1 {
		t.Errorf("compare calls = %d", n)
	}
	// Only the two new contents are fetched: the renamed file's blob is
	// held by its git blob id.
	if n := env.f.Count("GET", "/repos/octo/app/git/blobs/"); n != 2 {
		t.Errorf("blob fetches = %d, want 2", n)
	}
	if got := env.tree("main")["lib/b.go"].SHA256; got != sha256Hex("package b\n") {
		t.Errorf("renamed entry sha = %s", got)
	}

	// The same push again (a redelivery) changes nothing.
	n := env.changes()
	env.handle(env.repo.PushEvent("main", c.Head, after))
	if env.changes() != n {
		t.Error("a redelivered push wrote a change")
	}
}

func TestPushFallsBackToFullImport(t *testing.T) {
	env := newCodeEnv(t)
	c0 := env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a\n"))
	env.importRepo()

	// A lost delivery: the next push's before is not the stored head.
	env.repo.Commit("main", "lost", ghapptest.Put("b.txt", "b\n"))
	env.push("main", ghapptest.Put("c.txt", "c\n"))
	if c := env.lastChange(); !c.Full {
		t.Errorf("push after a lost delivery: %+v, want a full import", c)
	}
	env.wantFiles(t, "main")

	// A force push that rewrites the history.
	first := env.repo.Head("main")
	env.repo.SetBranch("main", c0)
	env.repo.Commit("main", "rewritten", ghapptest.Put("z.txt", "z\n"))
	fp := env.repo.PushEvent("main", first, env.repo.Head("main"))
	if !fp.Forced {
		t.Fatal("not a force push")
	}
	env.handle(fp)
	if c := env.lastChange(); !c.Full || c.Head != env.repo.Head("main") {
		t.Errorf("force push: %+v", c)
	}
	env.wantFiles(t, "main")

	// A truncated comparison.
	before := env.repo.Head("main")
	after := env.repo.Commit("main", "big", ghapptest.Put("y.txt", "y\n"))
	files := make([]ghapp.CompareFile, ghapp.MaxCompareFiles)
	for i := range files {
		files[i] = ghapp.CompareFile{Filename: fmt.Sprintf("f%d", i), Status: ghapp.FileAdded, SHA: strings.Repeat("a", 40)}
	}
	env.repo.SetCompare(before, after, &ghapp.Comparison{Status: "ahead", Files: files})
	env.handle(env.repo.PushEvent("main", before, after))
	if c := env.lastChange(); !c.Full || c.Head != after {
		t.Errorf("truncated compare: %+v", c)
	}
	env.wantFiles(t, "main")
}

func TestFullImportWalksTruncatedTree(t *testing.T) {
	env := newCodeEnv(t)
	var changes []ghapptest.Change
	for _, d := range []string{"a", "b/c", "b/d", "e"} {
		for i := range 3 {
			changes = append(changes, ghapptest.Put(fmt.Sprintf("%s/f%d.txt", d, i), d+strconv.Itoa(i)))
		}
	}
	changes = append(changes, ghapptest.Put("top.txt", "top"), ghapptest.Submodule("vendor/lib", strings.Repeat("c", 40)))
	env.repo.Commit("main", "init", changes...)
	env.repo.SetTreeLimit(5)
	env.importRepo()
	tr := env.tree("main")
	if len(tr) != 14 {
		t.Fatalf("entries = %d (%v), want 14", len(tr), keys(tr))
	}
	env.wantFiles(t, "main")
	sub := tr["vendor/lib"]
	if sub.Mode != ghapp.ModeSubmodule || sub.SHA256 != "" || sub.GitOID != strings.Repeat("c", 40) {
		t.Errorf("submodule = %+v", sub)
	}
	if !env.lastChange().Full {
		t.Error("import not Full")
	}
}

func TestImportDoesNotFetchBinaryFiles(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("README.md", "hello\n"), ghapptest.Put("assets/icon.png", "\x89PNG\x00\x00"))
	env.importRepo()
	if n := env.f.Count("GET", "/repos/octo/app/git/blobs/"); n != 1 {
		t.Errorf("blob fetches = %d, want 1 (a .png is never stored)", n)
	}
	tr := env.tree("main")
	if en := tr["assets/icon.png"]; en.SHA256 != "" || en.Size != 6 || !code.ValidGitOID(en.GitOID) {
		t.Errorf("assets/icon.png = %+v, want metadata only", en)
	}
	if en := tr["README.md"]; en.SHA256 != sha256Hex("hello\n") {
		t.Errorf("README.md = %+v", en)
	}
}

// firstPutStore records how many blobs had been fetched when the first blob
// was stored.
type firstPutStore struct {
	code.Store
	fetched  func() int
	firstPut *int
}

func (s firstPutStore) PutBlob(ctx context.Context, b code.Blob) error {
	if *s.firstPut == 0 {
		*s.firstPut = s.fetched()
	}
	return s.Store.PutBlob(ctx, b)
}

func TestImportStoresBlobsAsItGoes(t *testing.T) {
	var env *codeEnv
	firstPut := 0
	env = newCodeEnv(t, func(o *Options) {
		open := o.Code
		o.Code = func(ctx context.Context, target Target, slug string) (code.Store, error) {
			s, err := open(ctx, target, slug)
			if err != nil {
				return nil, err
			}
			return firstPutStore{Store: s, firstPut: &firstPut, fetched: func() int { return env.f.Count("GET", "/repos/octo/app/git/blobs/") }}, nil
		}
	})
	const files = 20
	var changes []ghapptest.Change
	for i := range files {
		changes = append(changes, ghapptest.Put(fmt.Sprintf("f%02d.txt", i), strings.Repeat(string(rune('a'+i)), 1<<20)))
	}
	env.repo.Commit("main", "init", changes...)
	env.importRepo()
	if limit := blobFlushBytes >> 20; firstPut == 0 || firstPut > limit {
		t.Errorf("first blob stored after %d fetches of 1 MiB, want at most %d", firstPut, limit)
	}
	tr := env.tree("main")
	if len(tr) != files {
		t.Fatalf("entries = %d, want %d", len(tr), files)
	}
	for p, en := range tr {
		if _, err := env.store().Blob(context.Background(), en.SHA256); err != nil {
			t.Errorf("blob of %s: %v", p, err)
		}
	}
}

func TestRunOneKeepsTheLeaseOfALongJob(t *testing.T) {
	jobs := NewMemJobs(nil)
	env := newCodeEnv(t, func(o *Options) {
		o.Jobs = jobs
		o.Lease = 300 * time.Millisecond
		o.Claims = claimsFunc(func(context.Context, int64, int64) (Claim, error) {
			time.Sleep(time.Second) // a job that outlives its first lease
			return Claim{}, ErrNotClaimed
		})
	})
	if _, err := jobs.Enqueue(context.Background(), Job{Kind: JobImport, Installation: env.inst, Repository: env.repo.ID()}); err != nil {
		t.Fatal(err)
	}
	if ran, err := env.svc.RunOne(context.Background()); !ran || err != nil {
		t.Fatalf("RunOne = %v, %v", ran, err)
	}
	if n := jobs.Len(); n != 0 {
		t.Errorf("jobs left = %d, want the job finished under its lease", n)
	}
}

// lostLeaseJobs refuses every extension.
type lostLeaseJobs struct{ Jobs }

func (lostLeaseJobs) Extend(context.Context, Lease, time.Duration) (Lease, error) {
	return Lease{}, ErrLeaseLost
}

func TestRunOneStopsTheJobWhenItsLeaseIsLost(t *testing.T) {
	jobs := NewMemJobs(nil)
	env := newCodeEnv(t, func(o *Options) {
		o.Jobs = lostLeaseJobs{jobs}
		o.Lease = 30 * time.Millisecond
		o.Claims = claimsFunc(func(ctx context.Context, _, _ int64) (Claim, error) {
			<-ctx.Done() // works until told to stop
			return Claim{}, ctx.Err()
		})
	})
	if _, err := jobs.Enqueue(context.Background(), Job{Kind: JobImport, Installation: env.inst, Repository: env.repo.ID()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := env.svc.RunOne(ctx); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("RunOne = %v, want ErrLeaseLost", err)
	}
	if ctx.Err() != nil {
		t.Error("the job ran on after its lease was lost")
	}
}

func TestFilterAndRedaction(t *testing.T) {
	env := newCodeEnv(t, func(o *Options) { o.MaxFileBytes = 64 })
	env.patterns = []string{`internal-[0-9]+`}
	env.repo.Commit("main", "init",
		ghapptest.Put("ok.txt", "plain\n"),
		ghapptest.Put(".env", "SECRET=1\n"),
		ghapptest.Put("config/prod/secret.yaml", "x: 1\n"),
		ghapptest.Put("big.txt", strings.Repeat("x", 100)),
		ghapptest.PutMode("bin.dat", ghapp.ModeFile, []byte{1, 0, 2}),
		ghapptest.Put("keys.go", "aws := \"AKIAABCDEFGHIJKLMNOP\" // internal-42\n"),
	)
	env.importRepo()
	tr := env.tree("main")
	if _, ok := tr[".env"]; ok {
		t.Error(".env stored")
	}
	if _, ok := tr["config/prod/secret.yaml"]; ok {
		t.Error("config secret stored")
	}
	if en := tr["big.txt"]; en.SHA256 != "" || en.Size != 100 || en.GitOID == "" {
		t.Errorf("big.txt = %+v, want metadata only", en)
	}
	if en := tr["bin.dat"]; en.SHA256 != "" || en.Size != 3 {
		t.Errorf("bin.dat = %+v, want metadata only", en)
	}
	got := env.content(t, tr["keys.go"])
	if strings.Contains(got, "AKIA") || strings.Contains(got, "internal-42") || !strings.Contains(got, "[REDACTED:aws-key]") || !strings.Contains(got, "[REDACTED:pattern-1]") {
		t.Errorf("keys.go stored as %q", got)
	}
	if env.content(t, tr["ok.txt"]) != "plain\n" {
		t.Error("ok.txt content")
	}

	// A push that renames a file onto the deny list drops it.
	env.push("main", ghapptest.Delete("ok.txt"), ghapptest.Put("id_rsa", "plain\n"))
	tr = env.tree("main")
	if _, ok := tr["id_rsa"]; ok {
		t.Error("id_rsa stored")
	}
	if _, ok := tr["ok.txt"]; ok {
		t.Error("ok.txt kept after its rename")
	}
}

func TestCodeSyncOptOut(t *testing.T) {
	t.Run("config", func(t *testing.T) {
		env := newCodeEnv(t)
		env.repo.Commit("main", "init", ghapptest.Put("mind-palace.config.yaml", "codeSync: false\n"), ghapptest.Put("a.txt", "a"))
		env.importRepo()
		env.push("main", ghapptest.Put("b.txt", "b"))
		if n := env.changes(); n != 0 {
			t.Errorf("changes = %d, want none", n)
		}
		// Events are still recorded.
		evs, _ := env.store().Events(context.Background(), 0, 100)
		if len(evs) == 0 {
			t.Error("no activity events")
		}
	})
	t.Run("claim", func(t *testing.T) {
		env := newCodeEnv(t)
		env.setClaim(func(c *Claim) { c.CodeSync = false })
		env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
		env.importRepo()
		if n := env.changes(); n != 0 {
			t.Errorf("changes = %d, want none", n)
		}
	})
	t.Run("invalid config", func(t *testing.T) {
		env := newCodeEnv(t)
		env.repo.Commit("main", "init", ghapptest.Put("mind-palace.config.yaml", "codeSync: [\n"), ghapptest.Put("a.txt", "a"))
		env.importRepo()
		if n := env.changes(); n != 0 {
			t.Errorf("changes = %d, want none", n)
		}
	})
	t.Run("remote.project", func(t *testing.T) {
		env := newCodeEnv(t)
		env.repo.Commit("main", "init", ghapptest.Put("mind-palace.config.yaml", "remote:\n  project: pinned\n"), ghapptest.Put("a.txt", "a"))
		env.importRepo()
		s, _ := env.openCode(context.Background(), codeTarget, "pinned")
		tr, _ := s.Tree(context.Background(), env.worktree(), "main")
		if len(tr) != 2 {
			t.Errorf("pinned project tree = %d entries", len(tr))
		}
	})
}

func TestBranchCreateDelete(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	env.importRepo()

	env.repo.Commit("feature", "f", ghapptest.Put("f.txt", "f"))
	env.handle(&ghapp.CreateEvent{Envelope: env.repo.Envelope("", nil), Ref: "feature", RefType: "branch"})
	env.wantFiles(t, "feature")
	if c := env.lastChange(); !c.Full || c.Ref != "feature" {
		t.Errorf("create: %+v", c)
	}
	// The push GitHub sends with the create finds the head stored.
	n := env.changes()
	env.handle(env.repo.PushEvent("feature", ghapp.ZeroSHA, env.repo.Head("feature")))
	if env.changes() != n {
		t.Error("the create's push re-imported")
	}

	env.repo.DeleteBranch("feature")
	env.handle(&ghapp.DeleteEvent{Envelope: env.repo.Envelope("", nil), Ref: "feature", RefType: "branch"})
	if tr := env.tree("feature"); len(tr) != 0 {
		t.Errorf("deleted branch keeps %d entries", len(tr))
	}
	wts, _ := env.store().Worktrees(context.Background())
	if slices.Contains(wts[0].Refs, "feature") {
		t.Errorf("refs = %v", wts[0].Refs)
	}
	c := env.lastChange()
	if c.Ref != "feature" || !c.Full || len(c.Upserts) != 0 || c.Head != "" {
		t.Errorf("tombstone = %+v", c)
	}

	// Tags are ignored.
	n = env.changes()
	env.handle(&ghapp.PushEvent{Envelope: env.repo.Envelope("", nil), Ref: "refs/tags/v1", Before: ghapp.ZeroSHA, After: env.repo.Head("main")})
	if env.changes() != n {
		t.Error("a tag push wrote a change")
	}
}

func TestBranchPolicy(t *testing.T) {
	t.Run("default only", func(t *testing.T) {
		env := newCodeEnv(t)
		env.repo.Commit("main", "init", ghapptest.Put("mind-palace.config.yaml", "github:\n  branches: default\n"))
		env.importRepo()
		env.push("feature", ghapptest.Put("f.txt", "f"))
		if tr := env.tree("feature"); len(tr) != 0 {
			t.Errorf("feature synced under the default policy: %v", keys(tr))
		}
	})
	t.Run("page setting", func(t *testing.T) {
		env := newCodeEnv(t)
		env.setClaim(func(c *Claim) { c.Policy = BranchPolicy{Mode: BranchesDefault} })
		env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
		env.importRepo()
		env.push("feature", ghapptest.Put("f.txt", "f"))
		if tr := env.tree("feature"); len(tr) != 0 {
			t.Error("feature synced under the page's default policy")
		}
	})
	t.Run("config wins over page", func(t *testing.T) {
		env := newCodeEnv(t)
		env.setClaim(func(c *Claim) { c.Policy = BranchPolicy{Mode: BranchesDefault} })
		env.repo.Commit("main", "init", ghapptest.Put("mind-palace.config.yaml", "github:\n  branches: all\n"))
		env.importRepo()
		env.push("feature", ghapptest.Put("f.txt", "f"))
		env.wantFiles(t, "feature")
	})
	t.Run("all with maxBranches", func(t *testing.T) {
		env := newCodeEnv(t)
		env.repo.Commit("main", "init", ghapptest.Put("mind-palace.config.yaml", "github:\n  branches: all\n  maxBranches: 2\n"))
		env.repo.Commit("old", "o", ghapptest.Put("o.txt", "o"))
		env.repo.Commit("older", "o", ghapptest.Put("p.txt", "p"))
		env.importRepo()
		// Nothing was pushed while installed: by name, one of the two.
		if len(env.tree("old")) == 0 || len(env.tree("older")) != 0 {
			t.Errorf("old %d, older %d entries", len(env.tree("old")), len(env.tree("older")))
		}
		env.clock.Advance(time.Hour)
		env.push("older", ghapptest.Put("q.txt", "q"))
		env.wantFiles(t, "older")
	})
	t.Run("inactive keeps tree; push or open pull request reactivates", func(t *testing.T) {
		env := newCodeEnv(t)
		env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
		env.importRepo()
		env.push("feature", ghapptest.Put("f.txt", "f"))
		env.wantFiles(t, "feature")

		env.clock.Advance(15 * 24 * time.Hour)
		if _, err := env.jobs.Enqueue(context.Background(), Job{Kind: JobSweep, Installation: env.inst, Repository: env.repo.ID()}); err != nil {
			t.Fatal(err)
		}
		env.run()
		st, _ := env.state.Load(context.Background(), codeTarget, env.repo.ID())
		if !st.Branches["feature"].Inactive {
			t.Error("sweep left feature active")
		}
		if len(env.tree("feature")) == 0 {
			t.Error("sweep dropped the inactive branch's tree")
		}

		// A commit lands while it is inactive (no delivery), then a pull
		// request opens: the branch is re-imported at the PR's head.
		env.repo.Commit("feature", "g", ghapptest.Put("g.txt", "g"))
		pr := env.repo.OpenPull("feature", "", ghapp.PullRequest{})
		env.handle(&ghapp.PullRequestEvent{Envelope: env.repo.Envelope("opened", nil), Number: pr.Number, PullRequest: pr})
		env.wantFiles(t, "feature")
		st, _ = env.state.Load(context.Background(), codeTarget, env.repo.ID())
		if st.Branches["feature"].Inactive || !st.OpenPRs.Has("feature") {
			t.Errorf("after the pull request: %+v, open %v", st.Branches["feature"], st.OpenPRs)
		}
		// With the pull request open, the sweep keeps it active.
		env.clock.Advance(30 * 24 * time.Hour)
		if _, err := env.jobs.Enqueue(context.Background(), Job{Kind: JobSweep, Installation: env.inst, Repository: env.repo.ID()}); err != nil {
			t.Fatal(err)
		}
		env.run()
		st, _ = env.state.Load(context.Background(), codeTarget, env.repo.ID())
		if st.Branches["feature"].Inactive {
			t.Error("sweep marked a branch with an open pull request inactive")
		}
	})
}

func TestStateLostSeedsHeadsFromStore(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	env.importRepo()
	// A new service with an empty state over the same stores.
	svc, err := New(Options{App: env.f.App(), Jobs: env.jobs, Claims: claimsFunc(env.claimFor), Code: env.openCode, Docs: env.openDocs, Now: env.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	env.svc = svc
	env.push("main", ghapptest.Put("b.txt", "b"))
	if c := env.lastChange(); c.Full {
		t.Errorf("push after a state loss was a full import")
	}
	env.wantFiles(t, "main")
}

func TestMemState(t *testing.T) {
	ctx := context.Background()
	m := NewMemState()
	if st, err := m.Load(ctx, codeTarget, 1); st != nil || err != nil {
		t.Fatalf("empty Load = %v, %v", st, err)
	}
	st := &RepoState{Repository: 1, Name: "o/r", Branches: map[string]*BranchState{"main": {Head: "abc"}}, OpenPRs: OpenPRs{3: "f"}}
	if err := m.Save(ctx, codeTarget, st); err != nil {
		t.Fatal(err)
	}
	st.Branches["main"].Head = "changed"
	got, err := m.Load(ctx, codeTarget, 1)
	if err != nil || got.Branches["main"].Head != "abc" || got.OpenPRs[3] != "f" {
		t.Fatalf("Load = %+v, %v", got, err)
	}
	other := Target{Kind: TargetWorkspace, ID: "w1", Owner: codeTarget.Owner}
	if st, _ := m.Load(ctx, other, 1); st != nil {
		t.Error("state leaked across targets")
	}
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := m.Load(cctx, codeTarget, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("Load on a cancelled context = %v", err)
	}
}
