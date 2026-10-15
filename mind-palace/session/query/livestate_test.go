package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

func TestFindSessionsReportsAndFiltersHotnessAndLiveState(t *testing.T) {
	st := memsession.New()
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	seedSession(t, st, account, "hot", "/p/hot", "", now.Add(-10*time.Minute))
	seedSession(t, st, account, "warm", "/p/warm", "", now.Add(-50*time.Minute))
	seedSession(t, st, account, "cool", "/p/cool", "", now.Add(-5*time.Hour))
	seedSession(t, st, account, "cold", "/p/cold", "", now.Add(-26*time.Hour))
	ctx := context.Background()
	for id, state := range map[string]string{"hot": "working", "warm": "waiting", "cold": "ended"} {
		if _, _, err := st.SetLiveState(ctx, account, id, state, "x", now); err != nil {
			t.Fatal(err)
		}
	}
	svc := &query.Service{Store: st, AccountID: account, Now: func() time.Time { return now }}
	ids := func(args query.FindSessionsArgs) []string {
		t.Helper()
		docs, err := svc.FindSessions(ctx, args)
		if err != nil {
			t.Fatalf("%+v: %v", args, err)
		}
		out := []string{}
		for _, d := range docs {
			id, _ := d.String("session_id")
			if d["hotness"] != id {
				t.Errorf("%s: hotness = %v", id, d["hotness"])
			}
			out = append(out, id)
		}
		return out
	}
	eq := func(what string, got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", what, got, want)
			return
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: %v, want %v", what, got, want)
				return
			}
		}
	}
	eq("all", ids(query.FindSessionsArgs{}), []string{"hot", "warm", "cool", "cold"})
	for _, grade := range []string{"hot", "warm", "cool", "cold"} {
		eq(grade, ids(query.FindSessionsArgs{Hotness: grade}), []string{grade})
	}
	eq("live state", ids(query.FindSessionsArgs{LiveState: "waiting"}), []string{"warm"})
	eq("both", ids(query.FindSessionsArgs{LiveState: "working", Hotness: "warm"}), []string{})
	for _, bad := range []query.FindSessionsArgs{{LiveState: "asleep"}, {Hotness: "tepid"}} {
		if _, err := svc.FindSessions(ctx, bad); err == nil {
			t.Errorf("%+v: want an error", bad)
		}
	}
	// Just after midnight nothing can be cool.
	svc.Now = func() time.Time { return time.Date(2026, 10, 14, 0, 10, 0, 0, time.UTC) }
	if docs, err := svc.FindSessions(ctx, query.FindSessionsArgs{Hotness: "cool"}); err != nil || len(docs) != 0 {
		t.Errorf("cool at 00:10 = %v (%v)", docs, err)
	}

	svc.Now = func() time.Time { return now }
	c, err := svc.SessionContext(ctx, "warm")
	if err != nil {
		t.Fatal(err)
	}
	if c.Session["live_state"] != "waiting" || c.Session["live_state_reason"] != "x" || c.Session["live_state_at"] == nil || c.Session["hotness"] != "warm" {
		t.Errorf("context session = %v", c.Session)
	}
}

func TestActivitiesIncludeStateChanges(t *testing.T) {
	st := memsession.New()
	ctx := context.Background()
	now := time.Now()
	seedSession(t, st, "acc", "s1", "/p/one", "", now)
	seedActivity(t, st, "acc", "s1", 0, session.Doc{"kind": "user", "label": "user", "detail": "go"})
	for _, ev := range []session.Doc{
		{"session_id": "s1", "account_id": "acc", "hook_event_name": session.StateChangedEventName, "from": "", "to": "working", "reason": "UserPromptSubmit", "at": now, "created_at": now.Add(time.Second)},
		{"session_id": "s1", "account_id": "acc", "hook_event_name": session.StateChangedEventName, "from": "working", "to": "waiting", "reason": "Stop", "at": now.Add(2 * time.Second), "created_at": now.Add(2 * time.Second)},
	} {
		if err := st.InsertHookEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	svc := &query.Service{Store: st, AccountID: "acc"}
	got, err := svc.Activities(ctx, query.ActivitiesArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Kind != "state" || got[0].Label != "waiting" || got[0].Detail != "Stop" || got[0].Seq != -1 || got[0].Timestamp == "" || *got[0].ProjectPath != "/p/one" {
		t.Fatalf("timeline = %+v", got)
	}
	if got[1].Kind != "state" || got[1].Label != "working" || got[2].Kind != "user" {
		t.Errorf("order = %s %s %s", got[0].Kind, got[1].Kind, got[2].Kind)
	}
	only, err := svc.Activities(ctx, query.ActivitiesArgs{Kinds: []string{"state"}})
	if err != nil || len(only) != 2 || only[0].Kind != "state" || only[1].Kind != "state" {
		t.Errorf("states only = %+v (%v)", only, err)
	}
	lines, err := svc.Activities(ctx, query.ActivitiesArgs{Kinds: []string{"user"}})
	if err != nil || len(lines) != 1 || lines[0].Kind != "user" {
		t.Errorf("lines only = %+v (%v)", lines, err)
	}
}
