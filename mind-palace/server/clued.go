package server

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/server/storeapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/enrich"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Paths of the clued server's own routes; sessionapi.Prefix and
// storeapi.Prefix hold the transports.
const (
	PathWhoami = "/api/v1/whoami"
	// CluedService is the "service" value of the clued server's /health.
	CluedService = "clued-server"
)

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
	// Open returns a project's document store, served by storeapi. Required.
	Open func(ctx context.Context, project string) (store.Store, error)
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
}

// NewClued composes the clued server onto one mux: GET /health without
// authentication, and behind auth.Middleware the whoami route, the session
// transport under sessionapi.Prefix and the store transport under
// storeapi.Prefix. Every request is logged once with the principal's name
// (or "-"), method, path, status and duration.
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

	protected := http.NewServeMux()
	protected.Handle("GET "+PathWhoami, auth.WhoamiHandler(opts.Version))
	protected.Handle(sessionapi.Prefix+"/", sessionapi.Handler(opts.Sessions, sessionapi.Options{
		Account: func(r *http.Request) (string, bool) {
			p, ok := auth.FromContext(r.Context())
			return p.AccountID, ok
		},
	}))
	protected.Handle(storeapi.Prefix+"/", storeapi.Handler(storeapi.Options{
		Open: opts.Open,
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

	return &Clued{Handler: requestLog(opts.Logger, mux), opts: opts}
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

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }
