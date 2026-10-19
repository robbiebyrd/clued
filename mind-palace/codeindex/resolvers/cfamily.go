package resolvers

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"weak"

	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// The C family: C, C++ and Objective-C. There are no packages: a file sees
// what it defines, what its #include / #import chain declares and, through
// header/source pairing, what the source files of its headers define.
//
//   - Package: the file's base name without its extension, so a header and
//     its sources (list.h, list.c) share a key and Scope can look files up
//     by name (View.Package); the rules below never treat two files of one
//     key as one scope except a header and its paired sources.
//   - Includes (include, import: "x.h"; include_system, import_system:
//     <x.h>): a quoted include is looked up next to the including file
//     first; both kinds then in the include directories that
//     CMakeLists.txt (include_directories, target_include_directories, in
//     the file's directory and every directory above it) and
//     compile_commands.json (-I, -iquote, -isystem, -idirafter; at the root
//     or in build/) name, then at the project root, then by path suffix and
//     finally by base name, nearest the including file first. Includes are
//     followed transitively; their definitions are reachable unqualified
//     (Scope.Open), static ones included (the text is included).
//     include_macro (#include NAME) and Objective-C's @import of a module
//     (module) are never resolved.
//   - Pairing: a header pairs with the source files of its base name in its
//     directory, else with those anywhere; a source with the headers. A
//     file sees its pair's definitions (StepPackage), and a name an
//     included header declares (a prototype, an extern) and its paired
//     source defines resolves to that definition (StepImport), so callers
//     land on the body rather than the prototype.
//   - Declarations: a prototype, an in-class method declaration, an
//     Objective-C @interface method or an extern variable is not a target
//     while a definition of the same qualified name is in its own file or,
//     for an included header, in the header's paired source. A C++ method
//     defined outside its class inside a namespace block (namespace n {
//     double A::f() {} }) is matched to its declaration by the receiver in
//     its signature (n::A::f).
//   - C++: std:: (and a namespace alias of a std namespace) is outside the
//     project, so references through it stay unresolved; a bare name that
//     only the name fallback finds must live in the global namespace, a
//     namespace the file opens (namespace n {}, or an enclosing one) or
//     uses (using namespace n), or be the target of a using declaration
//     (using n::x).
//   - Visibility: a static function or variable (not exported, or a
//     non-member whose signature starts with static) is visible only in its
//     own file and to files that include it.
//
// Objective-C messages ([Shape shapeNamed:], [list insertObject:x
// atIndex:0]) are references named by their full selector (shapeNamed:,
// insertObject:atIndex:), as the methods are, and qualified by the
// receiver, so a class receiver resolves through the qualified name
// (Shape.insertObject:atIndex:) and any other through the member fallback;
// two methods that share a first keyword never stand in for each other.
//
// A qualifier written from the root (::n::f) or a C++ qualified name
// (n::A::f) is matched whole first (AbsoluteQualifiers, exact), and a
// method defined out of line in another file than its class (no parent in
// its file's facts) is a member of the class (ReceiverMembers).
func init() { Register(codeparse.FamilyCFamily, cfamily{}) }

type cfamily struct{}

var cfamilyRules = Rules{Separators: []string{"::", "."}, NameFallback: true, MemberFallback: true, ReceiverMembers: true, AbsoluteQualifiers: true}

// Import kinds of the C family's extractors.
const (
	CKindInclude        = "include"         // #include "x.h"
	CKindIncludeSystem  = "include_system"  // #include <x.h>
	CKindImport         = "import"          // #import "x.h"
	CKindImportSystem   = "import_system"   // #import <x.h>
	CKindUsingNamespace = "using_namespace" // using namespace n;
	CKindUsing          = "using"           // using n::x; (Names: x)
	CKindNamespaceAlias = "namespace_alias" // namespace a = n; (Alias: a)
)

// Files the C family reads to find include directories.
const (
	CMakeListsFile      = "CMakeLists.txt"
	CompileCommandsFile = "compile_commands.json"
)

// cMaxIncludes bounds the files one file's transitive includes reach.
const cMaxIncludes = 512

// cExternalNamespaces are C++ namespaces outside any project.
var cExternalNamespaces = []string{"std"}

// cExternalKey is the package key of an external namespace: no file has
// it (a key is a base name and never holds a slash).
func cExternalKey(ns string) string { return "extern:/" + ns }

// Rules implements Resolver.
func (cfamily) Rules() Rules { return cfamilyRules }

// Package implements Resolver: the base name without its extension.
func (cfamily) Package(_ context.Context, _ View, f *File) (string, error) {
	return CStem(f.Path), nil
}

// CStem is a C-family file's package key: its base name without the
// extension (src/list.h → list).
func CStem(p string) string {
	b := path.Base(p)
	return strings.TrimSuffix(b, path.Ext(b))
}

var cHeaderExts = map[string]bool{".h": true, ".hh": true, ".hpp": true, ".hxx": true, ".h++": true}

// CIsHeader reports whether p names a header (.h, .hh, .hpp, .hxx, .h++).
func CIsHeader(p string) bool { return cHeaderExts[strings.ToLower(path.Ext(p))] }

// cScope is what Scope found for one file, for Visible.
type cScope struct {
	closure    map[string]bool // the files the includes reach
	pair       map[string]bool // the file's paired headers or sources
	importDefs map[string]bool // paired sources whose definitions Names bind
	hidden     map[string]bool // path + "\x00" + id of declarations a reachable pair defines
	namespaces map[string]bool // namespaces the file opens, with their parents
	usingNS    map[string]bool // namespaces of using directives
	usingDecl  map[string]bool // qualified names of using declarations
}

// cState is what the resolver keeps about one *File while the File lives:
// an index of its facts and, for a file whose references are resolved,
// its scope. It holds no pointer to a File (the map key is weak).
type cState struct {
	indexOnce sync.Once
	byName    map[string][]*codeparse.Symbol
	byID      map[string]*codeparse.Symbol
	scope     atomic.Pointer[cScope]
}

var (
	cStatesMu sync.Mutex
	cStates   = map[weak.Pointer[File]]*cState{}
)

// cStateOf returns f's state, created on first use and dropped when f is
// collected.
func cStateOf(f *File) *cState {
	k := weak.Make(f)
	cStatesMu.Lock()
	defer cStatesMu.Unlock()
	if st, ok := cStates[k]; ok {
		return st
	}
	st := &cState{}
	cStates[k] = st
	runtime.AddCleanup(f, func(k weak.Pointer[File]) {
		cStatesMu.Lock()
		delete(cStates, k)
		cStatesMu.Unlock()
	}, k)
	return st
}

// index returns f's symbols by name and by id.
func (st *cState) index(f *File) (map[string][]*codeparse.Symbol, map[string]*codeparse.Symbol) {
	st.indexOnce.Do(func() {
		st.byName, st.byID = map[string][]*codeparse.Symbol{}, map[string]*codeparse.Symbol{}
		if f.Facts == nil {
			return
		}
		for i := range f.Facts.Symbols {
			s := &f.Facts.Symbols[i]
			st.byName[s.Name] = append(st.byName[s.Name], s)
			st.byID[s.ID] = s
		}
	})
	return st.byName, st.byID
}

// Scope implements Resolver: the transitive includes are opened, the
// definitions of their paired sources bound by name, and what Visible needs
// is kept for f.
func (cfamily) Scope(ctx context.Context, v View, f *File) (Scope, error) {
	var s Scope
	sc := &cScope{closure: map[string]bool{}, pair: map[string]bool{}, importDefs: map[string]bool{},
		hidden: map[string]bool{}, namespaces: map[string]bool{}, usingNS: map[string]bool{}, usingDecl: map[string]bool{}}
	defer cStateOf(f).scope.Store(sc)
	if f.Facts == nil {
		return s, nil
	}
	for _, ns := range cExternalNamespaces {
		s.AddQualifier(ns, cExternalKey(ns))
	}
	for _, g := range cPairs(v, f) {
		sc.pair[g.Path] = true
	}
	for _, sym := range f.Facts.Symbols {
		if sym.Kind == codeparse.KindNamespace {
			addNamespace(sc.namespaces, sym.QualName)
		}
	}
	for _, im := range f.Facts.Imports {
		spec := strings.TrimPrefix(strings.TrimSpace(im.Spec), "::")
		switch im.Kind {
		case CKindUsingNamespace:
			sc.usingNS[spec] = true
		case CKindUsing:
			sc.usingDecl[spec] = true
		case CKindNamespaceAlias:
			if im.Alias != "" && slices.Contains(cExternalNamespaces, strings.SplitN(spec, "::", 2)[0]) {
				s.AddQualifier(im.Alias, cExternalKey(spec))
			}
		}
	}

	roots, err := cIncludeRoots(ctx, v, f)
	if err != nil {
		return s, err
	}
	queue := []*File{f}
	for len(queue) > 0 && len(sc.closure) < cMaxIncludes {
		from := queue[0]
		queue = queue[1:]
		for _, im := range from.Facts.Imports {
			var quoted bool
			switch im.Kind {
			case CKindInclude, CKindImport:
				quoted = true
			case CKindIncludeSystem, CKindImportSystem:
			default:
				continue
			}
			for _, p := range cResolveInclude(v, from.Dir(), im.Spec, quoted, roots) {
				if sc.closure[p] || p == f.Path {
					continue
				}
				sc.closure[p] = true
				s.AddOpen(CStem(p))
				if g := cFileAt(v, p); g != nil && g.Facts != nil {
					queue = append(queue, g)
				}
			}
		}
	}

	// A declaration of an included header that its paired source defines:
	// the name is bound to the source, and the declaration hidden.
	for p := range sc.closure {
		h := cFileAt(v, p)
		if h == nil || h.Facts == nil || !CIsHeader(p) {
			continue
		}
		for _, src := range cPairs(v, h) {
			names, _ := cStateOf(src).index(src)
			for i := range h.Facts.Symbols {
				d := &h.Facts.Symbols[i]
				if !cIsDeclaration(d) || !cDefines(names, d) {
					continue
				}
				sc.hidden[h.Path+"\x00"+d.ID] = true
				if d.Parent == "" {
					s.AddName(d.Name, src.Package)
					sc.importDefs[src.Path] = true
				}
			}
		}
	}
	return s, nil
}

// addNamespace records ns and the namespaces enclosing it.
func addNamespace(set map[string]bool, ns string) {
	for ns != "" {
		set[ns] = true
		i := strings.LastIndex(ns, "::")
		if i < 0 {
			return
		}
		ns = ns[:i]
	}
}

// Visible implements Resolver.
func (cfamily) Visible(f *File, c Candidate, step Step) bool {
	same := c.File == f || c.File.Path == f.Path
	if cHidden(f, c) {
		return false
	}
	sc := cStateOf(f).scope.Load()
	switch step {
	case StepScope, StepFile:
		return same
	case StepPackage:
		if sc == nil {
			return c.File.Dir() == f.Dir() && CIsHeader(c.File.Path) != CIsHeader(f.Path) && cExported(c)
		}
		return sc.pair[c.File.Path] && (CIsHeader(c.File.Path) || cExported(c))
	case StepImport:
		return (sc == nil || sc.importDefs[c.File.Path]) && cExported(c)
	case StepOpen:
		return sc == nil || sc.closure[c.File.Path]
	}
	reachable := same || (sc != nil && (sc.closure[c.File.Path] || sc.pair[c.File.Path]))
	if !reachable && !cExported(c) {
		return false
	}
	if step == StepName && sc != nil {
		return cNamespaceVisible(sc, c)
	}
	return true
}

// cHidden reports whether c is a declaration whose definition f reaches:
// in the declaration's own file, or in the paired source of a header f
// includes.
func cHidden(f *File, c Candidate) bool {
	if !cIsDeclaration(c.Symbol) {
		return false
	}
	if sc := cStateOf(f).scope.Load(); sc != nil && sc.hidden[c.File.Path+"\x00"+c.Symbol.ID] {
		return true
	}
	names, _ := cStateOf(c.File).index(c.File)
	return cDefines(names, c.Symbol)
}

// cDefines reports whether the symbols names hold a definition (not a
// declaration) of the same kind of thing and effective qualified name as
// declaration d.
func cDefines(names map[string][]*codeparse.Symbol, d *codeparse.Symbol) bool {
	want := CEffectiveQualName(d)
	for _, s := range names[d.Name] {
		if s != d && !cIsDeclaration(s) && cCallable(s.Kind) == cCallable(d.Kind) && CEffectiveQualName(s) == want {
			return true
		}
	}
	return false
}

func cCallable(kind string) bool {
	return kind == codeparse.KindFunction || kind == codeparse.KindMethod
}

var cExternWords = map[string]bool{"extern": true, "FOUNDATION_EXPORT": true, "FOUNDATION_EXTERN": true,
	"UIKIT_EXTERN": true, "OBJC_EXTERN": true, "APPKIT_EXTERN": true}

// cIsDeclaration reports whether sym declares what another symbol defines:
// a function or method without a body (its signature ends with ";": a
// prototype, an in-class declaration, an @interface method) or an extern
// variable or constant.
func cIsDeclaration(sym *codeparse.Symbol) bool {
	sig := strings.TrimSpace(sym.Signature)
	switch sym.Kind {
	case codeparse.KindFunction, codeparse.KindMethod:
		return strings.HasSuffix(sig, ";")
	case codeparse.KindVar, codeparse.KindConst:
		first, _, _ := strings.Cut(sig, " ")
		return cExternWords[first]
	}
	return false
}

// cExported reports whether c is visible outside its file: exported, and
// not a static non-member.
func cExported(c Candidate) bool {
	if !c.Symbol.Exported {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(c.Symbol.Signature), "static ") {
		return true
	}
	if c.Symbol.Parent == "" {
		return false
	}
	_, byID := cStateOf(c.File).index(c.File)
	p := byID[c.Symbol.Parent]
	return p != nil && p.Kind != codeparse.KindNamespace
}

// cNamespaceVisible reports whether a bare name found by the name
// fallback may be c: its innermost namespace is the global one, one the
// file opens or uses, or c is named by a using declaration.
func cNamespaceVisible(sc *cScope, c Candidate) bool {
	ns := cNamespaceOf(c)
	if ns == "" || sc.namespaces[ns] || sc.usingNS[ns] {
		return true
	}
	return sc.usingDecl[CEffectiveQualName(c.Symbol)] || sc.usingDecl[c.Symbol.QualName]
}

// cNamespaceOf is the qualified name of the innermost namespace enclosing
// c's definition, "" for the global namespace.
func cNamespaceOf(c Candidate) string {
	_, byID := cStateOf(c.File).index(c.File)
	seen := 0
	for id := c.Symbol.Parent; id != "" && seen < 64; seen++ {
		p := byID[id]
		if p == nil {
			return ""
		}
		if p.Kind == codeparse.KindNamespace {
			return p.QualName
		}
		id = p.Parent
	}
	return ""
}

var cTemplateArgs = regexp.MustCompile(`<[^<>]*>`)

// CEffectiveQualName is sym's qualified name with the receiver a C++
// out-of-line definition names in its signature: double Circle::area()
// inside namespace geometry is geometry::Circle::area although the
// extractor qualifies it by position (geometry::area). Other symbols keep
// their QualName.
func CEffectiveQualName(sym *codeparse.Symbol) string {
	q := sym.QualName
	if !cCallable(sym.Kind) || sym.Name == "" {
		return q
	}
	sig := sym.Signature
	i := strings.Index(sig, "::"+sym.Name+"(")
	if i < 0 {
		return q
	}
	j, depth := i, 0
scan:
	for j > 0 {
		ch := sig[j-1]
		switch {
		case ch == '>':
			depth++
		case ch == '<':
			depth--
		case depth > 0, ch == ':', ch == '_', ch >= 'a' && ch <= 'z', ch >= 'A' && ch <= 'Z', ch >= '0' && ch <= '9':
		default:
			break scan
		}
		j--
	}
	chain := sig[j:i]
	for strings.Contains(chain, "<") {
		next := cTemplateArgs.ReplaceAllString(chain, "")
		if next == chain {
			break
		}
		chain = next
	}
	chain = strings.TrimPrefix(chain, "::")
	if chain == "" {
		return q
	}
	want := chain + "::" + sym.Name
	if q == want || strings.HasSuffix(q, "::"+want) {
		return q
	}
	prefix := strings.TrimSuffix(q, sym.Name)
	if prefix == q {
		return q
	}
	// The receiver chain may start with enclosing namespaces (void b::C::f()
	// inside namespace a is a::b::C::f, qualified a::b::f by position).
	parts := strings.Split(strings.TrimSuffix(prefix, "::"), "::")
	for k := range parts {
		if strings.HasPrefix(want, strings.Join(parts[k:], "::")+"::") {
			if k == 0 {
				return want
			}
			return strings.Join(parts[:k], "::") + "::" + want
		}
	}
	return prefix + want
}

// cFileAt returns the family's file at p.
func cFileAt(v View, p string) *File {
	for _, g := range v.Package(codeparse.FamilyCFamily, CStem(p)) {
		if g.Path == p {
			return g
		}
	}
	return nil
}

// cPairs returns f's paired files: for a header the sources of its base
// name, for a source the headers; those in f's directory, else those
// anywhere.
func cPairs(v View, f *File) []*File {
	hdr := CIsHeader(f.Path)
	var same, other []*File
	for _, g := range v.Package(codeparse.FamilyCFamily, CStem(f.Path)) {
		if g.Path == f.Path || CIsHeader(g.Path) == hdr {
			continue
		}
		if g.Dir() == f.Dir() {
			same = append(same, g)
		} else {
			other = append(other, g)
		}
	}
	if len(same) > 0 {
		return same
	}
	return other
}

// cLocal joins dir and rel and reports whether the result stays inside
// the project.
func cLocal(dir, rel string) (string, bool) {
	if rel == "" || path.IsAbs(rel) {
		return "", false
	}
	p := path.Join(dir, rel)
	if p == ".." || strings.HasPrefix(p, "../") || path.IsAbs(p) {
		return "", false
	}
	return p, true
}

// cResolveInclude returns the files an include of spec from a file in dir
// names: next to the file (quoted only), in the include roots, then by
// path suffix and by base name, nearest dir first (several when equally
// near).
func cResolveInclude(v View, dir, spec string, quoted bool, roots []string) []string {
	spec = strings.TrimSpace(spec)
	if quoted {
		if p, ok := cLocal(dir, spec); ok && v.Has(p) {
			return []string{p}
		}
	}
	for _, r := range roots {
		if p, ok := cLocal(r, spec); ok && v.Has(p) {
			return []string{p}
		}
	}
	clean := path.Clean(spec)
	if clean == "." || path.IsAbs(clean) || strings.HasPrefix(clean, "../") {
		clean = path.Base(clean)
	}
	var suffix, base []string
	for _, g := range v.Package(codeparse.FamilyCFamily, CStem(clean)) {
		switch {
		case g.Path == clean || strings.HasSuffix(g.Path, "/"+clean):
			suffix = append(suffix, g.Path)
		case path.Base(g.Path) == path.Base(clean):
			base = append(base, g.Path)
		}
	}
	if len(suffix) == 0 {
		suffix = base
	}
	return cNearest(dir, suffix)
}

// cNearest keeps the paths whose directory shares the most leading
// elements with dir.
func cNearest(dir string, paths []string) []string {
	if len(paths) < 2 {
		return paths
	}
	score := func(p string) int {
		a, b := strings.Split(dir, "/"), strings.Split(path.Dir(p), "/")
		n := 0
		for n < len(a) && n < len(b) && a[n] == b[n] {
			n++
		}
		return n
	}
	best, out := -1, []string(nil)
	for _, p := range paths {
		switch s := score(p); {
		case s > best:
			best, out = s, []string{p}
		case s == best:
			out = append(out, p)
		}
	}
	return out
}

// cIncludeRoots returns the include directories for f: those of the
// CMakeLists.txt files in f's directory and above (nearest first), those
// of compile_commands.json at the root or in build/, then the root.
func cIncludeRoots(ctx context.Context, v View, f *File) ([]string, error) {
	var roots []string
	add := func(dirs []string) {
		for _, d := range dirs {
			roots = appendNew(roots, d)
		}
	}
	for d := f.Dir(); ; d = path.Dir(d) {
		cm := path.Join(d, CMakeListsFile)
		if v.Has(cm) {
			content, ok, err := v.Content(ctx, cm)
			if err != nil {
				return nil, err
			}
			if ok {
				add(cBuildCached(cm, content, func() any { return CMakeIncludeDirs(content, d) }).([]string))
			}
		}
		if d == "." || d == "/" || d == "" {
			break
		}
	}
	for _, cc := range []string{CompileCommandsFile, "build/" + CompileCommandsFile} {
		if !v.Has(cc) {
			continue
		}
		content, ok, err := v.Content(ctx, cc)
		if err != nil {
			return nil, err
		}
		if ok {
			db := cBuildCached(cc, content, func() any { return parseCompileCommands(content) }).(*compileCommands)
			add(db.dirs(v))
		}
	}
	add([]string{"."})
	return roots, nil
}

// cBuildCache keeps the parsed build files by path, checked against their
// content (the engine hands out one slice per blob and run, so the check
// is a pointer compare after the first).
var (
	cBuildMu    sync.Mutex
	cBuildCache = map[string]*cBuildEntry{}
)

const cBuildCacheSize = 256

type cBuildEntry struct {
	content []byte
	parsed  any
}

func cBuildCached(p string, content []byte, parse func() any) any {
	cBuildMu.Lock()
	e := cBuildCache[p]
	if e != nil && bytes.Equal(e.content, content) {
		e.content = content
		cBuildMu.Unlock()
		return e.parsed
	}
	cBuildMu.Unlock()
	parsed := parse()
	cBuildMu.Lock()
	defer cBuildMu.Unlock()
	if len(cBuildCache) >= cBuildCacheSize {
		clear(cBuildCache)
	}
	cBuildCache[p] = &cBuildEntry{content: content, parsed: parsed}
	return parsed
}

var (
	cmakeComment  = regexp.MustCompile(`(?m)#.*$`)
	cmakeIncludes = regexp.MustCompile(`(?is)\b(target_include_directories|include_directories)\s*\(([^)]*)\)`)
	cmakeGenex    = regexp.MustCompile(`^\$<(?:BUILD_INTERFACE|BUILD_LOCAL_INTERFACE):(.*)>$`)
)

var cmakeKeywords = map[string]bool{"SYSTEM": true, "BEFORE": true, "AFTER": true, "PUBLIC": true, "PRIVATE": true, "INTERFACE": true}

// CMakeIncludeDirs returns the project-relative include directories a
// CMakeLists.txt in dir names with include_directories and
// target_include_directories. ${CMAKE_CURRENT_SOURCE_DIR} and
// ${CMAKE_CURRENT_LIST_DIR} are dir, ${PROJECT_SOURCE_DIR} and
// ${CMAKE_SOURCE_DIR} the project root; a directory with another variable,
// a generator expression other than $<BUILD_INTERFACE:…>, an absolute path
// or a path leaving the project is skipped.
func CMakeIncludeDirs(content []byte, dir string) []string {
	text := cmakeComment.ReplaceAllString(string(content), "")
	var out []string
	for _, m := range cmakeIncludes.FindAllStringSubmatch(text, -1) {
		args := strings.Fields(m[2])
		if strings.EqualFold(m[1], "target_include_directories") && len(args) > 0 {
			args = args[1:]
		}
		for _, a := range args {
			a = strings.Trim(a, `"`)
			if cmakeKeywords[a] {
				continue
			}
			if g := cmakeGenex.FindStringSubmatch(a); g != nil {
				a = g[1]
			}
			base := dir
			for _, v := range []string{"${CMAKE_CURRENT_SOURCE_DIR}", "${CMAKE_CURRENT_LIST_DIR}"} {
				if rest, ok := strings.CutPrefix(a, v); ok {
					a = strings.TrimPrefix(rest, "/")
				}
			}
			for _, v := range []string{"${PROJECT_SOURCE_DIR}", "${CMAKE_SOURCE_DIR}"} {
				if rest, ok := strings.CutPrefix(a, v); ok {
					a, base = strings.TrimPrefix(rest, "/"), "."
				}
			}
			if strings.ContainsAny(a, "$<>") {
				continue
			}
			if a == "" {
				a = "."
			}
			if p, ok := cLocal(base, a); ok {
				out = appendNew(out, p)
			}
		}
	}
	return out
}

// compileCommands is what the resolver keeps of a compile_commands.json:
// the absolute include directories and source files it names.
type compileCommands struct {
	includes []string
	files    []string
}

var cIncludeFlags = []string{"-I", "-iquote", "-isystem", "-idirafter", "/I"}

func parseCompileCommands(content []byte) *compileCommands {
	var entries []struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Command   string   `json:"command"`
		Arguments []string `json:"arguments"`
	}
	db := &compileCommands{}
	if err := json.Unmarshal(content, &entries); err != nil {
		return db
	}
	abs := func(dir, p string) string {
		p = strings.Trim(p, `"'`)
		if path.IsAbs(p) {
			return path.Clean(p)
		}
		return path.Join(dir, p)
	}
	for _, e := range entries {
		if e.File != "" {
			db.files = append(db.files, abs(e.Directory, e.File))
		}
		args := e.Arguments
		if len(args) == 0 {
			args = strings.Fields(e.Command)
		}
		for i := 0; i < len(args); i++ {
			for _, flag := range cIncludeFlags {
				a := args[i]
				if !strings.HasPrefix(a, flag) {
					continue
				}
				val := a[len(flag):]
				if val == "" && i+1 < len(args) {
					i++
					val = args[i]
				}
				if val != "" {
					db.includes = appendNew(db.includes, abs(e.Directory, val))
				}
				break
			}
		}
	}
	return db
}

// dirs maps the include directories to project paths: the project root is
// the prefix of a source file's absolute path whose rest is a file of the
// view.
func (db *compileCommands) dirs(v View) []string {
	root := ""
	for i, file := range db.files {
		if i >= 32 || root != "" {
			break
		}
		parts := strings.Split(strings.TrimPrefix(file, "/"), "/")
		for k := 1; k < len(parts); k++ {
			if v.Has(strings.Join(parts[k:], "/")) {
				root = "/" + strings.Join(parts[:k], "/")
				break
			}
		}
	}
	if root == "" {
		return nil
	}
	var out []string
	for _, d := range db.includes {
		switch {
		case d == root:
			out = appendNew(out, ".")
		case strings.HasPrefix(d, root+"/"):
			if p, ok := cLocal(".", strings.TrimPrefix(d, root+"/")); ok {
				out = appendNew(out, p)
			}
		}
	}
	return out
}
