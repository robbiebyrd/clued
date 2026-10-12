package sessionmcp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/sessionapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/remotesession"
	"github.com/robbiebyrd/clued/mind-palace/session/sessionmcp"
)

const remoteToken = "mcp-remote-token"

type remoteTokenSource struct{}

func (remoteTokenSource) Lookup(raw string) (auth.Principal, bool) {
	if raw != remoteToken {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: "laptop", AccountID: account}, true
}

// remoteTools builds the session tools in remote mode: a clued server over
// backing (session API and whoami behind the bearer token) and a config
// naming it, with the WAL under a temp directory.
func remoteTools(t *testing.T, backing *memsession.Store) (*sessionmcp.Tools, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/api/v1/whoami", auth.WhoamiHandler("test"))
	mux.Handle(sessionapi.Prefix+"/", sessionapi.Handler(backing, sessionapi.Options{Account: func(r *http.Request) (string, bool) {
		p, ok := auth.FromContext(r.Context())
		return p.AccountID, ok
	}}))
	srv := httptest.NewServer(auth.Middleware(remoteTokenSource{})(mux))
	t.Cleanup(srv.Close)
	cfg := session.Config{ServerURL: srv.URL, ServerToken: remoteToken, WalPath: filepath.Join(t.TempDir(), "events.wal")}
	connect := func(context.Context) (session.Store, error) {
		c, err := remote.New(cfg)
		if err != nil {
			return nil, err
		}
		return remotesession.New(c), nil
	}
	return sessionmcp.New(cfg, connect, account), srv
}

func TestPrimeContextInsertsASnapshotThroughTheRemoteStore(t *testing.T) {
	backing := memsession.New()
	root := t.TempDir()
	seedSession(t, backing, account, "sess-remote", filepath.ToSlash(root), "")
	tools, srv := remoteTools(t, backing)
	tools.ConfigPath = "/tmp/clued.json"
	cs := connectClient(t, tools.Server(), nil)

	res := call(t, cs, "prime_context", obj{"cwd": root})
	if res.IsError {
		t.Fatalf("tool error: %s", text(t, res))
	}
	if report := text(t, res); !strings.Contains(report, "=== CLUED ===\nconfig: /tmp/clued.json\nserver: healthy ("+srv.URL+")") {
		t.Errorf("CLUED section must report the server:\n%s", report)
	}

	full, err := backing.FullSession(context.Background(), account, "sess-remote")
	if err != nil {
		t.Fatal(err)
	}
	events := full["hook_events"].([]session.Doc)
	if len(events) != 1 || events[0]["hook_event_name"] != "ContextCollected" || events[0]["cwd"] != filepath.ToSlash(root) {
		t.Fatalf("hook_events = %v, want one ContextCollected event", events)
	}
	if report, _ := events[0]["report"].(string); !strings.Contains(report, "=== ENV ===") {
		t.Errorf("event report = %q", events[0]["report"])
	}

	call(t, cs, "prime_context", obj{"cwd": root, "no_snapshot": true})
	full, _ = backing.FullSession(context.Background(), account, "sess-remote")
	if n := len(full["hook_events"].([]session.Doc)); n != 1 {
		t.Errorf("no_snapshot must not insert, got %d events", n)
	}
}
