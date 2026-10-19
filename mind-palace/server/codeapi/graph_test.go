package codeapi_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/falkor"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

const (
	wtAlice   = "wt_aaaaaaaaaaaaaaaa"
	wtBob     = "wt_bbbbbbbbbbbbbbbb"
	sessAlice = "sess-alice"
	sessBob   = "sess-bob"
	commitF   = "3333333333333333333333333333333333333333"
)

var (
	branchF  = codegraph.BranchLayerID("feature", commitF)
	overlayA = codegraph.WorktreeLayerID(wtAlice, "feature")
	overlayB = codegraph.WorktreeLayerID(wtBob, "feature")
)

// graphBackends runs a test on memgraph and, when
// MIND_PALACE_TEST_FALKORDB_URL is set, on FalkorDB.
func graphBackends(t *testing.T, run func(t *testing.T, graphs codegraph.Opener)) {
	t.Run("memgraph", func(t *testing.T) { run(t, memgraph.New()) })
	t.Run("falkordb", func(t *testing.T) {
		u := os.Getenv("MIND_PALACE_TEST_FALKORDB_URL")
		if u == "" {
			t.Skip("MIND_PALACE_TEST_FALKORDB_URL is not set")
		}
		o, err := falkor.New(u)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = o.Close() })
		run(t, o)
	})
}

// graphEnv is a clued server with the code and graph routes, every account
// sharing one namespaced key (a workspace project: the hardest case for
// overlay privacy), of its own so parallel runs on one FalkorDB never meet.
type graphEnv struct {
	t      *testing.T
	ctx    context.Context
	key    string
	stores *stores
	store  code.Store
	graphs codegraph.Opener
	g      codegraph.Graph
	ts     *httptest.Server

	mu      sync.Mutex
	indexed map[string]int64
}

func newGraphEnv(t *testing.T, graphs codegraph.Opener) *graphEnv {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	e := &graphEnv{t: t, ctx: t.Context(), key: "w" + hex.EncodeToString(b) + "__" + project, stores: newStores(),
		graphs: graphs, indexed: map[string]int64{}}
	e.store = e.stores.get(e.key)
	g, err := graphs.Open(e.ctx, e.key)
	if err != nil {
		t.Fatal(err)
	}
	e.g = g
	t.Cleanup(func() { _ = g.Drop(context.Background()) })
	c := server.NewClued(server.CluedOptions{
		Version:   "test",
		Tokens:    testTokens,
		Sessions:  memsession.New(),
		Open:      func(_ context.Context, key string) (store.Store, error) { return memstore.New(key), nil },
		Namespace: func(auth.Principal, string) string { return e.key },
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Code:      e.stores.Open,
		Graph: &codeapi.GraphOptions{Graphs: graphs, Indexed: func(_ context.Context, key string) (int64, bool) {
			e.mu.Lock()
			defer e.mu.Unlock()
			seq, ok := e.indexed[key]
			return seq, ok
		}},
	})
	t.Cleanup(c.Close)
	e.ts = httptest.NewServer(c.Handler)
	t.Cleanup(e.ts.Close)
	return e
}

func graphPath(route string) string { return codePath("graph/" + route) }

// post calls a graph route and decodes the result into out (when the call
// succeeded).
func (e *graphEnv) post(token, route string, body any, out any) (int, envelope) {
	e.t.Helper()
	status, env := call(e.t, e.ts.URL, token, "POST", graphPath(route), body)
	if env.OK && out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			e.t.Fatalf("%s: %v in %s", route, err, env.Result)
		}
	}
	return status, env
}

func (e *graphEnv) must(err error) {
	e.t.Helper()
	if err != nil {
		e.t.Fatal(err)
	}
}

// putBlob stores content and returns its hash.
func (e *graphEnv) putBlob(content string) string {
	e.t.Helper()
	e.must(e.store.PutBlob(e.ctx, code.Blob{SHA256: sha(content), Size: int64(len(content)), Content: []byte(content)}))
	return sha(content)
}

func sym(layer, path, name, kind string, start, end int, doc, parent string) codegraph.Symbol {
	return codegraph.Symbol{Layer: layer, ID: path + "#pkg." + name, Kind: kind, Name: name, QualName: "pkg." + name,
		Signature: "func " + name + "()", Doc: doc, Path: path, Start: start, End: end, Exported: true, Parent: parent}
}

// The fixture's files.
var (
	baseA = strings.Join([]string{
		"package pkg",
		"",
		"func Alloc() int {", // 3
		"\treturn 1",
		"}",
		"",
		"func Caller() {", // 7
		"\tAlloc()",
		"}",
		"",
		"type T struct{}", // 11
		"func (T) M() {}", // 12
		"",
	}, "\n")
	overlayAContent = strings.Join([]string{
		"package pkg",
		"",
		"func AllocNew() int {", // 3
		"\treturn 2",
		"}",
	}, "\n")
	bobContent = "package pkg\n\nfunc BobSecret() {}\n"
)

// seed writes the fixture: alice and bob each own a worktree on feature,
// each with a session; the graph holds the base (pkg/a.go with Alloc,
// Caller and T.M, Caller calling Alloc), the feature branch layer, alice's
// overlay rewriting pkg/a.go (Alloc renamed AllocNew) and bob's overlay
// adding pkg/b.go with BobSecret.
func (e *graphEnv) seed() {
	e.t.Helper()
	ctx := e.ctx
	for _, w := range []struct{ id, owner, sess string }{{wtAlice, "acct-alice", sessAlice}, {wtBob, "acct-bob", sessBob}} {
		e.must(e.store.PutWorktree(ctx, code.Worktree{ID: w.id, Owner: w.owner, DefaultBranch: "main", Refs: []string{"feature"}}))
		e.must(e.store.TouchSession(ctx, w.id, w.sess, false))
	}
	now := time.Now().UTC()
	for _, l := range []codegraph.Layer{
		{ID: codegraph.BaseLayerID, Kind: codegraph.LayerBase, Ref: "main", Commit: oid2, Updated: now},
		{ID: branchF, Kind: codegraph.LayerBranch, Parent: codegraph.BaseLayerID, Ref: "feature", Commit: commitF, Updated: now},
		{ID: overlayA, Kind: codegraph.LayerWorktree, Parent: branchF, Owner: "acct-alice", Ref: "feature", Commit: oid1, Updated: now},
		{ID: overlayB, Kind: codegraph.LayerWorktree, Parent: branchF, Owner: "acct-bob", Ref: "feature", Commit: oid1, Updated: now},
	} {
		e.must(e.g.PutLayer(ctx, l))
	}
	base := codegraph.BaseLayerID
	tSym := sym(base, "pkg/a.go", "T", codegraph.SymbolStruct, 11, 12, "", "")
	mSym := sym(base, "pkg/a.go", "M", codegraph.SymbolMethod, 12, 12, "", tSym.ID)
	mSym.ID, mSym.QualName = "pkg/a.go#pkg.T.M", "pkg.T.M"
	e.must(e.g.ApplyFile(ctx, codegraph.FileFacts{
		File: codegraph.File{Layer: base, Path: "pkg/a.go", SHA256: e.putBlob(baseA), GitOID: oid1, Lang: "go", Package: "pkg",
			HiddenBy: []string{overlayA}},
		PackageKind: codegraph.PackageKindPackage,
		Symbols: []codegraph.Symbol{
			sym(base, "pkg/a.go", "Alloc", codegraph.SymbolFunction, 3, 5, "Alloc allocates an identifier.\nIt never fails.", ""),
			sym(base, "pkg/a.go", "Caller", codegraph.SymbolFunction, 7, 9, "Caller calls Alloc.", ""),
			tSym, mSym,
		},
	}))
	alloc, caller := codegraph.NodeRef{Layer: base, ID: "pkg/a.go#pkg.Alloc"}, codegraph.NodeRef{Layer: base, ID: "pkg/a.go#pkg.Caller"}
	e.must(e.g.PutEdges(ctx, base, []codegraph.NodeRef{caller}, []codegraph.Edge{{
		Type: codegraph.EdgeCalls, Layer: base, From: caller, To: alloc, Confidence: codegraph.ConfidenceExact, Count: 1, Line: 8,
	}}))
	e.must(e.g.ApplyFile(ctx, codegraph.FileFacts{
		File:    codegraph.File{Layer: overlayA, Path: "pkg/a.go", SHA256: e.putBlob(overlayAContent), Lang: "go", Package: "pkg"},
		Symbols: []codegraph.Symbol{sym(overlayA, "pkg/a.go", "AllocNew", codegraph.SymbolFunction, 3, 5, "AllocNew allocates an identifier.", "")},
	}))
	e.must(e.g.ApplyFile(ctx, codegraph.FileFacts{
		File:    codegraph.File{Layer: overlayB, Path: "pkg/b.go", SHA256: e.putBlob(bobContent), Lang: "go", Package: "pkg"},
		Symbols: []codegraph.Symbol{sym(overlayB, "pkg/b.go", "BobSecret", codegraph.SymbolFunction, 3, 3, "BobSecret is bob's.", "")},
	}))
}

type listResult struct {
	Status   string `json:"status"`
	Progress *struct {
		Pending int  `json:"pending"`
		More    bool `json:"more"`
	} `json:"progress"`
	View struct {
		Layers     []string `json:"layers"`
		LocalEdits bool     `json:"localEdits"`
		Note       string   `json:"note"`
	} `json:"view"`
	Items     []json.RawMessage `json:"items"`
	Truncated bool              `json:"truncated"`
	Next      string            `json:"next"`
}

type hit struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Match    string `json:"match"`
	Doc      string `json:"doc"`
}

func (r listResult) hits(t *testing.T) []hit {
	t.Helper()
	var out []hit
	for _, raw := range r.Items {
		var h hit
		if err := json.Unmarshal(raw, &h); err != nil {
			t.Fatal(err)
		}
		out = append(out, h)
	}
	return out
}

func names(hs []hit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.Name)
	}
	return out
}

func (e *graphEnv) search(token string, view any, q string) listResult {
	e.t.Helper()
	var res listResult
	body := map[string]any{"query": q}
	if view != nil {
		body["view"] = view
	}
	status, env := e.post(token, "search", body, &res)
	expect(e.t, "search "+q, status, env, 200, "")
	return res
}

func TestGraphViews(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newGraphEnv(t, graphs)
		e.seed()

		// Omitted view: the base.
		res := e.search(tokenAlice, nil, "Alloc")
		if res.Status != codeapi.StatusReady || strings.Join(res.View.Layers, ",") != "base" || res.View.LocalEdits {
			t.Fatalf("base answer %+v", res)
		}
		hs := res.hits(t)
		if len(hs) == 0 || hs[0].Name != "Alloc" || hs[0].Location != "pkg/a.go:3-5" || hs[0].Match != codeapi.MatchExact ||
			hs[0].Doc != "Alloc allocates an identifier." {
			t.Fatalf("base hits %+v", hs)
		}

		// Alice's session: her overlay over the branch layer and the base.
		mine := map[string]any{"session_id": sessAlice}
		res = e.search(tokenAlice, mine, "AllocNew")
		if want := "base," + branchF + "," + overlayA; strings.Join(res.View.Layers, ",") != want || !res.View.LocalEdits {
			t.Fatalf("alice's view %+v, want %s", res.View, want)
		}
		if hs := res.hits(t); len(hs) != 1 || hs[0].Name != "AllocNew" {
			t.Fatalf("alice's hits %+v", hs)
		}
		// Her overlay rewrote pkg/a.go: the base's Alloc is hidden there.
		if hs := e.search(tokenAlice, mine, "Alloc").hits(t); len(hs) != 0 {
			t.Fatalf("hidden Alloc found in alice's view: %+v", hs)
		}

		// Overlays are never shared: bob's session is unknown to alice, and
		// no view of alice's shows bob's overlay.
		status, env := e.post(tokenAlice, "search", map[string]any{"view": map[string]any{"session_id": sessBob}, "query": "BobSecret"}, nil)
		expect(t, "bob's session for alice", status, env, 404, "not_found")
		for _, v := range []any{nil, mine, map[string]any{"ref": "feature"}, map[string]any{"session_id": sessAlice, "ref": "feature"}} {
			if hs := e.search(tokenAlice, v, "BobSecret").hits(t); len(hs) != 0 {
				t.Fatalf("alice sees bob's overlay in %v: %+v", v, hs)
			}
		}
		if hs := e.search(tokenBob, map[string]any{"session_id": sessBob}, "BobSecret").hits(t); len(hs) != 1 {
			t.Fatalf("bob's own overlay: %+v", hs)
		}

		// A ref: the base plus its branch layer, no overlay.
		res = e.search(tokenAlice, map[string]any{"ref": "feature"}, "Alloc")
		if strings.Join(res.View.Layers, ",") != "base,"+branchF || res.View.LocalEdits {
			t.Fatalf("ref view %+v", res.View)
		}
		// The default branch is the base; an unknown branch falls back to it.
		if v := e.search(tokenAlice, map[string]any{"ref": "main"}, "Alloc").View; strings.Join(v.Layers, ",") != "base" {
			t.Fatalf("main view %+v", v)
		}
		if v := e.search(tokenAlice, map[string]any{"ref": "gone"}, "Alloc").View; strings.Join(v.Layers, ",") != "base" || v.Note == "" {
			t.Fatalf("unknown ref view %+v", v)
		}

		for _, c := range []struct {
			what string
			view any
			want int
			kind string
		}{
			{"unknown session", map[string]any{"session_id": "nope"}, 404, "not_found"},
			{"bad session id", map[string]any{"session_id": "a b"}, 400, "invalid"},
			{"bad ref", map[string]any{"ref": "a..b"}, 400, "invalid"},
			{"unknown view field", map[string]any{"layers": []string{overlayB}}, 400, "invalid"},
		} {
			status, env := e.post(tokenAlice, "search", map[string]any{"view": c.view, "query": "Alloc"}, nil)
			expect(t, c.what, status, env, c.want, c.kind)
		}

		// A session on a worktree without an overlay sees its ref's layers
		// and is told the local edits are missing.
		e.must(e.store.TouchSession(e.ctx, wtAlice, "sess-2", false))
		e.must(e.g.DropLayer(e.ctx, overlayA))
		res = e.search(tokenAlice, map[string]any{"session_id": "sess-2"}, "Alloc")
		if strings.Join(res.View.Layers, ",") != "base,"+branchF || res.View.LocalEdits || res.View.Note == "" {
			t.Fatalf("no overlay view %+v", res.View)
		}
	})
}

func TestGraphSessionOverlayFallsBackWhenItsBranchLayerIsGone(t *testing.T) {
	e := newGraphEnv(t, memgraph.New())
	e.seed()
	newer := codegraph.BranchLayerID("feature", oid1)
	e.must(e.g.PutLayer(e.ctx, codegraph.Layer{ID: newer, Kind: codegraph.LayerBranch, Parent: codegraph.BaseLayerID, Ref: "feature", Commit: oid1, Updated: time.Now()}))
	e.must(e.g.DropLayer(e.ctx, branchF))
	res := e.search(tokenAlice, map[string]any{"session_id": sessAlice}, "AllocNew")
	if strings.Join(res.View.Layers, ",") != "base,"+newer+","+overlayA || res.View.Note == "" {
		t.Fatalf("view %+v", res.View)
	}
	e.must(e.g.DropLayer(e.ctx, newer))
	res = e.search(tokenAlice, map[string]any{"session_id": sessAlice}, "AllocNew")
	if strings.Join(res.View.Layers, ",") != "base,"+overlayA {
		t.Fatalf("view without a branch layer %+v", res.View)
	}
}

type symbolResult struct {
	Status string `json:"status"`
	Symbol *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Doc  string `json:"doc"`
	} `json:"symbol"`
	Location string `json:"location"`
	Lang     string `json:"lang"`
	Children []struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	} `json:"children"`
	Body *struct {
		Location string `json:"location"`
		Text     string `json:"text"`
	} `json:"body"`
	BodyNote  string `json:"bodyNote"`
	Truncated bool   `json:"truncated"`
	Next      string `json:"next"`
}

func TestGraphSymbol(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newGraphEnv(t, graphs)
		e.seed()

		var res symbolResult
		status, env := e.post(tokenAlice, "symbol", map[string]any{"qualName": "pkg.Alloc"}, &res)
		expect(t, "symbol", status, env, 200, "")
		if res.Symbol == nil || res.Symbol.Name != "Alloc" || res.Location != "pkg/a.go:3-5" || res.Lang != "go" ||
			res.Symbol.Doc != "Alloc allocates an identifier.\nIt never fails." {
			t.Fatalf("symbol %s", env.Result)
		}
		if res.Body == nil || res.Body.Text != "func Alloc() int {\n\treturn 1\n}" || res.Body.Location != "pkg/a.go:3-5" || res.Truncated {
			t.Fatalf("body %s", env.Result)
		}

		// The body comes from the view's file: alice's overlay version.
		res = symbolResult{}
		e.post(tokenAlice, "symbol", map[string]any{"view": map[string]any{"session_id": sessAlice}, "id": "pkg/a.go#pkg.AllocNew"}, &res)
		if res.Body == nil || !strings.Contains(res.Body.Text, "return 2") {
			t.Fatalf("overlay body %+v", res)
		}
		// Bob's symbol is not alice's to read in any view.
		status, env = e.post(tokenAlice, "symbol", map[string]any{"id": "pkg/b.go#pkg.BobSecret", "view": map[string]any{"ref": "feature"}}, nil)
		expect(t, "bob's symbol", status, env, 404, "not_found")
		status, env = e.post(tokenAlice, "symbol", map[string]any{"id": "pkg/a.go#pkg.Alloc", "view": map[string]any{"session_id": sessAlice}}, nil)
		expect(t, "hidden in the overlay", status, env, 404, "not_found")

		// Children; include narrows the answer.
		res = symbolResult{}
		e.post(tokenAlice, "symbol", map[string]any{"qualName": "pkg.T", "include": []string{"children"}}, &res)
		if len(res.Children) != 1 || res.Children[0].Name != "M" || res.Children[0].Location != "pkg/a.go:12" || res.Body != nil {
			t.Fatalf("children %+v", res)
		}
		res = symbolResult{}
		e.post(tokenAlice, "symbol", map[string]any{"qualName": "pkg.Alloc", "include": []string{"body"}}, &res)
		if res.Symbol.Doc != "Alloc allocates an identifier." || res.Body == nil {
			t.Fatalf("doc not included %+v", res)
		}

		for _, c := range []struct {
			what string
			body map[string]any
			want int
			kind string
		}{
			{"no key", map[string]any{}, 400, "invalid"},
			{"both keys", map[string]any{"id": "x", "qualName": "y"}, 400, "invalid"},
			{"bad include", map[string]any{"id": "x", "include": []string{"tests"}}, 400, "invalid"},
			{"unknown", map[string]any{"qualName": "pkg.Nope"}, 404, "not_found"},
			{"bad cursor", map[string]any{"qualName": "pkg.Alloc", "cursor": "nope"}, 400, "invalid"},
		} {
			status, env := e.post(tokenAlice, "symbol", c.body, nil)
			expect(t, c.what, status, env, c.want, c.kind)
		}
	})
}

func TestGraphSymbolBodyIsCappedAndContinues(t *testing.T) {
	e := newGraphEnv(t, memgraph.New())
	e.seed()
	var lines []string
	for i := 1; i <= 300; i++ {
		lines = append(lines, fmt.Sprintf("\tx%03d := compute(%d) // a line long enough to cost a few tokens", i, i))
	}
	content := "func Long() {\n" + strings.Join(lines, "\n") + "\n}"
	e.must(e.g.ApplyFile(e.ctx, codegraph.FileFacts{
		File:    codegraph.File{Layer: "base", Path: "pkg/long.go", SHA256: e.putBlob(content), Lang: "go"},
		Symbols: []codegraph.Symbol{sym("base", "pkg/long.go", "Long", codegraph.SymbolFunction, 1, 302, "", "")},
	}))
	var got []string
	cursor := ""
	for page := 0; ; page++ {
		if page > 50 {
			t.Fatal("no end")
		}
		var res symbolResult
		status, env := e.post(tokenAlice, "symbol", map[string]any{"qualName": "pkg.Long", "max_tokens": 500, "cursor": cursor}, &res)
		expect(t, "page", status, env, 200, "")
		if len(env.Result)/4 > 700 {
			t.Fatalf("page of %d bytes over a 500-token budget", len(env.Result))
		}
		got = append(got, res.Body.Text)
		if res.Next == "" {
			if page == 0 {
				t.Fatal("a 300-line body fit in 500 tokens")
			}
			break
		}
		if !res.Truncated {
			t.Fatal("next without truncated")
		}
		cursor = res.Next
	}
	if strings.Join(got, "\n") != content {
		t.Fatalf("pages do not add up to the body")
	}
	// A cursor of another route is refused.
	var res listResult
	e.post(tokenAlice, "search", map[string]any{"query": "Long"}, &res)
	status, env := e.post(tokenAlice, "symbol", map[string]any{"qualName": "pkg.Long", "cursor": cursorOf(t, e, "search")}, nil)
	expect(t, "foreign cursor", status, env, 400, "invalid")
}

// cursorOf returns a next cursor issued by route (a search cut to the
// smallest budget).
func cursorOf(t *testing.T, e *graphEnv, route string) string {
	t.Helper()
	for i := range 80 {
		e.must(e.g.ApplyFile(e.ctx, codegraph.FileFacts{
			File:    codegraph.File{Layer: "base", Path: fmt.Sprintf("gen/f%02d.go", i), Lang: "go"},
			Symbols: []codegraph.Symbol{sym("base", fmt.Sprintf("gen/f%02d.go", i), fmt.Sprintf("Helper%02d", i), codegraph.SymbolFunction, 1, 1, "helper number", "")},
		}))
	}
	var res listResult
	e.post(tokenAlice, route, map[string]any{"query": "helper", "max_tokens": 1, "limit": 80}, &res)
	if res.Next == "" {
		t.Fatalf("no cursor from %s: %+v", route, res)
	}
	return res.Next
}

func TestGraphSearchPages(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newGraphEnv(t, graphs)
		e.seed()
		for i := range 60 {
			p := fmt.Sprintf("gen/f%02d.go", i)
			e.must(e.g.ApplyFile(e.ctx, codegraph.FileFacts{
				File: codegraph.File{Layer: "base", Path: p, Lang: "go"},
				Symbols: []codegraph.Symbol{sym("base", p, fmt.Sprintf("Helper%02d", i), codegraph.SymbolFunction, 1, 2,
					"helper number "+fmt.Sprint(i)+" does a small thing", "")},
			}))
		}
		seen := map[string]bool{}
		cursor := ""
		pages := 0
		for {
			var res listResult
			status, env := e.post(tokenAlice, "search", map[string]any{"query": "helper", "limit": 60, "max_tokens": 400, "cursor": cursor}, &res)
			expect(t, "search page", status, env, 200, "")
			pages++
			for _, h := range res.hits(t) {
				if seen[h.ID] {
					t.Fatalf("%s twice", h.ID)
				}
				seen[h.ID] = true
			}
			if res.Next == "" {
				break
			}
			if !res.Truncated || pages > 60 {
				t.Fatalf("page %d: %+v", pages, res)
			}
			cursor = res.Next
		}
		if len(seen) != 60 || pages < 2 {
			t.Fatalf("%d hits over %d pages", len(seen), pages)
		}

		// Filters and limits.
		var res listResult
		e.post(tokenAlice, "search", map[string]any{"query": "helper", "paths": []string{"gen/f01.go"}}, &res)
		if hs := res.hits(t); len(hs) != 1 || hs[0].Name != "Helper01" {
			t.Fatalf("path filter %+v", hs)
		}
		res = listResult{}
		e.post(tokenAlice, "search", map[string]any{"query": "helper", "limit": 5}, &res)
		if len(res.Items) != 5 {
			t.Fatalf("limit 5: %d", len(res.Items))
		}
		for _, c := range []struct {
			what string
			body any
		}{
			{"no query", map[string]any{}},
			{"blank query", map[string]any{"query": "  "}},
			{"unknown field", map[string]any{"query": "x", "cypher": "MATCH (n) RETURN n"}},
			{"bad cursor", map[string]any{"query": "x", "cursor": "!!"}},
			{"empty filter", map[string]any{"query": "x", "kinds": []string{""}}},
			{"not json", "{"},
		} {
			status, env := e.post(tokenAlice, "search", c.body, nil)
			expect(t, c.what, status, env, 400, "invalid")
		}
	})
}

type neighborItem struct {
	From struct {
		Name string `json:"name"`
	} `json:"from"`
	To struct {
		Name string `json:"name"`
	} `json:"to"`
	Relation   string `json:"relation"`
	Location   string `json:"location"`
	Confidence string `json:"confidence"`
	Depth      int    `json:"depth"`
}

func TestGraphNeighborsAndOutline(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newGraphEnv(t, graphs)
		e.seed()

		var res listResult
		status, env := e.post(tokenAlice, "neighbors", map[string]any{"id": "pkg/a.go#pkg.Alloc", "relation": "callers"}, &res)
		expect(t, "callers", status, env, 200, "")
		if len(res.Items) != 1 {
			t.Fatalf("callers %s", env.Result)
		}
		var row neighborItem
		if err := json.Unmarshal(res.Items[0], &row); err != nil {
			t.Fatal(err)
		}
		if row.From.Name != "Caller" || row.To.Name != "Alloc" || row.Location != "pkg/a.go:8" || row.Confidence != "exact" || row.Depth != 1 {
			t.Fatalf("caller row %+v", row)
		}
		res = listResult{}
		e.post(tokenAlice, "neighbors", map[string]any{"id": "pkg/a.go#pkg.Caller", "relation": "callees"}, &res)
		if len(res.Items) != 1 {
			t.Fatalf("callees %+v", res)
		}
		for _, c := range []struct {
			what string
			body map[string]any
			want int
			kind string
		}{
			{"bad relation", map[string]any{"id": "x", "relation": "friends"}, 400, "invalid"},
			{"too deep", map[string]any{"id": "x", "relation": "callers", "depth": 4}, 400, "invalid"},
			{"no start", map[string]any{"relation": "callers"}, 400, "invalid"},
			{"bad path", map[string]any{"path": "../etc/passwd", "relation": "imports"}, 400, "invalid"},
		} {
			status, env := e.post(tokenAlice, "neighbors", c.body, nil)
			expect(t, c.what, status, env, c.want, c.kind)
		}

		var out struct {
			Status string `json:"status"`
			Files  []struct {
				Path    string `json:"path"`
				Package string `json:"package"`
			} `json:"files"`
			Items []struct {
				Name      string `json:"name"`
				Depth     int    `json:"depth"`
				Location  string `json:"location"`
				Signature string `json:"signature"`
			} `json:"items"`
		}
		status, env = e.post(tokenAlice, "outline", map[string]any{"path": "pkg/a.go"}, &out)
		expect(t, "outline", status, env, 200, "")
		var got []string
		for _, it := range out.Items {
			got = append(got, fmt.Sprintf("%d:%s@%s", it.Depth, it.Name, it.Location))
		}
		if len(out.Files) != 1 || out.Files[0].Path != "pkg/a.go" ||
			strings.Join(got, " ") != "0:Alloc@pkg/a.go:3-5 0:Caller@pkg/a.go:7-9 0:T@pkg/a.go:11-12 1:M@pkg/a.go:12" {
			t.Fatalf("outline %s", env.Result)
		}
		if strings.Contains(string(env.Result), "return 1") {
			t.Fatalf("outline carries a body: %s", env.Result)
		}
		status, env = e.post(tokenAlice, "outline", map[string]any{"package": "pkg", "view": map[string]any{"session_id": sessAlice}}, &out)
		expect(t, "package outline", status, env, 200, "")
		if len(out.Items) != 1 || out.Items[0].Name != "AllocNew" {
			t.Fatalf("alice's package outline %s", env.Result)
		}
		for _, c := range []struct {
			what string
			body map[string]any
			want int
			kind string
		}{
			{"unknown path", map[string]any{"path": "pkg/none.go"}, 404, "not_found"},
			{"bob's file", map[string]any{"path": "pkg/b.go", "view": map[string]any{"ref": "feature"}}, 404, "not_found"},
			{"path and package", map[string]any{"path": "pkg/a.go", "package": "pkg"}, 400, "invalid"},
			{"absolute path", map[string]any{"path": "/etc/passwd"}, 400, "invalid"},
		} {
			status, env := e.post(tokenAlice, "outline", c.body, nil)
			expect(t, c.what, status, env, c.want, c.kind)
		}
	})
}

type contextResult struct {
	Status    string            `json:"status"`
	Hits      []hit             `json:"hits"`
	Neighbors []neighborItem    `json:"neighbors"`
	Bodies    []json.RawMessage `json:"bodies"`
	Truncated bool              `json:"truncated"`
	Next      string            `json:"next"`
}

func TestGraphContext(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newGraphEnv(t, graphs)
		e.seed()

		var res contextResult
		status, env := e.post(tokenAlice, "context", map[string]any{"task": "Where is Alloc called and what does it return?"}, &res)
		expect(t, "context", status, env, 200, "")
		if len(res.Hits) == 0 || res.Hits[0].Name != "Alloc" {
			t.Fatalf("context hits %s", env.Result)
		}
		var callers []string
		for _, n := range res.Neighbors {
			if n.To.Name == "Alloc" {
				callers = append(callers, n.From.Name)
			}
		}
		if strings.Join(callers, ",") != "Caller" {
			t.Fatalf("context neighbours %s", env.Result)
		}
		if len(res.Bodies) == 0 || !strings.Contains(string(res.Bodies[0]), `return 1`) {
			t.Fatalf("context bodies %s", env.Result)
		}

		// A small budget leaves bodies for the next page.
		var lines []string
		for i := range 120 {
			lines = append(lines, fmt.Sprintf("\tstep%03d() // allocation work", i))
		}
		for i := range 4 {
			p := fmt.Sprintf("alloc/a%d.go", i)
			content := "func Allocate" + fmt.Sprint(i) + "() {\n" + strings.Join(lines, "\n") + "\n}"
			e.must(e.g.ApplyFile(e.ctx, codegraph.FileFacts{
				File:    codegraph.File{Layer: "base", Path: p, SHA256: e.putBlob(content), Lang: "go"},
				Symbols: []codegraph.Symbol{sym("base", p, "Allocate"+fmt.Sprint(i), codegraph.SymbolFunction, 1, 122, "allocate things", "")},
			}))
		}
		res = contextResult{}
		e.post(tokenAlice, "context", map[string]any{"task": "allocate", "budget": 1500}, &res)
		if !res.Truncated || res.Next == "" || len(res.Hits) == 0 {
			t.Fatalf("small budget %+v", res)
		}
		bodies := len(res.Bodies)
		for cursor, n := res.Next, 0; cursor != ""; n++ {
			if n > 20 {
				t.Fatal("no end")
			}
			var more contextResult
			status, env := e.post(tokenAlice, "context", map[string]any{"task": "allocate", "budget": 1500, "cursor": cursor}, &more)
			expect(t, "context page", status, env, 200, "")
			if len(more.Bodies) == 0 || len(more.Hits) != 0 {
				t.Fatalf("continuation %s", env.Result)
			}
			bodies += len(more.Bodies)
			cursor = more.Next
		}
		if bodies < 4 {
			t.Fatalf("%d bodies over the pages", bodies)
		}

		status, env = e.post(tokenAlice, "context", map[string]any{"task": ""}, nil)
		expect(t, "no task", status, env, 400, "invalid")
	})
}

// failingGraphs opens graphs whose every call fails as unavailable.
type failingGraphs struct{ openErr bool }

func (f failingGraphs) Open(_ context.Context, key string) (codegraph.Graph, error) {
	if f.openErr {
		return nil, fmt.Errorf("%w: connection refused", codegraph.ErrUnavailable)
	}
	g, _ := memgraph.New().Open(context.Background(), key)
	return failingGraph{g}, nil
}
func (failingGraphs) Close() error { return nil }

type failingGraph struct{ codegraph.Graph }

func (failingGraph) Layers(context.Context) ([]codegraph.Layer, error) {
	return nil, fmt.Errorf("%w: read timed out", codegraph.ErrUnavailable)
}

func TestGraphUnavailableAndIndexing(t *testing.T) {
	t.Run("unavailable", func(t *testing.T) {
		for _, graphs := range []codegraph.Opener{failingGraphs{openErr: true}, failingGraphs{}} {
			e := newGraphEnv(t, memgraph.New())
			c := server.NewClued(server.CluedOptions{
				Tokens: testTokens, Sessions: memsession.New(),
				Open:      func(_ context.Context, key string) (store.Store, error) { return memstore.New(key), nil },
				Namespace: func(auth.Principal, string) string { return e.key },
				Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
				Code:      e.stores.Open,
				Graph:     &codeapi.GraphOptions{Graphs: graphs},
			})
			t.Cleanup(c.Close)
			ts := httptest.NewServer(c.Handler)
			t.Cleanup(ts.Close)
			for _, route := range []string{"search", "symbol", "neighbors", "outline", "context"} {
				var body map[string]any
				switch route {
				case "search":
					body = map[string]any{"query": "x"}
				case "symbol":
					body = map[string]any{"id": "x"}
				case "neighbors":
					body = map[string]any{"id": "x", "relation": "callers"}
				case "outline":
					body = map[string]any{"path": "a.go"}
				case "context":
					body = map[string]any{"task": "x"}
				}
				status, env := call(t, ts.URL, tokenAlice, "POST", graphPath(route), body)
				expect(t, route, status, env, 503, codeapi.KindUnavailable)
			}
		}
	})

	t.Run("indexing", func(t *testing.T) {
		e := newGraphEnv(t, memgraph.New())
		var res listResult
		e.post(tokenAlice, "search", map[string]any{"query": "Alloc"}, &res)
		if res.Status != codeapi.StatusEmpty || len(res.Items) != 0 {
			t.Fatalf("nothing collected: %+v", res)
		}
		// Collected but not indexed: indexing with progress, no results.
		call(t, e.ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha("a")), "a")
		call(t, e.ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
		status, env := call(t, e.ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"), changeBody(cleanEntry("a.go", "a")))
		expect(t, "commit", status, env, 200, "")
		res = listResult{}
		e.post(tokenAlice, "search", map[string]any{"query": "Alloc"}, &res)
		if res.Status != codeapi.StatusIndexing || res.Progress == nil || res.Progress.Pending != 1 || len(res.Items) != 0 {
			t.Fatalf("not indexed: %+v", res)
		}
		var cres contextResult
		status, env = e.post(tokenAlice, "context", map[string]any{"task": "Alloc"}, &cres)
		expect(t, "context while indexing", status, env, 200, "")
		if cres.Status != codeapi.StatusIndexing {
			t.Fatalf("context while indexing %s", env.Result)
		}
		// Indexed but behind: results with an indexing status.
		e.seed()
		e.mu.Lock()
		e.indexed[e.key] = 0
		e.mu.Unlock()
		res = listResult{}
		e.post(tokenAlice, "search", map[string]any{"query": "Alloc"}, &res)
		if res.Status != codeapi.StatusIndexing || len(res.Items) == 0 {
			t.Fatalf("behind: %+v", res)
		}
		e.mu.Lock()
		e.indexed[e.key] = 1
		e.mu.Unlock()
		res = listResult{}
		e.post(tokenAlice, "search", map[string]any{"query": "Alloc"}, &res)
		if res.Status != codeapi.StatusReady || res.Progress != nil {
			t.Fatalf("caught up: %+v", res)
		}
	})
}

func TestGraphRoutesAreAuthenticatedAndOptIn(t *testing.T) {
	e := newGraphEnv(t, memgraph.New())
	e.seed()
	status, env := call(t, e.ts.URL, "", "POST", graphPath("search"), map[string]any{"query": "Alloc"})
	expect(t, "no token", status, env, 401, "unauthorized")
	status, env = call(t, e.ts.URL, tokenOnlyA, "POST", graphPath("search"), map[string]any{"query": "Alloc"})
	expect(t, "project not allowed", status, env, 403, "forbidden")
	status, env = call(t, e.ts.URL, tokenAlice, "GET", graphPath("search"), nil)
	expect(t, "GET", status, env, 404, "not_found")
	status, env = call(t, e.ts.URL, tokenAlice, "POST", graphPath("cypher"), map[string]any{"query": "MATCH (n) RETURN n"})
	expect(t, "no raw query route", status, env, 404, "not_found")

	// Without Graph the graph routes are not served.
	st := newStores()
	c := server.NewClued(server.CluedOptions{
		Tokens: testTokens, Sessions: memsession.New(),
		Open:   func(_ context.Context, key string) (store.Store, error) { return memstore.New(key), nil },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Code:   st.Open,
	})
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	for _, route := range []string{"search", "symbol", "neighbors", "outline", "context"} {
		status, env := call(t, ts.URL, tokenAlice, "POST", graphPath(route), map[string]any{})
		expect(t, "no graph "+route, status, env, 404, "not_found")
	}
}

// goParse is a small parser for the end-to-end test: "package p" names the
// package, and "func Name(" opens a function that runs to the next line
// that is "}".
func goParse(_ string, content []byte) (codeparse.Facts, error) {
	f := codeparse.Facts{Lang: "go"}
	lines := strings.Split(string(content), "\n")
	offset := 0
	offsets := make([]int, len(lines))
	for i, l := range lines {
		offsets[i] = offset
		offset += len(l) + 1
	}
	for i, l := range lines {
		if p, ok := strings.CutPrefix(l, "package "); ok {
			f.Package = p
		}
		rest, ok := strings.CutPrefix(l, "func ")
		if !ok {
			continue
		}
		name, _, _ := strings.Cut(rest, "(")
		end := i
		for end < len(lines)-1 && lines[end] != "}" {
			end++
		}
		qual := f.Package + "." + name
		f.Symbols = append(f.Symbols, codeparse.Symbol{ID: codeparse.SymbolID(qual, 1), Kind: codeparse.KindFunction, Name: name,
			QualName: qual, Signature: l, Doc: name + " is documented.", Exported: true,
			Start: codeparse.Pos{Line: i + 1, Offset: offsets[i]},
			End:   codeparse.Pos{Line: end + 1, Column: len(lines[end]), Offset: offsets[end] + len(lines[end])}})
	}
	return f, nil
}

func TestGraphEndToEndWithTheIndexer(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newGraphEnv(t, graphs)
		cursors := codeindex.NewMemCursors(nil)
		svc, err := codeindex.New(codeindex.Options{
			Code:    e.stores.Open,
			Graphs:  graphs,
			Cursors: cursors,
			Apply:   codeindex.FileApplier{},
			Parse:   goParse,
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		e.must(err)
		pushed := "package pkg\n\nfunc Pushed() int {\n\treturn 1\n}\n"
		local := "package pkg\n\nfunc LocalEdit() int {\n\treturn 2\n}\n"
		for _, c := range []string{pushed, local} {
			status, env := call(t, e.ts.URL, tokenAlice, "PUT", codePath("blobs/"+sha(c)), c)
			expect(t, "blob", status, env, 200, "")
		}
		status, env := call(t, e.ts.URL, tokenAlice, "PUT", codePath("worktrees/"+wt1), worktreeBody())
		expect(t, "worktree", status, env, 200, "")
		dirty := cleanEntry("pkg/local.go", local)
		dirty["dirty"], dirty["git_oid"] = true, oid2
		status, env = call(t, e.ts.URL, tokenAlice, "POST", codePath("worktrees/"+wt1+"/changes"),
			changeBody(cleanEntry("pkg/pushed.go", pushed), dirty))
		expect(t, "commit", status, env, 200, "")

		res, err := svc.Sync(e.ctx, e.key)
		e.must(err)
		e.mu.Lock()
		e.indexed[e.key] = res.Seq
		e.mu.Unlock()

		// The base holds the pushed file; the session's view adds its edit.
		var lr listResult
		e.post(tokenAlice, "search", map[string]any{"query": "Pushed"}, &lr)
		if hs := lr.hits(t); lr.Status != codeapi.StatusReady || len(hs) != 1 || hs[0].Location != "pkg/pushed.go:3-5" {
			t.Fatalf("base search %+v", lr)
		}
		lr = listResult{}
		e.post(tokenAlice, "search", map[string]any{"query": "LocalEdit"}, &lr)
		if len(lr.Items) != 0 {
			t.Fatalf("local edit in the base: %+v", lr)
		}
		lr = listResult{}
		e.post(tokenAlice, "search", map[string]any{"query": "LocalEdit", "view": map[string]any{"session_id": "sess-1"}}, &lr)
		if !lr.View.LocalEdits || len(lr.Items) != 1 {
			t.Fatalf("session search %+v", lr)
		}
		var sr symbolResult
		e.post(tokenAlice, "symbol", map[string]any{"qualName": "pkg.LocalEdit", "view": map[string]any{"session_id": "sess-1"}}, &sr)
		if sr.Body == nil || sr.Body.Text != "func LocalEdit() int {\n\treturn 2\n}" {
			t.Fatalf("session body %+v", sr)
		}
		// Bob has no session there.
		status, env = e.post(tokenBob, "search", map[string]any{"query": "LocalEdit", "view": map[string]any{"session_id": "sess-1"}}, nil)
		expect(t, "bob with alice's session", status, env, 404, "not_found")
	})
}
