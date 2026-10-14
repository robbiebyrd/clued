package ingest

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func TestLimitsSampleRecordsTheSessionModel(t *testing.T) {
	f := newFixture(t)
	f.handle(t, session.Doc{"session_id": "s1", "hook_event_name": "SessionStart", "cwd": "/tmp"})
	f.handle(t, session.Doc{"session_id": "s1", "hook_event_name": session.LimitsEventName, "model": map[string]any{"id": "claude-fable-5-1", "display_name": "Fable 5.1"}})
	if got := sessionDoc(f.store, "s1")["model"]; got != "claude-fable-5-1" {
		t.Fatalf("session model = %v", got)
	}
	var changes []session.Doc
	for _, ev := range hookEvents(t, f.store) {
		if ev["hook_event_name"] == session.ModelChangedEventName {
			changes = append(changes, ev)
		}
	}
	if len(changes) != 1 || changes[0]["to"] != "claude-fable-5-1" || changes[0]["from"] != "" || changes[0]["source"] != "limits" || changes[0]["account_id"] != testAccount {
		t.Fatalf("changes = %v", changes)
	}
	// The same model again changes nothing; an event of an unknown session is
	// stored and ignored without a log line.
	f.handle(t, session.Doc{"session_id": "s1", "hook_event_name": session.LimitsEventName, "model": map[string]any{"id": "claude-fable-5-1"}})
	f.handle(t, session.Doc{"hook_event_name": session.LimitsEventName, "model": map[string]any{"id": "claude-x"}})
	n := 0
	for _, ev := range hookEvents(t, f.store) {
		if ev["hook_event_name"] == session.ModelChangedEventName {
			n++
		}
	}
	if n != 1 {
		t.Errorf("model changes = %d, want 1", n)
	}
}
