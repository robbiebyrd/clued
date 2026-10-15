package remotesession_test

import (
	"context"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/remotesession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessiontest"
)

var ctx = context.Background()

const (
	// tokenPrefix + account is the bearer token the stub source resolves to
	// a principal of that account, so one harness can act as any account.
	tokenPrefix = "token-"
	// defaultAccount is the account the suite uses whenever a port call
	// carries none (SetGitInfoIfMissing, HookEventIDsByToolUse, a query with
	// an empty AccountID). A server always acts as one principal, so the
	// harness acts as this one.
	defaultAccount = "acc"
	// unscopedAccount is the principal a hook event without account_id is
	// written as; the server stamps every write, and the harness strips this
	// stamp again where the suite reads the raw document (Unenriched).
	unscopedAccount = "unscoped"
)

// stubSource resolves "token-<account>" to a principal of that account.
type stubSource struct{}

func (stubSource) Lookup(raw string) (auth.Principal, bool) {
	account, ok := strings.CutPrefix(raw, tokenPrefix)
	if !ok || account == "" {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: account, AccountID: account}, true
}

// accountFromContext is sessionapi's Options.Account behind auth.Middleware.
func accountFromContext(r *http.Request) (string, bool) {
	p, ok := auth.FromContext(r.Context())
	return p.AccountID, ok
}

// server is a sessionapi server over a fresh memsession behind the stub
// auth, plain or TLS, counting the requests it serves per method and path.
type server struct {
	backing  *memsession.Store
	srv      *httptest.Server
	cfg      session.Config // ServerURL and ServerCAFile; the token is set per client
	mu       sync.Mutex
	requests map[string]int
}

func newServer(t *testing.T, tls bool, opts sessionapi.Options) *server {
	t.Helper()
	s := &server{backing: memsession.New(), requests: map[string]int{}}
	opts.Account = accountFromContext
	h := auth.Middleware(stubSource{})(sessionapi.Handler(s.backing, opts))
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	})
	if tls {
		s.srv = httptest.NewTLSServer(counted)
		s.cfg.ServerCAFile = writeCA(t, s.srv)
	} else {
		s.srv = httptest.NewServer(counted)
	}
	t.Cleanup(s.srv.Close)
	s.cfg.ServerURL = s.srv.URL
	return s
}

// writeCA writes the test server's certificate as a PEM bundle, the form a
// serverCaFile takes.
func writeCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	block := &pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (s *server) count(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[method+" "+sessionapi.Prefix+path]
}

// client returns the adapter acting as account.
func (s *server) client(t *testing.T, account string) session.Store {
	t.Helper()
	cfg := s.cfg
	cfg.ServerToken = tokenPrefix + account
	c, err := remote.New(cfg)
	if err != nil {
		t.Fatalf("remote.New: %v", err)
	}
	return remotesession.New(c)
}

// harness is the session.Store the conformance suite runs against. Every
// port call goes through the remote adapter as the principal the call names
// (a server scopes everything to its principal, so acting as the right one is
// what makes the account-scoping cases meaningful); only the enrichment
// methods, which the server does not expose and the adapter reports as
// unsupported, go to the backing store directly, as the server's own
// enrichment loop would.
type harness struct {
	t       *testing.T
	server  *server
	mu      sync.Mutex
	clients map[string]session.Store
}

var _ session.Store = (*harness)(nil)

func newHarness(t *testing.T, tls bool) *harness {
	t.Helper()
	return &harness{t: t, server: newServer(t, tls, sessionapi.Options{}), clients: map[string]session.Store{}}
}

// as returns the adapter acting as account, or as the default account when
// the call carried none.
func (h *harness) as(account string) session.Store {
	if account == "" {
		account = defaultAccount
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.clients[account]; ok {
		return c
	}
	c := h.server.client(h.t, account)
	h.clients[account] = c
	return c
}

func (h *harness) UpsertSession(ctx context.Context, s session.Session) error {
	return h.as(s.AccountID).UpsertSession(ctx, s)
}

func (h *harness) TouchSession(ctx context.Context, accountID, sessionID string, at time.Time) error {
	return h.as(accountID).TouchSession(ctx, accountID, sessionID, at)
}

func (h *harness) SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error {
	return h.as("").SetGitInfoIfMissing(ctx, sessionID, origin, branch)
}

func (h *harness) SetModel(ctx context.Context, accountID, sessionID, model string, at time.Time) (string, bool, error) {
	return h.as(accountID).SetModel(ctx, accountID, sessionID, model, at)
}

func (h *harness) SetSessionMeta(ctx context.Context, accountID, sessionID string, fields session.Doc) error {
	return h.as(accountID).SetSessionMeta(ctx, accountID, sessionID, fields)
}

func (h *harness) SetLiveState(ctx context.Context, accountID, sessionID, state, reason string, at time.Time) (string, bool, error) {
	return h.as(accountID).SetLiveState(ctx, accountID, sessionID, state, reason, at)
}

func (h *harness) StateChanges(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).StateChanges(ctx, q)
}

func (h *harness) RejectedToolUses(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).RejectedToolUses(ctx, q)
}

func (h *harness) LastModelBefore(ctx context.Context, sessionID string, seq int, at time.Time) (string, error) {
	return h.as("").LastModelBefore(ctx, sessionID, seq, at)
}

func (h *harness) GetSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	return h.as(accountID).GetSession(ctx, accountID, sessionID)
}

func (h *harness) FindSessions(ctx context.Context, q session.SessionQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).FindSessions(ctx, q)
}

func (h *harness) InsertHookEvent(ctx context.Context, ev session.Doc) error {
	account, _ := ev.String("account_id")
	if account == "" {
		account = unscopedAccount
	}
	return h.as(account).InsertHookEvent(ctx, ev)
}

func (h *harness) HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error) {
	return h.as("").HookEventIDsByToolUse(ctx, sessionID, toolUseIDs)
}

// UpsertTranscriptLines sends each account's lines as that account, keeping
// the order within an account.
func (h *harness) UpsertTranscriptLines(ctx context.Context, lines []session.TranscriptLine) error {
	var order []string
	byAccount := map[string][]session.TranscriptLine{}
	for _, l := range lines {
		if _, seen := byAccount[l.AccountID]; !seen {
			order = append(order, l.AccountID)
		}
		byAccount[l.AccountID] = append(byAccount[l.AccountID], l)
	}
	for _, account := range order {
		if err := h.as(account).UpsertTranscriptLines(ctx, byAccount[account]); err != nil {
			return err
		}
	}
	return nil
}

func (h *harness) CountTranscriptLines(ctx context.Context, accountID, sessionID string) (int, error) {
	return h.as(accountID).CountTranscriptLines(ctx, accountID, sessionID)
}

func (h *harness) TranscriptLines(ctx context.Context, q session.LineQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).TranscriptLines(ctx, q)
}

func (h *harness) UpsertSubagentLine(ctx context.Context, l session.SubagentLine) error {
	return h.as(l.AccountID).UpsertSubagentLine(ctx, l)
}

func (h *harness) SubagentIDs(ctx context.Context, accountID, sessionID string) ([]string, error) {
	return h.as(accountID).SubagentIDs(ctx, accountID, sessionID)
}

func (h *harness) SubagentLines(ctx context.Context, accountID, sessionID, subagentID string) ([]session.Doc, error) {
	return h.as(accountID).SubagentLines(ctx, accountID, sessionID, subagentID)
}

func (h *harness) UpsertBlob(ctx context.Context, b session.Blob) error {
	return h.as(b.AccountID).UpsertBlob(ctx, b)
}

func (h *harness) Blobs(ctx context.Context, accountID, sessionID string) ([]session.Doc, error) {
	return h.as(accountID).Blobs(ctx, accountID, sessionID)
}

func (h *harness) BashEvents(ctx context.Context, q session.CommandQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).BashEvents(ctx, q)
}

func (h *harness) MemoryEvents(ctx context.Context, q session.MemoryQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).MemoryEvents(ctx, q)
}

func (h *harness) FileEvents(ctx context.Context, q session.FileQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).FileEvents(ctx, q)
}

func (h *harness) ErrorLines(ctx context.Context, q session.ErrorQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).ErrorLines(ctx, q)
}

func (h *harness) Activities(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).Activities(ctx, q)
}

func (h *harness) SearchTranscripts(ctx context.Context, q session.TextQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).SearchTranscripts(ctx, q)
}

// SearchVectors is not served over the transport; the suite checks the
// backing store behind it.
func (h *harness) SearchVectors(ctx context.Context, q session.VectorQuery) ([]session.Doc, error) {
	return h.server.backing.SearchVectors(ctx, q)
}

// The hourly aggregates are not served over the transport either.
func (h *harness) ActivityCounts(ctx context.Context, q session.CountQuery) ([]session.HourCount, error) {
	return h.server.backing.ActivityCounts(ctx, q)
}

func (h *harness) ErrorCounts(ctx context.Context, q session.CountQuery) ([]session.HourCount, error) {
	return h.server.backing.ErrorCounts(ctx, q)
}

func (h *harness) LimitBuckets(ctx context.Context, q session.CountQuery) ([]session.LimitBucket, error) {
	return h.server.backing.LimitBuckets(ctx, q)
}

func (h *harness) UsageTotals(ctx context.Context, q session.UsageQuery) ([]session.UsageTotal, error) {
	return h.as(q.AccountID).UsageTotals(ctx, q)
}

// DeleteSessions without an account cannot be sent (the token names the
// account), so that case goes to the backing store.
func (h *harness) DeleteSessions(ctx context.Context, q session.ForgetQuery) (session.Forgotten, error) {
	if q.AccountID == "" {
		return h.server.backing.DeleteSessions(ctx, q)
	}
	return h.as(q.AccountID).DeleteSessions(ctx, q)
}

func (h *harness) LimitSamples(ctx context.Context, q session.LimitQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).LimitSamples(ctx, q)
}

// InsertOtelEvents sends each account's events with that account's token.
func (h *harness) InsertOtelEvents(ctx context.Context, events []session.OtelEvent) (int, error) {
	byAccount := map[string][]session.OtelEvent{}
	var order []string
	for _, e := range events {
		if _, seen := byAccount[e.AccountID]; !seen {
			order = append(order, e.AccountID)
		}
		byAccount[e.AccountID] = append(byAccount[e.AccountID], e)
	}
	total := 0
	for _, account := range order {
		n, err := h.as(account).InsertOtelEvents(ctx, byAccount[account])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (h *harness) OtelEvents(ctx context.Context, q session.OtelQuery) ([]session.Doc, error) {
	return h.as(q.AccountID).OtelEvents(ctx, q)
}

func (h *harness) FullSession(ctx context.Context, accountID, sessionID string) (session.Doc, error) {
	return h.as(accountID).FullSession(ctx, accountID, sessionID)
}

// Unenriched reads the backing store; the account_id the server stamped on
// a hook event that carried none is removed again, so the suite sees what
// it wrote.
func (h *harness) Unenriched(ctx context.Context, collection, enricher string, limit int) ([]session.Doc, error) {
	docs, err := h.server.backing.Unenriched(ctx, collection, enricher, limit)
	for _, d := range docs {
		if d["account_id"] == unscopedAccount {
			delete(d, "account_id")
		}
	}
	return docs, err
}

func (h *harness) SetEnrichment(ctx context.Context, collection string, id any, enricher string, result any) error {
	return h.server.backing.SetEnrichment(ctx, collection, id, enricher, result)
}

func (h *harness) SetEnrichmentFailure(ctx context.Context, collection string, id any, enricher, message string, at time.Time) error {
	return h.server.backing.SetEnrichmentFailure(ctx, collection, id, enricher, message, at)
}

func (h *harness) SetEnrichmentSkipped(ctx context.Context, collection string, id any, enricher string) error {
	return h.server.backing.SetEnrichmentSkipped(ctx, collection, id, enricher)
}

func (h *harness) RequeueSkipped(ctx context.Context, collection, accountID, enricher string) (int64, error) {
	return h.server.backing.RequeueSkipped(ctx, collection, accountID, enricher)
}

func (h *harness) DeleteAccount(ctx context.Context, accountID string) (int64, error) {
	return h.server.backing.DeleteAccount(ctx, accountID)
}

func (h *harness) PurgeContent(ctx context.Context, accountID string, sessionIDs []string) (session.ContentPurged, error) {
	return h.server.backing.PurgeContent(ctx, accountID, sessionIDs)
}

func (h *harness) Close(ctx context.Context) error { return h.as("").Close(ctx) }

func TestConformanceOverHTTP(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) session.Store { return newHarness(t, false) })
}

func TestConformanceOverTLSWithPrivateCA(t *testing.T) {
	sessiontest.Run(t, func(t *testing.T) session.Store { return newHarness(t, true) })
}

func TestTLSServerIsRefusedWithoutItsCA(t *testing.T) {
	s := newServer(t, true, sessionapi.Options{})
	s.cfg.ServerCAFile = ""
	st := s.client(t, "acc")
	if _, err := st.GetSession(ctx, "acc", "s1"); err == nil || errors.Is(err, session.ErrNotFound) {
		t.Fatalf("GetSession over TLS without the CA: err = %v, want a certificate error", err)
	}
}

func TestMaxLinesMatchesTheServerDefault(t *testing.T) {
	if remotesession.MaxLines != sessionapi.DefaultMaxLines {
		t.Fatalf("MaxLines = %d, want sessionapi.DefaultMaxLines = %d", remotesession.MaxLines, sessionapi.DefaultMaxLines)
	}
}

func TestNotFoundIsMapped(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	st := s.client(t, "acc")
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s1", LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetSession(ctx, "acc", "missing"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("GetSession(missing) = %v, want ErrNotFound", err)
	}
	if _, err := st.FullSession(ctx, "acc", "missing"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("FullSession(missing) = %v, want ErrNotFound", err)
	}
	if _, err := s.client(t, "other").GetSession(ctx, "other", "s1"); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("GetSession as another account = %v, want ErrNotFound", err)
	}
	if _, err := st.GetSession(ctx, "acc", "s1"); err != nil {
		t.Errorf("GetSession(s1) = %v", err)
	}
}

func TestUnauthorizedIsReported(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	c, err := remote.New(session.Config{ServerURL: s.srv.URL, ServerToken: "bogus"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = remotesession.New(c).GetSession(ctx, "acc", "s1")
	if !errors.Is(err, remote.ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

func TestEnrichmentIsUnsupportedAndCloseIsANoOp(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	st := s.client(t, "acc")
	if _, err := st.Unenriched(ctx, "hook_events", "e", 10); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("Unenriched = %v, want ErrUnsupported", err)
	}
	if err := st.SetEnrichment(ctx, "hook_events", "1", "e", session.Doc{}); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("SetEnrichment = %v, want ErrUnsupported", err)
	}
	if err := st.SetEnrichmentFailure(ctx, "hook_events", "1", "e", "boom", time.Now()); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("SetEnrichmentFailure = %v, want ErrUnsupported", err)
	}
	if err := st.SetEnrichmentSkipped(ctx, "hook_events", "1", "e"); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("SetEnrichmentSkipped = %v, want ErrUnsupported", err)
	}
	if _, err := st.RequeueSkipped(ctx, "hook_events", "acct", "e"); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("RequeueSkipped = %v, want ErrUnsupported", err)
	}
	if _, err := st.SearchVectors(ctx, session.VectorQuery{Vector: []float32{1}}); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("SearchVectors = %v, want ErrUnsupported", err)
	}
	if _, err := st.ActivityCounts(ctx, session.CountQuery{}); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("ActivityCounts = %v, want ErrUnsupported", err)
	}
	if _, err := st.ErrorCounts(ctx, session.CountQuery{}); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("ErrorCounts = %v, want ErrUnsupported", err)
	}
	if _, err := st.LimitBuckets(ctx, session.CountQuery{}); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("LimitBuckets = %v, want ErrUnsupported", err)
	}
	if _, err := st.DeleteAccount(ctx, "acct"); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("DeleteAccount = %v, want ErrUnsupported", err)
	}
	if _, err := st.PurgeContent(ctx, "acct", nil); !errors.Is(err, session.ErrUnsupported) {
		t.Errorf("PurgeContent = %v, want ErrUnsupported", err)
	}
	if err := st.Close(ctx); err != nil {
		t.Errorf("Close = %v", err)
	}
	if n := len(s.requests); n != 0 {
		t.Errorf("%d requests were sent, want none", n)
	}
}

func TestTranscriptBatchesAreSplitAtMaxLines(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	st := s.client(t, "acc")
	lines := make([]session.TranscriptLine, 0, 2*remotesession.MaxLines+201)
	for i := range cap(lines) {
		lines = append(lines, session.TranscriptLine{SessionID: "s1", Seq: i, Line: map[string]any{"n": i}})
	}
	if err := st.UpsertTranscriptLines(ctx, lines); err != nil {
		t.Fatalf("UpsertTranscriptLines(%d lines): %v", len(lines), err)
	}
	if got := s.count("PUT", "/transcript-lines"); got != 3 {
		t.Errorf("PUT /transcript-lines was sent %d times, want 3", got)
	}
	if n, err := st.CountTranscriptLines(ctx, "acc", "s1"); err != nil || n != len(lines) {
		t.Errorf("count = %d, %v; want %d", n, err, len(lines))
	}
	if err := st.UpsertTranscriptLines(ctx, nil); err != nil {
		t.Errorf("empty batch: %v", err)
	}
	if got := s.count("PUT", "/transcript-lines"); got != 3 {
		t.Errorf("an empty batch sent a request (%d total)", got)
	}
}

func TestTranscriptBatchStopsAtTheFirstFailure(t *testing.T) {
	// A server that caps batches below MaxLines rejects the first request.
	s := newServer(t, false, sessionapi.Options{MaxLines: 10})
	st := s.client(t, "acc")
	lines := make([]session.TranscriptLine, 0, 2*remotesession.MaxLines)
	for i := range cap(lines) {
		lines = append(lines, session.TranscriptLine{SessionID: "s1", Seq: i, Line: map[string]any{}})
	}
	err := st.UpsertTranscriptLines(ctx, lines)
	if err == nil || !strings.Contains(err.Error(), "the limit is 10") {
		t.Fatalf("err = %v, want the server's batch error", err)
	}
	if got := s.count("PUT", "/transcript-lines"); got != 1 {
		t.Errorf("PUT /transcript-lines was sent %d times after a failure, want 1", got)
	}
}

func TestTooLargeIsMapped(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{MaxBody: 256})
	st := s.client(t, "acc")
	err := st.UpsertBlob(ctx, session.Blob{SessionID: "s1", BlobType: "tool-result", Name: "big", Content: strings.Repeat("x", 1024), Encoding: "utf8"})
	if !errors.Is(err, session.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestResultsAreTypedLikeTheLocalStores(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	st := s.client(t, "acc")
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.UTC)
	ip := "10.0.0.1"
	host := &session.HostInfo{Hostname: "h", IP: &ip, UID: 501}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.UpsertSession(ctx, session.Session{SessionID: "s1", Cwd: "/w", Host: host, LastSeen: t0}))
	must(st.UpsertTranscriptLines(ctx, []session.TranscriptLine{{SessionID: "s1", Seq: 7, Line: map[string]any{"timestamp": "2026-01-02T03:04:05.000Z"}, Host: host}}))
	must(st.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: "s1", SubagentID: "ag", Seq: 2, Line: map[string]any{}}))
	must(st.UpsertBlob(ctx, session.Blob{SessionID: "s1", BlobType: "tool-result", Name: "n", Content: "c", Encoding: "utf8"}))
	must(st.InsertHookEvent(ctx, session.Doc{"session_id": "s1", "tool_name": "Bash", "tool_input": session.Doc{"command": "ls"}, "created_at": t0}))
	// An enrichment failure written on the server comes back with its time.
	pending, err := s.backing.Unenriched(ctx, "transcript_lines", "e", 1)
	must(err)
	must(s.backing.SetEnrichmentFailure(ctx, "transcript_lines", pending[0]["_id"], "e", "boom", t0))

	got, err := st.GetSession(ctx, "acc", "s1")
	must(err)
	for _, k := range []string{"started_at", "last_seen"} {
		if at, ok := got[k].(time.Time); !ok || !at.Equal(t0) {
			t.Errorf("session %s = %#v, want time %v", k, got[k], t0)
		}
	}
	if uid, ok := got.Map("host")["uid"].(int); !ok || uid != 501 {
		t.Errorf("host.uid = %#v, want int 501", got.Map("host")["uid"])
	}

	lines, err := st.TranscriptLines(ctx, session.LineQuery{SessionID: "s1"})
	must(err)
	if seq, ok := lines[0]["seq"].(int); !ok || seq != 7 {
		t.Errorf("seq = %#v, want int 7", lines[0]["seq"])
	}
	if _, ok := lines[0]["created_at"].(time.Time); !ok {
		t.Errorf("created_at = %#v, want a time", lines[0]["created_at"])
	}
	if ts, ok := lines[0].Line()["timestamp"].(string); !ok || ts != "2026-01-02T03:04:05.000Z" {
		t.Errorf("line.timestamp = %#v, want the string left as written", lines[0].Line()["timestamp"])
	}
	failed := lines[0].Map("enrichments").Map("e_failed")
	if at, ok := failed["at"].(time.Time); !ok || !at.Equal(t0) {
		t.Errorf("enrichments.e_failed.at = %#v, want time %v", failed["at"], t0)
	}

	events, err := st.BashEvents(ctx, session.CommandQuery{PatternRe: "ls", Limit: 5})
	must(err)
	if at, ok := events[0]["created_at"].(time.Time); !ok || !at.Equal(t0) {
		t.Errorf("bash event created_at = %#v, want time %v", events[0]["created_at"], t0)
	}

	full, err := st.FullSession(ctx, "acc", "s1")
	must(err)
	for _, k := range []string{"transcript_lines", "subagent_lines", "blobs", "hook_events"} {
		list, ok := full[k].([]session.Doc)
		if !ok || len(list) != 1 {
			t.Fatalf("full.%s = %#v, want one session.Doc", k, full[k])
		}
		if _, isTime := list[0]["created_at"].(time.Time); !isTime {
			t.Errorf("full.%s[0].created_at = %#v, want a time", k, list[0]["created_at"])
		}
	}
	if seq, ok := full["subagent_lines"].([]session.Doc)[0]["seq"].(int); !ok || seq != 2 {
		t.Errorf("full.subagent_lines[0].seq = %#v, want int 2", full["subagent_lines"].([]session.Doc)[0]["seq"])
	}
}

func TestEmptyGitOriginSendsNothing(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	st := s.client(t, "acc")
	if err := st.SetGitInfoIfMissing(ctx, "s1", "", "main"); err != nil {
		t.Fatalf("SetGitInfoIfMissing with an empty origin: %v", err)
	}
	if got := s.count("POST", "/sessions/s1/git"); got != 0 {
		t.Errorf("POST /sessions/s1/git was sent %d times, want 0", got)
	}
}

func TestIDsAreEscapedInPaths(t *testing.T) {
	s := newServer(t, false, sessionapi.Options{})
	st := s.client(t, "acc")
	// A session id with a space is a single safe path element the server
	// accepts; it must reach it as one element.
	const id = "a b"
	if err := st.UpsertSession(ctx, session.Session{SessionID: id, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSession(ctx, "acc", id)
	if err != nil || got["session_id"] != id {
		t.Fatalf("GetSession(%q) = %v, %v", id, got, err)
	}
	if n, err := st.CountTranscriptLines(ctx, "acc", id); err != nil || n != 0 {
		t.Errorf("CountTranscriptLines(%q) = %d, %v", id, n, err)
	}
	if err := st.TouchSession(ctx, "acc", id, time.Now()); err != nil {
		t.Errorf("TouchSession(%q): %v", id, err)
	}
	if _, err := st.FullSession(ctx, "acc", id); err != nil {
		t.Errorf("FullSession(%q): %v", id, err)
	}
	if _, err := st.GetSession(ctx, "acc", fmt.Sprintf("%s/x", id)); err == nil {
		t.Errorf("an id with a slash was accepted")
	}
}
