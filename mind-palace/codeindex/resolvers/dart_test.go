package resolvers_test

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

// dartSrc is a Dart file: its directives as source text, and the facts the
// Dart extractor gives for it, built by hand. The file's content is the
// source followed by the facts as JSON on a "//facts " line, which
// dartParse reads back, so the resolver sees both the facts and the
// directives' text at their offsets.
type dartSrc struct {
	text strings.Builder
	f    codeparse.Facts
	line int
}

const dartFactsMarker = "\n//facts "

func dartFile(library string) *dartSrc {
	d := &dartSrc{f: codeparse.Facts{Lang: "dart", Package: library}}
	if library != "" {
		d.text.WriteString("library " + library + ";\n")
		d.line++
	}
	return d
}

func (d *dartSrc) pos() codeparse.Pos {
	d.line++
	return codeparse.Pos{Line: d.line, Offset: d.text.Len()}
}

// dir writes a directive and records its import fact: kind as the
// extractor writes it, the URI, the prefix and the combinators' names.
func (d *dartSrc) dir(text, kind, spec, alias string, names ...string) *dartSrc {
	d.f.Imports = append(d.f.Imports, codeparse.Import{Spec: spec, Alias: alias, Names: names, Kind: kind, Start: d.pos()})
	d.text.WriteString(text + "\n")
	return d
}

// at moves the last directive's start, where no source text says what
// its combinators are.
func (d *dartSrc) at(offset int) *dartSrc {
	d.f.Imports[len(d.f.Imports)-1].Start.Offset = offset
	return d
}

func (d *dartSrc) imp(text, spec, alias string, names ...string) *dartSrc {
	return d.dir(text, "import", spec, alias, names...)
}

func dartID(local string) string { return codeparse.SymbolID(local, 1) }

// def adds a definition; local is Name or Parent.Name.
func (d *dartSrc) def(kind, local string) *dartSrc {
	name, parent := local, ""
	if i := strings.LastIndex(local, "."); i >= 0 {
		name, parent = local[i+1:], dartID(local[:i])
	}
	p := d.pos()
	d.text.WriteString("// " + kind + " " + local + "\n")
	d.f.Symbols = append(d.f.Symbols, codeparse.Symbol{ID: dartID(local), Kind: kind, Name: name, QualName: local,
		Start: p, End: p, Parent: parent, Exported: !strings.HasPrefix(name, "_")})
	return d
}

func (d *dartSrc) fn(local string) *dartSrc { return d.def(codeparse.KindFunction, local) }

func (d *dartSrc) class(local string) *dartSrc { return d.def(codeparse.KindClass, local) }

// call adds a call from the definition enclosing to target, a name with
// its qualifier (u.add, helper).
func (d *dartSrc) call(enclosing, target string) *dartSrc {
	qual, name := "", target
	if i := strings.LastIndex(target, "."); i >= 0 {
		qual, name = target[:i], target[i+1:]
	}
	r := codeparse.Ref{Name: name, Qualifier: qual, Kind: codeparse.RefCall, Start: d.pos(), Enclosing: dartID(enclosing)}
	d.text.WriteString("// call " + target + "\n")
	d.f.Refs = append(d.f.Refs, r)
	return d
}

func (d *dartSrc) src() string {
	if err := d.f.Validate(); err != nil {
		panic(err) // the indexer would read the file as text
	}
	b, err := json.Marshal(d.f)
	if err != nil {
		panic(err)
	}
	return d.text.String() + dartFactsMarker + string(b) + "\n"
}

func dartParse(p string, content []byte) (codeparse.Facts, error) {
	i := bytes.LastIndex(content, []byte(dartFactsMarker))
	if !strings.HasSuffix(p, ".dart") || i < 0 {
		return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
	}
	var f codeparse.Facts
	err := json.Unmarshal(content[i+len(dartFactsMarker):], &f)
	return f, err
}

func dartSHA(content string) string {
	s := sha256.Sum256([]byte(content))
	return hex.EncodeToString(s[:])
}

func dartGitOID(content string) string {
	s := sha1.Sum([]byte(content))
	return hex.EncodeToString(s[:])
}

// dartEnv indexes a tree with the resolution engine into a memory graph.
type dartEnv struct {
	t   *testing.T
	ctx context.Context
	g   codegraph.Graph
}

func dartIndex(t *testing.T, tree map[string]string) *dartEnv {
	t.Helper()
	ctx := context.Background()
	const project = "u0123456789abcdef__dart"
	store := memcode.New()
	graphs := memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Code: func(_ context.Context, p string) (code.Store, error) {
			if p != project {
				return nil, code.ErrNotFound
			}
			return store, nil
		},
		Graphs:  graphs,
		Cursors: codeindex.NewMemCursors(nil),
		Apply:   codeindex.NewResolver(codeindex.ResolverOptions{}),
		Parse:   dartParse,
		Holder:  "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := fmt.Sprintf("wt_%016x", 1)
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := dartGitOID("commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: commit, Upstream: commit, Full: true}
	for p, c := range tree {
		if err := store.PutBlob(ctx, code.Blob{SHA256: dartSHA(c), Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: dartSHA(c), Size: int64(len(c)), GitOID: dartGitOID(c), Mode: "100644"})
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
	return &dartEnv{t: t, ctx: ctx, g: g}
}

// callees returns the targets of the calls of path#local in the base view,
// as target → confidence.
func (e *dartEnv) callees(p, local string) map[string]string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{
		ID: codeindex.SymbolNodeID(p, dartID(local)), Relation: codegraph.RelCallees})
	if err != nil {
		e.t.Fatalf("callees of %s#%s: %v", p, local, err)
	}
	out := map[string]string{}
	for _, r := range rows {
		out[r.To.ID] = r.Confidence
	}
	return out
}

func (e *dartEnv) want(p, local string, want map[string]string) {
	e.t.Helper()
	if got := e.callees(p, local); !reflect.DeepEqual(got, want) {
		e.t.Errorf("callees of %s#%s:\n got %v\nwant %v", p, local, got, want)
	}
}

func TestResolve_dart(t *testing.T) {
	main := dartFile("").
		imp("import 'dart:async';", "dart:async", "").
		imp("import 'package:flutter/material.dart';", "package:flutter/material.dart", "").
		imp("import 'package:util/util.dart' as u;", "package:util/util.dart", "u").
		imp("import 'package:app/models/user.dart';", "package:app/models/user.dart", "").
		imp("import 'widgets.dart' hide Hidden, Other;", "widgets.dart", "", "Hidden", "Other").
		imp("import \"helpers.dart\" show helper; // not otherHelper", "helpers.dart", "", "helper").
		dir("part 'main_part.dart';", "part", "main_part.dart", "").
		fn("main").class("App").def(codeparse.KindMethod, "App.build").fn("_private").
		call("main", "runApp").        // Flutter: not in the project
		call("main", "u.utilFn").      // the prefixed library
		call("main", "u.add").         // exported by it
		call("main", "u.capitalize").  // exported through show
		call("main", "u.Stream").      // a name the prefix does not reach
		call("main", "User").          // package: URI of this package, unprefixed
		call("main", "Shown").         // through hide
		call("main", "Hidden").        // hidden: only the name fallback
		call("main", "helper").        // shown
		call("main", "otherHelper").   // not shown
		call("main", "partFn").        // the library's part
		call("main", "_partPrivate").  // its private names too
		call("main", "_secret").       // another library's private name
		call("main", "dup").           // opened twice
		call("main", "_private").      // the file
		call("App.build", "App.build") // a static through its class
	part := dartFile("").
		dir("part of 'main.dart';", "part", "main.dart", "").
		fn("partFn").fn("_partPrivate").
		call("partFn", "helper"). // the library's imports reach its parts
		call("partFn", "main").
		call("partFn", "_private")
	tree := map[string]string{
		"pubspec.yaml":       "name: app\ndependencies:\n  flutter:\n    sdk: flutter\n  util:\n    path: packages/util\n",
		"lib/main.dart":      main.src(),
		"lib/main_part.dart": part.src(),
		"lib/models/user.dart": dartFile("").
			imp("import '../widgets.dart';", "../widgets.dart", "").
			class("User").fn("dup").src(),
		"lib/widgets.dart": dartFile("").class("Shown").class("Hidden").class("Other").fn("dup").src(),
		"lib/helpers.dart": dartFile("").fn("helper").fn("otherHelper").src(),
		"lib/other.dart": dartFile("").
			// combinators the source does not show: the Kind decides
			dir("// generated", "import_hide", "helpers.dart", "", "helper").at(1<<20).
			fn("other").call("other", "helper").call("other", "otherHelper").src(),
		"lib/legacy.dart": dartFile("app.legacy").
			dir("part 'legacy_part.dart';", "part", "legacy_part.dart", "").
			fn("legacyFn").fn("_legacyPrivate").call("legacyFn", "_lp").src(),
		"lib/legacy_part.dart": dartFile("").
			dir("part of app.legacy;", "part_of", "app.legacy", "").
			fn("_lp").call("_lp", "_legacyPrivate").src(),
		"packages/util/pubspec.yaml": "name: util\n",
		"packages/util/lib/util.dart": dartFile("util").
			dir("export 'src/strings.dart' show capitalize;", "export", "src/strings.dart", "", "capitalize").
			dir("export 'package:util/src/math.dart';", "export", "package:util/src/math.dart", "").
			fn("utilFn").src(),
		"packages/util/lib/src/strings.dart": dartFile("").fn("capitalize").fn("lowercase").src(),
		"packages/util/lib/src/math.dart":    dartFile("").fn("add").fn("_secret").src(),
		"README.md":                          "# app\n",
	}
	e := dartIndex(t, tree)

	e.want("lib/main.dart", "main", map[string]string{
		"packages/util/lib/util.dart#" + dartID("utilFn"):            "exact",
		"packages/util/lib/src/math.dart#" + dartID("add"):           "exact",
		"packages/util/lib/src/strings.dart#" + dartID("capitalize"): "exact",
		"lib/models/user.dart#" + dartID("User"):                     "exact",
		"lib/widgets.dart#" + dartID("Shown"):                        "exact",
		"lib/widgets.dart#" + dartID("Hidden"):                       "name",
		"lib/helpers.dart#" + dartID("helper"):                       "exact",
		"lib/helpers.dart#" + dartID("otherHelper"):                  "name",
		"lib/main_part.dart#" + dartID("partFn"):                     "exact",
		"lib/main_part.dart#" + dartID("_partPrivate"):               "exact",
		"lib/models/user.dart#" + dartID("dup"):                      "name",
		"lib/main.dart#" + dartID("_private"):                        "exact",
	})
	e.want("lib/main.dart", "App.build", map[string]string{"lib/main.dart#" + dartID("App.build"): "qualified"})
	e.want("lib/main_part.dart", "partFn", map[string]string{
		"lib/helpers.dart#" + dartID("helper"): "exact",
		"lib/main.dart#" + dartID("main"):      "exact",
		"lib/main.dart#" + dartID("_private"):  "exact",
	})
	e.want("lib/other.dart", "other", map[string]string{
		"lib/helpers.dart#" + dartID("helper"):      "name",
		"lib/helpers.dart#" + dartID("otherHelper"): "exact",
	})
	e.want("lib/legacy.dart", "legacyFn", map[string]string{"lib/legacy_part.dart#" + dartID("_lp"): "exact"})
	e.want("lib/legacy_part.dart", "_lp", map[string]string{"lib/legacy.dart#" + dartID("_legacyPrivate"): "name"})
}

// dartView is a View over file contents, for the URI rules.
type dartView map[string]string

func (v dartView) Layer() string                         { return codegraph.BaseLayerID }
func (v dartView) Has(p string) bool                     { _, ok := v[p]; return ok }
func (v dartView) Package(_, _ string) []*resolvers.File { return nil }
func (v dartView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v[p]
	return []byte(c), ok, nil
}

func TestURIPath_dart(t *testing.T) {
	v := dartView{
		"app/pubspec.yaml":            "name: app\ndev_dependencies:\n  test_utils:\n    path: ../tools/test_utils\ndependency_overrides:\n  shared: {path: ../shared}\n",
		"pubspec.yaml":                "name: root\nworkspace:\n  - pkgs/core\n  - ../outside\n",
		"pkgs/core/pubspec.yaml":      "name: core\nresolution: workspace\n",
		"packages/net/pubspec.yaml":   "name: net\n",
		"broken/pubspec.yaml":         "name: [unclosed\n",
		"tools/test_utils/x.dart":     "",
		"packages/other/pubspec.yaml": "name: not_other\n",
	}
	ctx := context.Background()
	for _, tc := range []struct{ dir, uri, want string }{
		{"app/lib/src", "../a.dart", "app/lib/a.dart"},
		{"app/lib", "src/b.dart", "app/lib/src/b.dart"},
		{"app/lib", "package:app/src/b.dart", "app/lib/src/b.dart"},
		{"app/lib", "package:test_utils/t.dart", "tools/test_utils/lib/t.dart"},
		{"app/lib", "package:shared/s.dart", "shared/lib/s.dart"},
		{"app/lib", "package:core/core.dart", "pkgs/core/lib/core.dart"},
		{"app/lib", "package:net/net.dart", "packages/net/lib/net.dart"},
		{"app/lib", "package:other/o.dart", "package:other/o.dart"},
		{"app/lib", "package:flutter/material.dart", "package:flutter/material.dart"},
		{"app/lib", "package:app/../../../x.dart", "package:app/../../../x.dart"},
		{"app/lib", "package:noslash", "package:noslash"},
		{"app/lib", "dart:async", "dart:async"},
		{"app", "../../escape.dart", "../../escape.dart"},
		{"broken/lib", "package:broken/b.dart", "package:broken/b.dart"},
		{".", "lib/a.dart", "lib/a.dart"},
	} {
		got, err := resolvers.DartURIPath(ctx, v, tc.dir, tc.uri)
		if err != nil || got != tc.want {
			t.Errorf("DartURIPath(%q, %q) = %q, %v; want %q", tc.dir, tc.uri, got, err, tc.want)
		}
	}
	if root, ok, err := resolvers.DartPackageRoot(ctx, v, "pkgs/core/lib", "root"); err != nil || !ok || root != "." {
		t.Errorf("DartPackageRoot(root) = %q, %v, %v", root, ok, err)
	}
	if _, ok, _ := resolvers.DartPackageRoot(ctx, v, ".", "outside"); ok {
		t.Error("a workspace member outside the project was found")
	}
}

func TestRules_dart(t *testing.T) {
	fam, r, ok := resolvers.ForLang("dart")
	if !ok || fam != codeparse.FamilyDart {
		t.Fatalf("ForLang(dart) = %q, %v", fam, ok)
	}
	rules := r.Rules()
	if !reflect.DeepEqual(rules.Separators, []string{"."}) || rules.CaseInsensitive || !rules.NameFallback || !rules.MemberFallback {
		t.Errorf("rules = %+v", rules)
	}
	f := &resolvers.File{Path: "lib/a.dart", Package: "lib/a.dart", Facts: &codeparse.Facts{}}
	other := &resolvers.File{Path: "lib/b.dart", Package: "lib/b.dart", Facts: &codeparse.Facts{}}
	pub := codeparse.Symbol{Name: "f"}
	priv := codeparse.Symbol{Name: "_f"}
	for _, tc := range []struct {
		c    resolvers.Candidate
		want bool
	}{
		{resolvers.Candidate{File: other, Symbol: &pub}, true},
		{resolvers.Candidate{File: other, Symbol: &priv}, false},
		{resolvers.Candidate{File: f, Symbol: &priv}, true},
	} {
		if got := r.Visible(f, tc.c, resolvers.StepName); got != tc.want {
			t.Errorf("Visible(%s in %s) = %v", tc.c.Symbol.Name, tc.c.File.Path, got)
		}
	}
}
