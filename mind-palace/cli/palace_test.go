package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
	_ "github.com/robbiebyrd/clued/mind-palace/store/remotestore"
)

const remoteTestToken = "cli-test-token"

// remoteStub resolves remoteTestToken to a principal allowed everywhere.
type remoteStub struct{}

func (remoteStub) Lookup(raw string) (auth.Principal, bool) {
	if raw != remoteTestToken {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: "test", AccountID: "acct", Projects: []string{"*"}}, true
}

// remoteServer is storeapi behind auth.Middleware with one memstore per
// project, so a test can look at what the client sent for each project.
type remoteServer struct {
	*httptest.Server
	mu     sync.Mutex
	stores map[string]*memstore.Store
}

func (s *remoteServer) open(_ context.Context, project string) (store.Store, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.stores[project]
	if !ok {
		st = memstore.New(project)
		s.stores[project] = st
	}
	return st, nil
}

// projects lists the projects the server has opened a store for.
func (s *remoteServer) projects() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for p := range s.stores {
		out = append(out, p)
	}
	return out
}

// planIDs returns the ids of the plans stored under project.
func (s *remoteServer) planIDs(t *testing.T, project string) []string {
	t.Helper()
	s.mu.Lock()
	st, ok := s.stores[project]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	docs, err := st.List(context.Background(), kind.Plan)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, d := range docs {
		ids = append(ids, d.ID())
	}
	return ids
}

func newRemoteServer(t *testing.T) *remoteServer {
	t.Helper()
	s := &remoteServer{stores: map[string]*memstore.Store{}}
	h := storeapi.Handler(storeapi.Options{
		Open: s.open,
		Allow: func(r *http.Request, project string) bool {
			p, ok := auth.FromContext(r.Context())
			return ok && auth.AllowProject(p, project)
		},
	})
	s.Server = httptest.NewServer(auth.Middleware(remoteStub{})(h))
	t.Cleanup(s.Close)
	return s
}

// remoteMode points the plugin's session config at url through the
// environment and keeps the real config.json out of the test.
func remoteMode(t *testing.T, url string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLUED_SERVER_URL", url)
	t.Setenv("CLUED_SERVER_TOKEN", remoteTestToken)
	t.Setenv("CLUED_SERVER_CA_FILE", "")
	t.Setenv("CLUED_SERVER_INSECURE", "")
	t.Setenv("MIND_PALACE_CONFIG", "")
}

// gitRepo makes a repository at dir with origin set to origin and changes
// into it for the rest of the test; the test is skipped without git.
func gitRepo(t *testing.T, dir, origin string) {
	t.Helper()
	if _, err := osexec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, args := range [][]string{{"init", "-q"}, {"remote", "add", "origin", origin}} {
		cmd := osexec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	t.Chdir(dir)
}

const inlinePlan = `{"frontMatter":{"title":"Remote plan","type":"drft","priority":"1"},"body":{"summary":{"goal":"g","problem":"p"},"design":{}}}`

// planFiles lists the Markdown files directly under dir.
func planFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestRemoteModeAppendsCluedServerAfterTheFileStore(t *testing.T) {
	srv := newRemoteServer(t)
	remoteMode(t, srv.URL)
	repo := t.TempDir()
	gitRepo(t, repo, "git@github.com:o/r.git")
	plans := filepath.Join(repo, "docs", "plans")

	r := exec(t, plans, "stores")
	if r.code != 0 {
		t.Fatalf("stores: %+v", r)
	}
	var stores struct {
		Result struct {
			Stores []string `json:"stores"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &stores); err != nil {
		t.Fatalf("stores output: %q (%v)", r.stdout, err)
	}
	if got := strings.Join(stores.Result.Stores, ","); got != "file (file),clued-server (clued)" {
		t.Errorf("stores = %q, want the file store first and clued-server second", got)
	}

	r = exec(t, plans, "config")
	if r.code != 0 {
		t.Fatalf("config: %+v", r)
	}
	if !strings.Contains(r.stdout, "clued-server") {
		t.Errorf("config does not list clued-server: %s", r.stdout)
	}
	if strings.Contains(r.stdout, remoteTestToken) {
		t.Errorf("config reveals the server token: %s", r.stdout)
	}
	var cfg struct {
		Result struct {
			Config struct {
				Storage []struct {
					Name    string            `json:"name"`
					Kind    string            `json:"kind"`
					Options map[string]string `json:"options"`
				} `json:"storage"`
			} `json:"config"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &cfg); err != nil {
		t.Fatalf("config output: %q (%v)", r.stdout, err)
	}
	if n := len(cfg.Result.Config.Storage); n != 2 {
		t.Fatalf("storage entries = %d, want 2: %s", n, r.stdout)
	}
	remote := cfg.Result.Config.Storage[1]
	if remote.Name != "clued-server" || remote.Kind != "clued" || remote.Options["url"] != srv.URL || remote.Options["project"] != "github.com__o__r" {
		t.Errorf("remote storage def = %+v", remote)
	}

	r = exec(t, plans, "plan", "create", "--input", inlinePlan)
	if r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	if strings.Contains(r.stdout, `"warning"`) || strings.Contains(r.stderr, "warning") {
		t.Errorf("create warned although the server is up: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if files := planFiles(t, plans); len(files) != 1 || !strings.HasPrefix(files[0], id) {
		t.Errorf("plan file under docs/plans = %v, want one file for %s", files, id)
	}
	if ids := srv.planIDs(t, "github.com__o__r"); len(ids) != 1 || ids[0] != id {
		t.Errorf("plans on the server under github.com__o__r = %v, want [%s] (projects seen: %v)", ids, id, srv.projects())
	}
}

func TestRemoteProjectOverrideWins(t *testing.T) {
	srv := newRemoteServer(t)
	remoteMode(t, srv.URL)
	repo := t.TempDir()
	gitRepo(t, repo, "git@github.com:o/r.git")
	if err := os.WriteFile(filepath.Join(repo, "mind-palace.config.yaml"), []byte("remote:\n  project: team__thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plans := filepath.Join(repo, "docs", "plans")

	r := exec(t, plans, "plan", "create", "--input", inlinePlan)
	if r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if ids := srv.planIDs(t, "team__thing"); len(ids) != 1 || ids[0] != id {
		t.Errorf("plans under the overriding project = %v, want [%s] (projects seen: %v)", ids, id, srv.projects())
	}
	if ids := srv.planIDs(t, "github.com__o__r"); len(ids) != 0 {
		t.Errorf("the origin's project received %v although remote.project overrides it", ids)
	}
	r = exec(t, plans, "config")
	if !strings.Contains(r.stdout, `"project": "team__thing"`) {
		t.Errorf("config does not show the overriding project: %s", r.stdout)
	}
}

func TestRemoteProjectFallsBackToDirectoryName(t *testing.T) {
	srv := newRemoteServer(t)
	remoteMode(t, srv.URL)
	repo := filepath.Join(t.TempDir(), "Scratch Pad")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)
	plans := filepath.Join(repo, "docs", "plans")

	r := exec(t, plans, "plan", "create", "--input", inlinePlan)
	if r.code != 0 {
		t.Fatalf("create: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if ids := srv.planIDs(t, "scratch-pad"); len(ids) != 1 || ids[0] != id {
		t.Errorf("plans under the directory-name project = %v, want [%s] (projects seen: %v)", ids, id, srv.projects())
	}
	if !strings.Contains(r.stderr, "derived from directory name") {
		t.Errorf("stderr does not say the project was derived from the directory name: %q", r.stderr)
	}
}

func TestRemoteStatusReviewNotesProjectFromDirectoryName(t *testing.T) {
	srv := newRemoteServer(t)
	remoteMode(t, srv.URL)
	root := finishedStoryDir(t)

	in := `{"hook_event_name":"UserPromptSubmit","cwd":` + strconv.Quote(root) + `}`
	r := execStdin(t, in, "hook", "status-review", "--state-dir", t.TempDir())
	if r.code != 0 {
		t.Fatalf("got %+v", r)
	}
	var out struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("stdout is not hook JSON: %q (%v)", r.stdout, err)
	}
	ctx := out.HookSpecificOutput.AdditionalContext
	if !strings.Contains(ctx, "Story 0001-abc") || !strings.Contains(ctx, "project derived from directory name") {
		t.Errorf("context = %q, want the finished story and the project note", ctx)
	}

	// `mind-palace review` derives the project from its working directory.
	t.Chdir(root)
	r = execStdin(t, "", "--plans-dir", filepath.Join(root, "docs", "plans"), "--stories-dir", filepath.Join(root, "docs", "stories"), "review")
	if r.code != 0 {
		t.Fatalf("review: %+v", r)
	}
	notes, _ := result(t, r)["notes"].([]any)
	if len(notes) != 1 || !strings.Contains(notes[0].(string), "project derived from directory name") {
		t.Errorf("review notes = %v", notes)
	}
}

func TestRemoteServerDownKeepsWritingLocallyAndSyncRepairs(t *testing.T) {
	srv := newRemoteServer(t)
	url := srv.URL
	srv.Close() // the server is down: the port refuses connections
	remoteMode(t, url)
	repo := t.TempDir()
	gitRepo(t, repo, "https://github.com/o/r.git")
	plans := filepath.Join(repo, "docs", "plans")

	r := exec(t, plans, "plan", "create", "--input", inlinePlan)
	if r.code != 0 {
		t.Fatalf("create with the server down must still succeed: %+v", r)
	}
	id := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if files := planFiles(t, plans); len(files) != 1 || !strings.HasPrefix(files[0], id) {
		t.Errorf("plan file under docs/plans = %v, want one file for %s", files, id)
	}
	var env struct {
		Warning struct {
			Message string `json:"message"`
		} `json:"warning"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(env.Warning.Message, "clued-server") || !strings.Contains(env.Warning.Message, "mind-palace sync") {
		t.Errorf("warning = %q, want it to name clued-server and mind-palace sync", env.Warning.Message)
	}
	if !strings.Contains(r.stderr, "clued-server") {
		t.Errorf("stderr does not mention clued-server: %q", r.stderr)
	}

	// The server comes back on the same address.
	back := &remoteServer{stores: map[string]*memstore.Store{}}
	h := storeapi.Handler(storeapi.Options{Open: back.open})
	back.Server = httptest.NewUnstartedServer(auth.Middleware(remoteStub{})(h))
	l, err := listenOn(url)
	if err != nil {
		t.Skipf("cannot reuse the address: %v", err)
	}
	back.Listener = l
	back.Start()
	t.Cleanup(back.Close)

	r = exec(t, plans, "sync", "--from", "file", "--to", "clued-server")
	if r.code != 0 {
		t.Fatalf("sync: %+v", r)
	}
	if !strings.Contains(r.stdout, `"copied"`) {
		t.Errorf("sync output: %s", r.stdout)
	}
	if ids := back.planIDs(t, "github.com__o__r"); len(ids) != 1 || ids[0] != id {
		t.Errorf("plans on the server after sync = %v, want [%s]", ids, id)
	}
}

func TestWithoutServerURLNothingIsAppended(t *testing.T) {
	remoteMode(t, "")
	plans := filepath.Join(t.TempDir(), "docs", "plans")
	r := exec(t, plans, "stores")
	if r.code != 0 {
		t.Fatalf("stores: %+v", r)
	}
	if strings.Contains(r.stdout, "clued-server") {
		t.Errorf("clued-server listed without a server URL: %s", r.stdout)
	}
}

// listenOn opens a listener on the host:port of a server URL.
func listenOn(rawURL string) (net.Listener, error) {
	u, err := neturl.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	return net.Listen("tcp", u.Host)
}
