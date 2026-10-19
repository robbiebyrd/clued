package resolvers_test

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

// cSrc builds the facts the C, C++ and Objective-C extractors give for one
// file, by hand, as those extractors do (C++ namespaces as definitions of
// kind namespace, out-of-line methods qualified by position, Objective-C
// methods named by their selector's first keyword).
type cSrc struct {
	f    codeparse.Facts
	line int
}

func cFile(lang string) *cSrc { return &cSrc{f: codeparse.Facts{Lang: lang}} }

func (s *cSrc) pos() codeparse.Pos {
	s.line++
	return codeparse.Pos{Line: s.line, Offset: s.line * 100}
}

func (s *cSrc) imp(kind, spec string, names ...string) *cSrc {
	s.f.Imports = append(s.f.Imports, codeparse.Import{Spec: spec, Kind: kind, Names: names, Start: s.pos()})
	return s
}

func (s *cSrc) alias(spec, alias string) *cSrc {
	s.f.Imports = append(s.f.Imports, codeparse.Import{Spec: spec, Kind: resolvers.CKindNamespaceAlias, Alias: alias, Start: s.pos()})
	return s
}

// def adds a definition with id and qualified name qual; its name is the
// last part of qual, its parent the definition id parent ("" at the top).
func (s *cSrc) def(kind, id, qual, parent, sig string) *cSrc {
	name := qual
	if i := strings.LastIndexAny(qual, ":."); i >= 0 {
		name = qual[i+1:]
	}
	p := s.pos()
	s.f.Symbols = append(s.f.Symbols, codeparse.Symbol{ID: id, Kind: kind, Name: name, QualName: qual, Signature: sig,
		Start: p, End: p, Parent: parent, Exported: !strings.HasPrefix(sig, "static ")})
	return s
}

// ref adds a reference of kind from the definition enclosing.
func (s *cSrc) ref(kind, enclosing, qualifier, name string) *cSrc {
	s.f.Refs = append(s.f.Refs, codeparse.Ref{Name: name, Qualifier: qualifier, Kind: kind, Enclosing: enclosing, Start: s.pos()})
	return s
}

func (s *cSrc) call(enclosing, qualifier, name string) *cSrc {
	return s.ref(codeparse.RefCall, enclosing, qualifier, name)
}

func (s *cSrc) src() string {
	b, err := json.Marshal(s.f)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// golden returns a codeparse golden's facts, as the file content cParse
// reads back.
func golden(t *testing.T, lang, name string) string {
	t.Helper()
	b, err := os.ReadFile(path.Join("..", "..", "codeparse", "testdata", lang, name+".facts.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// cParse reads the facts a C-family file's content holds; other files
// (build files) have no grammar.
func cParse(p string, content []byte) (codeparse.Facts, error) {
	if l, ok := codeparse.LookupLanguage(codeparse.Detect(p, nil, nil).Lang); !ok || l.Family != codeparse.FamilyCFamily {
		return codeparse.Facts{ParseError: codeparse.ParseNoGrammar}, nil
	}
	var f codeparse.Facts
	err := json.Unmarshal(content, &f)
	return f, err
}

func hexOf(b []byte) string { return hex.EncodeToString(b) }

func sha256Of(s string) string { h := sha256.Sum256([]byte(s)); return hexOf(h[:]) }

func gitOID(s string) string { h := sha1.Sum([]byte(s)); return hexOf(h[:]) }

// cEnv indexes one commit of a tree with the real resolution engine and
// reads the edges back.
type cEnv struct {
	t   *testing.T
	ctx context.Context
	g   codegraph.Graph
}

func indexTree(t *testing.T, tree map[string]string) *cEnv {
	t.Helper()
	ctx := context.Background()
	const project = "u0123456789abcdef__github.com__o__cfamily"
	store, graphs := memcode.New(), memgraph.New()
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{}),
		Parse:    cParse,
		Holder:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	commit := gitOID("commit main")
	ch := code.Change{Worktree: wt, Ref: "main", Head: commit, Upstream: commit, Full: true}
	for p, c := range tree {
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha256Of(c), Size: int64(len(c)), Content: []byte(c)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha256Of(c), Size: int64(len(c)), GitOID: gitOID(c),
			Mode: "100644", Lang: codeparse.Detect(p, nil, nil).Lang})
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
	return &cEnv{t: t, ctx: ctx, g: g}
}

// out returns the edges of relation rel at the symbol path#id, as the
// other end → confidence.
func (e *cEnv) out(rel codegraph.Relation, p, id string) map[string]string {
	e.t.Helper()
	rows, err := e.g.Neighbors(e.ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(p, id), Relation: rel})
	if err != nil {
		e.t.Fatalf("%s of %s#%s: %v", rel, p, id, err)
	}
	got := map[string]string{}
	for _, r := range rows {
		other := r.To.ID
		if rel != codegraph.RelCallees {
			other = r.From.ID
		}
		got[other] = r.Confidence
	}
	return got
}

func (e *cEnv) callees(p, id string) map[string]string { return e.out(codegraph.RelCallees, p, id) }

// referrers returns who refers to p#id (REFERENCES), sorted.
func (e *cEnv) referrers(p, id string) []string {
	var out []string
	for from, conf := range e.out(codegraph.RelReferences, p, id) {
		out = append(out, from+" "+conf)
	}
	sort.Strings(out)
	return out
}

func wantMap(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

func wantList(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got %v\nwant %v", what, got, want)
	}
}

const (
	cInclude       = resolvers.CKindInclude
	cIncludeSystem = resolvers.CKindIncludeSystem
	kFn            = codeparse.KindFunction
	kMethod        = codeparse.KindMethod
	kVar           = codeparse.KindVar
	kConst         = codeparse.KindConst
	kNS            = codeparse.KindNamespace
	kClass         = codeparse.KindClass
	kStruct        = codeparse.KindStruct
	kField         = codeparse.KindField
	kType          = codeparse.KindType
)

func Test_cfamilyRegistered(t *testing.T) {
	for _, lang := range []string{codeparse.LangC, codeparse.LangCPP, codeparse.LangObjC} {
		family, r, ok := resolvers.ForLang(lang)
		if !ok || family != codeparse.FamilyCFamily || r == nil {
			t.Errorf("ForLang(%s) = %q, %v, %v", lang, family, r, ok)
		}
	}
	r, _ := resolvers.Lookup(codeparse.FamilyCFamily)
	if seps := r.Rules().Separators; !reflect.DeepEqual(seps, []string{"::", "."}) || !r.Rules().NameFallback || !r.Rules().MemberFallback {
		t.Errorf("Rules = %+v", r.Rules())
	}
}

// Test_cfamilyC resolves the C goldens (codeparse/testdata/c: list.h and
// registry.c) with hand-built neighbours: include directories from
// CMakeLists.txt, a decoy header of the same path elsewhere, header/source
// pairing, static functions and a prototype before its definition.
func Test_cfamilyC(t *testing.T) {
	tree := map[string]string{
		"CMakeLists.txt": "cmake_minimum_required(VERSION 3.20)\n# the public headers\ninclude_directories(${CMAKE_CURRENT_SOURCE_DIR}/include)\n",
		"lib/list.h":     golden(t, "c", "list.h"),
		"lib/registry.c": golden(t, "c", "registry.c"),
		"lib/config.h":   cFile("c").def(codeparse.KindMacro, "CONFIG_CAP", "CONFIG_CAP", "", "#define CONFIG_CAP 8").src(),
		"include/util/log.h": cFile("c").
			def(kFn, "log_debug", "log_debug", "", "void log_debug(const char *fmt, ...);").src(),
		"vendor/util/log.h": cFile("c").
			def(kFn, "log_debug", "log_debug", "", "void log_debug(const char *fmt, ...);").src(),
		// registry.h pairs with registry.c by name: what it declares
		// resolves to registry.c's definitions.
		"lib/registry.h": cFile("c").imp(cInclude, "list.h").
			def(kFn, "registry_find", "registry_find", "", "entry_t *registry_find(const registry_t *r, const char *name);").
			def(kVar, "registry_count", "registry_count", "", "extern size_t registry_count;").src(),
		"app/main.c": cFile("c").
			imp(cIncludeSystem, "stdio.h").imp(cInclude, "../lib/registry.h").imp("include_macro", "PLATFORM_H").
			def(kFn, "main", "main", "", "int main(void)").
			call("main", "", "registry_find"). // registry.h's pair: the body in registry.c
			call("main", "", "list_init").     // list.h (through registry.h) declares it; no pair defines it
			call("main", "", "list_empty").    // a static inline function of an included header
			call("main", "", "by_name").       // static in registry.c: not visible
			call("main", "", "printf").        // the system library
			call("main", "", "CONFIG_CAP").    // config.h, two includes away
			ref(codeparse.RefValue, "main", "", "registry_count").
			ref(codeparse.RefValue, "main", "", "KIND_STRING"). // an enumerator: nested, so only by name
			ref(codeparse.RefType, "main", "", "entry_t").src(),
		"lib/proto.c": cFile("c").
			def(kFn, "helper", "helper", "", "static void helper(void);").
			def(kFn, "run", "run", "", "void run(void)").
			def(kFn, "helper~2", "helper", "", "static void helper(void)").
			call("run", "", "helper").src(),
	}
	e := indexTree(t, tree)

	wantMap(t, "registry_find", e.callees("lib/registry.c", "registry_find"), map[string]string{
		"include/util/log.h#log_debug": "exact", // the CMake include directory, not vendor/
	})
	wantMap(t, "registry_sort", e.callees("lib/registry.c", "registry_sort"), map[string]string{
		"lib/list.h#container_of":  "exact",
		"lib/registry.c#ops.open":  "name", // a member through a variable
		"lib/registry.c#ops.close": "name",
	})
	wantMap(t, "entry_free", e.callees("lib/registry.c", "entry_free~2"), map[string]string{
		"lib/list.h#entry.on_free": "name",
	})
	wantList(t, "referrers of entry_t", filter(e.referrers("lib/list.h", "entry_t"), "lib/registry.c"),
		"lib/registry.c#by_name exact", "lib/registry.c#compare exact", "lib/registry.c#cursor exact",
		"lib/registry.c#registry_find exact", "lib/registry.c#registry_sort exact")
	wantList(t, "referrers of LIST_MAX", e.referrers("lib/list.h", "LIST_MAX"), "lib/registry.c#cursor exact")

	wantMap(t, "main", e.callees("app/main.c", "main"), map[string]string{
		"lib/registry.c#registry_find": "exact",
		"lib/list.h#list_init":         "exact",
		"lib/list.h#list_empty":        "exact",
		"lib/config.h#CONFIG_CAP":      "exact",
	})
	wantList(t, "referrers of registry_count", e.referrers("lib/registry.c", "registry_count"), "app/main.c#main exact")
	wantList(t, "referrers of the extern", e.referrers("lib/registry.h", "registry_count"))
	wantList(t, "referrers of KIND_STRING", e.referrers("lib/list.h", "kind.KIND_STRING"),
		"app/main.c#main name", "lib/registry.c#registry_find name")
	wantList(t, "referrers of entry_t from main", filter(e.referrers("lib/list.h", "entry_t"), "app/"), "app/main.c#main exact")

	// A prototype gives way to the definition in its file.
	wantMap(t, "run", e.callees("lib/proto.c", "run"), map[string]string{"lib/proto.c#helper~2": "exact"})
}

func filter(list []string, prefix string) []string {
	var out []string
	for _, s := range list {
		if strings.HasPrefix(s, prefix) {
			out = append(out, s)
		}
	}
	return out
}

// Test_cfamilyCPP resolves hand-built facts shaped like the C++ extractor's
// (codeparse/testdata/cpp geometry.hpp and geometry.cpp): namespaces as
// definitions, using directives and declarations, a namespace alias of
// std, and out-of-line definitions (A::f) inside a namespace block.
func Test_cfamilyCPP(t *testing.T) {
	hpp := cFile("cpp").imp(cIncludeSystem, "vector").imp(cInclude, "geometry/point.hpp").
		def(kNS, "geometry", "geometry", "", "namespace geometry").
		def(kVar, "geometry::shapeCount", "geometry::shapeCount", "geometry", "extern int shapeCount;").
		def(kClass, "geometry::Shape", "geometry::Shape", "geometry", "class Shape").
		def(kMethod, "geometry::Shape::area", "geometry::Shape::area", "geometry::Shape", "virtual double area() const = 0;").
		def(kMethod, "geometry::Shape::count", "geometry::Shape::count", "geometry::Shape", "static int count();").
		def(kClass, "geometry::Circle", "geometry::Circle", "geometry", "class Circle final : public Shape").
		def(kMethod, "geometry::Circle::area", "geometry::Circle::area", "geometry::Circle", "double area() const override;").
		def(kStruct, "geometry::Bounds", "geometry::Bounds", "geometry", "struct Bounds").
		def(kField, "geometry::Bounds::max", "geometry::Bounds::max", "geometry::Bounds", "Point max;").
		def(kFn, "geometry::largest", "geometry::largest", "geometry", "T* largest(const std::vector<T*>& shapes);").
		def(kFn, "geometry::totalArea", "geometry::totalArea", "geometry", "double totalArea(const std::vector<Shape*>& shapes);").
		ref(codeparse.RefExtends, "geometry::Circle", "", "Shape").
		ref(codeparse.RefType, "geometry::Bounds::max", "", "Point")
	point := cFile("cpp").
		def(kNS, "geometry", "geometry", "", "namespace geometry").
		def(kStruct, "geometry::Point", "geometry::Point", "geometry", "struct Point")
	cpp := cFile("cpp").imp(cInclude, "geometry.hpp").imp(cIncludeSystem, "algorithm").
		imp(resolvers.CKindUsingNamespace, "std").imp(resolvers.CKindUsing, "std::unique_ptr", "unique_ptr").alias("std::filesystem", "fs").
		def(kNS, "geometry", "geometry", "", "namespace geometry").
		def(kVar, "geometry::shapeCount", "geometry::shapeCount", "geometry", "int shapeCount = 0;").
		def(kFn, "geometry::clamp", "geometry::clamp", "geometry", "double clamp(double v, double lo, double hi)").
		def(kMethod, "geometry::count", "geometry::count", "geometry", "int Shape::count()").
		def(kMethod, "geometry::area", "geometry::area", "geometry", "double Circle::area() const").
		def(kFn, "geometry::totalArea", "geometry::totalArea", "geometry", "double totalArea(const std::vector<Shape*>& shapes)").
		def(kMethod, "geometry::describe", "geometry::describe", "geometry", "void geometry::describe(const geometry::Shape& s)").
		def(kFn, "main", "main", "", "int main(int argc, char** argv)").
		call("geometry::clamp", "std", "max"). // std: never the project's Bounds::max
		call("geometry::count", "", "clamp").
		ref(codeparse.RefValue, "geometry::count", "", "shapeCount").
		call("geometry::area", "", "clamp").
		call("geometry::totalArea", "s", "area").
		call("geometry::totalArea", "fs", "exists").
		call("main", "geometry", "describe").
		call("main", "", "largest").
		ref(codeparse.RefType, "main", "geometry", "Shape")
	use := cFile("cpp").imp(cInclude, "../geo/geometry.hpp").imp(resolvers.CKindUsingNamespace, "geometry").
		def(kFn, "report", "report", "", "void report()").
		call("report", "geometry", "totalArea"). // the declaration gives way to the paired definition
		call("report", "", "largest").           // using namespace geometry: declared only
		call("report", "Shape", "count").        // out-of-line Shape::count in geometry.cpp
		ref(codeparse.RefType, "report", "", "Point")
	other := cFile("cpp").
		def(kNS, "other", "other", "", "namespace other").
		def(kFn, "other::largest", "other::largest", "other", "int largest()").
		def(kClass, "other::Point", "other::Point", "other", "class Point").
		def(kClass, "other::FS", "other::FS", "other", "class FS").
		def(kMethod, "other::FS::exists", "other::FS::exists", "other::FS", "bool exists()")
	e := indexTree(t, map[string]string{
		"geo/geometry.hpp":            hpp.src(),
		"geo/geometry/point.hpp":      point.src(),
		"geo/geometry.cpp":            cpp.src(),
		"app/use.cpp":                 use.src(),
		"third/other.cpp":             other.src(),
		"CMakeLists.txt":              "project(geo)\n",
		"build/compile_commands.json": "[]",
	})

	wantMap(t, "clamp", e.callees("geo/geometry.cpp", "geometry::clamp"), map[string]string{})
	wantMap(t, "Shape::count", e.callees("geo/geometry.cpp", "geometry::count"), map[string]string{
		"geo/geometry.cpp#geometry::clamp": "exact"})
	wantList(t, "shapeCount", e.referrers("geo/geometry.cpp", "geometry::shapeCount"), "geo/geometry.cpp#geometry::count exact")
	wantMap(t, "totalArea", e.callees("geo/geometry.cpp", "geometry::totalArea"), map[string]string{
		"geo/geometry.cpp#geometry::area": "name"}) // s->area(): the member in the file
	wantMap(t, "main", e.callees("geo/geometry.cpp", "main"), map[string]string{
		"geo/geometry.cpp#geometry::describe": "qualified",
		"geo/geometry.hpp#geometry::largest":  "name", // namespace geometry is open in the file; other:: is not
	})
	wantList(t, "Shape", e.referrers("geo/geometry.hpp", "geometry::Shape"), "geo/geometry.cpp#main qualified")
	wantList(t, "Point", e.referrers("geo/geometry/point.hpp", "geometry::Point"),
		"app/use.cpp#report name", "geo/geometry.hpp#geometry::Bounds::max name")

	wantMap(t, "report", e.callees("app/use.cpp", "report"), map[string]string{
		"geo/geometry.cpp#geometry::totalArea": "qualified",
		"geo/geometry.hpp#geometry::largest":   "name",
		"geo/geometry.cpp#geometry::count":     "name",
	})
}

// Test_cfamilyObjC resolves hand-built facts shaped like the Objective-C
// extractor's (codeparse/testdata/objc Shapes.h, Shape.m, AppDelegate.m):
// #import, @import of a module, messages named by their selector, and a
// class extension's method declaration before its implementation.
func Test_cfamilyObjC(t *testing.T) {
	shapes := cFile("objc").imp("import_system", "Foundation/Foundation.h").imp("import", "ShapeTypes.h").
		def(kFn, "SKDistance", "SKDistance", "", "double SKDistance(SKPoint3 a, SKPoint3 b);").
		def(kFn, "SKMakeShape", "SKMakeShape", "", "id<Drawable> SKMakeShape(ShapeKind kind);").
		def(kConst, "SKErrorDomain", "SKErrorDomain", "", "extern NSString *const SKErrorDomain;").
		def(kClass, "Shape", "Shape", "", "@interface Shape : NSObject <Drawable, NSCopying>").
		def(kMethod, "Shape.shapeNamed", "Shape.shapeNamed", "Shape", "+ (Shape *)shapeNamed:(NSString *)name;").
		def(kMethod, "Shape.area", "Shape.area", "Shape", "- (double)area;")
	types := cFile("objc").def(kStruct, "SKPoint3", "SKPoint3", "", "typedef struct")
	shape := cFile("objc").imp("import", "Shapes.h").
		def(kConst, "SKErrorDomain", "SKErrorDomain", "", "NSString *const SKErrorDomain = @\"com.example.ShapeKit\";").
		def(codeparse.KindExtension, "Shape", "Shape", "", "@interface Shape () <NSSecureCoding>").
		def(kMethod, "Shape.invalidate", "Shape.invalidate", "Shape", "- (void)invalidate;").
		def(codeparse.KindImpl, "Shape~2", "Shape", "", "@implementation Shape").
		def(kMethod, "Shape.shapeNamed", "Shape.shapeNamed", "Shape~2", "+ (Shape *)shapeNamed:(NSString *)name").
		def(kMethod, "Shape.area", "Shape.area", "Shape~2", "- (double)area").
		def(kMethod, "Shape.splitInto", "Shape.splitInto", "Shape~2", "- (NSArray<Shape *> *)splitInto:(NSUInteger)parts").
		def(kMethod, "Shape.invalidate~2", "Shape.invalidate", "Shape~2", "- (void)invalidate").
		def(kFn, "SKDistance", "SKDistance", "", "double SKDistance(SKPoint3 a, SKPoint3 b)").
		def(kFn, "SKMakeShape", "SKMakeShape", "", "id<Drawable> SKMakeShape(ShapeKind kind)").
		ref(codeparse.RefType, "Shape.area", "", "SKPoint3").
		call("Shape.area", "", "SKDistance").
		call("Shape.area", "self", "invalidate").
		call("Shape.splitInto", "Shape", "shapeNamed").
		call("Shape.splitInto", "NSMutableArray", "arrayWithCapacity").
		call("SKMakeShape", "Shape", "shapeNamed")
	delegateH := cFile("objc").imp("import_system", "UIKit/UIKit.h").
		def(kClass, "AppDelegate", "AppDelegate", "", "@interface AppDelegate : UIResponder <UIApplicationDelegate>")
	delegate := cFile("objc").imp("module", "UIKit").imp("module", "CoreData", "NSManagedObject").
		imp("import", "AppDelegate.h").imp("import", "Kit/Shapes.h").
		def(codeparse.KindImpl, "AppDelegate~2", "AppDelegate", "", "@implementation AppDelegate").
		def(kMethod, "AppDelegate.application", "AppDelegate.application", "AppDelegate~2", "- (BOOL)application:(UIApplication *)application").
		call("AppDelegate.application", "", "SKMakeShape").
		call("AppDelegate.application", "Shape", "shapeNamed").
		ref(codeparse.RefType, "AppDelegate.application", "", "AppDelegate").
		ref(codeparse.RefValue, "AppDelegate.application", "", "SKErrorDomain")
	e := indexTree(t, map[string]string{
		"Kit/Shapes.h":       shapes.src(),
		"Kit/ShapeTypes.h":   types.src(),
		"Kit/Shape.m":        shape.src(),
		"App/AppDelegate.h":  delegateH.src(),
		"App/AppDelegate.m":  delegate.src(),
		"App/Unrelated/x.mm": cFile("objc").def(kMethod, "X.shapeNamed", "X.shapeNamed", "", "- (id)shapeNamed").src(),
	})

	wantMap(t, "area", e.callees("Kit/Shape.m", "Shape.area"), map[string]string{
		"Kit/Shape.m#SKDistance":         "exact", // the definition in the file, not Shapes.h's prototype
		"Kit/Shape.m#Shape.invalidate~2": "name",  // [self invalidate]: the implementation, not the extension's declaration
	})
	wantList(t, "SKPoint3", e.referrers("Kit/ShapeTypes.h", "SKPoint3"), "Kit/Shape.m#Shape.area exact")
	wantMap(t, "splitInto", e.callees("Kit/Shape.m", "Shape.splitInto"), map[string]string{
		"Kit/Shape.m#Shape.shapeNamed": "qualified", // [Shape shapeNamed:]
	})
	wantMap(t, "application", e.callees("App/AppDelegate.m", "AppDelegate.application"), map[string]string{
		"Kit/Shapes.h#SKMakeShape":     "exact", // #import "Kit/Shapes.h" from the root; Shape.m does not pair with Shapes.h
		"Kit/Shape.m#Shape.shapeNamed": "name",  // the implementation or the @interface declaration: the first by path
	})
	// The class from its own implementation: the @implementation in the file.
	wantList(t, "AppDelegate", e.referrers("App/AppDelegate.m", "AppDelegate~2"), "App/AppDelegate.m#AppDelegate.application exact")
	wantList(t, "SKErrorDomain", e.referrers("Kit/Shapes.h", "SKErrorDomain"), "App/AppDelegate.m#AppDelegate.application exact")
}

// fakeView is a View over files with facts and other files' content.
type fakeView struct {
	files   []*resolvers.File
	content map[string]string
}

func newFakeView(r resolvers.Resolver, files map[string]*cSrc, content map[string]string) *fakeView {
	v := &fakeView{content: content}
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		f := &resolvers.File{Path: p, Layer: "base", Lang: files[p].f.Lang, Family: codeparse.FamilyCFamily, Facts: &files[p].f}
		f.Package, _ = r.Package(context.Background(), v, f)
		v.files = append(v.files, f)
	}
	return v
}

func (v *fakeView) Layer() string { return "base" }

func (v *fakeView) Has(p string) bool {
	_, ok := v.content[p]
	return ok || v.file(p) != nil
}

func (v *fakeView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v.content[p]
	return []byte(c), ok, nil
}

func (v *fakeView) Package(family, key string) []*resolvers.File {
	var out []*resolvers.File
	for _, f := range v.files {
		if f.Family == family && f.Package == key {
			out = append(out, f)
		}
	}
	return out
}

func (v *fakeView) file(p string) *resolvers.File {
	for _, f := range v.files {
		if f.Path == p {
			return f
		}
	}
	return nil
}

// reached returns the files whose definitions Visible lets f reach
// through its includes (StepOpen), after Scope.
func reached(t *testing.T, r resolvers.Resolver, v *fakeView, from string) []string {
	t.Helper()
	f := v.file(from)
	if _, err := r.Scope(context.Background(), v, f); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, g := range v.files {
		if g == f || len(g.Facts.Symbols) == 0 {
			continue
		}
		if r.Visible(f, resolvers.Candidate{File: g, Symbol: &g.Facts.Symbols[0]}, resolvers.StepOpen) {
			out = append(out, g.Path)
		}
	}
	return out
}

func hdr(name string) *cSrc { return cFile("c").def(kFn, name, name, "", "void "+name+"(void);") }

// Test_cfamilyIncludes checks where includes resolve: next to the file
// (quoted only), the include directories of compile_commands.json and
// CMakeLists.txt, the root, then by path suffix and base name, nearest
// first; transitive includes resolve from the including header.
func Test_cfamilyIncludes(t *testing.T) {
	r, _ := resolvers.Lookup(codeparse.FamilyCFamily)
	files := map[string]*cSrc{
		"src/a.c": cFile("c").imp(cInclude, "foo.h").imp(cIncludeSystem, "bar.h").imp(cInclude, "deep/baz.h").
			imp(cInclude, "../../outside.h").imp("include_macro", "PLATFORM_H"),
		"src/foo.h":                  hdr("foo_local").imp(cInclude, "chain.h"),
		"src/chain.h":                hdr("chain"),
		"chain.h":                    hdr("chain_root"),
		"include/foo.h":              hdr("foo_inc"),
		"include/bar.h":              hdr("bar_inc"),
		"third/bar.h":                hdr("bar_third"),
		"x/deep/baz.h":               hdr("baz_x"),
		"src/sub/deep/baz.h":         hdr("baz_near"),
		"outside.h":                  hdr("outside"),
		"src/nested/q.c":             cFile("c").imp(cInclude, "bar.h").imp(cIncludeSystem, "baz.h"),
		"tools/gen/z.c":              cFile("c").imp(cIncludeSystem, "foo.h"),
		"tools/gen/CMakeLists.txt.h": hdr("not_cmake"),
	}
	compile := `[{"directory": "/home/u/proj/build", "file": "../src/a.c",
	  "command": "cc -I/home/u/proj/include -isystem ../third -iquote /elsewhere -c ../src/a.c"}]`
	v := newFakeView(r, files, map[string]string{"build/compile_commands.json": compile})

	wantList(t, "a.c", reached(t, r, v, "src/a.c"),
		"include/bar.h",      // <bar.h>: the first -I directory
		"outside.h",          // ../../outside.h leaves the project: by base name
		"src/chain.h",        // foo.h's own include, next to foo.h
		"src/foo.h",          // "foo.h": next to a.c before the include directories
		"src/sub/deep/baz.h") // by path suffix, nearest src/
	wantList(t, "q.c", reached(t, r, v, "src/nested/q.c"), "include/bar.h", "src/sub/deep/baz.h") // not x/deep/baz.h: farther

	// CMake's directories, nearest CMakeLists.txt first, replace nothing
	// the quoted rule finds.
	v = newFakeView(r, files, map[string]string{
		"CMakeLists.txt":       "include_directories(BEFORE SYSTEM \"include\")\nadd_subdirectory(tools)\n",
		"tools/CMakeLists.txt": "target_include_directories(gen PRIVATE $<BUILD_INTERFACE:${PROJECT_SOURCE_DIR}/third> ${OTHER}/x $<INSTALL_INTERFACE:y>)\n",
	})
	wantList(t, "z.c", reached(t, r, v, "tools/gen/z.c"), "include/foo.h")
	files["tools/gen/z.c"] = cFile("c").imp(cIncludeSystem, "bar.h")
	v = newFakeView(r, files, map[string]string{
		"CMakeLists.txt":       "include_directories(include)\n",
		"tools/CMakeLists.txt": "target_include_directories(gen PRIVATE $<BUILD_INTERFACE:${PROJECT_SOURCE_DIR}/third>)\n",
	})
	wantList(t, "z.c bar", reached(t, r, v, "tools/gen/z.c"), "third/bar.h")
}

func Test_cfamilyCMakeIncludeDirs(t *testing.T) {
	cm := `
# include_directories(commented)
include_directories(include "quoted dir"
  ${CMAKE_CURRENT_SOURCE_DIR}/gen ../up ../../../escape /abs)
target_include_directories(lib PUBLIC
  $<BUILD_INTERFACE:${CMAKE_CURRENT_LIST_DIR}/api>
  $<INSTALL_INTERFACE:include>
  ${PROJECT_SOURCE_DIR}/common ${CMAKE_BINARY_DIR}/gen SYSTEM ${CMAKE_SOURCE_DIR})
INCLUDE_DIRECTORIES(Upper)
`
	got := resolvers.CMakeIncludeDirs([]byte(cm), "a/b")
	for _, w := range []string{"a/b/include", "a/b/gen", "a/up", "a/b/api", "common", ".", "a/b/Upper"} {
		if !contains(got, w) {
			t.Errorf("CMakeIncludeDirs = %v, missing %s", got, w)
		}
	}
	for _, bad := range []string{"../escape", "/abs", "include"} {
		if contains(got, bad) {
			t.Errorf("CMakeIncludeDirs = %v, has %s", got, bad)
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Test_cfamilyPairs checks header/source pairing: the same directory
// first, then by base name anywhere; a header's prototype gives way to its
// paired definition for files that include it.
func Test_cfamilyPairs(t *testing.T) {
	r, _ := resolvers.Lookup(codeparse.FamilyCFamily)
	files := map[string]*cSrc{
		"inc/list.h":   hdr("list_init").def(kVar, "list_len", "list_len", "", "extern int list_len;"),
		"src/list.c":   cFile("c").def(kFn, "list_init", "list_init", "", "void list_init(void)").def(kFn, "list_helper", "list_helper", "", "static void list_helper(void)").def(kVar, "list_len", "list_len", "", "int list_len = 0;"),
		"inc/other.h":  hdr("other"),
		"inc/other.c":  cFile("c").def(kFn, "other", "other", "", "void other(void)"),
		"lib/other.c":  cFile("c").def(kFn, "other", "other", "", "void other(void)"),
		"app/main.c":   cFile("c").imp(cInclude, "../inc/list.h").imp(cInclude, "../inc/other.h"),
		"app/other.cc": cFile("cpp"),
	}
	v := newFakeView(r, files, nil)
	ctx := context.Background()
	main := v.file("app/main.c")
	s, err := r.Scope(ctx, v, main)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := s.Names, map[string][]string{"list_init": {"list"}, "list_len": {"list"}, "other": {"other"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Names = %v, want %v", got, want)
	}
	sym := func(p, id string) resolvers.Candidate {
		f := v.file(p)
		for i := range f.Facts.Symbols {
			if f.Facts.Symbols[i].ID == id {
				return resolvers.Candidate{File: f, Symbol: &f.Facts.Symbols[i]}
			}
		}
		t.Fatalf("no %s#%s", p, id)
		return resolvers.Candidate{}
	}
	for _, c := range []struct {
		p, id string
		step  resolvers.Step
		want  bool
	}{
		{"src/list.c", "list_init", resolvers.StepImport, true},    // pair by base name
		{"src/list.c", "list_len", resolvers.StepImport, true},     // the extern's definition
		{"inc/list.h", "list_init", resolvers.StepOpen, false},     // the prototype gives way
		{"inc/list.h", "list_init", resolvers.StepName, false},     // also for the fallback
		{"inc/list.h", "list_len", resolvers.StepOpen, false},      // so does the extern
		{"src/list.c", "list_helper", resolvers.StepName, false},   // static
		{"inc/other.c", "other", resolvers.StepImport, true},       // the same directory first
		{"lib/other.c", "other", resolvers.StepImport, false},      // not the other one
		{"inc/other.h", "other", resolvers.StepOpen, false},        // defined by its pair
		{"src/list.c", "list_init", resolvers.StepPackage, false},  // not main.c's pair
		{"src/list.c", "list_init", resolvers.StepQualified, true}, // exported
	} {
		if got := r.Visible(main, sym(c.p, c.id), c.step); got != c.want {
			t.Errorf("Visible(main.c, %s#%s, %s) = %v, want %v", c.p, c.id, c.step, got, c.want)
		}
	}

	// A source sees its header (the pair) and the header its source, statics
	// only in a header.
	list := v.file("src/list.c")
	if _, err := r.Scope(ctx, v, list); err != nil {
		t.Fatal(err)
	}
	if !r.Visible(list, sym("inc/list.h", "list_len"), resolvers.StepPackage) {
		t.Error("list.c does not see its header")
	}
	header := v.file("inc/list.h")
	if _, err := r.Scope(ctx, v, header); err != nil {
		t.Fatal(err)
	}
	if !r.Visible(header, sym("src/list.c", "list_init"), resolvers.StepPackage) || r.Visible(header, sym("src/list.c", "list_helper"), resolvers.StepPackage) {
		t.Error("list.h: its source's definitions, statics excluded")
	}
}

func Test_cfamilyNames(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"src/list.h", "list"}, {"a.tab.c", "a.tab"}, {"Makefile", "Makefile"}, {"x/Shape.mm", "Shape"},
	} {
		if got := resolvers.CStem(c.in); got != c.want {
			t.Errorf("CStem(%s) = %s, want %s", c.in, got, c.want)
		}
	}
	for p, want := range map[string]bool{"a.h": true, "a.HPP": true, "a.hh": true, "a.hxx": true, "a.c": false, "a.mm": false, "a.cpp": false} {
		if CIsHeader := resolvers.CIsHeader(p); CIsHeader != want {
			t.Errorf("CIsHeader(%s) = %v", p, CIsHeader)
		}
	}
	for _, c := range []struct{ kind, qual, sig, want string }{
		{kMethod, "geometry::area", "double Circle::area() const", "geometry::Circle::area"},
		{kMethod, "geometry::Shape", "Shape::Shape(std::string name) : name_(std::move(name))", "geometry::Shape::Shape"},
		{kMethod, "geometry::add", "std::size_t Registry<T>::add(std::unique_ptr<T> shape)", "geometry::Registry::add"},
		{kMethod, "geometry::operator==", "bool Shape::operator==(const Shape& other) const", "geometry::Shape::operator=="},
		{kMethod, "geometry::describe", "void geometry::describe(const geometry::Shape& s)", "geometry::describe"},
		{kMethod, "a::b::f", "void b::C::f()", "a::b::C::f"},
		{kMethod, "geometry::Circle::area", "double area() const override;", "geometry::Circle::area"},
		{kFn, "list_init", "void list_init(struct list_node *head)", "list_init"},
		{kVar, "x::y", "int A::y = 0;", "x::y"},
	} {
		name := c.qual[strings.LastIndex(c.qual, ":")+1:]
		sym := &codeparse.Symbol{Kind: c.kind, Name: name, QualName: c.qual, Signature: c.sig}
		if got := resolvers.CEffectiveQualName(sym); got != c.want {
			t.Errorf("CEffectiveQualName(%s, %q) = %s, want %s", c.qual, c.sig, got, c.want)
		}
	}
}
