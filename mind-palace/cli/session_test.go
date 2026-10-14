package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const testAccount = "acct-1"

var sessionT0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// sessionEnv points the session config at a temp tree, installs store as the
// session store, and returns the projects and file-history directories.
func sessionEnv(t *testing.T, store session.Store) (projectsDir, historyDir string) {
	t.Helper()
	dir := t.TempDir()
	projectsDir, historyDir = filepath.Join(dir, "projects"), filepath.Join(dir, "history")
	appConfig := filepath.Join(dir, "claude.json")
	if err := os.WriteFile(appConfig, []byte(`{"lastKnownAccountUuid":"`+testAccount+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUED_PROJECTS_DIR", projectsDir)
	t.Setenv("CLUED_FILE_HISTORY_DIR", historyDir)
	t.Setenv("CLUED_CLAUDE_APP_CONFIG_PATH", appConfig)
	t.Setenv("CLUED_WAL_PATH", filepath.Join(dir, "events.wal"))

	prev := openSessionStore
	openSessionStore = func(context.Context, session.Config) (session.Store, error) { return store, nil }
	t.Cleanup(func() { openSessionStore = prev })
	return projectsDir, historyDir
}

// seedSessions stores two sessions: s1 (alpha, 30 lines, two commands) and
// s2 (beta, a different repository, one command).
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
	must(st.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: testAccount, ProjectPath: "/work/alpha", Cwd: "/work/alpha", GitOrigin: "git@example.com:org/alpha.git", TranscriptPath: "/p/-work-alpha/s1.jsonl", LastSeen: sessionT0.Add(time.Hour)}))
	must(st.UpsertSession(ctx, session.Session{SessionID: "s2", AccountID: testAccount, ProjectPath: "/work/beta", Cwd: "/work/beta", GitOrigin: "git@example.com:org/beta.git", TranscriptPath: "/p/-work-beta/s2.jsonl", LastSeen: sessionT0}))
	var lines []session.TranscriptLine
	for i := range 30 {
		lines = append(lines, session.TranscriptLine{SessionID: "s1", Seq: i, AccountID: testAccount, Line: map[string]any{"n": i}})
	}
	must(st.UpsertTranscriptLines(ctx, lines))
	for i, m := range []struct{ session, kind, text string }{{"s1", "entry", "## 09:15 | main\nFixed the daemon restart"}, {"s2", "handoff", "Next: wire the listener"}} {
		must(st.InsertHookEvent(ctx, session.Doc{
			"session_id": m.session, "account_id": testAccount, "hook_event_name": session.MemoryEventName, "cwd": "/work/" + m.session, "created_at": sessionT0.Add(time.Duration(i) * time.Minute),
			"memory": map[string]any{"kind": m.kind, "file": "now.md", "text": m.text, "hash": "h-" + m.text},
		}))
	}
	for i, c := range []struct{ session, command string }{{"s1", "go test ./..."}, {"s1", "ls -la"}, {"s2", "go build"}} {
		must(st.InsertHookEvent(ctx, session.Doc{
			"session_id": c.session, "account_id": testAccount, "tool_name": "Bash",
			"tool_input": map[string]any{"command": c.command}, "created_at": sessionT0.Add(time.Duration(i) * time.Minute),
		}))
	}
	return st
}

func runSession(t *testing.T, args ...string) run {
	t.Helper()
	app := New()
	var out, errb bytes.Buffer
	app.Stdout, app.Stderr, app.Stdin = &out, &errb, strings.NewReader("")
	root := app.Root()
	root.SetArgs(append([]string{"session", "--config", filepath.Join(t.TempDir(), "none.json")}, args...))
	err := root.Execute()
	app.close()
	code := 0
	if err != nil {
		code = ExitCode(err)
	}
	return run{code: code, stdout: out.String(), stderr: errb.String()}
}

// sessionResult decodes the success envelope's result.
func sessionResult(t *testing.T, r run) any {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("unexpected failure: %+v", r)
	}
	var env struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil || !env.OK {
		t.Fatalf("not an ok envelope: %q (%v)", r.stdout, err)
	}
	return env.Result
}

func sessionIDs(t *testing.T, result any) []string {
	t.Helper()
	var ids []string
	for _, d := range result.([]any) {
		ids = append(ids, d.(map[string]any)["session_id"].(string))
	}
	return ids
}

func TestSessionFind(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	res := sessionResult(t, runSession(t, "find"))
	if got := sessionIDs(t, res); !reflect.DeepEqual(got, []string{"s1", "s2"}) {
		t.Errorf("find ids = %v, want newest first [s1 s2]", got)
	}
	if n := res.([]any)[0].(map[string]any)["event_count"]; n != float64(30) {
		t.Errorf("event_count = %v, want 30", n)
	}
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"find", "--project-path", "ALPHA"}, []string{"s1"}},
		{[]string{"find", "--git-origin", "org/beta"}, []string{"s2"}},
		{[]string{"find", "--query", "/work/b"}, []string{"s2"}},
		{[]string{"find", "--limit", "1"}, []string{"s1"}},
	} {
		if got := sessionIDs(t, sessionResult(t, runSession(t, c.args...))); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v = %v, want %v", c.args, got, c.want)
		}
	}
}

func TestSessionSearchCommands(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	commands := func(args ...string) []string {
		var out []string
		for _, h := range sessionResult(t, runSession(t, args...)).([]any) {
			out = append(out, h.(map[string]any)["command"].(string))
		}
		return out
	}
	if got := commands("search-commands", "^go "); !reflect.DeepEqual(got, []string{"go build", "go test ./..."}) {
		t.Errorf("all sessions = %v", got)
	}
	if got := commands("search-commands", "^go ", "--session", "s1"); !reflect.DeepEqual(got, []string{"go test ./..."}) {
		t.Errorf("--session = %v", got)
	}
	if got := commands("search-commands", ".", "--git-origin", "org/beta"); !reflect.DeepEqual(got, []string{"go build"}) {
		t.Errorf("--git-origin = %v", got)
	}
	if got := commands("search-commands", ".", "--limit", "1"); !reflect.DeepEqual(got, []string{"go build"}) {
		t.Errorf("--limit = %v", got)
	}
	hit := sessionResult(t, runSession(t, "search-commands", "build")).([]any)[0].(map[string]any)
	if hit["session_id"] != "s2" || hit["project_path"] != "/work/beta" || hit["git_origin"] != "git@example.com:org/beta.git" {
		t.Errorf("hit = %v", hit)
	}
}

func TestSessionContext(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	res := sessionResult(t, runSession(t, "context", "s1")).(map[string]any)
	if res["session"].(map[string]any)["session_id"] != "s1" {
		t.Errorf("session = %v", res["session"])
	}
	if !reflect.DeepEqual(res["top_commands"], []any{"ls -la", "go test ./..."}) {
		t.Errorf("top_commands = %v", res["top_commands"])
	}
	if len(res["first_lines"].([]any)) != 20 || len(res["last_lines"].([]any)) != 20 {
		t.Errorf("first/last lines = %d/%d, want 20/20", len(res["first_lines"].([]any)), len(res["last_lines"].([]any)))
	}
}

func TestSessionFull(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	res := sessionResult(t, runSession(t, "full", "s1")).(map[string]any)
	if res["session_id"] != "s1" || len(res["transcript_lines"].([]any)) != 30 {
		t.Errorf("full = session %v, %d transcript lines", res["session_id"], len(res["transcript_lines"].([]any)))
	}
}

func TestSessionTranscript(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	lines := sessionResult(t, runSession(t, "transcript", "s1", "--offset", "5", "--limit", "3")).([]any)
	var got []float64
	for _, l := range lines {
		got = append(got, l.(map[string]any)["line"].(map[string]any)["n"].(float64))
	}
	if !reflect.DeepEqual(got, []float64{5, 6, 7}) {
		t.Errorf("lines = %v, want n = 5 6 7", got)
	}
}

func TestSessionRestore(t *testing.T) {
	st := seedSessions(t)
	sessionEnv(t, st)
	target := t.TempDir()

	res := sessionResult(t, runSession(t, "restore", "s1", "--projects-dir", target)).(map[string]any)
	if res["files_written"] != float64(1) {
		t.Errorf("result = %v, want one file written", res)
	}
	data, err := os.ReadFile(filepath.Join(target, "-work-alpha", "s1.jsonl"))
	if err != nil || strings.Count(string(data), "\n") != 30 {
		t.Errorf("restored transcript: %d lines, err %v", strings.Count(string(data), "\n"), err)
	}

	override := t.TempDir()
	sessionResult(t, runSession(t, "restore", "s1", "--projects-dir", override, "--project-path", "/other/place"))
	if _, err := os.Stat(filepath.Join(override, "-other-place", "s1.jsonl")); err != nil {
		t.Errorf("--project-path not honoured: %v", err)
	}
}

func TestSessionRestoreDefaultsToConfiguredProjectsDir(t *testing.T) {
	projectsDir, _ := sessionEnv(t, seedSessions(t))

	sessionResult(t, runSession(t, "restore", "s1"))
	if _, err := os.Stat(filepath.Join(projectsDir, "-work-alpha", "s1.jsonl")); err != nil {
		t.Errorf("not restored into the configured projects dir: %v", err)
	}
}

func TestSessionBackfill(t *testing.T) {
	st := memsession.New()
	projectsDir, _ := sessionEnv(t, st)
	proj := filepath.Join(projectsDir, "-nonexistent-gamma")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	for id, body := range map[string]string{"b1": "{\"a\":1}\n{\"a\":2}\n", "b2": "{\"a\":3}\n"} {
		if err := os.WriteFile(filepath.Join(proj, id+".jsonl"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := sessionResult(t, runSession(t, "backfill")).(map[string]any)
	if res["sessions"] != float64(2) || res["lines"] != float64(3) {
		t.Errorf("result = %v, want 2 sessions and 3 lines", res)
	}
	if n, err := st.CountTranscriptLines(context.Background(), testAccount, "b1"); err != nil || n != 2 {
		t.Errorf("stored lines for b1 = %d, %v", n, err)
	}
}

func TestSessionUnknownSessionExitsNotFound(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	for _, cmd := range []string{"context", "full", "transcript", "restore"} {
		r := runSession(t, cmd, "missing")
		var env struct {
			OK    bool `json:"ok"`
			Error struct {
				Kind string `json:"kind"`
			} `json:"error"`
		}
		if r.code != 4 || r.stdout != "" || json.Unmarshal([]byte(r.stderr), &env) != nil || env.OK || env.Error.Kind != "NotFound" {
			t.Errorf("%s: %+v", cmd, r)
		}
	}
}

func TestSessionBadInputExitsBadRequest(t *testing.T) {
	sessionEnv(t, seedSessions(t))

	for _, args := range [][]string{
		{"find", "--query", "("},
		{"find", "--project-path", "["},
		{"search-commands", "("},
		{"find", "--limit", "-1"},
		{"transcript", "s1", "--offset", "-1"},
	} {
		if r := runSession(t, args...); r.code != 2 || !strings.Contains(r.stderr, "BadRequest") {
			t.Errorf("%v: %+v", args, r)
		}
	}
}

func TestRootGainsOnlySessionGroup(t *testing.T) {
	groups := map[string][]string{}
	var top []string
	for _, c := range New().Root().Commands() {
		top = append(top, c.Name())
		for _, sub := range c.Commands() {
			groups[c.Name()] = append(groups[c.Name()], sub.Name())
		}
	}
	wantTop := []string{"call", "config", "context", "hook", "mcp", "ops", "plan", "review", "schema", "serve", "server", "session", "stores", "story", "sync"}
	if !reflect.DeepEqual(top, wantTop) {
		t.Errorf("top-level commands = %v, want %v", top, wantTop)
	}
	wantPlan := []string{"add-spec", "add-tag", "clear-effort", "clear-repo", "create", "delete", "dependencies", "get", "link", "link-story", "list", "ops", "patch", "progress", "progress-story", "progress-unstory", "ready", "remove-progress", "remove-spec", "remove-tag", "remove-web", "schema", "set-due-date", "set-effort", "set-milestone", "set-order", "set-planned", "set-priority", "set-progress", "set-repo", "set-status", "set-tags", "set-title", "set-type", "set-web", "template", "transitions", "unlink", "unlink-story", "update", "validate"}
	wantStory := []string{"add-criterion", "add-file", "add-spec", "add-tag", "check", "claim", "clear-effort", "clear-repo", "create", "criteria", "delete", "dependencies", "get", "link", "link-plan", "list", "log", "ops", "patch", "plan-sections", "progress", "ready", "remove-criterion", "remove-file", "remove-progress", "remove-spec", "remove-tag", "remove-web", "schema", "set-assignee", "set-due-date", "set-effort", "set-milestone", "set-order", "set-planned", "set-priority", "set-progress", "set-purpose", "set-repo", "set-status", "set-tags", "set-title", "set-type", "set-web", "template", "transitions", "unlink", "unlink-plan", "update", "validate"}
	if !reflect.DeepEqual(groups["plan"], wantPlan) {
		t.Errorf("plan group = %v", groups["plan"])
	}
	if !reflect.DeepEqual(groups["story"], wantStory) {
		t.Errorf("story group = %v", groups["story"])
	}
	wantSession := []string{"backfill", "config-pull", "context", "daemon", "find", "full", "limits", "login", "logout", "mcp", "memory-ingest", "restore", "search", "search-commands", "search-memory", "transcript", "usage", "whoami"}
	if !reflect.DeepEqual(groups["session"], wantSession) {
		t.Errorf("session group = %v, want %v", groups["session"], wantSession)
	}
}

// syncBuffer is a bytes.Buffer that commands running in a goroutine can write
// while the test polls it.
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

// runningCommand is a command executing in a goroutine under a cancellable context.
type runningCommand struct {
	stderr *syncBuffer
	cancel context.CancelFunc
	done   chan struct{}
	code   int // valid once done is closed
}

// startCommand runs args on a fresh App in the background.
func startCommand(t *testing.T, args ...string) *runningCommand {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rc := &runningCommand{stderr: &syncBuffer{}, cancel: cancel, done: make(chan struct{})}
	app := New()
	app.Stdout, app.Stderr, app.Stdin = &syncBuffer{}, rc.stderr, strings.NewReader("")
	root := app.Root()
	root.SetArgs(args)
	go func() {
		err := root.ExecuteContext(ctx)
		app.close()
		rc.code = ExitCode(err)
		close(rc.done)
	}()
	t.Cleanup(func() { cancel(); <-rc.done })
	return rc
}

// wait returns the command's exit code once it has exited on its own.
func (rc *runningCommand) wait(t *testing.T) int {
	t.Helper()
	select {
	case <-rc.done:
		return rc.code
	case <-time.After(10 * time.Second):
		t.Fatal("command did not exit")
		return -1
	}
}

// stop cancels the command and returns its exit code.
func (rc *runningCommand) stop(t *testing.T) int {
	t.Helper()
	rc.cancel()
	return rc.wait(t)
}

// waitFor polls cond until it holds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// freeAddr returns a loopback address nothing is listening on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func healthy(addr string) bool {
	resp, err := http.Get("http://" + addr + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func TestSessionDaemonServesHealthAndStopsOnCancel(t *testing.T) {
	sessionEnv(t, memsession.New())
	addr := freeAddr(t)
	rc := startCommand(t, "session", "--config", filepath.Join(t.TempDir(), "none.json"), "daemon", "--addr", addr)

	waitFor(t, "daemon health", func() bool { return healthy(addr) })
	if want := "clued daemon listening on http://" + addr; !strings.Contains(rc.stderr.String(), want) {
		t.Errorf("stderr = %q, want it to contain %q", rc.stderr.String(), want)
	}
	if code := rc.stop(t); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if healthy(addr) {
		t.Error("daemon still answers after cancel")
	}
}

func TestSessionDaemonExitsZeroWhenAddressInUse(t *testing.T) {
	sessionEnv(t, memsession.New())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rc := startCommand(t, "session", "--config", filepath.Join(t.TempDir(), "none.json"), "daemon", "--addr", ln.Addr().String())

	if code := rc.wait(t); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if got := rc.stderr.String(); !strings.Contains(got, "already running on "+ln.Addr().String()+"\n") {
		t.Errorf("stderr = %q, want an already-running note", got)
	}
}

func TestSessionDaemonConnectFailureExitsOne(t *testing.T) {
	sessionEnv(t, memsession.New())
	openSessionStore = func(context.Context, session.Config) (session.Store, error) {
		return nil, errors.New("mongo down")
	}
	rc := startCommand(t, "session", "--config", filepath.Join(t.TempDir(), "none.json"), "daemon", "--addr", freeAddr(t))

	if code := rc.wait(t); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := rc.stderr.String(); !strings.Contains(got, `"ok": false`) || !strings.Contains(got, "mongo down") {
		t.Errorf("stderr = %q, want the error envelope", got)
	}
}

// startStdioCommand runs args with the stdio transport replaced by an
// in-memory pair and returns the command and a client connected to it.
func startStdioCommand(t *testing.T, args ...string) (*runningCommand, *mcp.ClientSession, context.Context) {
	t.Helper()
	serverEnd, clientEnd := mcp.NewInMemoryTransports()
	prev := stdioTransport
	stdioTransport = func() mcp.Transport { return serverEnd }
	t.Cleanup(func() { stdioTransport = prev })
	rc := startCommand(t, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientEnd, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return rc, cs, ctx
}

func toolNames(t *testing.T, ctx context.Context, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return names
}

var sessionToolNames = []string{"find_sessions", "get_session_context", "get_full_session", "search_commands", "search_memory", "search_transcripts", "usage", "file_history", "find_errors", "get_limits", "read_transcript", "restore_session", "prime_context"}

func TestSessionMCPListsTheTwelveTools(t *testing.T) {
	sessionEnv(t, seedSessions(t))
	rc, cs, ctx := startStdioCommand(t, "session", "--config", filepath.Join(t.TempDir(), "none.json"), "mcp")

	names := toolNames(t, ctx, cs)
	if len(names) != len(sessionToolNames) {
		t.Errorf("tools = %v, want exactly %v", names, sessionToolNames)
	}
	for _, want := range sessionToolNames {
		if !contains(names, want) {
			t.Errorf("tool %s missing from %v", want, names)
		}
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "find_sessions", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("find_sessions: %v %+v", err, res)
	}
	if text := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(text, `"s1"`) {
		t.Errorf("find_sessions = %s, want the seeded session", text)
	}
	if code := rc.stop(t); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestSessionOutputTimestampsHaveExactlyThreeFractionalDigits(t *testing.T) {
	st := seedSessions(t)
	at := time.Date(2026, 10, 11, 17, 29, 21, 760_000_000, time.UTC)
	if err := st.UpsertSession(context.Background(), session.Session{SessionID: "s3", AccountID: testAccount, ProjectPath: "/work/ms", LastSeen: at}); err != nil {
		t.Fatal(err)
	}
	sessionEnv(t, st)

	r := runSession(t, "find", "--project-path", "/work/ms")

	if r.code != 0 || !strings.Contains(r.stdout, `"2026-10-11T17:29:21.760Z"`) {
		t.Fatalf("stdout = %q (code %d, stderr %q)", r.stdout, r.code, r.stderr)
	}
}

func TestSessionSearchMemory(t *testing.T) {
	sessionEnv(t, seedSessions(t))
	texts := func(args ...string) []string {
		var out []string
		for _, h := range sessionResult(t, runSession(t, args...)).([]any) {
			out = append(out, h.(map[string]any)["text"].(string))
		}
		return out
	}
	if got := texts("search-memory"); !reflect.DeepEqual(got, []string{"Next: wire the listener", "## 09:15 | main\nFixed the daemon restart"}) {
		t.Errorf("all records newest first = %v", got)
	}
	if got := texts("search-memory", "daemon"); !reflect.DeepEqual(got, []string{"## 09:15 | main\nFixed the daemon restart"}) {
		t.Errorf("pattern = %v", got)
	}
	if got := texts("search-memory", "--kind", "handoff"); !reflect.DeepEqual(got, []string{"Next: wire the listener"}) {
		t.Errorf("kind = %v", got)
	}
	if got := texts("search-memory", "--git-origin", "org/alpha"); !reflect.DeepEqual(got, []string{"## 09:15 | main\nFixed the daemon restart"}) {
		t.Errorf("git origin = %v", got)
	}
	hit := sessionResult(t, runSession(t, "search-memory", "--session", "s2")).([]any)[0].(map[string]any)
	if hit["project_path"] != "/work/beta" || hit["kind"] != "handoff" || hit["cwd"] != "/work/s2" || hit["file"] != "now.md" {
		t.Errorf("hit = %v", hit)
	}
	if r := runSession(t, "search-memory", "("); r.code == 0 {
		t.Errorf("invalid regex accepted: %+v", r)
	}
}

func TestSessionMemoryIngest(t *testing.T) {
	sessionEnv(t, seedSessions(t))
	var posted []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var doc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		posted = append(posted, doc)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	t.Setenv("CLUED_PORT", strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "now.md"), []byte("## 10:00 | main\nShipped the ingest.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "remember.md"), []byte("Handoff text.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := sessionResult(t, runSession(t, "memory-ingest", "--dir", store, "--cwd", "/work/alpha", "--session", "s1")).(map[string]any)
	if res["posted"] != float64(2) || res["scanned"] != float64(2) || res["session_id"] != "s1" || len(posted) != 2 {
		t.Fatalf("result = %v, posted %d", res, len(posted))
	}
	if posted[0]["hook_event_name"] != "MemorySaved" || posted[0]["session_id"] != "s1" || posted[0]["cwd"] != "/work/alpha" {
		t.Errorf("event = %v", posted[0])
	}
	// --quiet prints nothing; the state file keeps the second run idempotent.
	r := runSession(t, "memory-ingest", "--dir", store, "--cwd", "/work/alpha", "--quiet")
	if r.code != 0 || r.stdout != "" || len(posted) != 2 {
		t.Errorf("quiet rerun: %+v, posted %d", r, len(posted))
	}
	stateDir := filepath.Join(filepath.Dir(os.Getenv("CLUED_WAL_PATH")), "remember")
	if entries, err := os.ReadDir(stateDir); err != nil || len(entries) != 1 {
		t.Errorf("state dir %s: %v %v", stateDir, entries, err)
	}
}

// remoteEnv points the session config at a temp tree like sessionEnv, but
// keeps the real openSessionStore: it configures a server instead, serving
// store behind a bearer token, and makes the MongoDB constructor fail the
// test if it is reached. It returns the number of requests the server saw.
func remoteEnv(t *testing.T, store session.Store) (requests func() int32) {
	t.Helper()
	sessionEnv(t, nil)
	openSessionStore = openSessionStoreReal
	prev := openMongoSessionStore
	openMongoSessionStore = func(context.Context, session.Config) (session.Store, error) {
		t.Error("the MongoDB store was opened in remote mode")
		return nil, errors.New("mongo must not be reached")
	}
	t.Cleanup(func() { openMongoSessionStore = prev })

	const token = "remote-test-token"
	source := stubTokenSource{token: token, account: testAccount}
	h := auth.Middleware(source)(sessionapi.Handler(store, sessionapi.Options{Account: func(r *http.Request) (string, bool) {
		p, ok := auth.FromContext(r.Context())
		return p.AccountID, ok
	}}))
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("CLUED_SERVER_URL", srv.URL)
	t.Setenv("CLUED_SERVER_TOKEN", token)
	return n.Load
}

// openSessionStoreReal is the production openSessionStore, captured before
// any test replaces it.
var openSessionStoreReal = openSessionStore

type stubTokenSource struct{ token, account string }

func (s stubTokenSource) Lookup(raw string) (auth.Principal, bool) {
	if raw != s.token {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: "test", AccountID: s.account}, true
}

func TestSessionFindUsesTheRemoteStoreWhenAServerIsConfigured(t *testing.T) {
	requests := remoteEnv(t, seedSessions(t))

	res := sessionResult(t, runSession(t, "find"))
	if got := sessionIDs(t, res); !reflect.DeepEqual(got, []string{"s1", "s2"}) {
		t.Errorf("find ids = %v, want [s1 s2]", got)
	}
	if n := res.([]any)[0].(map[string]any)["event_count"]; n != float64(30) {
		t.Errorf("event_count = %v, want 30", n)
	}
	if requests() == 0 {
		t.Error("the server saw no requests")
	}
	full := sessionResult(t, runSession(t, "full", "s1")).(map[string]any)
	if full["session_id"] != "s1" || len(full["transcript_lines"].([]any)) != 30 {
		t.Errorf("full = session %v, %d transcript lines", full["session_id"], len(full["transcript_lines"].([]any)))
	}
	lines := sessionResult(t, runSession(t, "transcript", "s1", "--offset", "5", "--limit", "3")).([]any)
	if len(lines) != 3 || lines[0].(map[string]any)["line"].(map[string]any)["n"] != float64(5) {
		t.Errorf("transcript = %v, want n = 5 6 7", lines)
	}
	r := runSession(t, "context", "missing")
	if r.code != 4 || !strings.Contains(r.stderr, "NotFound") {
		t.Errorf("context missing: %+v, want NotFound", r)
	}
}

func TestSessionRemoteRefusesPlainHTTPOffLoopback(t *testing.T) {
	remoteEnv(t, memsession.New())
	t.Setenv("CLUED_SERVER_URL", "http://clued.example.com:8080")

	r := runSession(t, "find")
	if r.code == 0 || r.stdout != "" || !strings.Contains(r.stderr, remote.ErrInsecureURL.Error()) {
		t.Errorf("find = %+v, want a failure naming ErrInsecureURL", r)
	}
}

func TestSessionMemoryIngestStoresOnTheServerInRemoteMode(t *testing.T) {
	requests := remoteEnv(t, memsession.New())
	t.Setenv("CLUED_PORT", "1") // no daemon listens here, and none must be needed

	store := t.TempDir()
	if err := os.WriteFile(filepath.Join(store, "now.md"), []byte("## 10:00 | main\nStored on the server.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := sessionResult(t, runSession(t, "memory-ingest", "--dir", store, "--cwd", "/work/alpha", "--session", "s9")).(map[string]any)
	if res["posted"] != float64(1) || requests() == 0 {
		t.Fatalf("result = %v, requests %d", res, requests())
	}
	if data, err := os.ReadFile(os.Getenv("CLUED_WAL_PATH")); err == nil && len(data) != 0 {
		t.Errorf("the event went to the WAL instead of the server: %s", data)
	}
	hits := sessionResult(t, runSession(t, "search-memory", "server")).([]any)
	if len(hits) != 1 || hits[0].(map[string]any)["session_id"] != "s9" || hits[0].(map[string]any)["cwd"] != "/work/alpha" {
		t.Errorf("search-memory over the server = %v", hits)
	}
}
