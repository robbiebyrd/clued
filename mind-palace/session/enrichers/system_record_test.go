package enrichers

import (
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func systemLine(subtype string, extra map[string]any) session.Doc {
	line := map[string]any{"type": "system", "subtype": subtype}
	for k, v := range extra {
		line[k] = v
	}
	return session.Doc{"line": line}
}

func TestSystemRecordEnricher(t *testing.T) {
	requireTranscriptEnricher(t, SystemRecordEnricher, "system-record")
	requireBSONTagsMirrorJSON(t, SystemRecord{})
	if !SystemRecordEnricher.Matches(systemLine("turn_duration", nil)) || SystemRecordEnricher.Matches(session.Doc{"line": map[string]any{"type": "user"}}) || SystemRecordEnricher.Matches(session.Doc{}) {
		t.Error("only system lines match")
	}
	record := func(doc session.Doc) SystemRecord {
		t.Helper()
		got, err := SystemRecordEnricher.Enrich(t.Context(), doc, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got.(SystemRecord)
	}
	if r := record(systemLine("turn_duration", map[string]any{"durationMs": 12345.0})); r.DurationMs != 12345 || r.Subtype != "turn_duration" {
		t.Errorf("turn_duration = %+v", r)
	}
	if r := record(systemLine("api_error", map[string]any{"error": map[string]any{"status": 529.0, "message": "Overloaded"}, "retryAttempt": 2.0, "content": "API error"})); r.Status != 529 || r.Message != "Overloaded" || r.Retry != 2 || r.Text != "API error" {
		t.Errorf("api_error = %+v", r)
	}
	if r := record(systemLine("api_error", map[string]any{"status": 500.0, "message": "boom"})); r.Status != 500 || r.Message != "boom" {
		t.Errorf("flat api_error = %+v", r)
	}
	if r := record(systemLine("compact_boundary", map[string]any{"content": "Conversation compacted", "compactMetadata": map[string]any{"trigger": "auto", "preTokens": 791988.0, "postTokens": 15929.0}})); r.Trigger != "auto" || r.PreTokens != 791988 || r.PostTokens != 15929 || r.Text != "Conversation compacted" {
		t.Errorf("compact_boundary = %+v", r)
	}
	if r := record(systemLine("stop_hook_summary", map[string]any{"hookCount": 1.0, "hookErrors": []any{"x"}})); r.HookCount != 1 || !r.HookError {
		t.Errorf("stop_hook_summary = %+v", r)
	}
	if r := record(systemLine("away_summary", map[string]any{"content": "You were away: tests passed"})); r.Text != "You were away: tests passed" {
		t.Errorf("away_summary = %+v", r)
	}
	if got := marshalEnrichment(t, SystemRecordEnricher, systemLine("other", nil)); got != `{"subtype":"other"}` {
		t.Errorf("stored shape = %s", got)
	}
}
