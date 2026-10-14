package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

func TestFindSessionsByTitleAndContextFieldsAndTurns(t *testing.T) {
	st := memsession.New()
	ctx := context.Background()
	now := time.Now()
	seedSession(t, st, account, "s1", "/p/one", "", now)
	seedSession(t, st, account, "s2", "/p/two", "", now.Add(-time.Minute))
	if err := st.SetSessionMeta(ctx, account, "s1", session.Doc{"title": "Fix the build", "title_kind": "ai", "pr_link": "https://x/pull/1", "permission_mode": "plan", "entrypoint": "cli"}); err != nil {
		t.Fatal(err)
	}
	svc := &query.Service{Store: st, AccountID: account}
	found, err := svc.FindSessions(ctx, query.FindSessionsArgs{Title: "build"})
	if err != nil || len(found) != 1 || found[0]["session_id"] != "s1" || found[0]["pr_link"] != "https://x/pull/1" || found[0]["entrypoint"] != "cli" {
		t.Errorf("find by title = %v (%v)", found, err)
	}
	if _, err := svc.FindSessions(ctx, query.FindSessionsArgs{Title: "("}); err == nil {
		t.Error("an invalid title regex must be an error")
	}

	// Two timed turns and a compaction in the timeline.
	for seq, a := range []session.Doc{
		{"kind": "turn", "label": "turn", "detail": "1s"},
		{"kind": "turn", "label": "turn", "detail": "3s"},
		{"kind": "compaction", "label": "compaction", "detail": "auto 1000 tokens"},
	} {
		seedActivity(t, st, account, "s1", seq, a)
	}
	pending, err := st.Unenriched(ctx, "transcript_lines", "system-record", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range pending {
		if d["session_id"] != "s1" {
			continue
		}
		ms := map[int]int64{0: 1000, 1: 3000}[seqOf(d["seq"])]
		if ms == 0 {
			continue
		}
		if err := st.SetEnrichment(ctx, "transcript_lines", d["_id"], "system-record", session.Doc{"subtype": "turn_duration", "duration_ms": ms}); err != nil {
			t.Fatal(err)
		}
	}
	c, err := svc.SessionContext(ctx, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if c.Session["title"] != "Fix the build" || c.Session["permission_mode"] != "plan" || c.Session["pr_link"] != "https://x/pull/1" {
		t.Errorf("context session = %v", c.Session)
	}
	if c.Turns == nil || c.Turns.Turns != 2 || c.Turns.TotalMs != 4000 || c.Turns.MeanMs != 2000 || c.Turns.LongestMs != 3000 {
		t.Errorf("turns = %+v", c.Turns)
	}
	other, err := svc.SessionContext(ctx, "s2")
	if err != nil || other.Turns != nil {
		t.Errorf("no turns = %+v (%v)", other.Turns, err)
	}
	acts, err := svc.Activities(ctx, query.ActivitiesArgs{Kinds: []string{"compaction"}})
	if err != nil || len(acts) != 1 || acts[0].Detail != "auto 1000 tokens" {
		t.Errorf("compaction entries = %+v (%v)", acts, err)
	}
}
