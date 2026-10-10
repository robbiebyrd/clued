package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/tail"
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

// failingInserts is a real memsession whose hook-event inserts fail.
type failingInserts struct{ *memsession.Store }

func (failingInserts) InsertHookEvent(context.Context, session.Doc) error {
	return errors.New("store down")
}

// fakeGit reports an origin for directories named "repo" only.
func fakeGit(_ context.Context, dir string) (string, string) {
	if filepath.Base(dir) == "repo" {
		return "https://github.com/test/repo.git", "main"
	}
	return "", ""
}

type fixture struct {
	d      *Daemon
	store  *memsession.Store
	srv    *httptest.Server
	logs   *syncBuffer
	tmp    string
	walDir string
}

func newOptions(t *testing.T, store session.Store, logs *syncBuffer) (Options, string) {
	t.Helper()
	tmp := t.TempDir()
	return Options{
		Config: session.Config{
			WalPath:        filepath.Join(tmp, "wal", "events.wal"),
			ProjectsDir:    filepath.Join(tmp, "projects"),
			FileHistoryDir: filepath.Join(tmp, "file-history"),
		},
		Store:            store,
		AccountID:        testAccount,
		Host:             session.HostInfo{Hostname: "test-host"},
		Git:              fakeGit,
		Logger:           slog.New(slog.NewTextHandler(logs, nil)),
		Tail:             tail.Options{Interval: 10 * time.Millisecond, WaitInterval: 10 * time.Millisecond, Logger: slog.New(slog.NewTextHandler(logs, nil))},
		EnrichInterval:   20 * time.Millisecond,
		WALFlushInterval: 20 * time.Millisecond,
	}, tmp
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	logs := &syncBuffer{}
	store := memsession.New()
	opts, tmp := newOptions(t, store, logs)
	d := New(opts)
	srv := httptest.NewServer(d.Handler())
	t.Cleanup(func() {
		srv.Close()
		d.shutdown()
		if out := logs.String(); out != "" {
			t.Errorf("unexpected daemon log output:\n%s", out)
		}
	})
	return &fixture{d: d, store: store, srv: srv, logs: logs, tmp: tmp}
}

func (f *fixture) post(t *testing.T, body string) (int, string) {
	t.Helper()
	res, err := http.Post(f.srv.URL+"/event", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func (f *fixture) get(t *testing.T, path string) (int, string) {
	t.Helper()
	res, err := http.Get(f.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
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

func TestHealth(t *testing.T) {
	f := newFixture(t)
	code, body := f.get(t, "/health")
	if code != 200 || body != "ok" {
		t.Fatalf("got %d %q", code, body)
	}
}

func TestUnknownRoutesReturn404(t *testing.T) {
	f := newFixture(t)
	if code, _ := f.get(t, "/unknown"); code != 404 {
		t.Fatalf("GET /unknown: %d", code)
	}
	if code, _ := f.get(t, "/event"); code != 404 {
		t.Fatalf("GET /event: %d", code)
	}
	res, err := http.Post(f.srv.URL+"/health", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 404 {
		t.Fatalf("POST /health: %d", res.StatusCode)
	}
}

func TestEventIsStoredWithAccountHostAndTimestamp(t *testing.T) {
	f := newFixture(t)
	before := time.Now()
	code, body := f.post(t, `{"session_id":"s1","type":"PreToolUse","tool_name":"Bash"}`)
	if code != 200 || body != "ok" {
		t.Fatalf("got %d %q", code, body)
	}
	evs := hookEvents(t, f.store)
	if len(evs) != 1 {
		t.Fatalf("want 1 event, got %d", len(evs))
	}
	ev := evs[0]
	if ev["tool_name"] != "Bash" || ev["account_id"] != testAccount {
		t.Fatalf("event: %v", ev)
	}
	if h, ok := ev["host"].(session.HostInfo); !ok || h.Hostname != "test-host" {
		t.Fatalf("host: %v", ev["host"])
	}
	if at, ok := ev["created_at"].(time.Time); !ok || at.Before(before) {
		t.Fatalf("created_at: %v", ev["created_at"])
	}
}

func TestMalformedJSONReturns400(t *testing.T) {
	f := newFixture(t)
	for _, body := range []string{`{not json`, `null`, `[1]`} {
		code, msg := f.post(t, body)
		if code != 400 || msg == "" {
			t.Errorf("%q: got %d %q", body, code, msg)
		}
	}
	if n := len(hookEvents(t, f.store)); n != 0 {
		t.Fatalf("bad bodies stored %d events", n)
	}
}

func TestSessionCreatedAndTranscriptTailed(t *testing.T) {
	f := newFixture(t)
	transcript := filepath.Join(f.tmp, "projects", "sess-2.jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"user","message":{"role":"user","content":"hello"}}` + "\nnot json\n"
	if err := os.WriteFile(transcript, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"session_id":"sess-2","transcript_path":%q,"cwd":"/tmp"}`, transcript)
	if code, _ := f.post(t, body); code != 200 {
		t.Fatalf("status %d", code)
	}
	eventually(t, "session doc", func() bool { return sessionDoc(f.store, "sess-2") != nil })
	sd := sessionDoc(f.store, "sess-2")
	if sd["transcript_path"] != transcript || sd["account_id"] != testAccount {
		t.Fatalf("session: %v", sd)
	}
	eventually(t, "transcript lines", func() bool {
		n, _ := f.store.CountTranscriptLines(context.Background(), testAccount, "sess-2")
		return n == 2
	})
	lines, err := f.store.TranscriptLines(context.Background(), session.LineQuery{AccountID: testAccount, SessionID: "sess-2"})
	if err != nil {
		t.Fatal(err)
	}
	if lines[0]["seq"] != 0 || lines[1]["seq"] != 1 {
		t.Fatalf("seq: %v %v", lines[0]["seq"], lines[1]["seq"])
	}
	if raw := lines[1].Line()["raw"]; raw != "not json" {
		t.Fatalf("non-JSON line: %v", lines[1])
	}
	if lines[0].Message()["role"] != "user" {
		t.Fatalf("parsed line: %v", lines[0])
	}
}

func TestArtifactDirsAreWatched(t *testing.T) {
	f := newFixture(t)
	projDir := filepath.Join(f.tmp, "projects")
	transcript := filepath.Join(projDir, "sess-art.jsonl")
	resultDir := filepath.Join(projDir, "sess-art", "tool-results")
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, "result.txt"), []byte("daemon wired output"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.post(t, fmt.Sprintf(`{"session_id":"sess-art","transcript_path":%q}`, transcript))
	eventually(t, "tool-result blob", func() bool {
		blobs, _ := f.store.Blobs(context.Background(), testAccount, "sess-art")
		return len(blobs) == 1 && blobs[0]["content"] == "daemon wired output"
	})
}

func TestGitOriginSetAtCreation(t *testing.T) {
	f := newFixture(t)
	repo := filepath.Join(f.tmp, "repo")
	f.post(t, fmt.Sprintf(`{"session_id":"git-now","cwd":%q}`, repo))
	eventually(t, "git origin", func() bool {
		d := sessionDoc(f.store, "git-now")
		return d != nil && d["git_origin"] == "https://github.com/test/repo.git" && d["git_branch"] == "main"
	})
}

func TestNoGitOriginForNonGitCwd(t *testing.T) {
	f := newFixture(t)
	f.post(t, `{"session_id":"git-never","cwd":"/tmp"}`)
	eventually(t, "session doc", func() bool { return sessionDoc(f.store, "git-never") != nil })
	if _, ok := sessionDoc(f.store, "git-never")["git_origin"]; ok {
		t.Fatal("git_origin must be absent")
	}
}

func TestGitOriginFilledByLaterEvent(t *testing.T) {
	f := newFixture(t)
	f.post(t, `{"session_id":"git-late","transcript_path":"`+filepath.Join(f.tmp, "projects", "late.jsonl")+`"}`)
	eventually(t, "session doc", func() bool { return sessionDoc(f.store, "git-late") != nil })
	if _, ok := sessionDoc(f.store, "git-late")["git_origin"]; ok {
		t.Fatal("git_origin should not be set yet")
	}
	f.post(t, fmt.Sprintf(`{"session_id":"git-late","cwd":%q}`, filepath.Join(f.tmp, "repo")))
	eventually(t, "git origin", func() bool {
		d := sessionDoc(f.store, "git-late")
		return d["git_origin"] == "https://github.com/test/repo.git" && d["git_branch"] == "main"
	})
}

func TestEventTouchesLastSeen(t *testing.T) {
	f := newFixture(t)
	f.post(t, `{"session_id":"touch"}`)
	eventually(t, "session doc", func() bool { return sessionDoc(f.store, "touch") != nil })
	first, _ := sessionDoc(f.store, "touch")["last_seen"].(time.Time)
	time.Sleep(20 * time.Millisecond)
	f.post(t, `{"session_id":"touch"}`)
	eventually(t, "last_seen advance", func() bool {
		at, _ := sessionDoc(f.store, "touch")["last_seen"].(time.Time)
		return at.After(first)
	})
}

func TestInsertFailureFallsBackToWAL(t *testing.T) {
	logs := &syncBuffer{}
	store := failingInserts{memsession.New()}
	opts, _ := newOptions(t, store, logs)
	d := New(opts)
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()
	defer d.shutdown()

	res, err := http.Post(srv.URL+"/event", "application/json", strings.NewReader(`{"session_id":"wal-1","tool_name":"Bash"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(b) != "ok" {
		t.Fatalf("got %d %q", res.StatusCode, b)
	}
	data, err := os.ReadFile(opts.Config.WalPath)
	if err != nil {
		t.Fatal(err)
	}
	line := string(data)
	for _, want := range []string{`"session_id":"wal-1"`, `"account_id":"` + testAccount + `"`, `"hostname":"test-host"`, `"created_at":"20`} {
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

func startRun(t *testing.T, d *Daemon, addr string) (cancel func(), done chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done = make(chan error, 1)
	go func() { done <- d.Run(ctx, addr) }()
	return cancel, done
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func waitHealthy(t *testing.T, addr string) {
	t.Helper()
	eventually(t, "health endpoint", func() bool {
		res, err := http.Get("http://" + addr + "/health")
		if err != nil {
			return false
		}
		res.Body.Close()
		return res.StatusCode == 200
	})
}

func TestRunFlushesWALIntoStoreOnStart(t *testing.T) {
	logs := &syncBuffer{}
	store := memsession.New()
	opts, _ := newOptions(t, store, logs)
	opts.WALFlushInterval = time.Hour
	wal := `{"session_id":"wal-flush-1","tool_name":"Bash","created_at":"2026-01-02T03:04:05Z"}` + "\n" +
		`{"session_id":"wal-flush-2","tool_name":"Read"}` + "\n"
	if err := os.MkdirAll(filepath.Dir(opts.Config.WalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.Config.WalPath, []byte(wal), 0o600); err != nil {
		t.Fatal(err)
	}
	d := New(opts)
	addr := freeAddr(t)
	cancel, done := startRun(t, d, addr)
	waitHealthy(t, addr)
	eventually(t, "flushed events", func() bool { return len(hookEvents(t, store)) == 2 })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	evs := hookEvents(t, store)
	if evs[0]["tool_name"] != "Bash" || evs[1]["tool_name"] != "Read" {
		t.Fatalf("events: %v", evs)
	}
	if at, ok := evs[0]["created_at"].(time.Time); !ok || !at.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Fatalf("created_at: %v", evs[0]["created_at"])
	}
	if data, _ := os.ReadFile(opts.Config.WalPath); strings.TrimSpace(string(data)) != "" {
		t.Fatalf("WAL not drained: %q", data)
	}
	if out := logs.String(); out != "" {
		t.Fatalf("unexpected logs: %s", out)
	}
}

func TestRunFlushesWALPeriodically(t *testing.T) {
	logs := &syncBuffer{}
	store := memsession.New()
	opts, _ := newOptions(t, store, logs)
	d := New(opts)
	addr := freeAddr(t)
	cancel, done := startRun(t, d, addr)
	waitHealthy(t, addr)
	// An entry that appears after start is picked up by the ticker.
	if err := d.appendWAL(session.Doc{"session_id": "late-wal"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "ticker flush", func() bool { return len(hookEvents(t, store)) == 1 })
	cancel()
	<-done
}

func TestRunReturnsErrAddrInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	logs := &syncBuffer{}
	opts, _ := newOptions(t, memsession.New(), logs)
	err = New(opts).Run(context.Background(), ln.Addr().String())
	if !errors.Is(err, ErrAddrInUse) {
		t.Fatalf("got %v", err)
	}
}

func TestRunStopsCleanlyOnCancel(t *testing.T) {
	logs := &syncBuffer{}
	store := memsession.New()
	opts, tmp := newOptions(t, store, logs)
	d := New(opts)
	addr := freeAddr(t)
	cancel, done := startRun(t, d, addr)
	waitHealthy(t, addr)
	transcript := filepath.Join(tmp, "projects", "run.jsonl")
	body := fmt.Sprintf(`{"session_id":"run-1","transcript_path":%q}`, transcript)
	res, err := http.Post("http://"+addr+"/event", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if _, err := http.Get("http://" + addr + "/health"); err == nil {
		t.Fatal("server still listening")
	}
	// A tailer stopped by shutdown no longer delivers lines.
	if err := os.MkdirAll(filepath.Dir(transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if n, _ := store.CountTranscriptLines(context.Background(), testAccount, "run-1"); n != 0 {
		t.Fatalf("tailer still running: %d lines", n)
	}
	if out := logs.String(); out != "" {
		t.Fatalf("unexpected logs: %s", out)
	}
}

func (f *fixture) expectWarn(t *testing.T, want string) {
	t.Helper()
	out := f.logs.String()
	if !strings.Contains(out, "level=WARN") || !strings.Contains(out, want) {
		t.Fatalf("expected warning containing %q, got:\n%s", want, out)
	}
	f.logs.mu.Lock()
	f.logs.buf.Reset()
	f.logs.mu.Unlock()
}

func (f *fixture) trackedCount() int {
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	return len(f.d.tracked)
}

func TestUnsafeSessionIDIsStoredButNotTracked(t *testing.T) {
	f := newFixture(t)
	transcript := filepath.Join(f.tmp, "projects", "x.jsonl")
	for _, id := range []string{"../evil", "a/b", "..", "/abs"} {
		body := fmt.Sprintf(`{"session_id":%q,"transcript_path":%q}`, id, transcript)
		if code, _ := f.post(t, body); code != 200 {
			t.Fatalf("%q: status %d", id, code)
		}
		f.expectWarn(t, "session_id is not a single safe path element")
	}
	if n := len(hookEvents(t, f.store)); n != 4 {
		t.Fatalf("want 4 stored events, got %d", n)
	}
	if n := f.trackedCount(); n != 0 || len(f.d.stops) != 0 {
		t.Fatalf("unsafe sessions tracked: %d", n)
	}
}

func TestTranscriptOutsideProjectsDirIsNotTailed(t *testing.T) {
	f := newFixture(t)
	outside := filepath.Join(f.tmp, "elsewhere", "s.jsonl")
	if err := os.MkdirAll(filepath.Dir(outside), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(`{"a":1}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{outside, "relative/s.jsonl", filepath.Join(f.tmp, "projects", "..", "elsewhere", "s.jsonl"), filepath.Join(f.tmp, "projects-evil", "s.jsonl")} {
		if code, _ := f.post(t, fmt.Sprintf(`{"session_id":"out","transcript_path":%q}`, p)); code != 200 {
			t.Fatalf("%q: status %d", p, code)
		}
		f.d.mu.Lock()
		delete(f.d.tracked, "out")
		f.d.mu.Unlock()
		f.expectWarn(t, "transcript_path is outside the projects directory")
	}
	time.Sleep(100 * time.Millisecond)
	if n, _ := f.store.CountTranscriptLines(context.Background(), testAccount, "out"); n != 0 {
		t.Fatalf("outside transcript tailed: %d lines", n)
	}
	f.d.mu.Lock()
	defer f.d.mu.Unlock()
	if len(f.d.stops) != 0 {
		t.Fatalf("tailers started: %d", len(f.d.stops))
	}
}

func TestOriginHeaderIsRejected(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ method, path string }{{"POST", "/event"}, {"GET", "/health"}} {
		req, _ := http.NewRequest(tc.method, f.srv.URL+tc.path, strings.NewReader(`{"session_id":"o"}`))
		req.Header.Set("Origin", "https://evil.example")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("%s %s: %d", tc.method, tc.path, res.StatusCode)
		}
	}
	if n := len(hookEvents(t, f.store)); n != 0 {
		t.Fatalf("cross-origin event stored: %d", n)
	}
}

func TestOversizeBodyReturns413(t *testing.T) {
	f := newFixture(t)
	big := `{"x":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	code, _ := f.post(t, big)
	if code != 413 {
		t.Fatalf("got %d", code)
	}
	if n := len(hookEvents(t, f.store)); n != 0 {
		t.Fatalf("oversize event stored: %d", n)
	}
}

// slowStore blocks SetGitInfoIfMissing and TouchSession until released.
type slowStore struct {
	*memsession.Store
	release chan struct{}
}

func (s slowStore) SetGitInfoIfMissing(ctx context.Context, id, o, b string) error {
	<-s.release
	return s.Store.SetGitInfoIfMissing(ctx, id, o, b)
}

func (s slowStore) TouchSession(ctx context.Context, a, id string, at time.Time) error {
	<-s.release
	return s.Store.TouchSession(ctx, a, id, at)
}

func TestSlowStoreDoesNotStallHooks(t *testing.T) {
	logs := &syncBuffer{}
	store := slowStore{memsession.New(), make(chan struct{})}
	opts, tmp := newOptions(t, store, logs)
	d := New(opts)
	srv := httptest.NewServer(d.Handler())
	defer srv.Close()
	defer d.shutdown()
	defer close(store.release)

	post := func(body string) {
		done := make(chan struct{})
		go func() {
			res, err := http.Post(srv.URL+"/event", "application/json", strings.NewReader(body))
			if err == nil {
				res.Body.Close()
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("hook stalled behind a slow store")
		}
	}
	post(`{"session_id":"slow"}`)
	eventually(t, "session doc", func() bool { return sessionDoc(store, "slow") != nil })
	// Late git lookup parks in the store; further hooks must still be served.
	post(fmt.Sprintf(`{"session_id":"slow","cwd":%q}`, filepath.Join(tmp, "repo")))
	post(`{"session_id":"slow"}`)
	post(`{"session_id":"other"}`)
}
