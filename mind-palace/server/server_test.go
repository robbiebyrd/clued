package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// stubTokens is an auth.Source over a map of raw token to principal.
type stubTokens map[string]auth.Principal

func (s stubTokens) Lookup(raw string) (auth.Principal, bool) {
	p, ok := s[raw]
	return p, ok
}

const (
	tokenAB   = "tok-ab"   // account acct-ab, projects a and b
	tokenStar = "tok-star" // account acct-star, every project
)

// newCluedServer starts a clued server with a memory store per project and
// two tokens. opens counts the project stores opened.
func newCluedServer(t *testing.T) (*httptest.Server, *Clued, session.Store, *int) {
	t.Helper()
	sessions := memsession.New()
	opens := 0
	c := NewClued(CluedOptions{
		Version: "test",
		Tokens: stubTokens{
			tokenAB:   {Name: "ab", AccountID: "acct-ab", Projects: []string{"a", "b"}},
			tokenStar: {Name: "star", AccountID: "acct-star", Projects: []string{"*"}},
		},
		Sessions: sessions,
		Open: func(ctx context.Context, project string) (store.Store, error) {
			if project == "down" {
				return nil, errors.New("database down")
			}
			opens++
			return memstore.New(project), nil
		},
		Config: config.Default(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	return ts, c, sessions, &opens
}

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"error"`
}

// call sends a request with the token and project header (empty project
// sends no header) and decodes the envelope.
func call(t *testing.T, ts *httptest.Server, method, path, body, token, project string) (int, envelope) {
	t.Helper()
	req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if project != "" {
		req.Header.Set(ProjectHeader, project)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s %s: %d: not an envelope: %s", method, path, resp.StatusCode, raw)
	}
	return resp.StatusCode, env
}

func wantEnvelopeError(t *testing.T, what string, status int, env envelope, wantStatus int, wantKind string) {
	t.Helper()
	if status != wantStatus || env.OK || env.Error == nil || env.Error.Kind != wantKind {
		t.Errorf("%s: got %d %+v, want %d %s", what, status, env.Error, wantStatus, wantKind)
	}
}

func planInput(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../testdata/create-dsgn.json")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func count(t *testing.T, env envelope) int {
	t.Helper()
	var list []json.RawMessage
	if err := json.Unmarshal(env.Result, &list); err != nil {
		t.Fatalf("result is not a list: %s", env.Result)
	}
	return len(list)
}

func TestCluedServiceAPIIsProjectScoped(t *testing.T) {
	ts, _, _, opens := newCluedServer(t)
	input := planInput(t)
	if status, env := call(t, ts, "POST", "/api/v1/plans", input, tokenAB, "a"); status != http.StatusCreated || !env.OK {
		t.Fatalf("create in a: %d %+v", status, env.Error)
	}
	if status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "a"); status != 200 || count(t, env) != 1 {
		t.Errorf("list a: %d %s", status, env.Result)
	}
	if status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "b"); status != 200 || count(t, env) != 0 {
		t.Errorf("list b: %d %s", status, env.Result)
	}
	// ?project= is the fallback; the header wins when both are given.
	if status, env := call(t, ts, "GET", "/api/v1/plans?project=a", "", tokenAB, ""); status != 200 || count(t, env) != 1 {
		t.Errorf("list ?project=a: %d %s", status, env.Result)
	}
	if status, env := call(t, ts, "GET", "/api/v1/plans?project=a", "", tokenAB, "b"); status != 200 || count(t, env) != 0 {
		t.Errorf("header b over ?project=a: %d %s", status, env.Result)
	}
	// Another token sees the same project store.
	if status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenStar, "a"); status != 200 || count(t, env) != 1 {
		t.Errorf("list a as star: %d %s", status, env.Result)
	}
	if *opens != 2 {
		t.Errorf("opened %d project stores, want 2 (one per project)", *opens)
	}
	status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "")
	wantEnvelopeError(t, "missing project", status, env, http.StatusBadRequest, "invalid")
	status, env = call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "../x")
	wantEnvelopeError(t, "invalid slug", status, env, http.StatusBadRequest, "invalid")
	status, env = call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "c")
	wantEnvelopeError(t, "project outside the token", status, env, http.StatusForbidden, "forbidden")
	status, env = call(t, ts, "POST", "/api/v1/plans", input, tokenAB, "c")
	wantEnvelopeError(t, "write outside the token", status, env, http.StatusForbidden, "forbidden")
	status, env = call(t, ts, "GET", "/api/v1/plans", "", tokenStar, "down")
	wantEnvelopeError(t, "store failure", status, env, http.StatusBadGateway, "store")
	// No token at all is still 401 from the middleware.
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/plans", nil)
	req.Header.Set(ProjectHeader, "a")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", resp.StatusCode)
	}
	// The other routes of the clued server still answer.
	if status, env := call(t, ts, "GET", PathWhoami, "", tokenAB, ""); status != 200 || !env.OK {
		t.Errorf("whoami: %d", status)
	}
	if status, env := call(t, ts, "GET", "/api/v1/session/sessions/nope", "", tokenAB, ""); status != http.StatusNotFound || env.Error == nil || strings.Contains(env.Error.Message, "no route") {
		t.Errorf("sessionapi route lost: %d %+v", status, env.Error)
	}
	if status, env := call(t, ts, "GET", "/api/v1/projects/a/store/plan/nope", "", tokenAB, ""); status != http.StatusNotFound || env.Error == nil || strings.Contains(env.Error.Message, "no route") {
		t.Errorf("storeapi route lost: %d %+v", status, env.Error)
	}
	if status, env := call(t, ts, "GET", "/nope", "", tokenAB, ""); status != http.StatusNotFound || env.Error == nil || env.Error.Kind != "not_found" {
		t.Errorf("unknown route: %d %+v", status, env.Error)
	}
}

type frame struct {
	ID     json.RawMessage `json:"id"`
	OK     *bool           `json:"ok"`
	Result json.RawMessage `json:"result"`
	Event  string          `json:"event"`
	DocID  string          `json:"documentId"`
	Error  *service.Error  `json:"error"`
}

func dialWS(t *testing.T, ts *httptest.Server, token, project string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	if project != "" {
		h.Set(ProjectHeader, project)
	}
	c, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+PathWS, h)
	if c != nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, resp, err
}

func wsSend(t *testing.T, c *websocket.Conn, msg string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(msg)); err != nil {
		t.Fatal(err)
	}
}

func wsRecv(t *testing.T, c *websocket.Conn) frame {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var f frame
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("bad frame %s: %v", data, err)
	}
	return f
}

func TestCluedWebSocketIsProjectScoped(t *testing.T) {
	ts, _, _, _ := newCluedServer(t)
	for _, tc := range []struct {
		name, token, project string
		status               int
	}{
		{"missing project", tokenAB, "", http.StatusBadRequest},
		{"invalid slug", tokenAB, "A/B", http.StatusBadRequest},
		{"outside the token", tokenAB, "c", http.StatusForbidden},
		{"store failure", tokenStar, "down", http.StatusBadGateway},
	} {
		if _, resp, err := dialWS(t, ts, tc.token, tc.project); err == nil || resp == nil || resp.StatusCode != tc.status {
			t.Errorf("%s: err=%v resp=%v", tc.name, err, resp)
		}
	}
	ca, _, err := dialWS(t, ts, tokenAB, "a")
	if err != nil {
		t.Fatal(err)
	}
	cb, _, err := dialWS(t, ts, tokenAB, "b")
	if err != nil {
		t.Fatal(err)
	}
	// Both subscribe to everything; only a's socket sees a's events.
	for _, c := range []*websocket.Conn{ca, cb} {
		wsSend(t, c, `{"id":1,"op":"subscribe"}`)
		if f := wsRecv(t, c); f.OK == nil || !*f.OK {
			t.Fatalf("subscribe: %+v", f)
		}
	}
	wsSend(t, ca, `{"id":2,"op":"create","params":{"input":`+planInput(t)+`}}`)
	// The response and the plan.created event are written by different
	// goroutines, so either may arrive first; read both before judging.
	var f, ev frame
	for i := 0; i < 2; i++ {
		switch fr := wsRecv(t, ca); {
		case fr.Event != "":
			ev = fr
		default:
			f = fr
		}
	}
	if f.OK == nil || !*f.OK {
		t.Fatalf("create on a: %+v", f)
	}
	var plan struct {
		FrontMatter struct{ ID string } `json:"frontMatter"`
	}
	json.Unmarshal(f.Result, &plan)
	if ev.Event != "plan.created" || ev.DocID != plan.FrontMatter.ID {
		t.Errorf("a's event: %+v", ev)
	}
	wsSend(t, cb, `{"id":3,"op":"list"}`)
	if f := wsRecv(t, cb); f.Event != "" || string(f.ID) != "3" || string(f.Result) != "[]" {
		t.Errorf("b must see no event and an empty list: %+v %s", f, f.Result)
	}
	wsSend(t, ca, `{"id":4,"op":"list"}`)
	if f := wsRecv(t, ca); string(f.ID) != "4" || !strings.Contains(string(f.Result), plan.FrontMatter.ID) {
		t.Errorf("a lists its plan: %s", f.Result)
	}
	// The REST API and the socket share the project's palace and bus.
	if status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenStar, "a"); status != 200 || count(t, env) != 1 {
		t.Errorf("REST sees the socket's plan: %d %s", status, env.Result)
	}
	if status, env := call(t, ts, "PATCH", "/api/v1/plans/"+plan.FrontMatter.ID, `{"priority":"P1"}`, tokenStar, "a"); status != 200 {
		t.Errorf("REST patch: %d %+v", status, env.Error)
	}
	if f := wsRecv(t, ca); f.Event != "plan.updated" || f.DocID != plan.FrontMatter.ID {
		t.Errorf("a's socket sees the REST change: %+v", f)
	}
	// ?project= works for the socket too.
	cq, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+PathWS+"?project=a", http.Header{"Authorization": {"Bearer " + tokenAB}})
	if err != nil {
		t.Fatal(err)
	}
	defer cq.Close()
	wsSend(t, cq, `{"id":5,"op":"list"}`)
	if f := wsRecv(t, cq); !strings.Contains(string(f.Result), plan.FrontMatter.ID) {
		t.Errorf("?project=a socket: %s", f.Result)
	}
}

// headerTransport adds fixed headers to every request of an http.Client.
type headerTransport struct {
	header http.Header
	next   http.RoundTripper
}

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for k, v := range h.header {
		r.Header[k] = v
	}
	return h.next.RoundTrip(r)
}

func connectMCP(t *testing.T, endpoint, token, project string) (*mcp.ClientSession, error) {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	if project != "" {
		h.Set(ProjectHeader, project)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := client.Connect(t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: headerTransport{header: h, next: http.DefaultTransport}},
	}, nil)
	if err == nil {
		t.Cleanup(func() { sess.Close() })
	}
	return sess, err
}

func toolCall(t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := ""
	if len(res.Content) > 0 {
		if tc, ok := res.Content[0].(*mcp.TextContent); ok {
			text = tc.Text
		}
	}
	return res, text
}

func listLen(t *testing.T, text string) int {
	t.Helper()
	var env struct {
		Result []json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("not a list envelope: %s", text)
	}
	return len(env.Result)
}

func TestCluedMCPIsProjectScopedAndDiskFree(t *testing.T) {
	ts, _, sessions, _ := newCluedServer(t)
	ctx := context.Background()
	for _, s := range []session.Session{
		{SessionID: "s-ab", AccountID: "acct-ab", ProjectPath: "/home/ab/repo", LastSeen: time.Now()},
		{SessionID: "s-star", AccountID: "acct-star", ProjectPath: "/home/star/repo", LastSeen: time.Now()},
	} {
		if err := sessions.UpsertSession(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	endpoint := ts.URL + PathMCP
	sa, err := connectMCP(t, endpoint, tokenAB, "a")
	if err != nil {
		t.Fatal(err)
	}
	tools, err := sa.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"plan_create", "plan_list", "story_create", "sync", "watch", "find_sessions", "get_session_context", "get_full_session", "search_commands", "read_transcript"} {
		if !names[want] {
			t.Errorf("tools/list lacks %s", want)
		}
	}
	for _, unwanted := range []string{"restore_session", "prime_context"} {
		if names[unwanted] {
			t.Errorf("tools/list offers %s on the server", unwanted)
		}
	}

	var input map[string]any
	json.Unmarshal([]byte(planInput(t)), &input)
	if res, text := toolCall(t, sa, "plan_create", map[string]any{"input": input}); res.IsError {
		t.Fatalf("plan_create on a: %s", text)
	}
	if _, text := toolCall(t, sa, "plan_list", map[string]any{}); listLen(t, text) != 1 {
		t.Errorf("plan_list on a: %s", text)
	}
	sb, err := connectMCP(t, endpoint, tokenAB, "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, text := toolCall(t, sb, "plan_list", map[string]any{}); listLen(t, text) != 0 {
		t.Errorf("plan_list on b: %s", text)
	}
	// The REST API sees the plan MCP created.
	if status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "a"); status != 200 || count(t, env) != 1 {
		t.Errorf("REST after MCP create: %d %s", status, env.Result)
	}
	// Session tools are scoped by the token's account, not by project.
	if res, text := toolCall(t, sa, "find_sessions", map[string]any{}); res.IsError || !strings.Contains(text, "s-ab") || strings.Contains(text, "s-star") {
		t.Errorf("find_sessions as ab: %s", text)
	}
	sstar, err := connectMCP(t, endpoint, tokenStar, "b")
	if err != nil {
		t.Fatal(err)
	}
	if res, text := toolCall(t, sstar, "find_sessions", map[string]any{}); res.IsError || !strings.Contains(text, "s-star") || strings.Contains(text, "s-ab") {
		t.Errorf("find_sessions as star: %s", text)
	}
	// ?project= is the fallback for clients that cannot set headers.
	sq, err := connectMCP(t, endpoint+"?project=a", tokenAB, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, text := toolCall(t, sq, "plan_list", map[string]any{}); listLen(t, text) != 1 {
		t.Errorf("plan_list over ?project=a: %s", text)
	}
	// The SDK client reports the status text of a failed initialize.
	for _, tc := range []struct{ name, token, project, status string }{
		{"missing project", tokenAB, "", "Bad Request"},
		{"outside the token", tokenAB, "c", "Forbidden"},
		{"store failure", tokenStar, "down", "Bad Gateway"},
	} {
		if _, err := connectMCP(t, endpoint, tc.token, tc.project); err == nil || !strings.Contains(err.Error(), tc.status) {
			t.Errorf("%s: err=%v, want %s", tc.name, err, tc.status)
		}
	}
	// A session initialised on one project cannot be driven under another.
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"raw","version":"0"}}}`))
	req.Header.Set("Authorization", "Bearer "+tokenAB)
	req.Header.Set(ProjectHeader, "a")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != 200 || sid == "" {
		t.Fatalf("raw initialize: %d session=%q", resp.StatusCode, sid)
	}
	req, _ = http.NewRequest("POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+tokenAB)
	req.Header.Set(ProjectHeader, "b")
	req.Header.Set("Mcp-Session-Id", sid)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("a's MCP session under b: %d", resp.StatusCode)
	}
}

func TestCluedCloseRefusesNewProjects(t *testing.T) {
	ts, c, _, _ := newCluedServer(t)
	if status, _ := call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "a"); status != 200 {
		t.Fatalf("before close: %d", status)
	}
	c.Close()
	status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenAB, "a")
	wantEnvelopeError(t, "after close", status, env, http.StatusBadGateway, "store")
}

func TestResolveProjectReadsHeaderThenQuery(t *testing.T) {
	for _, tc := range []struct{ header, query, want string }{
		{"a", "", "a"},
		{"", "b", "b"},
		{"a", "b", "a"},
		{" a ", "", "a"},
		{"", "", ""},
	} {
		req := httptest.NewRequest("GET", "/api/v1/plans?project="+tc.query, nil)
		if tc.header != "" {
			req.Header.Set(ProjectHeader, tc.header)
		}
		if got := RequestProject(req); got != tc.want {
			t.Errorf("header %q query %q: got %q, want %q", tc.header, tc.query, got, tc.want)
		}
	}
}

func TestNewWithoutResolverKeepsTheFixedPalace(t *testing.T) {
	palace, err := service.NewPalace(config.Default(), memstore.New(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(palace, ops.All(), Options{HTTP: true, WebSocket: true, MCP: true})
	t.Cleanup(srv.Close)
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	if srv.MCP() == nil {
		t.Error("the fixed palace keeps the single MCP server")
	}
	resp, err := http.Get(ts.URL + "/api/v1/plans")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("fixed palace: %d", resp.StatusCode)
	}
	health, err := http.Get(ts.URL + PathHealth)
	if err != nil {
		t.Fatal(err)
	}
	defer health.Body.Close()
	body, _ := io.ReadAll(health.Body)
	if !strings.Contains(string(body), `"stores"`) {
		t.Errorf("health: %s", body)
	}
}

func TestMapResolveError(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		kind   string
	}{
		{ErrMissingProject, 400, "invalid"},
		{projects.ErrInvalidProject, 400, "invalid"},
		{ErrProjectForbidden, 403, "forbidden"},
		{errors.New("boom"), 502, "store"},
	} {
		se := mapResolveError(tc.err)
		if se.Status != tc.status || se.Kind != tc.kind || se.Message == "" {
			t.Errorf("%v: %+v", tc.err, se)
		}
	}
}
