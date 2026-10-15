package prime

import (
	"context"
	"net/http"
	"sync"

	"github.com/robbiebyrd/clued/mind-palace/remote"
)

// WhoamiPath is the clued server's connectivity probe: it answers with the
// principal the bearer token resolves to.
const WhoamiPath = "/api/v1/whoami"

// Principal is what the server answers to GET /api/v1/whoami: the token's
// name, the account its writes are stamped with, the projects it may use
// and the server's version.
type Principal struct {
	Name      string   `json:"name"`
	AccountID string   `json:"account_id"`
	Projects  []string `json:"projects"`
	Version   string   `json:"version"`
	// Assignee is the provider:handle the server suggests for claims ("" when it has none).
	Assignee string `json:"assignee,omitempty"`
}

// Whoami asks the server c talks to who its token is. A rejected token is
// remote.ErrUnauthorized; a server that cannot be reached is the transport
// error.
func Whoami(ctx context.Context, c *remote.Client) (Principal, error) {
	var p Principal
	if err := c.Do(ctx, http.MethodGet, WhoamiPath, nil, &p); err != nil {
		return Principal{}, err
	}
	if p.Projects == nil {
		p.Projects = []string{}
	}
	return p, nil
}

// healthyServers remembers the servers (URL and token) that answered the
// whoami probe in this process.
var healthyServers sync.Map

// ServerHealthy is DaemonHealthy for remote mode: it reports whether the
// server c talks to accepts its token, by GET /api/v1/whoami within one
// second. A success is remembered for the rest of the process for that
// server and token, so a long-lived MCP server probes once; a failure is
// probed again on the next call.
func ServerHealthy(ctx context.Context, c *remote.Client) bool {
	if c == nil {
		return false
	}
	key := c.BaseURL() + "\x00" + c.Token()
	if _, ok := healthyServers.Load(key); ok {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, healthTimeout)
	defer cancel()
	if _, err := Whoami(ctx, c); err != nil {
		return false
	}
	healthyServers.Store(key, struct{}{})
	return true
}
