package query

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// ForgetArgs are the forget_session tool parameters: sessions by id, by a
// regular expression over the project path, or last seen before a day
// (YYYY-MM-DD, UTC) or time (RFC 3339); given together they must all hold.
// Limit, above zero with a project path or a day, deletes at most that many
// sessions, the oldest first, and the result's more reports that others
// match.
type ForgetArgs struct {
	SessionIDs  []string `json:"session_ids,omitempty"`
	SessionID   string   `json:"session_id,omitempty"`
	ProjectPath string   `json:"project_path,omitempty"`
	Before      string   `json:"before,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

// ErrNothingToForget is returned for a forget with no criterion, so that a
// mistake never asks for every session.
var ErrNothingToForget = errors.New("name a session, a project path or a day to forget sessions before")

// Forget deletes the account's sessions the arguments select, with
// everything captured for them, from the store (the local JSONL files are
// Claude Code's and stay).
func (s *Service) Forget(ctx context.Context, args ForgetArgs) (session.Forgotten, error) {
	q := session.ForgetQuery{AccountID: s.AccountID, ProjectPathRe: args.ProjectPath, Limit: args.Limit}
	q.SessionIDs = append(q.SessionIDs, args.SessionIDs...)
	if args.SessionID != "" {
		q.SessionIDs = append(q.SessionIDs, args.SessionID)
	}
	before, err := args.BeforeTime()
	if err != nil {
		return session.Forgotten{}, err
	}
	q.Before = before
	if q.Empty() {
		return session.Forgotten{}, ErrNothingToForget
	}
	return s.Store.DeleteSessions(ctx, q)
}

// BeforeTime is Before as a time: a day's start (UTC) or an RFC 3339 time;
// zero when Before is empty.
func (a ForgetArgs) BeforeTime() (time.Time, error) {
	if a.Before == "" {
		return time.Time{}, nil
	}
	return parseInstant(a.Before)
}

// parseInstant reads a day (its start, UTC) or an RFC 3339 time.
func parseInstant(v string) (time.Time, error) {
	if at, err := time.Parse(time.DateOnly, v); err == nil {
		return at, nil
	}
	if at, err := time.Parse(time.RFC3339Nano, v); err == nil {
		return at, nil
	}
	return time.Time{}, fmt.Errorf("%q is not a day (YYYY-MM-DD) or an RFC 3339 time", v)
}
