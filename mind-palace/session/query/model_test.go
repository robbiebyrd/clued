package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

func TestSessionContextAndFindSessionsReportTheModel(t *testing.T) {
	st := memsession.New()
	now := time.Now()
	seedSession(t, st, account, "s1", "/p/one", "", now)
	if _, _, err := st.SetModel(context.Background(), account, "s1", "claude-fable-5-1", now); err != nil {
		t.Fatal(err)
	}
	svc := &query.Service{Store: st, AccountID: account}
	c, err := svc.SessionContext(context.Background(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Session["model"] != "claude-fable-5-1" || c.Session["model_changed_at"] == nil {
		t.Errorf("session = %v", c.Session)
	}
	found, err := svc.FindSessions(context.Background(), query.FindSessionsArgs{})
	if err != nil || len(found) != 1 || found[0]["model"] != "claude-fable-5-1" {
		t.Errorf("found = %v (%v)", found, err)
	}
}
