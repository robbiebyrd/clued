// Package memsession is an in-memory session.Store. It mirrors the MongoDB
// adapter's semantics (unique keys, projections, orderings, the session_full
// join) and backs the use-case tests.
package memsession

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	sessions        = "sessions"
	hookEvents      = "hook_events"
	transcriptLines = "transcript_lines"
	subagentLines   = "subagent_lines"
	blobs           = "blobs"

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
	if branch != "" {
		d["git_branch"] = branch
	}
	return nil
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

	s.mu.Lock()
	defer s.mu.Unlock()
	found := s.filter(sessions, func(d session.Doc) bool {
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
		out = append(out, project(d, "session_id", "project_path", "git_origin", "cwd", "started_at", "last_seen"))
	}
	return out, nil
}

// --- hook events ---

func (s *Store) InsertHookEvent(_ context.Context, ev session.Doc) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.insert(hookEvents, copyDoc(ev))
	return nil
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
		out = append(out, project(d, "session_id", "tool_input", "created_at"))
	}
	return out, nil
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

func (s *Store) UpsertBlob(_ context.Context, b session.Blob) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := session.Doc{"session_id": b.SessionID, "blob_type": b.BlobType, "name": b.Name, "content": b.Content, "encoding": b.Encoding}
	present(set, map[string]string{"account_id": b.AccountID})
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
		return e[enricher] == nil && e[enricher+"_failed"] == nil
	})
	return copies(page(pending, 0, limit)), nil
}

func (s *Store) SetEnrichment(_ context.Context, collection string, id any, enricher string, result any) error {
	return s.setEnrichmentField(collection, id, enricher, result)
}

func (s *Store) SetEnrichmentFailure(_ context.Context, collection string, id any, enricher, message string, at time.Time) error {
	return s.setEnrichmentField(collection, id, enricher+"_failed", session.Doc{"message": message, "at": at})
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
	e[field] = deepCopy(value)
	return nil
}
