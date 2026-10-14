package remote

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// PathTokenExchange is the server's token exchange (PROTOCOL.md §7.11): the
// environment key as the bearer, a short-lived token in the result.
const PathTokenExchange = "/api/v1/token/exchange"

// Token exchange timing.
const (
	// renewBefore is how long before its end an exchanged token is
	// replaced, so no request goes out with a token about to lapse.
	renewBefore = 10 * time.Minute
	// lockWait bounds the wait for another process's exchange.
	lockWait = 10 * time.Second
	// lockStale is when a lock file left by a crashed process is taken over.
	lockStale = 30 * time.Second
)

// exchanged is the token exchange's result.
type exchanged struct {
	Token     string    `json:"token"`
	TokenID   string    `json:"token_id"`
	Name      string    `json:"name"`
	AccountID string    `json:"account_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// cachedToken is the token cache file: the exchanged token with the server
// and (the hash of) the key it was exchanged for, so a changed URL or key
// never reuses it.
type cachedToken struct {
	Server    string    `json:"server"`
	KeyHash   string    `json:"key_hash"`
	Token     string    `json:"token"`
	TokenID   string    `json:"token_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// bearer is the token a request carries: the configured token, or for an
// exchanging client a token it holds or the cache holds with more than
// renewBefore left, else a fresh exchange.
func (c *Client) bearer(ctx context.Context) (string, error) {
	if c.key == "" {
		return c.token, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Until(c.expires) > renewBefore {
		return c.token, nil
	}
	return c.obtain(ctx, "")
}

// renew replaces rejected, a token the server refused, and returns the new
// one.
func (c *Client) renew(ctx context.Context, rejected string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != rejected && c.token != "" && time.Until(c.expires) > renewBefore {
		return c.token, nil // another request renewed it meanwhile
	}
	return c.obtain(ctx, rejected)
}

// obtain takes a usable token from the cache file, or exchanges the key
// and writes the cache, under a lock file so the processes of one session
// starting together exchange once. A cached token equal to rejected is not
// used. The caller holds c.mu.
func (c *Client) obtain(ctx context.Context, rejected string) (string, error) {
	sum := sha256.Sum256([]byte(c.key))
	keyHash := hex.EncodeToString(sum[:])
	if c.cache != "" {
		unlock, err := lockFile(ctx, c.cache+".lock")
		if err != nil {
			return "", err
		}
		defer unlock()
		if t, ok := readCache(c.cache); ok && t.Server == c.baseURL && t.KeyHash == keyHash && t.Token != rejected && time.Until(t.ExpiresAt) > renewBefore {
			c.token, c.expires = t.Token, t.ExpiresAt
			return c.token, nil
		}
	}
	got, err := c.exchange(ctx)
	if err != nil {
		return "", err
	}
	c.token, c.expires = got.Token, got.ExpiresAt
	if c.cache != "" {
		// A cache that cannot be written only costs another exchange.
		_ = writeCache(c.cache, cachedToken{Server: c.baseURL, KeyHash: keyHash, Token: got.Token, TokenID: got.TokenID, ExpiresAt: got.ExpiresAt})
	}
	return c.token, nil
}

// exchange trades the key for a token, naming this machine.
func (c *Client) exchange(ctx context.Context) (exchanged, error) {
	host, _ := os.Hostname()
	body := map[string]any{"client_name": host, "client_info": map[string]string{"source": "exchange", "host": host}}
	var got exchanged
	err := c.send(ctx, "POST", PathTokenExchange, mustJSON(body), &got, c.key)
	switch {
	case errors.Is(err, ErrNotFound):
		return exchanged{}, fmt.Errorf("remote: %s offers no token exchange (an older server); give the client a token (CLUED_SERVER_TOKEN) instead of an environment key: %w", c.baseURL, err)
	case errors.Is(err, ErrUnauthorized):
		return exchanged{}, fmt.Errorf("remote: the environment key was refused (unknown, revoked, or not an environment key): %w", err)
	case err != nil:
		return exchanged{}, fmt.Errorf("remote: token exchange: %w", err)
	case got.Token == "":
		return exchanged{}, errors.New("remote: token exchange returned no token")
	}
	return got, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func readCache(path string) (cachedToken, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return cachedToken{}, false
	}
	var t cachedToken
	if json.Unmarshal(raw, &t) != nil || t.Token == "" {
		return cachedToken{}, false
	}
	return t, true
}

// writeCache replaces the cache file atomically with mode 0600.
func writeCache(path string, t cachedToken) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-token-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := json.NewEncoder(tmp).Encode(t); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// lockFile takes an exclusive lock file, waiting up to lockWait for another
// holder and taking over one older than lockStale.
func lockFile(ctx context.Context, path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, serr := os.Stat(path); serr == nil && time.Since(info.ModTime()) > lockStale {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("remote: waiting for the token exchange lock %s timed out", path)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
