package remote

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// errAny marks a scheme case that must fail with something other than
// ErrInsecureURL.
var errAny = errors.New("any error")

func TestNewSchemeRule(t *testing.T) {
	cases := []struct {
		name     string
		url      string
		insecure bool
		wantErr  error
	}{
		{name: "https remote", url: "https://clued.example.com"},
		{name: "https with path and port", url: "https://clued.example.com:8443/clued"},
		{name: "http localhost", url: "http://localhost:8085"},
		{name: "http LOCALHOST", url: "http://LOCALHOST:8085"},
		{name: "http 127.0.0.1", url: "http://127.0.0.1:8085"},
		{name: "http 127.0.0.0/8", url: "http://127.5.6.7"},
		{name: "http ::1", url: "http://[::1]:8085"},
		{name: "http remote host", url: "http://clued.example.com", wantErr: ErrInsecureURL},
		{name: "http private ip", url: "http://10.0.0.5:8085", wantErr: ErrInsecureURL},
		{name: "http ipv6 non-loopback", url: "http://[fe80::1]:8085", wantErr: ErrInsecureURL},
		{name: "http remote host insecure", url: "http://clued.example.com", insecure: true},
		{name: "http private ip insecure", url: "http://10.0.0.5:8085", insecure: true},
		{name: "other scheme", url: "ftp://clued.example.com", wantErr: errAny},
		{name: "other scheme insecure", url: "ftp://clued.example.com", insecure: true, wantErr: errAny},
		{name: "no scheme", url: "clued.example.com", wantErr: errAny},
		{name: "empty", url: "", wantErr: errAny},
		{name: "no host", url: "https://", wantErr: errAny},
		{name: "unparsable", url: "https://clued.example.com/%zz", wantErr: errAny},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := New(session.Config{ServerURL: tc.url, ServerToken: "tok", ServerInsecure: tc.insecure})
			switch {
			case tc.wantErr == nil:
				if err != nil {
					t.Fatalf("New(%q) = %v, want success", tc.url, err)
				}
				if c == nil {
					t.Fatal("New returned nil client without error")
				}
			case errors.Is(tc.wantErr, errAny):
				if err == nil {
					t.Fatalf("New(%q) succeeded, want an error", tc.url)
				}
				if errors.Is(err, ErrInsecureURL) {
					t.Fatalf("New(%q) = ErrInsecureURL, want a different error", tc.url)
				}
			default:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("New(%q) = %v, want %v", tc.url, err, tc.wantErr)
				}
				if c != nil {
					t.Fatal("New returned a client together with an error")
				}
			}
		})
	}
}

func TestNewTrimsTrailingSlashAndKeepsToken(t *testing.T) {
	c, err := New(session.Config{ServerURL: "https://clued.example.com/base/", ServerToken: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.BaseURL(); got != "https://clued.example.com/base" {
		t.Fatalf("BaseURL = %q", got)
	}
	if got := c.Token(); got != "secret" {
		t.Fatalf("Token = %q", got)
	}
	if c.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %v, want %v", c.Timeout, DefaultTimeout)
	}
}

func TestFromConfig(t *testing.T) {
	if c, ok := FromConfig(session.Config{}); ok || c != nil {
		t.Fatalf("FromConfig(empty) = %v, %v; want nil, false", c, ok)
	}
	if c, ok := FromConfig(session.Config{ServerURL: "https://clued.example.com"}); !ok || c == nil {
		t.Fatalf("FromConfig(https) = %v, %v; want client, true", c, ok)
	}
	if c, ok := FromConfig(session.Config{ServerURL: "http://clued.example.com"}); ok || c != nil {
		t.Fatalf("FromConfig(insecure http) = %v, %v; want nil, false", c, ok)
	}
	if c, ok := FromConfig(session.Config{ServerURL: "http://clued.example.com", ServerInsecure: true}); !ok || c == nil {
		t.Fatalf("FromConfig(http, serverInsecure) = %v, %v; want client, true", c, ok)
	}
}

func TestFromConfigE(t *testing.T) {
	if c, err := FromConfigE(session.Config{}); err != nil || c != nil {
		t.Fatalf("FromConfigE(empty) = %v, %v; want nil, nil", c, err)
	}
	if c, err := FromConfigE(session.Config{ServerURL: "https://clued.example.com"}); err != nil || c == nil {
		t.Fatalf("FromConfigE(https) = %v, %v; want client, nil", c, err)
	}
	if _, err := FromConfigE(session.Config{ServerURL: "http://clued.example.com"}); !errors.Is(err, ErrInsecureURL) {
		t.Fatalf("FromConfigE(insecure http) = %v, want ErrInsecureURL", err)
	}
}

type recorded struct {
	method, path, auth, ua, ctype, accept string
	body                                  []byte
}

// envelopeServer records the last request and answers with status and body.
func envelopeServer(t *testing.T, status int, body string) (*httptest.Server, *recorded) {
	t.Helper()
	rec := &recorded{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*rec = recorded{
			method: r.Method, path: r.URL.Path, auth: r.Header.Get("Authorization"),
			ua: r.Header.Get("User-Agent"), ctype: r.Header.Get("Content-Type"),
			accept: r.Header.Get("Accept"), body: b,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func client(t *testing.T, srv *httptest.Server, cfg session.Config) *Client {
	t.Helper()
	cfg.ServerURL = srv.URL
	if cfg.ServerToken == "" {
		cfg.ServerToken = "tok-123"
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestDoSendsJSONWithHeadersAndDecodesResult(t *testing.T) {
	srv, rec := envelopeServer(t, http.StatusOK, `{"ok": true, "result": {"id": "abc", "n": 3}, "warning": "ignored"}`)
	c := client(t, srv, session.Config{})

	in := map[string]any{"name": "x", "count": 2}
	var out struct {
		ID string `json:"id"`
		N  int    `json:"n"`
	}
	if err := c.Do(context.Background(), http.MethodPost, "/api/v1/things", in, &out); err != nil {
		t.Fatal(err)
	}
	if out.ID != "abc" || out.N != 3 {
		t.Fatalf("out = %+v", out)
	}
	if rec.method != http.MethodPost || rec.path != "/api/v1/things" {
		t.Fatalf("request = %s %s", rec.method, rec.path)
	}
	if rec.auth != "Bearer tok-123" {
		t.Fatalf("Authorization = %q", rec.auth)
	}
	if rec.ua != "clued/"+Version || !strings.HasPrefix(rec.ua, "clued/") {
		t.Fatalf("User-Agent = %q", rec.ua)
	}
	if !strings.HasPrefix(rec.ctype, "application/json") {
		t.Fatalf("Content-Type = %q", rec.ctype)
	}
	if !strings.Contains(rec.accept, "application/json") {
		t.Fatalf("Accept = %q", rec.accept)
	}
	var sent map[string]any
	if err := json.Unmarshal(rec.body, &sent); err != nil {
		t.Fatalf("body %q: %v", rec.body, err)
	}
	if sent["name"] != "x" || sent["count"] != float64(2) {
		t.Fatalf("sent = %v", sent)
	}
}

func TestDoUserAgentCarriesVersion(t *testing.T) {
	old := Version
	Version = "1.2.3"
	t.Cleanup(func() { Version = old })
	srv, rec := envelopeServer(t, http.StatusOK, `{"ok": true, "result": null}`)
	c := client(t, srv, session.Config{})
	if err := c.Do(context.Background(), http.MethodGet, "/api/v1/whoami", nil, nil); err != nil {
		t.Fatal(err)
	}
	if rec.ua != "clued/1.2.3" {
		t.Fatalf("User-Agent = %q", rec.ua)
	}
}

func TestDoWithoutBodyOrOutput(t *testing.T) {
	srv, rec := envelopeServer(t, http.StatusOK, `{"ok": true, "result": {"anything": 1}}`)
	c := client(t, srv, session.Config{})
	if err := c.Do(context.Background(), http.MethodGet, "api/v1/whoami", nil, nil); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/api/v1/whoami" {
		t.Fatalf("path = %q, want a leading slash added", rec.path)
	}
	if len(rec.body) != 0 {
		t.Fatalf("body = %q, want none", rec.body)
	}
	if rec.ctype != "" {
		t.Fatalf("Content-Type = %q, want none without a body", rec.ctype)
	}
}

func TestDoNoTokenSendsNoAuthorization(t *testing.T) {
	srv, rec := envelopeServer(t, http.StatusOK, `{"ok": true}`)
	c, err := New(session.Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), http.MethodGet, "/health", nil, nil); err != nil {
		t.Fatal(err)
	}
	if rec.auth != "" {
		t.Fatalf("Authorization = %q, want none", rec.auth)
	}
}

func TestDoKeepsBasePath(t *testing.T) {
	srv, rec := envelopeServer(t, http.StatusOK, `{"ok": true}`)
	c, err := New(session.Config{ServerURL: srv.URL + "/clued/", ServerToken: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Do(context.Background(), http.MethodGet, "/api/v1/whoami", nil, nil); err != nil {
		t.Fatal(err)
	}
	if rec.path != "/clued/api/v1/whoami" {
		t.Fatalf("path = %q", rec.path)
	}
}

func TestDoNullResultLeavesOutputUntouched(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusOK, `{"ok": true, "result": null}`)
	c := client(t, srv, session.Config{})
	out := map[string]any{"keep": true}
	if err := c.Do(context.Background(), http.MethodGet, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out["keep"] != true {
		t.Fatalf("out = %v, want untouched", out)
	}
}

func TestDoNoContentIsSuccess(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusNoContent, ``)
	c := client(t, srv, session.Config{})
	var out map[string]any
	if err := c.Do(context.Background(), http.MethodDelete, "/x", nil, &out); err != nil {
		t.Fatal(err)
	}
}

func TestDoDecodesResultIntoSlices(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusCreated, `{"ok": true, "result": ["a", "b"]}`)
	c := client(t, srv, session.Config{})
	var out []string
	if err := c.Do(context.Background(), http.MethodPost, "/x", struct{}{}, &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != "a" || out[1] != "b" {
		t.Fatalf("out = %v", out)
	}
}

func TestDoErrorMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		sentinel error  // expected via errors.Is, when non-nil
		notFound bool   // expected IsNotFound
		kind     string // expected *Error.Kind, when sentinel is nil
		msg      string // substring expected in the error text
		problems int
	}{
		{name: "401 envelope", status: 401, body: `{"ok": false, "error": {"kind": "unauthorized", "message": "bad token"}}`, sentinel: ErrUnauthorized, msg: "bad token"},
		{name: "401 bare", status: 401, body: `Unauthorized`, sentinel: ErrUnauthorized},
		{name: "403 envelope", status: 403, body: `{"ok": false, "error": {"kind": "forbidden", "message": "project not allowed"}}`, sentinel: ErrForbidden, msg: "project not allowed"},
		{name: "413 envelope", status: 413, body: `{"ok": false, "error": {"kind": "too_large", "message": "body too large"}}`, sentinel: ErrTooLarge},
		{name: "413 bare", status: 413, body: `request entity too large`, sentinel: ErrTooLarge},
		{name: "not_found kind", status: 404, body: `{"ok": false, "error": {"kind": "not_found", "message": "no such session"}}`, sentinel: ErrNotFound, notFound: true, msg: "no such session"},
		{name: "NotFound kind (service api)", status: 404, body: `{"ok": false, "error": {"kind": "NotFound", "message": "plan 0001-abc not found"}}`, sentinel: ErrNotFound, notFound: true},
		{name: "not_found kind with 200", status: 200, body: `{"ok": false, "error": {"kind": "not_found", "message": "gone"}}`, sentinel: ErrNotFound, notFound: true},
		{name: "invalid with problems", status: 400, body: `{"ok": false, "error": {"kind": "invalid", "message": "bad input", "problems": ["a", "b"]}}`, kind: "invalid", msg: "bad input", problems: 2},
		{name: "store error", status: 502, body: `{"ok": false, "error": {"kind": "store", "message": "mongo down"}}`, kind: "store", msg: "mongo down"},
		{name: "ok false without error object", status: 500, body: `{"ok": false}`, kind: "", msg: "500"},
		{name: "non-json 502 from a proxy", status: 502, body: `<html>Bad Gateway</html>`, kind: "http", msg: "Bad Gateway"},
		{name: "non-envelope 404", status: 404, body: `404 page not found`, kind: "http", msg: "404 page not found"},
		{name: "ok true with error status", status: 500, body: `{"ok": true, "result": 1}`, kind: "http", msg: "500"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := envelopeServer(t, tc.status, tc.body)
			c := client(t, srv, session.Config{})
			var out any
			err := c.Do(context.Background(), http.MethodGet, "/api/v1/x", nil, &out)
			if err == nil {
				t.Fatal("Do succeeded, want an error")
			}
			if IsNotFound(err) != tc.notFound {
				t.Fatalf("IsNotFound = %v, want %v (err %v)", IsNotFound(err), tc.notFound, err)
			}
			if tc.msg != "" && !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %q, want it to mention %q", err, tc.msg)
			}
			if tc.sentinel != nil {
				if !errors.Is(err, tc.sentinel) {
					t.Fatalf("err = %v, want %v", err, tc.sentinel)
				}
				for _, other := range []error{ErrUnauthorized, ErrForbidden, ErrTooLarge, ErrNotFound, ErrInsecureURL} {
					if other != tc.sentinel && errors.Is(err, other) {
						t.Fatalf("err = %v also matches %v", err, other)
					}
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("err = %T %v, want *Error", err, err)
			}
			if e.Status != tc.status {
				t.Fatalf("Status = %d, want %d", e.Status, tc.status)
			}
			if e.Kind != tc.kind {
				t.Fatalf("Kind = %q, want %q", e.Kind, tc.kind)
			}
			if len(e.Problems) != tc.problems {
				t.Fatalf("Problems = %v, want %d", e.Problems, tc.problems)
			}
			for _, s := range []error{ErrUnauthorized, ErrForbidden, ErrTooLarge, ErrNotFound} {
				if errors.Is(err, s) {
					t.Fatalf("*Error must not match sentinel %v", s)
				}
			}
		})
	}
}

func TestIsNotFoundOnOtherErrors(t *testing.T) {
	if IsNotFound(nil) {
		t.Fatal("IsNotFound(nil)")
	}
	if IsNotFound(errors.New("x")) {
		t.Fatal("IsNotFound(other)")
	}
	if !IsNotFound(fmt.Errorf("wrapped: %w", ErrNotFound)) {
		t.Fatal("IsNotFound(wrapped ErrNotFound) = false")
	}
}

func TestDoTransportErrorIsURLError(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusOK, `{"ok": true}`)
	c := client(t, srv, session.Config{})
	srv.Close()
	err := c.Do(context.Background(), http.MethodGet, "/x", nil, nil)
	if err == nil {
		t.Fatal("Do succeeded against a closed server")
	}
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("err = %T %v, want *url.Error", err, err)
	}
	for _, s := range []error{ErrUnauthorized, ErrForbidden, ErrTooLarge, ErrNotFound} {
		if errors.Is(err, s) {
			t.Fatalf("transport error matches %v", s)
		}
	}
}

func TestDoPerCallTimeout(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
		io.WriteString(w, `{"ok": true}`)
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	c := client(t, srv, session.Config{})
	c.Timeout = 50 * time.Millisecond
	start := time.Now()
	err := c.Do(context.Background(), http.MethodGet, "/slow", nil, nil)
	if err == nil {
		t.Fatal("Do succeeded, want a timeout")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("Do took %v, the timeout did not apply", time.Since(start))
	}
	var ue *url.Error
	if !errors.As(err, &ue) {
		t.Fatalf("err = %T %v, want *url.Error", err, err)
	}
	if !ue.Timeout() && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestDoHonoursCallerContext(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusOK, `{"ok": true}`)
	c := client(t, srv, session.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Do(ctx, http.MethodGet, "/x", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDoRejectsUnmarshalableInput(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusOK, `{"ok": true}`)
	c := client(t, srv, session.Config{})
	if err := c.Do(context.Background(), http.MethodPost, "/x", make(chan int), nil); err == nil {
		t.Fatal("Do accepted an unmarshalable body")
	}
}

func TestDoMalformedResultIsAnError(t *testing.T) {
	srv, _ := envelopeServer(t, http.StatusOK, `{"ok": true, "result": {"n": "not a number"}}`)
	c := client(t, srv, session.Config{})
	var out struct {
		N int `json:"n"`
	}
	if err := c.Do(context.Background(), http.MethodGet, "/x", nil, &out); err == nil {
		t.Fatal("Do decoded a mismatched result without error")
	}
}

// writeServerCA writes the test server's certificate as PEM so it can serve as
// the private CA of a serverCaFile.
func writeServerCA(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	cert := srv.Certificate()
	if cert == nil {
		t.Fatal("test server has no certificate")
	}
	p := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTLSWithPrivateCA(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok": true, "result": {"name": "ci"}}`)
	}))
	t.Cleanup(srv.Close)
	caFile := writeServerCA(t, srv)

	t.Run("with serverCaFile", func(t *testing.T) {
		c, err := New(session.Config{ServerURL: srv.URL, ServerToken: "t", ServerCAFile: caFile})
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Name string `json:"name"`
		}
		if err := c.Do(context.Background(), http.MethodGet, "/api/v1/whoami", nil, &out); err != nil {
			t.Fatalf("Do over TLS with the CA: %v", err)
		}
		if out.Name != "ci" {
			t.Fatalf("out = %+v", out)
		}
	})

	t.Run("without serverCaFile", func(t *testing.T) {
		c, err := New(session.Config{ServerURL: srv.URL, ServerToken: "t"})
		if err != nil {
			t.Fatal(err)
		}
		err = c.Do(context.Background(), http.MethodGet, "/api/v1/whoami", nil, nil)
		if err == nil {
			t.Fatal("Do succeeded without trusting the private CA")
		}
		var ue *url.Error
		if !errors.As(err, &ue) {
			t.Fatalf("err = %T %v, want *url.Error", err, err)
		}
	})

	t.Run("serverInsecure does not skip verification", func(t *testing.T) {
		c, err := New(session.Config{ServerURL: srv.URL, ServerToken: "t", ServerInsecure: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Do(context.Background(), http.MethodGet, "/api/v1/whoami", nil, nil); err == nil {
			t.Fatal("serverInsecure must not disable certificate verification")
		}
	})
}

func TestNewRejectsBadCAFile(t *testing.T) {
	if _, err := New(session.Config{ServerURL: "https://clued.example.com", ServerCAFile: filepath.Join(t.TempDir(), "missing.pem")}); err == nil {
		t.Fatal("New accepted a missing CA file")
	}
	p := filepath.Join(t.TempDir(), "empty.pem")
	if err := os.WriteFile(p, []byte("not a certificate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(session.Config{ServerURL: "https://clued.example.com", ServerCAFile: p}); err == nil {
		t.Fatal("New accepted a CA file with no certificates")
	}
}
