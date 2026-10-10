package sessionmcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

const (
	account = "test-mcp-account"
	foreign = "other-account"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func seedSession(t *testing.T, st session.Store, acc, id, project, origin string) {
	t.Helper()
	must(t, st.UpsertSession(context.Background(), session.Session{
		SessionID: id, AccountID: acc, ProjectPath: project, GitOrigin: origin, Cwd: project, LastSeen: t0,
	}))
}

func seedLines(t *testing.T, st session.Store, acc, id string, n int) {
	t.Helper()
	lines := make([]session.TranscriptLine, n)
	for i := range lines {
		lines[i] = session.TranscriptLine{SessionID: id, Seq: i, AccountID: acc, Line: map[string]any{"type": "msg", "index": i}}
	}
	must(t, st.UpsertTranscriptLines(context.Background(), lines))
}

func seedBash(t *testing.T, st session.Store, acc, id, command string, at time.Time) {
	t.Helper()
	must(t, st.InsertHookEvent(context.Background(), session.Doc{
		"session_id": id, "account_id": acc, "tool_name": "Bash",
		"tool_input": map[string]any{"command": command}, "created_at": at,
	}))
}

// seeded mirrors the data of test/integration/mcp.test.ts.
func seeded(t *testing.T) session.Store {
	t.Helper()
	st := memsession.New()
	ctx := context.Background()
	seedSession(t, st, account, "sess-1", "/home/user/myrepo", "https://github.com/user/myrepo.git")
	seedSession(t, st, account, "sess-2", "/home/user/other", "https://github.com/user/other.git")
	seedSession(t, st, account, "sess-3", "/home/user/myrepo", "https://github.com/user/myrepo.git")
	seedSession(t, st, account, "sess-acct-1", "/home/user/acct", "")
	seedSession(t, st, foreign, "sess-acct-2", "/home/other/acct", "")
	seedBash(t, st, account, "sess-1", "git status", t0.Add(-3*time.Second))
	seedBash(t, st, account, "sess-1", "npm install", t0.Add(-2*time.Second))
	seedBash(t, st, account, "sess-1", "git status", t0.Add(-time.Second))
	seedBash(t, st, account, "sess-2", "cargo build", t0)
	seedBash(t, st, foreign, "sess-acct-2", "echo hello", t0)
	seedLines(t, st, account, "sess-1", 25)
	seedLines(t, st, account, "sess-3", 120)
	seedLines(t, st, foreign, "sess-acct-2", 1)
	for _, l := range []session.SubagentLine{
		{SessionID: "sess-1", SubagentID: "agent-a", Seq: 0, Line: map[string]any{"type": "user"}, AccountID: account},
		{SessionID: "sess-1", SubagentID: "agent-a", Seq: 1, Line: map[string]any{"type": "assistant"}, AccountID: account},
	} {
		must(t, st.UpsertSubagentLine(ctx, l))
	}
	must(t, st.UpsertBlob(ctx, session.Blob{
		SessionID: "sess-1", BlobType: "tool-result", Name: "out.txt", Content: "secret-content", Encoding: "utf8", AccountID: account,
	}))
	return st
}

type progressLog struct {
	mu     sync.Mutex
	params []*mcp.ProgressNotificationParams
}

func (p *progressLog) add(params *mcp.ProgressNotificationParams) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.params = append(p.params, params)
}

func (p *progressLog) all() []*mcp.ProgressNotificationParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*mcp.ProgressNotificationParams(nil), p.params...)
}

func connectClient(t *testing.T, srv *mcp.Server, progress *progressLog) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	must(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	opts := &mcp.ClientOptions{}
	if progress != nil {
		opts.ProgressNotificationHandler = func(_ context.Context, req *mcp.ProgressNotificationClientRequest) {
			progress.add(req.Params)
		}
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, opts).Connect(ctx, ct, nil)
	must(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func newTools(st session.Store, cfg session.Config) *sessionmcp.Tools {
	return sessionmcp.New(cfg, func(context.Context) (session.Store, error) { return st, nil }, account)
}

func client(t *testing.T) *mcp.ClientSession {
	t.Helper()
	return connectClient(t, newTools(seeded(t), session.Config{}).Server(), nil)
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	must(t, err)
	return res
}

func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("want one content block, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content is %T, want text", res.Content[0])
	}
	return tc.Text
}

func okJSON[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", text(t, res))
	}
	var out T
	must(t, json.Unmarshal([]byte(text(t, res)), &out))
	return out
}

func wantError(t *testing.T, res *mcp.CallToolResult, msg string) {
	t.Helper()
	if !res.IsError {
		t.Fatalf("want IsError result, got %s", text(t, res))
	}
	if got := text(t, res); got != msg {
		t.Fatalf("error text = %q, want %q", got, msg)
	}
}

type obj = map[string]any

func ids(list []obj) []string {
	out := make([]string, len(list))
	for i, o := range list {
		out[i], _ = o["session_id"].(string)
	}
	sort.Strings(out)
	return out
}

const toolSchemas = `{
  "find_sessions": {"description": "Find previous Claude Code sessions, newest first. Filters match (case-insensitive regex) against project path, cwd, and git origin.",
    "inputSchema": {"type":"object","properties":{
      "project_path":{"type":"string","description":"Regex matched against the session project path"},
      "git_origin":{"type":"string","description":"Regex matched against the git remote origin URL"},
      "query":{"type":"string","description":"Regex matched against project path or cwd"},
      "limit":{"type":"number","description":"Max sessions to return (default 10, max 500)"}}}},
  "get_session_context": {"description": "Summarise one session: metadata, its most recent distinct Bash commands, and the first/last transcript lines.",
    "inputSchema": {"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}},
  "get_full_session": {"description": "Return the complete record for one session: metadata, all transcript lines, all subagent lines, blob metadata (content excluded), and all hook events.",
    "inputSchema": {"type":"object","properties":{"session_id":{"type":"string"}},"required":["session_id"]}},
  "search_commands": {"description": "Search Bash commands run in previous sessions, newest first.",
    "inputSchema": {"type":"object","properties":{
      "pattern":{"type":"string","description":"Case-insensitive regex matched against the command"},
      "session_id":{"type":"string","description":"Restrict to one session"},
      "git_origin":{"type":"string","description":"Restrict to sessions whose git origin matches this regex"},
      "limit":{"type":"number","description":"Max commands to return (default 20, max 500)"}},"required":["pattern"]}},
  "read_transcript": {"description": "Read transcript lines of a session in order, paginated by offset/limit.",
    "inputSchema": {"type":"object","properties":{
      "session_id":{"type":"string"},
      "offset":{"type":"number","description":"Line offset (default 0)"},
      "limit":{"type":"number","description":"Lines to return (default 200, max 500)"}},"required":["session_id"]}},
  "restore_session": {"description": "Restore a session from MongoDB to the local filesystem. Reconstructs the main JSONL, subagent files, tool-result blobs, and file-history backups.",
    "inputSchema": {"type":"object","properties":{
      "session_id":{"type":"string","description":"Session to restore"},
      "project_path":{"type":"string","description":"Override the recorded project path (use when username/homedir differs on this machine)"},
      "projects_dir":{"type":"string","description":"Override the target ~/.claude/projects directory"}},"required":["session_id"]}}
}`

func TestToolListHasTheSixToolsWithTheProtocolSchemas(t *testing.T) {
	var want map[string]struct {
		Description string `json:"description"`
		InputSchema any    `json:"inputSchema"`
	}
	must(t, json.Unmarshal([]byte(toolSchemas), &want))

	res, err := client(t).ListTools(context.Background(), nil)
	must(t, err)
	if len(res.Tools) != len(want) {
		t.Fatalf("got %d tools, want %d", len(res.Tools), len(want))
	}
	for _, tool := range res.Tools {
		w, ok := want[tool.Name]
		if !ok {
			t.Fatalf("unexpected tool %q", tool.Name)
		}
		if tool.Description != w.Description {
			t.Errorf("%s description = %q", tool.Name, tool.Description)
		}
		var got any
		raw, err := json.Marshal(tool.InputSchema)
		must(t, err)
		must(t, json.Unmarshal(raw, &got))
		if !reflect.DeepEqual(got, w.InputSchema) {
			t.Errorf("%s inputSchema = %s", tool.Name, raw)
		}
		readOnly := tool.Name != "restore_session"
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != readOnly {
			t.Errorf("%s ReadOnlyHint want %v, annotations %+v", tool.Name, readOnly, tool.Annotations)
		}
	}
}

func TestServerIdentity(t *testing.T) {
	cs := client(t)
	info := cs.InitializeResult().ServerInfo
	if info.Name != "clued" {
		t.Fatalf("server name = %q", info.Name)
	}
	if cs.InitializeResult().Instructions != sessionmcp.Instructions || sessionmcp.Instructions == "" {
		t.Fatalf("instructions = %q", cs.InitializeResult().Instructions)
	}
}

func TestFindSessions(t *testing.T) {
	cs := client(t)

	byOrigin := okJSON[[]obj](t, call(t, cs, "find_sessions", obj{"git_origin": "user/myrepo"}))
	if got := ids(byOrigin); !reflect.DeepEqual(got, []string{"sess-1", "sess-3"}) {
		t.Fatalf("by git_origin = %v", got)
	}

	byPath := okJSON[[]obj](t, call(t, cs, "find_sessions", obj{"project_path": "/home/user/other"}))
	if got := ids(byPath); !reflect.DeepEqual(got, []string{"sess-2"}) {
		t.Fatalf("by project_path = %v", got)
	}

	none := okJSON[[]obj](t, call(t, cs, "find_sessions", obj{"git_origin": "nonexistent/repo"}))
	if none == nil || len(none) != 0 {
		t.Fatalf("no match = %#v, want []", none)
	}
	if got := text(t, call(t, cs, "find_sessions", obj{"git_origin": "nonexistent/repo"})); got != "[]" {
		t.Fatalf("no match text = %s", got)
	}

	counted := okJSON[[]obj](t, call(t, cs, "find_sessions", obj{"project_path": "/home/user/myrepo", "limit": 5}))
	for _, s := range counted {
		if s["session_id"] == "sess-1" && s["event_count"] != float64(25) {
			t.Fatalf("sess-1 event_count = %v", s["event_count"])
		}
	}

	byQuery := okJSON[[]obj](t, call(t, cs, "find_sessions", obj{"query": "other"}))
	if got := ids(byQuery); !reflect.DeepEqual(got, []string{"sess-2"}) {
		t.Fatalf("by query = %v", got)
	}
}

func TestFindSessionsExcludesOtherAccounts(t *testing.T) {
	all := okJSON[[]obj](t, call(t, client(t), "find_sessions", obj{}))
	got := ids(all)
	for _, id := range got {
		if id == "sess-acct-2" {
			t.Fatal("foreign session leaked")
		}
	}
	if !reflect.DeepEqual(got, []string{"sess-1", "sess-2", "sess-3", "sess-acct-1"}) {
		t.Fatalf("sessions = %v", got)
	}
}

func TestSearchCommands(t *testing.T) {
	cs := client(t)

	git := okJSON[[]obj](t, call(t, cs, "search_commands", obj{"pattern": "git"}))
	if len(git) != 2 || git[0]["command"] != "git status" || git[0]["session_id"] != "sess-1" || git[0]["project_path"] != "/home/user/myrepo" {
		t.Fatalf("git hits = %v", git)
	}

	scoped := okJSON[[]obj](t, call(t, cs, "search_commands", obj{"pattern": "build", "git_origin": "user/other"}))
	if len(scoped) != 1 || scoped[0]["command"] != "cargo build" || scoped[0]["session_id"] != "sess-2" {
		t.Fatalf("scoped hits = %v", scoped)
	}

	if got := text(t, call(t, cs, "search_commands", obj{"pattern": "xyzzy_no_match_ever"})); got != "[]" {
		t.Fatalf("no match text = %s", got)
	}
}

func TestSearchCommandsAccountIsolation(t *testing.T) {
	cs := client(t)
	if got := text(t, call(t, cs, "search_commands", obj{"pattern": "echo"})); got != "[]" {
		t.Fatalf("foreign command leaked: %s", got)
	}
	if got := text(t, call(t, cs, "search_commands", obj{"session_id": "sess-acct-2", "pattern": "echo"})); got != "[]" {
		t.Fatalf("foreign session_id leaked: %s", got)
	}
}

func TestGetSessionContext(t *testing.T) {
	ctx := okJSON[obj](t, call(t, client(t), "get_session_context", obj{"session_id": "sess-1"}))
	sess := ctx["session"].(obj)
	if sess["session_id"] != "sess-1" || sess["git_origin"] != "https://github.com/user/myrepo.git" {
		t.Fatalf("session = %v", sess)
	}
	if !reflect.DeepEqual(ctx["top_commands"], []any{"git status", "npm install"}) {
		t.Fatalf("top_commands = %v", ctx["top_commands"])
	}
	first, last := ctx["first_lines"].([]any), ctx["last_lines"].([]any)
	if len(first) != 20 || first[0].(obj)["seq"] != float64(0) {
		t.Fatalf("first_lines = %d", len(first))
	}
	if len(last) != 20 || last[19].(obj)["seq"] != float64(24) {
		t.Fatalf("last_lines = %d", len(last))
	}
}

func TestSessionNotFoundErrors(t *testing.T) {
	cs := client(t)
	for _, tool := range []string{"get_session_context", "get_full_session", "read_transcript"} {
		for _, id := range []string{"does-not-exist", "sess-acct-2"} {
			wantError(t, call(t, cs, tool, obj{"session_id": id}), "session not found")
		}
	}
	wantError(t, call(t, cs, "restore_session", obj{"session_id": "does-not-exist"}), "session not found")
}

func TestGetFullSession(t *testing.T) {
	doc := okJSON[obj](t, call(t, client(t), "get_full_session", obj{"session_id": "sess-1"}))
	if doc["session_id"] != "sess-1" {
		t.Fatalf("session_id = %v", doc["session_id"])
	}
	lines := doc["transcript_lines"].([]any)
	if len(lines) != 25 || lines[0].(obj)["seq"] != float64(0) || lines[24].(obj)["seq"] != float64(24) {
		t.Fatalf("transcript_lines = %d", len(lines))
	}
	if n := len(doc["subagent_lines"].([]any)); n != 2 {
		t.Fatalf("subagent_lines = %d", n)
	}
	if n := len(doc["hook_events"].([]any)); n != 3 {
		t.Fatalf("hook_events = %d", n)
	}
	blobs := doc["blobs"].([]any)
	if len(blobs) != 1 {
		t.Fatalf("blobs = %d", len(blobs))
	}
	blob := blobs[0].(obj)
	if _, has := blob["content"]; has || blob["name"] != "out.txt" {
		t.Fatalf("blob = %v", blob)
	}
}

func TestReadTranscriptPaginates(t *testing.T) {
	lines := okJSON[[]obj](t, call(t, client(t), "read_transcript", obj{"session_id": "sess-1", "offset": 0, "limit": 10}))
	if len(lines) != 10 || lines[0]["seq"] != float64(0) || lines[9]["seq"] != float64(9) {
		t.Fatalf("lines = %v", lines)
	}
}

func TestReadTranscriptSendsProgressBeforeTheResult(t *testing.T) {
	progress := &progressLog{}
	cs := connectClient(t, newTools(seeded(t), session.Config{}).Server(), progress)

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Meta:      mcp.Meta{"progressToken": "tok-21"},
		Name:      "read_transcript",
		Arguments: obj{"session_id": "sess-3", "offset": 0, "limit": 200},
	})
	must(t, err)

	got := progress.all()
	if len(got) < 1 {
		t.Fatal("no progress notification arrived before the result")
	}
	last := 0.0
	for _, p := range got {
		if p.ProgressToken != "tok-21" {
			t.Fatalf("progressToken = %v", p.ProgressToken)
		}
		if p.Total != 120 || p.Progress <= last {
			t.Fatalf("progress %v/%v after %v", p.Progress, p.Total, last)
		}
		last = p.Progress
	}
	if last != 120 {
		t.Fatalf("final progress = %v, want 120", last)
	}
	if n := len(okJSON[[]obj](t, res)); n != 120 {
		t.Fatalf("lines = %d", n)
	}
}

func TestReadTranscriptWithoutTokenSendsNoProgress(t *testing.T) {
	progress := &progressLog{}
	cs := connectClient(t, newTools(seeded(t), session.Config{}).Server(), progress)
	call(t, cs, "read_transcript", obj{"session_id": "sess-3"})
	if n := len(progress.all()); n != 0 {
		t.Fatalf("%d progress notifications without a token", n)
	}
}

func TestRestoreSessionWritesIntoProjectsDir(t *testing.T) {
	projects, history := t.TempDir(), t.TempDir()
	cs := connectClient(t, newTools(seeded(t), session.Config{ProjectsDir: filepath.Join(t.TempDir(), "unused"), FileHistoryDir: history}).Server(), nil)

	res := okJSON[obj](t, call(t, cs, "restore_session", obj{"session_id": "sess-1", "projects_dir": projects}))
	if res["files_written"].(float64) < 3 {
		t.Fatalf("result = %v", res)
	}
	jsonl := filepath.Join(projects, session.EncodeProjectPath("/home/user/myrepo"), "sess-1.jsonl")
	if _, err := os.Stat(jsonl); err != nil {
		t.Fatalf("transcript not restored: %v", err)
	}
}

func TestRestoreSessionDefaultsToTheConfiguredProjectsDir(t *testing.T) {
	projects := t.TempDir()
	cs := connectClient(t, newTools(seeded(t), session.Config{ProjectsDir: projects, FileHistoryDir: t.TempDir()}).Server(), nil)

	okJSON[obj](t, call(t, cs, "restore_session", obj{"session_id": "sess-1", "project_path": "/elsewhere"}))
	if _, err := os.Stat(filepath.Join(projects, session.EncodeProjectPath("/elsewhere"), "sess-1.jsonl")); err != nil {
		t.Fatalf("transcript not restored into cfg.ProjectsDir: %v", err)
	}
}

func TestStoreConnectsOnceOnFirstCall(t *testing.T) {
	var calls atomic.Int32
	st := seeded(t)
	tools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) {
		calls.Add(1)
		return st, nil
	}, account)
	cs := connectClient(t, tools.Server(), nil)

	if n := calls.Load(); n != 0 {
		t.Fatalf("connected %d times before any tool call", n)
	}
	call(t, cs, "find_sessions", obj{})
	call(t, cs, "get_session_context", obj{"session_id": "sess-1"})
	call(t, cs, "read_transcript", obj{"session_id": "sess-1"})
	if n := calls.Load(); n != 1 {
		t.Fatalf("connected %d times, want 1", n)
	}
}

func TestFailedConnectIsReportedAndRetried(t *testing.T) {
	var calls atomic.Int32
	st := seeded(t)
	tools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("connection refused")
		}
		return st, nil
	}, account)
	cs := connectClient(t, tools.Server(), nil)

	wantError(t, call(t, cs, "find_sessions", obj{}), "MongoDB connection failed: connection refused")
	if n := len(okJSON[[]obj](t, call(t, cs, "find_sessions", obj{}))); n == 0 {
		t.Fatal("retry returned no sessions")
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("connect calls = %d, want 2", n)
	}
}

func TestAddToolsRegistersPrefixedNames(t *testing.T) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "shared", Version: "1"}, nil)
	newTools(seeded(t), session.Config{}).AddTools(srv, "pfx_")
	cs := connectClient(t, srv, nil)

	res, err := cs.ListTools(context.Background(), nil)
	must(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	if len(names) != 6 || !names["pfx_find_sessions"] || !names["pfx_restore_session"] {
		t.Fatalf("tools = %v", names)
	}
	if n := len(okJSON[[]obj](t, call(t, cs, "pfx_find_sessions", obj{}))); n != 4 {
		t.Fatalf("pfx_find_sessions returned %d sessions", n)
	}
}
