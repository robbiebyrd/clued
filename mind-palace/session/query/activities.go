package query

import (
	"context"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const defaultActivityLimit = 50

// ActivitiesArgs are the get_activities tool parameters. Kinds keeps only
// those activity kinds; Since is an RFC 3339 time or a duration back from
// now ("24h"); empty means no lower bound.
type ActivitiesArgs struct {
	SessionID   string   `json:"session_id,omitempty"`
	GitOrigin   string   `json:"git_origin,omitempty"`
	ProjectPath string   `json:"project_path,omitempty"`
	Kinds       []string `json:"kinds,omitempty"`
	Since       string   `json:"since,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

// Activity is one entry of the timeline: a transcript line's activity-kind
// enrichment with where it happened. Count is how many consecutive lines
// of the same tool activity (same kind, label and detail in one session)
// the entry stands for; Seq is the newest of them.
type Activity struct {
	SessionID   string    `json:"session_id"`
	Seq         int       `json:"seq"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	Detail      string    `json:"detail"`
	ToolName    string    `json:"tool_name,omitempty"`
	ToolUseID   string    `json:"tool_use_id,omitempty"`
	IsError     bool      `json:"is_error,omitempty"`
	Blocks      []string  `json:"blocks,omitempty"`
	Count       int       `json:"count"`
	Timestamp   string    `json:"timestamp,omitempty"`
	ProjectPath *string   `json:"project_path"`
	GitOrigin   *string   `json:"git_origin"`
	CreatedAt   time.Time `json:"created_at"`
}

// collapsibleKinds are the activities a timeline folds when they repeat
// back to back (the same command run again, a file read three times).
var collapsibleKinds = map[string]bool{"bash": true, "commit": true, "edit": true, "read": true, "task": true, "tool": true}

// Activities returns the timeline of the scope, newest first: the lines the
// activity-kind enricher labelled, optionally of some kinds and after
// args.Since, with consecutive identical tool activities collapsed into one
// entry carrying their count. The limit bounds the lines read, before they
// are collapsed.
func (s *Service) Activities(ctx context.Context, args ActivitiesArgs) ([]Activity, error) {
	since, err := parseSince(args.Since, time.Now())
	if err != nil {
		return nil, err
	}
	sessionIDs, scoped, err := s.scope(ctx, args.SessionID, args.GitOrigin, args.ProjectPath)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []Activity{}, nil
	}
	lines, err := s.Store.Activities(ctx, session.ActivityQuery{
		AccountID:  s.AccountID,
		SessionIDs: sessionIDs,
		Kinds:      args.Kinds,
		Since:      since,
		Limit:      withDefault(args.Limit, defaultActivityLimit),
	})
	if err != nil {
		return nil, err
	}
	sessions := map[string]session.Doc{}
	out := make([]Activity, 0, len(lines))
	for _, l := range lines {
		id, _ := l.String("session_id")
		if _, known := sessions[id]; !known {
			if sessions[id], err = s.sessionOrNil(ctx, id); err != nil {
				return nil, err
			}
		}
		a := activityOf(l)
		a.ProjectPath, a.GitOrigin = stringPtr(sessions[id], "project_path"), stringPtr(sessions[id], "git_origin")
		if n := len(out); n > 0 && sameActivity(out[n-1], a) {
			out[n-1].Count++
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// activityOf reads the stored activity of one projected line.
func activityOf(l session.Doc) Activity {
	e := l.Map("enrichments").Map("activity-kind")
	id, _ := l.String("session_id")
	kind, _ := e.String("kind")
	label, _ := e.String("label")
	detail, _ := e.String("detail")
	toolName, _ := e.String("tool_name")
	toolUseID, _ := e.String("tool_use_id")
	isErr, _ := e["is_error"].(bool)
	ts, _ := l.Line().String("timestamp")
	createdAt, _ := l["created_at"].(time.Time)
	return Activity{
		SessionID: id, Seq: toInt(l["seq"]), Kind: kind, Label: label, Detail: detail,
		ToolName: toolName, ToolUseID: toolUseID, IsError: isErr, Blocks: stringList(e["blocks"]),
		Count: 1, Timestamp: ts, CreatedAt: createdAt,
	}
}

// sameActivity reports whether b repeats a: the same tool activity in the
// same session.
func sameActivity(a, b Activity) bool {
	return collapsibleKinds[a.Kind] && a.SessionID == b.SessionID && a.Kind == b.Kind && a.Label == b.Label && a.Detail == b.Detail && len(a.Blocks) == 0 && len(b.Blocks) == 0
}

// stringList reads a stored list of strings whatever its decoded type.
func stringList(v any) []string {
	switch list := v.(type) {
	case []string:
		return list
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
