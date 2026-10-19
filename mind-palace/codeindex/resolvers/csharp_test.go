package resolvers_test

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"os"
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

// csharpGolden reads one of the extraction goldens' inputs.
func csharpGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../codeparse/testdata/csharp/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// csharpParse parses C# source with the real extractor.
func csharpParse(t *testing.T, path, src string) *codeparse.Facts {
	t.Helper()
	f, err := codeparse.Parse(path, []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if f.Lang != "csharp" {
		t.Fatalf("%s parsed as %q", path, f.Lang)
	}
	return &f
}

// csharpView is a resolvers.View over parsed files, packages filled the
// way the engine fills them.
type csharpView struct {
	files    map[string]*resolvers.File
	packages map[string][]*resolvers.File
}

func (v *csharpView) Layer() string     { return codegraph.BaseLayerID }
func (v *csharpView) Has(p string) bool { _, ok := v.files[p]; return ok }
func (v *csharpView) Content(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (v *csharpView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilyCSharp {
		return nil
	}
	return v.packages[key]
}

func newCSharpView(t *testing.T, tree map[string]string) *csharpView {
	t.Helper()
	r, ok := resolvers.Lookup(codeparse.FamilyCSharp)
	if !ok {
		t.Fatal("no csharp resolver registered")
	}
	v := &csharpView{files: map[string]*resolvers.File{}, packages: map[string][]*resolvers.File{}}
	paths := make([]string, 0, len(tree))
	for p := range tree {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		v.files[p] = &resolvers.File{Path: p, Layer: codegraph.BaseLayerID, Lang: "csharp",
			Family: codeparse.FamilyCSharp, Facts: csharpParse(t, p, tree[p])}
	}
	for _, p := range paths {
		f := v.files[p]
		key, err := r.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
		v.packages[key] = append(v.packages[key], f)
	}
	return v
}

func (v *csharpView) candidate(t *testing.T, path, id string) resolvers.Candidate {
	t.Helper()
	f := v.files[path]
	for i := range f.Facts.Symbols {
		if f.Facts.Symbols[i].ID == id {
			return resolvers.Candidate{File: f, Symbol: &f.Facts.Symbols[i]}
		}
	}
	t.Fatalf("no symbol %s in %s", id, path)
	return resolvers.Candidate{}
}

func TestResolvers_csharpRules(t *testing.T) {
	family, r, ok := resolvers.ForLang("csharp")
	if !ok || family != codeparse.FamilyCSharp {
		t.Fatalf("ForLang(csharp) = %q, %v", family, ok)
	}
	rules := r.Rules()
	if !reflect.DeepEqual(rules.Separators, []string{"."}) || rules.CaseInsensitive || !rules.NameFallback || !rules.MemberFallback {
		t.Errorf("rules = %+v", rules)
	}
	for in, want := range map[string]string{
		"System.IO":                             "System.IO",
		"global::System.Threading.Tasks":        "System.Threading.Tasks",
		"System.Collections.Generic.List<int>":  "System.Collections.Generic.List",
		"Ext::Lib.Thing":                        "Lib.Thing",
		" Demo . Data ":                         "Demo.Data",
		"System.Collections.Generic.Dictionary": "System.Collections.Generic.Dictionary",
	} {
		if got := resolvers.CSharpName(in); got != want {
			t.Errorf("CSharpName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolvers_csharpPackageAndScope(t *testing.T) {
	v := newCSharpView(t, map[string]string{
		"tools/Usings.cs":       csharpGolden(t, "Usings.cs"),
		"ui/Form1.Designer.cs":  csharpGolden(t, "Form1.Designer.cs"),
		"geometry/Shapes.cs":    csharpGolden(t, "Shapes.cs"),
		"Global.cs":             "public class GlobalThing { }\n",
		"data/Customer.cs":      "namespace Demo.Data { public class Customer { } }\n",
		"app/Program.cs":        csharpProgram,
		"app/nested/Program.cs": "namespace Outer { namespace Inner { class P { } } }\n",
	})
	for path, want := range map[string]string{
		"tools/Usings.cs":       "Demo.Tools.Io", // file-scoped
		"ui/Form1.Designer.cs":  "Demo.Ui",       // the outermost of nested namespaces
		"geometry/Shapes.cs":    "Demo.Geometry", // a block
		"Global.cs":             "",              // the global namespace
		"app/nested/Program.cs": "Outer",
	} {
		if got := v.files[path].Package; got != want {
			t.Errorf("package of %s = %q, want %q", path, got, want)
		}
	}

	r, _ := resolvers.Lookup(codeparse.FamilyCSharp)
	s, err := r.Scope(context.Background(), v, v.files["tools/Usings.cs"])
	if err != nil {
		t.Fatal(err)
	}
	want := resolvers.Scope{
		Qualifiers: map[string][]string{
			"Json":    {"System.Text.Json.JsonSerializer"}, // outside the project: kept
			"IntList": {"System.Collections.Generic.List"},
		},
		// The enclosing namespaces, the global one, then the usings
		// (global using and global:: alike); using static adds nothing.
		Open: []string{"Demo.Tools", "Demo", "", "System.Linq", "System", "System.IO", "System.Threading.Tasks"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("scope of Usings.cs:\n got %+v\nwant %+v", s, want)
	}

	s, err = r.Scope(context.Background(), v, v.files["app/Program.cs"])
	if err != nil {
		t.Fatal(err)
	}
	want = resolvers.Scope{
		Qualifiers: map[string][]string{
			"G":   {"Demo.Geometry"}, // a namespace alias
			"UiC": {"Demo.Ui"},       // a nested namespace: its outermost namespace
			"Cus": {"Demo.Data"},     // a type alias: the type's namespace
		},
		Open: []string{"Demo", "", "Demo.Geometry", "Demo.Orders", "Demo.Ui"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("scope of Program.cs:\n got %+v\nwant %+v", s, want)
	}

	s, err = r.Scope(context.Background(), v, v.files["Global.cs"])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s, resolvers.Scope{}) {
		t.Errorf("scope of Global.cs = %+v, want nothing", s)
	}
}

func TestResolvers_csharpVisible(t *testing.T) {
	v := newCSharpView(t, map[string]string{
		"ui/Form1.Designer.cs": csharpGolden(t, "Form1.Designer.cs"),
		"ui/Form1.cs":          "namespace Demo.Ui;\npublic partial class Form1 { public Form1() { InitializeComponent(); } }\n",
		"orders/Orders.cs":     csharpGolden(t, "Orders.cs"),
		"other/Dialog.cs":      csharpDialog,
	})
	r, _ := resolvers.Lookup(codeparse.FamilyCSharp)
	designer, form, orders, dialog := v.files["ui/Form1.Designer.cs"], v.files["ui/Form1.cs"], v.files["orders/Orders.cs"], v.files["other/Dialog.cs"]
	for _, c := range []struct {
		from       *resolvers.File
		path, id   string
		want       bool
		reasonText string
	}{
		{form, "ui/Form1.Designer.cs", "Demo.Ui.Form1.InitializeComponent", true, "a private member of another part of the same partial type"},
		{form, "ui/Form1.Designer.cs", "Demo.Ui.Form1.Nested.Make", true, "internal"},
		{form, "ui/Form1.Designer.cs", "Demo.Ui.Form1.Dispose", true, "protected"},
		{orders, "ui/Form1.Designer.cs", "Demo.Ui.Form1.InitializeComponent", false, "private, from a file without the type"},
		{orders, "ui/Form1.Designer.cs", "Demo.Ui.Form1", true, "an internal top-level type, from another namespace"},
		{orders, "ui/Form1.Designer.cs", "Demo.Ui.Form1~2", false, "the second part of a partial type in one file"},
		{designer, "ui/Form1.Designer.cs", "Demo.Ui.Form1~2", false, "the second part, from its own file"},
		{orders, "ui/Form1.cs", "Demo.Ui.Form1", true, "a part in a file of its own"},
		{designer, "ui/Form1.Designer.cs", "Demo.Ui.Form1.okButton", true, "private, in the same file"},
		{form, "ui/Form1.Designer.cs", "Demo.Ui.Form1.Trace~2", true, "an overload is not a partial part"},
		{orders, "other/Dialog.cs", "Demo.Other.Dialog.InitializeComponent", false, "private"},
		{orders, "other/Dialog.cs", "Demo.Other.Dialog.Show", true, "internal static"},
		{orders, "other/Dialog.cs", "Demo.Other.Dialog.Open", true, "public static"},
		{orders, "other/Dialog.cs", "Demo.Other.Hidden", false, "file-local, from another file"},
		{dialog, "other/Dialog.cs", "Demo.Other.Hidden", true, "file-local, from its file"},
		{dialog, "orders/Orders.cs", "Demo.Orders.OrderService.Validate", false, "private static"},
		{orders, "orders/Orders.cs", "Demo.Orders.OrderService.Validate", true, "private, in the same file"},
	} {
		cand := v.candidate(t, c.path, c.id)
		for _, step := range []resolvers.Step{resolvers.StepPackage, resolvers.StepMember, resolvers.StepName} {
			if got := r.Visible(c.from, cand, step); got != c.want {
				t.Errorf("Visible(%s → %s#%s, %v) = %v, want %v (%s)", c.from.Path, c.path, c.id, step, got, c.want, c.reasonText)
			}
		}
	}
}

// csharpProgram exercises the using forms against a project.
const csharpProgram = `using Demo.Geometry;
using G = Demo.Geometry;
using UiC = Demo.Ui.Controls;
using Cus = Demo.Data.Customer;
using static Demo.Tools.Io.LineReader;
using global::Demo.Orders;
using Demo.Ui.Controls;

namespace Demo.App
{
    class Program
    {
        static void Main()
        {
            var p = new Point(1, 2);
            G.Size size = null;
            UiC.Form1 form = null;
            var text = Dump(p);
            Shared shared = null;
            GlobalThing global = null;
            Demo.Other.Dialog.Show();
            Hidden hidden = null;
            OrderService service = null;
            Cus.Notify(null);
            Unknown.Call();
            Local();
        }

        static void Local() { }
    }
}
`

const csharpDialog = `namespace Demo.Other
{
    class Dialog
    {
        private void InitializeComponent() { }
        internal static void Show() { }
        public static void Open() { }
    }

    file class Hidden { }
}
`

// csharpEnv indexes a tree through codeindex with the real C# extractor and
// the resolvers, into a memory graph.
type csharpEnv struct {
	t   *testing.T
	ctx context.Context
	g   codegraph.Graph
}

func newCSharpEnv(t *testing.T, tree map[string]string) *csharpEnv {
	t.Helper()
	ctx := context.Background()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	project := "u0123456789abcdef__csharp-" + hex.EncodeToString(b)
	store, graphs := memcode.New(), memgraph.New()
	engine, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
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
	const wt = "wt_00000000000000c5"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := sha1.Sum([]byte("commit main"))
	ch := code.Change{Worktree: wt, Ref: "main", Head: hex.EncodeToString(commit[:]), Upstream: hex.EncodeToString(commit[:]), Full: true}
	for p, c := range tree {
		sum, oid := sha256.Sum256([]byte(c)), sha1.Sum([]byte(c))
		if err := store.PutBlob(ctx, code.Blob{SHA256: hex.EncodeToString(sum[:]), Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(c)),
			GitOID: hex.EncodeToString(oid[:]), Mode: "100644", Lang: "csharp"})
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
	t.Cleanup(func() { _ = g.Drop(context.Background()) })
	return &csharpEnv{t: t, ctx: ctx, g: g}
}

// edges returns the edges of relation rel at the symbol path#id, as the
// other end's node id → confidence.
func (e *csharpEnv) edges(rel codegraph.Relation, path, id string, incoming bool) map[string]string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: rel})
	if err != nil {
		e.t.Fatalf("%s of %s#%s: %v", rel, path, id, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		other := r.To.ID
		if incoming {
			other = r.From.ID
		}
		out[other] = r.Confidence
	}
	return out
}

func csharpWant(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

func TestResolve_csharp(t *testing.T) {
	e := newCSharpEnv(t, map[string]string{
		"tools/Usings.cs":      csharpGolden(t, "Usings.cs"),
		"ui/Form1.Designer.cs": csharpGolden(t, "Form1.Designer.cs"),
		"ui/Form1.cs":          "namespace Demo.Ui;\n\npublic partial class Form1\n{\n    public Form1()\n    {\n        InitializeComponent();\n    }\n}\n",
		"geometry/Shapes.cs":   csharpGolden(t, "Shapes.cs"),
		"orders/Orders.cs":     csharpGolden(t, "Orders.cs"),
		"data/Data.cs": `namespace Demo.Data
{
    public abstract class Entity { }
    public interface IAudited { }
    class Money { }
    public class Customer { public static void Notify(object e) { } }
}
`,
		"other/Dialog.cs": csharpDialog,
		"Shared.cs":       "namespace Demo { public class Shared { } }\n",
		"Global.cs":       "public class GlobalThing { }\n",
		"app/Program.cs":  csharpProgram,
	})
	const prog = "app/Program.cs"
	main := "Demo.App.Program.Main"

	csharpWant(t, "calls of Main", e.edges(codegraph.RelCallees, prog, main, false), map[string]string{
		"tools/Usings.cs#Demo.Tools.Io.LineReader.Dump": "name",      // using static: the name fallback
		"other/Dialog.cs#Demo.Other.Dialog.Show":        "qualified", // a qualified name, internal
		"data/Data.cs#Demo.Data.Customer.Notify":        "name",      // a type alias: a member of its namespace
		prog + "#Demo.App.Program.Local":                "exact",     // the enclosing type
	})
	for _, c := range []struct{ path, id, conf string }{
		{"geometry/Shapes.cs", "Demo.Geometry.Point", "exact"},    // using
		{"geometry/Shapes.cs", "Demo.Geometry.Size", "exact"},     // a namespace alias
		{"Shared.cs", "Demo.Shared", "exact"},                     // an enclosing namespace
		{"Global.cs", "GlobalThing", "exact"},                     // the global namespace
		{"orders/Orders.cs", "Demo.Orders.OrderService", "exact"}, // using global::
		{"ui/Form1.cs", "Demo.Ui.Form1", "exact"},                 // two files' parts: one type, the exported part
	} {
		got := e.edges(codegraph.RelReferences, c.path, c.id, true)
		if got[prog+"#"+main] != c.conf {
			t.Errorf("references of %s#%s = %v, want Main %s", c.path, c.id, got, c.conf)
		}
	}
	if got := e.edges(codegraph.RelReferences, "other/Dialog.cs", "Demo.Other.Hidden", true); len(got) != 0 {
		t.Errorf("a file-local class is referenced from another file: %v", got)
	}

	// Partial types: the constructor in Form1.cs reaches the private
	// InitializeComponent of the Designer's part (the enclosing type's
	// other part), and not Dialog's.
	csharpWant(t, "calls of the Form1 constructor", e.edges(codegraph.RelCallees, "ui/Form1.cs", "Demo.Ui.Form1.Form1", false), map[string]string{
		"ui/Form1.Designer.cs#Demo.Ui.Form1.InitializeComponent": "exact",
	})
	// Inside the Designer: Trace has two definitions (#if/#else); Add,
	// through this.Controls (inherited from Form, outside the project),
	// is any member of that name.
	csharpWant(t, "calls of InitializeComponent", e.edges(codegraph.RelCallees, "ui/Form1.Designer.cs", "Demo.Ui.Form1.InitializeComponent", false), map[string]string{
		"ui/Form1.Designer.cs#Demo.Ui.Form1.Trace":            "name",
		"geometry/Shapes.cs#Demo.Geometry.Circle.Builder.Add": "name",
	})

	// Orders.cs: a using of another namespace, a qualified interface, an
	// internal class, a private method of the enclosing type.
	for _, c := range []struct {
		rel      codegraph.Relation
		path, id string
		from     string
		conf     string
	}{
		{codegraph.RelImplementations, "data/Data.cs", "Demo.Data.Entity", "Demo.Orders.Order", "exact"},
		{codegraph.RelImplementations, "data/Data.cs", "Demo.Data.IAudited", "Demo.Orders.Order", "qualified"},
		{codegraph.RelReferences, "data/Data.cs", "Demo.Data.Money", "Demo.Orders.Order.Totals", "exact"},
		{codegraph.RelCallers, "orders/Orders.cs", "Demo.Orders.OrderService.Validate", "Demo.Orders.OrderService.Get", "exact"},
		{codegraph.RelImplementations, "orders/Orders.cs", "Demo.Orders.IRepository", "Demo.Orders.IOrderRepository", "exact"},
	} {
		got := e.edges(c.rel, c.path, c.id, true)
		if got["orders/Orders.cs#"+c.from] != c.conf {
			t.Errorf("%s of %s#%s = %v, want %s %s", c.rel, c.path, c.id, got, c.from, c.conf)
		}
	}
	// An alias of a type outside the project resolves nothing.
	if got := e.edges(codegraph.RelCallees, "tools/Usings.cs", "Demo.Tools.Io.LineReader.Dump", false); len(got) != 0 {
		t.Errorf("Json.Serialize resolved to %v", got)
	}
}
