package codesync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/code/remotecode"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/codesync"
)

const (
	account = "acct-1"
	host    = "host-a"
	sessID  = "sess-1"
)

// stores hands out one memcode store per namespaced key, as the clued
// server's opener does.
type stores struct {
	mu    sync.Mutex
	byKey map[string]*memcode.Store
}

func (s *stores) open(_ context.Context, key string) (code.Store, error) { return s.get(key), nil }

func (s *stores) get(key string) *memcode.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byKey[key]
	if !ok {
		st = memcode.New()
		s.byKey[key] = st
	}
	return st
}

func (s *stores) empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byKey) == 0
}

// harness is a git repository with a bare origin it was pushed to, and a
// codeapi server over memcode the runs sync to.
type harness struct {
	t        *testing.T
	base     string
	bare     string
	root     string
	stores   *stores
	srv      *httptest.Server
	stateDir string
	cfg      session.Config
	now      time.Time
	mtime    time.Time
	logs     []string
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.name=Test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main",
		"-c", "advice.detachedHead=false", "-C", dir,
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// newHarness commits files to a new repository on main, pushes it to its
// origin and starts a code server with opts (Open and Account filled in).
func newHarness(t *testing.T, files map[string]string, opts codeapi.Options) *harness {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv(session.NoCaptureEnv, "")
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t:        t,
		base:     base,
		bare:     filepath.Join(base, "remote.git"),
		root:     filepath.Join(base, "repo"),
		stores:   &stores{byKey: map[string]*memcode.Store{}},
		stateDir: filepath.Join(base, "state"),
		now:      time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		mtime:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	git(t, base, "init", "-q", "--bare", "remote.git")
	git(t, base, "init", "-q", "repo")
	git(t, h.root, "remote", "add", "origin", h.bare)
	for rel, content := range files {
		h.write(rel, content)
	}
	git(t, h.root, "add", "-A")
	git(t, h.root, "commit", "-q", "--allow-empty", "-m", "init")
	git(t, h.root, "push", "-q", "-u", "origin", "main")

	if opts.Open == nil {
		opts.Open = h.stores.open
	}
	opts.Account = func(*http.Request) (string, bool) { return account, true }
	h.srv = httptest.NewServer(codeapi.Handler(opts))
	t.Cleanup(h.srv.Close)
	on := true
	h.cfg = session.Config{ServerURL: h.srv.URL, ServerToken: "tok", CodeSync: &on}
	return h
}

// write writes rel under the root with a fresh mtime, so its stamp always
// changes.
func (h *harness) write(rel, content string) {
	h.writeIn(h.root, rel, content)
}

func (h *harness) writeIn(root, rel, content string) {
	h.t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.mtime = h.mtime.Add(time.Second)
	if err := os.Chtimes(p, h.mtime, h.mtime); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) project() string { return projects.Slug(h.bare) }

func (h *harness) store() *memcode.Store { return h.stores.get(h.project()) }

func (h *harness) request(mod func(*codesync.Request)) codesync.Request {
	req := codesync.Request{
		Cwd:       h.root,
		SessionID: sessID,
		Budget:    -1,
		Config:    h.cfg,
		StateDir:  h.stateDir,
		Host:      host,
		Now:       func() time.Time { return h.now },
		Log:       func(msg string) { h.logs = append(h.logs, msg) },
	}
	if mod != nil {
		mod(&req)
	}
	return req
}

func (h *harness) run(mod func(*codesync.Request)) codesync.Report {
	h.t.Helper()
	rep, err := codesync.Run(context.Background(), h.request(mod))
	if err != nil {
		h.t.Fatalf("Run: %v", err)
	}
	return rep
}

func (h *harness) id() string { return codesync.WorktreeID(host, h.root) }

func (h *harness) tree(ref string) map[string]code.Entry {
	return treeOf(h.t, h.store(), h.id(), ref)
}

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

func (h *harness) changes() []code.Change {
	h.t.Helper()
	cs, err := h.store().Changes(context.Background(), 0, 1000)
	if err != nil {
		h.t.Fatal(err)
	}
	return cs
}

func (h *harness) state() codesync.State {
	h.t.Helper()
	st, err := codesync.LoadState(h.stateDir, h.id())
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *harness) oid(rev string) string { return git(h.t, h.root, "rev-parse", rev) }

func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func blobContent(t *testing.T, st code.Store, sha string) string {
	t.Helper()
	b, err := st.Blob(context.Background(), sha)
	if err != nil {
		t.Fatalf("blob %s: %v", sha, err)
	}
	return string(b.Content)
}

func paths(tree map[string]code.Entry) []string {
	var out []string
	for p := range tree {
		out = append(out, p)
	}
	return out
}

func TestFirstRunFiltersAndCommitsFull(t *testing.T) {
	h := newHarness(t, map[string]string{
		".gitignore":            "ignored/\n*.log\n",
		"main.go":               "package main\n",
		"README.md":             "# demo\n",
		".env":                  "TOKEN=1\n",
		"config/db-secret.yaml": "password: x\n",
		"bin.dat":               "ab\x00cd",
		"big.txt":               strings.Repeat("x", 200),
		"gen/out.go":            "package gen\n",
		"private/key.go":        "package private\n",
	}, codeapi.Options{})
	h.write("ignored/x.go", "package ignored\n")
	h.write("app.log", "log line\n")
	h.write("new.go", "package main // new\n")
	h.write("run.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(filepath.Join(h.root, "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("main.go", filepath.Join(h.root, "link")); err != nil {
		t.Fatal(err)
	}
	h.cfg.CodeSyncMaxFileBytes = 100
	h.cfg.CodeSyncExclude = []string{"gen"}
	h.cfg.ExcludePaths = []string{filepath.Join(h.root, "private")}

	rep := h.run(nil)
	if rep.Skipped != "" || !rep.Full || !rep.FullCommitted || rep.Pending != 0 {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Project != h.project() || rep.Ref != "main" || rep.Worktree != h.id() {
		t.Fatalf("identity = %+v", rep)
	}
	tree := h.tree("main")
	want := []string{".gitignore", "README.md", "big.txt", "bin.dat", "link", "main.go", "new.go", "run.sh"}
	if len(tree) != len(want) {
		t.Fatalf("tree paths = %v, want %v", paths(tree), want)
	}
	for _, p := range want {
		if _, ok := tree[p]; !ok {
			t.Fatalf("tree lacks %s: %v", p, paths(tree))
		}
	}
	main := tree["main.go"]
	if main.SHA256 != shaOf("package main\n") || main.GitOID != h.oid("HEAD:main.go") || main.Dirty || main.Unpushed ||
		main.Lang != "go" || main.Mode != "100644" || main.Session != sessID || main.Size != 13 {
		t.Fatalf("main.go = %+v", main)
	}
	if got := blobContent(t, h.store(), main.SHA256); got != "package main\n" {
		t.Fatalf("main.go blob = %q", got)
	}
	if e := tree["new.go"]; !e.Dirty || e.GitOID != "" || e.Unpushed || e.SHA256 != shaOf("package main // new\n") {
		t.Fatalf("untracked new.go = %+v", e)
	}
	for _, p := range []string{"bin.dat", "big.txt", "link"} {
		if tree[p].SHA256 != "" {
			t.Fatalf("%s content was sent: %+v", p, tree[p])
		}
	}
	if tree["big.txt"].Size != 200 || tree["link"].Mode != "120000" || tree["run.sh"].Mode != "100755" {
		t.Fatalf("metadata: big %+v link %+v run %+v", tree["big.txt"], tree["link"], tree["run.sh"])
	}
	blobs, _, err := h.store().Usage(context.Background())
	if err != nil || blobs != 5 {
		t.Fatalf("blobs = %d, %v; want 5", blobs, err)
	}
	cs := h.changes()
	if len(cs) != 1 || !cs[0].Full || cs[0].Upstream != h.oid("origin/main") || cs[0].Head != h.oid("HEAD") ||
		cs[0].Ahead != 0 || cs[0].SessionID != sessID || cs[0].Owner != account {
		t.Fatalf("changes = %+v", cs)
	}
	wts, err := h.store().Worktrees(context.Background())
	if err != nil || len(wts) != 1 {
		t.Fatalf("worktrees = %+v, %v", wts, err)
	}
	if w := wts[0]; w.Root != h.root || w.Host != host || w.Origin != h.bare || w.DefaultBranch != "main" || w.Owner != account {
		t.Fatalf("worktree = %+v", w)
	}

	// Nothing changed: no request beyond the status check, no change.
	rep = h.run(nil)
	if rep.Full || rep.Upserts != 0 || rep.Deletes != 0 || rep.Hashed != 0 || len(h.changes()) != 1 {
		t.Fatalf("idle run = %+v, %d changes", rep, len(h.changes()))
	}
	st := h.state()
	if st.Ref != "main" || st.Head != h.oid("HEAD") || st.LastFull.IsZero() || !st.Pending.Empty() || len(st.Files) != len(want) {
		t.Fatalf("state = %+v", st)
	}
}

func TestRedactsBeforeHashing(t *testing.T) {
	secret := `key := "sk-abcdefghijklmnopqrstuvwxyz0123"` + "\n"
	h := newHarness(t, map[string]string{"keys.go": secret}, codeapi.Options{})
	on := true
	h.cfg.RedactAtCapture = &on
	h.run(nil)
	e := h.tree("main")["keys.go"]
	got := blobContent(t, h.store(), e.SHA256)
	if strings.Contains(got, "sk-abc") || !strings.Contains(got, "[REDACTED:api-key]") {
		t.Fatalf("blob = %q", got)
	}
	if e.SHA256 != shaOf(got) || e.SHA256 == shaOf(secret) {
		t.Fatalf("sha256 %s does not name the redacted content", e.SHA256)
	}
	if e.Size != int64(len(secret)) || e.GitOID != h.oid("HEAD:keys.go") || e.Dirty {
		t.Fatalf("entry = %+v", e)
	}
}

func TestEditSendsOnlyThePath(t *testing.T) {
	h := newHarness(t, map[string]string{".gitignore": "*.log\n", "main.go": "package main\n", "util.go": "package main // util\n"}, codeapi.Options{})
	h.run(nil)
	h.write("main.go", "package main\n\nfunc main() {}\n")
	h.write("app.log", "ignored\n")
	rep := h.run(func(r *codesync.Request) {
		r.Paths = []string{filepath.Join(h.root, "main.go"), "app.log", "/etc/passwd", "../outside.go"}
	})
	if rep.Full || rep.Upserts != 1 || rep.Hashed != 1 || rep.Uploaded != 1 {
		t.Fatalf("report = %+v", rep)
	}
	cs := h.changes()
	last := cs[len(cs)-1]
	if len(cs) != 2 || last.Full || len(last.Upserts) != 1 || len(last.Deletes) != 0 {
		t.Fatalf("changes = %+v", cs)
	}
	e := last.Upserts[0]
	if e.Path != "main.go" || !e.Dirty || e.Unpushed || e.GitOID != h.oid("HEAD:main.go") || e.SHA256 != shaOf("package main\n\nfunc main() {}\n") {
		t.Fatalf("upsert = %+v", e)
	}
	if _, ok := h.tree("main")["app.log"]; ok {
		t.Fatal("ignored file synced")
	}
}

func TestBashRenameAndDelete(t *testing.T) {
	h := newHarness(t, map[string]string{"a.go": "package a\n", "README.md": "# r\n", "keep.go": "package keep\n"}, codeapi.Options{})
	h.run(nil)
	if err := os.Rename(filepath.Join(h.root, "a.go"), filepath.Join(h.root, "b.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(h.root, "README.md")); err != nil {
		t.Fatal(err)
	}
	rep := h.run(func(r *codesync.Request) { r.Status = true })
	if rep.Upserts != 1 || rep.Deletes != 2 || rep.Uploaded != 0 {
		t.Fatalf("report = %+v", rep)
	}
	tree := h.tree("main")
	if _, ok := tree["a.go"]; ok {
		t.Fatal("a.go still in the tree")
	}
	if _, ok := tree["README.md"]; ok {
		t.Fatal("README.md still in the tree")
	}
	if b := tree["b.go"]; b.SHA256 != shaOf("package a\n") || !b.Dirty || b.GitOID != "" {
		t.Fatalf("b.go = %+v", b)
	}
	if len(tree) != 2 {
		t.Fatalf("tree = %v", paths(tree))
	}
}

func TestGitFlagsAfterCommitPushAndReset(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n", "other.go": "package main // other\n"}, codeapi.Options{})
	h.run(nil)
	pushed := h.oid("HEAD")

	// Modified, then committed: clean with the new blob id, unpushed.
	h.write("main.go", "package main // v2\n")
	h.run(func(r *codesync.Request) { r.Paths = []string{"main.go"} })
	if e := h.tree("main")["main.go"]; !e.Dirty || e.Unpushed {
		t.Fatalf("modified main.go = %+v", e)
	}
	git(t, h.root, "commit", "-q", "-am", "v2")
	rep := h.run(nil)
	if rep.Uploaded != 0 || rep.Hashed != 0 {
		t.Fatalf("a commit re-sent content: %+v", rep)
	}
	tree := h.tree("main")
	if e := tree["main.go"]; e.Dirty || !e.Unpushed || e.GitOID != h.oid("HEAD:main.go") {
		t.Fatalf("committed main.go = %+v", e)
	}
	if e := tree["other.go"]; e.Dirty || e.Unpushed {
		t.Fatalf("other.go = %+v", e)
	}
	cs := h.changes()
	if last := cs[len(cs)-1]; last.Ahead != 1 || last.Upstream != pushed || last.Head != h.oid("HEAD") {
		t.Fatalf("change after commit = %+v", last)
	}

	// Pushed: nothing unpushed any more.
	git(t, h.root, "push", "-q")
	h.run(nil)
	if e := h.tree("main")["main.go"]; e.Dirty || e.Unpushed {
		t.Fatalf("pushed main.go = %+v", e)
	}
	cs = h.changes()
	if last := cs[len(cs)-1]; last.Ahead != 0 || last.Upstream != h.oid("HEAD") {
		t.Fatalf("change after push = %+v", last)
	}

	// Force-pushed back to the first commit: the upstream moved back.
	git(t, h.root, "reset", "-q", "--hard", pushed)
	git(t, h.root, "push", "-q", "--force")
	h.run(nil)
	if e := h.tree("main")["main.go"]; e.Dirty || e.Unpushed || e.SHA256 != shaOf("package main\n") || e.GitOID != h.oid("HEAD:main.go") {
		t.Fatalf("main.go after force push = %+v", e)
	}

	// Staged, then reverted.
	h.write("staged.go", "package main // staged\n")
	git(t, h.root, "add", "staged.go")
	h.write("other.go", "package main // edited\n")
	h.run(func(r *codesync.Request) { r.Status = true })
	tree = h.tree("main")
	if e := tree["staged.go"]; !e.Dirty || e.GitOID != "" {
		t.Fatalf("staged.go = %+v", e)
	}
	if e := tree["other.go"]; !e.Dirty {
		t.Fatalf("edited other.go = %+v", e)
	}
	h.write("other.go", "package main // other\n")
	h.run(func(r *codesync.Request) { r.Status = true })
	if e := h.tree("main")["other.go"]; e.Dirty || e.GitOID != h.oid("HEAD:other.go") || e.SHA256 != shaOf("package main // other\n") {
		t.Fatalf("reverted other.go = %+v", e)
	}
}

func TestBranchWithoutUpstreamAndRefSwitch(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	h.write("scratch.go", "package main // scratch\n")
	h.run(nil)
	if _, ok := h.tree("main")["scratch.go"]; !ok {
		t.Fatal("scratch.go not synced")
	}
	if err := os.Remove(filepath.Join(h.root, "scratch.go")); err != nil {
		t.Fatal(err)
	}

	git(t, h.root, "checkout", "-q", "-b", "topic")
	h.write("topic.go", "package main // topic\n")
	git(t, h.root, "add", "topic.go")
	git(t, h.root, "commit", "-q", "-m", "topic")
	rep := h.run(nil)
	if rep.Ref != "topic" || !rep.FullCommitted || rep.Uploaded != 1 {
		t.Fatalf("topic run = %+v", rep)
	}
	topic := h.tree("topic")
	if len(topic) != 2 {
		t.Fatalf("topic tree = %v", paths(topic))
	}
	for p, e := range topic {
		if !e.Unpushed || e.Dirty {
			t.Fatalf("%s on a branch with no upstream = %+v", p, e)
		}
	}
	cs := h.changes()
	if last := cs[len(cs)-1]; last.Ref != "topic" || last.Upstream != "" || !last.Full {
		t.Fatalf("topic change = %+v", last)
	}

	git(t, h.root, "push", "-q", "-u", "origin", "topic")
	h.run(nil)
	for p, e := range h.tree("topic") {
		if e.Unpushed {
			t.Fatalf("%s after pushing topic = %+v", p, e)
		}
	}

	// Back on main: its tree is reconciled in full, so scratch.go, deleted
	// while on topic, is dropped there too.
	git(t, h.root, "checkout", "-q", "main")
	rep = h.run(nil)
	if rep.Ref != "main" || !rep.FullCommitted {
		t.Fatalf("main run = %+v", rep)
	}
	main := h.tree("main")
	if len(main) != 1 || main["main.go"].SHA256 == "" {
		t.Fatalf("main tree = %v", paths(main))
	}
	wts, _ := h.store().Worktrees(context.Background())
	if len(wts) != 1 || len(wts[0].Refs) != 2 {
		t.Fatalf("worktrees = %+v", wts)
	}
}

func TestDetachedHead(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	git(t, h.root, "checkout", "-q", "--detach", "HEAD")
	rep := h.run(nil)
	want := "detached@" + h.oid("HEAD")[:12]
	if rep.Ref != want || len(h.tree(want)) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if e := h.tree(want)["main.go"]; !e.Unpushed {
		t.Fatalf("detached entry = %+v", e)
	}
}

func TestByteBudgetLeavesThePendingForLaterRuns(t *testing.T) {
	files := map[string]string{}
	for i := range 8 {
		files[fmt.Sprintf("f%d.go", i)] = fmt.Sprintf("package f // %d %s\n", i, strings.Repeat("x", 40))
	}
	h := newHarness(t, files, codeapi.Options{})
	budget := func(r *codesync.Request) { r.MaxBytes = 120 }
	rep := h.run(budget)
	if rep.FullCommitted || rep.Pending == 0 || rep.Hashed != 2 {
		t.Fatalf("first run = %+v", rep)
	}
	if !h.state().LastFull.IsZero() {
		t.Fatal("an incomplete reconcile counted as full")
	}
	runs := 1
	for !rep.FullCommitted {
		if runs++; runs > 10 {
			t.Fatal("the reconcile never finished")
		}
		rep = h.run(budget)
	}
	if rep.Pending != 0 || len(h.tree("main")) != 8 {
		t.Fatalf("final run = %+v, tree %v", rep, paths(h.tree("main")))
	}
	cs := h.changes()
	if !cs[len(cs)-1].Full || cs[0].Full {
		t.Fatalf("changes: first full=%v last full=%v", cs[0].Full, cs[len(cs)-1].Full)
	}
	if st := h.state(); st.LastFull.IsZero() || !st.Pending.Empty() {
		t.Fatalf("state = %+v", st)
	}
}

func TestLockSkipsAndStaleLockIsTaken(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	release, held, err := codesync.AcquireLock(h.stateDir, h.id())
	if err != nil || held {
		t.Fatalf("lock: held=%v err=%v", held, err)
	}
	if rep := h.run(nil); rep.Skipped != codesync.SkipLocked {
		t.Fatalf("report = %+v", rep)
	}
	if !h.stores.empty() {
		t.Fatal("a locked run sent something")
	}
	lock, _ := codesync.LockPath(h.stateDir, h.id())
	old := time.Now().Add(-2 * codesync.StaleAfter)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	if rep := h.run(nil); rep.Skipped != "" || !rep.FullCommitted {
		t.Fatalf("stale lock run = %+v", rep)
	}
	release()
}

func TestBackOffWhenTheServerHasNoCodeTransport(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	h.cfg.ServerURL = plain.URL
	rep := h.run(nil)
	if rep.BackedOff != "unsupported" {
		t.Fatalf("report = %+v", rep)
	}
	if st := h.state(); !st.UnsupportedUntil.Equal(h.now.Add(codesync.BackoffFor)) {
		t.Fatalf("unsupportedUntil = %v", st.UnsupportedUntil)
	}
	h.now = h.now.Add(time.Hour)
	if rep := h.run(nil); rep.Skipped != codesync.SkipBackedOff {
		t.Fatalf("report during back-off = %+v", rep)
	}
	h.now = h.now.Add(codesync.BackoffFor)
	h.cfg.ServerURL = h.srv.URL
	if rep := h.run(nil); rep.Skipped != "" || !rep.FullCommitted {
		t.Fatalf("report after back-off = %+v", rep)
	}
	if len(h.logs) != 0 {
		t.Fatalf("an unsupported server was logged: %v", h.logs)
	}
}

func TestBackOffOnForbiddenAndQuota(t *testing.T) {
	t.Run("forbidden", func(t *testing.T) {
		h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{
			BeforeWrite: func(context.Context, string, int64) error { return errors.New("no entitlement") },
		})
		rep := h.run(nil)
		if rep.BackedOff != "forbidden" || h.state().UnsupportedUntil.IsZero() || len(h.logs) != 0 {
			t.Fatalf("report = %+v, logs %v", rep, h.logs)
		}
	})
	t.Run("quota", func(t *testing.T) {
		h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{
			BeforeWrite: func(_ context.Context, _ string, n int64) error {
				if n > 0 {
					return fmt.Errorf("full: %w", codeapi.ErrQuota)
				}
				return nil
			},
		})
		rep := h.run(nil)
		if rep.BackedOff != "quota" || h.state().UnsupportedUntil.IsZero() {
			t.Fatalf("report = %+v", rep)
		}
		if len(h.logs) != 1 || !strings.Contains(h.logs[0], "quota") {
			t.Fatalf("logs = %v", h.logs)
		}
		if st := h.state(); st.Pending.Empty() {
			t.Fatal("the refused work was dropped")
		}
	})
}

// hidden hides remotecode's Capabilities, so the client learns the
// server's size limit only from a 413.
type hidden struct{ code.Store }

func TestTooLargeBlobIsRefusedUntilTheFileChanges(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n", "a.go": "package a\n"}, codeapi.Options{MaxFileBytes: 12})
	client, err := remote.New(h.cfg)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := remotecode.New(client, h.project())
	if err != nil {
		t.Fatal(err)
	}
	withStore := func(r *codesync.Request) { r.Store = hidden{rc} }
	rep := h.run(withStore)
	if rep.Refused != 1 || !rep.FullCommitted {
		t.Fatalf("report = %+v", rep)
	}
	tree := h.tree("main")
	if tree["main.go"].SHA256 != "" || tree["main.go"].Size != 13 || tree["a.go"].SHA256 == "" {
		t.Fatalf("tree = %+v", tree)
	}
	if _, ok := h.state().Refused["main.go"]; !ok {
		t.Fatal("main.go not recorded as refused")
	}
	h.write("main.go", "package main\n")
	h.write("main.go", "package main\n")
	rep = h.run(func(r *codesync.Request) { withStore(r); r.Paths = []string{"main.go"} })
	if rep.Refused != 1 || rep.Hashed != 1 {
		t.Fatalf("changed file not retried: %+v", rep)
	}
	rep = h.run(func(r *codesync.Request) { withStore(r); r.Paths = []string{"main.go"} })
	if rep.Refused != 0 || rep.Hashed != 0 || rep.Upserts != 0 {
		t.Fatalf("unchanged refused file retried: %+v", rep)
	}
}

func TestConflictClearsSyncedMarks(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n", "a.go": "package a\n"}, codeapi.Options{})
	first := memcode.New()
	h.run(func(r *codesync.Request) { r.Store = first })

	// A server that lost the blobs (a purge): the full commit names blobs
	// it lacks.
	purged := memcode.New()
	full := func(r *codesync.Request) { r.Store = purged; r.Full = true }
	rep := h.run(full)
	if rep.FullCommitted || rep.Pending != 2 {
		t.Fatalf("conflict run = %+v", rep)
	}
	for p, f := range h.state().Files {
		if f.Synced {
			t.Fatalf("%s still marked synced", p)
		}
	}
	rep = h.run(full)
	if !rep.FullCommitted || rep.Uploaded != 2 || rep.Pending != 0 {
		t.Fatalf("retry = %+v", rep)
	}
	if tree := treeOf(t, purged, h.id(), "main"); len(tree) != 2 || tree["a.go"].SHA256 == "" {
		t.Fatalf("tree = %+v", tree)
	}
}

func TestNetworkFailureKeepsThePending(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	h.run(nil)
	h.write("main.go", "package main // v2\n")
	h.srv.Close()
	_, err := codesync.Run(context.Background(), h.request(func(r *codesync.Request) { r.Paths = []string{"main.go"} }))
	if err == nil {
		t.Fatal("a failed request was not reported")
	}
	if st := h.state(); len(st.Pending.Upserts) != 1 || st.Pending.Upserts[0] != "main.go" || !st.UnsupportedUntil.IsZero() {
		t.Fatalf("state = %+v", st)
	}
}

func TestNothingIsSentWhenOff(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	off := false
	cases := map[string]struct {
		mod  func(*codesync.Request)
		env  string
		want string
	}{
		"codeSync unset":       {mod: func(r *codesync.Request) { r.Config.CodeSync = nil }, want: codesync.SkipDisabled},
		"codeSync false":       {mod: func(r *codesync.Request) { r.Config.CodeSync = &off }, want: codesync.SkipDisabled},
		"captureContent false": {mod: func(r *codesync.Request) { r.Config.CaptureContent = &off }, want: codesync.SkipDisabled},
		"local mode":           {mod: func(r *codesync.Request) { r.Config.ServerURL = "" }, want: codesync.SkipDisabled},
		"no capture":           {env: "1", want: codesync.SkipNoCapture},
		"excludePaths":         {mod: func(r *codesync.Request) { r.Config.ExcludePaths = []string{h.root} }, want: codesync.SkipExcluded},
		"not a repository":     {mod: func(r *codesync.Request) { r.Cwd = h.base }, want: codesync.SkipNotRepository},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(session.NoCaptureEnv, c.env)
			rep, err := codesync.Run(context.Background(), h.request(c.mod))
			if err != nil || rep.Skipped != c.want {
				t.Fatalf("report = %+v, %v; want skipped %q", rep, err, c.want)
			}
		})
	}

	t.Run("repository opt-out", func(t *testing.T) {
		h.write("mind-palace.config.yaml", "codeSync: false\n")
		defer os.Remove(filepath.Join(h.root, "mind-palace.config.yaml"))
		rep, err := codesync.Run(context.Background(), h.request(nil))
		if err != nil || rep.Skipped != codesync.SkipRepoOptOut {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
	t.Run("no origin", func(t *testing.T) {
		dir := filepath.Join(h.base, "plain")
		git(t, h.base, "init", "-q", "plain")
		h.writeIn(dir, "x.go", "package x\n")
		rep, err := codesync.Run(context.Background(), h.request(func(r *codesync.Request) { r.Cwd = dir }))
		if err != nil || rep.Skipped != codesync.SkipDirectorySlug {
			t.Fatalf("report = %+v, %v", rep, err)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		rep, err := codesync.Run(context.Background(), h.request(func(r *codesync.Request) { r.DryRun = true }))
		if err != nil || rep.Skipped != "" || rep.Upserts != 1 {
			t.Fatalf("report = %+v, %v", rep, err)
		}
		if _, err := os.Stat(filepath.Join(h.stateDir, h.id()+".json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("a dry run saved the state: %v", err)
		}
	})
	if !h.stores.empty() {
		t.Fatal("something reached the server")
	}
}

func TestTwoClonesOfOneBranchAgreeOnPushedEntries(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n", "lib/lib.go": "package lib\n"}, codeapi.Options{})
	var ids []string
	for _, name := range []string{"clone1", "clone2"} {
		git(t, h.base, "clone", "-q", h.bare, name)
		dir := filepath.Join(h.base, name)
		rep, err := codesync.Run(context.Background(), h.request(func(r *codesync.Request) { r.Cwd = dir }))
		if err != nil || !rep.FullCommitted || rep.Project != h.project() {
			t.Fatalf("%s: %+v, %v", name, rep, err)
		}
		ids = append(ids, rep.Worktree)
	}
	if ids[0] == ids[1] {
		t.Fatal("two clones share a worktree id")
	}
	a, b := treeOf(t, h.store(), ids[0], "main"), treeOf(t, h.store(), ids[1], "main")
	if len(a) != 2 || len(a) != len(b) {
		t.Fatalf("trees: %v / %v", paths(a), paths(b))
	}
	for p, e := range a {
		f := b[p]
		if e.Dirty || e.Unpushed || e.SHA256 != f.SHA256 || e.GitOID != f.GitOID || e.Dirty != f.Dirty || e.Unpushed != f.Unpushed {
			t.Fatalf("%s: %+v vs %+v", p, e, f)
		}
	}
	wts, _ := h.store().Worktrees(context.Background())
	if len(wts) != 2 {
		t.Fatalf("worktrees = %+v", wts)
	}
	for _, w := range wts {
		if w.DefaultBranch != "main" {
			t.Fatalf("default branch = %q", w.DefaultBranch)
		}
	}
}

func TestSessionsAndRemove(t *testing.T) {
	h := newHarness(t, map[string]string{"main.go": "package main\n"}, codeapi.Options{})
	h.run(func(r *codesync.Request) { r.Touch = codesync.TouchStart })
	wts, _ := h.store().Worktrees(context.Background())
	if len(wts) != 1 || len(wts[0].Sessions) != 1 || wts[0].Sessions[0].ID != sessID || wts[0].Sessions[0].Ended {
		t.Fatalf("after start: %+v", wts)
	}
	h.run(func(r *codesync.Request) { r.Touch = codesync.TouchEnd })
	wts, _ = h.store().Worktrees(context.Background())
	if !wts[0].Sessions[0].Ended {
		t.Fatalf("after end: %+v", wts[0].Sessions)
	}

	if err := codesync.Remove(context.Background(), h.request(nil), h.root); err != nil {
		t.Fatal(err)
	}
	wts, _ = h.store().Worktrees(context.Background())
	if !wts[0].Removed {
		t.Fatalf("worktree not removed: %+v", wts[0])
	}
	if _, err := os.Stat(filepath.Join(h.stateDir, h.id()+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("state kept: %v", err)
	}
	// A worktree the state does not know is a no-op.
	if err := codesync.Remove(context.Background(), h.request(nil), filepath.Join(h.base, "gone")); err != nil {
		t.Fatal(err)
	}
}

// gitHubBacked is a code server whose project is GitHub-backed while on
// holds.
func gitHubBacked(on *atomic.Bool) codeapi.Options {
	return codeapi.Options{GitHubBacked: func(context.Context, string) bool { return on.Load() }}
}

// wantOmitted checks that rel is in the tree without content: clean,
// pushed, with the blob id of rev:rel and its size.
func (h *harness) wantOmitted(step, rel, rev string, size int64) {
	h.t.Helper()
	e, ok := h.tree("main")[rel]
	if !ok || e.SHA256 != "" || e.Dirty || e.Unpushed || e.Size != size || e.GitOID != h.oid(rev+":"+rel) {
		h.t.Fatalf("%s: %s = %+v (present %v), want clean, pushed, size %d, git_oid %s and no content",
			step, rel, e, ok, size, h.oid(rev+":"+rel))
	}
}

func TestGitHubBackedProjectSkipsCleanPushedContent(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	h := newHarness(t, map[string]string{"main.go": "package main\n", "lib/lib.go": "package lib\n"}, gitHubBacked(&on))
	git(t, h.root, "push", "-q", "origin", "main:old")

	// The first reconcile reads nothing: GitHub has every file.
	rep := h.run(nil)
	if !rep.GitHub || !rep.FullCommitted || rep.Omitted != 2 || rep.Hashed != 0 || rep.Uploaded != 0 || rep.Upserts != 2 {
		t.Fatalf("first run = %+v", rep)
	}
	h.wantOmitted("first run", "main.go", "HEAD", 13)
	h.wantOmitted("first run", "lib/lib.go", "HEAD", 12)
	if !h.state().GitHub {
		t.Fatal("the state does not remember the capability")
	}
	for _, c := range []string{"package main\n", "package lib\n"} {
		if _, err := h.store().Blob(context.Background(), shaOf(c)); err == nil {
			t.Fatalf("the blob of %q was uploaded", c)
		}
	}

	// An edit is an overlay: uploaded as before.
	v2 := "package main // v2\n"
	h.write("main.go", v2)
	rep = h.run(func(r *codesync.Request) { r.Paths = []string{"main.go"} })
	if rep.Hashed != 1 || rep.Uploaded != 1 || rep.Omitted != 0 {
		t.Fatalf("edit = %+v", rep)
	}
	if e := h.tree("main")["main.go"]; e.SHA256 != shaOf(v2) || !e.Dirty || blobContent(t, h.store(), e.SHA256) != v2 {
		t.Fatalf("edited entry = %+v", e)
	}

	// Committed but not pushed: still an overlay, the content is kept.
	git(t, h.root, "commit", "-q", "-am", "v2")
	rep = h.run(func(r *codesync.Request) { r.Status = true })
	if e := h.tree("main")["main.go"]; e.SHA256 != shaOf(v2) || e.Dirty || !e.Unpushed || rep.Uploaded != 0 {
		t.Fatalf("after commit: %+v, report %+v", e, rep)
	}

	// Pushed: GitHub has it now, so it goes without content.
	git(t, h.root, "push", "-q")
	rep = h.run(func(r *codesync.Request) { r.Status = true })
	if rep.Uploaded != 0 || rep.Hashed != 0 {
		t.Fatalf("after push = %+v", rep)
	}
	h.wantOmitted("after push", "main.go", "HEAD", int64(len(v2)))

	// Files another machine pushed arrive with a pull, unread.
	git(t, h.base, "clone", "-q", h.bare, "other")
	other := filepath.Join(h.base, "other")
	h.writeIn(other, "pulled.go", "package main // pulled\n")
	git(t, other, "add", "pulled.go")
	git(t, other, "commit", "-q", "-m", "pulled")
	git(t, other, "push", "-q", "origin", "main")
	git(t, h.root, "pull", "-q", "--ff-only")
	rep = h.run(nil)
	if rep.Hashed != 0 || rep.Uploaded != 0 || rep.Upserts == 0 {
		t.Fatalf("after pull = %+v", rep)
	}
	h.wantOmitted("after pull", "pulled.go", "HEAD", int64(len("package main // pulled\n")))

	// An entry GitHub supplied that turns unpushed without its file
	// changing (another upstream) is read and uploaded.
	git(t, h.root, "branch", "-q", "--set-upstream-to=origin/old")
	rep = h.run(nil)
	tree := h.tree("main")
	if e := tree["main.go"]; e.SHA256 != shaOf(v2) || !e.Unpushed {
		t.Fatalf("main.go unpushed against old = %+v", e)
	}
	if e := tree["pulled.go"]; e.SHA256 != shaOf("package main // pulled\n") || !e.Unpushed ||
		blobContent(t, h.store(), e.SHA256) != "package main // pulled\n" {
		t.Fatalf("pulled.go unpushed against old = %+v", e)
	}
	h.wantOmitted("upstream old", "lib/lib.go", "HEAD", 12)
	if rep.Uploaded != 1 || rep.Hashed != 2 {
		t.Fatalf("upstream old = %+v", rep)
	}
}

func TestUploadsResumeWhenTheProjectStopsBeingGitHubBacked(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	files := map[string]string{"main.go": "package main\n", "lib/lib.go": "package lib\n"}
	h := newHarness(t, files, gitHubBacked(&on))
	if rep := h.run(nil); rep.Omitted != 2 || rep.Uploaded != 0 {
		t.Fatalf("GitHub-backed run = %+v", rep)
	}

	// Uninstalled: an incremental run does not ask, the next full
	// reconcile does.
	on.Store(false)
	h.write("new.go", "package main // new\n")
	rep := h.run(func(r *codesync.Request) { r.Paths = []string{"new.go"} })
	if !rep.GitHub || rep.Full || rep.Uploaded != 1 {
		t.Fatalf("incremental run = %+v", rep)
	}
	h.wantOmitted("incremental run", "main.go", "HEAD", 13)

	h.now = h.now.Add(codesync.FullAfter + time.Minute)
	rep = h.run(nil)
	if rep.GitHub || !rep.FullCommitted || rep.Omitted != 0 || rep.Hashed != 2 || rep.Uploaded != 2 {
		t.Fatalf("full reconcile after uninstall = %+v", rep)
	}
	tree := h.tree("main")
	for p, c := range files {
		if e := tree[p]; e.SHA256 != shaOf(c) || blobContent(t, h.store(), e.SHA256) != c {
			t.Fatalf("%s = %+v, want its content", p, e)
		}
	}
	if h.state().GitHub {
		t.Fatal("the state still says GitHub-backed")
	}
	if rep := h.run(func(r *codesync.Request) { r.Full = true }); rep.Hashed != 0 || rep.Uploaded != 0 || rep.Upserts != 3 {
		t.Fatalf("a later full reconcile = %+v", rep)
	}

	// Installed again: the next full reconcile drops the content of what
	// GitHub has.
	on.Store(true)
	rep = h.run(func(r *codesync.Request) { r.Full = true })
	if !rep.GitHub || rep.Omitted != 2 || rep.Hashed != 0 || rep.Uploaded != 0 {
		t.Fatalf("reinstalled = %+v", rep)
	}
	h.wantOmitted("reinstalled", "main.go", "HEAD", 13)
	if e := h.tree("main")["new.go"]; e.SHA256 != shaOf("package main // new\n") || !e.Dirty {
		t.Fatalf("the untracked file lost its content: %+v", e)
	}
}
