package enrichers

import (
	"context"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func assistantLine(message map[string]any, extra map[string]any) session.Doc {
	line := map[string]any{"type": "assistant", "message": message}
	for k, v := range extra {
		line[k] = v
	}
	return session.Doc{"seq": 7, "line": line}
}

func TestUsageMatchesAssistantLinesWithUsage(t *testing.T) {
	usage := map[string]any{"input_tokens": 1.0}
	cases := []struct {
		name string
		doc  session.Doc
		want bool
	}{
		{"assistant with usage", assistantLine(map[string]any{"model": "claude-sonnet-4-5", "usage": usage}, nil), true},
		{"assistant without usage", assistantLine(map[string]any{"model": "claude-sonnet-4-5"}, nil), false},
		{"synthetic", assistantLine(map[string]any{"model": "<synthetic>", "usage": usage}, nil), false},
		{"user", session.Doc{"line": map[string]any{"type": "user", "message": map[string]any{"usage": usage}}}, false},
		{"empty", session.Doc{}, false},
	}
	for _, c := range cases {
		if got := Usage.Matches(c.doc); got != c.want {
			t.Errorf("%s: Matches = %v, want %v", c.name, got, c.want)
		}
	}
	if Usage.Name != "usage" || Usage.Collection != "transcript_lines" || !Usage.Enabled {
		t.Errorf("metadata = %q/%q/%v", Usage.Name, Usage.Collection, Usage.Enabled)
	}
}

func TestUsageRecordsTheCountersModelMessageAndDay(t *testing.T) {
	doc := assistantLine(map[string]any{
		"id": "msg_1", "model": "claude-opus-4-6",
		"usage": map[string]any{
			"input_tokens": 3.0, "cache_creation_input_tokens": int32(500), "cache_read_input_tokens": int64(10000), "output_tokens": 42,
			"cache_creation": map[string]any{"ephemeral_5m_input_tokens": 200.0, "ephemeral_1h_input_tokens": 300.0},
		},
	}, map[string]any{"timestamp": "2026-10-13T23:59:59.500-02:00"})
	r, err := Usage.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := UsageResult{Model: "claude-opus-4-6", MessageID: "msg_1", Day: "2026-10-14", InputTokens: 3, CacheCreationInputTokens: 500,
		CacheCreation1hTokens: 300, CacheReadInputTokens: 10000, OutputTokens: 42, ContextTokens: 10503}
	if r != want {
		t.Errorf("result = %+v, want %+v", r, want)
	}
}

func TestUsageFallsBackToTheSeqAndTheStoredDay(t *testing.T) {
	doc := assistantLine(map[string]any{"model": "claude-haiku-4-5", "usage": map[string]any{"output_tokens": -5.0, "input_tokens": "9"}}, nil)
	doc["created_at"] = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	r, err := Usage.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := r.(UsageResult)
	if got.MessageID != "seq:7" || got.Day != "2026-10-01" || got.OutputTokens != 0 || got.InputTokens != 0 {
		t.Errorf("result = %+v", got)
	}
}
