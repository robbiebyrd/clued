// Package memgraph is the in-memory codegraph adapter: every graph lives in
// the Opener's maps and disappears with it. It exists so the indexer, the
// read routes and the tools can be tested without FalkorDB, and it passes
// codegraph/codegraphtest like the FalkorDB adapter, so the two answer every
// read the same way.
package memgraph

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/robbiebyrd/clued/mind-palace/codegraph"
)

// Opener holds the graphs of one process by key. Opening a key twice
// returns two handles over the same graph.
type Opener struct {
	mu     sync.Mutex
	graphs map[string]*graphData
}

var _ codegraph.Opener = (*Opener)(nil)

// New returns an empty Opener.
func New() *Opener {
	return &Opener{graphs: make(map[string]*graphData)}
}

// Open returns the graph named key, creating it empty on first use.
func (o *Opener) Open(ctx context.Context, key string) (codegraph.Graph, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := codegraph.ValidKey(key); err != nil {
		return nil, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	d, ok := o.graphs[key]
	if !ok {
		d = newGraphData()
		o.graphs[key] = d
	}
	return &Graph{key: key, d: d}, nil
}

// Close forgets nothing: the graphs live as long as the Opener does.
func (o *Opener) Close() error { return nil }

type fileKey struct{ layer, path string }

type pkgKey struct{ layer, name string }

type edgeKey struct {
	typ      codegraph.EdgeType
	layer    string
	from, to codegraph.NodeRef
}

// fileRec is a File with what hangs off it.
type fileRec struct {
	file    codegraph.File
	symbols []string // ids, in the order ApplyFile got them
	chunks  []codegraph.Chunk
	imports []codegraph.Import
}

type graphData struct {
	mu         sync.RWMutex
	layers     map[string]codegraph.Layer
	files      map[fileKey]*fileRec
	symbols    map[codegraph.NodeRef]codegraph.Symbol
	chunkOwner map[codegraph.NodeRef]string // chunk (layer, id) → its path
	packages   map[pkgKey]codegraph.Package
	edges      map[edgeKey]codegraph.Edge
}

func newGraphData() *graphData {
	d := &graphData{}
	d.reset()
	return d
}

func (d *graphData) reset() {
	d.layers = make(map[string]codegraph.Layer)
	d.files = make(map[fileKey]*fileRec)
	d.symbols = make(map[codegraph.NodeRef]codegraph.Symbol)
	d.chunkOwner = make(map[codegraph.NodeRef]string)
	d.packages = make(map[pkgKey]codegraph.Package)
	d.edges = make(map[edgeKey]codegraph.Edge)
}

// Graph is one key's graph.
type Graph struct {
	key string
	d   *graphData
}

var _ codegraph.Graph = (*Graph)(nil)

// Key is the graph's key.
func (g *Graph) Key() string { return g.key }

// PutLayer creates or replaces a layer node.
func (g *Graph) PutLayer(ctx context.Context, l codegraph.Layer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.Validate(); err != nil {
		return err
	}
	g.d.mu.Lock()
	defer g.d.mu.Unlock()
	g.d.layers[l.ID] = l
	return nil
}

// Layers lists the layers ordered by id.
func (g *Graph) Layers(ctx context.Context) ([]codegraph.Layer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	g.d.mu.RLock()
	defer g.d.mu.RUnlock()
	out := make([]codegraph.Layer, 0, len(g.d.layers))
	for _, l := range g.d.layers {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// Files lists one layer's files ordered by path, tombstones included.
func (g *Graph) Files(ctx context.Context, layer string) ([]codegraph.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := codegraph.ParseLayerID(layer); err != nil {
		return nil, err
	}
	g.d.mu.RLock()
	defer g.d.mu.RUnlock()
	if _, ok := g.d.layers[layer]; !ok {
		return nil, fmt.Errorf("%w: layer %q", codegraph.ErrNotFound, layer)
	}
	out := []codegraph.File{}
	for k, r := range g.d.files {
		if k.layer == layer {
			out = append(out, copyFile(r.file))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ApplyFile replaces what the file's layer holds for its path with ff.
func (g *Graph) ApplyFile(ctx context.Context, ff codegraph.FileFacts) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ff.Validate(); err != nil {
		return err
	}
	d := g.d
	d.mu.Lock()
	defer d.mu.Unlock()
	f := ff.File
	if _, ok := d.layers[f.Layer]; !ok {
		return fmt.Errorf("%w: %q", codegraph.ErrUnknownLayer, f.Layer)
	}
	for _, s := range ff.Symbols {
		if old, ok := d.symbols[codegraph.NodeRef{Layer: f.Layer, ID: s.ID}]; ok && old.Path != f.Path {
			return fmt.Errorf("%w: symbol %q of %s already belongs to %s", codegraph.ErrInvalid, s.ID, f.Path, old.Path)
		}
	}
	for _, c := range ff.Chunks {
		if p, ok := d.chunkOwner[codegraph.NodeRef{Layer: f.Layer, ID: c.ID}]; ok && p != f.Path {
			return fmt.Errorf("%w: chunk %q of %s already belongs to %s", codegraph.ErrInvalid, c.ID, f.Path, p)
		}
	}

	key := fileKey{f.Layer, f.Path}
	hidden := f.HiddenBy
	if hidden == nil {
		if old, ok := d.files[key]; ok {
			hidden = old.file.HiddenBy
		}
	}
	keep := make(map[string]bool, len(ff.Symbols))
	for _, s := range ff.Symbols {
		keep[s.ID] = true
	}
	if old, ok := d.files[key]; ok {
		for _, id := range old.symbols {
			if !keep[id] {
				d.deleteSymbol(codegraph.NodeRef{Layer: f.Layer, ID: id})
			}
		}
		for _, c := range old.chunks {
			delete(d.chunkOwner, codegraph.NodeRef{Layer: f.Layer, ID: c.ID})
		}
	}

	rec := &fileRec{file: copyFile(f)}
	rec.file.HiddenBy = cloneStrings(hidden)
	for _, s := range ff.Symbols {
		d.symbols[codegraph.NodeRef{Layer: f.Layer, ID: s.ID}] = s
		rec.symbols = append(rec.symbols, s.ID)
	}
	rec.chunks = append([]codegraph.Chunk(nil), ff.Chunks...)
	for _, c := range ff.Chunks {
		d.chunkOwner[codegraph.NodeRef{Layer: f.Layer, ID: c.ID}] = f.Path
	}
	rec.imports = append([]codegraph.Import(nil), ff.Imports...)
	if f.Package != "" {
		d.ensurePackage(f.Layer, f.Package, ff.PackageKind)
	}
	for _, im := range ff.Imports {
		d.ensurePackage(f.Layer, im.Package, im.Kind)
	}
	d.files[key] = rec
	return nil
}

// ensurePackage creates the package when missing; an existing one keeps its
// kind. A package created without a kind is a PackageKindPackage.
func (d *graphData) ensurePackage(layer, name, kind string) {
	k := pkgKey{layer, name}
	if _, ok := d.packages[k]; ok {
		return
	}
	if kind == "" {
		kind = codegraph.PackageKindPackage
	}
	d.packages[k] = codegraph.Package{Layer: layer, Name: name, Kind: kind}
}

// deleteSymbol removes a symbol and every resolved edge touching it.
func (d *graphData) deleteSymbol(ref codegraph.NodeRef) {
	delete(d.symbols, ref)
	for k := range d.edges {
		if k.from == ref || k.to == ref {
			delete(d.edges, k)
		}
	}
}

// RemoveFile deletes the file at path in layer and what hangs off it.
func (g *Graph) RemoveFile(ctx context.Context, layer, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := (codegraph.File{Layer: layer, Path: path}).Validate(); err != nil {
		return err
	}
	d := g.d
	d.mu.Lock()
	defer d.mu.Unlock()
	key := fileKey{layer, path}
	rec, ok := d.files[key]
	if !ok {
		return nil
	}
	for _, id := range rec.symbols {
		d.deleteSymbol(codegraph.NodeRef{Layer: layer, ID: id})
	}
	for _, c := range rec.chunks {
		delete(d.chunkOwner, codegraph.NodeRef{Layer: layer, ID: c.ID})
	}
	delete(d.files, key)
	return nil
}

// SetHidden replaces the hiddenBy list of the file at path in layer.
func (g *Graph) SetHidden(ctx context.Context, layer, path string, hiddenBy []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := (codegraph.File{Layer: layer, Path: path}).Validate(); err != nil {
		return err
	}
	for _, h := range hiddenBy {
		if _, err := codegraph.ParseLayerID(h); err != nil {
			return err
		}
	}
	d := g.d
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.files[fileKey{layer, path}]
	if !ok {
		return fmt.Errorf("%w: file %s:%s", codegraph.ErrNotFound, layer, path)
	}
	rec.file.HiddenBy = cloneStrings(hiddenBy)
	return nil
}

// PutEdges replaces layer's resolved edges from the sources with edges.
func (g *Graph) PutEdges(ctx context.Context, layer string, from []codegraph.NodeRef, edges []codegraph.Edge) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := codegraph.ValidateEdges(layer, from, edges); err != nil {
		return err
	}
	d := g.d
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.layers[layer]; !ok {
		return fmt.Errorf("%w: %q", codegraph.ErrUnknownLayer, layer)
	}
	for _, e := range edges {
		for _, end := range []codegraph.NodeRef{e.From, e.To} {
			if _, ok := d.symbols[end]; !ok {
				return fmt.Errorf("%w: symbol %s/%s", codegraph.ErrNotFound, end.Layer, end.ID)
			}
		}
	}
	src := make(map[codegraph.NodeRef]bool, len(from))
	for _, r := range from {
		src[r] = true
	}
	for k := range d.edges {
		if k.layer == layer && src[k.from] {
			delete(d.edges, k)
		}
	}
	for _, e := range edges {
		d.edges[edgeKey{e.Type, e.Layer, e.From, e.To}] = e
	}
	return nil
}

// DropLayer removes a layer and everything it holds or resolved.
func (g *Graph) DropLayer(ctx context.Context, layer string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := codegraph.ParseLayerID(layer); err != nil {
		return err
	}
	d := g.d
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.layers, layer)
	for k, r := range d.files {
		if k.layer == layer {
			delete(d.files, k)
			continue
		}
		if contains(r.file.HiddenBy, layer) {
			var kept []string
			for _, h := range r.file.HiddenBy {
				if h != layer {
					kept = append(kept, h)
				}
			}
			r.file.HiddenBy = kept
		}
	}
	for k := range d.symbols {
		if k.Layer == layer {
			delete(d.symbols, k)
		}
	}
	for k := range d.chunkOwner {
		if k.Layer == layer {
			delete(d.chunkOwner, k)
		}
	}
	for k := range d.packages {
		if k.layer == layer {
			delete(d.packages, k)
		}
	}
	for k := range d.edges {
		if k.layer == layer || k.from.Layer == layer || k.to.Layer == layer {
			delete(d.edges, k)
		}
	}
	return nil
}

// Drop removes the whole graph; the key can be written again afterwards.
func (g *Graph) Drop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	g.d.mu.Lock()
	defer g.d.mu.Unlock()
	g.d.reset()
	return nil
}

// visibleSymbol returns the symbol at ref with its file when both are
// visible in view.
func (d *graphData) visibleSymbol(view codegraph.View, ref codegraph.NodeRef) (codegraph.Symbol, *fileRec, bool) {
	s, ok := d.symbols[ref]
	if !ok {
		return codegraph.Symbol{}, nil, false
	}
	r, ok := d.files[fileKey{s.Layer, s.Path}]
	if !ok || !view.FileVisible(r.file) {
		return codegraph.Symbol{}, nil, false
	}
	return s, r, true
}

// visibleSymbols returns the visible symbols that pass keep, one per id: when
// several layers of the view hold an id, only the highest one is visible
// and keep decides on it alone.
func (d *graphData) visibleSymbols(view codegraph.View, keep func(codegraph.Symbol, *fileRec) bool) []codegraph.Symbol {
	type cand struct {
		s codegraph.Symbol
		r *fileRec
	}
	best := make(map[string]cand)
	for ref := range d.symbols {
		if !view.Has(ref.Layer) {
			continue
		}
		s, r, ok := d.visibleSymbol(view, ref)
		if !ok {
			continue
		}
		if cur, ok := best[s.ID]; ok && view.Rank(cur.s.Layer) > view.Rank(s.Layer) {
			continue
		}
		best[s.ID] = cand{s, r}
	}
	out := make([]codegraph.Symbol, 0, len(best))
	for _, c := range best {
		if keep(c.s, c.r) {
			out = append(out, c.s)
		}
	}
	return out
}

func lessSymbol(a, b codegraph.Symbol) bool {
	if a.Path != b.Path {
		return a.Path < b.Path
	}
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Layer < b.Layer
}

// FindSymbols returns the visible symbols named q.Name, qualified-name
// matches first, each group ordered by path, start and id.
func (g *Graph) FindSymbols(ctx context.Context, view codegraph.View, q codegraph.SymbolQuery) ([]codegraph.Symbol, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if q.Name == "" {
		return nil, fmt.Errorf("%w: find symbols needs a name", codegraph.ErrInvalid)
	}
	g.d.mu.RLock()
	defer g.d.mu.RUnlock()
	out := g.d.visibleSymbols(view, func(s codegraph.Symbol, r *fileRec) bool {
		return (s.Name == q.Name || s.QualName == q.Name) && q.Filter.Match(s.Kind, r.file.Lang, s.Path)
	})
	sort.Slice(out, func(i, j int) bool {
		qi, qj := out[i].QualName == q.Name, out[j].QualName == q.Name
		if qi != qj {
			return qi
		}
		return lessSymbol(out[i], out[j])
	})
	return limit(out, q.Limit), nil
}

// Symbol returns one visible symbol by id or qualified name.
func (g *Graph) Symbol(ctx context.Context, view codegraph.View, key codegraph.SymbolKey) (codegraph.SymbolInfo, error) {
	if err := ctx.Err(); err != nil {
		return codegraph.SymbolInfo{}, err
	}
	if err := view.Validate(); err != nil {
		return codegraph.SymbolInfo{}, err
	}
	if key.ID == "" && key.QualName == "" {
		return codegraph.SymbolInfo{}, fmt.Errorf("%w: a symbol needs an id or a qualified name", codegraph.ErrInvalid)
	}
	d := g.d
	d.mu.RLock()
	defer d.mu.RUnlock()
	cands := d.visibleSymbols(view, func(s codegraph.Symbol, _ *fileRec) bool {
		if key.ID != "" {
			return s.ID == key.ID
		}
		return s.QualName == key.QualName
	})
	if len(cands) == 0 {
		k := key.ID
		if k == "" {
			k = key.QualName
		}
		return codegraph.SymbolInfo{}, fmt.Errorf("%w: symbol %q", codegraph.ErrNotFound, k)
	}
	sort.Slice(cands, func(i, j int) bool {
		ri, rj := view.Rank(cands[i].Layer), view.Rank(cands[j].Layer)
		if ri != rj {
			return ri > rj
		}
		return lessSymbol(cands[i], cands[j])
	})
	s := cands[0]
	r := d.files[fileKey{s.Layer, s.Path}]
	info := codegraph.SymbolInfo{Symbol: s, Lang: r.file.Lang}
	for _, id := range r.symbols {
		if c := d.symbols[codegraph.NodeRef{Layer: s.Layer, ID: id}]; c.Parent == s.ID {
			info.Children = append(info.Children, c)
		}
	}
	sort.Slice(info.Children, func(i, j int) bool { return lessSymbol(info.Children[i], info.Children[j]) })
	for _, c := range r.chunks {
		if c.Symbol == s.ID {
			info.Chunks = append(info.Chunks, c)
		}
	}
	sort.Slice(info.Chunks, func(i, j int) bool {
		if info.Chunks[i].Start != info.Chunks[j].Start {
			return info.Chunks[i].Start < info.Chunks[j].Start
		}
		return info.Chunks[i].ID < info.Chunks[j].ID
	})
	return info, nil
}

func symbolSummary(s codegraph.Symbol) codegraph.NodeSummary {
	return codegraph.NodeSummary{
		Label: codegraph.LabelSymbol, Layer: s.Layer, ID: s.ID, Kind: s.Kind,
		Name: s.Name, QualName: s.QualName, Path: s.Path, Start: s.Start,
	}
}

func fileSummary(f codegraph.File) codegraph.NodeSummary {
	return codegraph.NodeSummary{Label: codegraph.LabelFile, Layer: f.Layer, Name: f.Path, Path: f.Path}
}

func packageSummary(p codegraph.Package) codegraph.NodeSummary {
	return codegraph.NodeSummary{Label: codegraph.LabelPackage, Layer: p.Layer, Kind: p.Kind, Name: p.Name}
}

// Neighbors walks visible edges from the query's start. Each visible edge is
// a row once, at the depth of the hop that first reached it; rows are
// ordered by depth, then From, then To (path, start, id, name, layer), type
// and alias.
func (g *Graph) Neighbors(ctx context.Context, view codegraph.View, q codegraph.NeighborQuery) ([]codegraph.NeighborRow, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	d := g.d
	d.mu.RLock()
	defer d.mu.RUnlock()
	var rows []codegraph.NeighborRow
	switch q.Relation {
	case codegraph.RelImports:
		r := d.visibleFile(view, q.Path)
		if r == nil {
			return nil, fmt.Errorf("%w: file %q", codegraph.ErrNotFound, q.Path)
		}
		for _, im := range r.imports {
			p := d.packages[pkgKey{r.file.Layer, im.Package}]
			rows = append(rows, codegraph.NeighborRow{
				From: fileSummary(r.file), Relation: q.Relation, Type: codegraph.EdgeImports,
				To: packageSummary(p), Path: r.file.Path, Alias: im.Alias, Depth: 1,
			})
		}
	case codegraph.RelImporters:
		found := false
		for k := range d.packages {
			if k.name == q.Package && view.Has(k.layer) {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("%w: package %q", codegraph.ErrNotFound, q.Package)
		}
		for _, r := range d.files {
			if !view.FileVisible(r.file) {
				continue
			}
			for _, im := range r.imports {
				if im.Package != q.Package {
					continue
				}
				p := d.packages[pkgKey{r.file.Layer, im.Package}]
				rows = append(rows, codegraph.NeighborRow{
					From: fileSummary(r.file), Relation: q.Relation, Type: codegraph.EdgeImports,
					To: packageSummary(p), Path: r.file.Path, Alias: im.Alias, Depth: 1,
				})
			}
		}
	default:
		rows = d.walk(view, q)
		if rows == nil {
			return nil, fmt.Errorf("%w: symbol %q", codegraph.ErrNotFound, q.ID)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return lessRow(rows[i], rows[j]) })
	return limit(rows, q.Limit), nil
}

// visibleFile is the visible file at path in the highest layer of view that
// has one, or nil.
func (d *graphData) visibleFile(view codegraph.View, path string) *fileRec {
	for i := len(view) - 1; i >= 0; i-- {
		if r, ok := d.files[fileKey{view[i], path}]; ok && view.FileVisible(r.file) {
			return r
		}
	}
	return nil
}

// walk runs a symbol relation breadth first. It returns nil when the start
// is not visible and an empty, non-nil slice when it has no neighbours.
func (d *graphData) walk(view codegraph.View, q codegraph.NeighborQuery) []codegraph.NeighborRow {
	start, ok := d.highest(view, q.ID)
	if !ok {
		return nil
	}
	types, incoming, _ := q.Relation.Edges()
	follow := make(map[codegraph.EdgeType]bool, len(types))
	for _, t := range types {
		follow[t] = true
	}
	depth := q.Depth
	if depth == 0 {
		depth = 1
	}
	rows := []codegraph.NeighborRow{}
	seenNode := map[codegraph.NodeRef]bool{start: true}
	seenEdge := map[edgeKey]bool{}
	frontier := []codegraph.NodeRef{start}
	for hop := 1; hop <= depth && len(frontier) > 0; hop++ {
		at := make(map[codegraph.NodeRef]bool, len(frontier))
		for _, n := range frontier {
			at[n] = true
		}
		var next []codegraph.NodeRef
		for k, e := range d.edges {
			if !follow[k.typ] || seenEdge[k] {
				continue
			}
			near, far := k.from, k.to
			if incoming {
				near, far = k.to, k.from
			}
			if !at[near] {
				continue
			}
			from, _, fromOK := d.visibleSymbol(view, k.from)
			to, _, toOK := d.visibleSymbol(view, k.to)
			if !view.EdgeVisible(k.layer, fromOK, toOK) {
				continue
			}
			seenEdge[k] = true
			rows = append(rows, codegraph.NeighborRow{
				From: symbolSummary(from), Relation: q.Relation, Type: e.Type, To: symbolSummary(to),
				Path: from.Path, Line: e.Line, Confidence: e.Confidence, Depth: hop,
			})
			if !seenNode[far] {
				seenNode[far] = true
				next = append(next, far)
			}
		}
		frontier = next
	}
	return rows
}

// highest is the visible symbol id in the highest layer of view that holds it.
func (d *graphData) highest(view codegraph.View, id string) (codegraph.NodeRef, bool) {
	for i := len(view) - 1; i >= 0; i-- {
		ref := codegraph.NodeRef{Layer: view[i], ID: id}
		if _, _, ok := d.visibleSymbol(view, ref); ok {
			return ref, true
		}
	}
	return codegraph.NodeRef{}, false
}

func cmpNode(a, b codegraph.NodeSummary) int {
	switch {
	case a.Path != b.Path:
		return strings.Compare(a.Path, b.Path)
	case a.Start != b.Start:
		if a.Start < b.Start {
			return -1
		}
		return 1
	case a.ID != b.ID:
		return strings.Compare(a.ID, b.ID)
	case a.Name != b.Name:
		return strings.Compare(a.Name, b.Name)
	}
	return strings.Compare(a.Layer, b.Layer)
}

func lessRow(a, b codegraph.NeighborRow) bool {
	if a.Depth != b.Depth {
		return a.Depth < b.Depth
	}
	if c := cmpNode(a.From, b.From); c != 0 {
		return c < 0
	}
	if c := cmpNode(a.To, b.To); c != 0 {
		return c < 0
	}
	if a.Type != b.Type {
		return a.Type < b.Type
	}
	return a.Alias < b.Alias
}

// Outline returns the symbol trees of the visible files at q.Path or in
// q.Package, ordered by path then layer rank.
func (g *Graph) Outline(ctx context.Context, view codegraph.View, q codegraph.OutlineQuery) ([]codegraph.FileOutline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	d := g.d
	d.mu.RLock()
	defer d.mu.RUnlock()
	var recs []*fileRec
	for _, r := range d.files {
		if !view.FileVisible(r.file) {
			continue
		}
		if (q.Path != "" && r.file.Path == q.Path) || (q.Package != "" && r.file.Package == q.Package) {
			recs = append(recs, r)
		}
	}
	if len(recs) == 0 {
		what := q.Path
		if what == "" {
			what = "package " + q.Package
		}
		return nil, fmt.Errorf("%w: %s", codegraph.ErrNotFound, what)
	}
	sort.Slice(recs, func(i, j int) bool {
		if recs[i].file.Path != recs[j].file.Path {
			return recs[i].file.Path < recs[j].file.Path
		}
		return view.Rank(recs[i].file.Layer) < view.Rank(recs[j].file.Layer)
	})
	out := make([]codegraph.FileOutline, 0, len(recs))
	for _, r := range recs {
		syms := make([]codegraph.Symbol, 0, len(r.symbols))
		for _, id := range r.symbols {
			syms = append(syms, d.symbols[codegraph.NodeRef{Layer: r.file.Layer, ID: id}])
		}
		out = append(out, codegraph.FileOutline{File: copyFile(r.file), Symbols: codegraph.BuildOutline(syms)})
	}
	return out, nil
}

// stopwords are the words the full-text index drops (RediSearch's default
// list, which FalkorDB's full-text procedures use).
var stopwords = map[string]bool{
	"a": true, "is": true, "the": true, "an": true, "and": true, "are": true, "as": true,
	"at": true, "be": true, "but": true, "by": true, "for": true, "if": true, "in": true,
	"into": true, "it": true, "no": true, "not": true, "of": true, "on": true, "or": true,
	"such": true, "that": true, "their": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "to": true, "was": true, "will": true, "with": true,
}

// tokens splits text the way the full-text index does: lower-cased runs of
// letters, digits and underscores. withStar keeps '*' for prefix terms.
func tokens(text string, withStar bool) []string {
	return strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || (withStar && r == '*'))
	})
}

type term struct {
	word   string
	prefix bool
}

func (t term) count(toks []string) int {
	n := 0
	for _, w := range toks {
		if w == t.word || (t.prefix && strings.HasPrefix(w, t.word)) {
			n++
		}
	}
	return n
}

// SearchText matches every term of q.Query (a trailing '*' makes a prefix)
// against the visible symbols' names, signatures and doc comments. Every
// term must occur; the score weighs a name hit 3, a signature hit 2 and a
// doc hit 1. Results are best score first, then path, start and id.
func (g *Graph) SearchText(ctx context.Context, view codegraph.View, q codegraph.TextQuery) ([]codegraph.TextHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := view.Validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(q.Query) == "" {
		return nil, fmt.Errorf("%w: search needs a query", codegraph.ErrInvalid)
	}
	var terms []term
	for _, w := range tokens(q.Query, true) {
		prefix := strings.HasSuffix(w, "*")
		w = strings.Trim(w, "*")
		if w == "" || strings.Contains(w, "*") || (!prefix && stopwords[w]) {
			continue
		}
		terms = append(terms, term{word: w, prefix: prefix})
	}
	hits := []codegraph.TextHit{}
	if len(terms) == 0 {
		return hits, nil
	}
	g.d.mu.RLock()
	defer g.d.mu.RUnlock()
	scores := make(map[codegraph.NodeRef]float64)
	syms := g.d.visibleSymbols(view, func(s codegraph.Symbol, r *fileRec) bool {
		if !q.Filter.Match(s.Kind, r.file.Lang, s.Path) {
			return false
		}
		name, sig, doc := tokens(s.Name, false), tokens(s.Signature, false), tokens(s.Doc, false)
		score := 0.0
		for _, t := range terms {
			n, sg, dc := t.count(name), t.count(sig), t.count(doc)
			if n+sg+dc == 0 {
				return false
			}
			score += float64(3*n + 2*sg + dc)
		}
		scores[codegraph.NodeRef{Layer: s.Layer, ID: s.ID}] = score
		return true
	})
	for _, s := range syms {
		hits = append(hits, codegraph.TextHit{Symbol: s, Score: scores[codegraph.NodeRef{Layer: s.Layer, ID: s.ID}]})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		return lessSymbol(hits[i].Symbol, hits[j].Symbol)
	})
	return limit(hits, q.Limit), nil
}

func limit[T any](s []T, n int) []T {
	if s == nil {
		s = []T{}
	}
	if n = codegraph.EffectiveLimit(n); len(s) > n {
		s = s[:n]
	}
	return s
}

func copyFile(f codegraph.File) codegraph.File {
	f.HiddenBy = cloneStrings(f.HiddenBy)
	return f
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return append([]string(nil), s...)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
