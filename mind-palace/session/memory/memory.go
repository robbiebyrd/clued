// Package memory is clued's native memory: the notes of each session
// (session/summary), consolidated into layers that shrink old work while
// keeping its long tail (a day, the rolling last seven days, one archive
// per month), the notes a user or agent writes (a note, a handoff for the
// next session, a pinned note injected into every session), a budgeted
// bundle the prime report injects, and the tools to read, edit, forget,
// export and import it all.
//
// Every record is a MemorySaved event in the session store, keyed by a
// stable memory.id and filed under memory.project (ProjectKey), so the
// capture, search, write-ahead log, remote transport and cloud tenancy of
// sessions carry memory too, and worktrees and machines share one memory.
// Roll-ups replace their record by id; a record the user edited
// (memory.edited) is never overwritten by an engine. Private text never
// reaches an engine or the store, and private notes are never injected or
// exported.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
	"github.com/robbiebyrd/clued/mind-palace/session/summary"
)

// The kinds of native memory records (memory.kind) and their ids:
//
//	memory-checkpoint/<session_id>       a running session's note at PreCompact
//	session-summary/<session_id>         a session's note (summary.Kind)
//	memory-day/<project>/<YYYY-MM-DD>    one UTC day
//	memory-recent/<project>              the last seven days, rebuilt daily
//	memory-archive/<project>/<YYYY-MM>   the days of a month older than the recent window
//	memory-note/<project>/<stamp>        a note written with Remember
//	memory-handoff/<project>/<stamp>     a note for the next session
//	memory-pinned/<project>              the note injected into every session
const (
	KindCheckpoint = summary.CheckpointKind
	KindDay        = "memory-day"
	KindRecent     = "memory-recent"
	KindArchive    = "memory-archive"
	KindNote       = "memory-note"
	KindHandoff    = "memory-handoff"
	KindPinned     = "memory-pinned"
)

// KindSession is the kind of a session's note.
const KindSession = summary.Kind

// NativeKinds are the kinds this package writes.
var NativeKinds = []string{KindPinned, KindHandoff, KindNote, KindSession, KindCheckpoint, KindDay, KindRecent, KindArchive}

// LegacyKinds are the kinds of the records ingested from claude-remember
// before native memory: read-only, still searchable, and injected only
// while a project has no native record.
var LegacyKinds = []string{"entry", "day", "recent", "archive", "handoff", "identity"}

// ProjectKey is the key a project's memory is filed under: the git origin
// normalised to host/owner/repo, else the cleaned project path, so the
// worktrees and clones of one repository share memory.
func ProjectKey(gitOrigin, projectPath string) string {
	return session.MemoryProjectKey(gitOrigin, projectPath)
}

// Limits bound the engine runs: at most MaxRunsPerDay engine calls a UTC
// day (zero is DefaultMaxRunsPerDay, negative no cap; past the cap the
// rest of the work falls back to the free extract engine), MinPrompts for
// a session's note (zero leaves the Summary service's own) and Timeout
// for one engine call (zero is DefaultTimeout).
type Limits struct {
	MaxRunsPerDay, MinPrompts int
	Timeout                   time.Duration
}

// Defaults of the limits.
const (
	DefaultMaxRunsPerDay = 20
	DefaultTimeout       = 3 * time.Minute
	// RecentDays is the window of the recent layer.
	RecentDays = 7
)

// Service runs and reads one account's memory.
type Service struct {
	Store     session.Store
	AccountID string
	// Summary writes the session notes; its Store and AccountID are the
	// service's.
	Summary *summary.Service
	// Redact masks what is written; nil means the built-in rules.
	Redact *redact.Redactor
	Limits Limits
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// StateDir keeps the run lock and the count of engine runs per day;
	// empty keeps both in this process only.
	StateDir string
	// TemplateDir holds day.md, recent.md and archive.md overriding the
	// built-in prompts; empty means the built-in ones.
	TemplateDir string

	// runs counts the engine runs per day when there is no StateDir.
	runs map[string]int
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) redactor() *redact.Redactor {
	if s.Redact != nil {
		return s.Redact
	}
	r, _ := redact.New(nil)
	return r
}

// clean drops private spans and masks secrets.
func (s *Service) clean(text string) string {
	text, _ = s.redactor().String(StripPrivate(text))
	return strings.TrimSpace(text)
}

// Note is one memory record as the tools show it.
type Note struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Project   string    `json:"project,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Day       string    `json:"day,omitempty"`
	Text      string    `json:"text"`
	Engine    string    `json:"engine,omitempty"`
	Model     string    `json:"model,omitempty"`
	CostUSD   float64   `json:"cost_usd,omitempty"`
	Private   bool      `json:"private,omitempty"`
	Edited    bool      `json:"edited,omitempty"`
	Legacy    bool      `json:"legacy,omitempty"`
	Hash      string    `json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// noteOf reads a stored event.
func noteOf(ev session.Doc) Note {
	mem := ev.Map("memory")
	str := func(k string) string { v, _ := mem.String(k); return v }
	n := Note{ID: str("id"), Kind: str("kind"), Project: str("project"), Day: str("day"), Text: str("text"), Engine: str("engine"), Model: str("model"), Hash: str("hash")}
	n.SessionID, _ = ev.String("session_id")
	n.CostUSD, _ = mem["cost_usd"].(float64)
	n.Private, _ = mem["private"].(bool)
	n.Edited, _ = mem["edited"].(bool)
	n.CreatedAt, _ = ev["created_at"].(time.Time)
	if n.ID == "" {
		n.Legacy = true
		n.ID = "legacy/" + n.Hash
	}
	return n
}

func notesOf(events []session.Doc) []Note {
	out := make([]Note, 0, len(events))
	for _, ev := range events {
		out = append(out, noteOf(ev))
	}
	return out
}

// records lists the project's native records of the kinds (every native
// kind when none), newest first.
func (s *Service) records(ctx context.Context, project string, includePrivate bool, kinds ...string) ([]session.Doc, error) {
	if len(kinds) == 0 {
		kinds = NativeKinds
	}
	q := session.MemoryQuery{AccountID: s.AccountID, Kinds: kinds, IncludePrivate: includePrivate}
	if project != "" {
		q.Projects = []string{project}
	}
	return s.Store.MemoryEvents(ctx, q)
}

// record builds a native record event.
func (s *Service) record(kind, id, project, text string, at time.Time, extra session.Doc) session.Doc {
	mem := session.Doc{"id": id, "kind": kind, "project": project, "text": text, "file": id + ".md"}
	for k, v := range extra {
		mem[k] = v
	}
	version, _ := mem["sources"].(string)
	mem["hash"] = summary.Hash(id, version, text)
	return session.Doc{
		"hook_event_name": session.MemoryEventName,
		"account_id":      s.AccountID,
		"saved_at":        at.UTC().Format(time.RFC3339Nano),
		"created_at":      at,
		"memory":          mem,
	}
}

// RememberKinds are the kinds Remember writes, by their short names.
var RememberKinds = map[string]string{"note": KindNote, "handoff": KindHandoff, "pinned": KindPinned}

// Remember stores a note the user or the agent wrote: kind "note", a
// "handoff" for the next session or the project's "pinned" note (which
// replaces the previous pinned note). A private note is stored (secrets
// masked) but never injected or exported; in any other note <private>
// spans are dropped first.
func (s *Service) Remember(ctx context.Context, project, kind, text string, private bool) (Note, error) {
	if k, ok := RememberKinds[kind]; ok {
		kind = k
	}
	if kind != KindNote && kind != KindHandoff && kind != KindPinned {
		return Note{}, fmt.Errorf("kind must be note, handoff or pinned, not %q", kind)
	}
	if project == "" {
		return Note{}, errors.New("a project is required")
	}
	if private {
		text, _ = s.redactor().String(strings.ReplaceAll(strings.ReplaceAll(text, "<private>", ""), "</private>", ""))
		text = strings.TrimSpace(text)
	} else {
		text = s.clean(text)
	}
	if text == "" {
		return Note{}, errors.New("the note is empty")
	}
	at := s.now()
	id := kind + "/" + project
	if kind != KindPinned {
		sum := sha256.Sum256([]byte(text))
		id += "/" + at.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(sum[:4])
	}
	ev := s.record(kind, id, project, text, at, session.Doc{"private": private, "edited": true, "engine": "user", "day": at.UTC().Format(time.DateOnly)})
	if _, err := summary.Replace(ctx, s.Store, s.AccountID, ev); err != nil {
		return Note{}, err
	}
	return noteOf(ev), nil
}

// Forget deletes the project's native records whose id is one of ids or
// whose text matches pattern (a case-insensitive regular expression),
// private ones included; with dryRun it only lists them. One of ids and
// pattern is required.
func (s *Service) Forget(ctx context.Context, project string, ids []string, pattern string, dryRun bool) ([]Note, error) {
	if len(ids) == 0 && pattern == "" {
		return nil, errors.New("name ids or a pattern")
	}
	var re *regexp.Regexp
	if pattern != "" {
		var err error
		if re, err = regexp.Compile("(?i)" + pattern); err != nil {
			return nil, fmt.Errorf("pattern: %w", err)
		}
	}
	events, err := s.records(ctx, project, true)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var hit []Note
	var hashes []string
	for _, ev := range events {
		n := noteOf(ev)
		if (len(want) > 0 && want[n.ID]) || (re != nil && re.MatchString(n.Text)) {
			hit = append(hit, n)
			hashes = append(hashes, n.Hash)
		}
	}
	if hit == nil {
		hit = []Note{}
	}
	if dryRun || len(hashes) == 0 {
		return hit, nil
	}
	if _, err := s.Store.DeleteMemory(ctx, session.MemoryDelete{AccountID: s.AccountID, Hashes: hashes}); err != nil {
		return nil, err
	}
	return hit, nil
}

// Show lists the project's native records, newest first, of the kinds
// (every native kind when none), private ones only when asked.
func (s *Service) Show(ctx context.Context, project string, includePrivate bool, kinds ...string) ([]Note, error) {
	events, err := s.records(ctx, project, includePrivate, kinds...)
	if err != nil {
		return nil, err
	}
	return notesOf(events), nil
}

// locks serialises the runs of one process; StateDir adds a lock file so
// runs of separate processes (two hooks at once) exclude each other too.
var locks sync.Map

// ErrLocked is returned by Run when another run of the project holds the
// lock.
var ErrLocked = errors.New("another memory run of this project is in progress")
