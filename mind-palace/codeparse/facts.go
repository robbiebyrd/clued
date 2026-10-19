// Package codeparse turns one file version into the facts the code graph is
// built from: its symbols, imports, unresolved references and retrieval
// chunks.
//
// Facts are a pure function of a file's bytes, the language it was detected
// as and the parser version, so they are cached by content hash (Cache) and
// shared by every branch, layer and member that holds the same content. This
// file fixes their shape; it does not depend on the tree-sitter runtime that
// produces them. Language detection is in detect.go, the per-language data
// (queries/<lang>/profile.yaml) in profiles.go, the cache port in cache.go.
package codeparse

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version is the version of the parsing engine: the runtime, the extractor,
// the chunker and the shape of Facts. Bump it whenever a change would give
// different facts for the same bytes; cached facts under an older version are
// then missed and re-parsed lazily. A language's own data (its grammar
// registration, query files and profile) is versioned by its profile digest
// instead (Profile.FactsVersion), so a language story never edits Go code to
// invalidate its cache entries.
const Version = "3"

// Facts are everything the indexer needs from one file version.
type Facts struct {
	// Lang is the detected language (a directory name under queries/),
	// Markdown for Markdown files, or "" for a file indexed as text.
	Lang string `json:"lang"`
	// Dialect narrows Lang when one language uses several grammars (tsx,
	// a Lisp dialect, an OCaml interface); "" otherwise.
	Dialect string `json:"dialect,omitempty"`
	// Package is the file's own package, module or namespace as the language
	// declares it (Go package, Python module path, TS module specifier); ""
	// when the language derives it from the path only.
	Package string `json:"package,omitempty"`
	// Symbols are the definitions in source order.
	Symbols []Symbol `json:"symbols,omitempty"`
	// Imports are the import, include, use and require forms in source order.
	Imports []Import `json:"imports,omitempty"`
	// Refs are the unresolved references in source order.
	Refs []Ref `json:"refs,omitempty"`
	// Chunks are the retrieval units in source order.
	Chunks []Chunk `json:"chunks,omitempty"`
	// ParseError is set when the file could not be parsed (a syntax the
	// grammar rejects, a timeout, no grammar registered) and was indexed as
	// text instead: Symbols, Imports and Refs are then empty and Chunks are
	// line windows. It names the cause (ParseTimeout, ...).
	ParseError string `json:"parseError,omitempty"`
}

// Markdown is the Lang of Markdown files: they have no grammar or profile and
// are chunked by heading.
const Markdown = "markdown"

// Causes recorded in Facts.ParseError.
const (
	ParseTimeout   = "timeout"    // the parse ran past its deadline
	ParseFailed    = "failed"     // the runtime returned an error
	ParseNoGrammar = "no grammar" // the language has no registered grammar
)

// Pos is a position in a file: Line is 1-based (what path:line shows),
// Column a 0-based byte offset in the line and Offset a 0-based byte offset
// in the file, as tree-sitter reports points and bytes.
type Pos struct {
	Line   int `json:"line"`
	Column int `json:"column"`
	Offset int `json:"offset"`
}

// Before reports whether p comes strictly before q.
func (p Pos) Before(q Pos) bool { return p.Offset < q.Offset }

// String renders p as line:column with a 1-based column, the editor form.
func (p Pos) String() string { return strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column+1) }

func (p Pos) valid() bool { return p.Line >= 1 && p.Column >= 0 && p.Offset >= 0 }

// Symbol is one definition.
type Symbol struct {
	// ID is unique within the file (see SymbolID); the indexer prefixes it
	// with the layer and path.
	ID string `json:"id"`
	// Kind is one word from the symbol kinds below (or a language's own,
	// lowercase, from its profile).
	Kind string `json:"kind"`
	// Name is the bare name as written (f, Server, ServeHTTP).
	Name string `json:"name"`
	// QualName is the name qualified by its enclosing definitions and the
	// file's package, with the language's own separators (profile
	// scope.separators): pkg.Server.ServeHTTP, ns::A::f.
	QualName string `json:"qualName"`
	// Signature is the declaration line(s) up to the body, whitespace
	// normalized; "" when the kind has none.
	Signature string `json:"signature,omitempty"`
	// Doc is the attached doc comment with comment markers stripped.
	Doc string `json:"doc,omitempty"`
	// Start and End span the whole definition, doc comment excluded.
	Start Pos `json:"start"`
	End   Pos `json:"end"`
	// Parent is the ID of the enclosing symbol (a method's type, a field's
	// struct); "" at top level.
	Parent string `json:"parent,omitempty"`
	// Exported reports whether the symbol is visible outside its package or
	// module by the language's rule (the profile's scope.exported).
	Exported bool `json:"exported,omitempty"`
}

// Symbol kinds shared by the languages. A profile may map a capture to
// another lowercase word (table, trigger, selector, functor, ...); readers
// treat unknown kinds as opaque.
const (
	KindFunction  = "function"
	KindMethod    = "method"
	KindClass     = "class"
	KindStruct    = "struct"
	KindUnion     = "union"
	KindInterface = "interface"
	KindTrait     = "trait"
	KindProtocol  = "protocol"
	KindEnum      = "enum"
	KindType      = "type"
	KindField     = "field"
	KindProperty  = "property"
	KindConst     = "const"
	KindVar       = "var"
	KindModule    = "module"
	KindNamespace = "namespace"
	KindPackage   = "package"
	KindMacro     = "macro"
	KindImpl      = "impl"
	KindExtension = "extension"
)

// Import is one import, include, use or require form.
type Import struct {
	// Spec is the imported path, module, package or header as written,
	// without quotes or angle brackets ("fmt", "./util", "stdio.h", "a.b.c").
	Spec string `json:"spec"`
	// Alias is the local name the import binds, when the form names one
	// (import f "fmt", import numpy as np); "." or "_" as the language
	// writes them.
	Alias string `json:"alias,omitempty"`
	// Names are the names the form imports explicitly (from m import a, b;
	// import {a, b} from "m"; use a::{b, c}); empty for a whole-module import.
	Names []string `json:"names,omitempty"`
	// Kind is the form's word when the language has several (include,
	// include_system for angle-bracket includes, import, require, use, using,
	// export, part); "" when it has one.
	Kind string `json:"kind,omitempty"`
	// Start is where the form begins.
	Start Pos `json:"start"`
}

// Ref is an unresolved reference: the resolver links it to a symbol later,
// following the language family's scope rules.
type Ref struct {
	// Name is the referenced name as written, without its qualifier.
	Name string `json:"name"`
	// Qualifier is what the name was reached through (pkg in pkg.F, the
	// receiver expression's text, A in A::f); "" for a bare name.
	Qualifier string `json:"qualifier,omitempty"`
	// Kind is one of the RefKinds.
	Kind string `json:"kind"`
	// Start is where the name begins.
	Start Pos `json:"start"`
	// Enclosing is the ID of the innermost symbol containing the reference;
	// "" at file level.
	Enclosing string `json:"enclosing,omitempty"`
}

// Reference kinds.
const (
	RefCall       = "call"
	RefType       = "type"
	RefValue      = "value"
	RefExtends    = "extends"
	RefImplements = "implements"
)

// RefKinds are the valid Ref.Kind values.
var RefKinds = []string{RefCall, RefType, RefValue, RefExtends, RefImplements}

// Chunk is a retrieval unit: a top-level function, method or type with its
// doc comment and signature (or one part of a large one, each repeating the
// signature), a Markdown section, or a window of lines.
type Chunk struct {
	// ID is unique within the file (see ChunkID).
	ID string `json:"id"`
	// Kind is one of the chunk kinds below.
	Kind string `json:"kind"`
	// SymbolID is the symbol the chunk belongs to; "" for sections and
	// windows.
	SymbolID string `json:"symbolId,omitempty"`
	// Start and End are the span of the file the chunk covers (Text may add
	// a repeated signature before it).
	Start Pos `json:"start"`
	End   Pos `json:"end"`
	// Text is the chunk's content as it is retrieved.
	Text string `json:"text"`
	// Hash is HashText(Text).
	Hash string `json:"hash"`
}

// Chunk kinds.
const (
	ChunkSymbol  = "symbol"  // a whole definition with its doc comment
	ChunkPart    = "part"    // one part of a large definition, signature repeated
	ChunkSection = "section" // a Markdown heading and its body
	ChunkWindow  = "window"  // a window of lines of a file indexed as text
)

// SymbolID returns the file-local ID of the n-th symbol (counting from 1 in
// source order) with this qualified name: the name itself for the first,
// name~n for later ones (overloads, a prototype and its definition, the
// branches of an #if).
func SymbolID(qualName string, n int) string {
	if n <= 1 {
		return qualName
	}
	return qualName + "~" + strconv.Itoa(n)
}

// ChunkID returns the ID of part (counting from 1) of the chunks of symbol
// id: id#part. Sections and windows, which have no symbol, use their first
// line: L<line>#part.
func ChunkID(symbolID string, startLine, part int) string {
	if symbolID == "" {
		symbolID = "L" + strconv.Itoa(startLine)
	}
	return symbolID + "#" + strconv.Itoa(part)
}

// HashText returns the hex SHA-256 of a chunk's text.
func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

var wordPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// validWord reports whether s is a kind word: lowercase letters, digits and
// underscores, starting with a letter.
func validWord(s string) bool { return wordPattern.MatchString(s) }

// Validate checks the invariants every producer of Facts keeps and every
// consumer relies on: unique IDs, parents, enclosing symbols and chunk
// symbols that exist, kinds that are words, valid ordered positions and chunk
// hashes that match their text. It reports every violation found.
func (f *Facts) Validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if f.Lang != "" && f.Lang != Markdown && !validWord(f.Lang) {
		bad("lang %q is not a word", f.Lang)
	}
	if f.Dialect != "" && !validWord(f.Dialect) {
		bad("dialect %q is not a word", f.Dialect)
	}
	if f.ParseError != "" && (len(f.Symbols) > 0 || len(f.Imports) > 0 || len(f.Refs) > 0) {
		bad("parse error %q but symbols, imports or refs were extracted", f.ParseError)
	}
	ids := make(map[string]bool, len(f.Symbols))
	for i, s := range f.Symbols {
		switch {
		case s.ID == "":
			bad("symbol %d: empty id", i)
		case ids[s.ID]:
			bad("symbol %d: duplicate id %q", i, s.ID)
		}
		ids[s.ID] = true
		if s.Name == "" {
			bad("symbol %q: empty name", s.ID)
		}
		if !validWord(s.Kind) {
			bad("symbol %q: kind %q is not a word", s.ID, s.Kind)
		}
		checkSpan(bad, "symbol "+strconv.Quote(s.ID), s.Start, s.End)
	}
	for _, s := range f.Symbols {
		if s.Parent == "" {
			continue
		}
		if s.Parent == s.ID || !ids[s.Parent] {
			bad("symbol %q: parent %q is not another symbol of the file", s.ID, s.Parent)
		}
	}
	for i, imp := range f.Imports {
		if imp.Spec == "" {
			bad("import %d: empty spec", i)
		}
		if imp.Kind != "" && !validWord(imp.Kind) {
			bad("import %d: kind %q is not a word", i, imp.Kind)
		}
		if !imp.Start.valid() {
			bad("import %d: invalid position %+v", i, imp.Start)
		}
	}
	for i, r := range f.Refs {
		if r.Name == "" {
			bad("ref %d: empty name", i)
		}
		if !isRefKind(r.Kind) {
			bad("ref %d (%s): kind %q is not one of %s", i, r.Name, r.Kind, strings.Join(RefKinds, ", "))
		}
		if !r.Start.valid() {
			bad("ref %d (%s): invalid position %+v", i, r.Name, r.Start)
		}
		if r.Enclosing != "" && !ids[r.Enclosing] {
			bad("ref %d (%s): enclosing %q is not a symbol of the file", i, r.Name, r.Enclosing)
		}
	}
	chunks := make(map[string]bool, len(f.Chunks))
	for i, c := range f.Chunks {
		switch {
		case c.ID == "":
			bad("chunk %d: empty id", i)
		case chunks[c.ID]:
			bad("chunk %d: duplicate id %q", i, c.ID)
		}
		chunks[c.ID] = true
		if !validWord(c.Kind) {
			bad("chunk %q: kind %q is not a word", c.ID, c.Kind)
		}
		if c.SymbolID != "" && !ids[c.SymbolID] {
			bad("chunk %q: symbol %q is not a symbol of the file", c.ID, c.SymbolID)
		}
		checkSpan(bad, "chunk "+strconv.Quote(c.ID), c.Start, c.End)
		if c.Hash != HashText(c.Text) {
			bad("chunk %q: hash does not match its text", c.ID)
		}
	}
	return errors.Join(errs...)
}

func checkSpan(bad func(string, ...any), what string, start, end Pos) {
	if !start.valid() || !end.valid() {
		bad("%s: invalid span %+v-%+v", what, start, end)
		return
	}
	if end.Before(start) || end.Line < start.Line {
		bad("%s: ends (%s) before it starts (%s)", what, end, start)
	}
}

func isRefKind(k string) bool {
	for _, v := range RefKinds {
		if k == v {
			return true
		}
	}
	return false
}

// Clone returns a deep copy of f, so a cache can hand out facts its callers
// may change.
func (f Facts) Clone() Facts {
	f.Symbols = append([]Symbol(nil), f.Symbols...)
	f.Refs = append([]Ref(nil), f.Refs...)
	f.Chunks = append([]Chunk(nil), f.Chunks...)
	if f.Imports != nil {
		imports := make([]Import, len(f.Imports))
		for i, imp := range f.Imports {
			imp.Names = append([]string(nil), imp.Names...)
			imports[i] = imp
		}
		f.Imports = imports
	}
	return f
}
