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

	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/server/httpapi"
	"github.com/robbiebyrd/clued/plan/server/mcpserver"
	"github.com/robbiebyrd/clued/plan/server/ws"
	"github.com/robbiebyrd/clued/plan/service"
)

// Options selects which entrypoints to mount.
type Options struct {
	HTTP          bool
	WebSocket     bool
	MCP           bool
	MCPToolPrefix string
	Logger        *slog.Logger
	// AllowAnyOrigin disables the same-host origin check on the WebSocket.
	AllowAnyOrigin bool
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
	opts    Options
}

// New builds the handler.
func New(svc *service.Service, reg *ops.Registry, opts Options) *Server {
	mux := http.NewServeMux()
	s := &Server{opts: opts}
	mux.HandleFunc("GET "+PathHealth, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"ok": true, "service": "plan", "stores": svc.StoreNames(),
			"entrypoints": map[string]bool{"http": opts.HTTP, "ws": opts.WebSocket, "mcp": opts.MCP},
		})
	})
	if opts.HTTP {
		mux.Handle(httpapi.Prefix+"/", httpapi.Handler(svc, reg))
	}
	if opts.WebSocket {
		wsOpts := &ws.Options{}
		if opts.AllowAnyOrigin {
			wsOpts.CheckOrigin = func(*http.Request) bool { return true }
		}
		mux.Handle(PathWS, ws.Handler(svc, reg, wsOpts))
	}
	if opts.MCP {
		s.mcp = mcpserver.New(svc, reg, &mcpserver.Options{ToolPrefix: opts.MCPToolPrefix, Logger: opts.Logger})
		mux.Handle(PathMCP, s.mcp.Handler())
		mux.Handle(PathMCP+"/", s.mcp.Handler())
	}
	s.Handler = mux
	return s
}

// MCP returns the MCP server when mounted.
func (s *Server) MCP() *mcpserver.Server { return s.mcp }

// Close releases resources.
func (s *Server) Close() {
	if s.mcp != nil {
		s.mcp.Close()
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
