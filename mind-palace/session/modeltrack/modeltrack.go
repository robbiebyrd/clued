// Package modeltrack keeps a session's model up to date from what the
// capture sees: the model each assistant transcript line names and the one
// a status-line sample (LimitsSampled) carries. A change sets the session's
// model and records a ModelChanged hook event, so a session's model
// switches are a queryable sequence. The daemon, the catch-up of the relay,
// the backfill and the hook ingest all go through it.
package modeltrack

import (
	"context"
	"errors"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The sources a ModelChanged event names.
const (
	SourceTranscript = "transcript"
	SourceLimits     = "limits"
)

// Observe records that model served the session at the time at, as learned
// from the transcript line seq (negative when the source is not a line).
// It sets the session's model when it differs and then records a
// ModelChanged event; a session the store does not have is ErrNotFound and
// nothing is recorded. It reports whether the model changed.
func Observe(ctx context.Context, store session.Store, accountID, sessionID, model string, seq int, at time.Time, source string) (bool, error) {
	if model == "" || sessionID == "" {
		return false, nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	previous, changed, err := store.SetModel(ctx, accountID, sessionID, model, at)
	if err != nil || !changed {
		return false, err
	}
	ev := session.Doc{
		"hook_event_name": session.ModelChangedEventName,
		"session_id":      sessionID,
		"account_id":      accountID,
		"from":            previous,
		"to":              model,
		"source":          source,
		"at":              at,
		"created_at":      time.Now(),
	}
	if seq >= 0 {
		ev["seq"] = seq
	}
	return true, store.InsertHookEvent(ctx, ev)
}

// Lines observes the models the transcript lines name, in order, calling
// Observe only when a line names a model other than last (the model last
// seen for the session, "" at the start). It returns the model last seen,
// for the caller to pass next time. A session the store does not have yet
// leaves last unchanged, so the next line tries again.
func Lines(ctx context.Context, store session.Store, accountID string, lines []session.TranscriptLine, last string) (string, error) {
	for _, l := range lines {
		model := session.LineModel(session.Doc{"line": l.Line})
		if model == "" || model == last {
			continue
		}
		at := LineTime(session.Doc{"line": l.Line})
		if _, err := Observe(ctx, store, accountID, l.SessionID, model, l.Seq, at, SourceTranscript); err != nil {
			if errors.Is(err, session.ErrNotFound) {
				continue
			}
			return last, err
		}
		last = model
	}
	return last, nil
}

// EventModel is the model a hook event names: model.id (a LimitsSampled
// sample) or model as a string; "" when it names none.
func EventModel(ev session.Doc) string {
	if id, ok := ev.Map("model").String("id"); ok {
		return id
	}
	model, _ := ev.String("model")
	return model
}

// LineTime is the line's own timestamp, or the zero time.
func LineTime(d session.Doc) time.Time {
	if s, ok := d.Line().String("timestamp"); ok {
		if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return at
		}
	}
	return time.Time{}
}
