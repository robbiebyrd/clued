package codeparse

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Language is one supported language as the plan's Languages table lists it.
// The table is the single source of detection; each language's profile.yaml
// repeats its family and extensions and LoadProfiles refuses a profile that
// disagrees, so the two cannot drift apart.
type Language struct {
	// ID is the language's directory under queries/ and Facts.Lang.
	ID string
	// Name is the human name.
	Name string
	// Family is the resolution family whose resolver links its references.
	Family string
	// Extensions are the file suffixes, lowercase with the leading dot,
	// matched longest first (.d.ts before .ts).
	Extensions []string
	// Filenames are whole base names detected regardless of extension.
	Filenames []string
	// Dialects maps an extension to the dialect it selects; extensions not
	// listed have no dialect.
	Dialects map[string]string
}

// Resolution families: one resolver each in codeindex/resolvers.
const (
	FamilyGo      = "go"
	FamilyPython  = "python"
	FamilyJSTS    = "jsts"
	FamilyJVM     = "jvm"
	FamilyCSharp  = "csharp"
	FamilyCFamily = "cfamily"
	FamilySwift   = "swift"
	FamilyRust    = "rust"
	FamilyDart    = "dart"
	FamilyPHP     = "php"
	FamilyRuby    = "ruby"
	FamilyPerl    = "perl"
	FamilyLua     = "lua"
	FamilyOCaml   = "ocaml"
	FamilyLisp    = "lisp"
	FamilySQL     = "sql"
)

// Language IDs of the languages detected by content or shared extensions.
const (
	LangC    = "c"
	LangCPP  = "cpp"
	LangObjC = "objc"
)

var languages = []Language{
	{ID: "go", Name: "Go", Family: FamilyGo, Extensions: []string{".go"}},
	{ID: "python", Name: "Python", Family: FamilyPython, Extensions: []string{".py", ".pyi"}},
	{ID: "javascript", Name: "JavaScript", Family: FamilyJSTS, Extensions: []string{".js", ".mjs", ".cjs", ".jsx"}},
	{ID: "typescript", Name: "TypeScript", Family: FamilyJSTS,
		Extensions: []string{".ts", ".tsx", ".mts", ".cts", ".d.ts"},
		Dialects:   map[string]string{".tsx": "tsx"}},
	{ID: "java", Name: "Java", Family: FamilyJVM, Extensions: []string{".java"}},
	{ID: "kotlin", Name: "Kotlin", Family: FamilyJVM, Extensions: []string{".kt", ".kts"}},
	{ID: "csharp", Name: "C#", Family: FamilyCSharp, Extensions: []string{".cs"}},
	{ID: "rust", Name: "Rust", Family: FamilyRust, Extensions: []string{".rs"}},
	{ID: LangC, Name: "C", Family: FamilyCFamily, Extensions: []string{".c", ".h"}},
	{ID: LangCPP, Name: "C++", Family: FamilyCFamily, Extensions: []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".h"}},
	{ID: LangObjC, Name: "Objective-C", Family: FamilyCFamily, Extensions: []string{".m", ".mm", ".h"}},
	{ID: "swift", Name: "Swift", Family: FamilySwift, Extensions: []string{".swift"}},
	{ID: "dart", Name: "Dart", Family: FamilyDart, Extensions: []string{".dart"}},
	{ID: "php", Name: "PHP", Family: FamilyPHP, Extensions: []string{".php"}},
	{ID: "ruby", Name: "Ruby", Family: FamilyRuby, Extensions: []string{".rb", ".rake"}, Filenames: []string{"Gemfile"}},
	{ID: "sql", Name: "SQL", Family: FamilySQL, Extensions: []string{".sql"}},
	{ID: "perl", Name: "Perl", Family: FamilyPerl, Extensions: []string{".pl", ".pm", ".t"}},
	{ID: "ocaml", Name: "OCaml", Family: FamilyOCaml, Extensions: []string{".ml", ".mli"},
		Dialects: map[string]string{".mli": "interface"}},
	{ID: "lua", Name: "Lua", Family: FamilyLua, Extensions: []string{".lua"}},
	{ID: "lisp", Name: "Lisp", Family: FamilyLisp,
		Extensions: []string{".lisp", ".lsp", ".cl", ".asd", ".el", ".clj", ".cljs", ".cljc", ".edn", ".scm", ".ss", ".rkt"},
		Dialects: map[string]string{
			".lisp": "commonlisp", ".lsp": "commonlisp", ".cl": "commonlisp", ".asd": "commonlisp",
			".el":  "elisp",
			".clj": "clojure", ".cljs": "clojure", ".cljc": "clojure", ".edn": "clojure",
			".scm": "scheme", ".ss": "scheme",
			".rkt": "racket",
		}},
}

// markdownExtensions are chunked by heading (Lang Markdown).
var markdownExtensions = []string{".md", ".markdown", ".mdx"}

var (
	byExtension = map[string][]string{} // extension → language IDs (several for .h)
	byFilename  = map[string]string{}
	byID        = map[string]*Language{}
	families    = map[string]bool{}
)

func init() {
	for i := range languages {
		l := &languages[i]
		byID[l.ID] = l
		families[l.Family] = true
		for _, ext := range l.Extensions {
			byExtension[ext] = append(byExtension[ext], l.ID)
		}
		for _, name := range l.Filenames {
			byFilename[name] = l.ID
		}
	}
	for _, ext := range markdownExtensions {
		byExtension[ext] = []string{Markdown}
	}
}

// Languages returns the supported languages, sorted by ID.
func Languages() []Language {
	out := make([]Language, len(languages))
	copy(out, languages)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// LookupLanguage returns the language with this ID.
func LookupLanguage(id string) (Language, bool) {
	l, ok := byID[id]
	if !ok {
		return Language{}, false
	}
	return *l, true
}

// Families returns the resolution families, sorted.
func Families() []string {
	out := make([]string, 0, len(families))
	for f := range families {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// How a Detection was decided.
const (
	ByExtension  = "extension"  // the extension names one language
	ByFilename   = "filename"   // the base name is a known file (Gemfile)
	ByContent    = "content"    // an ambiguous extension, decided by the file's own text
	ByNeighbours = "neighbours" // an ambiguous extension, decided by the other files in its directory
	ByDefault    = "default"    // an ambiguous extension with no evidence
)

// Detection is the language a file is parsed as.
type Detection struct {
	// Lang is a language ID, Markdown, or "" for a file indexed as text.
	Lang string
	// Dialect selects a grammar within Lang ("" for its default).
	Dialect string
	// By says how Lang was decided (ByExtension, ...); "" when Lang is "".
	By string
}

// Detect decides the language of the file at path (slash-separated, as the
// code store records it) from its name, then, for an extension several
// languages share (.h), from its content and the base names of the other
// files in its directory (neighbours; nil when unknown). The result depends
// only on its arguments.
//
// .h is Objective-C when its text has an Objective-C form (@interface,
// @protocol, @implementation, @class, @property, @import, #import), C++ when
// it has a C++ one (class, template, namespace, using namespace, an access
// specifier, std::), and otherwise follows its neighbours: Objective-C next
// to .m or .mm files, C++ next to C++ sources or headers, C next to .c
// files, and C when nothing says otherwise. Comments and string literals are
// ignored. .m is always Objective-C (MATLAB is not supported).
func Detect(filePath string, content []byte, neighbours []string) Detection {
	base := path.Base(filePath)
	if id, ok := byFilename[base]; ok {
		return Detection{Lang: id, By: ByFilename}
	}
	ext, ids := matchExtension(base)
	switch len(ids) {
	case 0:
		return Detection{}
	case 1:
		d := Detection{Lang: ids[0], By: ByExtension}
		if l := byID[d.Lang]; l != nil {
			d.Dialect = l.Dialects[ext]
		}
		return d
	}
	// Only .h is shared today; any other shared extension would need its own
	// rule, so it falls to the first language listed for it.
	if ext != ".h" {
		return Detection{Lang: ids[0], By: ByDefault}
	}
	if lang := headerByContent(content); lang != "" {
		return Detection{Lang: lang, By: ByContent}
	}
	if lang := headerByNeighbours(base, neighbours); lang != "" {
		return Detection{Lang: lang, By: ByNeighbours}
	}
	return Detection{Lang: LangC, By: ByDefault}
}

// matchExtension returns the longest known suffix of base that starts at a
// dot and the languages it maps to. The first character is skipped so a dot
// file (.eslintrc) has no extension of its own.
func matchExtension(base string) (string, []string) {
	lower := strings.ToLower(base)
	for i := 1; i < len(lower); i++ {
		if lower[i] != '.' {
			continue
		}
		if ids, ok := byExtension[lower[i:]]; ok {
			return lower[i:], ids
		}
	}
	return "", nil
}

// headerScanLimit bounds how much of a header the content rule reads.
const headerScanLimit = 256 << 10

var (
	objcHeader = regexp.MustCompile(`(?m)^[ \t]*(@interface\b|@protocol\b|@implementation\b|@class\b|@property\b|@import\b|#[ \t]*import\b)`)
	cppHeader  = regexp.MustCompile(`(?m)(^[ \t]*(template[ \t]*<|namespace\b[ \t\w:]*\{|using[ \t]+namespace\b|(class|struct)[ \t]+\w+[ \t]*(final[ \t]*)?:[^:]|class[ \t]+\w+[ \t]*(final[ \t]*)?[{;]|(public|private|protected)[ \t]*:[^:])|\bstd::)`)
)

func headerByContent(content []byte) string {
	if len(content) > headerScanLimit {
		content = content[:headerScanLimit]
	}
	code := stripCommentsAndStrings(content)
	switch {
	case objcHeader.Match(code):
		return LangObjC
	case cppHeader.Match(code):
		return LangCPP
	}
	return ""
}

var (
	objcSources = map[string]bool{".m": true, ".mm": true}
	cppSources  = map[string]bool{".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".hh": true}
)

func headerByNeighbours(self string, neighbours []string) string {
	var objc, cpp, c bool
	for _, n := range neighbours {
		n = path.Base(n)
		if n == self {
			continue
		}
		ext := strings.ToLower(path.Ext(n))
		switch {
		case objcSources[ext]:
			objc = true
		case cppSources[ext]:
			cpp = true
		case ext == ".c":
			c = true
		}
	}
	switch {
	case objc:
		return LangObjC
	case cpp:
		return LangCPP
	case c:
		return LangC
	}
	return ""
}

// stripCommentsAndStrings blanks C-family comments and string and character
// literals, keeping newlines so line anchors still match.
func stripCommentsAndStrings(src []byte) []byte {
	out := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		ch := src[i]
		switch {
		case ch == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			if i < len(src) {
				out = append(out, '\n')
			}
		case ch == '/' && i+1 < len(src) && src[i+1] == '*':
			i += 2
			for i < len(src) && (src[i] != '*' || i+1 >= len(src) || src[i+1] != '/') {
				if src[i] == '\n' {
					out = append(out, '\n')
				}
				i++
			}
			i++ // the closing slash
			out = append(out, ' ')
		case ch == '"' || ch == '\'':
			quote := ch
			i++
			for i < len(src) && src[i] != quote && src[i] != '\n' {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			out = append(out, ' ')
		default:
			out = append(out, ch)
		}
	}
	return out
}
