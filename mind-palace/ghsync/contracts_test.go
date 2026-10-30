package ghsync

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/service"
)

// clock is a settable test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)} }

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func delivery(repo int64, id string) Job {
	return Job{Kind: JobDelivery, Installation: 7, Repository: repo, Delivery: &Delivery{ID: id, Event: "push", Installation: 7, Repository: repo}}
}

func mustEnqueue(t *testing.T, q Jobs, j Job) string {
	t.Helper()
	id, err := q.Enqueue(context.Background(), j)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	return id
}

func mustClaim(t *testing.T, q Jobs, lease time.Duration) Lease {
	t.Helper()
	l, err := q.Claim(context.Background(), lease)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return l
}

func wantNoJob(t *testing.T, q Jobs) {
	t.Helper()
	if l, err := q.Claim(context.Background(), time.Minute); !errors.Is(err, ErrNoJob) {
		t.Fatalf("Claim = %+v, %v; want ErrNoJob", l.Job, err)
	}
}

func TestMemJobsEnqueueValidates(t *testing.T) {
	q := NewMemJobs(nil)
	ctx := context.Background()
	for name, j := range map[string]Job{
		"kind":         {Kind: "nope", Installation: 1},
		"installation": {Kind: JobImport},
		"repository":   {Kind: JobImport, Installation: 1, Repository: -1},
		"delivery":     {Kind: JobDelivery, Installation: 1, Repository: 2},
	} {
		if _, err := q.Enqueue(ctx, j); !errors.Is(err, ErrInvalidJob) {
			t.Errorf("%s: Enqueue err = %v, want ErrInvalidJob", name, err)
		}
	}
	id := mustEnqueue(t, q, Job{Kind: JobImport, Installation: 1, Repository: 2, Attempts: 5})
	l := mustClaim(t, q, time.Minute)
	if l.Job.ID != id || l.Job.Attempts != 0 || l.Job.EnqueuedAt.IsZero() {
		t.Fatalf("claimed %+v; want id %s, no attempts, EnqueuedAt set", l.Job, id)
	}
}

func TestMemJobsPerRepositoryOrder(t *testing.T) {
	c := newClock()
	q := NewMemJobs(c.Now)
	a1 := mustEnqueue(t, q, delivery(1, "a1"))
	a2 := mustEnqueue(t, q, delivery(1, "a2"))
	b1 := mustEnqueue(t, q, delivery(2, "b1"))
	i1 := mustEnqueue(t, q, Job{Kind: JobImport, Installation: 7})

	// One job per key at a time, keys in parallel, oldest first.
	la1 := mustClaim(t, q, time.Minute)
	lb1 := mustClaim(t, q, time.Minute)
	li1 := mustClaim(t, q, time.Minute)
	if got := []string{la1.Job.ID, lb1.Job.ID, li1.Job.ID}; !reflect.DeepEqual(got, []string{a1, b1, i1}) {
		t.Fatalf("claimed %v, want %v", got, []string{a1, b1, i1})
	}
	wantNoJob(t, q) // a2 waits for a1

	if err := q.Done(context.Background(), la1); err != nil {
		t.Fatal(err)
	}
	if l := mustClaim(t, q, time.Minute); l.Job.ID != a2 {
		t.Fatalf("after a1 done claimed %s, want %s", l.Job.ID, a2)
	}
	if err := q.Done(context.Background(), la1); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("second Done = %v, want ErrLeaseLost", err)
	}
	if q.Len() != 3 {
		t.Fatalf("Len = %d, want 3", q.Len())
	}
}

func TestMemJobsRetryHoldsOrderAndBacksOff(t *testing.T) {
	c := newClock()
	q := NewMemJobs(c.Now)
	ctx := context.Background()
	first := mustEnqueue(t, q, delivery(1, "x"))
	mustEnqueue(t, q, delivery(1, "y"))

	l := mustClaim(t, q, time.Minute)
	if err := q.Retry(ctx, l, 5*time.Minute, errors.New("github 502")); err != nil {
		t.Fatal(err)
	}
	wantNoJob(t, q) // the retried job is still the head of repository 1
	c.Advance(5 * time.Minute)
	l = mustClaim(t, q, time.Minute)
	if l.Job.ID != first || l.Job.Attempts != 1 || l.Job.LastError != "github 502" {
		t.Fatalf("reclaimed %+v; want %s with 1 attempt and the cause", l.Job, first)
	}
}

func TestMemJobsDeadLetter(t *testing.T) {
	c := newClock()
	q := NewMemJobs(c.Now)
	ctx := context.Background()
	id := mustEnqueue(t, q, delivery(1, "x"))
	next := mustEnqueue(t, q, delivery(1, "y"))
	for i := 1; i <= MaxAttempts; i++ {
		l := mustClaim(t, q, time.Minute)
		if l.Job.ID != id {
			t.Fatalf("attempt %d claimed %s, want %s", i, l.Job.ID, id)
		}
		err := q.Retry(ctx, l, 0, errors.New("boom"))
		if i < MaxAttempts && err != nil {
			t.Fatalf("attempt %d Retry = %v", i, err)
		}
		if i == MaxAttempts && !errors.Is(err, ErrDeadLettered) {
			t.Fatalf("last Retry = %v, want ErrDeadLettered", err)
		}
	}
	dead := q.DeadLetters()
	if len(dead) != 1 || dead[0].ID != id || dead[0].Attempts != MaxAttempts {
		t.Fatalf("dead letters %+v", dead)
	}
	// The dead job no longer holds its repository back.
	if l := mustClaim(t, q, time.Minute); l.Job.ID != next {
		t.Fatalf("claimed %s, want %s", l.Job.ID, next)
	}
}

func TestMemJobsLeaseExpiry(t *testing.T) {
	c := newClock()
	q := NewMemJobs(c.Now)
	ctx := context.Background()
	id := mustEnqueue(t, q, delivery(1, "x"))
	old := mustClaim(t, q, time.Minute)
	c.Advance(30 * time.Second)
	wantNoJob(t, q)
	c.Advance(30 * time.Second)

	l := mustClaim(t, q, time.Minute)
	if l.Job.ID != id || l.Job.Attempts != 1 || l.Token == old.Token {
		t.Fatalf("reclaimed %+v token %q; want %s, 1 attempt, a new token", l.Job, l.Token, id)
	}
	for name, err := range map[string]error{
		"Done":  q.Done(ctx, old),
		"Retry": q.Retry(ctx, old, 0, nil),
		"Pause": q.Pause(ctx, old, c.Now().Add(time.Hour)),
	} {
		if !errors.Is(err, ErrLeaseLost) {
			t.Errorf("%s with the expired lease = %v, want ErrLeaseLost", name, err)
		}
	}
	if err := q.Done(ctx, l); err != nil {
		t.Fatal(err)
	}

	// A lease left to expire on its last attempt dead-letters the job.
	mustEnqueue(t, q, delivery(2, "z"))
	for i := 0; i < MaxAttempts; i++ {
		mustClaim(t, q, time.Minute)
		c.Advance(time.Minute)
	}
	wantNoJob(t, q)
	if dead := q.DeadLetters(); len(dead) != 1 || dead[0].LastError != "lease expired" {
		t.Fatalf("dead letters %+v", dead)
	}
}

func TestMemJobsExtend(t *testing.T) {
	c := newClock()
	q := NewMemJobs(c.Now)
	ctx := context.Background()
	mustEnqueue(t, q, delivery(1, "x"))
	l := mustClaim(t, q, time.Minute)
	c.Advance(40 * time.Second)
	l, err := q.Extend(ctx, l, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := c.Now().Add(time.Minute); !l.Until.Equal(want) {
		t.Errorf("Until = %v, want %v", l.Until, want)
	}
	c.Advance(40 * time.Second) // past the first Until
	wantNoJob(t, q)
	expired := l
	if err := q.Done(ctx, l); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Extend(ctx, expired, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Extend of a finished job = %v, want ErrLeaseLost", err)
	}

	mustEnqueue(t, q, delivery(2, "y"))
	l = mustClaim(t, q, time.Minute)
	c.Advance(time.Minute)
	if _, err := q.Extend(ctx, l, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Errorf("Extend of an expired lease = %v, want ErrLeaseLost", err)
	}
}

func TestMemJobsPauseHoldsInstallation(t *testing.T) {
	c := newClock()
	q := NewMemJobs(c.Now)
	ctx := context.Background()
	a := mustEnqueue(t, q, delivery(1, "a"))
	b := mustEnqueue(t, q, delivery(2, "b"))
	other := mustEnqueue(t, q, Job{Kind: JobImport, Installation: 8, Repository: 3})

	l := mustClaim(t, q, time.Minute)
	if err := q.Pause(ctx, l, c.Now().Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Installation 7 is held (both repositories); installation 8 is not.
	if l := mustClaim(t, q, time.Minute); l.Job.ID != other {
		t.Fatalf("claimed %s, want %s", l.Job.ID, other)
	}
	wantNoJob(t, q)
	c.Advance(10 * time.Minute)
	la := mustClaim(t, q, time.Minute)
	lb := mustClaim(t, q, time.Minute)
	if la.Job.ID != a || lb.Job.ID != b || la.Job.Attempts != 0 {
		t.Fatalf("after the pause claimed %+v, %+v; want %s (no attempt counted), %s", la.Job, lb.Job, a, b)
	}
}

func TestMemJobsConcurrentClaimsRespectKeys(t *testing.T) {
	q := NewMemJobs(nil)
	want := map[string][]string{}
	for i := 0; i < 50; i++ {
		j := delivery(int64(i%5+1), "d")
		want[j.OrderKey()] = append(want[j.OrderKey()], mustEnqueue(t, q, j))
	}
	var mu sync.Mutex
	running := map[string]bool{}
	order := map[string][]string{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				l, err := q.Claim(context.Background(), time.Minute)
				if errors.Is(err, ErrNoJob) {
					if q.Len() == 0 {
						return
					}
					time.Sleep(time.Millisecond)
					continue
				}
				if err != nil {
					t.Error(err)
					return
				}
				key := l.Job.OrderKey()
				mu.Lock()
				if running[key] {
					t.Errorf("two jobs of %s at once", key)
				}
				running[key] = true
				order[key] = append(order[key], l.Job.ID)
				mu.Unlock()
				time.Sleep(100 * time.Microsecond)
				mu.Lock()
				running[key] = false
				mu.Unlock()
				if err := q.Done(context.Background(), l); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	total := 0
	for key, ids := range order {
		total += len(ids)
		if !reflect.DeepEqual(ids, want[key]) {
			t.Errorf("%s ran %v, want %v", key, ids, want[key])
		}
	}
	if total != 50 {
		t.Fatalf("ran %d jobs, want 50", total)
	}
}

func TestBackoff(t *testing.T) {
	for attempts, want := range map[int]time.Duration{
		0: 30 * time.Second, 1: 30 * time.Second, 2: time.Minute, 3: 2 * time.Minute,
		7: 32 * time.Minute, 8: time.Hour, 20: time.Hour,
	} {
		if got := Backoff(attempts); got != want {
			t.Errorf("Backoff(%d) = %v, want %v", attempts, got, want)
		}
	}
}

func TestJobOrderKey(t *testing.T) {
	if k := (Job{Installation: 7, Repository: 42}).OrderKey(); k != "r42" {
		t.Errorf("repository key %q", k)
	}
	if k := (Job{Installation: 7}).OrderKey(); k != "i7" {
		t.Errorf("installation key %q", k)
	}
}

var now = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func days(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

func TestPolicyDefaults(t *testing.T) {
	p := BranchPolicy{}.Normalize()
	if p != DefaultBranchPolicy() || p.Mode != BranchesActive || p.ActiveDays != 14 || p.MaxBranches != 200 {
		t.Fatalf("defaults %+v", p)
	}
}

func TestPolicyWants(t *testing.T) {
	cases := []struct {
		name string
		p    BranchPolicy
		b    Branch
		want bool
	}{
		{"default branch always", BranchPolicy{Mode: BranchesDefault}, Branch{Name: "main"}, true},
		{"default mode skips others", BranchPolicy{Mode: BranchesDefault}, Branch{Name: "f", OpenPR: true, LastPush: now}, false},
		{"active by open PR", BranchPolicy{}, Branch{Name: "f", OpenPR: true, LastPush: days(90)}, true},
		{"active by recent push", BranchPolicy{}, Branch{Name: "f", LastPush: days(13)}, true},
		{"active on the boundary", BranchPolicy{}, Branch{Name: "f", LastPush: days(14)}, true},
		{"inactive after activeDays", BranchPolicy{}, Branch{Name: "f", LastPush: days(15)}, false},
		{"unknown push is inactive", BranchPolicy{}, Branch{Name: "f"}, false},
		{"custom activeDays", BranchPolicy{ActiveDays: 30}, Branch{Name: "f", LastPush: days(20)}, true},
		{"all", BranchPolicy{Mode: BranchesAll}, Branch{Name: "f", LastPush: days(900)}, true},
	}
	for _, c := range cases {
		if got := c.p.Wants("main", c.b, now); got != c.want {
			t.Errorf("%s: Wants = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPolicySelect(t *testing.T) {
	branches := []Branch{
		{Name: "main", LastPush: days(100)},
		{Name: "zeta", LastPush: days(1)},
		{Name: "pr-old", OpenPR: true, LastPush: days(60)},
		{Name: "stale", LastPush: days(40)},
		{Name: "alpha", LastPush: days(3)},
	}
	got := BranchPolicy{}.Select("main", branches, now)
	want := Selection{Active: []string{"main", "alpha", "pr-old", "zeta"}, Inactive: []string{"stale"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("active: %+v, want %+v", got, want)
	}

	got = BranchPolicy{Mode: BranchesDefault}.Select("main", branches, now)
	want = Selection{Active: []string{"main"}, Inactive: []string{"alpha", "pr-old", "stale", "zeta"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("default: %+v, want %+v", got, want)
	}

	got = BranchPolicy{Mode: BranchesAll}.Select("main", branches, now)
	want = Selection{Active: []string{"main", "alpha", "pr-old", "stale", "zeta"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all: %+v, want %+v", got, want)
	}

	// maxBranches counts the default branch and keeps the most recently pushed.
	got = BranchPolicy{Mode: BranchesAll, MaxBranches: 3}.Select("main", branches, now)
	want = Selection{Active: []string{"main", "alpha", "zeta"}, Inactive: []string{"pr-old", "stale"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("all capped: %+v, want %+v", got, want)
	}

	// The default branch is active even when the list does not carry it.
	got = BranchPolicy{}.Select("main", nil, now)
	if !reflect.DeepEqual(got, Selection{Active: []string{"main"}}) {
		t.Errorf("empty: %+v", got)
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := (BranchPolicy{}).Validate(); err != nil {
		t.Errorf("zero policy: %v", err)
	}
	if err := DefaultBranchPolicy().Validate(); err != nil {
		t.Errorf("default policy: %v", err)
	}
	for _, p := range []BranchPolicy{{Mode: "some"}, {Mode: "Active"}, {ActiveDays: -1}, {MaxBranches: -2}} {
		if err := p.Validate(); !errors.Is(err, ErrInvalidPolicy) {
			t.Errorf("%+v: Validate = %v, want ErrInvalidPolicy", p, err)
		}
	}
	if m, err := ParseBranchMode(" ALL "); err != nil || m != BranchesAll {
		t.Errorf("ParseBranchMode = %q, %v", m, err)
	}
}

func TestParseRepoConfig(t *testing.T) {
	rc, err := ParseRepoConfig([]byte(`
plans:
  dir: docs/plans
remote:
  project: " my-proj "
codeSync: false
github:
  branches: all
  maxBranches: 50
unknown: ignored
`))
	if err != nil {
		t.Fatal(err)
	}
	if rc.GitHub != (BranchPolicy{Mode: BranchesAll, MaxBranches: 50}) || rc.Project != "my-proj" || rc.CodeSync == nil || *rc.CodeSync {
		t.Fatalf("parsed %+v (codeSync %v)", rc, rc.CodeSync)
	}

	rc, err = ParseRepoConfig([]byte(`{"github": {"activeDays": 7}}`))
	if err != nil || rc.GitHub != (BranchPolicy{ActiveDays: 7}) || rc.CodeSync != nil {
		t.Fatalf("json: %+v, %v", rc, err)
	}

	if rc, err := ParseRepoConfig(nil); err != nil || rc.GitHub != (BranchPolicy{}) || rc.CodeSync != nil || rc.Project != "" {
		t.Fatalf("empty: %+v, %v", rc, err)
	}

	for _, bad := range []string{
		"github:\n  branches: some\n",
		"github:\n  activeDays: 0\n",
		"github:\n  maxBranches: -3\n",
		"github: [\n",
	} {
		if _, err := ParseRepoConfig([]byte(bad)); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func TestEffectivePolicy(t *testing.T) {
	page := BranchPolicy{Mode: BranchesDefault, ActiveDays: 30}
	file := RepoConfig{GitHub: BranchPolicy{Mode: BranchesActive}}
	got := EffectivePolicy(page, file)
	if want := (BranchPolicy{Mode: BranchesActive, ActiveDays: 30, MaxBranches: 200}); got != want {
		t.Errorf("file over page = %+v, want %+v", got, want)
	}
	if got := EffectivePolicy(BranchPolicy{}, RepoConfig{}); got != DefaultBranchPolicy() {
		t.Errorf("nothing set = %+v", got)
	}
	if got := EffectivePolicy(page, RepoConfig{}); got != (BranchPolicy{Mode: BranchesDefault, ActiveDays: 30, MaxBranches: 200}) {
		t.Errorf("page only = %+v", got)
	}
}

type fakeClaims map[int64]Claim

func (f fakeClaims) Claim(_ context.Context, installation, _ int64) (Claim, error) {
	c, ok := f[installation]
	if !ok {
		return Claim{}, ErrNotClaimed
	}
	return c, nil
}

func TestNewRequiresDependencies(t *testing.T) {
	code := func(context.Context, Target, string) (CodeStore, error) { return nil, nil }
	docs := func(context.Context, Target, string) (*service.Palace, error) { return nil, nil }
	full := Options{Jobs: NewMemJobs(nil), Claims: fakeClaims{}, Code: code, Docs: docs}
	for name, mutate := range map[string]func(*Options){
		"jobs":   func(o *Options) { o.Jobs = nil },
		"claims": func(o *Options) { o.Claims = nil },
		"code":   func(o *Options) { o.Code = nil },
		"docs":   func(o *Options) { o.Docs = nil },
	} {
		o := full
		mutate(&o)
		if _, err := New(o); err == nil {
			t.Errorf("missing %s: want an error", name)
		}
	}
	s, err := New(full)
	if err != nil {
		t.Fatal(err)
	}
	if s.Now().IsZero() || s.Logger() == nil {
		t.Error("New left the clock or logger unset")
	}
	if _, err := s.Claims.Claim(context.Background(), 1, 2); !errors.Is(err, ErrNotClaimed) {
		t.Errorf("Claims = %v, want ErrNotClaimed", err)
	}
}

func TestTargetValidate(t *testing.T) {
	good := []Target{
		{Kind: TargetUser, ID: "0123456789abcdef", Owner: "0123456789abcdef"},
		{Kind: TargetWorkspace, ID: "ws1", Owner: "0123456789abcdef"},
	}
	for _, tg := range good {
		if err := tg.Validate(); err != nil {
			t.Errorf("%+v: %v", tg, err)
		}
	}
	bad := []Target{
		{},
		{Kind: "team", ID: "x", Owner: "x"},
		{Kind: TargetUser, ID: "a", Owner: "b"},
		{Kind: TargetUser},
		{Kind: TargetWorkspace, ID: "ws1"},
	}
	for _, tg := range bad {
		if err := tg.Validate(); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("%+v: Validate = %v, want ErrInvalidTarget", tg, err)
		}
	}
}
