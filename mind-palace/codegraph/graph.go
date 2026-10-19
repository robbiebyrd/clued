// Package codegraph is the port of the code graph: one graph per namespaced
// project (its key is the namespace, u<user>__<slug> or w<workspace>__<slug>,
// so tenancy is physical) holding base, branch and session-overlay layers
// whose visibility is resolved per read through a View. The graph is a
// derived cache of the collected code and can be dropped and rebuilt at any
// time; adapters persist what they are given and hold no indexing rules
// (those live in codeindex). The FalkorDB adapter (codegraph/falkor) and the
// memory adapter (codegraph/memgraph) both pass codegraph/codegraphtest.
package codegraph

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Errors every adapter returns (possibly wrapped), so callers can tell them
// apart with errors.Is.
var (
	// ErrNotFound: a symbol, file or layer the call names does not exist
	// (or is not visible in the view).
	ErrNotFound = errors.New("not found")
	// ErrUnknownLayer: a write names a layer PutLayer never created.
	ErrUnknownLayer = errors.New("unknown layer")
	// ErrInvalid: the input breaks the contract (bad layer id, missing key,
	// edge from the wrong layer, depth over MaxDepth, …).
	ErrInvalid = errors.New("invalid")
	// ErrUnavailable: the backend cannot be reached; the indexer backs off
	// and read routes answer 503.
	ErrUnavailable = errors.New("graph unavailable")
)

// Limits shared by every adapter.
const (
	// DefaultLimit applies when a read's Limit is zero.
	DefaultLimit = 50
	// MaxLimit caps every read's Limit.
	MaxLimit = 500
	// MaxDepth caps Neighbors' Depth.
	MaxDepth = 3
)

// Opener opens a project's graph by its key. Opening does not create
// anything visible: an adapter may create the graph and its Indexes lazily
// on the first write.
type Opener interface {
	Open(ctx context.Context, key string) (Graph, error)
	Close() error
}

// keyRe matches a graph key: a namespace such as u0123456789abcdef__slug.
var keyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.\-]{0,254}$`)

// ValidKey reports whether key can name a graph. Adapters refuse any other
// key with ErrInvalid before it reaches the backend.
func ValidKey(key string) error {
	if !keyRe.MatchString(key) {
		return fmt.Errorf("%w: graph key %q", ErrInvalid, key)
	}
	return nil
}

// Graph is one project's code graph. Writes name a layer; reads take the
// View they answer for and return only what is visible in it (see View).
type Graph interface {
	// Key is the graph's key (the project namespace).
	Key() string

	// PutLayer creates or updates a layer node.
	PutLayer(ctx context.Context, l Layer) error
	// Layers lists the graph's layers ordered by id.
	Layers(ctx context.Context) ([]Layer, error)
	// Files lists the files of one layer ordered by path, tombstones
	// included (consistency checks compare them with the store).
	Files(ctx context.Context, layer string) ([]File, error)

	// ApplyFile replaces everything the file's layer holds for its path
	// (the File, its Symbols, Chunks, CONTAINS, IN_PACKAGE and IMPORTS
	// edges) with f, creating the packages it names in that layer. Resolved
	// edges, whatever their layer, are removed when an endpoint no longer
	// exists after the replace; those whose endpoints keep their (layer, id)
	// stay until PutEdges replaces them. The File's HiddenBy is kept from
	// the stored file when f leaves it nil. ErrUnknownLayer when the layer
	// was never put.
	ApplyFile(ctx context.Context, f FileFacts) error
	// RemoveFile deletes the file at path in layer with its symbols, chunks
	// and every edge touching them. It does not touch other layers' hiddenBy
	// (the indexer calls SetHidden). Removing a missing file is not an error.
	RemoveFile(ctx context.Context, layer, path string) error
	// SetHidden replaces the hiddenBy list of the file at path in layer.
	// ErrNotFound when there is no such file.
	SetHidden(ctx context.Context, layer, path string, hiddenBy []string) error
	// PutEdges replaces, in layer, every resolved edge whose source is one
	// of from with edges. Each edge must carry Layer == layer, a source in
	// from and a resolved type; endpoints that do not exist make it
	// ErrNotFound and nothing is written. An empty edges clears them.
	PutEdges(ctx context.Context, layer string, from []NodeRef, edges []Edge) error
	// DropLayer removes a layer: its node, files, symbols, chunks and
	// packages, every edge it resolved or that touches its nodes, and its id
	// from every hiddenBy. Dropping a missing layer is not an error.
	DropLayer(ctx context.Context, layer string) error
	// Drop removes the whole graph.
	Drop(ctx context.Context) error

	// FindSymbols returns the visible symbols whose name or qualified name
	// equals q.Name exactly; qualified-name matches rank first.
	FindSymbols(ctx context.Context, view View, q SymbolQuery) ([]Symbol, error)
	// Symbol returns one visible symbol by id or qualified name, with its
	// file's language and its direct children. When several layers of the
	// view hold the key, the highest layer wins. ErrNotFound otherwise.
	Symbol(ctx context.Context, view View, key SymbolKey) (SymbolInfo, error)
	// Neighbors walks visible edges from a start node, up to q.Depth hops.
	Neighbors(ctx context.Context, view View, q NeighborQuery) ([]NeighborRow, error)
	// Outline returns the symbol trees of the visible files at a path or in
	// a package, with signatures and no bodies.
	Outline(ctx context.Context, view View, q OutlineQuery) ([]FileOutline, error)
	// SearchText runs a full-text query over the visible symbols' names,
	// signatures and doc comments, best score first.
	SearchText(ctx context.Context, view View, q TextQuery) ([]TextHit, error)
}

// Layer is a Layer node.
type Layer struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"`             // LayerBase, LayerBranch, LayerWorktree
	Parent  string    `json:"parent,omitempty"` // the layer below it
	Owner   string    `json:"owner,omitempty"`  // account id of an overlay's owner
	Ref     string    `json:"ref,omitempty"`
	Commit  string    `json:"commit,omitempty"`
	Updated time.Time `json:"updated"`
}

// Validate checks the id and that Kind agrees with it.
func (l Layer) Validate() error {
	p, err := ParseLayerID(l.ID)
	if err != nil {
		return err
	}
	if l.Kind != p.Kind {
		return fmt.Errorf("%w: layer %q has kind %q, its id says %q", ErrInvalid, l.ID, l.Kind, p.Kind)
	}
	if l.Parent != "" {
		if _, err := ParseLayerID(l.Parent); err != nil {
			return err
		}
	}
	return nil
}

// File is a File node, keyed by (Layer, Path).
type File struct {
	Layer      string   `json:"layer"`
	Path       string   `json:"path"`
	SHA256     string   `json:"sha256,omitempty"`
	GitOID     string   `json:"gitOid,omitempty"`
	Lang       string   `json:"lang,omitempty"`
	Package    string   `json:"package,omitempty"`  // name of the file's Package in the same layer
	HiddenBy   []string `json:"hiddenBy,omitempty"` // higher layers that replace or delete this path
	Deleted    bool     `json:"deleted,omitempty"`  // a tombstone: the path is deleted in this layer
	ParseError bool     `json:"parseError,omitempty"`
}

// Validate checks the key: a well-formed layer id and a clean, relative,
// slash-separated path.
func (f File) Validate() error {
	if _, err := ParseLayerID(f.Layer); err != nil {
		return err
	}
	return ValidPath(f.Path)
}

// ValidPath checks a file path: relative, slash-separated, clean, inside the
// project.
func ValidPath(p string) error {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") ||
		strings.Contains(p, "\x00") {
		return fmt.Errorf("%w: path %q", ErrInvalid, p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: path %q", ErrInvalid, p)
		}
	}
	return nil
}

// Package is a Package node, keyed by (Layer, Name).
type Package struct {
	Layer string `json:"layer"`
	Name  string `json:"name"`
	Kind  string `json:"kind"` // one of PackageKinds
}

// Symbol is a Symbol node, keyed by (Layer, ID). An id is unique within a
// layer and names the same definition across layers (the parser derives it
// from the path and the qualified name), which is what lets a higher layer
// replace it. Start and End are 1-based lines.
type Symbol struct {
	Layer     string `json:"layer"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	QualName  string `json:"qualName,omitempty"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Path      string `json:"path"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Exported  bool   `json:"exported,omitempty"`
	// Parent is the id of the enclosing symbol (a method's type), empty at
	// the top level; ApplyFile writes it as Symbol→Symbol CONTAINS.
	Parent string `json:"parent,omitempty"`
}

// Chunk is a Chunk node: a retrieval unit (a function with its signature,
// a part of a large body, a Markdown section, a window of text). Its text
// stays in the blob store; Hash is the chunk content's hash.
type Chunk struct {
	Layer string `json:"layer"`
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	// Symbol is the id of the symbol the chunk belongs to, if any (Symbol→
	// Chunk CONTAINS); every chunk also hangs off its file.
	Symbol string `json:"symbol,omitempty"`
	Path   string `json:"path"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Hash   string `json:"hash,omitempty"`
}

// Import is one IMPORTS edge from a file to a package of its layer.
type Import struct {
	Package string `json:"package"`
	Kind    string `json:"kind,omitempty"` // the package's kind when ApplyFile creates it
	Alias   string `json:"alias,omitempty"`
}

// FileFacts is what ApplyFile writes for one path of one layer: the file
// and the nodes and structural edges hanging off it. A tombstone carries
// File.Deleted and nothing else.
type FileFacts struct {
	File        File     `json:"file"`
	PackageKind string   `json:"packageKind,omitempty"` // kind of File.Package when it is created
	Imports     []Import `json:"imports,omitempty"`
	Symbols     []Symbol `json:"symbols,omitempty"`
	Chunks      []Chunk  `json:"chunks,omitempty"`
}

// Validate checks that every node belongs to the file's layer and path, ids
// are unique and set, parents and chunk symbols exist, and a tombstone holds
// nothing.
func (ff FileFacts) Validate() error {
	f := ff.File
	if err := f.Validate(); err != nil {
		return err
	}
	if f.Deleted && (len(ff.Symbols) > 0 || len(ff.Chunks) > 0 || len(ff.Imports) > 0 || f.Package != "") {
		return fmt.Errorf("%w: tombstone %s:%s carries facts", ErrInvalid, f.Layer, f.Path)
	}
	syms := make(map[string]bool, len(ff.Symbols))
	for _, s := range ff.Symbols {
		if s.ID == "" || s.Name == "" {
			return fmt.Errorf("%w: symbol in %s needs an id and a name", ErrInvalid, f.Path)
		}
		if s.Layer != f.Layer || s.Path != f.Path {
			return fmt.Errorf("%w: symbol %q is not in %s:%s", ErrInvalid, s.ID, f.Layer, f.Path)
		}
		if syms[s.ID] {
			return fmt.Errorf("%w: symbol %q appears twice in %s", ErrInvalid, s.ID, f.Path)
		}
		syms[s.ID] = true
	}
	for _, s := range ff.Symbols {
		if s.Parent != "" && (!syms[s.Parent] || s.Parent == s.ID) {
			return fmt.Errorf("%w: symbol %q has parent %q outside %s", ErrInvalid, s.ID, s.Parent, f.Path)
		}
	}
	chunks := make(map[string]bool, len(ff.Chunks))
	for _, c := range ff.Chunks {
		if c.ID == "" {
			return fmt.Errorf("%w: chunk in %s needs an id", ErrInvalid, f.Path)
		}
		if c.Layer != f.Layer || c.Path != f.Path {
			return fmt.Errorf("%w: chunk %q is not in %s:%s", ErrInvalid, c.ID, f.Layer, f.Path)
		}
		if chunks[c.ID] {
			return fmt.Errorf("%w: chunk %q appears twice in %s", ErrInvalid, c.ID, f.Path)
		}
		if c.Symbol != "" && !syms[c.Symbol] {
			return fmt.Errorf("%w: chunk %q belongs to unknown symbol %q", ErrInvalid, c.ID, c.Symbol)
		}
		chunks[c.ID] = true
	}
	for _, im := range ff.Imports {
		if im.Package == "" {
			return fmt.Errorf("%w: import without a package in %s", ErrInvalid, f.Path)
		}
	}
	return nil
}

// NodeRef names a symbol: its layer and id.
type NodeRef struct {
	Layer string `json:"layer"`
	ID    string `json:"id"`
}

// Edge is a resolved Symbol→Symbol edge. Layer is the layer whose view
// resolved it, which may be higher than both endpoints' layers (a base
// caller re-resolved to an overlay's renamed symbol).
type Edge struct {
	Type       EdgeType `json:"type"`
	Layer      string   `json:"layer"`
	From       NodeRef  `json:"from"`
	To         NodeRef  `json:"to"`
	Confidence string   `json:"confidence"` // ConfidenceExact, ConfidenceQualified, ConfidenceName
	Count      int      `json:"count"`      // references from From to To of this type
	Line       int      `json:"line"`       // the first one's line in From's file
}

// Validate checks the type, confidence and keys.
func (e Edge) Validate() error {
	if !e.Type.Resolved() {
		return fmt.Errorf("%w: edge type %q", ErrInvalid, e.Type)
	}
	if ConfidenceRank(e.Confidence) > ConfidenceRank(ConfidenceName) {
		return fmt.Errorf("%w: edge confidence %q", ErrInvalid, e.Confidence)
	}
	for _, id := range []string{e.Layer, e.From.Layer, e.To.Layer} {
		if _, err := ParseLayerID(id); err != nil {
			return err
		}
	}
	if e.From.ID == "" || e.To.ID == "" {
		return fmt.Errorf("%w: edge endpoints need ids", ErrInvalid)
	}
	if e.Count < 0 || e.Line < 0 {
		return fmt.Errorf("%w: edge count and line must not be negative", ErrInvalid)
	}
	return nil
}

// ValidateEdges checks PutEdges' input: a valid layer, sources set, and
// every edge valid, in that layer and from one of from.
func ValidateEdges(layer string, from []NodeRef, edges []Edge) error {
	if _, err := ParseLayerID(layer); err != nil {
		return err
	}
	src := make(map[NodeRef]bool, len(from))
	for _, r := range from {
		if r.ID == "" {
			return fmt.Errorf("%w: source without an id", ErrInvalid)
		}
		if _, err := ParseLayerID(r.Layer); err != nil {
			return err
		}
		src[r] = true
	}
	for _, e := range edges {
		if err := e.Validate(); err != nil {
			return err
		}
		if e.Layer != layer {
			return fmt.Errorf("%w: edge of layer %q written to %q", ErrInvalid, e.Layer, layer)
		}
		if !src[e.From] {
			return fmt.Errorf("%w: edge from %s/%s, which is not a source of this write", ErrInvalid, e.From.Layer, e.From.ID)
		}
	}
	return nil
}

// Filter narrows a read to some symbol kinds, languages and path prefixes;
// an empty list is no filter. A path prefix matches the path itself or a
// directory ("cli" matches "cli/run.go", not "client.go").
type Filter struct {
	Kinds []string `json:"kinds,omitempty"`
	Langs []string `json:"langs,omitempty"`
	Paths []string `json:"paths,omitempty"`
}

// Match reports whether a symbol of kind, in a file of lang at path, passes.
func (f Filter) Match(kind, lang, path string) bool {
	if len(f.Kinds) > 0 && !contains(f.Kinds, kind) {
		return false
	}
	if len(f.Langs) > 0 && !contains(f.Langs, lang) {
		return false
	}
	if len(f.Paths) == 0 {
		return true
	}
	for _, p := range f.Paths {
		p = strings.TrimSuffix(p, "/")
		if p == "" || path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// EffectiveLimit is limit with DefaultLimit for zero and MaxLimit as cap.
func EffectiveLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	}
	return limit
}

// SymbolQuery is FindSymbols' input: an exact name or qualified name.
type SymbolQuery struct {
	Name string `json:"name"`
	Filter
	Limit int `json:"limit,omitempty"`
}

// SymbolKey names one symbol by ID or, when ID is empty, by QualName.
type SymbolKey struct {
	ID       string `json:"id,omitempty"`
	QualName string `json:"qualName,omitempty"`
}

// SymbolInfo is Symbol's result.
type SymbolInfo struct {
	Symbol   Symbol   `json:"symbol"`
	Lang     string   `json:"lang,omitempty"`
	Children []Symbol `json:"children,omitempty"` // direct children ordered by Start
	Chunks   []Chunk  `json:"chunks,omitempty"`   // the symbol's chunks ordered by Start
}

// Relation is the direction and type a Neighbors walk follows.
type Relation string

// Relations.
const (
	RelCallers         Relation = "callers"         // incoming CALLS
	RelCallees         Relation = "callees"         // outgoing CALLS
	RelReferences      Relation = "references"      // incoming REFERENCES: who refers to it
	RelImplementations Relation = "implementations" // incoming IMPLEMENTS and EXTENDS: what implements or extends it
	RelImports         Relation = "imports"         // the packages a file imports
	RelImporters       Relation = "importers"       // the files that import a package
)

// Edges are the edge types the relation follows and whether the walk goes
// against their direction (incoming).
func (r Relation) Edges() (types []EdgeType, incoming bool, ok bool) {
	switch r {
	case RelCallers:
		return []EdgeType{EdgeCalls}, true, true
	case RelCallees:
		return []EdgeType{EdgeCalls}, false, true
	case RelReferences:
		return []EdgeType{EdgeReferences}, true, true
	case RelImplementations:
		return []EdgeType{EdgeImplements, EdgeExtends}, true, true
	case RelImports:
		return []EdgeType{EdgeImports}, false, true
	case RelImporters:
		return []EdgeType{EdgeImports}, true, true
	}
	return nil, false, false
}

// NeighborQuery is Neighbors' input. Symbol relations start at the visible
// symbol ID; imports starts at the visible file Path; importers at the
// Package name (in any visible layer). Depth 0 means 1; imports and
// importers are one hop whatever the depth.
type NeighborQuery struct {
	ID       string   `json:"id,omitempty"`
	Path     string   `json:"path,omitempty"`
	Package  string   `json:"package,omitempty"`
	Relation Relation `json:"relation"`
	Depth    int      `json:"depth,omitempty"`
	Limit    int      `json:"limit,omitempty"`
}

// Validate checks the relation, its start and the depth.
func (q NeighborQuery) Validate() error {
	if _, _, ok := q.Relation.Edges(); !ok {
		return fmt.Errorf("%w: relation %q", ErrInvalid, q.Relation)
	}
	if q.Depth < 0 || q.Depth > MaxDepth {
		return fmt.Errorf("%w: depth %d (at most %d)", ErrInvalid, q.Depth, MaxDepth)
	}
	switch q.Relation {
	case RelImports:
		return ValidPath(q.Path)
	case RelImporters:
		if q.Package == "" {
			return fmt.Errorf("%w: importers needs a package", ErrInvalid)
		}
	default:
		if q.ID == "" {
			return fmt.Errorf("%w: %s needs a symbol id", ErrInvalid, q.Relation)
		}
	}
	return nil
}

// NodeSummary is one end of a NeighborRow: a symbol, a file or a package.
type NodeSummary struct {
	Label    string `json:"label"` // LabelSymbol, LabelFile or LabelPackage
	Layer    string `json:"layer"`
	ID       string `json:"id,omitempty"` // symbols
	Kind     string `json:"kind,omitempty"`
	Name     string `json:"name"` // a symbol's or package's name, a file's path
	QualName string `json:"qualName,omitempty"`
	Path     string `json:"path,omitempty"`
	Start    int    `json:"start,omitempty"`
}

// NeighborRow is one visible edge of a Neighbors walk, in the edge's own
// direction (From calls To, whichever end the walk started from). Path and
// Line locate the reference: From's file and the edge's first line.
type NeighborRow struct {
	From       NodeSummary `json:"from"`
	Relation   Relation    `json:"relation"`
	Type       EdgeType    `json:"type"`
	To         NodeSummary `json:"to"`
	Path       string      `json:"path"`
	Line       int         `json:"line,omitempty"`
	Confidence string      `json:"confidence,omitempty"`
	Alias      string      `json:"alias,omitempty"` // imports
	Depth      int         `json:"depth"`           // 1 for the start's direct neighbours
}

// OutlineQuery is Outline's input: a file path or a package name.
type OutlineQuery struct {
	Path    string `json:"path,omitempty"`
	Package string `json:"package,omitempty"`
}

// Validate checks that exactly one of Path and Package is set.
func (q OutlineQuery) Validate() error {
	if (q.Path == "") == (q.Package == "") {
		return fmt.Errorf("%w: outline needs a path or a package", ErrInvalid)
	}
	if q.Path != "" {
		return ValidPath(q.Path)
	}
	return nil
}

// OutlineNode is a symbol and its children, ordered by Start.
type OutlineNode struct {
	Symbol   Symbol        `json:"symbol"`
	Children []OutlineNode `json:"children,omitempty"`
}

// FileOutline is one file's symbol tree.
type FileOutline struct {
	File    File          `json:"file"`
	Symbols []OutlineNode `json:"symbols"`
}

// BuildOutline arranges a file's symbols into trees by Parent, ordered by
// Start then ID; a symbol whose parent is missing is a root.
func BuildOutline(syms []Symbol) []OutlineNode {
	byParent := make(map[string][]Symbol)
	ids := make(map[string]bool, len(syms))
	for _, s := range syms {
		ids[s.ID] = true
	}
	for _, s := range syms {
		p := s.Parent
		if !ids[p] {
			p = ""
		}
		byParent[p] = append(byParent[p], s)
	}
	var build func(parent string, seen map[string]bool) []OutlineNode
	build = func(parent string, seen map[string]bool) []OutlineNode {
		kids := byParent[parent]
		sortSymbols(kids)
		out := make([]OutlineNode, 0, len(kids))
		for _, s := range kids {
			if seen[s.ID] {
				continue
			}
			seen[s.ID] = true
			out = append(out, OutlineNode{Symbol: s, Children: build(s.ID, seen)})
		}
		return out
	}
	return build("", map[string]bool{})
}

func sortSymbols(s []Symbol) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Start != s[j].Start {
			return s[i].Start < s[j].Start
		}
		return s[i].ID < s[j].ID
	})
}

// TextQuery is SearchText's input: free text matched against names,
// signatures and doc comments.
type TextQuery struct {
	Query string `json:"query"`
	Filter
	Limit int `json:"limit,omitempty"`
}

// TextHit is one SearchText result. Scores are comparable within one result
// only.
type TextHit struct {
	Symbol Symbol  `json:"symbol"`
	Score  float64 `json:"score"`
}
