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
	"sync"
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
	// ErrPrecondition is HTTP 412: an If-Match the server no longer
	// matches (the document changed since it was read).
	ErrPrecondition = errors.New("remote: precondition failed")
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

	// key and cache are the environment key and the token cache file of an
	// exchanging client (see exchange.go); mu guards token and expires.
	key     string
	cache   string
	mu      sync.Mutex
	expires time.Time

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
	c := &Client{
		baseURL: base,
		token:   cfg.ServerToken,
		http:    &http.Client{Transport: transport},
		Timeout: DefaultTimeout,
	}
	if cfg.ServerToken == "" && cfg.ServerKey != "" {
		c.key, c.cache = cfg.ServerKey, cfg.TokenCache
	}
	return c, nil
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

// Token is the bearer token sent with every request; for a client that
// exchanges an environment key it is the key until the first exchange,
// which keeps it usable as an identity (a cache key) before any request.
func (c *Client) Token() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token == "" {
		return c.key
	}
	return c.token
}

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
//
// A client built with an environment key and no token gets its bearer from
// the server's token exchange (see exchange.go) and, when a request made
// with an exchanged token is refused with 401, exchanges once more and
// retries it once.
func (c *Client) Do(ctx context.Context, method, path string, in, out any) error {
	return c.DoHeader(ctx, method, path, nil, in, out)
}

// DoHeader is Do with extra request headers (If-Match, X-Clued-Force, …);
// they never replace Authorization, User-Agent, Accept or Content-Type.
// A 412 wraps ErrPrecondition.
func (c *Client) DoHeader(ctx context.Context, method, path string, header http.Header, in, out any) error {
	var payload []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("remote: encoding %s %s body: %w", method, path, err)
		}
		payload = b
	}
	token, err := c.bearer(ctx)
	if err != nil {
		return err
	}
	err = c.send(ctx, method, path, header, payload, out, token)
	if c.key != "" && errors.Is(err, ErrUnauthorized) {
		token, rerr := c.renew(ctx, token)
		if rerr != nil {
			// The exchange says why (a revoked key, no exchange at all).
			return rerr
		}
		err = c.send(ctx, method, path, header, payload, out, token)
	}
	return err
}

// Response is one answer as the server sent it, for an adapter that maps
// statuses and envelope kinds to its own errors (Send).
type Response struct {
	// Status is the HTTP status code.
	Status int
	// Envelope reports whether the body was a server/httpapi envelope.
	Envelope bool
	// OK is the envelope's ok (false without an envelope).
	OK bool
	// Result is the envelope's result, raw.
	Result json.RawMessage
	// Kind, Message and Problems are the envelope's error; without an
	// envelope Message is the body as is (a proxy's error page).
	Kind     string
	Message  string
	Problems []string
}

// Send sends body as is with contentType ("" sends none) and returns the
// response without mapping its status: the error is only a request that got
// no answer (a transport failure wrapping the *url.Error, or a failed token
// exchange). Headers and the token exchange are as for DoHeader, including
// the one retry after a 401 with an exchanged token. Adapters whose routes
// take raw bytes, or that need the envelope's kind of a 403 or 413, use it.
func (c *Client) Send(ctx context.Context, method, path string, header http.Header, contentType string, body []byte) (*Response, error) {
	token, err := c.bearer(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.roundTrip(ctx, method, path, header, contentType, body, token)
	if err == nil && c.key != "" && resp.Status == http.StatusUnauthorized {
		token, rerr := c.renew(ctx, token)
		if rerr != nil {
			return nil, rerr
		}
		resp, err = c.roundTrip(ctx, method, path, header, contentType, body, token)
	}
	return resp, err
}

// roundTrip performs one request with token as the bearer ("" sends none)
// and reads the response; payload is sent with contentType when not nil.
func (c *Client) roundTrip(ctx context.Context, method, path string, header http.Header, contentType string, payload []byte, token string) (*Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/"+strings.TrimLeft(path, "/"), body)
	if err != nil {
		return nil, fmt.Errorf("remote: building %s %s: %w", method, path, err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "clued/"+Version)
	req.Header.Set("Accept", "application/json")
	if body != nil && contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("remote: %s %s: reading response: %w", method, path, &url.Error{Op: method, URL: req.URL.String(), Err: err})
	}

	out := &Response{Status: resp.StatusCode}
	var env envelope
	out.Envelope = len(bytes.TrimSpace(raw)) > 0 && json.Unmarshal(raw, &env) == nil
	// The message is the envelope's when there is one; a non-envelope body
	// (a proxy's error page, a bare status text) is used as is.
	switch {
	case out.Envelope:
		out.OK, out.Result = env.OK, env.Result
		if env.Error != nil {
			out.Kind, out.Message, out.Problems = env.Error.Kind, env.Error.Message, env.Error.Problems
		}
	default:
		out.Message = strings.TrimSpace(string(raw))
	}
	return out, nil
}

// send performs one request with token as the bearer ("" sends none).
func (c *Client) send(ctx context.Context, method, path string, header http.Header, payload []byte, out any, token string) error {
	resp, err := c.roundTrip(ctx, method, path, header, "application/json", payload, token)
	if err != nil {
		return err
	}
	hasEnvelope, message := resp.Envelope, resp.Message

	switch resp.Status {
	case http.StatusUnauthorized:
		return wrap(ErrUnauthorized, method, path, message)
	case http.StatusForbidden:
		return wrap(ErrForbidden, method, path, message)
	case http.StatusRequestEntityTooLarge:
		return wrap(ErrTooLarge, method, path, message)
	case http.StatusPreconditionFailed:
		return wrap(ErrPrecondition, method, path, message)
	}

	if hasEnvelope && !resp.OK {
		e := &Error{Status: resp.Status, Message: message, Kind: resp.Kind, Problems: resp.Problems}
		if isNotFoundKind(resp.Kind) {
			return wrap(ErrNotFound, method, path, message)
		}
		if e.Message == "" {
			e.Message = fmt.Sprintf("%s %s failed with HTTP %d", method, path, resp.Status)
		}
		return e
	}
	if resp.Status < 200 || resp.Status > 299 || (!hasEnvelope && resp.Status != http.StatusNoContent) {
		msg := message
		if msg == "" {
			msg = http.StatusText(resp.Status)
		}
		return &Error{Status: resp.Status, Kind: "http", Message: fmt.Sprintf("%s %s: HTTP %d: %s", method, path, resp.Status, msg)}
	}

	if out == nil || !hasEnvelope || len(resp.Result) == 0 || bytes.Equal(bytes.TrimSpace(resp.Result), []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(resp.Result, out); err != nil {
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
