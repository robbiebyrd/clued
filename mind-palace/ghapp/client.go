package ghapp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client defaults. The zero values of the corresponding Client fields select
// them.
const (
	// DefaultMaxRetries is how often a request is retried after a 5xx, a
	// transport error or a short rate-limit wait.
	DefaultMaxRetries = 3
	// DefaultMaxRetryWait is the longest wait the client sleeps through
	// in-process; a longer Retry-After or rate-limit reset is returned to the
	// caller as a RateLimitError so it can pause the installation instead.
	DefaultMaxRetryWait = 10 * time.Second
	// DefaultMaxPages bounds how many pages a list call follows.
	DefaultMaxPages = 100
	// MaxCompareFiles is the most files GitHub lists in a comparison; a
	// comparison with this many is reported as Truncated.
	MaxCompareFiles = 300
	// perPage is the page size every list call asks for (GitHub's maximum).
	perPage = 100
	// maxResponseBody bounds a successful response body (a recursive tree of
	// a very large repository, or a base64 blob near GitHub's 100 MB cap).
	maxResponseBody = 200 << 20
	// firstBackoff is the wait before the first retry of a 5xx; it doubles.
	firstBackoff = 500 * time.Millisecond
)

// Errors a Client call can be matched against with errors.Is. Every failed
// response is an *APIError; these classify it.
var (
	// ErrNotFound is a 404: the object, or a repository the installation
	// cannot see (GitHub answers 404 rather than 403 for those).
	ErrNotFound = errors.New("ghapp: not found")
	// ErrRateLimited is a primary or secondary rate limit (403 or 429 with
	// X-RateLimit-Remaining: 0 or Retry-After). The *APIError carries when to
	// try again.
	ErrRateLimited = errors.New("ghapp: rate limited")
	// ErrTooLarge is content GitHub will not return through the API (a diff
	// or file too large) or a response over the client's body limit.
	ErrTooLarge = errors.New("ghapp: too large")
	// ErrUnauthorized is a 401: the credential was refused.
	ErrUnauthorized = errors.New("ghapp: unauthorized")
)

// APIError is a GitHub REST call that failed. It never carries the
// credential or the response body beyond GitHub's message.
type APIError struct {
	Method  string
	Path    string // request path without the query
	Status  int
	Message string
	// RateLimited marks a primary or secondary rate limit; RetryAfter is how
	// long GitHub asked to wait (from Retry-After, or until the reset).
	RateLimited bool
	RetryAfter  time.Duration
	// Reset is when the primary rate limit window resets (zero if unknown).
	Reset time.Time
	// TooLarge marks content GitHub will not serve through the API.
	TooLarge bool
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	s := fmt.Sprintf("ghapp: %s %s: HTTP %d: %s", e.Method, e.Path, e.Status, msg)
	if e.RateLimited && e.RetryAfter > 0 {
		s += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	return s
}

// Is classifies the error as ErrNotFound, ErrRateLimited, ErrTooLarge or
// ErrUnauthorized.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrRateLimited:
		return e.RateLimited
	case ErrTooLarge:
		return e.TooLarge
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	}
	return false
}

// Temporary reports whether retrying later can succeed: a rate limit or a
// 5xx.
func (e *APIError) Temporary() bool { return e.RateLimited || e.Status >= 500 }

// RetryAfter returns how long to wait before retrying err, when err is (or
// wraps) a rate-limit *APIError.
func RetryAfter(err error) (time.Duration, bool) {
	var e *APIError
	if errors.As(err, &e) && e.RateLimited {
		return e.RetryAfter, true
	}
	return 0, false
}

// RateLimit is the primary rate-limit state of the last response.
type RateLimit struct {
	Limit     int
	Remaining int
	Used      int
	Reset     time.Time
	Resource  string
}

// Client is a small GitHub REST client for one credential: an installation
// token (App.InstallationClient) or the app JWT (App.AppClient). It follows
// pagination, records the rate-limit headers, retries 5xx and short waits,
// and returns *APIError for every failed response. It is safe for concurrent
// use.
type Client struct {
	// BaseURL is the REST API root (DefaultBaseURL when empty).
	BaseURL string
	// HTTPClient sends the requests (http.DefaultClient when nil).
	HTTPClient *http.Client
	// MaxRetries: 0 means DefaultMaxRetries, negative means none.
	MaxRetries int
	// MaxRetryWait: 0 means DefaultMaxRetryWait.
	MaxRetryWait time.Duration
	// MaxPages: 0 means DefaultMaxPages.
	MaxPages int
	// Sleep waits between retries (a context-aware timer when nil).
	Sleep func(ctx context.Context, d time.Duration) error
	// Now is the clock used for rate-limit resets (time.Now when nil).
	Now func() time.Time

	credential func(ctx context.Context) (string, error)
	refresh    func() // drops a cached credential after a 401

	mu   sync.Mutex
	rate RateLimit
}

// NewClient returns a Client that authenticates every request with the
// bearer credential returns (any GitHub token).
func NewClient(baseURL string, httpClient *http.Client, credential func(ctx context.Context) (string, error)) *Client {
	return &Client{BaseURL: baseURL, HTTPClient: httpClient, credential: credential}
}

// InstallationClient returns a Client acting as the installation, with the
// app's base URL and HTTP client. It buys (or reuses) the installation token
// up front, so a removed installation fails here with ErrInstallationNotFound;
// later calls refresh the token from the app's cache as it nears expiry, and
// a 401 drops it and retries once.
func (a *App) InstallationClient(ctx context.Context, installationID int64) (*Client, error) {
	if _, err := a.InstallationToken(ctx, installationID); err != nil {
		return nil, err
	}
	return &Client{
		BaseURL:    a.baseURL(),
		HTTPClient: a.HTTPClient,
		Now:        a.Now,
		credential: func(ctx context.Context) (string, error) {
			tok, err := a.InstallationToken(ctx, installationID)
			if err != nil {
				return "", err
			}
			return tok.Value(), nil
		},
		refresh: func() { a.Invalidate(installationID) },
	}, nil
}

// AppClient returns a Client acting as the app itself (a fresh JWT per
// request), for the /app endpoints: Installation, Installations.
func (a *App) AppClient() *Client {
	return &Client{
		BaseURL:    a.baseURL(),
		HTTPClient: a.HTTPClient,
		Now:        a.Now,
		credential: func(context.Context) (string, error) { return a.JWT() },
	}
}

// RateLimit returns the rate-limit state of the last response that carried
// the headers.
func (c *Client) RateLimit() RateLimit {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rate
}

// Installation is GET /app/installations/{id} (an app client).
func (c *Client) Installation(ctx context.Context, installationID int64) (*Installation, error) {
	var out Installation
	if err := c.get(ctx, "/app/installations/"+strconv.FormatInt(installationID, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Installations lists the app's installations, every page (an app client).
func (c *Client) Installations(ctx context.Context) ([]Installation, error) {
	var out []Installation
	err := c.list(ctx, "/app/installations", nil, func(body []byte) error {
		var page []Installation
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// InstallationRepositories lists the repositories the installation can access
// (GET /installation/repositories), every page.
func (c *Client) InstallationRepositories(ctx context.Context) ([]Repository, error) {
	var out []Repository
	err := c.list(ctx, "/installation/repositories", nil, func(body []byte) error {
		var page struct {
			Repositories []Repository `json:"repositories"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		out = append(out, page.Repositories...)
		return nil
	})
	return out, err
}

// Repository is GET /repos/{owner}/{repo}.
func (c *Client) Repository(ctx context.Context, owner, repo string) (*Repository, error) {
	var out Repository
	if err := c.get(ctx, repoPath(owner, repo), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RepositoryByID is GET /repositories/{id}: the repository under its stable
// numeric id, which survives renames and transfers.
func (c *Client) RepositoryByID(ctx context.Context, id int64) (*Repository, error) {
	var out Repository
	if err := c.get(ctx, "/repositories/"+strconv.FormatInt(id, 10), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Branch is a branch and the commit at its tip.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected,omitempty"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// Branches lists every branch of the repository.
func (c *Client) Branches(ctx context.Context, owner, repo string) ([]Branch, error) {
	var out []Branch
	err := c.list(ctx, repoPath(owner, repo)+"/branches", nil, func(body []byte) error {
		var page []Branch
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// Branch is GET /repos/{owner}/{repo}/branches/{branch}.
func (c *Client) Branch(ctx context.Context, owner, repo, branch string) (*Branch, error) {
	var out Branch
	if err := c.get(ctx, repoPath(owner, repo)+"/branches/"+escapeRef(branch), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// OpenPulls lists the repository's open pull requests, every page.
func (c *Client) OpenPulls(ctx context.Context, owner, repo string) ([]PullRequest, error) {
	var out []PullRequest
	q := url.Values{"state": {"open"}}
	err := c.list(ctx, repoPath(owner, repo)+"/pulls", q, func(body []byte) error {
		var page []PullRequest
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		out = append(out, page...)
		return nil
	})
	return out, err
}

// File statuses in a Comparison.
const (
	FileAdded     = "added"
	FileRemoved   = "removed"
	FileModified  = "modified"
	FileRenamed   = "renamed"
	FileCopied    = "copied"
	FileChanged   = "changed"
	FileUnchanged = "unchanged"
)

// CompareFile is one changed file of a comparison. SHA is the file's blob id
// at head (for a removed file, the blob it had); PreviousFilename is set for
// a rename.
type CompareFile struct {
	SHA              string `json:"sha"`
	Filename         string `json:"filename"`
	Status           string `json:"status"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Additions        int    `json:"additions,omitempty"`
	Deletions        int    `json:"deletions,omitempty"`
	Changes          int    `json:"changes,omitempty"`
}

// CompareCommit is one commit of a comparison.
type CompareCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message,omitempty"`
		Tree    struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	} `json:"commit"`
	Author *User `json:"author,omitempty"`
}

// Comparison is GET compare/{base}...{head}. Status is "ahead", "behind",
// "identical" or "diverged". Truncated is set when GitHub's 300-file cap was
// reached, so the file list may be incomplete and the caller should import
// the head's full tree instead.
type Comparison struct {
	Status       string          `json:"status"`
	AheadBy      int             `json:"ahead_by"`
	BehindBy     int             `json:"behind_by"`
	TotalCommits int             `json:"total_commits"`
	BaseCommit   CompareCommit   `json:"base_commit"`
	MergeBase    CompareCommit   `json:"merge_base_commit"`
	Commits      []CompareCommit `json:"commits"`
	Files        []CompareFile   `json:"files"`
	Truncated    bool            `json:"-"`
}

// Compare is GET /repos/{owner}/{repo}/compare/{base}...{head}. A diff
// GitHub refuses as too large is an *APIError matching ErrTooLarge.
func (c *Client) Compare(ctx context.Context, owner, repo, base, head string) (*Comparison, error) {
	var out Comparison
	p := repoPath(owner, repo) + "/compare/" + escapeRef(base) + "..." + escapeRef(head)
	if err := c.get(ctx, p, nil, &out); err != nil {
		return nil, err
	}
	out.Truncated = len(out.Files) >= MaxCompareFiles
	return &out, nil
}

// Tree entry types and modes.
const (
	TypeBlob   = "blob"
	TypeTree   = "tree"
	TypeCommit = "commit" // a submodule

	ModeFile       = "100644"
	ModeExecutable = "100755"
	ModeSymlink    = "120000"
	ModeTree       = "040000"
	ModeSubmodule  = "160000"
)

// TreeEntry is one entry of a git tree. Size is set for blobs only.
type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size int64  `json:"size,omitempty"`
}

// Tree is a git tree. Truncated is GitHub's flag that a recursive listing hit
// its limit: the caller then lists the subtrees (non-recursively, or
// recursively one at a time) to see everything.
type Tree struct {
	SHA       string      `json:"sha"`
	Truncated bool        `json:"truncated"`
	Entries   []TreeEntry `json:"tree"`
}

// Tree is GET /repos/{owner}/{repo}/git/trees/{sha} (sha may be a tree, a
// commit or a branch name), with ?recursive=1 when recursive.
func (c *Client) Tree(ctx context.Context, owner, repo, sha string, recursive bool) (*Tree, error) {
	var q url.Values
	if recursive {
		q = url.Values{"recursive": {"1"}}
	}
	var out Tree
	if err := c.get(ctx, repoPath(owner, repo)+"/git/trees/"+escapeRef(sha), q, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Blob is a git blob's content.
type Blob struct {
	SHA     string
	Size    int64
	Content []byte
}

// Blob is GET /repos/{owner}/{repo}/git/blobs/{sha}, decoded.
func (c *Client) Blob(ctx context.Context, owner, repo, sha string) (*Blob, error) {
	var raw struct {
		SHA      string `json:"sha"`
		Size     int64  `json:"size"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	p := repoPath(owner, repo) + "/git/blobs/" + url.PathEscape(sha)
	if err := c.get(ctx, p, nil, &raw); err != nil {
		return nil, err
	}
	content, err := decodeContent(raw.Encoding, raw.Content)
	if err != nil {
		return nil, fmt.Errorf("ghapp: blob %s: %w", sha, err)
	}
	return &Blob{SHA: raw.SHA, Size: raw.Size, Content: content}, nil
}

// ContentEntry is one entry of a directory listing from Contents.
type ContentEntry struct {
	Type string `json:"type"` // file, dir, symlink, submodule
	Name string `json:"name"`
	Path string `json:"path"`
	SHA  string `json:"sha"`
	Size int64  `json:"size"`
}

// Content is what Contents returns: a file (Type "file", Content decoded) or
// a directory (Type "dir", Entries listed).
type Content struct {
	Type    string
	Name    string
	Path    string
	SHA     string
	Size    int64
	Content []byte
	Entries []ContentEntry
}

// Contents is GET /repos/{owner}/{repo}/contents/{path}?ref=…; ref empty
// means the default branch. A file GitHub will not inline (over 1 MB, encoding
// "none") is an *APIError matching ErrTooLarge: fetch it with Blob by its SHA.
func (c *Client) Contents(ctx context.Context, owner, repo, path, ref string) (*Content, error) {
	var q url.Values
	if ref != "" {
		q = url.Values{"ref": {ref}}
	}
	p := repoPath(owner, repo) + "/contents"
	if path = strings.Trim(path, "/"); path != "" {
		p += "/" + escapeRef(path)
	}
	body, err := c.do(ctx, http.MethodGet, c.url(p, q))
	if err != nil {
		return nil, err
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var entries []ContentEntry
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("ghapp: contents %s: decode: %w", path, err)
		}
		return &Content{Type: "dir", Path: path, Entries: entries}, nil
	}
	var raw struct {
		ContentEntry
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		return nil, fmt.Errorf("ghapp: contents %s: decode: %w", path, err)
	}
	out := &Content{Type: raw.Type, Name: raw.Name, Path: raw.Path, SHA: raw.SHA, Size: raw.Size}
	if raw.Type == "file" {
		if raw.Encoding == "none" {
			return nil, &APIError{Method: http.MethodGet, Path: p, Status: http.StatusOK, Message: "file too large for the contents API", TooLarge: true}
		}
		if out.Content, err = decodeContent(raw.Encoding, raw.Content); err != nil {
			return nil, fmt.Errorf("ghapp: contents %s: %w", path, err)
		}
	}
	return out, nil
}

func decodeContent(encoding, content string) ([]byte, error) {
	switch encoding {
	case "base64":
		// GitHub wraps base64 at 60 columns.
		clean := strings.NewReplacer("\n", "", "\r", "").Replace(content)
		return base64.StdEncoding.DecodeString(clean)
	case "utf-8", "":
		return []byte(content), nil
	default:
		return nil, fmt.Errorf("unsupported encoding %q", encoding)
	}
}

// repoPath is /repos/{owner}/{repo} with both escaped.
func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// escapeRef escapes each slash-separated segment of a ref or path, keeping
// the slashes (branch names and file paths contain them).
func escapeRef(ref string) string {
	parts := strings.Split(ref, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) url(path string, q url.Values) string {
	u := c.baseURL() + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	body, err := c.do(ctx, http.MethodGet, c.url(path, q))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("ghapp: GET %s: decode: %w", path, err)
	}
	return nil
}

// list follows a paginated listing from its first page through the Link
// header's rel="next", calling decode on each page's body. Next links must
// stay on the base URL's scheme and host, so the credential never leaves it.
func (c *Client) list(ctx context.Context, path string, q url.Values, decode func([]byte) error) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("per_page", strconv.Itoa(perPage))
	next := c.url(path, q)
	maxPages := c.MaxPages
	if maxPages <= 0 {
		maxPages = DefaultMaxPages
	}
	base, err := url.Parse(c.baseURL())
	if err != nil {
		return fmt.Errorf("ghapp: base URL: %w", err)
	}
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return fmt.Errorf("ghapp: GET %s: more than %d pages", path, maxPages)
		}
		body, header, err := c.send(ctx, http.MethodGet, next)
		if err != nil {
			return err
		}
		if err := decode(body); err != nil {
			return fmt.Errorf("ghapp: GET %s: decode: %w", path, err)
		}
		next = ""
		if link := nextLink(header.Get("Link")); link != "" {
			u, err := url.Parse(link)
			if err != nil || u.Scheme != base.Scheme || u.Host != base.Host {
				return fmt.Errorf("ghapp: GET %s: next page link leaves %s", path, base.Host)
			}
			next = u.String()
		}
	}
	return nil
}

// nextLink extracts the rel="next" URL of an RFC 8288 Link header.
func nextLink(header string) string {
	for part := range strings.SplitSeq(header, ",") {
		segs := strings.Split(part, ";")
		if len(segs) < 2 {
			continue
		}
		target := strings.TrimSpace(segs[0])
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		for _, p := range segs[1:] {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.TrimSpace(k) == "rel" && strings.Trim(strings.TrimSpace(v), `"`) == "next" {
				return target[1 : len(target)-1]
			}
		}
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, rawURL string) ([]byte, error) {
	body, _, err := c.send(ctx, method, rawURL)
	return body, err
}

// send performs one logical request: retries 5xx, transport errors and
// rate-limit waits up to MaxRetryWait, refreshes the credential once after a
// 401, and returns the body of a 2xx.
func (c *Client) send(ctx context.Context, method, rawURL string) ([]byte, http.Header, error) {
	maxRetries := c.MaxRetries
	switch {
	case maxRetries == 0:
		maxRetries = DefaultMaxRetries
	case maxRetries < 0:
		maxRetries = 0
	}
	maxWait := c.MaxRetryWait
	if maxWait <= 0 {
		maxWait = DefaultMaxRetryWait
	}
	refreshed := false
	backoff := firstBackoff
	for attempt := 0; ; attempt++ {
		body, header, err := c.attempt(ctx, method, rawURL)
		if err == nil {
			return body, header, nil
		}
		if ctx.Err() != nil {
			return nil, nil, err
		}
		var apiErr *APIError
		isAPI := errors.As(err, &apiErr)
		var wait time.Duration
		switch {
		case isAPI && apiErr.Status == http.StatusUnauthorized && c.refresh != nil && !refreshed:
			refreshed = true
			c.refresh()
			attempt-- // a refreshed credential is not a retry
			continue
		case isAPI && apiErr.RateLimited:
			if apiErr.RetryAfter > maxWait {
				return nil, nil, err
			}
			wait = apiErr.RetryAfter
		case isAPI && apiErr.Status >= 500:
			wait = min(backoff, maxWait)
			backoff *= 2
		case !isAPI && isTransport(err):
			wait = min(backoff, maxWait)
			backoff *= 2
		default:
			return nil, nil, err
		}
		if attempt >= maxRetries {
			return nil, nil, err
		}
		if err := c.sleep(ctx, wait); err != nil {
			return nil, nil, err
		}
	}
}

// transportError marks a failure to get any response (connection refused,
// reset, timeout) as retryable.
type transportError struct{ err error }

func (e *transportError) Error() string { return e.err.Error() }
func (e *transportError) Unwrap() error { return e.err }

func isTransport(err error) bool {
	var t *transportError
	return errors.As(err, &t)
}

func (c *Client) attempt(ctx context.Context, method, rawURL string) ([]byte, http.Header, error) {
	if c.credential == nil {
		return nil, nil, errors.New("ghapp: client has no credential")
	}
	cred, err := c.credential(ctx)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("ghapp: %s: %w", method, err)
	}
	SetHeaders(req, cred)
	path := req.URL.Path
	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		// url.Error embeds the URL, which carries no credential.
		return nil, nil, &transportError{fmt.Errorf("ghapp: %s %s: %w", method, path, err)}
	}
	defer func() { _ = resp.Body.Close() }()
	c.recordRate(resp.Header)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
		if err != nil {
			return nil, nil, &transportError{fmt.Errorf("ghapp: %s %s: read response: %w", method, path, err)}
		}
		if len(body) > maxResponseBody {
			return nil, nil, &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: "response over the client's body limit", TooLarge: true}
		}
		return body, resp.Header, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return nil, nil, c.apiError(method, path, resp, body)
}

func (c *Client) apiError(method, path string, resp *http.Response, body []byte) *APIError {
	var msg struct {
		Message string `json:"message"`
		Errors  []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	_ = json.Unmarshal(body, &msg)
	e := &APIError{Method: method, Path: path, Status: resp.StatusCode, Message: msg.Message}
	lower := strings.ToLower(msg.Message)
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		retryAfter, hasRetryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
		remaining := resp.Header.Get("X-RateLimit-Remaining")
		switch {
		case hasRetryAfter:
			e.RateLimited, e.RetryAfter = true, retryAfter
		case remaining == "0":
			e.RateLimited = true
		case resp.StatusCode == http.StatusTooManyRequests, strings.Contains(lower, "rate limit"):
			// A secondary limit without Retry-After: GitHub asks for at
			// least a minute.
			e.RateLimited, e.RetryAfter = true, time.Minute
		}
		if reset := parseUnix(resp.Header.Get("X-RateLimit-Reset")); !reset.IsZero() {
			e.Reset = reset
			if e.RateLimited && !hasRetryAfter && remaining == "0" {
				e.RetryAfter = max(reset.Sub(c.now()), 0)
			}
		}
	}
	tooLarge := strings.Contains(lower, "too large") || strings.Contains(lower, "too_large")
	for _, er := range msg.Errors {
		if er.Code == "too_large" {
			tooLarge = true
		}
	}
	if !e.RateLimited && tooLarge {
		e.TooLarge = true
	}
	return e
}

func (c *Client) recordRate(h http.Header) {
	if h.Get("X-RateLimit-Limit") == "" && h.Get("X-RateLimit-Remaining") == "" {
		return
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	rl := RateLimit{
		Limit:     atoi(h.Get("X-RateLimit-Limit")),
		Remaining: atoi(h.Get("X-RateLimit-Remaining")),
		Used:      atoi(h.Get("X-RateLimit-Used")),
		Reset:     parseUnix(h.Get("X-RateLimit-Reset")),
		Resource:  h.Get("X-RateLimit-Resource"),
	}
	c.mu.Lock()
	c.rate = rl
	c.mu.Unlock()
}

// parseRetryAfter reads Retry-After as seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return time.Duration(n) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}

func parseUnix(v string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.Unix(n, 0).UTC()
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
