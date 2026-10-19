package server

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/server/device"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/mcpserver"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
	"github.com/robbiebyrd/clued/mind-palace/session/otlp"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Paths of the clued server's own routes; sessionapi.Prefix and
// storeapi.Prefix hold the transports, httpapi.Prefix, PathWS and PathMCP
// the project-scoped service API, WebSocket and MCP endpoint.
const (
	PathWhoami = "/api/v1/whoami"
	// CluedService is the "service" value of the clued server's /health.
	CluedService = "clued-server"
	// ProjectHeader names the project a service API, WebSocket or MCP
	// request works on (`claude mcp add --header "X-Clued-Project: <slug>"`);
	// the ?project= query parameter is the fallback (see RequestProject).
	ProjectHeader = "X-Clued-Project"
	// ProjectQuery is the query parameter RequestProject falls back to.
	ProjectQuery = "project"
)

// RequestProject returns the project slug a request names: the
// ProjectHeader, else the ProjectQuery parameter, trimmed; "" when neither
// is set. The slug is not validated here.
func RequestProject(r *http.Request) string {
	if p := strings.TrimSpace(r.Header.Get(ProjectHeader)); p != "" {
		return p
	}
	return strings.TrimSpace(r.URL.Query().Get(ProjectQuery))
}

// Clued server limits.
const (
	cluedReadHeaderTimeout = 10 * time.Second
	cluedMaxHeaderBytes    = 1 << 20
	cluedShutdownTimeout   = 10 * time.Second
	// DefaultEnrichInterval is the enrichment loop's period when
	// CluedOptions.EnrichInterval is zero (the daemon's).
	DefaultEnrichInterval = 5 * time.Second
)

// CluedOptions composes the clued server (`mind-palace server run`): the
// remote storage of sessions, plans and stories behind bearer tokens.
type CluedOptions struct {
	// Version is reported by /health and /api/v1/whoami.
	Version string
	// Tokens resolves bearer tokens to principals. Required.
	Tokens auth.Source
	// Sessions is the session store served by sessionapi. Required.
	Sessions session.Store
	// Open returns a project's document store, served by storeapi and, as
	// a palace, by the service API, the WebSocket and the MCP endpoint.
	// Required. It receives the key Namespace derives for the request's
	// principal and project (the project itself without a Namespace).
	Open func(ctx context.Context, project string) (store.Store, error)
	// Namespace maps a principal's project to the key its store and palace
	// are opened and cached under; nil keeps the project slug itself. A
	// multi-tenant server returns a key that includes the account, so two
	// accounts' repositories with the same slug never share a store. The
	// key must satisfy projects.ValidSlug when Open is a projects.Opener.
	// Project checks (auth.AllowProject) still see the slug the request
	// named.
	Namespace func(p auth.Principal, project string) string
	// NamespaceCtx, when set, replaces Namespace: it receives the request's
	// context (deadline, cancellation, values the cloud's middleware put
	// there) and may fail. An error fails the request instead of opening a
	// store: a *httpapi.StatusError answers with its own status and kind,
	// an error wrapping ErrProjectForbidden is 403 "forbidden", one
	// wrapping projects.ErrInvalidProject 400 "invalid", anything else 502
	// "store". It runs on every request to the service API and storeapi
	// (and once per WebSocket connection or MCP session), so a routing
	// change (a member removed from a workspace) takes effect on the next
	// request.
	NamespaceCtx func(ctx context.Context, p auth.Principal, project string) (string, error)
	// BeforePut, when set, is storeapi.Options.BeforePut: it sees every
	// document PUT of the store transport (prev nil for a new document)
	// and refuses one by returning an error (403 forbidden, or a
	// *httpapi.StatusError's own status). The context carries the
	// principal (auth.FromContext), the project slug the request named
	// (storeapi.Project) and the X-Clued-Force header (storeapi.Forced).
	// Writes through the service API, the WebSocket and MCP go through the
	// palace's own rules and do not call it.
	BeforePut func(ctx context.Context, prev, next *model.Document) error
	// Assignee, when set, is the provider:handle GET /api/v1/whoami
	// suggests the principal claims documents as (the `assignee` field),
	// so agents' claims match what the server's own pages use; nil or ""
	// leaves the field out.
	Assignee func(ctx context.Context, p auth.Principal) string
	// Config is the plan and story configuration (kinds, workflows,
	// schemas) every project's palace is built with; config.Default when nil.
	Config *config.Config
	// Enrichers run on the server over Sessions; none disables the loop.
	Enrichers []session.Enricher
	// EnrichInterval is the enrichment period (DefaultEnrichInterval when zero).
	EnrichInterval time.Duration
	// EnrichPolicy, when set, decides per document's account whether each
	// of Enrichers runs (enrich.Policy); a multi-tenant server applies each
	// account's own settings with it. Nil runs Enrichers for everyone.
	EnrichPolicy enrich.Policy
	// Logger receives one line per request and the enrichment log
	// (slog.Default when nil).
	Logger *slog.Logger
	// Device, when set, serves the device login clients run `session
	// login` against: PathDeviceCode, PathDeviceToken and PathDevice
	// without authentication, PathDevicePending, PathDeviceApprove,
	// PathDeviceDeny and PathMeCurrentToken behind it. Nil leaves those
	// routes unserved (404), as with a token source that mints nothing.
	Device *device.Flow
	// Exchange, when set, serves PathTokenExchange without authentication:
	// it trades an environment key for a short-lived token (the clued
	// server's token file, or the cloud's accounts). Nil leaves it unserved.
	Exchange auth.Exchanger
	// ClientConfig, when set, serves PathMeConfig: the client settings a
	// linked machine applies with `session config-pull` (config.json keys
	// such as enabledEnrichers) and when they last changed.
	ClientConfig func() (map[string]any, time.Time)
	// Code, when set, serves the write-only code transport (server/codeapi)
	// under storeapi.Prefix/{project}/code/: it returns the code store of
	// the key NamespaceCtx (or Namespace) derives for the request's
	// principal and project, exactly as Open does for plans and stories.
	// Nil leaves the code routes unserved: every one answers 404 not_found,
	// which a client takes as "this server collects no code".
	Code func(ctx context.Context, project string) (code.Store, error)
	// BeforeCodeWrite, when set, is codeapi.Options.BeforeWrite: it runs
	// before every write of the code transport with the namespaced key
	// Code opens and the bytes the write adds (a new blob's size, else 0),
	// and refuses the write by returning an error wrapping
	// codeapi.ErrRefused (403 forbidden: no entitlement), codeapi.ErrQuota
	// (413 quota: the account's code storage is full) or a
	// *httpapi.StatusError (its own status); any other error is 403
	// forbidden. The context carries the principal (auth.FromContext), the
	// project slug the request named (codeapi.Project) and the write
	// (codeapi.Operation).
	BeforeCodeWrite func(ctx context.Context, project string, bytes int64) error
	// CodeMaxFileBytes caps one blob of the code transport
	// (codeapi.DefaultMaxFileBytes when zero); clients read it from the
	// capabilities route.
	CodeMaxFileBytes int64
	// Semantic, when set, offers search_semantic on the MCP endpoint.
	// The enrichment that stores the vectors is one of Enrichers
	// (enrichers.Embedding over the same Embedder).
	Semantic *SemanticOptions
}

// SemanticOptions configure the search_semantic tool of the clued
// server's MCP endpoint.
type SemanticOptions struct {
	// Embedder embeds the queries; it must be the model the embedding
	// enricher stores vectors with. Required.
	Embedder session.Embedder
	// Allow, when set, is asked before each search with the principal's
	// account; its error is the tool's error (no entitlement, a rate
	// limit). Nil allows every account.
	Allow func(ctx context.Context, accountID string) error
}

// Clued is the composed clued server. Handler serves every route; Serve
// adds the listener, the enrichment loop and the graceful shutdown.
type Clued struct {
	Handler http.Handler
	opts    CluedOptions
	palaces *Palaces
	svc     *Server
}

// NewClued composes the clued server onto one mux: GET /health without
// authentication, and behind auth.Middleware the whoami route, the session
// transport under sessionapi.Prefix, Claude Code's OTLP/HTTP logs and
// metrics at otlp.PathLogs and otlp.PathMetrics (stored under the token's
// account for the sessions it holds), the store transport under
// storeapi.Prefix, the write-only code transport under
// storeapi.Prefix/{project}/code/ when Code is set (codeapi), and, for the project each request names (see
// RequestProject), the service API under httpapi.Prefix, the WebSocket at
// PathWS and the MCP endpoint at PathMCP. The MCP endpoint serves the plan
// and story tools plus the session tools that need no local disk
// (sessionmcp.Tools.Subset), scoped to the principal's account. Every
// request is logged once with the principal's name (or "-"), method, path,
// status and duration.
func NewClued(opts CluedOptions) *Clued {
	switch {
	case opts.Tokens == nil:
		panic("server: CluedOptions.Tokens is required")
	case opts.Sessions == nil:
		panic("server: CluedOptions.Sessions is required")
	case opts.Open == nil:
		panic("server: CluedOptions.Open is required")
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.EnrichInterval <= 0 {
		opts.EnrichInterval = DefaultEnrichInterval
	}
	if opts.Config == nil {
		opts.Config = config.Default()
	}
	if opts.Namespace == nil {
		opts.Namespace = func(_ auth.Principal, project string) string { return project }
	}
	if opts.NamespaceCtx == nil {
		namespace := opts.Namespace
		opts.NamespaceCtx = func(_ context.Context, p auth.Principal, project string) (string, error) {
			return namespace(p, project), nil
		}
	}
	c := &Clued{opts: opts, palaces: NewPalaces(opts.Config, opts.Open)}

	sessionTools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) { return opts.Sessions, nil }, "").Subset(true)
	sessionTools.Account = func(ctx context.Context) string {
		p, _ := auth.FromContext(ctx)
		return p.AccountID
	}
	if opts.Semantic != nil && opts.Semantic.Embedder != nil {
		sessionTools.Embedder = opts.Semantic.Embedder
		sessionTools.AllowSemantic = opts.Semantic.Allow
	}
	c.svc = New(nil, ops.All(), Options{
		HTTP: true, WebSocket: true, MCP: true,
		Logger:               opts.Logger,
		Resolve:              c.resolve,
		MCPTools:             []mcpserver.ToolProvider{sessionTools},
		MCPExtraInstructions: sessionTools.Instructions(),
	})

	scoped := withProject(c.svc.Handler)
	routes := &deviceRoutes{flow: opts.Device, clientConfig: opts.ClientConfig}
	protected := http.NewServeMux()
	protected.Handle("GET "+PathWhoami, auth.WhoamiHandlerWith(opts.Version, opts.Assignee))
	routes.protected(protected)
	protected.Handle(httpapi.Prefix+"/", scoped)
	protected.Handle(PathWS, scoped)
	protected.Handle(PathMCP, scoped)
	protected.Handle(PathMCP+"/", scoped)
	protected.Handle(sessionapi.Prefix+"/", sessionapi.Handler(opts.Sessions, sessionapi.Options{
		Account: func(r *http.Request) (string, bool) {
			p, ok := auth.FromContext(r.Context())
			return p.AccountID, ok
		},
	}))
	ingest := otlp.Handler(otlp.Options{
		Store:  opts.Sessions,
		Logger: opts.Logger,
		Account: func(r *http.Request) (string, bool) {
			p, ok := auth.FromContext(r.Context())
			return p.AccountID, ok && p.AccountID != ""
		},
	})
	protected.Handle(otlp.PathLogs, ingest)
	protected.Handle(otlp.PathMetrics, ingest)
	protected.Handle(storeapi.Prefix+"/", storeapi.Handler(storeapi.Options{
		Open: func(ctx context.Context, project string) (store.Store, error) {
			p, _ := auth.FromContext(ctx)
			key, err := opts.NamespaceCtx(ctx, p, project)
			if err != nil {
				return nil, err
			}
			return opts.Open(ctx, key)
		},
		Allow: func(r *http.Request, project string) bool {
			p, ok := auth.FromContext(r.Context())
			return ok && auth.AllowProject(p, project)
		},
		BeforePut: opts.BeforePut,
	}))
	// The code prefix answers 404 in the envelope on its own, so a server
	// without Code, or a route the code transport does not have (it has no
	// read route), never falls through to storeapi.
	protected.HandleFunc(codeapi.Pattern, codeapi.NotFound)
	if opts.Code != nil {
		codeapi.Mount(protected, codeapi.Options{
			Open: opts.Code,
			Namespace: func(ctx context.Context, project string) (string, error) {
				p, ok := auth.FromContext(ctx)
				if !ok {
					return "", fmt.Errorf("%w: %q", ErrProjectForbidden, project)
				}
				return opts.NamespaceCtx(ctx, p, project)
			},
			Allow: func(r *http.Request, project string) bool {
				p, ok := auth.FromContext(r.Context())
				return ok && auth.AllowProject(p, project)
			},
			BeforeWrite:  opts.BeforeCodeWrite,
			MaxFileBytes: opts.CodeMaxFileBytes,
		})
	}
	protected.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, http.StatusNotFound, "not_found", fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path))
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET "+PathHealth, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "service": CluedService, "version": opts.Version, "auth": "bearer",
		})
	})
	routes.public(mux)
	if opts.Exchange != nil {
		mux.Handle("POST "+PathTokenExchange, exchangeHandler(opts.Exchange))
	}
	mux.Handle("/", auth.Middleware(opts.Tokens)(notePrincipal(protected)))

	c.Handler = requestLog(opts.Logger, mux)
	return c
}

// withProject normalises how a request names its project before the
// project-scoped entrypoints see it: a ?project= fallback is promoted to
// the ProjectHeader and removed from the query, so the service API never
// takes it for an operation parameter and the resolver reads one place.
func withProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if fromQuery := q.Get(ProjectQuery); fromQuery != "" || q.Has(ProjectQuery) {
			r = r.Clone(r.Context())
			if r.Header.Get(ProjectHeader) == "" {
				r.Header.Set(ProjectHeader, strings.TrimSpace(fromQuery))
			}
			q.Del(ProjectQuery)
			r.URL.RawQuery = q.Encode()
		}
		next.ServeHTTP(w, r)
	})
}

// resolve is the Options.Resolve of the project-scoped entrypoints: the
// palace (see Palace) of the project named by RequestProject for the
// request's principal.
func (c *Clued) resolve(r *http.Request) (*service.Palace, error) {
	project := RequestProject(r)
	if project == "" {
		return nil, ErrMissingProject
	}
	p, ok := auth.FromContext(r.Context())
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProjectForbidden, project)
	}
	return c.Palace(r.Context(), p, project)
}

// Palace returns the cached palace the service API, the WebSocket and the
// MCP endpoint use for principal p's project: the slug is validated
// (projects.ValidSlug, else projects.ErrInvalidProject), allowed for p
// (auth.AllowProject, else ErrProjectForbidden), mapped to its key by
// NamespaceCtx (or Namespace) with ctx, and opened once per key. The
// pointer is the one the transport serves, so a caller writing through it
// (a web board) shares the claim mutex and the event bus with MCP and
// WebSocket clients of the same key. Errors are those resolve answers
// with (see CluedOptions.NamespaceCtx); after Close it is ErrPalacesClosed.
func (c *Clued) Palace(ctx context.Context, p auth.Principal, project string) (*service.Palace, error) {
	if !projects.ValidSlug(project) {
		return nil, fmt.Errorf("%w: %q", projects.ErrInvalidProject, project)
	}
	if !auth.AllowProject(p, project) {
		return nil, fmt.Errorf("%w: %q", ErrProjectForbidden, project)
	}
	key, err := c.opts.NamespaceCtx(ctx, p, project)
	if err != nil {
		return nil, err
	}
	return c.palaces.Get(ctx, key)
}

// Close stops the project-scoped entrypoints (the per-project MCP servers
// and their event forwarding) and empties the palace cache; later
// resolutions fail with ErrPalacesClosed. The stores stay the caller's.
func (c *Clued) Close() {
	c.svc.Close()
	c.palaces.Close()
}

// ErrPalacesClosed is returned by Palaces.Get after Close.
var ErrPalacesClosed = errors.New("palaces are closed")

// Palaces builds and caches one service.Palace per project over the
// project's store, each with its own event bus, so WebSocket subscriptions,
// SSE streams and MCP resource notifications stay within the project.
type Palaces struct {
	cfg  *config.Config
	open func(ctx context.Context, project string) (store.Store, error)

	mu      sync.Mutex
	palaces map[string]*service.Palace
	closed  bool
}

// NewPalaces returns an empty cache over open, building palaces with cfg
// the way the local CLI does (service.NewPalace over the store and a fresh
// events.Bus).
func NewPalaces(cfg *config.Config, open func(ctx context.Context, project string) (store.Store, error)) *Palaces {
	if cfg == nil {
		cfg = config.Default()
	}
	return &Palaces{cfg: cfg, open: open, palaces: map[string]*service.Palace{}}
}

// Get returns the project's palace, opening its store and building the
// palace on first use. A failure is not cached, so the next call retries.
// The lock is held across open so one project is opened once.
func (p *Palaces) Get(ctx context.Context, project string) (*service.Palace, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, ErrPalacesClosed
	}
	if palace, ok := p.palaces[project]; ok {
		return palace, nil
	}
	st, err := p.open(ctx, project)
	if err != nil {
		return nil, err
	}
	palace, err := service.NewPalace(p.cfg, st, events.New())
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", project, err)
	}
	p.palaces[project] = palace
	return palace, nil
}

// Projects lists the cached project slugs in sorted order.
func (p *Palaces) Projects() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.palaces))
	for slug := range p.palaces {
		out = append(out, slug)
	}
	sort.Strings(out)
	return out
}

// Close empties the cache and refuses further gets. The stores behind the
// palaces are owned by whoever provided open and are not closed here.
func (p *Palaces) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	p.palaces = map[string]*service.Palace{}
}

// Listen says how Serve binds: the address, the certificate pair for TLS
// and whether a reverse proxy terminates TLS in front of the server.
type Listen struct {
	Addr     string
	CertFile string
	KeyFile  string
	// BehindProxy allows plain HTTP on a non-loopback address.
	BehindProxy bool
}

// TLS reports whether Serve terminates TLS itself.
func (l Listen) TLS() bool { return l.CertFile != "" && l.KeyFile != "" }

// Check enforces the plain-HTTP rule: without a certificate pair the
// address must be loopback or BehindProxy must be set. A certificate
// without its key (or the reverse) is an error too.
func (l Listen) Check() error {
	if (l.CertFile == "") != (l.KeyFile == "") {
		return errors.New("--tls-cert and --tls-key must be given together")
	}
	if l.TLS() || l.BehindProxy || IsLoopbackAddr(l.Addr) {
		return nil
	}
	return fmt.Errorf("refusing to serve plain HTTP on %s: pass --tls-cert and --tls-key to terminate TLS, or --behind-proxy when a reverse proxy terminates it", l.Addr)
}

// IsLoopbackAddr reports whether a host:port binds a loopback interface
// only: "localhost", 127.0.0.0/8 or ::1. An empty host binds every
// interface and is not loopback.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Serve listens as l says and serves until ctx is done, running the
// enrichment loop alongside. ready, when non-nil, receives the bound
// address once listening. On ctx cancellation in-flight requests get
// cluedShutdownTimeout to finish; the caller still owns (and closes) the
// stores in opts.
func (c *Clued) Serve(ctx context.Context, l Listen, ready chan<- string) error {
	if err := l.Check(); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", l.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           c.Handler,
		ReadHeaderTimeout: cluedReadHeaderTimeout,
		MaxHeaderBytes:    cluedMaxHeaderBytes,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		ErrorLog:          slog.NewLogLogger(c.opts.Logger.Handler(), slog.LevelWarn),
	}
	if ready != nil {
		ready <- ln.Addr().String()
	}

	loopCtx, stopLoop := context.WithCancel(context.Background())
	var loop sync.WaitGroup
	if len(c.opts.Enrichers) > 0 {
		loop.Add(1)
		go func() {
			defer loop.Done()
			enrich.LoopWithPolicy(loopCtx, c.opts.Sessions, c.opts.Enrichers, c.opts.EnrichPolicy, c.opts.EnrichInterval, c.opts.Logger)
		}()
	}
	stop := func() {
		stopLoop()
		loop.Wait()
		c.Close()
	}

	errc := make(chan error, 1)
	go func() {
		if l.TLS() {
			errc <- srv.ServeTLS(ln, l.CertFile, l.KeyFile)
			return
		}
		errc <- srv.Serve(ln)
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cluedShutdownTimeout)
		defer cancel()
		err := srv.Shutdown(shutdownCtx)
		<-errc
		stop()
		if err != nil {
			c.opts.Logger.Warn("shutdown did not finish cleanly", "error", err)
		}
		return nil
	case err := <-errc:
		stop()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// --- request logging ---

// logEntry is the per-request record the logging middleware fills; the
// principal is noted by notePrincipal, which runs after authentication.
type logEntry struct {
	mu        sync.Mutex
	principal string
}

type logEntryKey struct{}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		entry := &logEntry{principal: "-"}
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), logEntryKey{}, entry)))
		entry.mu.Lock()
		principal := entry.principal
		entry.mu.Unlock()
		logger.Info("request",
			"principal", principal,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}

// notePrincipal records the authenticated principal's name on the log entry.
func notePrincipal(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p, ok := auth.FromContext(r.Context()); ok {
			if entry, ok := r.Context().Value(logEntryKey{}).(*logEntry); ok {
				entry.mu.Lock()
				entry.principal = p.Name
				entry.mu.Unlock()
			}
		}
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status for the log line.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status = code
		s.wrote = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Flush forwards streaming flushes when the underlying writer supports them.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack hands the connection to a WebSocket upgrade (gorilla asserts
// http.Hijacker on the writer it is given); the log line then shows 101.
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("server: the response writer does not support hijacking")
	}
	conn, rw, err := h.Hijack()
	if err == nil && !s.wrote {
		s.status, s.wrote = http.StatusSwitchingProtocols, true
	}
	return conn, rw, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
