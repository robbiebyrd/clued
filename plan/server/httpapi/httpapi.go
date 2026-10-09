// Package httpapi exposes the plan service over HTTP/REST. Every operation is
// reachable as POST /api/v1/ops/{op} with its JSON parameters as the body;
// resource-style routes cover the common reads and writes. Responses use the
// envelope {"ok": true, "result": …, "warning"?: …} or
// {"ok": false, "error": {"kind", "message", "problems"}}.
package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/plan/events"
	"github.com/robbiebyrd/clued/plan/ops"
	"github.com/robbiebyrd/clued/plan/service"
)

// Prefix is the API root.
const Prefix = "/api/v1"

// MaxBody limits request bodies (plans are text; 8 MiB is generous).
const MaxBody = 8 << 20

// Handler returns the HTTP API handler.
func Handler(svc *service.Service, reg *ops.Registry) http.Handler {
	a := &api{svc: svc, reg: reg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+Prefix+"/ops", a.listOps)
	mux.HandleFunc("POST "+Prefix+"/ops/{op}", a.callOp)
	mux.HandleFunc("GET "+Prefix+"/ops/{op}", a.callOpGet)

	mux.HandleFunc("GET "+Prefix+"/plans", a.op("list", fromQuery))
	mux.HandleFunc("POST "+Prefix+"/plans", a.create)
	mux.HandleFunc("GET "+Prefix+"/plans/{id}", a.op("get", withPlan))
	mux.HandleFunc("GET "+Prefix+"/plans/{id}/front-matter", a.op("getFrontMatter", withPlan))
	mux.HandleFunc("GET "+Prefix+"/plans/{id}/content", a.content)
	mux.HandleFunc("PUT "+Prefix+"/plans/{id}/content", a.updateContent)
	mux.HandleFunc("GET "+Prefix+"/plans/{id}/transitions", a.op("getTransitions", withPlan))
	mux.HandleFunc("GET "+Prefix+"/plans/{id}/progress", a.op("getProgress", withPlan))
	mux.HandleFunc("GET "+Prefix+"/plans/{id}/validate", a.op("validate", withPlan))
	mux.HandleFunc("PUT "+Prefix+"/plans/{id}/status", a.op("setStatus", withPlanBody))
	mux.HandleFunc("PATCH "+Prefix+"/plans/{id}", a.patch)
	mux.HandleFunc("DELETE "+Prefix+"/plans/{id}", a.op("delete", withPlanQuery))

	mux.HandleFunc("GET "+Prefix+"/templates", a.op("listTemplates", nil))
	mux.HandleFunc("GET "+Prefix+"/templates/{id}", a.template)
	mux.HandleFunc("PUT "+Prefix+"/templates/{id}", a.putTemplate)
	mux.HandleFunc("DELETE "+Prefix+"/templates/{id}", a.op("deleteTemplate", withTemplate))

	mux.HandleFunc("GET "+Prefix+"/config", a.op("getConfig", nil))
	mux.HandleFunc("GET "+Prefix+"/schema", a.schema)
	mux.HandleFunc("GET "+Prefix+"/events", a.events)
	return mux
}

type api struct {
	svc *service.Service
	reg *ops.Registry
}

type paramsFn func(r *http.Request) (json.RawMessage, error)

func withPlan(r *http.Request) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"plan": r.PathValue("id")})
}

func withTemplate(r *http.Request) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"template": r.PathValue("id")})
}

func withPlanQuery(r *http.Request) (json.RawMessage, error) {
	m := queryMap(r)
	m["plan"] = r.PathValue("id")
	if v, ok := m["force"]; ok {
		m["force"] = isTrue(fmt.Sprint(v))
	}
	return json.Marshal(m)
}

// withPlanBody merges the JSON body with the plan id from the path.
func withPlanBody(r *http.Request) (json.RawMessage, error) {
	m, err := bodyMap(r)
	if err != nil {
		return nil, err
	}
	m["plan"] = r.PathValue("id")
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
	res, err := a.reg.Invoke(r.Context(), a.svc, name, raw)
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": a.reg.Describe(a.svc)})
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
	op, ok := a.reg.Get(r.PathValue("op"))
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
	// Accept either {"input": {...}, "template": "..."} or the bare plan input.
	if _, ok := m["input"]; !ok {
		m = map[string]any{"input": m, "template": r.URL.Query().Get("template")}
	}
	raw, _ := json.Marshal(m)
	a.run(w, r, "create", raw)
}

func (a *api) content(w http.ResponseWriter, r *http.Request) {
	content, err := a.svc.GetContent(r.Context(), r.PathValue("id"))
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

func (a *api) updateContent(w http.ResponseWriter, r *http.Request) {
	b, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return
	}
	content := string(b)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			writeError(w, &service.Error{Kind: service.KindBadRequest, Message: "expected {\"content\": \"...\"}"})
			return
		}
		content = body.Content
	}
	raw, _ := json.Marshal(map[string]any{"plan": r.PathValue("id"), "content": content})
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
	raw, _ := json.Marshal(map[string]any{"plan": r.PathValue("id"), "patch": m, "force": force})
	a.run(w, r, "patchFrontMatter", raw)
}

func (a *api) template(w http.ResponseWriter, r *http.Request) {
	t, err := a.svc.GetTemplate(r.Context(), r.PathValue("id"))
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
	b, err := readBody(r)
	if err != nil {
		writeError(w, err)
		return
	}
	content := string(b)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(b, &body); err != nil {
			writeError(w, &service.Error{Kind: service.KindBadRequest, Message: "expected {\"content\": \"...\"}"})
			return
		}
		content = body.Content
	}
	id := r.PathValue("id")
	name := "createTemplate"
	if _, err := a.svc.GetTemplate(r.Context(), id); err == nil {
		name = "updateTemplate"
	}
	raw, _ := json.Marshal(map[string]any{"template": id, "content": content})
	a.run(w, r, name, raw)
}

func (a *api) schema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/schema+json")
	w.Write(a.svc.Schema())
}

// events streams plan events as Server-Sent Events. ?plans=a,b narrows.
func (a *api) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, &service.Error{Kind: service.KindBadRequest, Message: "streaming not supported"})
		return
	}
	var filter []string
	if q := strings.TrimSpace(r.URL.Query().Get("plans")); q != "" {
		filter = strings.Split(q, ",")
	}
	sub := a.svc.Bus().Subscribe(filter, 64)
	defer sub.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	fmt.Fprintf(w, "event: ready\ndata: {\"plans\":%s}\n\n", mustJSON(filter))
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

var _ = events.PlanUpdated
