package ghapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// TokenRefreshMargin is how long before its expiry a cached installation
// token is replaced: a token handed out always has at least this long left.
const TokenRefreshMargin = 5 * time.Minute

// APIVersion is the GitHub REST API version every request asks for.
const APIVersion = "2022-11-28"

// maxErrorBody bounds what is read of a GitHub response body.
const maxErrorBody = 1 << 20

// ErrInstallationNotFound is a 404 from GitHub when buying a token: the
// installation was removed (or never belonged to this app).
var ErrInstallationNotFound = errors.New("ghapp: installation not found")

// TokenError is a failed installation-token request. It carries the HTTP
// status and GitHub's message, never the token or the JWT; a 404 unwraps to
// ErrInstallationNotFound.
type TokenError struct {
	InstallationID int64
	Status         int
	Message        string
}

func (e *TokenError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("ghapp: installation %d token: HTTP %d: %s", e.InstallationID, e.Status, msg)
}

// Unwrap maps a 404 to ErrInstallationNotFound.
func (e *TokenError) Unwrap() error {
	if e.Status == http.StatusNotFound {
		return ErrInstallationNotFound
	}
	return nil
}

// Token is an installation access token. Its String, GoString and LogValue
// hide the secret so that formatting or logging a Token never leaks it; use
// Value for the Authorization header.
type Token struct {
	value     string
	ExpiresAt time.Time
}

// Value is the raw token, for the Authorization header only.
func (t Token) Value() string { return t.value }

// String hides the token.
func (t Token) String() string {
	return "ghapp.Token{redacted, expires " + t.ExpiresAt.UTC().Format(time.RFC3339) + "}"
}

// GoString hides the token from %#v.
func (t Token) GoString() string { return t.String() }

// LogValue hides the token from log/slog.
func (t Token) LogValue() slog.Value {
	return slog.GroupValue(slog.String("token", "redacted"), slog.Time("expires_at", t.ExpiresAt))
}

// tokenEntry is one installation's cache slot. Its mutex serialises the
// refresh so concurrent callers for the same installation share one request
// while other installations proceed.
type tokenEntry struct {
	mu    sync.Mutex
	token Token
}

// InstallationToken returns an access token for the installation, from the
// cache while it has more than TokenRefreshMargin left, otherwise bought
// afresh with the app JWT (POST /app/installations/{id}/access_tokens).
func (a *App) InstallationToken(ctx context.Context, installationID int64) (Token, error) {
	if installationID <= 0 {
		return Token{}, fmt.Errorf("ghapp: invalid installation id %d", installationID)
	}
	entry := a.entry(installationID)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.token.value != "" && a.now().Add(TokenRefreshMargin).Before(entry.token.ExpiresAt) {
		return entry.token, nil
	}
	tok, err := a.fetchToken(ctx, installationID)
	if err != nil {
		return Token{}, err
	}
	entry.token = tok
	return tok, nil
}

// Invalidate drops the cached token of an installation, for a caller that got
// a 401 with it or saw the installation removed or suspended.
func (a *App) Invalidate(installationID int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.tokens, installationID)
}

func (a *App) entry(installationID int64) *tokenEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokens == nil {
		a.tokens = make(map[int64]*tokenEntry)
	}
	e, ok := a.tokens[installationID]
	if !ok {
		e = &tokenEntry{}
		a.tokens[installationID] = e
	}
	return e
}

func (a *App) fetchToken(ctx context.Context, installationID int64) (Token, error) {
	jwt, err := a.JWT()
	if err != nil {
		return Token{}, err
	}
	url := a.baseURL() + "/app/installations/" + strconv.FormatInt(installationID, 10) + "/access_tokens"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return Token{}, fmt.Errorf("ghapp: installation %d token: %w", installationID, err)
	}
	SetHeaders(req, jwt)
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("ghapp: installation %d token: %w", installationID, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	if err != nil {
		return Token{}, fmt.Errorf("ghapp: installation %d token: read response: %w", installationID, err)
	}
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &e)
		return Token{}, &TokenError{InstallationID: installationID, Status: resp.StatusCode, Message: e.Message}
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return Token{}, fmt.Errorf("ghapp: installation %d token: decode response: %w", installationID, err)
	}
	if out.Token == "" || out.ExpiresAt.IsZero() {
		return Token{}, fmt.Errorf("ghapp: installation %d token: response without token or expires_at", installationID)
	}
	return Token{value: out.Token, ExpiresAt: out.ExpiresAt}, nil
}

// SetHeaders sets what every GitHub REST request carries: the bearer
// credential (an app JWT or an installation token), the JSON media type and
// the pinned API version.
func SetHeaders(req *http.Request, credential string) {
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", APIVersion)
	req.Header.Set("User-Agent", "clued-github-app")
}
