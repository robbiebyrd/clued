// Package memsession is an in-memory session.Store. It mirrors the MongoDB
// adapter's semantics (unique keys, projections, orderings, the session_full
// join) and backs the use-case tests.
package memsession

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	sessions        = "sessions"
	hookEvents      = "hook_events"
	transcriptLines = "transcript_lines"
	subagentLines   = "subagent_lines"
	blobs           = "blobs"
	otelEvents      = "otel_events"

	maxSessions = 500
)

// Store keeps every collection as a slice of documents guarded by one mutex.
type Store struct {
	mu     sync.Mutex
	nextID int
	cols   map[string][]session.Doc
}

var _ session.Store = (*Store)(nil)

// New returns an empty store.
func New() *Store { return &Store{cols: map[string][]session.Doc{}} }

// DeleteSessions deletes the sessions the query selects with their
// documents, oldest first under a limit, and orphaned captures when asked.
func (s *Store) DeleteSessions(_ context.Context, q session.ForgetQuery) (session.Forgotten, error) {
	out := session.Forgotten{Sessions: []string{}}
	if q.AccountID == "" {
		return out, session.ErrNoAccount
	}
	if q.Empty() {
		return out, nil
	}
	var re *regexp.Regexp
	if q.ProjectPathRe != "" {
		var err error
		if re, err = regexp.Compile("(?i)" + q.ProjectPathRe); err != nil {
			return out, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := map[string]bool{}
	if re == nil && q.Before.IsZero() {
		for _, id := range q.SessionIDs {
			ids[id] = true
		}
	} else {
		inSessions := setOf(q.SessionIDs)
		var found []session.Doc
		for _, d := range s.cols[sessions] {
			if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
				continue
			}
			if re != nil && !re.MatchString(strOf(d, "project_path")) {
				continue
			}
			if !q.Before.IsZero() && !timeOf(d, "last_seen").Before(q.Before) {
				continue
			}
			found = append(found, d)
		}
		sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "last_seen").Before(timeOf(found[j], "last_seen")) })
		if q.Limit > 0 && len(found) > q.Limit {
			found, out.More = found[:q.Limit], true
		}
		for _, d := range found {
			ids[strOf(d, "session_id")] = true
		}
		if q.Orphans && re == nil {
			if s.orphans(q, inSessions, ids) {
				out.More = true
			}
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	for name, docs := range s.cols {
		kept := docs[:0]
		for _, d := range docs {
			if owned(d, q.AccountID) && ids[strOf(d, "session_id")] {
				out.Documents++
				continue
			}
			kept = append(kept, d)
		}
		s.cols[name] = kept
	}
	for id := range ids {
		out.Sessions = append(out.Sessions, id)
	}
	sort.Strings(out.Sessions)
	return out, nil
}

// orphans adds to ids, per capture collection, up to q.Limit session ids
// (all with no limit) of the account's documents created before q.Before
// (timestamp for OpenTelemetry events) whose session has no record, oldest
// first; only the ids in inSessions when it is not empty. It reports
// whether a collection held more. Callers hold s.mu.
func (s *Store) orphans(q session.ForgetQuery, inSessions, ids map[string]bool) bool {
	recorded := map[string]bool{}
	for _, d := range s.cols[sessions] {
		if owned(d, q.AccountID) {
			recorded[strOf(d, "session_id")] = true
		}
	}
	more := false
	for _, col := range []string{hookEvents, transcriptLines, subagentLines, blobs, otelEvents} {
		field := "created_at"
		if col == otelEvents {
			field = "timestamp"
		}
		first := map[string]time.Time{}
		for _, d := range s.cols[col] {
			sid := strOf(d, "session_id")
			if sid == "" || !owned(d, q.AccountID) || recorded[sid] || (len(inSessions) > 0 && !inSessions[sid]) {
				continue
			}
			at := timeOf(d, field)
			if at.IsZero() || !at.Before(q.Before) {
				continue
			}
			if prev, seen := first[sid]; !seen || at.Before(prev) {
				first[sid] = at
			}
		}
		found := make([]string, 0, len(first))
		for sid := range first {
			found = append(found, sid)
		}
		sort.Slice(found, func(i, j int) bool {
			if a, b := first[found[i]], first[found[j]]; !a.Equal(b) {
				return a.Before(b)
			}
			return found[i] < found[j]
		})
		if q.Limit > 0 && len(found) > q.Limit {
			found, more = found[:q.Limit], true
		}
		for _, sid := range found {
			ids[sid] = true
		}
	}
	return more
}

// PurgeContent deletes the selected sessions' lines and blobs and strips
// their hook events (session.WithoutContent); session records stay.
func (s *Store) PurgeContent(_ context.Context, accountID string, sessionIDs []string) (session.ContentPurged, error) {
	var out session.ContentPurged
	if accountID == "" {
		return out, session.ErrNoAccount
	}
	ids := setOf(sessionIDs)
	selected := func(d session.Doc) bool {
		return owned(d, accountID) && (len(ids) == 0 || ids[strOf(d, "session_id")])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, name := range []string{transcriptLines, subagentLines, blobs} {
		docs := s.cols[name]
		kept := docs[:0]
		for _, d := range docs {
			if selected(d) {
				out.Deleted++
				continue
			}
			kept = append(kept, d)
		}
		s.cols[name] = kept
	}
	for i, d := range s.cols[hookEvents] {
		if !selected(d) {
			continue
		}
		if stripped, changed := session.WithoutContent(d); changed {
			s.cols[hookEvents][i] = stripped
			out.Stripped++
		}
	}
	for _, d := range s.cols[sessions] {
		if !selected(d) {
			continue
		}
		changed := false
		for _, f := range session.SessionContentFields {
			if _, ok := d[f]; ok {
				delete(d, f)
				changed = true
			}
		}
		if changed {
			out.Sessions++
		}
	}
	return out, nil
}

// DeleteAccount drops every document of every collection stamped with the account.
func (s *Store) DeleteAccount(_ context.Context, accountID string) (int64, error) {
	if accountID == "" {
		return 0, session.ErrNoAccount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for col, docs := range s.cols {
		kept := docs[:0]
		for _, d := range docs {
			if owned(d, accountID) {
				n++
				continue
			}
			kept = append(kept, d)
		}
		s.cols[col] = kept
	}
	return n, nil
}

// Close is a no-op; the store has nothing to release.
func (s *Store) Close(context.Context) error { return nil }

// --- storage primitives (callers hold s.mu) ---

func (s *Store) insert(col string, d session.Doc) {
	s.nextID++
	d["_id"] = fmt.Sprint(s.nextID)
	s.cols[col] = append(s.cols[col], d)
}

func (s *Store) find(col string, match func(session.Doc) bool) session.Doc {
	for _, d := range s.cols[col] {
		if match(d) {
			return d
		}
	}
	return nil
}

func (s *Store) filter(col string, match func(session.Doc) bool) []session.Doc {
	var out []session.Doc
	for _, d := range s.cols[col] {
		if match(d) {
			out = append(out, d)
		}
	}
	return out
}

// upsert applies set to the document matching key, or inserts a new one made
// of key, set and onInsert.
func (s *Store) upsert(col string, key, set, onInsert session.Doc) {
	d := s.find(col, func(d session.Doc) bool { return hasFields(d, key) })
	if d == nil {
		d = session.Doc{}
		for k, v := range key {
			d[k] = v
		}
		for k, v := range onInsert {
			d[k] = deepCopy(v)
		}
		s.insert(col, d)
	}
	for k, v := range set {
		d[k] = deepCopy(v)
	}
}

func hasFields(d, fields session.Doc) bool {
	for k, v := range fields {
		if d[k] != v {
			return false
		}
	}
	return true
}

// --- copying and shaping ---

func deepCopy(v any) any {
	switch x := v.(type) {
	case session.Doc:
		return copyDoc(x)
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = deepCopy(e)
		}
		return out
	case []session.Doc:
		out := make([]session.Doc, len(x))
		for i, e := range x {
			out[i] = copyDoc(e)
		}
		return out
	case []string:
		return append([]string(nil), x...)
	case []int8:
		return append([]int8(nil), x...)
	}
	return v
}

func copyDoc(d session.Doc) session.Doc {
	out := make(session.Doc, len(d))
	for k, v := range d {
		out[k] = deepCopy(v)
	}
	return out
}

// without returns a copy of d minus the named fields.
func without(d session.Doc, fields ...string) session.Doc {
	out := copyDoc(d)
	for _, f := range fields {
		delete(out, f)
	}
	return out
}

// project returns a copy of d holding only the named fields that are present.
func project(d session.Doc, fields ...string) session.Doc {
	out := session.Doc{}
	for _, f := range fields {
		if v, ok := d[f]; ok {
			out[f] = deepCopy(v)
		}
	}
	return out
}

func hostDoc(h *session.HostInfo) session.Doc {
	var ip, mac any
	if h.IP != nil {
		ip = *h.IP
	}
	if h.MAC != nil {
		mac = *h.MAC
	}
	return session.Doc{
		"hostname": h.Hostname, "ip": ip, "mac": mac, "username": h.Username, "uid": h.UID,
		"platform": h.Platform, "arch": h.Arch, "os_release": h.OSRelease, "os_type": h.OSType,
	}
}

// present adds the string fields whose value is not empty.
func present(d session.Doc, fields map[string]string) {
	for k, v := range fields {
		if v != "" {
			d[k] = v
		}
	}
}

// --- sorting and matching ---

func timeOf(d session.Doc, key string) time.Time {
	t, _ := d[key].(time.Time)
	return t
}

func intOf(d session.Doc, key string) int {
	n, _ := d[key].(int)
	return n
}

// numOf reads a number of any type a stored or decoded document holds.
func numOf(d session.Doc, key string) int64 {
	switch n := d[key].(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func strOf(d session.Doc, key string) string {
	s, _ := d.String(key)
	return s
}

// compileRe compiles a case-insensitive regex; nil when pattern is empty.
func compileRe(pattern string) (*regexp.Regexp, error) {
	if pattern == "" {
		return nil, nil
	}
	return regexp.Compile("(?i)" + pattern)
}

func matches(re *regexp.Regexp, d session.Doc, keys ...string) bool {
	if re == nil {
		return true
	}
	for _, k := range keys {
		if s, ok := d.String(k); ok && re.MatchString(s) {
			return true
		}
	}
	return false
}

func owned(d session.Doc, accountID string) bool { return strOf(d, "account_id") == accountID }

// page applies offset and limit; a limit of zero or less means no limit.
func page(docs []session.Doc, offset, limit int) []session.Doc {
	if offset >= len(docs) {
		return []session.Doc{}
	}
	docs = docs[offset:]
	if limit > 0 && limit < len(docs) {
		docs = docs[:limit]
	}
	return docs
}

// copies returns copies of docs with the named fields removed.
func copies(docs []session.Doc, drop ...string) []session.Doc {
	out := make([]session.Doc, len(docs))
	for i, d := range docs {
		out[i] = without(d, drop...)
	}
	return out
}

// --- sessions ---

func (s *Store) UpsertSession(_ context.Context, in session.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := session.Doc{"session_id": in.SessionID, "last_seen": in.LastSeen}
	present(set, map[string]string{
		"transcript_path": in.TranscriptPath, "cwd": in.Cwd, "project_path": in.ProjectPath,
		"git_origin": in.GitOrigin, "git_branch": in.GitBranch, "account_id": in.AccountID,
		"project_slug": session.ProjectSlugOf(in),
	})
	if in.Host != nil {
		set["host"] = hostDoc(in.Host)
	}
	s.upsert(sessions, session.Doc{"session_id": in.SessionID}, set, session.Doc{"started_at": in.LastSeen})
	return nil
}

func (s *Store) TouchSession(_ context.Context, accountID, sessionID string, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.ownedSession(accountID, sessionID); d != nil {
		d["last_seen"] = at
	}
	return nil
}

func (s *Store) SetGitInfoIfMissing(_ context.Context, sessionID, origin, branch string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.find(sessions, func(d session.Doc) bool { return d["session_id"] == sessionID })
	if d == nil || d["git_origin"] != nil {
		return nil
	}
	d["git_origin"] = origin
	if slug := session.ProjectSlugOf(session.Session{GitOrigin: origin}); slug != "" {
		d["project_slug"] = slug
	}
	if branch != "" {
		d["git_branch"] = branch
	}
	return nil
}

func (s *Store) SetModel(_ context.Context, accountID, sessionID, model string, at time.Time) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if model == "" {
		return "", false, nil
	}
	d := s.ownedSession(accountID, sessionID)
	if d == nil {
		return "", false, session.ErrNotFound
	}
	previous := strOf(d, "model")
	if previous == model {
		return previous, false, nil
	}
	d["model"], d["model_changed_at"] = model, at
	return previous, true, nil
}

func (s *Store) SetSessionMeta(_ context.Context, accountID, sessionID string, fields session.Doc) error {
	if len(fields) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.ownedSession(accountID, sessionID)
	if d == nil {
		return session.ErrNotFound
	}
	for k, v := range fields {
		d[k] = deepCopy(v)
	}
	return nil
}

func (s *Store) SetLiveState(_ context.Context, accountID, sessionID, state, reason string, at time.Time) (string, bool, error) {
	if state == "" {
		return "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.ownedSession(accountID, sessionID)
	if d == nil {
		return "", false, session.ErrNotFound
	}
	previous := strOf(d, "live_state")
	if previous == state {
		return previous, false, nil
	}
	d["live_state"], d["live_state_at"], d["live_state_reason"] = state, at, reason
	return previous, true, nil
}

func (s *Store) StateChanges(_ context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	inSessions := setOf(q.SessionIDs)
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(hookEvents, func(d session.Doc) bool {
		if strOf(d, "hook_event_name") != session.StateChangedEventName || !owned(d, q.AccountID) {
			return false
		}
		if len(inSessions) > 0 && !inSessions[strOf(d, "session_id")] {
			return false
		}
		return q.Since.IsZero() || !timeOf(d, "created_at").Before(q.Since)
	})
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "created_at").After(timeOf(found[j], "created_at")) })
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		out = append(out, project(d, "session_id", "from", "to", "reason", "at", "created_at"))
	}
	return out, nil
}

func (s *Store) RejectedToolUses(_ context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	inSessions := setOf(q.SessionIDs)
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(transcriptLines, func(d session.Doc) bool {
		if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			return false
		}
		if rejected, _ := d.Map("enrichments").Map("error-flag")["rejected"].(bool); !rejected {
			return false
		}
		return q.Since.IsZero() || !timeOf(d, "created_at").Before(q.Since)
	})
	sort.SliceStable(found, func(i, j int) bool {
		ti, tj := timeOf(found[i], "created_at"), timeOf(found[j], "created_at")
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return intOf(found[i], "seq") > intOf(found[j], "seq")
	})
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		p := project(d, "session_id", "seq", "created_at")
		p["enrichments"] = session.Doc{"error-flag": deepCopy(d.Map("enrichments")["error-flag"])}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) LastModelBefore(_ context.Context, sessionID string, seq int, at time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	best, bestSeq := "", -1
	for _, d := range s.filter(transcriptLines, func(d session.Doc) bool { return strOf(d, "session_id") == sessionID }) {
		lineSeq := intOf(d, "seq")
		if seq >= 0 && lineSeq >= seq || !at.IsZero() && timeOf(d, "created_at").After(at) || lineSeq <= bestSeq {
			continue
		}
		if model := session.LineModel(d); model != "" {
			best, bestSeq = model, lineSeq
		}
	}
	return best, nil
}

func (s *Store) ownedSession(accountID, sessionID string) session.Doc {
	return s.find(sessions, func(d session.Doc) bool { return d["session_id"] == sessionID && owned(d, accountID) })
}

func (s *Store) GetSession(_ context.Context, accountID, sessionID string) (session.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.ownedSession(accountID, sessionID)
	if d == nil {
		return nil, session.ErrNotFound
	}
	return without(d, "_id"), nil
}

func (s *Store) FindSessions(_ context.Context, q session.SessionQuery) ([]session.Doc, error) {
	projectRe, err := compileRe(q.ProjectPathRe)
	if err != nil {
		return nil, err
	}
	originRe, err := compileRe(q.GitOriginRe)
	if err != nil {
		return nil, err
	}
	queryRe, err := compileRe(q.QueryRe)
	if err != nil {
		return nil, err
	}
	titleRe, err := compileRe(q.TitleRe)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	inStates := setOf(q.LiveStates)
	inSlugs := setOf(q.ProjectSlugs)
	found := s.filter(sessions, func(d session.Doc) bool {
		if len(inStates) > 0 && !inStates[strOf(d, "live_state")] {
			return false
		}
		if len(inSlugs) > 0 && !inSlugs[strOf(d, "project_slug")] {
			return false
		}
		if !matches(titleRe, d, "title") {
			return false
		}
		lastSeen := timeOf(d, "last_seen")
		if !q.LastSeenFrom.IsZero() && lastSeen.Before(q.LastSeenFrom) || !q.LastSeenBefore.IsZero() && !lastSeen.Before(q.LastSeenBefore) {
			return false
		}
		return owned(d, q.AccountID) && matches(projectRe, d, "project_path") &&
			matches(originRe, d, "git_origin") && matches(queryRe, d, "project_path", "cwd")
	})
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "last_seen").After(timeOf(found[j], "last_seen")) })

	limit := q.Limit
	if limit <= 0 || limit > maxSessions {
		limit = maxSessions
	}
	out := []session.Doc{}
	for _, d := range page(found, 0, limit) {
		out = append(out, project(d, "session_id", "project_path", "git_origin", "project_slug", "cwd", "model", "live_state", "live_state_at", "live_state_reason",
			"title", "title_kind", "slug", "permission_mode", "pr_link", "worktree", "entrypoint", "claude_version", "started_at", "last_seen"))
	}
	return out, nil
}

// --- hook events ---

func (s *Store) InsertHookEvent(_ context.Context, ev session.Doc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if hash := memoryHash(ev); hash != "" {
		acc := strOf(ev, "account_id")
		if dup := s.find(hookEvents, func(d session.Doc) bool { return strOf(d, "account_id") == acc && memoryHash(d) == hash }); dup != nil {
			return nil
		}
	}
	s.insert(hookEvents, copyDoc(ev))
	return nil
}

// memoryHash is the hash of a MemorySaved event, empty for anything else.
func memoryHash(d session.Doc) string {
	if strOf(d, "hook_event_name") != session.MemoryEventName {
		return ""
	}
	return strOf(d.Map("memory"), "hash")
}

func (s *Store) MemoryEvents(_ context.Context, q session.MemoryQuery) ([]session.Doc, error) {
	re, err := compileRe(q.PatternRe)
	if err != nil {
		return nil, err
	}
	inSessions := map[string]bool{}
	for _, id := range q.SessionIDs {
		inSessions[id] = true
	}
	inKinds := map[string]bool{}
	for _, k := range q.Kinds {
		inKinds[k] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(hookEvents, func(d session.Doc) bool {
		if strOf(d, "hook_event_name") != session.MemoryEventName || !owned(d, q.AccountID) {
			return false
		}
		if len(inSessions) > 0 && !inSessions[strOf(d, "session_id")] {
			return false
		}
		mem := d.Map("memory")
		if len(inKinds) > 0 && !inKinds[strOf(mem, "kind")] {
			return false
		}
		text, isString := mem.String("text")
		return isString && (re == nil || re.MatchString(text))
	})
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "created_at").After(timeOf(found[j], "created_at")) })

	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		out = append(out, project(d, "session_id", "cwd", "memory", "created_at"))
	}
	return out, nil
}

// limitProjection is what LimitSamples returns of a LimitsSampled event.
var limitProjection = []string{"session_id", "cwd", "model", "version", "context_window", "rate_limits", "cost", "live_state", "sampled_at", "created_at"}

func (s *Store) LimitSamples(_ context.Context, q session.LimitQuery) ([]session.Doc, error) {
	inSessions := map[string]bool{}
	for _, id := range q.SessionIDs {
		inSessions[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(hookEvents, func(d session.Doc) bool {
		if strOf(d, "hook_event_name") != session.LimitsEventName || !owned(d, q.AccountID) {
			return false
		}
		if len(inSessions) > 0 && !inSessions[strOf(d, "session_id")] {
			return false
		}
		return q.Since.IsZero() || !timeOf(d, "created_at").Before(q.Since)
	})
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "created_at").After(timeOf(found[j], "created_at")) })
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		out = append(out, project(d, limitProjection...))
	}
	return out, nil
}

// --- OpenTelemetry events ---

var otelProjection = []string{"session_id", "prompt_id", "name", "sequence", "timestamp", "attributes", "redacted", "created_at"}

// InsertOtelEvents stores each event unless one with the same account,
// session, name, sequence and timestamp is stored.
func (s *Store) InsertOtelEvents(_ context.Context, events []session.OtelEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	inserted := 0
	for _, e := range events {
		at := e.Timestamp.UTC()
		if s.find(otelEvents, func(d session.Doc) bool {
			return owned(d, e.AccountID) && strOf(d, "session_id") == e.SessionID && strOf(d, "name") == e.Name &&
				numOf(d, "sequence") == e.Sequence && timeOf(d, "timestamp").Equal(at)
		}) != nil {
			continue
		}
		d := session.Doc{"session_id": e.SessionID, "name": e.Name, "sequence": e.Sequence, "timestamp": at,
			"attributes": deepCopy(e.Attributes), "created_at": time.Now()}
		present(d, map[string]string{"account_id": e.AccountID, "prompt_id": e.PromptID})
		withRedacted(d, e.Redacted)
		s.insert(otelEvents, d)
		inserted++
	}
	return inserted, nil
}

func (s *Store) OtelEvents(_ context.Context, q session.OtelQuery) ([]session.Doc, error) {
	inSessions, names := setOf(q.SessionIDs), setOf(q.Names)
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(otelEvents, func(d session.Doc) bool {
		switch {
		case !owned(d, q.AccountID),
			len(inSessions) > 0 && !inSessions[strOf(d, "session_id")],
			len(names) > 0 && !names[strOf(d, "name")],
			q.PromptID != "" && strOf(d, "prompt_id") != q.PromptID:
			return false
		}
		return q.Since.IsZero() || !timeOf(d, "timestamp").Before(q.Since)
	})
	sort.SliceStable(found, func(i, j int) bool {
		if a, b := timeOf(found[i], "timestamp"), timeOf(found[j], "timestamp"); !a.Equal(b) {
			return a.After(b)
		}
		return numOf(found[i], "sequence") > numOf(found[j], "sequence")
	})
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		out = append(out, project(d, otelProjection...))
	}
	return out, nil
}

func (s *Store) HookEventIDsByToolUse(_ context.Context, sessionID string, toolUseIDs []string) ([]string, error) {
	wanted := map[string]bool{}
	for _, id := range toolUseIDs {
		wanted[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for _, d := range s.filter(hookEvents, func(d session.Doc) bool {
		return (sessionID == "" || d["session_id"] == sessionID) && wanted[strOf(d, "tool_use_id")]
	}) {
		ids = append(ids, d["_id"].(string))
	}
	return ids, nil
}

func (s *Store) BashEvents(_ context.Context, q session.CommandQuery) ([]session.Doc, error) {
	re, err := compileRe(q.PatternRe)
	if err != nil {
		return nil, err
	}
	inSessions := map[string]bool{}
	for _, id := range q.SessionIDs {
		inSessions[id] = true
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(hookEvents, func(d session.Doc) bool {
		if d["tool_name"] != "Bash" || !owned(d, q.AccountID) {
			return false
		}
		if len(inSessions) > 0 && !inSessions[strOf(d, "session_id")] {
			return false
		}
		cmd, isString := d.ToolInput().String("command")
		return isString && (re == nil || re.MatchString(cmd))
	})
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "created_at").After(timeOf(found[j], "created_at")) })

	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		out = append(out, project(d, "session_id", "tool_use_id", "tool_input", "created_at"))
	}
	return out, nil
}

func (s *Store) FileEvents(_ context.Context, q session.FileQuery) ([]session.Doc, error) {
	re, err := compileRe(q.PathRe)
	if err != nil {
		return nil, err
	}
	inSessions := setOf(q.SessionIDs)
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(hookEvents, func(d session.Doc) bool {
		if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			return false
		}
		e := d.Map("enrichments")
		var path string
		var ok bool
		if tracked := e.Map("file-tracker"); tracked != nil {
			path, ok = tracked.String("file_path")
		} else if e["edit-diff-stats"] != nil && strOf(d, "hook_event_name") == "PostToolUse" {
			path, ok = d.ToolInput().String("file_path")
		}
		return ok && (re == nil || re.MatchString(path))
	})
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "created_at").After(timeOf(found[j], "created_at")) })
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		p := project(d, "session_id", "hook_event_name", "tool_name", "tool_use_id", "created_at")
		if path, ok := d.ToolInput().String("file_path"); ok {
			p["tool_input"] = session.Doc{"file_path": path}
		}
		e := session.Doc{}
		for _, name := range []string{"file-tracker", "edit-diff-stats"} {
			if v, ok := d.Map("enrichments")[name]; ok {
				e[name] = deepCopy(v)
			}
		}
		p["enrichments"] = e
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) ErrorLines(_ context.Context, q session.ErrorQuery) ([]session.Doc, error) {
	inSessions, inTypes := setOf(q.SessionIDs), setOf(q.Types)
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(transcriptLines, func(d session.Doc) bool {
		if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			return false
		}
		flag := d.Map("enrichments").Map("error-flag")
		if isErr, _ := flag["is_error"].(bool); !isErr {
			return false
		}
		typ, _ := flag.String("error_type")
		return len(inTypes) == 0 || inTypes[typ]
	})
	sort.SliceStable(found, func(i, j int) bool {
		ti, tj := timeOf(found[i], "created_at"), timeOf(found[j], "created_at")
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return intOf(found[i], "seq") > intOf(found[j], "seq")
	})
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		p := project(d, "session_id", "seq", "line", "created_at")
		p["enrichments"] = session.Doc{"error-flag": deepCopy(d.Map("enrichments")["error-flag"])}
		out = append(out, p)
	}
	return out, nil
}

func (s *Store) Activities(_ context.Context, q session.ActivityQuery) ([]session.Doc, error) {
	inSessions, inKinds := setOf(q.SessionIDs), setOf(q.Kinds)
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(transcriptLines, func(d session.Doc) bool {
		if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			return false
		}
		activity := d.Map("enrichments").Map("activity-kind")
		if activity == nil {
			return false
		}
		if kind, _ := activity.String("kind"); len(inKinds) > 0 && !inKinds[kind] {
			return false
		}
		return q.Since.IsZero() || !timeOf(d, "created_at").Before(q.Since)
	})
	sort.SliceStable(found, func(i, j int) bool {
		ti, tj := timeOf(found[i], "created_at"), timeOf(found[j], "created_at")
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return intOf(found[i], "seq") > intOf(found[j], "seq")
	})
	out := []session.Doc{}
	for _, d := range page(found, 0, q.Limit) {
		p := project(d, "session_id", "seq", "created_at")
		if ts, ok := d.Line()["timestamp"]; ok {
			p["line"] = session.Doc{"timestamp": ts}
		}
		p["enrichments"] = session.Doc{"activity-kind": deepCopy(d.Map("enrichments")["activity-kind"])}
		for _, name := range []string{"tool-result-detail", "system-record"} {
			if detail, ok := d.Map("enrichments")[name]; ok {
				p["enrichments"].(session.Doc)[name] = deepCopy(detail)
			}
		}
		out = append(out, p)
	}
	return out, nil
}

func setOf(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// --- transcript, subagent lines and blobs ---

func (s *Store) UpsertTranscriptLines(_ context.Context, lines []session.TranscriptLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, l := range lines {
		set := session.Doc{"session_id": l.SessionID, "seq": l.Seq, "line": l.Line}
		present(set, map[string]string{"account_id": l.AccountID})
		if l.Host != nil {
			set["host"] = hostDoc(l.Host)
		}
		withRedacted(set, l.Redacted)
		s.upsert(transcriptLines, session.Doc{"session_id": l.SessionID, "seq": l.Seq}, set, session.Doc{"created_at": now})
	}
	return nil
}

func (s *Store) CountTranscriptLines(_ context.Context, accountID, sessionID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.filter(transcriptLines, func(d session.Doc) bool {
		return d["session_id"] == sessionID && owned(d, accountID)
	})), nil
}

func (s *Store) TranscriptLines(_ context.Context, q session.LineQuery) ([]session.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(transcriptLines, func(d session.Doc) bool {
		return d["session_id"] == q.SessionID && (q.AccountID == "" || owned(d, q.AccountID))
	})
	sort.SliceStable(found, func(i, j int) bool {
		if q.Descending {
			return intOf(found[i], "seq") > intOf(found[j], "seq")
		}
		return intOf(found[i], "seq") < intOf(found[j], "seq")
	})
	return copies(page(found, q.Offset, q.Limit), "_id"), nil
}

func (s *Store) UpsertSubagentLine(_ context.Context, l session.SubagentLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := session.Doc{"session_id": l.SessionID, "subagent_id": l.SubagentID, "seq": l.Seq, "line": l.Line}
	present(set, map[string]string{"account_id": l.AccountID})
	if l.Host != nil {
		set["host"] = hostDoc(l.Host)
	}
	withRedacted(set, l.Redacted)
	key := session.Doc{"session_id": l.SessionID, "subagent_id": l.SubagentID, "seq": l.Seq}
	s.upsert(subagentLines, key, set, session.Doc{"created_at": time.Now()})
	return nil
}

func (s *Store) subagentLines(accountID, sessionID string) []session.Doc {
	found := s.filter(subagentLines, func(d session.Doc) bool {
		return d["session_id"] == sessionID && owned(d, accountID)
	})
	sortSubagentLines(found)
	return found
}

func sortSubagentLines(docs []session.Doc) {
	sort.SliceStable(docs, func(i, j int) bool {
		if a, b := strOf(docs[i], "subagent_id"), strOf(docs[j], "subagent_id"); a != b {
			return a < b
		}
		return intOf(docs[i], "seq") < intOf(docs[j], "seq")
	})
}

func (s *Store) SubagentIDs(_ context.Context, accountID, sessionID string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := []string{}
	for _, d := range s.subagentLines(accountID, sessionID) {
		if id := strOf(d, "subagent_id"); len(ids) == 0 || ids[len(ids)-1] != id {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *Store) SubagentLines(_ context.Context, accountID, sessionID, subagentID string) ([]session.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var found []session.Doc
	for _, d := range s.subagentLines(accountID, sessionID) {
		if d["subagent_id"] == subagentID {
			found = append(found, d)
		}
	}
	return copies(found, "_id"), nil
}

// withRedacted records the redaction rules that fired, as a list of
// strings like the one MongoDB stores.
func withRedacted(set session.Doc, rules []string) {
	if len(rules) > 0 {
		list := make([]any, len(rules))
		for i, r := range rules {
			list[i] = r
		}
		set["redacted"] = list
	}
}

func (s *Store) UpsertBlob(_ context.Context, b session.Blob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := session.Doc{"session_id": b.SessionID, "blob_type": b.BlobType, "name": b.Name, "content": b.Content, "encoding": b.Encoding}
	present(set, map[string]string{"account_id": b.AccountID})
	withRedacted(set, b.Redacted)
	key := session.Doc{"session_id": b.SessionID, "blob_type": b.BlobType, "name": b.Name}
	s.upsert(blobs, key, set, session.Doc{"created_at": time.Now()})
	return nil
}

func (s *Store) Blobs(_ context.Context, accountID, sessionID string) ([]session.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return copies(s.sortedBlobs(sessionID, func(d session.Doc) bool { return owned(d, accountID) }), "_id"), nil
}

func (s *Store) sortedBlobs(sessionID string, keep func(session.Doc) bool) []session.Doc {
	found := s.filter(blobs, func(d session.Doc) bool { return d["session_id"] == sessionID && keep(d) })
	sort.SliceStable(found, func(i, j int) bool {
		if a, b := strOf(found[i], "blob_type"), strOf(found[j], "blob_type"); a != b {
			return a < b
		}
		return strOf(found[i], "name") < strOf(found[j], "name")
	})
	return found
}

// --- session_full ---

// FullSession is the session_full view: the session joined with its
// transcript lines, subagent lines, blobs (without content) and hook events.
func (s *Store) FullSession(_ context.Context, accountID, sessionID string) (session.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	base := s.ownedSession(accountID, sessionID)
	if base == nil {
		return nil, session.ErrNotFound
	}
	forSession := func(d session.Doc) bool { return d["session_id"] == sessionID }

	lines := s.filter(transcriptLines, forSession)
	sort.SliceStable(lines, func(i, j int) bool { return intOf(lines[i], "seq") < intOf(lines[j], "seq") })
	agents := s.filter(subagentLines, forSession)
	sortSubagentLines(agents)
	events := s.filter(hookEvents, forSession)
	sort.SliceStable(events, func(i, j int) bool { return timeOf(events[i], "created_at").Before(timeOf(events[j], "created_at")) })

	full := without(base, "_id")
	full["transcript_lines"] = copies(lines, "_id", "session_id", "account_id", "host")
	full["subagent_lines"] = copies(agents, "_id", "session_id", "account_id", "host")
	full["blobs"] = copies(s.sortedBlobs(sessionID, func(session.Doc) bool { return true }), "_id", "session_id", "account_id", "content")
	full["hook_events"] = copies(events, "_id", "session_id", "account_id")
	return full, nil
}

// --- enrichment ---

func (s *Store) Unenriched(_ context.Context, collection, enricher string, limit int) ([]session.Doc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.filter(collection, func(d session.Doc) bool {
		e := d.Map("enrichments")
		return e[enricher] == nil && e[enricher+"_failed"] == nil && e[enricher+"_skipped"] == nil
	})
	return copies(page(pending, 0, limit)), nil
}

func (s *Store) SetEnrichment(_ context.Context, collection string, id any, enricher string, result any) error {
	return s.setEnrichmentField(collection, id, enricher, result)
}

func (s *Store) SetEnrichmentFailure(_ context.Context, collection string, id any, enricher, message string, at time.Time) error {
	return s.setEnrichmentField(collection, id, enricher+"_failed", session.Doc{"message": message, "at": at})
}

func (s *Store) SetEnrichmentSkipped(_ context.Context, collection string, id any, enricher string) error {
	return s.setEnrichmentField(collection, id, enricher+"_skipped", true)
}

func (s *Store) RequeueSkipped(_ context.Context, collection, accountID, enricher string) (int64, error) {
	if accountID == "" {
		return 0, session.ErrNoAccount
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for _, d := range s.cols[collection] {
		if e := d.Map("enrichments"); owned(d, accountID) && e != nil {
			if _, ok := e[enricher+"_skipped"]; ok {
				delete(e, enricher+"_skipped")
				n++
			}
		}
	}
	return n, nil
}

// setEnrichmentField writes enrichments.<field> on the document with _id id;
// a missing document is ignored, as an update that matches nothing is.
func (s *Store) setEnrichmentField(collection string, id any, field string, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.find(collection, func(d session.Doc) bool { return d["_id"] == id })
	if d == nil {
		return nil
	}
	e := d.Map("enrichments")
	if e == nil {
		e = session.Doc{}
		d["enrichments"] = e
	}
	e[field] = storable(value)
	return nil
}

// storable is how the store keeps an enrichment result: documents and plain
// values as they are, anything else (an enricher's result struct) as the
// document its JSON encodes, the way MongoDB keeps the BSON of the struct,
// so reads find the same fields in both stores.
func storable(v any) any {
	switch e := v.(type) {
	case session.Embedding:
		return embeddingDoc(e)
	case *session.Embedding:
		return embeddingDoc(*e)
	}
	switch v.(type) {
	case nil, bool, string, int, int32, int64, float64, time.Time, session.Doc, map[string]any, []any:
		return deepCopy(v)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return deepCopy(v)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return deepCopy(v)
	}
	if m, ok := out.(map[string]any); ok {
		return session.Doc(m)
	}
	return out
}

// hourCounts counts the account's transcript lines that keep returns a
// kind for, per UTC hour of their LineTime.
func (s *Store) hourCounts(q session.CountQuery, kindOf func(session.Doc) (string, bool)) []session.HourCount {
	type key struct {
		hour time.Time
		kind string
	}
	counts := map[key]int64{}
	s.mu.Lock()
	for _, d := range s.cols[transcriptLines] {
		if !owned(d, q.AccountID) {
			continue
		}
		kind, ok := kindOf(d)
		if !ok {
			continue
		}
		at := session.LineTime(d)
		if !q.InRange(at) {
			continue
		}
		counts[key{at.Truncate(time.Hour), kind}]++
	}
	s.mu.Unlock()
	out := make([]session.HourCount, 0, len(counts))
	for k, n := range counts {
		out = append(out, session.HourCount{Hour: k.hour, Kind: k.kind, Count: n})
	}
	session.SortHourCounts(out)
	return out
}

func (s *Store) ActivityCounts(_ context.Context, q session.CountQuery) ([]session.HourCount, error) {
	return s.hourCounts(q, func(d session.Doc) (string, bool) {
		return d.Map("enrichments").Map("activity-kind").String("kind")
	}), nil
}

func (s *Store) ErrorCounts(_ context.Context, q session.CountQuery) ([]session.HourCount, error) {
	return s.hourCounts(q, func(d session.Doc) (string, bool) {
		flag := d.Map("enrichments").Map("error-flag")
		if isErr, _ := flag["is_error"].(bool); !isErr {
			return "", false
		}
		typ, _ := flag.String("error_type")
		return typ, true
	}), nil
}

func (s *Store) LimitBuckets(_ context.Context, q session.CountQuery) ([]session.LimitBucket, error) {
	buckets := map[time.Time]*session.LimitBucket{}
	s.mu.Lock()
	for _, d := range s.cols[hookEvents] {
		if !owned(d, q.AccountID) || strOf(d, "hook_event_name") != session.LimitsEventName {
			continue
		}
		at := timeOf(d, "created_at").UTC()
		if !q.InRange(at) {
			continue
		}
		hour := at.Truncate(time.Hour)
		b := buckets[hour]
		if b == nil {
			b = &session.LimitBucket{Hour: hour, FiveHourMax: -1, SevenDayMax: -1, ContextMax: -1, CostUSDMax: -1}
			buckets[hour] = b
		}
		b.Samples++
		limits := d.Map("rate_limits")
		for _, f := range []struct {
			into *float64
			v    any
		}{
			{&b.FiveHourMax, limits.Map("five_hour")["used_percentage"]},
			{&b.SevenDayMax, limits.Map("seven_day")["used_percentage"]},
			{&b.ContextMax, d.Map("context_window")["used_percentage"]},
			{&b.CostUSDMax, d.Map("cost")["total_cost_usd"]},
		} {
			if n, ok := number(f.v); ok && (*f.into < 0 || n > *f.into) {
				*f.into = n
			}
		}
	}
	s.mu.Unlock()
	out := make([]session.LimitBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hour.Before(out[j].Hour) })
	return out, nil
}

// number reads a stored number of any numeric type.
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	}
	return 0, false
}

// embeddingDoc keeps the vector as []int8, as Mongo keeps a binary vector.
func embeddingDoc(e session.Embedding) session.Doc {
	return session.Doc{"model": e.Model, "dim": e.Dim, "v": append([]int8(nil), e.V...), "scale": float64(e.Scale)}
}

// SearchVectors scans the account's lines with an embedding of the
// query's model and length and keeps the best by cosine similarity.
func (s *Store) SearchVectors(_ context.Context, q session.VectorQuery) ([]session.Doc, error) {
	if len(q.Vector) == 0 {
		return []session.Doc{}, nil
	}
	inSessions := setOf(q.SessionIDs)
	top := session.NewTopK(session.VectorLimit(q.Limit))
	s.mu.Lock()
	for _, d := range s.cols[transcriptLines] {
		if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			continue
		}
		if !q.Since.IsZero() && timeOf(d, "created_at").Before(q.Since) {
			continue
		}
		emb := d.Map("enrichments").Map(session.EmbeddingName)
		v, _ := emb["v"].([]int8)
		if model, _ := emb.String("model"); model != q.Model || len(v) != len(q.Vector) {
			continue
		}
		p := project(d, "session_id", "seq", "created_at")
		if text, ok := d.Map("enrichments")["search-text"].(string); ok {
			p["enrichments"] = session.Doc{"search-text": text}
		}
		top.Push(session.CosineInt8(q.Vector, v), p)
	}
	s.mu.Unlock()
	return top.Sorted(), nil
}

// UsageTotals keeps, per session and message id, the largest value of each
// counter of the usage enrichment, then sums the messages by group, model
// and (with LongPromptTokens) prompt length.
func (s *Store) UsageTotals(_ context.Context, q session.UsageQuery) ([]session.UsageTotal, error) {
	inSessions := setOf(q.SessionIDs)
	type message struct{ session, id string }
	type entry struct {
		total      session.UsageTotal
		day, model string
	}
	messages := map[message]*entry{}
	order := []message{}
	s.mu.Lock()
	for _, d := range s.cols[transcriptLines] {
		u := d.Map("enrichments").Map("usage")
		id, _ := u.String("message_id")
		if u == nil || id == "" || !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			continue
		}
		day, _ := u.String("day")
		if (q.Since != "" && day < q.Since) || (q.Until != "" && day > q.Until) {
			continue
		}
		k := message{strOf(d, "session_id"), id}
		e := messages[k]
		if e == nil {
			e = &entry{day: day}
			messages[k] = e
			order = append(order, k)
		}
		if model, _ := u.String("model"); model > e.model {
			e.model = model
		}
		e.day = min(e.day, day)
		t := &e.total
		t.InputTokens = max(t.InputTokens, numOf(u, "input_tokens"))
		t.CacheCreationInputTokens = max(t.CacheCreationInputTokens, numOf(u, "cache_creation_input_tokens"))
		t.CacheCreation1hTokens = max(t.CacheCreation1hTokens, numOf(u, "cache_creation_1h_tokens"))
		t.CacheReadInputTokens = max(t.CacheReadInputTokens, numOf(u, "cache_read_input_tokens"))
		t.OutputTokens = max(t.OutputTokens, numOf(u, "output_tokens"))
		t.PeakContextTokens = max(t.PeakContextTokens, numOf(u, "context_tokens"))
	}
	s.mu.Unlock()
	type group struct {
		key, model string
		long       bool
	}
	groups := map[group]*session.UsageTotal{}
	for _, k := range order {
		e := messages[k]
		g := group{k.session, e.model, q.LongPromptTokens > 0 && e.total.PeakContextTokens > q.LongPromptTokens}
		switch q.GroupBy {
		case session.UsageByModel:
			g.key = e.model
		case session.UsageByDay:
			g.key = e.day
		}
		t := groups[g]
		if t == nil {
			t = &session.UsageTotal{Key: g.key, Model: g.model, LongPrompt: g.long}
			groups[g] = t
		}
		e.total.Messages = 1
		t.Add(e.total)
	}
	out := make([]session.UsageTotal, 0, len(groups))
	for _, t := range groups {
		out = append(out, *t)
	}
	session.SortUsage(out)
	return out, nil
}

// SearchTranscripts scores lines the way a Mongo text index with no
// language does, closely enough for one process: the query's words, "quoted
// phrases" (all of which must appear) and -excluded words, matched
// case-insensitively on word boundaries; the score is how often the
// positive words occur.
func (s *Store) SearchTranscripts(_ context.Context, q session.TextQuery) ([]session.Doc, error) {
	words, phrases, excluded := parseTextQuery(q.Query)
	if len(words) == 0 && len(phrases) == 0 {
		return []session.Doc{}, nil
	}
	inSessions := setOf(q.SessionIDs)
	type hit struct {
		doc   session.Doc
		score float64
	}
	s.mu.Lock()
	var hits []hit
	for _, d := range s.cols[transcriptLines] {
		if !owned(d, q.AccountID) || (len(inSessions) > 0 && !inSessions[strOf(d, "session_id")]) {
			continue
		}
		text, ok := d.Map("enrichments")["search-text"].(string)
		if !ok {
			continue
		}
		lower := strings.ToLower(text)
		tokens := textTokens(lower)
		if anyToken(tokens, excluded) {
			continue
		}
		matched := true
		for _, p := range phrases {
			if !strings.Contains(lower, p) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		score := 0.0
		for _, w := range words {
			score += float64(tokens[w])
		}
		for _, p := range phrases {
			for _, w := range strings.Fields(p) {
				score += float64(tokens[w])
			}
		}
		if score == 0 {
			continue
		}
		p := project(d, "session_id", "seq", "created_at")
		p["score"] = score
		p["enrichments"] = session.Doc{"search-text": text}
		hits = append(hits, hit{p, score})
	}
	s.mu.Unlock()
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return timeOf(hits[i].doc, "created_at").After(timeOf(hits[j].doc, "created_at"))
	})
	out := []session.Doc{}
	for i, h := range hits {
		if q.Limit > 0 && i == q.Limit {
			break
		}
		out = append(out, h.doc)
	}
	return out, nil
}

// parseTextQuery splits a text search into words, "phrases" and -excluded
// words, lower-cased.
func parseTextQuery(query string) (words, phrases, excluded []string) {
	query = strings.ToLower(query)
	for {
		start := strings.IndexByte(query, '"')
		if start < 0 {
			break
		}
		end := strings.IndexByte(query[start+1:], '"')
		if end < 0 {
			break
		}
		if p := strings.TrimSpace(query[start+1 : start+1+end]); p != "" {
			phrases = append(phrases, p)
		}
		query = query[:start] + " " + query[start+1+end+1:]
	}
	for _, f := range strings.Fields(query) {
		if strings.HasPrefix(f, "-") {
			for w := range textTokens(f[1:]) {
				excluded = append(excluded, w)
			}
			continue
		}
		for w := range textTokens(f) {
			words = append(words, w)
		}
	}
	return words, phrases, excluded
}

// textTokens counts the words of lower-cased text, split on anything that
// is not a letter or digit.
func textTokens(text string) map[string]int {
	out := map[string]int{}
	for _, w := range strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		out[w]++
	}
	return out
}

func anyToken(tokens map[string]int, words []string) bool {
	for _, w := range words {
		if tokens[w] > 0 {
			return true
		}
	}
	return false
}
