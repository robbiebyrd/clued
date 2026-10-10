// Package httpapi exposes the plan and story services over HTTP/REST. Every
// operation is reachable as POST /api/v1/{plans|stories}/ops/{op} with its
// JSON parameters as the body; resource-style routes cover the common reads
// and writes. Responses use the envelope {"ok": true, "result": …,
// "warning"?: …} or {"ok": false, "error": {"kind", "message", "problems"}}.
//
// /api/v1/ops/{op}, /api/v1/templates… and /api/v1/schema are shortcuts for
// the plan routes.
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/events"
	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/ops"
	"github.com/robbiebyrd/clued/mind-palace/service"
)

// Prefix is the API root.
const Prefix = "/api/v1"

// MaxBody limits request bodies (documents are text; 8 MiB is generous).
const MaxBody = 8 << 20

// Handler returns the HTTP API handler.
func Handler(palace *service.Palace, regs ops.Registries) http.Handler {
	a := &api{palace: palace, regs: regs}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Prefix+"/ops", a.listOps)
	mux.HandleFunc("POST "+Prefix+"/ops/{op}", a.callOp)
	mux.HandleFunc("GET "+Prefix+"/ops/{op}", a.callOpGet)
	// Kind-scoped routes are registered with literal kinds (plans, stories)
	// so they cannot conflict with the plan shortcuts below.
	for _, k := range kind.All() {
		p := Prefix + "/" + k.Plural
		kindRoute := func(method, suffix string, h http.HandlerFunc) {
			mux.HandleFunc(method+" "+p+suffix, withKind(k.Plural, h))
		}
		kindRoute("GET", "/ops", a.listOps)
		kindRoute("POST", "/ops/{op}", a.callOp)
		kindRoute("GET", "/ops/{op}", a.callOpGet)
		kindRoute("GET", "", a.op("list", fromQuery))
		kindRoute("POST", "", a.create)
		kindRoute("GET", "/{id}", a.op("get", withDoc))
		kindRoute("PATCH", "/{id}", a.patch)
		kindRoute("DELETE", "/{id}", a.op("delete", withDocQuery))
		kindRoute("GET", "/{id}/{sub}", a.getSub)
		kindRoute("PUT", "/{id}/{sub}", a.putSub)
		kindRoute("GET", "/templates", a.op("listTemplates", nil))
		kindRoute("GET", "/templates/{id}", a.template)
		kindRoute("PUT", "/templates/{id}", a.putTemplate)
		kindRoute("DELETE", "/templates/{id}", a.op("deleteTemplate", withTemplate))
		kindRoute("GET", "/schema", a.schema)
	}
	mux.HandleFunc("GET "+Prefix+"/templates", a.op("listTemplates", nil))
	mux.HandleFunc("GET "+Prefix+"/templates/{id}", a.template)
	mux.HandleFunc("PUT "+Prefix+"/templates/{id}", a.putTemplate)
	mux.HandleFunc("DELETE "+Prefix+"/templates/{id}", a.op("deleteTemplate", withTemplate))
	mux.HandleFunc("GET "+Prefix+"/schema", a.schema)

	mux.HandleFunc("GET "+Prefix+"/config", a.op("getConfig", nil))
	mux.HandleFunc("GET "+Prefix+"/events", a.events)
	return mux
}

type api struct {
	palace *service.Palace
	regs   ops.Registries
}

// withKind runs next with the kind path value set (for literal-kind routes).
func withKind(k string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.SetPathValue("kind", k)
		next(w, r)
	}
}

// target resolves the kind named in the path ("plans", "stories"; the plan
// shortcuts carry no kind) to its service and registry.
func (a *api) target(r *http.Request) (*service.Service, *ops.Registry, error) {
	k := r.PathValue("kind")
	if k == "" {
		k = kind.Plan
	}
	reg, err := a.regs.Get(k)
	if err != nil {
		return nil, nil, &service.Error{Kind: service.KindNotFound, Message: fmt.Sprintf("unknown document kind %q (known: %s)", k, strings.Join(kind.Names(), ", "))}
	}
	svc, err := a.palace.Service(k)
	if err != nil {
		return nil, nil, err
	}
	return svc, reg, nil
}

type paramsFn func(r *http.Request) (json.RawMessage, error)

func withDoc(r *http.Request) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"document": r.PathValue("id")})
}

func withTemplate(r *http.Request) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"template": r.PathValue("id")})
}

func withDocQuery(r *http.Request) (json.RawMessage, error) {
	m := queryMap(r)
	m["document"] = r.PathValue("id")
	if v, ok := m["force"]; ok {
		m["force"] = isTrue(fmt.Sprint(v))
	}
	return json.Marshal(m)
}

// withDocBody merges the JSON body with the document id from the path.
func withDocBody(r *http.Request) (json.RawMessage, error) {
	m, err := bodyMap(r)
	if err != nil {
		return nil, err
	}
	m["document"] = r.PathValue("id")
	return json.Marshal(m)
}

func fromQuery(r *http.Request) (json.RawMessage, error) {
	m := queryMap(r)
	if v, ok := m["includeArchived"]; ok {
		m["includeArchived"] = isTrue(fmt.Sprint(v))
	}
	if v, ok := m["archived"]; ok {
		delete(m, "archived")
		m["includeArchived"] = isTrue(fmt.Sprint(v))
	}
	return json.Marshal(m)
}

func queryMap(r *http.Request) map[string]any {
	m := map[string]any{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

func isTrue(s string) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "y", "on", "":
		return true
	}
	return false
}

func readBody(r *http.Request) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBody {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: "request body too large"}
	}
	return b, nil
}

func bodyMap(r *http.Request) (map[string]any, error) {
	b, err := readBody(r)
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return m, nil
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, &service.Error{Kind: service.KindBadRequest, Message: "body must be a JSON object: " + err.Error()}
	}
	return m, nil
}

// op builds a handler that runs a named operation with parameters from fn.
func (a *api) op(name string, fn paramsFn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if fn != nil {
			var err error
			raw, err = fn(r)
			if err != nil {
				writeError(w, err)
				return
			}
		}
		a.run(w, r, name, raw)
	}
}

func (a *api) run(w http.ResponseWriter, r *http.Request, name string, raw json.RawMessage) {
	svc, reg, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	res, err := reg.Invoke(r.Context(), svc, name, raw)
	if err != nil {
		writeError(w, err)
		return
	}
	status := http.StatusOK
	if name == "create" {
		status = http.StatusCreated
	}
	writeResult(w, status, res)
}

func (a *api) listOps(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("kind") == "" {
		var all []ops.Describe
		for _, svc := range a.palace.Services() {
			all = append(all, a.regs[svc.Kind().Name].Describe(svc)...)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": all})
		return
	}
	svc, reg, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": reg.Describe(svc)})
}

func (a *api) callOp(w http.ResponseWriter, r *http.Request) {
	b, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return
	}
	a.run(w, r, r.PathValue("op"), b)
}

// callOpGet allows read-only operations via GET with query parameters.
func (a *api) callOpGet(w http.ResponseWriter, r *http.Request) {
	_, reg, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	op, ok := reg.Get(r.PathValue("op"))
	if !ok || !op.ReadOnly {
		writeError(w, &service.Error{Kind: service.KindBadRequest, Message: "only read-only operations can be called with GET; use POST"})
		return
	}
	raw, _ := fromQuery(r)
	a.run(w, r, op.Name, raw)
}

func (a *api) create(w http.ResponseWriter, r *http.Request) {
	m, err := bodyMap(r)
	if err != nil {
		writeError(w, err)
		return
	}
	// Accept either {"input": {...}, "template": "..."} or the bare input.
	if _, ok := m["input"]; !ok {
		m = map[string]any{"input": m, "template": r.URL.Query().Get("template")}
	}
	raw, _ := json.Marshal(m)
	a.run(w, r, "create", raw)
}

// getSub serves the read-only sub-resources of a document.
func (a *api) getSub(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("sub") {
	case "front-matter":
		a.op("getFrontMatter", withDoc)(w, r)
	case "content":
		a.content(w, r)
	case "transitions":
		a.op("getTransitions", withDoc)(w, r)
	case "progress":
		a.op("getProgress", withDoc)(w, r)
	case "validate":
		a.op("validate", withDoc)(w, r)
	case "criteria":
		a.op("getCriteria", withDoc)(w, r)
	default:
		writeError(w, &service.Error{Kind: service.KindNotFound, Message: fmt.Sprintf("unknown sub-resource %q (front-matter, content, transitions, progress, validate, criteria)", r.PathValue("sub"))})
	}
}

// putSub serves the writable sub-resources of a document.
func (a *api) putSub(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("sub") {
	case "content":
		a.updateContent(w, r)
	case "status":
		a.op("setStatus", withDocBody)(w, r)
	default:
		writeError(w, &service.Error{Kind: service.KindNotFound, Message: fmt.Sprintf("unknown sub-resource %q (content, status)", r.PathValue("sub"))})
	}
}

func (a *api) content(w http.ResponseWriter, r *http.Request) {
	svc, _, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	content, err := svc.GetContent(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": content})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	io.WriteString(w, content)
}

// textOrJSON reads a Markdown body, or {"content": "..."} when JSON is sent.
func textOrJSON(w http.ResponseWriter, r *http.Request) (string, bool) {
	b, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return "", false
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			writeError(w, &service.Error{Kind: service.KindBadRequest, Message: "expected {\"content\": \"...\"}"})
			return "", false
		}
		return body.Content, true
	}
	return string(b), true
}

func (a *api) updateContent(w http.ResponseWriter, r *http.Request) {
	content, ok := textOrJSON(w, r)
	if !ok {
		return
	}
	raw, _ := json.Marshal(map[string]any{"document": r.PathValue("id"), "content": content})
	a.run(w, r, "update", raw)
}

func (a *api) patch(w http.ResponseWriter, r *http.Request) {
	m, err := bodyMap(r)
	if err != nil {
		writeError(w, err)
		return
	}
	force := false
	if v, ok := m["force"]; ok {
		force, _ = v.(bool)
		delete(m, "force")
	}
	if inner, ok := m["patch"].(map[string]any); ok && len(m) == 1 {
		m = inner
	}
	raw, _ := json.Marshal(map[string]any{"document": r.PathValue("id"), "patch": m, "force": force})
	a.run(w, r, "patchFrontMatter", raw)
}

func (a *api) template(w http.ResponseWriter, r *http.Request) {
	svc, _, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	t, err := svc.GetTemplate(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": t})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	io.WriteString(w, t.Content)
}

// putTemplate creates the template when it does not exist and updates it otherwise.
func (a *api) putTemplate(w http.ResponseWriter, r *http.Request) {
	svc, _, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	content, ok := textOrJSON(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	name := "createTemplate"
	if _, err := svc.GetTemplate(r.Context(), id); err == nil {
		name = "updateTemplate"
	}
	raw, _ := json.Marshal(map[string]any{"template": id, "content": content})
	a.run(w, r, name, raw)
}

func (a *api) schema(w http.ResponseWriter, r *http.Request) {
	svc, _, err := a.target(r)
	if err != nil {
		writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/schema+json")
	w.Write(svc.Schema())
}

// events streams document events as Server-Sent Events. ?kind=plan|story
// narrows to one kind and ?ids=a,b to specific documents (?plans=a,b and
// ?stories=a,b are shortcuts for both).
func (a *api) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, &service.Error{Kind: service.KindBadRequest, Message: "streaming not supported"})
		return
	}
	filter, err := eventFilter(r)
	if err != nil {
		writeError(w, err)
		return
	}
	sub := a.palace.Bus().Subscribe(filter, 64)
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ids := filter.IDs
	if ids == nil {
		ids = []string{}
	}
	fmt.Fprintf(w, "event: ready\ndata: %s\n\n", mustJSON(map[string]any{"kind": filter.Kind, "ids": ids}))
	flusher.Flush()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, mustJSON(e))
			flusher.Flush()
		}
	}
}

func eventFilter(r *http.Request) (events.Filter, error) {
	q := r.URL.Query()
	f := events.Filter{}
	if k := strings.TrimSpace(q.Get("kind")); k != "" {
		kk, ok := kind.Get(k)
		if !ok {
			return f, &service.Error{Kind: service.KindBadRequest, Message: fmt.Sprintf("unknown document kind %q", k)}
		}
		f.Kind = kk.Name
	}
	if ids := strings.TrimSpace(q.Get("ids")); ids != "" {
		f.IDs = splitList(ids)
	}
	for _, k := range kind.All() {
		if list := strings.TrimSpace(q.Get(k.Plural)); list != "" {
			if f.Kind != "" && f.Kind != k.Name {
				return f, &service.Error{Kind: service.KindBadRequest, Message: "kind and " + k.Plural + " disagree"}
			}
			f.Kind = k.Name
			f.IDs = append(f.IDs, splitList(list)...)
		}
	}
	return f, nil
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ",") {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func writeResult(w http.ResponseWriter, status int, res *ops.Result) {
	body := map[string]any{"ok": true, "result": res.Value}
	if res.Warning != nil {
		body["warning"] = res.Warning
	}
	writeJSON(w, status, body)
}

func writeError(w http.ResponseWriter, err error) {
	e := service.AsError(err)
	writeJSON(w, e.HTTPStatus(), map[string]any{"ok": false, "error": e})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
