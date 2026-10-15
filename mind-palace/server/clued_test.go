package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/service"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// newNamespacedClued composes a clued server whose keys are
// "<account>__<project>" through Namespace or, with nsCtx set, NamespaceCtx.
func newNamespacedClued(t *testing.T, nsCtx func(ctx context.Context, p auth.Principal, project string) (string, error)) (*Clued, *httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var opened []string
	opts := CluedOptions{
		Version: "test",
		Tokens: stubTokens{
			tokenAB:   {Name: "ab", AccountID: "acct-ab", Projects: []string{"a", "b"}},
			tokenStar: {Name: "star", AccountID: "acct-star", Projects: []string{"*"}},
		},
		Sessions: memsession.New(),
		Open: func(_ context.Context, key string) (store.Store, error) {
			mu.Lock()
			opened = append(opened, key)
			mu.Unlock()
			return memstore.New(key), nil
		},
		Namespace:    func(p auth.Principal, project string) string { return p.AccountID + "__" + project },
		NamespaceCtx: nsCtx,
		Config:       config.Default(),
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	c := NewClued(opts)
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	return c, ts, &opened
}

func TestCluedPalaceSharesTransportPalace(t *testing.T) {
	c, ts, opened := newNamespacedClued(t, nil)
	ab := auth.Principal{Name: "ab", AccountID: "acct-ab", Projects: []string{"a", "b"}}

	palace, err := c.Palace(context.Background(), ab, "a")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := c.Palace(context.Background(), ab, "a"); err != nil || again != palace {
		t.Fatalf("second Palace = %p, %v; want the cached %p", again, err, palace)
	}
	if len(*opened) != 1 || (*opened)[0] != "acct-ab__a" {
		t.Fatalf("opened %v, want [acct-ab__a]: Namespace must be applied", *opened)
	}

	// The resolver the service API, WebSocket and MCP use returns the same pointer.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plans", nil)
	req.Header.Set(ProjectHeader, "a")
	req = req.WithContext(auth.WithPrincipal(req.Context(), ab))
	resolved, err := c.resolve(req)
	if err != nil || resolved != palace {
		t.Fatalf("resolve = %p, %v; want the Palace pointer %p", resolved, err, palace)
	}

	// A write over the transport publishes on the returned palace's bus:
	// the same event bus (and claim mutex) the board would write through.
	sub := palace.Bus().Subscribe(events.Filter{}, 8)
	defer sub.Close()
	if status, env := call(t, ts, "POST", "/api/v1/plans", planInput(t), tokenAB, "a"); status != http.StatusCreated {
		t.Fatalf("POST plans over the transport: %d %+v", status, env.Error)
	}
	select {
	case e := <-sub.C:
		if !strings.HasPrefix(e.Type, "plan.") {
			t.Errorf("event %+v, want a plan event", e)
		}
	default:
		t.Fatal("the transport's write published nothing on the palace's bus")
	}
	if got, err := palace.Plans().List(context.Background(), service.ListFilter{}); err != nil || len(got) != 1 {
		t.Fatalf("palace lists %d plans (%v), want the one the transport created", len(got), err)
	}

	// Another account's palace for the same slug is another key.
	star := auth.Principal{Name: "star", AccountID: "acct-star", Projects: []string{"*"}}
	other, err := c.Palace(context.Background(), star, "a")
	if err != nil || other == palace {
		t.Fatalf("other account's palace = %p, %v; want a different palace", other, err)
	}

	// Outside the principal's project list, or an invalid slug: an error.
	if _, err := c.Palace(context.Background(), ab, "c"); !errors.Is(err, ErrProjectForbidden) {
		t.Errorf("Palace outside AllowProject = %v, want ErrProjectForbidden", err)
	}
	if _, err := c.Palace(context.Background(), star, "../x"); !errors.Is(err, projects.ErrInvalidProject) {
		t.Errorf("Palace with an invalid slug = %v, want ErrInvalidProject", err)
	}

	c.Close()
	if _, err := c.Palace(context.Background(), ab, "a"); !errors.Is(err, ErrPalacesClosed) {
		t.Errorf("Palace after Close = %v, want ErrPalacesClosed", err)
	}
}

type ctxKey struct{}

func TestNamespaceCtxReceivesRequestContextAndFailsRequests(t *testing.T) {
	var mu sync.Mutex
	revoked := map[string]bool{}
	var sawRequestCtx bool
	nsCtx := func(ctx context.Context, p auth.Principal, project string) (string, error) {
		if _, ok := auth.FromContext(ctx); ok {
			mu.Lock()
			sawRequestCtx = true
			mu.Unlock()
		}
		if ctx.Value(ctxKey{}) == "direct" {
			return "direct__" + project, nil
		}
		mu.Lock()
		defer mu.Unlock()
		switch {
		case project == "down":
			return "", errors.New("membership store down")
		case revoked[p.AccountID]:
			return "", &httpapi.StatusError{Status: http.StatusForbidden, Kind: "forbidden", Message: "no longer a member"}
		case project == "gone":
			return "", ErrProjectForbidden
		}
		return "ctx__" + p.AccountID + "__" + project, nil
	}
	c, ts, opened := newNamespacedClued(t, nsCtx)

	// NamespaceCtx wins over Namespace and sees the request's context.
	if status, _ := call(t, ts, "GET", "/api/v1/plans", "", tokenStar, "a"); status != 200 {
		t.Fatalf("service API: %d", status)
	}
	if status, _ := call(t, ts, "GET", "/api/v1/projects/b/store/plan", "", tokenStar, ""); status != 200 {
		t.Fatalf("storeapi: %d", status)
	}
	if got := strings.Join(*opened, ","); got != "ctx__acct-star__a,ctx__acct-star__b" {
		t.Fatalf("opened %s", got)
	}
	if !sawRequestCtx {
		t.Error("NamespaceCtx did not receive the request context (no principal on it)")
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, "direct")
	if _, err := c.Palace(ctx, auth.Principal{AccountID: "x", Projects: []string{"*"}}, "a"); err != nil {
		t.Fatal(err)
	}
	if last := (*opened)[len(*opened)-1]; last != "direct__a" {
		t.Errorf("Palace did not pass its ctx to NamespaceCtx: opened %s", last)
	}

	// An error fails the request in both the service resolver and storeapi.
	for _, tc := range []struct {
		project    string
		status     int
		kind       string
		beforeFunc func()
	}{
		{project: "down", status: http.StatusBadGateway, kind: "store"},
		{project: "gone", status: http.StatusForbidden, kind: "forbidden"},
		{project: "a", status: http.StatusForbidden, kind: "forbidden", beforeFunc: func() {
			mu.Lock()
			revoked["acct-star"] = true
			mu.Unlock()
		}},
	} {
		if tc.beforeFunc != nil {
			tc.beforeFunc()
		}
		before := len(*opened)
		status, env := call(t, ts, "GET", "/api/v1/plans", "", tokenStar, tc.project)
		wantEnvelopeError(t, "service API "+tc.project, status, env, tc.status, tc.kind)
		status, env = call(t, ts, "PUT", "/api/v1/projects/"+tc.project+"/store/plan/0001-abc", `{"kind":"plan","front_matter":{"id":"0001-abc"}}`, tokenStar, "")
		wantEnvelopeError(t, "storeapi "+tc.project, status, env, tc.status, tc.kind)
		if len(*opened) != before {
			t.Errorf("%s: a failed NamespaceCtx opened a store: %v", tc.project, (*opened)[before:])
		}
	}
}

func TestWhoamiSuggestsTheAssignee(t *testing.T) {
	c := NewClued(CluedOptions{
		Tokens:   stubTokens{tokenAB: {Name: "ab", AccountID: "acct-ab", Projects: []string{"a"}}},
		Sessions: memsession.New(),
		Open:     func(_ context.Context, key string) (store.Store, error) { return memstore.New(key), nil },
		Assignee: func(ctx context.Context, p auth.Principal) string {
			if _, ok := auth.FromContext(ctx); !ok {
				return "no-principal-on-ctx"
			}
			return "clued:" + p.AccountID
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	status, env := call(t, ts, "GET", PathWhoami, "", tokenAB, "")
	if status != 200 || !strings.Contains(string(env.Result), `"assignee": "clued:acct-ab"`) {
		t.Fatalf("whoami: %d %s", status, env.Result)
	}
}
