// Package remotecode is the client side of the write-only code transport:
// a code.Store whose writes go to a clued server's server/codeapi routes
// under /api/v1/projects/{project}/code/ through the shared remote client
// (base URL, bearer token, CA file, the http:// rule). session/codesync
// mirrors a worktree through it.
//
// The transport serves no read, so the read side of code.Store (Worktrees,
// Tree, Changes, Blob, Usage, Events, SHA256ForGitOID) and the server-side
// writes (RemoveRef, AppendEvents) answer ErrUnsupported without a request.
//
// Errors wrap the code sentinels where the port defines one (400 invalid is
// code.ErrInvalid, 404 not_found code.ErrNotFound, 409 conflict
// code.ErrMissingBlob) and this package's typed errors the collector backs
// off on: ErrUnsupported (a server without the code transport, 404 on the
// route itself), ErrForbidden (403), ErrConflict (409), ErrTooLarge (413
// too_large) and ErrQuota (413 quota). 401 wraps remote.ErrUnauthorized.
// The detail of the server's answer is kept as a *remote.Error.
package remotecode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/server/codeapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
)

// Typed errors of the transport, matched with errors.Is.
var (
	// ErrUnsupported is a server without the code transport (the route
	// answers 404 or 405 rather than a code error), or a method of the port
	// the transport has no route for.
	ErrUnsupported = errors.New("remotecode: not supported by the server")
	// ErrForbidden is 403: the token may not use the project, or the
	// server refuses code writes for the account (no entitlement).
	ErrForbidden = errors.New("remotecode: forbidden")
	// ErrConflict is 409: a commit names a blob the server lacks; the
	// error also wraps code.ErrMissingBlob.
	ErrConflict = errors.New("remotecode: conflict")
	// ErrTooLarge is 413 too_large: a blob, a manifest, a change or a body
	// over the server's limits (see Capabilities).
	ErrTooLarge = errors.New("remotecode: too large")
	// ErrQuota is 413 quota: the account's code storage is full.
	ErrQuota = errors.New("remotecode: quota exceeded")
)

// Store is the code.Store of one project on a clued server. It is safe for
// concurrent use.
type Store struct {
	client  *remote.Client
	project string
	base    string
}

var _ code.Store = (*Store)(nil)

// New returns the store of project (a projects slug, e.g.
// github.com__owner__repo) over client; the server namespaces it by the
// token's principal.
func New(client *remote.Client, project string) (*Store, error) {
	if client == nil {
		return nil, errors.New("remotecode: a remote client is required")
	}
	if !projects.ValidSlug(project) {
		return nil, fmt.Errorf("remotecode: invalid project slug %q", project)
	}
	return &Store{
		client:  client,
		project: project,
		base:    codeapi.Prefix + "/" + url.PathEscape(project) + "/code/",
	}, nil
}

// Project is the slug the store writes to.
func (s *Store) Project() string { return s.project }

// Capabilities returns the server's code limits and whether the project is
// GitHub-backed (Capabilities.GitHub: send clean, pushed entries with
// their git_oid and no content, and upload no blob for them); a server
// that predates the flag reads as false. ErrUnsupported when the server
// has no code transport.
func (s *Store) Capabilities(ctx context.Context) (codeapi.Capabilities, error) {
	var out codeapi.Capabilities
	resp, err := s.client.Send(ctx, http.MethodGet, s.base+"capabilities", nil, "", nil)
	if err != nil {
		return out, err
	}
	if resp.Status == http.StatusNotFound {
		// The capabilities route exists on every server with the code
		// transport, so any 404 means there is none.
		return out, s.fail(http.MethodGet, "capabilities", resp, ErrUnsupported)
	}
	if err := s.result(http.MethodGet, "capabilities", resp, &out); err != nil {
		return out, err
	}
	return out, nil
}

// MissingBlobs returns the hashes in shas the server lacks, in the order
// given (POST manifest).
func (s *Store) MissingBlobs(ctx context.Context, shas []string) ([]string, error) {
	var out codeapi.ManifestResult
	in := codeapi.Manifest{SHAs: append([]string{}, shas...)}
	if err := s.doJSON(ctx, http.MethodPost, "manifest", in, &out); err != nil {
		return nil, err
	}
	return out.Missing, nil
}

// PutBlob uploads b's content as raw bytes (PUT blobs/{sha256}). The route
// carries no size, so a Size that differs from the content's length is
// refused here as code.ErrInvalid; the server verifies the hash.
func (s *Store) PutBlob(ctx context.Context, b code.Blob) error {
	if !code.ValidSHA256(b.SHA256) {
		return fmt.Errorf("%w: invalid sha256 %q", code.ErrInvalid, b.SHA256)
	}
	if b.Size != int64(len(b.Content)) {
		return fmt.Errorf("%w: blob %s: size %d differs from its %d bytes of content", code.ErrInvalid, b.SHA256, b.Size, len(b.Content))
	}
	content := b.Content
	if content == nil {
		content = []byte{}
	}
	return s.do(ctx, http.MethodPut, "blobs/"+b.SHA256, "application/octet-stream", content, nil)
}

// PutWorktree creates or updates the worktree (PUT worktrees/{id}), one
// request per ref since the route adds one ref at a time (one request with
// no ref when w.Refs is empty). Owner, Source, Sessions, Removed and
// UpdatedAt are the server's and not sent.
func (s *Store) PutWorktree(ctx context.Context, w code.Worktree) error {
	if !code.ValidWorktreeID(w.ID) {
		return fmt.Errorf("%w: invalid worktree id %q", code.ErrInvalid, w.ID)
	}
	body := codeapi.WorktreeBody{Root: w.Root, Host: w.Host, Origin: w.Origin, DefaultBranch: w.DefaultBranch}
	refs := w.Refs
	if len(refs) == 0 {
		refs = []string{""}
	}
	for _, ref := range refs {
		body.Ref = ref
		if err := s.doJSON(ctx, http.MethodPut, s.worktreePath(w.ID), body, nil); err != nil {
			return err
		}
	}
	return nil
}

// Commit sends c (POST worktrees/{id}/changes) and returns its Seq. Owner,
// Seq, At and each upsert's Session are the server's and not sent. JSON
// cannot carry invalid UTF-8 (it would arrive as U+FFFD, another path), so
// such a path is refused here as code.ErrInvalid.
func (s *Store) Commit(ctx context.Context, c code.Change) (int64, error) {
	if !code.ValidWorktreeID(c.Worktree) {
		return 0, fmt.Errorf("%w: invalid worktree id %q", code.ErrInvalid, c.Worktree)
	}
	body := codeapi.ChangeBody{
		Ref:       c.Ref,
		Head:      c.Head,
		Upstream:  c.Upstream,
		Ahead:     c.Ahead,
		SessionID: c.SessionID,
		Full:      c.Full,
		Upserts:   make([]codeapi.EntryBody, 0, len(c.Upserts)),
		Deletes:   make([]string, 0, len(c.Deletes)),
	}
	for _, e := range c.Upserts {
		if !utf8.ValidString(e.Path) {
			return 0, fmt.Errorf("%w: path %q is not valid UTF-8", code.ErrInvalid, e.Path)
		}
		body.Upserts = append(body.Upserts, codeapi.EntryBody{
			Path:     e.Path,
			SHA256:   e.SHA256,
			Size:     e.Size,
			Mode:     e.Mode,
			Lang:     e.Lang,
			GitOID:   e.GitOID,
			Dirty:    e.Dirty,
			Unpushed: e.Unpushed,
		})
	}
	for _, p := range c.Deletes {
		if !utf8.ValidString(p) {
			return 0, fmt.Errorf("%w: path %q is not valid UTF-8", code.ErrInvalid, p)
		}
		body.Deletes = append(body.Deletes, p)
	}
	var out codeapi.ChangeResult
	if err := s.doJSON(ctx, http.MethodPost, s.worktreePath(c.Worktree)+"/changes", body, &out); err != nil {
		return 0, err
	}
	return out.Seq, nil
}

// RemoveWorktree removes the worktree (DELETE worktrees/{id}).
func (s *Store) RemoveWorktree(ctx context.Context, id string) error {
	if !code.ValidWorktreeID(id) {
		return fmt.Errorf("%w: invalid worktree id %q", code.ErrInvalid, id)
	}
	return s.do(ctx, http.MethodDelete, s.worktreePath(id), "", nil, nil)
}

// TouchSession records the session's activity on the worktree (PUT
// worktrees/{id}/sessions/{session_id}).
func (s *Store) TouchSession(ctx context.Context, worktree, sessionID string, ended bool) error {
	if !code.ValidWorktreeID(worktree) {
		return fmt.Errorf("%w: invalid worktree id %q", code.ErrInvalid, worktree)
	}
	if sessionID == "" {
		return fmt.Errorf("%w: empty session id", code.ErrInvalid)
	}
	path := s.worktreePath(worktree) + "/sessions/" + url.PathEscape(sessionID)
	return s.doJSON(ctx, http.MethodPut, path, codeapi.SessionBody{Ended: ended}, nil)
}

// RemoveRef has no route: the GitHub sync calls it on the server-side store.
func (s *Store) RemoveRef(context.Context, string, string) error {
	return unsupported("RemoveRef")
}

// AppendEvents has no route: the GitHub sync calls it on the server-side
// store.
func (s *Store) AppendEvents(context.Context, []code.Event) error {
	return unsupported("AppendEvents")
}

// SHA256ForGitOID has no route: the GitHub sync calls it on the
// server-side store.
func (s *Store) SHA256ForGitOID(context.Context, []string) (map[string]string, error) {
	return nil, unsupported("SHA256ForGitOID")
}

// Worktrees has no route: the transport is write-only.
func (s *Store) Worktrees(context.Context) ([]code.Worktree, error) {
	return nil, unsupported("Worktrees")
}

// Tree has no route: the transport is write-only.
func (s *Store) Tree(context.Context, string, string) ([]code.Entry, error) {
	return nil, unsupported("Tree")
}

// Changes has no route: the transport is write-only.
func (s *Store) Changes(context.Context, int64, int) ([]code.Change, error) {
	return nil, unsupported("Changes")
}

// Blob has no route: the transport is write-only.
func (s *Store) Blob(context.Context, string) (code.Blob, error) {
	return code.Blob{}, unsupported("Blob")
}

// Usage has no route: the transport is write-only.
func (s *Store) Usage(context.Context) (int64, int64, error) {
	return 0, 0, unsupported("Usage")
}

// Events has no route: the transport is write-only.
func (s *Store) Events(context.Context, int64, int) ([]code.Event, error) {
	return nil, unsupported("Events")
}

func unsupported(method string) error {
	return fmt.Errorf("%w: the code transport has no route for %s (write-only, server-side)", ErrUnsupported, method)
}

func (s *Store) worktreePath(id string) string { return "worktrees/" + url.PathEscape(id) }

// doJSON sends in as JSON to the route rel (relative to the code prefix)
// and decodes the result into out.
func (s *Store) doJSON(ctx context.Context, method, rel string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("remotecode: encoding %s %s body: %w", method, rel, err)
	}
	return s.do(ctx, method, rel, "application/json", b, out)
}

// do sends body with contentType to rel and maps the answer.
func (s *Store) do(ctx context.Context, method, rel, contentType string, body []byte, out any) error {
	resp, err := s.client.Send(ctx, method, s.base+rel, nil, contentType, body)
	if err != nil {
		return err
	}
	return s.result(method, rel, resp, out)
}

// result decodes a successful answer into out, or maps a failure.
func (s *Store) result(method, rel string, resp *remote.Response, out any) error {
	if resp.Status >= 200 && resp.Status <= 299 && resp.Envelope && resp.OK {
		if out == nil || len(resp.Result) == 0 || string(resp.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("remotecode: %s %s: decoding result: %w", method, rel, err)
		}
		return nil
	}
	return s.fail(method, rel, resp, classify(resp)...)
}

// classify returns the sentinels a failed answer wraps.
func classify(resp *remote.Response) []error {
	switch resp.Status {
	case http.StatusUnauthorized:
		return []error{remote.ErrUnauthorized}
	case http.StatusForbidden:
		return []error{ErrForbidden, remote.ErrForbidden}
	case http.StatusRequestEntityTooLarge:
		if resp.Kind == codeapi.KindQuota {
			return []error{ErrQuota}
		}
		return []error{ErrTooLarge, remote.ErrTooLarge}
	case http.StatusConflict:
		return []error{ErrConflict, code.ErrMissingBlob}
	case http.StatusBadRequest:
		if resp.Kind == codeapi.KindInvalid {
			return []error{code.ErrInvalid}
		}
	case http.StatusNotFound:
		// codeapi answers an unknown worktree or project with not_found
		// and a message naming it; the catch-all of a server without the
		// transport (codeapi.NotFound) says "no route", and an older
		// server answers without the envelope at all.
		if resp.Envelope && resp.Kind == codeapi.KindNotFound && !strings.HasPrefix(resp.Message, "no route for ") {
			return []error{code.ErrNotFound, remote.ErrNotFound}
		}
		return []error{ErrUnsupported}
	case http.StatusMethodNotAllowed:
		return []error{ErrUnsupported}
	}
	return nil
}

// fail builds the error of a failed answer: the sentinels, then the
// server's detail as a *remote.Error.
func (s *Store) fail(method, rel string, resp *remote.Response, sentinels ...error) error {
	detail := &remote.Error{Status: resp.Status, Kind: resp.Kind, Message: resp.Message, Problems: resp.Problems}
	if !resp.Envelope {
		detail.Kind = "http"
	}
	if detail.Message == "" {
		detail.Message = http.StatusText(resp.Status)
	}
	detail.Message = fmt.Sprintf("%s %s: %s", method, s.base+rel, detail.Message)
	return &Error{sentinels: sentinels, detail: detail}
}

// Error is a failed answer of the code transport. It matches its sentinels
// with errors.Is and the server's detail with errors.As(*remote.Error).
type Error struct {
	sentinels []error
	detail    *remote.Error
}

func (e *Error) Error() string {
	if len(e.sentinels) == 0 {
		return e.detail.Error()
	}
	return e.sentinels[0].Error() + ": " + e.detail.Error()
}

// Unwrap returns the sentinels and the *remote.Error.
func (e *Error) Unwrap() []error {
	return append(append([]error{}, e.sentinels...), e.detail)
}

// Status is the HTTP status of the answer.
func (e *Error) Status() int { return e.detail.Status }

// Kind is the envelope's error kind ("http" without an envelope).
func (e *Error) Kind() string { return e.detail.Kind }
