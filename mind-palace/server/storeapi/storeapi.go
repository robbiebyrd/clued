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
//	GET    /api/v1/projects/{project}/store/{kind}/templates        ListTemplates
//	GET    /api/v1/projects/{project}/store/{kind}/templates/{id}   GetTemplate
//	PUT    /api/v1/projects/{project}/store/{kind}/templates/{id}   PutTemplate (body: model.Template)
//	DELETE /api/v1/projects/{project}/store/{kind}/templates/{id}   DeleteTemplate
//
// Each request validates the project slug (400 invalid), asks Options.Allow
// (403 forbidden), checks the kind (400 invalid) and only then opens the
// project's store (502 store on failure). store.ErrNotFound is 404
// not_found; a body over MaxBody is 413 too_large. The literal "templates"
// segment shadows a document with that id, which no generated id has.
package storeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
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
)

// Options configures the handler.
type Options struct {
	// Open returns the store of a project (a validated slug). Required.
	Open func(ctx context.Context, project string) (store.Store, error)
	// Allow reports whether the request may use the project; nil allows
	// every project. It runs after slug validation and before Open.
	Allow func(r *http.Request, project string) bool
	// MaxBody limits PUT bodies in bytes (DefaultMaxBody when zero).
	MaxBody int64
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
	return mux
}

type api struct {
	opts Options
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
			if errors.Is(err, projects.ErrInvalidProject) {
				httpapi.WriteError(w, http.StatusBadRequest, ErrInvalid, err.Error())
				return
			}
			httpapi.WriteError(w, http.StatusBadGateway, ErrStore, "open project store: "+err.Error())
			return
		}
		op(w, r, &request{project: project, kind: k.Name, id: r.PathValue("id"), store: st})
	}
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
	httpapi.WriteResult(w, http.StatusOK, d)
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
	err := req.store.Put(r.Context(), &d)
	writeWriteResult(w, &d, err, req.kind+" "+req.id)
}

func (a *api) delete(w http.ResponseWriter, r *http.Request, req *request) {
	err := req.store.Delete(r.Context(), req.kind, req.id)
	writeWriteResult(w, map[string]string{"kind": req.kind, "id": req.id}, err, req.kind+" "+req.id)
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
	err := req.store.PutTemplate(r.Context(), &t)
	writeWriteResult(w, &t, err, req.kind+" template "+req.id)
}

func (a *api) deleteTemplate(w http.ResponseWriter, r *http.Request, req *request) {
	err := req.store.DeleteTemplate(r.Context(), req.kind, req.id)
	writeWriteResult(w, map[string]string{"kind": req.kind, "id": req.id}, err, req.kind+" template "+req.id)
}
