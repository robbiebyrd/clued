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
// token; the raw token itself is never stored.
type Token struct {
	Name      string     `json:"name"`
	Hash      string     `json:"hash"`
	AccountID string     `json:"account_id"`
	Projects  []string   `json:"projects"`
	Created   time.Time  `json:"created"`
	Revoked   *time.Time `json:"revoked,omitempty"`
}

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

	mu     sync.Mutex
	loaded bool
	mtime  time.Time
	size   int64
	tokens []Token
	err    error
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

// Lookup resolves a raw token to its principal. Every stored token is
// compared with crypto/subtle, so the time taken does not reveal how close
// a guess came; revoked tokens never match.
func (f *TokenFile) Lookup(raw string) (Principal, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil || raw == "" {
		return Principal{}, false
	}
	want := HashToken(raw)
	found := -1
	for i := range f.tokens {
		if matchHash(f.tokens[i].Hash, want) && f.tokens[i].Revoked == nil {
			found = i
		}
	}
	if found < 0 {
		return Principal{}, false
	}
	t := f.tokens[found]
	return Principal{Name: t.Name, AccountID: t.AccountID, Projects: slices.Clone(t.Projects)}, true
}

// ErrTokenExists is returned (wrapped, naming the token) by Create for a
// name the file already holds.
var ErrTokenExists = errors.New("auth: token already exists")

// Create mints a token: 32 random bytes, base64url without padding, returned
// exactly once. The file keeps its SHA-256 digest with the name, the account
// (a fresh random id when accountID is empty) and the projects. Names are
// unique within the file: a taken name is ErrTokenExists.
func (f *TokenFile) Create(name, accountID string, projects []string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("auth: token name is required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.load(); err != nil {
		return "", err
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
	f.tokens = append(f.tokens, Token{
		Name:      name,
		Hash:      HashToken(raw),
		AccountID: accountID,
		Projects:  cleanProjects(nil, projects),
		Created:   time.Now().UTC().Truncate(time.Second),
	})
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
	}
	return out, nil
}

// Revoke marks the named token revoked; it stops matching at once and stays
// in the file for the record. Revoking twice is not an error.
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
	if f.tokens[i].Revoked != nil {
		return nil
	}
	now := time.Now().UTC().Truncate(time.Second)
	f.tokens[i].Revoked = &now
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
