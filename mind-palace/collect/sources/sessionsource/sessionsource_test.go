package sessionsource

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const testAccount = "acct-1"

var t0 = time.Date(2026, 9, 9, 14, 0, 0, 0, time.UTC)

// seedSessions stores, for acct-1: two alpha sessions (a-new an hour newer
// than a-old), one beta session, a session whose cwd holds regexp
// metacharacters, two look-alikes an unquoted pattern would match, and, for
// acct-2, a delta session. a-new has three Bash events, one a duplicate.
func seedSessions(t *testing.T) session.Store {
	t.Helper()
	ctx := context.Background()
	st := memsession.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []session.Session{
		{SessionID: "a-old", AccountID: testAccount, Cwd: "/work/alpha", LastSeen: t0},
		{SessionID: "a-new", AccountID: testAccount, ProjectPath: "/work/alpha", Cwd: "/work/alpha", LastSeen: t0.Add(time.Hour)},
		{SessionID: "b", AccountID: testAccount, ProjectPath: "/work/beta", Cwd: "/work/beta", LastSeen: t0},
		{SessionID: "meta", AccountID: testAccount, Cwd: "/work/a+b (x)", LastSeen: t0},
		{SessionID: "aab", AccountID: testAccount, Cwd: "/work/aab", LastSeen: t0.Add(2 * time.Hour)},
		{SessionID: "trap", AccountID: testAccount, Cwd: "/work/aab x", LastSeen: t0.Add(3 * time.Hour)},
		{SessionID: "other", AccountID: "acct-2", Cwd: "/work/delta", LastSeen: t0.Add(4 * time.Hour)},
	} {
		must(st.UpsertSession(ctx, s))
	}
	for i, c := range []string{"go test ./...", "ls -la", "go test ./..."} {
		must(st.InsertHookEvent(ctx, session.Doc{
			"session_id": "a-new", "account_id": testAccount, "tool_name": "Bash",
			"tool_input": map[string]any{"command": c}, "created_at": t0.Add(time.Duration(i) * time.Minute),
		}))
	}
	// Memory records: two for alpha (the handoff newer), one for beta and
	// one of another account's session.
	for i, m := range []struct{ sid, acct, kind, text, hash string }{
		{"a-new", testAccount, "entry", "## 09:15 | main\nFixed the restart", "h1"},
		{"a-new", testAccount, "handoff", "Next: wire the listener", "h2"},
		{"b", testAccount, "entry", "beta note", "h3"},
		{"other", "acct-2", "entry", "delta note", "h4"},
	} {
		must(st.InsertHookEvent(ctx, session.Doc{
			"session_id": m.sid, "account_id": m.acct, "hook_event_name": session.MemoryEventName, "cwd": "/work/x",
			"memory":     map[string]any{"kind": m.kind, "file": "now.md", "text": m.text, "hash": m.hash, "branch": "main", "day": "2026-09-09"},
			"created_at": t0.Add(time.Duration(i) * time.Minute),
		}))
	}
	return st
}

func TestRecentReturnsTheProjectsMemoryNewestFirst(t *testing.T) {
	ctx := context.Background()
	src := New(connectTo(seedSessions(t)), testAccount)
	defer src.Close(ctx)
	notes, err := src.Recent(ctx, "/work/alpha", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(notes) != 2 || notes[0].Kind != "handoff" || notes[0].Text != "Next: wire the listener" || notes[1].Text != "## 09:15 | main\nFixed the restart" {
		t.Fatalf("notes = %+v", notes)
	}
	if notes[0].Branch != "main" || notes[0].Day != "2026-09-09" || !notes[0].CreatedAt.Equal(t0.Add(time.Minute)) {
		t.Errorf("note fields = %+v", notes[0])
	}
	if notes, _ := src.Recent(ctx, "/work/alpha", 1); len(notes) != 1 || notes[0].Kind != "handoff" {
		t.Errorf("limit 1 = %+v", notes)
	}
	if notes, _ := src.Recent(ctx, "/work/nowhere", 5); len(notes) != 0 {
		t.Errorf("unknown project = %+v", notes)
	}
	if notes, err := src.Recent(ctx, "", 5); err != nil || len(notes) != 0 {
		t.Errorf("empty cwd = %+v, %v", notes, err)
	}
	other := New(connectTo(seedSessions(t)), "acct-2")
	defer other.Close(ctx)
	if notes, _ := other.Recent(ctx, "/work/alpha", 5); len(notes) != 0 {
		t.Errorf("another account sees alpha's memory: %+v", notes)
	}
}

func connectTo(st session.Store) func(context.Context) (session.Store, error) {
	return func(context.Context) (session.Store, error) { return st, nil }
}

func TestCurrentReturnsNewestSessionOfCwd(t *testing.T) {
	ctx := context.Background()
	src := New(connectTo(seedSessions(t)), testAccount)
	defer src.Close(ctx)

	snap, err := src.Current(ctx, "/work/alpha")
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if snap == nil || snap.SessionID != "a-new" {
		t.Fatalf("snapshot = %+v, want session a-new", snap)
	}
	if want := []string{"go test ./...", "ls -la"}; !reflect.DeepEqual(snap.TopCommands, want) {
		t.Errorf("TopCommands = %v, want %v", snap.TopCommands, want)
	}
	if !snap.LastSeen.Equal(t0.Add(time.Hour)) {
		t.Errorf("LastSeen = %v, want %v", snap.LastSeen, t0.Add(time.Hour))
	}
	if snap.StartedAt.IsZero() {
		t.Errorf("StartedAt is zero, want the session's started_at")
	}

	beta, err := src.Current(ctx, "/work/beta")
	if err != nil || beta == nil || beta.SessionID != "b" {
		t.Fatalf("beta = %+v, %v", beta, err)
	}
	if beta.TopCommands == nil || len(beta.TopCommands) != 0 {
		t.Errorf("TopCommands of a session without commands = %#v, want empty non-nil", beta.TopCommands)
	}
}

func TestCurrentWithoutSessionIsNil(t *testing.T) {
	ctx := context.Background()
	src := New(connectTo(seedSessions(t)), testAccount)
	defer src.Close(ctx)

	for _, cwd := range []string{"/work/gamma", "/work/delta", "/work/alph", "work/alpha"} {
		snap, err := src.Current(ctx, cwd)
		if err != nil || snap != nil {
			t.Errorf("Current(%q) = %+v, %v; want nil, nil", cwd, snap, err)
		}
	}
}

func TestCurrentEmptyCwdDoesNotConnect(t *testing.T) {
	ctx := context.Background()
	connects := 0
	src := New(func(context.Context) (session.Store, error) {
		connects++
		return memsession.New(), nil
	}, testAccount)

	snap, err := src.Current(ctx, "")
	if err != nil || snap != nil {
		t.Fatalf("Current(\"\") = %+v, %v; want nil, nil", snap, err)
	}
	if connects != 0 {
		t.Errorf("connect called %d times, want 0", connects)
	}
	if err := src.Close(ctx); err != nil {
		t.Errorf("Close without a store: %v", err)
	}
}

func TestConnectFailureIsRetried(t *testing.T) {
	ctx := context.Background()
	st := seedSessions(t)
	boom := errors.New("mongo down")
	connects := 0
	src := New(func(context.Context) (session.Store, error) {
		connects++
		if connects == 1 {
			return nil, boom
		}
		return st, nil
	}, testAccount)
	defer src.Close(ctx)

	if _, err := src.Current(ctx, "/work/alpha"); !errors.Is(err, boom) {
		t.Fatalf("first Current error = %v, want %v", err, boom)
	}
	snap, err := src.Current(ctx, "/work/alpha")
	if err != nil || snap == nil || snap.SessionID != "a-new" {
		t.Fatalf("second Current = %+v, %v", snap, err)
	}
	if _, err := src.Current(ctx, "/work/beta"); err != nil {
		t.Fatal(err)
	}
	if connects != 2 {
		t.Errorf("connect called %d times, want 2 (one failure, then once)", connects)
	}
}

func TestCurrentQuotesRegexpMetacharacters(t *testing.T) {
	ctx := context.Background()
	src := New(connectTo(seedSessions(t)), testAccount)
	defer src.Close(ctx)

	for cwd, want := range map[string]string{
		"/work/a+b (x)": "meta",
		"/work/aab":     "aab",
		"/work/aab x":   "trap",
	} {
		snap, err := src.Current(ctx, cwd)
		if err != nil {
			t.Fatalf("Current(%q): %v", cwd, err)
		}
		if snap == nil || snap.SessionID != want {
			t.Errorf("Current(%q) = %+v, want session %s", cwd, snap, want)
		}
	}
}

func TestCurrentIsScopedToAccount(t *testing.T) {
	ctx := context.Background()
	st := seedSessions(t)

	mine := New(connectTo(st), testAccount)
	if snap, err := mine.Current(ctx, "/work/delta"); err != nil || snap != nil {
		t.Errorf("acct-1 sees acct-2's session: %+v, %v", snap, err)
	}
	theirs := New(connectTo(st), "acct-2")
	snap, err := theirs.Current(ctx, "/work/delta")
	if err != nil || snap == nil || snap.SessionID != "other" {
		t.Errorf("acct-2 Current(/work/delta) = %+v, %v; want session other", snap, err)
	}
	if snap, err := theirs.Current(ctx, "/work/alpha"); err != nil || snap != nil {
		t.Errorf("acct-2 sees acct-1's session: %+v, %v", snap, err)
	}
}

func TestTimeOfHandlesEveryRepresentation(t *testing.T) {
	at := t0.Add(5 * time.Minute)
	for name, v := range map[string]any{
		"time":    at,
		"pointer": &at,
		"string":  at.Format(time.RFC3339Nano),
	} {
		if got := timeOf(session.Doc{"k": v}, "k"); !got.Equal(at) {
			t.Errorf("%s: timeOf = %v, want %v", name, got, at)
		}
	}
	var nilTime *time.Time
	for name, v := range map[string]any{"missing": nil, "nil pointer": nilTime, "number": 42, "junk": "yesterday"} {
		if got := timeOf(session.Doc{"k": v}, "k"); !got.IsZero() {
			t.Errorf("%s: timeOf = %v, want zero", name, got)
		}
	}
}
