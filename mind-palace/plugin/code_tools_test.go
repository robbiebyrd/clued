package plugin_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/code/remotecode"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/codesync"
)

// The code tools end to end: a real `mind-palace server run --store memory
// --graph memory`, a repository synced to it with session/codesync (the
// client the relay runs) and indexed by the server's own indexer, and the
// plugin's clued MCP server started through the shim as Claude Code starts
// it (.mcp.json: scripts/clued session mcp, in the project directory).

const (
	codeOrigin   = "https://github.com/acme/widgets.git"
	codeSlug     = "github.com__acme__widgets"
	toolsSession = "sess-plugin-code"
)

const widgetGo = `package widgets

// NewWidget builds a widget with its default size.
func NewWidget() *Widget {
	return &Widget{Size: defaultSize()}
}

// Widget is a thing with a size.
type Widget struct {
	Size int
}

func defaultSize() int {
	return 3
}
`

// cluedServer runs `mind-palace server run` with the code store and the
// memory graph on a free loopback port and returns its URL and a token for
// account acct-plugin-code on every project.
func cluedServer(t *testing.T) (string, string) {
	t.Helper()
	bin := filepath.Join(binaryDir(t), "mind-palace")
	dir := t.TempDir()
	tokens := filepath.Join(dir, "tokens.json")
	out, err := exec.Command(bin, "server", "token", "create", "--tokens", tokens, "--name", "plugin", "--project", "*",
		"--account", "acct-plugin-code", "--format", "text").CombinedOutput()
	if err != nil {
		t.Fatalf("token create: %v\n%s", err, out)
	}
	token := lastField(string(out))
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	cmd := exec.Command(bin, "server", "run", "--addr", addr, "--tokens", tokens, "--store", "memory", "--code-store", "memory",
		"--graph", "memory", "--session-config", filepath.Join(dir, "config.json"))
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + dir, "PATH=/usr/bin:/bin"}
	logFile, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		logFile.Close()
		if t.Failed() {
			if b, err := os.ReadFile(logFile.Name()); err == nil {
				t.Logf("server log:\n%s", b)
			}
		}
	})
	url := "http://" + addr
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(url + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return url, token
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the clued server never became healthy: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// lastField is the token token create printed: the last word of its
// output that looks like one.
func lastField(out string) string {
	fields := strings.Fields(out)
	for i := len(fields) - 1; i >= 0; i-- {
		f := strings.Trim(fields[i], `"`)
		if len(f) >= 20 && !strings.ContainsAny(f, "/:{}") {
			return f
		}
	}
	return strings.TrimSpace(out)
}

// gitCommitAll commits every file of dir on main.
func gitCommitAll(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"checkout", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "widgets"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+dir, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// mcpSession starts the plugin's clued MCP server through the shim in dir.
func mcpSession(t *testing.T, dir string, env []string) *mcp.ClientSession {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "scripts", "clued"), "session", "mcp")
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stderr = os.Stderr
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "plugin-test", Version: "1"}, nil).Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listedTools(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, tool := range res.Tools {
		out[tool.Name] = true
	}
	return out
}

func callJSON(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("%s: %d content blocks", name, len(res.Content))
	}
	body := res.Content[0].(*mcp.TextContent).Text
	if res.IsError {
		t.Fatalf("%s: tool error: %s", name, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("%s: %v in %s", name, err, body)
	}
	return out
}

func TestCodeToolsAgainstAServerWithTheMemoryGraph(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	url, token := cluedServer(t)
	home := remoteHome(t, url, token)

	// A project with no code collected: the tools are not listed.
	bare := gitRepoWithOrigin(t, "https://github.com/acme/empty.git")
	env := remoteEnv(t, home, closedPort(t), "CLAUDE_SESSION_ID="+toolsSession)
	if names := listedTools(t, mcpSession(t, bare, env)); names["code_search"] || !names["find_sessions"] {
		t.Fatalf("a project without code lists %v", names)
	}

	// Collect the repository's code for the session, as the relay does.
	repo := gitRepoWithOrigin(t, codeOrigin)
	if err := os.WriteFile(filepath.Join(repo, "widget.go"), []byte(widgetGo), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCommitAll(t, repo)
	on := true
	cfg := session.Config{ServerURL: url, ServerToken: token, CodeSync: &on}
	rep, err := codesync.Run(context.Background(), codesync.Request{
		Cwd: repo, SessionID: toolsSession, Full: true, Touch: codesync.TouchStart, Budget: -1,
		Config: cfg, StateDir: t.TempDir(), Host: "plugin-test",
	})
	if err != nil || rep.Skipped != "" || rep.Upserts == 0 || rep.Project != codeSlug {
		t.Fatalf("code sync: %+v, %v", rep, err)
	}

	// Wait for the server's indexer to put the session's file in its view.
	client, err := remote.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := remotecode.New(client, codeSlug)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := graph.Outline(context.Background(), codeapi.OutlineBody{View: &codeapi.ViewBody{SessionID: toolsSession}, Path: "widget.go"})
		if err == nil && res.Status == codeapi.StatusReady && len(res.Files) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never indexed: %+v, %v", res, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	// The plugin's clued server in the project lists the code tools and
	// reads the session's view, which it names on its own.
	cs := mcpSession(t, repo, env)
	names := listedTools(t, cs)
	for _, name := range []string{"code_search", "code_symbol", "code_neighbors", "code_outline", "code_context"} {
		if !names[name] {
			t.Fatalf("%s not listed: %v", name, names)
		}
	}

	res := callJSON(t, cs, "code_outline", map[string]any{"path": "widget.go"})
	view, _ := res["view"].(map[string]any)
	if res["status"] != codeapi.StatusReady || view == nil || view["session"] != toolsSession || view["localEdits"] != true {
		t.Fatalf("code_outline did not read the session's view: %v", res)
	}
	files, _ := res["files"].([]any)
	if len(files) != 1 || files[0].(map[string]any)["path"] != "widget.go" || files[0].(map[string]any)["lang"] != "go" {
		t.Fatalf("code_outline files: %v", res)
	}
	// The server's indexer runs without a parser for now (every file is
	// indexed as text, parseError true); once it has one, the symbols come
	// with their path:line.
	for _, s := range anyList(res["symbols"]) {
		m := s.(map[string]any)
		if at, _ := m["at"].(string); !strings.HasPrefix(at, "widget.go:") {
			t.Fatalf("outline symbol at %q is not path:line", at)
		}
	}

	res = callJSON(t, cs, "code_search", map[string]any{"query": "NewWidget"})
	if view, _ := res["view"].(map[string]any); res["status"] != codeapi.StatusReady || view == nil || view["session"] != toolsSession {
		t.Fatalf("code_search: %v", res)
	}
	for _, h := range anyList(res["results"]) {
		if at, _ := h.(map[string]any)["at"].(string); !strings.HasPrefix(at, "widget.go:4") {
			t.Fatalf("code_search hit at %q, want widget.go:4-6", at)
		}
	}
	res = callJSON(t, cs, "code_context", map[string]any{"task": "change the default size of NewWidget"})
	if res["status"] != codeapi.StatusReady {
		t.Fatalf("code_context: %v", res)
	}

	// A branch the index does not hold reads the base, and an unknown
	// symbol is an error the agent sees.
	res = callJSON(t, cs, "code_search", map[string]any{"query": "NewWidget", "ref": "elsewhere"})
	if res["status"] != codeapi.StatusReady {
		t.Fatalf("code_search on another ref: %v", res)
	}
	r, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "code_symbol", Arguments: map[string]any{"qualName": "widgets.Missing"}})
	if err != nil || !r.IsError || !strings.Contains(r.Content[0].(*mcp.TextContent).Text, "not found") {
		t.Fatalf("code_symbol of an unknown symbol: %+v, %v", r, err)
	}
}

func anyList(v any) []any {
	l, _ := v.([]any)
	return l
}
