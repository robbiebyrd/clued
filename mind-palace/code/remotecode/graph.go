package remotecode

// The client of the graph read routes (server/codeapi/graph.go): typed
// search, symbol, neighbors, outline and context over the project's code
// graph, under /api/v1/projects/{project}/code/graph/. Each takes the
// route's body as server/codeapi defines it and decodes the answer into a
// typed result.
//
// Errors are those of the write side, plus ErrUnavailable (503: the graph
// backend cannot be reached, so the caller searches the files instead) and
// ErrUnknownSession (404 for the view's session_id: the server holds no
// worktree the session was seen on, so the caller asks for a ref or the
// base). A server without the graph routes (no --graph) answers
// ErrUnsupported; one that refuses the project, ErrForbidden.

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
)

// Typed errors of the graph routes, matched with errors.Is.
var (
	// ErrUnavailable is 503 unavailable: the server's code graph cannot be
	// reached (or failed); the request may be retried later.
	ErrUnavailable = errors.New("remotecode: the code graph is unavailable")
	// ErrUnknownSession is a 404 for the view's session: the session is
	// not on a worktree the caller owns (code sync off, or another
	// account's). It also wraps code.ErrNotFound.
	ErrUnknownSession = errors.New("remotecode: the session is not known to the code graph")
)

// SearchResult is the answer of graph/search.
type SearchResult struct {
	codeapi.Answer
	Items     []codeapi.Hit `json:"items"`
	Truncated bool          `json:"truncated"`
	Next      string        `json:"next,omitempty"`
}

// NeighborsResult is the answer of graph/neighbors.
type NeighborsResult struct {
	codeapi.Answer
	Items     []codeapi.NeighborItem `json:"items"`
	Truncated bool                   `json:"truncated"`
	Next      string                 `json:"next,omitempty"`
}

// OutlineResult is the answer of graph/outline: the files and their symbol
// trees flattened in order.
type OutlineResult struct {
	codeapi.Answer
	Files     []codeapi.OutlineFile `json:"files"`
	Items     []codeapi.SymbolLine  `json:"items"`
	Truncated bool                  `json:"truncated"`
	Next      string                `json:"next,omitempty"`
}

// ContextResult is the answer of graph/context.
type ContextResult struct {
	codeapi.Answer
	Hits      []codeapi.Hit             `json:"hits"`
	Neighbors []codeapi.NeighborItem    `json:"neighbors"`
	Bodies    []codeapi.ContextBodyItem `json:"bodies"`
	Truncated bool                      `json:"truncated"`
	Next      string                    `json:"next,omitempty"`
}

// Search runs POST graph/search: exact and qualified name matches first,
// then full-text matches.
func (s *Store) Search(ctx context.Context, body codeapi.SearchBody) (SearchResult, error) {
	var out SearchResult
	err := s.graph(ctx, "search", body, body.View, &out)
	return out, err
}

// Symbol runs POST graph/symbol: one symbol with its doc, children and
// body.
func (s *Store) Symbol(ctx context.Context, body codeapi.SymbolBody) (codeapi.SymbolResult, error) {
	var out codeapi.SymbolResult
	err := s.graph(ctx, "symbol", body, body.View, &out)
	return out, err
}

// Neighbors runs POST graph/neighbors: the edges around a symbol, file or
// package.
func (s *Store) Neighbors(ctx context.Context, body codeapi.NeighborsBody) (NeighborsResult, error) {
	var out NeighborsResult
	err := s.graph(ctx, "neighbors", body, body.View, &out)
	return out, err
}

// Outline runs POST graph/outline: the symbol trees of a file or a
// package, without bodies.
func (s *Store) Outline(ctx context.Context, body codeapi.OutlineBody) (OutlineResult, error) {
	var out OutlineResult
	err := s.graph(ctx, "outline", body, body.View, &out)
	return out, err
}

// Context runs POST graph/context: a bundle of hits, their callers and
// callees, and the bodies that fit the budget.
func (s *Store) Context(ctx context.Context, body codeapi.ContextBody) (ContextResult, error) {
	var out ContextResult
	err := s.graph(ctx, "context", body, body.View, &out)
	return out, err
}

// graph posts body to graph/{route} and decodes the answer into out,
// adding the graph routes' own errors to the transport's.
func (s *Store) graph(ctx context.Context, route string, body any, view *codeapi.ViewBody, out any) error {
	err := s.doJSON(ctx, http.MethodPost, "graph/"+route, body, out)
	var e *Error
	if err == nil || !errors.As(err, &e) {
		return err
	}
	switch {
	case e.Status() == http.StatusServiceUnavailable && (e.Kind() == codeapi.KindUnavailable || e.Kind() == "http"):
		e.sentinels = append([]error{ErrUnavailable}, e.sentinels...)
	case e.Status() == http.StatusNotFound && e.Kind() == codeapi.KindNotFound && view != nil && view.SessionID != "" &&
		strings.HasSuffix(e.detail.Message, ": session not found"):
		e.sentinels = append([]error{ErrUnknownSession}, e.sentinels...)
	}
	return e
}
