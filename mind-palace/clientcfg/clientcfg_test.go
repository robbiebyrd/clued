package clientcfg

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/store"
	_ "github.com/robbiebyrd/clued/mind-palace/store/filestore"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

var ctx = context.Background()

func gitRepo(t *testing.T, origin string) string {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	args := [][]string{{"init", "-q"}}
	if origin != "" {
		args = append(args, []string{"remote", "add", "origin", origin})
	}
	for _, a := range args {
		if out, err := osexec.Command("git", append([]string{"-C", dir}, a...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", a, err, out)
		}
	}
	return dir
}

func TestResolveProjectFromOrigin(t *testing.T) {
	dir := gitRepo(t, "git@github.com:Owner/Repo.git")
	p := ResolveProject(ctx, dir, "")
	if p.Slug != "github.com__owner__repo" || p.Source != ProjectFromOrigin {
		t.Errorf("project = %+v", p)
	}
	if p.Note() != "" {
		t.Errorf("a project from the origin needs no note, got %q", p.Note())
	}
}

func TestResolveProjectOverrideWins(t *testing.T) {
	dir := gitRepo(t, "git@github.com:o/r.git")
	p := ResolveProject(ctx, dir, "Team/Thing")
	if p.Slug != "team__thing" || p.Source != ProjectFromConfig {
		t.Errorf("project = %+v", p)
	}
}

func TestResolveProjectFallsBackToDirectoryName(t *testing.T) {
	for _, dir := range []string{gitRepo(t, ""), t.TempDir()} {
		dir = filepath.Join(dir, "Scratch Pad")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		p := ResolveProject(ctx, dir, "")
		if p.Slug != "scratch-pad" || p.Source != ProjectFromDirectory {
			t.Errorf("project for %s = %+v", dir, p)
		}
		if !strings.Contains(p.Note(), "project derived from directory name") || !strings.Contains(p.Note(), "scratch-pad") {
			t.Errorf("note = %q", p.Note())
		}
	}
}

func TestRemoteStorageDef(t *testing.T) {
	if _, ok := RemoteStorageDef(session.Config{}, "p"); ok {
		t.Error("a definition without a server URL")
	}
	def, ok := RemoteStorageDef(session.Config{ServerURL: "https://clued.example/", ServerToken: "tok", ServerCAFile: "/ca.pem", ServerInsecure: true}, "github.com__o__r")
	if !ok {
		t.Fatal("no definition in remote mode")
	}
	if def.Name != RemoteStoreName || def.Kind != "clued" || !def.IsEnabled() {
		t.Errorf("def = %+v", def)
	}
	want := map[string]string{"url": "https://clued.example/", "token": "tok", "project": "github.com__o__r", "caFile": "/ca.pem", "insecure": "true"}
	for k, v := range want {
		if def.Options[k] != v {
			t.Errorf("option %s = %q, want %q", k, def.Options[k], v)
		}
	}
	def, _ = RemoteStorageDef(session.Config{ServerURL: "https://clued.example", ServerToken: "tok"}, "p")
	for _, k := range []string{"caFile", "insecure"} {
		if _, set := def.Options[k]; set {
			t.Errorf("option %s set although empty", k)
		}
	}
	public := Redact(def)
	if _, set := public.Options["token"]; set {
		t.Error("Redact kept the token")
	}
	if def.Options["token"] != "tok" {
		t.Error("Redact changed the original")
	}
}

func TestOpenWithoutRemoteModeIsUnchanged(t *testing.T) {
	cfg := config.Default()
	root := t.TempDir()
	cfg.Plans.Dir = filepath.Join(root, "docs", "plans")
	cfg.Stories.Dir = filepath.Join(root, "docs", "stories")
	o, err := Open(ctx, root, cfg, session.Config{}, Options{Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Remote != nil || len(o.Store.Stores()) != 1 || len(cfg.Storage) != 1 {
		t.Errorf("remote = %v, stores = %d, defs = %d", o.Remote, len(o.Store.Stores()), len(cfg.Storage))
	}
}

func TestOpenRejectsAnInvalidServerURL(t *testing.T) {
	cfg := config.Default()
	root := t.TempDir()
	cfg.Plans.Dir = filepath.Join(root, "docs", "plans")
	cfg.Stories.Dir = filepath.Join(root, "docs", "stories")
	_, err := Open(ctx, root, cfg, session.Config{ServerURL: "http://clued.example", ServerToken: "tok"}, Options{})
	if err == nil || !strings.Contains(err.Error(), RemoteStoreName) {
		t.Errorf("err = %v, want one naming %s", err, RemoteStoreName)
	}
	// The config must not keep a half-added definition.
	if len(cfg.Storage) != 1 {
		t.Errorf("storage definitions after a failed open = %+v", cfg.Storage)
	}
}

// sharedServer is a clued cloud stand-in: storeapi over one memory store
// per project, counting lock requests, and GET /api/v1/me/workspaces
// binding team__thing to a workspace.
type sharedServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	st    map[string]*memstore.Store
	locks atomic.Int32
}

func newSharedServer(t *testing.T, workspaces string) *sharedServer {
	t.Helper()
	s := &sharedServer{st: map[string]*memstore.Store{}}
	api := storeapi.Handler(storeapi.Options{Open: func(_ context.Context, project string) (store.Store, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.st[project] == nil {
			s.st[project] = memstore.New(project)
		}
		return s.st[project], nil
	}})
	mux := http.NewServeMux()
	mux.Handle(storeapi.Prefix+"/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/lock") {
			s.locks.Add(1)
		}
		api.ServeHTTP(w, r)
	}))
	if workspaces != "" {
		mux.HandleFunc("GET "+MeWorkspacesPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true,"result":` + workspaces + `}`))
		})
	}
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func openClient(t *testing.T, s *sharedServer, project string) *Opened {
	t.Helper()
	cfg := config.Default()
	root := t.TempDir()
	cfg.Plans.Dir = filepath.Join(root, "docs", "plans")
	cfg.Stories.Dir = filepath.Join(root, "docs", "stories")
	cfg.Remote.Project = project
	o, err := Open(ctx, root, cfg, session.Config{ServerURL: s.srv.URL, ServerToken: "tok"}, Options{Probe: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func storyInput(t *testing.T) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile("../testdata/create-story.json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSharedProjectIsRemotePrimary(t *testing.T) {
	s := newSharedServer(t, `{"workspaces":[{"id":"w1","name":"Team","role":"member","projects":["team__thing"]},{"id":"w2","role":"owner","projects":[{"slug":"other__x"}]}]}`)
	a, b := openClient(t, s, "team/thing"), openClient(t, s, "team/thing")
	if a.Remote == nil || !a.Remote.Shared || a.Remote.Workspace != "Team" || a.Remote.SharedErr != nil {
		t.Fatalf("remote = %+v, want a shared project of Team", a.Remote)
	}
	if a.Store.Primary().Name() != RemoteStoreName || len(a.Store.Stores()) != 2 {
		t.Fatalf("primary = %s of %d stores, want %s first", a.Store.Primary().Name(), len(a.Store.Stores()), RemoteStoreName)
	}
	if a.Palace.Config().Storage[0].Name != RemoteStoreName {
		t.Errorf("effective storage does not list the server first: %+v", a.Palace.Config().Storage)
	}

	// A's write is read by B from the server, not from B's empty files.
	created, err := a.Palace.Stories().Create(ctx, storyInput(t), "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.Palace.Stories().Get(ctx, created.ID())
	if err != nil || got.FrontMatter.Title != created.FrontMatter.Title {
		t.Fatalf("B reads %+v, %v", got, err)
	}

	// Two clients claiming the same story serialise on the server's lease:
	// exactly one wins, the other sees the conflict.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, o := range []*Opened{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = o.Palace.Stories().Claim(ctx, created.ID(), fmt.Sprintf("github:member%d", i), false)
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("claims = %v, %v; want exactly one to win", errs[0], errs[1])
	}
	if s.locks.Load() < 2 {
		t.Errorf("lock route called %d times, want a lease per claim", s.locks.Load())
	}
	final, _ := s.st["team__thing"].Get(ctx, "story", created.ID())
	winner := "github:member0"
	if errs[0] != nil {
		winner = "github:member1"
	}
	if final.FrontMatter.Assignee != winner {
		t.Errorf("server copy assignee %q, want the winner %q", final.FrontMatter.Assignee, winner)
	}
}

func TestUnsharedProjectStaysLocalPrimary(t *testing.T) {
	for name, ws := range map[string]string{
		"not bound":       `{"workspaces":[{"id":"w1","projects":["other__x"]}]}`,
		"bare empty list": `[]`,
		"no such route":   "",
	} {
		t.Run(name, func(t *testing.T) {
			o := openClient(t, newSharedServer(t, ws), "team/thing")
			if o.Remote == nil || o.Remote.Shared || o.Remote.SharedErr != nil {
				t.Fatalf("remote = %+v", o.Remote)
			}
			if o.Store.Primary().Name() == RemoteStoreName {
				t.Error("the server is primary for an unshared project")
			}
		})
	}
}
