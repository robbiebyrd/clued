package sessionmcp_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/code/remotecode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

const (
	codeProject = "github.com__acme__widgets"
	codeAccount = "acct-code"
	codeWT      = "wt_0123456789abcdef"
	codeSession = "sess-code"
	codeCommit  = "1111111111111111111111111111111111111111"
)

var codeTools = []string{"code_search", "code_symbol", "code_neighbors", "code_outline", "code_context"}

var codeOverlay = codegraph.WorktreeLayerID(codeWT, "main")

const (
	codeBase = "package pkg\n\nfunc Alloc() int {\n\treturn 1\n}\n\nfunc Caller() {\n\tAlloc()\n}\n"
	codeNew  = "package pkg\n\nfunc Fresh() {}\n"
)

// flakyGraphs is a memgraph whose Open fails with ErrUnavailable while down.
type flakyGraphs struct {
	*memgraph.Opener
	down atomic.Bool
}

func (f *flakyGraphs) Open(ctx context.Context, key string) (codegraph.Graph, error) {
	if f.down.Load() {
		return nil, codegraph.ErrUnavailable
	}
	return f.Opener.Open(ctx, key)
}

// codeEnv is a clued server with the code transport and, unless noGraph,
// the graph read routes over memgraph, for one account and one project.
type codeEnv struct {
	t      *testing.T
	store  *memcode.Store
	graphs *flakyGraphs
	g      codegraph.Graph
	ts     *httptest.Server
	asked  atomic.Int64
	views  chan codeapi.ViewBody
}

type codeEnvOptions struct {
	noGraph bool
	refuse  bool
}

func newCodeEnv(t *testing.T, o codeEnvOptions) *codeEnv {
	t.Helper()
	e := &codeEnv{t: t, store: memcode.New(), graphs: &flakyGraphs{Opener: memgraph.New()}, views: make(chan codeapi.ViewBody, 64)}
	g, err := e.graphs.Open(t.Context(), codeProject)
	must(t, err)
	e.g = g
	opts := codeapi.Options{
		Open:    func(context.Context, string) (code.Store, error) { return e.store, nil },
		Account: func(*http.Request) (string, bool) { return codeAccount, true },
		Allow:   func(*http.Request, string) bool { return !o.refuse },
	}
	mux := http.NewServeMux()
	codeapi.Mount(mux, opts)
	if !o.noGraph {
		codeapi.MountGraph(mux, opts, codeapi.GraphOptions{Graphs: e.graphs})
	}
	e.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/code/graph/") {
			e.asked.Add(1)
			var b struct {
				View *codeapi.ViewBody `json:"view"`
			}
			raw, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(raw))
			_ = json.Unmarshal(raw, &b)
			if b.View != nil {
				select {
				case e.views <- *b.View:
				default:
				}
			}
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(e.ts.Close)
	return e
}

func (e *codeEnv) client() *remotecode.Store {
	e.t.Helper()
	c, err := remote.New(session.Config{ServerURL: e.ts.URL, ServerToken: "code-token"})
	must(e.t, err)
	st, err := remotecode.New(c, codeProject)
	must(e.t, err)
	return st
}

func blobSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func (e *codeEnv) blob(content string) string {
	e.t.Helper()
	must(e.t, e.store.PutBlob(e.t.Context(), code.Blob{SHA256: blobSHA(content), Size: int64(len(content)), Content: []byte(content)}))
	return blobSHA(content)
}

func codeSym(layer, path, name, kind string, start, end int, doc string) codegraph.Symbol {
	return codegraph.Symbol{Layer: layer, ID: path + "#pkg." + name, Kind: kind, Name: name, QualName: "pkg." + name,
		Signature: "func " + name + "()", Doc: doc, Path: path, Start: start, End: end, Exported: true}
}

// seed writes the fixture: the account's worktree on main with
// codeSession; the base holds pkg/a.go (Alloc, and Caller calling it at
// line 8) and pkg/gen.go (Gen00…Gen39); the worktree's overlay adds
// pkg/new.go with Fresh.
func (e *codeEnv) seed() {
	e.t.Helper()
	ctx := e.t.Context()
	must(e.t, e.store.PutWorktree(ctx, code.Worktree{ID: codeWT, Owner: codeAccount, DefaultBranch: "main", Refs: []string{"main"}}))
	must(e.t, e.store.TouchSession(ctx, codeWT, codeSession, false))
	now := time.Now().UTC()
	base := codegraph.BaseLayerID
	for _, l := range []codegraph.Layer{
		{ID: base, Kind: codegraph.LayerBase, Ref: "main", Commit: codeCommit, Updated: now},
		{ID: codeOverlay, Kind: codegraph.LayerWorktree, Parent: base, Owner: codeAccount, Ref: "main", Commit: codeCommit, Updated: now},
	} {
		must(e.t, e.g.PutLayer(ctx, l))
	}
	must(e.t, e.g.ApplyFile(ctx, codegraph.FileFacts{
		File:        codegraph.File{Layer: base, Path: "pkg/a.go", SHA256: e.blob(codeBase), Lang: "go", Package: "pkg"},
		PackageKind: codegraph.PackageKindPackage,
		Symbols: []codegraph.Symbol{
			codeSym(base, "pkg/a.go", "Alloc", codegraph.SymbolFunction, 3, 5, "Alloc allocates an identifier."),
			codeSym(base, "pkg/a.go", "Caller", codegraph.SymbolFunction, 7, 9, "Caller calls Alloc."),
		},
	}))
	var gen []codegraph.Symbol
	var src strings.Builder
	src.WriteString("package pkg\n")
	for i := range 40 {
		name := fmt.Sprintf("Gen%02d", i)
		gen = append(gen, codeSym(base, "pkg/gen.go", name, codegraph.SymbolFunction, i+2, i+2, name+" is generated code with a long enough doc line."))
		src.WriteString("func " + name + "() {}\n")
	}
	must(e.t, e.g.ApplyFile(ctx, codegraph.FileFacts{
		File:    codegraph.File{Layer: base, Path: "pkg/gen.go", SHA256: e.blob(src.String()), Lang: "go", Package: "pkg"},
		Symbols: gen,
	}))
	alloc := codegraph.NodeRef{Layer: base, ID: "pkg/a.go#pkg.Alloc"}
	caller := codegraph.NodeRef{Layer: base, ID: "pkg/a.go#pkg.Caller"}
	must(e.t, e.g.PutEdges(ctx, base, []codegraph.NodeRef{caller}, []codegraph.Edge{{
		Type: codegraph.EdgeCalls, Layer: base, From: caller, To: alloc, Confidence: codegraph.ConfidenceExact, Count: 1, Line: 8,
	}}))
	must(e.t, e.g.ApplyFile(ctx, codegraph.FileFacts{
		File:    codegraph.File{Layer: codeOverlay, Path: "pkg/new.go", SHA256: e.blob(codeNew), Lang: "go", Package: "pkg"},
		Symbols: []codegraph.Symbol{codeSym(codeOverlay, "pkg/new.go", "Fresh", codegraph.SymbolFunction, 3, 3, "Fresh is a local edit.")},
	}))
}

// tools returns session tools reading e's graph, as the session view says.
func (e *codeEnv) tools(view sessionmcp.CodeView) *sessionmcp.Tools {
	tools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) { return memsession.New(), nil }, account)
	st := e.client()
	tools.CodeGraph = func(context.Context) (sessionmcp.CodeGraph, error) { return st, nil }
	tools.CodeView = func(context.Context) sessionmcp.CodeView { return view }
	tools.CodeRecheck = -1
	return tools
}

func hasCodeTools(t *testing.T, names map[string]bool) bool {
	t.Helper()
	n := countCodeTools(names)
	if n != 0 && n != len(codeTools) {
		t.Fatalf("some code tools listed: %v", names)
	}
	return n == len(codeTools)
}

// countCodeTools counts the code tools in names.
func countCodeTools(names map[string]bool) int {
	n := 0
	for _, name := range codeTools {
		if names[name] {
			n++
		}
	}
	return n
}

func decodeCode(t *testing.T, res *mcp.CallToolResult) obj {
	t.Helper()
	if res.IsError {
		t.Fatalf("tool error: %s", text(t, res))
	}
	var out obj
	must(t, json.Unmarshal([]byte(text(t, res)), &out))
	return out
}

func list(o obj, key string) []obj {
	raw, _ := o[key].([]any)
	out := make([]obj, 0, len(raw))
	for _, v := range raw {
		m, _ := v.(map[string]any)
		out = append(out, m)
	}
	return out
}

func TestCodeToolsAreListedOnlyWhenTheProjectIsIndexedAndAllowed(t *testing.T) {
	view := sessionmcp.CodeView{SessionID: codeSession, Ref: "main"}

	indexed := newCodeEnv(t, codeEnvOptions{})
	indexed.seed()
	if !hasCodeTools(t, toolNames(t, connectClient(t, indexed.tools(view).Server(), nil))) {
		t.Error("indexed project: code tools not listed")
	}

	empty := newCodeEnv(t, codeEnvOptions{})
	if hasCodeTools(t, toolNames(t, connectClient(t, empty.tools(view).Server(), nil))) {
		t.Error("project without code: code tools listed")
	}

	refused := newCodeEnv(t, codeEnvOptions{refuse: true})
	refused.seed()
	if hasCodeTools(t, toolNames(t, connectClient(t, refused.tools(view).Server(), nil))) {
		t.Error("project the server refuses: code tools listed")
	}

	noGraph := newCodeEnv(t, codeEnvOptions{noGraph: true})
	noGraph.seed()
	if hasCodeTools(t, toolNames(t, connectClient(t, noGraph.tools(view).Server(), nil))) {
		t.Error("server without graph routes: code tools listed")
	}

	down := newCodeEnv(t, codeEnvOptions{})
	down.seed()
	down.graphs.down.Store(true)
	if hasCodeTools(t, toolNames(t, connectClient(t, down.tools(view).Server(), nil))) {
		t.Error("unreachable graph: code tools listed")
	}

	// The remote subset (the server's own /mcp) and a local-mode config
	// never offer them, and ask nothing.
	before := indexed.asked.Load()
	if hasCodeTools(t, toolNames(t, connectClient(t, indexed.tools(view).Subset(true).Server(), nil))) {
		t.Error("remote subset lists the code tools")
	}
	local := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) { return memsession.New(), nil }, account)
	if hasCodeTools(t, toolNames(t, connectClient(t, local.Server(), nil))) {
		t.Error("local mode lists the code tools")
	}
	if n := indexed.asked.Load(); n != before {
		t.Errorf("the remote subset asked the graph %d times", n-before)
	}
}

func TestCodeToolsAreAddedOnceTheProjectIsIndexed(t *testing.T) {
	e := newCodeEnv(t, codeEnvOptions{})
	tools := e.tools(sessionmcp.CodeView{SessionID: codeSession})
	tools.CodeRecheck = 20 * time.Millisecond
	changed := make(chan struct{}, 1)
	srv := tools.Server()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), st, nil)
	must(t, err)
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {
			select {
			case changed <- struct{}{}:
			default:
			}
		},
	}).Connect(t.Context(), ct, nil)
	must(t, err)
	t.Cleanup(func() { _ = cs.Close() })
	if hasCodeTools(t, toolNames(t, cs)) {
		t.Fatal("code tools listed before the project was indexed")
	}
	e.seed()
	deadline := time.After(10 * time.Second)
	// The tools are added one at a time, so a listing in between holds
	// some of them; the client lists again on the change notice.
	for countCodeTools(toolNames(t, cs)) != len(codeTools) {
		select {
		case <-changed:
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("code tools never listed after indexing")
		}
	}
	res := decodeCode(t, call(t, cs, "code_search", obj{"query": "Fresh"}))
	if hits := list(res, "results"); len(hits) != 1 || hits[0]["at"] != "pkg/new.go:3" {
		t.Fatalf("results %v", res)
	}
}

func TestCodeToolsReadTheSessionViewAndAnswerPathLine(t *testing.T) {
	e := newCodeEnv(t, codeEnvOptions{})
	e.seed()
	cs := connectClient(t, e.tools(sessionmcp.CodeView{SessionID: codeSession, Ref: "main"}).Server(), nil)
	drain(e.views)

	// search: the session's overlay is visible, with path:line.
	res := decodeCode(t, call(t, cs, "code_search", obj{"query": "Fresh"}))
	hits := list(res, "results")
	if res["status"] != codeapi.StatusReady || len(hits) != 1 || hits[0]["at"] != "pkg/new.go:3" || hits[0]["name"] != "Fresh" ||
		hits[0]["doc"] != "Fresh is a local edit." || hits[0]["id"] != "pkg/new.go#pkg.Fresh" {
		t.Fatalf("search Fresh: %v", res)
	}
	if v, _ := res["view"].(map[string]any); v == nil || v["localEdits"] != true {
		t.Fatalf("view %v, want the session's local edits", res["view"])
	}
	if got := <-e.views; got.SessionID != codeSession || got.Ref != "" {
		t.Fatalf("the tool sent view %+v, want the session", got)
	}
	res = decodeCode(t, call(t, cs, "code_search", obj{"query": "Alloc"}))
	if hits := list(res, "results"); len(hits) == 0 || hits[0]["at"] != "pkg/a.go:3-5" || hits[0]["qualName"] != "pkg.Alloc" {
		t.Fatalf("search Alloc: %v", res)
	}

	// symbol: span, doc and the body from the stored blob.
	res = decodeCode(t, call(t, cs, "code_symbol", obj{"qualName": "pkg.Alloc"}))
	sym, _ := res["symbol"].(map[string]any)
	body, _ := res["body"].(map[string]any)
	if sym == nil || sym["at"] != "pkg/a.go:3-5" || sym["doc"] != "Alloc allocates an identifier." || sym["lang"] != "go" {
		t.Fatalf("symbol %v", res)
	}
	if body == nil || body["at"] != "pkg/a.go:3-5" || body["text"] != "func Alloc() int {\n\treturn 1\n}" {
		t.Fatalf("body %v", res["body"])
	}

	// neighbors: the call site as path:line, the caller with its own.
	res = decodeCode(t, call(t, cs, "code_neighbors", obj{"id": "pkg/a.go#pkg.Alloc", "relation": "callers"}))
	edges := list(res, "edges")
	if len(edges) != 1 || edges[0]["at"] != "pkg/a.go:8" || edges[0]["from"] != "pkg.Caller" || edges[0]["fromAt"] != "pkg/a.go:7" ||
		edges[0]["to"] != "pkg.Alloc" || edges[0]["confidence"] != codegraph.ConfidenceExact || edges[0]["relation"] != "callers" {
		t.Fatalf("callers %v", res)
	}

	// outline: symbols in order, without bodies.
	res = decodeCode(t, call(t, cs, "code_outline", obj{"path": "pkg/a.go"}))
	syms := list(res, "symbols")
	if len(syms) != 2 || syms[0]["at"] != "pkg/a.go:3-5" || syms[1]["name"] != "Caller" || len(list(res, "files")) != 1 {
		t.Fatalf("outline %v", res)
	}

	// context: hits, a hop of neighbours and bodies.
	res = decodeCode(t, call(t, cs, "code_context", obj{"task": "change Alloc and its Caller"}))
	if len(list(res, "hits")) == 0 || len(list(res, "bodies")) == 0 || !strings.Contains(text(t, call(t, cs, "code_context", obj{"task": "Alloc"})), "pkg/a.go:3-5") {
		t.Fatalf("context %v", res)
	}
	for _, b := range list(res, "bodies") {
		if at, _ := b["at"].(string); !strings.HasPrefix(at, "pkg/") || !strings.Contains(at, ":") {
			t.Fatalf("body at %q is not path:line", at)
		}
	}

	// An explicit ref reads that branch, without the session's overlay.
	drain(e.views)
	res = decodeCode(t, call(t, cs, "code_search", obj{"query": "Fresh", "ref": "main"}))
	if hits := list(res, "results"); len(hits) != 0 {
		t.Fatalf("ref view shows the overlay: %v", res)
	}
	if got := <-e.views; got.SessionID != "" || got.Ref != "main" {
		t.Fatalf("ref view sent %+v", got)
	}
}

func drain(c chan codeapi.ViewBody) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

func TestCodeToolsCutToTheBudgetAndContinue(t *testing.T) {
	e := newCodeEnv(t, codeEnvOptions{})
	e.seed()
	cs := connectClient(t, e.tools(sessionmcp.CodeView{SessionID: codeSession}).Server(), nil)

	tools, err := cs.ListTools(t.Context(), nil)
	must(t, err)
	for _, tool := range tools.Tools {
		if tool.Name != "code_search" {
			continue
		}
		raw, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(raw), `"max_tokens"`) || !strings.Contains(string(raw), `"cursor"`) || !strings.Contains(tool.Description, "2000 tokens") {
			t.Fatalf("code_search has no budget arguments: %s %s", raw, tool.Description)
		}
	}

	first := decodeCode(t, call(t, cs, "code_search", obj{"query": "generated", "limit": 40, "max_tokens": 300}))
	next, _ := first["next"].(string)
	if first["truncated"] != true || next == "" || len(list(first, "results")) == 0 {
		t.Fatalf("first page %v", first)
	}
	if n := len(text(t, call(t, cs, "code_search", obj{"query": "generated", "limit": 40, "max_tokens": 300}))) / 4; n > 400 {
		t.Errorf("a 300-token page is about %d tokens", n)
	}
	second := decodeCode(t, call(t, cs, "code_search", obj{"query": "generated", "limit": 40, "max_tokens": 300, "cursor": next}))
	a, b := list(first, "results"), list(second, "results")
	if len(b) == 0 || a[0]["at"] == b[0]["at"] {
		t.Fatalf("the cursor did not continue: %v then %v", a, b)
	}
}

func TestCodeToolsFallBackToTheBranchForAnUnknownSession(t *testing.T) {
	e := newCodeEnv(t, codeEnvOptions{})
	e.seed()
	cs := connectClient(t, e.tools(sessionmcp.CodeView{SessionID: "sess-elsewhere", Ref: "main"}).Server(), nil)
	res := decodeCode(t, call(t, cs, "code_search", obj{"query": "Alloc"}))
	note, _ := res["note"].(string)
	if hits := list(res, "results"); len(hits) == 0 || hits[0]["at"] != "pkg/a.go:3-5" ||
		!strings.Contains(note, "does not know session sess-elsewhere") || !strings.Contains(note, "branch main") {
		t.Fatalf("fallback %v", res)
	}
	// An unknown symbol is still an error, not a fallback.
	res2 := call(t, cs, "code_symbol", obj{"qualName": "pkg.Missing"})
	if !res2.IsError || !strings.Contains(text(t, res2), "not found") {
		t.Fatalf("unknown symbol: %s", text(t, res2))
	}
}

func TestCodeToolsSayWhenTheIndexIsUnavailable(t *testing.T) {
	e := newCodeEnv(t, codeEnvOptions{})
	e.seed()
	cs := connectClient(t, e.tools(sessionmcp.CodeView{SessionID: codeSession}).Server(), nil)
	e.graphs.down.Store(true)
	res := call(t, cs, "code_search", obj{"query": "Alloc"})
	if !res.IsError || !strings.Contains(text(t, res), "the code index is unavailable right now: search and read the files directly") {
		t.Fatalf("unavailable: %s", text(t, res))
	}
}

func TestCodeToolsFindTheCurrentSession(t *testing.T) {
	e := newCodeEnv(t, codeEnvOptions{})
	e.seed()
	sessions := memsession.New()
	newTools := func() *sessionmcp.Tools {
		tools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) { return sessions, nil }, account)
		st := e.client()
		tools.CodeGraph = func(context.Context) (sessionmcp.CodeGraph, error) { return st, nil }
		tools.CodeRecheck = -1
		return tools
	}

	// CLAUDE_SESSION_ID names it.
	t.Setenv("CLAUDE_SESSION_ID", codeSession)
	cs := connectClient(t, newTools().Server(), nil)
	drain(e.views)
	res := decodeCode(t, call(t, cs, "code_search", obj{"query": "Fresh"}))
	if got := <-e.views; got.SessionID != codeSession || len(list(res, "results")) != 1 {
		t.Fatalf("env session: view %+v, result %v", got, res)
	}

	// Without it, the newest session captured in the working directory.
	t.Setenv("CLAUDE_SESSION_ID", "")
	dir := t.TempDir()
	t.Chdir(dir)
	wd, err := os.Getwd()
	must(t, err)
	must(t, sessions.UpsertSession(context.Background(), session.Session{SessionID: "sess-old", AccountID: account, ProjectPath: wd, Cwd: wd, LastSeen: t0}))
	must(t, sessions.UpsertSession(context.Background(), session.Session{SessionID: codeSession, AccountID: account, ProjectPath: wd, Cwd: wd, LastSeen: t0.Add(time.Hour)}))
	must(t, sessions.UpsertSession(context.Background(), session.Session{SessionID: "sess-other", AccountID: account, ProjectPath: wd + "/sub", Cwd: wd + "/sub", LastSeen: t0.Add(2 * time.Hour)}))
	cs = connectClient(t, newTools().Server(), nil)
	drain(e.views)
	res = decodeCode(t, call(t, cs, "code_search", obj{"query": "Fresh"}))
	if got := <-e.views; got.SessionID != codeSession || len(list(res, "results")) != 1 {
		t.Fatalf("cwd session: view %+v, result %v", got, res)
	}
}
