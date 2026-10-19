package codeindex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// goSrc builds the facts the Go extractor gives for one file, by hand: a
// file's content is its facts as JSON, which jsonParse reads back.
type goSrc struct {
	f    codeparse.Facts
	line int
}

func goFile(pkg string) *goSrc { return &goSrc{f: codeparse.Facts{Lang: "go", Package: pkg}} }

func (g *goSrc) pos() codeparse.Pos {
	g.line++
	return codeparse.Pos{Line: g.line, Offset: g.line * 100}
}

// id is the file-local id of a definition named by its dotted path below
// the package (Run, Server.Serve).
func (g *goSrc) id(local string) string { return codeparse.SymbolID(g.f.Package+"."+local, 1) }

func (g *goSrc) imp(spec, alias string) *goSrc {
	g.f.Imports = append(g.f.Imports, codeparse.Import{Spec: spec, Alias: alias, Start: g.pos()})
	return g
}

// def adds a definition; local is Name or Parent.Name.
func (g *goSrc) def(kind, local string) *goSrc {
	name, parent := local, ""
	if i := strings.LastIndex(local, "."); i >= 0 {
		name, parent = local[i+1:], g.id(local[:i])
	}
	p := g.pos()
	g.f.Symbols = append(g.f.Symbols, codeparse.Symbol{ID: g.id(local), Kind: kind, Name: name,
		QualName: g.f.Package + "." + local, Start: p, End: p, Parent: parent, Exported: unicode.IsUpper(rune(name[0]))})
	return g
}

func (g *goSrc) fn(local string) *goSrc { return g.def(codeparse.KindFunction, local) }

// ref adds a reference from the definition enclosing (local, "" for file
// level) to target, a name with its qualifier (b.Do, s.x.Serve, helper).
func (g *goSrc) ref(kind, enclosing, target string) *goSrc {
	qual, name := "", target
	if i := strings.LastIndex(target, "."); i >= 0 {
		qual, name = target[:i], target[i+1:]
	}
	r := codeparse.Ref{Name: name, Qualifier: qual, Kind: kind, Start: g.pos()}
	if enclosing != "" {
		r.Enclosing = g.id(enclosing)
	}
	g.f.Refs = append(g.f.Refs, r)
	return g
}

func (g *goSrc) call(enclosing, target string) *goSrc {
	return g.ref(codeparse.RefCall, enclosing, target)
}

func (g *goSrc) src() string {
	b, err := json.Marshal(g.f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// jsonParse reads the facts a goSrc wrote; other files have no grammar.
func jsonParse(path string, content []byte) (codeparse.Facts, error) {
	if !strings.HasSuffix(path, ".go") {
		return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
	}
	var f codeparse.Facts
	err := json.Unmarshal(content, &f)
	return f, err
}

// newResolveEnv is an applyEnv whose Service resolves with r.
func newResolveEnv(t *testing.T, graphs codegraph.Opener, r *Resolver) *applyEnv {
	t.Helper()
	return newApplyEnv(t, graphs, func(o *Options) {
		o.Apply = r
		o.Parse = jsonParse
	})
}

// commitTree records a full, clean checkout of ref at commit in worktree
// wt (a map of path → content).
func (e *applyEnv) commitTree(wt, ref, commit string, tree map[string]string) {
	e.t.Helper()
	ch := code.Change{Worktree: wt, Ref: ref, Head: commit, Upstream: commit, Full: true}
	var contents []string
	for p, c := range tree {
		ch.Upserts = append(ch.Upserts, clean(p, c))
		contents = append(contents, c)
	}
	e.commit(ch, contents...)
}

// edges returns the outgoing edges of rel (callees, or references when
// rel is references, read from the targets) of the symbol path#id visible
// in view, as target → layer/confidence/count.
func (e *applyEnv) callees(view codegraph.View, path, id string) map[string]string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, view, codegraph.NeighborQuery{ID: SymbolNodeID(path, id), Relation: codegraph.RelCallees})
	if err != nil {
		e.t.Fatalf("callees of %s#%s: %v", path, id, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.To.ID] = r.To.Layer + "/" + r.Confidence
	}
	return out
}

// referrers returns who REFERENCES the symbol path#id in view.
func (e *applyEnv) referrers(view codegraph.View, path, id string) []string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, view, codegraph.NeighborQuery{ID: SymbolNodeID(path, id), Relation: codegraph.RelReferences})
	if err != nil {
		e.t.Fatalf("references of %s#%s: %v", path, id, err)
	}
	var out []string
	for _, r := range rows {
		out = append(out, r.From.ID+" "+r.Confidence)
	}
	sort.Strings(out)
	return out
}

func wantEdges(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

func TestResolveGo(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		r := NewResolver(ResolverOptions{})
		e := newResolveEnv(t, graphs, r)
		m := wtID(1)
		e.worktree(m, "0123456789abcdef", "", "main")
		a := goFile("a").
			imp("fmt", "").imp("example.com/m/b", "").imp("example.com/m/b", "bb").
			imp("example.com/m/named", "").imp("example.com/sub/x", "").
			fn("Run").fn("Local").def(codeparse.KindStruct, "T").def(codeparse.KindMethod, "T.M").
			fn("Run.inner").
			call("Run", "Local").         // the file
			call("Run", "helper").        // the package, another file
			call("Run", "fmt.Println").   // an import outside the project
			call("Run", "b.Do").          // an import
			call("Run", "bb.Do").         // the same, through an alias
			call("Run", "b.hidden").      // unexported in another package
			call("Run", "s.Serve").       // a member through a receiver
			call("Run", "pkgname.Hello"). // the package clause names the import
			call("Run", "named.Hello").   // not the directory
			call("Run", "x.X").           // a nested module
			call("Run", "T.M").           // a method expression
			call("Run", "dup").           // two definitions in the package
			call("Run", "inner").         // the enclosing scope beats the package
			call("Run", "nowhere").
			ref(codeparse.RefType, "Run", "b.Server").
			call("T.M", "Local").
			call("", "Local") // file level: no source
		tree := map[string]string{
			"go.mod":       "module example.com/m // the module\n\ngo 1.26\n",
			"a/a.go":       a.src(),
			"a/a2.go":      goFile("a").fn("helper").fn("dup").fn("inner").src(),
			"a/a3.go":      goFile("a").fn("dup").src(),
			"a/a_test.go":  goFile("a_test").imp("example.com/m/a", "").fn("TestRun").call("TestRun", "a.Run").call("TestRun", "helper").src(),
			"b/b.go":       goFile("b").fn("Do").fn("hidden").def(codeparse.KindStruct, "Server").def(codeparse.KindMethod, "Server.Serve").src(),
			"named/n.go":   goFile("pkgname").fn("Hello").src(),
			"c/c.go":       goFile("c").imp("example.com/m/b", ".").fn("C").call("C", "Do").ref(codeparse.RefType, "C", "Server").call("C", "Server.Serve").src(),
			"sub/go.mod":   "module \"example.com/sub\"\n",
			"sub/x/x.go":   goFile("x").fn("X").src(),
			"README.md":    "# m\n",
			"nomod/z/z.go": goFile("z").fn("Z").src(),
		}
		e.commitTree(m, "main", commitID("main"), tree)
		e.sync()

		base := codegraph.BaseView()
		wantEdges(t, "Run", e.callees(base, "a/a.go", "a.Run"), map[string]string{
			"a/a.go#a.Local":           "base/exact",
			"a/a2.go#a.helper":         "base/exact",
			"b/b.go#b.Do":              "base/exact",
			"b/b.go#b.Server.Serve":    "base/name",
			"named/n.go#pkgname.Hello": "base/exact",
			"sub/x/x.go#x.X":           "base/exact",
			"a/a.go#a.T.M":             "base/qualified",
			"a/a2.go#a.dup":            "base/name",
			"a/a.go#a.Run.inner":       "base/exact",
		})
		wantEdges(t, "T.M", e.callees(base, "a/a.go", "a.T.M"), map[string]string{"a/a.go#a.Local": "base/exact"})
		wantEdges(t, "TestRun", e.callees(base, "a/a_test.go", "a_test.TestRun"), map[string]string{"a/a.go#a.Run": "base/exact"})
		wantEdges(t, "C", e.callees(base, "c/c.go", "c.C"), map[string]string{
			"b/b.go#b.Do": "base/exact", "b/b.go#b.Server.Serve": "base/qualified"})
		if got, want := e.referrers(base, "b/b.go", "b.Server"), []string{"a/a.go#a.Run exact", "c/c.go#c.C exact"}; !reflect.DeepEqual(got, want) {
			t.Errorf("referrers of b.Server = %v, want %v", got, want)
		}

		// Count and first line of a merged edge (b.Do and bb.Do).
		rows, err := e.g.Neighbors(e.ctx, base, codegraph.NeighborQuery{ID: SymbolNodeID("b/b.go", "b.Do"), Relation: codegraph.RelCallers})
		if err != nil {
			t.Fatal(err)
		}
		lines := map[string]int{}
		for _, row := range rows {
			lines[row.From.ID] = row.Line
		}
		if !reflect.DeepEqual(lines, map[string]int{"a/a.go#a.Run": 14, "c/c.go#c.C": 3}) {
			t.Errorf("callers of b.Do = %v", lines)
		}
	})
}

func TestResolveOverlayReResolvesDependents(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		e := newResolveEnv(t, graphs, NewResolver(ResolverOptions{}))
		m, y := wtID(1), wtID(2)
		e.worktree(m, "0123456789abcdef", "", "main")
		e.worktree(y, "0123456789abcdef", "", "main")
		a1 := goFile("p").fn("Foo").src()
		c1 := goFile("p").fn("C").fn("D").call("C", "Foo").call("D", "Bar").src()
		u := commitID("main")
		e.commitTree(m, "main", u, map[string]string{"go.mod": "module example.com/p\n", "a.go": a1, "c.go": c1})
		e.sync()
		base, ov := codegraph.BaseView(), e.view("base", codegraph.WorktreeLayerID(y, "main"))
		ovID := codegraph.WorktreeLayerID(y, "main")
		wantEdges(t, "base C", e.callees(base, "c.go", "p.C"), map[string]string{"a.go#p.Foo": "base/exact"})
		wantEdges(t, "base D", e.callees(base, "c.go", "p.D"), map[string]string{})

		// y edits a.go: Foo changes and Bar appears; y adds n.go calling Foo.
		a2 := goFile("p").fn("Bar").fn("Foo").src()
		n := goFile("p").fn("N").call("N", "Foo").src()
		e.commit(code.Change{Worktree: y, Ref: "main", Head: u, Upstream: u, Full: true, SessionID: "s1",
			Upserts: []code.Entry{clean("go.mod", "module example.com/p\n"), dirty("a.go", a2, a1), clean("c.go", c1), dirty("n.go", n, "")}}, a2, n)
		e.sync()
		wantEdges(t, "base C after y", e.callees(base, "c.go", "p.C"), map[string]string{"a.go#p.Foo": "base/exact"})
		wantEdges(t, "base D after y", e.callees(base, "c.go", "p.D"), map[string]string{})
		wantEdges(t, "y's C", e.callees(ov, "c.go", "p.C"), map[string]string{"a.go#p.Foo": ovID + "/exact"})
		wantEdges(t, "y's D", e.callees(ov, "c.go", "p.D"), map[string]string{"a.go#p.Bar": ovID + "/exact"})
		wantEdges(t, "y's N", e.callees(ov, "n.go", "p.N"), map[string]string{"a.go#p.Foo": ovID + "/exact"})

		// y renames Foo to Baz: the base caller stays unresolved in y's view.
		a3 := goFile("p").fn("Bar").fn("Baz").src()
		e.commit(code.Change{Worktree: y, Ref: "main", Head: u, Upstream: u, Full: true, SessionID: "s1",
			Upserts: []code.Entry{clean("go.mod", "module example.com/p\n"), dirty("a.go", a3, a1), clean("c.go", c1), dirty("n.go", n, "")}}, a3)
		e.sync()
		wantEdges(t, "y's C after the rename", e.callees(ov, "c.go", "p.C"), map[string]string{})
		wantEdges(t, "y's D after the rename", e.callees(ov, "c.go", "p.D"), map[string]string{"a.go#p.Bar": ovID + "/exact"})
		wantEdges(t, "y's N after the rename", e.callees(ov, "n.go", "p.N"), map[string]string{})
		wantEdges(t, "base C after the rename", e.callees(base, "c.go", "p.C"), map[string]string{"a.go#p.Foo": "base/exact"})

	})
}

func TestResolveDependentsAreBoundedPerBatch(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		r := NewResolver(ResolverOptions{MaxRefs: 2})
		e := newResolveEnv(t, graphs, r)
		m := wtID(1)
		e.worktree(m, "0123456789abcdef", "", "main")
		tree := map[string]string{"a.go": goFile("p").fn("A").src()}
		for i := 1; i <= 5; i++ {
			c := fmt.Sprintf("C%d", i)
			tree[strings.ToLower(c)+".go"] = goFile("p").fn(c).call(c, "Bar").src()
		}
		e.commitTree(m, "main", commitID("main1"), tree)
		e.sync()
		callers := func() int {
			t.Helper()
			rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: SymbolNodeID("a.go", "p.Bar"), Relation: codegraph.RelCallers})
			if err != nil {
				t.Fatal(err)
			}
			return len(rows)
		}

		// Bar appears: five callers to re-resolve, two per batch.
		tree["a.go"] = goFile("p").fn("A").fn("Bar").src()
		e.commitTree(m, "main", commitID("main2"), tree)
		e.sync()
		if got, pending := callers(), r.Pending(e.project); got != 2 || pending != 3 {
			t.Fatalf("after the first batch: %d callers, %d pending; want 2 and 3", got, pending)
		}
		for i, want := range []int{4, 5} {
			tree[fmt.Sprintf("z%d.go", i)] = goFile("p").fn(fmt.Sprintf("Z%d", i)).src()
			e.commitTree(m, "main", commitID(fmt.Sprintf("main%d", i+3)), tree)
			e.sync()
			if got := callers(); got != want {
				t.Errorf("after batch %d: %d callers, want %d", i+2, got, want)
			}
		}
		if p := r.Pending(e.project); p != 0 {
			t.Errorf("pending at the end = %d", p)
		}
	})
}

func TestResolverRegistry(t *testing.T) {
	if got := resolvers.Registered(); !slicesContain(got, codeparse.FamilyGo) {
		t.Errorf("Registered() = %v, want go among them", got)
	}
	if fam, res, ok := resolvers.ForLang("go"); !ok || fam != codeparse.FamilyGo || res == nil {
		t.Errorf("ForLang(go) = %q, %v, %v", fam, res, ok)
	}
	if _, _, ok := resolvers.ForLang("klingon"); ok {
		t.Error("ForLang(klingon) found a resolver")
	}
	panics := func(what string, f func()) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Errorf("%s did not panic", what)
			}
		}()
		f()
	}
	panics("an unknown family", func() { resolvers.Register("klingon", resolvers.Default{}) })
	panics("a second go resolver", func() { resolvers.Register(codeparse.FamilyGo, resolvers.Default{}) })
	panics("a nil resolver", func() { resolvers.Register(codeparse.FamilyGo, nil) })
}

func slicesContain(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestGoModulePathAndNames(t *testing.T) {
	for in, want := range map[string]string{
		"module example.com/m\n":                  "example.com/m",
		"// c\nmodule   \"example.com/q\" // x\n": "example.com/q",
		"go 1.26\nmodulex foo\n":                  "",
		"":                                        "",
	} {
		if got := resolvers.GoModulePath([]byte(in)); got != want {
			t.Errorf("GoModulePath(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"fmt":                            "fmt",
		"net/http":                       "http",
		"github.com/go-redis/redis/v9":   "redis",
		"gopkg.in/yaml.v3":               "yaml",
		"github.com/mattn/go-sqlite3":    "sqlite3",
		"github.com/x/client-go":         "client",
		"github.com/x/foo.bar":           "bar",
		"go.mongodb.org/mongo-driver/v2": "driver",
	} {
		if got := resolvers.GoDefaultName(in); got != want {
			t.Errorf("GoDefaultName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"fmt": "fmt", "./util.js": "util", "a.b.c": "c", "a::b": "b", "pkg/": "pkg", "x/y.v2": "y",
	} {
		if got := resolvers.LastSegment(in, []string{".", "::"}); got != want {
			t.Errorf("LastSegment(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeView is a resolvers.View over a map of contents.
type fakeView struct {
	files   map[string]string
	packags map[string][]*resolvers.File
}

func (v fakeView) Layer() string     { return codegraph.BaseLayerID }
func (v fakeView) Has(p string) bool { _, ok := v.files[p]; return ok }
func (v fakeView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v.files[p]
	return []byte(c), ok, nil
}
func (v fakeView) Package(family, key string) []*resolvers.File { return v.packags[family+" "+key] }

func TestGoPackageKeysAndScope(t *testing.T) {
	ctx := context.Background()
	res, ok := resolvers.Lookup(codeparse.FamilyGo)
	if !ok {
		t.Fatal("no go resolver")
	}
	v := fakeView{files: map[string]string{"go.mod": "module example.com/m\n", "tools/go.mod": "module example.com/tools\n"},
		packags: map[string][]*resolvers.File{"go example.com/m/named": {{Facts: &codeparse.Facts{Package: "pkgname"}}}}}
	for p, want := range map[string]string{
		"main.go":          "example.com/m",
		"a/b/c.go":         "example.com/m/a/b",
		"a/b/c_test.go":    "example.com/m/a/b_test",
		"tools/gen/gen.go": "example.com/tools/gen",
		"tools/t.go":       "example.com/tools",
	} {
		pkg := "x"
		if strings.HasSuffix(p, "_test.go") {
			pkg = "x_test"
		}
		got, err := res.Package(ctx, v, &resolvers.File{Path: p, Facts: &codeparse.Facts{Package: pkg}})
		if err != nil || got != want {
			t.Errorf("Package(%s) = %q, %v; want %q", p, got, err, want)
		}
	}
	got, err := res.Package(ctx, fakeView{}, &resolvers.File{Path: "a/b.go", Facts: &codeparse.Facts{Package: "a"}})
	if err != nil || got != "a" {
		t.Errorf("Package without go.mod = %q, %v; want the directory", got, err)
	}

	f := &resolvers.File{Path: "main.go", Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "fmt"}, {Spec: "example.com/m/named"}, {Spec: "example.com/m/b", Alias: "bb"},
		{Spec: "example.com/m/dot", Alias: "."}, {Spec: "example.com/m/side", Alias: "_"},
	}}}
	sc, err := res.Scope(ctx, v, f)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvers.Scope{Qualifiers: map[string][]string{"fmt": {"fmt"}, "pkgname": {"example.com/m/named"}, "bb": {"example.com/m/b"}},
		Open: []string{"example.com/m/dot"}}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Scope = %+v, want %+v", sc, want)
	}
}

func TestDefaultResolver(t *testing.T) {
	ctx := context.Background()
	d := resolvers.Default{}
	if r := d.Rules(); !r.NameFallback || !r.MemberFallback || !reflect.DeepEqual(r.Separators, []string{"."}) {
		t.Errorf("Default rules = %+v", r)
	}
	profile := func(by string) *codeparse.Profile { return &codeparse.Profile{Scope: codeparse.Scope{PackageBy: by}} }
	for _, c := range []struct {
		f    resolvers.File
		want string
	}{
		{resolvers.File{Path: "a/b.py", Facts: &codeparse.Facts{}}, "a"},
		{resolvers.File{Path: "a/b.cs", Facts: &codeparse.Facts{Package: "N.S"}}, "N.S"},
		{resolvers.File{Path: "a/b.cs", Facts: &codeparse.Facts{Package: "N.S"}, Profile: profile(codeparse.PackageByDeclaration)}, "N.S"},
		{resolvers.File{Path: "a/b.rs", Facts: &codeparse.Facts{Package: "x"}, Profile: profile(codeparse.PackageByDirectory)}, "a"},
		{resolvers.File{Path: "a/b.py", Facts: &codeparse.Facts{}, Profile: profile(codeparse.PackageByFile)}, "a/b"},
		{resolvers.File{Path: "a/b.c", Facts: &codeparse.Facts{}, Profile: profile(codeparse.PackageByNone)}, ""},
	} {
		if got, err := d.Package(ctx, fakeView{}, &c.f); err != nil || got != c.want {
			t.Errorf("Package(%s, %+v) = %q, %v; want %q", c.f.Path, c.f.Profile, got, err, c.want)
		}
	}
	f := &resolvers.File{Path: "m.py", Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "os.path"}, {Spec: "numpy", Alias: "np"}, {Spec: "m", Names: []string{"a", "b"}},
		{Spec: "star", Alias: "*"}, {Spec: "side", Alias: "_"},
	}}}
	sc, err := d.Scope(ctx, fakeView{}, f)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvers.Scope{Qualifiers: map[string][]string{"path": {"os.path"}, "np": {"numpy"}},
		Names: map[string][]string{"a": {"m"}, "b": {"m"}}, Open: []string{"star"}}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Scope = %+v, want %+v", sc, want)
	}
	from := &resolvers.File{Package: "p"}
	sym := &codeparse.Symbol{Name: "x"}
	if !d.Visible(from, resolvers.Candidate{File: &resolvers.File{Package: "p"}, Symbol: sym}, resolvers.StepPackage) ||
		d.Visible(from, resolvers.Candidate{File: &resolvers.File{Package: "q"}, Symbol: sym}, resolvers.StepImport) {
		t.Error("Default.Visible: same package sees all, another only exported")
	}
}

func TestQualifierHelpers(t *testing.T) {
	seps := []string{"::", ":"}
	if got := splitQualifier("a::b:c", seps); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Errorf("splitQualifier = %v", got)
	}
	for _, c := range []struct {
		qualName, qual, name string
		want                 bool
	}{
		{"ns::A::f", "A", "f", true},
		{"ns::A::f", "ns::A", "f", true},
		{"ns::BA::f", "A", "f", false},
		{"A::f", "A", "f", true},
		{"ns::A::g", "A", "f", false},
	} {
		if got := endsWithQualified(c.qualName, c.qual, c.name, seps); got != c.want {
			t.Errorf("endsWithQualified(%q, %q, %q) = %v", c.qualName, c.qual, c.name, got)
		}
	}
	if resolvers.StepMember.String() != "member" || resolvers.Step(99).String() != "step(99)" {
		t.Error("Step.String")
	}
}

func TestFactsCacheEvicts(t *testing.T) {
	c := newFactsCache(2)
	a, b := &codeparse.Facts{Lang: "a"}, &codeparse.Facts{Lang: "b"}
	c.put("a", a)
	c.put("b", b)
	c.get("a")
	c.put("c", nil)
	if _, ok := c.get("b"); ok {
		t.Error("b should be evicted (least recently used)")
	}
	if got, ok := c.get("a"); !ok || got != a {
		t.Error("a should stay")
	}
	if got, ok := c.get("c"); !ok || got != nil {
		t.Error("c should be cached as a file without facts")
	}
}

// edgeFailingOpener opens graphs whose PutEdges fails while fail is set.
type edgeFailingOpener struct {
	codegraph.Opener
	fail *atomic.Bool
}

func (o edgeFailingOpener) Open(ctx context.Context, key string) (codegraph.Graph, error) {
	g, err := o.Opener.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return edgeFailingGraph{Graph: g, fail: o.fail}, nil
}

type edgeFailingGraph struct {
	codegraph.Graph
	fail *atomic.Bool
}

func (g edgeFailingGraph) PutEdges(ctx context.Context, layer string, from []codegraph.NodeRef, edges []codegraph.Edge) error {
	if g.fail.Load() {
		return codegraph.ErrUnavailable
	}
	return g.Graph.PutEdges(ctx, layer, from, edges)
}

func TestResolveFailedBatchIsCompletedByTheNextOne(t *testing.T) {
	graphBackends(t, func(t *testing.T, graphs codegraph.Opener) {
		fail := &atomic.Bool{}
		r := NewResolver(ResolverOptions{})
		e := newResolveEnv(t, edgeFailingOpener{Opener: graphs, fail: fail}, r)
		m := wtID(1)
		e.worktree(m, "0123456789abcdef", "", "main")
		tree := map[string]string{"a.go": goFile("p").fn("A").call("A", "B").src(), "b.go": goFile("p").fn("B").src()}
		fail.Store(true)
		e.commitTree(m, "main", commitID("main1"), tree)
		if _, err := e.svc.Sync(e.ctx, e.project); !errors.Is(err, codegraph.ErrUnavailable) {
			t.Fatalf("Sync with failing edges = %v", err)
		}
		if r.Pending(e.project) == 0 {
			t.Fatal("nothing pending after a failed resolution")
		}
		fail.Store(false)
		// The retried batch finds the files written: its delta is empty and
		// resolution waits for the project's next change.
		e.sync()
		tree["c.go"] = goFile("p").fn("C").src()
		e.commitTree(m, "main", commitID("main2"), tree)
		e.sync()
		wantEdges(t, "A", e.callees(codegraph.BaseView(), "a.go", "p.A"), map[string]string{"b.go#p.B": "base/exact"})
		if p := r.Pending(e.project); p != 0 {
			t.Errorf("pending = %d", p)
		}
	})
}

func TestResolveWithoutParserWritesNoEdges(t *testing.T) {
	r := NewResolver(ResolverOptions{})
	e := newApplyEnv(t, memgraph.New(), func(o *Options) { o.Apply = r; o.Parse = nil })
	m := wtID(1)
	e.worktree(m, "0123456789abcdef", "", "main")
	e.commitTree(m, "main", commitID("main1"), map[string]string{"a.go": goFile("p").fn("A").call("A", "A").src()})
	e.sync()
	if r.Pending(e.project) != 0 {
		t.Error("pending work without a parser")
	}
}
