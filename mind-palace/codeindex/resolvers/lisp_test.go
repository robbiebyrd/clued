package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The Lisp extractor (story 0099) is not in this branch: each file's
// content is the facts a dialect's tags query yields for it, built by hand
// and written as JSON, which lispParse reads back.

// lispSrc builds one Lisp file's facts.
type lispSrc struct {
	f    codeparse.Facts
	line int
}

// lispFile starts a file of dialect whose package form (in-package, ns,
// define-library) names pkg ("" for none).
func lispFile(dialect, pkg string) *lispSrc {
	return &lispSrc{f: codeparse.Facts{Lang: "lisp", Dialect: dialect, Package: pkg}}
}

func (l *lispSrc) pos() codeparse.Pos {
	l.line++
	return codeparse.Pos{Line: l.line, Offset: l.line * 100}
}

// qual is the qualified name of a top-level definition, as the extractor
// joins the package and the name with the profile's first separator.
func (l *lispSrc) qual(name string) string {
	if l.f.Package == "" {
		return name
	}
	return l.f.Package + ":" + name
}

func (l *lispSrc) def(kind, name string) *lispSrc {
	p := l.pos()
	l.f.Symbols = append(l.f.Symbols, codeparse.Symbol{ID: l.qual(name), Kind: kind, Name: name,
		QualName: l.qual(name), Start: p, End: p, Exported: true})
	return l
}

func (l *lispSrc) fn(names ...string) *lispSrc {
	for _, n := range names {
		l.def(codeparse.KindFunction, n)
	}
	return l
}

// defpackage adds a defpackage form whose clauses are imps.
func (l *lispSrc) defpackage(name string, imps ...codeparse.Import) *lispSrc {
	start := l.pos()
	for _, im := range imps {
		im.Start = l.pos()
		l.f.Imports = append(l.f.Imports, im)
	}
	end := l.pos()
	l.f.Symbols = append(l.f.Symbols, codeparse.Symbol{ID: "package:" + name, Kind: codeparse.KindPackage, Name: name,
		QualName: name, Start: start, End: end, Exported: true})
	return l
}

func (l *lispSrc) imp(im codeparse.Import) *lispSrc {
	im.Start = l.pos()
	l.f.Imports = append(l.f.Imports, im)
	return l
}

// call adds a call from the definition enclosing to target, a name with
// its qualifier written the dialect's way, which the extractor splits off
// (pkg:f, pkg::f, ns/f, p:f).
func (l *lispSrc) call(enclosing, target string) *lispSrc {
	qual, name := "", target
	for _, sep := range []string{"::", ":", "/"} {
		if i := strings.Index(target, sep); i > 0 {
			qual, name = target[:i], target[i+len(sep):]
			break
		}
	}
	l.f.Refs = append(l.f.Refs, codeparse.Ref{Name: name, Qualifier: qual, Kind: codeparse.RefCall,
		Start: l.pos(), Enclosing: l.qual(enclosing)})
	return l
}

func (l *lispSrc) src() string {
	b, err := json.Marshal(l.f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func lispParse(path string, content []byte) (codeparse.Facts, error) {
	if codeparse.Detect(path, nil, nil).Lang != "lisp" {
		return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
	}
	var f codeparse.Facts
	err := json.Unmarshal(content, &f)
	return f, err
}

const lispProject = "u0123456789abcdef__github.com__o__lisp"

// lispIndex indexes tree (path → content) as one commit of main and
// returns the graph.
func lispIndex(t *testing.T, tree map[string]string) codegraph.Graph {
	t.Helper()
	ctx := context.Background()
	store := memcode.New()
	graphs := memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{lispProject}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{}),
		Parse:    lispParse,
		Holder:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := sha1Hex("commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: commit, Upstream: commit, Full: true}
	for p, c := range tree {
		sha := sha256Hex(c)
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(c)),
			GitOID: sha1Hex(c), Mode: "100644", Lang: "lisp"})
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, lispProject); err != nil {
		t.Fatal(err)
	}
	g, err := graphs.Open(ctx, lispProject)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func sha1Hex(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// lispCallees returns the callees of path#id in the base view, as target
// → confidence.
func lispCallees(t *testing.T, g codegraph.Graph, path, id string) map[string]string {
	t.Helper()
	rows, err := g.Neighbors(context.Background(), codegraph.BaseView(),
		codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: codegraph.RelCallees})
	if err != nil {
		t.Fatalf("callees of %s#%s: %v", path, id, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.To.ID] = r.Confidence
	}
	return out
}

func wantLispEdges(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

func use(spec string) codeparse.Import { return codeparse.Import{Spec: spec, Kind: "use"} }

func TestResolve_lispCommonLisp(t *testing.T) {
	main := lispFile("commonlisp", ":my-app").
		fn("main", "local").
		call("main", "local").             // the file
		call("main", "shared").            // the package, another file
		call("main", "FLATTEN").           // :use alexandria (in the defpackage of another file), any case
		call("main", "helper").            // :import-from util, though x defines helper too
		call("main", "Helper").            // the same, another spelling
		call("main", "util:internal-fn").  // a package qualifier, never imported
		call("main", "UTIL::internal-fn"). // the same, internal and upper case
		call("main", "cl:car").            // a package outside the project
		call("main", "dupe").              // two other packages define it: name
		call("main", "only-in-clojure").   // another dialect defines it: nothing
		call("main", "nowhere").           //
		call("local", "x:helper")          // the qualifier beats the import
	tree := map[string]string{
		"src/package.lisp": lispFile("commonlisp", "").
			defpackage("#:my-app", use(":cl"), use(":alexandria"),
				codeparse.Import{Spec: ":util", Kind: "import_from", Names: []string{"#:helper"}}).src(),
		"src/main.lisp":       main.src(),
		"src/other.lisp":      lispFile("commonlisp", "#:my-app").fn("shared").src(),
		"lib/alexandria.lisp": lispFile("commonlisp", ":alexandria").fn("flatten").src(),
		"lib/util.lisp":       lispFile("commonlisp", `"UTIL"`).fn("helper", "internal-fn", "dupe").src(),
		"lib/x.lisp":          lispFile("commonlisp", ":x").fn("helper", "dupe").src(),
		"clj/only.clj":        lispFile("clojure", "only").fn("only-in-clojure").src(),
	}
	g := lispIndex(t, tree)
	wantLispEdges(t, "main", lispCallees(t, g, "src/main.lisp", ":my-app:main"), map[string]string{
		"src/main.lisp#:my-app:local":             "exact",
		"src/other.lisp##:my-app:shared":          "exact",
		"lib/alexandria.lisp#:alexandria:flatten": "exact",
		`lib/util.lisp#"UTIL":helper`:             "exact",
		`lib/util.lisp#"UTIL":internal-fn`:        "exact",
		`lib/util.lisp#"UTIL":dupe`:               "name",
	})
	wantLispEdges(t, "local", lispCallees(t, g, "src/main.lisp", ":my-app:local"), map[string]string{
		"lib/x.lisp#:x:helper": "exact",
	})
}

func TestResolve_lispClojure(t *testing.T) {
	core := lispFile("clojure", "app.core").
		imp(codeparse.Import{Spec: "app.util", Alias: "u", Kind: "require"}).
		imp(codeparse.Import{Spec: "app.str", Names: []string{"shout"}, Kind: "require"}).
		imp(codeparse.Import{Spec: "clojure.string", Alias: "str", Kind: "require"}).
		imp(codeparse.Import{Spec: "java.util.Date", Kind: "import"}).
		fn("run", "local").
		call("run", "u/helper").        // the :as alias
		call("run", "app.util/helper"). // the namespace's full name
		call("run", "shout").           // :refer
		call("run", "str/join").        // a namespace outside the project, though app.util has join
		call("run", "app.other/thing"). // a namespace of the project, not required here
		call("run", "local").           // the file
		call("run", "nowhere")
	tree := map[string]string{
		"src/app/core.clj":  core.src(),
		"src/app/util.clj":  lispFile("clojure", "app.util").fn("helper", "join").src(),
		"src/app/str.cljs":  lispFile("clojure", "app.str").fn("shout").src(),
		"src/app/other.clj": lispFile("clojure", "app.other").fn("thing").src(),
		"cl/other.lisp":     lispFile("commonlisp", ":other").fn("nowhere").src(),
	}
	g := lispIndex(t, tree)
	wantLispEdges(t, "run", lispCallees(t, g, "src/app/core.clj", "app.core:run"), map[string]string{
		"src/app/util.clj#app.util:helper":  "exact",
		"src/app/str.cljs#app.str:shout":    "exact",
		"src/app/other.clj#app.other:thing": "exact",
		"src/app/core.clj#app.core:local":   "exact",
	})
}

func TestResolve_lispEmacsLisp(t *testing.T) {
	mode := lispFile("elisp", "").
		imp(codeparse.Import{Spec: "my-utils", Kind: "require"}).
		fn("my-mode-start", "my-mode--helper").
		call("my-mode-start", "my-mode--helper"). // the file
		call("my-mode-start", "my-utils-fmt").    // the one global namespace
		call("my-mode-start", "my-twice")         // defined twice: name
	tree := map[string]string{
		"lisp/my-mode.el":  mode.src(),
		"lisp/my-utils.el": lispFile("elisp", "").fn("my-utils-fmt", "my-twice").src(),
		"lisp/my-more.el":  lispFile("elisp", "").fn("my-twice").src(),
		"rkt/decoy.rkt":    lispFile("racket", "").fn("my-utils-fmt").src(),
	}
	g := lispIndex(t, tree)
	wantLispEdges(t, "my-mode-start", lispCallees(t, g, "lisp/my-mode.el", "my-mode-start"), map[string]string{
		"lisp/my-mode.el#my-mode--helper": "exact",
		"lisp/my-utils.el#my-utils-fmt":   "exact",
		"lisp/my-more.el#my-twice":        "name",
	})
}

func TestResolve_lispRacketAndScheme(t *testing.T) {
	main := lispFile("racket", "").
		imp(codeparse.Import{Spec: "racket/base", Kind: "lang"}).
		imp(codeparse.Import{Spec: "util.rkt", Kind: "require"}).
		imp(codeparse.Import{Spec: "mylib/geom", Alias: "g:", Kind: "require"}).
		imp(codeparse.Import{Spec: "mylib/text", Names: []string{"shout"}, Kind: "require"}).
		imp(codeparse.Import{Spec: "racket/list", Kind: "require"}).
		fn("main").
		call("main", "util-fn"). // a relative require opens the module
		call("main", "g:area").  // prefix-in, though util.rkt defines area too
		call("main", "shout").   // only-in
		call("main", "first")    // racket/list is outside the project
	app := lispFile("scheme", "").
		imp(codeparse.Import{Spec: "(mylib shapes)", Kind: "import"}).
		imp(codeparse.Import{Spec: "(geo)", Alias: "geo:", Kind: "import"}).
		imp(codeparse.Import{Spec: "(srfi 1)", Names: []string{"fold"}, Kind: "import"}).
		fn("run").
		call("run", "square"). // a declared library, in another directory
		call("run", "geo:dist").
		call("run", "fold")
	tree := map[string]string{
		"rkt/main.rkt":         main.src(),
		"rkt/util.rkt":         lispFile("racket", "").fn("util-fn", "area").src(),
		"mylib/geom.rkt":       lispFile("racket", "").fn("area").src(),
		"mylib/text.rkt":       lispFile("racket", "").fn("shout").src(),
		"scm/app.scm":          app.src(),
		"scm/geo.scm":          lispFile("scheme", "").fn("dist").src(),
		"impl/shapes-impl.scm": lispFile("scheme", "(mylib shapes)").fn("square").src(),
		"cl/decoy.lisp":        lispFile("commonlisp", ":decoy").fn("first", "fold").src(),
	}
	g := lispIndex(t, tree)
	wantLispEdges(t, "racket main", lispCallees(t, g, "rkt/main.rkt", "main"), map[string]string{
		"rkt/util.rkt#util-fn": "exact",
		"mylib/geom.rkt#area":  "exact",
		"mylib/text.rkt#shout": "exact",
	})
	wantLispEdges(t, "scheme run", lispCallees(t, g, "scm/app.scm", "run"), map[string]string{
		"impl/shapes-impl.scm#(mylib shapes):square": "exact",
		"scm/geo.scm#dist":                           "exact",
	})
}

// lispView is a resolvers.View over a fixed set of files.
type lispView struct {
	paths map[string]bool
	pkgs  map[string][]*resolvers.File
}

func (v lispView) Layer() string                                         { return codegraph.BaseLayerID }
func (v lispView) Has(p string) bool                                     { return v.paths[p] }
func (v lispView) Content(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func (v lispView) Package(family, key string) []*resolvers.File          { return v.pkgs[family+" "+key] }

func TestPackageKeys_lisp(t *testing.T) {
	r, ok := resolvers.Lookup(codeparse.FamilyLisp)
	if !ok {
		t.Fatal("no lisp resolver")
	}
	if rules := r.Rules(); !rules.CaseInsensitive || !rules.NameFallback || rules.MemberFallback {
		t.Errorf("rules = %+v", rules)
	}
	two := lispFile("commonlisp", "").defpackage("a").defpackage("b")
	for _, c := range []struct {
		path string
		f    *lispSrc
		want string
	}{
		{"p.lisp", lispFile("commonlisp", "#:My-Pkg"), "commonlisp:my-pkg"},
		{"p.lisp", lispFile("commonlisp", "common-lisp-user"), "commonlisp:cl-user"},
		{"p.lisp", lispFile("commonlisp", ""), "commonlisp:cl-user"},
		{"p.lisp", lispFile("commonlisp", ":cl-user").defpackage(":Foo"), "commonlisp:foo"},
		{"p.lisp", two, "commonlisp:cl-user"},
		{"p.asd", lispFile("", ""), "commonlisp:cl-user"},
		{"a/b.clj", lispFile("clojure", "my.ns"), "clojure:my.ns"},
		{"a/b.cljs", lispFile("", ""), "clojure:user"},
		{"a/b.el", lispFile("elisp", ""), "elisp:"},
		{"a/b.rkt", lispFile("racket", ""), "scheme:a/b"},
		{"a/b.ss", lispFile("", ""), "scheme:a/b"},
		{"a/b.scm", lispFile("scheme", "(foo bar (1 0))"), "scheme:foo/bar"},
	} {
		f := &resolvers.File{Path: c.path, Lang: "lisp", Family: codeparse.FamilyLisp, Facts: &c.f.f}
		got, err := r.Package(context.Background(), lispView{}, f)
		if err != nil || got != c.want {
			t.Errorf("Package(%s %+v) = %q, %v; want %q", c.path, c.f.f.Package, got, err, c.want)
		}
	}
}

func TestScope_lisp(t *testing.T) {
	r, _ := resolvers.Lookup(codeparse.FamilyLisp)
	file := func(p, key string, s *lispSrc) *resolvers.File {
		return &resolvers.File{Path: p, Lang: "lisp", Family: codeparse.FamilyLisp, Package: key, Facts: &s.f}
	}
	collection := file("coll/main.rkt", "scheme:coll/main", lispFile("racket", "").fn("x"))
	nested := file("src/pkg/sub/util.rkt", "scheme:src/pkg/sub/util", lispFile("racket", "").fn("y"))
	v := lispView{pkgs: map[string][]*resolvers.File{
		"lisp scheme:coll/main":        {collection},
		"lisp scheme:src/pkg/sub/util": {nested},
	}}
	f := file("src/pkg/a/main.rkt", "scheme:src/pkg/a/main", lispFile("racket", "").
		imp(codeparse.Import{Spec: "coll", Kind: "require"}).         // a collection's main.rkt
		imp(codeparse.Import{Spec: "pkg/sub/util", Kind: "require"}). // a collection rooted at an ancestor
		imp(codeparse.Import{Spec: "../sub/util.rkt", Kind: "require"}).
		imp(codeparse.Import{Spec: "/abs/x.rkt", Kind: "require"}))
	s, err := r.Scope(context.Background(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"scheme:coll/main", "scheme:src/pkg/sub/util"}; !reflect.DeepEqual(s.Open, want) {
		t.Errorf("open = %v, want %v", s.Open, want)
	}

	// A file of several packages' defpackages scopes none of them into
	// another package; its own top-level imports stay its own.
	pkgFile := file("pkgs.lisp", "commonlisp:cl-user", lispFile("commonlisp", "").
		defpackage("a", use(":lib-a")).defpackage("b", use(":lib-b")).imp(use(":top")))
	s, err = r.Scope(context.Background(), lispView{}, pkgFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"commonlisp:top"}; !reflect.DeepEqual(s.Open, want) {
		t.Errorf("cl-user open = %v, want %v", s.Open, want)
	}
}

func TestNames_lisp(t *testing.T) {
	for in, want := range map[string]string{
		":foo": "foo", "#:Foo": "foo", `"FOO"`: "foo", "'foo": "foo", "COMMON-LISP": "cl", "|x|": "x",
	} {
		if got := resolvers.LispPackageName(in); got != want {
			t.Errorf("LispPackageName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"(foo bar)": "foo/bar", "(srfi 1)": "srfi/1", "(foo (1 2))": "foo", "mylib/util": "mylib/util", "'(a b)": "a/b",
	} {
		if got := resolvers.SchemeLibraryName(in); got != want {
			t.Errorf("SchemeLibraryName(%q) = %q, want %q", in, got, want)
		}
	}
}
