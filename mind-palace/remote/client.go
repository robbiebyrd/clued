// Package remote is the HTTP client every remote adapter of mind-palace
// shares: one base URL, one bearer token, the TLS trust from serverCaFile, a
// per-call timeout and the scheme rule that refuses plain http:// off
// loopback. Do sends JSON and decodes the {ok, result} / {ok: false, error}
// envelope of server/httpapi into typed errors so the adapters above it can
// map them to their port sentinels.
package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Version is reported in the User-Agent header as clued/<Version>. Builds
// may set it with -ldflags "-X …/remote.Version=1.2.3".
var Version = "dev"

// DefaultTimeout bounds one Do call unless Client.Timeout is changed.
const DefaultTimeout = 10 * time.Second

// Sentinels for the outcomes callers branch on. Errors returned by Do wrap
// them, so test with errors.Is (or IsNotFound).
var (
	// ErrInsecureURL is returned by New for a plain http:// server URL whose
	// host is not loopback while serverInsecure is not set.
	ErrInsecureURL = errors.New("remote: plain http:// server URL is only allowed for localhost or with serverInsecure")
	// ErrUnauthorized is HTTP 401: the token is missing, unknown or revoked.
	ErrUnauthorized = errors.New("remote: unauthorized")
	// ErrForbidden is HTTP 403: the token may not use this project.
	ErrForbidden = errors.New("remote: forbidden")
	// ErrTooLarge is HTTP 413: the request body exceeds the server's limit.
	ErrTooLarge = errors.New("remote: request body too large")
	// ErrNotFound is an envelope error of kind not_found.
	ErrNotFound = errors.New("remote: not found")
)

// Error is any other failed response: an envelope with {ok: false} whose
// kind has no sentinel, or a non-envelope body such as a proxy's HTML error
// page (Kind "http").
type Error struct {
	Status   int
	Kind     string
	Message  string
	Problems []string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString("remote: ")
	if e.Message != "" {
		b.WriteString(e.Message)
	} else {
		b.WriteString("request failed")
	}
	b.WriteString(" (")
	if e.Kind != "" {
		b.WriteString(e.Kind)
		b.WriteString(", ")
	}
	fmt.Fprintf(&b, "HTTP %d)", e.Status)
	for _, p := range e.Problems {
		b.WriteString("; ")
		b.WriteString(p)
	}
	return b.String()
}

// IsNotFound reports whether err is, or wraps, ErrNotFound.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// Client talks to one clued server.
type Client struct {
	baseURL string
	token   string
	http    *http.Client

	// Timeout bounds each Do call, in addition to the caller's context.
	Timeout time.Duration
}

// New builds a client from cfg.ServerURL, cfg.ServerToken, cfg.ServerCAFile
// and cfg.ServerInsecure. https:// is always accepted; http:// only when the
// host is loopback (localhost, 127.0.0.0/8, ::1) or ServerInsecure is set,
// otherwise ErrInsecureURL. A ServerCAFile that cannot be read or holds no
// certificate is an error.
func New(cfg session.Config) (*Client, error) {
	base, err := checkURL(cfg.ServerURL, cfg.ServerInsecure)
	if err != nil {
		return nil, err
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("remote: http.DefaultTransport is not an *http.Transport")
	}
	transport = transport.Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.ServerCAFile != "" {
		pool, err := loadCA(cfg.ServerCAFile)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig.RootCAs = pool
	}
	return &Client{
		baseURL: base,
		token:   cfg.ServerToken,
		http:    &http.Client{Transport: transport},
		Timeout: DefaultTimeout,
	}, nil
}

// FromConfig returns the client for cfg when a server URL is configured and
// valid, and (nil, false) when the URL is empty or New fails. Callers that
// need the reason use FromConfigE.
func FromConfig(cfg session.Config) (*Client, bool) {
	c, err := FromConfigE(cfg)
	if err != nil || c == nil {
		return nil, false
	}
	return c, true
}

// FromConfigE is FromConfig with the error: (nil, nil) when no server URL is
// configured, (nil, err) when New fails, and the client otherwise.
func FromConfigE(cfg session.Config) (*Client, error) {
	if !cfg.Remote() {
		return nil, nil
	}
	return New(cfg)
}

// BaseURL is the server URL without a trailing slash.
func (c *Client) BaseURL() string { return c.baseURL }

// Token is the bearer token sent with every request.
func (c *Client) Token() string { return c.token }

// checkURL validates the server URL against the scheme rule and returns it
// without a trailing slash.
func checkURL(raw string, insecure bool) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("remote: server URL is empty")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("remote: invalid server URL %q: %w", raw, err)
	}
	if u.Host == "" || u.Hostname() == "" {
		return "", fmt.Errorf("remote: server URL %q has no host", raw)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !insecure && !isLoopback(u.Hostname()) {
			return "", fmt.Errorf("%w: %s", ErrInsecureURL, raw)
		}
	default:
		return "", fmt.Errorf("remote: server URL %q must use https:// (or http:// on localhost)", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

// isLoopback reports whether host names the local machine: localhost, an
// address in 127.0.0.0/8 or ::1.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// loadCA reads a PEM bundle into a pool that also holds the system roots, so
// a private CA is trusted in addition to public ones.
func loadCA(path string) (*x509.CertPool, error) {
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("remote: reading serverCaFile: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("remote: serverCaFile %s holds no PEM certificate", path)
	}
	return pool, nil
}

// envelope is the response shape of server/httpapi.
type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *envelopeError  `json:"error"`
}

type envelopeError struct {
	Kind     string   `json:"kind"`
	Message  string   `json:"message"`
	Problems []string `json:"problems"`
}

// Do sends method path to the server with in as the JSON body (none when
// nil) and decodes the envelope's result into out (ignored when nil or when
// the result is null). Requests carry Authorization: Bearer <token>,
// User-Agent: clued/<Version> and a per-call timeout of c.Timeout.
//
// Errors: 401 wraps ErrUnauthorized, 403 ErrForbidden, 413 ErrTooLarge, an
// envelope error of kind not_found wraps ErrNotFound, any other failure is a
// *Error, and a transport failure wraps the *url.Error so errors.As works.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("remote: encoding %s %s body: %w", method, path, err)
		}
		body = bytes.NewReader(b)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/"+strings.TrimLeft(path, "/"), body)
	if err != nil {
		return fmt.Errorf("remote: building %s %s: %w", method, path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("User-Agent", "clued/"+Version)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("remote: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("remote: %s %s: reading response: %w", method, path, &url.Error{Op: method, URL: req.URL.String(), Err: err})
	}

	var env envelope
	hasEnvelope := len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &env) == nil
	// The message is the envelope's when there is one; a non-envelope body
	// (a proxy's error page, a bare status text) is used as is.
	message := ""
	switch {
	case hasEnvelope && env.Error != nil:
		message = env.Error.Message
	case !hasEnvelope:
		message = strings.TrimSpace(string(raw))
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		return wrap(ErrUnauthorized, method, path, message)
	case http.StatusForbidden:
		return wrap(ErrForbidden, method, path, message)
	case http.StatusRequestEntityTooLarge:
		return wrap(ErrTooLarge, method, path, message)
	}

	if hasEnvelope && !env.OK {
		e := &Error{Status: resp.StatusCode, Message: message}
		if env.Error != nil {
			e.Kind = env.Error.Kind
			e.Problems = env.Error.Problems
			if isNotFoundKind(env.Error.Kind) {
				return wrap(ErrNotFound, method, path, message)
			}
		}
		if e.Message == "" {
			e.Message = fmt.Sprintf("%s %s failed with HTTP %d", method, path, resp.StatusCode)
		}
		return e
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 || (!hasEnvelope && resp.StatusCode != http.StatusNoContent) {
		msg := message
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		return &Error{Status: resp.StatusCode, Kind: "http", Message: fmt.Sprintf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, msg)}
	}

	if out == nil || !hasEnvelope || len(env.Result) == 0 || bytes.Equal(bytes.TrimSpace(env.Result), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("remote: %s %s: decoding result: %w", method, path, err)
	}
	return nil
}

func wrap(sentinel error, method, path, message string) error {
	if message == "" {
		return fmt.Errorf("%w: %s %s", sentinel, method, path)
	}
	return fmt.Errorf("%w: %s %s: %s", sentinel, method, path, message)
}

// isNotFoundKind accepts the envelope kind of the session and store routes
// (not_found) and of the plan and story service (NotFound).
func isNotFoundKind(kind string) bool {
	return strings.ToLower(strings.ReplaceAll(kind, "_", "")) == "notfound"
}
