package codeindex

import (
	"context"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// coreResolver is the Go resolver with other rules and a hook that adds to
// each file's scope, to drive the engine's generic steps over goSrc facts.
type coreResolver struct {
	resolvers.Resolver
	rules resolvers.Rules
	scope func(v resolvers.View, f *resolvers.File, s *resolvers.Scope)
}

func (c coreResolver) Rules() resolvers.Rules { return c.rules }

func (c coreResolver) Scope(ctx context.Context, v resolvers.View, f *resolvers.File) (resolvers.Scope, error) {
	s, err := c.Resolver.Scope(ctx, v, f)
	if err == nil && c.scope != nil {
		c.scope(v, f, &s)
	}
	return s, err
}

// newCoreResolver is a Resolver whose go family is res.
func newCoreResolver(t *testing.T, res coreResolver) *Resolver {
	t.Helper()
	goRes, ok := resolvers.Lookup(codeparse.FamilyGo)
	if !ok {
		t.Fatal("no go resolver")
	}
	res.Resolver = goRes
	r := NewResolver(ResolverOptions{})
	r.lookup = func(family string) (resolvers.Resolver, bool) {
		if family == codeparse.FamilyGo {
			return res, true
		}
		return resolvers.Lookup(family)
	}
	return r
}

// sym appends a definition built by hand (a receiver's, a namespace's).
func (g *goSrc) sym(s codeparse.Symbol) *goSrc {
	p := g.pos()
	s.Start, s.End = p, p
	g.f.Symbols = append(g.f.Symbols, s)
	return g
}

func TestResolveCoreSteps(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		var mu sync.Mutex
		importOnly := -1 // files of the import-only package a/a.go's scope saw
		r := newCoreResolver(t, coreResolver{
			rules: resolvers.Rules{Separators: []string{"."}, MemberFallback: true, ImportMembers: true,
				ReceiverMembers: true, AbsoluteQualifiers: true, CaseInsensitive: true},
			scope: func(v resolvers.View, f *resolvers.File, s *resolvers.Scope) {
				if f.Path != "a/a.go" {
					return
				}
				mu.Lock()
				importOnly = len(v.Package(codeparse.FamilyGo, "example.com/m/imp"))
				mu.Unlock()
				s.AddQualifier("deep", "example.com/m/deep")                 // a short binding
				s.AddQualifier("deep.sub.pkg", "example.com/m/deep/sub/pkg") // and a longer one: it wins
				s.AddRenamed("Alias", "example.com/m/b", "Do")               // import {Do as Alias}
				s.AddRenamed("Srv", "example.com/m/b", "Server")             // import {Server as Srv}
			},
		})
		e := newResolveEnv(t, graphs, r)
		m := wtID(1)
		e.worktree(m, "0123456789abcdef", "", "main")

		recv := goFile("ext").sym(codeparse.Symbol{ID: "ext.String.Shout", Kind: codeparse.KindFunction, Name: "Shout",
			QualName: "ext.String.Shout", Exported: true}) // fun String.Shout(): no parent, a receiver
		ns := goFile("nsp").sym(codeparse.Symbol{ID: "nsp.N", Kind: codeparse.KindNamespace, Name: "N", QualName: "nsp.N", Exported: true}).
			sym(codeparse.Symbol{ID: "nsp.N.InNs", Kind: codeparse.KindFunction, Name: "InNs", QualName: "nsp.N.InNs", Parent: "nsp.N", Exported: true})
		a := goFile("a").
			imp("example.com/m/b", "").imp("example.com/m/mod", "").imp("example.com/m/nsp", "").
			fn("Run").def(codeparse.KindStruct, "T").def(codeparse.KindField, "T.Customer").fn("T.Use").
			def(codeparse.KindStruct, "S").
			call("Run", "deep.sub.pkg.F").               // (b) the longest bound prefix
			call("Run", "deep.sub.pkg.K.M").             // (b) and the rest qualifies
			call("Run", "mod.F").                        // (c) a member of the exported table
			call("Run", "MOD.F").                        // (m) the qualifier in another case
			call("Run", "Alias").                        // (g) a renamed import
			call("Run", "Srv.Serve").                    // (g) a renamed qualifier
			call("Run", "s.Shout").                      // (h) a receiver's definition as a member
			call("Run", "x.X.Deep").                     // (o) a whole qualified name
			call("Run", "nsp.InNs").                     // (n) inside a namespace
			ref(codeparse.RefType, "T.Use", "Customer"). // (d) the type, not the field
			ref(codeparse.RefType, "Run", "P").          // (e) the parts of one type
			ref(codeparse.RefExtends, "S", "I")          // (l) a struct conforming to an interface
		tree := map[string]string{
			"go.mod":            "module example.com/m\n",
			"a/a.go":            a.src(),
			"a/customer.go":     goFile("a").def(codeparse.KindStruct, "Customer").def(codeparse.KindInterface, "I").src(),
			"a/p1.go":           goFile("a").def(codeparse.KindStruct, "P").def(codeparse.KindMethod, "P.A").call("P.A", "B").src(),
			"a/p2.go":           goFile("a").def(codeparse.KindStruct, "P").def(codeparse.KindMethod, "P.B").src(),
			"imp/i.go":          goFile("imp").imp("example.com/m/b", "").src(), // (a) imports only
			"b/b.go":            goFile("b").fn("Do").def(codeparse.KindStruct, "Server").def(codeparse.KindMethod, "Server.Serve").src(),
			"deep/d.go":         goFile("deep").fn("F").src(),
			"deep/sub/pkg/s.go": goFile("pkg").fn("F").def(codeparse.KindStruct, "K").def(codeparse.KindMethod, "K.M").src(),
			"mod/mod.go":        goFile("mod").def(codeparse.KindVar, "M").def(codeparse.KindFunction, "M.F").src(),
			"ext/ext.go":        recv.src(),
			"nsp/n.go":          ns.src(),
			"x/x.go":            goFile("x").def(codeparse.KindStruct, "X").def(codeparse.KindMethod, "X.Deep").src(),
		}
		e.commitTree(m, "main", commitID("main"), tree)
		e.sync()
		base := codegraph.BaseView()

		mu.Lock()
		if importOnly != 1 {
			t.Errorf("the view holds %d files of the import-only package, want 1", importOnly)
		}
		mu.Unlock()
		wantEdges(t, "Run", e.callees(base, "a/a.go", "a.Run"), map[string]string{
			"deep/sub/pkg/s.go#pkg.F":     "base/exact",
			"deep/sub/pkg/s.go#pkg.K.M":   "base/qualified",
			"mod/mod.go#mod.M.F":          "base/qualified",
			"b/b.go#b.Do":                 "base/exact",
			"b/b.go#b.Server.Serve":       "base/qualified",
			"ext/ext.go#ext.String.Shout": "base/name",
			"x/x.go#x.X.Deep":             "base/exact",
			"nsp/n.go#nsp.N.InNs":         "base/exact",
		})
		if got := e.referrers(base, "a/customer.go", "a.Customer"); len(got) != 1 || got[0] != "a/a.go#a.T.Use exact" {
			t.Errorf("referrers of the struct Customer = %v, want T.Use exact", got)
		}
		if got := e.referrers(base, "a/a.go", "a.T.Customer"); len(got) != 0 {
			t.Errorf("the field Customer is referenced as a type: %v", got)
		}
		if got := e.referrers(base, "a/p1.go", "a.P"); len(got) != 1 || got[0] != "a/a.go#a.Run exact" {
			t.Errorf("referrers of the parts of P = %v, want Run exact (the first part)", got)
		}
		wantEdges(t, "P.A", e.callees(base, "a/p1.go", "a.P.A"), map[string]string{"a/p2.go#a.P.B": "base/exact"})
		rows, err := e.g.Neighbors(e.ctx, base, codegraph.NeighborQuery{ID: SymbolNodeID("a/customer.go", "a.I"), Relation: codegraph.RelImplementations})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Type != codegraph.EdgeImplements || rows[0].From.ID != SymbolNodeID("a/a.go", "a.S") {
			t.Errorf("implementations of I = %+v, want S IMPLEMENTS", rows)
		}
	})
}

// TestResolveCoreFlagsOff: without ImportMembers, ReceiverMembers and
// AbsoluteQualifiers the engine keeps its earlier answers.
func TestResolveCoreFlagsOff(t *testing.T) {
	r := newCoreResolver(t, coreResolver{rules: resolvers.Rules{Separators: []string{"."}, MemberFallback: true}})
	e := newResolveEnv(t, memgraph.New(), r)
	m := wtID(1)
	e.worktree(m, "0123456789abcdef", "", "main")
	a := goFile("a").imp("example.com/m/mod", "").fn("Run").
		call("Run", "mod.F").call("Run", "MOD.G").call("Run", "s.Shout").call("Run", "x.X.Deep")
	e.commitTree(m, "main", commitID("main"), map[string]string{
		"go.mod":     "module example.com/m\n",
		"a/a.go":     a.src(),
		"mod/mod.go": goFile("mod").def(codeparse.KindVar, "M").def(codeparse.KindFunction, "M.F").def(codeparse.KindFunction, "M.G").src(),
		"ext/ext.go": goFile("ext").sym(codeparse.Symbol{ID: "ext.String.Shout", Kind: codeparse.KindFunction, Name: "Shout",
			QualName: "ext.String.Shout", Exported: true}).src(),
		"x/x.go": goFile("x").def(codeparse.KindStruct, "X").def(codeparse.KindMethod, "X.Deep").src(),
	})
	e.sync()
	wantEdges(t, "Run", e.callees(codegraph.BaseView(), "a/a.go", "a.Run"), map[string]string{
		"mod/mod.go#mod.M.F": "base/name",      // a member through the import, by name
		"mod/mod.go#mod.M.G": "base/name",      // MOD is not mod: G is any member of that name
		"x/x.go#x.X.Deep":    "base/qualified", // the qualified name's suffix
	})
}
