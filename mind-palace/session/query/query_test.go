package query_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

const (
	account = "acc-1"
	foreign = "acc-2"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func seedSession(t *testing.T, st session.Store, acc, id, project, origin string, lastSeen time.Time) {
	t.Helper()
	must(t, st.UpsertSession(ctx, session.Session{
		SessionID: id, AccountID: acc, ProjectPath: project, GitOrigin: origin, Cwd: project, LastSeen: lastSeen,
	}))
}

func seedLines(t *testing.T, st session.Store, acc, id string, n int) {
	t.Helper()
	lines := make([]session.TranscriptLine, n)
	for i := range lines {
		lines[i] = session.TranscriptLine{SessionID: id, Seq: i, AccountID: acc, Line: map[string]any{"n": i}}
	}
	must(t, st.UpsertTranscriptLines(ctx, lines))
}

func seedBash(t *testing.T, st session.Store, acc, id, command string, at time.Time) {
	t.Helper()
	must(t, st.InsertHookEvent(ctx, session.Doc{
		"session_id": id, "account_id": acc, "tool_name": "Bash",
		"tool_input": map[string]any{"command": command}, "created_at": at,
	}))
}

// fixture mirrors the mcp.test.ts data: two own sessions in different repos,
// a third with a long transcript, and one session of another account.
func fixture(t *testing.T) (*query.Service, session.Store) {
	t.Helper()
	st := memsession.New()
	seedSession(t, st, account, "sess-1", "/home/user/myrepo", "https://github.com/user/myrepo.git", t0.Add(3*time.Hour))
	seedSession(t, st, account, "sess-2", "/home/user/other", "https://github.com/user/other.git", t0.Add(2*time.Hour))
	seedSession(t, st, account, "sess-3", "/home/user/third", "", t0.Add(time.Hour))
	seedSession(t, st, foreign, "sess-acct-2", "/home/user/foreign", "https://github.com/user/foreign.git", t0)
	seedLines(t, st, account, "sess-1", 25)
	seedLines(t, st, account, "sess-3", 120)
	seedLines(t, st, foreign, "sess-acct-2", 3)
	seedBash(t, st, account, "sess-1", "git status", t0.Add(1*time.Minute))
	seedBash(t, st, account, "sess-1", "npm install", t0.Add(2*time.Minute))
	seedBash(t, st, account, "sess-1", "git status", t0.Add(3*time.Minute))
	seedBash(t, st, account, "sess-2", "cargo build", t0.Add(4*time.Minute))
	seedBash(t, st, account, "sess-3", "git log", t0.Add(5*time.Minute))
	seedBash(t, st, foreign, "sess-acct-2", "echo foreign", t0.Add(6*time.Minute))
	return &query.Service{Store: st, AccountID: account}, st
}

func ids(docs []session.Doc) []string {
	out := []string{}
	for _, d := range docs {
		s, _ := d.String("session_id")
		out = append(out, s)
	}
	return out
}

func TestFindSessionsByGitOrigin(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.FindSessions(ctx, query.FindSessionsArgs{GitOrigin: "user/myrepo"})
	must(t, err)
	if want := []string{"sess-1"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
	got, err = svc.FindSessions(ctx, query.FindSessionsArgs{GitOrigin: "github.com/user"})
	must(t, err)
	if want := []string{"sess-1", "sess-2"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("ordered by last_seen desc: got %v, want %v", ids(got), want)
	}
}

func TestFindSessionsByProjectPathAndQuery(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.FindSessions(ctx, query.FindSessionsArgs{ProjectPath: "/home/user/other"})
	must(t, err)
	if want := []string{"sess-2"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
	got, err = svc.FindSessions(ctx, query.FindSessionsArgs{Query: "THIRD"})
	must(t, err)
	if want := []string{"sess-3"}; !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
}

func TestFindSessionsEmptyResultIsEmptyList(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.FindSessions(ctx, query.FindSessionsArgs{GitOrigin: "nonexistent/repo"})
	must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil list, got %#v", got)
	}
	b, _ := json.Marshal(got)
	if string(b) != "[]" {
		t.Fatalf("json = %s", b)
	}
}

func TestFindSessionsIncludesEventCount(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.FindSessions(ctx, query.FindSessionsArgs{ProjectPath: "/home/user/myrepo", Limit: 5})
	must(t, err)
	if len(got) != 1 {
		t.Fatalf("got %v", ids(got))
	}
	if n, ok := got[0]["event_count"].(int); !ok || n != 25 {
		t.Fatalf("event_count = %#v, want int 25", got[0]["event_count"])
	}
	if got[0]["project_path"] != "/home/user/myrepo" {
		t.Fatalf("store fields lost: %v", got[0])
	}
}

func TestFindSessionsDefaultAndCappedLimit(t *testing.T) {
	st := memsession.New()
	for i := 0; i < 600; i++ {
		seedSession(t, st, account, fmt.Sprintf("s-%03d", i), "/p", "", t0.Add(time.Duration(i)*time.Second))
	}
	svc := &query.Service{Store: st, AccountID: account}
	got, err := svc.FindSessions(ctx, query.FindSessionsArgs{})
	must(t, err)
	if len(got) != 10 {
		t.Fatalf("default limit: got %d, want 10", len(got))
	}
	got, err = svc.FindSessions(ctx, query.FindSessionsArgs{Limit: 10000})
	must(t, err)
	if len(got) != 500 {
		t.Fatalf("capped limit: got %d, want 500", len(got))
	}
}

func TestFindSessionsExcludesForeignAccount(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.FindSessions(ctx, query.FindSessionsArgs{Limit: 500})
	must(t, err)
	for _, id := range ids(got) {
		if id == "sess-acct-2" {
			t.Fatal("foreign session returned")
		}
	}
	if len(got) != 3 {
		t.Fatalf("got %v", ids(got))
	}
}

func TestFindSessionsInvalidRegexIsAnError(t *testing.T) {
	svc, _ := fixture(t)
	if _, err := svc.FindSessions(ctx, query.FindSessionsArgs{Query: "("}); err == nil {
		t.Fatal("want error")
	}
}

func TestSearchCommandsAcrossSessions(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "git"})
	must(t, err)
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	// newest first
	if got[0].Command != "git log" || got[0].SessionID != "sess-3" {
		t.Fatalf("first hit = %+v", got[0])
	}
	for _, h := range got {
		if h.ProjectPath == nil {
			t.Fatalf("project_path not joined: %+v", h)
		}
	}
	if !got[0].CreatedAt.Equal(t0.Add(5 * time.Minute)) {
		t.Fatalf("created_at = %v", got[0].CreatedAt)
	}
}

func TestSearchCommandsJoinsNullForMissingGitOrigin(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "git log"})
	must(t, err)
	if len(got) != 1 || got[0].GitOrigin != nil || got[0].ProjectPath == nil || *got[0].ProjectPath != "/home/user/third" {
		t.Fatalf("got %+v", got)
	}
	b, _ := json.Marshal(got[0])
	var m map[string]any
	must(t, json.Unmarshal(b, &m))
	if v, present := m["git_origin"]; !present || v != nil {
		t.Fatalf("git_origin must be JSON null, got %s", b)
	}
	for _, k := range []string{"session_id", "project_path", "command", "created_at"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing json key %s in %s", k, b)
		}
	}
}

func TestSearchCommandsScopedByGitOrigin(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "build", GitOrigin: "user/other"})
	must(t, err)
	if len(got) != 1 || got[0].Command != "cargo build" || got[0].SessionID != "sess-2" {
		t.Fatalf("got %+v", got)
	}
	if got[0].GitOrigin == nil || *got[0].GitOrigin != "https://github.com/user/other.git" {
		t.Fatalf("git_origin = %v", got[0].GitOrigin)
	}
	got, err = svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "git", GitOrigin: "user/other"})
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("scope leaked: %+v", got)
	}
}

func TestSearchCommandsScopedBySessionID(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "git", SessionID: "sess-1"})
	must(t, err)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
}

func TestSearchCommandsNoMatchIsEmptyList(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "xyzzy_no_match_ever"})
	must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil, got %#v", got)
	}
	got, err = svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "git", GitOrigin: "no/such"})
	must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("want empty non-nil, got %#v", got)
	}
}

func TestSearchCommandsAccountIsolation(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "echo"})
	must(t, err)
	if len(got) != 0 {
		t.Fatalf("foreign command returned: %+v", got)
	}
	got, err = svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "echo", SessionID: "sess-acct-2"})
	must(t, err)
	if got == nil || len(got) != 0 {
		t.Fatalf("foreign session_id must give empty list, got %#v", got)
	}
}

func TestSearchCommandsDefaultAndCappedLimit(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, account, "s", "/p", "", t0)
	for i := 0; i < 600; i++ {
		seedBash(t, st, account, "s", fmt.Sprintf("echo %d", i), t0.Add(time.Duration(i)*time.Second))
	}
	svc := &query.Service{Store: st, AccountID: account}
	got, err := svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "echo"})
	must(t, err)
	if len(got) != 20 {
		t.Fatalf("default: got %d", len(got))
	}
	got, err = svc.SearchCommands(ctx, query.SearchCommandsArgs{Pattern: "echo", Limit: 9999})
	must(t, err)
	if len(got) != 500 {
		t.Fatalf("cap: got %d", len(got))
	}
}

func TestSessionContext(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.SessionContext(ctx, "sess-1")
	must(t, err)
	if got.Session["session_id"] != "sess-1" || got.Session["git_origin"] != "https://github.com/user/myrepo.git" {
		t.Fatalf("session = %v", got.Session)
	}
	if len(got.Session) != 6 {
		t.Fatalf("session must hold exactly six keys, got %v", got.Session)
	}
	if want := []string{"git status", "npm install"}; !reflect.DeepEqual(got.TopCommands, want) {
		t.Fatalf("top_commands = %v, want %v", got.TopCommands, want)
	}
	if len(got.FirstLines) != 20 || got.FirstLines[0]["seq"] != 0 {
		t.Fatalf("first_lines = %d, first %v", len(got.FirstLines), got.FirstLines[0])
	}
	if len(got.LastLines) != 20 || got.LastLines[19]["seq"] != 24 || got.LastLines[0]["seq"] != 5 {
		t.Fatalf("last_lines = %d, ends %v..%v", len(got.LastLines), got.LastLines[0], got.LastLines[19])
	}
}

func TestSessionContextNullGitOriginAndShortTranscript(t *testing.T) {
	svc, st := fixture(t)
	seedLines(t, st, account, "sess-2", 20)
	got, err := svc.SessionContext(ctx, "sess-2")
	must(t, err)
	if len(got.FirstLines) != 20 || got.LastLines == nil || len(got.LastLines) != 0 {
		t.Fatalf("exactly 20 lines gives no last_lines: first=%d last=%#v", len(got.FirstLines), got.LastLines)
	}
	got, err = svc.SessionContext(ctx, "sess-3")
	must(t, err)
	if len(got.LastLines) != 20 || got.LastLines[19]["seq"] != 119 {
		t.Fatalf("last_lines = %v", got.LastLines)
	}
	seedSession(t, st, account, "bare", "/bare", "", t0)
	got, err = svc.SessionContext(ctx, "bare")
	must(t, err)
	v, present := got.Session["git_origin"]
	if !present || v != nil {
		t.Fatalf("git_origin must be present and nil, got %v", got.Session)
	}
	if got.TopCommands == nil || got.FirstLines == nil || got.LastLines == nil {
		t.Fatalf("empty slices must be non-nil: %+v", got)
	}
	b, _ := json.Marshal(got)
	var m map[string]any
	must(t, json.Unmarshal(b, &m))
	for _, k := range []string{"session", "top_commands", "first_lines", "last_lines"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing json key %s in %s", k, b)
		}
	}
}

func TestSessionContextTopCommandsAreTenMostRecentDistinct(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, account, "s", "/p", "", t0)
	for i := 0; i < 15; i++ {
		seedBash(t, st, account, "s", fmt.Sprintf("cmd %d", i), t0.Add(time.Duration(i)*time.Minute))
	}
	svc := &query.Service{Store: st, AccountID: account}
	got, err := svc.SessionContext(ctx, "s")
	must(t, err)
	if len(got.TopCommands) != 10 || got.TopCommands[0] != "cmd 14" || got.TopCommands[9] != "cmd 5" {
		t.Fatalf("top_commands = %v", got.TopCommands)
	}
}

func TestSessionContextNotFound(t *testing.T) {
	svc, _ := fixture(t)
	for _, id := range []string{"does-not-exist", "sess-acct-2"} {
		if _, err := svc.SessionContext(ctx, id); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}

func TestReadTranscriptPaginates(t *testing.T) {
	svc, _ := fixture(t)
	got, err := svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: "sess-1", Offset: 0, Limit: 10}, nil)
	must(t, err)
	if len(got) != 10 || got[0]["seq"] != 0 || got[9]["seq"] != 9 {
		t.Fatalf("got %d lines", len(got))
	}
	got, err = svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: "sess-1", Offset: 20, Limit: 10}, nil)
	must(t, err)
	if len(got) != 5 || got[0]["seq"] != 20 {
		t.Fatalf("short page: got %d lines", len(got))
	}
}

func TestReadTranscriptDefaultAndCappedLimit(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, account, "s", "/p", "", t0)
	seedLines(t, st, account, "s", 700)
	svc := &query.Service{Store: st, AccountID: account}
	got, err := svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: "s"}, nil)
	must(t, err)
	if len(got) != 200 {
		t.Fatalf("default: %d", len(got))
	}
	got, err = svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: "s", Limit: 5000}, nil)
	must(t, err)
	if len(got) != 500 {
		t.Fatalf("cap: %d", len(got))
	}
}

type progressCall struct{ done, total int }

func TestReadTranscriptReportsProgressPerBatch(t *testing.T) {
	svc, _ := fixture(t)
	var calls []progressCall
	got, err := svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: "sess-3"}, func(done, total int) {
		calls = append(calls, progressCall{done, total})
	})
	must(t, err)
	if len(got) != 120 {
		t.Fatalf("lines = %d", len(got))
	}
	want := []progressCall{{50, 120}, {100, 120}, {120, 120}}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("progress = %v, want %v", calls, want)
	}
}

func TestReadTranscriptStopsOnShortBatchWithoutEmptyProgress(t *testing.T) {
	svc, _ := fixture(t)
	var calls []progressCall
	got, err := svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: "sess-1", Offset: 25, Limit: 200}, func(done, total int) {
		calls = append(calls, progressCall{done, total})
	})
	must(t, err)
	if len(got) != 0 || len(calls) != 0 {
		t.Fatalf("got %d lines, progress %v", len(got), calls)
	}
}

func TestReadTranscriptNotFound(t *testing.T) {
	svc, _ := fixture(t)
	for _, id := range []string{"no-such-session", "sess-acct-2"} {
		if _, err := svc.ReadTranscript(ctx, query.ReadTranscriptArgs{SessionID: id}, nil); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}

func TestFullSession(t *testing.T) {
	svc, st := fixture(t)
	must(t, st.UpsertSubagentLine(ctx, session.SubagentLine{SessionID: "sess-1", SubagentID: "agent-1", Seq: 0, AccountID: account, Line: map[string]any{"x": 1}}))
	got, err := svc.FullSession(ctx, "sess-1")
	must(t, err)
	if got["session_id"] != "sess-1" {
		t.Fatalf("got %v", got["session_id"])
	}
	if _, has := got["_id"]; has {
		t.Fatal("_id must be stripped")
	}
	for _, k := range []string{"transcript_lines", "subagent_lines", "blobs", "hook_events"} {
		if _, ok := got[k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
	if lines, _ := got["transcript_lines"].([]session.Doc); len(lines) != 25 {
		t.Fatalf("transcript_lines = %v", got["transcript_lines"])
	}
}

func TestFullSessionNotFound(t *testing.T) {
	svc, _ := fixture(t)
	for _, id := range []string{"does-not-exist", "sess-acct-2"} {
		if _, err := svc.FullSession(ctx, id); !errors.Is(err, session.ErrNotFound) {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
}
