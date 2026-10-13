package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/config"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/device"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// testClock is a settable clock for the device flow; the test advances it
// between requests while the server's handlers read it, so it is locked.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// newDeviceServer is a clued server over a real token file holding one
// token, "star", with the device login and client settings served.
func newDeviceServer(t *testing.T, clock *testClock) (*httptest.Server, string) {
	t.Helper()
	tf := auth.OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	star, err := tf.Create("star", "", []string{"*"})
	if err != nil {
		t.Fatal(err)
	}
	c := NewClued(CluedOptions{
		Version:  "test",
		Tokens:   tf,
		Sessions: memsession.New(),
		Open:     func(_ context.Context, project string) (store.Store, error) { return memstore.New(project), nil },
		Config:   config.Default(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Device:   device.New(device.Options{Tokens: tf, Now: clock.now}),
		ClientConfig: func() (map[string]any, time.Time) {
			return map[string]any{"disabledEnrichers": []string{"privacy-redact"}}, time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
		},
	})
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	return ts, star
}

func decodeResult(t *testing.T, env envelope, out any) {
	t.Helper()
	if !env.OK {
		t.Fatalf("not ok: %+v", env.Error)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		t.Fatalf("decode result: %v\n%s", err, env.Result)
	}
}

func TestCluedDeviceLoginAndMeRoutes(t *testing.T) {
	clock := &testClock{t: time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)}
	ts, star := newDeviceServer(t, clock)

	var me struct {
		AccountID string `json:"account_id"`
	}
	status, env := call(t, ts, "GET", PathWhoami, "", star, "")
	if status != 200 {
		t.Fatalf("whoami: %d %+v", status, env.Error)
	}
	decodeResult(t, env, &me)

	// A client without a token starts a request; no authentication needed.
	var start struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		Complete   string `json:"verification_uri_complete"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
	}
	status, env = call(t, ts, "POST", PathDeviceCode, `{"client_name": "laptop", "client_info": {"os": "darwin"}}`, "", "")
	if status != 200 {
		t.Fatalf("device/code: %d %+v", status, env.Error)
	}
	decodeResult(t, env, &start)
	if start.URI != ts.URL+PathDevice || !strings.HasSuffix(start.Complete, "?user_code="+start.UserCode) || start.ExpiresIn != 900 || start.Interval != 5 || start.DeviceCode == "" {
		t.Errorf("start = %+v", start)
	}

	// The verification page explains how to approve.
	resp, err := http.Get(start.Complete)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") || !strings.Contains(string(page), "server device approve "+start.UserCode) {
		t.Errorf("verification page: %d %q\n%s", resp.StatusCode, resp.Header.Get("Content-Type"), page)
	}

	// Polling before the approval is pending; an unknown code is invalid_grant.
	status, env = call(t, ts, "POST", PathDeviceToken, `{"device_code": "`+start.DeviceCode+`"}`, "", "")
	wantEnvelopeError(t, "poll before approval", status, env, 400, "authorization_pending")
	status, env = call(t, ts, "POST", PathDeviceToken, `{"device_code": "nope"}`, "", "")
	wantEnvelopeError(t, "poll unknown", status, env, 400, "invalid_grant")

	// Listing and approving need a token.
	status, env = call(t, ts, "GET", PathDevicePending, "", "", "")
	wantEnvelopeError(t, "pending without a token", status, env, 401, "unauthorized")
	var pending []device.Request
	status, env = call(t, ts, "GET", PathDevicePending, "", star, "")
	if status != 200 {
		t.Fatalf("pending: %d %+v", status, env.Error)
	}
	decodeResult(t, env, &pending)
	if len(pending) != 1 || pending[0].UserCode != start.UserCode || pending[0].ClientName != "laptop" || pending[0].ClientInfo["os"] != "darwin" {
		t.Errorf("pending = %+v", pending)
	}
	status, env = call(t, ts, "POST", PathDeviceApprove, `{"user_code": "BCDF-GHJK"}`, star, "")
	wantEnvelopeError(t, "approve unknown", status, env, 404, "not_found")
	status, env = call(t, ts, "POST", PathDeviceApprove, `{"user_code": "`+strings.ToLower(start.UserCode)+`", "projects": ["a"]}`, star, "")
	if status != 200 {
		t.Fatalf("approve: %d %+v", status, env.Error)
	}
	var approval device.Approval
	decodeResult(t, env, &approval)
	if approval.Name != "laptop" || approval.AccountID != me.AccountID || len(approval.Projects) != 1 || approval.Projects[0] != "a" || approval.UserCode != start.UserCode {
		t.Errorf("approval = %+v (star's account %s)", approval, me.AccountID)
	}
	status, env = call(t, ts, "POST", PathDeviceApprove, `{"user_code": "`+start.UserCode+`"}`, star, "")
	wantEnvelopeError(t, "approve twice", status, env, 409, "conflict")

	// The next poll hands the token over, once.
	clock.advance(5 * time.Second)
	var link device.Link
	status, env = call(t, ts, "POST", PathDeviceToken, `{"device_code": "`+start.DeviceCode+`"}`, "", "")
	if status != 200 {
		t.Fatalf("poll after approval: %d %+v", status, env.Error)
	}
	decodeResult(t, env, &link)
	if link.Token == "" || link.Name != "laptop" || link.TokenID != "laptop" || link.AccountID != me.AccountID {
		t.Errorf("link = %+v", link)
	}
	clock.advance(5 * time.Second)
	status, env = call(t, ts, "POST", PathDeviceToken, `{"device_code": "`+start.DeviceCode+`"}`, "", "")
	wantEnvelopeError(t, "poll after the hand-over", status, env, 400, "invalid_grant")

	// The minted token authenticates with the approver's account and the
	// granted projects, reads the client settings and revokes itself.
	var who struct {
		Name      string   `json:"name"`
		AccountID string   `json:"account_id"`
		Projects  []string `json:"projects"`
	}
	status, env = call(t, ts, "GET", PathWhoami, "", link.Token, "")
	if status != 200 {
		t.Fatalf("whoami with the new token: %d %+v", status, env.Error)
	}
	decodeResult(t, env, &who)
	if who.Name != "laptop" || who.AccountID != me.AccountID || len(who.Projects) != 1 || who.Projects[0] != "a" {
		t.Errorf("whoami = %+v", who)
	}
	var cfg struct {
		ClientConfig map[string]any `json:"client_config"`
		UpdatedAt    string         `json:"updated_at"`
	}
	status, env = call(t, ts, "GET", PathMeConfig, "", link.Token, "")
	if status != 200 {
		t.Fatalf("me/config: %d %+v", status, env.Error)
	}
	decodeResult(t, env, &cfg)
	if d, _ := cfg.ClientConfig["disabledEnrichers"].([]any); len(d) != 1 || d[0] != "privacy-redact" || cfg.UpdatedAt != "2026-10-13T12:00:00Z" {
		t.Errorf("me/config = %+v", cfg)
	}
	status, env = call(t, ts, "DELETE", PathMeCurrentToken, "", link.Token, "")
	if status != 200 || !strings.Contains(string(env.Result), `"revoked": "laptop"`) {
		t.Errorf("logout: %d %s %+v", status, env.Result, env.Error)
	}
	status, env = call(t, ts, "GET", PathWhoami, "", link.Token, "")
	wantEnvelopeError(t, "whoami after logout", status, env, 401, "unauthorized")

	// A denied request tells its client so.
	status, env = call(t, ts, "POST", PathDeviceCode, `{"client_name": "desk"}`, "", "")
	if status != 200 {
		t.Fatalf("second device/code: %d", status)
	}
	decodeResult(t, env, &start)
	status, env = call(t, ts, "POST", PathDeviceDeny, `{"user_code": "`+start.UserCode+`"}`, star, "")
	if status != 200 || !strings.Contains(string(env.Result), `"denied": true`) {
		t.Errorf("deny: %d %s %+v", status, env.Result, env.Error)
	}
	status, env = call(t, ts, "POST", PathDeviceToken, `{"device_code": "`+start.DeviceCode+`"}`, "", "")
	wantEnvelopeError(t, "poll after deny", status, env, 400, "access_denied")

	// Behind a reverse proxy the verification URI is the public one.
	req, _ := http.NewRequest("POST", ts.URL+PathDeviceCode, strings.NewReader(`{"client_name": "x"}`))
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "clued.example.com:8443")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var proxied envelope
	_ = json.NewDecoder(resp.Body).Decode(&proxied)
	resp.Body.Close()
	decodeResult(t, proxied, &start)
	if start.URI != "https://clued.example.com:8443"+PathDevice {
		t.Errorf("proxied verification_uri = %q", start.URI)
	}
}

func TestCluedWithoutDeviceLoginServesNoSuchRoutes(t *testing.T) {
	ts, _, _, _ := newCluedServer(t)
	for _, c := range []struct{ method, path string }{
		{"POST", PathDeviceCode}, {"POST", PathDeviceToken}, {"GET", PathDevice},
		{"GET", PathDevicePending}, {"POST", PathDeviceApprove}, {"GET", PathMeConfig}, {"DELETE", PathMeCurrentToken},
	} {
		status, env := call(t, ts, c.method, c.path, "", tokenStar, "")
		wantEnvelopeError(t, c.method+" "+c.path, status, env, 404, "not_found")
	}
}
