package ghsync

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/ghapp"
	"github.com/robbiebyrd/clued/mind-palace/ghapp/ghapptest"
)

func TestHandleUnclaimedDoesNothing(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	other := env.f.AddRepo("octo", "other")
	env.f.SetInstallationRepos(env.inst, env.repo, other)
	other.Commit("main", "x", ghapptest.Put("x", "x"))
	env.f.ResetRequests()
	ev := other.PushEvent("main", ghapp.ZeroSHA, other.Head("main"))
	if err := env.svc.Handle(context.Background(), env.delivery(ev)); err != nil {
		t.Fatal(err)
	}
	if len(env.stores) != 0 || len(env.f.Requests()) != 0 {
		t.Errorf("unclaimed delivery: %d stores, %d GitHub calls", len(env.stores), len(env.f.Requests()))
	}
}

func TestHandleDocsAndActivity(t *testing.T) {
	env := newCodeEnv(t)
	planPath, plan := planFile("0001-aaa", "First plan", "2026-10-02T10:00:00.000Z")
	env.repo.Commit("main", "init", ghapptest.Put(planPath, plan), ghapptest.Put("a.txt", "a"))
	env.importRepo()
	p, _ := env.openDocs(context.Background(), codeTarget, codeSlug)
	if _, err := p.Store().Get(context.Background(), "plan", "0001-aaa"); err != nil {
		t.Fatalf("plan not imported: %v", err)
	}

	// A push to the default branch that does not touch the docs syncs no
	// docs; one that does syncs them.
	env.f.ResetRequests()
	env.push("main", ghapptest.Put("a.txt", "b"))
	if n := env.f.Count("GET", "/repos/octo/app/contents/docs"); n != 0 {
		t.Errorf("docs read on an unrelated push: %d calls", n)
	}
	storyPath, story := storyFile("0001-bbb", "A story", "2026-10-02T10:00:00.000Z")
	_, after := env.push("main", ghapptest.Put(storyPath, story))
	if _, err := p.Store().Get(context.Background(), "story", "0001-bbb"); err != nil {
		t.Fatalf("story not synced: %v", err)
	}
	evs, err := env.store().Events(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	joined := strings.Join(kinds, ",")
	if !strings.Contains(joined, code.EventPush) || !strings.Contains(joined, code.EventPlanSync) {
		t.Errorf("events = %v", kinds)
	}
	last := evs[len(evs)-1]
	if last.Kind != code.EventPush || last.Commit != after || last.Worktree != env.worktree() {
		t.Errorf("last event = %+v", last)
	}
	st, _ := env.state.Load(context.Background(), codeTarget, env.repo.ID())
	if st.DocsCommit != after {
		t.Errorf("docs commit = %s, want %s", st.DocsCommit, after)
	}

	// A push to another branch never syncs docs.
	env.push("feature", ghapptest.Put(planFile("0002-ccc", "X", "2026-10-02T10:00:00.000Z")))
	if _, err := p.Store().Get(context.Background(), "plan", "0002-ccc"); err == nil {
		t.Error("a plan on a branch was synced")
	}
}

func TestDeliveryWithoutPayload(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	env.importRepo()
	before := env.repo.Head("main")
	after := env.repo.Commit("main", "c", ghapptest.Put("b.txt", "b"))
	d := Delivery{ID: "bare", Event: ghapp.EventPush, Installation: env.inst, Repository: env.repo.ID(), RepositoryName: "octo/app", Ref: "refs/heads/main", Before: before, After: after, ReceivedAt: env.clock.Now()}
	if err := env.svc.Handle(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	env.wantFiles(t, "main")
	if c := env.lastChange(); c.Full {
		t.Error("bare push was a full import")
	}
	if _, err := deliveryEvent(Delivery{Event: ghapp.EventCheckRun}); !errors.Is(err, ErrBadDelivery) {
		t.Errorf("check_run without payload = %v", err)
	}
	if _, err := deliveryEvent(Delivery{Event: "nope", Payload: []byte(`{}`)}); !errors.Is(err, ErrBadDelivery) {
		t.Errorf("unknown event = %v", err)
	}
}

func TestInstallationCreatedImports(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	inst, _ := env.f.Installation(env.inst)
	ev := &ghapp.InstallationEvent{Action: "created", Installation: inst, Repositories: []ghapp.RepositoryRef{{ID: env.repo.ID(), FullName: "octo/app"}}}
	if _, err := env.jobs.Enqueue(context.Background(), Job{Kind: JobDelivery, Installation: env.inst, Delivery: ptr(env.delivery(ev))}); err != nil {
		t.Fatal(err)
	}
	env.run()
	env.wantFiles(t, "main")

	// EnqueueImport (the host after a claim) imports through the
	// installation's repository listing.
	env.repo.Commit("main", "more", ghapptest.Put("b.txt", "b"))
	if err := env.svc.EnqueueImport(context.Background(), env.inst); err != nil {
		t.Fatal(err)
	}
	env.run()
	env.wantFiles(t, "main")
}

func ptr[T any](v T) *T { return &v }

func TestReconcileRepairsDrift(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	env.importRepo()
	env.push("feature", ghapptest.Put("f.txt", "f"))

	// Lost deliveries: a commit on main and the branch deleted.
	env.repo.Commit("main", "lost", ghapptest.Put("b.txt", "b"))
	env.repo.DeleteBranch("feature")
	// An unclaimed repository of the installation is skipped.
	other := env.f.AddRepo("octo", "other")
	env.f.SetInstallationRepos(env.inst, env.repo, other)

	n, err := env.svc.Reconcile(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("queued %d jobs, want reconcile and sweep of one repository", n)
	}
	env.run()
	env.wantFiles(t, "main")
	if tr := env.tree("feature"); len(tr) != 0 {
		t.Errorf("deleted branch kept %d entries", len(tr))
	}
}

func TestWorkRetriesAndPauses(t *testing.T) {
	env := newCodeEnv(t)
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	ctx := context.Background()

	// A store that fails: retried with back-off.
	env.codeErr = errors.New("store down")
	if _, err := env.jobs.Enqueue(ctx, Job{Kind: JobImport, Installation: env.inst, Repository: env.repo.ID()}); err != nil {
		t.Fatal(err)
	}
	if ran, err := env.svc.RunOne(ctx); !ran || err != nil {
		t.Fatalf("RunOne = %v, %v", ran, err)
	}
	if ran, _ := env.svc.RunOne(ctx); ran {
		t.Fatal("a failed job ran again before its back-off")
	}
	env.codeErr = nil
	env.clock.Advance(BackoffBase)
	env.run()
	env.wantFiles(t, "main")

	// A rate limit pauses the installation without counting an attempt.
	before := env.repo.Head("main")
	after := env.repo.Commit("main", "c", ghapptest.Put("b.txt", "b"))
	env.f.RateLimitNext("/repos/octo/app/compare", time.Hour, 1)
	d := env.delivery(env.repo.PushEvent("main", before, after))
	if _, err := env.jobs.Enqueue(ctx, Job{Kind: JobDelivery, Installation: env.inst, Repository: env.repo.ID(), Delivery: &d}); err != nil {
		t.Fatal(err)
	}
	env.run()
	if c := env.lastChange(); c.Head == after {
		t.Fatal("rate-limited push applied")
	}
	env.clock.Advance(30 * time.Minute)
	if ran, _ := env.svc.RunOne(ctx); ran {
		t.Fatal("paused installation ran a job")
	}
	env.clock.Advance(31 * time.Minute)
	env.run()
	if c := env.lastChange(); c.Head != after || c.Full {
		t.Errorf("after the pause: %+v", c)
	}

	// A job for an unclaimed repository or a bad delivery is dropped.
	if _, err := env.jobs.Enqueue(ctx, Job{Kind: JobImport, Installation: env.inst, Repository: 999}); err != nil {
		t.Fatal(err)
	}
	bad := Delivery{ID: "bad", Event: ghapp.EventPush, Installation: env.inst, Payload: []byte(`{"ref":`)}
	if _, err := env.jobs.Enqueue(ctx, Job{Kind: JobDelivery, Installation: env.inst, Delivery: &bad}); err != nil {
		t.Fatal(err)
	}
	if n, err := env.svc.RunPending(ctx); n != 2 || err != nil {
		t.Fatalf("RunPending = %d, %v", n, err)
	}
	if _, err := env.jobs.Claim(ctx, time.Minute); !errors.Is(err, ErrNoJob) {
		t.Errorf("queue not empty: %v", err)
	}
}

func TestWorkStopsWithContext(t *testing.T) {
	env := newCodeEnv(t, func(o *Options) { o.PollInterval = time.Millisecond })
	env.repo.Commit("main", "init", ghapptest.Put("a.txt", "a"))
	if _, err := env.jobs.Enqueue(context.Background(), Job{Kind: JobImport, Installation: env.inst, Repository: env.repo.ID()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- env.svc.Work(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for env.changes() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Work = %v", err)
	}
	env.wantFiles(t, "main")
}

func TestClientFetcher(t *testing.T) {
	env := newCodeEnv(t)
	big := strings.Repeat("y", 1<<20+10)
	head := env.repo.Commit("main", "init", ghapptest.Put("docs/plans/a.md", "plan"), ghapptest.Put("big.txt", big))
	client, err := env.f.App().InstallationClient(context.Background(), env.inst)
	if err != nil {
		t.Fatal(err)
	}
	f := ClientFetcher{Client: client}
	ctx := context.Background()
	root, err := f.ListDir(ctx, "octo/app", head, "")
	if err != nil || len(root) != 2 {
		t.Fatalf("root = %+v, %v", root, err)
	}
	if _, err := f.ListDir(ctx, "octo/app", head, "nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing dir = %v", err)
	}
	if _, err := f.ListDir(ctx, "octo/app", head, "big.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file as dir = %v", err)
	}
	if b, err := f.ReadFile(ctx, "octo/app", head, "docs/plans/a.md"); err != nil || string(b) != "plan" {
		t.Errorf("ReadFile = %q, %v", b, err)
	}
	if b, err := f.ReadFile(ctx, "octo/app", head, "big.txt"); err != nil || len(b) != len(big) {
		t.Errorf("big ReadFile = %d bytes, %v", len(b), err)
	}
	if _, err := f.ReadFile(ctx, "octo/app", head, "nope.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file = %v", err)
	}
	if _, err := f.ReadFile(ctx, "octo/app", head, "docs"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("dir as file = %v", err)
	}
	if _, err := f.ListDir(ctx, "bad", head, ""); err == nil {
		t.Error("invalid repository accepted")
	}
}
