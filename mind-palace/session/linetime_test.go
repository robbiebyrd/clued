package session

import (
	"testing"
	"time"
)

func TestTranscriptSpan(t *testing.T) {
	lines := []TranscriptLine{
		{Line: map[string]any{"timestamp": "2026-10-14T09:42:10.500Z"}},
		{Line: map[string]any{"type": "summary"}},
		{Line: map[string]any{"timestamp": "not a time"}},
		{Line: "raw text"},
		{Line: map[string]any{"timestamp": "2026-10-14T09:00:00Z"}},
	}
	first, last := TranscriptSpan(lines)
	if want := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC); !first.Equal(want) {
		t.Errorf("first = %v, want %v", first, want)
	}
	if want := time.Date(2026, 10, 14, 9, 42, 10, 500e6, time.UTC); !last.Equal(want) {
		t.Errorf("last = %v, want %v", last, want)
	}
	if f, l := TranscriptSpan(lines[1:4]); !f.IsZero() || !l.IsZero() {
		t.Errorf("undated lines = %v, %v, want zero", f, l)
	}
}
