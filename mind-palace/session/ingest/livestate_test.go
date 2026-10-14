package ingest

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func stateChanges(t *testing.T, s session.Store) []string {
	t.Helper()
	var out []string
	for _, ev := range hookEvents(t, s) {
		if ev["hook_event_name"] == session.StateChangedEventName {
			from, _ := ev.String("from")
			to, _ := ev.String("to")
			reason, _ := ev.String("reason")
			out = append(out, from+">"+to+":"+reason)
		}
	}
	return out
}

func TestHookSequenceDrivesTheLiveState(t *testing.T) {
	f := newFixture(t)
	steps := []struct {
		ev   session.Doc
		want string
	}{
		{session.Doc{"session_id": "s1", "hook_event_name": "SessionStart", "cwd": "/tmp"}, ""},
		{session.Doc{"session_id": "s1", "hook_event_name": "UserPromptSubmit"}, "working"},
		{session.Doc{"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": "Bash"}, "working"},
		{session.Doc{"session_id": "s1", "hook_event_name": "PostToolUse", "tool_name": "Bash"}, "working"},
		{session.Doc{"session_id": "s1", "hook_event_name": "Stop"}, "waiting"},
		{session.Doc{"session_id": "s1", "hook_event_name": "UserPromptSubmit"}, "working"},
		{session.Doc{"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": "AskUserQuestion"}, "waiting"},
		{session.Doc{"session_id": "s1", "hook_event_name": "SessionEnd"}, "ended"},
	}
	for i, s := range steps {
		f.handle(t, s.ev)
		got, _ := sessionDoc(f.store, "s1").String("live_state")
		if got != s.want {
			t.Errorf("after step %d (%s): live_state = %q, want %q", i, s.ev["hook_event_name"], got, s.want)
		}
	}
	doc := sessionDoc(f.store, "s1")
	if doc["live_state_reason"] != "SessionEnd" || doc["live_state_at"] == nil {
		t.Errorf("session = %v", doc)
	}
	want := []string{">working:UserPromptSubmit", "working>waiting:Stop", "waiting>working:UserPromptSubmit", "working>waiting:PreToolUse:AskUserQuestion", "waiting>ended:SessionEnd"}
	got := stateChanges(t, f.store)
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("change %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestLimitsSampleCarriesTheLiveState(t *testing.T) {
	f := newFixture(t)
	f.handle(t, session.Doc{"session_id": "s1", "hook_event_name": "UserPromptSubmit", "cwd": "/tmp"})
	f.handle(t, session.Doc{"session_id": "s1", "hook_event_name": session.LimitsEventName, "context_window": map[string]any{"used_percentage": 5}})
	// A fresh service (the relay) reads the state from the store.
	fresh, _ := newService(t, f.store, f.logs)
	if _, err := fresh.Handle(t.Context(), session.Doc{"session_id": "s1", "hook_event_name": session.LimitsEventName, "context_window": map[string]any{"used_percentage": 6}}); err != nil {
		t.Fatal(err)
	}
	f.handle(t, session.Doc{"hook_event_name": session.LimitsEventName})
	var states []any
	for _, ev := range hookEvents(t, f.store) {
		if ev["hook_event_name"] == session.LimitsEventName {
			states = append(states, ev["live_state"])
		}
	}
	if len(states) != 3 || states[0] != "working" || states[1] != "working" || states[2] != nil {
		t.Errorf("live_state on samples = %v", states)
	}
}
