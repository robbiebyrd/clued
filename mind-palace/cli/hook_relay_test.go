package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

const relayToken = "relay-test-token"

// relayServer is a clued server for the relay tests: the session API and
// the whoami route over a store, behind a bearer token for testAccount,
// counting the requests it serves per method and path.
type relayServer struct {
	srv      *httptest.Server
	mu       sync.Mutex
	requests map[string]int
}

func newRelayServer(t *testing.T, store session.Store) *relayServer {
	t.Helper()
	s := &relayServer{requests: map[string]int{}}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/whoami", auth.WhoamiHandler("test"))
	mux.Handle(sessionapi.Prefix+"/", sessionapi.Handler(store, sessionapi.Options{Account: func(r *http.Request) (string, bool) {
		p, ok := auth.FromContext(r.Context())
		return p.AccountID, ok
	}}))
	h := auth.Middleware(stubTokenSource{token: relayToken, account: testAccount})(mux)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *relayServer) count(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[method+" "+path]
}

// relayConfig writes a config.json under a fresh config directory pointing
// at serverURL with token (local mode when serverURL is empty) and at a
// projects tree, and returns the config path and the projects directory.
// The MongoDB store fails the test if it is opened.
func relayConfig(t *testing.T, serverURL, token string) (configPath, projectsDir string) {
	t.Helper()
	dir := t.TempDir()
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectsDir = filepath.Join(dir, "projects")
	appConfig := filepath.Join(dir, "claude.json")
	if err := os.WriteFile(appConfig, []byte(`{"lastKnownAccountUuid":"`+testAccount+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"projectsDir":         projectsDir,
		"fileHistoryDir":      filepath.Join(dir, "history"),
		"claudeAppConfigPath": appConfig,
		"port":                1,
	}
	if serverURL != "" {
		cfg["serverUrl"] = serverURL
		cfg["serverToken"] = token
	}
	raw, _ := json.Marshal(cfg)
	configPath = filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	prev := openMongoSessionStore
	openMongoSessionStore = func(context.Context, session.Config) (session.Store, error) {
		t.Error("the MongoDB store was opened")
		return nil, errors.New("mongo must not be reached")
	}
	t.Cleanup(func() { openMongoSessionStore = prev })
	return configPath, projectsDir
}

// transcript writes lines to <projectsDir>/<project>/<sid>.jsonl and
// returns the file's path.
func transcript(t *testing.T, projectsDir, sid string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(projectsDir, "-work-alpha")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sid+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// hookEvent is the JSON of a hook input; sid and transcriptPath are left
// out when empty.
func hookEvent(event, sid, transcriptPath string) string {
	ev := map[string]any{"hook_event_name": event, "cwd": "/work/alpha", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}}
	if sid != "" {
		ev["session_id"] = sid
	}
	if transcriptPath != "" {
		ev["transcript_path"] = transcriptPath
	}
	raw, _ := json.Marshal(ev)
	return string(raw)
}

// runRelay runs hook relay with stdin and fails the test unless it exited 0
// without printing anything.
func runRelay(t *testing.T, configPath, stdin string) run {
	t.Helper()
	r := execStdin(t, stdin, "hook", "relay", "--config", configPath)
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("relay must exit 0 and print nothing: %+v", r)
	}
	return r
}

// fileLines returns the non-empty lines of path, or nil when it is absent.
func fileLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func TestHookRelayStoresTheEventAndPushesTheTranscript(t *testing.T) {
	backing := memsession.New()
	srv := newRelayServer(t, backing)
	configPath, projectsDir := relayConfig(t, srv.srv.URL, relayToken)
	tp := transcript(t, projectsDir, "sid-1", `{"type":"user","n":1}`, `{"type":"assistant","n":2}`)

	runRelay(t, configPath, hookEvent("PostToolUse", "sid-1", tp))

	ctx := context.Background()
	full, err := backing.FullSession(ctx, testAccount, "sid-1")
	if err != nil {
		t.Fatalf("session is not stored under the token's account: %v", err)
	}
	if full["cwd"] != "/work/alpha" || full["transcript_path"] != tp {
		t.Errorf("session = cwd %v transcript %v", full["cwd"], full["transcript_path"])
	}
	events := full["hook_events"].([]session.Doc)
	if len(events) != 2 || events[0]["hook_event_name"] != "PostToolUse" || events[0]["tool_name"] != "Bash" || events[1]["hook_event_name"] != session.StateChangedEventName || events[1]["to"] != "working" {
		t.Errorf("hook_events = %v, want the event and the state change it caused", events)
	}
	lines := full["transcript_lines"].([]session.Doc)
	if len(lines) != 2 || lines[1].Map("line")["n"] != float64(2) {
		t.Errorf("transcript_lines = %v", lines)
	}
	if n := srv.count("PUT", sessionapi.Prefix+"/transcript-lines"); n != 1 {
		t.Errorf("transcript pushes = %d, want 1", n)
	}
	configDir := filepath.Dir(configPath)
	if _, err := os.Stat(filepath.Join(configDir, "relay.log")); err == nil {
		t.Error("relay.log written although nothing failed")
	}
	if _, err := os.Stat(filepath.Join(configDir, "catchup", "sid-1.json")); err != nil {
		t.Errorf("catch-up state not kept under <config dir>/catchup: %v", err)
	}

	// Nothing new in the transcript: the event is stored, no lines are pushed.
	runRelay(t, configPath, hookEvent("PostToolUse", "sid-1", tp))
	if n := srv.count("PUT", sessionapi.Prefix+"/transcript-lines"); n != 1 {
		t.Errorf("a relay without new lines pushed the transcript (%d pushes)", n)
	}
	// Three posts: the first event, its state change, and the second event
	// (the state did not change again).
	if n := srv.count("POST", sessionapi.Prefix+"/hook-events"); n != 3 {
		t.Errorf("hook event posts = %d, want 3", n)
	}

	// Stop runs the catch-up as well and delivers the line written since.
	appendLine(t, tp, `{"type":"assistant","n":3}`)
	runRelay(t, configPath, hookEvent("Stop", "sid-1", tp))
	if count, err := backing.CountTranscriptLines(ctx, testAccount, "sid-1"); err != nil || count != 3 {
		t.Errorf("transcript lines after Stop = %d (%v), want 3", count, err)
	}
}

func TestHookRelayDropsAnEventWithoutAUsableSession(t *testing.T) {
	srv := newRelayServer(t, memsession.New())
	configPath, _ := relayConfig(t, srv.srv.URL, relayToken)
	configDir := filepath.Dir(configPath)

	// The server would refuse these for good; buffering them would replay
	// that refusal on every hook, so they are logged and dropped.
	runRelay(t, configPath, hookEvent("Notification", "", ""))
	runRelay(t, configPath, hookEvent("PostToolUse", "../etc", ""))

	if n := srv.count("POST", sessionapi.Prefix+"/hook-events"); n != 0 {
		t.Errorf("hook event posts = %d, want none", n)
	}
	if n := srv.count("PUT", sessionapi.Prefix+"/transcript-lines"); n != 0 {
		t.Errorf("an event without a session ran a catch-up (%d pushes)", n)
	}
	if _, err := os.Stat(filepath.Join(configDir, "events.wal")); err == nil {
		t.Error("a dropped event was buffered in the WAL")
	}
	logLines := fileLines(t, filepath.Join(configDir, "relay.log"))
	if len(logLines) != 2 || !strings.Contains(logLines[0], "Notification") || !strings.Contains(logLines[1], `"../etc"`) {
		t.Errorf("relay.log = %q, want one line per dropped event", logLines)
	}
}

func TestHookRelaySkipsTheCatchUpWithoutATranscriptPath(t *testing.T) {
	backing := memsession.New()
	srv := newRelayServer(t, backing)
	configPath, _ := relayConfig(t, srv.srv.URL, relayToken)

	runRelay(t, configPath, hookEvent("SessionStart", "sid-5", ""))

	if n := srv.count("POST", sessionapi.Prefix+"/hook-events"); n != 1 {
		t.Errorf("hook event posts = %d, want 1", n)
	}
	if n := srv.count("PUT", sessionapi.Prefix+"/transcript-lines"); n != 0 {
		t.Errorf("an event without a transcript path ran a catch-up (%d pushes)", n)
	}
	if _, err := backing.GetSession(context.Background(), testAccount, "sid-5"); err != nil {
		t.Errorf("session not tracked: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(configPath), "relay.log")); err == nil {
		t.Error("relay.log written although nothing failed")
	}
}

func TestHookRelayBuffersWhenTheServerIsDown(t *testing.T) {
	srv := newRelayServer(t, memsession.New())
	url := srv.srv.URL
	srv.srv.Close()
	configPath, projectsDir := relayConfig(t, url, relayToken)
	tp := transcript(t, projectsDir, "sid-2", `{"n":1}`)

	started := time.Now()
	r := runRelay(t, configPath, hookEvent("PostToolUse", "sid-2", tp))
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("relay took %s, over the 5 s budget", elapsed)
	}
	if r.stderr != "" {
		t.Errorf("stderr = %q, want nothing (failures go to relay.log)", r.stderr)
	}

	configDir := filepath.Dir(configPath)
	wal := fileLines(t, filepath.Join(configDir, "events.wal"))
	if len(wal) != 1 || !strings.Contains(wal[0], `"session_id":"sid-2"`) || !strings.Contains(wal[0], `"account_id":"`+testAccount+`"`) {
		t.Errorf("events.wal = %q, want the stamped event", wal)
	}
	logLines := fileLines(t, filepath.Join(configDir, "relay.log"))
	if len(logLines) != 1 || !strings.Contains(logLines[0], "sid-2") || !strings.Contains(logLines[0], "PostToolUse") {
		t.Fatalf("relay.log = %q, want one line naming the event", logLines)
	}
	if _, err := time.Parse(time.RFC3339, strings.Fields(logLines[0])[0]); err != nil {
		t.Errorf("relay.log line does not start with a timestamp: %q", logLines[0])
	}
	if info, err := os.Stat(filepath.Join(configDir, "relay.log")); err == nil && info.Mode().Perm() != 0o600 {
		t.Errorf("relay.log mode = %o, want 0600", info.Mode().Perm())
	}

	// A second failure appends a second line; the WAL keeps both events.
	runRelay(t, configPath, hookEvent("Stop", "sid-2", tp))
	if n := len(fileLines(t, filepath.Join(configDir, "relay.log"))); n != 2 {
		t.Errorf("relay.log has %d lines after two failed relays, want 2", n)
	}
	if n := len(fileLines(t, filepath.Join(configDir, "events.wal"))); n != 2 {
		t.Errorf("events.wal has %d events after two failed relays, want 2", n)
	}
}

func TestHookRelayFlushesTheWALOnceTheServerIsBack(t *testing.T) {
	backing := memsession.New()
	srv := newRelayServer(t, backing)
	configPath, projectsDir := relayConfig(t, srv.srv.URL, relayToken)
	tp := transcript(t, projectsDir, "sid-3", `{"n":1}`)
	walPath := filepath.Join(filepath.Dir(configPath), "events.wal")
	buffered := `{"hook_event_name":"PreToolUse","session_id":"sid-3","account_id":"` + testAccount + `","created_at":"2026-01-02T03:04:05Z"}` + "\n"
	if err := os.WriteFile(walPath, []byte(buffered), 0o600); err != nil {
		t.Fatal(err)
	}

	runRelay(t, configPath, hookEvent("PostToolUse", "sid-3", tp))

	if left := fileLines(t, walPath); len(left) != 0 {
		t.Errorf("events.wal still holds %q after a successful relay", left)
	}
	full, err := backing.FullSession(context.Background(), testAccount, "sid-3")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, ev := range full["hook_events"].([]session.Doc) {
		names = append(names, ev["hook_event_name"].(string))
	}
	if strings.Join(names, ",") != "PreToolUse,PostToolUse,SessionStateChanged" {
		t.Errorf("hook events = %v, want the buffered one replayed before the new one", names)
	}
}

func TestHookRelayDoesNothingInLocalMode(t *testing.T) {
	configPath, projectsDir := relayConfig(t, "", "")
	tp := transcript(t, projectsDir, "sid-4", `{"n":1}`)

	r := runRelay(t, configPath, hookEvent("PostToolUse", "sid-4", tp))
	if r.stderr != "" {
		t.Errorf("stderr = %q", r.stderr)
	}
	configDir := filepath.Dir(configPath)
	for _, name := range []string{"relay.log", "events.wal", "catchup"} {
		if _, err := os.Stat(filepath.Join(configDir, name)); err == nil {
			t.Errorf("%s was written in local mode", name)
		}
	}
}

func TestHookRelayLogsUnparsableInput(t *testing.T) {
	srv := newRelayServer(t, memsession.New())
	configPath, _ := relayConfig(t, srv.srv.URL, relayToken)

	runRelay(t, configPath, "not json")

	logLines := fileLines(t, filepath.Join(filepath.Dir(configPath), "relay.log"))
	if len(logLines) != 1 || !strings.Contains(logLines[0], "parse") {
		t.Errorf("relay.log = %q, want one parse failure", logLines)
	}
	if n := srv.count("POST", sessionapi.Prefix+"/hook-events"); n != 0 {
		t.Errorf("hook event posts = %d, want none", n)
	}
}

func runWhoami(t *testing.T, configPath string) run {
	t.Helper()
	return execStdin(t, "", "session", "--config", configPath, "whoami")
}

func TestSessionWhoamiPrintsThePrincipal(t *testing.T) {
	srv := newRelayServer(t, memsession.New())
	configPath, _ := relayConfig(t, srv.srv.URL, relayToken)

	res := sessionResult(t, runWhoami(t, configPath)).(map[string]any)
	if res["name"] != "test" || res["account_id"] != testAccount || res["version"] != "test" || res["server"] != srv.srv.URL {
		t.Errorf("whoami = %v", res)
	}
	if _, ok := res["projects"].([]any); !ok {
		t.Errorf("projects = %v, want a list", res["projects"])
	}
}

func TestSessionWhoamiFailsOnARejectedToken(t *testing.T) {
	srv := newRelayServer(t, memsession.New())
	configPath, _ := relayConfig(t, srv.srv.URL, "wrong-token")

	r := runWhoami(t, configPath)
	if r.code != exitCodes[service.KindStorage] || r.stdout != "" || !strings.Contains(r.stderr, "unauthorized") {
		t.Errorf("whoami with a rejected token = %+v, want a storage failure naming 401", r)
	}
}

func TestSessionWhoamiFailsWhenTheServerIsDown(t *testing.T) {
	srv := newRelayServer(t, memsession.New())
	url := srv.srv.URL
	srv.srv.Close()
	configPath, _ := relayConfig(t, url, relayToken)

	r := runWhoami(t, configPath)
	if r.code != exitCodes[service.KindStorage] || r.stdout != "" || !strings.Contains(r.stderr, "whoami") {
		t.Errorf("whoami with the server down = %+v, want a storage failure", r)
	}
}

func TestSessionWhoamiFailsWithoutAServer(t *testing.T) {
	configPath, _ := relayConfig(t, "", "")

	r := runWhoami(t, configPath)
	if r.code != exitCodes[service.KindBadRequest] || r.stdout != "" || !strings.Contains(r.stderr, "no server configured") {
		t.Errorf("whoami without a server = %+v, want BadRequest", r)
	}
}
