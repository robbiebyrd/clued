package resolvers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// golangCallees indexes files (path → content; .go files are Go) with the
// real Go extractor and the resolution engine, and returns the callees of
// path#id as "path#id confidence".
func golangCallees(t *testing.T, files map[string]string) func(path, id string) []string {
	t.Helper()
	ctx := context.Background()
	const project = "u0123456789abcdef__go-resolver"
	store := memcode.New()
	graphs := memgraph.New()
	eng, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{Profiles: eng.Profiles()}),
		Parse:    codeparse.Parse,
		Holder:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	head := hex.EncodeToString(sha256.New().Sum(nil))[:40]
	ch := code.Change{Worktree: wt, Ref: "main", Head: head, Upstream: head, Full: true}
	for p, content := range files {
		lang := ""
		if len(p) > 3 && p[len(p)-3:] == ".go" {
			lang = "go"
		}
		sum := sha256.Sum256([]byte(content))
		sha := hex.EncodeToString(sum[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(content)), Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(content)), GitOID: sha[:40], Mode: "100644", Lang: lang})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, project); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, project)
	if err != nil {
		t.Fatal(err)
	}
	return func(p, id string) []string {
		t.Helper()
		rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, id), Relation: codegraph.RelCallees})
		if err != nil {
			t.Fatalf("callees of %s#%s: %v", p, id, err)
		}
		out := []string{}
		for _, r := range rows {
			out = append(out, r.To.ID+" "+r.Confidence)
		}
		sort.Strings(out)
		return out
	}
}

func TestResolve_golang_Rules(t *testing.T) {
	_, res, ok := resolvers.ForLang("go")
	if !ok {
		t.Fatal("no resolver for go")
	}
	if r := res.Rules(); r.NameFallback || !r.MemberFallback || !r.ReceiverMembers || r.ImportMembers || r.AbsoluteQualifiers {
		t.Errorf("rules = %+v", r)
	}
}

// TestResolve_golang_ReceiverMembers: a method declared in another file
// than its type (no parent in its file's facts) is a member of the type
// for a call through a receiver, in the package and through an import.
func TestResolve_golang_ReceiverMembers(t *testing.T) {
	callees := golangCallees(t, map[string]string{
		"go.mod":           "module example.com/m\n",
		"srv/types.go":     "package srv\n\ntype Server struct{}\n",
		"srv/serve.go":     "package srv\n\nfunc (s *Server) Serve() {}\n\nfunc (s *Server) stop() {}\n",
		"srv/run.go":       "package srv\n\nfunc Run(s *Server) {\n\ts.Serve()\n\ts.stop()\n}\n",
		"app/main.go":      "package main\n\nimport \"example.com/m/srv\"\n\nfunc main() {\n\tvar s srv.Server\n\ts.Serve()\n\ts.stop()\n}\n",
	})
	if got, want := callees("srv/run.go", "srv.Run"), []string{
		"srv/serve.go#srv.Server.Serve name",
		"srv/serve.go#srv.Server.stop name",
	}; !reflect.DeepEqual(got, want) {
		t.Errorf("Run:\n got %v\nwant %v", got, want)
	}
	// From another package only the exported method.
	if got, want := callees("app/main.go", "main.main"), []string{"srv/serve.go#srv.Server.Serve name"}; !reflect.DeepEqual(got, want) {
		t.Errorf("main:\n got %v\nwant %v", got, want)
	}
}
