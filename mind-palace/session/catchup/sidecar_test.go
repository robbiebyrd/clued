package catchup

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func TestRunSetsTheSessionFieldsTheRecordsCarry(t *testing.T) {
	f := newFixture(t)
	st := newStore()
	if err := st.UpsertSession(context.Background(), session.Session{SessionID: f.sid, AccountID: account, LastSeen: time.Now()}); err != nil {
		t.Fatal(err)
	}
	write(t, f.transcript, `{"type":"user","entrypoint":"cli","version":"2.1.0","message":{"role":"user","content":"hi"}}`+"\n"+
		`{"type":"ai-title","title":"Draft"}`+"\n")
	f.mustRun(st)
	doc, _ := st.GetSession(context.Background(), account, f.sid)
	if doc["title"] != "Draft" || doc["entrypoint"] != "cli" || doc["claude_version"] != "2.1.0" {
		t.Fatalf("session = %v", doc)
	}
	if f.state().Meta["title"] != "Draft" {
		t.Errorf("state meta = %v", f.state().Meta)
	}
	appendTo(t, f.transcript, `{"type":"custom-title","title":"Mine"}`+"\n"+`{"type":"ai-title","title":"Generated"}`+"\n")
	f.mustRun(st)
	doc, _ = st.GetSession(context.Background(), account, f.sid)
	if doc["title"] != "Mine" || doc["title_kind"] != "custom" {
		t.Fatalf("after a custom title: %v", doc)
	}
	// A later run with only a generated title keeps the custom one.
	appendTo(t, f.transcript, `{"type":"ai-title","title":"Later"}`+"\n")
	f.mustRun(st)
	doc, _ = st.GetSession(context.Background(), account, f.sid)
	if doc["title"] != "Mine" {
		t.Errorf("custom title replaced: %v", doc["title"])
	}
}
