package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"reflect"
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

// rubyProfile is the Ruby profile compiled into the binary.
func rubyProfile(t *testing.T) *codeparse.Profile {
	t.Helper()
	e, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	p := e.Profiles()["ruby"]
	if p == nil {
		t.Fatal("no ruby profile")
	}
	return p
}

func TestResolverRules_ruby(t *testing.T) {
	fam, res, ok := resolvers.ForLang("ruby")
	if !ok || fam != codeparse.FamilyRuby {
		t.Fatalf("ForLang(ruby) = %q, %v", fam, ok)
	}
	p := rubyProfile(t)
	rules := res.Rules()
	if !reflect.DeepEqual(rules.Separators, p.Scope.Separators) {
		t.Errorf("separators %v, profile %v", rules.Separators, p.Scope.Separators)
	}
	if !rules.NameFallback || !rules.MemberFallback || rules.CaseInsensitive || !rules.ImportMembers || !rules.AbsoluteQualifiers {
		t.Errorf("rules = %+v", rules)
	}
	if got := p.Scope.Options[resolvers.RubyLoadPathOption]; got != resolvers.RubyDefaultLoadPath {
		t.Errorf("profile loadPath = %q, the resolver's default is %q", got, resolvers.RubyDefaultLoadPath)
	}
}

func TestRubyFeature_ruby(t *testing.T) {
	lib := []string{"lib"}
	for _, c := range []struct {
		in       string
		loadPath []string
		want     string
	}{
		{"lib/shop/order.rb", lib, "shop/order"},
		{"shop/order", lib, "shop/order"},
		{"shop/order.rb", lib, "shop/order"},
		{"./shop/order", lib, "shop/order"},
		{"app/models/order.rb", lib, "app/models/order"},
		{"gems/pay/lib/pay.rb", lib, "pay"},
		{"gems/pay/lib/pay/lib/x.rb", lib, "pay/lib/x"},
		{"lib/tasks/shop.rake", lib, "tasks/shop"},
		{"lib", lib, "lib"},
		{"lib/x.rb", nil, "lib/x"},
		{"Gemfile", lib, "Gemfile"},
		{"app/lib/x.rb", []string{"app/lib", "lib"}, "x"},
		{"src/a/lib/b.rb", []string{"lib", "src"}, "a/lib/b"},
		{"library/x.rb", lib, "library/x"},
		{".rb", lib, ".rb"},
	} {
		if got := resolvers.RubyFeature(c.in, c.loadPath); got != c.want {
			t.Errorf("RubyFeature(%q, %v) = %q, want %q", c.in, c.loadPath, got, c.want)
		}
	}
}

// rubyView is a View over hand-built Ruby files, keyed by their feature.
type rubyView struct{ files map[string][]*resolvers.File }

func (rubyView) Layer() string                                         { return "base" }
func (rubyView) Has(string) bool                                       { return false }
func (rubyView) Content(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func (v rubyView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilyRuby {
		return nil
	}
	return v.files[key]
}

func newRubyView(t *testing.T, res resolvers.Resolver, files ...*resolvers.File) rubyView {
	t.Helper()
	v := rubyView{files: map[string][]*resolvers.File{}}
	for _, f := range files {
		key, err := res.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
		v.files[key] = append(v.files[key], f)
	}
	return v
}

func rubyImports(ims ...codeparse.Import) *codeparse.Facts {
	return &codeparse.Facts{Lang: "ruby", Imports: ims}
}

func TestScope_ruby(t *testing.T) {
	ctx := context.Background()
	_, res, _ := resolvers.ForLang("ruby")
	prof := rubyProfile(t)
	req := func(kind, spec string, names ...string) codeparse.Import {
		return codeparse.Import{Kind: kind, Spec: spec, Names: names}
	}
	app := &resolvers.File{Path: "app/models/order.rb", Profile: prof, Facts: rubyImports(
		req("require", "bigdecimal"),         // outside the project
		req("require", "shop"),               // lib/shop.rb, which requires more
		req("require_relative", "line_item"), // app/models/line_item.rb
		req("require_relative", "../concerns/auditable"),
		req("load", "tasks/seed.rb"), // lib/tasks/seed.rb
		req("autoload", "billing/ledger", "Ledger"),
		req("autoload", "missing/thing", "Thing"),
		req("gem", "rails"),
		req("require", "shop"), // twice
	)}
	files := []*resolvers.File{
		app,
		{Path: "app/models/line_item.rb", Profile: prof, Facts: rubyImports()},
		{Path: "app/concerns/auditable.rb", Profile: prof, Facts: rubyImports(req("require", "app/models/order"))}, // a cycle
		{Path: "lib/shop.rb", Profile: prof, Facts: rubyImports(req("require", "shop/order"), req("require_relative", "shop/cart"))},
		{Path: "lib/shop/order.rb", Profile: prof, Facts: rubyImports(req("require_relative", "../money"))},
		{Path: "lib/shop/cart.rb", Profile: prof, Facts: rubyImports()},
		{Path: "lib/money.rb", Profile: prof},
		{Path: "lib/tasks/seed.rb", Profile: prof, Facts: rubyImports()},
		{Path: "lib/billing/ledger.rb", Profile: prof, Facts: rubyImports(req("require", "never/followed"))},
		{Path: "lib/never/followed.rb", Profile: prof, Facts: rubyImports()},
	}
	v := newRubyView(t, res, files...)
	if app.Package != "app/models/order" || files[3].Package != "shop" {
		t.Fatalf("packages %q, %q", app.Package, files[3].Package)
	}
	s, err := res.Scope(ctx, v, app)
	if err != nil {
		t.Fatal(err)
	}
	wantOpen := []string{"shop", "app/models/line_item", "app/concerns/auditable", "tasks/seed", "shop/order", "shop/cart", "money"}
	if !reflect.DeepEqual(s.Open, wantOpen) {
		t.Errorf("open = %v\nwant %v", s.Open, wantOpen)
	}
	if !reflect.DeepEqual(s.Names, map[string][]string{"Ledger": {"billing/ledger"}}) {
		t.Errorf("names = %v", s.Names)
	}
	if len(s.Qualifiers) != 0 {
		t.Errorf("qualifiers = %v: a require binds no name", s.Qualifiers)
	}

	// No facts, no scope.
	if s, err := res.Scope(ctx, v, &resolvers.File{Path: "x.rb"}); err != nil || len(s.Open)+len(s.Names) != 0 {
		t.Errorf("scope without facts = %+v, %v", s, err)
	}

	// The load path comes from the profile.
	custom := *prof
	custom.Scope.Options = map[string]string{resolvers.RubyLoadPathOption: "src, app/lib/"}
	for p, want := range map[string]string{"src/a.rb": "a", "app/lib/b.rb": "b", "lib/c.rb": "lib/c"} {
		if got, _ := res.Package(ctx, v, &resolvers.File{Path: p, Profile: &custom}); got != want {
			t.Errorf("package of %s with a custom load path = %q, want %q", p, got, want)
		}
	}
	if got, _ := res.Package(ctx, v, &resolvers.File{Path: "lib/c.rb"}); got != "c" {
		t.Errorf("package without a profile = %q, want c", got)
	}
}

func TestVisible_ruby(t *testing.T) {
	_, res, _ := resolvers.ForLang("ruby")
	from := &resolvers.File{Path: "a.rb", Package: "a"}
	other := &resolvers.File{Path: "b.rb", Package: "b"}
	for kind, want := range map[string]bool{
		codeparse.KindClass: true, codeparse.KindModule: true, codeparse.KindMethod: true,
		codeparse.KindConst: true, codeparse.KindProperty: true, codeparse.KindFunction: true,
		"task": false, codeparse.KindNamespace: false,
	} {
		c := resolvers.Candidate{File: other, Symbol: &codeparse.Symbol{Kind: kind, Name: "x"}}
		if got := res.Visible(from, c, resolvers.StepName); got != want {
			t.Errorf("Visible(%s) = %v, want %v", kind, got, want)
		}
	}
}

// rubyEnv indexes a Ruby tree with the real parser and the resolver.
type rubyEnv struct {
	t     *testing.T
	ctx   context.Context
	g     codegraph.Graph
	facts map[string]codeparse.Facts
}

const rubyProject = "u0123456789abcdef__github.com__o__ruby"

func rubyHexSum(h []byte) string { return hex.EncodeToString(h) }

func indexRuby(t *testing.T, tree map[string]string) *rubyEnv {
	t.Helper()
	ctx := context.Background()
	engine, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	store := memcode.New()
	graphs := memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Code: func(_ context.Context, p string) (code.Store, error) {
			if p != rubyProject {
				return nil, code.ErrNotFound
			}
			return store, nil
		},
		Graphs:       graphs,
		Cursors:      codeindex.NewMemCursors(nil),
		Apply:        codeindex.NewResolver(codeindex.ResolverOptions{Profiles: engine.Profiles()}),
		Parse:        codeparse.Parse,
		ParseVersion: codeparse.ParseVersion,
		Holder:       "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_00000000000000a1"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := sha1.Sum([]byte("ruby main"))
	ch := code.Change{Worktree: wt, Ref: "main", Head: rubyHexSum(commit[:]), Upstream: rubyHexSum(commit[:]), Full: true}
	e := &rubyEnv{t: t, ctx: ctx, facts: map[string]codeparse.Facts{}}
	for p, content := range tree {
		sum := sha256.Sum256([]byte(content))
		gitOID := sha1.Sum([]byte(content))
		if err := store.PutBlob(ctx, code.Blob{SHA256: rubyHexSum(sum[:]), Size: int64(len(content)), Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: rubyHexSum(sum[:]), Size: int64(len(content)),
			GitOID: rubyHexSum(gitOID[:]), Mode: "100644", Lang: "ruby"})
		facts, err := codeparse.Parse(p, []byte(content))
		if err != nil {
			t.Fatal(err)
		}
		e.facts[p] = facts
	}
	if _, err := store.Commit(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, rubyProject); err != nil {
		t.Fatal(err)
	}
	if e.g, err = graphs.Open(ctx, rubyProject); err != nil {
		t.Fatal(err)
	}
	return e
}

// edges returns every resolved edge of the base view as
// "from -type-> to" → confidence, nodes written path#id.
func (e *rubyEnv) edges() map[string]string {
	e.t.Helper()
	out := map[string]string{}
	paths := make([]string, 0, len(e.facts))
	for p := range e.facts {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		for _, sym := range e.facts[p].Symbols {
			for _, rel := range []codegraph.Relation{codegraph.RelCallers, codegraph.RelReferences, codegraph.RelImplementations} {
				rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, sym.ID), Relation: rel})
				if err != nil {
					e.t.Fatalf("%s of %s#%s: %v", rel, p, sym.ID, err)
				}
				for _, r := range rows {
					out[r.From.ID+" -"+strings.ToLower(string(r.Type))+"-> "+r.To.ID] = r.Confidence
				}
			}
		}
	}
	return out
}

func readTestdata(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(path.Join("..", "..", "codeparse", "testdata", "ruby", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResolve_ruby(t *testing.T) {
	tree := map[string]string{
		"Gemfile":                   readTestdata(t, "Gemfile"),
		"lib/shop/order.rb":         readTestdata(t, "order.rb"),
		"lib/billing.rb":            readTestdata(t, "nesting.rb"),
		"lib/tasks/shop.rake":       readTestdata(t, "tasks.rake"),
		"lib/shop/line_item.rb":     "module Shop\n  class LineItem\n    def price = 1\n  end\nend\n",
		"lib/concerns/auditable.rb": "module Shop\n  module Auditable\n    def audit!\n    end\n  end\nend\n",
		"lib/shop/importer.rb":      "require_relative \"mailer\"\n\nmodule Shop\n  class Importer\n    def run\n      Mailer.order_placed(nil)\n    end\n  end\nend\n",
		"lib/shop/mailer.rb":        "module Shop\n  class Mailer\n    def self.order_placed(order) = order\n  end\nend\n",
		"lib/instrumented.rb":       "module Instrumented\nend\n",
		"lib/repository.rb":         "class Repository\n  def self.instance = new\n  def fetch(id) = id\nend\n",
		"app/other/repository.rb":   "class Repository\nend\n",
		"lib/billing/ledger.rb":     "class Ledger\nend\n",
		"app/ledger.rb":             "class Ledger\nend\n",
		"lib/accounts.rb":           "autoload :Ledger, \"billing/ledger\"\n\nclass Accounts\n  def book = Ledger.new\nend\n",
		"lib/report.rb":             "require \"repository\"\n\nclass Report\n  def build = Repository.instance\nend\n",
		"lib/summary.rb":            "require_relative \"report\"\n\nclass Summary\n  def build = Repository.new\n  def plain = Ledger.new\nend\n",
		"lib/cli.rb":                "def main\n  reindex\n  ::Shop::Mailer.order_placed(1)\n  ::Shop\nend\n",
		"lib/helpers.rb":            "module Helpers\n  def fmt(x) = x\nend\n",
		"lib/page.rb":               "require \"helpers\"\n\nclass Page\n  include Helpers\n  def show = fmt(1)\nend\n",
	}
	got := indexRuby(t, tree).edges()
	want := map[string]string{
		// require: a top-level constant two files define resolves to the
		// required one, directly or through another file's require.
		"lib/report.rb#Report::build -references-> lib/repository.rb#Repository":      "exact",
		"lib/summary.rb#Summary::build -references-> lib/repository.rb#Repository":    "exact",
		"lib/report.rb#Report::build -calls-> lib/repository.rb#Repository::instance": "exact",
		// Not required: any definition of the name, the best of two.
		"lib/shop/order.rb#Shop::Order::find -references-> app/other/repository.rb#Repository": "name",
		// autoload imports the name from its feature.
		"lib/accounts.rb#Accounts::book -references-> lib/billing/ledger.rb#Ledger": "exact",
		"lib/summary.rb#Summary::plain -references-> app/ledger.rb#Ledger":          "name",
		// Module::Class nesting: the lexical scope, then qualified names; a
		// path written from the root (Shop::Mailer, Billing::Invoice) is
		// the whole qualified name (AbsoluteQualifiers: exact), one written
		// from the lexical scope (Mailer in Shop, Gateways::Stripe in
		// Billing) a suffix of it.
		"lib/shop/order.rb#Shop::Order::initialize -references-> lib/shop/order.rb#Shop::LineItem":        "exact",
		"lib/shop/order.rb#Shop::Order::place! -references-> lib/shop/order.rb#Shop::Error":               "exact",
		"lib/shop/order.rb#Shop::Order::place! -references-> lib/shop/order.rb#Shop::MAX_ITEMS":           "exact",
		"lib/shop/order.rb#Shop::Order::place! -calls-> lib/shop/order.rb#Shop::Order::notify":            "exact",
		"lib/shop/order.rb#format_money -references-> lib/shop/order.rb#Shop::CURRENCY":                   "exact",
		"lib/shop/order.rb#Shop::Order::place! -references-> lib/shop/mailer.rb#Shop::Mailer":             "exact",
		"lib/shop/order.rb#Shop::Order::place! -calls-> lib/shop/mailer.rb#Shop::Mailer::order_placed":    "exact",
		"lib/shop/importer.rb#Shop::Importer::run -calls-> lib/shop/mailer.rb#Shop::Mailer::order_placed": "qualified",
		"lib/billing.rb#Billing::Invoice::pay -references-> lib/billing.rb#Billing::Gateways::Stripe":     "qualified",
		"lib/billing.rb#Billing::Invoice::== -references-> lib/billing.rb#Billing::Invoice":               "exact",
		"lib/billing.rb#Refund -references-> lib/billing.rb#Billing::Invoice":                             "exact",
		"lib/tasks/shop.rake#shop::import -references-> lib/shop/importer.rb#Shop::Importer":              "exact",
		"lib/tasks/shop.rake#shop::orders::purge -references-> lib/shop/order.rb#Shop::Order":             "exact",
		// A module reopened in several required files is ambiguous.
		"lib/tasks/shop.rake#shop::import -references-> lib/shop/importer.rb#Shop": "name",
		// include, extend and prepend link the class to the module.
		"lib/shop/order.rb#Shop::Order -extends-> lib/concerns/auditable.rb#Shop::Auditable": "exact",
		"lib/shop/order.rb#Shop::Order -extends-> lib/instrumented.rb#Instrumented":          "name",
		// A root qualifier is the whole qualified name.
		"lib/cli.rb#main -calls-> lib/shop/mailer.rb#Shop::Mailer::order_placed": "exact",
		// A method of a module a required file defines, mixed in: the
		// module's member through the require (ImportMembers).
		"lib/page.rb#Page::show -calls-> lib/helpers.rb#Helpers::fmt": "qualified",
		"lib/page.rb#Page -extends-> lib/helpers.rb#Helpers":          "exact",
		// A method through a receiver is a member.
		"lib/shop/order.rb#Shop::Order::total -calls-> lib/shop/line_item.rb#Shop::LineItem::price": "name",
	}
	for k, conf := range want {
		if got[k] != conf {
			t.Errorf("%s: confidence %q, want %q", k, got[k], conf)
		}
	}
	for k := range got {
		from, to, _ := strings.Cut(k, " -")
		switch {
		case strings.Contains(to, "lib/tasks/shop.rake#"):
			t.Errorf("%s: a Rake task or namespace is no target", k)
		case strings.HasPrefix(from, "Gemfile#"):
			t.Errorf("%s: a Gemfile defines nothing", k)
		case strings.Contains(k, "-extends-> ") && strings.HasSuffix(k, "#Comparable"):
			t.Errorf("%s: Comparable is not in the project", k)
		}
	}
}
