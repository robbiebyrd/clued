// Package remotesession is the session.Store adapter over a clued server: one
// HTTP call per port method to the routes server/sessionapi serves, through
// the shared remote.Client. The query service, restore, backfill and the
// session MCP tools run unchanged on it, so a client needs no MongoDB.
//
// The server acts as the token's principal: it stamps every write with that
// account and scopes every read by it, so the accountID arguments and the
// AccountID fields of the queries are sent but have no effect there. The
// enrichment methods are not served (enrichment runs on the server) and
// return session.ErrUnsupported; Close is a no-op. A not_found answer is
// session.ErrNotFound and a 413 wraps session.ErrTooLarge; every other
// failure is the remote package's error.
//
// Results come back as JSON, so the adapter restores what the local stores
// return natively: the time fields created_at, last_seen, started_at, model_changed_at, at and
// enrichments.<name>_failed.at become time.Time, seq and host.uid become int,
// and the joined lists of a full session become []session.Doc. Transcript
// content (line, tool_input) is left exactly as stored.
package remotesession

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/remote"
	"github.com/robbiebyrd/clued/mind-palace/session"
)

// MaxLines is the largest transcript batch one request carries; the
// server's sessionapi.DefaultMaxLines. Larger batches are split.
const MaxLines = 500

// prefix is sessionapi.Prefix, the root every route is under.
const prefix = "/api/v1/session"

// Store is the remote adapter; build it with New.
type Store struct {
	c *remote.Client
}

var _ session.Store = (*Store)(nil)

// New returns the store that calls the server c talks to.
func New(c *remote.Client) session.Store { return &Store{c: c} }

// do sends one request and maps the outcome to the port's sentinels.
func (s *Store) do(ctx context.Context, method, path string, in, out any) error {
	err := s.c.Do(ctx, method, path, in, out)
	switch {
	case err == nil:
		return nil
	case remote.IsNotFound(err):
		return session.ErrNotFound
	case errors.Is(err, remote.ErrTooLarge):
		return fmt.Errorf("%w: %v", session.ErrTooLarge, err)
	}
	return err
}

// sessionPath is prefix/sessions/<id>[suffix] with the id escaped as one
// path element.
func sessionPath(id, suffix string) string {
	return prefix + "/sessions/" + url.PathEscape(id) + suffix
}

// withSession is prefix<path>?session=<id>.
func withSession(path, id string) string {
	return prefix + path + "?session=" + url.QueryEscape(id)
}

// --- sessions ---

func (s *Store) UpsertSession(ctx context.Context, in session.Session) error {
	return s.do(ctx, "PUT", prefix+"/sessions", in, nil)
}

func (s *Store) TouchSession(ctx context.Context, _ string, sessionID string, at time.Time) error {
	return s.do(ctx, "POST", sessionPath(sessionID, "/touch"), map[string]any{"at": at}, nil)
}

// SetGitInfoIfMissing sends nothing for an empty origin: there is nothing to
// write, and the server would refuse the request.
func (s *Store) SetGitInfoIfMissing(ctx context.Context, sessionID, origin, branch string) error {
	if origin == "" {
		return nil
	}
	return s.do(ctx, "POST", sessionPath(sessionID, "/git"), map[string]string{"origin": origin, "branch": branch}, nil)
}

func (s *Store) SetModel(ctx context.Context, _ string, sessionID, model string, at time.Time) (string, bool, error) {
	if model == "" {
		return "", false, nil
	}
	var out struct {
		Previous string `json:"previous"`
		Changed  bool   `json:"changed"`
	}
	if err := s.do(ctx, "POST", sessionPath(sessionID, "/model"), map[string]any{"model": model, "at": at}, &out); err != nil {
		return "", false, err
	}
	return out.Previous, out.Changed, nil
}

func (s *Store) SetSessionMeta(ctx context.Context, _ string, sessionID string, fields session.Doc) error {
	if len(fields) == 0 {
		return nil
	}
	return s.do(ctx, "POST", sessionPath(sessionID, "/meta"), map[string]any{"fields": fields}, nil)
}

func (s *Store) SetLiveState(ctx context.Context, _ string, sessionID, state, reason string, at time.Time) (string, bool, error) {
	if state == "" {
		return "", false, nil
	}
	var out struct {
		Previous string `json:"previous"`
		Changed  bool   `json:"changed"`
	}
	if err := s.do(ctx, "POST", sessionPath(sessionID, "/state"), map[string]any{"state": state, "reason": reason, "at": at}, &out); err != nil {
		return "", false, err
	}
	return out.Previous, out.Changed, nil
}

func (s *Store) StateChanges(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/state-changes", q)
}

func (s *Store) RejectedToolUses(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/rejected-tool-uses", q)
}

func (s *Store) LastModelBefore(ctx context.Context, sessionID string, seq int, at time.Time) (string, error) {
	var model string
	body := map[string]any{"session_id": sessionID, "seq": seq}
	if !at.IsZero() {
		body["at"] = at
	}
	if err := s.do(ctx, "POST", prefix+"/transcript-lines/last-model", body, &model); err != nil {
		return "", err
	}
	return model, nil
}

func (s *Store) GetSession(ctx context.Context, _ string, sessionID string) (session.Doc, error) {
	var d session.Doc
	if err := s.do(ctx, "GET", sessionPath(sessionID, ""), nil, &d); err != nil {
		return nil, err
	}
	return normalizeDoc(d), nil
}

func (s *Store) FindSessions(ctx context.Context, q session.SessionQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/sessions/find", q)
}

func (s *Store) FullSession(ctx context.Context, _ string, sessionID string) (session.Doc, error) {
	var d session.Doc
	if err := s.do(ctx, "GET", sessionPath(sessionID, "/full"), nil, &d); err != nil {
		return nil, err
	}
	return normalizeFull(d), nil
}

// --- hook events ---

func (s *Store) InsertHookEvent(ctx context.Context, ev session.Doc) error {
	return s.do(ctx, "POST", prefix+"/hook-events", []session.Doc{ev}, nil)
}

func (s *Store) HookEventIDsByToolUse(ctx context.Context, sessionID string, toolUseIDs []string) ([]string, error) {
	if toolUseIDs == nil {
		toolUseIDs = []string{}
	}
	body := map[string]any{"session_id": sessionID, "tool_use_ids": toolUseIDs}
	return s.strList(ctx, "POST", prefix+"/hook-events/by-tool-use", body)
}

func (s *Store) BashEvents(ctx context.Context, q session.CommandQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/bash-events", q)
}

func (s *Store) MemoryEvents(ctx context.Context, q session.MemoryQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/memory-events", q)
}

func (s *Store) FileEvents(ctx context.Context, q session.FileQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/file-events", q)
}

func (s *Store) ErrorLines(ctx context.Context, q session.ErrorQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/error-lines", q)
}

func (s *Store) Activities(ctx context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/activities", q)
}

func (s *Store) SearchTranscripts(ctx context.Context, q session.TextQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/transcript-search", q)
}

func (s *Store) UsageTotals(ctx context.Context, q session.UsageQuery) ([]session.UsageTotal, error) {
	var out []session.UsageTotal
	if err := s.do(ctx, "POST", prefix+"/usage-totals", q, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []session.UsageTotal{}
	}
	return out, nil
}

func (s *Store) LimitSamples(ctx context.Context, q session.LimitQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/limit-samples", q)
}

// --- transcript, subagent lines and blobs ---

// UpsertTranscriptLines sends the lines in batches of at most MaxLines, in
// order, stopping at the first failure.
func (s *Store) UpsertTranscriptLines(ctx context.Context, lines []session.TranscriptLine) error {
	for len(lines) > 0 {
		n := min(len(lines), MaxLines)
		if err := s.do(ctx, "PUT", prefix+"/transcript-lines", lines[:n], nil); err != nil {
			return err
		}
		lines = lines[n:]
	}
	return nil
}

func (s *Store) CountTranscriptLines(ctx context.Context, _ string, sessionID string) (int, error) {
	var n int
	if err := s.do(ctx, "GET", withSession("/transcript-lines/count", sessionID), nil, &n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *Store) TranscriptLines(ctx context.Context, q session.LineQuery) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/transcript-lines/query", q)
}

func (s *Store) UpsertSubagentLine(ctx context.Context, l session.SubagentLine) error {
	return s.do(ctx, "PUT", prefix+"/subagent-lines", l, nil)
}

func (s *Store) SubagentIDs(ctx context.Context, _ string, sessionID string) ([]string, error) {
	return s.strList(ctx, "GET", withSession("/subagents", sessionID), nil)
}

func (s *Store) SubagentLines(ctx context.Context, _ string, sessionID, subagentID string) ([]session.Doc, error) {
	body := map[string]string{"session_id": sessionID, "subagent_id": subagentID}
	return s.docs(ctx, "POST", prefix+"/subagent-lines/query", body)
}

func (s *Store) InsertOtelEvents(ctx context.Context, events []session.OtelEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	var out struct {
		Inserted int `json:"inserted"`
	}
	err := s.do(ctx, "POST", prefix+"/otel-events", events, &out)
	return out.Inserted, err
}

// OtelEvents restores the event's own timestamp and sequence beside the
// times every document has.
func (s *Store) OtelEvents(ctx context.Context, q session.OtelQuery) ([]session.Doc, error) {
	docs, err := s.docs(ctx, "POST", prefix+"/otel-events/query", q)
	for _, d := range docs {
		setTime(d, "timestamp")
		setInt(d, "sequence")
	}
	return docs, err
}

func (s *Store) UpsertBlob(ctx context.Context, b session.Blob) error {
	return s.do(ctx, "PUT", prefix+"/blobs", b, nil)
}

func (s *Store) Blobs(ctx context.Context, _ string, sessionID string) ([]session.Doc, error) {
	return s.docs(ctx, "POST", prefix+"/blobs/query", map[string]string{"session_id": sessionID})
}

// --- enrichment: served on the server, not through this adapter ---

func (s *Store) Unenriched(context.Context, string, string, int) ([]session.Doc, error) {
	return nil, session.ErrUnsupported
}

func (s *Store) SetEnrichment(context.Context, string, any, string, any) error {
	return session.ErrUnsupported
}

func (s *Store) SetEnrichmentFailure(context.Context, string, any, string, string, time.Time) error {
	return session.ErrUnsupported
}

func (s *Store) SetEnrichmentSkipped(context.Context, string, any, string) error {
	return session.ErrUnsupported
}

// SearchVectors is not part of the transport: semantic search runs on the
// server (the search_semantic tool of its MCP endpoint).
func (s *Store) SearchVectors(context.Context, session.VectorQuery) ([]session.Doc, error) {
	return nil, session.ErrUnsupported
}

// RequeueSkipped is the server's: the enrichment queue is not part of the
// transport.
func (s *Store) RequeueSkipped(context.Context, string, string, string) (int64, error) {
	return 0, session.ErrUnsupported
}

// DeleteSessions asks the server to forget the token's account's sessions.
// A query with no criterion deletes nothing and is not sent (the server
// refuses it).
func (s *Store) DeleteSessions(ctx context.Context, q session.ForgetQuery) (session.Forgotten, error) {
	var out session.Forgotten
	if q.Empty() {
		return session.Forgotten{Sessions: []string{}}, nil
	}
	if err := s.do(ctx, "POST", prefix+"/sessions/delete", q, &out); err != nil {
		return session.Forgotten{Sessions: []string{}}, err
	}
	if out.Sessions == nil {
		out.Sessions = []string{}
	}
	return out, nil
}

// DeleteAccount is the server operator's: the transport offers no way to
// delete an account's data.
func (s *Store) DeleteAccount(context.Context, string) (int64, error) {
	return 0, session.ErrUnsupported
}

// Close is a no-op: the client holds no connection to release.
func (s *Store) Close(context.Context) error { return nil }

// --- result decoding ---

// docs sends a request whose result is a list of documents.
func (s *Store) docs(ctx context.Context, method, path string, in any) ([]session.Doc, error) {
	var out []session.Doc
	if err := s.do(ctx, method, path, in, &out); err != nil {
		return nil, err
	}
	return normalizeDocs(out), nil
}

// strList sends a request whose result is a list of strings.
func (s *Store) strList(ctx context.Context, method, path string, in any) ([]string, error) {
	var out []string
	if err := s.do(ctx, method, path, in, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// timeKeys are the document fields the stores hold as times.
var timeKeys = []string{"created_at", "last_seen", "started_at", "model_changed_at", "live_state_at", "at"}

// joinedLists are the sub-collections a full session carries.
var joinedLists = []string{"transcript_lines", "subagent_lines", "blobs", "hook_events"}

// normalizeDocs normalises a list in place; a nil list becomes an empty one.
func normalizeDocs(docs []session.Doc) []session.Doc {
	if docs == nil {
		return []session.Doc{}
	}
	for i := range docs {
		docs[i] = normalizeDoc(docs[i])
	}
	return docs
}

// normalizeDoc restores the native types of one stored document decoded
// from JSON: its time fields, seq, host.uid and the times under enrichments.
func normalizeDoc(d session.Doc) session.Doc {
	if d == nil {
		return d
	}
	for _, k := range timeKeys {
		setTime(d, k)
	}
	setInt(d, "seq")
	if host := d.Map("host"); host != nil {
		setInt(host, "uid")
	}
	if e := d.Map("enrichments"); e != nil {
		for _, v := range e {
			if entry := session.Doc(asMap(v)); entry != nil {
				setTime(entry, "at")
			}
		}
	}
	return d
}

// normalizeFull normalises a session_full document and its joined lists,
// which become []session.Doc as the local stores return them.
func normalizeFull(d session.Doc) session.Doc {
	d = normalizeDoc(d)
	for _, k := range joinedLists {
		items, ok := d[k].([]any)
		if !ok {
			continue
		}
		list := make([]session.Doc, 0, len(items))
		for _, item := range items {
			if m := asMap(item); m != nil {
				list = append(list, normalizeDoc(session.Doc(m)))
			}
		}
		d[k] = list
	}
	return d
}

func asMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case session.Doc:
		return m
	}
	return nil
}

// setTime replaces an RFC 3339 string at key with the time it names.
func setTime(d session.Doc, key string) {
	if s, ok := d[key].(string); ok {
		if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
			d[key] = at
		}
	}
}

// setInt replaces an integral float64 at key with an int.
func setInt(d session.Doc, key string) {
	if f, ok := d[key].(float64); ok && f == float64(int(f)) {
		d[key] = int(f)
	}
}
