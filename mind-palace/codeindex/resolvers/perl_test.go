package resolvers_test

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
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

// The Perl resolver, end to end: the extractor's goldens for the files of
// codeparse/testdata/perl, plus hand-built facts for what those files do
// not show (other packages, scripts that require each other) and for the
// behaviour the extractor does not give yet (a file of several statement
// packages, sub Other::f qualified by Other alone).

// perlGolden reads the facts the extractor gives for a testdata input.
func perlGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "codeparse", "testdata", "perl", name+".facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// perlSrc builds facts by hand, the way the Perl extractor writes them.
type perlSrc struct {
	f    codeparse.Facts
	line int
}

func perlFile(pkg string) *perlSrc { return &perlSrc{f: codeparse.Facts{Lang: "perl", Package: pkg}} }

func (p *perlSrc) pos() codeparse.Pos {
	p.line++
	return codeparse.Pos{Line: p.line, Offset: p.line * 100}
}

func (p *perlSrc) use(kind, spec string, names ...string) *perlSrc {
	p.f.Imports = append(p.f.Imports, codeparse.Import{Spec: spec, Kind: kind, Names: names, Start: p.pos()})
	return p
}

// sub adds a top-level sub whose qualified name is qual (Pkg::name, or a
// bare name in a script); its id is qual.
func (p *perlSrc) sub(qual string) *perlSrc {
	name := qual
	if i := strings.LastIndex(qual, "::"); i >= 0 {
		name = qual[i+2:]
	}
	s := p.pos()
	p.f.Symbols = append(p.f.Symbols, codeparse.Symbol{ID: qual, Kind: codeparse.KindFunction, Name: name,
		QualName: qual, Signature: "sub " + name, Start: s, End: s, Exported: !strings.HasPrefix(name, "_")})
	return p
}

// abs adds a sub declared by its full name (sub Other::f).
func (p *perlSrc) abs(qual string) *perlSrc {
	p.sub(qual)
	p.f.Symbols[len(p.f.Symbols)-1].Signature = "sub " + qual
	return p
}

// class adds a block package or class symbol (named by its full name).
func (p *perlSrc) class(kind, name string) *perlSrc {
	s := p.pos()
	p.f.Symbols = append(p.f.Symbols, codeparse.Symbol{ID: name, Kind: kind, Name: name, QualName: name, Start: s, End: s, Exported: true})
	return p
}

// call adds a call from the sub enclosing to target: Pkg::name,
// $obj->name (written "$obj->name") or a bare name.
func (p *perlSrc) call(enclosing, target string) *perlSrc {
	qual, name := "", target
	if i := strings.LastIndex(target, "->"); i >= 0 {
		qual, name = target[:i], target[i+2:]
	} else if i := strings.LastIndex(target, "::"); i >= 0 {
		qual, name = target[:i], target[i+2:]
	}
	p.f.Refs = append(p.f.Refs, codeparse.Ref{Name: name, Qualifier: qual, Kind: codeparse.RefCall, Enclosing: enclosing, Start: p.pos()})
	return p
}

func (p *perlSrc) extends(enclosing, parent string) *perlSrc {
	p.f.Refs = append(p.f.Refs, codeparse.Ref{Name: parent, Kind: codeparse.RefExtends, Enclosing: enclosing, Start: p.pos()})
	return p
}

func (p *perlSrc) src() string {
	b, err := json.Marshal(p.f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// perlEnv indexes a tree of facts (each file's content is its facts as
// JSON) with the resolving applier.
type perlEnv struct {
	t       *testing.T
	ctx     context.Context
	project string
	store   *memcode.Store
	g       codegraph.Graph
	svc     *codeindex.Service
}

func newPerlEnv(t *testing.T) *perlEnv {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	e := &perlEnv{t: t, ctx: context.Background(), project: "u0123456789abcdef__perl-" + hex.EncodeToString(b), store: memcode.New()}
	graphs := memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{e.project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return e.store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{}),
		Parse: func(_ string, content []byte) (codeparse.Facts, error) {
			var f codeparse.Facts
			err := json.Unmarshal(content, &f)
			f.Chunks = nil
			return f, err
		},
		Holder: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	if e.g, err = graphs.Open(e.ctx, e.project); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.g.Drop(context.Background()) })
	return e
}

// index commits tree as main's checkout and syncs.
func (e *perlEnv) index(tree map[string]string) {
	e.t.Helper()
	hash := func(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }
	commit := hash("commit main")
	const wt = "wt_0000000000000001"
	if err := e.store.PutWorktree(e.ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		e.t.Fatal(err)
	}
	ch := code.Change{Worktree: wt, Ref: "main", Head: commit, Upstream: commit, Full: true}
	for p, c := range tree {
		sum := sha256.Sum256([]byte(c))
		sha := hex.EncodeToString(sum[:])
		if err := e.store.PutBlob(e.ctx, code.Blob{SHA256: sha, Size: int64(len(c)), Content: []byte(c)}); err != nil {
			e.t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(c)), GitOID: hash(c), Mode: "100644", Lang: "perl"})
	}
	if _, err := e.store.Commit(e.ctx, ch); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.svc.Sync(e.ctx, e.project); err != nil {
		e.t.Fatal(err)
	}
}

// out returns the targets of the symbol path#id along rel in the base
// view, as target → confidence.
func (e *perlEnv) out(rel codegraph.Relation, path, id string) map[string]string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: rel})
	if err != nil {
		e.t.Fatalf("%s of %s#%s: %v", rel, path, id, err)
	}
	got := map[string]string{}
	for _, r := range rows {
		if rel == codegraph.RelImplementations {
			got[r.From.ID] = r.Confidence
		} else {
			got[r.To.ID] = r.Confidence
		}
	}
	return got
}

func (e *perlEnv) want(rel codegraph.Relation, path, id string, want map[string]string) {
	e.t.Helper()
	if got := e.out(rel, path, id); !reflect.DeepEqual(got, want) {
		e.t.Errorf("%s of %s#%s:\n got %v\nwant %v", rel, path, id, got, want)
	}
}

const (
	circle = "lib/My/Shape/Circle.pm"
	report = "bin/report.pl"
	zoo    = "lib/Zoo.pm"
	rep    = "lib/My/Report.pm"
	multi  = "lib/Multi.pm"
)

func TestResolve_perl(t *testing.T) {
	e := newPerlEnv(t)
	e.index(map[string]string{
		circle:             perlGolden(t, "Circle.pm"),
		report:             perlGolden(t, "report.pl"),
		zoo:                perlGolden(t, "Modern.pm"),
		rep:                perlFile("My::Report").sub("My::Report::emit").sub("My::Report::flush").sub("My::Report::_private").src(),
		"lib/Zoo/Shape.pm": perlFile("").class(codeparse.KindClass, "Zoo::Shape").src(),
		// What the extractor will give once a statement package scopes the
		// subs after it and sub Other::f is absolute.
		multi: perlFile("Multi").sub("Multi::run").sub("Multi::Inner::step").abs("Other::absolute").
			call("Multi::run", "Multi::Inner::step").
			call("Multi::run", "Other::absolute").
			call("Multi::run", "$self->step"). // $self's package depends on the statement
			call("Multi::Inner::step", "run").
			src(),
		"lib/Multi/More.pm": perlFile("Multi").sub("Multi::more").
			call("Multi::more", "run").      // the package, another file
			call("Multi::more", "step").     // Multi::Inner's, not Multi's
			call("Multi::more", "absolute"). // Other's
			src(),
		"bin/caller.pl": perlFile("").use("pragma", "strict").use("use", "Multi").use("use", "My::Report").sub("go").
			call("go", "Multi::run").           // a bound single-segment module
			call("go", "emit").                 // use My::Report opens it
			call("go", "_private").             // but not its private subs
			call("go", "My::Report::_private"). // which a qualified call reaches
			call("go", "My::Shape::Registry::register").
			call("go", "Carp::croak"). // not in the project
			src(),
		"bin/caller2.pl": perlFile("").use("use", "Multi").use("use", "My::Report", "flush").sub("go").
			call("go", "Multi::run").         // Multi:: also names Multi::Inner: unbound
			call("go", "Multi::Inner::step"). // absolute, in a file of package Multi
			call("go", "Other::absolute").    // sub Other::absolute
			call("go", "flush").              // an explicit import
			call("go", "emit").               // an import list opens nothing else
			src(),
		"shapes/config.pl": perlFile("").sub("config_value").sub("_hidden").src(),
		"bin/run.pl":       perlFile("").use("require", "../shapes/config.pl").use("require", "My/Report.pm").sub("main").call("main", "config_value").call("main", "_hidden").call("main", "My::Report::emit").src(),
		"bin/other.pl":     perlFile("").sub("main").call("main", "config_value").call("main", "load_shapes").src(),
	})

	// Circle.pm: a constant of the file, $self's own subs, a module that is
	// not in the project, and the SUPER call the extractor reports as $self.
	e.want(codegraph.RelCallees, circle, "My::Shape::Circle::area", map[string]string{circle + "#My::Shape::Circle::PI": "exact"})
	e.want(codegraph.RelCallees, circle, "My::Shape::Circle::describe", map[string]string{
		circle + "#My::Shape::Circle::area":     "exact",
		circle + "#My::Shape::Circle::describe": "exact",
	})
	e.want(codegraph.RelCallees, circle, "My::Shape::Circle::My::Shape::Registry::register", map[string]string{})

	// report.pl: its own subs, Package::sub and Class->method by qualified
	// name, unknown objects and UNIVERSAL::can left out.
	e.want(codegraph.RelCallees, report, "main", map[string]string{
		report + "#load_shapes":            "exact",
		report + "#report":                 "exact",
		report + "#log_line":               "exact",
		rep + "#My::Report::emit":          "qualified",
		rep + "#My::Report::flush":         "qualified",
		circle + "#My::Shape::Circle::new": "qualified",
	})
	e.want(codegraph.RelCallees, report, "load_shapes", map[string]string{circle + "#My::Shape::Circle::new": "qualified"})

	// Modern.pm: $self in a block package reaches its attribute (a
	// definition of the package, which only a package block encloses);
	// a class's :isa reaches a class of another file by name.
	e.want(codegraph.RelCallees, zoo, "Zoo::Animal::greet", map[string]string{zoo + "#Zoo::Animal::name": "exact"})
	e.want(codegraph.RelImplementations, "lib/Zoo/Shape.pm", "Zoo::Shape", map[string]string{zoo + "#Zoo::Point": "name"})

	// Several packages in one file.
	e.want(codegraph.RelCallees, multi, "Multi::run", map[string]string{
		multi + "#Multi::Inner::step": "qualified",
		multi + "#Other::absolute":    "qualified",
	})
	e.want(codegraph.RelCallees, multi, "Multi::Inner::step", map[string]string{multi + "#Multi::run": "exact"})
	e.want(codegraph.RelCallees, "lib/Multi/More.pm", "Multi::more", map[string]string{
		multi + "#Multi::run":         "exact",
		multi + "#Multi::Inner::step": "name",
		multi + "#Other::absolute":    "name",
	})

	// Imports.
	e.want(codegraph.RelCallees, "bin/caller.pl", "go", map[string]string{
		multi + "#Multi::run":                                        "exact",
		rep + "#My::Report::emit":                                    "exact",
		rep + "#My::Report::_private":                                "qualified",
		circle + "#My::Shape::Circle::My::Shape::Registry::register": "qualified",
	})
	e.want(codegraph.RelCallees, "bin/caller2.pl", "go", map[string]string{
		multi + "#Multi::run":         "qualified",
		multi + "#Multi::Inner::step": "qualified",
		multi + "#Other::absolute":    "qualified",
		rep + "#My::Report::flush":    "exact",
		rep + "#My::Report::emit":     "name",
	})

	// Scripts: each is a package main of its own; a required script opens.
	e.want(codegraph.RelCallees, "bin/run.pl", "main", map[string]string{
		"shapes/config.pl#config_value": "exact",
		"shapes/config.pl#_hidden":      "exact",
		rep + "#My::Report::emit":       "qualified",
	})
	e.want(codegraph.RelCallees, "bin/other.pl", "main", map[string]string{
		"shapes/config.pl#config_value": "name",
		report + "#load_shapes":         "name",
	})
}

// perlView is a resolvers.View over a set of paths and package keys.
type perlView struct {
	paths    map[string]bool
	packages map[string][]*resolvers.File
}

func (v perlView) Layer() string     { return codegraph.BaseLayerID }
func (v perlView) Has(p string) bool { return v.paths[p] }
func (v perlView) Content(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (v perlView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilyPerl {
		return nil
	}
	return v.packages[key]
}

func perlFacts(t *testing.T, name string) *codeparse.Facts {
	t.Helper()
	var f codeparse.Facts
	if err := json.Unmarshal([]byte(perlGolden(t, name)), &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func TestPackageAndScope_perl(t *testing.T) {
	ctx := context.Background()
	res, ok := resolvers.Lookup(codeparse.FamilyPerl)
	if !ok {
		t.Fatal("no perl resolver")
	}
	if fam, r, ok := resolvers.ForLang("perl"); !ok || fam != codeparse.FamilyPerl || r != res {
		t.Fatalf("ForLang(perl) = %q, %v", fam, ok)
	}
	if r := res.Rules(); !reflect.DeepEqual(r.Separators, []string{"::"}) || r.CaseInsensitive || !r.NameFallback || !r.MemberFallback {
		t.Errorf("Rules = %+v", r)
	}

	files := map[string]*resolvers.File{}
	for p, name := range map[string]string{circle: "Circle.pm", report: "report.pl", zoo: "Modern.pm", "t/generated.t": "generated.t"} {
		f := &resolvers.File{Path: p, Lang: "perl", Family: codeparse.FamilyPerl, Facts: perlFacts(t, name)}
		key, err := res.Package(ctx, perlView{}, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
		files[p] = f
	}
	for p, want := range map[string]string{circle: "My::Shape::Circle", report: report, zoo: zoo, "t/generated.t": "Fixture::One"} {
		if got := files[p].Package; got != want {
			t.Errorf("Package(%s) = %q, want %q", p, got, want)
		}
	}

	v := perlView{paths: map[string]bool{"lib/Carp.pm": true, "shapes/config.pl": true},
		packages: map[string][]*resolvers.File{"Carp": {{}}, "shapes/config.pl": {{}}}}
	sc, err := res.Scope(ctx, v, files[circle])
	if err != nil {
		t.Fatal(err)
	}
	self := []string{"My::Shape::Circle"}
	want := resolvers.Scope{
		Qualifiers: map[string][]string{"Carp": {"Carp"}, "$self": self, "$class": self, "shift": self, "__PACKAGE__": self},
		Names:      map[string][]string{"floor": {"POSIX"}, "blessed": {"Scalar::Util"}},
		Open:       []string{"List::Util", "shapes/config.pl"},
	}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Scope(Circle.pm) =\n %+v\nwant\n %+v", sc, want)
	}
	sc, err = res.Scope(ctx, perlView{}, files[report])
	if err != nil {
		t.Fatal(err)
	}
	self = []string{report}
	want = resolvers.Scope{
		Qualifiers: map[string][]string{"$self": self, "$class": self, "shift": self, "__PACKAGE__": self},
		Names:      map[string][]string{"GetOptions": {"Getopt::Long"}, "wrap": {"Text::Wrap"}, "fill": {"Text::Wrap"}},
		Open:       []string{"My::Shape::Circle"},
	}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Scope(report.pl) =\n %+v\nwant\n %+v", sc, want)
	}

	// Sigils and tags in an import list; a .pm path is its module; a
	// single-segment module that also prefixes a longer qualifier is not
	// bound; several packages leave $self unbound.
	f := perlFile("Multi").use("use", "Exp", "$VERSION", "&f", ":all").use("require", "Fixture/Two.pm").
		use("require", "/abs/x.pl").use("require", "../../up.pl").use("use", "Fixture").
		sub("Multi::a").sub("Other::b").call("Multi::a", "Fixture::One::c").f
	sc, err = res.Scope(ctx, perlView{packages: map[string][]*resolvers.File{"Exp": {{}}, "Fixture": {{}}}},
		&resolvers.File{Path: "lib/Multi.pm", Package: "Multi", Facts: &f})
	if err != nil {
		t.Fatal(err)
	}
	want = resolvers.Scope{Qualifiers: map[string][]string{"Exp": {"Exp"}},
		Names: map[string][]string{"VERSION": {"Exp"}, "f": {"Exp"}}, Open: []string{"Exp", "Fixture"}}
	if !reflect.DeepEqual(sc, want) {
		t.Errorf("Scope(hand-built) =\n %+v\nwant\n %+v", sc, want)
	}
}

func TestVisible_perl(t *testing.T) {
	res, _ := resolvers.Lookup(codeparse.FamilyPerl)
	mk := func(pkg, key string) *resolvers.File {
		return &resolvers.File{Path: key + ".pm", Package: key, Facts: &codeparse.Facts{Package: pkg}}
	}
	from := mk("A", "A")
	own := mk("B", "B")
	sym := func(qual string) *codeparse.Symbol {
		name := qual[strings.LastIndex(qual, ":")+1:]
		return &codeparse.Symbol{ID: qual, Kind: codeparse.KindFunction, Name: name, QualName: qual, Exported: !strings.HasPrefix(name, "_")}
	}
	for _, c := range []struct {
		file *resolvers.File
		sym  *codeparse.Symbol
		step resolvers.Step
		want bool
	}{
		{own, sym("B::f"), resolvers.StepImport, true},
		{own, sym("B::_f"), resolvers.StepImport, true}, // an explicit import list may name it
		{own, sym("B::_f"), resolvers.StepOpen, false},
		{own, sym("B::_f"), resolvers.StepName, false},
		{own, sym("B::_f"), resolvers.StepQualified, true},
		{own, sym("B::_f"), resolvers.StepMember, true},
		{own, sym("C::f"), resolvers.StepImport, false}, // defined into C from B's file
		{own, sym("C::f"), resolvers.StepOpen, false},
		{own, sym("C::f"), resolvers.StepQualified, true},
		{own, sym("C::f"), resolvers.StepName, true},
		{from, sym("A::_f"), resolvers.StepName, true},
		{&resolvers.File{Path: "s.pl", Package: "s.pl", Facts: &codeparse.Facts{}}, sym("main"), resolvers.StepOpen, true},
		{own, &codeparse.Symbol{ID: "Zoo::P", Kind: codeparse.KindPackage, Name: "Zoo::P", QualName: "Zoo::P", Exported: true}, resolvers.StepImport, true},
	} {
		if got := res.Visible(from, resolvers.Candidate{File: c.file, Symbol: c.sym}, c.step); got != c.want {
			t.Errorf("Visible(%s in %s, %s) = %v, want %v", c.sym.QualName, c.file.Package, c.step, got, c.want)
		}
	}
}

func TestModuleOfPath_perl(t *testing.T) {
	for in, want := range map[string]string{"My/Module.pm": "My::Module", "Carp.pm": "Carp", "./A/B.pm": "A::B", "../x.pm": "", "/abs/x.pm": "", ".pm": ""} {
		if got := resolvers.PerlModuleOfPath(in); got != want {
			t.Errorf("PerlModuleOfPath(%q) = %q, want %q", in, got, want)
		}
	}
}
