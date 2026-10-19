package plugin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/session/codesync"
	"github.com/robbiebyrd/clued/mind-palace/session/hostinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

// The code sync tests run the real event-relay hook, which hands each
// event to `mind-palace hook relay`, against a clued server with a session
// store and a code store, and read what reached the code store.

const codeSession = "s-code"

// codeServer is a clued server with the session API and the code transport
// over in-memory stores (one code store per project key), behind
// remoteToken, counting the requests it serves per method and path.
type codeServer struct {
	sessions *memsession.Store
	srv      *httptest.Server

	mu       sync.Mutex
	code     map[string]*memcode.Store
	requests map[string]int
}

func newCodeServer(t *testing.T) *codeServer {
	t.Helper()
	return newCodeServerWith(t, nil)
}

// newCodeServerWith is newCodeServer whose code transport reports every
// project GitHub-backed while githubBacked returns true (nil: none is).
func newCodeServerWith(t *testing.T, githubBacked func() bool) *codeServer {
	t.Helper()
	s := &codeServer{sessions: memsession.New(), code: map[string]*memcode.Store{}, requests: map[string]int{}}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/whoami", auth.WhoamiHandler("test"))
	mux.Handle(sessionapi.Prefix+"/", sessionapi.Handler(s.sessions, sessionapi.Options{Account: func(r *http.Request) (string, bool) {
		p, ok := auth.FromContext(r.Context())
		return p.AccountID, ok
	}}))
	opts := codeapi.Options{Open: func(_ context.Context, key string) (code.Store, error) {
		return s.store(key), nil
	}}
	if githubBacked != nil {
		opts.GitHubBacked = func(context.Context, string) bool { return githubBacked() }
	}
	codeapi.Mount(mux, opts)
	h := auth.Middleware(stubTokens{token: remoteToken, account: remoteAccount, projects: []string{"*"}})(mux)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests[r.Method+" "+r.URL.Path]++
		s.mu.Unlock()
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *codeServer) store(key string) *memcode.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.code[key]
	if !ok {
		st = memcode.New()
		s.code[key] = st
	}
	return st
}

// codeRequests counts the requests under the code routes.
func (s *codeServer) codeRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.requests {
		if strings.Contains(k, "/code/") {
			n += v
		}
	}
	return n
}

func (s *codeServer) count(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, v := range s.requests {
		if strings.HasPrefix(k, prefix) {
			n += v
		}
	}
	return n
}

// codeRepo is a git repository with files committed on main and pushed to
// a bare origin next to it.
type codeRepo struct {
	t          *testing.T
	root, bare string
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main", "-C", dir,
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func newCodeRepo(t *testing.T, files map[string]string) *codeRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := &codeRepo{t: t, root: filepath.Join(base, "repo"), bare: filepath.Join(base, "origin.git")}
	runGit(t, base, "init", "-q", "--bare", "origin.git")
	runGit(t, base, "init", "-q", "repo")
	runGit(t, r.root, "remote", "add", "origin", r.bare)
	for rel, content := range files {
		r.write(rel, content)
	}
	runGit(t, r.root, "add", "-A")
	runGit(t, r.root, "commit", "-q", "-m", "init")
	runGit(t, r.root, "push", "-q", "-u", "origin", "main")
	return r
}

func (r *codeRepo) path(rel string) string { return filepath.Join(r.root, filepath.FromSlash(rel)) }

func (r *codeRepo) write(rel, content string) {
	r.t.Helper()
	p := r.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *codeRepo) project() string { return projects.Slug(r.bare) }

// worktree is the id the relay computes for the repository on this host.
func (r *codeRepo) worktree() string { return codesync.WorktreeID(hostinfo.Read().Hostname, r.root) }

// codeHome is remoteHome for srv with codeSync set as given.
func codeHome(t *testing.T, srv *codeServer, codeSync bool) string {
	t.Helper()
	home := remoteHome(t, srv.srv.URL, remoteToken)
	path := dataPath(home, "config.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["codeSync"] = codeSync
	if raw, err = json.Marshal(cfg); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// relayEvent runs the event-relay hook in the repository with the event
// body (session id and cwd filled in) and checks it stayed silent.
func relayEvent(t *testing.T, r *codeRepo, env []string, body map[string]any) {
	t.Helper()
	body["session_id"] = codeSession
	if _, ok := body["cwd"]; !ok {
		body["cwd"] = r.root
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res := runScriptIn(t, r.root, hook(t, "event-relay"), string(raw), env)
	if res.code != 0 || res.stdout != "" {
		t.Fatalf("%s: got %+v, want exit 0 and no output", body["hook_event_name"], res)
	}
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// treeOf is the (worktree, ref) tree as path → entry.
func treeOf(t *testing.T, st code.Store, id, ref string) map[string]code.Entry {
	t.Helper()
	entries, err := st.Tree(context.Background(), id, ref)
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]code.Entry, len(entries))
	for _, e := range entries {
		out[e.Path] = e
	}
	return out
}

func treePaths(tree map[string]code.Entry) []string {
	out := make([]string, 0, len(tree))
	for p := range tree {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func wantTree(t *testing.T, step string, tree map[string]code.Entry, want map[string]string) {
	t.Helper()
	if len(tree) != len(want) {
		t.Errorf("%s: tree = %v, want the paths of %v", step, treePaths(tree), want)
	}
	for p, content := range want {
		e, ok := tree[p]
		if !ok {
			t.Errorf("%s: %s is missing from the tree %v", step, p, treePaths(tree))
			continue
		}
		if e.SHA256 != sha(content) {
			t.Errorf("%s: %s has sha256 %s, want that of %q", step, p, e.SHA256, content)
		}
	}
}

func noRelayLog(t *testing.T, home string) {
	t.Helper()
	if log, err := os.ReadFile(dataPath(home, "relay.log")); err == nil {
		t.Fatalf("relay.log reports a failure:\n%s", log)
	}
}

func TestEventRelaySyncsCodeOnFileChangingEvents(t *testing.T) {
	requireJq(t)
	srv := newCodeServer(t)
	home := codeHome(t, srv, true)
	env := remoteEnv(t, home, closedPort(t), "CLUED_WAL_PATH="+filepath.Join(t.TempDir(), "events.wal"))
	r := newCodeRepo(t, map[string]string{"main.go": "package main\n", "util.go": "package main // util\n", "old.go": "package main // old\n"})
	st := srv.store(r.project())
	id := r.worktree()
	files := map[string]string{"main.go": "package main\n", "util.go": "package main // util\n", "old.go": "package main // old\n"}

	// SessionStart: a full reconcile of the new worktree, and the session.
	relayEvent(t, r, env, map[string]any{"hook_event_name": "SessionStart", "source": "startup"})
	noRelayLog(t, home)
	wantTree(t, "SessionStart", treeOf(t, st, id, "main"), files)
	wts, err := st.Worktrees(context.Background())
	if err != nil || len(wts) != 1 || wts[0].ID != id {
		t.Fatalf("worktrees = %+v, %v; want %s", wts, err, id)
	}
	if w := wts[0]; w.Root != r.root || len(w.Sessions) != 1 || w.Sessions[0].ID != codeSession {
		t.Errorf("worktree = %+v, want root %s and session %s", w, r.root, codeSession)
	}

	// Write: a new file, named by the tool input.
	r.write("pkg/new.go", "package pkg // written\n")
	files["pkg/new.go"] = "package pkg // written\n"
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": r.path("pkg/new.go"), "content": "package pkg // written\n"},
	})
	noRelayLog(t, home)
	tree := treeOf(t, st, id, "main")
	wantTree(t, "Write", tree, files)
	if e := tree["pkg/new.go"]; !e.Dirty || e.Session != codeSession {
		t.Errorf("Write: entry = %+v, want dirty and attributed to %s", e, codeSession)
	}

	// Edit: an existing file, named relative to the cwd.
	r.write("util.go", "package main // util, edited\n")
	files["util.go"] = "package main // util, edited\n"
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": "util.go", "old_string": "util", "new_string": "util, edited"},
	})
	noRelayLog(t, home)
	wantTree(t, "Edit", treeOf(t, st, id, "main"), files)

	// An edit outside the worktree is not sent.
	outside := filepath.Join(t.TempDir(), "elsewhere.go")
	if err := os.WriteFile(outside, []byte("package elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := srv.count("POST " + codeapi.Prefix)
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": outside},
	})
	noRelayLog(t, home)
	if n := srv.count("POST " + codeapi.Prefix); n != before {
		t.Errorf("a write outside the worktree sent %d code requests", n-before)
	}

	// Bash: a removal and a rename the edit tools never saw, found by git
	// status.
	runGit(t, r.root, "rm", "-q", "old.go")
	runGit(t, r.root, "mv", "main.go", "app.go")
	delete(files, "old.go")
	files["app.go"] = files["main.go"]
	delete(files, "main.go")
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": "git rm old.go && git mv main.go app.go"},
	})
	noRelayLog(t, home)
	wantTree(t, "Bash", treeOf(t, st, id, "main"), files)

	// FileChanged: the reported path.
	r.write("watched.txt", "watched\n")
	files["watched.txt"] = "watched\n"
	relayEvent(t, r, env, map[string]any{"hook_event_name": "FileChanged", "file_path": r.path("watched.txt"), "event": "add"})
	noRelayLog(t, home)
	wantTree(t, "FileChanged", treeOf(t, st, id, "main"), files)

	// A failed tool wrote nothing: PostToolUseFailure sends nothing.
	before = srv.codeRequests()
	r.write("util.go", "package main // not by a tool that succeeded\n")
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUseFailure", "tool_name": "Edit",
		"tool_input": map[string]any{"file_path": r.path("util.go")},
	})
	if n := srv.codeRequests(); n != before {
		t.Errorf("PostToolUseFailure sent %d code requests", n-before)
	}

	// Stop: what changed outside any tool (the edit above, a generated file).
	files["util.go"] = "package main // not by a tool that succeeded\n"
	r.write("gen/out.go", "package gen // generated\n")
	files["gen/out.go"] = "package gen // generated\n"
	relayEvent(t, r, env, map[string]any{"hook_event_name": "Stop"})
	noRelayLog(t, home)
	wantTree(t, "Stop", treeOf(t, st, id, "main"), files)

	// Every change is in the log, attributed to the session.
	changes, err := st.Changes(context.Background(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) < 6 {
		t.Errorf("changes = %d, want one per syncing event (at least 6)", len(changes))
	}
	for _, c := range changes {
		if c.SessionID != codeSession || c.Owner != remoteAccount || c.Worktree != id || c.Ref != "main" {
			t.Errorf("change %d = session %q owner %q worktree %q ref %q", c.Seq, c.SessionID, c.Owner, c.Worktree, c.Ref)
		}
	}
	if !changes[0].Full {
		t.Error("the SessionStart change is not a full reconcile")
	}

	// The CLI: status reads the local state; a dry run sends nothing.
	cli := func(args ...string) map[string]any {
		t.Helper()
		cmd := exec.Command(filepath.Join(binaryDir(t), "mind-palace"), args...)
		cmd.Dir = r.root
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("mind-palace %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		var res struct {
			OK     bool           `json:"ok"`
			Result map[string]any `json:"result"`
		}
		if err := json.Unmarshal(out, &res); err != nil || !res.OK {
			t.Fatalf("mind-palace %s: %s (%v)", strings.Join(args, " "), out, err)
		}
		return res.Result
	}
	status := cli("session", "code-sync", "status")
	wts2, _ := status["worktrees"].([]any)
	if status["enabled"] != true || len(wts2) != 1 {
		t.Fatalf("status = %v, want enabled and one worktree", status)
	}
	w := wts2[0].(map[string]any)
	if w["worktree"] != id || w["root"] != r.root || w["ref"] != "main" || w["project"] != r.project() ||
		w["files"] != float64(len(files)) || w["synced"] != float64(len(files)) {
		t.Errorf("status worktree = %v, want %s at %s on main with %d synced files", w, id, r.root, len(files))
	}
	before = srv.codeRequests()
	dry := cli("session", "code-sync", "--full", "--dry-run", "--path", r.root)
	if dry["worktree"] != id || dry["dry_run"] != true || dry["upserts"] != float64(len(files)) {
		t.Errorf("dry run = %v, want the %d files of %s", dry, len(files), id)
	}
	if n := srv.codeRequests(); n != before {
		t.Errorf("a dry run sent %d code requests", n-before)
	}
	r.write("later.go", "package main // later\n")
	files["later.go"] = "package main // later\n"
	run := cli("session", "code-sync", "--path", filepath.Join(r.root, "pkg"))
	if run["upserts"] != float64(1) {
		t.Errorf("code-sync = %v, want the one new file", run)
	}
	wantTree(t, "code-sync", treeOf(t, st, id, "main"), files)

	// SessionEnd records the session's end.
	relayEvent(t, r, env, map[string]any{"hook_event_name": "SessionEnd", "reason": "exit"})
	noRelayLog(t, home)
	if wts, _ = st.Worktrees(context.Background()); len(wts) != 1 || len(wts[0].Sessions) != 1 || !wts[0].Sessions[0].Ended {
		t.Errorf("after SessionEnd worktrees = %+v, want the session ended", wts)
	}

	// WorktreeRemove drops the worktree on the server and its local state.
	relayEvent(t, r, env, map[string]any{"hook_event_name": "WorktreeRemove", "worktree_path": r.root})
	noRelayLog(t, home)
	if wts, _ = st.Worktrees(context.Background()); len(wts) == 1 && !wts[0].Removed {
		t.Errorf("after WorktreeRemove worktrees = %+v, want it removed", wts)
	}
	if tree := treeOf(t, st, id, "main"); len(tree) != 0 {
		t.Errorf("after WorktreeRemove the tree holds %v", treePaths(tree))
	}
	statePath, err := codesync.StatePath(codesync.StateDir(filepath.Dir(dataPath(home, "config.json"))), id)
	if err != nil {
		t.Fatal(err)
	}
	mustNotExist(t, statePath, "code sync state")
}

func TestEventRelaySendsNoCodeWithCodeSyncOff(t *testing.T) {
	requireJq(t)
	srv := newCodeServer(t)
	home := codeHome(t, srv, false)
	env := remoteEnv(t, home, closedPort(t), "CLUED_WAL_PATH="+filepath.Join(t.TempDir(), "events.wal"))
	r := newCodeRepo(t, map[string]string{"main.go": "package main\n"})

	r.write("new.go", "package main // new\n")
	for _, body := range []map[string]any{
		{"hook_event_name": "SessionStart", "source": "startup"},
		{"hook_event_name": "PostToolUse", "tool_name": "Write", "tool_input": map[string]any{"file_path": r.path("new.go")}},
		{"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_input": map[string]any{"command": "ls"}},
		{"hook_event_name": "Stop"},
		{"hook_event_name": "SessionEnd", "reason": "exit"},
	} {
		relayEvent(t, r, env, body)
	}
	noRelayLog(t, home)
	if n := srv.count("POST " + sessionapi.Prefix + "/hook-events"); n == 0 {
		t.Error("no hook event reached the server: the relay did not run")
	}
	if n := srv.codeRequests(); n != 0 {
		t.Errorf("code requests = %d with codeSync off, want 0", n)
	}
	srv.mu.Lock()
	stores := len(srv.code)
	srv.mu.Unlock()
	if stores != 0 {
		t.Errorf("%d code stores were opened with codeSync off", stores)
	}
	mustNotExist(t, codesync.StateDir(filepath.Dir(dataPath(home, "config.json"))), "code sync state directory")
}

func TestEventRelayLeavesCleanPushedContentToGitHub(t *testing.T) {
	requireJq(t)
	srv := newCodeServerWith(t, func() bool { return true })
	home := codeHome(t, srv, true)
	env := remoteEnv(t, home, closedPort(t), "CLUED_WAL_PATH="+filepath.Join(t.TempDir(), "events.wal"))
	files := map[string]string{"main.go": "package main\n", "util.go": "package main // util\n"}
	r := newCodeRepo(t, files)
	st := srv.store(r.project())
	id := r.worktree()
	manifest := "POST " + codeapi.Prefix + "/" + r.project() + "/code/manifest"
	blobs := "PUT " + codeapi.Prefix + "/" + r.project() + "/code/blobs/"
	wantOmitted := func(step, rel string) {
		t.Helper()
		e, ok := treeOf(t, st, id, "main")[rel]
		oid := runGit(t, r.root, "rev-parse", "HEAD:"+rel)
		if !ok || e.SHA256 != "" || e.Dirty || e.Unpushed || e.GitOID != oid || e.Size != int64(len(files[rel])) {
			t.Errorf("%s: %s = %+v, want clean, pushed, git_oid %s, size %d and no content", step, rel, e, oid, len(files[rel]))
		}
	}

	// SessionStart: the full reconcile reads capabilities and sends the
	// clean, pushed files without content: no manifest, no upload.
	relayEvent(t, r, env, map[string]any{"hook_event_name": "SessionStart", "source": "startup"})
	noRelayLog(t, home)
	if n := srv.count("GET " + codeapi.Prefix + "/" + r.project() + "/code/capabilities"); n == 0 {
		t.Fatal("the full reconcile did not read the capabilities")
	}
	if tree := treeOf(t, st, id, "main"); len(tree) != 2 {
		t.Fatalf("SessionStart: tree = %v", treePaths(tree))
	}
	wantOmitted("SessionStart", "main.go")
	wantOmitted("SessionStart", "util.go")
	if m, b := srv.count(manifest), srv.count(blobs); m != 0 || b != 0 {
		t.Errorf("SessionStart: %d manifest and %d blob requests, want none", m, b)
	}

	// Write: an overlay is uploaded as before.
	r.write("new.go", "package main // new\n")
	files["new.go"] = "package main // new\n"
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Write",
		"tool_input": map[string]any{"file_path": r.path("new.go")},
	})
	noRelayLog(t, home)
	if e := treeOf(t, st, id, "main")["new.go"]; e.SHA256 != sha(files["new.go"]) || !e.Dirty {
		t.Errorf("Write: new.go = %+v, want dirty with its content", e)
	}
	if b := srv.count(blobs); b != 1 {
		t.Errorf("Write: %d blob uploads, want 1", b)
	}

	// Bash: committed and pushed, the file is GitHub's to supply now.
	runGit(t, r.root, "add", "new.go")
	runGit(t, r.root, "commit", "-q", "-m", "new")
	runGit(t, r.root, "push", "-q")
	relayEvent(t, r, env, map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Bash",
		"tool_input": map[string]any{"command": "git commit -am new && git push"},
	})
	noRelayLog(t, home)
	wantOmitted("Bash push", "new.go")
	if b := srv.count(blobs); b != 1 {
		t.Errorf("Bash push: %d blob uploads, want still 1", b)
	}
}
