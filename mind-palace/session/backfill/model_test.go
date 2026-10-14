package backfill_test

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func TestRunRecordsTheSessionModel(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, "-Users-test-myproject", sessionID,
		`{"type":"assistant","message":{"role":"assistant","model":"claude-a"}}`+"\n"+
			`{"type":"user","message":{"role":"user","content":"x"}}`+"\n"+
			`{"type":"assistant","message":{"role":"assistant","model":"claude-b"}}`+"\n")
	store := memsession.New()
	run(t, store, dir, "acc", nil)
	sess, err := store.GetSession(context.Background(), "acc", sessionID)
	if err != nil || sess["model"] != "claude-b" {
		t.Fatalf("session model = %v (%v)", sess["model"], err)
	}
	full, err := store.FullSession(context.Background(), "acc", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range full["hook_events"].([]session.Doc) {
		if ev["hook_event_name"] == session.ModelChangedEventName {
			n++
		}
	}
	if n != 2 {
		t.Errorf("model changes = %d, want 2", n)
	}
	// Running again changes nothing more.
	run(t, store, dir, "acc", nil)
	full, _ = store.FullSession(context.Background(), "acc", sessionID)
	if len(full["hook_events"].([]session.Doc)) != 2 {
		t.Errorf("a second backfill recorded more changes: %v", full["hook_events"])
	}
}
