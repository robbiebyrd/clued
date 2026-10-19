package codeindex

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// Writing a batch's files (plan 0012, "Per changed file in a layer"): each
// put file's facts come from the parse cache (parsed on a miss) and replace
// what its layer holds for the path; a deletion is a tombstone File; and the
// same path in the layers below records, in its hiddenBy, every layer above
// it in a chain (Layer.Parent) that replaces or deletes it.
//
// hiddenBy is a function of the selection alone: the hiddenBy of the file
// at path p in layer A lists the layers H of the plan, A excluded, that hold
// p (a file or a tombstone) and have A below them through Parent. The
// FileApplier recomputes it for every (layer, path) a batch can change: the
// put and removed paths with the same path in each layer below them (before
// and after the batch, so a moved Parent is covered too), and every path of
// a layer whose chain of parents changed. hiddenBy is written before the
// files of the batch (lower files hidden before the higher file appears,
// unhidden before it goes), so a batch interrupted half-way leaves its
// remaining puts and removes in the next batch's delta, which recomputes
// the same paths again.

// Defaults of a FileApplier.
const (
	// DefaultParseTimeout cuts a parse off; the file is then indexed as
	// text (plan 0012: "a parse is cut off after 2 s").
	DefaultParseTimeout = 2 * time.Second
	// MaxChunksPerFile caps the chunks written for one file; the rest of a
	// huge file stays searchable by its symbols' full text only.
	MaxChunksPerFile = 1000
	// TextWindowLines and TextWindowOverlap shape the line windows of a
	// file indexed as text.
	TextWindowLines   = 60
	TextWindowOverlap = 10
)

// FileApplier is the Applier that writes a batch's files into the graph:
// facts through the parse cache, ApplyFile, tombstones, RemoveFile and the
// hiddenBy lists of the layers below. A file the parser cannot handle (no
// parser, a parse error, invalid facts, a timeout) is indexed as text: its
// File with parse_error set and line-window chunks.
type FileApplier struct {
	// ParseTimeout bounds one parse (default DefaultParseTimeout). The
	// parse itself is not interrupted (ParseFunc takes no context): its
	// result is dropped and the file indexed as text.
	ParseTimeout time.Duration
}

var _ Applier = FileApplier{}

// Apply implements Applier.
func (a FileApplier) Apply(ctx context.Context, s *Service, b Batch) error {
	if b.Graph == nil {
		return errors.New("codeindex: apply needs a graph")
	}
	next, prev := indexPlan(b.Plan), indexPlan(b.Prev)

	puts := map[layerPath]FileSource{}
	removes := map[layerPath]bool{}
	affected := map[layerPath]bool{}
	below := func(layer, path string) {
		for _, id := range next.ancestors(layer) {
			affected[layerPath{id, path}] = true
		}
		for _, id := range prev.ancestors(layer) {
			affected[layerPath{id, path}] = true
		}
	}
	for _, ld := range b.Delta.Layers {
		id := ld.Layer.ID
		for _, f := range ld.Put {
			puts[layerPath{id, f.Path}] = f
			below(id, f.Path)
		}
		for _, p := range ld.Remove {
			removes[layerPath{id, p}] = true
			below(id, p)
		}
	}
	for _, l := range b.Plan.Layers {
		id := l.Layer.ID
		if _, had := prev.layers[id]; !had || slices.Equal(next.ancestors(id), prev.ancestors(id)) {
			continue
		}
		for _, f := range l.Files {
			below(id, f.Path)
		}
		if old, ok := prev.layers[id]; ok {
			for _, f := range old.Files {
				below(id, f.Path)
			}
		}
	}

	// Hide and unhide the files that stay in place first.
	pending := make([]layerPath, 0, len(affected))
	for k := range affected {
		if _, put := puts[k]; put || removes[k] || !next.holds(k) || !prev.holds(k) {
			continue
		}
		pending = append(pending, k)
	}
	slices.SortFunc(pending, compareLayerPath)
	for _, k := range pending {
		if err := b.Graph.SetHidden(ctx, k.layer, k.path, next.hiddenBy(k)); err != nil {
			return fmt.Errorf("codeindex: hide %s in %s: %w", k.path, k.layer, err)
		}
	}

	// Then the files themselves, lower layers first.
	for _, ld := range b.Delta.Layers {
		id := ld.Layer.ID
		for _, p := range ld.Remove {
			if err := b.Graph.RemoveFile(ctx, id, p); err != nil {
				return fmt.Errorf("codeindex: remove %s from %s: %w", p, id, err)
			}
		}
		for _, f := range ld.Put {
			ff, err := a.fileFacts(ctx, s, b.Code, id, f)
			if err != nil {
				return err
			}
			ff.File.HiddenBy = next.hiddenBy(layerPath{id, f.Path})
			if err := b.Graph.ApplyFile(ctx, ff); err != nil {
				return fmt.Errorf("codeindex: apply %s to %s: %w", f.Path, id, err)
			}
		}
	}
	return nil
}

// fileFacts builds what ApplyFile writes for one put file.
func (a FileApplier) fileFacts(ctx context.Context, s *Service, store code.Store, layer string, f FileSource) (codegraph.FileFacts, error) {
	if f.Deleted {
		return GraphFacts(layer, f, codeparse.Facts{}), nil
	}
	if f.SHA256 == "" {
		// The content is unknown: the path exists (and hides the layers
		// below) but has nothing to parse.
		return GraphFacts(layer, f, codeparse.Facts{}), nil
	}
	facts, err := a.parse(ctx, s, store, f)
	if err != nil {
		return codegraph.FileFacts{}, err
	}
	return GraphFacts(layer, f, facts), nil
}

// parse returns a file version's facts from the parse cache, parsing on a
// miss. Facts that parsed (or failed to parse: the parser is a pure
// function, so its error is too) are cached; a timeout or a missing parser
// gives text facts that are not, so the next sighting of the content tries
// again.
func (a FileApplier) parse(ctx context.Context, s *Service, store code.Store, f FileSource) (codeparse.Facts, error) {
	blob, err := store.Blob(ctx, f.SHA256)
	if err != nil {
		return codeparse.Facts{}, fmt.Errorf("codeindex: blob of %s: %w", f.Path, err)
	}
	if s.opts.Parse == nil {
		return TextFacts(blob.Content, codeparse.ParseNoGrammar), nil
	}
	version := codeparse.Version
	if s.opts.ParseVersion != nil {
		version = s.opts.ParseVersion(f.Path, blob.Content)
	}
	key := codeparse.Key{SHA256: f.SHA256, Version: version}
	if err := key.Validate(); err != nil {
		return codeparse.Facts{}, fmt.Errorf("codeindex: parse key of %s: %w", f.Path, err)
	}
	if facts, ok, err := s.cache.Get(ctx, key); err != nil {
		return codeparse.Facts{}, fmt.Errorf("codeindex: parse cache: %w", err)
	} else if ok {
		return facts, nil
	}
	facts, err := a.parseOnce(ctx, s.opts.Parse, f.Path, blob.Content)
	switch {
	case errors.Is(err, errParseTimeout):
		s.log.Debug("codeindex: parse timed out, indexed as text", "path", f.Path)
		return TextFacts(blob.Content, codeparse.ParseTimeout), nil
	case ctx.Err() != nil:
		return codeparse.Facts{}, ctx.Err()
	case err == nil:
		err = facts.Validate()
		if err != nil {
			err = fmt.Errorf("invalid facts: %w", err)
		}
	}
	if err != nil {
		s.log.Debug("codeindex: parse failed, indexed as text", "path", f.Path, "error", err)
		facts = TextFacts(blob.Content, codeparse.ParseFailed)
	} else if facts.ParseError != "" && len(facts.Chunks) == 0 {
		facts.Chunks = textWindows(blob.Content)
	}
	if err := s.cache.Put(ctx, key, facts); err != nil {
		s.log.Warn("codeindex: parse cache put", "path", f.Path, "error", err)
	}
	return facts, nil
}

var errParseTimeout = errors.New("parse timed out")

// parseOnce runs parse with the applier's timeout, turning a panic into an
// error.
func (a FileApplier) parseOnce(ctx context.Context, parse ParseFunc, path string, content []byte) (codeparse.Facts, error) {
	type result struct {
		facts codeparse.Facts
		err   error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if p := recover(); p != nil {
				r = result{err: fmt.Errorf("parser panic: %v", p)}
			}
			done <- r
		}()
		r.facts, r.err = parse(path, content)
	}()
	timeout := a.ParseTimeout
	if timeout <= 0 {
		timeout = DefaultParseTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.facts, r.err
	case <-timer.C:
		return codeparse.Facts{}, errParseTimeout
	case <-ctx.Done():
		return codeparse.Facts{}, ctx.Err()
	}
}

// TextFacts are the facts of a file indexed as text: no symbols, imports or
// references, line-window chunks, and cause recorded as the parse error
// (codeparse.ParseTimeout, ParseFailed, ParseNoGrammar).
func TextFacts(content []byte, cause string) codeparse.Facts {
	return codeparse.Facts{ParseError: cause, Chunks: textWindows(content)}
}

// textWindows cuts content into windows of TextWindowLines lines, each
// overlapping the previous one by TextWindowOverlap lines.
func textWindows(content []byte) []codeparse.Chunk {
	if len(content) == 0 {
		return nil
	}
	// starts[i] is the offset of line i+1; a last line without a newline
	// counts.
	starts := []int{0}
	for i, c := range content {
		if c == '\n' && i+1 < len(content) {
			starts = append(starts, i+1)
		}
	}
	lines := len(starts)
	var chunks []codeparse.Chunk
	for first := 0; first < lines && len(chunks) < MaxChunksPerFile; first += TextWindowLines - TextWindowOverlap {
		last := min(first+TextWindowLines, lines) - 1
		end := len(content)
		if last+1 < lines {
			end = starts[last+1]
		}
		text := content[starts[first]:end]
		lastStart := starts[last]
		lastLen := len(bytes.TrimSuffix(content[lastStart:end], []byte("\n")))
		chunks = append(chunks, codeparse.Chunk{
			ID:    codeparse.ChunkID("", first+1, 1),
			Kind:  codeparse.ChunkWindow,
			Start: codeparse.Pos{Line: first + 1, Offset: starts[first]},
			End:   codeparse.Pos{Line: last + 1, Column: lastLen, Offset: lastStart + lastLen},
			Text:  string(text),
			Hash:  codeparse.HashText(string(text)),
		})
		if last == lines-1 {
			break
		}
	}
	return chunks
}

// SymbolNodeID is the graph id of a file's symbol: the path and the
// file-local id (codeparse.SymbolID), so a symbol is unique within a layer
// and the same definition has the same id in every layer.
func SymbolNodeID(path, id string) string { return path + "#" + id }

// ChunkNodeID is the graph id of a file's chunk (codeparse.ChunkID), unique
// within a layer.
func ChunkNodeID(path, id string) string { return path + "#" + id }

// GraphFacts maps one file version's facts to what ApplyFile writes for it
// in layer: the File (the selection's blob, git blob id and language, the
// parser's language and package winning, parse_error when it was indexed
// as text), its symbols and chunks with graph ids, and its imports, one per
// package and alias. Positions become 1-based lines; chunks beyond
// MaxChunksPerFile are dropped. HiddenBy is left nil (Apply sets it).
func GraphFacts(layer string, f FileSource, facts codeparse.Facts) codegraph.FileFacts {
	if f.Deleted {
		return codegraph.FileFacts{File: codegraph.File{Layer: layer, Path: f.Path, Deleted: true}}
	}
	file := codegraph.File{Layer: layer, Path: f.Path, SHA256: f.SHA256, GitOID: f.GitOID,
		Lang: cmp.Or(facts.Lang, f.Lang), Package: facts.Package, ParseError: facts.ParseError != ""}
	ff := codegraph.FileFacts{File: file}
	if file.Package != "" {
		ff.PackageKind = PackageKind(file.Lang, "")
	}
	for _, s := range facts.Symbols {
		gs := codegraph.Symbol{Layer: layer, ID: SymbolNodeID(f.Path, s.ID), Kind: s.Kind, Name: s.Name,
			QualName: s.QualName, Signature: s.Signature, Doc: s.Doc, Path: f.Path,
			Start: s.Start.Line, End: s.End.Line, Exported: s.Exported}
		if s.Parent != "" {
			gs.Parent = SymbolNodeID(f.Path, s.Parent)
		}
		ff.Symbols = append(ff.Symbols, gs)
	}
	for i, c := range facts.Chunks {
		if i == MaxChunksPerFile {
			break
		}
		gc := codegraph.Chunk{Layer: layer, ID: ChunkNodeID(f.Path, c.ID), Kind: c.Kind, Path: f.Path,
			Start: c.Start.Line, End: c.End.Line, Hash: c.Hash}
		if c.SymbolID != "" {
			gc.Symbol = SymbolNodeID(f.Path, c.SymbolID)
		}
		ff.Chunks = append(ff.Chunks, gc)
	}
	type importKey struct{ pkg, alias string }
	seen := map[importKey]bool{}
	for _, im := range facts.Imports {
		k := importKey{im.Spec, im.Alias}
		if im.Spec == "" || seen[k] {
			continue
		}
		seen[k] = true
		ff.Imports = append(ff.Imports, codegraph.Import{Package: im.Spec, Kind: PackageKind(file.Lang, im.Kind), Alias: im.Alias})
	}
	return ff
}

// PackageKind is the kind of a Package node a file of lang declares or an
// import form (codeparse.Import.Kind) names: headers for includes and for
// the C family, otherwise the word the language's resolution family uses
// for its units.
func PackageKind(lang, importKind string) string {
	switch importKind {
	case "include", "include_system", "import_header":
		return codegraph.PackageKindHeader
	}
	l, _ := codeparse.LookupLanguage(lang)
	switch l.Family {
	case codeparse.FamilyCFamily:
		return codegraph.PackageKindHeader
	case codeparse.FamilyCSharp, codeparse.FamilyPHP:
		return codegraph.PackageKindNamespace
	case codeparse.FamilyRust:
		return codegraph.PackageKindCrate
	case codeparse.FamilySQL:
		return codegraph.PackageKindSchema
	case codeparse.FamilyPython, codeparse.FamilyJSTS, codeparse.FamilySwift, codeparse.FamilyRuby,
		codeparse.FamilyLua, codeparse.FamilyOCaml:
		return codegraph.PackageKindModule
	}
	return codegraph.PackageKindPackage
}

// layerPath is one path of one layer.
type layerPath struct{ layer, path string }

func compareLayerPath(a, b layerPath) int {
	return cmp.Or(cmp.Compare(a.layer, b.layer), cmp.Compare(a.path, b.path))
}

// planIndex answers the questions hiddenBy needs about a Plan.
type planIndex struct {
	layers map[string]LayerPlan
	// above maps a layer to the layers that have it below them, lowest
	// first.
	above map[string][]string
	order []string
}

func indexPlan(p Plan) planIndex {
	ix := planIndex{layers: make(map[string]LayerPlan, len(p.Layers)), above: map[string][]string{}}
	for _, l := range p.Layers {
		ix.layers[l.Layer.ID] = l
		ix.order = append(ix.order, l.Layer.ID)
	}
	for _, id := range ix.order {
		for _, a := range ix.ancestors(id) {
			ix.above[a] = append(ix.above[a], id)
		}
	}
	return ix
}

// ancestors are the layers below id through Parent, nearest first; a parent
// the plan does not hold ends the chain.
func (ix planIndex) ancestors(id string) []string {
	var out []string
	seen := map[string]bool{id: true}
	for cur := ix.layers[id].Layer.Parent; cur != "" && !seen[cur]; {
		l, ok := ix.layers[cur]
		if !ok {
			break
		}
		seen[cur] = true
		out = append(out, cur)
		cur = l.Layer.Parent
	}
	return out
}

// holds reports whether the plan's layer has a file (or tombstone) at path.
func (ix planIndex) holds(k layerPath) bool {
	l, ok := ix.layers[k.layer]
	if !ok {
		return false
	}
	_, ok = l.File(k.path)
	return ok
}

// hiddenBy is the hiddenBy list of the file at k: the layers above k's layer
// that hold its path, ordered by id; never nil, so ApplyFile replaces what
// the graph had.
func (ix planIndex) hiddenBy(k layerPath) []string {
	out := []string{}
	for _, h := range ix.above[k.layer] {
		if ix.holds(layerPath{h, k.path}) {
			out = append(out, h)
		}
	}
	slices.Sort(out)
	return out
}
