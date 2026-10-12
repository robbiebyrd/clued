package plugin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// The remote-mode tests run the real hooks against a clued server started
// in the test and the mind-palace binary built from this module, which the
// shim finds on PATH because nothing is bundled (CLUED_DIST_DIR is empty).

const (
	remoteToken   = "plugin-test-token"
	remoteAccount = "acct-plugin"
)

// builtBinary is the mind-palace binary built once per test run.
var builtBinary struct {
	once    sync.Once
	dir     string
	skip    string
	failure string
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtBinary.dir != "" {
		os.RemoveAll(builtBinary.dir)
	}
	os.Exit(code)
}

// binaryDir returns a directory holding `mind-palace` built from this
// module, building it on first use. It skips the test when go is missing.
func binaryDir(t *testing.T) string {
	t.Helper()
	builtBinary.once.Do(func() {
		goBin, err := exec.LookPath("go")
		if err != nil {
			builtBinary.skip = "go not installed"
			return
		}
		dir, err := os.MkdirTemp("", "clued-plugin-bin-")
		if err != nil {
			builtBinary.failure = err.Error()
			return
		}
		cmd := exec.Command(goBin, "build", "-o", filepath.Join(dir, "mind-palace"), "./cmd/mind-palace")
		cmd.Dir = filepath.Join(repoRoot(t), "mind-palace")
		if out, err := cmd.CombinedOutput(); err != nil {
			os.RemoveAll(dir)
			builtBinary.failure = fmt.Sprintf("go build ./cmd/mind-palace: %v\n%s", err, out)
			return
		}
		builtBinary.dir = dir
	})
	if builtBinary.skip != "" {
		t.Skip(builtBinary.skip)
	}
	if builtBinary.failure != "" {
		t.Fatal(builtBinary.failure)
	}
	return builtBinary.dir
}

// stubTokens resolves one bearer token to a principal with the given projects.
type stubTokens struct {
	token, account string
	projects       []string
}

func (s stubTokens) Lookup(raw string) (auth.Principal, bool) {
	if raw != s.token {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: "plugin-test", AccountID: s.account, Projects: s.projects}, true
}

// remoteServer is a clued server for the hook tests: the session API and
// the whoami route over an in-memory store, behind remoteToken, counting
// the requests it serves per method and path.
type remoteServer struct {
	store    *memsession.Store
	srv      *httptest.Server
	mu       sync.Mutex
	requests map[string]int
}

func newRemoteServer(t *testing.T, tokenProjects ...string) *remoteServer {
	t.Helper()
	s := &remoteServer{store: memsession.New(), requests: map[string]int{}}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/whoami", auth.WhoamiHandler("test"))
	mux.Handle(sessionapi.Prefix+"/", sessionapi.Handler(s.store, sessionapi.Options{Account: func(r *http.Request) (string, bool) {
		p, ok := auth.FromContext(r.Context())
		return p.AccountID, ok
	}}))
	h := auth.Middleware(stubTokens{token: remoteToken, account: remoteAccount, projects: tokenProjects})(mux)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *remoteServer) URL() string { return s.srv.URL }

func (s *remoteServer) count(method, path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[method+" "+path]
}

// tracked reports whether the server holds session id under the token's account.
func (s *remoteServer) tracked(id string) bool {
	_, err := s.store.GetSession(context.Background(), remoteAccount, id)
	return err == nil
}

// waitTracked polls until the server holds session id, or fails the test.
func (s *remoteServer) waitTracked(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !s.tracked(id) {
		if time.Now().After(deadline) {
			t.Fatalf("session %s never reached the server", id)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// remoteHome returns a HOME whose config.json names serverURL and token
// (local mode when serverURL is empty) and keeps the Claude data under it.
func remoteHome(t *testing.T, serverURL, token string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".claude/plugins/data/clued")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"port":                1,
		"projectsDir":         filepath.Join(home, ".claude/projects"),
		"fileHistoryDir":      filepath.Join(home, ".claude/file-history"),
		"claudeAppConfigPath": filepath.Join(home, "claude.json"),
	}
	if serverURL != "" {
		cfg["serverUrl"] = serverURL
		cfg["serverToken"] = token
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func dataPath(home, name string) string {
	return filepath.Join(home, ".claude/plugins/data/clued", name)
}

// remoteEnv is the environment of a hook run against the built binary:
// nothing bundled, the binary on PATH, the daemon port closed.
func remoteEnv(t *testing.T, home, port string, extra ...string) []string {
	t.Helper()
	env := []string{
		"HOME=" + home,
		"PATH=" + binaryDir(t) + ":/usr/bin:/bin",
		"CLAUDE_PLUGIN_ROOT=" + repoRoot(t),
		"CLUED_PORT=" + port,
		"CLUED_DIST_DIR=" + t.TempDir(),
	}
	return append(env, extra...)
}

func hook(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "hooks", name)
}

func mustNotExist(t *testing.T, path, what string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("%s exists at %s (stat err = %v)", what, path, err)
	}
}

func TestEventRelayRoutesToTheServerInRemoteMode(t *testing.T) {
	requireJq(t)
	body := `{"session_id":"s-remote","hook_event_name":"PostToolUse","cwd":"/work/alpha","tool_name":"Bash","tool_input":{"command":"ls"}}`
	cases := map[string]func(t *testing.T, srv *remoteServer) (home string, extra []string){
		"from config.json": func(t *testing.T, srv *remoteServer) (string, []string) {
			return remoteHome(t, srv.URL(), remoteToken), nil
		},
		"from the environment": func(t *testing.T, srv *remoteServer) (string, []string) {
			return remoteHome(t, "", ""), []string{"CLUED_SERVER_URL=" + srv.URL(), "CLUED_SERVER_TOKEN=" + remoteToken}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			srv := newRemoteServer(t, "*")
			home, extra := setup(t, srv)
			walPath := filepath.Join(t.TempDir(), "events.wal")
			env := remoteEnv(t, home, closedPort(t), append(extra, "CLUED_WAL_PATH="+walPath)...)

			r := runScript(t, hook(t, "event-relay"), body, env)
			if r.code != 0 || r.stdout != "" {
				t.Fatalf("got %+v, want exit 0 and no output", r)
			}
			if n := srv.count("POST", sessionapi.Prefix+"/hook-events"); n != 1 {
				t.Errorf("hook-events requests = %d, want 1", n)
			}
			if !srv.tracked("s-remote") {
				t.Error("the event's session was not tracked on the server")
			}
			// The bash fallback would have appended the body to the WAL after
			// the loopback POST failed; the Go relay writes it only when the
			// server refused the event, which it also notes in relay.log.
			mustNotExist(t, walPath, "WAL")
			mustNotExist(t, dataPath(home, "events.wal"), "default WAL")
			if log, err := os.ReadFile(dataPath(home, "relay.log")); err == nil {
				t.Errorf("relay.log reports a failure:\n%s", log)
			}
		})
	}
}

func TestEventRelayKeepsTheLoopbackPathWithoutAServer(t *testing.T) {
	requireJq(t)
	home := remoteHome(t, "", "")
	walPath := filepath.Join(t.TempDir(), "events.wal")
	env := remoteEnv(t, home, closedPort(t), "CLUED_WAL_PATH="+walPath)
	body := `{"session_id":"s-local","hook_event_name":"PostToolUse"}`
	r := runScript(t, hook(t, "event-relay"), body, env)
	if r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	data, err := os.ReadFile(walPath)
	if err != nil {
		t.Fatalf("local mode should buffer in the WAL when no daemon answers: %v", err)
	}
	if string(data) != body+"\n" {
		t.Errorf("WAL = %q, want the body as one line", data)
	}
}

func TestSessionStartStartsNoDaemonInRemoteMode(t *testing.T) {
	requireJq(t)
	srv := newRemoteServer(t, "*")
	home := remoteHome(t, srv.URL(), remoteToken)
	port := closedPort(t)
	env := remoteEnv(t, home, port)
	body := `{"session_id":"s-start","hook_event_name":"SessionStart","source":"startup","cwd":"/work/alpha"}`

	r := runScript(t, hook(t, "session-start"), body, env)
	if r.code != 0 || r.stdout != "" {
		t.Fatalf("got %+v, want exit 0 and no output", r)
	}
	if strings.Contains(r.stderr, "daemon") {
		t.Errorf("stderr mentions the daemon: %q", r.stderr)
	}
	mustNotExist(t, dataPath(home, "daemon.log"), "daemon.log")
	mustNotExist(t, dataPath(home, "daemon.binary"), "daemon.binary")

	// The relay runs detached: the starting session reaches the server
	// shortly after the hook returned.
	srv.waitTracked(t, "s-start")
	if n := srv.count("POST", sessionapi.Prefix+"/hook-events"); n != 1 {
		t.Errorf("hook-events requests = %d, want 1 (the SessionStart event)", n)
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second); err == nil {
		conn.Close()
		t.Errorf("something listens on the daemon port %s", port)
	}
	// Hooks stay registered whichever mode is in use.
	if _, err := os.Stat(filepath.Join(home, ".claude/settings.json")); err != nil {
		t.Errorf("register-hooks did not write settings.json: %v", err)
	}
}

// gitRepoWithOrigin returns a fresh repository whose origin is origin (no
// origin at all when it is empty).
func gitRepoWithOrigin(t *testing.T, origin string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	gitHome := t.TempDir()
	commands := [][]string{{"init", "-q"}}
	if origin != "" {
		commands = append(commands, []string{"remote", "add", "origin", origin})
	}
	for _, args := range commands {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+gitHome, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

func runCheckSetupIn(t *testing.T, dir, home string) result {
	t.Helper()
	return runScriptIn(t, dir, hook(t, "check-setup"), "", remoteEnv(t, home, closedPort(t)))
}

func TestCheckSetupReportsARejectedToken(t *testing.T) {
	requireJq(t)
	srv := newRemoteServer(t, "*")
	home := remoteHome(t, srv.URL(), "not-the-token")
	r := runCheckSetupIn(t, t.TempDir(), home)
	msg := systemMessage(t, r)
	if !strings.Contains(msg, srv.URL()) || !strings.Contains(msg, "rejected the token") {
		t.Errorf("message should name the server and the rejected token: %s", msg)
	}
	if n := srv.count("GET", "/api/v1/whoami"); n != 1 {
		t.Errorf("whoami requests = %d, want 1", n)
	}
}

func TestCheckSetupReportsAnUnreachableServer(t *testing.T) {
	requireJq(t)
	url := "http://127.0.0.1:" + closedPort(t)
	home := remoteHome(t, url, remoteToken)
	r := runCheckSetupIn(t, t.TempDir(), home)
	msg := systemMessage(t, r)
	if !strings.Contains(msg, url) || !strings.Contains(msg, "unreachable") {
		t.Errorf("message should name the server as unreachable: %s", msg)
	}
	if strings.Contains(msg, checkSetupInstallCmd) || strings.Contains(msg, "/clued-setup to finish") {
		t.Errorf("binary and config are present, so no other problem expected: %s", msg)
	}
}

func TestCheckSetupSilentWhenTheServerAcceptsTheToken(t *testing.T) {
	requireJq(t)
	srv := newRemoteServer(t, "*")
	home := remoteHome(t, srv.URL(), remoteToken)
	for name, dir := range map[string]string{
		"in a repository":   gitRepoWithOrigin(t, "git@github.com:Owner/Repo.git"),
		"outside git":       t.TempDir(),
		"without an origin": gitRepoWithOrigin(t, ""),
	} {
		t.Run(name, func(t *testing.T) {
			r := runCheckSetupIn(t, dir, home)
			if r.stdout != "" || r.code != 0 {
				t.Errorf("got %+v, want silence", r)
			}
		})
	}
}

func TestCheckSetupWarnsWhenTheTokenDoesNotCoverThisProject(t *testing.T) {
	requireJq(t)
	srv := newRemoteServer(t, "github.com__other__repo")
	home := remoteHome(t, srv.URL(), remoteToken)
	repo := gitRepoWithOrigin(t, "git@github.com:Owner/Repo.git")
	msg := systemMessage(t, runCheckSetupIn(t, repo, home))
	for _, want := range []string{srv.URL(), "does not cover", "github.com__owner__repo", "github.com__other__repo"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q: %s", want, msg)
		}
	}

	// A token with no project at all covers nothing either.
	none := newRemoteServer(t)
	msg = systemMessage(t, runCheckSetupIn(t, repo, remoteHome(t, none.URL(), remoteToken)))
	if !strings.Contains(msg, "does not cover") {
		t.Errorf("empty project list should not cover the project: %s", msg)
	}
}

// TestCheckSetupDerivesTheProjectLikeTheServer pins the bash slug to
// projects.Slug: a token listing exactly the server's slug for the origin
// keeps check-setup silent.
func TestCheckSetupDerivesTheProjectLikeTheServer(t *testing.T) {
	requireJq(t)
	origins := []string{
		"git@github.com:Owner/Repo.git",
		"https://GitHub.com/Owner/Repo.git",
		"ssh://git@github.com:2222/owner/repo",
		"git://example.org/some.dir/sub-repo.git",
		"/srv/git/My Repo.git",
		"file:///tmp/x.git",
	}
	for _, origin := range origins {
		t.Run(origin, func(t *testing.T) {
			slug := projects.Slug(origin)
			srv := newRemoteServer(t, slug)
			home := remoteHome(t, srv.URL(), remoteToken)
			repo := gitRepoWithOrigin(t, origin)
			if r := runCheckSetupIn(t, repo, home); r.stdout != "" {
				t.Errorf("token covers %q, yet: %s", slug, r.stdout)
			}
			other := newRemoteServer(t, "github.com__someone__else")
			msg := systemMessage(t, runCheckSetupIn(t, repo, remoteHome(t, other.URL(), remoteToken)))
			if !strings.Contains(msg, "("+slug+")") {
				t.Errorf("message should name the project %q: %s", slug, msg)
			}
		})
	}
}
