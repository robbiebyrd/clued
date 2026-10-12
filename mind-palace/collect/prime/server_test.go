package prime

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

type stubSource struct{ token string }

func (s stubSource) Lookup(raw string) (auth.Principal, bool) {
	if raw != s.token {
		return auth.Principal{}, false
	}
	return auth.Principal{Name: "laptop", AccountID: "acct-9", Projects: []string{"github.com/org/repo"}}, true
}

// whoamiServer serves GET /api/v1/whoami behind a bearer token and counts
// the requests it answers.
func whoamiServer(t *testing.T, token string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	mux := http.NewServeMux()
	mux.Handle("/api/v1/whoami", auth.WhoamiHandler("9.9.9"))
	h := auth.Middleware(stubSource{token: token})(mux)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func client(t *testing.T, url, token string) *remote.Client {
	t.Helper()
	c, err := remote.New(session.Config{ServerURL: url, ServerToken: token})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWhoamiReturnsThePrincipal(t *testing.T) {
	srv, _ := whoamiServer(t, "good")
	p, err := Whoami(context.Background(), client(t, srv.URL, "good"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "laptop" || p.AccountID != "acct-9" || p.Version != "9.9.9" || strings.Join(p.Projects, ",") != "github.com/org/repo" {
		t.Errorf("principal = %+v", p)
	}

	_, err = Whoami(context.Background(), client(t, srv.URL, "bad"))
	if !errors.Is(err, remote.ErrUnauthorized) {
		t.Errorf("rejected token: err = %v, want ErrUnauthorized", err)
	}
}

func TestServerHealthyProbesWhoamiAndCachesSuccess(t *testing.T) {
	srv, requests := whoamiServer(t, "good")
	ctx := context.Background()

	good := client(t, srv.URL, "good")
	if !ServerHealthy(ctx, good) {
		t.Fatal("a server answering whoami is not healthy")
	}
	if ServerHealthy(ctx, client(t, srv.URL, "bad")) {
		t.Error("a rejected token counts as healthy")
	}
	seen := requests.Load()
	if !ServerHealthy(ctx, good) || requests.Load() != seen {
		t.Errorf("a successful probe is not cached: %d requests before, %d after", seen, requests.Load())
	}

	// A fresh client for the same server and token shares the cache; a
	// server that has gone away is still reported from it.
	srv.Close()
	if !ServerHealthy(ctx, client(t, srv.URL, "good")) {
		t.Error("the cached result is not shared across clients of one server")
	}
	if ServerHealthy(ctx, client(t, srv.URL, "other")) {
		t.Error("an unreachable server with an unprobed token counts as healthy")
	}
}

func TestCluedSectionReportsTheServerInRemoteMode(t *testing.T) {
	srv, _ := whoamiServer(t, "good")
	root := repo(t)
	c := collector(t, root)
	c.Server = client(t, srv.URL, "good")

	body := sectionMap(t, c.Collect(context.Background(), Options{}))["CLUED"]
	if !strings.Contains(body, "server: healthy ("+srv.URL+")") || strings.Contains(body, "daemon:") {
		t.Errorf("remote mode must report the server, not the daemon:\n%s", body)
	}

	c.Server = client(t, srv.URL, "bad")
	body = sectionMap(t, c.Collect(context.Background(), Options{}))["CLUED"]
	if !strings.Contains(body, "server: unreachable ("+srv.URL+")") {
		t.Errorf("a rejected token must be reported:\n%s", body)
	}
}
