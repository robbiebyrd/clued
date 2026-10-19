// Package ghapp is clued's GitHub App plumbing: the RS256 app JWT, the
// installation access tokens it buys (cached per installation until five
// minutes before they expire) and the verification of webhook deliveries
// (HMAC-SHA256 over the raw body, compared in constant time). It uses the
// standard library only and never logs a key, a token or a payload.
package ghapp

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is GitHub's REST API, used when App.BaseURL is empty.
const DefaultBaseURL = "https://api.github.com"

// JWT timing. GitHub refuses an app JWT whose exp is more than ten minutes
// after iat; iat is backdated by a minute to absorb clock drift between this
// host and GitHub.
const (
	jwtBackdate = 60 * time.Second
	jwtLifetime = 9 * time.Minute // exp − now; exp − iat = 10 minutes
)

// ErrNoKey is returned when the App has no private key to sign with.
var ErrNoKey = errors.New("ghapp: app private key is not set")

// App is one GitHub App registration: its numeric app id, the private key
// GitHub generated for it and the API base URL (DefaultBaseURL when empty;
// tests point it at an httptest server). Use it by pointer: it carries the
// installation-token cache. The zero values of HTTPClient and Now mean
// http.DefaultClient and time.Now.
type App struct {
	ID      int64
	Key     *rsa.PrivateKey
	BaseURL string

	HTTPClient *http.Client
	Now        func() time.Time

	mu     sync.Mutex
	tokens map[int64]*tokenEntry
}

// ParsePrivateKey reads the PEM private key GitHub issues for an app (PKCS#1,
// "RSA PRIVATE KEY") or the same key converted to PKCS#8 ("PRIVATE KEY").
func ParsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("ghapp: private key is not PEM")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("ghapp: parse PKCS#1 private key: %w", err)
		}
		return key, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("ghapp: parse PKCS#8 private key: %w", err)
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("ghapp: private key is not RSA")
		}
		return key, nil
	default:
		return nil, fmt.Errorf("ghapp: unsupported PEM block %q", block.Type)
	}
}

// JWT returns a fresh app JWT for authenticating as the app itself (the
// /app/... endpoints, including buying installation tokens): iat 60 s in the
// past, exp nine minutes ahead, iss the app id, signed RS256.
func (a *App) JWT() (string, error) {
	if a.Key == nil {
		return "", ErrNoKey
	}
	if a.ID <= 0 {
		return "", errors.New("ghapp: app id is not set")
	}
	return SignJWT(a.ID, a.Key, a.now())
}

// SignJWT builds the RS256 app JWT for appID at now. It is JWT without an App
// (and with an explicit clock) for callers and tests that need one.
func SignJWT(appID int64, key *rsa.PrivateKey, now time.Time) (string, error) {
	if key == nil {
		return "", ErrNoKey
	}
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(struct {
		IAT int64  `json:"iat"`
		EXP int64  `json:"exp"`
		ISS string `json:"iss"`
	}{
		IAT: now.Add(-jwtBackdate).Unix(),
		EXP: now.Add(jwtLifetime).Unix(),
		ISS: strconv.FormatInt(appID, 10),
	})
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	digest := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("ghapp: sign app JWT: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}

func (a *App) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *App) baseURL() string {
	if a.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(a.BaseURL, "/")
}

func (a *App) httpClient() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return http.DefaultClient
}
