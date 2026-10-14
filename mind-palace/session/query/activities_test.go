package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

// seqOf reads a stored seq whatever its decoded type.
func seqOf(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return -1
}

// seedActivity stores one transcript line labelled with the activity.
func seedActivity(t *testing.T, st session.Store, acc, id string, seq int, activity session.Doc) {
	t.Helper()
	ctx := context.Background()
	line := session.TranscriptLine{SessionID: id, Seq: seq, AccountID: acc, Line: map[string]any{"type": "assistant", "timestamp": "2026-10-14T10:00:00Z"}}
	if err := st.UpsertTranscriptLines(ctx, []session.TranscriptLine{line}); err != nil {
		t.Fatal(err)
	}
	docs, err := st.Unenriched(ctx, "transcript_lines", "activity-kind", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if d["session_id"] == id && seqOf(d["seq"]) == seq {
			if err := st.SetEnrichment(ctx, "transcript_lines", d["_id"], "activity-kind", activity); err != nil {
				t.Fatal(err)
			}
		}
	}
	time.Sleep(2 * time.Millisecond)
}

func TestActivitiesCollapsesRepeatedToolCallsAndScopes(t *testing.T) {
	st := memsession.New()
	now := time.Now()
	seedSession(t, st, "acc", "s1", "/p/one", "git@github.com:o/one.git", now)
	seedSession(t, st, "acc", "s2", "/p/two", "git@github.com:o/two.git", now)
	seedSession(t, st, "other", "s3", "/p/three", "", now)
	seedActivity(t, st, "acc", "s1", 0, session.Doc{"kind": "user", "label": "user", "detail": "fix it"})
	seedActivity(t, st, "acc", "s1", 1, session.Doc{"kind": "read", "label": "Read", "detail": "a.go", "tool_name": "Read", "tool_use_id": "t1"})
	seedActivity(t, st, "acc", "s1", 2, session.Doc{"kind": "read", "label": "Read", "detail": "a.go", "tool_name": "Read", "tool_use_id": "t2"})
	seedActivity(t, st, "acc", "s1", 3, session.Doc{"kind": "read", "label": "Read", "detail": "a.go", "tool_name": "Read", "tool_use_id": "t3"})
	seedActivity(t, st, "acc", "s1", 4, session.Doc{"kind": "response", "label": "response", "detail": "done"})
	seedActivity(t, st, "acc", "s1", 5, session.Doc{"kind": "response", "label": "response", "detail": "done"})
	seedActivity(t, st, "acc", "s2", 0, session.Doc{"kind": "read", "label": "Read", "detail": "a.go", "tool_name": "Read", "tool_use_id": "t9", "blocks": []any{"response"}})
	seedActivity(t, st, "other", "s3", 0, session.Doc{"kind": "user", "label": "user", "detail": "secret"})
	svc := &query.Service{Store: st, AccountID: "acc"}

	got, err := svc.Activities(context.Background(), query.ActivitiesArgs{})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		sid, kind, detail string
		seq, count        int
	}
	rows := func(got []query.Activity) []row {
		out := []row{}
		for _, a := range got {
			out = append(out, row{a.SessionID, a.Kind, a.Detail, a.Seq, a.Count})
		}
		return out
	}
	want := []row{{"s2", "read", "a.go", 0, 1}, {"s1", "response", "done", 5, 1}, {"s1", "response", "done", 4, 1}, {"s1", "read", "a.go", 3, 3}, {"s1", "user", "fix it", 0, 1}}
	if r := rows(got); len(r) != len(want) {
		t.Fatalf("rows = %+v, want %+v", r, want)
	} else {
		for i := range want {
			if r[i] != want[i] {
				t.Errorf("row %d = %+v, want %+v", i, r[i], want[i])
			}
		}
	}
	if got[0].Blocks == nil || got[0].Blocks[0] != "response" || *got[0].ProjectPath != "/p/two" || *got[0].GitOrigin != "git@github.com:o/two.git" || got[0].Timestamp != "2026-10-14T10:00:00Z" || got[0].ToolUseID != "t9" {
		t.Errorf("first entry = %+v", got[0])
	}
	if got[3].ToolUseID != "t3" || got[3].ToolName != "Read" {
		t.Errorf("collapsed entry keeps the newest line: %+v", got[3])
	}

	scoped, err := svc.Activities(context.Background(), query.ActivitiesArgs{SessionID: "s1", Kinds: []string{"user", "response"}})
	if err != nil || len(scoped) != 3 || scoped[0].Seq != 5 || scoped[2].Kind != "user" {
		t.Errorf("scoped = %+v (%v)", rows(scoped), err)
	}
	byOrigin, err := svc.Activities(context.Background(), query.ActivitiesArgs{GitOrigin: "two"})
	if err != nil || len(byOrigin) != 1 || byOrigin[0].SessionID != "s2" {
		t.Errorf("by origin = %+v (%v)", rows(byOrigin), err)
	}
	none, err := svc.Activities(context.Background(), query.ActivitiesArgs{ProjectPath: "/nowhere"})
	if err != nil || len(none) != 0 || none == nil {
		t.Errorf("no sessions in scope = %v (%v)", none, err)
	}
	if _, err := svc.Activities(context.Background(), query.ActivitiesArgs{Since: "yesterday"}); err == nil {
		t.Error("an unreadable since must be an error")
	}
	recent, err := svc.Activities(context.Background(), query.ActivitiesArgs{Since: "1h", Limit: 2})
	if err != nil || len(recent) != 2 {
		t.Errorf("since and limit = %+v (%v)", rows(recent), err)
	}
}
