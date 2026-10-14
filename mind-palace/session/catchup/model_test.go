package catchup

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func modelChanges(t *testing.T, st session.Store, sid string) []session.Doc {
	t.Helper()
	full, err := st.FullSession(context.Background(), account, sid)
	if err != nil {
		t.Fatal(err)
	}
	var out []session.Doc
	for _, ev := range full["hook_events"].([]session.Doc) {
		if ev["hook_event_name"] == session.ModelChangedEventName {
			out = append(out, ev)
		}
	}
	return out
}

func TestRunRecordsTheModelOfThePushedLines(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	if err := st.UpsertSession(context.Background(), session.Session{SessionID: f.sid, AccountID: account, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	write(t, f.transcript, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","model":"claude-a"}}`+"\n"+
		`{"type":"assistant","message":{"role":"assistant","model":"claude-b"}}`+"\n")
	f.mustRun(st)
	doc, err := st.GetSession(context.Background(), account, f.sid)
	if err != nil || doc["model"] != "claude-b" {
		t.Fatalf("session model = %v (%v)", doc["model"], err)
	}
	if got := f.state().Model; got != "claude-b" {
		t.Errorf("state model = %q", got)
	}
	if changes := modelChanges(t, st, f.sid); len(changes) != 2 || changes[1]["seq"] != 2 {
		t.Fatalf("changes = %v", changes)
	}

	// Later lines on the same model add nothing; a switch is recorded once.
	appendTo(t, f.transcript, `{"type":"assistant","message":{"role":"assistant","model":"claude-b"}}`+"\n")
	f.mustRun(st)
	if changes := modelChanges(t, st, f.sid); len(changes) != 2 {
		t.Fatalf("changes after a repeat = %v", changes)
	}
	appendTo(t, f.transcript, `{"type":"assistant","message":{"role":"assistant","model":"claude-a"}}`+"\n")
	f.mustRun(st)
	if changes := modelChanges(t, st, f.sid); len(changes) != 3 || changes[2]["from"] != "claude-b" || changes[2]["to"] != "claude-a" {
		t.Fatalf("changes after a switch = %v", changes)
	}
}
