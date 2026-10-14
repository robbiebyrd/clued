package query

import (
	"context"
	"sort"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const (
	defaultFileSessionLimit = 10
	defaultErrorLimit       = 20
	fileEventScan           = maxLimit
	filesPerSession         = 10
	errorSnippetChars       = 300
)

// FileHistoryArgs are the file_history tool parameters. Path is a
// case-insensitive regex over the file path; Limit counts sessions.
type FileHistoryArgs struct {
	Path        string `json:"path"`
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// FileSession is one session that touched the matching files: which files
// (at most ten), how often each operation ran, the lines its edits and
// writes added and removed (from edit-diff-stats), and when it first and
// last touched them.
type FileSession struct {
	SessionID    string         `json:"session_id"`
	ProjectPath  *string        `json:"project_path"`
	GitOrigin    *string        `json:"git_origin"`
	Files        []string       `json:"files"`
	Operations   map[string]int `json:"operations"`
	LinesAdded   int            `json:"lines_added"`
	LinesRemoved int            `json:"lines_removed"`
	FirstTouch   time.Time      `json:"first_touch"`
	LastTouch    time.Time      `json:"last_touch"`
}

// FileHistory returns the sessions that touched files matching the path,
// the one that touched them most recently first, from the newest 500 file
// events of the scope. A session_id that is not the account's yields none.
func (s *Service) FileHistory(ctx context.Context, args FileHistoryArgs) ([]FileSession, error) {
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []FileSession{}, nil
	}
	events, err := s.Store.FileEvents(ctx, session.FileQuery{AccountID: s.AccountID, PathRe: args.Path, SessionIDs: sessionIDs, Limit: fileEventScan})
	if err != nil {
		return nil, err
	}
	limit := withDefault(args.Limit, defaultFileSessionLimit)
	bySession := map[string]*FileSession{}
	order := []string{}
	for _, ev := range events {
		id, _ := ev.String("session_id")
		fs := bySession[id]
		if fs == nil {
			if len(order) == limit {
				continue
			}
			fs = &FileSession{SessionID: id, Files: []string{}, Operations: map[string]int{}}
			bySession[id] = fs
			order = append(order, id)
		}
		at, _ := ev["created_at"].(time.Time)
		if fs.LastTouch.IsZero() || at.After(fs.LastTouch) {
			fs.LastTouch = at
		}
		if fs.FirstTouch.IsZero() || at.Before(fs.FirstTouch) {
			fs.FirstTouch = at
		}
		e := ev.Map("enrichments")
		path, _ := ev.ToolInput().String("file_path")
		if tracked := e.Map("file-tracker"); tracked != nil {
			if p, ok := tracked.String("file_path"); ok {
				path = p
			}
			if op, ok := tracked.String("operation"); ok {
				fs.Operations[op]++
			}
		}
		if stats := e.Map("edit-diff-stats"); stats != nil {
			fs.LinesAdded += toInt(stats["lines_added"])
			fs.LinesRemoved += toInt(stats["lines_removed"])
		}
		if path != "" && len(fs.Files) < filesPerSession && !contains(fs.Files, path) {
			fs.Files = append(fs.Files, path)
		}
	}
	out := make([]FileSession, 0, len(order))
	for _, id := range order {
		fs := bySession[id]
		sort.Strings(fs.Files)
		sess, err := s.sessionOrNil(ctx, id)
		if err != nil {
			return nil, err
		}
		fs.ProjectPath, fs.GitOrigin = stringPtr(sess, "project_path"), stringPtr(sess, "git_origin")
		out = append(out, *fs)
	}
	return out, nil
}

// FindErrorsArgs are the find_errors tool parameters.
type FindErrorsArgs struct {
	Type        string `json:"type,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	GitOrigin   string `json:"git_origin,omitempty"`
	ProjectPath string `json:"project_path,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

// ErrorHit is a transcript line the error-flag enricher marked: its type,
// the start of its text and where it happened.
type ErrorHit struct {
	SessionID   string    `json:"session_id"`
	Seq         int       `json:"seq"`
	Type        string    `json:"type"`
	Text        string    `json:"text"`
	Timestamp   string    `json:"timestamp,omitempty"`
	ProjectPath *string   `json:"project_path"`
	GitOrigin   *string   `json:"git_origin"`
	CreatedAt   time.Time `json:"created_at"`
}

// FindErrors returns the error lines of the scope, newest first, optionally
// of one type (hook_error, tool_failure, error_text).
func (s *Service) FindErrors(ctx context.Context, args FindErrorsArgs) ([]ErrorHit, error) {
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []ErrorHit{}, nil
	}
	var types []string
	if args.Type != "" {
		types = []string{args.Type}
	}
	lines, err := s.Store.ErrorLines(ctx, session.ErrorQuery{AccountID: s.AccountID, Types: types, SessionIDs: sessionIDs, Limit: withDefault(args.Limit, defaultErrorLimit)})
	if err != nil {
		return nil, err
	}
	sessions := map[string]session.Doc{}
	hits := make([]ErrorHit, 0, len(lines))
	for _, l := range lines {
		id, _ := l.String("session_id")
		if _, known := sessions[id]; !known {
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		typ, _ := l.Map("enrichments").Map("error-flag").String("error_type")
		ts, _ := l.Line().String("timestamp")
		createdAt, _ := l["created_at"].(time.Time)
		hits = append(hits, ErrorHit{
			SessionID: id, Seq: toInt(l["seq"]), Type: typ,
			Text:      session.Snippet(session.LineText(l), nil, errorSnippetChars),
			Timestamp: ts, ProjectPath: stringPtr(sessions[id], "project_path"), GitOrigin: stringPtr(sessions[id], "git_origin"),
			CreatedAt: createdAt,
		})
	}
	return hits, nil
}

// toInt reads a stored number whatever its decoded type.
func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
