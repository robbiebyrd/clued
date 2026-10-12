// Package query holds the read use cases over captured sessions that the MCP
// tools and the CLI call: finding sessions, searching commands, and reading a
// session's context, full record or transcript.
package query

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	maxLimit             = 500
	defaultSessionLimit  = 10
	defaultCommandLimit  = 20
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
}

// FindSessionsArgs are the find_sessions tool parameters.
type FindSessionsArgs struct {
	ProjectPath string `json:"project_path,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	Query       string `json:"query,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// SearchCommandsArgs are the search_commands tool parameters.
type SearchCommandsArgs struct {
	Pattern   string `json:"pattern"`
	SessionID string `json:"session_id,omitempty"`
	GitOrigin string `json:"git_origin,omitempty"`
	Limit     int    `json:"limit,omitempty"`
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

// Context is a session's metadata, its most recent distinct commands and the
// start and end of its transcript.
type Context struct {
	Session     session.Doc   `json:"session"`
	TopCommands []string      `json:"top_commands"`
	FirstLines  []session.Doc `json:"first_lines"`
	LastLines   []session.Doc `json:"last_lines"`
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
	docs, err := s.Store.FindSessions(ctx, session.SessionQuery{
		AccountID:     s.AccountID,
		ProjectPathRe: args.ProjectPath,
		GitOriginRe:   args.GitOrigin,
		QueryRe:       args.Query,
		Limit:         withDefault(args.Limit, defaultSessionLimit),
	})
	if err != nil {
		return nil, err
	}
	for _, d := range docs {
		id, _ := d.String("session_id")
		if d["event_count"], err = s.Store.CountTranscriptLines(ctx, s.AccountID, id); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

// SearchCommands returns Bash commands matching the pattern, newest first,
// optionally limited to one session or to the sessions of a repository.
// A session_id that is not the account's yields no hits.
func (s *Service) SearchCommands(ctx context.Context, args SearchCommandsArgs) ([]CommandHit, error) {
	sessionIDs, scoped, err := s.searchScope(ctx, args)
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

	sessions := map[string]session.Doc{}
	hits := make([]CommandHit, 0, len(events))
	for _, ev := range events {
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

// searchScope resolves the session ids a command search is limited to.
// scoped is false when the search covers every session of the account.
func (s *Service) searchScope(ctx context.Context, args SearchCommandsArgs) (ids []string, scoped bool, err error) {
	switch {
	case args.SessionID != "":
		owned, err := s.sessionOrNil(ctx, args.SessionID)
		if err != nil || owned == nil {
			return nil, true, err
		}
		return []string{args.SessionID}, true, nil
	case args.GitOrigin != "":
		found, err := s.Store.FindSessions(ctx, session.SessionQuery{
			AccountID: s.AccountID, GitOriginRe: args.GitOrigin, Limit: maxLimit,
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
// Bash commands (from the last 100), and the first 20 and last 20 transcript
// lines; last_lines is empty unless the transcript has more than 20 lines.
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
	return Context{Session: sessionSummary(stored), TopCommands: topCommands, FirstLines: first, LastLines: last}, nil
}

// sessionSummary keeps the six fields the context reports; git_origin is
// always present, null when the session has none.
func sessionSummary(stored session.Doc) session.Doc {
	out := session.Doc{"git_origin": nil}
	for _, k := range []string{"session_id", "project_path", "git_origin", "cwd", "started_at", "last_seen"} {
		if v, ok := stored[k]; ok {
			out[k] = v
		}
	}
	return out
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
