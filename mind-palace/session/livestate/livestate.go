// Package livestate derives a session's live state from the hook events
// Claude Code sends and keeps it on the session: working (the agent is
// mid-turn: a prompt was submitted, a tool is running or just ran), waiting
// (the agent yielded and the next move is the user's: it stopped, asked a
// question, needs a permission or is idle) or ended (the session ended).
// A change sets the session's live_state and records a SessionStateChanged
// hook event. Hotness, how recently a session was active, is a function of
// its last_seen time and is computed when asked, never stored.
package livestate

import (
	"context"
	"strings"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

// The live states.
const (
	Working = "working"
	Waiting = "waiting"
	Ended   = "ended"
)

// The hotness grades: last seen within the last 30 minutes, within the last
// hour, earlier the same UTC day, or before that.
const (
	Hot  = "hot"
	Warm = "warm"
	Cool = "cool"
	Cold = "cold"
)

// Hotness thresholds.
const (
	HotWithin  = 30 * time.Minute
	WarmWithin = time.Hour
)

// States lists the live states; Grades lists the hotness grades.
var (
	States = []string{Working, Waiting, Ended}
	Grades = []string{Hot, Warm, Cool, Cold}
)

// The tool whose call means the agent is waiting on the user, and the
// notification types that do.
const askUserTool = "AskUserQuestion"

var waitingNotifications = []string{"permission_prompt", "idle_prompt", "elicitation"}

// StateOf is the live state a hook event puts the session in and the
// reason recorded for it (the event's name, with the tool or notification
// type that qualified it); "" when the event says nothing about the state.
func StateOf(ev session.Doc) (state, reason string) {
	name, _ := ev.String("hook_event_name")
	switch name {
	case "UserPromptSubmit", "PostToolUse", "PostToolUseFailure", "SubagentStart":
		return Working, name
	case "PreToolUse":
		if tool, _ := ev.String("tool_name"); tool == askUserTool {
			return Waiting, name + ":" + tool
		}
		return Working, name
	case "Stop", "StopFailure", "PermissionRequest":
		return Waiting, name
	case "Notification":
		kind, _ := ev.String("notification_type")
		for _, waiting := range waitingNotifications {
			if strings.HasPrefix(kind, waiting) {
				return Waiting, name + ":" + kind
			}
		}
		return "", ""
	case "SessionEnd":
		return Ended, name
	}
	return "", ""
}

// Observe records that the session entered state at the time at because of
// reason: it sets the session's live state when it differs and then
// records a SessionStateChanged event. A session the store does not have
// is ErrNotFound and nothing is recorded. It reports whether the state
// changed.
func Observe(ctx context.Context, store session.Store, accountID, sessionID, state, reason string, at time.Time) (bool, error) {
	if state == "" || sessionID == "" {
		return false, nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	previous, changed, err := store.SetLiveState(ctx, accountID, sessionID, state, reason, at)
	if err != nil || !changed {
		return false, err
	}
	return true, store.InsertHookEvent(ctx, session.Doc{
		"hook_event_name": session.StateChangedEventName,
		"session_id":      sessionID,
		"account_id":      accountID,
		"from":            previous,
		"to":              state,
		"reason":          reason,
		"at":              at,
		"created_at":      time.Now(),
	})
}

// Hotness grades how recently a session was active, from its last_seen
// time: hot within the last 30 minutes, warm within the last hour, cool
// earlier the same UTC day, cold before that; "" for a zero time.
func Hotness(lastSeen, now time.Time) string {
	if lastSeen.IsZero() {
		return ""
	}
	switch age := now.Sub(lastSeen); {
	case age <= HotWithin:
		return Hot
	case age <= WarmWithin:
		return Warm
	case !lastSeen.Before(dayStart(now)):
		return Cool
	}
	return Cold
}

// Window is the last_seen range (inclusive from, exclusive before; a zero
// time is no bound) of the sessions of a hotness grade at now, and whether
// the grade is known. An empty range (from not before before) means no
// session can be in the grade right now.
func Window(grade string, now time.Time) (from, before time.Time, ok bool) {
	switch grade {
	case Hot:
		return now.Add(-HotWithin), time.Time{}, true
	case Warm:
		return now.Add(-WarmWithin), now.Add(-HotWithin), true
	case Cool:
		return dayStart(now), now.Add(-WarmWithin), true
	case Cold:
		return time.Time{}, dayStart(now), true
	}
	return time.Time{}, time.Time{}, false
}

// dayStart is midnight UTC of now's day.
func dayStart(now time.Time) time.Time {
	y, m, d := now.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
