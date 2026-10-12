package mcpserver

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// sessionIDHeader is the Streamable HTTP session header.
const sessionIDHeader = "Mcp-Session-Id"

// Multi serves the MCP endpoint over the palace a resolver picks for each
// request. Every palace gets its own Server (tools bound to its services,
// resources from its documents, notifications from its event bus), built
// on first use and kept until Close. A Streamable HTTP session belongs to
// the palace it was initialised on: its id starts with that server's
// prefix, and a request that names a session under another palace is
// answered 404 so the client initialises again.
type Multi struct {
	resolve httpapi.Resolver
	regs    ops.Registries
	opts    Options

	mu      sync.Mutex
	servers map[*service.Palace]*Server
	closed  bool
}

// NewMulti returns a Multi over resolve. opts applies to every per-palace
// server (tool prefix, extra tool providers, instructions, logger).
func NewMulti(resolve httpapi.Resolver, regs ops.Registries, opts *Options) *Multi {
	if opts == nil {
		opts = &Options{}
	}
	return &Multi{resolve: resolve, regs: regs, opts: *opts, servers: map[*service.Palace]*Server{}}
}

type serverKey struct{}

// Handler returns the Streamable HTTP handler. The resolver runs on every
// request, before the MCP transport sees it, so its failures are answered
// in the httpapi envelope.
func (m *Multi) Handler() http.Handler {
	inner := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		if s, ok := r.Context().Value(serverKey{}).(*Server); ok {
			return s.MCP
		}
		return nil
	}, &mcp.StreamableHTTPOptions{Logger: m.opts.Logger})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		palace, err := m.resolve(r)
		if err != nil {
			httpapi.WriteResolveError(w, err)
			return
		}
		s, ok := m.server(palace)
		if !ok {
			httpapi.WriteError(w, http.StatusServiceUnavailable, "store", "the MCP endpoint is shutting down")
			return
		}
		if sid := r.Header.Get(sessionIDHeader); sid != "" && !strings.HasPrefix(sid, s.opts.sessionPrefix) {
			httpapi.WriteError(w, http.StatusNotFound, "not_found", "MCP session "+sid+" belongs to another project; initialize a new session")
			return
		}
		inner.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), serverKey{}, s)))
	})
}

// server returns the palace's server, building it on first use; ok is
// false after Close.
func (m *Multi) server(palace *service.Palace) (*Server, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false
	}
	if s, ok := m.servers[palace]; ok {
		return s, true
	}
	opts := m.opts
	opts.sessionPrefix = rand.Text()[:12] + "."
	s := New(palace, m.regs, &opts)
	m.servers[palace] = s
	return s, true
}

// Len reports how many palaces have a server.
func (m *Multi) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers)
}

// Close stops every per-palace server and refuses further requests.
func (m *Multi) Close() {
	m.mu.Lock()
	servers := m.servers
	m.servers, m.closed = map[*service.Palace]*Server{}, true
	m.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
}
