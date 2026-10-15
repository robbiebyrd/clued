// Package summary writes the memory note of one captured session: a short
// account of what the session did (its outcome, decisions, open threads
// and files), made by an engine from the session's activity timeline and
// stored as a MemorySaved record of kind session-summary with the
// session's id, so it expires with the session. The free ExtractEngine
// builds the note without a model; digest.ClaudeEngine runs the user's own
// `claude -p`; the hosted runner of the cloud passes its own engine.
//
// Nothing private reaches an engine or the store: <private> spans are
// dropped and the built-in and configured redaction rules mask the prompt
// and the note.
package summary

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/digest"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
	"github.com/robbiebyrd/clued/mind-palace/session/redact"
)

//go:embed templates/summary.md
var templates embed.FS

// Kind is the memory kind of a session note (memory.kind), stored with
// the session's id under the id "session-summary/<session_id>".
const Kind = "session-summary"

// CheckpointKind is the kind of the note taken at PreCompact while a
// session is still running ("memory-checkpoint/<session_id>"); the
// session's note replaces it.
const CheckpointKind = "memory-checkpoint"

// Defaults of the Service knobs left zero.
const (
	DefaultMinPrompts = 2
	// DefaultPromptTokens bounds a prompt (estimated at four characters
	// per token).
	DefaultPromptTokens = 6000
	// activityScan bounds the activities read for one session.
	activityScan = 500
)

// ErrSkipped is returned (wrapped with the reason) for a session that gets
// no note: memory is off for it (CLUED_MEMORY=off), its project is
// excluded, or it holds fewer prompts than MinPrompts.
var ErrSkipped = errors.New("session skipped")

// Summary is one stored session note and how it was made.
type Summary struct {
	SessionID string    `json:"session_id"`
	Project   string    `json:"project"`
	Text      string    `json:"text"`
	Engine    string    `json:"engine"`
	Model     string    `json:"model,omitempty"`
	CostUSD   float64   `json:"cost_usd"`
	CreatedAt time.Time `json:"created_at"`
}

// Service writes and reads the session notes of one account.
type Service struct {
	Store     session.Store
	AccountID string
	// Query answers the activity queries the notes are built from; its
	// Store and AccountID are the service's.
	Query *query.Service
	// Redact masks the prompt and the note; nil means the built-in rules.
	Redact *redact.Redactor
	// TemplateDir holds a summary.md that overrides the built-in prompt;
	// empty means the built-in one.
	TemplateDir string
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	// MinPrompts is the fewest prompts a session needs for a note (zero is
	// DefaultMinPrompts); Exclude are project path globs whose sessions
	// get none (redact.Paths syntax); PromptTokens bounds the prompt (zero
	// is DefaultPromptTokens).
	MinPrompts   int
	Exclude      []string
	PromptTokens int
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

func (s *Service) minPrompts() int {
	if s.MinPrompts > 0 {
		return s.MinPrompts
	}
	return DefaultMinPrompts
}

func (s *Service) promptChars() int {
	if s.PromptTokens > 0 {
		return s.PromptTokens * 4
	}
	return DefaultPromptTokens * 4
}

func (s *Service) query() *query.Service {
	if s.Query != nil {
		return s.Query
	}
	return &query.Service{Store: s.Store, AccountID: s.AccountID}
}

// ID is the memory id of a session's note.
func ID(sessionID string) string { return Kind + "/" + sessionID }

// CheckpointID is the memory id of a session's checkpoint.
func CheckpointID(sessionID string) string { return CheckpointKind + "/" + sessionID }

// Cached returns the stored note of the session, or nil.
func (s *Service) Cached(ctx context.Context, sessionID string) (*Summary, error) {
	events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: []string{Kind}, IDs: []string{ID(sessionID)}, IncludePrivate: true, Limit: 1})
	if err != nil || len(events) == 0 {
		return nil, err
	}
	sum := FromEvent(events[0])
	return &sum, nil
}

// FromEvent reads a stored note (or checkpoint) event.
func FromEvent(ev session.Doc) Summary {
	mem := ev.Map("memory")
	str := func(k string) string { v, _ := mem.String(k); return v }
	sid, _ := ev.String("session_id")
	if sid == "" {
		sid = str("session_id")
	}
	cost, _ := mem["cost_usd"].(float64)
	createdAt, _ := ev["created_at"].(time.Time)
	return Summary{SessionID: sid, Project: str("project"), Text: str("text"), Engine: str("engine"), Model: str("model"), CostUSD: cost, CreatedAt: createdAt}
}

// Pending lists the sessions last seen at or after since that need a
// note, oldest first, at most limit (zero or less: 20): ended, or idle for
// idle, with no note made since their last activity, and eligible (memory
// not off, project not excluded, at least MinPrompts prompts). Sessions of
// other accounts are never listed.
func (s *Service) Pending(ctx context.Context, since time.Time, idle time.Duration, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	found, err := s.Store.FindSessions(ctx, session.SessionQuery{AccountID: s.AccountID, LastSeenFrom: since, Limit: 500})
	if err != nil {
		return nil, err
	}
	made := map[string]time.Time{}
	notes, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, Kinds: []string{Kind}, Since: since, IncludePrivate: true})
	if err != nil {
		return nil, err
	}
	for _, ev := range notes {
		sid, _ := ev.String("session_id")
		made[sid] = noteLastSeen(ev)
	}
	now := s.now()
	sort.SliceStable(found, func(i, j int) bool { return timeOf(found[i], "last_seen").Before(timeOf(found[j], "last_seen")) })
	out := []string{}
	for _, d := range found {
		if len(out) >= limit {
			break
		}
		sid, _ := d.String("session_id")
		last := timeOf(d, "last_seen")
		state, _ := d.String("live_state")
		if sid == "" || (state != "ended" && now.Sub(last) < idle) {
			continue
		}
		if at, ok := made[sid]; ok && !at.Before(last) {
			continue
		}
		if _, err := s.eligible(ctx, sid); err != nil {
			if errors.Is(err, ErrSkipped) || errors.Is(err, session.ErrNotFound) {
				continue
			}
			return nil, err
		}
		out = append(out, sid)
	}
	return out, nil
}

// noteLastSeen is the session last_seen a stored note was made at.
func noteLastSeen(ev session.Doc) time.Time {
	switch v := ev.Map("memory")["last_seen"].(type) {
	case time.Time:
		return v
	case string:
		t, _ := time.Parse(time.RFC3339Nano, v)
		return t
	}
	return time.Time{}
}

// material is what a session's note is made from.
type material struct {
	SessionID string
	Project   string
	Title     string
	Cwd       string
	LastSeen  time.Time
	Material  string
}

// eligible loads the session and its activities, or says why it gets no
// note (ErrSkipped) or that it is not the account's (ErrNotFound).
func (s *Service) eligible(ctx context.Context, sessionID string) (material, error) {
	doc, err := s.Store.GetSession(ctx, s.AccountID, sessionID)
	if err != nil {
		return material{}, err
	}
	m := material{SessionID: sessionID, LastSeen: timeOf(doc, "last_seen")}
	m.Title, _ = doc.String("title")
	projectPath, _ := doc.String("project_path")
	cwd, _ := doc.String("cwd")
	origin, _ := doc.String("git_origin")
	if projectPath == "" {
		projectPath = cwd
	}
	m.Cwd = projectPath
	m.Project = session.MemoryProjectKey(origin, projectPath)
	if off, _ := doc.String("memory"); off == "off" {
		return m, fmt.Errorf("%w: memory is off for this session (CLUED_MEMORY=off)", ErrSkipped)
	}
	if len(s.Exclude) > 0 {
		paths, _ := redact.NewPaths(s.Exclude)
		if paths.Excludes(projectPath) || paths.Excludes(cwd) {
			return m, fmt.Errorf("%w: the project %s is excluded from memory", ErrSkipped, projectPath)
		}
	}
	acts, err := s.query().Activities(ctx, query.ActivitiesArgs{SessionID: sessionID, Limit: activityScan})
	if err != nil {
		return m, err
	}
	prompts := 0
	for _, a := range acts {
		if a.Kind == "user" {
			prompts++
		}
	}
	if prompts < s.minPrompts() {
		return m, fmt.Errorf("%w: %d prompts, fewer than the %d a note needs", ErrSkipped, prompts, s.minPrompts())
	}
	m.Material = s.clean(Material(sessionID, acts))
	return m, nil
}

// Eligible reports whether the session gets a note: nil, an ErrSkipped
// error saying why not, or ErrNotFound for a session the account does not
// have.
func (s *Service) Eligible(ctx context.Context, sessionID string) error {
	_, err := s.eligible(ctx, sessionID)
	return err
}

// clean drops private spans and masks secrets.
func (s *Service) clean(text string) string {
	text = redact.StripPrivate(text)
	text, _ = s.redactor().String(text)
	return text
}

// Prompt renders the prompt of a session's note, clipped to the prompt
// budget.
func (s *Service) Prompt(ctx context.Context, sessionID string) (string, error) {
	m, err := s.eligible(ctx, sessionID)
	if err != nil {
		return "", err
	}
	return s.render(m)
}

func (s *Service) render(m material) (string, error) {
	text, err := s.template()
	if err != nil {
		return "", err
	}
	tmpl, err := template.New("summary.md").Parse(text)
	if err != nil {
		return "", fmt.Errorf("template summary.md: %w", err)
	}
	exec := func(material string) (string, error) {
		var b strings.Builder
		data := map[string]string{"SessionID": m.SessionID, "Project": m.Project, "Title": m.Title, "Material": material}
		if err := tmpl.Execute(&b, data); err != nil {
			return "", fmt.Errorf("template summary.md: %w", err)
		}
		return b.String(), nil
	}
	prompt, err := exec(m.Material)
	if err != nil || len(prompt) <= s.promptChars() {
		return prompt, err
	}
	empty, err := exec("")
	if err != nil {
		return "", err
	}
	return exec(Clip(m.Material, s.promptChars()-len(empty)))
}

func (s *Service) template() (string, error) {
	if s.TemplateDir != "" {
		if text, err := os.ReadFile(filepath.Join(s.TemplateDir, "summary.md")); err == nil {
			return string(text), nil
		}
	}
	text, err := templates.ReadFile("templates/summary.md")
	return string(text), err
}

// Compose makes a session's note with the engine without storing it.
func (s *Service) Compose(ctx context.Context, sessionID string, engine digest.Engine, model string) (Summary, error) {
	sum, _, err := s.compose(ctx, sessionID, engine, model)
	return sum, err
}

func (s *Service) compose(ctx context.Context, sessionID string, engine digest.Engine, model string) (Summary, material, error) {
	m, err := s.eligible(ctx, sessionID)
	if err != nil {
		return Summary{}, m, err
	}
	prompt, err := s.render(m)
	if err != nil {
		return Summary{}, m, err
	}
	text, cost, err := engine.Run(ctx, prompt, model)
	if err != nil {
		return Summary{}, m, err
	}
	text = strings.TrimSpace(s.clean(text))
	if text == "" {
		return Summary{}, m, fmt.Errorf("%s returned an empty note", engine.Name())
	}
	return Summary{SessionID: sessionID, Project: m.Project, Text: text, Engine: engine.Name(), Model: model, CostUSD: cost, CreatedAt: s.now()}, m, nil
}

// Summarize makes the session's note with the engine and stores it as
// "session-summary/<session_id>", replacing an older note of the session
// and its checkpoint. A note the user edited is kept and returned as it
// is, without running the engine. An engine error stores nothing.
func (s *Service) Summarize(ctx context.Context, sessionID string, engine digest.Engine, model string) (Summary, error) {
	if old, err := s.edited(ctx, ID(sessionID)); err != nil || old != nil {
		if old != nil {
			return FromEvent(old), nil
		}
		return Summary{}, err
	}
	sum, m, err := s.compose(ctx, sessionID, engine, model)
	if err != nil {
		return Summary{}, err
	}
	if err := s.put(ctx, Kind, ID(sessionID), sum, m); err != nil {
		return Summary{}, err
	}
	if _, err := s.Store.DeleteMemory(ctx, session.MemoryDelete{AccountID: s.AccountID, IDs: []string{CheckpointID(sessionID)}}); err != nil && !errors.Is(err, session.ErrNoAccount) {
		return sum, err
	}
	return sum, nil
}

// Checkpoint makes a running session's note and stores it as
// "memory-checkpoint/<session_id>", replacing the previous checkpoint.
func (s *Service) Checkpoint(ctx context.Context, sessionID string, engine digest.Engine, model string) (Summary, error) {
	sum, m, err := s.compose(ctx, sessionID, engine, model)
	if err != nil {
		return Summary{}, err
	}
	return sum, s.put(ctx, CheckpointKind, CheckpointID(sessionID), sum, m)
}

// edited returns the stored record of id when the user edited it.
func (s *Service) edited(ctx context.Context, id string) (session.Doc, error) {
	events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, IDs: []string{id}, IncludePrivate: true})
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if edited, _ := ev.Map("memory")["edited"].(bool); edited {
			return ev, nil
		}
	}
	return nil, nil
}

// put stores a note or checkpoint.
func (s *Service) put(ctx context.Context, kind, id string, sum Summary, m material) error {
	mem := session.Doc{
		"id": id, "kind": kind, "project": sum.Project, "text": sum.Text, "file": id + ".md",
		"session_id": sum.SessionID, "engine": sum.Engine, "cost_usd": sum.CostUSD,
		"last_seen": m.LastSeen.UTC().Format(time.RFC3339Nano), "day": m.LastSeen.UTC().Format(time.DateOnly),
	}
	if sum.Model != "" {
		mem["model"] = sum.Model
	}
	if m.Title != "" {
		mem["title"] = m.Title
	}
	mem["hash"] = Hash(id, m.LastSeen.UTC().Format(time.RFC3339Nano), sum.Text)
	ev := session.Doc{
		"hook_event_name": session.MemoryEventName,
		"account_id":      s.AccountID,
		"session_id":      sum.SessionID,
		"cwd":             m.Cwd,
		"saved_at":        sum.CreatedAt.UTC().Format(time.RFC3339Nano),
		"created_at":      sum.CreatedAt,
		"memory":          mem,
	}
	_, err := Replace(ctx, s.Store, s.AccountID, ev)
	return err
}

// Hash is the memory.hash of a record: its id, a version stamp and its
// text, so one text under two ids is two records and a record rewritten
// with the same text is deduplicated.
func Hash(id, version, text string) string {
	sum := sha256.Sum256([]byte(id + "|" + version + "|" + strings.TrimSpace(text)))
	return hex.EncodeToString(sum[:])
}

// Replace stores ev, a MemorySaved event whose memory carries an id and a
// hash, as the one record of that id: the new record is inserted first
// (so a failure loses nothing), then the account's other records of the
// id go. A stored record the user edited (memory.edited) is never
// replaced by one that is not: replaced is then false and nothing is
// written.
func Replace(ctx context.Context, st session.Store, accountID string, ev session.Doc) (replaced bool, err error) {
	mem := ev.Map("memory")
	id, _ := mem.String("id")
	hash, _ := mem.String("hash")
	if id == "" || hash == "" {
		return false, errors.New("memory record without an id or a hash")
	}
	old, err := st.MemoryEvents(ctx, session.MemoryQuery{AccountID: accountID, IDs: []string{id}, IncludePrivate: true})
	if err != nil {
		return false, err
	}
	newEdited, _ := mem["edited"].(bool)
	var stale []string
	for _, o := range old {
		om := o.Map("memory")
		if edited, _ := om["edited"].(bool); edited && !newEdited {
			return false, nil
		}
		if h, _ := om.String("hash"); h != "" && h != hash {
			stale = append(stale, h)
		}
	}
	if err := st.InsertHookEvent(ctx, ev); err != nil {
		return false, err
	}
	if len(stale) > 0 {
		if _, err := st.DeleteMemory(ctx, session.MemoryDelete{AccountID: accountID, Hashes: stale}); err != nil {
			return true, err
		}
	}
	return true, nil
}

// timeOf reads a time field a store may hold as a time or an RFC 3339
// string.
func timeOf(d session.Doc, key string) time.Time {
	switch v := d[key].(type) {
	case time.Time:
		return v
	case string:
		t, _ := time.Parse(time.RFC3339Nano, v)
		return t
	}
	return time.Time{}
}
