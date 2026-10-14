package enrichers

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
)

func modelLine(sid string, seq int, lineType, model string) session.TranscriptLine {
	line := map[string]any{"type": lineType}
	if model != "" {
		line["message"] = map[string]any{"role": lineType, "model": model}
	}
	return session.TranscriptLine{SessionID: sid, Seq: seq, AccountID: "acc", Line: line}
}

func storedLine(t *testing.T, st session.Store, sid string, seq int) session.Doc {
	t.Helper()
	docs, err := st.TranscriptLines(context.Background(), session.LineQuery{AccountID: "acc", SessionID: sid, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range docs {
		if n, _ := seqOf(d["seq"]); n == seq {
			d["session_id"] = sid
			return d
		}
	}
	t.Fatalf("no line %s/%d", sid, seq)
	return nil
}

func TestModelEnricherCarriesTheModelForward(t *testing.T) {
	requireTranscriptEnricher(t, Model, "model")
	requireBSONTagsMirrorJSON(t, ModelResult{})
	if HookModel.Name != "hook-model" || HookModel.Collection != "hook_events" || !HookModel.Enabled {
		t.Errorf("hook-model metadata = %q/%q/%v", HookModel.Name, HookModel.Collection, HookModel.Enabled)
	}
	ctx := context.Background()
	st := memsession.New()
	if err := st.UpsertTranscriptLines(ctx, []session.TranscriptLine{
		modelLine("s1", 0, "user", ""),
		modelLine("s1", 1, "assistant", "claude-a"),
		modelLine("s1", 2, "user", ""),
		modelLine("s1", 3, "assistant", session.SyntheticModel),
		modelLine("s1", 4, "assistant", "claude-b"),
		modelLine("s1", 5, "user", ""),
	}); err != nil {
		t.Fatal(err)
	}
	want := []ModelResult{
		{Source: "unknown"},
		{Model: "claude-a", Source: "line"},
		{Model: "claude-a", Source: "carried"},
		{Model: "claude-a", Source: "carried"},
		{Model: "claude-b", Source: "line"},
		{Model: "claude-b", Source: "carried"},
	}
	for seq, w := range want {
		got, err := Model.Enrich(ctx, storedLine(t, st, "s1", seq), st)
		if err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("seq %d: got %+v, want %+v", seq, got, w)
		}
	}
	// Without a lookup or a session the model is unknown.
	if got, _ := Model.Enrich(ctx, session.Doc{"line": map[string]any{"type": "user"}}, nil); got != (ModelResult{Source: "unknown"}) {
		t.Errorf("no lookup: %+v", got)
	}
	if got := marshalEnrichment(t, Model, session.Doc{}); got != `{"model":"","source":"unknown"}` {
		t.Errorf("stored shape = %s", got)
	}
}

func TestHookModelEnricher(t *testing.T) {
	ctx := context.Background()
	st := memsession.New()
	if err := st.UpsertTranscriptLines(ctx, []session.TranscriptLine{modelLine("s1", 0, "assistant", "claude-a")}); err != nil {
		t.Fatal(err)
	}
	firstAt := time.Now()
	time.Sleep(5 * time.Millisecond)
	if err := st.UpsertTranscriptLines(ctx, []session.TranscriptLine{modelLine("s1", 1, "assistant", "claude-b")}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		doc  session.Doc
		want ModelResult
	}{
		{"sample names it", session.Doc{"session_id": "s1", "hook_event_name": "LimitsSampled", "model": map[string]any{"id": "claude-z"}}, ModelResult{Model: "claude-z", Source: "event"}},
		{"string model", session.Doc{"session_id": "s1", "model": "claude-y"}, ModelResult{Model: "claude-y", Source: "event"}},
		{"event between the lines", session.Doc{"session_id": "s1", "hook_event_name": "PreToolUse", "created_at": firstAt}, ModelResult{Model: "claude-a", Source: "carried"}},
		{"event after both", session.Doc{"session_id": "s1", "hook_event_name": "PreToolUse", "created_at": time.Now()}, ModelResult{Model: "claude-b", Source: "carried"}},
		{"event before any line falls back to the session's latest", session.Doc{"session_id": "s1", "hook_event_name": "SessionStart", "created_at": firstAt.Add(-time.Hour)}, ModelResult{Model: "claude-b", Source: "session"}},
		{"no created_at", session.Doc{"session_id": "s1", "hook_event_name": "Stop"}, ModelResult{Model: "claude-b", Source: "session"}},
		{"unknown session", session.Doc{"session_id": "s9", "hook_event_name": "Stop"}, ModelResult{Source: "unknown"}},
		{"no session", session.Doc{"hook_event_name": "Stop"}, ModelResult{Source: "unknown"}},
	}
	for _, c := range cases {
		got, err := HookModel.Enrich(ctx, c.doc, st)
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
