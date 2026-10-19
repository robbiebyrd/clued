package sessionmcp

// The code tools (plan 0012, "Read API and agent tools"): code_search,
// code_symbol, code_neighbors, code_outline and code_context over the
// clued server's graph read routes (code/remotecode, server/codeapi). They
// pass the current session as the view, so an agent sees its own worktree's
// edits over its branch and the base, and describe every result by
// path:line (path:start-end for a span) so the agent opens exactly the
// lines it needs instead of grepping and reading whole files.
//
// The tools are listed only when the project is indexed and the server
// allows it: AddTools asks a graph route once (search, base view) and lists
// them when it answers ready or indexing; a server without the graph routes
// (404), one that refuses the project or the account (403, 401) never gets
// them, and an empty project or an unreachable graph is asked again every
// CodeRecheck, the tools being added (tools/list_changed) once it is
// indexed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/clientcfg"
	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/code/remotecode"
	"github.com/robbiebyrd/clued/mind-palace/codegraph"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/session/gitinfo"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// CodeGraph is the graph read client the code tools call;
// *remotecode.Store implements it.
type CodeGraph interface {
	Search(ctx context.Context, body codeapi.SearchBody) (remotecode.SearchResult, error)
	Symbol(ctx context.Context, body codeapi.SymbolBody) (codeapi.SymbolResult, error)
	Neighbors(ctx context.Context, body codeapi.NeighborsBody) (remotecode.NeighborsResult, error)
	Outline(ctx context.Context, body codeapi.OutlineBody) (remotecode.OutlineResult, error)
	Context(ctx context.Context, body codeapi.ContextBody) (remotecode.ContextResult, error)
}

var _ CodeGraph = (*remotecode.Store)(nil)

// CodeView is what a code tool call reads: the current session's view
// (its worktree's overlay over its branch and the base) and the branch to
// fall back to when the server does not know the session.
type CodeView struct {
	SessionID string
	Ref       string
}

// Defaults of the code tools' listing.
const (
	// DefaultCodeProbeTimeout bounds the listing probe AddTools waits for.
	DefaultCodeProbeTimeout = 3 * time.Second
	// DefaultCodeRecheck is how often a project that is not indexed yet
	// (or a graph that could not be reached) is asked again.
	DefaultCodeRecheck = 2 * time.Minute
	// maxCodeRechecks bounds the rechecks of one server (a day at the
	// default interval).
	maxCodeRechecks = 720
)

// sessionIDEnv names the current Claude Code session when its environment
// carries it.
const sessionIDEnv = "CLAUDE_SESSION_ID"

// codeConn is the lazily opened default graph client, shared by a Tools and
// its subsets.
type codeConn struct {
	mu    sync.Mutex
	graph CodeGraph
}

// codeEnabled reports whether this set of tools may offer the code tools:
// never on the server's own remote subset (it has no working directory of
// the caller's), else with a CodeGraph given or a clued server configured.
func (t *Tools) codeEnabled() bool {
	return !t.remote && (t.CodeGraph != nil || t.cfg.Remote())
}

// codeGraph returns the graph client of a call: CodeGraph's, else
// code/remotecode over the config's server for the project of the working
// directory (its remote.project, else its git origin), opened once.
func (t *Tools) codeGraph(ctx context.Context) (CodeGraph, error) {
	if t.CodeGraph != nil {
		return t.CodeGraph(ctx)
	}
	c := t.code
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.graph != nil {
		return c.graph, nil
	}
	client, err := remote.New(t.cfg)
	if err != nil {
		return nil, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	root := gitinfo.Root(ctx, wd)
	if root == "" {
		root = wd
	}
	proj := clientcfg.ResolveProject(ctx, root, repoProject(root))
	st, err := remotecode.New(client, proj.Slug)
	if err != nil {
		return nil, err
	}
	c.graph = st
	return st, nil
}

// repoProject is the remote.project of the repository's mind-palace config
// at root ("" without one, or when it cannot be read).
func repoProject(root string) string {
	for _, name := range config.DefaultConfigFiles {
		p := filepath.Join(root, name)
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			cfg, err := config.Load(p)
			if err != nil {
				return ""
			}
			return cfg.Remote.Project
		}
	}
	return ""
}

// codeView returns the view of a call: CodeView's, else the session named
// by CLAUDE_SESSION_ID or, without it, the newest session captured in the
// working directory, and the working directory's branch.
func (t *Tools) codeView(ctx context.Context) CodeView {
	if t.CodeView != nil {
		return t.CodeView(ctx)
	}
	wd, err := os.Getwd()
	if err != nil {
		return CodeView{SessionID: os.Getenv(sessionIDEnv)}
	}
	v := CodeView{SessionID: os.Getenv(sessionIDEnv), Ref: gitinfo.Branch(ctx, wd)}
	if v.SessionID == "" {
		v.SessionID = t.newestSessionIn(ctx, wd)
	}
	return v
}

// newestSessionIn is the newest captured session whose project path or
// working directory is dir ("" when none, or the store is unreachable).
func (t *Tools) newestSessionIn(ctx context.Context, dir string) string {
	store, err := t.getStore(ctx)
	if err != nil {
		return ""
	}
	q := &query.Service{Store: store, AccountID: t.account(ctx)}
	docs, err := q.FindSessions(ctx, query.FindSessionsArgs{Query: "^" + regexp.QuoteMeta(dir) + "$", Limit: 1})
	if err != nil || len(docs) == 0 {
		return ""
	}
	id, _ := docs[0].String("session_id")
	return id
}

// codeProbe is what the listing probe found.
type codeProbe int

const (
	// codeListable: the project is indexed (or being indexed) and allowed.
	codeListable codeProbe = iota
	// codeLater: nothing indexed yet, or the graph could not be reached.
	codeLater
	// codeRefused: the server has no graph, or refuses the project.
	codeRefused
)

// probeCode asks graph/search once (base view, the smallest budget) and
// classifies the answer.
func (t *Tools) probeCode(ctx context.Context) codeProbe {
	g, err := t.codeGraph(ctx)
	if err != nil {
		return codeRefused
	}
	res, err := g.Search(ctx, codeapi.SearchBody{Query: "main", Limit: 1, MaxTokens: query.MinTokens})
	switch {
	case err == nil:
		if res.Status == codeapi.StatusReady || res.Status == codeapi.StatusIndexing {
			return codeListable
		}
		return codeLater
	case errors.Is(err, remotecode.ErrUnsupported), errors.Is(err, remotecode.ErrForbidden),
		errors.Is(err, remote.ErrUnauthorized), errors.Is(err, code.ErrInvalid), errors.Is(err, code.ErrNotFound):
		return codeRefused
	}
	return codeLater
}

// addCodeTools lists the code tools on s when the project is indexed and
// allowed, now or, when it may become so, after a later recheck.
func (t *Tools) addCodeTools(s *mcp.Server, prefix string) {
	if !t.codeEnabled() {
		return
	}
	add := func() {
		for _, spec := range t.codeSpecs() {
			register(s, prefix, spec)
		}
	}
	timeout := t.CodeProbeTimeout
	if timeout <= 0 {
		timeout = DefaultCodeProbeTimeout
	}
	probe := func() codeProbe {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return t.probeCode(ctx)
	}
	switch probe() {
	case codeListable:
		add()
		return
	case codeRefused:
		return
	}
	every := t.CodeRecheck
	if every < 0 {
		return
	}
	if every == 0 {
		every = DefaultCodeRecheck
	}
	go func() {
		for range maxCodeRechecks {
			time.Sleep(every)
			switch probe() {
			case codeListable:
				add()
				return
			case codeRefused:
				return
			}
		}
	}()
}

// codeHead starts every code tool result: the index's status, what the view
// shows, and a note when the answer needs one.
type codeHead struct {
	Status string    `json:"status"`
	Note   string    `json:"note,omitempty"`
	View   *codeSeen `json:"view,omitempty"`
}

// codeSeen is the view a result answers for.
type codeSeen struct {
	Session    string `json:"session,omitempty"`
	Ref        string `json:"ref,omitempty"`
	LocalEdits bool   `json:"localEdits"`
	Note       string `json:"note,omitempty"`
}

func (h *codeHead) head() *codeHead { return h }

// codeResult is a code tool's result.
type codeResult interface{ head() *codeHead }

func headOf(a codeapi.Answer) codeHead {
	h := codeHead{Status: a.Status}
	switch a.Status {
	case codeapi.StatusIndexing:
		pending := "some"
		if a.Progress != nil {
			pending = strconv.Itoa(a.Progress.Pending)
			if a.Progress.More {
				pending = "at least " + pending
			}
		}
		h.Note = "the index is catching up (" + pending + " collected changes are not in it yet): the results may miss recent code"
	case codeapi.StatusEmpty:
		h.Note = "no code has been collected for this project yet: search the files directly"
	}
	if a.View.Session != "" || a.View.Ref != "" || a.View.Note != "" || a.View.LocalEdits {
		h.View = &codeSeen{Session: a.View.Session, Ref: a.View.Ref, LocalEdits: a.View.LocalEdits, Note: a.View.Note}
	}
	return h
}

// codeArgs are the arguments every code tool takes besides its own.
type codeArgs struct {
	Ref string `json:"ref"`
}

// codeTool adapts a graph call to an MCP handler: it decodes the arguments,
// opens the graph, reads the current session's view (the ref's when the
// call names one) and, when the server does not know the session, reads
// the branch instead and says so.
func codeTool[A any](t *Tools, run func(ctx context.Context, g CodeGraph, view *codeapi.ViewBody, args A, b budgetArgs) (codeResult, error)) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args A
		var common codeArgs
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				return failure(err), nil
			}
			_ = json.Unmarshal(req.Params.Arguments, &common)
		}
		b := budgetOf(req)
		g, err := t.codeGraph(ctx)
		if err != nil {
			return failure(codeError(err)), nil
		}
		var view *codeapi.ViewBody
		if common.Ref != "" {
			view = &codeapi.ViewBody{Ref: common.Ref}
		} else if v := t.codeView(ctx); v.SessionID != "" || v.Ref != "" {
			// The session's overlay on whichever ref it works on, else the
			// branch.
			view = &codeapi.ViewBody{SessionID: v.SessionID}
			if v.SessionID == "" {
				view = &codeapi.ViewBody{Ref: v.Ref}
			}
			res, err := run(ctx, g, view, args, b)
			if !errors.Is(err, remotecode.ErrUnknownSession) {
				return codeAnswer(res, err)
			}
			sessionID := view.SessionID
			view = nil
			if v.Ref != "" {
				view = &codeapi.ViewBody{Ref: v.Ref}
			}
			res, err = run(ctx, g, view, args, b)
			if err == nil {
				h := res.head()
				h.Note = joinNotes(h.Note, "the code index does not know session "+sessionID+" (code sync off for this worktree?): the results show "+
					refOrBase(v.Ref)+" without its local edits")
			}
			return codeAnswer(res, err)
		}
		res, err := run(ctx, g, view, args, b)
		return codeAnswer(res, err)
	}
}

func refOrBase(ref string) string {
	if ref == "" {
		return "the default branch"
	}
	return "branch " + ref
}

func joinNotes(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func codeAnswer(res codeResult, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return failure(codeError(err)), nil
	}
	return success(res)
}

// codeError words a graph failure for the agent: what it can do instead.
func codeError(err error) error {
	switch {
	case errors.Is(err, remotecode.ErrUnavailable):
		return fmt.Errorf("the code index is unavailable right now: search and read the files directly (%w)", err)
	case errors.Is(err, remotecode.ErrUnsupported):
		return fmt.Errorf("the clued server has no code index: search and read the files directly (%w)", err)
	case errors.Is(err, remotecode.ErrForbidden):
		return fmt.Errorf("the clued server does not allow the code index for this project (%w)", err)
	}
	return err
}

// codeHit is a search or context hit.
type codeHit struct {
	At        string `json:"at"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	QualName  string `json:"qualName,omitempty"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Match     string `json:"match,omitempty"`
	ID        string `json:"id"`
}

func hitsOf(hs []codeapi.Hit) []codeHit {
	out := make([]codeHit, 0, len(hs))
	for _, h := range hs {
		out = append(out, codeHit{At: h.Location, Kind: h.Kind, Name: h.Name, QualName: h.QualName, Signature: h.Signature,
			Doc: h.Doc, Match: h.Match, ID: h.ID})
	}
	return out
}

// codeLine is a symbol in a list (children, outline).
type codeLine struct {
	At        string `json:"at"`
	Depth     int    `json:"depth,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Signature string `json:"signature,omitempty"`
	Exported  bool   `json:"exported,omitempty"`
	ID        string `json:"id"`
}

func linesOf(ls []codeapi.SymbolLine) []codeLine {
	out := make([]codeLine, 0, len(ls))
	for _, l := range ls {
		out = append(out, codeLine{At: l.Location, Depth: l.Depth, Kind: l.Kind, Name: l.Name, Signature: l.Signature,
			Exported: l.Exported, ID: l.ID})
	}
	return out
}

// codeEdge is one neighbors row: the edge's site (path:line), its ends
// (each with its own path:line) and how sure the resolution is.
type codeEdge struct {
	At         string `json:"at"`
	Relation   string `json:"relation"`
	From       string `json:"from"`
	FromAt     string `json:"fromAt,omitempty"`
	FromID     string `json:"fromId,omitempty"`
	To         string `json:"to"`
	ToAt       string `json:"toAt,omitempty"`
	ToID       string `json:"toId,omitempty"`
	Confidence string `json:"confidence,omitempty"`
	Alias      string `json:"alias,omitempty"`
	Depth      int    `json:"depth,omitempty"`
}

func edgesOf(rows []codeapi.NeighborItem) []codeEdge {
	out := make([]codeEdge, 0, len(rows))
	for _, r := range rows {
		depth := r.Depth
		if depth == 1 {
			depth = 0
		}
		out = append(out, codeEdge{At: r.Location, Relation: string(r.Relation), From: nodeName(r.From), FromAt: nodeAt(r.From),
			FromID: r.From.ID, To: nodeName(r.To), ToAt: nodeAt(r.To), ToID: r.To.ID, Confidence: r.Confidence, Alias: r.Alias, Depth: depth})
	}
	return out
}

func nodeName(n codegraph.NodeSummary) string {
	if n.QualName != "" {
		return n.QualName
	}
	return n.Name
}

func nodeAt(n codegraph.NodeSummary) string {
	switch {
	case n.Path == "":
		return ""
	case n.Start > 0:
		return n.Path + ":" + strconv.Itoa(n.Start)
	}
	return n.Path
}

// codeBody is source text with the lines it spans.
type codeBody struct {
	At   string `json:"at"`
	ID   string `json:"id,omitempty"`
	Text string `json:"text"`
}

type codeSearchArgs struct {
	Query string   `json:"query"`
	Kinds []string `json:"kinds"`
	Langs []string `json:"langs"`
	Paths []string `json:"paths"`
	Limit int      `json:"limit"`
}

type codeSearchOut struct {
	codeHead
	Results   []codeHit `json:"results"`
	Truncated bool      `json:"truncated,omitempty"`
	Next      string    `json:"next,omitempty"`
}

type codeSymbolArgs struct {
	ID       string   `json:"id"`
	QualName string   `json:"qualName"`
	Include  []string `json:"include"`
}

type codeSymbolInfo struct {
	At        string `json:"at"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	QualName  string `json:"qualName,omitempty"`
	Signature string `json:"signature,omitempty"`
	Doc       string `json:"doc,omitempty"`
	Lang      string `json:"lang,omitempty"`
	Exported  bool   `json:"exported,omitempty"`
	Parent    string `json:"parent,omitempty"`
	ID        string `json:"id"`
}

type codeSymbolOut struct {
	codeHead
	Symbol    *codeSymbolInfo `json:"symbol,omitempty"`
	Children  []codeLine      `json:"children,omitempty"`
	Body      *codeBody       `json:"body,omitempty"`
	BodyNote  string          `json:"bodyNote,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
	Next      string          `json:"next,omitempty"`
}

type codeNeighborsArgs struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Package  string `json:"package"`
	Relation string `json:"relation"`
	Depth    int    `json:"depth"`
	Limit    int    `json:"limit"`
}

type codeNeighborsOut struct {
	codeHead
	Edges     []codeEdge `json:"edges"`
	Truncated bool       `json:"truncated,omitempty"`
	Next      string     `json:"next,omitempty"`
}

type codeOutlineArgs struct {
	Path    string `json:"path"`
	Package string `json:"package"`
}

type codeOutlineOut struct {
	codeHead
	Files     []codeapi.OutlineFile `json:"files"`
	Symbols   []codeLine            `json:"symbols"`
	Truncated bool                  `json:"truncated,omitempty"`
	Next      string                `json:"next,omitempty"`
}

type codeContextArgs struct {
	Task string `json:"task"`
}

type codeContextOut struct {
	codeHead
	Hits      []codeHit  `json:"hits"`
	Neighbors []codeEdge `json:"neighbors"`
	Bodies    []codeBody `json:"bodies"`
	Truncated bool       `json:"truncated,omitempty"`
	Next      string     `json:"next,omitempty"`
}

// codeRefSchema is the ref argument every code tool takes.
const codeRefSchema = `"ref":{"type":"string","description":"Read this branch (the base plus its pushed changes) instead of the current session's own view"}`

// codeSpecs are the code tools.
func (t *Tools) codeSpecs() []toolSpec {
	return []toolSpec{
		{
			name:        "code_search",
			description: "Search the project's code index for symbols (functions, types, methods, constants, sections) by name or words: exact and qualified name matches first, then matches in names, signatures and doc comments. Each result gives where it is as path:start-end (open just those lines), its kind, signature, one-line doc and id (for code_symbol and code_neighbors). It reads the current session's view: the worktree's own edits over its branch and the default branch. Use it instead of grep to find where something is defined.",
			schema:      `{"type":"object","properties":{"query":{"type":"string","description":"A name (Alloc, pkg.Alloc, T.M) or words to look for"},"kinds":{"type":"array","items":{"type":"string"},"description":"Only these symbol kinds (function, method, struct, interface, …)"},"langs":{"type":"array","items":{"type":"string"},"description":"Only files of these languages (go, typescript, python, …)"},"paths":{"type":"array","items":{"type":"string"},"description":"Only files under these path prefixes"},"limit":{"type":"number","description":"Max results (default 20)"},` + codeRefSchema + `},"required":["query"]}`,
			readOnly:    true,
			budget:      codeapi.DefaultGraphTokens,
			handler: codeTool(t, func(ctx context.Context, g CodeGraph, view *codeapi.ViewBody, a codeSearchArgs, b budgetArgs) (codeResult, error) {
				res, err := g.Search(ctx, codeapi.SearchBody{View: view, Query: a.Query, Kinds: a.Kinds, Langs: a.Langs, Paths: a.Paths,
					Limit: a.Limit, MaxTokens: b.MaxTokens, Cursor: b.Cursor})
				if err != nil {
					return nil, err
				}
				return &codeSearchOut{codeHead: headOf(res.Answer), Results: hitsOf(res.Items), Truncated: res.Truncated, Next: res.Next}, nil
			}),
		},
		{
			name:        "code_symbol",
			description: "Read one symbol from the code index by id (from code_search) or qualified name: where it is (path:start-end), its signature and doc, its children (methods, fields) each with path:line, and its source lines from the collected (redacted) code. include picks the parts (default all three); a cut body continues with next.",
			schema:      `{"type":"object","properties":{"id":{"type":"string","description":"Symbol id from code_search or code_outline"},"qualName":{"type":"string","description":"Qualified name (pkg.Alloc, pkg.T.M) when no id is given"},"include":{"type":"array","items":{"type":"string","enum":["body","doc","children"]},"description":"Parts to include (default all)"},` + codeRefSchema + `}}`,
			readOnly:    true,
			budget:      codeapi.DefaultGraphTokens,
			handler: codeTool(t, func(ctx context.Context, g CodeGraph, view *codeapi.ViewBody, a codeSymbolArgs, b budgetArgs) (codeResult, error) {
				res, err := g.Symbol(ctx, codeapi.SymbolBody{View: view, ID: a.ID, QualName: a.QualName, Include: a.Include,
					MaxTokens: b.MaxTokens, Cursor: b.Cursor})
				if err != nil {
					return nil, err
				}
				out := &codeSymbolOut{codeHead: headOf(res.Answer), Children: linesOf(res.Children), BodyNote: res.BodyNote,
					Truncated: res.Truncated, Next: res.Next}
				if s := res.Symbol; s != nil {
					out.Symbol = &codeSymbolInfo{At: res.Location, Kind: s.Kind, Name: s.Name, QualName: s.QualName, Signature: s.Signature,
						Doc: s.Doc, Lang: res.Lang, Exported: s.Exported, Parent: s.Parent, ID: s.ID}
				}
				if res.Body != nil {
					out.Body = &codeBody{At: res.Body.Location, Text: res.Body.Text}
				}
				return out, nil
			}),
		},
		{
			name:        "code_neighbors",
			description: "Walk the code index from a symbol, file or package: callers, callees, references and implementations of a symbol (id), the packages a file imports (path), or the files that import a package. Each edge gives the site as path:line, both ends with their own path:line and id, and its confidence (exact, or name when only the name matched). Use it instead of grepping for call sites, e.g. before changing a function.",
			schema:      `{"type":"object","properties":{"id":{"type":"string","description":"Symbol id (callers, callees, references, implementations)"},"path":{"type":"string","description":"File path (imports)"},"package":{"type":"string","description":"Package name (importers)"},"relation":{"type":"string","enum":["callers","callees","references","implementations","imports","importers"]},"depth":{"type":"number","description":"Hops to follow (default 1, at most 3)"},"limit":{"type":"number","description":"Max edges"},` + codeRefSchema + `},"required":["relation"]}`,
			readOnly:    true,
			budget:      codeapi.DefaultGraphTokens,
			handler: codeTool(t, func(ctx context.Context, g CodeGraph, view *codeapi.ViewBody, a codeNeighborsArgs, b budgetArgs) (codeResult, error) {
				res, err := g.Neighbors(ctx, codeapi.NeighborsBody{View: view, ID: a.ID, Path: a.Path, Package: a.Package, Relation: a.Relation,
					Depth: a.Depth, Limit: a.Limit, MaxTokens: b.MaxTokens, Cursor: b.Cursor})
				if err != nil {
					return nil, err
				}
				return &codeNeighborsOut{codeHead: headOf(res.Answer), Edges: edgesOf(res.Items), Truncated: res.Truncated, Next: res.Next}, nil
			}),
		},
		{
			name:        "code_outline",
			description: "Outline a file (path) or a package from the code index: its symbols in order with their kind, signature, nesting depth and path:start-end, without bodies. Cheaper than reading the file to see what it holds.",
			schema:      `{"type":"object","properties":{"path":{"type":"string","description":"File path relative to the repository root"},"package":{"type":"string","description":"Package name, instead of a path"},` + codeRefSchema + `}}`,
			readOnly:    true,
			budget:      codeapi.DefaultGraphTokens,
			handler: codeTool(t, func(ctx context.Context, g CodeGraph, view *codeapi.ViewBody, a codeOutlineArgs, b budgetArgs) (codeResult, error) {
				res, err := g.Outline(ctx, codeapi.OutlineBody{View: view, Path: a.Path, Package: a.Package, MaxTokens: b.MaxTokens, Cursor: b.Cursor})
				if err != nil {
					return nil, err
				}
				files := res.Files
				if files == nil {
					files = []codeapi.OutlineFile{}
				}
				return &codeOutlineOut{codeHead: headOf(res.Answer), Files: files, Symbols: linesOf(res.Items), Truncated: res.Truncated, Next: res.Next}, nil
			}),
		},
		{
			name:        "code_context",
			description: "Gather the code a task needs in one call: the best code_search hits for the task's words, one hop of their callers and callees, and the bodies of the best hits that fit the budget, each with path:start-end. Start a change here, then open only the lines it points to.",
			schema:      `{"type":"object","properties":{"task":{"type":"string","description":"The task in plain words, naming the identifiers you know"},` + codeRefSchema + `},"required":["task"]}`,
			readOnly:    true,
			budget:      codeapi.DefaultContextTokens,
			handler: codeTool(t, func(ctx context.Context, g CodeGraph, view *codeapi.ViewBody, a codeContextArgs, b budgetArgs) (codeResult, error) {
				res, err := g.Context(ctx, codeapi.ContextBody{View: view, Task: a.Task, Budget: b.MaxTokens, Cursor: b.Cursor})
				if err != nil {
					return nil, err
				}
				out := &codeContextOut{codeHead: headOf(res.Answer), Hits: hitsOf(res.Hits), Neighbors: edgesOf(res.Neighbors),
					Bodies: make([]codeBody, 0, len(res.Bodies)), Truncated: res.Truncated, Next: res.Next}
				for _, body := range res.Bodies {
					out.Bodies = append(out.Bodies, codeBody{At: body.Location, ID: body.ID, Text: body.Text})
				}
				return out, nil
			}),
		},
	}
}
