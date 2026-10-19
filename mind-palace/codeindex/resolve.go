package codeindex

import (
	"cmp"
	"container/list"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/codeindex/resolvers"
	"github.com/robbiebyrd/clued/mind-palace/codeparse"
)

// Resolution (plan 0012, "Resolution"): after a batch's files are written,
// each reference (codeparse.Ref) of a changed file is linked to the
// definition it names in the view of the file's layer, and the edges are
// written with that layer's id (codegraph.Graph.PutEdges). The language
// family's resolver (codeindex/resolvers) names packages and imports; the
// engine walks the scopes in order and picks the target:
//
//  1. a bare name: the enclosing definitions of the reference in its file
//     (innermost first), the file's top level, the file's package, the
//     names imported explicitly, the packages imported unqualified, then,
//     when the family allows it, any definition of that name;
//  2. a qualified name whose qualifier's first part is an import: only the
//     packages it names (a top-level definition, then one whose qualified
//     name ends with the qualifier, then a member); none when the package
//     is not in the project;
//  3. another qualifier (a type, a receiver, an expression): a definition
//     whose qualified name ends with the qualifier and the name, then, when
//     the family allows it, a member of that name, nearest first.
//
// Confidence is exact for a single definition found by a scope or import
// rule, qualified for one found by its qualified name, and name for a
// member reached through an unknown qualifier, a name-only fallback, or
// any step that found several definitions (the best is kept by kind, then
// exported, then path).
//
// Each layer's edges cover the references its view resolves differently
// from the layer below: the files of the layer itself completely, and, for
// files of lower layers, the edges whose target differs from the parent
// view's (a base caller of a function an overlay changed points at the
// overlay's version in that overlay's views only). When a batch changes
// the definitions at a path, the references to those names (case folded)
// from every file visible in the views above are re-resolved; that
// re-resolution is bounded per batch (MaxRefs references) and the rest is
// carried, in memory, to the project's next batch (as is the whole work of
// a batch whose resolution failed). A lower file is re-resolved in a layer
// only when it refers to a name that layer defines or hides; a layer that
// changes which package a file belongs to without touching the names (a
// go.mod alone) is caught up by a rebuild. When a layer's view resolves a
// lower reference to another still-visible definition than the view below
// (a duplicate definition), both edges are visible in its view.

// Defaults of a Resolver.
const (
	// DefaultMaxRefs bounds the references re-resolved for dependents per
	// batch (plan 0012: "default 5,000 references").
	DefaultMaxRefs = 5000
	// DefaultFactsCacheSize bounds the files whose facts the Resolver keeps
	// in memory between batches.
	DefaultFactsCacheSize = 50000
)

// ResolverOptions configure a Resolver.
type ResolverOptions struct {
	// Files writes the batch's files before resolution (default
	// FileApplier{}).
	Files Applier
	// MaxRefs bounds dependents' re-resolution per batch (default
	// DefaultMaxRefs).
	MaxRefs int
	// FactsCacheSize bounds the in-memory facts (default
	// DefaultFactsCacheSize).
	FactsCacheSize int
	// Profiles are the languages' profiles, handed to the resolvers with
	// each file; nil when none are loaded.
	Profiles codeparse.Profiles
}

// Resolver is the Applier that writes a batch's files (Files) and then
// resolves references into per-layer edges:
//
//	codeindex.New(codeindex.Options{Apply: codeindex.NewResolver(codeindex.ResolverOptions{}), …})
//
// It is safe for concurrent use by a Service's workers.
type Resolver struct {
	opts  ResolverOptions
	facts *factsCache

	mu      sync.Mutex
	pending map[string]*pendingWork // by project
}

// pendingWork is what a project's last batch left to resolve: files to
// resolve in a layer's view (dependents beyond the budget, or every file of
// a batch whose resolution failed), and names whose dependents a failed
// batch did not reach.
type pendingWork struct {
	files map[layerPath]bool
	names map[string]map[string]bool // layer → case-folded names
}

func (w *pendingWork) empty() bool { return w == nil || len(w.files)+len(w.names) == 0 }

func (w *pendingWork) addName(layer, n string) {
	if w.names[layer] == nil {
		w.names[layer] = map[string]bool{}
	}
	w.names[layer][n] = true
}

var _ Applier = (*Resolver)(nil)

// NewResolver returns a Resolver.
func NewResolver(opts ResolverOptions) *Resolver {
	if opts.Files == nil {
		opts.Files = FileApplier{}
	}
	if opts.MaxRefs <= 0 {
		opts.MaxRefs = DefaultMaxRefs
	}
	if opts.FactsCacheSize <= 0 {
		opts.FactsCacheSize = DefaultFactsCacheSize
	}
	return &Resolver{opts: opts, facts: newFactsCache(opts.FactsCacheSize), pending: map[string]*pendingWork{}}
}

// Apply implements Applier: Files, then Resolve.
func (r *Resolver) Apply(ctx context.Context, s *Service, b Batch) error {
	if err := r.opts.Files.Apply(ctx, s, b); err != nil {
		return err
	}
	return r.Resolve(ctx, s, b)
}

// Pending is the number of dependents (a file in a layer's view) of
// project still to re-resolve.
func (r *Resolver) Pending(project string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if w := r.pending[project]; w != nil {
		return len(w.files)
	}
	return 0
}

// Resolve writes the edges of a batch whose files are already in the
// graph. When it fails, the work of the batch is kept for the project's
// next batch (the retried batch's delta no longer holds the files already
// written).
func (r *Resolver) Resolve(ctx context.Context, s *Service, b Batch) (err error) {
	if b.Graph == nil {
		return errors.New("codeindex: resolve needs a graph")
	}
	if s.opts.Parse == nil {
		return nil // files are indexed as text: there is nothing to resolve
	}
	run := &resolveRun{r: r, s: s, b: b, next: indexPlan(b.Plan), prev: indexPlan(b.Prev),
		views: map[string]*viewIndex{}, content: map[string][]byte{}, byPath: map[string]map[string]bool{}}

	r.mu.Lock()
	pending := r.pending[b.Project]
	delete(r.pending, b.Project)
	r.mu.Unlock()
	if pending == nil {
		pending = &pendingWork{}
	}
	leftover := &pendingWork{files: map[layerPath]bool{}, names: map[string]map[string]bool{}}

	// What changed: per layer, the paths put or removed, and the paths to
	// resolve completely (the puts, or every file of a layer whose chain
	// of parents moved).
	changed := map[string][]string{}
	full := map[string]map[string]bool{}
	addFull := func(layer, p string) {
		if full[layer] == nil {
			full[layer] = map[string]bool{}
		}
		full[layer][p] = true
	}
	for _, ld := range b.Delta.Layers {
		id := ld.Layer.ID
		for _, f := range ld.Put {
			changed[id] = append(changed[id], f.Path)
			if !f.Deleted {
				addFull(id, f.Path)
			}
		}
		changed[id] = append(changed[id], ld.Remove...)
	}
	for _, l := range b.Plan.Layers {
		id := l.Layer.ID
		if _, had := run.prev.layers[id]; had && !slices.Equal(run.next.ancestors(id), run.prev.ancestors(id)) {
			for _, f := range l.Files {
				if !f.Deleted {
					addFull(id, f.Path)
				}
			}
		}
	}
	names := map[string]map[string]bool{}
	for layer, ns := range pending.names {
		names[layer] = ns
	}

	defer func() {
		if err != nil {
			// Keep everything this batch was to do.
			for k := range pending.files {
				leftover.files[k] = true
			}
			for layer, ps := range full {
				for p := range ps {
					leftover.files[layerPath{layer, p}] = true
				}
			}
			for layer, ns := range names {
				for n := range ns {
					leftover.addName(layer, n)
				}
			}
		}
		if leftover.empty() {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		r.pending[b.Project] = leftover
	}()

	for layer, paths := range changed {
		ns, err := run.namesAt(ctx, paths)
		if err != nil {
			return err
		}
		if names[layer] == nil {
			names[layer] = map[string]bool{}
		}
		for n := range ns {
			names[layer][n] = true
		}
	}

	budget := r.opts.MaxRefs
	for _, l := range b.Plan.Layers {
		id := l.Layer.ID
		chain := append([]string{id}, run.next.ancestors(id)...)
		affected := map[string]bool{} // case-folded names whose definitions changed below or in id
		for _, c := range append(chain, run.prev.ancestors(id)...) {
			for n := range names[c] {
				affected[n] = true
			}
		}
		var lowerPuts []string
		for _, a := range chain[1:] {
			for p := range full[a] {
				lowerPuts = append(lowerPuts, p)
			}
		}
		var carried []string
		for k := range pending.files {
			if k.layer == id {
				carried = append(carried, k.path)
			}
		}
		var shadow map[string]bool // what this layer may resolve differently from the one below
		if len(chain) > 1 {
			if shadow, err = run.shadow(ctx, id); err != nil {
				return err
			}
		}
		if len(full[id]) == 0 && len(carried) == 0 {
			need := len(shadow) > 0 && (len(lowerPuts) > 0 || len(affected) > 0)
			if !need && len(affected) > 0 {
				if need, err = run.layerMentions(ctx, l, affected); err != nil {
					return err
				}
			}
			if !need {
				continue
			}
		}
		v, err := run.view(ctx, id)
		if err != nil {
			return err
		}

		// The layer's own changed files, completely.
		for _, p := range sortedKeys(full[id]) {
			if err := run.resolveFile(ctx, v, p); err != nil {
				return err
			}
		}

		// Then the dependents, within the budget: the files carried from the
		// last batch; the layer's files that refer to a changed name; and
		// lower files, put by this batch or referring to a changed name,
		// when they refer to a name this layer defines or hides (otherwise
		// the view below resolves them the same).
		deps := map[string]bool{}
		for _, p := range carried {
			deps[p] = true
		}
		consider := func(p string) {
			f := v.files[p]
			if f == nil || deps[p] || full[id][p] {
				return
			}
			if f.Layer == id || mentions(f, shadow) {
				deps[p] = true
			}
		}
		for _, p := range lowerPuts {
			consider(p)
		}
		for n := range affected {
			for _, p := range v.refNames[n] {
				consider(p)
			}
		}
		order := make([]string, 0, len(deps))
		for p := range deps {
			if full[id][p] || v.files[p] == nil {
				continue
			}
			order = append(order, p)
		}
		slices.Sort(order)
		for i, p := range order {
			refs := len(v.files[p].Facts.Refs)
			if i > 0 && refs > budget {
				for _, rest := range order[i:] {
					leftover.files[layerPath{id, rest}] = true
				}
				break
			}
			if err := run.resolveFile(ctx, v, p); err != nil {
				return err
			}
			budget -= refs
		}
	}
	return nil
}

// layerMentions reports whether a file of the layer itself refers to any
// of names.
func (run *resolveRun) layerMentions(ctx context.Context, l LayerPlan, names map[string]bool) (bool, error) {
	for _, f := range l.Files {
		if f.Deleted || f.SHA256 == "" {
			continue
		}
		facts, err := run.facts(ctx, f)
		if err != nil {
			return false, err
		}
		if facts == nil {
			continue
		}
		for _, r := range facts.Refs {
			if names[strings.ToLower(r.Name)] {
				return true, nil
			}
		}
	}
	return false, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// resolveRun is one batch's resolution: the views it built and the
// content it read.
type resolveRun struct {
	r          *Resolver
	s          *Service
	b          Batch
	next, prev planIndex
	views      map[string]*viewIndex
	content    map[string][]byte          // by sha256
	byPath     map[string]map[string]bool // pathNames
}

// pathNames returns the case-folded names defined at path by any version
// of it in the plans before and after the batch.
func (run *resolveRun) pathNames(ctx context.Context, p string) (map[string]bool, error) {
	if ns, ok := run.byPath[p]; ok {
		return ns, nil
	}
	ns := map[string]bool{}
	for _, ix := range []planIndex{run.prev, run.next} {
		for _, l := range ix.layers {
			f, ok := l.File(p)
			if !ok || f.Deleted || f.SHA256 == "" {
				continue
			}
			facts, err := run.facts(ctx, f)
			if err != nil {
				return nil, err
			}
			if facts == nil {
				continue
			}
			for _, sym := range facts.Symbols {
				ns[strings.ToLower(sym.Name)] = true
			}
		}
	}
	run.byPath[p] = ns
	return ns, nil
}

// namesAt returns the union of pathNames over paths.
func (run *resolveRun) namesAt(ctx context.Context, paths []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, p := range paths {
		ns, err := run.pathNames(ctx, p)
		if err != nil {
			return nil, err
		}
		for n := range ns {
			out[n] = true
		}
	}
	return out, nil
}

// shadow returns the names a layer can make its view resolve differently
// from the view below it: those defined by any version of the paths it
// holds, before or after the batch.
func (run *resolveRun) shadow(ctx context.Context, layer string) (map[string]bool, error) {
	var paths []string
	for _, ix := range []planIndex{run.prev, run.next} {
		for _, f := range ix.layers[layer].Files {
			paths = append(paths, f.Path)
		}
	}
	return run.namesAt(ctx, paths)
}

// mentions reports whether the file refers to any of names.
func mentions(f *resolvers.File, names map[string]bool) bool {
	for _, r := range f.Facts.Refs {
		if names[strings.ToLower(r.Name)] {
			return true
		}
	}
	return false
}

// facts returns a file version's facts, without chunks, through the
// Resolver's cache and the parse cache; nil for a file without facts (its
// blob is gone, it was indexed as text, its family has no resolver).
func (run *resolveRun) facts(ctx context.Context, f FileSource) (*codeparse.Facts, error) {
	key := f.Path + "\x00" + f.SHA256
	if facts, ok := run.r.facts.get(key); ok {
		return facts, nil
	}
	fa, ok := run.r.opts.Files.(FileApplier)
	if !ok {
		fa = FileApplier{}
	}
	full, err := fa.parse(ctx, run.s, run.b.Code, f)
	switch {
	case errors.Is(err, code.ErrNotFound):
		return nil, nil
	case err != nil:
		return nil, err
	}
	var facts *codeparse.Facts
	if full.ParseError == "" && len(full.Symbols)+len(full.Refs) > 0 {
		lean := full
		lean.Chunks = nil
		lean.Symbols = slices.Clone(full.Symbols)
		for i := range lean.Symbols {
			lean.Symbols[i].Doc = ""
		}
		facts = &lean
	}
	run.r.facts.put(key, facts)
	return facts, nil
}

// view returns the index of layer's view, built once per run.
func (run *resolveRun) view(ctx context.Context, layer string) (*viewIndex, error) {
	if v, ok := run.views[layer]; ok {
		return v, nil
	}
	v, err := run.buildView(ctx, layer)
	if err != nil {
		return nil, err
	}
	run.views[layer] = v
	return v, nil
}

// viewIndex is one layer's view as the engine resolves in it: the visible
// path of every file, the files of registered families with their facts
// and package keys, and the definitions by family and case-folded name.
type viewIndex struct {
	run      *resolveRun
	layer    string
	parent   string // the layer below, "" for none
	visible  map[string]FileSource
	from     map[string]string // path → the layer its visible version comes from
	files    map[string]*resolvers.File
	packages map[string]map[string][]*resolvers.File     // family → key → files
	byName   map[string]map[string][]resolvers.Candidate // family → folded name → definitions
	refNames map[string][]string                         // lower-cased referenced name → paths
	scopes   map[string]*fileScope
}

var _ resolvers.View = (*viewIndex)(nil)

func (run *resolveRun) buildView(ctx context.Context, layer string) (*viewIndex, error) {
	chain := append([]string{layer}, run.next.ancestors(layer)...) // highest first
	v := &viewIndex{run: run, layer: layer, visible: map[string]FileSource{}, from: map[string]string{},
		files: map[string]*resolvers.File{}, packages: map[string]map[string][]*resolvers.File{},
		byName: map[string]map[string][]resolvers.Candidate{}, refNames: map[string][]string{},
		scopes: map[string]*fileScope{}}
	if len(chain) > 1 {
		v.parent = chain[1]
	}
	seen := map[string]bool{}
	for _, id := range chain {
		for _, f := range run.next.layers[id].Files {
			if seen[f.Path] {
				continue
			}
			seen[f.Path] = true
			if !f.Deleted {
				v.visible[f.Path] = f
				v.from[f.Path] = id
			}
		}
	}
	paths := make([]string, 0, len(v.visible))
	for p := range v.visible {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		src := v.visible[p]
		if src.SHA256 == "" {
			continue
		}
		lang := codeparse.Detect(p, nil, nil).Lang
		if lang == "" {
			lang = src.Lang
		}
		if _, _, ok := resolvers.ForLang(lang); !ok {
			continue
		}
		facts, err := run.facts(ctx, src)
		if err != nil {
			return nil, err
		}
		if facts == nil {
			continue
		}
		if facts.Lang != "" {
			lang = facts.Lang
		}
		family, _, ok := resolvers.ForLang(lang)
		if !ok {
			continue
		}
		f := &resolvers.File{Path: p, Layer: v.from[p], Lang: lang, Family: family, Facts: facts}
		if run.r.opts.Profiles != nil {
			f.Profile = run.r.opts.Profiles[lang]
		}
		v.files[p] = f
	}
	for _, p := range paths {
		f := v.files[p]
		if f == nil {
			continue
		}
		res, _ := resolvers.Lookup(f.Family)
		key, err := res.Package(ctx, v, f)
		if err != nil {
			return nil, fmt.Errorf("codeindex: package of %s in %s: %w", p, layer, err)
		}
		f.Package = key
	}
	for _, p := range paths {
		f := v.files[p]
		if f == nil {
			continue
		}
		res, _ := resolvers.Lookup(f.Family)
		fold := folder(res.Rules())
		if v.packages[f.Family] == nil {
			v.packages[f.Family] = map[string][]*resolvers.File{}
			v.byName[f.Family] = map[string][]resolvers.Candidate{}
		}
		v.packages[f.Family][f.Package] = append(v.packages[f.Family][f.Package], f)
		for i := range f.Facts.Symbols {
			sym := &f.Facts.Symbols[i]
			k := fold(sym.Name)
			v.byName[f.Family][k] = append(v.byName[f.Family][k], resolvers.Candidate{File: f, Symbol: sym})
		}
		refd := map[string]bool{}
		for _, ref := range f.Facts.Refs {
			n := strings.ToLower(ref.Name)
			if !refd[n] {
				refd[n] = true
				v.refNames[n] = append(v.refNames[n], p)
			}
		}
	}
	return v, nil
}

// Layer implements resolvers.View.
func (v *viewIndex) Layer() string { return v.layer }

// Has implements resolvers.View.
func (v *viewIndex) Has(p string) bool {
	_, ok := v.visible[p]
	return ok
}

// Content implements resolvers.View.
func (v *viewIndex) Content(ctx context.Context, p string) ([]byte, bool, error) {
	f, ok := v.visible[p]
	if !ok || f.SHA256 == "" {
		return nil, false, nil
	}
	if c, ok := v.run.content[f.SHA256]; ok {
		return c, true, nil
	}
	blob, err := v.run.b.Code.Blob(ctx, f.SHA256)
	if errors.Is(err, code.ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("codeindex: blob of %s: %w", p, err)
	}
	v.run.content[f.SHA256] = blob.Content
	return blob.Content, true, nil
}

// Package implements resolvers.View.
func (v *viewIndex) Package(family, key string) []*resolvers.File {
	return v.packages[family][key]
}

// fileScope is what the engine knows of one referencing file in a view.
type fileScope struct {
	scope    resolvers.Scope
	imported map[string]bool // every package the imports name
	parent   map[string]string
}

func (v *viewIndex) scope(ctx context.Context, f *resolvers.File, res resolvers.Resolver) (*fileScope, error) {
	if fs, ok := v.scopes[f.Path]; ok {
		return fs, nil
	}
	sc, err := res.Scope(ctx, v, f)
	if err != nil {
		return nil, fmt.Errorf("codeindex: scope of %s in %s: %w", f.Path, v.layer, err)
	}
	fs := &fileScope{scope: sc, imported: map[string]bool{}, parent: map[string]string{}}
	for _, pkgs := range sc.Qualifiers {
		for _, p := range pkgs {
			fs.imported[p] = true
		}
	}
	for _, pkgs := range sc.Names {
		for _, p := range pkgs {
			fs.imported[p] = true
		}
	}
	for _, p := range sc.Open {
		fs.imported[p] = true
	}
	for _, sym := range f.Facts.Symbols {
		fs.parent[sym.ID] = sym.Parent
	}
	v.scopes[f.Path] = fs
	return fs, nil
}

// target is a resolved reference.
type target struct {
	c          resolvers.Candidate
	confidence string
}

func (t target) node() codegraph.NodeRef {
	return codegraph.NodeRef{Layer: t.c.File.Layer, ID: SymbolNodeID(t.c.File.Path, t.c.Symbol.ID)}
}

// resolve links one reference of f in the view.
func (v *viewIndex) resolve(ctx context.Context, f *resolvers.File, ref codeparse.Ref) (target, bool, error) {
	res, ok := resolvers.Lookup(f.Family)
	if !ok {
		return target{}, false, nil
	}
	rules := res.Rules()
	fold := folder(rules)
	cands := v.byName[f.Family][fold(ref.Name)]
	if len(cands) == 0 {
		return target{}, false, nil
	}
	fs, err := v.scope(ctx, f, res)
	if err != nil {
		return target{}, false, err
	}
	seps := rules.Separators
	if len(seps) == 0 {
		seps = []string{"."}
	}
	pick := func(step resolvers.Step, confidence string, keep func(resolvers.Candidate) bool) (target, bool) {
		var found []resolvers.Candidate
		for _, c := range cands {
			if keep(c) && res.Visible(f, c, step) {
				found = append(found, c)
			}
		}
		return best(ref.Kind, found, confidence)
	}
	sameFile := func(c resolvers.Candidate) bool { return c.File == f }
	samePackage := func(c resolvers.Candidate) bool { return c.File != f && c.File.Package == f.Package }
	in := func(pkgs []string) func(resolvers.Candidate) bool {
		return func(c resolvers.Candidate) bool { return slices.Contains(pkgs, c.File.Package) }
	}
	and := func(a, b func(resolvers.Candidate) bool) func(resolvers.Candidate) bool {
		return func(c resolvers.Candidate) bool { return a(c) && b(c) }
	}
	top := func(c resolvers.Candidate) bool { return c.TopLevel() }
	member := func(c resolvers.Candidate) bool { return !c.TopLevel() }
	anywhere := func(resolvers.Candidate) bool { return true }
	type try struct {
		step       resolvers.Step
		confidence string
		keep       func(resolvers.Candidate) bool
	}
	first := func(tries ...try) (target, bool, error) {
		for _, t := range tries {
			if got, ok := pick(t.step, t.confidence, t.keep); ok {
				return got, true, nil
			}
		}
		return target{}, false, nil
	}

	if ref.Qualifier == "" {
		var tries []try
		for id := ref.Enclosing; id != ""; id = fs.parent[id] {
			tries = append(tries, try{resolvers.StepScope, codegraph.ConfidenceExact, func(c resolvers.Candidate) bool {
				return c.File == f && c.Symbol.Parent == id
			}})
		}
		tries = append(tries,
			try{resolvers.StepFile, codegraph.ConfidenceExact, and(sameFile, top)},
			try{resolvers.StepPackage, codegraph.ConfidenceExact, and(samePackage, top)},
			try{resolvers.StepImport, codegraph.ConfidenceExact, and(in(fs.scope.Names[ref.Name]), top)},
			try{resolvers.StepOpen, codegraph.ConfidenceExact, and(in(fs.scope.Open), top)})
		if rules.NameFallback {
			tries = append(tries, try{resolvers.StepName, codegraph.ConfidenceName, anywhere})
		}
		return first(tries...)
	}

	parts := splitQualifier(ref.Qualifier, seps)
	qualified := func(qual string) func(resolvers.Candidate) bool {
		return func(c resolvers.Candidate) bool {
			return endsWithQualified(fold(c.Symbol.QualName), fold(qual), fold(ref.Name), seps)
		}
	}
	if pkgs, ok := fs.scope.Qualifiers[parts[0]]; ok {
		inPkg := in(pkgs)
		var tries []try
		if len(parts) == 1 {
			tries = append(tries, try{resolvers.StepImport, codegraph.ConfidenceExact, and(inPkg, top)})
		} else {
			tries = append(tries, try{resolvers.StepQualified, codegraph.ConfidenceQualified,
				and(inPkg, qualified(strings.Join(parts[1:], seps[0])))})
		}
		tries = append(tries, try{resolvers.StepQualified, codegraph.ConfidenceQualified, and(inPkg, qualified(ref.Qualifier))})
		if rules.MemberFallback {
			tries = append(tries, try{resolvers.StepMember, codegraph.ConfidenceName, and(inPkg, member)})
		}
		return first(tries...)
	}
	isQual := qualified(ref.Qualifier)
	imported := func(c resolvers.Candidate) bool { return fs.imported[c.File.Package] }
	tries := []try{
		{resolvers.StepQualified, codegraph.ConfidenceQualified, and(isQual, sameFile)},
		{resolvers.StepQualified, codegraph.ConfidenceQualified, and(isQual, samePackage)},
		{resolvers.StepQualified, codegraph.ConfidenceQualified, isQual},
	}
	if rules.MemberFallback {
		tries = append(tries,
			try{resolvers.StepMember, codegraph.ConfidenceName, and(member, sameFile)},
			try{resolvers.StepMember, codegraph.ConfidenceName, and(member, samePackage)},
			try{resolvers.StepMember, codegraph.ConfidenceName, and(member, imported)},
			try{resolvers.StepMember, codegraph.ConfidenceName, member})
	}
	return first(tries...)
}

// best keeps the best of several definitions: by kind for the reference's
// kind, exported first, then by path and id. More than one makes the
// confidence name.
func best(refKind string, found []resolvers.Candidate, confidence string) (target, bool) {
	switch len(found) {
	case 0:
		return target{}, false
	case 1:
		return target{found[0], confidence}, true
	}
	b := slices.MinFunc(found, func(x, y resolvers.Candidate) int {
		return cmp.Or(
			cmp.Compare(defRank(refKind, x.Symbol.Kind), defRank(refKind, y.Symbol.Kind)),
			cmpBool(y.Symbol.Exported, x.Symbol.Exported),
			cmp.Compare(x.File.Path, y.File.Path),
			cmp.Compare(x.Symbol.ID, y.Symbol.ID))
	})
	return target{b, codegraph.ConfidenceName}, true
}

func cmpBool(a, b bool) int {
	switch {
	case a == b:
		return 0
	case !a:
		return -1
	}
	return 1
}

// defRank orders definition kinds for a reference kind: lower fits better.
func defRank(refKind, kind string) int {
	switch refKind {
	case codeparse.RefCall:
		switch kind {
		case codeparse.KindFunction, codeparse.KindMethod, codeparse.KindMacro:
			return 0
		case codeparse.KindClass, codeparse.KindStruct, codeparse.KindType:
			return 1
		}
	case codeparse.RefImplements:
		switch kind {
		case codeparse.KindInterface, codeparse.KindProtocol, codeparse.KindTrait:
			return 0
		case codeparse.KindClass, codeparse.KindType:
			return 1
		}
	case codeparse.RefType, codeparse.RefExtends:
		switch kind {
		case codeparse.KindClass, codeparse.KindStruct, codeparse.KindInterface, codeparse.KindTrait,
			codeparse.KindProtocol, codeparse.KindType, codeparse.KindEnum, codeparse.KindUnion:
			return 0
		}
	case codeparse.RefValue:
		switch kind {
		case codeparse.KindConst, codeparse.KindVar, codeparse.KindField, codeparse.KindProperty:
			return 0
		case codeparse.KindFunction, codeparse.KindMethod:
			return 1
		}
	}
	return 2
}

// folder is the name comparison of a family's rules.
func folder(rules resolvers.Rules) func(string) string {
	if rules.CaseInsensitive {
		return strings.ToLower
	}
	return func(s string) string { return s }
}

// longestFirst orders separators so "::" is tried before ":".
func longestFirst(seps []string) []string {
	out := slices.Clone(seps)
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return out
}

// splitQualifier splits a qualifier at any of seps.
func splitQualifier(q string, seps []string) []string {
	parts := []string{q}
	for _, sep := range longestFirst(seps) {
		var next []string
		for _, p := range parts {
			next = append(next, strings.Split(p, sep)...)
		}
		parts = next
	}
	return parts
}

// endsWithQualified reports whether qualName is qual, a separator and name,
// alone or after a separator; any of seps separates.
func endsWithQualified(qualName, qual, name string, seps []string) bool {
	const sep = "\x00"
	norm := func(s string) string {
		for _, s2 := range longestFirst(seps) {
			s = strings.ReplaceAll(s, s2, sep)
		}
		return s
	}
	qn, want := norm(qualName), norm(qual)+sep+name
	return qn == want || strings.HasSuffix(qn, sep+want)
}

// edgeType is the resolved edge a reference kind makes.
func edgeType(refKind string) codegraph.EdgeType {
	switch refKind {
	case codeparse.RefCall:
		return codegraph.EdgeCalls
	case codeparse.RefExtends:
		return codegraph.EdgeExtends
	case codeparse.RefImplements:
		return codegraph.EdgeImplements
	}
	return codegraph.EdgeReferences
}

// resolveFile resolves the references of the file visible at p in v and
// replaces the edges v's layer holds from its symbols: all of them when the
// file is v's own, the ones that differ from the parent view's otherwise.
func (run *resolveRun) resolveFile(ctx context.Context, v *viewIndex, p string) error {
	f := v.files[p]
	if f == nil || len(f.Facts.Symbols) == 0 {
		return nil
	}
	var parent *viewIndex
	if f.Layer != v.layer && v.parent != "" {
		var err error
		if parent, err = run.view(ctx, v.parent); err != nil {
			return err
		}
	}
	type edgeKey struct {
		typ      codegraph.EdgeType
		from, to codegraph.NodeRef
	}
	edges := map[edgeKey]*codegraph.Edge{}
	for _, ref := range f.Facts.Refs {
		if ref.Enclosing == "" {
			continue // an edge needs a source definition
		}
		t, ok, err := v.resolve(ctx, f, ref)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if parent != nil {
			if pf := parent.files[p]; pf != nil {
				was, ok, err := parent.resolve(ctx, pf, ref)
				if err != nil {
					return err
				}
				if ok && was.node() == t.node() {
					continue // the layer below already has this edge
				}
			}
		}
		k := edgeKey{edgeType(ref.Kind), codegraph.NodeRef{Layer: f.Layer, ID: SymbolNodeID(p, ref.Enclosing)}, t.node()}
		e := edges[k]
		if e == nil {
			e = &codegraph.Edge{Type: k.typ, Layer: v.layer, From: k.from, To: k.to, Confidence: t.confidence, Line: ref.Start.Line}
			edges[k] = e
		}
		e.Count++
		e.Line = min(e.Line, ref.Start.Line)
		if codegraph.ConfidenceRank(t.confidence) < codegraph.ConfidenceRank(e.Confidence) {
			e.Confidence = t.confidence
		}
	}
	from := make([]codegraph.NodeRef, 0, len(f.Facts.Symbols))
	for _, sym := range f.Facts.Symbols {
		from = append(from, codegraph.NodeRef{Layer: f.Layer, ID: SymbolNodeID(p, sym.ID)})
	}
	out := make([]codegraph.Edge, 0, len(edges))
	for _, e := range edges {
		out = append(out, *e)
	}
	slices.SortFunc(out, func(a, b codegraph.Edge) int {
		return cmp.Or(cmp.Compare(a.From.ID, b.From.ID), cmp.Compare(a.Type, b.Type),
			cmp.Compare(a.To.Layer, b.To.Layer), cmp.Compare(a.To.ID, b.To.ID))
	})
	err := run.b.Graph.PutEdges(ctx, v.layer, from, out)
	if errors.Is(err, codegraph.ErrNotFound) {
		// The graph holds other facts for an endpoint than the parse cache
		// gives now (a parse that timed out when the file was applied):
		// the file keeps its edges until it is applied again.
		run.s.log.Warn("codeindex: edges of a file whose facts changed", "path", p, "layer", v.layer, "error", err)
		return nil
	}
	if err != nil {
		return fmt.Errorf("codeindex: edges of %s in %s: %w", p, v.layer, err)
	}
	return nil
}

// factsCache keeps the lean facts of recently resolved file versions, by
// path and blob, least recently used out first. A nil entry records a file
// without facts.
type factsCache struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

type factsEntry struct {
	key   string
	facts *codeparse.Facts
}

func newFactsCache(max int) *factsCache {
	return &factsCache{max: max, order: list.New(), items: map[string]*list.Element{}}
}

func (c *factsCache) get(key string) (*codeparse.Facts, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*factsEntry).facts, true
}

func (c *factsCache) put(key string, facts *codeparse.Facts) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*factsEntry).facts = facts
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&factsEntry{key, facts})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.items, last.Value.(*factsEntry).key)
	}
}
