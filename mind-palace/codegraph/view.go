package codegraph

import (
	"fmt"
	"strings"
)

// View is the ordered list of layer ids a read sees, lowest first: the base,
// then a branch layer, then a session overlay, e.g.
// [base, branch:feature@abc, wt:wt_12:feature]. Adapters pass it to their
// queries as a plain list parameter, so a view query stays a property filter
// over a few layer ids.
//
// The visibility rule, which every adapter implements and codegraphtest
// pins:
//   - a file is visible when its layer is in the view, it is not a deletion
//     tombstone, and no layer in the view hides it (its hiddenBy names none
//     of the view's layers);
//   - a symbol, chunk or package is visible when its file is (the indexer
//     replaces a path in a higher layer by recording that layer in the
//     lower file's hiddenBy, and a deletion by a tombstone plus the same
//     hiddenBy entry);
//   - a resolved edge is visible when its own layer is in the view and both
//     its endpoints are visible, so an overlay's re-resolved edges appear
//     only in that overlay's views.
type View []string

// NewView returns the view over layers, lowest first. It refuses an empty
// list, an empty or malformed layer id, and a layer named twice.
func NewView(layers ...string) (View, error) {
	v := View(append([]string(nil), layers...))
	if err := v.Validate(); err != nil {
		return nil, err
	}
	return v, nil
}

// BaseView is the view of the base layer alone.
func BaseView() View { return View{BaseLayerID} }

// Validate checks that the view names at least one layer, every id is a
// well-formed layer id, and none appears twice.
func (v View) Validate() error {
	if len(v) == 0 {
		return fmt.Errorf("%w: a view needs at least one layer", ErrInvalid)
	}
	seen := make(map[string]bool, len(v))
	for _, id := range v {
		if _, err := ParseLayerID(id); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("%w: layer %q appears twice in the view", ErrInvalid, id)
		}
		seen[id] = true
	}
	return nil
}

// Has reports whether layer is one of the view's layers.
func (v View) Has(layer string) bool {
	return v.Rank(layer) >= 0
}

// Rank is layer's position in the view (0 for the lowest), or -1 when the
// view does not include it. Higher ranks win when one symbol id is visible
// in more than one layer.
func (v View) Rank(layer string) int {
	for i, id := range v {
		if id == layer {
			return i
		}
	}
	return -1
}

// Top is the highest layer of the view, the one a session's edits live in.
func (v View) Top() string {
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}

// Hides reports whether any layer in hiddenBy is in the view.
func (v View) Hides(hiddenBy []string) bool {
	for _, h := range hiddenBy {
		if v.Has(h) {
			return true
		}
	}
	return false
}

// NodeVisible is the visibility rule for a node in layer whose file carries
// hiddenBy: the layer is in the view and no layer of the view hides the
// file. Symbols, chunks and packages pass their file's hiddenBy.
func (v View) NodeVisible(layer string, hiddenBy []string) bool {
	return v.Has(layer) && !v.Hides(hiddenBy)
}

// FileVisible is NodeVisible for a file, which a deletion tombstone never
// is: a tombstone only hides the lower files of its path.
func (v View) FileVisible(f File) bool {
	return !f.Deleted && v.NodeVisible(f.Layer, f.HiddenBy)
}

// EdgeVisible is the rule for a resolved edge: its layer is in the view and
// both endpoints are visible (the caller decides those with NodeVisible).
func (v View) EdgeVisible(edgeLayer string, fromVisible, toVisible bool) bool {
	return fromVisible && toVisible && v.Has(edgeLayer)
}

// BaseLayerID is the id of the base layer: the default branch at its pushed
// commit.
const BaseLayerID = "base"

// BranchLayerID is the id of the branch layer of ref at its upstream commit:
// branch:<ref>@<commit>.
func BranchLayerID(ref, commit string) string {
	return LayerBranch + ":" + ref + "@" + commit
}

// WorktreeLayerID is the id of the session overlay of a worktree on ref:
// wt:<worktree>:<ref>.
func WorktreeLayerID(worktree, ref string) string {
	return LayerWorktree + ":" + worktree + ":" + ref
}

// LayerID is a parsed layer id.
type LayerID struct {
	Kind     string // LayerBase, LayerBranch or LayerWorktree
	Ref      string // branch and worktree layers
	Commit   string // branch layers
	Worktree string // worktree layers
}

// String formats the id back into its canonical form.
func (l LayerID) String() string {
	switch l.Kind {
	case LayerBranch:
		return BranchLayerID(l.Ref, l.Commit)
	case LayerWorktree:
		return WorktreeLayerID(l.Worktree, l.Ref)
	}
	return BaseLayerID
}

// ParseLayerID parses base, branch:<ref>@<commit> or wt:<worktree>:<ref>.
// A ref may contain '@' (git allows it outside "@{"), so a branch id splits
// at its last '@'; git refs cannot contain ':', so a worktree id splits at
// the first ':' after its prefix and the worktree id itself has none.
func ParseLayerID(id string) (LayerID, error) {
	bad := func(why string) (LayerID, error) {
		return LayerID{}, fmt.Errorf("%w: layer id %q: %s", ErrInvalid, id, why)
	}
	switch {
	case id == BaseLayerID:
		return LayerID{Kind: LayerBase}, nil
	case strings.HasPrefix(id, LayerBranch+":"):
		rest := strings.TrimPrefix(id, LayerBranch+":")
		at := strings.LastIndexByte(rest, '@')
		if at <= 0 || at == len(rest)-1 {
			return bad("want branch:<ref>@<commit>")
		}
		ref, commit := rest[:at], rest[at+1:]
		if strings.ContainsAny(ref, ":") || strings.ContainsAny(commit, ":@") {
			return bad("want branch:<ref>@<commit>")
		}
		return LayerID{Kind: LayerBranch, Ref: ref, Commit: commit}, nil
	case strings.HasPrefix(id, LayerWorktree+":"):
		rest := strings.TrimPrefix(id, LayerWorktree+":")
		wt, ref, ok := strings.Cut(rest, ":")
		if !ok || wt == "" || ref == "" || strings.Contains(ref, ":") {
			return bad("want wt:<worktree>:<ref>")
		}
		return LayerID{Kind: LayerWorktree, Worktree: wt, Ref: ref}, nil
	}
	return bad("want base, branch:<ref>@<commit> or wt:<worktree>:<ref>")
}
