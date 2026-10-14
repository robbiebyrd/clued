package modeltrack

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

var t0 = time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)

func assistant(sid string, seq int, model, ts string) session.TranscriptLine {
	return session.TranscriptLine{SessionID: sid, Seq: seq, AccountID: "acc", Line: map[string]any{
		"type": "assistant", "timestamp": ts, "message": map[string]any{"role": "assistant", "model": model},
	}}
}

func changes(t *testing.T, st session.Store, sid string) []session.Doc {
	t.Helper()
	full, err := st.FullSession(context.Background(), "acc", sid)
	if err != nil {
		t.Fatal(err)
	}
	var out []session.Doc
	for _, ev := range toDocs(full["hook_events"]) {
		if ev["hook_event_name"] == session.ModelChangedEventName {
			out = append(out, ev)
		}
	}
	return out
}

func toDocs(v any) []session.Doc {
	var out []session.Doc
	switch list := v.(type) {
	case []session.Doc:
		return list
	case []any:
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				out = append(out, session.Doc(m))
			} else if d, ok := item.(session.Doc); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

func TestLinesRecordsEachChangeOnce(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s1", AccountID: "acc", LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	lines := []session.TranscriptLine{
		{SessionID: "s1", Seq: 0, AccountID: "acc", Line: map[string]any{"type": "user"}},
		assistant("s1", 1, "claude-a", "2026-10-14T10:00:01Z"),
		assistant("s1", 2, "claude-a", "2026-10-14T10:00:02Z"),
		assistant("s1", 3, session.SyntheticModel, "2026-10-14T10:00:03Z"),
		assistant("s1", 4, "claude-b", "bad time"),
	}
	last, err := Lines(ctx, st, "acc", lines, "")
	if err != nil || last != "claude-b" {
		t.Fatalf("last = %q, err %v", last, err)
	}
	doc, _ := st.GetSession(ctx, "acc", "s1")
	if doc["model"] != "claude-b" {
		t.Errorf("session model = %v", doc["model"])
	}
	got := changes(t, st, "s1")
	if len(got) != 2 {
		t.Fatalf("changes = %v", got)
	}
	first, second := got[0], got[1]
	if first["from"] != "" || first["to"] != "claude-a" || first["seq"] != 1 || first["source"] != SourceTranscript || !first["at"].(time.Time).Equal(t0.Add(time.Second)) {
		t.Errorf("first change = %v", first)
	}
	if second["from"] != "claude-a" || second["to"] != "claude-b" || second["seq"] != 4 || second["at"].(time.Time).IsZero() {
		t.Errorf("second change = %v", second)
	}

	// Lines already seen change nothing more.
	if last, err = Lines(ctx, st, "acc", lines[4:], last); err != nil || last != "claude-b" || len(changes(t, st, "s1")) != 2 {
		t.Errorf("repeat: last %q err %v changes %d", last, err, len(changes(t, st, "s1")))
	}
}

func TestLinesWaitsForTheSession(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	line := assistant("s2", 0, "claude-a", "")
	last, err := Lines(ctx, st, "acc", []session.TranscriptLine{line}, "")
	if err != nil || last != "" {
		t.Fatalf("missing session: last %q err %v", last, err)
	}
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s2", AccountID: "acc", LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	if last, err = Lines(ctx, st, "acc", []session.TranscriptLine{line}, last); err != nil || last != "claude-a" {
		t.Fatalf("after upsert: last %q err %v", last, err)
	}
	if len(changes(t, st, "s2")) != 1 {
		t.Error("the change should be recorded once the session exists")
	}
}

func TestObserveFromASampleAndEventModel(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	if err := st.UpsertSession(ctx, session.Session{SessionID: "s3", AccountID: "acc", LastSeen: t0}); err != nil {
		t.Fatal(err)
	}
	sample := session.Doc{"hook_event_name": session.LimitsEventName, "model": map[string]any{"id": "claude-fable-5-1", "display_name": "Fable 5.1"}}
	if got := EventModel(sample); got != "claude-fable-5-1" {
		t.Errorf("EventModel(sample) = %q", got)
	}
	if got := EventModel(session.Doc{"model": "claude-x"}); got != "claude-x" {
		t.Errorf("EventModel(string) = %q", got)
	}
	if got := EventModel(session.Doc{"hook_event_name": "PreToolUse"}); got != "" {
		t.Errorf("EventModel(none) = %q", got)
	}
	changed, err := Observe(ctx, st, "acc", "s3", EventModel(sample), -1, t0, SourceLimits)
	if err != nil || !changed {
		t.Fatalf("observe: changed %v err %v", changed, err)
	}
	got := changes(t, st, "s3")
	if len(got) != 1 || got[0]["source"] != SourceLimits || got[0]["to"] != "claude-fable-5-1" {
		t.Fatalf("changes = %v", got)
	}
	if _, has := got[0]["seq"]; has {
		t.Error("a sample names no line")
	}
	if changed, err = Observe(ctx, st, "acc", "s3", "claude-fable-5-1", -1, t0, SourceLimits); err != nil || changed {
		t.Errorf("same model again: changed %v err %v", changed, err)
	}
	if changed, err = Observe(ctx, st, "acc", "s3", "", -1, t0, SourceLimits); err != nil || changed {
		t.Errorf("empty model: changed %v err %v", changed, err)
	}
	if _, err = Observe(ctx, st, "acc", "nope", "claude-a", -1, t0, SourceLimits); !errors.Is(err, session.ErrNotFound) {
		t.Errorf("missing session: err %v", err)
	}
}
