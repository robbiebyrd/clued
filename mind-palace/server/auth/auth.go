// Package auth authenticates clued-server requests with bearer tokens.
//
// Tokens are minted on the server (`mind-palace server token create`), kept
// hashed in a 0600 JSON file and bound to an account id and a list of
// project slugs. Middleware reads `Authorization: Bearer <token>`, compares
// the token's SHA-256 digest in constant time and puts the matching Principal
// on the request context; failure is 401 with `WWW-Authenticate: Bearer` in
// the httpapi envelope. AllowProject and Forbid let project-scoped handlers
// answer 403 for a project outside the token's list. The token file is
// re-read whenever its mtime changes, so revocation and grants need no
// restart.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Principal is who a request acts as: the token's name, the account every
// write is stamped with and every read is scoped by, and the projects the
// token may touch ("*" for all of them).
type Principal struct {
	Name      string
	AccountID string
	Projects  []string
}

// Token is one entry of the token file. Hash is the hex SHA-256 of the raw
// token; the raw token itself is never stored. Kind is empty for a token
// and KindEnvironment for an environment key; Parent names the key a token
// was exchanged for, and Expires ends a token's life (keys and tokens
// minted by hand have none).
type Token struct {
	Name      string     `json:"name"`
	Hash      string     `json:"hash"`
	AccountID string     `json:"account_id"`
	Projects  []string   `json:"projects"`
	Created   time.Time  `json:"created"`
	Revoked   *time.Time `json:"revoked,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	// TokenTTL is, on a key, how long the tokens it is exchanged for live.
	TokenTTL Duration   `json:"token_ttl,omitempty"`
	Parent   string     `json:"parent,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
}

// KindEnvironment marks an environment key: a credential that authenticates
// nothing but the token exchange, which trades it for a short-lived token
// (one per Claude Code cloud container).
const KindEnvironment = "environment"

// Token lifetimes of an environment key's exchanges.
const (
	DefaultTokenTTL = 24 * time.Hour
	MinTokenTTL     = time.Hour
	MaxTokenTTL     = 7 * 24 * time.Hour
	// ExchangesPerHour bounds the exchanges of one key in a rolling hour.
	ExchangesPerHour = 60
	// pruneAfter is how long an expired exchanged token stays in the file.
	pruneAfter = 24 * time.Hour
)

// Duration is a time.Duration written as a Go duration string ("24h").
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(time.Duration(d).String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Expired reports whether the token's life ended before now.
func (t Token) Expired(now time.Time) bool { return t.Expires != nil && !now.Before(*t.Expires) }

// Source resolves a raw bearer token to its principal; TokenFile implements
// it and tests stub it.
type Source interface {
	Lookup(raw string) (Principal, bool)
}

// TokenFile is the token store: a JSON file {"tokens": [...]} owned by the
// server operator. It is read lazily and re-read when the file's mtime or
// size changes; writes replace the file atomically with mode 0600.
type TokenFile struct {
	path string
	// Now is the clock (time.Now when nil); tests set it.
	Now func() time.Time

	mu        sync.Mutex
	exchanges map[string][]time.Time
	loaded    bool
	mtime     time.Time
	size      int64
	tokens    []Token
	err       error
}

// OpenTokenFile returns a TokenFile for path. Nothing is read until the
// first call; a missing file is an empty token set.
func OpenTokenFile(path string) *TokenFile {
	return &TokenFile{path: path}
}

// Path returns the file the tokens live in.
func (f *TokenFile) Path() string { return f.path }

// HashToken returns the hex SHA-256 digest stored for a raw token.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// matchHash compares two hex digests in constant time.
func matchHash(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func (f *TokenFile) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

// Lookup resolves a raw token to its principal. Every stored token is
// compared with crypto/subtle, so the time taken does not reveal how close
// a guess came; revoked and expired tokens never match, and neither does an
// environment key, which only the exchange accepts.
func (f *TokenFile) Lookup(raw string) (Principal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.match(raw)
	if i < 0 || f.tokens[i].Kind == KindEnvironment || f.tokens[i].Expired(f.now()) {
		return Principal{}, false
	}
	t := f.tokens[i]
	return Principal{Name: t.Name, AccountID: t.AccountID, Projects: slices.Clone(t.Projects)}, true
}

// match returns the index of the unrevoked token whose hash is raw's, or -1.
// The caller holds f.mu.
func (f *TokenFile) match(raw string) int {
	if err := f.load(); err != nil || raw == "" {
		return -1
	}
	want := HashToken(raw)
	found := -1
	for i := range f.tokens {
		if matchHash(f.tokens[i].Hash, want) && f.tokens[i].Revoked == nil {
			found = i
		}
	}
	return found
}

// Exchanged is what the token exchange hands a client: a token, its name
// (TokenID is the same name, as for the device login), its account and the
// end of its life.
type Exchanged struct {
	Token     string    `json:"token"`
	TokenID   string    `json:"token_id"`
	Name      string    `json:"name"`
	AccountID string    `json:"account_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Exchanger trades an environment key for a short-lived token;
// TokenFile implements it for the clued server, the cloud's account service
// for the cloud.
type Exchanger interface {
	Exchange(ctx context.Context, key, clientName string, info map[string]string) (Exchanged, error)
}

// Exchange errors.
var (
	// ErrInvalidKey: the key is unknown, revoked or not an environment key.
	ErrInvalidKey = errors.New("auth: unknown or revoked environment key")
	// ErrRateLimited: the key was exchanged ExchangesPerHour times in the
	// last hour.
	ErrRateLimited = errors.New("auth: too many exchanges for this key; try again later")
)

// CreateEnvironmentKey mints an environment key, which authenticates only
// Exchange: the tokens it is exchanged for get its account and projects and
// live ttl (DefaultTokenTTL when zero; MinTokenTTL to MaxTokenTTL).
func (f *TokenFile) CreateEnvironmentKey(name, accountID string, projects []string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = DefaultTokenTTL
	}
	if ttl < MinTokenTTL || ttl > MaxTokenTTL {
		return "", fmt.Errorf("auth: token lifetime %s is outside %s to %s", ttl, MinTokenTTL, MaxTokenTTL)
	}
	return f.create(name, accountID, projects, func(t *Token) {
		t.Kind, t.TokenTTL = KindEnvironment, Duration(ttl)
	})
}

// Exchange trades an environment key for a new token named
// <key>@<client>-<4 hex> with the key's account and projects, expiring after
// the key's TokenTTL. Tokens exchanged before and expired for more than a
// day leave the file on the way.
func (f *TokenFile) Exchange(_ context.Context, key, clientName string, _ map[string]string) (Exchanged, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.match(key)
	if i < 0 || f.tokens[i].Kind != KindEnvironment {
		return Exchanged{}, ErrInvalidKey
	}
	k := f.tokens[i]
	now := f.now()
	if !f.allowExchange(k.Name, now) {
		return Exchanged{}, ErrRateLimited
	}
	suffix := make([]byte, 2)
	if _, err := rand.Read(suffix); err != nil {
		return Exchanged{}, fmt.Errorf("auth: generate token name: %w", err)
	}
	name := k.Name + "@" + hex.EncodeToString(suffix)
	if c := clientLabel(clientName); c != "" {
		name = k.Name + "@" + c + "-" + hex.EncodeToString(suffix)
	}
	ttl := time.Duration(k.TokenTTL)
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	expires := now.UTC().Add(ttl).Truncate(time.Second)
	f.prune(now)
	raw, err := f.appendToken(name, k.AccountID, k.Projects, func(t *Token) {
		t.Parent, t.Expires = k.Name, &expires
	})
	if err != nil {
		return Exchanged{}, err
	}
	return Exchanged{Token: raw, TokenID: name, Name: name, AccountID: k.AccountID, ExpiresAt: expires}, nil
}

// allowExchange counts an exchange of the named key, refusing it past
// ExchangesPerHour in the last hour. The caller holds f.mu.
func (f *TokenFile) allowExchange(key string, now time.Time) bool {
	if f.exchanges == nil {
		f.exchanges = map[string][]time.Time{}
	}
	recent := f.exchanges[key][:0]
	for _, at := range f.exchanges[key] {
		if now.Sub(at) < time.Hour {
			recent = append(recent, at)
		}
	}
	if len(recent) >= ExchangesPerHour {
		f.exchanges[key] = recent
		return false
	}
	f.exchanges[key] = append(recent, now)
	return true
}

// prune drops exchanged tokens that expired more than pruneAfter ago. The
// caller holds f.mu and saves.
func (f *TokenFile) prune(now time.Time) {
	kept := f.tokens[:0]
	for _, t := range f.tokens {
		if t.Parent != "" && t.Expires != nil && now.Sub(*t.Expires) > pruneAfter {
			continue
		}
		kept = append(kept, t)
	}
	f.tokens = kept
}

// clientLabel keeps the letters, digits, dots and dashes of a client name,
// at most 40 of them.
func clientLabel(name string) string {
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= 40 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ErrTokenExists is returned (wrapped, naming the token) by Create for a
// name the file already holds.
var ErrTokenExists = errors.New("auth: token already exists")

// Create mints a token: 32 random bytes, base64url without padding, returned
// exactly once. The file keeps its SHA-256 digest with the name, the account
// (a fresh random id when accountID is empty) and the projects. Names are
// unique within the file: a taken name is ErrTokenExists.
func (f *TokenFile) Create(name, accountID string, projects []string) (string, error) {
	return f.create(name, accountID, projects, nil)
}

func (f *TokenFile) create(name, accountID string, projects []string, set func(*Token)) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return "", err
	}
	return f.appendToken(name, accountID, projects, set)
}

// appendToken adds a token (set fills in the extra fields) and saves. The
// caller holds f.mu and has loaded the file.
func (f *TokenFile) appendToken(name, accountID string, projects []string, set func(*Token)) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("auth: token name is required")
	}
	for _, t := range f.tokens {
		if t.Name == name {
			return "", fmt.Errorf("%w: %q", ErrTokenExists, name)
		}
	}
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		return "", fmt.Errorf("auth: generate token: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(rawBytes)
	if accountID == "" {
		id, err := newAccountID()
		if err != nil {
			return "", err
		}
		accountID = id
	}
	t := Token{
		Name:      name,
		Hash:      HashToken(raw),
		AccountID: accountID,
		Projects:  cleanProjects(nil, projects),
		Created:   f.now().UTC().Truncate(time.Second),
	}
	if set != nil {
		set(&t)
	}
	f.tokens = append(f.tokens, t)
	if err := f.save(); err != nil {
		return "", err
	}
	return raw, nil
}

// List returns every token in the file, revoked ones included.
func (f *TokenFile) List() ([]Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return nil, err
	}
	out := make([]Token, len(f.tokens))
	for i, t := range f.tokens {
		out[i] = t
		out[i].Projects = slices.Clone(t.Projects)
		if t.Revoked != nil {
			r := *t.Revoked
			out[i].Revoked = &r
		}
		if t.Expires != nil {
			e := *t.Expires
			out[i].Expires = &e
		}
	}
	return out, nil
}

// Revoke marks the named token revoked; it stops matching at once and stays
// in the file for the record. Revoking an environment key also revokes the
// tokens it was exchanged for. Revoking twice is not an error.
func (f *TokenFile) Revoke(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return err
	}
	i := f.index(name)
	if i < 0 {
		return fmt.Errorf("auth: no token named %q", name)
	}
	now := f.now().UTC().Truncate(time.Second)
	changed := false
	for j := range f.tokens {
		if (j == i || (f.tokens[i].Kind == KindEnvironment && f.tokens[j].Parent == name)) && f.tokens[j].Revoked == nil {
			f.tokens[j].Revoked = &now
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return f.save()
}

// Grant adds projects to the named token's list, skipping those already
// present. A revoked token cannot be granted anything.
func (f *TokenFile) Grant(name string, projects ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return err
	}
	i := f.index(name)
	if i < 0 {
		return fmt.Errorf("auth: no token named %q", name)
	}
	if f.tokens[i].Revoked != nil {
		return fmt.Errorf("auth: token %q is revoked", name)
	}
	f.tokens[i].Projects = cleanProjects(f.tokens[i].Projects, projects)
	return f.save()
}

func (f *TokenFile) index(name string) int {
	for i, t := range f.tokens {
		if t.Name == name {
			return i
		}
	}
	return -1
}

// cleanProjects appends the trimmed, non-empty, not yet listed slugs to base.
func cleanProjects(base, add []string) []string {
	out := slices.Clone(base)
	if out == nil {
		out = []string{}
	}
	for _, p := range add {
		p = strings.TrimSpace(p)
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// newAccountID returns a random UUID-shaped id (version 4 layout).
func newAccountID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generate account id: %w", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// tokenFileBody is the on-disk shape.
type tokenFileBody struct {
	Tokens []Token `json:"tokens"`
}

// load reads the file when it has not been read yet or its mtime or size
// changed since. The caller holds f.mu. A missing file is an empty set.
func (f *TokenFile) load() error {
	info, err := os.Stat(f.path)
	if errors.Is(err, os.ErrNotExist) {
		f.loaded, f.mtime, f.size, f.tokens, f.err = true, time.Time{}, 0, nil, nil
		return nil
	}
	if err != nil {
		f.err = fmt.Errorf("auth: stat token file: %w", err)
		return f.err
	}
	if f.loaded && info.ModTime().Equal(f.mtime) && info.Size() == f.size {
		return f.err
	}
	data, err := os.ReadFile(f.path)
	if err != nil {
		f.err = fmt.Errorf("auth: read token file: %w", err)
		return f.err
	}
	var body tokenFileBody
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &body); err != nil {
			f.loaded, f.mtime, f.size, f.tokens = true, info.ModTime(), info.Size(), nil
			f.err = fmt.Errorf("auth: parse token file %s: %w", f.path, err)
			return f.err
		}
	}
	for i := range body.Tokens {
		if body.Tokens[i].Projects == nil {
			body.Tokens[i].Projects = []string{}
		}
	}
	f.loaded, f.mtime, f.size, f.tokens, f.err = true, info.ModTime(), info.Size(), body.Tokens, nil
	return nil
}

// save writes the tokens atomically (temp file in the same directory, 0600,
// rename) and records the new mtime. The caller holds f.mu.
func (f *TokenFile) save() error {
	data, err := json.MarshalIndent(tokenFileBody{Tokens: f.tokens}, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: encode token file: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("auth: create token directory: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".tokens-*.json")
	if err != nil {
		return fmt.Errorf("auth: write token file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("auth: write token file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if _, err := tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err := tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("auth: write token file: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("auth: replace token file: %w", err)
	}
	if info, err := os.Stat(f.path); err == nil {
		f.loaded, f.mtime, f.size, f.err = true, info.ModTime(), info.Size(), nil
	} else {
		f.loaded = false
	}
	return nil
}

type principalKey struct{}

// WithPrincipal returns ctx carrying p.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the principal the middleware stored on ctx.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// Middleware authenticates every request with src. A missing, malformed,
// unknown or revoked bearer token answers 401 with WWW-Authenticate: Bearer;
// otherwise the principal is put on the context and next runs.
func Middleware(src Source) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw, msg := bearer(r.Header.Get("Authorization"))
			if msg != "" {
				Unauthorized(w, msg)
				return
			}
			p, ok := src.Lookup(raw)
			if !ok {
				Unauthorized(w, "unknown or revoked token")
				return
			}
			next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), p)))
		})
	}
}

// bearer extracts the token from an Authorization header value, or explains
// why it cannot.
// Bearer returns the raw token of the request's Authorization header, or
// "" with the reason it has none.
func Bearer(r *http.Request) (raw, problem string) { return bearer(r.Header.Get("Authorization")) }

func bearer(header string) (raw, problem string) {
	if strings.TrimSpace(header) == "" {
		return "", "missing Authorization header"
	}
	scheme, rest, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return "", "malformed Authorization header: expected \"Bearer <token>\""
	}
	raw = strings.TrimSpace(rest)
	if raw == "" || strings.ContainsAny(raw, " \t") {
		return "", "malformed Authorization header: expected \"Bearer <token>\""
	}
	return raw, ""
}

// AllowProject reports whether p may touch the project slug: its list holds
// "*" or the slug itself.
func AllowProject(p Principal, slug string) bool {
	if slug == "" {
		return false
	}
	for _, s := range p.Projects {
		if s == "*" || s == slug {
			return true
		}
	}
	return false
}

// Unauthorized writes the 401 envelope with the Bearer challenge.
func Unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	writeError(w, http.StatusUnauthorized, "unauthorized", msg)
}

// Forbid writes the 403 envelope for a principal outside a project's list.
func Forbid(w http.ResponseWriter, msg string) {
	writeError(w, http.StatusForbidden, "forbidden", msg)
}

// whoami is the body of GET /api/v1/whoami.
type whoami struct {
	Name      string   `json:"name"`
	AccountID string   `json:"account_id"`
	Projects  []string `json:"projects"`
	Version   string   `json:"version"`
}

// WhoamiHandler answers with the request's principal, its projects and the
// server version; clients use it as the connectivity and token probe. It
// expects to run behind Middleware and answers 401 without a principal.
func WhoamiHandler(version string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := FromContext(r.Context())
		if !ok {
			Unauthorized(w, "missing Authorization header")
			return
		}
		projects := p.Projects
		if projects == nil {
			projects = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": whoami{
			Name: p.Name, AccountID: p.AccountID, Projects: projects, Version: version,
		}})
	})
}

func writeError(w http.ResponseWriter, status int, kind, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": map[string]string{"kind": kind, "message": msg}})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
