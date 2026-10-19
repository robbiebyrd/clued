package cli

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/code/mongocode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/falkor"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
)

const graphTestProject = "u0123456789abcdef__github.com__o__r"

// seedCode records a main branch with two files and an overlay with an
// edit in store.
func seedCode(t *testing.T, store code.Store) {
	t.Helper()
	ctx := context.Background()
	hex40 := func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	entry := func(path, content string, dirty bool) code.Entry {
		h := sha256.Sum256([]byte(content))
		sha := hex.EncodeToString(h[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(content)), Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		e := code.Entry{Path: path, SHA256: sha, Size: int64(len(content)), Mode: "100644", Lang: "go", GitOID: hex40(content), Dirty: dirty}
		if dirty {
			e.GitOID = hex40("head " + path)
		}
		return e
	}
	m, x := "wt_0000000000000001", "wt_0000000000000002"
	for _, id := range []string{m, x} {
		if err := store.PutWorktree(ctx, code.Worktree{ID: id, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	u := hex40("commit main")
	for _, ch := range []code.Change{
		{Worktree: m, Ref: "main", Head: u, Upstream: u, Full: true,
			Upserts: []code.Entry{entry("a.go", "package p\n\nfunc A() {}\n", false), entry("b.go", "package p\n\nfunc B() {}\n", false)}},
		{Worktree: x, Ref: "main", Head: u, Upstream: u, Full: true, SessionID: "s1",
			Upserts: []code.Entry{entry("a.go", "package p\n\nfunc A2() {}\n", true), entry("b.go", "package p\n\nfunc B() {}\n", false)}},
	} {
		if _, err := store.Commit(ctx, ch); err != nil {
			t.Fatal(err)
		}
	}
}

// memGraphEnv replaces openGraphEnv with one memory environment shared by
// every command of the test, and returns it.
func memGraphEnv(t *testing.T) *graphEnv {
	t.Helper()
	store := memcode.New()
	seedCode(t, store)
	env := &graphEnv{
		Code: func(_ context.Context, p string) (code.Store, error) {
			if p != graphTestProject {
				return memcode.New(), nil
			}
			return store, nil
		},
		Graphs:  memgraph.New(),
		Cursors: codeindex.NewMemCursors(nil),
	}
	prev := openGraphEnv
	openGraphEnv = func(context.Context, graphCmdOptions) (*graphEnv, error) { return env, nil }
	t.Cleanup(func() { openGraphEnv = prev })
	return env
}

func graphResults(t *testing.T, r run) []map[string]any {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("unexpected failure: %+v", r)
	}
	var env struct {
		OK     bool             `json:"ok"`
		Result []map[string]any `json:"result"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &env); err != nil || !env.OK {
		t.Fatalf("decode %q: %v", r.stdout, err)
	}
	return env.Result
}

func TestServerGraphRebuildAndStatus(t *testing.T) {
	env := memGraphEnv(t)

	// Never indexed: two changes pending, no layer.
	st := graphResults(t, runServer(t, "graph", "status", graphTestProject))
	if len(st) != 1 || st[0]["project"] != graphTestProject || st[0]["pending"] != 2.0 || st[0]["seq"] != 0.0 {
		t.Fatalf("status before indexing = %v", st)
	}
	if layers, _ := st[0]["layers"].([]any); len(layers) != 0 {
		t.Errorf("layers before indexing = %v", layers)
	}

	res := graphResults(t, runServer(t, "graph", "rebuild", graphTestProject))
	if len(res) != 1 || res[0]["seq"] != 2.0 || res[0]["changes"] != 2.0 || res[0]["layers"] != 3.0 {
		t.Fatalf("rebuild = %v", res)
	}
	g, err := env.Graphs.Open(context.Background(), graphTestProject)
	if err != nil {
		t.Fatal(err)
	}
	if files, err := g.Files(context.Background(), codegraph.BaseLayerID); err != nil || len(files) != 2 {
		t.Fatalf("base files after rebuild = %+v, %v", files, err)
	}

	st = graphResults(t, runServer(t, "graph", "status", graphTestProject, "--check"))
	check, _ := st[0]["check"].(map[string]any)
	if st[0]["seq"] != 2.0 || st[0]["pending"] != 0.0 || check["consistent"] != true {
		t.Errorf("status --check after rebuild = %v", st)
	}
	layers, _ := st[0]["layers"].([]any)
	if len(layers) != 3 {
		t.Errorf("layers after rebuild = %v", layers)
	}

	// Drift is reported, and repaired with --repair.
	if err := g.RemoveFile(context.Background(), "base", "b.go"); err != nil {
		t.Fatal(err)
	}
	st = graphResults(t, runServer(t, "graph", "status", graphTestProject, "--check"))
	if check, _ := st[0]["check"].(map[string]any); check["consistent"] != false {
		t.Errorf("status --check after drift = %v", st)
	}
	st = graphResults(t, runServer(t, "graph", "status", graphTestProject, "--check", "--repair"))
	if check, _ := st[0]["check"].(map[string]any); check["repaired"] != true {
		t.Errorf("status --check --repair = %v", st)
	}
	st = graphResults(t, runServer(t, "graph", "status", graphTestProject, "--check"))
	if check, _ := st[0]["check"].(map[string]any); check["consistent"] != true {
		t.Errorf("status --check after repair = %v", st)
	}

	// A project another worker holds: the lease is reported, and rebuild
	// gives up after --wait with a conflict.
	if _, _, err := env.Cursors.Acquire(context.Background(), graphTestProject, "server-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	st = graphResults(t, runServer(t, "graph", "status", graphTestProject))
	if st[0]["holder"] != "server-1" {
		t.Errorf("status while leased = %v", st)
	}
	prevRetry := graphRetryEvery
	graphRetryEvery = 5 * time.Millisecond
	t.Cleanup(func() { graphRetryEvery = prevRetry })
	r := runServer(t, "graph", "rebuild", graphTestProject, "--wait", "30ms")
	if r.code != 9 || !strings.Contains(r.stderr, "another worker") {
		t.Errorf("rebuild while leased = %+v", r)
	}
}

func TestServerGraphRefusals(t *testing.T) {
	memGraphEnv(t)
	if r := runServer(t, "graph", "rebuild"); r.code == 0 {
		t.Errorf("rebuild without a project succeeded: %+v", r)
	}
	for _, c := range []struct {
		args []string
		code int
		want string
	}{
		{[]string{"graph", "rebuild", "../etc"}, 2, "invalid"},
		{[]string{"graph", "status", "a b"}, 2, "invalid"},
		{[]string{"graph", "status", graphTestProject, "--repair"}, 2, "--repair needs --check"},
	} {
		r := runServer(t, c.args...)
		if r.code != c.code || !strings.Contains(r.stderr, c.want) {
			t.Errorf("%v = %+v, want exit %d and %q", c.args, r, c.code, c.want)
		}
	}
}

func TestServerGraphOptions(t *testing.T) {
	var got graphCmdOptions
	prev := openGraphEnv
	openGraphEnv = func(_ context.Context, o graphCmdOptions) (*graphEnv, error) {
		got = o
		return prev(context.Background(), o)
	}
	t.Cleanup(func() { openGraphEnv = prev })
	t.Setenv(serverGraphURLEnv, "")
	t.Setenv(envServerCodeStore, "")

	r := runServer(t, "graph", "status", graphTestProject)
	if r.code != 2 || !strings.Contains(r.stderr, "--graph is required") {
		t.Errorf("no graph URL: %+v", r)
	}
	if got.CodeStore != codeStoreMongo {
		t.Errorf("default code store = %q", got.CodeStore)
	}
	t.Setenv(serverGraphURLEnv, "redis://127.0.0.1:1")
	t.Setenv(envServerCodeStore, "memory")
	r = runServer(t, "graph", "status", graphTestProject)
	if r.code != 2 || !strings.Contains(r.stderr, "only mongo") || got.GraphURL != "redis://127.0.0.1:1" {
		t.Errorf("memory code store: %+v (%+v)", r, got)
	}
	r = runServer(t, "graph", "status", graphTestProject, "--code-store", "mongo", "--graph", "http://x")
	if r.code != 2 || !strings.Contains(r.stderr, "scheme") || got.GraphURL != "http://x" {
		t.Errorf("bad graph URL: %+v (%+v)", r, got)
	}
}

// TestServerGraphOnMongoAndFalkorDB runs the commands through the real
// environment: the code store and cursors in MongoDB, the graph in
// FalkorDB, as beside a running server.
func TestServerGraphOnMongoAndFalkorDB(t *testing.T) {
	mongoURI, graphURL := os.Getenv("MIND_PALACE_TEST_MONGODB_URI"), os.Getenv("MIND_PALACE_TEST_FALKORDB_URL")
	if mongoURI == "" || graphURL == "" {
		t.Skip("set MIND_PALACE_TEST_MONGODB_URI and MIND_PALACE_TEST_FALKORDB_URL to run the command on MongoDB and FalkorDB")
	}
	ctx := context.Background()
	dbName := fmt.Sprintf("clued_server_graph_test_%d", time.Now().UnixNano())
	client, err := connectCodeMongo(ctx, mongoURI)
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database(dbName)
	t.Cleanup(func() {
		_ = db.Drop(context.Background())
		_ = client.Disconnect(context.Background())
	})
	project := fmt.Sprintf("u0123456789abcdef__graph-cli-%d", time.Now().UnixNano())
	store, err := mongocode.Open(ctx, db, mongocode.ProjectPrefix("", project))
	if err != nil {
		t.Fatal(err)
	}
	seedCode(t, store)
	graphs, err := falkor.New(graphURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if g, err := graphs.Open(context.Background(), project); err == nil {
			_ = g.Drop(context.Background())
		}
		_ = graphs.Close()
	})

	cfg := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf(`{"mongoUrl": %q, "dbName": %q}`, mongoURI, dbName)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLUED_MONGO_URL", "")
	t.Setenv("CLUED_DB_NAME", "")
	args := []string{"--session-config", cfg, "--graph", graphURL, "--code-store", "mongo"}
	res := graphResults(t, runServer(t, append([]string{"graph", "rebuild", project}, args...)...))
	if len(res) != 1 || res[0]["seq"] != 2.0 || res[0]["layers"] != 3.0 {
		t.Fatalf("rebuild = %v", res)
	}
	st := graphResults(t, runServer(t, append([]string{"graph", "status", project, "--check"}, args...)...))
	check, _ := st[0]["check"].(map[string]any)
	if st[0]["seq"] != 2.0 || st[0]["pending"] != 0.0 || check["consistent"] != true {
		t.Errorf("status --check = %v", st)
	}
	if layers, _ := st[0]["layers"].([]any); len(layers) != 3 {
		t.Errorf("layers = %v", st[0]["layers"])
	}
}
