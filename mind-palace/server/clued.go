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

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/mcpserver"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
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
	// Config is the plan and story configuration (kinds, workflows,
	// schemas) every project's palace is built with; config.Default when nil.
	Config *config.Config
	// Enrichers run on the server over Sessions; none disables the loop.
	Enrichers []session.Enricher
	// EnrichInterval is the enrichment period (DefaultEnrichInterval when zero).
	EnrichInterval time.Duration
	// Logger receives one line per request and the enrichment log
	// (slog.Default when nil).
	Logger *slog.Logger
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
// transport under sessionapi.Prefix, the store transport under
// storeapi.Prefix, and, for the project each request names (see
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
	c := &Clued{opts: opts, palaces: NewPalaces(opts.Config, opts.Open)}

	sessionTools := sessionmcp.New(session.Config{}, func(context.Context) (session.Store, error) { return opts.Sessions, nil }, "").Subset(true)
	sessionTools.Account = func(ctx context.Context) string {
		p, _ := auth.FromContext(ctx)
		return p.AccountID
	}
	c.svc = New(nil, ops.All(), Options{
		HTTP: true, WebSocket: true, MCP: true,
		Logger:               opts.Logger,
		Resolve:              c.resolve,
		MCPTools:             []mcpserver.ToolProvider{sessionTools},
		MCPExtraInstructions: sessionTools.Instructions(),
	})

	scoped := withProject(c.svc.Handler)
	protected := http.NewServeMux()
	protected.Handle("GET "+PathWhoami, auth.WhoamiHandler(opts.Version))
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
	protected.Handle(storeapi.Prefix+"/", storeapi.Handler(storeapi.Options{
		Open: func(ctx context.Context, project string) (store.Store, error) {
			p, _ := auth.FromContext(ctx)
			return opts.Open(ctx, opts.Namespace(p, project))
		},
		Allow: func(r *http.Request, project string) bool {
			p, ok := auth.FromContext(r.Context())
			return ok && auth.AllowProject(p, project)
		},
	}))
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
// project named by RequestProject, validated (projects.ValidSlug), allowed
// for the request's principal (auth.AllowProject) and opened once per
// namespaced key through Palaces.
func (c *Clued) resolve(r *http.Request) (*service.Palace, error) {
	project := RequestProject(r)
	if project == "" {
		return nil, ErrMissingProject
	}
	if !projects.ValidSlug(project) {
		return nil, fmt.Errorf("%w: %q", projects.ErrInvalidProject, project)
	}
	p, ok := auth.FromContext(r.Context())
	if !ok || !auth.AllowProject(p, project) {
		return nil, fmt.Errorf("%w: %q", ErrProjectForbidden, project)
	}
	return c.palaces.Get(r.Context(), c.opts.Namespace(p, project))
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
			enrich.Loop(loopCtx, c.opts.Sessions, c.opts.Enrichers, c.opts.EnrichInterval, c.opts.Logger)
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
