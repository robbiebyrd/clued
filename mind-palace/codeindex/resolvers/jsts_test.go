package resolvers

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// jsView is a View over hand-placed files: those with facts take part in
// the family, the others (package.json, tsconfig.json) only have content.
type jsView struct {
	t       *testing.T
	content map[string]string
	files   map[string]*File
}

func newJSView(t *testing.T) *jsView {
	return &jsView{t: t, content: map[string]string{}, files: map[string]*File{}}
}

func (v *jsView) Layer() string     { return "base" }
func (v *jsView) Has(p string) bool { _, ok := v.content[p]; return ok }
func (v *jsView) Package(family, key string) []*File {
	var out []*File
	for _, f := range v.files {
		if f.Family == family && f.Package == key {
			out = append(out, f)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func (v *jsView) Content(_ context.Context, p string) ([]byte, bool, error) {
	c, ok := v.content[p]
	return []byte(c), ok, nil
}

// put places a file with content only.
func (v *jsView) put(p, content string) { v.content[p] = content }

// facts places a file of the family with its facts, with the language's
// profile from codeparse/queries.
func (v *jsView) facts(p string, facts *codeparse.Facts) *File {
	v.t.Helper()
	d := codeparse.Detect(p, nil, nil)
	if d.Lang == "" {
		v.t.Fatalf("no language for %s", p)
	}
	f := &File{Path: p, Layer: "base", Lang: d.Lang, Family: codeparse.FamilyJSTS, Facts: facts,
		Profile: jsProfiles(v.t)[d.Lang]}
	key, err := jsts{}.Package(context.Background(), v, f)
	if err != nil {
		v.t.Fatal(err)
	}
	f.Package = key
	v.files[p] = f
	v.content[p] = "/* facts */"
	return f
}

// golden places a golden of codeparse/testdata at p.
func (v *jsView) golden(p, golden string) *File {
	v.t.Helper()
	b, err := os.ReadFile("../../codeparse/testdata/" + golden + ".facts.json")
	if err != nil {
		v.t.Fatal(err)
	}
	var facts codeparse.Facts
	if err := json.Unmarshal(b, &facts); err != nil {
		v.t.Fatalf("%s: %v", golden, err)
	}
	return v.facts(p, &facts)
}

// module places a hand-built module exporting names (functions, or
// classes when capitalized) and importing imps.
func (v *jsView) module(p string, exported []string, imps ...codeparse.Import) *File {
	facts := &codeparse.Facts{Imports: imps}
	for _, n := range exported {
		kind := codeparse.KindFunction
		if n[0] >= 'A' && n[0] <= 'Z' {
			kind = codeparse.KindClass
		}
		facts.Symbols = append(facts.Symbols, codeparse.Symbol{ID: n, Name: n, QualName: n, Kind: kind, Exported: true})
	}
	return v.facts(p, facts)
}

func (v *jsView) scope(f *File) Scope {
	v.t.Helper()
	s, err := jsts{}.Scope(context.Background(), v, f)
	if err != nil {
		v.t.Fatalf("scope of %s: %v", f.Path, err)
	}
	return s
}

var jsProfileCache codeparse.Profiles

func jsProfiles(t *testing.T) codeparse.Profiles {
	t.Helper()
	if jsProfileCache == nil {
		ps, err := codeparse.LoadProfiles(os.DirFS("../../codeparse/queries"))
		if err != nil {
			t.Fatal(err)
		}
		jsProfileCache = ps
	}
	return jsProfileCache
}

func wantBound(t *testing.T, what string, got map[string][]string, want map[string][]string) {
	t.Helper()
	for k, w := range want {
		g := slices.Clone(got[k])
		slices.Sort(g)
		w = slices.Clone(w)
		slices.Sort(w)
		if w == nil {
			if got[k] != nil {
				t.Errorf("%s %q: got %v, want unbound", what, k, got[k])
			}
			continue
		}
		if !reflect.DeepEqual(g, w) {
			t.Errorf("%s %q: got %v, want %v", what, k, g, w)
		}
	}
}

func imp(kind, spec, alias string, names ...string) codeparse.Import {
	return codeparse.Import{Kind: kind, Spec: spec, Alias: alias, Names: names}
}

func Test_jstsRegistered(t *testing.T) {
	for _, lang := range []string{"javascript", "typescript"} {
		family, r, ok := ForLang(lang)
		if !ok || family != codeparse.FamilyJSTS {
			t.Fatalf("%s: family %q, ok %v", lang, family, ok)
		}
		if _, ok := r.(jsts); !ok {
			t.Fatalf("%s: resolver %T", lang, r)
		}
		rules := r.Rules()
		if !reflect.DeepEqual(rules.Separators, []string{"."}) || !rules.NameFallback || !rules.MemberFallback || rules.CaseInsensitive {
			t.Errorf("%s: rules %+v", lang, rules)
		}
	}
}

// Test_jstsGoldensJavaScript resolves the imports of the JavaScript goldens.
func Test_jstsGoldensJavaScript(t *testing.T) {
	v := newJSView(t)
	app := v.golden("src/App.jsx", "javascript/App.jsx")
	shapes := v.golden("src/shapes.mjs", "javascript/shapes.mjs")
	reexports := v.golden("src/reexports.js", "javascript/reexports.js")
	store := v.golden("src/store.cjs", "javascript/store.cjs")
	v.golden("src/widget.umd.js", "javascript/widget.umd.js")
	v.module("src/geometry.mjs", []string{"Polygon", "squared"})
	v.module("src/ui/index.js", []string{"Panel"})
	v.module("src/Button.jsx", []string{"Button"})
	v.module("src/api.js", []string{"fetchItems"})
	v.module("src/polyfills.js", nil)
	v.put("src/App.css", "h1 {}")

	if app.Package != "src/App.jsx" || shapes.Package != "src/shapes.mjs" {
		t.Fatalf("package keys %q %q, want the paths", app.Package, shapes.Package)
	}

	s := v.scope(app)
	wantBound(t, "App.jsx qualifier", s.Qualifiers, map[string][]string{
		"React":     {"npm:react"},
		"PropTypes": {"npm:prop-types"},
		"UI":        {"src/ui/index.js"}, // a directory's index file
		"Button":    {"src/Button.jsx"},
	})
	wantBound(t, "App.jsx name", s.Names, map[string][]string{
		"useState":  {"npm:react"},
		"useEffect": {"npm:react"}, // the JavaScript query writes `useEffect as useMountEffect` as useEffect
		"Button":    {"src/Button.jsx"},
		"React":     {"npm:react"},
	})
	if len(s.Open) != 0 {
		t.Errorf("App.jsx open: %v", s.Open)
	}

	s = v.scope(shapes)
	wantBound(t, "shapes.mjs qualifier", s.Qualifiers, map[string][]string{
		"geometry": {"src/geometry.mjs"},
	})
	wantBound(t, "shapes.mjs name", s.Names, map[string][]string{
		"EventEmitter": {"npm:node:events"},
	})

	// Re-exports bind nothing in their own file; require does.
	s = v.scope(reexports)
	wantBound(t, "reexports.js qualifier", s.Qualifiers, map[string][]string{
		"geometry":      nil,
		"defaultExport": {"npm:lib"},
		"json":          {"src/data.json"}, // not in the project: a key no file has
	})
	wantBound(t, "reexports.js name", s.Names, map[string][]string{
		"round": nil,
		"a":     {"npm:quoted-lib"},
		"b":     {"npm:quoted-lib"},
		"Store": {"src/store.cjs"},
	})

	s = v.scope(store)
	wantBound(t, "store.cjs qualifier", s.Qualifiers, map[string][]string{
		"fs":   {"npm:fs"},
		"path": {"npm:node:path"},
	})
	wantBound(t, "store.cjs name", s.Names, map[string][]string{
		"promisify": {"npm:util"},
	})

	// An importer of reexports.js reaches the modules that define the names.
	main := v.module("src/main.js", nil,
		imp("import", "./reexports.js", "", "round", "load", "geometry", "Store"),
		imp("import", "./reexports", "Root"),
		imp("import", "./reexports.js", "all"))
	s = v.scope(main)
	wantBound(t, "main.js name", s.Names, map[string][]string{
		"round": {"src/reexports.js", "src/shapes.mjs"}, // export * and export {round} from
		"load":  {"src/reexports.js", "src/shapes.mjs"},
		"Store": {"src/reexports.js", "src/shapes.mjs"}, // reexports.js only requires it: not re-exported
	})
	wantBound(t, "main.js qualifier", s.Qualifiers, map[string][]string{
		"geometry": {"src/geometry.mjs"}, // export * as geometry from
		"all":      {"src/reexports.js", "src/shapes.mjs", "src/App.jsx"},
	})
	if !slices.Contains(s.Names["Root"], "src/App.jsx") {
		t.Errorf("main.js Root: %v, want App.jsx's default (export {default} from)", s.Names["Root"])
	}
}

// Test_jstsGoldensTypeScript resolves the imports of the TypeScript goldens,
// whose named imports keep `a as b` and `type a` as written.
func Test_jstsGoldensTypeScript(t *testing.T) {
	v := newJSView(t)
	imports := v.golden("ts/imports.mts", "typescript/imports.mts")
	legacy := v.golden("ts/legacy.cts", "typescript/legacy.cts")
	component := v.golden("ts/component.tsx", "typescript/component.tsx")
	refs := v.golden("ts/references.ts", "typescript/references.ts")
	v.golden("ts/definitions.ts", "typescript/definitions.ts")
	v.golden("ts/globals.d.ts", "typescript/globals.d.ts")
	v.module("ts/store.ts", []string{"Store", "createStore"})
	v.module("ts/types.ts", []string{"Kind2"})
	v.module("ts/util.ts", []string{"helper"})
	v.module("ts/ui/index.tsx", []string{"Panel"})
	v.module("ts/icon.tsx", []string{"Icon"})
	v.module("ts/base.ts", []string{"Base", "Mixin", "Repository"})

	s := v.scope(imports)
	wantBound(t, "imports.mts qualifier", s.Qualifiers, map[string][]string{
		"fs":      {"npm:node:fs"},
		"path":    {"npm:node:path"},
		"Default": {"lib/default.js"},
		"util":    nil, // export * as util binds nothing here
	})
	wantBound(t, "imports.mts name", s.Names, map[string][]string{
		"readFile":    {"npm:node:fs"},
		"write":       nil, // renamed: the definition is writeFile
		"writeFile":   nil,
		"Entry":       {"ts/definitions.ts"}, // "./definitions.js" is the .ts source
		"Options":     {"ts/definitions.ts"},
		"Store":       {"ts/store.ts"}, // "type Store"
		"createStore": {"ts/store.ts"}, // "default as createStore"
		"lazy":        nil,             // import() binds nothing named
	})

	s = v.scope(legacy)
	wantBound(t, "legacy.cts qualifier", s.Qualifiers, map[string][]string{
		"assert":  {"npm:node:assert"},
		"express": {"npm:express"},
	})
	wantBound(t, "legacy.cts name", s.Names, map[string][]string{
		"Router":    {"npm:express"},
		"parseJson": nil, // { json: parseJson }
		"json":      nil,
	})

	s = v.scope(component)
	wantBound(t, "component.tsx", s.Qualifiers, map[string][]string{"UI": {"ts/ui/index.tsx"}, "React": {"npm:react"}})
	wantBound(t, "component.tsx", s.Names, map[string][]string{"Icon": {"ts/icon.tsx"}, "ReactNode": {"npm:react"}})

	s = v.scope(refs)
	wantBound(t, "references.ts", s.Names, map[string][]string{
		"Base": {"ts/base.ts"}, "Mixin": {"ts/base.ts"}, "Repository": {"ts/base.ts"},
	})

	// Through imports.mts: export {format, parse as parseEntry} from,
	// export * from ./types.js, export * as util from ./util.js.
	user := v.module("ts/user.ts", nil, imp("import", "./imports.mjs", "", "format", "parseEntry", "Kind2", "util", "load"))
	s = v.scope(user)
	wantBound(t, "user.ts name", s.Names, map[string][]string{
		// export * from ./types.js may re-export any name, so types.ts is
		// a candidate too.
		"format":     {"ts/imports.mts", "ts/definitions.ts", "ts/types.ts"},
		"parseEntry": {"ts/imports.mts", "ts/definitions.ts", "ts/types.ts"},
		"Kind2":      {"ts/imports.mts", "ts/types.ts"},
		"load":       {"ts/imports.mts", "ts/types.ts"},
	})
	wantBound(t, "user.ts qualifier", s.Qualifiers, map[string][]string{"util": {"ts/util.ts"}})
}

// Test_jstsModuleResolution covers tsconfig paths and baseUrl, package.json
// exports, main, workspaces, node_modules, self-reference and #imports.
func Test_jstsModuleResolution(t *testing.T) {
	v := newJSView(t)
	v.put("tsconfig.base.json", `{
		// shared settings
		"compilerOptions": { "baseUrl": "./lib", "paths": { "@/*": ["src/*"], }, },
	}`)
	v.put("app/tsconfig.json", `{
		"extends": "../tsconfig.base.json",
		/* the app's own paths, relative to the inherited baseUrl */
		"compilerOptions": {
			"paths": {
				"@app/*": ["../app/src/*", "../app/gen/*"],
				"@app/special": ["../app/src/special/main.ts"],
				"shared": ["shared/index.ts"]
			}
		}
	}`)
	v.put("package.json", `{"name": "root", "private": true, "workspaces": ["packages/*", "tools/cli"]}`)
	v.put("packages/widgets/package.json", `{"name": "@acme/widgets", "exports": {
		".": {"types": "./src/index.ts", "import": "./src/index.ts", "default": "./dist/index.js"},
		"./button": "./src/button.ts",
		"./icons/*": "./src/icons/*.ts",
		"./internal/*": null
	}}`)
	v.put("tools/cli/package.json", `{"name": "acme-cli", "main": "bin/cli.js"}`)
	v.put("node_modules/left-pad/package.json", `{"name": "left-pad", "main": "lib/pad"}`)
	v.put("node_modules/legacy/package.json", `{"name": "legacy"}`)
	v.put("app/package.json", `{"name": "@acme/app", "imports": {"#db": "./src/db/index.ts", "#cfg/*": {"node": "./src/cfg/*.ts"}}}`)

	main := v.module("app/src/main.ts", nil,
		imp("import", "@app/feature", "", "Feature"),
		imp("import", "@app/special", "", "Special"),
		imp("import", "@app/generated", "", "Gen"),
		imp("import", "@/shared-util", "", "util"),
		imp("import", "shared", "", "Shared"),
		imp("import", "helpers/strings", "", "trim"),
		imp("import", "@acme/widgets", "", "Widget"),
		imp("import", "@acme/widgets/button", "", "Button"),
		imp("import", "@acme/widgets/icons/star", "", "Star"),
		imp("import", "@acme/widgets/internal/x", "", "Hidden"),
		imp("import", "@acme/widgets/package.json", "", "NotExported"),
		imp("import", "acme-cli", "", "run"),
		imp("import", "left-pad", "pad"),
		imp("import", "legacy", "legacy"),
		imp("import", "@acme/app/model", "", "Model"),
		imp("import", "#db", "", "connect"),
		imp("import", "#cfg/prod", "", "prod"),
		imp("import", "./vendor/jquery", "$"),
		imp("import", "../../../outside/x", "", "Out"))
	v.module("app/src/feature.ts", []string{"Feature"})
	v.module("app/src/special/main.ts", []string{"Special"})
	v.module("app/gen/generated.ts", []string{"Gen"})
	v.module("lib/src/shared-util.ts", []string{"util"})
	v.module("lib/shared/index.ts", []string{"Shared"})
	v.module("lib/helpers/strings.ts", []string{"trim"})
	v.module("packages/widgets/src/index.ts", []string{"Widget"})
	v.module("packages/widgets/src/button.ts", []string{"Button"})
	v.module("packages/widgets/src/icons/star.ts", []string{"Star"})
	v.module("packages/widgets/src/internal/x.ts", []string{"Hidden"})
	v.module("tools/cli/bin/cli.js", []string{"run"})
	v.module("node_modules/left-pad/lib/pad.js", []string{"pad"})
	v.put("node_modules/left-pad/lib/pad.d.ts", "/* types */")
	v.module("node_modules/legacy/index.js", []string{"legacy"})
	v.module("app/model.ts", []string{"Model"})
	v.module("app/src/db/index.ts", []string{"connect"})
	v.module("app/src/cfg/prod.ts", []string{"prod"})
	v.module("app/src/vendor/jquery.js", []string{"jQuery"})

	s := v.scope(main)
	wantBound(t, "main.ts name", s.Names, map[string][]string{
		"Feature":     {"app/src/feature.ts"},             // @app/* in app/tsconfig.json
		"Special":     {"app/src/special/main.ts"},        // the exact pattern beats the wildcard
		"Gen":         {"app/gen/generated.ts"},           // the pattern's second target
		"util":        {"npm:@/shared-util"},              // the app's paths replace the base config's @/*
		"Shared":      {"lib/shared/index.ts"},            // paths relative to the inherited baseUrl
		"trim":        {"lib/helpers/strings.ts"},         // baseUrl alone
		"Widget":      {"packages/widgets/src/index.ts"},  // a workspace, exports "." types
		"Button":      {"packages/widgets/src/button.ts"}, // exports "./button"
		"Star":        {"packages/widgets/src/icons/star.ts"},
		"Hidden":      {"npm:@acme/widgets/internal/x"}, // exports null: not reachable
		"NotExported": {"npm:@acme/widgets/package.json"},
		"run":         {"tools/cli/bin/cli.js"},   // a workspace named by its directory, main
		"Model":       {"app/model.ts"},           // the package itself (self-reference), no exports
		"connect":     {"app/src/db/index.ts"},    // package.json imports
		"prod":        {"app/src/cfg/prod.ts"},    // imports pattern with a condition
		"Out":         {"npm:../../../outside/x"}, // leaves the project
	})
	wantBound(t, "main.ts qualifier", s.Qualifiers, map[string][]string{
		"pad":    {"node_modules/left-pad/lib/pad.js", "node_modules/left-pad/lib/pad.d.ts"}, // main without extension, its declaration file
		"legacy": {"node_modules/legacy/index.js"},
		"$":      {"app/src/vendor/jquery.js"},
	})

	// A config that only extends inherits the base's paths and baseUrl.
	v.put("other/tsconfig.json", `{"extends": "../tsconfig.base.json"}`)
	other := v.module("other/o.ts", nil, imp("import", "@/shared-util", "", "util"))
	s = v.scope(other)
	wantBound(t, "o.ts name", s.Names, map[string][]string{"util": {"lib/src/shared-util.ts"}})

	// A JavaScript file with the default profile options: no tsconfig
	// above it, so @app/feature is an external package; and the profile's
	// moduleSystems decide whether require binds.
	js := v.module("scripts/build.js", nil,
		imp("import", "@app/feature", "", "Feature"),
		imp("require", "../app/src/feature", "feature"))
	s = v.scope(js)
	wantBound(t, "build.js name", s.Names, map[string][]string{"Feature": {"npm:@app/feature"}})
	wantBound(t, "build.js qualifier", s.Qualifiers, map[string][]string{"feature": {"app/src/feature.ts"}})

	esmOnly := *js.Profile
	esmOnly.Scope.Options = map[string]string{"moduleSystems": "esm"}
	js.Profile = &esmOnly
	s = v.scope(js)
	wantBound(t, "build.js (esm only) qualifier", s.Qualifiers, map[string][]string{"feature": nil})
	wantBound(t, "build.js (esm only) name", s.Names, map[string][]string{"Feature": {"npm:@app/feature"}})
}

func Test_jstsVisible(t *testing.T) {
	v := newJSView(t)
	shapes := v.golden("src/shapes.mjs", "javascript/shapes.mjs")
	store := v.golden("src/store.cjs", "javascript/store.cjs")
	widget := v.golden("src/widget.umd.js", "javascript/widget.umd.js")
	app := v.golden("src/App.jsx", "javascript/App.jsx")
	globals := v.golden("ts/globals.d.ts", "typescript/globals.d.ts")
	sym := func(f *File, id string) Candidate {
		for i := range f.Facts.Symbols {
			if f.Facts.Symbols[i].ID == id {
				return Candidate{File: f, Symbol: &f.Facts.Symbols[i]}
			}
		}
		t.Fatalf("%s has no symbol %s", f.Path, id)
		return Candidate{}
	}
	cases := []struct {
		name string
		from *File
		c    Candidate
		step Step
		want bool
	}{
		{"same file, not exported", shapes, sym(shapes, "created"), StepFile, true},
		{"exported, by name", app, sym(shapes, "round"), StepName, true},
		{"not exported, by name", app, sym(shapes, "registry"), StepName, false},
		// export { created } comes after the definition: not flagged, but an
		// import naming it proves it is exported.
		{"exported later, imported", app, sym(shapes, "created"), StepImport, true},
		{"exported later, by qualifier", app, sym(shapes, "created"), StepQualified, false},
		{"member, by member fallback", app, sym(shapes, "Shape.area"), StepMember, true},
		{"#private member", app, sym(shapes, "Shape.#describe"), StepMember, false},
		{"#private member, same file", shapes, sym(shapes, "Shape.#describe"), StepMember, true},
		// Store.prototype.get is a member, not something an import names.
		{"prototype member, imported", app, sym(store, "Store.get"), StepImport, false},
		{"prototype member, by member fallback", app, sym(store, "Store.get"), StepMember, true},
		{"nested function, imported", app, sym(store, "memoize.lookup"), StepImport, false},
		// A script (no import, no export) defines globals.
		{"script global, by name", app, sym(widget, "c"), StepName, true},
		{"ambient declaration file global", app, sym(globals, "configure"), StepName, true},
		// App.jsx imports useState from react: the name fallback must not
		// link it to a project definition of that name.
		{"imported name, by name", app, Candidate{File: shapes, Symbol: &codeparse.Symbol{ID: "useState", Name: "useState", QualName: "useState", Exported: true}}, StepName, false},
	}
	for _, c := range cases {
		if got := (jsts{}).Visible(c.from, c.c, c.step); got != c.want {
			t.Errorf("%s: Visible from %s of %s#%s at %s = %v, want %v", c.name, c.from.Path, c.c.File.Path, c.c.Symbol.ID, c.step, got, c.want)
		}
	}
}

func Test_jstsImportName(t *testing.T) {
	for written, want := range map[string][2]string{
		"a":                     {"a", "a"},
		"writeFile as write":    {"writeFile", "write"},
		"type Store":            {"Store", "Store"},
		"type Store as S":       {"Store", "S"},
		"default as createStor": {"default", "createStor"},
		"json: parseJson":       {"json", "parseJson"},
		" b ":                   {"b", "b"},
	} {
		n, l := JSImportName(written)
		if n != want[0] || l != want[1] {
			t.Errorf("JSImportName(%q) = %q, %q, want %q, %q", written, n, l, want[0], want[1])
		}
	}
}

func Test_jstsStripJSONC(t *testing.T) {
	in := `{
  // a comment with "quotes"
  "paths": {"@/*": ["src/*"], "a//b": ["x"],}, /* block
  comment */ "s": "esc\"aped // not a comment",
  "list": [1, 2,
  ],
}`
	var m map[string]any
	if err := json.Unmarshal(StripJSONC([]byte(in)), &m); err != nil {
		t.Fatalf("%v\n%s", err, StripJSONC([]byte(in)))
	}
	want := map[string]any{
		"paths": map[string]any{"@/*": []any{"src/*"}, "a//b": []any{"x"}},
		"s":     `esc"aped // not a comment`,
		"list":  []any{1.0, 2.0},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("got %v, want %v", m, want)
	}
}

func Test_jstsPackageName(t *testing.T) {
	for spec, want := range map[string][2]string{
		"lodash":            {"lodash", "."},
		"lodash/fp/map":     {"lodash", "./fp/map"},
		"@acme/widgets":     {"@acme/widgets", "."},
		"@acme/widgets/a/b": {"@acme/widgets", "./a/b"},
	} {
		n, sub := jsPackageName(spec)
		if n != want[0] || sub != want[1] {
			t.Errorf("jsPackageName(%q) = %q, %q, want %v", spec, n, sub, want)
		}
	}
}
