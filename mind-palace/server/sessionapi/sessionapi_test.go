package sessionapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const (
	accountA = "acc-a"
	accountB = "acc-b"
	// accountHeader selects the principal in tests; the real server resolves
	// it from the bearer token.
	accountHeader = "X-Test-Account"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

func headerAccount(r *http.Request) (string, bool) {
	a := r.Header.Get(accountHeader)
	return a, a != ""
}

func newHandler(t *testing.T, opts Options) (http.Handler, session.Store) {
	t.Helper()
	if opts.Account == nil {
		opts.Account = headerAccount
	}
	st := memsession.New()
	return Handler(st, opts), st
}

// call sends one request. A string or []byte body is sent as is; anything
// else is JSON-encoded. An empty account sends no account header.
func call(t *testing.T, h http.Handler, account, method, path string, body any) (int, envelope) {
	t.Helper()
	var raw []byte
	switch b := body.(type) {
	case nil:
	case string:
		raw = []byte(b)
	case []byte:
		raw = b
	default:
		var err error
		if raw, err = json.Marshal(b); err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(method, Prefix+path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	if account != "" {
		req.Header.Set(accountHeader, account)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: response is not an envelope: %v\n%s", method, path, err, rec.Body.String())
	}
	if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Errorf("%s %s: Content-Type = %q", method, path, rec.Header().Get("Content-Type"))
	}
	return rec.Code, env
}

// mustOK sends a request that must succeed and decodes its result into out.
func mustOK(t *testing.T, h http.Handler, account, method, path string, body, out any) {
	t.Helper()
	status, env := call(t, h, account, method, path, body)
	if status != http.StatusOK || !env.OK {
		t.Fatalf("%s %s: status %d, envelope %+v", method, path, status, env.Error)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			t.Fatalf("%s %s: decode result: %v\n%s", method, path, err, env.Result)
		}
	}
}

// mustFail sends a request that must fail with the given status and kind.
func mustFail(t *testing.T, h http.Handler, account, method, path string, body any, status int, kind string) {
	t.Helper()
	got, env := call(t, h, account, method, path, body)
	if got != status || env.OK || env.Error == nil || env.Error.Kind != kind {
		t.Fatalf("%s %s: status %d envelope ok=%v error=%+v, want %d %s", method, path, got, env.OK, env.Error, status, kind)
	}
}

func putSession(t *testing.T, h http.Handler, account string, s session.Session) {
	t.Helper()
	mustOK(t, h, account, "PUT", "/sessions", s, nil)
}

func docsOf(t *testing.T, h http.Handler, account, method, path string, body any) []session.Doc {
	t.Helper()
	var out []session.Doc
	mustOK(t, h, account, method, path, body, &out)
	return out
}

func TestUpsertAndGetSessionStampsAndScopesAccount(t *testing.T) {
	h, _ := newHandler(t, Options{})
	putSession(t, h, accountA, session.Session{SessionID: "s1", AccountID: "foreign", Cwd: "/w", LastSeen: t0})

	var got session.Doc
	mustOK(t, h, accountA, "GET", "/sessions/s1", nil, &got)
	if got["account_id"] != accountA {
		t.Errorf("account_id = %v, want the principal's %q", got["account_id"], accountA)
	}
	if got["cwd"] != "/w" || got["session_id"] != "s1" {
		t.Errorf("session = %v", got)
	}
	if got["started_at"] != t0.Format(time.RFC3339Nano) || got["last_seen"] != t0.Format(time.RFC3339Nano) {
		t.Errorf("times = %v / %v, want %s", got["started_at"], got["last_seen"], t0.Format(time.RFC3339Nano))
	}
	if _, has := got["_id"]; has {
		t.Errorf("_id leaked: %v", got)
	}

	mustFail(t, h, accountB, "GET", "/sessions/s1", nil, http.StatusNotFound, KindNotFound)
	mustFail(t, h, accountA, "GET", "/sessions/missing", nil, http.StatusNotFound, KindNotFound)
}

func TestTouchSession(t *testing.T) {
	h, _ := newHandler(t, Options{})
	putSession(t, h, accountA, session.Session{SessionID: "s1", LastSeen: t0})
	later := t0.Add(time.Hour)

	mustOK(t, h, accountB, "POST", "/sessions/s1/touch", map[string]any{"at": later}, nil)
	var got session.Doc
	mustOK(t, h, accountA, "GET", "/sessions/s1", nil, &got)
	if got["last_seen"] != t0.Format(time.RFC3339Nano) {
		t.Errorf("another account touched the session: last_seen = %v", got["last_seen"])
	}

	mustOK(t, h, accountA, "POST", "/sessions/s1/touch", map[string]any{"at": later}, nil)
	mustOK(t, h, accountA, "GET", "/sessions/s1", nil, &got)
	if got["last_seen"] != later.Format(time.RFC3339Nano) {
		t.Errorf("last_seen = %v, want %s", got["last_seen"], later.Format(time.RFC3339Nano))
	}

	mustFail(t, h, accountA, "POST", "/sessions/s1/touch", map[string]any{}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "POST", "/sessions/s1/touch", `{"at": "yesterday"}`, http.StatusBadRequest, KindInvalid)
}

func TestSetGitInfoIfMissing(t *testing.T) {
	h, _ := newHandler(t, Options{})
	putSession(t, h, accountA, session.Session{SessionID: "s1", LastSeen: t0})

	// Another account cannot fill it in, and a missing session is a no-op.
	mustOK(t, h, accountB, "POST", "/sessions/s1/git", map[string]any{"origin": "o-b", "branch": "b"}, nil)
	mustOK(t, h, accountA, "POST", "/sessions/absent/git", map[string]any{"origin": "o", "branch": "b"}, nil)
	var got session.Doc
	mustOK(t, h, accountA, "GET", "/sessions/s1", nil, &got)
	if _, has := got["git_origin"]; has {
		t.Errorf("another account wrote git info: %v", got)
	}
	mustFail(t, h, accountA, "GET", "/sessions/absent", nil, http.StatusNotFound, KindNotFound)

	mustOK(t, h, accountA, "POST", "/sessions/s1/git", map[string]any{"origin": "o-1", "branch": "main"}, nil)
	mustOK(t, h, accountA, "POST", "/sessions/s1/git", map[string]any{"origin": "o-2", "branch": "other"}, nil)
	mustOK(t, h, accountA, "GET", "/sessions/s1", nil, &got)
	if got["git_origin"] != "o-1" || got["git_branch"] != "main" {
		t.Errorf("git info = %v / %v, want o-1 / main", got["git_origin"], got["git_branch"])
	}

	mustFail(t, h, accountA, "POST", "/sessions/s1/git", map[string]any{"branch": "x"}, http.StatusBadRequest, KindInvalid)
}

func TestFindSessions(t *testing.T) {
	h, _ := newHandler(t, Options{})
	putSession(t, h, accountA, session.Session{SessionID: "s1", ProjectPath: "/p/one", LastSeen: t0})
	putSession(t, h, accountA, session.Session{SessionID: "s2", ProjectPath: "/p/two", LastSeen: t0.Add(time.Minute)})
	putSession(t, h, accountB, session.Session{SessionID: "s3", ProjectPath: "/p/one", LastSeen: t0})

	// AccountID in the body is ignored in favour of the principal's.
	found := docsOf(t, h, accountA, "POST", "/sessions/find", session.SessionQuery{AccountID: accountB})
	if len(found) != 2 || found[0]["session_id"] != "s2" || found[1]["session_id"] != "s1" {
		t.Fatalf("find = %v, want s2 then s1", found)
	}
	for _, d := range found {
		if _, has := d["account_id"]; has {
			t.Errorf("projection leaked account_id: %v", d)
		}
	}
	found = docsOf(t, h, accountA, "POST", "/sessions/find", session.SessionQuery{ProjectPathRe: "ONE$"})
	if len(found) != 1 || found[0]["session_id"] != "s1" {
		t.Errorf("find by regex = %v, want s1", found)
	}
	if found := docsOf(t, h, "nobody-else", "POST", "/sessions/find", session.SessionQuery{}); len(found) != 0 {
		t.Errorf("foreign account sees %v", found)
	}
	mustFail(t, h, accountA, "POST", "/sessions/find", session.SessionQuery{QueryRe: "("}, http.StatusBadRequest, KindInvalid)
}

func bashEvent(sid, cmd string, at time.Time) session.Doc {
	return session.Doc{"session_id": sid, "account_id": "foreign", "tool_name": "Bash", "tool_input": map[string]any{"command": cmd}, "created_at": at.Format(time.RFC3339Nano)}
}

func TestHookEventsAndBashEvents(t *testing.T) {
	h, st := newHandler(t, Options{})
	var ack map[string]int
	mustOK(t, h, accountA, "POST", "/hook-events", []session.Doc{
		bashEvent("s1", "git status", t0),
		bashEvent("s1", "go test", t0.Add(time.Minute)),
		{"session_id": "s1", "tool_name": "Read", "tool_use_id": "tu-1"},
	}, &ack)
	if ack["inserted"] != 3 {
		t.Errorf("inserted = %d, want 3", ack["inserted"])
	}

	stored, err := st.Unenriched(context.Background(), "hook_events", "probe", 10)
	if err != nil || len(stored) != 3 {
		t.Fatalf("stored = %v, %v", stored, err)
	}
	for _, d := range stored {
		if d["account_id"] != accountA {
			t.Errorf("account_id = %v, want %q", d["account_id"], accountA)
		}
	}
	if at, isTime := stored[1]["created_at"].(time.Time); !isTime || !at.Equal(t0.Add(time.Minute)) {
		t.Errorf("created_at = %#v, want the parsed time", stored[1]["created_at"])
	}

	// BashEvents: newest first, projected, scoped.
	found := docsOf(t, h, accountA, "POST", "/bash-events", session.CommandQuery{AccountID: accountB, PatternRe: "^g"})
	if len(found) != 2 || found[0].ToolInput()["command"] != "go test" || found[1].ToolInput()["command"] != "git status" {
		t.Fatalf("bash events = %v", found)
	}
	if found[0]["created_at"] != t0.Add(time.Minute).Format(time.RFC3339Nano) {
		t.Errorf("created_at = %v", found[0]["created_at"])
	}
	if found := docsOf(t, h, accountB, "POST", "/bash-events", session.CommandQuery{}); len(found) != 0 {
		t.Errorf("account B sees %v", found)
	}
	found = docsOf(t, h, accountA, "POST", "/bash-events", session.CommandQuery{SessionIDs: []string{"other"}})
	if len(found) != 0 {
		t.Errorf("session filter ignored: %v", found)
	}
	mustFail(t, h, accountA, "POST", "/bash-events", session.CommandQuery{PatternRe: "("}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "POST", "/bash-events", session.CommandQuery{SessionIDs: []string{"../x"}}, http.StatusBadRequest, KindInvalid)

	// Bad batches: not an array, an element without a session id, an unsafe id.
	mustFail(t, h, accountA, "POST", "/hook-events", session.Doc{"session_id": "s1"}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "POST", "/hook-events", []session.Doc{{"tool_name": "Bash"}}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "POST", "/hook-events", []session.Doc{{"session_id": "a/b"}}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "POST", "/hook-events", `[null]`, http.StatusBadRequest, KindInvalid)
	if stored, _ := st.Unenriched(context.Background(), "hook_events", "probe", 10); len(stored) != 3 {
		t.Errorf("a rejected batch was stored: %d events", len(stored))
	}
}

func TestHookEventIDsByToolUse(t *testing.T) {
	h, _ := newHandler(t, Options{})
	mustOK(t, h, accountA, "POST", "/hook-events", []session.Doc{
		{"session_id": "s1", "tool_use_id": "tu-1"},
		{"session_id": "s1", "tool_use_id": "tu-2"},
		{"session_id": "s2", "tool_use_id": "tu-1"},
	}, nil)
	var ids []string
	mustOK(t, h, accountA, "POST", "/hook-events/by-tool-use", map[string]any{"session_id": "s1", "tool_use_ids": []string{"tu-1", "tu-3"}}, &ids)
	if len(ids) != 1 {
		t.Errorf("ids = %v, want one", ids)
	}
	mustOK(t, h, accountA, "POST", "/hook-events/by-tool-use", map[string]any{"tool_use_ids": []string{"tu-1"}}, &ids)
	if len(ids) != 2 {
		t.Errorf("ids across sessions = %v, want two", ids)
	}
	mustOK(t, h, accountA, "POST", "/hook-events/by-tool-use", map[string]any{"session_id": "s1", "tool_use_ids": []string{"none"}}, &ids)
	if ids == nil || len(ids) != 0 {
		t.Errorf("no match = %#v, want an empty list", ids)
	}
	mustFail(t, h, accountA, "POST", "/hook-events/by-tool-use", map[string]any{"session_id": "a/b"}, http.StatusBadRequest, KindInvalid)
}

func line(sid string, seq int) session.TranscriptLine {
	return session.TranscriptLine{SessionID: sid, Seq: seq, Line: map[string]any{"type": "human", "seq": seq}, AccountID: "foreign"}
}

func TestTranscriptLines(t *testing.T) {
	h, _ := newHandler(t, Options{})
	var ack map[string]int
	mustOK(t, h, accountA, "PUT", "/transcript-lines", []session.TranscriptLine{line("s1", 1), line("s1", 0)}, &ack)
	if ack["upserted"] != 2 {
		t.Errorf("upserted = %d, want 2", ack["upserted"])
	}

	var n int
	mustOK(t, h, accountA, "GET", "/transcript-lines/count?session=s1", nil, &n)
	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	mustOK(t, h, accountB, "GET", "/transcript-lines/count?session=s1", nil, &n)
	if n != 0 {
		t.Errorf("count for another account = %d, want 0", n)
	}
	mustFail(t, h, accountA, "GET", "/transcript-lines/count", nil, http.StatusBadRequest, KindInvalid)

	got := docsOf(t, h, accountA, "POST", "/transcript-lines/query", session.LineQuery{AccountID: accountB, SessionID: "s1"})
	if len(got) != 2 || got[0]["seq"] != float64(0) || got[1]["seq"] != float64(1) {
		t.Fatalf("lines = %v, want seq 0 then 1", got)
	}
	if got[0]["account_id"] != accountA || got[0].Line()["type"] != "human" {
		t.Errorf("line = %v", got[0])
	}
	got = docsOf(t, h, accountA, "POST", "/transcript-lines/query", session.LineQuery{SessionID: "s1", Descending: true, Limit: 1})
	if len(got) != 1 || got[0]["seq"] != float64(1) {
		t.Errorf("descending limit 1 = %v", got)
	}
	if got := docsOf(t, h, accountB, "POST", "/transcript-lines/query", session.LineQuery{SessionID: "s1"}); len(got) != 0 {
		t.Errorf("account B sees %v", got)
	}
	mustFail(t, h, accountA, "POST", "/transcript-lines/query", session.LineQuery{}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "PUT", "/transcript-lines", []session.TranscriptLine{{Seq: 1}}, http.StatusBadRequest, KindInvalid)
}

func TestTranscriptBatchCap(t *testing.T) {
	h, _ := newHandler(t, Options{MaxLines: 3})
	batch := func(n int) []session.TranscriptLine {
		out := make([]session.TranscriptLine, n)
		for i := range out {
			out[i] = line("s1", i)
		}
		return out
	}
	mustOK(t, h, accountA, "PUT", "/transcript-lines", batch(3), nil)
	mustFail(t, h, accountA, "PUT", "/transcript-lines", batch(4), http.StatusBadRequest, KindInvalid)
	var n int
	mustOK(t, h, accountA, "GET", "/transcript-lines/count?session=s1", nil, &n)
	if n != 3 {
		t.Errorf("count = %d, want 3 (the rejected batch must not be stored)", n)
	}

	// The default cap is 500.
	h, _ = newHandler(t, Options{})
	mustOK(t, h, accountA, "PUT", "/transcript-lines", batch(DefaultMaxLines), nil)
	mustFail(t, h, accountA, "PUT", "/transcript-lines", batch(DefaultMaxLines+1), http.StatusBadRequest, KindInvalid)
}

func TestSubagentLines(t *testing.T) {
	h, _ := newHandler(t, Options{})
	mustOK(t, h, accountA, "PUT", "/subagent-lines", session.SubagentLine{SessionID: "s1", SubagentID: "b", Seq: 0, Line: map[string]any{"t": "x"}, AccountID: "foreign"}, nil)
	var ack map[string]int
	mustOK(t, h, accountA, "PUT", "/subagent-lines", []session.SubagentLine{
		{SessionID: "s1", SubagentID: "a", Seq: 1, Line: map[string]any{"t": "y"}},
		{SessionID: "s1", SubagentID: "a", Seq: 0, Line: map[string]any{"t": "z"}},
	}, &ack)
	if ack["upserted"] != 2 {
		t.Errorf("upserted = %d, want 2", ack["upserted"])
	}

	var ids []string
	mustOK(t, h, accountA, "GET", "/subagents?session=s1", nil, &ids)
	if strings.Join(ids, ",") != "a,b" {
		t.Errorf("subagent ids = %v, want a,b", ids)
	}
	mustOK(t, h, accountB, "GET", "/subagents?session=s1", nil, &ids)
	if ids == nil || len(ids) != 0 {
		t.Errorf("account B sees %#v", ids)
	}

	got := docsOf(t, h, accountA, "POST", "/subagent-lines/query", map[string]any{"session_id": "s1", "subagent_id": "a"})
	if len(got) != 2 || got[0]["seq"] != float64(0) || got[1]["seq"] != float64(1) || got[0]["account_id"] != accountA {
		t.Errorf("subagent lines = %v", got)
	}
	if got := docsOf(t, h, accountB, "POST", "/subagent-lines/query", map[string]any{"session_id": "s1", "subagent_id": "a"}); len(got) != 0 {
		t.Errorf("account B sees %v", got)
	}
	mustFail(t, h, accountA, "POST", "/subagent-lines/query", map[string]any{"session_id": "s1"}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "PUT", "/subagent-lines", session.SubagentLine{SessionID: "s1", SubagentID: "../a"}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "PUT", "/subagent-lines", `[{"session_id": "s1", "subagent_id": "a", "seq": "x"}]`, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "PUT", "/subagent-lines", `42`, http.StatusBadRequest, KindInvalid)
}

func TestBlobs(t *testing.T) {
	h, _ := newHandler(t, Options{})
	blob := session.Blob{SessionID: "s1", BlobType: "tool-result", Name: "r1.txt", Content: "hello", Encoding: "utf8", AccountID: "foreign"}
	mustOK(t, h, accountA, "PUT", "/blobs", blob, nil)
	blob.Content = "hello again"
	mustOK(t, h, accountA, "PUT", "/blobs", blob, nil)

	got := docsOf(t, h, accountA, "POST", "/blobs/query", map[string]any{"session_id": "s1"})
	if len(got) != 1 || got[0]["content"] != "hello again" || got[0]["account_id"] != accountA || got[0]["name"] != "r1.txt" {
		t.Errorf("blobs = %v", got)
	}
	if got := docsOf(t, h, accountB, "POST", "/blobs/query", map[string]any{"session_id": "s1"}); len(got) != 0 {
		t.Errorf("account B sees %v", got)
	}
	mustFail(t, h, accountA, "PUT", "/blobs", session.Blob{SessionID: "s1", BlobType: "tool-result", Name: "../etc/passwd"}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "PUT", "/blobs", session.Blob{SessionID: "s1", Name: "x"}, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "POST", "/blobs/query", map[string]any{}, http.StatusBadRequest, KindInvalid)
}

func TestFullSession(t *testing.T) {
	h, _ := newHandler(t, Options{})
	putSession(t, h, accountA, session.Session{SessionID: "s1", LastSeen: t0})
	mustOK(t, h, accountA, "PUT", "/transcript-lines", []session.TranscriptLine{line("s1", 0)}, nil)
	mustOK(t, h, accountA, "POST", "/hook-events", []session.Doc{{"session_id": "s1", "hook_event_name": "Stop"}}, nil)
	mustOK(t, h, accountA, "PUT", "/blobs", session.Blob{SessionID: "s1", BlobType: "tool-result", Name: "r", Content: "c", Encoding: "utf8"}, nil)

	var full session.Doc
	mustOK(t, h, accountA, "GET", "/sessions/s1/full", nil, &full)
	if full["session_id"] != "s1" || full["account_id"] != accountA {
		t.Errorf("full = %v", full)
	}
	for _, key := range []string{"transcript_lines", "subagent_lines", "blobs", "hook_events"} {
		list, isList := full[key].([]any)
		if !isList {
			t.Errorf("%s = %#v, want a list", key, full[key])
			continue
		}
		want := 1
		if key == "subagent_lines" {
			want = 0
		}
		if len(list) != want {
			t.Errorf("%s has %d entries, want %d", key, len(list), want)
		}
	}
	mustFail(t, h, accountB, "GET", "/sessions/s1/full", nil, http.StatusNotFound, KindNotFound)
	mustFail(t, h, accountA, "GET", "/sessions/none/full", nil, http.StatusNotFound, KindNotFound)
}

func TestBodyTooLarge(t *testing.T) {
	h, _ := newHandler(t, Options{MaxBody: 1024})
	big := session.Session{SessionID: "s1", Cwd: strings.Repeat("x", 2048), LastSeen: t0}
	mustFail(t, h, accountA, "PUT", "/sessions", big, http.StatusRequestEntityTooLarge, KindTooLarge)
	mustFail(t, h, accountA, "GET", "/sessions/s1", nil, http.StatusNotFound, KindNotFound)

	small := session.Session{SessionID: "s1", Cwd: "/w", LastSeen: t0}
	mustOK(t, h, accountA, "PUT", "/sessions", small, nil)

	// The default limit is 16 MiB: a blob just under it is accepted, one over it is not.
	h, _ = newHandler(t, Options{})
	blob := session.Blob{SessionID: "s1", BlobType: "file-history", Name: "f", Encoding: "utf8", Content: strings.Repeat("y", int(DefaultMaxBody)-256)}
	mustOK(t, h, accountA, "PUT", "/blobs", blob, nil)
	blob.Content = strings.Repeat("y", int(DefaultMaxBody)+1)
	mustFail(t, h, accountA, "PUT", "/blobs", blob, http.StatusRequestEntityTooLarge, KindTooLarge)
}

func TestMalformedJSON(t *testing.T) {
	h, _ := newHandler(t, Options{})
	for _, c := range []struct{ method, path string }{
		{"PUT", "/sessions"}, {"POST", "/sessions/s1/touch"}, {"POST", "/sessions/s1/git"}, {"POST", "/sessions/find"},
		{"POST", "/hook-events"}, {"POST", "/hook-events/by-tool-use"}, {"PUT", "/transcript-lines"},
		{"POST", "/transcript-lines/query"}, {"PUT", "/subagent-lines"}, {"POST", "/subagent-lines/query"},
		{"PUT", "/blobs"}, {"POST", "/blobs/query"}, {"POST", "/bash-events"},
	} {
		mustFail(t, h, accountA, c.method, c.path, `{"session_id": `, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, c.method, c.path, ``, http.StatusBadRequest, KindInvalid)
	}
	// The wrong JSON type for a typed field is also invalid.
	mustFail(t, h, accountA, "PUT", "/sessions", `{"session_id": 7}`, http.StatusBadRequest, KindInvalid)
	mustFail(t, h, accountA, "PUT", "/sessions", `[]`, http.StatusBadRequest, KindInvalid)
}

func TestUnsafeIDs(t *testing.T) {
	h, _ := newHandler(t, Options{})
	for _, id := range []string{"a%2Fb", "..", "%2E%2E%2Fx", "a%5Cb", "."} {
		mustFail(t, h, accountA, "GET", "/sessions/"+id, nil, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "GET", "/sessions/"+id+"/full", nil, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "POST", "/sessions/"+id+"/touch", map[string]any{"at": t0}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "POST", "/sessions/"+id+"/git", map[string]any{"origin": "o"}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "GET", "/transcript-lines/count?session="+id, nil, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "GET", "/subagents?session="+id, nil, http.StatusBadRequest, KindInvalid)
	}
	for _, id := range []string{"", "a/b", "../x", `a\b`, "/abs"} {
		mustFail(t, h, accountA, "PUT", "/sessions", session.Session{SessionID: id, LastSeen: t0}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "PUT", "/transcript-lines", []session.TranscriptLine{{SessionID: id}}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "PUT", "/subagent-lines", session.SubagentLine{SessionID: id, SubagentID: "a"}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "PUT", "/blobs", session.Blob{SessionID: id, BlobType: "t", Name: "n"}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "POST", "/transcript-lines/query", session.LineQuery{SessionID: id}, http.StatusBadRequest, KindInvalid)
		mustFail(t, h, accountA, "POST", "/blobs/query", map[string]any{"session_id": id}, http.StatusBadRequest, KindInvalid)
	}
	if found := docsOf(t, h, accountA, "POST", "/sessions/find", session.SessionQuery{}); len(found) != 0 {
		t.Errorf("an unsafe id was stored: %v", found)
	}
}

func TestUnauthorizedSafetyNet(t *testing.T) {
	h, _ := newHandler(t, Options{})
	req := httptest.NewRequest("GET", Prefix+"/sessions/s1", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("status %d, WWW-Authenticate %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	mustFail(t, h, "", "GET", "/sessions/s1", nil, http.StatusUnauthorized, KindUnauthorized)
	mustFail(t, h, "", "PUT", "/sessions", session.Session{SessionID: "s1", LastSeen: t0}, http.StatusUnauthorized, KindUnauthorized)
	mustFail(t, h, "", "GET", "/nowhere", nil, http.StatusUnauthorized, KindUnauthorized)

	// Without an Account func nothing is served.
	none := Handler(memsession.New(), Options{})
	mustFail(t, none, accountA, "GET", "/sessions/s1", nil, http.StatusUnauthorized, KindUnauthorized)
	denied := Handler(memsession.New(), Options{Account: func(*http.Request) (string, bool) { return accountA, false }})
	mustFail(t, denied, accountA, "GET", "/sessions/s1", nil, http.StatusUnauthorized, KindUnauthorized)
}

func TestUnknownRoute(t *testing.T) {
	h, _ := newHandler(t, Options{})
	mustFail(t, h, accountA, "GET", "/nowhere", nil, http.StatusNotFound, KindNotFound)
	mustFail(t, h, accountA, "DELETE", "/sessions/s1", nil, http.StatusNotFound, KindNotFound)
	mustFail(t, h, accountA, "GET", "/sessions", nil, http.StatusNotFound, KindNotFound)
}

// failingStore fails every operation a test exercises after construction.
type failingStore struct {
	session.Store
	err error
}

func (f failingStore) GetSession(context.Context, string, string) (session.Doc, error) {
	return nil, f.err
}

func (f failingStore) InsertHookEvent(context.Context, session.Doc) error { return f.err }

func (f failingStore) UpsertSession(context.Context, session.Session) error { return f.err }

func TestStoreFailureIs502(t *testing.T) {
	boom := errors.New("connection reset")
	h := Handler(failingStore{Store: memsession.New(), err: boom}, Options{Account: headerAccount})
	status, env := call(t, h, accountA, "GET", "/sessions/s1", nil)
	if status != http.StatusBadGateway || env.Error == nil || env.Error.Kind != KindStore || !strings.Contains(env.Error.Message, "connection reset") {
		t.Errorf("status %d, error %+v", status, env.Error)
	}
	mustFail(t, h, accountA, "PUT", "/sessions", session.Session{SessionID: "s1", LastSeen: t0}, http.StatusBadGateway, KindStore)
	status, env = call(t, h, accountA, "POST", "/hook-events", []session.Doc{{"session_id": "s1"}, {"session_id": "s1"}})
	if status != http.StatusBadGateway || env.Error == nil || !strings.Contains(env.Error.Message, "inserted 0 of 2") {
		t.Errorf("status %d, error %+v", status, env.Error)
	}
	// Ownership check failing on the git route is a store failure too.
	mustFail(t, h, accountA, "POST", "/sessions/s1/git", map[string]any{"origin": "o"}, http.StatusBadGateway, KindStore)
}

// ctxStore refuses writes whose context is already done, as a database
// driver does.
type ctxStore struct{ session.Store }

func (c ctxStore) UpsertSession(ctx context.Context, s session.Session) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.Store.UpsertSession(ctx, s)
}

func TestWritesIgnoreClientCancellation(t *testing.T) {
	// A client that hangs up mid-request must not turn a committed write into
	// a retry, so writes run detached from the request's cancellation.
	st := memsession.New()
	h := Handler(ctxStore{st}, Options{Account: headerAccount})
	body, _ := json.Marshal(session.Session{SessionID: "s1", LastSeen: t0})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("PUT", Prefix+"/sessions", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set(accountHeader, accountA)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := st.GetSession(context.Background(), accountA, "s1"); err != nil {
		t.Errorf("session not stored: %v", err)
	}
}
