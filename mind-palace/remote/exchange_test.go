package remote_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

// exchangeServer is a clued server whose token file serves the exchange,
// with one environment key.
func exchangeServer(t *testing.T) (*httptest.Server, *auth.TokenFile, string) {
	t.Helper()
	tokens := auth.OpenTokenFile(filepath.Join(t.TempDir(), "tokens.json"))
	key, err := tokens.CreateEnvironmentKey("cloud-env", "acct", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := server.NewClued(server.CluedOptions{
		Version: "test", Tokens: tokens, Exchange: tokens, Sessions: memsession.New(),
		Open:   func(_ context.Context, p string) (store.Store, error) { return memstore.New(p), nil },
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	t.Cleanup(c.Close)
	ts := httptest.NewServer(c.Handler)
	t.Cleanup(ts.Close)
	return ts, tokens, key
}

func whoami(t *testing.T, c *remote.Client) (string, error) {
	t.Helper()
	var got struct {
		Name      string `json:"name"`
		AccountID string `json:"account_id"`
	}
	err := c.Do(context.Background(), http.MethodGet, server.PathWhoami, nil, &got)
	return got.Name, err
}

func exchangedTokens(t *testing.T, f *auth.TokenFile) int {
	t.Helper()
	list, err := f.List()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, tok := range list {
		if tok.Parent != "" {
			n++
		}
	}
	return n
}

func keyClient(t *testing.T, ts *httptest.Server, key, cache string) *remote.Client {
	t.Helper()
	c, err := remote.New(session.Config{ServerURL: ts.URL, ServerKey: key, TokenCache: cache})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAClientWithAnEnvironmentKeyExchangesOnceAndCaches(t *testing.T) {
	ts, tokens, key := exchangeServer(t)
	cache := filepath.Join(t.TempDir(), "server-token.json")
	name, err := whoami(t, keyClient(t, ts, key, cache))
	if err != nil || !strings.HasPrefix(name, "cloud-env@") {
		t.Fatalf("whoami = %q, %v", name, err)
	}
	if n := exchangedTokens(t, tokens); n != 1 {
		t.Fatalf("%d exchanges, want 1", n)
	}
	info, err := os.Stat(cache)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("cache: %v %v", info, err)
	}
	raw, _ := os.ReadFile(cache)
	if strings.Contains(string(raw), key) {
		t.Error("the cache holds the key itself")
	}
	// Another process of the same container reads the cache.
	if again, err := whoami(t, keyClient(t, ts, key, cache)); err != nil || again != name {
		t.Fatalf("second client: %q, %v", again, err)
	}
	if n := exchangedTokens(t, tokens); n != 1 {
		t.Errorf("the cache was not reused: %d exchanges", n)
	}

	// The hooks of one session start together: one exchange between them.
	fresh := filepath.Join(t.TempDir(), "server-token.json")
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := whoami(t, keyClient(t, ts, key, fresh))
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := exchangedTokens(t, tokens); n != 2 {
		t.Errorf("concurrent clients exchanged %d times, want once more (2 in all)", n)
	}
}

func TestAClientRenewsAnExpiringOrRejectedToken(t *testing.T) {
	ts, tokens, key := exchangeServer(t)
	cache := filepath.Join(t.TempDir(), "server-token.json")
	first, err := whoami(t, keyClient(t, ts, key, cache))
	if err != nil {
		t.Fatal(err)
	}

	// A cached token about to lapse is replaced before it is sent.
	var c struct {
		Server    string    `json:"server"`
		KeyHash   string    `json:"key_hash"`
		Token     string    `json:"token"`
		TokenID   string    `json:"token_id"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	raw, _ := os.ReadFile(cache)
	json.Unmarshal(raw, &c)
	c.ExpiresAt = time.Now().Add(5 * time.Minute)
	raw, _ = json.Marshal(c)
	os.WriteFile(cache, raw, 0o600)
	second, err := whoami(t, keyClient(t, ts, key, cache))
	if err != nil || second == first {
		t.Fatalf("an expiring token was reused: %q (first %q), %v", second, first, err)
	}

	// A token the server refuses (revoked on its own) is exchanged anew and
	// the request retried.
	if err := tokens.Revoke(second); err != nil {
		t.Fatal(err)
	}
	third, err := whoami(t, keyClient(t, ts, key, cache))
	if err != nil || third == second {
		t.Fatalf("after a rejected token: %q, %v", third, err)
	}

	// Revoking the key cuts every client off.
	if err := tokens.Revoke("cloud-env"); err != nil {
		t.Fatal(err)
	}
	if _, err := whoami(t, keyClient(t, ts, key, cache)); !errors.Is(err, remote.ErrUnauthorized) {
		t.Errorf("after the key was revoked: %v", err)
	}
}

func TestAnEnvironmentKeyIsNoToken(t *testing.T) {
	ts, _, key := exchangeServer(t)
	c, err := remote.New(session.Config{ServerURL: ts.URL, ServerToken: key})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := whoami(t, c); !errors.Is(err, remote.ErrUnauthorized) {
		t.Errorf("a key sent as a token: %v", err)
	}
	// A server without the exchange says so.
	plain := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(plain.Close)
	if _, err := whoami(t, keyClient(t, plain, key, "")); err == nil || !strings.Contains(err.Error(), "token exchange") {
		t.Errorf("against a server without the exchange: %v", err)
	}
}
