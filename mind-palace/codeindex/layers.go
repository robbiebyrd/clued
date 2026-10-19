package codeindex

import (
	"cmp"
	"fmt"
	"slices"
	"sort"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
)

// Layer source selection: which entries of which (worktree, ref) trees make
// up the base, each branch layer and each session overlay (plan 0010,
// "Partitioning for the indexer"; plan 0012, "Choosing layer sources").
//
//   - Base: the default branch's entries with dirty and unpushed both false.
//     The GitHub-backed worktree (gh_<id>, Source github) supplies it when it
//     has the default branch; otherwise the client trees on the default
//     branch do, the newest upstream commit winning, every tree at that
//     commit contributing its clean entries. A path's content is picked by
//     git_oid (the most reported one) and, for that oid, the blob most trees
//     sent (members' redaction rules may differ), never mixing them.
//   - Branch layer per (ref, upstream commit) of every other pushed branch:
//     the clean, pushed entries of the trees at that commit (the GitHub
//     worktree alone when it is one of them), only the paths whose git_oid
//     differs from the base's, plus tombstones for base paths the branch no
//     longer has. A branch layer exists while a tree sits on its commit, so
//     the ref's newest is kept and older ones go once no checkout is on them.
//   - Session overlay per client (worktree, ref): the entries with dirty or
//     unpushed, each keeping its own blob and session tag, plus tombstones
//     for the paths visible below it (its branch layer over the base, or the
//     base alone when the branch was never pushed or is the default branch)
//     that the checkout no longer has. Its owner is the worktree's owner.
//
// Selection is a pure function of the trees (Select); Diff turns two
// selections into the writes that move the graph from one to the other.

// Tree is one (worktree, ref) tree as selection sees it: the worktree's
// metadata, what the change log said about the tree last, and its entries.
type Tree struct {
	Worktree code.Worktree
	State    TreeState
	Entries  []code.Entry
}

// FileSource is one path of a layer as selected: the blob to index (SHA256,
// empty when its content is unknown, such as a pushed version only seen
// through a dirty checkout or a file whose content was not collected), the
// git blob id, the client's language and mode, and, in an overlay, the
// session that last wrote it. Deleted marks a tombstone.
type FileSource struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256,omitempty"`
	GitOID  string `json:"gitOid,omitempty"`
	Lang    string `json:"lang,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Session string `json:"session,omitempty"`
	Deleted bool   `json:"deleted,omitempty"`
}

// sameFile reports whether a and b index the same thing: the fields the
// graph keeps on a File (blob, git blob id, tombstone).
func sameFile(a, b FileSource) bool {
	return a.SHA256 == b.SHA256 && a.GitOID == b.GitOID && a.Deleted == b.Deleted
}

// LayerPlan is one selected layer: its node, the trees its files came from
// and its files ordered by path.
type LayerPlan struct {
	Layer   codegraph.Layer `json:"layer"`
	Sources []TreeKey       `json:"sources,omitempty"`
	Files   []FileSource    `json:"files,omitempty"`
}

// File returns the layer's file at path.
func (l LayerPlan) File(path string) (FileSource, bool) {
	i, ok := slices.BinarySearchFunc(l.Files, path, func(f FileSource, p string) int { return cmp.Compare(f.Path, p) })
	if !ok {
		return FileSource{}, false
	}
	return l.Files[i], true
}

// Plan is a whole selection: the default branch the base follows ("" when
// none is known) and the layers, base first, then branch layers, then
// overlays, each group ordered by id, so lower layers always come before
// the layers above them.
type Plan struct {
	DefaultBranch string      `json:"defaultBranch,omitempty"`
	Layers        []LayerPlan `json:"layers,omitempty"`
}

// Layer returns the layer with that id.
func (p Plan) Layer(id string) (LayerPlan, bool) {
	for _, l := range p.Layers {
		if l.Layer.ID == id {
			return l, true
		}
	}
	return LayerPlan{}, false
}

// View is the view of a layer: the layer and the layers below it through
// Parent, lowest first (an overlay's is [base, branch:…, wt:…]).
func (p Plan) View(id string) (codegraph.View, error) {
	byID := make(map[string]codegraph.Layer, len(p.Layers))
	for _, l := range p.Layers {
		byID[l.Layer.ID] = l.Layer
	}
	var chain []string
	for cur := id; cur != ""; {
		l, ok := byID[cur]
		if !ok {
			return nil, fmt.Errorf("%w: layer %q", codegraph.ErrNotFound, cur)
		}
		if slices.Contains(chain, cur) {
			return nil, fmt.Errorf("%w: layer %q is its own ancestor", codegraph.ErrInvalid, cur)
		}
		chain = append(chain, cur)
		cur = l.Parent
	}
	slices.Reverse(chain)
	return codegraph.NewView(chain...)
}

// kindRank orders layer kinds bottom-up.
func kindRank(kind string) int {
	switch kind {
	case codegraph.LayerBase:
		return 0
	case codegraph.LayerBranch:
		return 1
	}
	return 2
}

// sortLayers orders layers base, branches, overlays, then by id.
func sortLayers(ls []LayerPlan) {
	sort.SliceStable(ls, func(i, j int) bool {
		a, b := ls[i].Layer, ls[j].Layer
		if ra, rb := kindRank(a.Kind), kindRank(b.Kind); ra != rb {
			return ra < rb
		}
		return a.ID < b.ID
	})
}

// isGitHub reports whether the tree belongs to the GitHub-backed worktree,
// whose entries are always clean and pushed.
func isGitHub(t Tree) bool { return t.Worktree.Source == code.SourceGitHub }

// pushedCommit is the commit a tree's clean, pushed entries are at: its
// upstream, or for the GitHub worktree (which is the remote) its head when
// no upstream was reported.
func pushedCommit(t Tree) string {
	if t.State.Upstream != "" {
		return t.State.Upstream
	}
	if isGitHub(t) {
		return t.State.Head
	}
	return ""
}

// newer reports whether a's upstream was seen after b's (then the later
// change, then the worktree id, so the choice is deterministic).
func newer(a, b Tree) bool {
	if a.State.UpstreamSeq != b.State.UpstreamSeq {
		return a.State.UpstreamSeq > b.State.UpstreamSeq
	}
	if a.State.Seq != b.State.Seq {
		return a.State.Seq > b.State.Seq
	}
	return a.Worktree.ID < b.Worktree.ID
}

// Select chooses the layers and their files from the project's trees. Trees
// of removed worktrees and empty trees are ignored. The result depends only
// on its argument.
func Select(trees []Tree) Plan {
	live := make([]Tree, 0, len(trees))
	for _, t := range trees {
		if t.Worktree.Removed || t.Worktree.ID == "" || t.State.Ref == "" || len(t.Entries) == 0 {
			continue
		}
		live = append(live, t)
	}
	slices.SortFunc(live, func(a, b Tree) int {
		return cmp.Or(cmp.Compare(a.Worktree.ID, b.Worktree.ID), cmp.Compare(a.State.Ref, b.State.Ref))
	})

	var plan Plan
	plan.DefaultBranch = defaultBranch(live)

	// Base.
	var base *LayerPlan
	baseFiles := map[string]FileSource{}
	if src, commit := baseSources(live, plan.DefaultBranch); len(src) > 0 {
		base = &LayerPlan{Layer: codegraph.Layer{ID: codegraph.BaseLayerID, Kind: codegraph.LayerBase,
			Ref: plan.DefaultBranch, Commit: commit}}
		for p, k := range pushedKnowledge(src) {
			if f, st := k.resolve(p); st == present {
				baseFiles[p] = f
			}
		}
		base.Sources = treeKeys(src)
		base.Files = sortedFiles(baseFiles)
		plan.Layers = append(plan.Layers, *base)
	}
	parentBase := ""
	if base != nil {
		parentBase = base.Layer.ID
	}

	// Branch layers, one per (ref, pushed commit) a tree sits on.
	type branchKey struct{ ref, commit string }
	groups := map[branchKey][]Tree{}
	for _, t := range live {
		if plan.DefaultBranch != "" && t.State.Ref == plan.DefaultBranch {
			continue
		}
		if c := pushedCommit(t); c != "" {
			k := branchKey{t.State.Ref, c}
			groups[k] = append(groups[k], t)
		}
	}
	visibleOn := map[string]map[string]FileSource{} // branch layer id → files visible in its view
	for k, ts := range groups {
		id := codegraph.BranchLayerID(k.ref, k.commit)
		if _, err := codegraph.ParseLayerID(id); err != nil {
			continue
		}
		if gh := filterTrees(ts, isGitHub); len(gh) > 0 {
			ts = gh
		}
		files := map[string]FileSource{}
		know := pushedKnowledge(ts)
		for p, kn := range know {
			f, st := kn.resolve(p)
			b, inBase := baseFiles[p]
			switch {
			case st == present && (!inBase || b.GitOID != f.GitOID):
				files[p] = f
			case st == absent && inBase:
				files[p] = FileSource{Path: p, Deleted: true}
			}
		}
		for p := range baseFiles {
			if _, known := know[p]; !known {
				files[p] = FileSource{Path: p, Deleted: true}
			}
		}
		l := LayerPlan{Layer: codegraph.Layer{ID: id, Kind: codegraph.LayerBranch, Parent: parentBase,
			Ref: k.ref, Commit: k.commit}, Sources: treeKeys(ts), Files: sortedFiles(files)}
		plan.Layers = append(plan.Layers, l)
		visibleOn[id] = stack(baseFiles, files)
	}

	// Session overlays, one per client tree.
	for _, t := range live {
		if isGitHub(t) {
			continue
		}
		id := codegraph.WorktreeLayerID(t.Worktree.ID, t.State.Ref)
		if _, err := codegraph.ParseLayerID(id); err != nil {
			continue
		}
		parent, below := parentBase, baseFiles
		if plan.DefaultBranch == "" || t.State.Ref != plan.DefaultBranch {
			if c := pushedCommit(t); c != "" {
				if v, ok := visibleOn[codegraph.BranchLayerID(t.State.Ref, c)]; ok {
					parent, below = codegraph.BranchLayerID(t.State.Ref, c), v
				}
			}
		}
		files := map[string]FileSource{}
		have := make(map[string]bool, len(t.Entries))
		for _, e := range t.Entries {
			have[e.Path] = true
			if e.Dirty || e.Unpushed {
				files[e.Path] = FileSource{Path: e.Path, SHA256: e.SHA256, GitOID: e.GitOID, Lang: e.Lang,
					Mode: e.Mode, Size: e.Size, Session: e.Session}
			}
		}
		for p := range below {
			if !have[p] {
				files[p] = FileSource{Path: p, Deleted: true}
			}
		}
		owner := t.Worktree.Owner
		if owner == "" {
			owner = t.State.Owner
		}
		plan.Layers = append(plan.Layers, LayerPlan{Layer: codegraph.Layer{ID: id, Kind: codegraph.LayerWorktree,
			Parent: parent, Owner: owner, Ref: t.State.Ref, Commit: t.State.Head},
			Sources: []TreeKey{t.State.Key()}, Files: sortedFiles(files)})
	}
	sortLayers(plan.Layers)
	return plan
}

// defaultBranch is the branch the base follows: the GitHub worktree's
// default branch when it has one, else the default branch most client
// worktrees report (the smallest name on a tie), "" when none is known.
func defaultBranch(live []Tree) string {
	for _, t := range live {
		if isGitHub(t) && t.Worktree.DefaultBranch != "" {
			return t.Worktree.DefaultBranch
		}
	}
	votes := map[string]int{}
	seen := map[string]bool{}
	for _, t := range live {
		if seen[t.Worktree.ID] || t.Worktree.DefaultBranch == "" {
			continue
		}
		seen[t.Worktree.ID] = true
		votes[t.Worktree.DefaultBranch]++
	}
	return argmax(votes)
}

// baseSources returns the trees the base is built from and its commit.
func baseSources(live []Tree, def string) ([]Tree, string) {
	if def == "" {
		return nil, ""
	}
	var gh, client []Tree
	for _, t := range live {
		if t.State.Ref != def || pushedCommit(t) == "" {
			continue
		}
		if isGitHub(t) {
			gh = append(gh, t)
		} else {
			client = append(client, t)
		}
	}
	cands := client
	if len(gh) > 0 {
		cands = gh
	}
	if len(cands) == 0 {
		return nil, ""
	}
	best := cands[0]
	for _, t := range cands[1:] {
		if newer(t, best) {
			best = t
		}
	}
	commit := pushedCommit(best)
	if len(gh) > 0 {
		return []Tree{best}, commit
	}
	return filterTrees(cands, func(t Tree) bool { return pushedCommit(t) == commit }), commit
}

// pushedState is what the source trees say about a path at their pushed
// commit.
type pushedState int

const (
	unknown pushedState = iota // only unpushed entries: the pushed version is not known
	present                    // the pushed commit has the path
	absent                     // the pushed commit does not have it
)

// pathKnowledge collects the source trees' entries of one path.
type pathKnowledge struct {
	oids    map[string]int            // git blob id → trees whose pushed version has it
	blobs   map[string]map[string]int // git blob id → blob sha → clean trees that sent it
	sample  map[string]code.Entry     // git blob id + "\x00" + sha (or just the oid) → an entry
	absents int                       // trees where the path is untracked and not unpushed
	unknown int                       // trees where the path is unpushed
}

// pushedKnowledge reads the source trees' entries: an entry that is not
// unpushed shows the pushed version (its git_oid, its blob too when it is
// clean, nothing when it is untracked); an unpushed entry says the pushed
// version differs from HEAD in an unknown way.
func pushedKnowledge(src []Tree) map[string]*pathKnowledge {
	know := map[string]*pathKnowledge{}
	for _, t := range src {
		for _, e := range t.Entries {
			k := know[e.Path]
			if k == nil {
				k = &pathKnowledge{oids: map[string]int{}, blobs: map[string]map[string]int{}, sample: map[string]code.Entry{}}
				know[e.Path] = k
			}
			switch {
			case e.Unpushed:
				k.unknown++
			case e.GitOID == "":
				k.absents++
			default:
				k.oids[e.GitOID]++
				if _, ok := k.sample[e.GitOID]; !ok {
					k.sample[e.GitOID] = e
				}
				if !e.Dirty && e.SHA256 != "" {
					if k.blobs[e.GitOID] == nil {
						k.blobs[e.GitOID] = map[string]int{}
					}
					k.blobs[e.GitOID][e.SHA256]++
					if _, ok := k.sample[e.GitOID+"\x00"+e.SHA256]; !ok {
						k.sample[e.GitOID+"\x00"+e.SHA256] = e
					}
				}
			}
		}
	}
	return know
}

// resolve decides the path's pushed version: the most reported git blob id
// and, for it, the most sent blob (content unknown when no clean tree sent
// one).
func (k *pathKnowledge) resolve(path string) (FileSource, pushedState) {
	switch {
	case len(k.oids) > 0:
		oid := argmax(k.oids)
		sha := argmax(k.blobs[oid])
		e := k.sample[oid]
		if sha != "" {
			e = k.sample[oid+"\x00"+sha]
		}
		f := FileSource{Path: path, SHA256: sha, GitOID: oid, Lang: e.Lang, Mode: e.Mode}
		if sha != "" {
			f.Size = e.Size
		}
		return f, present
	case k.unknown > 0:
		return FileSource{}, unknown
	}
	return FileSource{}, absent
}

// argmax returns the key with the largest count, the smallest key on a tie,
// "" for an empty map.
func argmax(m map[string]int) string {
	best, n := "", 0
	for k, c := range m {
		if c > n || (c == n && k < best) {
			best, n = k, c
		}
	}
	return best
}

// stack is the files visible when upper sits on lower: upper's files
// replace lower's and its tombstones remove them.
func stack(lower, upper map[string]FileSource) map[string]FileSource {
	out := make(map[string]FileSource, len(lower)+len(upper))
	for p, f := range lower {
		out[p] = f
	}
	for p, f := range upper {
		if f.Deleted {
			delete(out, p)
		} else {
			out[p] = f
		}
	}
	return out
}

func filterTrees(ts []Tree, keep func(Tree) bool) []Tree {
	var out []Tree
	for _, t := range ts {
		if keep(t) {
			out = append(out, t)
		}
	}
	return out
}

func treeKeys(ts []Tree) []TreeKey {
	out := make([]TreeKey, 0, len(ts))
	for _, t := range ts {
		out = append(out, t.State.Key())
	}
	return out
}

func sortedFiles(m map[string]FileSource) []FileSource {
	out := make([]FileSource, 0, len(m))
	for _, f := range m {
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b FileSource) int { return cmp.Compare(a.Path, b.Path) })
	return out
}

// Delta is what moves the graph from one selection to the next: the layers
// to put, each with the files to write and the paths to remove, lower layers
// first, and the layers to drop, higher layers first.
type Delta struct {
	Layers  []LayerDelta `json:"layers,omitempty"`
	Dropped []string     `json:"dropped,omitempty"`
}

// LayerDelta is one layer's part of a Delta.
type LayerDelta struct {
	Layer codegraph.Layer `json:"layer"`
	// New is true when the layer was not in the previous selection.
	New bool `json:"new,omitempty"`
	// Put are the files that are new or changed (tombstones included),
	// ordered by path.
	Put []FileSource `json:"put,omitempty"`
	// Remove are the paths the layer no longer holds, ordered.
	Remove []string `json:"remove,omitempty"`
}

// Empty reports whether the delta changes nothing.
func (d Delta) Empty() bool { return len(d.Layers) == 0 && len(d.Dropped) == 0 }

// sameLayer compares the fields selection sets (Updated is the writer's).
func sameLayer(a, b codegraph.Layer) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Parent == b.Parent && a.Owner == b.Owner &&
		a.Ref == b.Ref && a.Commit == b.Commit
}

// Diff returns the writes that turn prev into next. A layer appears in
// Layers when it is new, its node changed, or any of its files did.
func Diff(prev, next Plan) Delta {
	var d Delta
	old := make(map[string]LayerPlan, len(prev.Layers))
	for _, l := range prev.Layers {
		old[l.Layer.ID] = l
	}
	kept := map[string]bool{}
	for _, l := range next.Layers {
		kept[l.Layer.ID] = true
		o, had := old[l.Layer.ID]
		ld := LayerDelta{Layer: l.Layer, New: !had}
		for _, f := range l.Files {
			if of, ok := o.File(f.Path); !ok || !sameFile(of, f) {
				ld.Put = append(ld.Put, f)
			}
		}
		for _, f := range o.Files {
			if _, ok := l.File(f.Path); !ok {
				ld.Remove = append(ld.Remove, f.Path)
			}
		}
		if ld.New || !sameLayer(o.Layer, l.Layer) || len(ld.Put) > 0 || len(ld.Remove) > 0 {
			d.Layers = append(d.Layers, ld)
		}
	}
	var dropped []LayerPlan
	for _, l := range prev.Layers {
		if !kept[l.Layer.ID] {
			dropped = append(dropped, l)
		}
	}
	sortLayers(dropped)
	for i := len(dropped) - 1; i >= 0; i-- {
		d.Dropped = append(d.Dropped, dropped[i].Layer.ID)
	}
	return d
}
