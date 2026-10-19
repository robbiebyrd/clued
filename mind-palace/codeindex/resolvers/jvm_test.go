package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"reflect"
	"slices"
	"sort"
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

// jvmEnv indexes a tree of Java and Kotlin files with the real extractor
// and the resolution engine into a memory graph, then reads the edges.
type jvmEnv struct {
	t       *testing.T
	ctx     context.Context
	project string
	g       codegraph.Graph
	facts   map[string]codeparse.Facts
}

func jvmHex(h []byte) string { return hex.EncodeToString(h) }

func newJVMEnv(t *testing.T, tree map[string]string) *jvmEnv {
	t.Helper()
	ctx := context.Background()
	engine, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(t.Name()))
	e := &jvmEnv{t: t, ctx: ctx, project: "u0123456789abcdef__jvm-" + jvmHex(h[:6]), facts: map[string]codeparse.Facts{}}
	store, graphs := memcode.New(), memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Code:    func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:  graphs,
		Cursors: codeindex.NewMemCursors(nil),
		Apply:   codeindex.NewResolver(codeindex.ResolverOptions{Profiles: engine.Profiles()}),
		Parse:   codeparse.Parse,
		Holder:  "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	head := sha1.Sum([]byte("commit main"))
	ch := code.Change{Worktree: wt, Ref: "main", Head: jvmHex(head[:]), Upstream: jvmHex(head[:]), Full: true}
	for p, content := range tree {
		sum := sha256.Sum256([]byte(content))
		oid := sha1.Sum([]byte(content))
		if err := store.PutBlob(ctx, code.Blob{SHA256: jvmHex(sum[:]), Size: int64(len(content)), Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		lang := codeparse.Detect(p, nil, nil).Lang
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: jvmHex(sum[:]), Size: int64(len(content)),
			GitOID: jvmHex(oid[:]), Mode: "100644", Lang: lang})
		facts, err := codeparse.Parse(p, []byte(content))
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		e.facts[p] = facts
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, e.project); err != nil {
		t.Fatal(err)
	}
	if e.g, err = graphs.Open(ctx, e.project); err != nil {
		t.Fatal(err)
	}
	return e
}

// edges returns every resolved edge whose source is in the file at from,
// as "<source id> <edge type> <target path#id> <confidence>", sorted.
func (e *jvmEnv) edges(from string) []string {
	e.t.Helper()
	seen := map[string]bool{}
	var out []string
	for p, facts := range e.facts {
		for _, sym := range facts.Symbols {
			for _, rel := range []codegraph.Relation{codegraph.RelCallers, codegraph.RelReferences, codegraph.RelImplementations} {
				rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, sym.ID), Relation: rel})
				if err != nil {
					e.t.Fatalf("%s of %s#%s: %v", rel, p, sym.ID, err)
				}
				for _, r := range rows {
					src, ok := strings.CutPrefix(r.From.ID, from+"#")
					if !ok {
						continue
					}
					s := src + " " + string(r.Type) + " " + r.To.ID + " " + r.Confidence
					if !seen[s] {
						seen[s] = true
						out = append(out, s)
					}
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

// jvmGolden reads a codeparse golden input of the family.
func jvmGolden(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(path.Join("..", "..", "codeparse", "testdata", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var jvmRulesTree = map[string]string{
	"a/util/Util.java": `package a.util;
public class Util {
    public static int max(int x, int y) { return x; }
    public static class Inner { public void go() {} }
}
`,
	"a/other/Util.java": `package a.other;
public class Util {
    public static int max(int x, int y) { return y; }
}
`,
	"a/lib/Lib.java": `package a.lib;
public class Lib {
    public static void call() {}
}
`,
	"a/app/Helper.java": `package a.app;
public class Helper {
    public void help() {}
}
class Util {}
`,
	"a/app/App.java": `package a.app;

import a.util.Util;
import a.util.Util.Inner;
import a.lib.*;
import static a.other.Util.max;

public class App {
    void run() {
        Util u = new Util();
        new Helper().help();
        Inner i = new Inner();
        i.go();
        Lib.call();
        max(1, 2);
        Util.max(1, 2);
    }
}
`,
	"k/text/Text.kt": `package k.text

fun String.shout(): String = uppercase()

fun whisper(s: String): String = s

private fun secret(): Int = 1

class Box {
    fun open(): Int = secret()
}
`,
	"k/text/Use.java": `package k.text;

class Use {
    void use() {
        new Box();
    }
}
`,
	"k/app/Main.kt": `package k.app

import k.text.shout
import k.text.whisper
import k.text.Box as Crate

fun String.loud(): String = shout()

fun main() {
    whisper("a")
    secret()
    Crate()
}
`,
	"k/other/Other.kt": `package k.other

fun String.yell(): String = shout()

fun quiet(): String = whisper("b")
`,
}

// TestResolve_jvmRules covers each rule on small Java and Kotlin sources
// parsed by the real extractor.
func TestResolve_jvmRules(t *testing.T) {
	e := newJVMEnv(t, jvmRulesTree)
	for p, want := range map[string][]string{
		"a/app/App.java": {
			"a.app.App.run CALLS a/app/Helper.java#a.app.Helper exact",          // the package
			"a.app.App.run CALLS a/app/Helper.java#a.app.Helper.help name",      // a member through an expression
			"a.app.App.run CALLS a/lib/Lib.java#a.lib.Lib.call qualified",       // through a type of an on-demand import
			"a.app.App.run CALLS a/other/Util.java#a.other.Util.max name",       // a static import keeps its class's max only
			"a.app.App.run CALLS a/util/Util.java#a.util.Util exact",            // the import shadows the package's Util
			"a.app.App.run CALLS a/util/Util.java#a.util.Util.Inner name",       // a nested type's import
			"a.app.App.run CALLS a/util/Util.java#a.util.Util.Inner.go name",    // a member through a variable
			"a.app.App.run CALLS a/util/Util.java#a.util.Util.max qualified",    // through the imported class
			"a.app.App.run REFERENCES a/app/Helper.java#a.app.Helper exact",     //
			"a.app.App.run REFERENCES a/util/Util.java#a.util.Util exact",       //
			"a.app.App.run REFERENCES a/util/Util.java#a.util.Util.Inner name"}, //
		// Java and Kotlin share a package.
		"k/text/Use.java": {"k.text.Use.use CALLS k/text/Text.kt#k.text.Box exact", "k.text.Use.use REFERENCES k/text/Text.kt#k.text.Box exact"},
		// The extension and the function are imported; the private
		// function and the alias are not reached.
		"k/app/Main.kt": {"k.app.String.loud CALLS k/text/Text.kt#k.text.String.shout exact", "k.app.main CALLS k/text/Text.kt#k.text.whisper exact"},
		// A private function in its own file.
		"k/text/Text.kt": {"k.text.Box.open CALLS k/text/Text.kt#k.text.secret exact"},
		// An extension of another package is not reached unimported; a
		// plain function is, by name only.
		"k/other/Other.kt": {"k.other.quiet CALLS k/text/Text.kt#k.text.whisper name"},
	} {
		if got := e.edges(p); !reflect.DeepEqual(got, want) {
			t.Errorf("edges from %s:\n got %q\nwant %q", p, got, want)
		}
	}
}

// TestResolve_jvmGoldens resolves the extraction goldens of both
// languages together.
func TestResolve_jvmGoldens(t *testing.T) {
	e := newJVMEnv(t, map[string]string{
		"src/orders/OrderService.java": jvmGolden(t, "java/OrderService.java"),
		"src/store/Inventory.java":     jvmGolden(t, "java/Inventory.java"),
		"src/module-info.java":         jvmGolden(t, "java/module-info.java"),
		"kt/service/Service.kt":        jvmGolden(t, "kotlin/Service.kt"),
		"kt/model/Model.kt":            jvmGolden(t, "kotlin/Model.kt"),
		"kt/nested/Nested.kt":          jvmGolden(t, "kotlin/Nested.kt"),
	})
	for p, want := range map[string][]string{
		"src/orders/OrderService.java": {
			"com.example.orders.OrderService IMPLEMENTS src/orders/OrderService.java#com.example.orders.OrderApi exact",
			"com.example.orders.OrderService.inventory REFERENCES src/store/Inventory.java#com.example.store.Inventory exact",
			"com.example.orders.OrderService.place CALLS src/orders/OrderService.java#com.example.orders.OrderService.nextId exact",
			"com.example.orders.OrderService.place CALLS src/store/Inventory.java#com.example.store.Item.id qualified",
			"com.example.orders.OrderService.place REFERENCES src/store/Inventory.java#com.example.store.Item exact",
			"com.example.orders.OrderService.place REFERENCES src/store/Inventory.java#com.example.store.Status.IN_STOCK qualified",
			"com.example.orders.OrderService.place.run CALLS src/orders/OrderService.java#com.example.orders.OrderService.notifyShipping exact",
		},
		"src/store/Inventory.java": {
			"com.example.store.Inventory IMPLEMENTS src/store/Inventory.java#com.example.store.Store exact",
			"com.example.store.Item.status REFERENCES src/store/Inventory.java#com.example.store.Status exact",
		},
		"kt/service/Service.kt": {
			"com.example.shop.service.CustomerService EXTENDS kt/service/Service.kt#com.example.shop.service.BaseService exact",
			"com.example.shop.service.CustomerService.repo REFERENCES kt/model/Model.kt#com.example.shop.model.Customer exact",
			"com.example.shop.service.CustomerService.rename CALLS kt/model/Model.kt#com.example.shop.model.Customer.Factory.of name",
			"com.example.shop.service.CustomerService.start CALLS kt/model/Model.kt#com.example.shop.model.Catalog.price qualified",
			"com.example.shop.service.CustomerService.start CALLS kt/service/Service.kt#com.example.shop.service.log exact",
		},
		"kt/nested/Nested.kt": {
			"com.example.nested.Inner.extension REFERENCES kt/nested/Nested.kt#com.example.nested.Outer.Inner qualified",
			"com.example.nested.oneLineOld CALLS kt/nested/Nested.kt#com.example.nested.outsideHelper exact",
		},
	} {
		got := e.edges(p)
		for _, w := range want {
			if !slices.Contains(got, w) {
				t.Errorf("edges from %s: no %q in\n%s", p, w, strings.Join(got, "\n"))
			}
		}
		for _, g := range got {
			// Nothing reaches a definition of the imported class's package
			// by the import step, or the standard library's names.
			if strings.Contains(g, "#com.example.store.id ") || strings.Contains(g, "Service.kt#com.example.shop.service.Repository.find exact") {
				t.Errorf("edges from %s: unexpected %q", p, g)
			}
		}
	}
	if got := e.edges("src/module-info.java"); len(got) != 0 {
		t.Errorf("module-info resolved %q", got)
	}
}

// jvmView is a View with the packages of the files given.
type jvmView map[string][]*resolvers.File

func (v jvmView) Layer() string     { return codegraph.BaseLayerID }
func (v jvmView) Has(p string) bool { return false }
func (v jvmView) Content(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (v jvmView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilyJVM {
		return nil
	}
	return v[key]
}

func TestResolver_jvmPackageAndScope(t *testing.T) {
	ctx := context.Background()
	res, ok := resolvers.Lookup(codeparse.FamilyJVM)
	if !ok {
		t.Fatal("no jvm resolver")
	}
	if fam, _, ok := resolvers.ForLang("kotlin"); !ok || fam != codeparse.FamilyJVM {
		t.Fatalf("kotlin's family = %q, %v", fam, ok)
	}
	if r := res.Rules(); !reflect.DeepEqual(r.Separators, []string{"."}) || !r.NameFallback || !r.MemberFallback || r.CaseInsensitive {
		t.Errorf("Rules = %+v", r)
	}
	engine, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	profiles := engine.Profiles()

	for _, c := range []struct {
		path, lang, pkg, want string
	}{
		{"src/a/A.java", "java", "com.acme", "com.acme"},
		{"src/a/A.kt", "kotlin", "com.acme", "com.acme"},
		{"scripts/build.gradle.kts", "kotlin", "", "scripts"},
	} {
		f := &resolvers.File{Path: c.path, Lang: c.lang, Profile: profiles[c.lang], Facts: &codeparse.Facts{Package: c.pkg}}
		if got, err := res.Package(ctx, jvmView{}, f); err != nil || got != c.want {
			t.Errorf("Package(%s) = %q, %v; want %q", c.path, got, err, c.want)
		}
	}

	v := jvmView{"com.acme": {{}}, "com.acme.util": {{}}}
	java := &resolvers.File{Path: "A.java", Lang: "java", Profile: profiles["java"], Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "com.acme.Widget", Names: []string{"Widget"}},
		{Spec: "com.acme.Outer.Inner", Names: []string{"Inner"}},
		{Spec: "java.util.List", Names: []string{"List"}},
		{Spec: "com.acme.util", Kind: "on_demand"},
		{Spec: "java.util.Objects.requireNonNull", Names: []string{"requireNonNull"}, Kind: "static"},
		{Spec: "java.util.Collections", Kind: "static_on_demand"},
		{Spec: "java.sql", Kind: "requires"},
	}}}
	sc, err := res.Scope(ctx, v, java)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvers.Scope{
		Qualifiers: map[string][]string{"Widget": {"com.acme"}, "Inner": {"com.acme"}, "List": {"java.util"}},
		Names:      map[string][]string{"Widget": {"com.acme"}, "List": {"java.util"}},
		Open:       []string{"java.lang", "com.acme.util"},
	}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Java Scope = %+v\nwant %+v", sc, want)
	}

	kotlin := &resolvers.File{Path: "A.kt", Lang: "kotlin", Profile: profiles["kotlin"], Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "com.acme.Widget", Alias: "Gadget"},
		{Spec: "com.acme.shout"},
		{Spec: "com.acme.util", Kind: "wildcard"},
	}}}
	sc, err = res.Scope(ctx, v, kotlin)
	if err != nil {
		t.Fatal(err)
	}
	want = resolvers.Scope{
		Qualifiers: map[string][]string{"Gadget": {"com.acme"}, "shout": {"com.acme"}},
		Names:      map[string][]string{"shout": {"com.acme"}},
		Open: []string{"kotlin", "kotlin.annotation", "kotlin.collections", "kotlin.comparisons", "kotlin.io",
			"kotlin.ranges", "kotlin.sequences", "kotlin.text", "kotlin.jvm", "java.lang", "com.acme.util"},
	}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Kotlin Scope = %+v\nwant %+v", sc, want)
	}

	// Without a profile: the language's implicit imports, and a.b.* as
	// written.
	bare := &resolvers.File{Path: "B.java", Lang: "java", Facts: &codeparse.Facts{Imports: []codeparse.Import{{Spec: "com.acme.*"}}}}
	if sc, _ := res.Scope(ctx, v, bare); !reflect.DeepEqual(sc.Open, []string{"java.lang", "com.acme"}) || sc.Names != nil {
		t.Errorf("Scope without a profile = %+v", sc)
	}
}

func TestResolver_jvmVisible(t *testing.T) {
	res, _ := resolvers.Lookup(codeparse.FamilyJVM)
	file := func(path, lang, pkg string, imports ...codeparse.Import) *resolvers.File {
		return &resolvers.File{Path: path, Lang: lang, Package: pkg, Facts: &codeparse.Facts{Package: pkg, Imports: imports}}
	}
	cand := func(f *resolvers.File, qual string, exported bool, parent string) resolvers.Candidate {
		return resolvers.Candidate{File: f, Symbol: &codeparse.Symbol{Name: qual[strings.LastIndex(qual, ".")+1:], QualName: qual, Exported: exported, Parent: parent}}
	}
	widget := file("acme/Widget.java", "java", "com.acme")
	other := file("other/Widget.java", "java", "com.other")
	helper := file("acme/Helper.java", "java", "com.acme")
	text := file("text/Text.kt", "kotlin", "k.text")
	from := file("app/App.java", "java", "com.app", codeparse.Import{Spec: "com.acme.Widget", Names: []string{"Widget"}},
		codeparse.Import{Spec: "com.acme.Util.max", Names: []string{"max"}, Kind: "static"})
	fromKt := file("app/Main.kt", "kotlin", "com.app", codeparse.Import{Spec: "k.text.shout"})
	sameKt := file("text/Other.kt", "kotlin", "k.text")
	sameJava := file("acme/Same.java", "java", "com.acme")

	for _, c := range []struct {
		what string
		f    *resolvers.File
		c    resolvers.Candidate
		step resolvers.Step
		want bool
	}{
		{"imported type", from, cand(widget, "com.acme.Widget", true, ""), resolvers.StepImport, true},
		{"unexported in another package", from, cand(widget, "com.acme.Widget", false, ""), resolvers.StepImport, false},
		{"Java package-private in the package", sameJava, cand(helper, "com.acme.Helper", false, ""), resolvers.StepPackage, true},
		{"import step reaches only what is imported", from, cand(helper, "com.acme.Helper", true, ""), resolvers.StepImport, false},
		{"shadowed by the import", from, cand(other, "com.other.Widget", true, ""), resolvers.StepName, false},
		{"shadowed in an open package", from, cand(other, "com.other.Widget", true, ""), resolvers.StepOpen, false},
		{"not shadowed when qualified", from, cand(other, "com.other.Widget", true, ""), resolvers.StepQualified, true},
		{"the static import's member", from, cand(widget, "com.acme.Util.max", true, "com.acme.Util"), resolvers.StepName, true},
		{"another max", from, cand(other, "com.other.Util.max", true, "com.other.Util"), resolvers.StepName, false},
		{"Kotlin private in another file", sameKt, cand(text, "k.text.secret", false, ""), resolvers.StepPackage, false},
		{"Kotlin private in its file", text, cand(text, "k.text.secret", false, ""), resolvers.StepFile, true},
		{"imported extension", fromKt, cand(text, "k.text.String.shout", true, ""), resolvers.StepImport, true},
		{"imported extension through a type", fromKt, cand(text, "k.text.String.shout", true, ""), resolvers.StepQualified, true},
		{"unimported extension", from, cand(text, "k.text.String.shout", true, ""), resolvers.StepQualified, false},
		{"unimported extension by name", from, cand(text, "k.text.String.shout", true, ""), resolvers.StepName, false},
		{"extension in its package", sameKt, cand(text, "k.text.String.shout", true, ""), resolvers.StepPackage, true},
	} {
		if got := res.Visible(c.f, c.c, c.step); got != c.want {
			t.Errorf("%s: Visible = %v, want %v", c.what, got, c.want)
		}
	}
}
