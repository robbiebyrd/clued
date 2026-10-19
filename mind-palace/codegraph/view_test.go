package codegraph

import (
	"errors"
	"reflect"
	"testing"
)

const (
	tBranch  = "branch:feature@abc123"
	tOverlay = "wt:wt_12:feature"
	tOther   = "wt:wt_99:feature" // another session's overlay on the same branch
)

// TestVisibilityTruthTable pins the rule every adapter implements: a node is
// visible when its layer is in the view and no layer in the view hides its
// file; a tombstone is never a visible file; an edge needs its layer and
// both endpoints visible.
func TestVisibilityTruthTable(t *testing.T) {
	base := BaseView()
	branch := View{BaseLayerID, tBranch}
	overlay := View{BaseLayerID, tBranch, tOverlay}
	other := View{BaseLayerID, tBranch, tOther}

	nodes := []struct {
		name     string
		layer    string
		hiddenBy []string
		deleted  bool
		want     map[string]bool // view name → visible
	}{
		{"base file, not replaced", BaseLayerID, nil, false,
			map[string]bool{"base": true, "branch": true, "overlay": true, "other": true}},
		{"base file replaced by the branch", BaseLayerID, []string{tBranch}, false,
			map[string]bool{"base": true, "branch": false, "overlay": false, "other": false}},
		{"base file replaced by one overlay", BaseLayerID, []string{tOverlay}, false,
			map[string]bool{"base": true, "branch": true, "overlay": false, "other": true}},
		{"base file replaced by both overlays", BaseLayerID, []string{tOverlay, tOther}, false,
			map[string]bool{"base": true, "branch": true, "overlay": false, "other": false}},
		{"branch file", tBranch, nil, false,
			map[string]bool{"base": false, "branch": true, "overlay": true, "other": true}},
		{"branch file replaced by the overlay", tBranch, []string{tOverlay}, false,
			map[string]bool{"base": false, "branch": true, "overlay": false, "other": true}},
		{"overlay file", tOverlay, nil, false,
			map[string]bool{"base": false, "branch": false, "overlay": true, "other": false}},
		{"hidden by a layer of no view", BaseLayerID, []string{"wt:gone:main"}, false,
			map[string]bool{"base": true, "branch": true, "overlay": true, "other": true}},
		{"overlay tombstone", tOverlay, nil, true,
			map[string]bool{"base": false, "branch": false, "overlay": false, "other": false}},
		{"branch tombstone", tBranch, nil, true,
			map[string]bool{"base": false, "branch": false, "overlay": false, "other": false}},
	}
	views := map[string]View{"base": base, "branch": branch, "overlay": overlay, "other": other}
	for _, n := range nodes {
		for vn, v := range views {
			want := n.want[vn]
			f := File{Layer: n.layer, Path: "a.go", HiddenBy: n.hiddenBy, Deleted: n.deleted}
			if got := v.FileVisible(f); got != want {
				t.Errorf("%s in %s view: FileVisible = %v, want %v", n.name, vn, got, want)
			}
			// Symbols, chunks and packages follow their file; a tombstone has
			// none, so NodeVisible ignores Deleted.
			if !n.deleted {
				if got := v.NodeVisible(n.layer, n.hiddenBy); got != want {
					t.Errorf("%s in %s view: NodeVisible = %v, want %v", n.name, vn, got, want)
				}
			}
		}
	}
}

// TestDeletionHidesLowerFile: a deletion in the overlay is a tombstone plus
// the overlay's id in the lower file's hiddenBy; neither shows in the
// overlay's view, the lower file still shows everywhere else.
func TestDeletionHidesLowerFile(t *testing.T) {
	lower := File{Layer: BaseLayerID, Path: "x.go", HiddenBy: []string{tOverlay}}
	tomb := File{Layer: tOverlay, Path: "x.go", Deleted: true}
	overlay := View{BaseLayerID, tBranch, tOverlay}
	if overlay.FileVisible(lower) || overlay.FileVisible(tomb) {
		t.Fatal("deleted path visible in the overlay's view")
	}
	if !(View{BaseLayerID, tBranch, tOther}).FileVisible(lower) {
		t.Fatal("another overlay's deletion hid the file")
	}
}

func TestEdgeVisibilityTruthTable(t *testing.T) {
	overlay := View{BaseLayerID, tBranch, tOverlay}
	cases := []struct {
		layer    string
		from, to bool
		want     bool
	}{
		{BaseLayerID, true, true, true},
		{tOverlay, true, true, true},
		{tOther, true, true, false}, // resolved by another overlay
		{BaseLayerID, false, true, false},
		{BaseLayerID, true, false, false},
		{tOverlay, false, false, false},
	}
	for _, c := range cases {
		if got := overlay.EdgeVisible(c.layer, c.from, c.to); got != c.want {
			t.Errorf("EdgeVisible(%s, %v, %v) = %v, want %v", c.layer, c.from, c.to, got, c.want)
		}
	}
	// A base caller re-resolved by the overlay to its renamed symbol: the
	// overlay's edge shows only in the overlay's view; the base edge to the
	// old symbol disappears there because its target file is hidden.
	callerVisible := overlay.NodeVisible(BaseLayerID, nil)
	oldTarget := overlay.NodeVisible(BaseLayerID, []string{tOverlay})
	newTarget := overlay.NodeVisible(tOverlay, nil)
	if overlay.EdgeVisible(BaseLayerID, callerVisible, oldTarget) {
		t.Error("base edge to the replaced symbol visible in the overlay's view")
	}
	if !overlay.EdgeVisible(tOverlay, callerVisible, newTarget) {
		t.Error("overlay edge to the renamed symbol hidden in the overlay's view")
	}
	base := BaseView()
	if base.EdgeVisible(tOverlay, base.NodeVisible(BaseLayerID, nil), base.NodeVisible(tOverlay, nil)) {
		t.Error("overlay edge visible in the base view")
	}
}

func TestViewRankAndTop(t *testing.T) {
	v := View{BaseLayerID, tBranch, tOverlay}
	if v.Rank(BaseLayerID) != 0 || v.Rank(tOverlay) != 2 || v.Rank(tOther) != -1 {
		t.Fatalf("Rank wrong: %d %d %d", v.Rank(BaseLayerID), v.Rank(tOverlay), v.Rank(tOther))
	}
	if v.Top() != tOverlay || (View{}).Top() != "" {
		t.Fatal("Top wrong")
	}
}

func TestNewView(t *testing.T) {
	if _, err := NewView(BaseLayerID, tBranch, tOverlay); err != nil {
		t.Fatal(err)
	}
	bad := [][]string{
		nil,
		{""},
		{BaseLayerID, BaseLayerID},
		{"main"},
		{BaseLayerID, "branch:feature"},
	}
	for _, b := range bad {
		if _, err := NewView(b...); !errors.Is(err, ErrInvalid) {
			t.Errorf("NewView(%q) err = %v, want ErrInvalid", b, err)
		}
	}
	in := []string{BaseLayerID}
	v, _ := NewView(in...)
	in[0] = "changed"
	if v[0] != BaseLayerID {
		t.Fatal("NewView aliases its argument")
	}
}

func TestLayerIDs(t *testing.T) {
	good := []struct {
		id   string
		want LayerID
	}{
		{"base", LayerID{Kind: LayerBase}},
		{"branch:feature@abc", LayerID{Kind: LayerBranch, Ref: "feature", Commit: "abc"}},
		{"branch:user/x@y@0f0f", LayerID{Kind: LayerBranch, Ref: "user/x@y", Commit: "0f0f"}},
		{"wt:wt_12:feature/a", LayerID{Kind: LayerWorktree, Worktree: "wt_12", Ref: "feature/a"}},
	}
	for _, g := range good {
		got, err := ParseLayerID(g.id)
		if err != nil {
			t.Errorf("ParseLayerID(%q): %v", g.id, err)
			continue
		}
		if got != g.want {
			t.Errorf("ParseLayerID(%q) = %+v, want %+v", g.id, got, g.want)
		}
		if got.String() != g.id {
			t.Errorf("round trip of %q gave %q", g.id, got.String())
		}
	}
	if BranchLayerID("main", "c0ffee") != "branch:main@c0ffee" || WorktreeLayerID("w1", "main") != "wt:w1:main" {
		t.Error("layer id constructors")
	}
	for _, b := range []string{"", "Base", "branch:", "branch:x", "branch:@c", "branch:x@", "branch:a:b@c",
		"wt:", "wt:w1", "wt::main", "wt:w1:", "wt:w1:a:b", "tag:v1"} {
		if _, err := ParseLayerID(b); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseLayerID(%q) err = %v, want ErrInvalid", b, err)
		}
	}
}

func TestLayerValidate(t *testing.T) {
	if err := (Layer{ID: tBranch, Kind: LayerBranch, Parent: BaseLayerID}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Layer{ID: tBranch, Kind: LayerWorktree}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kind mismatch err = %v", err)
	}
	if err := (Layer{ID: tOverlay, Kind: LayerWorktree, Parent: "nope"}).Validate(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad parent err = %v", err)
	}
}

func TestValidPathAndKey(t *testing.T) {
	for _, p := range []string{"a.go", "cli/run.go", "a/b/c.d.ts"} {
		if err := ValidPath(p); err != nil {
			t.Errorf("ValidPath(%q): %v", p, err)
		}
	}
	for _, p := range []string{"", "/etc/passwd", "../x", "a/../b", "a//b", "./a", "a\\b", "a/"} {
		if err := ValidPath(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidPath(%q) err = %v", p, err)
		}
	}
	if err := ValidKey("u0123456789abcdef__github.com__o__r"); err != nil {
		t.Error(err)
	}
	for _, k := range []string{"", "a b", "x\n", "{evil}", "_x"} {
		if err := ValidKey(k); !errors.Is(err, ErrInvalid) {
			t.Errorf("ValidKey(%q) err = %v", k, err)
		}
	}
}

func TestFileFactsValidate(t *testing.T) {
	f := File{Layer: BaseLayerID, Path: "a.go", Package: "a"}
	sym := func(id, parent string) Symbol {
		return Symbol{Layer: BaseLayerID, Path: "a.go", ID: id, Name: id, Kind: SymbolFunction, Parent: parent}
	}
	ok := FileFacts{File: f, Symbols: []Symbol{sym("T", ""), sym("T.m", "T")},
		Chunks:  []Chunk{{Layer: BaseLayerID, Path: "a.go", ID: "c1", Symbol: "T"}},
		Imports: []Import{{Package: "fmt"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (FileFacts{File: File{Layer: tOverlay, Path: "a.go", Deleted: true}}).Validate(); err != nil {
		t.Fatalf("tombstone: %v", err)
	}
	bad := map[string]FileFacts{
		"tombstone with symbols":  {File: File{Layer: tOverlay, Path: "a.go", Deleted: true}, Symbols: []Symbol{sym("T", "")}},
		"symbol of another layer": {File: f, Symbols: []Symbol{{Layer: tBranch, Path: "a.go", ID: "T", Name: "T"}}},
		"symbol of another path":  {File: f, Symbols: []Symbol{{Layer: BaseLayerID, Path: "b.go", ID: "T", Name: "T"}}},
		"duplicate symbol":        {File: f, Symbols: []Symbol{sym("T", ""), sym("T", "")}},
		"unknown parent":          {File: f, Symbols: []Symbol{sym("m", "T")}},
		"self parent":             {File: f, Symbols: []Symbol{sym("T", "T")}},
		"chunk of unknown symbol": {File: f, Chunks: []Chunk{{Layer: BaseLayerID, Path: "a.go", ID: "c", Symbol: "X"}}},
		"duplicate chunk":         {File: f, Chunks: []Chunk{{Layer: BaseLayerID, Path: "a.go", ID: "c"}, {Layer: BaseLayerID, Path: "a.go", ID: "c"}}},
		"empty import":            {File: f, Imports: []Import{{}}},
		"bad path":                {File: File{Layer: BaseLayerID, Path: "../a.go"}},
	}
	for name, ff := range bad {
		if err := ff.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestValidateEdges(t *testing.T) {
	from := []NodeRef{{Layer: BaseLayerID, ID: "caller"}}
	e := Edge{Type: EdgeCalls, Layer: tOverlay, From: from[0], To: NodeRef{Layer: tOverlay, ID: "renamed"},
		Confidence: ConfidenceQualified, Count: 1, Line: 12}
	if err := ValidateEdges(tOverlay, from, []Edge{e}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEdges(tOverlay, from, nil); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	mut := func(f func(*Edge)) Edge { c := e; f(&c); return c }
	bad := map[string]Edge{
		"structural type":   mut(func(e *Edge) { e.Type = EdgeContains }),
		"unknown conf":      mut(func(e *Edge) { e.Confidence = "guess" }),
		"other layer":       mut(func(e *Edge) { e.Layer = BaseLayerID }),
		"not a source":      mut(func(e *Edge) { e.From.ID = "someone" }),
		"missing target id": mut(func(e *Edge) { e.To.ID = "" }),
		"negative count":    mut(func(e *Edge) { e.Count = -1 }),
	}
	for name, b := range bad {
		if err := ValidateEdges(tOverlay, from, []Edge{b}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	for _, typ := range ResolvedEdgeTypes {
		if !typ.Resolved() {
			t.Errorf("%s not resolved", typ)
		}
	}
	if EdgeImports.Resolved() || EdgeInPackage.Resolved() {
		t.Error("structural edge reported as resolved")
	}
}

func TestRelations(t *testing.T) {
	cases := map[Relation]struct {
		types    []EdgeType
		incoming bool
	}{
		RelCallers:         {[]EdgeType{EdgeCalls}, true},
		RelCallees:         {[]EdgeType{EdgeCalls}, false},
		RelReferences:      {[]EdgeType{EdgeReferences}, true},
		RelImplementations: {[]EdgeType{EdgeImplements, EdgeExtends}, true},
		RelImports:         {[]EdgeType{EdgeImports}, false},
		RelImporters:       {[]EdgeType{EdgeImports}, true},
	}
	for r, c := range cases {
		types, in, ok := r.Edges()
		if !ok || in != c.incoming || !reflect.DeepEqual(types, c.types) {
			t.Errorf("%s.Edges() = %v %v %v", r, types, in, ok)
		}
	}
	if _, _, ok := Relation("siblings").Edges(); ok {
		t.Error("unknown relation accepted")
	}
	good := []NeighborQuery{
		{ID: "f", Relation: RelCallers, Depth: 3},
		{Path: "a.go", Relation: RelImports},
		{Package: "fmt", Relation: RelImporters},
	}
	for _, q := range good {
		if err := q.Validate(); err != nil {
			t.Errorf("%+v: %v", q, err)
		}
	}
	bad := []NeighborQuery{
		{ID: "f", Relation: RelCallers, Depth: MaxDepth + 1},
		{ID: "f", Relation: RelCallers, Depth: -1},
		{Relation: RelCallees},
		{Relation: RelImports},
		{Relation: RelImporters},
		{ID: "f", Relation: "siblings"},
	}
	for _, q := range bad {
		if err := q.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v", q, err)
		}
	}
}

func TestFilterAndLimit(t *testing.T) {
	f := Filter{Kinds: []string{SymbolFunction}, Langs: []string{"go"}, Paths: []string{"cli/"}}
	if !f.Match(SymbolFunction, "go", "cli/run.go") {
		t.Error("match rejected")
	}
	for _, c := range [][3]string{{SymbolType, "go", "cli/run.go"}, {SymbolFunction, "python", "cli/run.go"}, {SymbolFunction, "go", "client.go"}} {
		if f.Match(c[0], c[1], c[2]) {
			t.Errorf("Match(%q) passed", c)
		}
	}
	if !(Filter{}).Match("x", "y", "z") || !(Filter{Paths: []string{"a.go"}}).Match("", "", "a.go") {
		t.Error("empty filter or exact path rejected")
	}
	if EffectiveLimit(0) != DefaultLimit || EffectiveLimit(MaxLimit+1) != MaxLimit || EffectiveLimit(7) != 7 {
		t.Error("EffectiveLimit")
	}
}

func TestOutlineQueryAndBuildOutline(t *testing.T) {
	if (OutlineQuery{}).Validate() == nil || (OutlineQuery{Path: "a", Package: "b"}).Validate() == nil {
		t.Error("outline query needs exactly one of path and package")
	}
	if (OutlineQuery{Package: "p"}).Validate() != nil || (OutlineQuery{Path: "a.go"}).Validate() != nil {
		t.Error("valid outline query refused")
	}
	syms := []Symbol{
		{ID: "T.b", Parent: "T", Start: 9},
		{ID: "f", Start: 20},
		{ID: "T", Start: 3},
		{ID: "T.a", Parent: "T", Start: 5},
		{ID: "orphan", Parent: "gone", Start: 1},
	}
	out := BuildOutline(syms)
	var ids []string
	for _, n := range out {
		ids = append(ids, n.Symbol.ID)
	}
	if !reflect.DeepEqual(ids, []string{"orphan", "T", "f"}) {
		t.Fatalf("roots = %v", ids)
	}
	if len(out[1].Children) != 2 || out[1].Children[0].Symbol.ID != "T.a" || out[1].Children[1].Symbol.ID != "T.b" {
		t.Fatalf("children of T = %+v", out[1].Children)
	}
	// A parent cycle cannot loop forever.
	cyc := BuildOutline([]Symbol{{ID: "a", Parent: "b"}, {ID: "b", Parent: "a"}})
	if len(cyc) != 0 {
		t.Fatalf("cycle produced roots %+v", cyc)
	}
}

func TestSchemaIndexes(t *testing.T) {
	var fulltext int
	for _, ix := range Indexes {
		if len(ix.Props) == 0 || ix.Label == "" {
			t.Errorf("empty index %+v", ix)
		}
		if ix.Kind == IndexFullText {
			fulltext++
			if ix.Label != LabelSymbol || !reflect.DeepEqual(ix.Props, []string{PropName, PropSignature, PropDoc}) {
				t.Errorf("full-text index %+v", ix)
			}
		}
	}
	if fulltext != 1 {
		t.Errorf("%d full-text indexes, want 1", fulltext)
	}
	if ConfidenceRank(ConfidenceExact) >= ConfidenceRank(ConfidenceQualified) ||
		ConfidenceRank(ConfidenceQualified) >= ConfidenceRank(ConfidenceName) ||
		ConfidenceRank("other") <= ConfidenceRank(ConfidenceName) {
		t.Error("confidence ranking")
	}
}
