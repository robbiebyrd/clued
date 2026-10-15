// Package query holds the read use cases over captured sessions that the MCP
// tools and the CLI call: finding sessions, searching commands, and reading a
// session's context, full record or transcript.
package query

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/livestate"
)

const (
	maxLimit             = 500
	defaultSessionLimit  = 10
	defaultCommandLimit  = 20
	defaultMemoryLimit   = 20
	defaultLimitSamples  = 20
	contextMemoryCount   = 5
	defaultLineLimit     = 200
	topCommandCount      = 10
	topCommandScanWindow = 100
	contextLineCount     = 20
	transcriptBatch      = 50
)

// Service answers queries for one account.
type Service struct {
	Store     session.Store
	AccountID string
	// Prices are the model prices (by model id prefix) that override the
	// built-in table for the usage estimates; nil keeps the table.
	Prices map[string]session.ModelPrice
	// Now is the clock the hotness grades and the time bounds use; nil is
	// time.Now.
	Now func() time.Time
}

// now is the service's clock.
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// FindSessionsArgs are the find_sessions tool parameters.
type FindSessionsArgs struct {
	ProjectPath string `json:"project_path,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	Query       string `json:"query,omitempty"`
	// Title keeps only sessions whose title matches this regex.
	Title string `json:"title,omitempty"`
	// LiveState keeps only sessions in that live state (working, waiting,
	// ended); Hotness only those of that grade (hot, warm, cool, cold).
	LiveState string `json:"live_state,omitempty"`
	Hotness   string `json:"hotness,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// SearchCommandsArgs are the search_commands tool parameters.
// IncludeRejected keeps the commands the harness refused to run.
type SearchCommandsArgs struct {
	IncludeRejected bool   `json:"include_rejected,omitempty"`
	Pattern         string `json:"pattern"`
	SessionID       string `json:"session_id,omitempty"`
	GitOrigin       string `json:"git_origin,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

// SearchMemoryArgs are the search_memory tool parameters. Pattern is a
// case-insensitive regex over the text; empty means every record.
type SearchMemoryArgs struct {
	Pattern     string `json:"pattern,omitempty"`
	Kind        string `json:"kind,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// LimitsArgs are the get_limits tool parameters. Since is an RFC 3339 time
// or a duration back from now ("24h"); empty means no lower bound.
type LimitsArgs struct {
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Since       string `json:"since,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// LimitSample is one LimitsSampled event (a status-line sample posted by
// `hook status`) with the project and repository of its session.
type LimitSample struct {
	SessionID     string      `json:"session_id"`
	ProjectPath   *string     `json:"project_path"`
	GitOrigin     *string     `json:"git_origin"`
	Cwd           string      `json:"cwd"`
	Model         session.Doc `json:"model,omitempty"`
	Version       string      `json:"version,omitempty"`
	ContextWindow session.Doc `json:"context_window,omitempty"`
	RateLimits    session.Doc `json:"rate_limits,omitempty"`
	Cost          session.Doc `json:"cost,omitempty"`
	LiveState     string      `json:"live_state,omitempty"`
	SampledAt     string      `json:"sampled_at,omitempty"`
	CreatedAt     time.Time   `json:"created_at"`
}

// MemoryHit is one memory record (a claude-remember summary, handoff or
// identity ingested as a MemorySaved event) with the project and repository
// of the session it belongs to (nil when unknown).
type MemoryHit struct {
	SessionID   string    `json:"session_id"`
	ProjectPath *string   `json:"project_path"`
	GitOrigin   *string   `json:"git_origin"`
	Cwd         string    `json:"cwd"`
	Kind        string    `json:"kind"`
	File        string    `json:"file"`
	Time        string    `json:"time,omitempty"`
	Day         string    `json:"day,omitempty"`
	Branch      string    `json:"branch,omitempty"`
	Text        string    `json:"text"`
	CreatedAt   time.Time `json:"created_at"`
}

// ReadTranscriptArgs are the read_transcript tool parameters.
type ReadTranscriptArgs struct {
	SessionID string `json:"session_id"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// CommandHit is a Bash command that matched a search, with the project and
// repository of the session it ran in (nil when the session has none).
type CommandHit struct {
	SessionID   string    `json:"session_id"`
	ProjectPath *string   `json:"project_path"`
	GitOrigin   *string   `json:"git_origin"`
	Command     string    `json:"command"`
	CreatedAt   time.Time `json:"created_at"`
}

// Context is a session's metadata, its most recent distinct commands, the
// start and end of its transcript and its latest memory records.
type Context struct {
	Session      session.Doc   `json:"session"`
	TopCommands  []string      `json:"top_commands"`
	FirstLines   []session.Doc `json:"first_lines"`
	LastLines    []session.Doc `json:"last_lines"`
	RecentMemory []MemoryHit   `json:"recent_memory"`
	// Usage is the session's token usage and estimated cost (see Usage);
	// null when no usage is recorded for it.
	Usage *UsageGroup `json:"usage"`
	// Turns is the session's pace from its turn_duration records; null
	// when none is recorded.
	Turns *TurnStats `json:"turns"`
	// Telemetry sums the OpenTelemetry events Claude Code exported for the
	// session (API requests, errors, tool calls); null when none arrived.
	Telemetry *Telemetry `json:"telemetry"`
}

// TurnStats sums the turn_duration records of a session: how many turns,
// their total and mean duration in milliseconds, and the longest.
type TurnStats struct {
	Turns     int   `json:"turns"`
	TotalMs   int64 `json:"total_ms"`
	MeanMs    int64 `json:"mean_ms"`
	LongestMs int64 `json:"longest_ms"`
}

// withDefault returns n, or def when n is not positive, never above maxLimit.
func withDefault(n, def int) int {
	if n <= 0 {
		n = def
	}
	return min(n, maxLimit)
}

// FindSessions returns the matching sessions, newest first, each with the
// number of transcript lines captured as event_count.
func (s *Service) FindSessions(ctx context.Context, args FindSessionsArgs) ([]session.Doc, error) {
	now := s.now()
	q := session.SessionQuery{
		AccountID:     s.AccountID,
		ProjectPathRe: args.ProjectPath,
		GitOriginRe:   args.GitOrigin,
		QueryRe:       args.Query,
		TitleRe:       args.Title,
		Limit:         withDefault(args.Limit, defaultSessionLimit),
	}
	if args.LiveState != "" {
		if !slices.Contains(livestate.States, args.LiveState) {
			return nil, fmt.Errorf("live_state must be one of %s: %q", strings.Join(livestate.States, ", "), args.LiveState)
		}
		q.LiveStates = []string{args.LiveState}
	}
	if args.Hotness != "" {
		from, before, ok := livestate.Window(args.Hotness, now)
		if !ok {
			return nil, fmt.Errorf("hotness must be one of %s: %q", strings.Join(livestate.Grades, ", "), args.Hotness)
		}
		if !from.IsZero() && !before.IsZero() && !from.Before(before) {
			return []session.Doc{}, nil // no session can be in that grade right now
		}
		q.LastSeenFrom, q.LastSeenBefore = from, before
	}
	docs, err := s.Store.FindSessions(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		id, _ := d.String("session_id")
		if d["event_count"], err = s.Store.CountTranscriptLines(ctx, s.AccountID, id); err != nil {
			return nil, err
		}
		addHotness(d, now)
	}
	return docs, nil
}

// addHotness sets the session's hotness grade from its last_seen time.
func addHotness(d session.Doc, now time.Time) {
	if lastSeen, ok := d["last_seen"].(time.Time); ok {
		d["hotness"] = livestate.Hotness(lastSeen, now)
	}
}

// SearchCommands returns Bash commands matching the pattern, newest first,
// optionally limited to one session or to the sessions of a repository.
// A session_id that is not the account's yields no hits.
func (s *Service) SearchCommands(ctx context.Context, args SearchCommandsArgs) ([]CommandHit, error) {
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, "")
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []CommandHit{}, nil
	}
	events, err := s.Store.BashEvents(ctx, session.CommandQuery{
		AccountID:  s.AccountID,
		PatternRe:  args.Pattern,
		SessionIDs: sessionIDs,
		Limit:      withDefault(args.Limit, defaultCommandLimit),
	})
	if err != nil {
		return nil, err
	}

	rejected, err := s.rejectedToolUses(ctx, sessionIDs, args.IncludeRejected)
	if err != nil {
		return nil, err
	}
	sessions := map[string]session.Doc{}
	hits := make([]CommandHit, 0, len(events))
	for _, ev := range events {
		if id, _ := ev.String("tool_use_id"); rejected[id] {
			continue
		}
		id, _ := ev.String("session_id")
		if _, known := sessions[id]; !known {
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		command, _ := ev.ToolInput().String("command")
		createdAt, _ := ev["created_at"].(time.Time)
		hits = append(hits, CommandHit{
			SessionID:   id,
			ProjectPath: stringPtr(sessions[id], "project_path"),
			GitOrigin:   stringPtr(sessions[id], "git_origin"),
			Command:     command,
			CreatedAt:   createdAt,
		})
	}
	return hits, nil
}

// SearchMemory returns memory records matching the pattern, newest first,
// optionally of one kind and limited to one session or to the sessions of a
// repository or project. A session_id that is not the account's yields no
// hits.
func (s *Service) SearchMemory(ctx context.Context, args SearchMemoryArgs) ([]MemoryHit, error) {
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []MemoryHit{}, nil
	}
	var kinds []string
	if args.Kind != "" {
		kinds = []string{args.Kind}
	}
	events, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{
		AccountID:  s.AccountID,
		PatternRe:  args.Pattern,
		Kinds:      kinds,
		SessionIDs: sessionIDs,
		Limit:      withDefault(args.Limit, defaultMemoryLimit),
	})
	if err != nil {
		return nil, err
	}
	return s.memoryHits(ctx, events)
}

// Limits returns the status-line samples of the account, newest first, scoped like
// SearchMemory and bounded below by args.Since.
func (s *Service) Limits(ctx context.Context, args LimitsArgs) ([]LimitSample, error) {
	since, err := parseSince(args.Since, time.Now())
	if err != nil {
		return nil, err
	}
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []LimitSample{}, nil
	}
	events, err := s.Store.LimitSamples(ctx, session.LimitQuery{
		AccountID:  s.AccountID,
		SessionIDs: sessionIDs,
		Since:      since,
		Limit:      withDefault(args.Limit, defaultLimitSamples),
	})
	if err != nil {
		return nil, err
	}
	sessions := map[string]session.Doc{}
	out := make([]LimitSample, 0, len(events))
	for _, ev := range events {
		id, _ := ev.String("session_id")
		if _, known := sessions[id]; !known && id != "" {
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		cwd, _ := ev.String("cwd")
		version, _ := ev.String("version")
		liveState, _ := ev.String("live_state")
		sampledAt, _ := ev.String("sampled_at")
		createdAt, _ := ev["created_at"].(time.Time)
		out = append(out, LimitSample{
			SessionID:     id,
			ProjectPath:   stringPtr(sessions[id], "project_path"),
			GitOrigin:     stringPtr(sessions[id], "git_origin"),
			Cwd:           cwd,
			Model:         docOrNil(ev, "model"),
			Version:       version,
			ContextWindow: docOrNil(ev, "context_window"),
			RateLimits:    docOrNil(ev, "rate_limits"),
			Cost:          docOrNil(ev, "cost"),
			LiveState:     liveState,
			SampledAt:     sampledAt,
			CreatedAt:     createdAt,
		})
	}
	return out, nil
}

// rejectedScan bounds the rejected tool calls a query looks up to leave
// their calls out.
const rejectedScan = maxLimit

// rejectedToolUses is the set of tool_use ids of the scope's calls the
// harness refused (the newest 500), empty when the caller wants them kept.
func (s *Service) rejectedToolUses(ctx context.Context, sessionIDs []string, include bool) (map[string]bool, error) {
	ids := map[string]bool{}
	if include {
		return ids, nil
	}
	lines, err := s.Store.RejectedToolUses(ctx, session.ActivityQuery{AccountID: s.AccountID, SessionIDs: sessionIDs, Limit: rejectedScan})
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		if id, _ := l.Map("enrichments").Map("error-flag").String("tool_use_id"); id != "" {
			ids[id] = true
		}
	}
	return ids, nil
}

// parseSince reads a lower time bound: empty means none, a duration means
// that long before now, anything else an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	if d, err := time.ParseDuration(s); err == nil {
		if d < 0 {
			d = -d
		}
		return now.Add(-d), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("since must be an RFC 3339 time or a duration such as 24h: %q", s)
	}
	return t, nil
}

// docOrNil returns the map under key, or nil when there is none.
func docOrNil(d session.Doc, key string) session.Doc {
	m := d.Map(key)
	if len(m) == 0 {
		return nil
	}
	return m
}

// memoryHits joins memory events with their sessions.
func (s *Service) memoryHits(ctx context.Context, events []session.Doc) ([]MemoryHit, error) {
	sessions := map[string]session.Doc{}
	hits := make([]MemoryHit, 0, len(events))
	for _, ev := range events {
		id, _ := ev.String("session_id")
		if _, known := sessions[id]; !known && id != "" {
			var err error
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		mem := ev.Map("memory")
		str := func(k string) string { v, _ := mem.String(k); return v }
		cwd, _ := ev.String("cwd")
		createdAt, _ := ev["created_at"].(time.Time)
		hits = append(hits, MemoryHit{
			SessionID:   id,
			ProjectPath: stringPtr(sessions[id], "project_path"),
			GitOrigin:   stringPtr(sessions[id], "git_origin"),
			Cwd:         cwd,
			Kind:        str("kind"), File: str("file"), Time: str("time"), Day: str("day"), Branch: str("branch"), Text: str("text"),
			CreatedAt: createdAt,
		})
	}
	return hits, nil
}

// scope resolves the session ids a search is limited to: one session, the
// sessions of a repository, or the sessions of a project path. scoped is
// false when the search covers every session of the account.
func (s *Service) scope(ctx context.Context, sessionID, gitOrigin, projectPath string) (ids []string, scoped bool, err error) {
	switch {
	case sessionID != "":
		owned, err := s.sessionOrNil(ctx, sessionID)
		if err != nil || owned == nil {
			return nil, true, err
		}
		return []string{sessionID}, true, nil
	case gitOrigin != "" || projectPath != "":
		found, err := s.Store.FindSessions(ctx, session.SessionQuery{
			AccountID: s.AccountID, GitOriginRe: gitOrigin, ProjectPathRe: projectPath, Limit: maxLimit,
		})
		if err != nil {
			return nil, true, err
		}
		for _, d := range found {
			id, _ := d.String("session_id")
			ids = append(ids, id)
		}
		return ids, true, nil
	}
	return nil, false, nil
}

// sessionOrNil returns the account's session, or nil when it does not exist.
func (s *Service) sessionOrNil(ctx context.Context, id string) (session.Doc, error) {
	d, err := s.Store.GetSession(ctx, s.AccountID, id)
	if errors.Is(err, session.ErrNotFound) {
		return nil, nil
	}
	return d, err
}

func stringPtr(d session.Doc, key string) *string {
	if v, ok := d.String(key); ok {
		return &v
	}
	return nil
}

// SessionContext returns the session's metadata, its ten most recent distinct
// Bash commands (from the last 100), the first 20 and last 20 transcript
// lines (last_lines is empty unless the transcript has more than 20 lines)
// and its five latest memory records.
func (s *Service) SessionContext(ctx context.Context, sessionID string) (Context, error) {
	stored, err := s.Store.GetSession(ctx, s.AccountID, sessionID)
	if err != nil {
		return Context{}, fmt.Errorf("session %s: %w", sessionID, err)
	}
	topCommands, err := s.topCommands(ctx, sessionID)
	if err != nil {
		return Context{}, err
	}
	first, err := s.Store.TranscriptLines(ctx, session.LineQuery{AccountID: s.AccountID, SessionID: sessionID, Limit: contextLineCount})
	if err != nil {
		return Context{}, err
	}
	total, err := s.Store.CountTranscriptLines(ctx, s.AccountID, sessionID)
	if err != nil {
		return Context{}, err
	}
	last := []session.Doc{}
	if total > contextLineCount {
		if last, err = s.Store.TranscriptLines(ctx, session.LineQuery{
			AccountID: s.AccountID, SessionID: sessionID, Limit: contextLineCount, Descending: true,
		}); err != nil {
			return Context{}, err
		}
		for i, j := 0, len(last)-1; i < j; i, j = i+1, j-1 {
			last[i], last[j] = last[j], last[i]
		}
	}
	memoryEvents, err := s.Store.MemoryEvents(ctx, session.MemoryQuery{AccountID: s.AccountID, SessionIDs: []string{sessionID}, Limit: contextMemoryCount})
	if err != nil {
		return Context{}, err
	}
	recent, err := s.memoryHits(ctx, memoryEvents)
	if err != nil {
		return Context{}, err
	}
	usage, err := s.Usage(ctx, UsageArgs{SessionID: sessionID})
	if err != nil {
		return Context{}, err
	}
	turns, err := s.turnStats(ctx, sessionID)
	if err != nil {
		return Context{}, err
	}
	telemetry, err := s.telemetry(ctx, sessionID)
	if err != nil {
		return Context{}, err
	}
	summary := sessionSummary(stored)
	addHotness(summary, s.now())
	c := Context{Session: summary, TopCommands: topCommands, FirstLines: first, LastLines: last, RecentMemory: recent, Turns: turns, Telemetry: telemetry}
	if len(usage.Groups) > 0 {
		c.Usage = &usage.Groups[0]
	}
	return c, nil
}

// turnScan bounds the turn records summed for one session.
const turnScan = maxLimit

// turnStats sums the session's turn_duration records (the lines the
// activity-kind enricher labelled "turn", with their system-record); nil
// when there is none.
func (s *Service) turnStats(ctx context.Context, sessionID string) (*TurnStats, error) {
	lines, err := s.Store.Activities(ctx, session.ActivityQuery{AccountID: s.AccountID, SessionIDs: []string{sessionID}, Kinds: []string{"turn"}, Limit: turnScan})
	if err != nil {
		return nil, err
	}
	var st TurnStats
	for _, l := range lines {
		ms := toInt64(l.Map("enrichments").Map("system-record")["duration_ms"])
		st.Turns++
		st.TotalMs += ms
		st.LongestMs = max(st.LongestMs, ms)
	}
	if st.Turns == 0 {
		return nil, nil
	}
	st.MeanMs = st.TotalMs / int64(st.Turns)
	return &st, nil
}

// toInt64 reads a stored number whatever its decoded type.
func toInt64(v any) int64 {
	switch n := v.(type) {
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

// sessionSummary keeps the fields the context reports; git_origin is
// always present, null when the session has none; model and
// model_changed_at appear once a model is known, live_state, live_state_at
// and live_state_reason once a state is.
func sessionSummary(stored session.Doc) session.Doc {
	out := session.Doc{"git_origin": nil}
	for _, k := range []string{"session_id", "project_path", "git_origin", "cwd", "model", "model_changed_at", "live_state", "live_state_at", "live_state_reason",
		"title", "title_kind", "slug", "permission_mode", "pr_link", "worktree", "entrypoint", "claude_version", "started_at", "last_seen"} {
		if v, ok := stored[k]; ok {
			out[k] = v
		}
	}
	return out
}

// TopCommands returns the session's ten most recent distinct Bash commands
// (from its last 100), one store call: what the prime report shows without
// the rest of SessionContext.
func (s *Service) TopCommands(ctx context.Context, sessionID string) ([]string, error) {
	return s.topCommands(ctx, sessionID)
}

func (s *Service) topCommands(ctx context.Context, sessionID string) ([]string, error) {
	events, err := s.Store.BashEvents(ctx, session.CommandQuery{
		AccountID: s.AccountID, SessionIDs: []string{sessionID}, Limit: topCommandScanWindow,
	})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	top := []string{}
	for _, ev := range events {
		command, _ := ev.ToolInput().String("command")
		if seen[command] {
			continue
		}
		seen[command] = true
		if top = append(top, command); len(top) == topCommandCount {
			break
		}
	}
	return top, nil
}

// FullSession returns the session joined with its transcript, subagent lines,
// blobs and hook events.
func (s *Service) FullSession(ctx context.Context, sessionID string) (session.Doc, error) {
	doc, err := s.Store.FullSession(ctx, s.AccountID, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session %s: %w", sessionID, err)
	}
	return doc, nil
}

// ReadTranscript returns up to Limit transcript lines from Offset, reading in
// batches of 50. progress, when not nil, is called with the lines read so far
// and the transcript's total after every non-empty batch.
func (s *Service) ReadTranscript(ctx context.Context, args ReadTranscriptArgs, progress func(done, total int)) ([]session.Doc, error) {
	if _, err := s.Store.GetSession(ctx, s.AccountID, args.SessionID); err != nil {
		return nil, fmt.Errorf("session %s: %w", args.SessionID, err)
	}
	total, err := s.Store.CountTranscriptLines(ctx, s.AccountID, args.SessionID)
	if err != nil {
		return nil, err
	}
	offset := max(args.Offset, 0)
	end := offset + withDefault(args.Limit, defaultLineLimit)

	all := []session.Doc{}
	for start := offset; start < end; start += transcriptBatch {
		want := min(transcriptBatch, end-start)
		batch, err := s.Store.TranscriptLines(ctx, session.LineQuery{
			AccountID: s.AccountID, SessionID: args.SessionID, Offset: start, Limit: want,
		})
		if err != nil {
			return nil, err
		}
		all = append(all, batch...)
		if progress != nil && len(batch) > 0 {
			progress(len(all), total)
		}
		if len(batch) < want {
			break
		}
	}
	return all, nil
}
