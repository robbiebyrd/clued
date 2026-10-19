package codegraph

// The graph schema, shared by every adapter: node labels, property names,
// edge types, the enumerations stored in properties, and the indexes an
// adapter creates. FalkorDB adapters build their parameterized queries from
// these constants; the memory adapter uses the same names when it reports
// what it holds, so the two stay interchangeable.

// Node labels.
const (
	LabelLayer   = "Layer"
	LabelFile    = "File"
	LabelPackage = "Package"
	LabelSymbol  = "Symbol"
	LabelChunk   = "Chunk"
)

// Node properties. A property name is shared by every label that carries it
// (layer, path, kind, start, end, …).
const (
	PropID        = "id"
	PropKind      = "kind"
	PropParent    = "parent"
	PropOwner     = "owner"
	PropRef       = "ref"
	PropCommit    = "commit"
	PropUpdated   = "updated"
	PropLayer     = "layer"
	PropPath      = "path"
	PropSHA256    = "sha256"
	PropGitOID    = "git_oid"
	PropLang      = "lang"
	PropPackage   = "package"
	PropHiddenBy  = "hiddenBy"
	PropDeleted   = "deleted"
	PropParseErr  = "parse_error"
	PropName      = "name"
	PropQualName  = "qualName"
	PropSignature = "signature"
	PropDoc       = "doc"
	PropStart     = "start"
	PropEnd       = "end"
	PropExported  = "exported"
	PropSymbol    = "symbol"
	PropHash      = "hash"
)

// Edge properties.
const (
	PropAlias      = "alias"
	PropConfidence = "confidence"
	PropCount      = "count"
	PropLine       = "line"
)

// EdgeType is a relationship type in the graph.
type EdgeType string

// Edge types. CONTAINS, IN_PACKAGE and IMPORTS are structural: ApplyFile
// writes them from a file's facts. CALLS, REFERENCES, IMPLEMENTS and EXTENDS
// are resolved Symbol→Symbol edges written by PutEdges, each carrying the
// layer that resolved it.
const (
	EdgeContains   EdgeType = "CONTAINS"   // Layer→File, File→Symbol, Symbol→Symbol, File→Chunk, Symbol→Chunk
	EdgeInPackage  EdgeType = "IN_PACKAGE" // File→Package
	EdgeImports    EdgeType = "IMPORTS"    // File→Package (alias), C-family includes to headers too
	EdgeCalls      EdgeType = "CALLS"      // Symbol→Symbol
	EdgeReferences EdgeType = "REFERENCES" // Symbol→Symbol
	EdgeImplements EdgeType = "IMPLEMENTS" // Symbol→Symbol
	EdgeExtends    EdgeType = "EXTENDS"    // Symbol→Symbol
)

// ResolvedEdgeTypes are the edge types PutEdges accepts.
var ResolvedEdgeTypes = []EdgeType{EdgeCalls, EdgeReferences, EdgeImplements, EdgeExtends}

// Resolved reports whether t is a Symbol→Symbol edge type PutEdges writes.
func (t EdgeType) Resolved() bool {
	for _, r := range ResolvedEdgeTypes {
		if t == r {
			return true
		}
	}
	return false
}

// Layer kinds.
const (
	LayerBase     = "base"
	LayerBranch   = "branch"
	LayerWorktree = "wt"
)

// Package kinds.
const (
	PackageKindPackage   = "package"
	PackageKindModule    = "module"
	PackageKindNamespace = "namespace"
	PackageKindCrate     = "crate"
	PackageKindHeader    = "header"
	PackageKindSchema    = "schema"
)

// PackageKinds lists the valid Package kinds.
var PackageKinds = []string{
	PackageKindPackage, PackageKindModule, PackageKindNamespace,
	PackageKindCrate, PackageKindHeader, PackageKindSchema,
}

// Symbol kinds. Parsers may emit other kinds (a language's own word for a
// definition); these are the ones every tool understands.
const (
	SymbolFunction  = "function"
	SymbolMethod    = "method"
	SymbolType      = "type"
	SymbolInterface = "interface"
	SymbolClass     = "class"
	SymbolStruct    = "struct"
	SymbolField     = "field"
	SymbolConst     = "const"
	SymbolVar       = "var"
	SymbolEnum      = "enum"
	SymbolModule    = "module"
	SymbolMacro     = "macro"
)

// Confidence of a resolved edge, best first.
const (
	ConfidenceExact     = "exact"
	ConfidenceQualified = "qualified"
	ConfidenceName      = "name"
)

// ConfidenceRank orders confidences for ranking: lower is better; an
// unknown confidence ranks last.
func ConfidenceRank(c string) int {
	switch c {
	case ConfidenceExact:
		return 0
	case ConfidenceQualified:
		return 1
	case ConfidenceName:
		return 2
	}
	return 3
}

// IndexKind says how an index is built.
type IndexKind string

// Index kinds.
const (
	IndexRange    IndexKind = "range"
	IndexFullText IndexKind = "fulltext"
)

// Index is one index an adapter creates on a node label.
type Index struct {
	Kind  IndexKind
	Label string
	Props []string
}

// Indexes are the indexes every graph has: range indexes for the key
// lookups and the name searches, full-text over a symbol's name, signature
// and doc comment for SearchText. There is no vector index.
var Indexes = []Index{
	{Kind: IndexRange, Label: LabelLayer, Props: []string{PropID}},
	{Kind: IndexRange, Label: LabelFile, Props: []string{PropLayer, PropPath}},
	{Kind: IndexRange, Label: LabelPackage, Props: []string{PropLayer, PropName}},
	{Kind: IndexRange, Label: LabelSymbol, Props: []string{PropName}},
	{Kind: IndexRange, Label: LabelSymbol, Props: []string{PropQualName}},
	{Kind: IndexRange, Label: LabelSymbol, Props: []string{PropLayer, PropID}},
	{Kind: IndexRange, Label: LabelChunk, Props: []string{PropLayer, PropID}},
	{Kind: IndexFullText, Label: LabelSymbol, Props: []string{PropName, PropSignature, PropDoc}},
}
