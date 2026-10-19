// Package codeapi is the write-only transport of collected code: it serves
// the write side of the code.Store port over HTTP, one store per namespaced
// project, under /api/v1/projects/{project}/code/. A Claude Code client's
// session/codesync (through code/remotecode) mirrors its worktrees here; no
// route returns code, paths or trees, so a leaked client token can write
// code into its account but read none back.
//
// Routes (every response uses the httpapi envelope):
//
//	POST   /api/v1/projects/{project}/code/manifest                          which blobs the server lacks
//	PUT    /api/v1/projects/{project}/code/blobs/{sha256}                    upload one blob (raw bytes)
//	PUT    /api/v1/projects/{project}/code/worktrees/{id}                    create or update a worktree
//	POST   /api/v1/projects/{project}/code/worktrees/{id}/changes            commit a change set
//	PUT    /api/v1/projects/{project}/code/worktrees/{id}/sessions/{session} record session activity
//	DELETE /api/v1/projects/{project}/code/worktrees/{id}                    remove a worktree
//	GET    /api/v1/projects/{project}/code/capabilities                      the limits and whether GitHub supplies the code
//
// Each request needs an account (Options.Account, 401 unauthorized
// otherwise), validates the project slug (400 invalid), asks Options.Allow
// (403 forbidden), maps the slug to its namespaced key (Options.Namespace)
// and only then opens the key's store (Options.Open). A failure of either
// answers as storeapi does: a *httpapi.StatusError with its own status and
// kind, one wrapping projects.ErrInvalidProject 400 invalid, one wrapping
// auth.ErrProjectForbidden 403 forbidden, one wrapping code.ErrNotFound 404
// not_found, anything else 502 store.
//
// The handler stamps what the client may not set: the owner of worktrees
// and changes is the request's account, and each upsert's session is the
// change's session_id; a body that carries "owner", or an entry that
// carries "session", is 400 invalid, as is any unknown field. A worktree
// held by another account answers 404 not_found, exactly as an unknown one,
// so a workspace member can neither overwrite nor learn of another member's
// checkout. Paths, hashes, git object ids, refs, worktree and session ids
// are validated here at the point of use (code.ValidPath and friends)
// before the store sees them; store errors map to code.ErrInvalid 400
// invalid, code.ErrNotFound 404 not_found, code.ErrMissingBlob 409 conflict.
//
// An upsert without content (sha256 "") records that the file exists
// without its bytes: plan 0010 sends binaries, symlinks, files over the
// size cap and blobs the server refused that way. On a GitHub-backed
// project (Options.GitHubBacked) a clean, pushed entry is also sent
// without content, and the indexer takes its content from the
// GitHub-backed worktree by git_oid; capabilities reports "github": true
// there so a client knows to skip those uploads.
//
// Options.BeforeWrite, when set, runs before every write (every route but
// capabilities) once the request is valid; it refuses one by returning an
// error wrapping ErrRefused (403 forbidden), ErrQuota (413 quota) or a
// *httpapi.StatusError (its own status), and any other error is 403
// forbidden too (fail closed).
package codeapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/robbiebyrd/clued/mind-palace/code"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
)

// Prefix is the root of the project-scoped routes (storeapi.Prefix); the
// code routes live under Prefix/{project}/code/.
const Prefix = "/api/v1/projects"

// Route patterns, without their method, as Mount registers them. Pattern is
// the catch-all of the code prefix a composer registers next to Mount (see
// NotFound), so an unknown code route answers 404 in the envelope.
const (
	Pattern             = Prefix + "/{project}/code/"
	PathManifest        = Prefix + "/{project}/code/manifest"
	PathBlob            = Prefix + "/{project}/code/blobs/{sha256}"
	PathWorktree        = Prefix + "/{project}/code/worktrees/{id}"
	PathWorktreeChanges = Prefix + "/{project}/code/worktrees/{id}/changes"
	PathWorktreeSession = Prefix + "/{project}/code/worktrees/{id}/sessions/{session_id}"
	PathCapabilities    = Prefix + "/{project}/code/capabilities"
)

// Defaults of the limits Options leaves at zero.
const (
	// DefaultMaxFileBytes caps one blob upload (the client's default
	// codeSyncMaxFileBytes; a client never sends more than capabilities
	// says).
	DefaultMaxFileBytes = 1 << 20
	// DefaultMaxBatch caps the hashes of one manifest and the upserts plus
	// deletes of one change.
	DefaultMaxBatch = 5000
	// DefaultMaxBody caps a JSON request body; a change of DefaultMaxBatch
	// entries with paths of code.MaxPathBytes fits in it.
	DefaultMaxBody = 16 << 20
)

// Field limits of the JSON bodies, beyond the code package's validators.
const (
	maxSessionIDBytes = 128
	maxRootBytes      = 4096
	maxHostBytes      = 255
	maxOriginBytes    = 2048
	maxLangBytes      = 64
	maxModeBytes      = 6
)

// Error kinds in the envelope (PROTOCOL.md §1.2), plus the code transport's
// own: conflict (409, a change names a blob the server lacks) and quota
// (413, BeforeWrite found the account's code storage full).
const (
	KindUnauthorized = "unauthorized"
	KindInvalid      = "invalid"
	KindForbidden    = "forbidden"
	KindNotFound     = "not_found"
	KindConflict     = "conflict"
	KindTooLarge     = "too_large"
	KindQuota        = "quota"
	KindStore        = "store"
)

// Refusals Options.BeforeWrite returns (wrapped with a message the client
// sees, e.g. fmt.Errorf("%w: code sync is not in your plan", ErrRefused)).
var (
	// ErrRefused refuses the write as 403 forbidden: no entitlement, a
	// policy that keeps the account's code out of the server.
	ErrRefused = errors.New("code write refused")
	// ErrQuota refuses the write as 413 quota: the account's code storage
	// is full. The client backs off as for a refusal.
	ErrQuota = errors.New("code quota exceeded")
)

// Operations, as Operation reports them to BeforeWrite.
const (
	OpManifest = "manifest"
	OpBlob     = "blob"
	OpWorktree = "worktree"
	OpChanges  = "changes"
	OpSession  = "session"
	OpRemove   = "remove"
)

// Options configures the transport.
type Options struct {
	// Open returns the code store of a namespaced project key (what
	// Namespace returned). Required.
	Open func(ctx context.Context, key string) (code.Store, error)
	// Namespace maps the request's project slug (already validated and
	// allowed) to the key its store is opened under; the principal is on
	// ctx. Nil keeps the slug. Its errors answer as described in the
	// package documentation, so a composer fails closed by returning one.
	Namespace func(ctx context.Context, project string) (string, error)
	// Account returns the request's account, stamped as the owner of
	// worktrees and changes. Nil reads the auth principal's AccountID
	// (auth.FromContext); no account is 401 unauthorized.
	Account func(r *http.Request) (string, bool)
	// Allow reports whether the request may use the project; nil allows
	// every project. It runs after slug validation and before Namespace.
	Allow func(r *http.Request, project string) bool
	// BeforeWrite, when set, runs before every write with the namespaced
	// key and the bytes the write adds to the store: a new blob's size on
	// PUT blobs/{sha256} (0 when the blob is already held), 0 for every
	// other write. ctx carries the principal, Project (the slug) and
	// Operation. See the package documentation for the refusals.
	BeforeWrite func(ctx context.Context, key string, bytes int64) error
	// GitHubBacked, when set, reports whether the project of the
	// namespaced key gets its pushed code from GitHub (a claimed, active
	// installation); ctx carries the principal and Project (the slug).
	// capabilities answers "github" with it, and a change may then send
	// clean, pushed entries without content (see the package
	// documentation). Nil: no project is GitHub-backed.
	GitHubBacked func(ctx context.Context, key string) bool
	// MaxFileBytes caps one blob (DefaultMaxFileBytes when zero); a larger
	// upload is 413 too_large. capabilities reports it.
	MaxFileBytes int64
	// MaxBatch caps the hashes of a manifest and the entries (upserts plus
	// deletes) of a change (DefaultMaxBatch when zero); more is 413
	// too_large. capabilities reports it.
	MaxBatch int
	// MaxBody caps a JSON request body (DefaultMaxBody when zero).
	MaxBody int64
}

type ctxKey int

const (
	projectKey ctxKey = iota
	operationKey
)

// Project is the project slug of the codeapi request ctx belongs to ("" outside one).
func Project(ctx context.Context) string {
	p, _ := ctx.Value(projectKey).(string)
	return p
}

// Operation is the write (OpManifest, OpBlob, …) of the codeapi request ctx
// belongs to ("" outside one, and for capabilities).
func Operation(ctx context.Context) string {
	op, _ := ctx.Value(operationKey).(string)
	return op
}

// Capabilities is the result of GET capabilities: the limits, and whether
// the project is GitHub-backed. When GitHub is true a client sends clean,
// pushed entries with sha256 "", their git_oid and size, and uploads no
// blob for them; dirty and unpushed entries are uploaded as before.
type Capabilities struct {
	Code   Limits `json:"code"`
	GitHub bool   `json:"github"`
}

// Limits are the transport's limits a client honours.
type Limits struct {
	MaxFileBytes int64 `json:"maxFileBytes"`
	MaxBatch     int   `json:"maxBatch"`
}

// Manifest is the body of POST manifest.
type Manifest struct {
	SHAs []string `json:"shas"`
}

// ManifestResult is its answer: the hashes the server lacks, in the order given.
type ManifestResult struct {
	Missing []string `json:"missing"`
}

// WorktreeBody is the body of PUT worktrees/{id}. Ref, when set, is added
// to the worktree's refs; Head is validated and not stored.
type WorktreeBody struct {
	Root          string `json:"root"`
	Host          string `json:"host"`
	Origin        string `json:"origin"`
	DefaultBranch string `json:"default_branch"`
	Ref           string `json:"ref"`
	Head          string `json:"head"`
	// Owner is never accepted: present at all, the body is invalid.
	Owner json.RawMessage `json:"owner,omitempty"`
}

// EntryBody is one upsert of a change.
type EntryBody struct {
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
	Mode     string `json:"mode"`
	Lang     string `json:"lang"`
	GitOID   string `json:"git_oid"`
	Dirty    bool   `json:"dirty"`
	Unpushed bool   `json:"unpushed"`
	// Session is never accepted: the server sets it from the change.
	Session json.RawMessage `json:"session,omitempty"`
}

// ChangeBody is the body of POST worktrees/{id}/changes.
type ChangeBody struct {
	Ref       string      `json:"ref"`
	Head      string      `json:"head"`
	Upstream  string      `json:"upstream"`
	Ahead     int         `json:"ahead"`
	SessionID string      `json:"session_id"`
	Full      bool        `json:"full"`
	Upserts   []EntryBody `json:"upserts"`
	Deletes   []string    `json:"deletes"`
	// Owner is never accepted: present at all, the body is invalid.
	Owner json.RawMessage `json:"owner,omitempty"`
}

// ChangeResult is the answer of a commit.
type ChangeResult struct {
	Seq int64 `json:"seq"`
}

// SessionBody is the body of PUT worktrees/{id}/sessions/{session_id}.
type SessionBody struct {
	Ended bool `json:"ended"`
}

// Handler returns the transport on its own mux: the seven routes (Mount)
// and the NotFound catch-all of the code prefix. A test mounts it behind
// auth.Middleware, or with Options.Account, and reads through the store
// Open hands out.
func Handler(opts Options) http.Handler {
	mux := http.NewServeMux()
	Mount(mux, opts)
	mux.HandleFunc(Pattern, NotFound)
	return mux
}

// NotFound answers 404 not_found in the envelope: the code prefix of a
// server without a code store, or a route under it that does not exist.
func NotFound(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteError(w, http.StatusNotFound, KindNotFound, fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path))
}

// Mount registers the seven code routes on mux, so a composer adds them
// next to its own (and later parts add theirs) without a route table here.
// It does not register the Pattern catch-all; the composer does (NotFound).
func Mount(mux *http.ServeMux, opts Options) {
	if opts.Open == nil {
		panic("codeapi: Options.Open is required")
	}
	if opts.MaxFileBytes <= 0 {
		opts.MaxFileBytes = DefaultMaxFileBytes
	}
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = DefaultMaxBatch
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.Account == nil {
		opts.Account = func(r *http.Request) (string, bool) {
			p, ok := auth.FromContext(r.Context())
			return p.AccountID, ok
		}
	}
	a := &api{opts: opts}
	mux.HandleFunc("GET "+PathCapabilities, a.handle("", a.capabilities))
	mux.HandleFunc("POST "+PathManifest, a.handle(OpManifest, a.manifest))
	mux.HandleFunc("PUT "+PathBlob, a.handle(OpBlob, a.putBlob))
	mux.HandleFunc("PUT "+PathWorktree, a.handle(OpWorktree, a.putWorktree))
	mux.HandleFunc("DELETE "+PathWorktree, a.handle(OpRemove, a.removeWorktree))
	mux.HandleFunc("POST "+PathWorktreeChanges, a.handle(OpChanges, a.commit))
	mux.HandleFunc("PUT "+PathWorktreeSession, a.handle(OpSession, a.touchSession))
}

type api struct {
	opts Options
	// locks serialise the ownership check and the write of PUT worktree
	// for one (key, worktree) within this process, so two accounts
	// creating one worktree id at once cannot both pass the check.
	locks [64]sync.Mutex
}

// request carries what every operation needs once the checks passed.
type request struct {
	project string
	key     string
	account string
	store   code.Store
}

type operation func(w http.ResponseWriter, r *http.Request, req *request)

// handle runs the common checks in order: account, slug, Allow, Namespace,
// Open.
func (a *api) handle(op string, fn operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		account, ok := a.opts.Account(r)
		if !ok || account == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpapi.WriteError(w, http.StatusUnauthorized, KindUnauthorized, "authentication required")
			return
		}
		project := r.PathValue("project")
		if !projects.ValidSlug(project) {
			httpapi.WriteError(w, http.StatusBadRequest, KindInvalid, fmt.Sprintf("invalid project slug %q", project))
			return
		}
		if a.opts.Allow != nil && !a.opts.Allow(r, project) {
			httpapi.WriteError(w, http.StatusForbidden, KindForbidden, fmt.Sprintf("project %q is not allowed for this token", project))
			return
		}
		ctx := context.WithValue(r.Context(), projectKey, project)
		if op != "" {
			ctx = context.WithValue(ctx, operationKey, op)
		}
		key := project
		if a.opts.Namespace != nil {
			k, err := a.opts.Namespace(ctx, project)
			if err != nil {
				writeOpenError(w, err)
				return
			}
			key = k
		}
		st, err := a.opts.Open(ctx, key)
		if err != nil {
			writeOpenError(w, err)
			return
		}
		fn(w, r.WithContext(ctx), &request{project: project, key: key, account: account, store: st})
	}
}

// writeOpenError maps a Namespace or Open failure to the envelope.
func writeOpenError(w http.ResponseWriter, err error) {
	var se *httpapi.StatusError
	switch {
	case errors.As(err, &se):
		httpapi.WriteError(w, se.Status, se.Kind, se.Message)
	case errors.Is(err, projects.ErrInvalidProject):
		httpapi.WriteError(w, http.StatusBadRequest, KindInvalid, err.Error())
	case errors.Is(err, auth.ErrProjectForbidden):
		httpapi.WriteError(w, http.StatusForbidden, KindForbidden, err.Error())
	case errors.Is(err, code.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, "unknown project")
	default:
		httpapi.WriteError(w, http.StatusBadGateway, KindStore, "open code store: "+err.Error())
	}
}

// writeStoreError maps a code.Store error to the envelope.
func writeStoreError(w http.ResponseWriter, err error, what string) {
	var se *httpapi.StatusError
	switch {
	case errors.As(err, &se):
		httpapi.WriteError(w, se.Status, se.Kind, se.Message)
	case errors.Is(err, code.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, what+" not found")
	case errors.Is(err, code.ErrMissingBlob):
		httpapi.WriteError(w, http.StatusConflict, KindConflict, err.Error()+"; upload the blob and retry")
	case errors.Is(err, code.ErrInvalid):
		httpapi.WriteError(w, http.StatusBadRequest, KindInvalid, err.Error())
	default:
		httpapi.WriteError(w, http.StatusBadGateway, KindStore, err.Error())
	}
}

// beforeWrite asks Options.BeforeWrite and answers its refusal; false when
// the write must not happen.
func (a *api) beforeWrite(w http.ResponseWriter, r *http.Request, req *request, n int64) bool {
	if a.opts.BeforeWrite == nil {
		return true
	}
	err := a.opts.BeforeWrite(r.Context(), req.key, n)
	if err == nil {
		return true
	}
	var se *httpapi.StatusError
	switch {
	case errors.As(err, &se):
		httpapi.WriteError(w, se.Status, se.Kind, se.Message)
	case errors.Is(err, ErrQuota):
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, KindQuota, err.Error())
	default:
		httpapi.WriteError(w, http.StatusForbidden, KindForbidden, err.Error())
	}
	return false
}

func invalid(w http.ResponseWriter, format string, args ...any) {
	httpapi.WriteError(w, http.StatusBadRequest, KindInvalid, fmt.Sprintf(format, args...))
}

// readJSON decodes the body into v within MaxBody, refusing unknown fields
// and trailing data.
func (a *api) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(io.LimitReader(r.Body, a.opts.MaxBody+1))
	if err != nil {
		invalid(w, "read body: %v", err)
		return false
	}
	if int64(len(b)) > a.opts.MaxBody {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, KindTooLarge, fmt.Sprintf("request body exceeds %d bytes", a.opts.MaxBody))
		return false
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		invalid(w, "body must be a JSON object of the route's fields: %v", err)
		return false
	}
	if dec.More() {
		invalid(w, "body has data after the JSON object")
		return false
	}
	return true
}

// owned reports whether worktree id of the request's store exists and is
// held by the request's account; it answers 404 (or the store's error) and
// returns false otherwise, so another account's worktree reads as unknown.
func (a *api) owned(w http.ResponseWriter, r *http.Request, req *request, id string) bool {
	wt, found, err := lookup(r.Context(), req.store, id)
	if err != nil {
		writeStoreError(w, err, "worktree "+id)
		return false
	}
	if !found || wt.Owner != req.account {
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, "worktree "+id+" not found")
		return false
	}
	return true
}

// lookup finds worktree id among the store's worktrees.
func lookup(ctx context.Context, st code.Store, id string) (code.Worktree, bool, error) {
	ws, err := st.Worktrees(ctx)
	if err != nil {
		return code.Worktree{}, false, err
	}
	for _, wt := range ws {
		if wt.ID == id {
			return wt, true, nil
		}
	}
	return code.Worktree{}, false, nil
}

// lock returns the stripe serialising writes to worktree id under key.
func (a *api) lock(key, id string) *sync.Mutex {
	h := fnv.New32a()
	h.Write([]byte(key))
	h.Write([]byte{0})
	h.Write([]byte(id))
	return &a.locks[h.Sum32()%uint32(len(a.locks))]
}

// worktreeID reads and validates the {id} path value.
func worktreeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("id")
	if !code.ValidWorktreeID(id) {
		invalid(w, "invalid worktree id %q (wt_ and 16 hex characters, or gh_ and a GitHub repository id)", id)
		return "", false
	}
	return id, true
}

// validSessionID accepts a Claude Code session id: 1 to maxSessionIDBytes
// of letters, digits, '-', '_' and '.'.
func validSessionID(s string) bool {
	if s == "" || len(s) > maxSessionIDBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// validText accepts metadata text: at most max bytes of valid UTF-8 with no
// control character.
func validText(s string, max int) bool {
	if len(s) > max || !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}

// validMode accepts git's file mode: empty or up to six octal digits.
func validMode(s string) bool {
	if len(s) > maxModeBytes {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '7' {
			return false
		}
	}
	return true
}

// validOID accepts an empty git object id or one code.ValidGitOID accepts.
func validOID(s string) bool { return s == "" || code.ValidGitOID(s) }

func (a *api) capabilities(w http.ResponseWriter, r *http.Request, req *request) {
	httpapi.WriteResult(w, http.StatusOK, Capabilities{
		Code:   Limits{MaxFileBytes: a.opts.MaxFileBytes, MaxBatch: a.opts.MaxBatch},
		GitHub: a.githubBacked(r.Context(), req.key),
	})
}

// githubBacked asks Options.GitHubBacked; false without one.
func (a *api) githubBacked(ctx context.Context, key string) bool {
	return a.opts.GitHubBacked != nil && a.opts.GitHubBacked(ctx, key)
}

func (a *api) manifest(w http.ResponseWriter, r *http.Request, req *request) {
	var in Manifest
	if !a.readJSON(w, r, &in) {
		return
	}
	if len(in.SHAs) > a.opts.MaxBatch {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, KindTooLarge, fmt.Sprintf("a manifest holds at most %d hashes, got %d", a.opts.MaxBatch, len(in.SHAs)))
		return
	}
	for _, sha := range in.SHAs {
		if !code.ValidSHA256(sha) {
			invalid(w, "invalid sha256 %q (64 lowercase hex characters)", sha)
			return
		}
	}
	if !a.beforeWrite(w, r, req, 0) {
		return
	}
	missing, err := req.store.MissingBlobs(r.Context(), in.SHAs)
	if err != nil {
		writeStoreError(w, err, "blobs")
		return
	}
	if missing == nil {
		missing = []string{}
	}
	httpapi.WriteResult(w, http.StatusOK, ManifestResult{Missing: missing})
}

func (a *api) putBlob(w http.ResponseWriter, r *http.Request, req *request) {
	sha := r.PathValue("sha256")
	if !code.ValidSHA256(sha) {
		invalid(w, "invalid sha256 %q (64 lowercase hex characters)", sha)
		return
	}
	if r.ContentLength > a.opts.MaxFileBytes {
		tooLargeBlob(w, a.opts.MaxFileBytes)
		return
	}
	content, err := io.ReadAll(io.LimitReader(r.Body, a.opts.MaxFileBytes+1))
	if err != nil {
		invalid(w, "read body: %v", err)
		return
	}
	if int64(len(content)) > a.opts.MaxFileBytes {
		tooLargeBlob(w, a.opts.MaxFileBytes)
		return
	}
	sum := sha256.Sum256(content)
	if got := hex.EncodeToString(sum[:]); got != sha {
		invalid(w, "the content hashes to %s, not %s", got, sha)
		return
	}
	missing, err := req.store.MissingBlobs(r.Context(), []string{sha})
	if err != nil {
		writeStoreError(w, err, "blob "+sha)
		return
	}
	added := int64(0)
	if len(missing) > 0 {
		added = int64(len(content))
	}
	if !a.beforeWrite(w, r, req, added) {
		return
	}
	if err := req.store.PutBlob(r.Context(), code.Blob{SHA256: sha, Size: int64(len(content)), Content: content}); err != nil {
		writeStoreError(w, err, "blob "+sha)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, struct{}{})
}

func tooLargeBlob(w http.ResponseWriter, max int64) {
	httpapi.WriteError(w, http.StatusRequestEntityTooLarge, KindTooLarge, fmt.Sprintf("a blob holds at most %d bytes", max))
}

func (a *api) putWorktree(w http.ResponseWriter, r *http.Request, req *request) {
	id, ok := worktreeID(w, r)
	if !ok {
		return
	}
	var in WorktreeBody
	if !a.readJSON(w, r, &in) {
		return
	}
	switch {
	case in.Owner != nil:
		invalid(w, "owner is set by the server from the token; leave it out")
		return
	case !validText(in.Root, maxRootBytes) || strings.ContainsRune(in.Root, 0):
		invalid(w, "invalid root (at most %d bytes of text)", maxRootBytes)
		return
	case !validText(in.Host, maxHostBytes):
		invalid(w, "invalid host (at most %d bytes of text)", maxHostBytes)
		return
	case !validText(in.Origin, maxOriginBytes):
		invalid(w, "invalid origin (at most %d bytes of text)", maxOriginBytes)
		return
	case in.DefaultBranch != "" && !code.ValidRef(in.DefaultBranch):
		invalid(w, "invalid default_branch %q", in.DefaultBranch)
		return
	case in.Ref != "" && !code.ValidRef(in.Ref):
		invalid(w, "invalid ref %q", in.Ref)
		return
	case !validOID(in.Head):
		invalid(w, "invalid head %q (40 or 64 lowercase hex characters, or empty)", in.Head)
		return
	}
	wt := code.Worktree{
		ID:            id,
		Owner:         req.account,
		Root:          in.Root,
		Host:          in.Host,
		Origin:        in.Origin,
		DefaultBranch: in.DefaultBranch,
	}
	if in.Ref != "" {
		wt.Refs = []string{in.Ref}
	}
	mu := a.lock(req.key, id)
	mu.Lock()
	defer mu.Unlock()
	cur, found, err := lookup(r.Context(), req.store, id)
	if err != nil {
		writeStoreError(w, err, "worktree "+id)
		return
	}
	if found && cur.Owner != req.account {
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, "worktree "+id+" not found")
		return
	}
	if found {
		// Writing through the transport never changes who writes the
		// worktree.
		wt.Source = cur.Source
	}
	if !a.beforeWrite(w, r, req, 0) {
		return
	}
	if err := req.store.PutWorktree(r.Context(), wt); err != nil {
		writeStoreError(w, err, "worktree "+id)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, struct{}{})
}

func (a *api) commit(w http.ResponseWriter, r *http.Request, req *request) {
	id, ok := worktreeID(w, r)
	if !ok {
		return
	}
	var in ChangeBody
	if !a.readJSON(w, r, &in) {
		return
	}
	if n := len(in.Upserts) + len(in.Deletes); n > a.opts.MaxBatch {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, KindTooLarge, fmt.Sprintf("a change holds at most %d upserts and deletes, got %d", a.opts.MaxBatch, n))
		return
	}
	switch {
	case in.Owner != nil:
		invalid(w, "owner is set by the server from the token; leave it out")
		return
	case !code.ValidRef(in.Ref):
		invalid(w, "invalid ref %q", in.Ref)
		return
	case !validOID(in.Head):
		invalid(w, "invalid head %q (40 or 64 lowercase hex characters, or empty)", in.Head)
		return
	case !validOID(in.Upstream):
		invalid(w, "invalid upstream %q (40 or 64 lowercase hex characters, or empty)", in.Upstream)
		return
	case in.Ahead < 0:
		invalid(w, "ahead must not be negative")
		return
	case in.SessionID != "" && !validSessionID(in.SessionID):
		invalid(w, "invalid session_id %q", in.SessionID)
		return
	}
	c := code.Change{
		Owner:     req.account,
		Worktree:  id,
		Ref:       in.Ref,
		Head:      in.Head,
		Upstream:  in.Upstream,
		Ahead:     in.Ahead,
		SessionID: in.SessionID,
		Full:      in.Full,
	}
	if len(in.Upserts) > 0 {
		c.Upserts = make([]code.Entry, 0, len(in.Upserts))
	}
	for i, e := range in.Upserts {
		switch {
		case e.Session != nil:
			invalid(w, "upserts[%d]: session is set by the server from session_id; leave it out", i)
			return
		case !code.ValidPath(e.Path):
			invalid(w, "upserts[%d]: invalid path %q (clean, slash-separated, relative, at most %d bytes)", i, e.Path, code.MaxPathBytes)
			return
		case e.SHA256 != "" && !code.ValidSHA256(e.SHA256):
			invalid(w, "upserts[%d] %s: invalid sha256 %q", i, e.Path, e.SHA256)
			return
		case e.Size < 0:
			invalid(w, "upserts[%d] %s: size must not be negative", i, e.Path)
			return
		case !validMode(e.Mode):
			invalid(w, "upserts[%d] %s: invalid mode %q", i, e.Path, e.Mode)
			return
		case !validText(e.Lang, maxLangBytes):
			invalid(w, "upserts[%d] %s: invalid lang", i, e.Path)
			return
		case !validOID(e.GitOID):
			invalid(w, "upserts[%d] %s: invalid git_oid %q", i, e.Path, e.GitOID)
			return
		case !e.Dirty && e.GitOID == "":
			invalid(w, "upserts[%d] %s: a clean entry (dirty false) needs its git_oid", i, e.Path)
			return
		case !e.Unpushed && in.Upstream == "":
			invalid(w, "upserts[%d] %s: a branch with no upstream has only unpushed entries", i, e.Path)
			return
		}
		c.Upserts = append(c.Upserts, code.Entry{
			Path:     e.Path,
			SHA256:   e.SHA256,
			Size:     e.Size,
			Mode:     e.Mode,
			Lang:     e.Lang,
			GitOID:   e.GitOID,
			Dirty:    e.Dirty,
			Unpushed: e.Unpushed,
			Session:  in.SessionID,
		})
	}
	for i, p := range in.Deletes {
		if !code.ValidPath(p) {
			invalid(w, "deletes[%d]: invalid path %q (clean, slash-separated, relative, at most %d bytes)", i, p, code.MaxPathBytes)
			return
		}
	}
	if len(in.Deletes) > 0 {
		c.Deletes = append([]string(nil), in.Deletes...)
	}
	if !a.owned(w, r, req, id) || !a.beforeWrite(w, r, req, 0) {
		return
	}
	seq, err := req.store.Commit(r.Context(), c)
	if err != nil {
		writeStoreError(w, err, "worktree "+id)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, ChangeResult{Seq: seq})
}

func (a *api) touchSession(w http.ResponseWriter, r *http.Request, req *request) {
	id, ok := worktreeID(w, r)
	if !ok {
		return
	}
	sessionID := r.PathValue("session_id")
	if !validSessionID(sessionID) {
		invalid(w, "invalid session id %q", sessionID)
		return
	}
	var in SessionBody
	if !a.readJSON(w, r, &in) {
		return
	}
	if !a.owned(w, r, req, id) || !a.beforeWrite(w, r, req, 0) {
		return
	}
	if err := req.store.TouchSession(r.Context(), id, sessionID, in.Ended); err != nil {
		writeStoreError(w, err, "worktree "+id)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, struct{}{})
}

func (a *api) removeWorktree(w http.ResponseWriter, r *http.Request, req *request) {
	id, ok := worktreeID(w, r)
	if !ok {
		return
	}
	if !a.owned(w, r, req, id) || !a.beforeWrite(w, r, req, 0) {
		return
	}
	if err := req.store.RemoveWorktree(r.Context(), id); err != nil {
		writeStoreError(w, err, "worktree "+id)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, struct{}{})
}
