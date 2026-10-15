// Package storeapi serves the store.Store port over HTTP, one store per
// project, under /api/v1/projects/{project}/store/. It is the raw document
// transport the remote store driver on a client talks to; business rules
// stay in the client's service layer, the server only persists what it is
// given.
//
// Routes (every response uses the httpapi envelope):
//
//	GET    /api/v1/projects/{project}/store/{kind}                  List
//	GET    /api/v1/projects/{project}/store/{kind}/{id}             Get
//	PUT    /api/v1/projects/{project}/store/{kind}/{id}             Put (body: model.Document)
//	DELETE /api/v1/projects/{project}/store/{kind}/{id}             Delete
//	POST   /api/v1/projects/{project}/store/{kind}/{id}/lock        take the lease (LeaseTTL)
//	POST   /api/v1/projects/{project}/store/{kind}/{id}/unlock      release it (body: {"lease": "…"})
//	GET    /api/v1/projects/{project}/store/{kind}/templates        ListTemplates
//	GET    /api/v1/projects/{project}/store/{kind}/templates/{id}   GetTemplate
//	PUT    /api/v1/projects/{project}/store/{kind}/templates/{id}   PutTemplate (body: model.Template)
//	DELETE /api/v1/projects/{project}/store/{kind}/templates/{id}   DeleteTemplate
//
// Each request validates the project slug (400 invalid), asks Options.Allow
// (403 forbidden), checks the kind (400 invalid) and only then opens the
// project's store (502 store on failure; an Open error that is a
// *httpapi.StatusError answers as it says, one wrapping
// auth.ErrProjectForbidden 403 forbidden, one wrapping
// projects.ErrInvalidProject 400 invalid). store.ErrNotFound is 404
// not_found; a body over MaxBody is 413 too_large.
//
// A document GET answers ETag: "<front_matter.updated>". A document PUT
// with If-Match: "<updated>" is written only while the stored copy still
// has that updated, atomically on a store.ConditionalPutter (412
// precondition otherwise, the document left as it was); Options.BeforePut
// may refuse a PUT (403 forbidden). The literal "templates"
// segment shadows a document with that id, which no generated id has.
package storeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/server/auth"
	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/server/projects"
	"github.com/robbiebyrd/clued/mind-palace/store"
)

// Prefix is the root of the project-scoped routes.
const Prefix = "/api/v1/projects"

// DefaultMaxBody is the request body limit when Options.MaxBody is zero.
const DefaultMaxBody = 8 << 20

// Error kinds in the envelope.
const (
	ErrInvalid   = "invalid"
	ErrForbidden = "forbidden"
	ErrNotFound  = "not_found"
	ErrTooLarge  = "too_large"
	ErrStore     = "store"
	// ErrPrecondition is the kind of a 412: the If-Match of a PUT names an
	// updated the stored document no longer has (or it does not exist).
	ErrPrecondition = "precondition"
)

// ForceHeader is the request header ("1") a client sends to ask for a
// forced write; BeforePut reads it with Forced. storeapi itself gives it no
// meaning.
const ForceHeader = "X-Clued-Force"

// Options configures the handler.
type Options struct {
	// Open returns the store of a project (a validated slug). Required.
	Open func(ctx context.Context, project string) (store.Store, error)
	// Allow reports whether the request may use the project; nil allows
	// every project. It runs after slug validation and before Open.
	Allow func(r *http.Request, project string) bool
	// MaxBody limits PUT bodies in bytes (DefaultMaxBody when zero).
	MaxBody int64
	// BeforePut, when set, runs before every document PUT with the stored
	// copy (nil when there is none) and the document about to be written.
	// An error refuses the write: a *httpapi.StatusError answers as it
	// says, anything else 403 forbidden with the error's message. ctx is
	// the request's (the principal is on it), and Project and Forced read
	// the path's project slug and the ForceHeader from it. The write that
	// follows is conditional on prev's updated, so a concurrent writer
	// between the check and the write gets 412 instead of slipping past.
	BeforePut func(ctx context.Context, prev, next *model.Document) error
}

type ctxKey int

const (
	projectKey ctxKey = iota
	forceKey
)

// Project is the project slug of the storeapi request ctx belongs to ("" outside one).
func Project(ctx context.Context) string {
	p, _ := ctx.Value(projectKey).(string)
	return p
}

// Forced reports whether the storeapi request ctx belongs to carries
// ForceHeader: 1.
func Forced(ctx context.Context) bool {
	f, _ := ctx.Value(forceKey).(bool)
	return f
}

// Handler returns the store transport.
func Handler(opts Options) http.Handler {
	if opts.Open == nil {
		panic("storeapi: Options.Open is required")
	}
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	a := &api{opts: opts}
	mux := http.NewServeMux()
	p := Prefix + "/{project}/store/{kind}"
	// The literal templates routes are registered before the {id} ones so a
	// reader sees the precedence; the mux prefers the literal either way.
	mux.HandleFunc("GET "+p+"/templates", a.handle(a.listTemplates))
	mux.HandleFunc("GET "+p+"/templates/{id}", a.handle(a.getTemplate))
	mux.HandleFunc("PUT "+p+"/templates/{id}", a.handle(a.putTemplate))
	mux.HandleFunc("DELETE "+p+"/templates/{id}", a.handle(a.deleteTemplate))
	mux.HandleFunc("GET "+p, a.handle(a.list))
	mux.HandleFunc("GET "+p+"/{id}", a.handle(a.get))
	mux.HandleFunc("PUT "+p+"/{id}", a.handle(a.put))
	mux.HandleFunc("DELETE "+p+"/{id}", a.handle(a.delete))
	mux.HandleFunc("POST "+p+"/{id}/lock", a.handle(a.lock))
	mux.HandleFunc("POST "+p+"/{id}/unlock", a.handle(a.unlock))
	return mux
}

// LeaseTTL is how long a lease from the lock route lives unless released.
const LeaseTTL = store.DefaultLeaseTTL

// ErrLocked is the kind of a 409 from the lock route: another client holds
// the document's lease.
const ErrLocked = "locked"

// Lease is the body of a lock answer, and the lease an unlock names.
type Lease struct {
	Lease     string    `json:"lease"`
	ExpiresAt time.Time `json:"expires_at,omitzero"`
}

type api struct {
	opts Options
	// leases serves the lock routes for stores that keep none themselves
	// (no store.Leaser): exclusive within this process only.
	leases store.MemLeases
}

// request carries what every operation needs once the checks passed.
type request struct {
	project string
	kind    string
	id      string
	store   store.Store
}

type operation func(w http.ResponseWriter, r *http.Request, req *request)

// handle runs the common checks in order: slug, Allow, kind, Open.
func (a *api) handle(op operation) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		project := r.PathValue("project")
		if !projects.ValidSlug(project) {
			httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("invalid project slug %q", project))
			return
		}
		if a.opts.Allow != nil && !a.opts.Allow(r, project) {
			httpapi.WriteError(w, http.StatusForbidden, ErrForbidden, fmt.Sprintf("project %q is not allowed for this token", project))
			return
		}
		k, ok := kind.Get(r.PathValue("kind"))
		if !ok || !isKindName(r.PathValue("kind")) {
			httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("unknown document kind %q (known: %s)", r.PathValue("kind"), strings.Join(kind.Names(), ", ")))
			return
		}
		st, err := a.opts.Open(r.Context(), project)
		if err != nil {
			var se *httpapi.StatusError
			switch {
			case errors.As(err, &se):
				httpapi.WriteError(w, se.Status, se.Kind, se.Message)
			case errors.Is(err, projects.ErrInvalidProject):
				httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, err.Error())
			case errors.Is(err, auth.ErrProjectForbidden):
				httpapi.WriteError(w, http.StatusForbidden, ErrForbidden, err.Error())
			default:
				httpapi.WriteError(w, http.StatusBadGateway, ErrStore, "open project store: "+err.Error())
			}
			return
		}
		ctx := context.WithValue(r.Context(), projectKey, project)
		ctx = context.WithValue(ctx, forceKey, strings.TrimSpace(r.Header.Get(ForceHeader)) == "1")
		op(w, r.WithContext(ctx), &request{project: project, kind: k.Name, id: r.PathValue("id"), store: st})
	}
}

// validDocPath accepts a kind-relative document path that stays under the
// kind's root: slash-separated, clean, and local (no "..", no absolute
// path, no backslashes).
func validDocPath(p string) bool {
	if strings.ContainsAny(p, "\\\x00") || path.Clean(p) != p || strings.HasPrefix(p, "/") {
		return false
	}
	return filepath.IsLocal(filepath.FromSlash(p))
}

// isKindName accepts only the registered kind names (plan, story), not
// their plurals, so the route has one spelling.
func isKindName(name string) bool {
	for _, n := range kind.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// writeStoreError maps a store error to the envelope.
func writeStoreError(w http.ResponseWriter, err error, what string) {
	var se *httpapi.StatusError
	if errors.As(err, &se) {
		httpapi.WriteError(w, se.Status, se.Kind, se.Message)
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		httpapi.WriteError(w, http.StatusNotFound, ErrNotFound, what+" not found")
		return
	}
	httpapi.WriteError(w, http.StatusBadGateway, ErrStore, err.Error())
}

// writePartial answers a write that succeeded on the primary store but
// failed on a copy: 200 with the result and a warning, as the service API
// does for the same case.
func writePartial(w http.ResponseWriter, v any, fe *store.FanoutError) {
	failed := make([]any, 0, len(fe.Failed))
	for name, err := range fe.Failed {
		failed = append(failed, name+": "+err.Error())
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"ok":      true,
		"result":  v,
		"warning": map[string]any{"kind": "partial", "message": fe.Error(), "problems": failed, "partial": true},
	})
}

// writeWriteResult handles the outcome of a fan-out write.
func writeWriteResult(w http.ResponseWriter, v any, err error, what string) {
	var fe *store.FanoutError
	switch {
	case err == nil:
		httpapi.WriteResult(w, http.StatusOK, v)
	case errors.As(err, &fe):
		writePartial(w, v, fe)
	default:
		writeStoreError(w, err, what)
	}
}

// readJSON decodes the body into v within the size limit.
func (a *api) readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	b, err := io.ReadAll(io.LimitReader(r.Body, a.opts.MaxBody+1))
	if err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, "read body: "+err.Error())
		return false
	}
	if int64(len(b)) > a.opts.MaxBody {
		httpapi.WriteError(w, http.StatusRequestEntityTooLarge, ErrTooLarge, fmt.Sprintf("request body exceeds %d bytes", a.opts.MaxBody))
		return false
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, "request body must be a JSON object")
		return false
	}
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(v); err != nil {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, "body must be JSON: "+err.Error())
		return false
	}
	return true
}

func (a *api) list(w http.ResponseWriter, r *http.Request, req *request) {
	docs, err := req.store.List(r.Context(), req.kind)
	if err != nil {
		writeStoreError(w, err, req.kind+" list")
		return
	}
	if docs == nil {
		docs = []*model.Document{}
	}
	httpapi.WriteResult(w, http.StatusOK, docs)
}

func (a *api) get(w http.ResponseWriter, r *http.Request, req *request) {
	d, err := req.store.Get(r.Context(), req.kind, req.id)
	if err != nil {
		writeStoreError(w, err, req.kind+" "+req.id)
		return
	}
	if d.FrontMatter.Updated != "" {
		w.Header().Set("ETag", ETag(d.FrontMatter.Updated))
	}
	httpapi.WriteResult(w, http.StatusOK, d)
}

// ETag is the entity tag of a document whose front_matter.updated is
// updated: the value quoted. GET answers it and a PUT's If-Match names it.
func ETag(updated string) string { return `"` + updated + `"` }

// ifMatch reads the If-Match header of a PUT: the updated it names (the
// quotes and a weak W/ prefix removed) and whether there is one.
func ifMatch(r *http.Request) (string, bool) {
	v := strings.TrimSpace(r.Header.Get("If-Match"))
	if v == "" {
		return "", false
	}
	v = strings.TrimPrefix(v, "W/")
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		v = v[1 : len(v)-1]
	}
	return v, true
}

func (a *api) put(w http.ResponseWriter, r *http.Request, req *request) {
	var d model.Document
	if !a.readJSON(w, r, &d) {
		return
	}
	if d.Kind == "" {
		d.Kind = req.kind
	}
	if d.Kind != req.kind {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("document kind %q does not match the path kind %q", d.Kind, req.kind))
		return
	}
	if d.ID() != req.id {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("document id %q does not match the path id %q", d.ID(), req.id))
		return
	}
	id, ok := model.CanonicalID(req.id)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("invalid document id %q", req.id))
		return
	}
	if d.Path != "" && !validDocPath(d.Path) {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("invalid document path %q", d.Path))
		return
	}
	d.FrontMatter.ID = id
	ctx := r.Context()
	want, conditional := ifMatch(r)
	var prev *model.Document
	if conditional || a.opts.BeforePut != nil {
		got, err := req.store.Get(ctx, req.kind, id)
		switch {
		case err == nil:
			prev = got
		case !errors.Is(err, store.ErrNotFound):
			writeStoreError(w, err, req.kind+" "+req.id)
			return
		}
	}
	if conditional && (prev == nil || prev.FrontMatter.Updated != want) {
		writePrecondition(w, req.kind+" "+req.id)
		return
	}
	if a.opts.BeforePut != nil {
		if err := a.opts.BeforePut(ctx, prev, d.Clone()); err != nil {
			var se *httpapi.StatusError
			if errors.As(err, &se) {
				httpapi.WriteError(w, se.Status, se.Kind, se.Message)
				return
			}
			httpapi.WriteError(w, http.StatusForbidden, ErrForbidden, err.Error())
			return
		}
	}
	var err error
	if prev != nil {
		err = store.PutIf(ctx, req.store, &d, prev.FrontMatter.Updated)
	} else {
		err = req.store.Put(ctx, &d)
	}
	if errors.Is(err, store.ErrPrecondition) {
		writePrecondition(w, req.kind+" "+req.id)
		return
	}
	if err == nil || errors.As(err, new(*store.FanoutError)) {
		w.Header().Set("ETag", ETag(d.FrontMatter.Updated))
	}
	writeWriteResult(w, &d, err, req.kind+" "+req.id)
}

// writePrecondition answers 412: the document changed since the client read it.
func writePrecondition(w http.ResponseWriter, what string) {
	httpapi.WriteError(w, http.StatusPreconditionFailed, ErrPrecondition, what+" changed since it was read (If-Match does not match its updated); read it again and retry")
}

func (a *api) delete(w http.ResponseWriter, r *http.Request, req *request) {
	err := req.store.Delete(r.Context(), req.kind, req.id)
	writeWriteResult(w, map[string]string{"kind": req.kind, "id": req.id}, err, req.kind+" "+req.id)
}

// leaser is the store's own Leaser, or this process's for one that keeps none.
func (a *api) leaser(req *request) (store.Leaser, string) {
	if l, ok := req.store.(store.Leaser); ok {
		return l, req.kind
	}
	return &a.leases, req.project + "/" + req.kind
}

// lock takes the document's lease for LeaseTTL: 200 with the lease, 409
// locked while another holder's lease is unexpired. It does not wait;
// clients retry.
func (a *api) lock(w http.ResponseWriter, r *http.Request, req *request) {
	if _, ok := model.CanonicalID(req.id); !ok {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("invalid document id %q", req.id))
		return
	}
	l, k := a.leaser(req)
	lease, err := l.AcquireLease(r.Context(), k, req.id, LeaseTTL)
	if errors.Is(err, store.ErrNoLease) {
		l, k = &a.leases, req.project+"/"+req.kind
		lease, err = l.AcquireLease(r.Context(), k, req.id, LeaseTTL)
	}
	switch {
	case errors.Is(err, store.ErrLeased):
		httpapi.WriteError(w, http.StatusConflict, ErrLocked, fmt.Sprintf("%s %s is locked by another client; retry", req.kind, req.id))
	case err != nil:
		writeStoreError(w, err, req.kind+" "+req.id)
	default:
		httpapi.WriteResult(w, http.StatusOK, Lease{Lease: lease, ExpiresAt: time.Now().UTC().Add(LeaseTTL)})
	}
}

// unlock releases the lease the body names; a lease that expired or was
// taken over is released already, so it answers 200 too.
func (a *api) unlock(w http.ResponseWriter, r *http.Request, req *request) {
	var in Lease
	if !a.readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Lease) == "" {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, "the body must name the lease")
		return
	}
	l, k := a.leaser(req)
	err := l.ReleaseLease(r.Context(), k, req.id, in.Lease)
	if errors.Is(err, store.ErrNoLease) {
		err = a.leases.ReleaseLease(r.Context(), req.project+"/"+req.kind, req.id, in.Lease)
	}
	if err != nil {
		writeStoreError(w, err, req.kind+" "+req.id)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, map[string]string{"kind": req.kind, "id": req.id})
}

func (a *api) listTemplates(w http.ResponseWriter, r *http.Request, req *request) {
	ts, err := req.store.ListTemplates(r.Context(), req.kind)
	if err != nil {
		writeStoreError(w, err, req.kind+" templates")
		return
	}
	if ts == nil {
		ts = []*model.Template{}
	}
	httpapi.WriteResult(w, http.StatusOK, ts)
}

func (a *api) getTemplate(w http.ResponseWriter, r *http.Request, req *request) {
	t, err := req.store.GetTemplate(r.Context(), req.kind, req.id)
	if err != nil {
		writeStoreError(w, err, req.kind+" template "+req.id)
		return
	}
	httpapi.WriteResult(w, http.StatusOK, t)
}

func (a *api) putTemplate(w http.ResponseWriter, r *http.Request, req *request) {
	var t model.Template
	if !a.readJSON(w, r, &t) {
		return
	}
	if t.Kind == "" {
		t.Kind = req.kind
	}
	if t.Kind != req.kind {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("template kind %q does not match the path kind %q", t.Kind, req.kind))
		return
	}
	if t.ID == "" {
		t.ID = req.id
	}
	if t.ID != req.id {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("template id %q does not match the path id %q", t.ID, req.id))
		return
	}
	id, ok := model.CanonicalTemplateID(req.id)
	if !ok {
		httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, fmt.Sprintf("invalid template id %q", req.id))
		return
	}
	t.ID = id
	err := req.store.PutTemplate(r.Context(), &t)
	writeWriteResult(w, &t, err, req.kind+" template "+req.id)
}

func (a *api) deleteTemplate(w http.ResponseWriter, r *http.Request, req *request) {
	err := req.store.DeleteTemplate(r.Context(), req.kind, req.id)
	writeWriteResult(w, map[string]string{"kind": req.kind, "id": req.id}, err, req.kind+" template "+req.id)
}
