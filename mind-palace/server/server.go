// Package server composes the network entrypoints (HTTP/REST, WebSocket and
// MCP Streamable HTTP) onto one http.Handler.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/mcpserver"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/server/ws"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// Options selects which entrypoints to mount.
type Options struct {
	HTTP          bool
	WebSocket     bool
	MCP           bool
	MCPToolPrefix string
	// MCPTools are extra tool providers on the MCP endpoint, described by MCPExtraInstructions.
	MCPTools             []mcpserver.ToolProvider
	MCPExtraInstructions string
	Logger               *slog.Logger
	// AllowAnyOrigin disables the same-host origin check on the WebSocket.
	AllowAnyOrigin bool
	// Resolve, when set, picks the palace of each request (each WebSocket
	// connection, each MCP session) instead of the fixed palace given to
	// New, which may then be nil. Its errors answer in the httpapi
	// envelope: ErrMissingProject and projects.ErrInvalidProject as 400
	// "invalid", ErrProjectForbidden as 403 "forbidden" and anything else
	// as 502 "store" (see mapResolveError).
	Resolve func(r *http.Request) (*service.Palace, error)
}

// Resolver failures with a status of their own; the clued server's
// resolver returns them (wrapped) and mapResolveError turns them into the
// envelope.
var (
	// ErrMissingProject: the request names no project.
	ErrMissingProject = errors.New("missing project: set the " + ProjectHeader + " header or ?project=")
	// ErrProjectForbidden: the principal's token does not list the project.
	ErrProjectForbidden = errors.New("project not allowed for this token")
)

// mapResolveError turns a Resolve failure into the envelope's status and
// kind, as the clued server's other transports answer them.
func mapResolveError(err error) *httpapi.StatusError {
	switch {
	case errors.Is(err, ErrMissingProject), errors.Is(err, projects.ErrInvalidProject):
		return &httpapi.StatusError{Status: http.StatusBadRequest, Kind: "invalid", Message: err.Error()}
	case errors.Is(err, ErrProjectForbidden):
		return &httpapi.StatusError{Status: http.StatusForbidden, Kind: "forbidden", Message: err.Error()}
	}
	return &httpapi.StatusError{Status: http.StatusBadGateway, Kind: "store", Message: err.Error()}
}

// Paths where the entrypoints are mounted.
const (
	PathHealth = "/health"
	PathWS     = "/ws"
	PathMCP    = "/mcp"
)

// Server is the composed HTTP server.
type Server struct {
	Handler http.Handler
	mcp     *mcpserver.Server
	multi   *mcpserver.Multi
	opts    Options
}

// New builds the handler. palace is the fixed palace every entrypoint
// serves; with opts.Resolve set it is only reported by /health and may be
// nil.
func New(palace *service.Palace, regs ops.Registries, opts Options) *Server {
	mux := http.NewServeMux()
	s := &Server{opts: opts}
	resolve := httpapi.Fixed(palace)
	if opts.Resolve != nil {
		resolve = func(r *http.Request) (*service.Palace, error) {
			p, err := opts.Resolve(r)
			if err != nil {
				return nil, mapResolveError(err)
			}
			return p, nil
		}
	}
	mux.HandleFunc("GET "+PathHealth, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := map[string]any{
			"ok": true, "service": "mind-palace", "kinds": kind.Names(),
			"entrypoints": map[string]bool{"http": opts.HTTP, "ws": opts.WebSocket, "mcp": opts.MCP},
		}
		if palace != nil {
			body["stores"] = palace.StoreNames()
		}
		json.NewEncoder(w).Encode(body)
	})
	if opts.HTTP {
		mux.Handle(httpapi.Prefix+"/", httpapi.ResolvingHandler(resolve, regs))
	}
	if opts.WebSocket {
		wsOpts := &ws.Options{}
		if opts.AllowAnyOrigin {
			wsOpts.CheckOrigin = func(*http.Request) bool { return true }
		}
		mux.Handle(PathWS, ws.ResolvingHandler(resolve, regs, wsOpts))
	}
	if opts.MCP {
		mcpOpts := &mcpserver.Options{ToolPrefix: opts.MCPToolPrefix, Logger: opts.Logger, Tools: opts.MCPTools, ExtraInstructions: opts.MCPExtraInstructions}
		var h http.Handler
		if opts.Resolve != nil {
			s.multi = mcpserver.NewMulti(resolve, regs, mcpOpts)
			h = s.multi.Handler()
		} else {
			s.mcp = mcpserver.New(palace, regs, mcpOpts)
			h = s.mcp.Handler()
		}
		mux.Handle(PathMCP, h)
		mux.Handle(PathMCP+"/", h)
	}
	s.Handler = mux
	return s
}

// MCP returns the MCP server when mounted over a fixed palace (nil with
// Resolve, where each palace has its own).
func (s *Server) MCP() *mcpserver.Server { return s.mcp }

// Close releases resources.
func (s *Server) Close() {
	if s.mcp != nil {
		s.mcp.Close()
	}
	if s.multi != nil {
		s.multi.Close()
	}
}

// ListenAndServe serves until ctx is cancelled. ready, when non-nil, receives
// the bound address once listening.
func (s *Server) ListenAndServe(ctx context.Context, addr string, ready chan<- string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	if ready != nil {
		ready <- ln.Addr().String()
	}
	srv := &http.Server{Handler: s.Handler, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		s.Close()
		return nil
	case err := <-errc:
		s.Close()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
