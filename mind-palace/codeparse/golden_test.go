package codeparse

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

// The golden harness: every directory testdata/<lang> holds inputs of that
// language and, next to each input x, the expected facts x.facts.json.
// TestGolden/<lang>/<input> parses the input with the engine over the
// embedded queries and compares; `go test ./codeparse -run TestGolden
// -update` rewrites the goldens. An input named x.in is parsed as x (for
// inputs a repository tool must not see, such as a Go file with a syntax
// error that gofmt would report).

var update = flag.Bool("update", false, "rewrite testdata/<lang>/*.facts.json from the parser's output")

const goldenSuffix = ".facts.json"

func engineForTest(t *testing.T) *Engine {
	t.Helper()
	e, err := Default()
	if err != nil {
		t.Fatalf("Default: %v", err)
	}
	return e
}

func TestGolden(t *testing.T) {
	e := engineForTest(t)
	if err := e.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	dirs, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	tested := map[string]bool{}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		lang := dir.Name()
		tested[lang] = true
		t.Run(lang, func(t *testing.T) { goldenLanguage(t, e, lang) })
	}
	for lang := range e.Profiles() {
		if !tested[lang] {
			t.Errorf("language %s has a profile but no testdata/%s/ goldens", lang, lang)
		}
	}
}

func goldenLanguage(t *testing.T, e *Engine, lang string) {
	dir := path.Join("testdata", lang)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string]bool{}
	withSymbols := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || strings.HasSuffix(name, goldenSuffix) {
			continue
		}
		inputs[name] = true
		t.Run(name, func(t *testing.T) {
			content, err := os.ReadFile(path.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			facts, err := e.Parse(path.Join(dir, strings.TrimSuffix(name, ".in")), content)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if facts.Lang != lang {
				t.Fatalf("parsed as %q, want %q (the directory)", facts.Lang, lang)
			}
			if len(facts.Symbols) > 0 {
				withSymbols++
			}
			checkChunks(t, content, facts)
			got, err := json.MarshalIndent(facts, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			golden := path.Join(dir, name+goldenSuffix)
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("facts differ from %s (run with -update to accept them):\n%s", golden, firstDiff(want, got))
			}
		})
	}
	if len(inputs) == 0 {
		t.Fatalf("%s holds no inputs", dir)
	}
	for _, entry := range entries {
		if input, ok := strings.CutSuffix(entry.Name(), goldenSuffix); ok && !inputs[input] {
			t.Errorf("%s has no input %s", entry.Name(), input)
		}
	}
	if withSymbols == 0 && !t.Failed() {
		t.Errorf("no input of %s gave a symbol", lang)
	}
}

// checkChunks checks the chunk invariants on a parsed file: within the
// size limit, every part after the first repeating its symbol's signature,
// the parts of a symbol contiguous, and every line covered but for gaps
// below minGapBytes.
func checkChunks(t *testing.T, content []byte, f Facts) {
	t.Helper()
	sigs := map[string]string{}
	for _, s := range f.Symbols {
		sigs[s.ID] = s.Signature
	}
	lines := strings.Split(string(content), "\n")
	covered := make([]bool, len(lines)+1)
	prevEnd := map[string]int{}
	for _, c := range f.Chunks {
		if n := (len(c.Text) + 3) / 4; n > MaxChunkTokens {
			t.Errorf("chunk %s: about %d tokens, over %d", c.ID, n, MaxChunkTokens)
		}
		body := string(content[c.Start.Offset:c.End.Offset])
		switch {
		case c.Kind == ChunkPart && strings.HasSuffix(c.ID, "#1"), c.Kind != ChunkPart:
			if c.Text != body && c.Text != body+"\n" {
				t.Errorf("chunk %s: text is not the span it covers", c.ID)
			}
		default:
			if c.Text != sigs[c.SymbolID]+"\n"+body {
				t.Errorf("chunk %s: part does not repeat the signature %q", c.ID, sigs[c.SymbolID])
			}
		}
		if c.Kind == ChunkPart {
			if end, ok := prevEnd[c.SymbolID]; ok && c.Start.Line != end+1 {
				t.Errorf("chunk %s starts on line %d, its previous part ended on %d", c.ID, c.Start.Line, end)
			}
			prevEnd[c.SymbolID] = c.End.Line
		}
		for l := c.Start.Line; l <= c.End.Line; l++ {
			covered[l] = true
		}
	}
	if f.ParseError != "" {
		return
	}
	// Runs of uncovered lines are allowed only below minGapBytes of text.
	var run []string
	flush := func(endLine int) {
		if text := strings.TrimSpace(strings.Join(run, "\n")); len(text) >= minGapBytes {
			t.Errorf("lines %d-%d are in no chunk: %q", endLine-len(run)+1, endLine, text)
		}
		run = nil
	}
	for i, line := range lines {
		if covered[i+1] {
			flush(i)
			continue
		}
		run = append(run, line)
	}
	flush(len(lines))
}

func firstDiff(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\n  want %s\n  got  %s", i+1, a, b)
		}
	}
	return "(no line differs)"
}

// testEngine is an engine over a Go-only queries directory with this query
// and extra profile lines.
func testEngine(t *testing.T, query, profileExtra string, opts Options) *Engine {
	t.Helper()
	fsys := fstest.MapFS{
		"go/profile.yaml": {Data: []byte("family: go\nextensions: [.go]\n" + profileExtra)},
		"go/tags.scm":     {Data: []byte(query)},
	}
	e, err := NewEngine(fsys, opts)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestParseText(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 130; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	f, err := Parse("notes/todo.txt", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if f.Lang != "" || f.ParseError != "" || len(f.Symbols) > 0 {
		t.Fatalf("text facts: %+v", f)
	}
	var starts []int
	for _, c := range f.Chunks {
		if c.Kind != ChunkWindow || c.SymbolID != "" {
			t.Errorf("chunk %+v", c)
		}
		starts = append(starts, c.Start.Line)
	}
	if fmt.Sprint(starts) != "[1 51 101]" || f.Chunks[0].End.Line != 60 || f.Chunks[2].End.Line != 130 {
		t.Errorf("windows start on %v, first ends on %d", starts, f.Chunks[0].End.Line)
	}
	if f.Chunks[0].ID != "L1#1" || f.Chunks[1].ID != "L51#1" {
		t.Errorf("ids %s %s", f.Chunks[0].ID, f.Chunks[1].ID)
	}
	if c := TextWindows([]byte("a\x00b")); c != nil {
		t.Errorf("binary file chunked: %v", c)
	}
	if c := TextWindows(nil); c != nil {
		t.Errorf("empty file chunked: %v", c)
	}
	if n := len(TextWindows([]byte(strings.Repeat("x\n", 60000)))); n != MaxChunks {
		t.Errorf("%d windows, want the cap %d", n, MaxChunks)
	}
	if v := ParseVersion("a.txt", nil); v != TextFactsVersion("") {
		t.Errorf("text version %q", v)
	}
}

func TestParseMarkdown(t *testing.T) {
	big := strings.Repeat("A paragraph of prose that goes on.\nAnd on.\n\n", 400)
	src := "Intro before any heading.\n\n# Title\n\nText.\n\n```sh\n# not a heading\n```\n\n## Usage\nRun it.\n\nSetext\n======\n\nBody.\n\n### Big\n" + big
	f, err := Parse("docs/README.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if f.Lang != Markdown || len(f.Symbols) > 0 || f.ParseError != "" {
		t.Fatalf("markdown facts: %+v", f)
	}
	var heads []string
	for _, c := range f.Chunks {
		if c.Kind != ChunkSection {
			t.Errorf("chunk %s kind %s", c.ID, c.Kind)
		}
		heads = append(heads, strings.SplitN(c.Text, "\n", 2)[0])
		if n := (len(c.Text) + 3) / 4; n > MaxChunkTokens {
			t.Errorf("chunk %s: %d tokens", c.ID, n)
		}
	}
	if len(heads) < 6 || strings.Join(heads[:5], "|") != "Intro before any heading.|# Title|## Usage|Setext|### Big" {
		t.Fatalf("sections start with %q", heads)
	}
	for _, h := range heads[5:] {
		if h != "### Big" {
			t.Errorf("a later part of the big section starts with %q, not its heading", h)
		}
	}
	if !strings.Contains(f.Chunks[1].Text, "# not a heading") {
		t.Errorf("fenced code split the Title section: %q", f.Chunks[1].Text)
	}
	if f.Chunks[4].ID != "L19#1" || f.Chunks[5].ID != "L19#2" {
		t.Errorf("big section ids %s %s", f.Chunks[4].ID, f.Chunks[5].ID)
	}
	if v := ParseVersion("a.md", nil); v != TextFactsVersion(Markdown) {
		t.Errorf("markdown version %q", v)
	}
}

func TestParseGoVersion(t *testing.T) {
	e := engineForTest(t)
	want := e.Profiles()["go"].FactsVersion("")
	if v := ParseVersion("x/y.go", nil); v != want || !strings.HasPrefix(v, Version+"/go@") {
		t.Errorf("go version %q, want %q", v, want)
	}
}

func TestParseNoGrammar(t *testing.T) {
	e := testEngine(t, "", "grammar: no-such-grammar\n", Options{})
	f, err := e.Parse("a.go", []byte("package a\n\nfunc F() {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Lang != "go" || f.ParseError != ParseNoGrammar || len(f.Symbols) > 0 || len(f.Chunks) != 1 {
		t.Errorf("facts %+v", f)
	}
	if v := e.Version("a.go", nil); v != TextFactsVersion("go") {
		t.Errorf("version %q", v)
	}
	if err := e.Check(); err == nil || !strings.Contains(err.Error(), "no-such-grammar") {
		t.Errorf("Check: %v", err)
	}
}

func TestBadQueries(t *testing.T) {
	for name, query := range map[string]string{
		"syntax":            "(function_declaration name: (identifier) @name",
		"unknown capture":   "(function_declaration name: (identifier) @title) @definition.function",
		"unknown reference": "(call_expression function: (identifier) @name) @reference.nonsense",
		"unknown node":      "(no_such_node) @name",
	} {
		e := testEngine(t, query, "", Options{})
		if err := e.Check(); err == nil {
			t.Errorf("%s: Check passed", name)
		}
		if _, err := e.Parse("a.go", []byte("package a\n")); err == nil {
			t.Errorf("%s: Parse passed", name)
		}
	}
}

func TestParseTimeout(t *testing.T) {
	var b strings.Builder
	b.WriteString("package big\n\nfunc F() {\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&b, "\tx%d := g(%d, h(%d))\n", i, i, i)
	}
	b.WriteString("}\n")
	e := testEngine(t, "(function_declaration name: (identifier) @name) @definition.function", "", Options{Timeout: time.Microsecond})
	f, err := e.Parse("big.go", []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if f.ParseError != ParseTimeout || len(f.Symbols) > 0 || len(f.Chunks) == 0 || f.Chunks[0].Kind != ChunkWindow {
		t.Errorf("timed-out facts: parse error %q, %d symbols, %d chunks", f.ParseError, len(f.Symbols), len(f.Chunks))
	}
}

// TestCaptures exercises the captures the Go query does not use, with the
// Go grammar: @definition.form, @doc, @signature, @exported, @import.name,
// import.kind and a profile's reference kinds.
func TestCaptures(t *testing.T) {
	query := `
(package_clause (package_identifier) @package)
(import_spec path: (_) @import.spec (#set! import.kind "use")) @import
(import_spec name: (_) @import.name path: (_) @import.spec) @import
(call_expression
  function: (identifier) @form
  arguments: (argument_list . (interpreted_string_literal (interpreted_string_literal_content) @name))) @definition.form
(function_declaration
  name: (identifier) @name
  parameters: (_) @signature
  body: (block (statement_list . (expression_statement (call_expression (argument_list (raw_string_literal) @doc))))))
  @definition.function
(function_declaration
  name: (identifier) @name @exported
  (#match? @name "^Pub")) @definition.function
(call_expression function: (selector_expression operand: (_) @qualifier field: (_) @name)) @reference.send
`
	e := testEngine(t, query, "kinds:\n  reference.send: value\ndefiningForms:\n  - { form: define, kind: macro }\nscope:\n  exported: declared\n", Options{})
	src := "package p\n\nimport (\n\tx \"a/b\"\n)\n\nvar _ = define(\"thing\")\nvar _ = other(\"skip\")\n\nfunc f(a int) {\n\tdoc(`Docstring\nhere.`)\n\tz.Do()\n}\n\nfunc Pubg() {}\n"
	f, err := e.Parse("p.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if f.Package != "p" || len(f.Imports) != 1 {
		t.Fatalf("package %q, imports %+v", f.Package, f.Imports)
	}
	if imp := f.Imports[0]; imp.Spec != "a/b" || imp.Kind != "use" || fmt.Sprint(imp.Names) != "[x]" {
		t.Errorf("import %+v", imp)
	}
	byName := map[string]Symbol{}
	for _, s := range f.Symbols {
		byName[s.Name] = s
	}
	if s := byName["thing"]; s.Kind != KindMacro || s.QualName != "p.thing" {
		t.Errorf("defining form: %+v", s)
	}
	if _, ok := byName["skip"]; ok {
		t.Errorf("a form not listed defined a symbol")
	}
	if s := byName["f"]; s.Signature != "(a int)" || s.Doc != "Docstring\nhere." || s.Exported {
		t.Errorf("f: %+v", s)
	}
	if s := byName["Pubg"]; !s.Exported {
		t.Errorf("Pubg not exported: %+v", s)
	}
	var refs []string
	for _, r := range f.Refs {
		refs = append(refs, r.Kind+":"+r.Qualifier+"."+r.Name+"@"+r.Enclosing)
	}
	if got := strings.Join(refs, " "); got != "call:.other@ value:z.Do@p.f" {
		t.Errorf("refs %s", got)
	}
}

func TestParseConcurrent(t *testing.T) {
	src, err := os.ReadFile("testdata/go/server.go")
	if err != nil {
		t.Fatal(err)
	}
	want, err := Parse("server.go", src)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, err := Parse("server.go", src)
			if err != nil {
				errs <- err
				return
			}
			if got, _ := json.Marshal(f); !bytes.Equal(got, wantJSON) {
				errs <- fmt.Errorf("concurrent parse gave different facts")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestSplitSpan(t *testing.T) {
	long := strings.Repeat("x", maxChunkBytes*2+10) + "\n" + strings.Repeat("é", maxChunkBytes) + "\nend"
	lines := newLineIndex([]byte(long))
	parts := splitSpan(lines, 0, len(long), nil, "SIG\n")
	var joined strings.Builder
	for i, p := range parts {
		if len(p.text) > maxChunkBytes {
			t.Errorf("part %d: %d bytes", i, len(p.text))
		}
		if i > 0 && !strings.HasPrefix(p.text, "SIG\n") {
			t.Errorf("part %d does not start with the prefix", i)
		}
		joined.WriteString(long[p.start:p.end])
		if p.end < len(long) && long[p.end] == '\n' {
			joined.WriteByte('\n')
		}
		if !strings.HasPrefix(long[p.start:], strings.TrimPrefix(p.text, map[bool]string{true: "SIG\n"}[i > 0])) {
			t.Errorf("part %d text is not its span", i)
		}
	}
	if joined.String() != long {
		t.Errorf("parts do not cover the span")
	}
}
