package codeparse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ProfileFile is the name of a language's profile in its queries directory.
const ProfileFile = "profile.yaml"

// DefaultQuery is the query file a profile uses when it names none.
const DefaultQuery = "tags.scm"

// Profile is a language's data: what the extractor needs to turn its query
// captures into Facts and what the resolver of its family needs to scope
// names. It is read from queries/<lang>/profile.yaml, so adding or changing a
// language edits data, never Go code:
//
//	name: Go
//	family: go
//	extensions: [.go]
//	query: tags.scm            # default
//	kinds:                     # capture → symbol kind, over DefaultKinds
//	  definition.class: struct
//	scope:
//	  separators: ["."]
//	  packageBy: directory
//	  exported: capitalized
//
// A Lisp-like language lists the head symbols that define something:
//
//	definingForms:
//	  - { form: defun, kind: function, dialect: commonlisp }
type Profile struct {
	// Lang is the language ID; it may be left out of the file and must equal
	// the directory name when present.
	Lang string `yaml:"lang"`
	// Name is the human name.
	Name string `yaml:"name"`
	// Family is the resolution family; it must match the Languages table.
	Family string `yaml:"family"`
	// Extensions and Filenames repeat the Languages table's, which decides
	// detection; a profile that disagrees is refused.
	Extensions []string `yaml:"extensions"`
	Filenames  []string `yaml:"filenames"`
	// Grammar is the name the language's grammar registers under; the
	// language ID by default.
	Grammar string `yaml:"grammar"`
	// Query is the query file, relative to the language's directory;
	// DefaultQuery by default.
	Query string `yaml:"query"`
	// Dialects override Grammar and Query per dialect (a key of the Languages
	// table's Dialects values); a dialect left out uses its own name as the
	// grammar and the language's query.
	Dialects map[string]DialectProfile `yaml:"dialects"`
	// Kinds maps a capture name (definition.class) to the symbol kind it
	// produces, over DefaultKinds.
	Kinds map[string]string `yaml:"kinds"`
	// DefiningForms are the forms that define a symbol in languages whose
	// definitions are ordinary calls (Lisp heads, Ruby attr_accessor, ...);
	// a form not listed is a reference.
	DefiningForms []DefiningForm `yaml:"definingForms"`
	// Scope is how names are scoped and qualified, for the resolver.
	Scope Scope `yaml:"scope"`

	// Digest is the hex SHA-256 over every file in the language's directory
	// (profile, query files), set by LoadProfiles; it versions the
	// language's facts (FactsVersion).
	Digest string `yaml:"-"`
}

// DialectProfile is one dialect's grammar and query.
type DialectProfile struct {
	Grammar string `yaml:"grammar"`
	Query   string `yaml:"query"`
}

// DefiningForm is a form that defines a symbol of Kind.
type DefiningForm struct {
	Form    string `yaml:"form"`
	Kind    string `yaml:"kind"`
	Dialect string `yaml:"dialect"` // "" for every dialect
}

// Scope is how a language family scopes and qualifies names.
type Scope struct {
	// Separators join a qualified name's parts (".", "::", ":", "\\"); the
	// first one is used to build QualName. ["."] by default.
	Separators []string `yaml:"separators"`
	// PackageBy is where a file's package comes from: PackageByDeclaration
	// (a package or namespace form in the file), PackageByDirectory,
	// PackageByFile (the file is its own module) or PackageByNone (one
	// global scope, as C).
	PackageBy string `yaml:"packageBy"`
	// Exported is the rule for Symbol.Exported: ExportedAll (default),
	// ExportedCapitalized (Go), ExportedNoUnderscore (Python, Dart) or
	// ExportedDeclared (a modifier or export form the query captures).
	Exported string `yaml:"exported"`
	// CaseInsensitive names compare without case (SQL, Common Lisp).
	CaseInsensitive bool `yaml:"caseInsensitive"`
	// SplitQualifiers, per dialect ("*" for every dialect and a file of
	// none), is the separator a grammar leaves inside one symbol between a
	// reference's qualifier and its name (Racket's str:string-join, after
	// prefix-in str:): a reference captured without @qualifier whose name
	// holds it, not at either end, is cut at its first occurrence.
	SplitQualifiers map[string]string `yaml:"splitQualifiers"`
	// Options are family-specific settings the family's resolver reads.
	Options map[string]string `yaml:"options"`
}

// Scope.PackageBy values.
const (
	PackageByDeclaration = "declaration"
	PackageByDirectory   = "directory"
	PackageByFile        = "file"
	PackageByNone        = "none"
)

// Scope.Exported values.
const (
	ExportedAll          = "all"
	ExportedCapitalized  = "capitalized"
	ExportedNoUnderscore = "noUnderscore"
	ExportedDeclared     = "declared"
)

// DefaultKinds maps tree-sitter's tags-query definition captures to symbol
// kinds. A capture definition.<word> not listed here or in a profile's Kinds
// produces the kind <word>.
var DefaultKinds = map[string]string{
	"definition.function":  KindFunction,
	"definition.method":    KindMethod,
	"definition.class":     KindClass,
	"definition.struct":    KindStruct,
	"definition.union":     KindUnion,
	"definition.interface": KindInterface,
	"definition.trait":     KindTrait,
	"definition.protocol":  KindProtocol,
	"definition.enum":      KindEnum,
	"definition.type":      KindType,
	"definition.field":     KindField,
	"definition.property":  KindProperty,
	"definition.constant":  KindConst,
	"definition.variable":  KindVar,
	"definition.module":    KindModule,
	"definition.namespace": KindNamespace,
	"definition.package":   KindPackage,
	"definition.macro":     KindMacro,
	"definition.impl":      KindImpl,
	"definition.extension": KindExtension,
}

// SymbolKind returns the symbol kind a definition capture produces, and false
// for a capture that defines nothing.
func (p *Profile) SymbolKind(capture string) (string, bool) {
	if k, ok := p.Kinds[capture]; ok {
		return k, true
	}
	if k, ok := DefaultKinds[capture]; ok {
		return k, true
	}
	if word, ok := strings.CutPrefix(capture, "definition."); ok && validWord(word) {
		return word, true
	}
	return "", false
}

// SplitQualifier returns the separator references are split at in a
// dialect (Scope.SplitQualifiers), or "".
func (p *Profile) SplitQualifier(dialect string) string {
	if s, ok := p.Scope.SplitQualifiers[dialect]; ok && dialect != "" {
		return s
	}
	return p.Scope.SplitQualifiers["*"]
}

// DefiningForm returns the kind a defining form produces in a dialect.
func (p *Profile) DefiningForm(dialect, form string) (string, bool) {
	for _, d := range p.DefiningForms {
		if d.Form == form && (d.Dialect == "" || d.Dialect == dialect) {
			return d.Kind, true
		}
	}
	return "", false
}

// Resolve returns the grammar name and query file for a dialect ("" for the
// language's default).
func (p *Profile) Resolve(dialect string) (grammar, query string) {
	if dialect == "" {
		return p.Grammar, p.Query
	}
	d, ok := p.Dialects[dialect]
	if !ok {
		return dialect, p.Query
	}
	return d.Grammar, d.Query
}

// FactsVersion is the cache version of facts parsed with this profile in a
// dialect: the engine Version, the language and dialect, and the profile's
// digest, so a change to the language's data re-parses only its files.
func (p *Profile) FactsVersion(dialect string) string {
	lang := p.Lang
	if dialect != "" {
		lang += ":" + dialect
	}
	digest := p.Digest
	if len(digest) > 16 {
		digest = digest[:16]
	}
	return Version + "/" + lang + "@" + digest
}

// TextFactsVersion is the cache version of a file with no profile: Markdown,
// or plain text (lang "") and files that fell back to text.
func TextFactsVersion(lang string) string {
	if lang == "" {
		lang = "text"
	}
	return Version + "/" + lang
}

// Profiles are the loaded profiles by language ID.
type Profiles map[string]*Profile

// For returns the profile of a detected language, or nil.
func (ps Profiles) For(d Detection) *Profile { return ps[d.Lang] }

// LoadProfiles reads every <lang>/profile.yaml at the root of fsys (the
// queries directory: the extractor passes fs.Sub of its embedded copy). A
// directory without a profile, a profile that fails ParseProfile, or one
// whose query files are missing is an error; plain files at the root (a
// README) are ignored.
func LoadProfiles(fsys fs.FS) (Profiles, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("codeparse: read profiles: %w", err)
	}
	out := Profiles{}
	var errs []error
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		lang := e.Name()
		p, err := loadProfile(fsys, lang)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		out[lang] = p
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

func loadProfile(fsys fs.FS, lang string) (*Profile, error) {
	data, err := fs.ReadFile(fsys, path.Join(lang, ProfileFile))
	if err != nil {
		return nil, fmt.Errorf("codeparse: %s: %w", lang, err)
	}
	p, err := ParseProfile(lang, data)
	if err != nil {
		return nil, err
	}
	queries := []string{p.Query}
	for _, d := range p.Dialects {
		queries = append(queries, d.Query)
	}
	for _, q := range queries {
		if _, err := fs.Stat(fsys, path.Join(lang, q)); err != nil {
			return nil, fmt.Errorf("codeparse: %s: query file %s: %w", lang, q, err)
		}
	}
	if p.Digest, err = digestDir(fsys, lang); err != nil {
		return nil, fmt.Errorf("codeparse: %s: %w", lang, err)
	}
	return p, nil
}

// digestDir hashes every regular file under dir by its relative path and
// content, in path order.
func digestDir(fsys fs.FS, dir string) (string, error) {
	var files []string
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	h := sha256.New()
	for _, f := range files {
		data, err := fs.ReadFile(fsys, f)
		if err != nil {
			return "", err
		}
		rel := strings.TrimPrefix(f, dir+"/")
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ParseProfile decodes and validates the profile of language lang, filling
// defaults. Unknown keys are refused, so a typo fails instead of being
// ignored. Its Digest is left empty (LoadProfiles sets it).
func ParseProfile(lang string, data []byte) (*Profile, error) {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("codeparse: %s/%s: %s", lang, ProfileFile, fmt.Sprintf(format, args...))
	}
	var p Profile
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return nil, fail("%v", err)
	}
	l, ok := byID[lang]
	if !ok {
		return nil, fail("unknown language %q (the Languages table in detect.go lists the supported ones)", lang)
	}
	if p.Lang != "" && p.Lang != lang {
		return nil, fail("lang %q does not match its directory", p.Lang)
	}
	p.Lang = lang
	if p.Name == "" {
		p.Name = l.Name
	}
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fail(format, args...)) }
	if p.Family != l.Family {
		bad("family %q, want %q", p.Family, l.Family)
	}
	if !sameSet(lowerAll(p.Extensions), l.Extensions) {
		bad("extensions %v, want %v", p.Extensions, l.Extensions)
	}
	if !sameSet(p.Filenames, l.Filenames) {
		bad("filenames %v, want %v", p.Filenames, l.Filenames)
	}
	p.Extensions = slices.Clone(l.Extensions)
	p.Filenames = slices.Clone(l.Filenames)
	if p.Grammar == "" {
		p.Grammar = lang
	}
	if p.Query == "" {
		p.Query = DefaultQuery
	}
	if !validQueryPath(p.Query) {
		bad("query %q is not a .scm file inside the language directory", p.Query)
	}
	dialects := map[string]bool{}
	for _, d := range l.Dialects {
		dialects[d] = true
	}
	for name, d := range p.Dialects {
		if !dialects[name] {
			bad("dialect %q is not one of %s's", name, l.Name)
		}
		if d.Grammar == "" {
			d.Grammar = name
		}
		if d.Query == "" {
			d.Query = p.Query
		}
		if !validQueryPath(d.Query) {
			bad("dialect %s: query %q is not a .scm file inside the language directory", name, d.Query)
		}
		p.Dialects[name] = d
	}
	for capture, kind := range p.Kinds {
		if capture == "" || !validWord(kind) {
			bad("kinds: %q → %q (the kind must be a lowercase word)", capture, kind)
		}
	}
	for i, d := range p.DefiningForms {
		if d.Form == "" || !validWord(d.Kind) {
			bad("definingForms[%d]: form %q, kind %q (both required, the kind a lowercase word)", i, d.Form, d.Kind)
		}
		if d.Dialect != "" && !dialects[d.Dialect] {
			bad("definingForms[%d]: dialect %q is not one of %s's", i, d.Dialect, l.Name)
		}
	}
	for d, sep := range p.Scope.SplitQualifiers {
		if d != "*" && !dialects[d] {
			bad("scope.splitQualifiers: dialect %q is not one of %s's", d, l.Name)
		}
		if sep == "" {
			bad("scope.splitQualifiers: %s: empty separator", d)
		}
	}
	if len(p.Scope.Separators) == 0 {
		p.Scope.Separators = []string{"."}
	}
	for _, s := range p.Scope.Separators {
		if s == "" {
			bad("scope.separators: empty separator")
		}
	}
	if p.Scope.PackageBy == "" {
		p.Scope.PackageBy = PackageByNone
	}
	switch p.Scope.PackageBy {
	case PackageByDeclaration, PackageByDirectory, PackageByFile, PackageByNone:
	default:
		bad("scope.packageBy %q is not one of declaration, directory, file, none", p.Scope.PackageBy)
	}
	if p.Scope.Exported == "" {
		p.Scope.Exported = ExportedAll
	}
	switch p.Scope.Exported {
	case ExportedAll, ExportedCapitalized, ExportedNoUnderscore, ExportedDeclared:
	default:
		bad("scope.exported %q is not one of all, capitalized, noUnderscore, declared", p.Scope.Exported)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return &p, nil
}

func validQueryPath(q string) bool {
	return fs.ValidPath(q) && q != "." && strings.HasSuffix(q, ".scm")
}

func lowerAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

func sameSet(a, b []string) bool {
	set := func(ss []string) []string {
		out := slices.Clone(ss)
		sort.Strings(out)
		return slices.Compact(out)
	}
	return slices.Equal(set(a), set(b))
}
