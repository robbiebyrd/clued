package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/remotesession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

// TestRemoteEndToEnd proves the whole remote loop in one process: the
// clued server (`server run --store memory`) behind a token file, a client
// configured through config.json alone, `hook relay` pushing an event and
// a transcript, the session MCP tools reading them back over the remote
// adapter, `plan create` fanning out to the server, an outage with the
// server stopped, `sync --from file --to clued-server` after the restart,
// and the HTTPS MCP endpoint selected by X-Clued-Project.
func TestRemoteEndToEnd(t *testing.T) {
	clearServerEnv(t)
	clearClientEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	tokens, raw, account := tokenFile(t, "laptop", "*")
	srv := startServer(t, serverRunOptions{Addr: "127.0.0.1:0", Tokens: tokens, Version: "e2e"}, nil)
	serverURL := srv.url("")

	// The client is configured by config.json under HOME, as /clued-setup
	// writes it: the server URL (plain http is allowed on loopback) and the
	// raw token, plus the capture paths under the temp HOME.
	configPath, projectsDir := clientConfig(t, home, serverURL, raw)
	configDir := filepath.Dir(configPath)
	noMongo(t)

	// 1. One PostToolUse through `hook relay`: the event is stored under
	// the token's account and the transcript is pushed line by line.
	tp := transcript(t, projectsDir, "sid-e2e", `{"type":"user","n":1}`, `{"type":"assistant","n":2}`, `{"type":"assistant","n":3}`)
	runRelay(t, configPath, hookEvent("PostToolUse", "sid-e2e", tp))
	if _, err := os.Stat(filepath.Join(configDir, "relay.log")); err == nil {
		t.Errorf("relay.log written although the relay succeeded:\n%s", strings.Join(fileLines(t, filepath.Join(configDir, "relay.log")), "\n"))
	}
	if _, err := os.Stat(filepath.Join(configDir, "catchup", "sid-e2e.json")); err != nil {
		t.Errorf("catch-up state not kept under %s/catchup: %v", configDir, err)
	}

	// 2. The session tools over remotesession see what the relay pushed.
	client, err := remote.New(session.Config{ServerURL: serverURL, ServerToken: raw})
	if err != nil {
		t.Fatal(err)
	}
	store := remotesession.New(client)
	tools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) { return store, nil }, account)
	cs := connectInMemory(t, tools.Server())

	found := mcpList(t, cs, "find_sessions", map[string]any{})
	if len(found) != 1 || found[0]["session_id"] != "sid-e2e" || found[0]["cwd"] != "/work/alpha" {
		t.Fatalf("find_sessions = %v, want sid-e2e in /work/alpha", found)
	}
	full := mcpObject(t, cs, "get_full_session", map[string]any{"session_id": "sid-e2e"})
	if full["account_id"] != account {
		t.Errorf("session account_id = %v, want the token's %q", full["account_id"], account)
	}
	events, _ := full["hook_events"].([]any)
	if len(events) != 1 {
		t.Fatalf("hook_events = %v, want one", events)
	}
	// The full view strips _id, session_id and account_id from each event;
	// the session's account_id above and the account the tools were given
	// are what prove the event was filed under the token's account.
	if ev := events[0].(map[string]any); ev["hook_event_name"] != "PostToolUse" || ev["tool_name"] != "Bash" || ev["transcript_path"] != tp {
		t.Errorf("hook event = %v", ev)
	}
	if lines, _ := full["transcript_lines"].([]any); len(lines) != 3 {
		t.Errorf("transcript_lines = %d, want 3", len(lines))
	}
	page := mcpList(t, cs, "read_transcript", map[string]any{"session_id": "sid-e2e", "offset": 1, "limit": 1})
	if len(page) != 1 || page[0]["line"].(map[string]any)["n"] != float64(2) {
		t.Errorf("read_transcript offset 1 limit 1 = %v, want the line with n = 2", page)
	}

	// 3. `plan create` in a repository with an origin writes the file and
	// fans out to the server under the project derived from that origin.
	repo := t.TempDir()
	gitRepo(t, repo, "git@github.com:acme/widgets.git")
	const project = "github.com__acme__widgets"
	plans := filepath.Join(repo, "docs", "plans")

	r := exec(t, plans, "plan", "create", "--input", inlinePlan)
	if r.code != 0 {
		t.Fatalf("plan create: %+v", r)
	}
	if strings.Contains(r.stdout, `"warning"`) || strings.Contains(r.stderr, "warning") {
		t.Errorf("plan create warned although the server is up: %+v", r)
	}
	first := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
	if files := planFiles(t, plans); len(files) != 1 || !strings.HasPrefix(files[0], first) {
		t.Errorf("docs/plans = %v, want one file for %s", files, first)
	}
	if ids := serverPlanIDs(t, srv, raw, project); len(ids) != 1 || ids[0] != first {
		t.Errorf("plans on the server under %s = %v, want [%s]", project, ids, first)
	}

	// 4. The server goes down: the plan is still written locally and the
	// fan-out failure is reported as a warning naming the repair command.
	if err := srv.wait(t); err != nil {
		t.Fatal(err)
	}
	r = exec(t, plans, "plan", "create", "--input", inlinePlan)
	if r.code != 0 {
		t.Fatalf("plan create with the server down: %+v", r)
	}
	second := result(t, r)["frontMatter"].(map[string]any)["id"].(string)
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
	if files := planFiles(t, plans); len(files) != 2 {
		t.Errorf("docs/plans = %v, want two files", files)
	}

	// 5. The server comes back on the same address (the in-memory stores
	// start empty) and `sync` copies the local plans to it.
	back := startServer(t, serverRunOptions{Addr: srv.addr, Tokens: tokens, Version: "e2e"}, nil)
	r = exec(t, plans, "sync", "--from", "file", "--to", "clued-server")
	if r.code != 0 {
		t.Fatalf("sync: %+v", r)
	}
	if !strings.Contains(r.stdout, `"copied"`) {
		t.Errorf("sync output: %s", r.stdout)
	}
	ids := serverPlanIDs(t, back, raw, project)
	if len(ids) != 2 || !contains(ids, first) || !contains(ids, second) {
		t.Errorf("plans on the server after sync = %v, want [%s %s]", ids, first, second)
	}

	// 6. The MCP endpoint over Streamable HTTP, with the project in the
	// X-Clued-Project header, lists the plan and story tools and the
	// session tools that need no local disk.
	mcpSess := connectStreamable(t, back.url(server.PathMCP), raw, project)
	listed, err := mcpSess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range listed.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"plan_create", "plan_list", "story_create", "find_sessions", "read_transcript"} {
		if !names[want] {
			t.Errorf("/mcp does not list %s (tools: %d)", want, len(names))
		}
	}
	for _, absent := range []string{"restore_session", "prime_context"} {
		if names[absent] {
			t.Errorf("/mcp lists %s, which needs the local disk", absent)
		}
	}
	_ = mcpSess.Close() // an open stream would hold the server's shutdown
	if err := back.wait(t); err != nil {
		t.Fatal(err)
	}
}

// clearClientEnv blanks every CLUED_* variable the client config reads, so
// the developer's shell cannot put the test in another mode.
func clearClientEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CLUED_SERVER_URL", "CLUED_SERVER_TOKEN", "CLUED_SERVER_CA_FILE", "CLUED_SERVER_INSECURE", "CLUED_PROJECTS_DIR", "CLUED_FILE_HISTORY_DIR", "CLUED_CLAUDE_APP_CONFIG_PATH", "CLUED_WAL_PATH", "CLUED_PORT"} {
		t.Setenv(name, "")
	}
}

// clientConfig writes the plugin's config.json at its default place under
// home (~/.claude/plugins/data/clued/config.json) for a server at serverURL
// with token, and returns its path and the projects directory it names.
func clientConfig(t *testing.T, home, serverURL, token string) (configPath, projectsDir string) {
	t.Helper()
	configDir := filepath.Join(home, ".claude", "plugins", "data", "clued")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	projectsDir = filepath.Join(home, ".claude", "projects")
	appConfig := filepath.Join(home, "claude.json")
	if err := os.WriteFile(appConfig, []byte(`{"lastKnownAccountUuid":"local-account"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"serverUrl":           serverURL,
		"serverToken":         token,
		"projectsDir":         projectsDir,
		"fileHistoryDir":      filepath.Join(home, ".claude", "file-history"),
		"claudeAppConfigPath": appConfig,
	})
	if err != nil {
		t.Fatal(err)
	}
	configPath = filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath, projectsDir
}

// noMongo fails the test if the local MongoDB store is opened: in remote
// mode nothing on the client may reach a database.
func noMongo(t *testing.T) {
	t.Helper()
	prev := openMongoSessionStore
	openMongoSessionStore = func(context.Context, session.Config) (session.Store, error) {
		t.Error("the MongoDB store was opened in remote mode")
		return nil, errors.New("mongo must not be reached")
	}
	t.Cleanup(func() { openMongoSessionStore = prev })
}

// connectInMemory connects a client to srv over in-memory transports.
func connectInMemory(t *testing.T, srv *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// mcpText calls a tool and returns the text of its single content block,
// failing the test on a transport or tool error.
func mcpText(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s: %d content blocks, want 1", name, len(res.Content))
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("%s: content is %T, want text", name, res.Content[0])
	}
	if res.IsError {
		t.Fatalf("%s failed: %s", name, tc.Text)
	}
	return tc.Text
}

func mcpList(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) []map[string]any {
	t.Helper()
	var out []map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, cs, name, args)), &out); err != nil {
		t.Fatalf("%s: not a JSON list: %v", name, err)
	}
	return out
}

func mcpObject(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, cs, name, args)), &out); err != nil {
		t.Fatalf("%s: not a JSON object: %v", name, err)
	}
	return out
}

// serverPlanIDs lists the plans the server holds for project through the
// store transport, as a second client would see them.
func serverPlanIDs(t *testing.T, r *running, token, project string) []string {
	t.Helper()
	status, body := r.call(t, "GET", "/api/v1/projects/"+project+"/store/plan", token, nil)
	if status != http.StatusOK {
		t.Fatalf("store list for %s = %d %v", project, status, body)
	}
	docs, _ := body["result"].([]any)
	var ids []string
	for _, d := range docs {
		fm, _ := d.(map[string]any)["frontMatter"].(map[string]any)
		id, _ := fm["id"].(string)
		ids = append(ids, id)
	}
	return ids
}

// bearerProject adds the bearer token and the project header to every
// request, the way `claude mcp add --header` registers the endpoint.
type bearerProject struct {
	token, project string
}

func (h bearerProject) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+h.token)
	r.Header.Set(server.ProjectHeader, h.project)
	return http.DefaultTransport.RoundTrip(r)
}

// connectStreamable connects an MCP client to the server's endpoint over
// Streamable HTTP with the token and project headers set.
func connectStreamable(t *testing.T, endpoint, token, project string) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: bearerProject{token: token, project: project}},
	}, nil)
	if err != nil {
		t.Fatalf("connect to %s: %v", endpoint, err)
	}
	return cs
}
