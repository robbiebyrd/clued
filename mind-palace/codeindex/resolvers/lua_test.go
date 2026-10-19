package resolvers_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
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

// luaGolden reads a Lua input of the extractor's goldens.
func luaGolden(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "codeparse", "testdata", "lua", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// luaView is a View over hand-placed files, keyed the way the engine keys
// them (the resolver's Package).
type luaView struct {
	files map[string]*resolvers.File
}

func newLuaView(t *testing.T, paths ...string) *luaView {
	t.Helper()
	_, res, ok := resolvers.ForLang("lua")
	if !ok {
		t.Fatal("no resolver for lua")
	}
	v := &luaView{files: map[string]*resolvers.File{}}
	for _, p := range paths {
		f := &resolvers.File{Path: p, Lang: "lua", Family: codeparse.FamilyLua, Facts: &codeparse.Facts{Lang: "lua"}}
		key, err := res.Package(context.Background(), v, f)
		if err != nil {
			t.Fatal(err)
		}
		f.Package = key
		v.files[p] = f
	}
	return v
}

func (v *luaView) Layer() string { return codegraph.BaseLayerID }

func (v *luaView) Has(p string) bool { return v.files[p] != nil }

func (v *luaView) Content(context.Context, string) ([]byte, bool, error) { return nil, false, nil }

func (v *luaView) Package(family, key string) []*resolvers.File {
	var out []*resolvers.File
	for _, f := range v.files {
		if f.Family == family && f.Package == key {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func TestResolve_lua_Registered(t *testing.T) {
	fam, res, ok := resolvers.ForLang("lua")
	if !ok || fam != codeparse.FamilyLua {
		t.Fatalf("ForLang(lua) = %q, %v", fam, ok)
	}
	rules := res.Rules()
	if !reflect.DeepEqual(rules.Separators, []string{".", ":"}) || rules.CaseInsensitive || !rules.NameFallback || !rules.MemberFallback {
		t.Errorf("rules = %+v", rules)
	}
	// The resolver's separators are the profile's.
	e, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	p := e.Profiles()["lua"]
	if p == nil || !reflect.DeepEqual(p.Scope.Separators, rules.Separators) || p.Scope.PackageBy != codeparse.PackageByFile {
		t.Errorf("profile scope = %+v", p)
	}
}

func TestResolve_lua_ModuleKeysAndRequire(t *testing.T) {
	v := newLuaView(t,
		"main.lua", "util.lua", "lib/base.lua", "src/app/model.lua", "pkg/init.lua",
		"game/main.lua", "game/player.lua", "game/ai/brain.lua", "game/ai/util.lua")
	for p, want := range map[string]string{"main.lua": "main", "lib/base.lua": "lib/base", "pkg/init.lua": "pkg/init"} {
		if got := v.files[p].Package; got != want {
			t.Errorf("package of %s = %q, want %q", p, got, want)
		}
	}
	if got := resolvers.LuaModuleKey("a/b.c.lua"); got != "a/b.c" {
		t.Errorf("LuaModuleKey = %q", got)
	}
	for _, c := range []struct{ dir, spec, want string }{
		{".", "util", "util"},                    // the root
		{".", "lib.base", "lib/base"},            // dots are directories
		{".", "lib/base", "lib/base"},            // a slash works too
		{".", "pkg", "pkg/init"},                 // a/init.lua
		{".", "app.model", "src/app/model"},      // a source root
		{"game", "player", "game/player"},        // the file's directory
		{"game/ai", "player", "game/player"},     // a directory above it
		{"game/ai", "util", "util"},              // the root before the directory
		{"game/ai", "ai.brain", "game/ai/brain"}, // the nearest directory that holds it
		{".", "socket.http", "socket/http"},      // not in the project: its own path
		{"game", "cjson", "cjson"},               // nor this
		{" ", " lib.base ", "lib/base"},          // spaces trimmed
		{".", "busted.runner", "busted/runner"},  // still a module name
		{".", "lib.base.missing", "lib/base/missing"},
	} {
		got, ok := resolvers.LuaRequire(v, c.dir, c.spec)
		if !ok || got != c.want {
			t.Errorf("LuaRequire(%q, %q) = %q, %v; want %q", c.dir, c.spec, got, ok, c.want)
		}
	}
	for _, spec := range []string{"", "..", "../x", "a..b", "/etc/passwd", "."} {
		if got, ok := resolvers.LuaRequire(v, ".", spec); ok {
			t.Errorf("LuaRequire(%q) = %q, want refused", spec, got)
		}
	}
}

func TestResolve_lua_Scope(t *testing.T) {
	v := newLuaView(t, "main.lua", "lib/base.lua", "lib/extra.lua", "config.lua", "plugins/init.lua", "lib/local.lua")
	f := v.files["lib/extra.lua"]
	f.Facts.Imports = []codeparse.Import{
		{Spec: "lib.base", Alias: "base", Kind: "require"},
		{Spec: "cjson", Alias: "json", Kind: "require"},
		{Spec: "table", Alias: "insert", Names: []string{"insert"}, Kind: "require"},
		{Spec: "lib.base", Alias: "split", Names: []string{"split"}, Kind: "require"},
		{Spec: "strict", Kind: "require"},
		{Spec: "main", Kind: "require"},
		{Spec: "../outside", Alias: "o", Kind: "require"},
		{Spec: "config.lua", Kind: "include"},
		{Spec: "plugins/init.lua", Kind: "include"},
		{Spec: "local.lua", Kind: "include"},   // the file's directory
		{Spec: "missing.lua", Kind: "include"}, // nothing to open
		{Spec: "../../etc/x.lua", Kind: "include"},
		{Spec: "/abs.lua", Kind: "include"},
	}
	_, res, _ := resolvers.ForLang("lua")
	s, err := res.Scope(context.Background(), v, f)
	if err != nil {
		t.Fatal(err)
	}
	want := resolvers.Scope{
		Qualifiers: map[string][]string{"base": {"lib/base"}, "json": {"cjson"}},
		Names:      map[string][]string{"insert": {"table"}, "split": {"lib/base"}},
		Open:       []string{"strict", "main", "config", "plugins/init", "lib/local"},
	}
	if !reflect.DeepEqual(s, want) {
		t.Errorf("scope:\n got %+v\nwant %+v", s, want)
	}
	if s, err := res.Scope(context.Background(), v, &resolvers.File{Path: "x.lua"}); err != nil || !reflect.DeepEqual(s, resolvers.Scope{}) {
		t.Errorf("scope without facts = %+v, %v", s, err)
	}
}

func TestResolve_lua_Visible(t *testing.T) {
	_, res, _ := resolvers.ForLang("lua")
	parse := func(p, src string) *resolvers.File {
		facts, err := codeparse.Parse(p, []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		return &resolvers.File{Path: p, Lang: "lua", Family: codeparse.FamilyLua, Package: resolvers.LuaModuleKey(p), Facts: &facts}
	}
	mod := parse("strutil.lua", luaGolden(t, "module.lua"))
	class := parse("class.lua", luaGolden(t, "class.lua"))
	from := parse("main.lua", "local function main() end\n")
	sym := func(f *resolvers.File, id string) resolvers.Candidate {
		for i := range f.Facts.Symbols {
			if f.Facts.Symbols[i].ID == id {
				return resolvers.Candidate{File: f, Symbol: &f.Facts.Symbols[i]}
			}
		}
		t.Fatalf("%s has no %s", f.Path, id)
		return resolvers.Candidate{}
	}
	for _, c := range []struct {
		c    resolvers.Candidate
		step resolvers.Step
		want bool
	}{
		// Through a require's qualifier: the module's top level, locals
		// included (return { f = f }), and its exported members.
		{sym(mod, "strutil"), resolvers.StepImport, true},
		{sym(mod, "memo"), resolvers.StepImport, true},
		{sym(mod, "strutil.split"), resolvers.StepQualified, true},
		{sym(mod, "strutil_debug"), resolvers.StepQualified, true},
		{sym(mod, "memo"), resolvers.StepQualified, false},
		// A bare name: globals only.
		{sym(mod, "strutil_debug"), resolvers.StepName, true},
		{sym(mod, "strutil_debug"), resolvers.StepOpen, true},
		{sym(mod, "strutil"), resolvers.StepName, false}, // returned, but local
		{sym(mod, "memo"), resolvers.StepName, false},
		{sym(mod, "count"), resolvers.StepOpen, false},
		{sym(mod, "cache"), resolvers.StepName, false},
		{sym(mod, "strutil.split"), resolvers.StepName, false}, // a member
		// A member through an expression: only from an exported table.
		{sym(mod, "strutil.split"), resolvers.StepMember, true},
		{sym(class, "Animal.speak"), resolvers.StepMember, false}, // Animal is local
		{sym(class, "Dog.fetch"), resolvers.StepMember, false},
	} {
		if got := res.Visible(from, c.c, c.step); got != c.want {
			t.Errorf("Visible(%s#%s, %s) = %v, want %v", c.c.File.Path, c.c.Symbol.ID, c.step, got, c.want)
		}
	}
	// Its own file sees everything.
	if !res.Visible(class, sym(class, "Animal.speak"), resolvers.StepMember) || !res.Visible(mod, sym(mod, "memo"), resolvers.StepName) {
		t.Error("a file does not see its own definitions")
	}
}

// luaProject indexes files (path → content) with the real Lua extractor and
// the resolution engine, and returns the graph.
func luaProject(t *testing.T, files map[string]string) (context.Context, codegraph.Graph) {
	t.Helper()
	ctx := context.Background()
	const project = "u0123456789abcdef__lua-resolver"
	store := memcode.New()
	graphs := memgraph.New()
	eng, err := codeparse.Default()
	if err != nil {
		t.Fatal(err)
	}
	svc, err := codeindex.New(codeindex.Options{
		Projects: func(context.Context) ([]string, error) { return []string{project}, nil },
		Code:     func(context.Context, string) (code.Store, error) { return store, nil },
		Graphs:   graphs,
		Cursors:  codeindex.NewMemCursors(nil),
		Apply:    codeindex.NewResolver(codeindex.ResolverOptions{Profiles: eng.Profiles()}),
		Parse:    codeparse.Parse,
		Holder:   "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	wt := "wt_0000000000000001"
	if err := store.PutWorktree(ctx, code.Worktree{ID: wt, Owner: "0123456789abcdef", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	head := hex.EncodeToString(sha256.New().Sum(nil))[:40]
	ch := code.Change{Worktree: wt, Ref: "main", Head: head, Upstream: head, Full: true}
	for p, content := range files {
		sum := sha256.Sum256([]byte(content))
		sha := hex.EncodeToString(sum[:])
		if err := store.PutBlob(ctx, code.Blob{SHA256: sha, Size: int64(len(content)), Content: []byte(content)}); err != nil {
			t.Fatal(err)
		}
		ch.Upserts = append(ch.Upserts, code.Entry{Path: p, SHA256: sha, Size: int64(len(content)), GitOID: sha[:40], Mode: "100644", Lang: "lua"})
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
	return ctx, g
}

// luaEdges returns the neighbours of path#id by rel as "path#id confidence"
// (the far end: the target of an outgoing relation, the source of an
// incoming one).
func luaEdges(t *testing.T, ctx context.Context, g codegraph.Graph, rel codegraph.Relation, path, id string) []string {
	t.Helper()
	rows, err := g.Neighbors(ctx, codegraph.BaseView(), codegraph.NeighborQuery{ID: codeindex.SymbolNodeID(path, id), Relation: rel})
	if err != nil {
		t.Fatalf("%s of %s#%s: %v", rel, path, id, err)
	}
	out := []string{}
	for _, r := range rows {
		end := r.To
		if rel != codegraph.RelCallees {
			end = r.From
		}
		out = append(out, end.ID+" "+r.Confidence)
	}
	sort.Strings(out)
	return out
}

func TestResolve_lua_Goldens(t *testing.T) {
	files := map[string]string{
		// The extractor's inputs.
		"src/class.lua":  luaGolden(t, "class.lua"),
		"strutil.lua":    luaGolden(t, "module.lua"),
		"app/nested.lua": luaGolden(t, "nested.lua"),
		"imports.lua":    luaGolden(t, "imports.lua"),
		"broken.lua":     luaGolden(t, "broken.lua"),
		// What they require and include.
		"src/lib/base.lua": "local Dog = setmetatable({}, {})\n\nfunction Dog.new() return Dog end\n\nreturn { Dog = Dog }\n",
		"socket/http.lua":  "local M = {}\n\nfunction M.request(url)\n  return url\nend\n\nreturn M\n",
		"helpers.lua":      "function helper(x)\n  return x\nend\n",
		"config.lua":       "function load_config()\n  return {}\nend\n",
		"main.lua": `local strutil = require("strutil")
local http = require "socket.http"
local json = require("cjson")
local new_dog = require("lib.base").new
local nested = require("app.nested")
require "helpers"
dofile("config.lua")

function main()
  strutil.trim(" x ")
  strutil.split("a,b")
  http.request("u")
  json.decode("{}")
  helper(1)
  load_config()
  strutil_debug("x")
  memo(1)
  nested.util.escape("a")
  strutil:words("a b")
end
`,
	}
	ctx, g := luaProject(t, files)
	want := func(what string, got []string, want ...string) {
		t.Helper()
		if want == nil {
			want = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", what, got, want)
		}
	}

	// A module required by path, its returned table qualified by the
	// require's alias; a member reached through a table the module returns
	// under another name; globals of an opened require and of dofile; a
	// global of a module that is only required (a name match); a local of
	// another module, a module outside the project: nothing.
	want("main", luaEdges(t, ctx, g, codegraph.RelCallees, "main.lua", "main"),
		"app/nested.lua#app.util.escape qualified",
		"config.lua#load_config exact",
		"helpers.lua#helper exact",
		"socket/http.lua#M.request name",
		"strutil.lua#strutil.split qualified",
		"strutil.lua#strutil.trim qualified",
		"strutil.lua#strutil.words qualified",
		"strutil.lua#strutil_debug name",
	)

	// class.lua: a qualified call in the file, methods through self (a
	// member match, by name; for speak the best of two), the class used as
	// a metatable, inheritance in the file and from a required module.
	want("Dog.new", luaEdges(t, ctx, g, codegraph.RelCallees, "src/class.lua", "Dog.new"),
		"src/class.lua#Animal.new qualified")
	want("Dog.fetch", luaEdges(t, ctx, g, codegraph.RelCallees, "src/class.lua", "Dog.fetch"),
		"src/class.lua#Animal.speak name")
	want("Animal.speak", luaEdges(t, ctx, g, codegraph.RelCallees, "src/class.lua", "Animal.speak"),
		"src/class.lua#Animal.sound_of name")
	want("Cat.speak", luaEdges(t, ctx, g, codegraph.RelCallees, "src/class.lua", "Cat.speak"),
		"src/class.lua#Animal.speak qualified")
	want("references to Dog", luaEdges(t, ctx, g, codegraph.RelReferences, "src/class.lua", "Dog"),
		"src/class.lua#Dog.new exact")
	want("references to Animal", luaEdges(t, ctx, g, codegraph.RelReferences, "src/class.lua", "Animal"),
		"src/class.lua#Animal.new exact")
	want("extends Animal", luaEdges(t, ctx, g, codegraph.RelImplementations, "src/class.lua", "Animal"),
		"src/class.lua#Dog exact")
	want("extends base.Dog", luaEdges(t, ctx, g, codegraph.RelImplementations, "src/lib/base.lua", "Dog"),
		"src/class.lua#Puppy exact")

	// module.lua: a call through the module table in the file, a local
	// function, a parameter that names nothing.
	want("split", luaEdges(t, ctx, g, codegraph.RelCallees, "strutil.lua", "strutil.split"),
		"strutil.lua#strutil.trim qualified")
	want("words", luaEdges(t, ctx, g, codegraph.RelCallees, "strutil.lua", "strutil.words"),
		"strutil.lua#count exact")
	want("memo", luaEdges(t, ctx, g, codegraph.RelCallees, "strutil.lua", "memo"))

	// nested.lua: enclosing scopes, deep table paths.
	want("get", luaEdges(t, ctx, g, codegraph.RelCallees, "app/nested.lua", "app.handlers.get"),
		"app/nested.lua#app.handlers.get.inner exact")
	want("inner", luaEdges(t, ctx, g, codegraph.RelCallees, "app/nested.lua", "app.handlers.get.inner"),
		"app/nested.lua#app.handlers.get.inner.innermost exact")
	want("loop", luaEdges(t, ctx, g, codegraph.RelCallees, "app/nested.lua", "loop"),
		"app/nested.lua#ops.add qualified", "app/nested.lua#ops.nested.mul qualified")

	// imports.lua: socket.http is in the project, cjson is not.
	want("fetch", luaEdges(t, ctx, g, codegraph.RelCallees, "imports.lua", "fetch"),
		"socket/http.lua#M.request name")

	// broken.lua: a global of a file it never requires (a name match); a
	// call through its own module table.
	want("ok", luaEdges(t, ctx, g, codegraph.RelCallees, "broken.lua", "M.ok"),
		"helpers.lua#helper name")
	want("after", luaEdges(t, ctx, g, codegraph.RelCallees, "broken.lua", "after"),
		"broken.lua#M.ok qualified")
}
