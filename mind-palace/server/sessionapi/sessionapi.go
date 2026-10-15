// Package sessionapi serves the session.Store port over HTTP: one JSON route
// per port method, mounted under Prefix, so a client needs no database
// connection. Every route runs as the authenticated account that
// Options.Account resolves: writes overwrite the document's account_id with
// it and reads pass it as the query's account, whatever the request carried.
// The enrichment methods are not exposed; enrichment runs on the server.
//
// Routes (JSON in and out, in the httpapi envelope {"ok": true, "result": …}
// or {"ok": false, "error": {"kind", "message"}}):
//
//	PUT  /sessions                       UpsertSession          body: session.Session
//	POST /sessions/{id}/touch            TouchSession           body: {"at": RFC3339}
//	POST /sessions/{id}/git              SetGitInfoIfMissing    body: {"origin", "branch"}
//	POST /sessions/{id}/model            SetModel               body: {"model", "at"} → {"previous", "changed"}
//	POST /sessions/{id}/state            SetLiveState           body: {"state", "reason", "at"} → {"previous", "changed"}
//	POST /sessions/{id}/meta             SetSessionMeta         body: {"fields": {…}}
//	GET  /sessions/{id}                  GetSession
//	POST /sessions/find                  FindSessions           body: session.SessionQuery
//	POST /hook-events                    InsertHookEvent        body: [session.Doc, …], in order
//	POST /hook-events/by-tool-use        HookEventIDsByToolUse  body: {"session_id", "tool_use_ids": []}
//	POST /transcript-lines/last-model    LastModelBefore        body: {"session_id", "seq", "at"} → the model as a string
//	PUT  /transcript-lines               UpsertTranscriptLines  body: [session.TranscriptLine, …] (≤ MaxLines)
//	GET  /transcript-lines/count?session=  CountTranscriptLines
//	POST /transcript-lines/query         TranscriptLines        body: session.LineQuery
//	PUT  /subagent-lines                 UpsertSubagentLine     body: session.SubagentLine or [session.SubagentLine, …]
//	GET  /subagents?session=             SubagentIDs
//	POST /subagent-lines/query           SubagentLines          body: {"session_id", "subagent_id"}
//	PUT  /blobs                          UpsertBlob             body: session.Blob
//	POST /blobs/query                    Blobs                  body: {"session_id"}
//	POST /bash-events                    BashEvents             body: session.CommandQuery
//	POST /memory-events                  MemoryEvents           body: session.MemoryQuery
//	POST /memory-delete                  DeleteMemory           body: session.MemoryDelete → {"deleted"}
//	POST /file-events                    FileEvents             body: session.FileQuery
//	POST /error-lines                    ErrorLines             body: session.ErrorQuery
//	POST /transcript-search              SearchTranscripts      body: session.TextQuery
//	POST /usage-totals                   UsageTotals            body: session.UsageQuery
//	POST /sessions/delete                DeleteSessions         body: session.ForgetQuery
//	POST /limit-samples                  LimitSamples           body: session.LimitQuery
//	POST /otel-events                    InsertOtelEvents       body: [session.OtelEvent, …] (≤ MaxLines) → {"inserted"}
//	POST /otel-events/query              OtelEvents             body: session.OtelQuery
//	POST /activities                     Activities             body: session.ActivityQuery
//	POST /state-changes                  StateChanges           body: session.ActivityQuery
//	POST /rejected-tool-uses             RejectedToolUses       body: session.ActivityQuery
//	GET  /sessions/{id}/full             FullSession
//
// The query structs (SessionQuery, LineQuery, CommandQuery, MemoryQuery) use
// camel-case keys ("projectPathRe", "sessionId", "patternRe", "sessionIds",
// …); the Go field names older clients sent ("ProjectPathRe", "SessionID")
// still decode, since keys match case-insensitively. Their AccountID is
// never read. Writes answer with a small acknowledgement
// ({"session_id"}, {"inserted"}, {"upserted"}); reads answer with exactly
// what the port returns, times encoded as RFC3339Nano strings and empty lists
// as []. A hook event's created_at may be sent as an RFC3339Nano string and is
// stored as a time, as the daemon's WAL does.
//
// Errors: 401 unauthorized when no account is resolved, 400 invalid for
// malformed JSON, bad ids, an invalid regex or a transcript batch above
// MaxLines, 404 not_found for session.ErrNotFound or an unknown route, 413
// too_large for a body above MaxBody and 502 store when the store fails (so a
// client's write-ahead log keeps the event).
package sessionapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"regexp/syntax"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/server/httpapi"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

// Prefix is the root every route is registered under.
const Prefix = "/api/v1/session"

const (
	// DefaultMaxBody is the request body limit when Options.MaxBody is zero:
	// the daemon's limit, which blobs already respect.
	DefaultMaxBody int64 = 16 << 20
	// DefaultMaxLines is the transcript batch cap when Options.MaxLines is zero.
	DefaultMaxLines = 500
)

// Error kinds carried in the envelope's error.kind.
const (
	KindUnauthorized = "unauthorized"
	KindInvalid      = "invalid"
	KindNotFound     = "not_found"
	KindTooLarge     = "too_large"
	KindStore        = "store"
)

// Options configures the handler.
type Options struct {
	// Account resolves the principal's account for a request. A nil func or
	// ok == false answers 401; the real server puts its auth middleware in
	// front, so this is a safety net.
	Account func(*http.Request) (accountID string, ok bool)
	// MaxBody is the request body limit in bytes (DefaultMaxBody when zero).
	MaxBody int64
	// MaxLines caps a transcript batch (DefaultMaxLines when zero).
	MaxLines int
}

// Handler serves store under Prefix with the given options.
func Handler(store session.Store, opts Options) http.Handler {
	if opts.MaxBody <= 0 {
		opts.MaxBody = DefaultMaxBody
	}
	if opts.MaxLines <= 0 {
		opts.MaxLines = DefaultMaxLines
	}
	a := &api{store: store, opts: opts}
	mux := http.NewServeMux()
	route := func(method, path string, h handler) {
		mux.HandleFunc(method+" "+Prefix+path, a.with(h))
	}
	route("PUT", "/sessions", a.upsertSession)
	route("POST", "/sessions/find", a.findSessions)
	route("POST", "/sessions/{id}/touch", a.touchSession)
	route("POST", "/sessions/{id}/git", a.setGitInfo)
	route("POST", "/sessions/{id}/model", a.setModel)
	route("POST", "/sessions/{id}/state", a.setLiveState)
	route("POST", "/sessions/{id}/meta", a.setSessionMeta)
	route("GET", "/sessions/{id}", a.getSession)
	route("GET", "/sessions/{id}/full", a.fullSession)
	route("POST", "/hook-events", a.insertHookEvents)
	route("POST", "/hook-events/by-tool-use", a.hookEventIDsByToolUse)
	route("POST", "/transcript-lines/last-model", a.lastModelBefore)
	route("PUT", "/transcript-lines", a.upsertTranscriptLines)
	route("GET", "/transcript-lines/count", a.countTranscriptLines)
	route("POST", "/transcript-lines/query", a.transcriptLines)
	route("PUT", "/subagent-lines", a.upsertSubagentLines)
	route("GET", "/subagents", a.subagentIDs)
	route("POST", "/subagent-lines/query", a.subagentLines)
	route("PUT", "/blobs", a.upsertBlob)
	route("POST", "/blobs/query", a.blobs)
	route("POST", "/bash-events", a.bashEvents)
	route("POST", "/memory-events", a.memoryEvents)
	route("POST", "/memory-delete", a.deleteMemory)
	route("POST", "/file-events", a.fileEvents)
	route("POST", "/error-lines", a.errorLines)
	route("POST", "/transcript-search", a.searchTranscripts)
	route("POST", "/usage-totals", a.usageTotals)
	route("POST", "/sessions/delete", a.deleteSessions)
	route("POST", "/limit-samples", a.limitSamples)
	route("POST", "/otel-events", a.insertOtelEvents)
	route("POST", "/otel-events/query", a.otelEvents)
	route("POST", "/activities", a.activities)
	route("POST", "/state-changes", a.stateChanges)
	route("POST", "/rejected-tool-uses", a.rejectedToolUses)
	mux.HandleFunc(Prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, fmt.Sprintf("no route for %s %s", r.Method, r.URL.Path))
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, ok := "", false
		if a.opts.Account != nil {
			account, ok = a.opts.Account(r)
		}
		if !ok || account == "" {
			w.Header().Set("WWW-Authenticate", "Bearer")
			httpapi.WriteError(w, http.StatusUnauthorized, KindUnauthorized, "authentication required")
			return
		}
		// The mux would answer an unclean path (".." segments, doubled
		// slashes) with a redirect; an API client wants the error instead.
		if path.Clean(r.URL.Path) != r.URL.Path {
			invalid(w, fmt.Sprintf("path %q is not clean", r.URL.Path))
			return
		}
		mux.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey{}, account)))
	})
}

type api struct {
	store session.Store
	opts  Options
}

// handler is a route handler that receives the principal's account.
type handler func(w http.ResponseWriter, r *http.Request, account string)

type accountKey struct{}

func (a *api) with(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		account, _ := r.Context().Value(accountKey{}).(string)
		h(w, r, account)
	}
}

// --- request and response helpers ---

// decode reads the JSON body into v within MaxBody, answering 413 or 400
// itself; it reports whether the caller may go on.
func (a *api) decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, a.opts.MaxBody)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpapi.WriteError(w, http.StatusRequestEntityTooLarge, KindTooLarge, fmt.Sprintf("request body exceeds %d bytes", a.opts.MaxBody))
			return false
		}
		invalid(w, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func invalid(w http.ResponseWriter, message string) {
	httpapi.WriteError(w, http.StatusBadRequest, KindInvalid, message)
}

// storeError maps a port error to the envelope: ErrNotFound → 404, an
// invalid regular expression (a caller's mistake) → 400, anything else → 502.
func storeError(w http.ResponseWriter, err error) {
	var re *syntax.Error
	switch {
	case errors.Is(err, session.ErrNotFound):
		httpapi.WriteError(w, http.StatusNotFound, KindNotFound, err.Error())
	case errors.As(err, &re):
		invalid(w, "invalid regular expression: "+err.Error())
	default:
		httpapi.WriteError(w, http.StatusBadGateway, KindStore, "store: "+err.Error())
	}
}

func ok(w http.ResponseWriter, v any) { httpapi.WriteResult(w, http.StatusOK, v) }

// docs makes an empty result encode as [] rather than null.
func docs(d []session.Doc) []session.Doc {
	if d == nil {
		return []session.Doc{}
	}
	return d
}

func strs(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// writeCtx is the context for a write: a client that hangs up mid-request
// must not turn a committed write into a retry (the daemon's rule).
func writeCtx(r *http.Request) context.Context { return context.WithoutCancel(r.Context()) }

// validID reports whether id is a single safe path element: non-empty, local
// and without separators. Session, subagent and blob ids are used as file
// names by the restore and catch-up code, so they are checked here before
// they reach the store.
func validID(id string) bool {
	return id != "" && id != "." && filepath.IsLocal(id) && filepath.Base(id) == id && !strings.ContainsAny(id, `/\`)
}

// idOrInvalid validates the named id, answering 400 when it is unsafe.
func idOrInvalid(w http.ResponseWriter, what, id string) bool {
	if !validID(id) {
		invalid(w, fmt.Sprintf("%s %q is not a single safe path element", what, id))
		return false
	}
	return true
}

// sessionParam reads the ?session= query parameter.
func sessionParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.URL.Query().Get("session")
	return id, idOrInvalid(w, "session", id)
}

// --- sessions ---

func (a *api) upsertSession(w http.ResponseWriter, r *http.Request, account string) {
	var s session.Session
	if !a.decode(w, r, &s) || !idOrInvalid(w, "session_id", s.SessionID) {
		return
	}
	s.AccountID = account
	if err := a.store.UpsertSession(writeCtx(r), s); err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"session_id": s.SessionID})
}

func (a *api) touchSession(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	var body struct {
		At time.Time `json:"at"`
	}
	if !idOrInvalid(w, "session id", id) || !a.decode(w, r, &body) {
		return
	}
	if body.At.IsZero() {
		invalid(w, `"at" (RFC3339) is required`)
		return
	}
	if err := a.store.TouchSession(writeCtx(r), account, id, body.At); err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"session_id": id})
}

// setGitInfo writes the git info of a session the account owns. The port
// takes no account, so ownership is checked first; a session that is missing
// or belongs to another account is left alone, as the port leaves a missing
// session alone.
func (a *api) setGitInfo(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	var body struct {
		Origin string `json:"origin"`
		Branch string `json:"branch"`
	}
	if !idOrInvalid(w, "session id", id) || !a.decode(w, r, &body) {
		return
	}
	if body.Origin == "" {
		invalid(w, `"origin" is required`)
		return
	}
	ctx := writeCtx(r)
	if _, err := a.store.GetSession(ctx, account, id); err != nil {
		if errors.Is(err, session.ErrNotFound) {
			ok(w, map[string]any{"session_id": id})
			return
		}
		storeError(w, err)
		return
	}
	if err := a.store.SetGitInfoIfMissing(ctx, id, body.Origin, body.Branch); err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"session_id": id})
}

func (a *api) getSession(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	if !idOrInvalid(w, "session id", id) {
		return
	}
	d, err := a.store.GetSession(r.Context(), account, id)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, d)
}

func (a *api) findSessions(w http.ResponseWriter, r *http.Request, account string) {
	var q session.SessionQuery
	if !a.decode(w, r, &q) {
		return
	}
	q.AccountID = account
	found, err := a.store.FindSessions(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) fullSession(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	if !idOrInvalid(w, "session id", id) {
		return
	}
	d, err := a.store.FullSession(r.Context(), account, id)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, d)
}

// --- hook events ---

func (a *api) insertHookEvents(w http.ResponseWriter, r *http.Request, account string) {
	var events []session.Doc
	if !a.decode(w, r, &events) {
		return
	}
	for i, ev := range events {
		if ev == nil {
			invalid(w, fmt.Sprintf("hook event %d is not a JSON object", i))
			return
		}
		sid, _ := ev.String("session_id")
		// A memory record may belong to no session (a day roll-up, a
		// pinned note, a digest): it is stored under the account alone.
		if name, _ := ev.String("hook_event_name"); !(sid == "" && name == session.MemoryEventName) && !validID(sid) {
			invalid(w, fmt.Sprintf("hook event %d: session_id %q is not a single safe path element", i, sid))
			return
		}
		ev["account_id"] = account
		// A created_at sent as text becomes a time, as the daemon's WAL does.
		if s, isText := ev["created_at"].(string); isText {
			if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
				ev["created_at"] = at
			}
		}
	}
	ctx := writeCtx(r)
	for i, ev := range events {
		if err := a.store.InsertHookEvent(ctx, ev); err != nil {
			httpapi.WriteError(w, http.StatusBadGateway, KindStore, fmt.Sprintf("store: %v (inserted %d of %d hook events)", err, i, len(events)))
			return
		}
	}
	ok(w, map[string]any{"inserted": len(events)})
}

// setModel records the model serving a session the account owns.
func (a *api) setModel(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	var body struct {
		Model string    `json:"model"`
		At    time.Time `json:"at"`
	}
	if !idOrInvalid(w, "session id", id) || !a.decode(w, r, &body) {
		return
	}
	if body.Model == "" {
		invalid(w, `"model" is required`)
		return
	}
	if body.At.IsZero() {
		body.At = time.Now()
	}
	previous, changed, err := a.store.SetModel(writeCtx(r), account, id, body.Model, body.At)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"previous": previous, "changed": changed})
}

// setLiveState records the live state of a session the account owns.
func (a *api) setLiveState(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	var body struct {
		State  string    `json:"state"`
		Reason string    `json:"reason"`
		At     time.Time `json:"at"`
	}
	if !idOrInvalid(w, "session id", id) || !a.decode(w, r, &body) {
		return
	}
	if body.State == "" {
		invalid(w, `"state" is required`)
		return
	}
	if body.At.IsZero() {
		body.At = time.Now()
	}
	previous, changed, err := a.store.SetLiveState(writeCtx(r), account, id, body.State, body.Reason, body.At)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"previous": previous, "changed": changed})
}

// setSessionMeta sets sidecar fields on a session the account owns.
func (a *api) setSessionMeta(w http.ResponseWriter, r *http.Request, account string) {
	id := r.PathValue("id")
	var body struct {
		Fields session.Doc `json:"fields"`
	}
	if !idOrInvalid(w, "session id", id) || !a.decode(w, r, &body) {
		return
	}
	for k := range body.Fields {
		if !sessionMetaFields[k] {
			invalid(w, fmt.Sprintf("field %q is not session metadata", k))
			return
		}
	}
	if err := a.store.SetSessionMeta(writeCtx(r), account, id, body.Fields); err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"session_id": id})
}

// sessionMetaFields are the session fields SetSessionMeta may set over
// the transport.
var sessionMetaFields = map[string]bool{"memory": true, "title": true, "title_kind": true, "slug": true, "permission_mode": true, "pr_link": true, "worktree": true, "entrypoint": true, "claude_version": true}

func (a *api) stateChanges(w http.ResponseWriter, r *http.Request, account string) {
	var q session.ActivityQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.StateChanges(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) rejectedToolUses(w http.ResponseWriter, r *http.Request, account string) {
	var q session.ActivityQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.RejectedToolUses(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) lastModelBefore(w http.ResponseWriter, r *http.Request, _ string) {
	var body struct {
		SessionID string    `json:"session_id"`
		Seq       *int      `json:"seq"`
		At        time.Time `json:"at"`
	}
	if !a.decode(w, r, &body) || !idOrInvalid(w, "session_id", body.SessionID) {
		return
	}
	seq := -1
	if body.Seq != nil {
		seq = *body.Seq
	}
	model, err := a.store.LastModelBefore(r.Context(), body.SessionID, seq, body.At)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, model)
}

func (a *api) hookEventIDsByToolUse(w http.ResponseWriter, r *http.Request, _ string) {
	var body struct {
		SessionID  string   `json:"session_id"`
		ToolUseIDs []string `json:"tool_use_ids"`
	}
	if !a.decode(w, r, &body) {
		return
	}
	// An empty session id matches across sessions, as the port allows.
	if body.SessionID != "" && !idOrInvalid(w, "session_id", body.SessionID) {
		return
	}
	ids, err := a.store.HookEventIDsByToolUse(r.Context(), body.SessionID, body.ToolUseIDs)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, strs(ids))
}

func (a *api) bashEvents(w http.ResponseWriter, r *http.Request, account string) {
	var q session.CommandQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.BashEvents(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) limitSamples(w http.ResponseWriter, r *http.Request, account string) {
	var q session.LimitQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.LimitSamples(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) insertOtelEvents(w http.ResponseWriter, r *http.Request, account string) {
	var events []session.OtelEvent
	if !a.decode(w, r, &events) {
		return
	}
	if len(events) > a.opts.MaxLines {
		invalid(w, fmt.Sprintf("%d OpenTelemetry events exceed the batch limit of %d", len(events), a.opts.MaxLines))
		return
	}
	for i := range events {
		if !validID(events[i].SessionID) {
			invalid(w, fmt.Sprintf("OpenTelemetry event %d: session_id %q is not a single safe path element", i, events[i].SessionID))
			return
		}
		if events[i].Name == "" {
			invalid(w, fmt.Sprintf("OpenTelemetry event %d has no name", i))
			return
		}
		events[i].AccountID = account
	}
	n, err := a.store.InsertOtelEvents(writeCtx(r), events)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"inserted": n})
}

func (a *api) otelEvents(w http.ResponseWriter, r *http.Request, account string) {
	var q session.OtelQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.OtelEvents(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) memoryEvents(w http.ResponseWriter, r *http.Request, account string) {
	var q session.MemoryQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.MemoryEvents(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

// deleteMemory deletes memory records of the caller's account only: the
// account the body might name is never read.
func (a *api) deleteMemory(w http.ResponseWriter, r *http.Request, account string) {
	var q session.MemoryDelete
	if !a.decode(w, r, &q) {
		return
	}
	q.AccountID = account
	n, err := a.store.DeleteMemory(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"deleted": n})
}

func (a *api) fileEvents(w http.ResponseWriter, r *http.Request, account string) {
	var q session.FileQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.FileEvents(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) errorLines(w http.ResponseWriter, r *http.Request, account string) {
	var q session.ErrorQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.ErrorLines(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) activities(w http.ResponseWriter, r *http.Request, account string) {
	var q session.ActivityQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.Activities(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) searchTranscripts(w http.ResponseWriter, r *http.Request, account string) {
	var q session.TextQuery
	if !a.decode(w, r, &q) {
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	found, err := a.store.SearchTranscripts(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

func (a *api) usageTotals(w http.ResponseWriter, r *http.Request, account string) {
	var q session.UsageQuery
	if !a.decode(w, r, &q) {
		return
	}
	if !session.ValidUsageGroup(q.GroupBy) {
		invalid(w, fmt.Sprintf("groupBy %q is not session, model or day", q.GroupBy))
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	totals, err := a.store.UsageTotals(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	if totals == nil {
		totals = []session.UsageTotal{}
	}
	ok(w, totals)
}

// deleteSessions forgets the account's sessions the query selects; a query
// with no criterion is refused so a mistake cannot ask for everything.
func (a *api) deleteSessions(w http.ResponseWriter, r *http.Request, account string) {
	var q session.ForgetQuery
	if !a.decode(w, r, &q) {
		return
	}
	if q.Empty() {
		invalid(w, "name sessionIds, projectPathRe or before")
		return
	}
	for _, sid := range q.SessionIDs {
		if !idOrInvalid(w, "session id", sid) {
			return
		}
	}
	q.AccountID = account
	out, err := a.store.DeleteSessions(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, out)
}

// --- transcript lines ---

func (a *api) upsertTranscriptLines(w http.ResponseWriter, r *http.Request, account string) {
	var lines []session.TranscriptLine
	if !a.decode(w, r, &lines) {
		return
	}
	if len(lines) > a.opts.MaxLines {
		invalid(w, fmt.Sprintf("transcript batch holds %d lines; the limit is %d", len(lines), a.opts.MaxLines))
		return
	}
	for i := range lines {
		if !idOrInvalid(w, fmt.Sprintf("line %d session_id", i), lines[i].SessionID) {
			return
		}
		lines[i].AccountID = account
	}
	if err := a.store.UpsertTranscriptLines(writeCtx(r), lines); err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"upserted": len(lines)})
}

func (a *api) countTranscriptLines(w http.ResponseWriter, r *http.Request, account string) {
	id, valid := sessionParam(w, r)
	if !valid {
		return
	}
	n, err := a.store.CountTranscriptLines(r.Context(), account, id)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, n)
}

func (a *api) transcriptLines(w http.ResponseWriter, r *http.Request, account string) {
	var q session.LineQuery
	if !a.decode(w, r, &q) || !idOrInvalid(w, "sessionId", q.SessionID) {
		return
	}
	q.AccountID = account
	found, err := a.store.TranscriptLines(r.Context(), q)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

// --- subagent lines ---

// upsertSubagentLines accepts one line or an array of lines; the port takes
// one at a time, so an array is upserted in order and stops at the first error.
func (a *api) upsertSubagentLines(w http.ResponseWriter, r *http.Request, account string) {
	var raw json.RawMessage
	if !a.decode(w, r, &raw) {
		return
	}
	var lines []session.SubagentLine
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(raw, &lines); err != nil {
			invalid(w, "invalid JSON body: "+err.Error())
			return
		}
	} else {
		var one session.SubagentLine
		if err := json.Unmarshal(raw, &one); err != nil {
			invalid(w, "invalid JSON body: "+err.Error())
			return
		}
		lines = []session.SubagentLine{one}
	}
	for i := range lines {
		if !idOrInvalid(w, fmt.Sprintf("line %d session_id", i), lines[i].SessionID) ||
			!idOrInvalid(w, fmt.Sprintf("line %d subagent_id", i), lines[i].SubagentID) {
			return
		}
		lines[i].AccountID = account
	}
	ctx := writeCtx(r)
	for i, l := range lines {
		if err := a.store.UpsertSubagentLine(ctx, l); err != nil {
			httpapi.WriteError(w, http.StatusBadGateway, KindStore, fmt.Sprintf("store: %v (upserted %d of %d subagent lines)", err, i, len(lines)))
			return
		}
	}
	ok(w, map[string]any{"upserted": len(lines)})
}

func (a *api) subagentIDs(w http.ResponseWriter, r *http.Request, account string) {
	id, valid := sessionParam(w, r)
	if !valid {
		return
	}
	ids, err := a.store.SubagentIDs(r.Context(), account, id)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, strs(ids))
}

func (a *api) subagentLines(w http.ResponseWriter, r *http.Request, account string) {
	var body struct {
		SessionID  string `json:"session_id"`
		SubagentID string `json:"subagent_id"`
	}
	if !a.decode(w, r, &body) || !idOrInvalid(w, "session_id", body.SessionID) || !idOrInvalid(w, "subagent_id", body.SubagentID) {
		return
	}
	found, err := a.store.SubagentLines(r.Context(), account, body.SessionID, body.SubagentID)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}

// --- blobs ---

func (a *api) upsertBlob(w http.ResponseWriter, r *http.Request, account string) {
	var b session.Blob
	if !a.decode(w, r, &b) || !idOrInvalid(w, "session_id", b.SessionID) ||
		!idOrInvalid(w, "blob_type", b.BlobType) || !idOrInvalid(w, "name", b.Name) {
		return
	}
	b.AccountID = account
	if err := a.store.UpsertBlob(writeCtx(r), b); err != nil {
		storeError(w, err)
		return
	}
	ok(w, map[string]any{"upserted": 1})
}

func (a *api) blobs(w http.ResponseWriter, r *http.Request, account string) {
	var body struct {
		SessionID string `json:"session_id"`
	}
	if !a.decode(w, r, &body) || !idOrInvalid(w, "session_id", body.SessionID) {
		return
	}
	found, err := a.store.Blobs(r.Context(), account, body.SessionID)
	if err != nil {
		storeError(w, err)
		return
	}
	ok(w, docs(found))
}
