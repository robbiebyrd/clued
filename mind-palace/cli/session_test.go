package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	wantTop := []string{"call", "config", "mcp", "ops", "plan", "schema", "serve", "session", "stores", "story", "sync"}
	if !reflect.DeepEqual(top, wantTop) {
		t.Errorf("top-level commands = %v, want %v", top, wantTop)
	}
	wantPlan := []string{"add-spec", "clear-effort", "clear-repo", "create", "delete", "get", "link", "link-story", "list", "ops", "patch", "progress", "progress-story", "progress-unstory", "remove-progress", "remove-spec", "remove-web", "schema", "set-effort", "set-priority", "set-progress", "set-repo", "set-status", "set-title", "set-type", "set-web", "template", "transitions", "unlink", "unlink-story", "update", "validate"}
	wantStory := []string{"add-criterion", "add-file", "add-spec", "check", "clear-effort", "clear-repo", "create", "criteria", "delete", "get", "link", "link-plan", "list", "log", "ops", "patch", "plan-sections", "progress", "remove-criterion", "remove-file", "remove-progress", "remove-spec", "remove-web", "schema", "set-effort", "set-priority", "set-progress", "set-purpose", "set-repo", "set-status", "set-title", "set-type", "set-web", "template", "transitions", "unlink", "unlink-plan", "update", "validate"}
	if !reflect.DeepEqual(groups["plan"], wantPlan) {
		t.Errorf("plan group = %v", groups["plan"])
	}
	if !reflect.DeepEqual(groups["story"], wantStory) {
		t.Errorf("story group = %v", groups["story"])
	}
	wantSession := []string{"backfill", "context", "find", "full", "restore", "search-commands", "transcript"}
	if !reflect.DeepEqual(groups["session"], wantSession) {
		t.Errorf("session group = %v, want %v", groups["session"], wantSession)
	}
}
