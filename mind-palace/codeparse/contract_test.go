package codeparse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func TestLanguagesTable(t *testing.T) {
	want := []string{"c", "cpp", "csharp", "dart", "go", "java", "javascript", "kotlin", "lisp", "lua",
		"objc", "ocaml", "perl", "php", "python", "ruby", "rust", "sql", "swift", "typescript"}
	var got []string
	for _, l := range Languages() {
		got = append(got, l.ID)
		if !validWord(l.ID) || l.Name == "" || len(l.Extensions) == 0 {
			t.Errorf("language %+v: needs a word ID, a name and extensions", l)
		}
		for ext, dialect := range l.Dialects {
			if !validWord(dialect) || !contains(l.Extensions, ext) {
				t.Errorf("%s: dialect %q on %q, not a word or not one of its extensions", l.ID, dialect, ext)
			}
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Languages() = %v, want %v", got, want)
	}
	if fams := Families(); len(fams) != 16 {
		t.Errorf("Families() = %v, want 16 families", fams)
	}
	if l, ok := LookupLanguage("kotlin"); !ok || l.Family != FamilyJVM {
		t.Errorf("LookupLanguage(kotlin) = %+v, %v", l, ok)
	}
	if _, ok := LookupLanguage("matlab"); ok {
		t.Error("LookupLanguage(matlab) found a language")
	}
}

// Every extension of the table detects as its language, except .h, which
// is decided by content and neighbours.
func TestDetectEveryExtension(t *testing.T) {
	for _, l := range Languages() {
		for _, ext := range l.Extensions {
			if ext == ".h" {
				continue
			}
			d := Detect("src/file"+ext, nil, nil)
			want := Detection{Lang: l.ID, Dialect: l.Dialects[ext], By: ByExtension}
			if d != want {
				t.Errorf("Detect(file%s) = %+v, want %+v", ext, d, want)
			}
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		path string
		want Detection
	}{
		{"main.go", Detection{Lang: "go", By: ByExtension}},
		{"a/b/types.d.ts", Detection{Lang: "typescript", By: ByExtension}},
		{"App.tsx", Detection{Lang: "typescript", Dialect: "tsx", By: ByExtension}},
		{"App.jsx", Detection{Lang: "javascript", By: ByExtension}},
		{"lib.mli", Detection{Lang: "ocaml", Dialect: "interface", By: ByExtension}},
		{"core.clj", Detection{Lang: "lisp", Dialect: "clojure", By: ByExtension}},
		{"init.el", Detection{Lang: "lisp", Dialect: "elisp", By: ByExtension}},
		{"pkg.asd", Detection{Lang: "lisp", Dialect: "commonlisp", By: ByExtension}},
		{"main.rkt", Detection{Lang: "lisp", Dialect: "racket", By: ByExtension}},
		{"lib.ss", Detection{Lang: "lisp", Dialect: "scheme", By: ByExtension}},
		{"View.m", Detection{Lang: "objc", By: ByExtension}},
		{"View.mm", Detection{Lang: "objc", By: ByExtension}},
		{"Main.JAVA", Detection{Lang: "java", By: ByExtension}},
		{"Gemfile", Detection{Lang: "ruby", By: ByFilename}},
		{"sub/Gemfile", Detection{Lang: "ruby", By: ByFilename}},
		{"Gemfile.lock", Detection{}},
		{"tasks.rake", Detection{Lang: "ruby", By: ByExtension}},
		{"README.md", Detection{Lang: Markdown, By: ByExtension}},
		{"docs/guide.markdown", Detection{Lang: Markdown, By: ByExtension}},
		{".eslintrc", Detection{}},
		{".eslintrc.js", Detection{Lang: "javascript", By: ByExtension}},
		{"Makefile", Detection{}},
		{"notes.txt", Detection{}},
		{"script.m.bak", Detection{}},
		{"test.t", Detection{Lang: "perl", By: ByExtension}},
	}
	for _, c := range cases {
		if got := Detect(c.path, nil, nil); got != c.want {
			t.Errorf("Detect(%q) = %+v, want %+v", c.path, got, c.want)
		}
	}
}

func TestDetectHeader(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		neighbours []string
		want       Detection
	}{
		{"objc interface", "#import <Foundation/Foundation.h>\n@interface View : NSObject\n@end\n", nil,
			Detection{Lang: LangObjC, By: ByContent}},
		{"objc protocol", "@protocol Drawable\n- (void)draw;\n@end\n", []string{"a.cpp"},
			Detection{Lang: LangObjC, By: ByContent}},
		{"objc module import", "@import UIKit;\n", nil, Detection{Lang: LangObjC, By: ByContent}},
		{"cpp class", "#pragma once\nclass Widget {\npublic:\n  void f();\n};\n", []string{"a.c"},
			Detection{Lang: LangCPP, By: ByContent}},
		{"cpp class with base", "class Widget : public Base {};\n", nil, Detection{Lang: LangCPP, By: ByContent}},
		{"cpp forward class", "class Widget;\nint f(void);\n", nil, Detection{Lang: LangCPP, By: ByContent}},
		{"cpp template", "template <typename T>\nT max(T a, T b);\n", nil, Detection{Lang: LangCPP, By: ByContent}},
		{"cpp namespace", "namespace a::b {\nint f();\n}\n", nil, Detection{Lang: LangCPP, By: ByContent}},
		{"cpp std", "#include <vector>\nextern std::vector<int> xs;\n", nil, Detection{Lang: LangCPP, By: ByContent}},
		{"cpp using", "using namespace std;\n", nil, Detection{Lang: LangCPP, By: ByContent}},
		{"c guarded header", "#ifndef A_H\n#define A_H\n#ifdef __cplusplus\nextern \"C\" {\n#endif\nint f(void);\nstruct s { int class_; };\n#ifdef __cplusplus\n}\n#endif\n#endif\n",
			nil, Detection{Lang: LangC, By: ByDefault}},
		{"markers in comments and strings", "// class Widget {\n/* template <T>\n   @interface X */\nconst char *s = \"namespace x {\";\nint f(void);\n",
			nil, Detection{Lang: LangC, By: ByDefault}},
		{"c bitfield is not a base list", "struct flags {\n  unsigned a : 1;\n};\n", nil, Detection{Lang: LangC, By: ByDefault}},
		{"neighbour objc", "int f(void);\n", []string{"x.h", "View.m", "util.c"}, Detection{Lang: LangObjC, By: ByNeighbours}},
		{"neighbour objc++", "int f(void);\n", []string{"View.mm"}, Detection{Lang: LangObjC, By: ByNeighbours}},
		{"neighbour cpp", "int f(void);\n", []string{"dir/x.cpp", "y.c"}, Detection{Lang: LangCPP, By: ByNeighbours}},
		{"neighbour hpp", "int f(void);\n", []string{"y.HPP"}, Detection{Lang: LangCPP, By: ByNeighbours}},
		{"neighbour c", "int f(void);\n", []string{"x.c", "README.md"}, Detection{Lang: LangC, By: ByNeighbours}},
		{"self and other headers ignored", "int f(void);\n", []string{"x.h", "y.h"}, Detection{Lang: LangC, By: ByDefault}},
		{"no evidence", "", nil, Detection{Lang: LangC, By: ByDefault}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect("include/x.h", []byte(c.content), c.neighbours); got != c.want {
				t.Errorf("Detect = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestStripCommentsAndStringsKeepsLines(t *testing.T) {
	src := "a /* x\ny */ b // c\n'\\'' \"q\\\"r\" d\n"
	got := string(stripCommentsAndStrings([]byte(src)))
	if strings.Count(got, "\n") != strings.Count(src, "\n") {
		t.Errorf("newlines changed: %q", got)
	}
	for _, gone := range []string{"x", "y", "c", "q", "r"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q still in %q", gone, got)
		}
	}
	for _, kept := range []string{"a", "b", "d"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q lost from %q", kept, got)
		}
	}
	// An unterminated comment or string must not run past the end.
	for _, s := range []string{"/* open", "\"open", "'", "/"} {
		stripCommentsAndStrings([]byte(s))
	}
}

const goProfile = `name: Go
family: go
extensions: [.go]
kinds:
  definition.class: struct
scope:
  separators: ["."]
  packageBy: directory
  exported: capitalized
`

func queriesFS() fstest.MapFS {
	return fstest.MapFS{
		"README.md":         {Data: []byte("# queries\n")},
		"go/profile.yaml":   {Data: []byte(goProfile)},
		"go/tags.scm":       {Data: []byte("(function_declaration name: (identifier) @name) @definition.function\n")},
		"lisp/profile.yaml": {Data: []byte(lispProfile)},
		"lisp/tags.scm":     {Data: []byte("; common\n")},
		"lisp/clojure.scm":  {Data: []byte("; clojure\n")},
	}
}

const lispProfile = `family: lisp
extensions: [.lisp, .lsp, .cl, .asd, .el, .clj, .cljs, .cljc, .edn, .scm, .ss, .rkt]
dialects:
  clojure: { query: clojure.scm }
  commonlisp: { grammar: commonlisp }
definingForms:
  - { form: defun, kind: function }
  - { form: defmacro, kind: macro }
  - { form: defn, kind: function, dialect: clojure }
scope:
  separators: [":", "/"]
  packageBy: declaration
  caseInsensitive: true
  options:
    packageForm: in-package
`

func TestLoadProfiles(t *testing.T) {
	ps, err := LoadProfiles(queriesFS())
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(ps) != 2 {
		t.Fatalf("loaded %d profiles, want 2", len(ps))
	}
	g := ps.For(Detect("main.go", nil, nil))
	if g == nil {
		t.Fatal("no profile for main.go")
	}
	if g.Lang != "go" || g.Name != "Go" || g.Grammar != "go" || g.Query != DefaultQuery {
		t.Errorf("go profile defaults: %+v", g)
	}
	if g.Scope.PackageBy != PackageByDirectory || g.Scope.Exported != ExportedCapitalized {
		t.Errorf("go scope: %+v", g.Scope)
	}
	if len(g.Digest) != 64 {
		t.Errorf("digest %q", g.Digest)
	}
	if k, ok := g.SymbolKind("definition.class"); !ok || k != KindStruct {
		t.Errorf("profile kinds override: %q %v", k, ok)
	}
	if k, ok := g.SymbolKind("definition.method"); !ok || k != KindMethod {
		t.Errorf("default kind: %q %v", k, ok)
	}
	if k, ok := g.SymbolKind("definition.record"); !ok || k != "record" {
		t.Errorf("open kind: %q %v", k, ok)
	}
	if _, ok := g.SymbolKind("reference.call"); ok {
		t.Error("a reference capture produced a symbol kind")
	}
	if _, ok := g.SymbolKind("definition.Bad-Kind"); ok {
		t.Error("a capture that is not a word produced a symbol kind")
	}

	l := ps["lisp"]
	if l.Name != "Lisp" || !l.Scope.CaseInsensitive || l.Scope.Options["packageForm"] != "in-package" {
		t.Errorf("lisp profile: %+v", l)
	}
	for _, c := range []struct{ dialect, grammar, query string }{
		{"", "lisp", "tags.scm"},
		{"clojure", "clojure", "clojure.scm"},
		{"commonlisp", "commonlisp", "tags.scm"},
		{"elisp", "elisp", "tags.scm"},
	} {
		if g, q := l.Resolve(c.dialect); g != c.grammar || q != c.query {
			t.Errorf("Resolve(%q) = %s, %s; want %s, %s", c.dialect, g, q, c.grammar, c.query)
		}
	}
	if k, ok := l.DefiningForm("commonlisp", "defun"); !ok || k != KindFunction {
		t.Errorf("defun: %q %v", k, ok)
	}
	if k, ok := l.DefiningForm("clojure", "defn"); !ok || k != KindFunction {
		t.Errorf("defn in clojure: %q %v", k, ok)
	}
	if _, ok := l.DefiningForm("commonlisp", "defn"); ok {
		t.Error("defn defines in commonlisp")
	}
	if _, ok := l.DefiningForm("elisp", "let"); ok {
		t.Error("let defines")
	}
}

func TestFactsVersionFollowsTheLanguageData(t *testing.T) {
	fsys := queriesFS()
	before, err := LoadProfiles(fsys)
	if err != nil {
		t.Fatal(err)
	}
	fsys["go/tags.scm"] = &fstest.MapFile{Data: []byte("; changed\n")}
	after, err := LoadProfiles(fsys)
	if err != nil {
		t.Fatal(err)
	}
	gb, ga := before["go"].FactsVersion(""), after["go"].FactsVersion("")
	if gb == ga || !strings.HasPrefix(gb, Version+"/go@") {
		t.Errorf("go version %q → %q: want a change, prefixed %s/go@", gb, ga, Version)
	}
	if lb, la := before["lisp"].FactsVersion("clojure"), after["lisp"].FactsVersion("clojure"); lb != la || !strings.HasPrefix(lb, Version+"/lisp:clojure@") {
		t.Errorf("lisp version %q → %q: want unchanged, with the dialect", lb, la)
	}
	if before["lisp"].FactsVersion("clojure") == before["lisp"].FactsVersion("") {
		t.Error("a dialect shares its language's version")
	}
	if TextFactsVersion("") != Version+"/text" || TextFactsVersion(Markdown) != Version+"/markdown" {
		t.Errorf("text versions: %q %q", TextFactsVersion(""), TextFactsVersion(Markdown))
	}
}

// A profile written from the Languages table loads for every language, so
// each language story only has to fill in its own data.
func TestEveryLanguageAcceptsAProfile(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, l := range Languages() {
		var b strings.Builder
		fmt.Fprintf(&b, "family: %s\nextensions: [%s]\n", l.Family, strings.Join(l.Extensions, ", "))
		if len(l.Filenames) > 0 {
			fmt.Fprintf(&b, "filenames: [%s]\n", strings.Join(l.Filenames, ", "))
		}
		fsys[l.ID+"/profile.yaml"] = &fstest.MapFile{Data: []byte(b.String())}
		fsys[l.ID+"/tags.scm"] = &fstest.MapFile{Data: []byte("")}
	}
	ps, err := LoadProfiles(fsys)
	if err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	if len(ps) != len(Languages()) {
		t.Errorf("loaded %d profiles, want %d", len(ps), len(Languages()))
	}
}

func TestProfileErrors(t *testing.T) {
	cases := []struct {
		name, lang, yaml, want string
	}{
		{"unknown key", "go", goProfile + "extentions: [.go]\n", "field extentions not found"},
		{"unknown language", "matlab", "family: matlab\nextensions: [.m]\n", "unknown language"},
		{"lang mismatch", "go", "lang: golang\n" + goProfile, "does not match its directory"},
		{"family", "go", "family: python\nextensions: [.go]\n", `family "python", want "go"`},
		{"missing family", "go", "extensions: [.go]\n", `family "", want "go"`},
		{"extensions", "c", "family: cfamily\nextensions: [.c]\n", "extensions [.c]"},
		{"filenames", "ruby", "family: ruby\nextensions: [.rb, .rake]\n", "filenames []"},
		{"query outside", "go", "family: go\nextensions: [.go]\nquery: ../x.scm\n", "not a .scm file"},
		{"query not scm", "go", "family: go\nextensions: [.go]\nquery: tags.txt\n", "not a .scm file"},
		{"dialect", "go", "family: go\nextensions: [.go]\ndialects:\n  tsx: {}\n", `dialect "tsx" is not one of Go's`},
		{"kind", "go", "family: go\nextensions: [.go]\nkinds:\n  definition.class: Struct\n", "lowercase word"},
		{"defining form", "lisp", strings.Replace(lispProfile, "kind: macro", "kind: \"\"", 1), "definingForms[1]"},
		{"defining form dialect", "lisp", strings.Replace(lispProfile, "dialect: clojure }", "dialect: guile }", 1), `dialect "guile"`},
		{"packageBy", "go", "family: go\nextensions: [.go]\nscope: {packageBy: folder}\n", "scope.packageBy"},
		{"exported", "go", "family: go\nextensions: [.go]\nscope: {exported: public}\n", "scope.exported"},
		{"separator", "go", "family: go\nextensions: [.go]\nscope: {separators: [\"\"]}\n", "empty separator"},
		{"not yaml", "go", "family: [", "go/profile.yaml"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseProfile(c.lang, []byte(c.yaml))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("ParseProfile error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestParseProfileReportsEveryProblem(t *testing.T) {
	_, err := ParseProfile("go", []byte("family: rust\nextensions: [.rs]\nscope: {exported: public}\n"))
	if err == nil {
		t.Fatal("no error")
	}
	for _, want := range []string{"family", "extensions", "scope.exported"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestLoadProfilesErrors(t *testing.T) {
	noProfile := queriesFS()
	noProfile["rust/tags.scm"] = &fstest.MapFile{Data: []byte("")}
	noQuery := queriesFS()
	delete(noQuery, "lisp/clojure.scm")
	badProfile := queriesFS()
	badProfile["go/profile.yaml"] = &fstest.MapFile{Data: []byte("family: go\n")}
	for name, fsys := range map[string]fstest.MapFS{
		"directory without profile": noProfile,
		"missing dialect query":     noQuery,
		"invalid profile":           badProfile,
	} {
		t.Run(name, func(t *testing.T) {
			if ps, err := LoadProfiles(fsys); err == nil {
				t.Errorf("LoadProfiles = %v, want an error", ps)
			}
		})
	}
	ps, err := LoadProfiles(fstest.MapFS{})
	if err != nil || len(ps) != 0 {
		t.Errorf("empty queries: %v, %v", ps, err)
	}
}

func pos(line, col, off int) Pos { return Pos{Line: line, Column: col, Offset: off} }

func sampleFacts() Facts {
	body := "func (s *Server) Run() error {\n\treturn nil\n}"
	return Facts{
		Lang:    "go",
		Package: "srv",
		Symbols: []Symbol{
			{ID: SymbolID("srv.Server", 1), Kind: KindStruct, Name: "Server", QualName: "srv.Server",
				Signature: "type Server struct", Start: pos(3, 0, 20), End: pos(5, 1, 50), Exported: true},
			{ID: SymbolID("srv.Server.Run", 1), Kind: KindMethod, Name: "Run", QualName: "srv.Server.Run",
				Signature: "func (s *Server) Run() error", Doc: "Run serves.", Start: pos(8, 0, 70), End: pos(10, 1, 110),
				Parent: "srv.Server", Exported: true},
		},
		Imports: []Import{{Spec: "fmt", Start: pos(2, 7, 15)}, {Spec: "net/http", Alias: "h", Names: nil, Start: pos(2, 13, 21)}},
		Refs: []Ref{{Name: "Println", Qualifier: "fmt", Kind: RefCall, Start: pos(9, 5, 95), Enclosing: "srv.Server.Run"},
			{Name: "Server", Kind: RefType, Start: pos(8, 9, 79)}},
		Chunks: []Chunk{{ID: ChunkID("srv.Server.Run", 8, 1), Kind: ChunkSymbol, SymbolID: "srv.Server.Run",
			Start: pos(7, 0, 60), End: pos(10, 1, 110), Text: body, Hash: HashText(body)}},
	}
}

func TestFactsValidate(t *testing.T) {
	f := sampleFacts()
	if err := f.Validate(); err != nil {
		t.Fatalf("valid facts: %v", err)
	}
	empty := Facts{}
	if err := empty.Validate(); err != nil {
		t.Errorf("empty (text) facts: %v", err)
	}
	cases := map[string]func(f *Facts){
		"lang":             func(f *Facts) { f.Lang = "Go!" },
		"dialect":          func(f *Facts) { f.Dialect = "TSX" },
		"empty symbol id":  func(f *Facts) { f.Symbols[0].ID = "" },
		"duplicate id":     func(f *Facts) { f.Symbols[1].ID = f.Symbols[0].ID; f.Symbols[1].Parent = "" },
		"symbol name":      func(f *Facts) { f.Symbols[0].Name = "" },
		"symbol kind":      func(f *Facts) { f.Symbols[0].Kind = "Struct" },
		"missing parent":   func(f *Facts) { f.Symbols[1].Parent = "nope" },
		"own parent":       func(f *Facts) { f.Symbols[0].Parent = f.Symbols[0].ID },
		"reversed span":    func(f *Facts) { f.Symbols[0].End = pos(1, 0, 0) },
		"zero position":    func(f *Facts) { f.Symbols[0].Start = Pos{} },
		"import spec":      func(f *Facts) { f.Imports[0].Spec = "" },
		"import kind":      func(f *Facts) { f.Imports[0].Kind = "#include" },
		"import position":  func(f *Facts) { f.Imports[0].Start = Pos{} },
		"ref name":         func(f *Facts) { f.Refs[0].Name = "" },
		"ref kind":         func(f *Facts) { f.Refs[0].Kind = "include" },
		"ref position":     func(f *Facts) { f.Refs[0].Start = pos(0, 0, 0) },
		"ref enclosing":    func(f *Facts) { f.Refs[0].Enclosing = "nope" },
		"chunk id":         func(f *Facts) { f.Chunks = append(f.Chunks, f.Chunks[0]) },
		"chunk kind":       func(f *Facts) { f.Chunks[0].Kind = "" },
		"chunk symbol":     func(f *Facts) { f.Chunks[0].SymbolID = "nope" },
		"chunk hash":       func(f *Facts) { f.Chunks[0].Text += " " },
		"parse error mix":  func(f *Facts) { f.ParseError = ParseTimeout },
		"chunk empty id":   func(f *Facts) { f.Chunks[0].ID = "" },
		"chunk span order": func(f *Facts) { f.Chunks[0].Start, f.Chunks[0].End = f.Chunks[0].End, f.Chunks[0].Start },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := sampleFacts().Clone()
			mutate(&f)
			if err := f.Validate(); err == nil {
				t.Error("Validate accepted invalid facts")
			}
		})
	}
	textFallback := Facts{Lang: "go", ParseError: ParseTimeout, Chunks: []Chunk{{ID: ChunkID("", 1, 1), Kind: ChunkWindow,
		Start: pos(1, 0, 0), End: pos(60, 0, 900), Text: "x", Hash: HashText("x")}}}
	if err := textFallback.Validate(); err != nil {
		t.Errorf("text fallback: %v", err)
	}
}

func TestIDsAndHash(t *testing.T) {
	if SymbolID("a.F", 1) != "a.F" || SymbolID("a.F", 0) != "a.F" || SymbolID("a.F", 3) != "a.F~3" {
		t.Error("SymbolID")
	}
	if ChunkID("a.F", 10, 2) != "a.F#2" || ChunkID("", 61, 1) != "L61#1" {
		t.Error("ChunkID")
	}
	if HashText("") != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Error("HashText")
	}
	if pos(3, 4, 40).String() != "3:5" || !pos(1, 0, 1).Before(pos(1, 0, 2)) {
		t.Error("Pos")
	}
}

func TestFactsJSONRoundTrip(t *testing.T) {
	f := sampleFacts()
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"qualName"`, `"symbolId"`, `"enclosing"`, `"offset"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("JSON lacks %s: %s", key, data)
		}
	}
	var back Facts
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, f) {
		t.Errorf("round trip changed facts:\n%+v\n%+v", back, f)
	}
}

func TestFactsCloneIsDeep(t *testing.T) {
	f := sampleFacts()
	f.Imports[1].Names = []string{"a"}
	c := f.Clone()
	c.Symbols[0].Name = "x"
	c.Imports[1].Names[0] = "b"
	c.Refs[0].Name = "y"
	c.Chunks[0].Text = "z"
	if f.Symbols[0].Name == "x" || f.Imports[1].Names[0] == "b" || f.Refs[0].Name == "y" || f.Chunks[0].Text == "z" {
		t.Error("Clone shares memory with the original")
	}
	if (Facts{}).Clone().Imports != nil {
		t.Error("Clone of empty facts allocated imports")
	}
}

func TestKey(t *testing.T) {
	k := ContentKey([]byte(""), "1/go@abc")
	if k.SHA256 != HashText("") || k.Version != "1/go@abc" {
		t.Errorf("ContentKey = %+v", k)
	}
	if err := k.Validate(); err != nil {
		t.Error(err)
	}
	for _, bad := range []Key{
		{SHA256: k.SHA256},
		{SHA256: strings.ToUpper(k.SHA256), Version: "1"},
		{SHA256: k.SHA256[:63], Version: "1"},
		{SHA256: k.SHA256[:63] + "g", Version: "1"},
	} {
		if err := bad.Validate(); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Validate(%+v) = %v", bad, err)
		}
	}
}

func TestMemoryCache(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(0)
	key := ContentKey([]byte("package srv"), "1/go@abc")
	if _, ok, err := c.Get(ctx, key); ok || err != nil {
		t.Fatalf("Get on empty cache: %v %v", ok, err)
	}
	f := sampleFacts()
	if err := c.Put(ctx, key, f); err != nil {
		t.Fatal(err)
	}
	f.Symbols[0].Name = "changed after Put"
	got, ok, err := c.Get(ctx, key)
	if !ok || err != nil || !reflect.DeepEqual(got, sampleFacts()) {
		t.Fatalf("Get = %+v %v %v", got, ok, err)
	}
	got.Symbols[0].Name = "changed after Get"
	if again, _, _ := c.Get(ctx, key); again.Symbols[0].Name != "Server" {
		t.Error("Get returned the cache's own copy")
	}
	// Another version of the same content misses: lazy invalidation.
	if _, ok, _ := c.Get(ctx, Key{SHA256: key.SHA256, Version: "2/go@abc"}); ok {
		t.Error("hit across versions")
	}
	if err := c.Put(ctx, key, Facts{Lang: "go"}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := c.Get(ctx, key); len(got.Symbols) != 0 || c.Len() != 1 {
		t.Errorf("Put did not replace: %+v, len %d", got, c.Len())
	}
	if err := c.Put(ctx, Key{}, f); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Put with invalid key: %v", err)
	}
	if _, _, err := c.Get(ctx, Key{}); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("Get with invalid key: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := c.Get(cancelled, key); !errors.Is(err, context.Canceled) {
		t.Errorf("Get with cancelled context: %v", err)
	}
	if err := c.Put(cancelled, key, f); !errors.Is(err, context.Canceled) {
		t.Errorf("Put with cancelled context: %v", err)
	}
}

func TestMemoryCacheEvictsLeastRecentlyUsed(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(2)
	k := func(s string) Key { return ContentKey([]byte(s), "1") }
	for _, s := range []string{"a", "b"} {
		if err := c.Put(ctx, k(s), Facts{Lang: "go", Package: s}); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok, _ := c.Get(ctx, k("a")); !ok { // a is now the most recent
		t.Fatal("a missing")
	}
	if err := c.Put(ctx, k("c"), Facts{}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(ctx, k("b")); ok {
		t.Error("b, the least recently used, was kept")
	}
	for _, s := range []string{"a", "c"} {
		if _, ok, _ := c.Get(ctx, k(s)); !ok {
			t.Errorf("%s evicted", s)
		}
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d", c.Len())
	}
}

type failingCache struct {
	*MemoryCache
	getErr, putErr error
}

func (f *failingCache) Get(ctx context.Context, k Key) (Facts, bool, error) {
	if f.getErr != nil {
		return Facts{}, false, f.getErr
	}
	return f.MemoryCache.Get(ctx, k)
}

func (f *failingCache) Put(ctx context.Context, k Key, facts Facts) error {
	if f.putErr != nil {
		return f.putErr
	}
	return f.MemoryCache.Put(ctx, k, facts)
}

func TestCached(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(0)
	key := ContentKey([]byte("x"), "1")
	parses := 0
	parse := func() (Facts, error) { parses++; return Facts{Lang: "go", Package: "p"}, nil }
	for range 3 {
		f, err := Cached(ctx, c, key, parse)
		if err != nil || f.Package != "p" {
			t.Fatalf("Cached = %+v, %v", f, err)
		}
	}
	if parses != 1 {
		t.Errorf("parsed %d times, want once", parses)
	}

	boom := errors.New("boom")
	other := ContentKey([]byte("y"), "1")
	if _, err := Cached(ctx, c, other, func() (Facts, error) { return Facts{}, boom }); !errors.Is(err, boom) {
		t.Errorf("parse error: %v", err)
	}
	if _, ok, _ := c.Get(ctx, other); ok {
		t.Error("a failed parse was cached")
	}
	if _, err := Cached(ctx, c, Key{}, parse); !errors.Is(err, ErrInvalidKey) {
		t.Errorf("invalid key: %v", err)
	}

	fc := &failingCache{MemoryCache: NewMemoryCache(0), getErr: boom}
	if _, err := Cached(ctx, fc, key, parse); !errors.Is(err, boom) {
		t.Errorf("get error: %v", err)
	}
	fc = &failingCache{MemoryCache: NewMemoryCache(0), putErr: boom}
	f, err := Cached(ctx, fc, key, parse)
	if !errors.Is(err, boom) || f.Package != "p" {
		t.Errorf("put error: %+v, %v (want the facts and the error)", f, err)
	}
}

func TestMemoryCacheConcurrent(t *testing.T) {
	ctx := context.Background()
	c := NewMemoryCache(8)
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				key := ContentKey([]byte(fmt.Sprint(i%16)), "1")
				if _, err := Cached(ctx, c, key, func() (Facts, error) {
					return Facts{Lang: "go", Package: fmt.Sprint(g)}, nil
				}); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
	if c.Len() > 8 {
		t.Errorf("Len = %d beyond capacity", c.Len())
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
