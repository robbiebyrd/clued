package codeapi

// The graph read routes: the first read path for collected code (plan 0012,
// "Read API and agent tools"), for agents only and only for the caller's own
// view. They sit beside the write routes under
// /api/v1/projects/{project}/code/graph/, go through the same checks
// (account, slug, Allow, Namespace, Open) and read the project's code graph
// (codegraph.Graph, opened under the same namespaced key) with typed
// operations; no route takes Cypher.
//
//	POST /api/v1/projects/{project}/code/graph/search     {view, query, kinds?, langs?, paths?, limit?}
//	POST /api/v1/projects/{project}/code/graph/symbol     {view, id | qualName, include?: [body, doc, children]}
//	POST /api/v1/projects/{project}/code/graph/neighbors  {view, id | path | package, relation, depth?, limit?}
//	POST /api/v1/projects/{project}/code/graph/outline    {view, path | package}
//	POST /api/v1/projects/{project}/code/graph/context    {view, task, budget?}
//
// Every body also takes max_tokens and cursor (session/sessionmcp's
// convention): a result is cut to about max_tokens (DefaultGraphTokens,
// DefaultContextTokens for context), says "truncated": true when it was,
// and its "next", passed back as cursor with the other fields unchanged,
// continues it.
//
// View resolution. "view" is {"session_id"} (the session's own overlay over
// its branch layer and the base; the session must be on a worktree the
// caller owns, else 404), {"ref"} (the base plus the newest branch layer of
// that ref), {"session_id", "ref"} (the session's overlay on that ref), or
// omitted (the base). Overlays are private to their owner: no view ever
// includes another account's overlay, in a workspace project too. A view
// whose branch layer was dropped falls back to the newest branch layer of
// its ref, then the base; a session whose worktree has no overlay (code
// sync off, nothing indexed yet) falls back to its ref's layers, and the
// result's view says "localEdits": false.
//
// Answers. Each result carries "status": "ready"; "indexing" when the graph
// does not yet hold every collected change (the results are what is ready,
// "progress" says how much is left; a project not indexed at all answers
// indexing with no results); "empty" when no code was collected. A graph
// backend that cannot be reached answers 503 unavailable, so an agent falls
// back to grep. Unknown projects, sessions, symbols and paths are 404.
// Bodies come from the stored blobs, which hold what the clients collected
// after redaction.

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// Graph route patterns, without their method, as MountGraph registers them.
const (
	PathGraphSearch    = Prefix + "/{project}/code/graph/search"
	PathGraphSymbol    = Prefix + "/{project}/code/graph/symbol"
	PathGraphNeighbors = Prefix + "/{project}/code/graph/neighbors"
	PathGraphOutline   = Prefix + "/{project}/code/graph/outline"
	PathGraphContext   = Prefix + "/{project}/code/graph/context"
)

// Token budgets of the graph routes when max_tokens (budget for context)
// is not set; query.MinTokens is the smallest honoured.
const (
	DefaultGraphTokens   = 2000
	DefaultContextTokens = 6000
)

// KindUnavailable is the error kind (503) of a graph backend that cannot be
// reached.
const KindUnavailable = "unavailable"

// Result statuses.
const (
	// StatusReady: the graph holds every collected change.
	StatusReady = "ready"
	// StatusIndexing: the indexer has not caught up; the results are what
	// is ready (none when the project was never indexed).
	StatusIndexing = "indexing"
	// StatusEmpty: no code has been collected for the project.
	StatusEmpty = "empty"
)

// Limits of the graph routes.
const (
	// maxPendingCount bounds the changes counted for progress.
	maxPendingCount = 1000
	// maxQueryBytes bounds a search query and a context task.
	maxQueryBytes = 1024
	// maxFilterItems bounds kinds, langs and paths.
	maxFilterItems = 32
	// contextHits, contextNeighbors and contextWords shape a context bundle:
	// the hits it keeps, the callers and callees per hit, and the words of
	// the task it searches for.
	contextHits      = 8
	contextNeighbors = 5
	contextWords     = 12
	// maxDocLine bounds the one-line doc of a hit.
	maxDocLine = 200
	// defaultSearchLimit is search's limit when the body sets none.
	defaultSearchLimit = 20
)

// The include values of symbol.
const (
	IncludeBody     = "body"
	IncludeDoc      = "doc"
	IncludeChildren = "children"
)

// GraphOptions configure the graph read routes.
type GraphOptions struct {
	// Graphs opens a project's graph by its namespaced key (the key
	// Options.Namespace returns). Required.
	Graphs codegraph.Opener
	// Indexed reports the change-log position the indexer has written into
	// the key's graph; ok is false when it is not known (the indexer has
	// not looked at the project since the server started). Nil: progress
	// is reported from the graph alone.
	Indexed func(ctx context.Context, key string) (seq int64, ok bool)
}

// ViewBody is the "view" of every graph body; see the package
// documentation of the graph routes.
type ViewBody struct {
	SessionID string `json:"session_id,omitempty"`
	Ref       string `json:"ref,omitempty"`
}

// SearchBody is the body of POST graph/search.
type SearchBody struct {
	View      *ViewBody `json:"view,omitempty"`
	Query     string    `json:"query"`
	Kinds     []string  `json:"kinds,omitempty"`
	Langs     []string  `json:"langs,omitempty"`
	Paths     []string  `json:"paths,omitempty"`
	Limit     int       `json:"limit,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Cursor    string    `json:"cursor,omitempty"`
}

// SymbolBody is the body of POST graph/symbol: the symbol by id, or by
// qualified name when id is empty. Include defaults to all three parts.
type SymbolBody struct {
	View      *ViewBody `json:"view,omitempty"`
	ID        string    `json:"id,omitempty"`
	QualName  string    `json:"qualName,omitempty"`
	Include   []string  `json:"include,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Cursor    string    `json:"cursor,omitempty"`
}

// NeighborsBody is the body of POST graph/neighbors: a symbol id for
// callers, callees, references and implementations, a file path for
// imports, a package name for importers.
type NeighborsBody struct {
	View      *ViewBody `json:"view,omitempty"`
	ID        string    `json:"id,omitempty"`
	Path      string    `json:"path,omitempty"`
	Package   string    `json:"package,omitempty"`
	Relation  string    `json:"relation"`
	Depth     int       `json:"depth,omitempty"`
	Limit     int       `json:"limit,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Cursor    string    `json:"cursor,omitempty"`
}

// OutlineBody is the body of POST graph/outline: a file path or a package.
type OutlineBody struct {
	View      *ViewBody `json:"view,omitempty"`
	Path      string    `json:"path,omitempty"`
	Package   string    `json:"package,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Cursor    string    `json:"cursor,omitempty"`
}

// ContextBody is the body of POST graph/context. Budget is its token
// budget (max_tokens is accepted for it too).
type ContextBody struct {
	View      *ViewBody `json:"view,omitempty"`
	Task      string    `json:"task"`
	Budget    int       `json:"budget,omitempty"`
	MaxTokens int       `json:"max_tokens,omitempty"`
	Cursor    string    `json:"cursor,omitempty"`
}

// ViewInfo says which view a result answers for.
type ViewInfo struct {
	// Layers are the layer ids of the view, lowest first.
	Layers []string `json:"layers"`
	// Session and Ref echo what the view resolved from.
	Session string `json:"session,omitempty"`
	Ref     string `json:"ref,omitempty"`
	// LocalEdits is true when the session's overlay (its dirty and
	// unpushed files) is part of the view.
	LocalEdits bool `json:"localEdits"`
	// Note explains a fallback.
	Note string `json:"note,omitempty"`
}

// Progress is how far the indexer is behind the collected code.
type Progress struct {
	// Pending counts the collected changes not yet in the graph (at least
	// that many when More).
	Pending int  `json:"pending"`
	More    bool `json:"more,omitempty"`
}

// Answer is what every graph result starts with.
type Answer struct {
	Status   string    `json:"status"`
	Progress *Progress `json:"progress,omitempty"`
	View     ViewInfo  `json:"view"`
}

// Hit is one search or context hit: a symbol, where it is, and why it
// matched ("qualified" or "exact" name, or "text").
type Hit struct {
	ID        string  `json:"id"`
	Layer     string  `json:"layer"`
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	QualName  string  `json:"qualName,omitempty"`
	Signature string  `json:"signature,omitempty"`
	Location  string  `json:"location"`
	Match     string  `json:"match"`
	Score     float64 `json:"score"`
	Doc       string  `json:"doc,omitempty"`
}

// Matches of a Hit.
const (
	MatchQualified = "qualified"
	MatchExact     = "exact"
	MatchText      = "text"
)

// ListResult is the answer of search and neighbors: the items (Hit,
// NeighborItem) that fit the budget.
type ListResult struct {
	Answer
	Items     []any  `json:"items"`
	Truncated bool   `json:"truncated"`
	Next      string `json:"next,omitempty"`
}

// NeighborItem is one row of neighbors with its location (path:line).
type NeighborItem struct {
	codegraph.NeighborRow
	Location string `json:"location"`
}

// SymbolLine is a symbol in a list (children, outline): no doc, no body.
type SymbolLine struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
	Location  string `json:"location"`
	Depth     int    `json:"depth,omitempty"`
	Exported  bool   `json:"exported,omitempty"`
}

// Body is a symbol's source lines, from the stored blob.
type Body struct {
	Location string `json:"location"`
	Text     string `json:"text"`
}

// SymbolResult is the answer of symbol.
type SymbolResult struct {
	Answer
	Symbol   *codegraph.Symbol `json:"symbol,omitempty"`
	Location string            `json:"location,omitempty"`
	Lang     string            `json:"lang,omitempty"`
	Children []SymbolLine      `json:"children,omitempty"`
	Body     *Body             `json:"body,omitempty"`
	// BodyNote says why there is no body (content not collected).
	BodyNote  string `json:"bodyNote,omitempty"`
	Truncated bool   `json:"truncated"`
	Next      string `json:"next,omitempty"`
}

// OutlineFile is a file of an outline.
type OutlineFile struct {
	Path       string `json:"path"`
	Lang       string `json:"lang,omitempty"`
	Package    string `json:"package,omitempty"`
	ParseError bool   `json:"parseError,omitempty"`
}

// OutlineResult is the answer of outline: the files, and their symbol
// trees flattened in order (Depth 0 for top-level symbols).
type OutlineResult struct {
	Answer
	Files     []OutlineFile `json:"files"`
	Items     []any         `json:"items"`
	Truncated bool          `json:"truncated"`
	Next      string        `json:"next,omitempty"`
}

// ContextResult is the answer of context: the top hits, one hop of their
// callers and callees, and the bodies that fit the budget, best hit first.
// Its next continues the bodies.
type ContextResult struct {
	Answer
	Hits      []any  `json:"hits"`
	Neighbors []any  `json:"neighbors"`
	Bodies    []any  `json:"bodies"`
	Truncated bool   `json:"truncated"`
	Next      string `json:"next,omitempty"`
}

// ContextBodyItem is one body of a context bundle.
type ContextBodyItem struct {
	ID       string `json:"id"`
	Location string `json:"location"`
	Text     string `json:"text"`
}

// Cursor tools of the graph routes (query.Cursor.Tool), so a cursor of one
// route is refused by another.
const (
	cursorSearch    = "graph.search"
	cursorSymbol    = "graph.symbol"
	cursorNeighbors = "graph.neighbors"
	cursorOutline   = "graph.outline"
	cursorContext   = "graph.context"
)

// MountGraph registers the five graph read routes on mux, behind the same
// checks as the write routes (opts as for Mount). The composer registers
// the Pattern catch-all as for Mount.
func MountGraph(mux *http.ServeMux, opts Options, g GraphOptions) {
	if opts.Open == nil {
		panic("codeapi: Options.Open is required")
	}
	if g.Graphs == nil {
		panic("codeapi: GraphOptions.Graphs is required")
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.Account == nil {
		opts.Account = func(r *http.Request) (string, bool) {
			p, ok := auth.FromContext(r.Context())
			return p.AccountID, ok
		}
	}
	ga := &graphAPI{api: &api{opts: opts}, g: g}
	mux.HandleFunc("POST "+PathGraphSearch, ga.handle("", ga.search))
	mux.HandleFunc("POST "+PathGraphSymbol, ga.handle("", ga.symbol))
	mux.HandleFunc("POST "+PathGraphNeighbors, ga.handle("", ga.neighbors))
	mux.HandleFunc("POST "+PathGraphOutline, ga.handle("", ga.outline))
	mux.HandleFunc("POST "+PathGraphContext, ga.handle("", ga.context))
}

type graphAPI struct {
	*api
	g GraphOptions
}

// graphRequest is a request whose graph is open and whose view is resolved.
type graphRequest struct {
	*request
	graph  codegraph.Graph
	view   codegraph.View
	answer Answer
	// ready is false when the graph holds no layer: there is nothing to
	// read yet.
	ready bool
}

// writeGraphError maps a graph or store error of a read to the envelope.
func writeGraphError(w http.ResponseWriter, err error, what string) {
	var se *httpapi.StatusError
	switch {
	case errors.As(err, &se):
		httpapi.WriteError(w, se.Status, se.Kind, se.Message)
	case errors.Is(err, codegraph.ErrNotFound), errors.Is(err, code.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, what+" not found")
	case errors.Is(err, codegraph.ErrInvalid), errors.Is(err, code.ErrInvalid), errors.Is(err, query.ErrBadCursor):
		httpapi.WriteError(w, http.StatusBadRequest, KindInvalid, err.Error())
	case errors.Is(err, codegraph.ErrUnavailable):
		w.Header().Set("Retry-After", "30")
		httpapi.WriteError(w, http.StatusServiceUnavailable, KindUnavailable, "the code graph is unavailable; search the files directly")
	default:
		w.Header().Set("Retry-After", "30")
		httpapi.WriteError(w, http.StatusServiceUnavailable, KindUnavailable, "the code graph failed: "+err.Error())
	}
}

// open opens the request's graph, resolves its view and works out the
// status; false when it answered.
func (ga *graphAPI) open(w http.ResponseWriter, r *http.Request, req *request, vb *ViewBody) (*graphRequest, bool) {
	ctx := r.Context()
	g, err := ga.g.Graphs.Open(ctx, req.key)
	if err != nil {
		writeGraphError(w, err, "project")
		return nil, false
	}
	layers, err := g.Layers(ctx)
	if err != nil {
		writeGraphError(w, err, "project")
		return nil, false
	}
	view, info, err := resolveView(ctx, req, layers, vb)
	if err != nil {
		writeGraphError(w, err, "session")
		return nil, false
	}
	gr := &graphRequest{request: req, graph: g, view: view, ready: len(layers) > 0}
	gr.answer = Answer{Status: StatusReady, View: info}
	if err := ga.status(ctx, gr); err != nil {
		writeGraphError(w, err, "project")
		return nil, false
	}
	return gr, true
}

// status fills the answer's status and progress: the changes collected
// after the position the indexer reported, or, without one, every change
// when the graph is still empty.
func (ga *graphAPI) status(ctx context.Context, gr *graphRequest) error {
	var after int64
	known := false
	if ga.g.Indexed != nil {
		after, known = ga.g.Indexed(ctx, gr.key)
	}
	if !known && gr.ready {
		return nil
	}
	changes, err := gr.store.Changes(ctx, after, maxPendingCount+1)
	if err != nil {
		return err
	}
	switch {
	case len(changes) > 0:
		gr.answer.Status = StatusIndexing
		gr.answer.Progress = &Progress{Pending: min(len(changes), maxPendingCount), More: len(changes) > maxPendingCount}
	case !gr.ready:
		// Nothing collected, or caught up with nothing to show (no default
		// branch known, every tree empty).
		gr.answer.Status = StatusEmpty
	}
	return nil
}

// resolveView turns the body's view into the layers a read sees. Only
// overlays of worktrees the caller owns are ever chosen.
func resolveView(ctx context.Context, req *request, layers []codegraph.Layer, vb *ViewBody) (codegraph.View, ViewInfo, error) {
	var sessionID, ref string
	if vb != nil {
		sessionID, ref = vb.SessionID, vb.Ref
	}
	if ref != "" && !code.ValidRef(ref) {
		return nil, ViewInfo{}, fmt.Errorf("%w: view ref %q", codegraph.ErrInvalid, ref)
	}
	if sessionID != "" && !validSessionID(sessionID) {
		return nil, ViewInfo{}, fmt.Errorf("%w: view session_id %q", codegraph.ErrInvalid, sessionID)
	}
	byID := make(map[string]codegraph.Layer, len(layers))
	for _, l := range layers {
		byID[l.ID] = l
	}
	info := ViewInfo{Session: sessionID, Ref: ref}
	if sessionID == "" {
		if ref == "" {
			info.Layers = []string{codegraph.BaseLayerID}
			return codegraph.BaseView(), info, nil
		}
		ids, note := refChain(byID, layers, ref)
		info.Layers, info.Note = ids, note
		v, err := codegraph.NewView(ids...)
		return v, info, err
	}

	wt, err := sessionWorktree(ctx, req, sessionID)
	if err != nil {
		return nil, ViewInfo{}, err
	}
	var overlays []codegraph.Layer
	for _, l := range layers {
		p, err := codegraph.ParseLayerID(l.ID)
		if err != nil || p.Kind != codegraph.LayerWorktree || p.Worktree != wt.ID {
			continue
		}
		if l.Owner != "" && l.Owner != req.account {
			continue
		}
		if ref != "" && p.Ref != ref {
			continue
		}
		overlays = append(overlays, l)
	}
	overlay, ok, err := sessionOverlay(ctx, req, wt.ID, sessionID, overlays)
	if err != nil {
		return nil, ViewInfo{}, err
	}
	if !ok {
		// No overlay: the worktree is not synced (or not indexed yet). Show
		// the ref's layers and say the local edits are missing.
		if ref == "" && len(wt.Refs) == 1 {
			ref = wt.Refs[0]
		}
		ids := []string{codegraph.BaseLayerID}
		if ref != "" {
			ids, _ = refChain(byID, layers, ref)
		}
		info.Layers = ids
		info.Note = "the session's worktree has no indexed overlay: the results do not include its local edits"
		v, err := codegraph.NewView(ids...)
		return v, info, err
	}
	ids, note := overlayChain(byID, layers, overlay)
	info.Layers, info.Note, info.LocalEdits = ids, note, true
	info.Ref = overlay.Ref
	v, err := codegraph.NewView(ids...)
	return v, info, err
}

// sessionWorktree finds the worktree the session was seen on among the
// worktrees the caller owns; an unknown session, or one on another
// account's worktree, is not found.
func sessionWorktree(ctx context.Context, req *request, sessionID string) (code.Worktree, error) {
	ws, err := req.store.Worktrees(ctx)
	if err != nil {
		return code.Worktree{}, err
	}
	var found []code.Worktree
	for _, w := range ws {
		if w.Owner != req.account || w.Removed || w.Source == code.SourceGitHub {
			continue
		}
		for _, s := range w.Sessions {
			if s.ID == sessionID {
				found = append(found, w)
				break
			}
		}
	}
	if len(found) == 0 {
		return code.Worktree{}, fmt.Errorf("%w: session %s", code.ErrNotFound, sessionID)
	}
	// A session seen on several worktrees (it changed directory) works in
	// the one it touched last.
	best := found[0]
	for _, w := range found[1:] {
		if lastSeen(w, sessionID).After(lastSeen(best, sessionID)) {
			best = w
		}
	}
	return best, nil
}

// lastSeen is the session's latest activity on w.
func lastSeen(w code.Worktree, sessionID string) time.Time {
	for _, s := range w.Sessions {
		if s.ID == sessionID {
			return s.LastAt
		}
	}
	return time.Time{}
}

// sessionOverlay picks the session's overlay among the worktree's: the only
// one, else the one whose tree holds most entries the session wrote, else
// the one updated last.
func sessionOverlay(ctx context.Context, req *request, worktree, sessionID string, overlays []codegraph.Layer) (codegraph.Layer, bool, error) {
	switch len(overlays) {
	case 0:
		return codegraph.Layer{}, false, nil
	case 1:
		return overlays[0], true, nil
	}
	best, bestN := -1, 0
	for i, l := range overlays {
		es, err := req.store.Tree(ctx, worktree, l.Ref)
		if err != nil {
			return codegraph.Layer{}, false, err
		}
		n := 0
		for _, e := range es {
			if e.Session == sessionID {
				n++
			}
		}
		if n > bestN {
			best, bestN = i, n
		}
	}
	if best >= 0 {
		return overlays[best], true, nil
	}
	return newestLayer(overlays), true, nil
}

// newestLayer is the layer updated last (the largest id on a tie).
func newestLayer(ls []codegraph.Layer) codegraph.Layer {
	return slices.MaxFunc(ls, func(a, b codegraph.Layer) int {
		return cmp.Or(a.Updated.Compare(b.Updated), cmp.Compare(a.ID, b.ID))
	})
}

// newestBranch is the newest branch layer of ref, if any.
func newestBranch(layers []codegraph.Layer, ref string) (codegraph.Layer, bool) {
	var bs []codegraph.Layer
	for _, l := range layers {
		if l.Kind == codegraph.LayerBranch && l.Ref == ref {
			bs = append(bs, l)
		}
	}
	if len(bs) == 0 {
		return codegraph.Layer{}, false
	}
	return newestLayer(bs), true
}

// refChain is the view of a ref: the base, plus the ref's newest branch
// layer unless the ref is the branch the base follows.
func refChain(byID map[string]codegraph.Layer, layers []codegraph.Layer, ref string) ([]string, string) {
	if base, ok := byID[codegraph.BaseLayerID]; ok && base.Ref == ref {
		return []string{codegraph.BaseLayerID}, ""
	}
	b, ok := newestBranch(layers, ref)
	if !ok {
		return []string{codegraph.BaseLayerID}, "no branch layer for " + ref + ": showing the base"
	}
	ids := []string{b.ID}
	if b.Parent != "" {
		ids = []string{b.Parent, b.ID}
	}
	return ids, ""
}

// overlayChain is the view of an overlay: its layers through Parent, lowest
// first. A dropped branch layer is replaced by the newest one of its ref,
// then by the base; nothing but the overlay itself is ever an overlay.
func overlayChain(byID map[string]codegraph.Layer, layers []codegraph.Layer, overlay codegraph.Layer) ([]string, string) {
	chain := []string{overlay.ID}
	note := ""
	for cur := overlay.Parent; cur != "" && len(chain) < 3; {
		l, ok := byID[cur]
		if !ok {
			p, err := codegraph.ParseLayerID(cur)
			if err == nil && p.Kind == codegraph.LayerBranch {
				if b, ok := newestBranch(layers, p.Ref); ok {
					l = b
					note = "the session's branch layer was replaced: showing the newest one of " + p.Ref
				}
			}
			if l.ID == "" {
				note = "the session's branch layer is gone: showing the base"
				l = codegraph.Layer{ID: codegraph.BaseLayerID, Kind: codegraph.LayerBase}
			}
		}
		if l.Kind == codegraph.LayerWorktree || slices.Contains(chain, l.ID) {
			break
		}
		chain = append(chain, l.ID)
		cur = l.Parent
	}
	slices.Reverse(chain)
	return chain, note
}

// location is path:start-end (path:line for one line).
func location(path string, start, end int) string {
	switch {
	case start <= 0:
		return path
	case end <= start:
		return path + ":" + strconv.Itoa(start)
	}
	return path + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

// docLine is the first line of a doc comment, cut to maxDocLine runes.
func docLine(doc string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(doc), "\n")
	line = strings.TrimSpace(line)
	if r := []rune(line); len(r) > maxDocLine {
		line = string(r[:maxDocLine]) + "…"
	}
	return line
}

func hitOf(s codegraph.Symbol, match string, score float64) Hit {
	return Hit{ID: s.ID, Layer: s.Layer, Kind: s.Kind, Name: s.Name, QualName: s.QualName, Signature: s.Signature,
		Location: location(s.Path, s.Start, s.End), Match: match, Score: score, Doc: docLine(s.Doc)}
}

// checkFilter validates search's filters.
func checkFilter(f codegraph.Filter) error {
	for _, list := range [][]string{f.Kinds, f.Langs, f.Paths} {
		if len(list) > maxFilterItems {
			return fmt.Errorf("%w: at most %d kinds, langs or paths", codegraph.ErrInvalid, maxFilterItems)
		}
		for _, v := range list {
			if v == "" || len(v) > code.MaxPathBytes || !validText(v, code.MaxPathBytes) {
				return fmt.Errorf("%w: filter value %q", codegraph.ErrInvalid, v)
			}
		}
	}
	return nil
}

// fitList cuts items, from the cursor's offset, to budget tokens minus
// what the rest of the answer takes.
func fitList(items []any, offset, budget int, rest any, tool string) (kept []any, truncated bool, next string) {
	offset = min(offset, len(items))
	kept, truncated = query.Fit(items[offset:], max(budget-query.Tokens(rest), query.MinTokens))
	if n := offset + len(kept); truncated && n < len(items) {
		next = query.Cursor{Tool: tool, Offset: n}.Encode()
	}
	return kept, truncated, next
}

func generic[T any](list []T) []any {
	out := make([]any, 0, len(list))
	for _, v := range list {
		out = append(out, query.Generic(v))
	}
	return out
}

// search: exact and qualified name matches first, then full-text matches
// over names, signatures and doc comments.
func (ga *graphAPI) search(w http.ResponseWriter, r *http.Request, req *request) {
	var b SearchBody
	if !ga.readJSON(w, r, &b) {
		return
	}
	b.Query = strings.TrimSpace(b.Query)
	if b.Query == "" || len(b.Query) > maxQueryBytes || !validText(b.Query, maxQueryBytes) {
		invalid(w, "query must be 1 to %d bytes of text", maxQueryBytes)
		return
	}
	filter := codegraph.Filter{Kinds: b.Kinds, Langs: b.Langs, Paths: b.Paths}
	if err := checkFilter(filter); err != nil {
		writeGraphError(w, err, "")
		return
	}
	cur, err := query.DecodeCursor(b.Cursor, cursorSearch)
	if err != nil {
		writeGraphError(w, err, "")
		return
	}
	gr, ok := ga.open(w, r, req, b.View)
	if !ok {
		return
	}
	res := ListResult{Answer: gr.answer, Items: []any{}}
	if gr.ready {
		limit := b.Limit
		if limit <= 0 {
			limit = defaultSearchLimit
		}
		hits, err := searchHits(r.Context(), gr.graph, gr.view, b.Query, filter, codegraph.EffectiveLimit(limit))
		if err != nil {
			writeGraphError(w, err, "")
			return
		}
		res.Items, res.Truncated, res.Next = fitList(generic(hits), cur.Offset, query.Budget(b.MaxTokens, DefaultGraphTokens), res.Answer, cursorSearch)
	}
	httpapi.WriteResult(w, http.StatusOK, res)
}

// searchHits merges FindSymbols (qualified, then exact) and SearchText,
// each symbol once, up to limit.
func searchHits(ctx context.Context, g codegraph.Graph, view codegraph.View, q string, filter codegraph.Filter, limit int) ([]Hit, error) {
	exact, err := g.FindSymbols(ctx, view, codegraph.SymbolQuery{Name: q, Filter: filter, Limit: limit})
	if err != nil {
		return nil, err
	}
	seen := map[codegraph.NodeRef]bool{}
	var hits []Hit
	for _, s := range exact {
		match := MatchExact
		if s.QualName == q {
			match = MatchQualified
		}
		seen[codegraph.NodeRef{Layer: s.Layer, ID: s.ID}] = true
		hits = append(hits, hitOf(s, match, 0))
	}
	// FindSymbols ranks qualified matches first already; keep that order
	// and number the scores so they read best first.
	for i := range hits {
		hits[i].Score = float64(1000 + len(hits) - i)
	}
	if len(hits) < limit {
		text, err := g.SearchText(ctx, view, codegraph.TextQuery{Query: q, Filter: filter, Limit: limit})
		if err != nil && !errors.Is(err, codegraph.ErrInvalid) {
			return nil, err
		}
		for _, t := range text {
			ref := codegraph.NodeRef{Layer: t.Symbol.Layer, ID: t.Symbol.ID}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			hits = append(hits, hitOf(t.Symbol, MatchText, t.Score))
		}
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	if hits == nil {
		hits = []Hit{}
	}
	return hits, nil
}

// symbol: one symbol with its doc, children and body.
func (ga *graphAPI) symbol(w http.ResponseWriter, r *http.Request, req *request) {
	var b SymbolBody
	if !ga.readJSON(w, r, &b) {
		return
	}
	if (b.ID == "") == (b.QualName == "") {
		invalid(w, "symbol needs an id or a qualName")
		return
	}
	for _, v := range []string{b.ID, b.QualName} {
		if len(v) > maxQueryBytes || !validText(v, maxQueryBytes) {
			invalid(w, "id and qualName must be at most %d bytes of text", maxQueryBytes)
			return
		}
	}
	include := map[string]bool{}
	for _, v := range b.Include {
		if v != IncludeBody && v != IncludeDoc && v != IncludeChildren {
			invalid(w, "include %q: one of body, doc and children", v)
			return
		}
		include[v] = true
	}
	if len(include) == 0 {
		include = map[string]bool{IncludeBody: true, IncludeDoc: true, IncludeChildren: true}
	}
	cur, err := query.DecodeCursor(b.Cursor, cursorSymbol)
	if err != nil {
		writeGraphError(w, err, "")
		return
	}
	gr, ok := ga.open(w, r, req, b.View)
	if !ok {
		return
	}
	res := SymbolResult{Answer: gr.answer}
	if !gr.ready {
		httpapi.WriteResult(w, http.StatusOK, res)
		return
	}
	ctx := r.Context()
	si, err := gr.graph.Symbol(ctx, gr.view, codegraph.SymbolKey{ID: b.ID, QualName: b.QualName})
	if err != nil {
		writeGraphError(w, err, "symbol")
		return
	}
	sym := si.Symbol
	if !include[IncludeDoc] {
		sym.Doc = docLine(sym.Doc)
	}
	res.Symbol, res.Lang = &sym, si.Lang
	res.Location = location(sym.Path, sym.Start, sym.End)
	if include[IncludeChildren] {
		for _, c := range si.Children {
			res.Children = append(res.Children, SymbolLine{ID: c.ID, Kind: c.Kind, Name: c.Name, Signature: c.Signature,
				Location: location(c.Path, c.Start, c.End), Exported: c.Exported})
		}
	}
	budget := query.Budget(b.MaxTokens, DefaultGraphTokens)
	if !include[IncludeBody] {
		if query.Tokens(res) > budget {
			res.Children, res.Truncated = fitChildren(res, budget)
		}
		httpapi.WriteResult(w, http.StatusOK, res)
		return
	}
	lines, note, err := symbolLines(ctx, gr, sym)
	if err != nil {
		writeGraphError(w, err, "symbol")
		return
	}
	if lines == nil {
		res.BodyNote = note
		httpapi.WriteResult(w, http.StatusOK, res)
		return
	}
	// The cursor continues the body at a line of the file.
	first := sym.Start
	if b.Cursor != "" {
		if cur.Offset < sym.Start || cur.Offset > sym.End {
			writeGraphError(w, query.ErrBadCursor, "")
			return
		}
		first = cur.Offset
	}
	room := max(budget-query.Tokens(res)-16, query.MinTokens/2)
	text, last, cut := fitLines(lines, sym.Start, first, room)
	res.Body = &Body{Location: location(sym.Path, first, last), Text: text}
	if cut {
		res.Truncated = true
		if last < sym.End {
			res.Next = query.Cursor{Tool: cursorSymbol, Offset: last + 1}.Encode()
		}
	}
	httpapi.WriteResult(w, http.StatusOK, res)
}

// fitChildren drops children from the end until the result fits.
func fitChildren(res SymbolResult, budget int) ([]SymbolLine, bool) {
	kids := res.Children
	for len(kids) > 0 && query.Tokens(res) > budget {
		kids = kids[:len(kids)-1]
		res.Children = kids
	}
	return kids, true
}

// symbolLines returns the source lines of sym (Start..End, 1-based) from
// the blob of the visible file that holds it; nil lines and a note when its
// content was not collected.
func symbolLines(ctx context.Context, gr *graphRequest, sym codegraph.Symbol) ([]string, string, error) {
	outs, err := gr.graph.Outline(ctx, gr.view, codegraph.OutlineQuery{Path: sym.Path})
	if err != nil {
		return nil, "", err
	}
	var file *codegraph.File
	for i := range outs {
		if outs[i].File.Layer == sym.Layer && outs[i].File.Path == sym.Path {
			file = &outs[i].File
		}
	}
	if file == nil || file.SHA256 == "" || !code.ValidSHA256(file.SHA256) {
		return nil, "the file's content was not collected", nil
	}
	blob, err := gr.store.Blob(ctx, file.SHA256)
	if errors.Is(err, code.ErrNotFound) {
		return nil, "the file's content is not stored", nil
	}
	if err != nil {
		return nil, "", err
	}
	all := strings.Split(string(blob.Content), "\n")
	start, end := max(sym.Start, 1), min(sym.End, len(all))
	if end < start {
		end = start
	}
	if start > len(all) {
		return nil, "the symbol's lines are outside the stored file", nil
	}
	return all[start-1 : end], "", nil
}

// fitLines joins lines (the first one being line start of the file) from
// line first, as many as fit room tokens; at least one line, clipped when
// it alone is over. It returns the text, the last line kept and whether
// anything was left out.
func fitLines(lines []string, start, first, room int) (string, int, bool) {
	from := first - start
	var b strings.Builder
	used := 0
	last := first - 1
	for i := from; i < len(lines); i++ {
		cost := (len(lines[i]) + 1 + 3) / 4
		if used+cost > room {
			if i == from {
				keep := max(room*4-40, 32)
				r := []rune(lines[i])
				if keep < len(r) {
					b.WriteString(string(r[:keep]) + fmt.Sprintf("…[%d characters clipped]", len(r)-keep))
				} else {
					b.WriteString(lines[i])
				}
				return b.String(), first, true
			}
			return strings.TrimSuffix(b.String(), "\n"), last, true
		}
		b.WriteString(lines[i])
		b.WriteByte('\n')
		used += cost
		last = start + i
	}
	return strings.TrimSuffix(b.String(), "\n"), last, false
}

// neighbors: the visible edges from a symbol, file or package.
func (ga *graphAPI) neighbors(w http.ResponseWriter, r *http.Request, req *request) {
	var b NeighborsBody
	if !ga.readJSON(w, r, &b) {
		return
	}
	q := codegraph.NeighborQuery{ID: b.ID, Path: b.Path, Package: b.Package, Relation: codegraph.Relation(b.Relation),
		Depth: b.Depth, Limit: b.Limit}
	if err := q.Validate(); err != nil {
		writeGraphError(w, err, "")
		return
	}
	for _, v := range []string{b.ID, b.Package} {
		if len(v) > maxQueryBytes || !validText(v, maxQueryBytes) {
			invalid(w, "id and package must be at most %d bytes of text", maxQueryBytes)
			return
		}
	}
	cur, err := query.DecodeCursor(b.Cursor, cursorNeighbors)
	if err != nil {
		writeGraphError(w, err, "")
		return
	}
	gr, ok := ga.open(w, r, req, b.View)
	if !ok {
		return
	}
	res := ListResult{Answer: gr.answer, Items: []any{}}
	if gr.ready {
		rows, err := gr.graph.Neighbors(r.Context(), gr.view, q)
		if err != nil {
			writeGraphError(w, err, "start node")
			return
		}
		items := make([]NeighborItem, 0, len(rows))
		for _, row := range rows {
			items = append(items, NeighborItem{NeighborRow: row, Location: location(row.Path, row.Line, row.Line)})
		}
		res.Items, res.Truncated, res.Next = fitList(generic(items), cur.Offset, query.Budget(b.MaxTokens, DefaultGraphTokens), res.Answer, cursorNeighbors)
	}
	httpapi.WriteResult(w, http.StatusOK, res)
}

// outline: the symbol trees of a file or a package, with signatures and no
// bodies.
func (ga *graphAPI) outline(w http.ResponseWriter, r *http.Request, req *request) {
	var b OutlineBody
	if !ga.readJSON(w, r, &b) {
		return
	}
	q := codegraph.OutlineQuery{Path: b.Path, Package: b.Package}
	if err := q.Validate(); err != nil {
		writeGraphError(w, err, "")
		return
	}
	if q.Path != "" && !code.ValidPath(q.Path) {
		invalid(w, "invalid path %q", q.Path)
		return
	}
	if len(b.Package) > maxQueryBytes || !validText(b.Package, maxQueryBytes) {
		invalid(w, "package must be at most %d bytes of text", maxQueryBytes)
		return
	}
	cur, err := query.DecodeCursor(b.Cursor, cursorOutline)
	if err != nil {
		writeGraphError(w, err, "")
		return
	}
	gr, ok := ga.open(w, r, req, b.View)
	if !ok {
		return
	}
	res := OutlineResult{Answer: gr.answer, Files: []OutlineFile{}, Items: []any{}}
	if gr.ready {
		outs, err := gr.graph.Outline(r.Context(), gr.view, q)
		if err != nil {
			writeGraphError(w, err, "path or package")
			return
		}
		var lines []SymbolLine
		var walk func(nodes []codegraph.OutlineNode, depth int)
		walk = func(nodes []codegraph.OutlineNode, depth int) {
			for _, n := range nodes {
				s := n.Symbol
				lines = append(lines, SymbolLine{ID: s.ID, Kind: s.Kind, Name: s.Name, Signature: s.Signature,
					Location: location(s.Path, s.Start, s.End), Depth: depth, Exported: s.Exported})
				walk(n.Children, depth+1)
			}
		}
		for _, o := range outs {
			res.Files = append(res.Files, OutlineFile{Path: o.File.Path, Lang: o.File.Lang, Package: o.File.Package, ParseError: o.File.ParseError})
			walk(o.Symbols, 0)
		}
		rest := struct {
			Answer
			Files []OutlineFile
		}{res.Answer, res.Files}
		res.Items, res.Truncated, res.Next = fitList(generic(lines), cur.Offset, query.Budget(b.MaxTokens, DefaultGraphTokens), rest, cursorOutline)
	}
	httpapi.WriteResult(w, http.StatusOK, res)
}

// contextStop are words a task is full of that name no code.
var contextStop = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true, "be": true, "by": true,
	"can": true, "do": true, "does": true, "for": true, "from": true, "how": true, "if": true, "in": true,
	"into": true, "is": true, "it": true, "of": true, "on": true, "or": true, "the": true, "this": true,
	"that": true, "to": true, "what": true, "when": true, "where": true, "which": true, "who": true,
	"why": true, "with": true, "find": true, "code": true, "should": true, "would": true, "will": true,
}

// taskWords are the distinct identifier-like words of a task, longest
// first, at most contextWords of them.
func taskWords(task string) []string {
	words := strings.FieldsFunc(task, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.')
	})
	seen := map[string]bool{}
	var out []string
	for _, w := range words {
		w = strings.Trim(w, ".")
		lw := strings.ToLower(w)
		if len(w) < 3 || contextStop[lw] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	if len(out) > contextWords {
		out = out[:contextWords]
	}
	return out
}

// context: a bundle for a task. The task's words are looked up as names and
// as full-text prefixes; the best hits come with one hop of callers and
// callees and with the bodies that fit the budget.
func (ga *graphAPI) context(w http.ResponseWriter, r *http.Request, req *request) {
	var b ContextBody
	if !ga.readJSON(w, r, &b) {
		return
	}
	b.Task = strings.TrimSpace(b.Task)
	if b.Task == "" || len(b.Task) > maxQueryBytes || !validText(b.Task, maxQueryBytes) {
		invalid(w, "task must be 1 to %d bytes of text", maxQueryBytes)
		return
	}
	cur, err := query.DecodeCursor(b.Cursor, cursorContext)
	if err != nil {
		writeGraphError(w, err, "")
		return
	}
	gr, ok := ga.open(w, r, req, b.View)
	if !ok {
		return
	}
	res := ContextResult{Answer: gr.answer, Hits: []any{}, Neighbors: []any{}, Bodies: []any{}}
	if !gr.ready {
		httpapi.WriteResult(w, http.StatusOK, res)
		return
	}
	ctx := r.Context()
	budget := query.Budget(cmp.Or(b.Budget, b.MaxTokens), DefaultContextTokens)
	hits, syms, err := contextHitsFor(ctx, gr, b.Task)
	if err != nil {
		writeGraphError(w, err, "")
		return
	}
	if cur.Offset > len(hits) {
		writeGraphError(w, query.ErrBadCursor, "")
		return
	}
	if b.Cursor == "" {
		// The first page carries the hits and their neighbours, each in
		// at most a third of the budget; the bodies get the rest.
		var cut bool
		res.Hits, cut = query.Fit(generic(hits), budget/3)
		res.Truncated = res.Truncated || cut
		var rows []NeighborItem
		type edgeKey struct{ from, to codegraph.NodeRef }
		seen := map[edgeKey]bool{}
		for _, h := range hits {
			for _, rel := range []codegraph.Relation{codegraph.RelCallers, codegraph.RelCallees} {
				rs, err := gr.graph.Neighbors(ctx, gr.view, codegraph.NeighborQuery{ID: h.ID, Relation: rel, Depth: 1, Limit: contextNeighbors})
				if errors.Is(err, codegraph.ErrNotFound) {
					continue
				}
				if err != nil {
					writeGraphError(w, err, "")
					return
				}
				for _, row := range rs {
					// Two hits calling each other show one edge once.
					k := edgeKey{codegraph.NodeRef{Layer: row.From.Layer, ID: row.From.ID}, codegraph.NodeRef{Layer: row.To.Layer, ID: row.To.ID}}
					if seen[k] {
						continue
					}
					seen[k] = true
					rows = append(rows, NeighborItem{NeighborRow: row, Location: location(row.Path, row.Line, row.Line)})
				}
			}
		}
		res.Neighbors, cut = query.Fit(generic(rows), budget/3)
		res.Truncated = res.Truncated || cut
	}
	used := query.Tokens(res)
	next := len(hits)
	for i := cur.Offset; i < len(hits); i++ {
		lines, _, err := symbolLines(ctx, gr, syms[i])
		if err != nil && !errors.Is(err, codegraph.ErrNotFound) {
			writeGraphError(w, err, "")
			return
		}
		if lines == nil {
			continue
		}
		item := ContextBodyItem{ID: hits[i].ID, Location: hits[i].Location, Text: strings.Join(lines, "\n")}
		cost := query.Tokens(item) + 1
		if used+cost > budget {
			if len(res.Bodies) == 0 && b.Cursor != "" {
				// A continuation always makes progress: the body clipped.
				text, _, _ := fitLines(lines, syms[i].Start, syms[i].Start, max(budget-used-32, query.MinTokens/2))
				item.Text = text
				res.Bodies = append(res.Bodies, query.Generic(item))
				i++
			}
			next = i
			res.Truncated = true
			break
		}
		res.Bodies = append(res.Bodies, query.Generic(item))
		used += cost
	}
	if next < len(hits) {
		res.Next = query.Cursor{Tool: cursorContext, Offset: next}.Encode()
	}
	httpapi.WriteResult(w, http.StatusOK, res)
}

// contextHitsFor ranks the symbols for a task: a name match of a word
// counts most, then full-text prefix matches of each word, summed per
// symbol; the best contextHits are returned with their symbols.
func contextHitsFor(ctx context.Context, gr *graphRequest, task string) ([]Hit, []codegraph.Symbol, error) {
	type scored struct {
		sym   codegraph.Symbol
		score float64
		match string
	}
	byRef := map[codegraph.NodeRef]*scored{}
	var order []codegraph.NodeRef
	add := func(s codegraph.Symbol, score float64, match string) {
		ref := codegraph.NodeRef{Layer: s.Layer, ID: s.ID}
		sc, ok := byRef[ref]
		if !ok {
			sc = &scored{sym: s, match: match}
			byRef[ref] = sc
			order = append(order, ref)
		}
		sc.score += score
		if match != MatchText {
			sc.match = match
		}
	}
	for _, word := range taskWords(task) {
		exact, err := gr.graph.FindSymbols(ctx, gr.view, codegraph.SymbolQuery{Name: word, Limit: contextHits})
		if err != nil {
			return nil, nil, err
		}
		for _, s := range exact {
			match := MatchExact
			if s.QualName == word {
				match = MatchQualified
			}
			add(s, 100, match)
		}
		text, err := gr.graph.SearchText(ctx, gr.view, codegraph.TextQuery{Query: strings.ToLower(strings.ReplaceAll(word, ".", " ")) + "*", Limit: 2 * contextHits})
		if err != nil && !errors.Is(err, codegraph.ErrInvalid) {
			return nil, nil, err
		}
		for _, t := range text {
			add(t.Symbol, t.Score, MatchText)
		}
	}
	list := make([]*scored, 0, len(order))
	for _, ref := range order {
		list = append(list, byRef[ref])
	}
	slices.SortStableFunc(list, func(a, b *scored) int { return cmp.Compare(b.score, a.score) })
	if len(list) > contextHits {
		list = list[:contextHits]
	}
	hits := make([]Hit, 0, len(list))
	syms := make([]codegraph.Symbol, 0, len(list))
	for _, sc := range list {
		hits = append(hits, hitOf(sc.sym, sc.match, sc.score))
		syms = append(syms, sc.sym)
	}
	return hits, syms, nil
}
