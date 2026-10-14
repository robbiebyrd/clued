package query

import (
	"context"
	"slices"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

const defaultActivityLimit = 50

// ActivitiesArgs are the get_activities tool parameters. Kinds keeps only
// those activity kinds; Since is an RFC 3339 time or a duration back from
// now ("24h"); empty means no lower bound. IncludeRejected keeps the tool
// calls the harness refused to run, and their results.
type ActivitiesArgs struct {
	SessionID       string   `json:"session_id,omitempty"`
	GitOrigin       string   `json:"git_origin,omitempty"`
	ProjectPath     string   `json:"project_path,omitempty"`
	Kinds           []string `json:"kinds,omitempty"`
	Since           string   `json:"since,omitempty"`
	IncludeRejected bool     `json:"include_rejected,omitempty"`
	Limit           int      `json:"limit,omitempty"`
}

// ActivityState is the kind of the entries made from SessionStateChanged
// events: Label is the state entered (working, waiting, ended), Detail the
// hook event that set it, Seq is -1 and Timestamp the change's time.
const ActivityState = "state"

// Activity is one entry of the timeline: a transcript line's activity-kind
// enrichment, or a session state change, with where it happened. Count is
// how many consecutive lines of the same tool activity (same kind, label
// and detail in one session) the entry stands for; Seq is the newest of
// them (-1 for a state change, which is no line).
type Activity struct {
	SessionID string   `json:"session_id"`
	Seq       int      `json:"seq"`
	Kind      string   `json:"kind"`
	Label     string   `json:"label"`
	Detail    string   `json:"detail"`
	ToolName  string   `json:"tool_name,omitempty"`
	ToolUseID string   `json:"tool_use_id,omitempty"`
	IsError   bool     `json:"is_error,omitempty"`
	Blocks    []string `json:"blocks,omitempty"`
	// Result is the tool-result-detail enrichment of a result line (the
	// tool its shape revealed, the file, span and line counts of an edit,
	// the exit code and interruption of a command, the range of a read,
	// the sub-agent's description) without its diff, output and result
	// texts; the entry's Detail is then that enrichment's summary.
	Result      session.Doc `json:"result,omitempty"`
	Count       int         `json:"count"`
	Timestamp   string      `json:"timestamp,omitempty"`
	ProjectPath *string     `json:"project_path"`
	GitOrigin   *string     `json:"git_origin"`
	CreatedAt   time.Time   `json:"created_at"`
}

// collapsibleKinds are the activities a timeline folds when they repeat
// back to back (the same command run again, a file read three times).
var collapsibleKinds = map[string]bool{"bash": true, "commit": true, "edit": true, "read": true, "task": true, "tool": true}

// Activities returns the timeline of the scope, newest first: the lines the
// activity-kind enricher labelled and the session's state changes
// (SessionStateChanged events, kind "state"), optionally of some kinds and
// after args.Since, with consecutive identical tool activities collapsed
// into one entry carrying their count. The limit bounds the lines and the
// state changes read, each before they are merged and collapsed.
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
	q := session.ActivityQuery{
		AccountID:  s.AccountID,
		SessionIDs: sessionIDs,
		Since:      since,
		Limit:      withDefault(args.Limit, defaultActivityLimit),
	}
	wantLines, wantStates := len(args.Kinds) == 0, len(args.Kinds) == 0
	for _, k := range args.Kinds {
		if k == ActivityState {
			wantStates = true
		} else {
			wantLines = true
			q.Kinds = append(q.Kinds, k)
		}
	}
	var entries []Activity
	if wantLines {
		lines, err := s.Store.Activities(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, l := range lines {
			entries = append(entries, activityOf(l))
		}
	}
	if wantStates {
		changes, err := s.Store.StateChanges(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, c := range changes {
			entries = append(entries, stateActivityOf(c))
		}
		if wantLines {
			slices.SortStableFunc(entries, func(a, b Activity) int { return b.CreatedAt.Compare(a.CreatedAt) })
		}
	}
	rejected, err := s.rejectedToolUses(ctx, sessionIDs, args.IncludeRejected)
	if err != nil {
		return nil, err
	}
	sessions := map[string]session.Doc{}
	out := make([]Activity, 0, len(entries))
	for _, a := range entries {
		if rejected[a.ToolUseID] {
			continue
		}
		if _, known := sessions[a.SessionID]; !known {
			if sessions[a.SessionID], err = s.sessionOrNil(ctx, a.SessionID); err != nil {
				return nil, err
			}
		}
		a.ProjectPath, a.GitOrigin = stringPtr(sessions[a.SessionID], "project_path"), stringPtr(sessions[a.SessionID], "git_origin")
		if n := len(out); n > 0 && sameActivity(out[n-1], a) {
			out[n-1].Count++
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

// stateActivityOf reads a SessionStateChanged event as a timeline entry.
func stateActivityOf(c session.Doc) Activity {
	id, _ := c.String("session_id")
	to, _ := c.String("to")
	reason, _ := c.String("reason")
	createdAt, _ := c["created_at"].(time.Time)
	var ts string
	if at, ok := c["at"].(time.Time); ok {
		ts = at.UTC().Format(time.RFC3339Nano)
	}
	return Activity{SessionID: id, Seq: -1, Kind: ActivityState, Label: to, Detail: reason, Count: 1, Timestamp: ts, CreatedAt: createdAt}
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
	a := Activity{
		SessionID: id, Seq: toInt(l["seq"]), Kind: kind, Label: label, Detail: detail,
		ToolName: toolName, ToolUseID: toolUseID, IsError: isErr, Blocks: stringList(e["blocks"]),
		Count: 1, Timestamp: ts, CreatedAt: createdAt,
	}
	if result := l.Map("enrichments").Map("tool-result-detail"); result != nil {
		a.Result = session.Doc{}
		for k, v := range result {
			if !resultTexts[k] {
				a.Result[k] = v
			}
		}
		if summary, _ := result.String("summary"); summary != "" {
			a.Detail = summary
		}
	}
	return a
}

// resultTexts are the fields of a tool-result-detail a timeline entry
// leaves out: the bodies get_full_session returns with the line.
var resultTexts = map[string]bool{"diff": true, "stdout": true, "stderr": true, "result": true, "text": true, "hunks": true}

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
