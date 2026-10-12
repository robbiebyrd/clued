package enrichers

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/session"
)

func messageStatsOf(t *testing.T, doc session.Doc) messageStatsResult {
	t.Helper()
	r, err := MessageStats.Enrich(context.Background(), doc, nil)
	if err != nil {
		t.Fatalf("Enrich: %v", err)
	}
	return r.(messageStatsResult)
}

func TestMessageStatsMetadata(t *testing.T) {
	if MessageStats.Name != "message-stats" || MessageStats.Collection != "transcript_lines" {
		t.Fatalf("name/collection = %q/%q", MessageStats.Name, MessageStats.Collection)
	}
	if !MessageStats.Enabled || MessageStats.BatchLimit != 0 {
		t.Fatalf("enabled/batch = %v/%d", MessageStats.Enabled, MessageStats.BatchLimit)
	}
}

func TestMessageStatsMatchesAllDocs(t *testing.T) {
	if !MessageStats.Matches(session.Doc{}) {
		t.Fatal("expected match")
	}
}

func TestMessageStatsZerosForLineWithNoTextContent(t *testing.T) {
	if r := messageStatsOf(t, session.Doc{"line": map[string]any{}}); r != (messageStatsResult{}) {
		t.Fatalf("got %+v", r)
	}
	if r := messageStatsOf(t, session.Doc{}); r != (messageStatsResult{}) {
		t.Fatalf("no line: got %+v", r)
	}
}

func TestMessageStatsCountsUserPromptDisplay(t *testing.T) {
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"display": "hello world", "sessionId": "x"}})
	want := messageStatsResult{CharCount: 11, WordCount: 2, BlockCount: 0, TokenEstimate: 3}
	if r != want {
		t.Fatalf("got %+v want %+v", r, want)
	}
}

func TestMessageStatsCountsTextBlocksConcatenated(t *testing.T) {
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "assistant",
		"content": []any{
			map[string]any{"type": "text", "text": "hello "},
			map[string]any{"type": "tool_use", "id": "x", "name": "Bash", "input": map[string]any{}},
			map[string]any{"type": "text", "text": "world"},
		},
	}}})
	if r.CharCount != 11 || r.BlockCount != 3 {
		t.Fatalf("got %+v", r)
	}
}

func TestMessageStatsCountsThinkingBlocks(t *testing.T) {
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"message": map[string]any{
		"content": []any{map[string]any{"type": "thinking", "thinking": "abc"}, map[string]any{"type": "text", "text": "de"}},
	}}})
	if r.CharCount != 5 {
		t.Fatalf("got %+v", r)
	}
}

func TestMessageStatsTokenEstimateIsCeilOfCharsOverFour(t *testing.T) {
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"display": "hi", "sessionId": "x"}})
	if r.TokenEstimate != 1 {
		t.Fatalf("got %+v", r)
	}
}

func TestMessageStatsWordCountSplitsOnWhitespace(t *testing.T) {
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"message": map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "text", "text": "  one   two\t\nthree  "}},
	}}})
	if r.WordCount != 3 {
		t.Fatalf("got %+v", r)
	}
}

func TestMessageStatsCharCountIsUTF16Units(t *testing.T) {
	// JavaScript counts UTF-16 code units: an emoji is 2, "é" is 1.
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"display": "é😀", "sessionId": "x"}})
	if r.CharCount != 3 {
		t.Fatalf("got %+v", r)
	}
}

func TestMessageStatsDisplayWithoutSessionIDFallsBackToMessage(t *testing.T) {
	r := messageStatsOf(t, session.Doc{"line": map[string]any{"display": "hello"}})
	if r != (messageStatsResult{}) {
		t.Fatalf("got %+v", r)
	}
}
