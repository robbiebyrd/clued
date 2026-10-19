package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/memcode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codegraph/memgraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

const swiftGoldens = "../../codeparse/testdata/swift"

// swiftView is a View over a set of paths, for the resolver's own rules.
type swiftView struct {
	has  map[string]bool
	pkgs map[string][]*resolvers.File
}

func (v swiftView) Layer() string     { return codegraph.BaseLayerID }
func (v swiftView) Has(p string) bool { return v.has[p] }
func (v swiftView) Content(context.Context, string) ([]byte, bool, error) {
	return nil, false, nil
}
func (v swiftView) Package(family, key string) []*resolvers.File {
	if family != codeparse.FamilySwift {
		return nil
	}
	return v.pkgs[key]
}

func swiftResolver(t *testing.T) resolvers.Resolver {
	t.Helper()
	r, ok := resolvers.Lookup(codeparse.FamilySwift)
	if !ok {
		t.Fatal("no resolver registered for swift")
	}
	return r
}

func swiftProfile(t *testing.T) *codeparse.Profile {
	t.Helper()
	e, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	p := e.Profiles()["swift"]
	if p == nil {
		t.Fatal("no swift profile")
	}
	return p
}

// goldenFacts reads the expected facts of a Swift golden input.
func goldenFacts(t *testing.T, name string) *codeparse.Facts {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(swiftGoldens, name+".facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f codeparse.Facts
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func goldenSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(swiftGoldens, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestModule_swift(t *testing.T) {
	r, prof := swiftResolver(t), swiftProfile(t)
	v := swiftView{has: map[string]bool{
		"Package.swift":                 true,
		"pkgs/Net/Package.swift":        true,
		"Sources/ShapeKit/Shapes.swift": true,
	}}
	for p, want := range map[string]string{
		"Sources/ShapeKit/Shapes.swift":           "ShapeKit",
		"Sources/ShapeKit/Models/Deep/Unit.swift": "ShapeKit",
		"Tests/ShapeKitTests/ShapeTests.swift":    "ShapeKitTests",
		"Sources/App/Tests/Fixture.swift":         "App",     // a Tests directory inside a target
		"pkgs/Net/Sources/Net/Client.swift":       "Net",     // the nearest manifest
		"pkgs/Net/Sources/Net/Sources/X/y.swift":  "Net",     // only directly below its roots
		"Package.swift":                           ".",       // the manifest itself
		"Sources/main.swift":                      "Sources", // no target directory
		"MyApp/Views/ContentView.swift":           "MyApp",   // an Xcode project
		"MyApp/Models/Item.swift":                 "MyApp",
		"MyAppTests/ItemTests.swift":              "MyAppTests",
		"vendor/Lib/Sources/Lib/a.swift":          "Lib", // no manifest visible: the first root
	} {
		f := &resolvers.File{Path: p, Lang: "swift", Family: codeparse.FamilySwift, Profile: prof, Facts: &codeparse.Facts{Lang: "swift"}}
		got, err := r.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("module of %s = %q, want %q", p, got, want)
		}
	}

	// A profile that names other roots, and one that does not use targets.
	custom := *prof
	custom.Scope.Options = map[string]string{"targetRoots": " Modules/ ,Plugins"}
	f := &resolvers.File{Path: "Modules/Feed/FeedView.swift", Profile: &custom, Facts: &codeparse.Facts{}}
	if got, _ := r.Package(context.Background(), v, f); got != "Feed" {
		t.Errorf("custom roots: module = %q, want Feed", got)
	}
	f.Path = "Sources/A/b.swift"
	if got, _ := r.Package(context.Background(), v, f); got != "Sources" {
		t.Errorf("custom roots: Sources is no root: module = %q, want Sources", got)
	}
	custom.Scope.Options = map[string]string{"module": "directory"}
	custom.Scope.PackageBy = codeparse.PackageByDirectory
	f.Path = "Sources/A/Views/b.swift"
	if got, _ := r.Package(context.Background(), v, f); got != "Sources/A/Views" {
		t.Errorf("module: directory: module = %q, want the directory", got)
	}
}

func TestScope_swift(t *testing.T) {
	r := swiftResolver(t)
	if got := r.Rules(); !reflect.DeepEqual(got.Separators, []string{"."}) || got.CaseInsensitive || !got.NameFallback || !got.MemberFallback {
		t.Errorf("rules = %+v", got)
	}
	// ShapeKit re-exports Combine and, through Core, Geometry.
	core := &resolvers.File{Path: "Sources/Core/Core.swift", Package: "Core", Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "Geometry", Kind: "reexport"}, {Spec: "Hidden"}}}}
	kit := &resolvers.File{Path: "Sources/ShapeKit/Kit.swift", Package: "ShapeKit", Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "Combine", Kind: "reexport"}, {Spec: "Core", Kind: "reexport"}, {Spec: "Core", Kind: "reexport", Names: []string{"Only"}}}}}
	geo := &resolvers.File{Path: "Sources/Geometry/G.swift", Package: "Geometry", Facts: &codeparse.Facts{Imports: []codeparse.Import{
		{Spec: "ShapeKit", Kind: "reexport"}}}} // a cycle back
	v := swiftView{pkgs: map[string][]*resolvers.File{"Core": {core}, "ShapeKit": {kit}, "Geometry": {geo}}}
	f := &resolvers.File{Path: "Tests/ShapeKitTests/Imports.swift", Package: "ShapeKitTests", Facts: goldenFacts(t, "Imports.swift")}
	s, err := r.Scope(context.Background(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	wantQual := map[string][]string{}
	for _, m := range []string{"ShapeKitTests", "Foundation", "os", "ShapeKit", "Combine", "Swift", "UIKit", "Darwin", "AppKit", "Core", "Geometry"} {
		wantQual[m] = []string{m}
	}
	if !reflect.DeepEqual(s.Qualifiers, wantQual) {
		t.Errorf("qualifiers = %v\nwant %v", s.Qualifiers, wantQual)
	}
	wantNames := map[string][]string{"Array": {"Swift"}, "UIViewController": {"UIKit"}, "sqrt": {"Darwin"}, "Shape": {"ShapeKit"}}
	if !reflect.DeepEqual(s.Names, wantNames) {
		t.Errorf("names = %v, want %v", s.Names, wantNames)
	}
	open := append([]string(nil), s.Open...)
	sort.Strings(open)
	if want := []string{"AppKit", "Combine", "Core", "Foundation", "Geometry", "ShapeKit", "UIKit", "os"}; !reflect.DeepEqual(open, want) {
		t.Errorf("open = %v, want %v", open, want)
	}

	// A file at the root has no module name to qualify with.
	s, _ = r.Scope(context.Background(), v, &resolvers.File{Path: "Package.swift", Package: ".", Facts: &codeparse.Facts{}})
	if len(s.Qualifiers)+len(s.Names)+len(s.Open) != 0 {
		t.Errorf("root scope = %+v", s)
	}
}

func TestVisible_swift(t *testing.T) {
	r := swiftResolver(t)
	shapes := &resolvers.File{Path: "Sources/ShapeKit/Shapes.swift", Package: "ShapeKit", Facts: goldenFacts(t, "Shapes.swift")}
	sym := func(id string) *codeparse.Symbol {
		for i := range shapes.Facts.Symbols {
			if s := &shapes.Facts.Symbols[i]; s.ID == id {
				return s
			}
		}
		t.Fatalf("no symbol %s in Shapes.swift", id)
		return nil
	}
	plain := &resolvers.File{Path: "Sources/App/A.swift", Package: "App", Facts: &codeparse.Facts{Imports: []codeparse.Import{{Spec: "ShapeKit"}}}}
	testable := &resolvers.File{Path: "Tests/T/A.swift", Package: "T", Facts: goldenFacts(t, "Imports.swift")}
	none := &resolvers.File{Path: "Sources/Other/A.swift", Package: "Other", Facts: &codeparse.Facts{}}
	same := &resolvers.File{Path: "Sources/ShapeKit/B.swift", Package: "ShapeKit", Facts: &codeparse.Facts{}}
	for _, tc := range []struct {
		f    *resolvers.File
		id   string
		step resolvers.Step
		want bool
	}{
		{same, "Point~2", resolvers.StepPackage, false}, // an extension is never a target
		{same, "Direction", resolvers.StepPackage, true},
		{same, "Direction", resolvers.StepName, true},
		{plain, "Point", resolvers.StepOpen, true},
		{plain, "Direction", resolvers.StepOpen, false}, // internal
		{plain, "Point.distance", resolvers.StepMember, false},
		{plain, "Point.move", resolvers.StepMember, true},
		{plain, "Point.move", resolvers.StepName, true},
		{testable, "Direction", resolvers.StepOpen, true}, // @testable import
		{testable, "Point.distance", resolvers.StepName, true},
		{testable, "Point~2", resolvers.StepOpen, false},
		{none, "Point", resolvers.StepQualified, true},
		{none, "Point.move", resolvers.StepName, false}, // not imported
	} {
		if got := r.Visible(tc.f, resolvers.Candidate{File: shapes, Symbol: sym(tc.id)}, tc.step); got != tc.want {
			t.Errorf("Visible(%s, %s, %s) = %v, want %v", tc.f.Path, tc.id, tc.step, got, tc.want)
		}
	}
}

// Hand-written sources beside the goldens.
const (
	swiftManifest = `// swift-tools-version:5.9
import PackageDescription

let package = Package(name: "Shapes")
`
	swiftFigure = `open class Figure {
    public init() {}
}

public protocol DrawingContext {
    func stroke(circleAt center: Point, radius: Double)
}

extension Point {
    public static func zero() -> Point { Point(x: 0, y: 0) }
}

extension Circle {
    public func scaled(by factor: Double) -> Circle {
        resize(factor)
        return self
    }
}

extension Circle {
    func resize(_ factor: Double) {}
}
`
	swiftExtras = `import ShapeKit

protocol Measurable {
    func measure() -> Double
}

extension Point {
    func mirrored() -> Point { Point.zero() }
}

extension Circle: Measurable {
    func measure() -> Double { 1 }
}

func render(_ c: Circle) -> Int {
    _ = c.scaled(by: 2)
    _ = Direction.north
    return ShapeKit.drawAll([c], in: Canvas())
}
`
	// A file of imports alone has no facts the engine keeps, so Core
	// re-exports from a file that defines something.
	swiftCore = `@_exported import ShapeKit

public let coreVersion = 1
`
	swiftUI = `import Core

func show(_ c: Circle) -> Point {
    drawAll([c], in: Canvas())
    return Point(x: 1, y: 1)
}
`
	swiftOther = `func lonely(_ p: Point) -> Int {
    drawAll([], in: Canvas())
}
`
	swiftTests = `@testable import ShapeKit

final class ShapeTests: Shape {
    func testTurn() -> Direction {
        Direction.north.turned()
    }
}
`
	swiftXcodeA = `struct ContentView {
    func body() -> Item { makeItem() }
}
`
	swiftXcodeB = `struct Item {}

func makeItem() -> Item { Item() }
`
)

type swiftEnv struct {
	t       *testing.T
	ctx     context.Context
	g       codegraph.Graph
	project string
}

// newSwiftEnv indexes tree (path → source) as the default branch of one
// worktree and resolves it with the real Swift extraction.
func newSwiftEnv(t *testing.T, tree map[string]string) *swiftEnv {
	t.Helper()
	ctx := context.Background()
	const project = "u0123456789abcdef__github.com__o__swift"
	store, graphs := memcode.New(), memgraph.New()
	// The queries on disk with a generous cut-off: -race on a busy host
	// slows the Swift grammar past the default cut-offs.
	e, err := codeparse.NewEngine(os.DirFS("../../codeparse/queries"), codeparse.Options{Timeout: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply: codeindex.NewResolver(codeindex.ResolverOptions{Profiles: e.Profiles(),
			Files: codeindex.FileApplier{ParseTimeout: 5 * time.Minute}}),
		Parse:  e.Parse,
		Holder: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	const wt = "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	sum1 := func(s string) string { b := sha1.Sum([]byte(s)); return hex.EncodeToString(b[:]) }
	commit := sum1("commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: commit, Upstream: commit, Full: true}
	for p, src := range tree {
		b := sha256.Sum256([]byte(src))
		sha := hex.EncodeToString(b[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(src)), Content: []byte(src)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(src)), GitOID: sum1(src), Mode: "100644", Lang: "swift"})
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
	return &swiftEnv{t: t, ctx: ctx, g: g, project: project}
}

// rows lists the edges of relation rel at the symbol path#id as
// "from -> to type/confidence", from and to as path#id.
func (e *swiftEnv) rows(rel codegraph.Relation, p, id string) []string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, id), Relation: rel})
	if err != nil {
		e.t.Fatalf("%s of %s#%s: %v", rel, p, id, err)
	}
	out := []string{}
	for _, r := range rows {
		out = append(out, r.From.ID+" -> "+r.To.ID+" "+string(r.Type)+"/"+r.Confidence)
	}
	sort.Strings(out)
	return out
}

func node(p, id string) string { return codeindex.SymbolNodeID(p, id) }

func edge(from, to string, typ codegraph.EdgeType, conf string) string {
	return from + " -> " + to + " " + string(typ) + "/" + conf
}

func wantRows(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if want == nil {
		want = []string{}
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestResolve_swift(t *testing.T) {
	const (
		shapes  = "Sources/ShapeKit/Shapes.swift"
		figure  = "Sources/ShapeKit/Figure.swift"
		builder = "Sources/ShapeKit/Builders.swift"
		store   = "Sources/App/ShapeStore.swift"
		extras  = "Sources/App/Extras.swift"
		core    = "Sources/Core/Core.swift"
		ui      = "Sources/UI/Show.swift"
		other   = "Sources/Other/Other.swift"
		tests   = "Tests/ShapeKitTests/ShapeTests.swift"
		xa      = "MyApp/Views/ContentView.swift"
		xb      = "MyApp/Models/Item.swift"
	)
	e := newSwiftEnv(t, map[string]string{
		"Package.swift": swiftManifest,
		shapes:          goldenSource(t, "Shapes.swift"),
		builder:         goldenSource(t, "Builders.swift"),
		figure:          swiftFigure,
		store:           goldenSource(t, "ShapeStore.swift"),
		extras:          swiftExtras,
		core:            swiftCore,
		ui:              swiftUI,
		other:           swiftOther,
		tests:           swiftTests,
		xa:              swiftXcodeA,
		xb:              swiftXcodeB,
	})
	const (
		calls = codegraph.EdgeCalls
		refs  = codegraph.EdgeReferences
		impl  = codegraph.EdgeImplements
		ext   = codegraph.EdgeExtends
		exact = codegraph.ConfidenceExact
		qual  = codegraph.ConfidenceQualified
		name  = codegraph.ConfidenceName
	)

	// The type, never an extension of it: Point has an extension in its own
	// file, in Figure.swift and in Extras.swift.
	wantRows(t, "references to Point", e.rows(codegraph.RelReferences, shapes, "Point"),
		edge(node(shapes, "Shape.subscript"), node(shapes, "Point"), refs, exact),
		edge(node(shapes, "Point.distance"), node(shapes, "Point"), refs, exact),
		edge(node(shapes, "Circle.subscript"), node(shapes, "Point"), refs, exact),
		edge(node(shapes, "Point.+"), node(shapes, "Point"), refs, exact),
		edge(node(figure, "DrawingContext.stroke"), node(shapes, "Point"), refs, exact),
		edge(node(figure, "Point.zero"), node(shapes, "Point"), refs, exact),
		edge(node(extras, "Point.mirrored"), node(shapes, "Point"), refs, exact),
		edge(node(ui, "show"), node(shapes, "Point"), refs, exact),
	)
	for _, ext := range []struct{ p, id string }{{shapes, "Point~2"}, {figure, "Point"}, {extras, "Point"}, {figure, "Circle"}, {figure, "Circle~2"}} {
		wantRows(t, "references to the extension "+ext.p+"#"+ext.id, e.rows(codegraph.RelReferences, ext.p, ext.id))
	}

	// Extension members are the type's: Point.zero from another module,
	// resize from a sibling extension in the same module.
	wantRows(t, "Point.mirrored", e.rows(codegraph.RelCallees, extras, "Point.mirrored"),
		edge(node(extras, "Point.mirrored"), node(figure, "Point.zero"), calls, qual))
	wantRows(t, "Circle.scaled", e.rows(codegraph.RelCallees, figure, "Circle.scaled"),
		edge(node(figure, "Circle.scaled"), node(figure, "Circle.resize"), calls, name))

	// Imports: open, module-qualified, internal definitions hidden.
	wantRows(t, "render", e.rows(codegraph.RelCallees, extras, "render"),
		edge(node(extras, "render"), node(figure, "Circle.scaled"), calls, name),
		edge(node(extras, "render"), node(shapes, "drawAll"), calls, exact),
		edge(node(extras, "render"), node(builder, "Canvas"), calls, exact))
	wantRows(t, "references to Direction", e.rows(codegraph.RelReferences, shapes, "Direction"),
		edge(node(shapes, "Direction.opposite"), node(shapes, "Direction"), refs, exact),
		edge(node(shapes, "Direction.turned"), node(shapes, "Direction"), refs, exact),
		edge(node(tests, "ShapeTests.testTurn"), node(shapes, "Direction"), refs, exact))

	// The golden ShapeStore: ShapeKit's public Circle, its own protocol
	// and functions, self members.
	wantRows(t, "ShapeStore.save", e.rows(codegraph.RelCallees, store, "ShapeStore.save"),
		edge(node(store, "ShapeStore.save"), node(store, "notify"), calls, exact))
	wantRows(t, "ShapeStore.init", e.rows(codegraph.RelCallees, store, "ShapeStore.init"),
		edge(node(store, "ShapeStore.init"), node(store, "ShapeStore.apply"), calls, name))
	circleRefs := e.rows(codegraph.RelReferences, shapes, "Circle")
	for _, from := range []string{"ShapeRepository.load", "ShapeRepository.save", "ShapeStore.shapes", "ShapeStore.load", "ShapeStore.save"} {
		w := edge(node(store, from), node(shapes, "Circle"), refs, exact)
		if !contains(circleRefs, w) {
			t.Errorf("references to Circle: missing %q in %q", w, circleRefs)
		}
	}

	// Conformance: implements edges to protocols, from types and from
	// extensions; a superclass extends.
	wantRows(t, "implementations of Shape", e.rows(codegraph.RelImplementations, shapes, "Shape"),
		edge(node(shapes, "Circle"), node(shapes, "Shape"), impl, exact),
		// A class whose first inherited name is a protocol: the extractor
		// cannot tell it from a superclass and records extends, and the
		// engine takes the edge type from the reference's kind.
		edge(node(tests, "ShapeTests"), node(shapes, "Shape"), ext, exact))
	wantRows(t, "implementations of ShapeRepository", e.rows(codegraph.RelImplementations, store, "ShapeRepository"),
		edge(node(store, "ShapeStore"), node(store, "ShapeRepository"), impl, exact))
	wantRows(t, "implementations of Measurable", e.rows(codegraph.RelImplementations, extras, "Measurable"),
		edge(node(extras, "Circle"), node(extras, "Measurable"), impl, exact))
	wantRows(t, "implementations of Figure", e.rows(codegraph.RelImplementations, figure, "Figure"),
		edge(node(shapes, "Circle"), node(figure, "Figure"), ext, exact))

	// Re-exports: UI imports Core, which re-exports ShapeKit.
	wantRows(t, "show", e.rows(codegraph.RelCallees, ui, "show"),
		edge(node(ui, "show"), node(shapes, "drawAll"), calls, exact),
		edge(node(ui, "show"), node(builder, "Canvas"), calls, exact),
		edge(node(ui, "show"), node(shapes, "Point"), calls, exact))

	// A module that imports nothing reaches no other module by bare name.
	wantRows(t, "lonely", e.rows(codegraph.RelCallees, other, "lonely"))

	// An Xcode project: the top directory is the module.
	wantRows(t, "ContentView.body", e.rows(codegraph.RelCallees, xa, "ContentView.body"),
		edge(node(xa, "ContentView.body"), node(xb, "makeItem"), calls, exact))
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
