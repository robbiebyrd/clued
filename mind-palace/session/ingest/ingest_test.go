package ingest

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const testAccount = "test-account-uuid"

// syncBuffer is a log sink safe for concurrent writers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *syncBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// failingInserts is a real store whose hook-event inserts fail.
type failingInserts struct{ session.Store }

func (failingInserts) InsertHookEvent(context.Context, session.Doc) error {
	return errors.New("store down")
}

// hangingInserts is a real store whose hook-event inserts block until their context ends.
type hangingInserts struct{ session.Store }

func (hangingInserts) InsertHookEvent(ctx context.Context, _ session.Doc) error {
	<-ctx.Done()
	return ctx.Err()
}

// recordingInserts is a real store that remembers the order of its inserts.
type recordingInserts struct {
	session.Store
	mu  sync.Mutex
	ids []string
}

func (r *recordingInserts) InsertHookEvent(ctx context.Context, ev session.Doc) error {
	id, _ := ev.String("session_id")
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
	return r.Store.InsertHookEvent(ctx, ev)
}

// fakeGit reports an origin for directories named "repo" only.
func fakeGit(_ context.Context, dir string) (string, string) {
	if filepath.Base(dir) == "repo" {
		return "https://github.com/test/repo.git", "main"
	}
	return "", ""
}

type fixture struct {
	svc   *Service
	store session.Store
	logs  *syncBuffer
	tmp   string
}

func newService(t *testing.T, store session.Store, logs *syncBuffer) (*Service, string) {
	t.Helper()
	tmp := t.TempDir()
	return &Service{
		Store:     store,
		AccountID: testAccount,
		Host:      session.HostInfo{Hostname: "test-host"},
		Git:       fakeGit,
		Logger:    slog.New(slog.NewTextHandler(logs, nil)),
		WALPath:   filepath.Join(tmp, "wal", "events.wal"),
	}, tmp
}

// newFixture builds a service over a fresh in-memory store and fails the
// test when anything was logged.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	logs := &syncBuffer{}
	store := memsession.New()
	svc, tmp := newService(t, store, logs)
	t.Cleanup(func() {
		if out := logs.String(); out != "" {
			t.Errorf("unexpected log output:\n%s", out)
		}
	})
	return &fixture{svc: svc, store: store, logs: logs, tmp: tmp}
}

func (f *fixture) handle(t *testing.T, ev session.Doc) Tracked {
	t.Helper()
	tr, err := f.svc.Handle(context.Background(), ev)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	return tr
}

func (f *fixture) expectWarn(t *testing.T, want string) {
	t.Helper()
	out := f.logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, want) {
		t.Fatalf("expected warning containing %q, got:\n%s", want, out)
	}
	f.logs.Reset()
}

func hookEvents(t *testing.T, s session.Store) []session.Doc {
	t.Helper()
	docs, err := s.Unenriched(context.Background(), "hook_events", "none", 1000)
	if err != nil {
		t.Fatal(err)
	}
	return docs
}

func sessionDoc(s session.Store, id string) session.Doc {
	d, err := s.GetSession(context.Background(), testAccount, id)
	if err != nil {
		return nil
	}
	return d
}

func hostname(ev session.Doc) string {
	if h, ok := ev["host"].(session.HostInfo); ok {
		return h.Hostname
	}
	name, _ := ev.Map("host").String("hostname")
	return name
}

func TestEventIsStoredWithAccountHostAndTimestamp(t *testing.T) {
	f := newFixture(t)
	before := time.Now().Truncate(time.Millisecond)
	tr := f.handle(t, session.Doc{"session_id": "s1", "type": "PreToolUse", "tool_name": "Bash"})
	if tr.SessionID != "s1" || !tr.FirstSeen {
		t.Fatalf("tracked: %+v", tr)
	}
	evs := hookEvents(t, f.store)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev["tool_name"] != "Bash" || ev["account_id"] != testAccount || ev["type"] != "PreToolUse" {
		t.Fatalf("event: %v", ev)
	}
	if hostname(ev) != "test-host" {
		t.Fatalf("host: %v", ev["host"])
	}
	if at, ok := ev["created_at"].(time.Time); !ok || at.Before(before) {
		t.Fatalf("created_at: %v", ev["created_at"])
	}
}

func TestHandleDoesNotMutateTheEventItWasGiven(t *testing.T) {
	f := newFixture(t)
	in := session.Doc{"session_id": "s1"}
	f.handle(t, in)
	if len(in) != 1 {
		t.Fatalf("input event was stamped: %v", in)
	}
}

func TestEventWithoutSessionIsStoredAndNotTracked(t *testing.T) {
	f := newFixture(t)
	tr := f.handle(t, session.Doc{"type": "Notification"})
	if tr != (Tracked{}) {
		t.Fatalf("tracked: %+v", tr)
	}
	if n := len(hookEvents(t, f.store)); n != 1 {
		t.Fatalf("want 1 event, got %d", n)
	}
}

func TestFirstSightUpsertsSessionWithGitHostAndAccount(t *testing.T) {
	f := newFixture(t)
	repo := filepath.Join(f.tmp, "repo")
	transcript := filepath.Join(f.tmp, "projects", "git-now.jsonl")
	before := time.Now().Truncate(time.Millisecond)
	tr := f.handle(t, session.Doc{"session_id": "git-now", "transcript_path": transcript, "cwd": repo})
	if !tr.FirstSeen || tr.TranscriptPath != transcript || tr.Cwd != repo {
		t.Fatalf("tracked: %+v", tr)
	}
	sd := sessionDoc(f.store, "git-now")
	if sd == nil {
		t.Fatal("session not upserted")
	}
	if sd["transcript_path"] != transcript || sd["cwd"] != repo || sd["account_id"] != testAccount {
		t.Fatalf("session: %v", sd)
	}
	if sd["git_origin"] != "https://github.com/test/repo.git" || sd["git_branch"] != "main" {
		t.Fatalf("git: %v", sd)
	}
	if hostname(sd) != "test-host" {
		t.Fatalf("host: %v", sd["host"])
	}
	if at, ok := sd["last_seen"].(time.Time); !ok || at.Before(before) {
		t.Fatalf("last_seen: %v", sd["last_seen"])
	}
	// The same session is first seen only once.
	if tr := f.handle(t, session.Doc{"session_id": "git-now"}); tr.FirstSeen {
		t.Fatal("second event reported as first sight")
	}
}

func TestNoGitOriginForNonGitCwd(t *testing.T) {
	f := newFixture(t)
	f.handle(t, session.Doc{"session_id": "git-never", "cwd": "/tmp"})
	sd := sessionDoc(f.store, "git-never")
	if sd == nil {
		t.Fatal("session not upserted")
	}
	if _, ok := sd["git_origin"]; ok {
		t.Fatal("git_origin must be absent")
	}
}

func TestGitOriginFilledByLaterEvent(t *testing.T) {
	f := newFixture(t)
	f.handle(t, session.Doc{"session_id": "git-late", "transcript_path": filepath.Join(f.tmp, "projects", "late.jsonl")})
	if _, ok := sessionDoc(f.store, "git-late")["git_origin"]; ok {
		t.Fatal("git_origin should not be set yet")
	}
	// A cwd outside a repository leaves it unset and allows a later retry.
	f.handle(t, session.Doc{"session_id": "git-late", "cwd": "/tmp"})
	if _, ok := sessionDoc(f.store, "git-late")["git_origin"]; ok {
		t.Fatal("git_origin set from a non-git cwd")
	}
	tr := f.handle(t, session.Doc{"session_id": "git-late", "cwd": filepath.Join(f.tmp, "repo")})
	if tr.FirstSeen {
		t.Fatal("later event reported as first sight")
	}
	sd := sessionDoc(f.store, "git-late")
	if sd["git_origin"] != "https://github.com/test/repo.git" || sd["git_branch"] != "main" {
		t.Fatalf("git: %v", sd)
	}
}

func TestGitIsLookedUpOncePerSessionAfterAnOriginIsKnown(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	calls := 0
	f.svc.Git = func(ctx context.Context, dir string) (string, string) {
		mu.Lock()
		calls++
		mu.Unlock()
		return fakeGit(ctx, dir)
	}
	repo := filepath.Join(f.tmp, "repo")
	f.handle(t, session.Doc{"session_id": "once", "cwd": repo})
	f.handle(t, session.Doc{"session_id": "once", "cwd": repo})
	f.handle(t, session.Doc{"session_id": "once", "cwd": repo})
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("git looked up %d times, want 1", calls)
	}
}

func TestEventTouchesLastSeen(t *testing.T) {
	f := newFixture(t)
	f.handle(t, session.Doc{"session_id": "touch"})
	first, ok := sessionDoc(f.store, "touch")["last_seen"].(time.Time)
	if !ok {
		t.Fatal("last_seen missing")
	}
	time.Sleep(20 * time.Millisecond)
	f.handle(t, session.Doc{"session_id": "touch"})
	if at, _ := sessionDoc(f.store, "touch")["last_seen"].(time.Time); !at.After(first) {
		t.Fatalf("last_seen did not advance: %v -> %v", first, at)
	}
}

func TestUnsafeSessionIDIsStoredButNotTracked(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"../evil", "a/b", "..", "/abs"} {
		tr := f.handle(t, session.Doc{"session_id": id, "transcript_path": filepath.Join(f.tmp, "projects", "x.jsonl")})
		if tr.FirstSeen || tr.SessionID != id {
			t.Fatalf("%q: tracked %+v", id, tr)
		}
		f.expectWarn(t, "session_id is not a single safe path element")
		if sessionDoc(f.store, id) != nil {
			t.Fatalf("%q: session upserted", id)
		}
	}
	if n := len(hookEvents(t, f.store)); n != 4 {
		t.Fatalf("want 4 stored events, got %d", n)
	}
	f.svc.mu.Lock()
	defer f.svc.mu.Unlock()
	if n := len(f.svc.seen); n != 0 {
		t.Fatalf("unsafe sessions tracked: %d", n)
	}
}

func TestInsertFailureFallsBackToWAL(t *testing.T) {
	logs := &syncBuffer{}
	svc, _ := newService(t, failingInserts{memsession.New()}, logs)
	tr, err := svc.Handle(context.Background(), session.Doc{"session_id": "wal-1", "tool_name": "Bash"})
	if !errors.Is(err, ErrBuffered) || !strings.Contains(err.Error(), "store down") {
		t.Fatalf("err = %v, want ErrBuffered wrapping the store error", err)
	}
	if !tr.FirstSeen || tr.SessionID != "wal-1" {
		t.Fatalf("tracked: %+v", tr)
	}
	data, err := os.ReadFile(svc.WALPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	for _, want := range []string{`"session_id":"wal-1"`, `"tool_name":"Bash"`, `"account_id":"` + testAccount + `"`, `"hostname":"test-host"`, `"created_at":"20`} {
		if !strings.Contains(line, want) {
			t.Errorf("WAL line missing %s: %s", want, line)
		}
	}
	if strings.Count(line, "\n") != 1 {
		t.Errorf("want one WAL line: %q", line)
	}
	if out := logs.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "hook event insert failed; appending to WAL") || !strings.Contains(out, "store down") {
		t.Errorf("expected WAL fallback warning, got logs:\n%s", out)
	}
}

func TestWALAppendFailureIsReportedWithoutErrBuffered(t *testing.T) {
	logs := &syncBuffer{}
	svc, tmp := newService(t, failingInserts{memsession.New()}, logs)
	// A WAL path under a regular file cannot be created.
	blocker := filepath.Join(tmp, "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	svc.WALPath = filepath.Join(blocker, "events.wal")
	_, err := svc.Handle(context.Background(), session.Doc{"session_id": "lost"})
	if err == nil || errors.Is(err, ErrBuffered) {
		t.Fatalf("err = %v, want a non-buffered failure", err)
	}
	if out := logs.String(); !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "WAL append failed") {
		t.Errorf("expected WAL append error, got logs:\n%s", out)
	}
}

func TestFlushWALReplaysInOrderAndDrainsTheFile(t *testing.T) {
	logs := &syncBuffer{}
	store := &recordingInserts{Store: memsession.New()}
	svc, _ := newService(t, store, logs)
	wal := `{"session_id":"wal-flush-1","tool_name":"Bash","created_at":"2026-01-02T03:04:05Z"}` + "\n" +
		`{"session_id":"wal-flush-2","tool_name":"Read"}` + "\n" +
		`{"session_id":"wal-flush-3","tool_name":"Edit","created_at":"2026-01-02T03:04:06.123456789Z"}` + "\n"
	if err := os.MkdirAll(filepath.Dir(svc.WALPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.WALPath, []byte(wal), 0o600); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Truncate(time.Millisecond)
	if err := svc.FlushWAL(context.Background()); err != nil {
		t.Fatalf("FlushWAL: %v", err)
	}
	store.mu.Lock()
	order := append([]string(nil), store.ids...)
	store.mu.Unlock()
	if strings.Join(order, ",") != "wal-flush-1,wal-flush-2,wal-flush-3" {
		t.Fatalf("replay order: %v", order)
	}
	byID := map[string]session.Doc{}
	for _, ev := range hookEvents(t, store) {
		id, _ := ev.String("session_id")
		byID[id] = ev
	}
	if len(byID) != 3 || byID["wal-flush-1"]["tool_name"] != "Bash" || byID["wal-flush-2"]["tool_name"] != "Read" {
		t.Fatalf("events: %v", byID)
	}
	if at, ok := byID["wal-flush-1"]["created_at"].(time.Time); !ok || !at.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("created_at: %v", byID["wal-flush-1"]["created_at"])
	}
	if at, ok := byID["wal-flush-3"]["created_at"].(time.Time); !ok || !at.Equal(time.Date(2026, 1, 2, 3, 4, 6, 123456789, time.UTC)) {
		t.Fatalf("nanosecond created_at: %v", byID["wal-flush-3"]["created_at"])
	}
	if at, ok := byID["wal-flush-2"]["created_at"].(time.Time); !ok || at.Before(before) {
		t.Fatalf("missing created_at should be stamped now: %v", byID["wal-flush-2"]["created_at"])
	}
	if data, _ := os.ReadFile(svc.WALPath); strings.TrimSpace(string(data)) != "" {
		t.Fatalf("WAL not drained: %q", data)
	}
	if out := logs.String(); out != "" {
		t.Fatalf("unexpected logs: %s", out)
	}
}

func TestFlushWALWithoutAFileIsANoOp(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.FlushWAL(context.Background()); err != nil {
		t.Fatalf("FlushWAL: %v", err)
	}
}

func TestFallbackThenFlushRoundTrips(t *testing.T) {
	logs := &syncBuffer{}
	store := memsession.New()
	svc, _ := newService(t, failingInserts{store}, logs)
	if _, err := svc.Handle(context.Background(), session.Doc{"session_id": "rt-1", "tool_name": "Bash"}); !errors.Is(err, ErrBuffered) {
		t.Fatalf("err = %v", err)
	}
	if _, err := svc.Handle(context.Background(), session.Doc{"session_id": "rt-2", "tool_name": "Read"}); !errors.Is(err, ErrBuffered) {
		t.Fatalf("err = %v", err)
	}
	logs.Reset()
	svc.Store = store // the store recovers
	if err := svc.FlushWAL(context.Background()); err != nil {
		t.Fatalf("FlushWAL: %v", err)
	}
	evs := hookEvents(t, store)
	if len(evs) != 2 {
		t.Fatalf("want 2 replayed events, got %d", len(evs))
	}
	for i, id := range []string{"rt-1", "rt-2"} {
		ev := evs[i]
		if got, _ := ev.String("session_id"); got != id {
			t.Fatalf("event %d: %v", i, ev)
		}
		if ev["account_id"] != testAccount || hostname(ev) != "test-host" {
			t.Fatalf("stamps lost on the WAL round trip: %v", ev)
		}
		if _, ok := ev["created_at"].(time.Time); !ok {
			t.Fatalf("created_at not restored to a time: %v", ev["created_at"])
		}
	}
	if out := logs.String(); out != "" {
		t.Fatalf("unexpected logs: %s", out)
	}
}

func TestFlushWALStopsAtATimedOutInsertAndKeepsTheRest(t *testing.T) {
	logs := &syncBuffer{}
	svc, _ := newService(t, hangingInserts{memsession.New()}, logs)
	svc.Timeout = 50 * time.Millisecond
	wal := `{"session_id":"w1"}` + "\n" + `{"session_id":"w2"}` + "\n" + `{"session_id":"w3"}` + "\n"
	if err := os.MkdirAll(filepath.Dir(svc.WALPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(svc.WALPath, []byte(wal), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := svc.FlushWAL(context.Background()); err != nil {
		t.Fatalf("FlushWAL: %v", err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("flush waited out every line: %v", took)
	}
	data, err := os.ReadFile(svc.WALPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != wal {
		t.Fatalf("WAL changed during an outage:\n%s", data)
	}
}

func TestHandleBuffersBehindAFlushDuringAnOutage(t *testing.T) {
	logs := &syncBuffer{}
	svc, _ := newService(t, hangingInserts{memsession.New()}, logs)
	svc.Timeout = 100 * time.Millisecond
	if err := svc.AppendWAL(session.Doc{"session_id": "w1"}); err != nil {
		t.Fatal(err)
	}
	flushed := make(chan struct{})
	go func() {
		_ = svc.FlushWAL(context.Background())
		close(flushed)
	}()
	time.Sleep(20 * time.Millisecond) // let the flush take the WAL lock
	done := make(chan error, 1)
	go func() {
		_, err := svc.Handle(context.Background(), session.Doc{"session_id": "live"})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrBuffered) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("event blocked behind a WAL flush during a store outage")
	}
	<-flushed
	data, err := os.ReadFile(svc.WALPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"w1", "live"} {
		if !strings.Contains(string(data), `"session_id":"`+id+`"`) {
			t.Errorf("WAL lost %s: %s", id, data)
		}
	}
}

func TestDetachRunsBookkeepingOffTheCaller(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var pending []func(context.Context)
	f.svc.Detach = func(run func(ctx context.Context)) {
		mu.Lock()
		pending = append(pending, run)
		mu.Unlock()
	}
	repo := filepath.Join(f.tmp, "repo")
	tr := f.handle(t, session.Doc{"session_id": "detached", "cwd": repo})
	if !tr.FirstSeen {
		t.Fatalf("tracked: %+v", tr)
	}
	// The event is stored at once; the session waits for the detached work.
	if n := len(hookEvents(t, f.store)); n != 1 {
		t.Fatalf("want 1 event, got %d", n)
	}
	if sessionDoc(f.store, "detached") != nil {
		t.Fatal("session upserted on the calling goroutine")
	}
	mu.Lock()
	runs := pending
	pending = nil
	mu.Unlock()
	if len(runs) != 2 { // upsert and touch
		t.Fatalf("want 2 detached runs, got %d", len(runs))
	}
	for _, run := range runs {
		run(context.Background())
	}
	sd := sessionDoc(f.store, "detached")
	if sd == nil || sd["git_origin"] != "https://github.com/test/repo.git" {
		t.Fatalf("session: %v", sd)
	}
	// A later event without an origin to fill detaches only the touch.
	f.handle(t, session.Doc{"session_id": "detached", "cwd": repo})
	mu.Lock()
	defer mu.Unlock()
	if len(pending) != 1 {
		t.Fatalf("want 1 detached run for a later event, got %d", len(pending))
	}
}

func TestBookkeepingErrorsAreLoggedNotReturned(t *testing.T) {
	logs := &syncBuffer{}
	svc, _ := newService(t, brokenSessions{memsession.New()}, logs)
	tr, err := svc.Handle(context.Background(), session.Doc{"session_id": "broken", "cwd": "/tmp"})
	if err != nil || !tr.FirstSeen {
		t.Fatalf("Handle: %v %+v", err, tr)
	}
	out := logs.String()
	for _, want := range []string{"upsert session failed", "touch session failed"} {
		if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, want) {
			t.Errorf("expected %q in logs:\n%s", want, out)
		}
	}
}

// brokenSessions is a real store whose session writes fail.
type brokenSessions struct{ session.Store }

func (brokenSessions) UpsertSession(context.Context, session.Session) error {
	return errors.New("sessions down")
}

func (brokenSessions) TouchSession(context.Context, string, string, time.Time) error {
	return errors.New("sessions down")
}

func TestDefaultsApplyToAZeroLoggerAndTimeout(t *testing.T) {
	svc := &Service{Store: memsession.New()}
	if svc.timeout() != DefaultTimeout {
		t.Fatalf("timeout: %v", svc.timeout())
	}
	if svc.logger() == nil {
		t.Fatal("nil logger")
	}
	if _, err := svc.Handle(context.Background(), session.Doc{"session_id": "zero"}); err != nil {
		t.Fatal(err)
	}
}
