package codeparse

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	ts "github.com/odvcencio/gotreesitter"

	"github.com/robbiebyrd/clued/mind-palace/codeparse/grammars"
)

// The extractor runs a language's query file (queries/<lang>/tags.scm,
// tree-sitter's tags-query convention plus clued's captures) over the
// file's tree and turns the matches into Facts. Every language goes
// through the same code; what differs is its grammar (registered in
// grammars/<lang>.go), its query file and its profile.
//
// Captures, per match (see queries/README.md):
//
//	@definition.<kind>  the whole definition; <kind> through the profile's
//	                    kinds, then DefaultKinds, else the word itself
//	@definition.form    a definition written as a call (defun, attr_reader):
//	                    the kind is the profile's definingForms entry for
//	                    @form, and a form not listed is a call reference
//	@reference.<kind>   a reference; call, type (class, interface, struct,
//	                    enum, trait), extends (superclass, mixin, inherits),
//	                    implements (implementation, interface... see
//	                    refKinds), value; a profile's kinds may map others
//	@name               the defined or referenced name
//	@doc                the doc comment or docstring (repeatable); without
//	                    it, the comments on the lines just above the
//	                    definition are its doc
//	@signature          the node whose text is the signature; without it,
//	                    the definition's text up to its body (or its first
//	                    line)
//	@receiver           the type a definition written outside it belongs to
//	                    (a Go method's receiver, A in C++'s A::f)
//	@qualifier          what a reference is reached through (pkg in pkg.F)
//	@exported           present when the definition is exported (profiles
//	                    with scope.exported: declared)
//	@form               the head of a @definition.form
//	@import             a whole import form, with @import.spec (the path or
//	                    module, quotes stripped; the whole form when
//	                    absent), @import.alias, @import.name (repeatable)
//	                    and (#set! import.kind "<word>")
//	@package            the file's package, module or namespace declaration
//
// Captures whose name starts with an underscore are free for predicates.
// A parse that is cut off or fails gives text facts with ParseError set; a
// tree with error nodes still gives whatever the query matched.

//go:embed queries
var embeddedQueries embed.FS

// Defaults of an Engine.
const (
	// DefaultTimeout cuts a parse off (plan 0012: "a parse is cut off
	// after 2 s and the file is indexed as text").
	DefaultTimeout = 2 * time.Second
	// DefaultMemoryBudget bounds the memory of one parse.
	DefaultMemoryBudget = 256 << 20
)

// maxSignatureBytes and maxQualifierBytes cap those texts.
const (
	maxSignatureBytes = 512
	maxQualifierBytes = 200
)

// Options tune an Engine; zero values take the defaults.
type Options struct {
	Timeout      time.Duration
	MemoryBudget int64
}

// Engine parses files into Facts with the languages of one queries
// directory. It is safe for concurrent use; each language's grammar and
// query are prepared once, on first use.
type Engine struct {
	fsys     fs.FS
	profiles Profiles
	opts     Options

	mu    sync.Mutex
	langs map[string]*language // by language ID and dialect
}

// NewEngine returns an engine over the queries directory fsys (one
// <lang>/profile.yaml and query files per language; see LoadProfiles).
func NewEngine(fsys fs.FS, opts Options) (*Engine, error) {
	profiles, err := LoadProfiles(fsys)
	if err != nil {
		return nil, err
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.MemoryBudget <= 0 {
		opts.MemoryBudget = DefaultMemoryBudget
	}
	return &Engine{fsys: fsys, profiles: profiles, opts: opts, langs: map[string]*language{}}, nil
}

var defaultEngine = sync.OnceValues(func() (*Engine, error) {
	sub, err := fs.Sub(embeddedQueries, "queries")
	if err != nil {
		return nil, err
	}
	return NewEngine(sub, Options{})
})

// Default returns the engine over the query files compiled into the
// binary.
func Default() (*Engine, error) { return defaultEngine() }

// Parse returns the facts of the file at path (slash-separated) with the
// default engine; see Engine.Parse. It is the codeindex ParseFunc.
func Parse(filePath string, content []byte) (Facts, error) {
	e, err := Default()
	if err != nil {
		return Facts{}, err
	}
	return e.Parse(filePath, content)
}

// ParseVersion returns the cache version of the facts Parse gives for the
// file (the codeindex ParseVersion option).
func ParseVersion(filePath string, content []byte) string {
	e, err := Default()
	if err != nil {
		return TextFactsVersion("")
	}
	return e.Version(filePath, content)
}

// Profiles returns the engine's language profiles.
func (e *Engine) Profiles() Profiles { return e.profiles }

// Parse returns the facts of the file at path: its language is detected
// from the path and content alone (Detect without neighbours; a caller
// that knows the directory uses ParseDetected). Markdown is chunked by
// heading, a file of no language by line windows, and a file whose
// language has no grammar, or whose parse is cut off or fails, is indexed
// as text with ParseError set. The error is reserved for a broken query
// file or facts that fail Validate, which a caller indexes as text too.
func (e *Engine) Parse(filePath string, content []byte) (Facts, error) {
	return e.ParseDetected(Detect(filePath, content, nil), content)
}

// Version is the cache version (Key.Version) of the facts Parse gives for
// the file.
func (e *Engine) Version(filePath string, content []byte) string {
	return e.VersionDetected(Detect(filePath, content, nil))
}

// VersionDetected is the cache version of facts parsed as d.
func (e *Engine) VersionDetected(d Detection) string {
	p := e.profiles.For(d)
	if p == nil {
		return TextFactsVersion(d.Lang)
	}
	if g, _ := p.Resolve(d.Dialect); !registered(g) {
		return TextFactsVersion(d.Lang)
	}
	return p.FactsVersion(d.Dialect)
}

func registered(grammar string) bool {
	_, ok := grammars.Lookup(grammar)
	return ok
}

// ParseDetected returns the facts of content parsed as d.
func (e *Engine) ParseDetected(d Detection, content []byte) (facts Facts, err error) {
	switch d.Lang {
	case "":
		return TextFacts("", content, ""), nil
	case Markdown:
		return MarkdownFacts(content), nil
	}
	l, err := e.language(d)
	switch {
	case errors.Is(err, errNoGrammar):
		facts = TextFacts(d.Lang, content, ParseNoGrammar)
	case err != nil:
		return Facts{}, err
	default:
		facts = l.extract(content)
	}
	facts.Dialect = d.Dialect
	if err := facts.Validate(); err != nil {
		return Facts{}, fmt.Errorf("codeparse: %s facts: %w", d.Lang, err)
	}
	return facts, nil
}

// Check prepares every language and dialect of the engine's profiles: each
// grammar must be registered and each query must compile and use only
// known captures. It reports every problem.
func (e *Engine) Check() error {
	var errs []error
	ids := make([]string, 0, len(e.profiles))
	for id := range e.profiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		dialects := []string{""}
		if l, ok := LookupLanguage(id); ok {
			for _, d := range l.Dialects {
				dialects = append(dialects, d)
			}
		}
		sort.Strings(dialects)
		for _, d := range compactStrings(dialects) {
			if _, err := e.language(Detection{Lang: id, Dialect: d}); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

var errNoGrammar = errors.New("no grammar registered")

// language is one language (in one dialect) ready to parse.
type language struct {
	once    sync.Once
	err     error
	profile *Profile
	dialect string
	lang    *ts.Language
	query   *ts.Query
	pool    *ts.ParserPool
}

func (e *Engine) language(d Detection) (*language, error) {
	p := e.profiles.For(d)
	if p == nil {
		return nil, fmt.Errorf("codeparse: %s: %w (no profile)", d.Lang, errNoGrammar)
	}
	key := d.Lang + "/" + d.Dialect
	e.mu.Lock()
	l, ok := e.langs[key]
	if !ok {
		l = &language{profile: p, dialect: d.Dialect}
		e.langs[key] = l
	}
	e.mu.Unlock()
	l.once.Do(func() { l.err = l.prepare(e) })
	return l, l.err
}

func (l *language) prepare(e *Engine) error {
	grammar, queryFile := l.profile.Resolve(l.dialect)
	name := l.profile.Lang
	if l.dialect != "" {
		name += " (" + l.dialect + ")"
	}
	lang, ok := grammars.Lookup(grammar)
	if !ok {
		return fmt.Errorf("codeparse: %s: grammar %q: %w", name, grammar, errNoGrammar)
	}
	text, err := fs.ReadFile(e.fsys, path.Join(l.profile.Lang, queryFile))
	if err != nil {
		return fmt.Errorf("codeparse: %s: %w", name, err)
	}
	q, err := ts.NewQuery(string(text), lang)
	if err != nil {
		return fmt.Errorf("codeparse: %s: query %s: %w", name, queryFile, err)
	}
	var bad []string
	for _, c := range q.CaptureNames() {
		if !l.knownCapture(c) {
			bad = append(bad, "@"+c)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("codeparse: %s: query %s: unknown captures %s (queries/README.md lists them)", name, queryFile, strings.Join(bad, ", "))
	}
	l.lang, l.query = lang, q
	l.pool = ts.NewParserPool(lang,
		ts.WithParserPoolTimeoutMicros(uint64(e.opts.Timeout.Microseconds())),
		ts.WithParserPoolMemoryBudgetBytes(e.opts.MemoryBudget))
	return nil
}

// Capture names the extractor reads.
const (
	capName        = "name"
	capDoc         = "doc"
	capSignature   = "signature"
	capReceiver    = "receiver"
	capQualifier   = "qualifier"
	capExported    = "exported"
	capForm        = "form"
	capImport      = "import"
	capImportSpec  = "import.spec"
	capImportAlias = "import.alias"
	capImportName  = "import.name"
	capPackage     = "package"
	capDefForm     = "definition.form"
	setImportKind  = "import.kind"
)

// refKinds maps reference captures to Ref kinds, under a profile's kinds.
var refKinds = map[string]string{
	"reference.call":           RefCall,
	"reference.send":           RefCall,
	"reference.macro":          RefCall,
	"reference.type":           RefType,
	"reference.class":          RefType,
	"reference.interface":      RefType,
	"reference.struct":         RefType,
	"reference.enum":           RefType,
	"reference.trait":          RefType,
	"reference.module":         RefType,
	"reference.extends":        RefExtends,
	"reference.superclass":     RefExtends,
	"reference.inherits":       RefExtends,
	"reference.mixin":          RefExtends,
	"reference.implements":     RefImplements,
	"reference.implementation": RefImplements,
	"reference.value":          RefValue,
	"reference.variable":       RefValue,
	"reference.constant":       RefValue,
	"reference.field":          RefValue,
	"reference.property":       RefValue,
}

func (l *language) refKind(capture string) (string, bool) {
	if k, ok := l.profile.Kinds[capture]; ok && isRefKind(k) {
		return k, true
	}
	k, ok := refKinds[capture]
	return k, ok
}

func (l *language) knownCapture(c string) bool {
	switch c {
	case capName, capDoc, capSignature, capReceiver, capQualifier, capExported, capForm,
		capImport, capImportSpec, capImportAlias, capImportName, capPackage, capDefForm:
		return true
	}
	if strings.HasPrefix(c, "_") {
		return true
	}
	if strings.HasPrefix(c, "definition.") {
		_, ok := l.profile.SymbolKind(c)
		return ok
	}
	if strings.HasPrefix(c, "reference.") {
		_, ok := l.refKind(c)
		return ok
	}
	return false
}

// extract parses content and builds its facts; a parse that stops early or
// fails (or panics) gives text facts.
func (l *language) extract(content []byte) (facts Facts) {
	defer func() {
		if r := recover(); r != nil {
			facts = TextFacts(l.profile.Lang, content, ParseFailed)
		}
	}()
	tree, err := l.pool.Parse(content)
	if err != nil || tree == nil {
		return TextFacts(l.profile.Lang, content, ParseFailed)
	}
	defer tree.Release()
	switch tree.ParseStopReason() {
	case ts.ParseStopAccepted:
	case ts.ParseStopTimeout:
		return TextFacts(l.profile.Lang, content, ParseTimeout)
	default:
		return TextFacts(l.profile.Lang, content, ParseFailed)
	}
	root := tree.RootNode()
	if root == nil {
		return TextFacts(l.profile.Lang, content, ParseFailed)
	}
	x := &extraction{l: l, src: content, lines: newLineIndex(content), root: root}
	x.run()
	return x.facts()
}

// extraction is the state of one file's extraction.
type extraction struct {
	l     *language
	src   []byte
	lines *lineIndex
	root  *ts.Node

	pkg     string
	defs    []*def
	refs    []ref
	imports []*imp
	order   int

	comments map[int]*ts.Node // whole-line comments by their last line, built on demand
}

type def struct {
	node      *ts.Node
	kind      string
	name      string
	nameAt    int
	docs      []*ts.Node
	signature string
	receiver  string
	exported  bool
	order     int

	container int // index of the innermost definition containing it, -1
	parent    int // container, or the receiver's definition, -1
	qual      string
	id        string
	docStart  int
}

func (d *def) start() int { return int(d.node.StartByte()) }
func (d *def) end() int   { return int(d.node.EndByte()) }

type ref struct {
	Ref
	at int
}

type imp struct {
	Import
	start, end int
}

func (x *extraction) text(c ts.QueryCapture) string { return c.Text(x.src) }

func (x *extraction) run() {
	cur := x.l.query.Exec(x.root, x.l.lang, x.src)
	for {
		m, ok := cur.NextMatch()
		if !ok {
			break
		}
		x.match(m)
	}
}

func (x *extraction) match(m ts.QueryMatch) {
	var name, receiver, qualifier, form, spec, alias *ts.QueryCapture
	var docs []*ts.Node
	var names []string
	var signature string
	exported := false
	for i := range m.Captures {
		c := &m.Captures[i]
		switch c.Name {
		case capName:
			if name == nil {
				name = c
			}
		case capDoc:
			docs = append(docs, c.Node)
		case capSignature:
			if signature == "" {
				signature = x.text(*c)
			}
		case capReceiver:
			receiver = c
		case capQualifier:
			qualifier = c
		case capExported:
			exported = true
		case capForm:
			form = c
		case capImportSpec:
			spec = c
		case capImportAlias:
			alias = c
		case capImportName:
			names = append(names, x.text(*c))
		case capPackage:
			if x.pkg == "" {
				x.pkg = strings.Join(strings.Fields(x.text(*c)), "")
			}
		}
	}
	for i := range m.Captures {
		c := &m.Captures[i]
		switch {
		case c.Name == capImport:
			x.addImport(c, spec, alias, names, m.SetValues(x.l.query, setImportKind))
		case c.Name == capDefForm:
			if name == nil || form == nil {
				continue
			}
			head := strings.TrimSpace(x.text(*form))
			lookup := head
			if x.l.profile.Scope.CaseInsensitive {
				lookup = strings.ToLower(head)
			}
			kind, ok := x.l.profile.DefiningForm(x.l.dialect, lookup)
			if !ok {
				x.addRef(RefCall, head, int(form.Node.StartByte()), nil)
				continue
			}
			x.addDef(c.Node, kind, name, docs, signature, receiver, exported)
		case strings.HasPrefix(c.Name, "definition."):
			kind, ok := x.l.profile.SymbolKind(c.Name)
			if !ok || name == nil {
				continue
			}
			x.addDef(c.Node, kind, name, docs, signature, receiver, exported)
		case strings.HasPrefix(c.Name, "reference."):
			kind, ok := x.l.refKind(c.Name)
			if !ok || name == nil {
				continue
			}
			x.addRef(kind, x.text(*name), int(name.Node.StartByte()), qualifier)
		}
	}
}

func (x *extraction) addDef(n *ts.Node, kind string, name *ts.QueryCapture, docs []*ts.Node, signature string, receiver *ts.QueryCapture, exported bool) {
	nm := normalizeSpace(x.text(*name))
	if nm == "" {
		return
	}
	d := &def{node: n, kind: kind, name: nm, nameAt: int(name.Node.StartByte()), docs: docs,
		signature: normalizeSignature(signature), exported: exported, order: x.order}
	if receiver != nil {
		d.receiver = normalizeSpace(x.text(*receiver))
	}
	x.order++
	x.defs = append(x.defs, d)
}

func (x *extraction) addRef(kind, name string, at int, qualifier *ts.QueryCapture) {
	name = normalizeSpace(name)
	if name == "" {
		return
	}
	r := ref{Ref: Ref{Name: name, Kind: kind, Start: x.lines.pos(at)}, at: at}
	if qualifier != nil {
		r.Qualifier = truncate(normalizeSpace(x.text(*qualifier)), maxQualifierBytes)
	}
	x.refs = append(x.refs, r)
}

func (x *extraction) addImport(c, spec, alias *ts.QueryCapture, names, kind []string) {
	s := c
	if spec != nil {
		s = spec
	}
	i := &imp{Import: Import{Spec: unquote(normalizeSpace(x.text(*s)))},
		start: int(c.Node.StartByte()), end: int(c.Node.EndByte())}
	if alias != nil {
		i.Alias = normalizeSpace(x.text(*alias))
	}
	i.Names = names
	if len(kind) > 0 {
		i.Kind = kind[0]
	}
	i.Start = x.lines.pos(i.start)
	if i.Spec != "" {
		x.imports = append(x.imports, i)
	}
}

// facts assembles the extracted matches.
func (x *extraction) facts() Facts {
	f := Facts{Lang: x.l.profile.Lang, Package: x.pkg}
	defs := x.resolveDefs()
	defAt := make(map[int]bool, len(defs))
	chunkSyms := make([]chunkSymbol, len(defs))
	for i, d := range defs {
		defAt[d.nameAt] = true
		if d.signature == "" {
			d.signature = x.defaultSignature(d.node)
		}
		s := Symbol{ID: d.id, Kind: d.kind, Name: d.name, QualName: d.qual, Signature: d.signature,
			Doc: x.doc(d), Start: x.lines.pos(d.start()), End: x.lines.pos(d.end()), Exported: x.exported(d)}
		if d.parent >= 0 {
			s.Parent = defs[d.parent].id
		}
		f.Symbols = append(f.Symbols, s)
		chunkSyms[i] = chunkSymbol{id: d.id, kind: d.kind, signature: d.signature, node: d.node,
			start: min(d.docStart, d.start()), end: d.end(), container: d.container}
	}
	f.Imports = x.mergeImports()
	f.Refs = x.resolveRefs(defs, defAt)
	f.Chunks = codeChunks(x.lines, x.l.lang, chunkSyms)
	return f
}

// resolveDefs sorts the definitions into source order, drops duplicates,
// and sets their containers, parents, qualified names and IDs.
func (x *extraction) resolveDefs() []*def {
	sort.SliceStable(x.defs, func(i, j int) bool {
		a, b := x.defs[i], x.defs[j]
		if a.start() != b.start() {
			return a.start() < b.start()
		}
		if a.end() != b.end() {
			return a.end() > b.end()
		}
		if a.nameAt != b.nameAt {
			return a.nameAt < b.nameAt
		}
		return a.order < b.order
	})
	var defs []*def
	for _, d := range x.defs {
		if n := len(defs); n > 0 {
			p := defs[n-1]
			if p.start() == d.start() && p.end() == d.end() && p.nameAt == d.nameAt {
				continue // the same definition matched by two patterns
			}
		}
		defs = append(defs, d)
	}
	// Containers: the innermost definition strictly containing each.
	var stack []int
	for i, d := range defs {
		for len(stack) > 0 && !strictlyContains(defs[stack[len(stack)-1]], d) {
			stack = stack[:len(stack)-1]
		}
		d.container, d.parent = -1, -1
		if len(stack) > 0 {
			d.container = stack[len(stack)-1]
			d.parent = d.container
		}
		stack = append(stack, i)
	}
	// Receivers: a definition written outside its type belongs to the
	// first top-level, non-callable definition of that name.
	types := map[string]int{}
	for j := len(defs) - 1; j >= 0; j-- {
		if t := defs[j]; t.container < 0 && t.receiver == "" && !callableKinds[t.kind] {
			types[t.name] = j
		}
	}
	// A nested one (C++'s Circle::area inside namespace geometry) belongs
	// to the first such definition that is, or is in or under, its
	// container; when the
	// file has none it keeps the container as parent and the receiver in
	// its qualified name (viaReceiver).
	viaReceiver := make([]bool, len(defs))
	for i, d := range defs {
		if d.receiver == "" {
			continue
		}
		if d.container < 0 {
			if j, ok := types[d.receiver]; ok {
				d.parent = j
			}
			continue
		}
		viaReceiver[i] = true
		c := defs[d.container]
		for j, t := range defs {
			if j != i && t.name == d.receiver && t.receiver == "" && !callableKinds[t.kind] && (t == c || strictlyContains(c, t)) {
				d.parent, viaReceiver[i] = j, false
				break
			}
		}
	}
	sep := x.l.profile.Scope.Separators[0]
	var qual func(i int) string
	qual = func(i int) string {
		d := defs[i]
		if d.qual != "" {
			return d.qual
		}
		switch {
		case viaReceiver[i]:
			d.qual = qual(d.parent) + sep + d.receiver + sep + d.name
		case d.parent >= 0:
			d.qual = qual(d.parent) + sep + d.name
		case d.receiver != "":
			d.qual = x.prefix(sep) + d.receiver + sep + d.name
		default:
			d.qual = x.prefix(sep) + d.name
		}
		return d.qual
	}
	seen := map[string]int{}
	for i, d := range defs {
		q := qual(i)
		seen[q]++
		d.id = SymbolID(q, seen[q])
		d.docStart = d.start()
	}
	return defs
}

// callableKinds are never a receiver's type.
var callableKinds = map[string]bool{KindFunction: true, KindMethod: true, KindVar: true, KindConst: true, KindField: true, KindProperty: true, KindMacro: true}

func (x *extraction) prefix(sep string) string {
	if x.pkg == "" {
		return ""
	}
	return x.pkg + sep
}

func strictlyContains(a, b *def) bool {
	return a.start() <= b.start() && b.end() <= a.end() && (a.start() != b.start() || a.end() != b.end())
}

// defaultSignature is a definition's text up to its body (the child in
// field body), less the comments just before the body (Ruby's
// "module Shop # The currency…"), or its first line when it has no body.
func (x *extraction) defaultSignature(n *ts.Node) string {
	start, end := int(n.StartByte()), int(n.EndByte())
	if body := n.ChildByFieldName("body", x.l.lang); body != nil && int(body.StartByte()) > start {
		end = int(body.StartByte())
		for i := n.ChildCount() - 1; i >= 0; i-- {
			c := n.Child(i)
			if c == nil || int(c.EndByte()) > end {
				continue
			}
			if !strings.Contains(c.Type(x.l.lang), "comment") || int(c.StartByte()) <= start {
				break
			}
			end = int(c.StartByte())
		}
	} else if first := x.lines.line(start); x.lines.line(max(start, end-1)) > first {
		end = x.lines.end(first)
	}
	return normalizeSignature(string(x.src[start:end]))
}

func (x *extraction) exported(d *def) bool {
	switch x.l.profile.Scope.Exported {
	case ExportedCapitalized:
		r, _ := utf8.DecodeRuneInString(d.name)
		return unicode.IsUpper(r)
	case ExportedNoUnderscore:
		return !strings.HasPrefix(d.name, "_")
	case ExportedDeclared:
		return d.exported
	}
	return true
}

// doc returns d's doc: its @doc captures, else the whole-line comments on
// the lines just above it. It sets d.docStart to where the doc begins when
// the doc is above the definition.
func (x *extraction) doc(d *def) string {
	if len(d.docs) > 0 {
		parts := make([]string, 0, len(d.docs))
		for _, n := range d.docs {
			parts = append(parts, cleanDoc(n.Text(x.src)))
			if s := int(n.StartByte()); s < d.docStart {
				d.docStart = s
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}
	if x.comments == nil {
		x.collectComments()
	}
	var above []*ts.Node
	line := x.lines.line(d.start()) - 1
	for line >= 0 {
		c, ok := x.comments[line]
		if !ok || int(c.EndByte()) > d.start() {
			break
		}
		above = append(above, c)
		line = x.lines.line(int(c.StartByte())) - 1
	}
	if len(above) == 0 {
		return ""
	}
	parts := make([]string, 0, len(above))
	for i := len(above) - 1; i >= 0; i-- {
		parts = append(parts, cleanDoc(above[i].Text(x.src)))
	}
	d.docStart = int(above[len(above)-1].StartByte())
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// collectComments indexes the comments that stand on lines of their own
// (nothing but blanks before and after them) by their last line.
func (x *extraction) collectComments() {
	x.comments = map[int]*ts.Node{}
	stack := []*ts.Node{x.root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !strings.Contains(n.Type(x.l.lang), "comment") {
			for i := n.ChildCount() - 1; i >= 0; i-- {
				stack = append(stack, n.Child(i))
			}
			continue
		}
		start, end := int(n.StartByte()), int(n.EndByte())
		if end <= start || strings.TrimSpace(string(x.src[x.lines.start(x.lines.line(start)):start])) != "" {
			continue
		}
		last := x.lines.line(end - 1)
		if lineEnd := x.lines.end(last); end < lineEnd && strings.TrimSpace(string(x.src[end:lineEnd])) != "" {
			continue
		}
		x.comments[last] = n
	}
}

// mergeImports returns the imports in source order, one per import node
// (several matches of one node merge their names).
func (x *extraction) mergeImports() []Import {
	sort.SliceStable(x.imports, func(i, j int) bool { return x.imports[i].start < x.imports[j].start })
	var out []Import
	var prev *imp
	for _, i := range x.imports {
		if prev != nil && prev.start == i.start && prev.end == i.end && prev.Spec == i.Spec {
			last := &out[len(out)-1]
			for _, n := range i.Names {
				if !containsString(last.Names, n) {
					last.Names = append(last.Names, n)
				}
			}
			if last.Alias == "" {
				last.Alias = i.Alias
			}
			if last.Kind == "" {
				last.Kind = i.Kind
			}
			continue
		}
		names := make([]string, 0, len(i.Names))
		for _, n := range i.Names {
			if n = normalizeSpace(n); n != "" && !containsString(names, n) {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			names = nil
		}
		i.Import.Names = names
		out = append(out, i.Import)
		prev = i
	}
	return out
}

// resolveRefs drops references at a definition's own name and duplicates
// (keeping the qualified one), sorts them and sets their enclosing symbols.
func (x *extraction) resolveRefs(defs []*def, defAt map[int]bool) []Ref {
	type key struct {
		at   int
		kind string
	}
	best := map[key]int{}
	var refs []ref
	for _, r := range x.refs {
		if defAt[r.at] {
			continue
		}
		k := key{r.at, r.Kind}
		if i, ok := best[k]; ok {
			if refs[i].Qualifier == "" && r.Qualifier != "" {
				refs[i] = r
			}
			continue
		}
		best[k] = len(refs)
		refs = append(refs, r)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].at != refs[j].at {
			return refs[i].at < refs[j].at
		}
		return refs[i].Kind < refs[j].Kind
	})
	out := make([]Ref, len(refs))
	var stack []*def
	di := 0
	for i, r := range refs {
		for di < len(defs) && defs[di].start() <= r.at {
			d := defs[di]
			for len(stack) > 0 && stack[len(stack)-1].end() <= d.start() {
				stack = stack[:len(stack)-1]
			}
			stack = append(stack, d)
			di++
		}
		for len(stack) > 0 && stack[len(stack)-1].end() <= r.at {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 {
			r.Enclosing = stack[len(stack)-1].id
		}
		out[i] = r.Ref
	}
	return out
}

// normalizeSpace collapses runs of white space to one space and trims.
func normalizeSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// normalizeSignature collapses a signature's white space, drops an opening
// brace it ends with and caps its length.
func normalizeSignature(s string) string {
	s = normalizeSpace(s)
	s = strings.TrimSpace(strings.TrimSuffix(s, "{"))
	return truncate(s, maxSignatureBytes)
}

// truncate cuts s to at most n bytes at a rune boundary, marking the cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// unquote strips one pair of quotes or angle brackets around an import
// spec.
func unquote(s string) string {
	if len(s) >= 2 {
		switch first, last := s[0], s[len(s)-1]; {
		case first == last && (first == '"' || first == '\'' || first == '`'),
			first == '<' && last == '>':
			return s[1 : len(s)-1]
		}
	}
	return s
}

// docDelimiters are the block forms a doc comment or docstring can take.
var docDelimiters = [][2]string{
	{`"""`, `"""`}, {"'''", "'''"}, {"/**", "*/"}, {"/*!", "*/"}, {"/*", "*/"},
	{"(**", "*)"}, {"(*", "*)"}, {"{-", "-}"}, {"--[[", "]]"}, {"=begin", "=end"}, {"=pod", "=cut"},
}

// lineMarkers start the lines of line comments, longest first.
var lineMarkers = []string{"///", "//!", "//", ";;;;", ";;;", ";;", ";", "##", "#", "---", "--", "%%", "%", "*"}

// cleanDoc strips comment markers or string quotes from a doc's text,
// keeping its lines.
func cleanDoc(raw string) string {
	s := strings.TrimSpace(raw)
	stringDoc := false
	for _, d := range docDelimiters {
		if len(s) >= len(d[0])+len(d[1]) && strings.HasPrefix(s, d[0]) && strings.HasSuffix(s, d[1]) {
			s = s[len(d[0]) : len(s)-len(d[1])]
			stringDoc = d[0] == `"""` || d[0] == "'''"
			break
		}
	}
	if !stringDoc && len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') && s[len(s)-1] == s[0] {
		s, stringDoc = s[1:len(s)-1], true
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if !stringDoc {
			for _, m := range lineMarkers {
				if strings.HasPrefix(t, m) {
					t = strings.TrimPrefix(t, m)
					break
				}
			}
			t = strings.TrimSpace(t)
		}
		lines[i] = t
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func containsString(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func compactStrings(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
