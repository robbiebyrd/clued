package ghapp

import (
	"context"
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
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var (
	testKeyOnce sync.Once
	testKey     *rsa.PrivateKey
)

func key(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	testKeyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		testKey = k
	})
	return testKey
}

// decodeJWT checks the RS256 signature against pub and returns header and claims.
func decodeJWT(tok string, pub *rsa.PublicKey) (map[string]any, map[string]any, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, nil, fmt.Errorf("JWT has %d parts", len(parts))
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, nil, fmt.Errorf("signature encoding: %w", err)
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, nil, fmt.Errorf("signature does not verify: %w", err)
	}
	var header, claims map[string]any
	for i, dst := range []*map[string]any{&header, &claims} {
		raw, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			return nil, nil, fmt.Errorf("part %d encoding: %w", i, err)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			return nil, nil, fmt.Errorf("part %d JSON: %w", i, err)
		}
	}
	return header, claims, nil
}

func parseJWT(t *testing.T, tok string, pub *rsa.PublicKey) (map[string]any, map[string]any) {
	t.Helper()
	header, claims, err := decodeJWT(tok, pub)
	if err != nil {
		t.Fatal(err)
	}
	return header, claims
}

func TestJWT(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	app := &App{ID: 4242, Key: key(t), Now: func() time.Time { return now }}
	tok, err := app.JWT()
	if err != nil {
		t.Fatal(err)
	}
	header, claims := parseJWT(t, tok, &key(t).PublicKey)
	if header["alg"] != "RS256" || header["typ"] != "JWT" {
		t.Errorf("header = %v", header)
	}
	if claims["iss"] != "4242" {
		t.Errorf("iss = %v, want \"4242\"", claims["iss"])
	}
	iat, exp := int64(claims["iat"].(float64)), int64(claims["exp"].(float64))
	if iat != now.Add(-60*time.Second).Unix() {
		t.Errorf("iat = %d, want now − 60 s", iat)
	}
	if exp <= now.Unix() {
		t.Errorf("exp %d is not in the future", exp)
	}
	if exp-iat > 600 {
		t.Errorf("exp − iat = %d s, GitHub allows at most 600", exp-iat)
	}

	other, _ := rsa.GenerateKey(rand.Reader, 1024)
	parts := strings.Split(tok, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(&other.PublicKey, crypto.SHA256, digest[:], sig) == nil {
		t.Error("JWT verifies under an unrelated key")
	}
}

func TestJWTRequiresKeyAndID(t *testing.T) {
	if _, err := (&App{ID: 1}).JWT(); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: err = %v, want ErrNoKey", err)
	}
	if _, err := (&App{Key: key(t)}).JWT(); err == nil {
		t.Error("no app id: want an error")
	}
}

func TestParsePrivateKey(t *testing.T) {
	k := key(t)
	pkcs1 := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}
	pkcs8 := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	for name, data := range map[string][]byte{"pkcs1": pkcs1, "pkcs8": pkcs8} {
		got, err := ParsePrivateKey(data)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !got.Equal(k) {
			t.Errorf("%s: parsed key differs", name)
		}
	}
	for name, data := range map[string][]byte{
		"not pem":  []byte("nope"),
		"wrong":    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte{1}}),
		"garbage1": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte{1, 2}}),
	} {
		if _, err := ParsePrivateKey(data); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

// fakeGitHub serves POST /app/installations/{id}/access_tokens, checking the
// app JWT, and hands out numbered tokens that expire after ttl.
type fakeGitHub struct {
	pub   *rsa.PublicKey
	clock func() time.Time
	ttl   time.Duration
	calls atomic.Int64
	// status, when set, is answered instead of a token.
	status int
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := f.calls.Add(1)
	if r.Method != http.MethodPost {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	var id int64
	if _, err := fmt.Sscanf(r.URL.Path, "/app/installations/%d/access_tokens", &id); err != nil {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("X-GitHub-Api-Version") != APIVersion || r.Header.Get("Accept") != "application/vnd.github+json" {
		http.Error(w, "headers", http.StatusBadRequest)
		return
	}
	jwt, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		http.Error(w, "auth", http.StatusUnauthorized)
		return
	}
	_, claims, err := decodeJWT(jwt, f.pub)
	if err != nil || claims["iss"] != "7" {
		http.Error(w, "iss", http.StatusUnauthorized)
		return
	}
	if f.status != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"message":"Not Found","documentation_url":"https://docs.github.com"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"token":      fmt.Sprintf("ghs_inst%d_call%d", id, n),
		"expires_at": f.clock().Add(f.ttl).UTC().Format(time.RFC3339),
	})
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time          { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *clock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func newTokenApp(t *testing.T, status int) (*App, *fakeGitHub, *clock) {
	t.Helper()
	c := &clock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	f := &fakeGitHub{pub: &key(t).PublicKey, clock: c.Now, ttl: time.Hour, status: status}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return &App{ID: 7, Key: key(t), BaseURL: srv.URL + "/", HTTPClient: srv.Client(), Now: c.Now}, f, c
}

func TestInstallationTokenCachedUntilFiveMinutesBeforeExpiry(t *testing.T) {
	app, f, c := newTokenApp(t, 0)
	ctx := context.Background()

	first, err := app.InstallationToken(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if first.Value() != "ghs_inst11_call1" {
		t.Fatalf("token = %q", first.Value())
	}
	if !first.ExpiresAt.Equal(c.Now().Add(time.Hour)) {
		t.Errorf("ExpiresAt = %v", first.ExpiresAt)
	}

	c.Advance(54 * time.Minute) // 6 minutes left: still cached
	again, err := app.InstallationToken(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if again.Value() != first.Value() || f.calls.Load() != 1 {
		t.Errorf("token refreshed with 6 minutes left (calls %d)", f.calls.Load())
	}

	// Another installation has its own slot.
	other, err := app.InstallationToken(ctx, 12)
	if err != nil {
		t.Fatal(err)
	}
	if other.Value() != "ghs_inst12_call2" {
		t.Errorf("installation 12 token = %q", other.Value())
	}

	c.Advance(time.Minute) // exactly 5 minutes left: refreshed
	fresh, err := app.InstallationToken(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Value() == first.Value() {
		t.Error("token not refreshed at 5 minutes before expiry")
	}
	if f.calls.Load() != 3 {
		t.Errorf("calls = %d, want 3", f.calls.Load())
	}

	app.Invalidate(11)
	if tok, _ := app.InstallationToken(ctx, 11); tok.Value() == fresh.Value() {
		t.Error("Invalidate kept the cached token")
	}
}

func TestInstallationTokenConcurrentCallersShareOneRequest(t *testing.T) {
	app, f, _ := newTokenApp(t, 0)
	var wg sync.WaitGroup
	got := make([]string, 16)
	for i := range got {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := app.InstallationToken(context.Background(), 5)
			if err != nil {
				t.Error(err)
				return
			}
			got[i] = tok.Value()
		}()
	}
	wg.Wait()
	if f.calls.Load() != 1 {
		t.Errorf("calls = %d, want 1", f.calls.Load())
	}
	for _, v := range got {
		if v != got[0] {
			t.Fatalf("callers got different tokens: %v", got)
		}
	}
}

func TestInstallationTokenErrors(t *testing.T) {
	app, _, _ := newTokenApp(t, http.StatusNotFound)
	_, err := app.InstallationToken(context.Background(), 99)
	if !errors.Is(err, ErrInstallationNotFound) {
		t.Fatalf("err = %v, want ErrInstallationNotFound", err)
	}
	var te *TokenError
	if !errors.As(err, &te) || te.Status != http.StatusNotFound || te.Message != "Not Found" {
		t.Errorf("TokenError = %+v", te)
	}

	app, _, _ = newTokenApp(t, http.StatusForbidden)
	_, err = app.InstallationToken(context.Background(), 99)
	if !errors.As(err, &te) || te.Status != http.StatusForbidden || errors.Is(err, ErrInstallationNotFound) {
		t.Errorf("403: err = %v", err)
	}

	if _, err := app.InstallationToken(context.Background(), 0); err == nil {
		t.Error("installation 0: want an error")
	}
	if _, err := (&App{ID: 7, BaseURL: "http://127.0.0.1:1"}).InstallationToken(context.Background(), 1); !errors.Is(err, ErrNoKey) {
		t.Errorf("no key: err = %v", err)
	}
}

func TestTokenNeverFormatted(t *testing.T) {
	app, _, _ := newTokenApp(t, 0)
	tok, err := app.InstallationToken(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	var logged strings.Builder
	slog.New(slog.NewJSONHandler(&logged, nil)).Info("token", "tok", tok)
	slog.New(slog.NewTextHandler(&logged, nil)).Info("token", "tok", tok)
	for _, s := range []string{
		fmt.Sprint(tok), fmt.Sprintf("%v", tok), fmt.Sprintf("%+v", tok), fmt.Sprintf("%#v", tok),
		fmt.Sprintf("%s", tok), logged.String(),
	} {
		if strings.Contains(s, tok.Value()) {
			t.Errorf("token leaked: %s", s)
		}
	}
}

func TestVerify(t *testing.T) {
	secret := []byte("It's a Secret to Everybody")
	body := []byte("Hello, World!")
	// GitHub's documented test vector.
	const want = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if got := Sign(secret, body); got != want {
		t.Fatalf("Sign = %s, want %s", got, want)
	}
	if err := Verify(secret, body, want); err != nil {
		t.Fatalf("valid signature: %v", err)
	}
	if err := Verify(secret, body, "  "+want+"\n"); err != nil {
		t.Errorf("surrounding space: %v", err)
	}

	cases := []struct {
		name   string
		secret []byte
		body   []byte
		header string
		want   error
	}{
		{"missing header", secret, body, "", ErrMissingSignature},
		{"wrong secret", []byte("not the secret"), body, want, ErrBadSignature},
		{"altered body", secret, []byte("Hello, World?"), want, ErrBadSignature},
		{"empty body", secret, nil, want, ErrBadSignature},
		{"no secret", nil, body, want, ErrNoSecret},
		{"sha1 header", secret, body, "sha1=0123456789abcdef0123456789abcdef01234567", ErrMalformedSig},
		{"no prefix", secret, body, strings.TrimPrefix(want, "sha256="), ErrMalformedSig},
		{"short", secret, body, want[:len(want)-2], ErrMalformedSig},
		{"not hex", secret, body, "sha256=" + strings.Repeat("zz", 32), ErrMalformedSig},
		{"upper prefix", secret, body, "SHA256=" + want[7:], ErrMalformedSig},
	}
	for _, c := range cases {
		if err := Verify(c.secret, c.body, c.header); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}

	// Hex case does not matter; a flipped bit does.
	if err := Verify(secret, body, "sha256="+strings.ToUpper(want[7:])); err != nil {
		t.Errorf("upper-case hex: %v", err)
	}
	flipped := []byte(want)
	flipped[len(flipped)-1] = '6'
	if err := Verify(secret, body, string(flipped)); !errors.Is(err, ErrBadSignature) {
		t.Errorf("flipped digit: err = %v", err)
	}
}
